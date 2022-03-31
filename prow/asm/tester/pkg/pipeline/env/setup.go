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

package env

import (
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/hashicorp/go-multierror"

	"istio.io/istio/prow/asm/tester/pkg/exec"
	"istio.io/istio/prow/asm/tester/pkg/gcp"
	"istio.io/istio/prow/asm/tester/pkg/kube"
	"istio.io/istio/prow/asm/tester/pkg/resource"
)

const (
	SharedGCPProject    = "istio-prow-build"
	configDir           = "prow/asm/tester/configs"
	newtaroCommitConfig = "newtaro/commit"
)

func Setup(settings *resource.Settings) error {
	log.Println("🎬 start setting up the environment...")

	// Validate the settings before proceeding.
	if err := resource.ReconcileAndValidateSettings(settings); err != nil {
		return err
	}

	// Populate the settings that will be used during runtime.
	if err := populateRuntimeSettings(settings); err != nil {
		return err
	}

	// Fix the cluster configs before proceeding.
	if err := fixClusterConfigs(settings); err != nil {
		return err
	}

	if settings.ClusterType == resource.GKEOnGCP && !settings.FeaturesToTest.Has(string(resource.Autopilot)) {
		// Enable core dumps for Istio proxy
		if err := enableCoreDumps(settings); err != nil {
			return err
		}
	}

	// Inject system env vars that are required for the test flow.
	if err := injectEnvVars(settings); err != nil {
		return err
	}

	log.Println("ASM Test Framework Settings:")
	log.Print(settings)
	return nil
}

// enableCoreDumps configures the core_pattern kernel property to write core dumps to a writeable directory.
// This allows CI to grab cores from crashing proxies. In OSS this is done by docker exec, since its always a single node.
// Note: OSS also allows an init container on each pod. This option was favored to avoid making tests not representing real world
// deployments.
func enableCoreDumps(settings *resource.Settings) error {
	yaml := filepath.Join(settings.ConfigDir, "core-dump-daemonset.yaml")
	for _, kc := range settings.KubeContexts {
		cmd := fmt.Sprintf("kubectl apply -f %s --context=%s", yaml, kc)
		if err := exec.Run(cmd); err != nil {
			return fmt.Errorf("error deploying core dump daemonset: %w", err)
		}
	}

	return nil
}

// populate extra settings that will be used during runtime
func populateRuntimeSettings(settings *resource.Settings) error {
	settings.ConfigDir = filepath.Join(settings.RepoRootDir, configDir)

	var gcrProjectID string
	if settings.ClusterType == resource.GKEOnGCP {
		gkeContexts, err := kube.Contexts(settings.Kubeconfig)
		if err != nil {
			return err
		}
		cs := kube.GKEClusterSpecsFromContexts(gkeContexts)
		projectIDs := make([]string, len(cs))
		for i, c := range cs {
			projectIDs[i] = c.ProjectID
		}
		settings.ClusterGCPProjects = projectIDs
		// If it's using the gke clusters, use the first available project to hold the images.
		gcrProjectID = settings.ClusterGCPProjects[0]
	} else {
		// Otherwise use the shared GCP project to hold these images.
		gcrProjectID = SharedGCPProject
	}
	settings.GCRProject = gcrProjectID

	f := filepath.Join(settings.ConfigDir, newtaroCommitConfig)
	bytes, err := ioutil.ReadFile(f)
	if err != nil {
		return fmt.Errorf("error reading the Newtaro commit config file: %w", err)
	}
	settings.NewtaroCommit = strings.Split(string(bytes), "\n")[0]

	return nil
}

func injectEnvVars(settings *resource.Settings) error {
	var meshID string
	if settings.ClusterType == resource.GKEOnGCP {
		projectNum, err := gcp.GetProjectNumber(settings.ClusterGCPProjects[0])
		if err != nil {
			return err
		}
		meshID = "proj-" + strings.TrimSpace(projectNum)
	}
	// For onprem with Hub CI jobs, clusters are registered into the environ project
	if settings.WIP == resource.HUBWorkloadIdentityPool && settings.ClusterType == resource.OnPrem {
		environProjectID, err := kube.GetEnvironProjectID(settings.Kubeconfig)
		if err != nil {
			return err
		}
		projectNum, err := gcp.GetProjectNumber(environProjectID)
		if err != nil {
			return err
		}
		meshID = "proj-" + strings.TrimSpace(projectNum)
	}

	// TODO(chizhg): delete most, if not all, the env var injections after we convert all the
	// bash to Go and remove the env var dependencies.
	envVars := map[string]string{
		// Run the Go tests with verbose logging.
		"T": "-v",
		// Do not start a container to run the build.
		"BUILD_WITH_CONTAINER": "0",
		// The GCP project we use when testing with multicloud clusters, or when we need to
		// hold some GCP resources that are shared across multiple jobs that are run in parallel.
		"SHARED_GCP_PROJECT": SharedGCPProject,

		"GCR_PROJECT_ID":   settings.GCRProject,
		"CONFIG_DIR":       settings.ConfigDir,
		"CLUSTER_TYPE":     settings.ClusterType.String(),
		"CLUSTER_TOPOLOGY": settings.ClusterTopology.String(),

		"MESH_ID": meshID,

		"CONTROL_PLANE": settings.ControlPlane.String(),
		"CA":            settings.CA.String(),
		"WIP":           settings.WIP.String(),

		"VM_DISTRO":     settings.VMImageFamily,
		"IMAGE_PROJECT": settings.VMImageProject,
	}
	// exported TAG and HUB are used for ASM installation, and as the --istio.test.tag and
	// --istio-test.hub flags of the testing framework
	if settings.InstallOverride.IsSet() {
		envVars["HUB"] = settings.InstallOverride.Hub
		envVars["TAG"] = settings.InstallOverride.Tag
	} else {
		envVars["TAG"] = "BUILD_ID_" + getBuildID()
		var hub string
		if settings.ControlPlane == resource.Unmanaged {
			hub = fmt.Sprintf("gcr.io/%s/asm/%s", settings.GCRProject, getBuildID())
		} else {
			// Don't change this to a publicly-accessible repo, otherwise we could
			// be exposing builds with CVE fixes.
			hub = "gcr.io/asm-staging-images/asm-mcp-e2e-test"
		}
		envVars["HUB"] = hub

	}
	if settings.RevisionConfig != "" {
		envVars["MULTI_VERSION"] = "1"
	}

	if settings.FeaturesToTest.Has(string(resource.Autopilot)) {
		envVars["MCP_TEST_TIMEOUT"] = "90m"
	}

	for name, val := range envVars {
		log.Printf("Set env var: %s=%s", name, val)
		if err := os.Setenv(name, val); err != nil {
			return fmt.Errorf("error setting env var %q to %q", name, val)
		}
	}

	return nil
}

func getBuildID() string {
	return os.Getenv("BUILD_ID")
}

// Fix the cluster configs to meet the test requirements for ASM.
// These fixes are considered as hacky and temporary, ideally in the future they
// should all be handled by the corresponding deployer.
func fixClusterConfigs(settings *resource.Settings) error {
	var err error

	switch resource.ClusterType(settings.ClusterType) {
	case resource.GKEOnGCP:
		err = fixGKE(settings)
	case resource.OnPrem:
		err = fixOnPrem(settings)
	case resource.BareMetal:
		err = fixBareMetal(settings)
	case resource.GKEOnAWS:
		err = fixAWS(settings)
	case resource.GKEOnAzure:
		err = fixAzure(settings)
	case resource.APM:
		err = fixAPM(settings)
	case resource.HybridGKEAndBareMetal:
		err = fixHybridGKEAndBareMetal(settings)
	}

	kubeContexts, kubeContextErr := kube.Contexts(settings.Kubeconfig)
	if kubeContextErr != nil {
		err = multierror.Append(err, kubeContextErr)
	}
	// Set KubeContexts after the cluster configs are fixed.
	settings.KubeContexts = kubeContexts

	return multierror.Flatten(err)
}

func fixGKE(settings *resource.Settings) error {
	gkeContexts, err := kube.Contexts(settings.Kubeconfig)
	if err != nil {
		return err
	}

	// Add firewall rules to enable multi-cluster communication
	if err := addFirewallRules(settings); err != nil {
		return err
	}

	if settings.FeaturesToTest.Has(string(resource.VPCSC)) {
		networkName := settings.GKENetworkName

		// Create router and NAT
		if err := exec.Run(fmt.Sprintf(
			"gcloud compute routers create test-router --network %s --region us-central1",
			networkName)); err != nil {
			log.Println("test-router already exists")
		}
		if err := exec.Run("gcloud compute routers nats create test-nat" +
			" --router=test-router --auto-allocate-nat-external-ips --nat-all-subnet-ip-ranges" +
			" --router-region=us-central1 --enable-logging"); err != nil {
			log.Println("test-nat already exists")
		}

		// Setup the firewall for VPC-SC
		for _, c := range kube.GKEClusterSpecsFromContexts(gkeContexts) {
			getFirewallRuleCmd := fmt.Sprintf("bash -c \"gcloud compute firewall-rules list --filter=\"name~gke-\"%s\"-[0-9a-z]*-master\" --format=json | jq -r '.[0].name'\"", c.Name)
			firewallRuleName, err := exec.RunWithOutput(getFirewallRuleCmd)
			if err != nil {
				return fmt.Errorf("failed to get firewall rule name: %w", err)
			}
			updateFirewallRuleCmd := fmt.Sprintf("gcloud compute firewall-rules update %s --allow tcp --source-ranges 0.0.0.0/0", firewallRuleName)
			if err := exec.Run(updateFirewallRuleCmd); err != nil {
				return fmt.Errorf("error updating firewall rule %q: %w", firewallRuleName, err)
			}
		}

		if err := addIpsToAuthorizedNetworks(settings, gkeContexts); err != nil {
			return fmt.Errorf("error adding ips to authorized networks: %w", err)
		}
	}

	if settings.FeaturesToTest.Has(string(resource.PrivateClusterLimitedAccess)) ||
		settings.FeaturesToTest.Has(string(resource.PrivateClusterNoAccess)) {
		if err := addIpsToAuthorizedNetworks(settings, gkeContexts); err != nil {
			return fmt.Errorf("error adding ips to authorized networks: %w", err)
		}
	}

	return nil
}

func addFirewallRules(settings *resource.Settings) error {
	networkProject := settings.GCPProjects[0]
	clusterProjects := settings.ClusterGCPProjects
	if settings.ClusterTopology != resource.MultiProject {
		clusterProjects = []string{settings.ClusterGCPProjects[0]}
	}

	var sourceRanges, targetTags []string
	for _, p := range clusterProjects {
		// Create the firewall rules to allow multi-cluster communication as per
		// https://github.com/istio/istio.io/blob/master/content/en/docs/setup/platform-setup/gke/index.md#multi-cluster-communication
		allClusterCIDRs, err := exec.RunWithOutput(
			fmt.Sprintf("bash -c 'gcloud --project %s container clusters list --format=\"value(clusterIpv4Cidr)\" | sort | uniq'", p))
		if err != nil {
			return fmt.Errorf("error getting all cluster CIDRs for project %q: %w", p, err)
		}
		sourceRanges = append(sourceRanges, strings.Split(strings.TrimSpace(allClusterCIDRs), "\n")...)

		allClusterTags, err := exec.RunWithOutput(
			fmt.Sprintf("bash -c 'gcloud --project %s compute instances list --format=\"value(tags.items.[0])\" | sort | uniq'", p))
		if err != nil {
			return fmt.Errorf("error getting all cluster tags for project %q: %w", p, err)
		}
		if strings.TrimSpace(allClusterTags) != "" {
			targetTags = append(targetTags, strings.Split(strings.TrimSpace(allClusterTags), "\n")...)
		}
	}

	// Get test runner CIDR
	testRunnerCidr, err := getTestRunnerCidr()
	if err != nil {
		return fmt.Errorf("failed to retrieve test runner IP: %w", err)
	}

	settings.TrustableSourceRanges = strings.Join(append(sourceRanges, testRunnerCidr), ",")

	// Check and delete if already exists
	foundFirewall, err := exec.RunWithOutput(fmt.Sprintf(`gcloud compute firewall-rules list \
	--project=%s \
	--filter name=%s \
	--format='get(name)'`, networkProject, resource.MCFireWallName))
	if err != nil {
		return fmt.Errorf("error checking for firwall rule")
	}
	if strings.TrimSpace(foundFirewall) != "" {
		if err := exec.Run(fmt.Sprintf(`gcloud compute firewall-rules delete %s\
		--project %s \
		--quiet`, resource.MCFireWallName, networkProject)); err != nil {
			return fmt.Errorf("error deleting existing multicluster firewall rule")
		}
	}
	// Now actually make the firewall rule.
	firewallRuleCmd := fmt.Sprintf(`gcloud compute firewall-rules create %s \
	--network=%s \
	--project=%s \
	--allow=tcp,udp,icmp,esp,ah,sctp \
	--direction=INGRESS \
	--priority=900 \
	--source-ranges=%s \
	--quiet`, resource.MCFireWallName, settings.GKENetworkName, networkProject, settings.TrustableSourceRanges)
	// By default GKE Autopilot clusters do not have instance groups, the
	// command to get the instance tags will return empty, and the `gcloud
	// compute firewall-rules create` command does not allow setting
	// `--target-tags` as empty. So do not set `--target-tags` if targetTags is
	// empty.
	if len(targetTags) != 0 {
		firewallRuleCmd = fmt.Sprintf("%s --target-tags=%s", firewallRuleCmd, strings.Join(targetTags, ","))
	}
	if err := exec.Run(firewallRuleCmd); err != nil {
		return fmt.Errorf("error creating the firewall rule to allow multi-cluster communication")
	}

	return nil
}

func addIpsToAuthorizedNetworks(settings *resource.Settings, gkeContexts []string) error {
	// Get test runner IP
	testRunnerCidr, err := getTestRunnerCidr()
	if err != nil {
		return fmt.Errorf("failed to retrieve test runner IP: %w", err)
	}

	// Get clusters' details
	gkeClusterSpecs := kube.GKEClusterSpecsFromContexts(gkeContexts)

	switch len(gkeClusterSpecs) {
	case 1:
		// Enable authorized networks for each cluster
		if err := enableAuthorizedNetworks(gkeClusterSpecs[0].Name, gkeClusterSpecs[0].ProjectID, gkeClusterSpecs[0].Location, testRunnerCidr, "", ""); err != nil {
			return fmt.Errorf("error enabling authorized networks on cluster %v: %w", gkeClusterSpecs[0], err)
		}
	case 2:
		// Get the Pod IP CIDR block for each cluster
		clusterCount := 2
		clusterPodIpCidrs := make([]string, clusterCount)
		subnetPrimaryIpCidrs := make([]string, clusterCount)
		for clusterIndex := 0; clusterIndex < clusterCount; clusterIndex++ {
			podIpCidr, err := getPodIpCidr(gkeClusterSpecs[clusterIndex].Name, gkeClusterSpecs[clusterIndex].ProjectID, gkeClusterSpecs[clusterIndex].Location)
			clusterPodIpCidrs[clusterIndex] = strings.TrimSpace(podIpCidr)
			if err != nil {
				return fmt.Errorf("failed to get pod ip CIDR: %w", err)
			}
			networkProject := settings.GCPProjects[0]
			subnetPrimaryIpCidr, err := getClusterSubnetPrimaryIpCidr(gkeClusterSpecs[clusterIndex].Name, gkeClusterSpecs[clusterIndex].ProjectID, networkProject, gkeClusterSpecs[clusterIndex].Location)
			subnetPrimaryIpCidrs[clusterIndex] = strings.TrimSpace(subnetPrimaryIpCidr)
			if err != nil {
				return fmt.Errorf("failed to get cluster's subnet primary ip CIDR: %w", err)
			}
		}

		// Enable authorized networks for each cluster
		if err := enableAuthorizedNetworks(gkeClusterSpecs[0].Name, gkeClusterSpecs[0].ProjectID, gkeClusterSpecs[0].Location, testRunnerCidr, clusterPodIpCidrs[1], subnetPrimaryIpCidrs[1]); err != nil {
			return fmt.Errorf("error enabling authorized networks on cluster %v: %w", gkeClusterSpecs[0].Name, err)
		}
		if err := enableAuthorizedNetworks(gkeClusterSpecs[1].Name, gkeClusterSpecs[1].ProjectID, gkeClusterSpecs[1].Location, testRunnerCidr, clusterPodIpCidrs[0], subnetPrimaryIpCidrs[0]); err != nil {
			return fmt.Errorf("error enabling authorized networks on cluster %v: %w", gkeClusterSpecs[1].Name, err)
		}
		// Sleep 10 seconds to wait for the cluster update command to take into effect
		time.Sleep(10 * time.Second)
	default:
		return fmt.Errorf("expected # of clusters <= 2, received %d", len(gkeClusterSpecs))
	}

	return nil
}

func getTestRunnerCidr() (string, error) {
	getIPCmd := "curl ifconfig.me"
	ip, err := exec.RunWithOutput(getIPCmd)
	if err != nil {
		return "", fmt.Errorf("error getting test runner IP: %w", err)
	}
	return ip + "/32", nil
}

func getPodIpCidr(clusterName, project, zone string) (string, error) {
	getPodIpCidrCmd := fmt.Sprintf("gcloud container clusters describe %s"+
		" --project %s --zone %s --format \"value(ipAllocationPolicy.clusterIpv4CidrBlock)\"",
		clusterName, project, zone)
	return exec.RunWithOutput(getPodIpCidrCmd)
}

func getClusterSubnetPrimaryIpCidr(clusterName, project, networkProject, zone string) (string, error) {
	getSubnetCmd := fmt.Sprintf("gcloud container clusters describe %s"+
		" --project %s --zone %s --format \"value(subnetwork)\"",
		clusterName, project, zone)
	subnetName, err := exec.RunWithOutput(getSubnetCmd)
	if err != nil {
		return "", err
	}
	getPrimaryIpCidrCmd := fmt.Sprintf("gcloud compute networks subnets describe %s"+
		" --project %s --region %s --format \"value(ipCidrRange)\"",
		strings.TrimSpace(subnetName), networkProject, zone)
	return exec.RunWithOutput(getPrimaryIpCidrCmd)
}

func enableAuthorizedNetworks(clusterName, project, zone, pod_cidr_1, pod_cidr_2, subnet_cidr string) error {
	enableAuthorizedNetCmd := fmt.Sprintf(`gcloud container clusters update %s --project %s --zone %s \
	--enable-master-authorized-networks \
	--master-authorized-networks %s,%s`, clusterName, project, zone, pod_cidr_1, pod_cidr_2)
	// TODO (tairan): https://buganizer.corp.google.com/issues/187960475
	if clusterName == "prow-test1" {
		enableAuthorizedNetCmd = fmt.Sprintf(`gcloud container clusters update %s --project %s --zone %s \
	--enable-master-authorized-networks \
	--master-authorized-networks %s,%s,%s`, clusterName, project, zone, pod_cidr_1, pod_cidr_2, subnet_cidr)
	}
	return exec.Run(enableAuthorizedNetCmd)
}

// Keeps only the user-kubeconfig.yaml entries in the KUBECONFIG for onprem
// by removing others including the admin-kubeconfig.yaml entries.
// This function will modify the KUBECONFIG env variable.
func fixOnPrem(settings *resource.Settings) error {
	return filterKubeconfigFiles(settings, func(name string) bool {
		return strings.HasSuffix(name, "user-kubeconfig.yaml")
	})
}

// Fix bare-metal cluster configs that are created by Tailorbird:
// 1. Keep only the artifacts/kubeconfig entries in the KUBECONFIG for baremetal
//    by removing any others entries.
// 2. Configure cluster proxy.
func fixBareMetal(settings *resource.Settings) error {
	err := filterKubeconfigFiles(settings, func(name string) bool {
		return strings.HasSuffix(name, "artifacts/kubeconfig")
	})
	if err != nil {
		return err
	}
	configs := filepath.SplitList(settings.Kubeconfig)
	for _, config := range configs {
		if err := configMulticloudClusterProxy(settings, multicloudClusterConfig{
			// kubeconfig has the format of "${ARTIFACTS}"/.kubetest2-tailorbird/tf97d94df28f4277/artifacts/kubeconfig
			clusterArtifactsPath: filepath.Dir(config),
			scriptRelPath:        "tunnel.sh",
			regexMatcher:         `.*\-L([0-9]*):localhost.* (root@[0-9]*\.[0-9]*\.[0-9]*\.[0-9]*)`,
			sshKeyRelPath:        "id_rsa",
		}); err != nil {
			return err
		}
	}
	return nil
}

// Fix APM cluster configs that are created by Tailorbird:
// 1. Keep only the artifacts/kubeconfig entries in the KUBECONFIG for baremetal
//    by removing any others entries.
// 2. Configure cluster proxy.
func fixAPM(settings *resource.Settings) error {
	err := filterKubeconfigFiles(settings, func(name string) bool {
		return strings.HasSuffix(name, "artifacts/kubeconfig")
	})
	if err != nil {
		return err
	}

	if err := configMulticloudClusterProxy(settings, multicloudClusterConfig{
		// kubeconfig has the format of "${ARTIFACTS}"/.kubetest2-tailorbird/tf97d94df28f4277/artifacts/kubeconfig
		clusterArtifactsPath: filepath.Dir(settings.Kubeconfig),
		scriptRelPath:        "tunnel.sh",
		regexMatcher:         `.*\-L([0-9]*):localhost.* (nonroot@[0-9]*\.[0-9]*\.[0-9]*\.[0-9]*)`,
		sshKeyRelPath:        "id_rsa",
	}); err != nil {
		return err
	}

	return nil
}

// Fix aws cluster configs that are created by Tailorbird:
// 1. Removes gke_aws_management.conf entry from the KUBECONFIG for aws
// 2. Configure cluster proxy.
func fixAWS(settings *resource.Settings) error {
	if settings.UseOnePlatform {
		if err := configMulticloudClusterProxy(settings, multicloudClusterConfig{
			// kubeconfig has the format of "${ARTIFACTS}"/.kubetest2-tailorbird/t96ea7cc97f047f5/kubeconfig
			clusterArtifactsPath: filepath.Dir(settings.Kubeconfig),
			scriptRelPath:        ".deployer/tunnel.sh",
			regexMatcher:         `.*\-L '([0-9]*):localhost.*' \\\n\t'(ubuntu@[0-9]*\.[0-9]*\.[0-9]*\.[0-9]*)'`,
			sshKeyRelPath:        ".deployer/id_rsa",
		}); err != nil {
			return err
		}
	} else {
		err := filterKubeconfigFiles(settings, func(name string) bool {
			return !strings.HasSuffix(name, "gke_aws_management.conf")
		})
		if err != nil {
			return err
		}

		if err := configMulticloudClusterProxy(settings, multicloudClusterConfig{
			// kubeconfig has the format of "${ARTIFACTS}"/.kubetest2-tailorbird/t96ea7cc97f047f5/.kube/gke_aws_default_t96ea7cc97f047f5.conf
			clusterArtifactsPath: filepath.Dir(filepath.Dir(settings.Kubeconfig)),
			scriptRelPath:        "tunnel-script.sh",
			regexMatcher:         `.*\-L([0-9]*):localhost.* (ubuntu@.*compute\.amazonaws\.com)`,
			sshKeyRelPath:        ".ssh/anthos-gke",
		}); err != nil {
			return err
		}
	}

	return nil
}

// Fix azure cluster configs that are created by Tailorbird:
func fixAzure(settings *resource.Settings) error {
	// Azure should use one platform.
	if !settings.UseOnePlatform {
		return errors.New("GKEOnAzure should use OnePlatform!")
	}

	if err := configMulticloudClusterProxy(settings, multicloudClusterConfig{
		// kubeconfig has the format of "${ARTIFACTS}"/.kubetest2-tailorbird/t96ea7cc97f047f5/kubeconfig
		clusterArtifactsPath: filepath.Dir(settings.Kubeconfig),
		scriptRelPath:        ".deployer/tunnel.sh",
		regexMatcher:         `.*\-L '([0-9]*):localhost.*' \\\n\t'(ubuntu@[0-9]*\.[0-9]*\.[0-9]*\.[0-9]*)'`,
		sshKeyRelPath:        ".deployer/id_rsa",
	}); err != nil {
		return err
	}

	return nil
}

func fixHybridGKEAndBareMetal(settings *resource.Settings) error {
	configs := filepath.SplitList(settings.Kubeconfig)
	for _, config := range configs {
		if strings.Contains(config, "artifacts/kubeconfig") {
			if err := configMulticloudClusterProxy(settings, multicloudClusterConfig{
				// kubeconfig has the format of "${ARTIFACTS}"/.kubetest2-tailorbird/tf97d94df28f4277/artifacts/kubeconfig
				clusterArtifactsPath: filepath.Dir(config),
				scriptRelPath:        "tunnel.sh",
				regexMatcher:         `.*\-L([0-9]*):localhost.* (root@[0-9]*\.[0-9]*\.[0-9]*\.[0-9]*)`,
				sshKeyRelPath:        "id_rsa",
			}); err != nil {
				return err
			}
		} else {
			settings.ClusterProxy = append(settings.ClusterProxy, "")
			settings.ClusterSSHUser = append(settings.ClusterSSHUser, "")
			settings.ClusterSSHKey = append(settings.ClusterSSHKey, "")
		}
	}
	return nil
}

func filterKubeconfigFiles(settings *resource.Settings, shouldKeep func(string) bool) error {
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		return errors.New("KUBECONFIG env var cannot be empty")
	}

	files := filepath.SplitList(kubeconfig)
	filteredFiles := make([]string, 0)
	for _, f := range files {
		if shouldKeep(f) {
			filteredFiles = append(filteredFiles, f)
		} else {
			log.Printf("Remove %q from KUBECONFIG", f)
		}
	}
	filteredKubeconfig := strings.Join(filteredFiles, string(os.PathListSeparator))
	os.Setenv("KUBECONFIG", filteredKubeconfig)
	settings.Kubeconfig = filteredKubeconfig

	return nil
}

type multicloudClusterConfig struct {
	// the path for storing the cluster artifacts files.
	clusterArtifactsPath string
	// tunnel script relative path to the cluster artifacts path.
	scriptRelPath string
	// ssh key file relative path to the cluster artifacts path.
	sshKeyRelPath string
	// regex to find the PORT_NUMBER and BOOTSTRAP_HOST_SSH_USER from the tunnel
	// script.
	regexMatcher string
}

func configMulticloudClusterProxy(settings *resource.Settings, mcConf multicloudClusterConfig) error {
	tunnelScriptPath := filepath.Join(mcConf.clusterArtifactsPath, mcConf.scriptRelPath)
	tunnelScriptContent, err := ioutil.ReadFile(tunnelScriptPath)
	if err != nil {
		return fmt.Errorf("error reading %q under the cluster artifacts path for aws: %w", mcConf.scriptRelPath, err)
	}

	patn := regexp.MustCompile(mcConf.regexMatcher)
	matches := patn.FindStringSubmatch(string(tunnelScriptContent))
	if len(matches) != 3 {
		return fmt.Errorf("error finding PORT_NUMBER and BOOTSTRAP_HOST_SSH_USER from: %q", tunnelScriptContent)
	}
	portNum, bootstrapHostSSHUser := matches[1], matches[2]
	httpProxy := "http://localhost:" + portNum
	bootstrapHostSSHKey := filepath.Join(mcConf.clusterArtifactsPath, mcConf.sshKeyRelPath)
	log.Printf("----------%s Cluster env----------", settings.ClusterType)
	log.Print("HTTPS_PROXY: ", httpProxy)
	settings.ClusterProxy = append(settings.ClusterProxy, httpProxy)
	settings.ClusterSSHUser = append(settings.ClusterSSHUser, bootstrapHostSSHUser)
	settings.ClusterSSHKey = append(settings.ClusterSSHKey, bootstrapHostSSHKey)
	log.Printf("BOOTSTRAP_HOST_SSH_USER: %s, BOOTSTRAP_HOST_SSH_KEY: %s", bootstrapHostSSHUser, bootstrapHostSSHKey)

	for name, val := range map[string]string{
		// Used by ingress related tests
		"BOOTSTRAP_HOST_SSH_USER": bootstrapHostSSHUser,
		"BOOTSTRAP_HOST_SSH_KEY":  bootstrapHostSSHKey,
	} {
		log.Printf("Set env var: %s=%s", name, val)
		if err := os.Setenv(name, val); err != nil {
			return fmt.Errorf("error setting env var %q to %q: %w", name, val, err)
		}
	}

	//  Increase proxy's max connection setup to avoid too many connections error
	sshCmd1 := fmt.Sprintf("ssh -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no -i %s %s \"sudo sed -i 's/#max-client-connections.*/max-client-connections 512/' '/etc/privoxy/config'\"",
		bootstrapHostSSHKey, bootstrapHostSSHUser)
	sshCmd2 := fmt.Sprintf("ssh -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no -i %s %s \"sudo sed -i 's/keep-alive-timeout 5/keep-alive-timeout 3000/' '/etc/privoxy/config'\"",
		bootstrapHostSSHKey, bootstrapHostSSHUser)
	sshCmd3 := fmt.Sprintf("ssh -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no -i %s %s \"sudo sed -i 's/socket-timeout 300/socket-timeout 3000/' '/etc/privoxy/config'\"",
		bootstrapHostSSHKey, bootstrapHostSSHUser)
	sshCmd4 := fmt.Sprintf("ssh -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no -i %s %s \"sudo sed -i 's/#default-server-timeout.*/default-server-timeout 3000/' '/etc/privoxy/config'\"",
		bootstrapHostSSHKey, bootstrapHostSSHUser)
	sshCmd5 := fmt.Sprintf("ssh -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no -i %s %s \"sudo sed -i 's/accept-intercepted-requests 0/accept-intercepted-requests 1/' '/etc/privoxy/config'\"",
		bootstrapHostSSHKey, bootstrapHostSSHUser)
	sshCmd6 := fmt.Sprintf("ssh -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no -i %s %s \"sudo systemctl restart privoxy.service\"",
		bootstrapHostSSHKey, bootstrapHostSSHUser)
	if err := exec.RunMultiple([]string{sshCmd1, sshCmd2, sshCmd3, sshCmd4, sshCmd5, sshCmd6}); err != nil {
		return fmt.Errorf("error running the commands to increase proxy's max connection setup: %w", err)
	}

	return nil
}
