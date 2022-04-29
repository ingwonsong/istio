// Copyright Istio Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package install

import (
	contextpkg "context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"gopkg.in/yaml.v3"

	"istio.io/istio/pkg/test/framework/util"
	"istio.io/istio/pkg/test/util/retry"
	"istio.io/istio/prow/asm/tester/pkg/exec"
	"istio.io/istio/prow/asm/tester/pkg/gcp"
	"istio.io/istio/prow/asm/tester/pkg/install/revision"
	"istio.io/istio/prow/asm/tester/pkg/kube"
	"istio.io/istio/prow/asm/tester/pkg/resource"
)

const (
	ImageAnnotationKey = "mesh.cloud.google.com/image"
)

// Since asmcli will be deprecated in the future, MLCP will be not be installed
// via asmcli anymore.
func (c *installer) installASMManagedLocalControlPlane(rev *revision.Config) error {
	contexts := c.settings.KubeContexts
	kubeconfigs := filepath.SplitList(c.settings.Kubeconfig)

	// Use staging environment for testing.
	if err := exec.Run("gcloud config set api_endpoint_overrides/gkehub https://staging-gkehub.sandbox.googleapis.com/"); err != nil {
		return fmt.Errorf("error setting gke hub endpoint to staging: %w", err)
	}

	if err := exec.Run(fmt.Sprintf("gcloud container hub mesh enable --project=%s", onPremFleetProject)); err != nil {
		return fmt.Errorf("error enabling hub mesh feature: %w", err)
	}

	for i, context := range contexts {
		if err := retry.UntilSuccess(func() error {
			return exec.Run(fmt.Sprintf("kubectl get crd/controlplanerevisions.mesh.cloud.google.com --context=%s", context))
		}, retry.Timeout(time.Second*600), retry.Delay(time.Second*10)); err != nil {
			return fmt.Errorf("error waiting for ControlPlaneRevision CRD: %w", err)
		}

		if err := exec.Run(fmt.Sprintf(`bash -c 'cat <<EOF | kubectl apply --context=%s -f -
apiVersion: mesh.cloud.google.com/v1alpha1
kind: ControlPlaneRevision
metadata:
  name: asm-managed-rapid
  namespace: istio-system
spec:
  type: managed_local
  channel: rapid
EOF'`, context)); err != nil {
			return fmt.Errorf("error creating Control Plane Revision CR")
		}

		if err := exec.Run(fmt.Sprintf("kubectl -n istio-system wait controlplanerevision asm-managed-rapid --for condition=reconciled --timeout=600s --context=%s", context)); err != nil {
			return fmt.Errorf("error waiting for ControlPlaneRevision CR: %w", err)
		}

		// Install Gateway
		if err := exec.Run("kubectl apply -f tools/packaging/knative/gateway -n istio-system --context=" + context); err != nil {
			return fmt.Errorf("error installing injected-gateway: %w", err)
		}

		if err := exec.Dispatch(c.settings.RepoRootDir, "onprem::configure_ingress_ip",
			[]string{kubeconfigs[i]},
			exec.WithAdditionalEnvs(
				[]string{"HERCULES_CLI_LAB=atl_shared"})); err != nil {
			return err
		}
	}

	return nil
}

func (c *installer) installAutomaticManagedControlPlane(rev *revision.Config) error {
	// Use staging environment for testing.
	if err := exec.Run("gcloud config set api_endpoint_overrides/gkehub https://staging-gkehub.sandbox.googleapis.com/"); err != nil {
		return fmt.Errorf("error setting gke hub endpoint to staging: %w", err)
	}

	// Use the first project as the fleet project.
	fleetProject := c.settings.GCPProjects[0]
	projectNumber, err := gcp.GetProjectNumber(fleetProject)
	if err != nil {
		return fmt.Errorf("failed to retrieve GCP project number for %s: %w", fleetProject, err)
	}
	if err := exec.Run(fmt.Sprintf("gcloud services enable mesh.googleapis.com --project=%s", fleetProject)); err != nil {
		return fmt.Errorf("error enabling mesh.googleapis.com service for project %s: %w", fleetProject, err)
	}
	if err := exec.Run(fmt.Sprintf("gcloud container hub mesh enable --project=%s", fleetProject)); err != nil {
		return fmt.Errorf("error enabling hub mesh feature for project %s: %w", fleetProject, err)
	}

	// Setup to be run per-cluster:
	//  (1) Label each cluster with mesh_id
	//  (2) Register the membership
	//  (3) Enable automatic CP management for the membership
	for _, context := range c.settings.KubeContexts {
		cluster := kube.GKEClusterSpecFromContext(context)
		log.Printf("Enabling automatic control plane management for cluster %s in project %s...",
			cluster.Name, cluster.ProjectID)

		if err := exec.Run(fmt.Sprintf("gcloud container clusters update %s --update-labels mesh_id=proj-%s --project %s --region %s",
			cluster.Name, projectNumber, cluster.ProjectID, cluster.Location)); err != nil {
		}
		if err := exec.Run(fmt.Sprintf(`gcloud container hub memberships register membership-%s \
		--gke-uri=%s --enable-workload-identity --project %s`,
			cluster.Name, gkeURI(cluster), fleetProject)); err != nil {
			return fmt.Errorf("failed registering cluster %s to fleet: %w", cluster.Name, err)
		}
		// TODO(samnaser) update to be --memberships once gcloud change lands.
		if err := exec.Run(fmt.Sprintf(`gcloud alpha container hub mesh update \
		--control-plane automatic --membership membership-%s --project %s`,
			cluster.Name, fleetProject)); err != nil {
			return fmt.Errorf("failed enabling automatic CP management for cluster %s: %w", cluster.Name, err)
		}
	}

	// Wait for the state to be active. Should be similar to:
	// membershipSpecs:
	// 	 projects/746296320118/locations/global/memberships/demo-cluster-1:
	//     mesh:
	//       controlPlane: AUTOMATIC
	// membershipStates:
	// 	 projects/746296320118/locations/global/memberships/demo-cluster-1:
	//     servicemesh:
	//       controlPlaneManagement:
	//         details:
	// 	         - code: REVISION_READY
	//             details: 'Ready: asm-managed'
	//     state: ACTIVE
	//       state:
	//         description: 'Revision(s) ready for use: asm-managed.'
	if err := retry.UntilSuccess(func() error {
		featureState, err := exec.RunWithOutput(fmt.Sprintf("gcloud alpha container hub mesh describe --project %s",
			fleetProject))
		if err != nil {
			return fmt.Errorf("failed to read feature state: %w", err)
		}
		log.Printf("Dumping feature state:\n%s", featureState)
		revisionsReady := strings.Count(featureState, "REVISION_READY")
		if revisionsReady != len(c.settings.KubeContexts) {
			return fmt.Errorf("want %d ready revisions, got %d",
				len(c.settings.KubeContexts), revisionsReady)
		}
		return nil
	}, retry.Timeout(time.Second*600), retry.Delay(time.Second*25)); err != nil {
		return fmt.Errorf("error waiting for revision readiness in feature state: %w", err)
	}

	// After auto-CP is ready, install ingress gateways.
	for _, context := range c.settings.KubeContexts {
		// Use default injection, since auto-CP revision name depends on cluster channel.
		if err := exec.Run("kubectl label namespace istio-system istio-injection=enabled istio.io/rev- --overwrite --context=" + context); err != nil {
			return fmt.Errorf("error labeling namespace: %w", err)
		}
		// Install Gateway
		if err := exec.Run("kubectl apply -f tools/packaging/knative/gateway -n istio-system --context=" + context); err != nil {
			return fmt.Errorf("error installing injected-gateway: %w", err)
		}
	}

	if err := createRemoteSecretsManaged(c.settings); err != nil {
		return fmt.Errorf("failed to enable managed multicluster: %w", err)
	}

	return nil
}

func (c *installer) installASMManagedControlPlaneAFC(rev *revision.Config) error {
	contexts := c.settings.KubeContexts

	log.Println("Downloading ASM script for the installation...")
	scriptPath, err := downloadInstallScript(c.settings, nil)
	if err != nil {
		return fmt.Errorf("failed to download the install script: %w", err)
	}

	// ASM MCP VPCSC with AFC test requires the latest, as of 10/13/2021, unreleased gcloud binary .
	// TODO(ruigu): Remove this part after the http://b/204468175.
	if c.settings.FeaturesToTest.Has(string(resource.VPCSC)) {
		if err := util.UpdateCloudSDKToPiperHead(); err != nil {
			return err
		}
	} else {
		// ASM MCP Prow job (except VPCSC) should use staging AFC since we should alert before
		// issues reach production.
		if err := exec.Run("gcloud config set api_endpoint_overrides/gkehub https://staging-gkehub.sandbox.googleapis.com/"); err != nil {
			return fmt.Errorf("error setting gke hub endpoint to staging: %w", err)
		}
	}

	// AFC uses staging GKE hub. Clean up staging GKE Hub membership from previous test runs.
	// TODO(ruigu): Remove this when we're able to delete staging hub memberships in boskos. b/202133285
	if err := exec.Run(`bash -c 'gcloud container hub memberships list --format="value(name)" | while read line ; do gcloud container hub memberships delete $line --location global --quiet ; done'`); err != nil {
		return fmt.Errorf("error clean up gke hub endpoint in staging: %w", err)
	}

	// Use the first project as the environ name
	// must do this here because each installation depends on the value
	projectID := c.settings.GCPProjects[0]
	environProjectNumber, err := gcp.GetProjectNumber(projectID)
	if err != nil {
		return fmt.Errorf("failed to read environ number: %w", err)
	}
	os.Setenv("_CI_ENVIRON_PROJECT_NUMBER", strings.TrimSpace(environProjectNumber))

	for _, context := range contexts {
		contextLogger := log.New(os.Stdout,
			fmt.Sprintf("[kubeContext: %s] ", context), log.Ldate|log.Ltime)
		contextLogger.Println("Performing ASM installation via AFC...")
		cluster := kube.GKEClusterSpecFromContext(context)

		contextLogger.Println("Running installation using install script...")
		// Running the test with offline mode because we need to test with custome images built on the fly.
		// However, the annotation in the ControlPlaneRevision is not user-facing. Therefore, we need to
		// do the patching here so we could test in CI while hiding the internal detail from the customers.
		outputDir, err := ASMOutputDir(rev)
		if err != nil {
			return fmt.Errorf("MCP create output dir failed: %w", err)
		}
		if err := exec.Run(scriptPath,
			exec.WithAdditionalEnvs(generateAFCInstallEnvvars(c.settings)),
			exec.WithAdditionalArgs(generateAFCBuildOfflineFlags(outputDir))); err != nil {
			return fmt.Errorf("MCP build offline pacakge failed: %w", err)
		}
		// VPC-SC only tests production so no need to patch CPRs.
		if !c.settings.FeaturesToTest.Has(string(resource.VPCSC)) {
			if err := filepath.Walk(filepath.Join(outputDir, "asm", "control-plane-revision"), patchCPRWithImageWalkFn); err != nil {
				return fmt.Errorf("MCP patch ControlPlaneRevision with custom image failed: %w", err)
			}
		}
		if err := exec.Run(scriptPath,
			exec.WithAdditionalEnvs(generateAFCInstallEnvvars(c.settings)),
			exec.WithAdditionalArgs(generateAFCInstallFlags(c.settings, cluster, outputDir))); err != nil {
			return fmt.Errorf("MCP installation via AFC failed: %w", err)
		}
		// Check if MCP is properly installed in VPCSC mode.
		// Calling the following API (fetchControlPlane) requires the consumer project to have GOOGLE_INTERNAL tenant manager label.
		if c.settings.FeaturesToTest.Has(string(resource.VPCSC)) {
			contextLogger.Println("Verifying MCP VPCSC installation...")
			ctx := contextpkg.Background()
			creds, err := google.FindDefaultCredentials(ctx, "https://www.googleapis.com/auth/cloud-platform")
			if err != nil {
				return fmt.Errorf("failed to find default credentials for MCP VPCSC installation verification: %w", err)
			}
			url := fmt.Sprintf("https://meshconfig.googleapis.com/v1alpha1/projects/%s/locations/%s/clusters/%s/controlPlanes/asm-managed-rapid:fetchControlPlane", cluster.ProjectID, cluster.Location, cluster.Name)
			resp, err := oauth2.NewClient(ctx, creds.TokenSource).Get(url)
			if err != nil {
				return fmt.Errorf("failed to create HTTP client for MCP VPCSC installation verification: %w", err)
			}
			defer resp.Body.Close()
			cp := struct {
				Name    string `json:"name"`
				State   string `json:"state"`
				VPCMode string `json:"vpcscMode"`
			}{}
			if err := json.NewDecoder(resp.Body).Decode(&cp); err != nil {
				return fmt.Errorf("failed to decode HTTP response for MCP VPCSC installation verification: %w", err)
			}
			const expectedVPCSCMode = "COMPATIBLE"
			if cp.VPCMode != expectedVPCSCMode {
				return fmt.Errorf("MCP VPCSC installation via AFC failed, got: %v, want: %v", cp.VPCMode, expectedVPCSCMode)
			}
			contextLogger.Printf("Done verification. MCP VPCSC is installed in %v mode\n", cp.VPCMode)
		}

		if err := exec.Run(
			fmt.Sprintf(`bash -c 'cat <<EOF | kubectl apply --context=%s -f -
apiVersion: v1
data:
  mesh: |-
    accessLogFile: /dev/stdout
kind: ConfigMap
metadata:
  name: istio-asm-managed-rapid
  namespace: istio-system
EOF'`, context)); err != nil {
			return fmt.Errorf("error enabling access logging to help with debugging tests")
		}

		// Install Gateway
		if err := exec.Run("kubectl apply -f tools/packaging/knative/gateway -n istio-system --context=" + context); err != nil {
			return fmt.Errorf("error installing injected-gateway: %w", err)
		}

		contextLogger.Println("Done installing MCP via AFC...")
	}

	if err := createRemoteSecretsManaged(c.settings); err != nil {
		return fmt.Errorf("failed to enable managed multicluster: %w", err)
	}

	if c.settings.FeaturesToTest.Has(string(resource.Autopilot)) {
		var wg sync.WaitGroup
		for _, context := range contexts {
			contextLogger := log.New(os.Stdout,
				fmt.Sprintf("[kubeContext: %s] ", context), log.Ldate|log.Ltime)
			contextLogger.Println("Warm up Autopilot cluster by deploying dummy workloads")
			wg.Add(1)
			go func(context string) {
				defer wg.Done()
				if err := warmupAutopilotCluster(context); err != nil {
					contextLogger.Println(err)
				}
			}(context)
		}
		wg.Wait()
	}

	return nil
}

func warmupAutopilotCluster(context string) error {
	cluster := kube.GKEClusterSpecFromContext(context)
	// b/203609464
	// Autopilot does not provide control on node, and new cluster only provides a very small node pool,
	// which will take time to scale up during test runs and lead to test timeout.
	// As a short term workaround, we warm up the cluster by deploying bunch of dummy workloads before running the test.
	if err := exec.Run(fmt.Sprintf("bash -c "+
		"\"kubectl --context=%s create deployment warmup --image=nginx --replicas=50 & sleep 30s\"", context)); err != nil {
		return fmt.Errorf("failed to deploy warm up workloads: %w", err)
	}

	// Wait for 20 minutes before we start checking the cluster status.
	time.Sleep(time.Minute * 20)

	// Wait for the Master VM to complete resize.
	for {
		getStatusCmd := fmt.Sprintf("gcloud container clusters describe %s"+
			" --project %s --region %s --format \"value(status)\"",
			cluster.Name, cluster.ProjectID, cluster.Location)
		clusterStatus, err := exec.RunWithOutput(getStatusCmd)
		if err != nil {
			return fmt.Errorf("failed to wait for cluster to complete reconciling: %w", err)
		}
		if strings.TrimSpace(clusterStatus) == "RUNNING" {
			break
		}
		// Master VM resizing will normally take ~30min.
		time.Sleep(time.Minute * 10)
	}

	// Delete the warmup workloads after the master VM got resized.
	// This is okay because the master VM won't shrink.
	if err := exec.Run(fmt.Sprintf("bash -c "+
		"\"kubectl --context=%s delete deployment warmup \"", context)); err != nil {
		return fmt.Errorf("failed to delete warm up workloads: %w", err)
	}
	return nil
}

func generateAFCBuildOfflineFlags(outputDir string) []string {
	return []string{
		"build-offline-package",
		"--output_dir", outputDir,
		"--verbose",
	}
}

func generateAFCInstallFlags(settings *resource.Settings, cluster *kube.GKEClusterSpec, outputDir string) []string {
	installFlags := []string{
		"install",
		"--project_id", cluster.ProjectID,
		"--cluster_location", cluster.Location,
		"--cluster_name", cluster.Name,
		"--managed",
		"--fleet_id", settings.GCRProject,
		// Fix the channel to rapid since the go test needs to know injection label beforehand.
		// Without this, AFC will use GKE channel which can change when we bump the cluster version.
		// The test will overwrite the istiod/proxyv2 image with test image built on-the-fly if
		// staging environment is used.
		"--channel", "rapid",
		"--enable-all", // We can't use getInstallEnableFlags() since it apparently doesn't match what AFC expects
		"--verbose",
		"--ca", "mesh_ca",
		"--output_dir", outputDir,
		"--offline",
	}
	if settings.FeaturesToTest.Has(string(resource.VPCSC)) {
		installFlags = append(installFlags, "--use_vpcsc")
	}

	// To test Managed CNI, we need to pass an extra flag to ASMCLI so that we don't
	// manually apply static manifests
	if settings.FeaturesToTest.Has(string(resource.CNI)) || settings.FeaturesToTest.Has(string(resource.Autopilot)) {
		installFlags = append(installFlags, "--use_managed_cni")
	}

	return installFlags
}

func generateAFCInstallEnvvars(settings *resource.Settings) []string {
	// _CI_ASM_PKG_LOCATION _CI_ASM_IMAGE_LOCATION are required for unreleased
	// ASM and its install script (master and staging branch).
	envvars := []string{
		"_CI_ASM_KPT_BRANCH=" + settings.NewtaroCommit,
	}
	if settings.InstallOverride.IsSet() {
		envvars = append(envvars,
			"_CI_ASM_IMAGE_LOCATION="+settings.InstallOverride.Hub,
			"_CI_ASM_IMAGE_TAG="+settings.InstallOverride.Tag,
			"_CI_ASM_PKG_LOCATION="+settings.InstallOverride.ASMImageBucket,
		)
	} else {
		// ASM MCP VPCSC test is required to use production by VPCSC integration.
		// Unfortunately, production meshconfig control plane doesn't have access
		// to asm-staging-images. So we'll skip any image overwrite for this particular
		// test.
		if !settings.FeaturesToTest.Has(string(resource.VPCSC)) {
			envvars = append(envvars,
				"_CI_ASM_IMAGE_LOCATION="+os.Getenv("HUB"),
				"_CI_ASM_IMAGE_TAG="+os.Getenv("TAG"),
				"_CI_ASM_PKG_LOCATION="+resource.DefaultASMImageBucket,
			)
		} else {
			// TODO(b/208667932): Only existing prod image can be used
			// for VPCSC test. This may need to be updated once the most
			// recent prod image is available.
			envvars = append(envvars,
				"MAJOR=1",
				"MINOR=11",
				"POINT=2",
				"REV=17",
			)
		}
	}
	return envvars
}

func patchCPRWithImageWalkFn(path string, info os.FileInfo, err error) error {
	// yaml.Node is the only way to preserve in-line kpt reference comments.
	// Simply Marshal/Unmarshal will lose the kpt setters and fail the tests.
	type Metadata struct {
		Name        string    `yaml:"name"`
		Namespace   string    `yaml:"namespace"`
		Labels      yaml.Node `yaml:"labels,omitempty"`
		Annotations yaml.Node `yaml:"annotations,omitempty"`
	}
	type ControlPlaneRevision struct {
		APIVersion string    `yaml:"apiVersion"`
		Kind       string    `yaml:"kind"`
		Metadata   Metadata  `yaml:"metadata"`
		Spec       yaml.Node `yaml:"spec"`
		Status     yaml.Node `yaml:"status,omitempty"`
	}

	if info.IsDir() {
		return nil
	}
	if err != nil {
		return err
	}

	// Read the ControlPlaneRevision and patch the annotation with the custom image.
	cprBytes, err := ioutil.ReadFile(path)
	if err != nil {
		return err
	}
	var cpr ControlPlaneRevision
	if err := yaml.Unmarshal(cprBytes, &cpr); err != nil {
		return fmt.Errorf("unable to parse %s: %w", path, err)
	}
	// No annotation field, initialize it as a mapping node.
	if len(cpr.Metadata.Annotations.Content) == 0 {
		cpr.Metadata.Annotations = yaml.Node{Kind: yaml.MappingNode}
	}
	patched := false
	cloudRunImage := fmt.Sprintf("%s/%s:%s", os.Getenv("HUB"), "cloudrun", os.Getenv("TAG"))
	for i := 0; i < len(cpr.Metadata.Annotations.Content); i += 2 {
		// Key/Value pairs are traversed in sequence like k1, v1, k2, v2...
		if cpr.Metadata.Annotations.Content[i].Value == ImageAnnotationKey {
			patched = true
			cpr.Metadata.Annotations.Content[i+1].Value = cloudRunImage
			break
		}
	}
	// Key does not exist, adding the key/value pair.
	if !patched {
		cpr.Metadata.Annotations.Content = append(cpr.Metadata.Annotations.Content,
			&yaml.Node{
				Kind:  yaml.ScalarNode,
				Value: ImageAnnotationKey,
			},
			&yaml.Node{
				Kind:  yaml.ScalarNode,
				Value: cloudRunImage,
			})
	}

	// Replace the ControlPlaneRevision with patched annotation without changing the mode.
	bytesToWrite, err := yaml.Marshal(cpr)
	if err != nil {
		return err
	}
	if err := ioutil.WriteFile(path, bytesToWrite, info.Mode()); err != nil {
		return err
	}
	return nil
}

func gkeURI(spec *kube.GKEClusterSpec) string {
	format := "https://container.googleapis.com/v1/projects/%s/locations/%s/clusters/%s"
	return fmt.Sprintf(format, spec.ProjectID, spec.Location, spec.Name)
}
