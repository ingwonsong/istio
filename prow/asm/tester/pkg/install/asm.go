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
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"istio.io/istio/prow/asm/tester/pkg/exec"
	"istio.io/istio/prow/asm/tester/pkg/gcp"
	"istio.io/istio/prow/asm/tester/pkg/install/revision"
	"istio.io/istio/prow/asm/tester/pkg/kube"
	"istio.io/istio/prow/asm/tester/pkg/resource"
)

func ASMOutputDir() string {
	return filepath.Join(os.Getenv("ARTIFACTS"), "asmcli_out")
}

func (c *installer) installASM(rev *revision.Config) error {
	pkgPath := filepath.Join(c.settings.RepoRootDir, resource.ConfigDirPath, "kpt-pkg")
	kptSetPrefix := fmt.Sprintf("kpt cfg set %s", pkgPath)
	contexts := c.settings.KubeContexts
	log.Println("Downloading ASM script for the installation...")
	scriptPath, err := downloadInstallScript(c.settings, rev)
	if err != nil {
		return fmt.Errorf("failed to download the install script: %w", err)
	}

	// Use the first project as the environ name
	// must do this here because each installation depends on the value
	environProjectNumber, err := gcp.GetProjectNumber(kube.GKEClusterSpecFromContext(contexts[0]).ProjectID)
	if err != nil {
		return fmt.Errorf("failed to read environ number: %w", err)
	}
	os.Setenv("_CI_ENVIRON_PROJECT_NUMBER", strings.TrimSpace(environProjectNumber))

	for _, context := range contexts {
		contextLogger := log.New(os.Stdout,
			fmt.Sprintf("[kubeContext: %s] ", context), log.Ldate|log.Ltime)
		contextLogger.Println("Performing ASM installation...")
		cluster := kube.GKEClusterSpecFromContext(context)
		var trustedGCPProjects string

		// Create the istio-system ns before running the install_asm script.
		// TODO(chizhg): remove this line after install_asm script can create it.
		if err := exec.Run(fmt.Sprintf("bash -c "+
			"\"kubectl create namespace istio-system --dry-run=client -o yaml "+
			"| kubectl apply -f - --context=%s \"", context)); err != nil {
			return fmt.Errorf("failed to create istio-system namespace: %w", err)
		}

		// Override CA with CA from revision
		// clunky but works
		ca := c.settings.CA
		if rev.CA != "" {
			ca = resource.CAType(rev.CA)
		}
		// Per-CA custom setup
		if ca == resource.MeshCA || ca == resource.PrivateCA {
			// add other projects to the trusted GCP projects for this cluster
			if c.settings.ClusterTopology == resource.MultiProject {
				var otherIds []string
				for _, otherContext := range contexts {
					if otherContext != context {
						otherIds = append(otherIds, kube.GKEClusterSpecFromContext(otherContext).ProjectID)
					}
				}
				trustedGCPProjects = strings.Join(otherIds, ",")
				contextLogger.Printf("Running with trusted GCP projects: %s", trustedGCPProjects)
			}

			// For Prow jobs running with GKE test/staging/staging2 clusters, overwrite
			// GKE_CLUSTER_URL with a custom overlay to fix the issue in installing ASM
			// with MeshCA. See b/177358640 for more details.
			endpoint := os.Getenv(cloudAPIEndpointOverrides)
			if endpoint == testEndpoint || endpoint == stagingEndpoint || endpoint == staging2Endpoint {
				contextLogger.Println("Setting KPT for GKE test/staging/staging2 clusters...")
				if err := exec.RunMultiple([]string{
					fmt.Sprintf("%s gcloud.core.project %s", kptSetPrefix, cluster.ProjectID),
					fmt.Sprintf("%s gcloud.compute.location %s", kptSetPrefix, cluster.Location),
					fmt.Sprintf("%s gcloud.container.cluster %s", kptSetPrefix, cluster.Name),
				}); err != nil {
					return err
				}
				// Set the env var to allow talking to the HUB autopush API in
				// the GKE test/staging/staging2 environment, according to go/gkehub/calling_api#set-endpoint-information
				os.Setenv("CLOUDSDK_API_ENDPOINT_OVERRIDES_GKEHUB", "https://autopush-gkehub.sandbox.googleapis.com/")
			}
		}

		contextLogger.Println("Running installation using install script...")
		if err := exec.Run(scriptPath,
			exec.WithAdditionalEnvs(generateASMInstallEnvvars(c.settings, rev, trustedGCPProjects)),
			exec.WithAdditionalArgs(generateASMInstallFlags(c.settings, rev, pkgPath, cluster))); err != nil {
			return fmt.Errorf("ASM installation using script failed: %w", err)
		}

		// Install Gateway
		// If this is Cloud ESF based, don't install gateway here. The customized
		// Cloud ESF gateway will be installed in each test.
		if c.settings.UseASMCLI && !c.settings.InstallCloudESF {
			if err := c.installIngressGateway(context, ""); err != nil {
				return err
			}
		}
	}

	if c.settings.UseASMCLI {
		if err := exec.Run(scriptPath,
			exec.WithAdditionalEnvs(generateASMInstallEnvvars(c.settings, rev, "")), // trustProjects is not used here
			exec.WithAdditionalArgs(generateASMCreateMeshFlags(c.settings))); err != nil {
			return fmt.Errorf("failed to create mesh using asmcli: %w", err)
		}
	} else {
		if err := createRemoteSecrets(c.settings, contexts); err != nil {
			return fmt.Errorf("failed to create remote secrets: %w", err)
		}
	}
	return nil
}

// generateASMInstallEnvvars generates the environment variables needed when
// running the ASM script to install ASM.
func generateASMInstallEnvvars(settings *resource.Settings, rev *revision.Config, trustedGCPProjects string) []string {
	var envvars []string
	varMap := map[string]string{
		"_CI_NO_VALIDATE": "1",
		"_CI_NO_REVISION": "1",
	}

	// For installations from master we point install script to use the images
	// we just built, however, for installations of older releases, we leave
	// these vars out.
	if rev.Version == "" {
		masterVars := map[string]string{
			"_CI_ISTIOCTL_REL_PATH": filepath.Join(settings.RepoRootDir, istioctlPath),
		}
		if settings.InstallOverride.IsSet() {
			masterVars["_CI_ASM_IMAGE_LOCATION"] = settings.InstallOverride.Hub
			masterVars["_CI_ASM_IMAGE_TAG"] = settings.InstallOverride.Tag
			masterVars["_CI_ASM_PKG_LOCATION"] = settings.InstallOverride.ASMImageBucket
		} else {
			masterVars["_CI_ASM_IMAGE_LOCATION"] = os.Getenv("HUB")
			masterVars["_CI_ASM_IMAGE_TAG"] = os.Getenv("TAG")
			masterVars["_CI_ASM_PKG_LOCATION"] = resource.DefaultASMImageBucket
		}
		if settings.UseASMCLI {
			masterVars["_CI_ASM_KPT_BRANCH"] = settings.NewtaroCommit
		} else {
			masterVars["_CI_ASM_KPT_BRANCH"] = settings.ScriptaroCommit
		}
		for k, v := range masterVars {
			varMap[k] = v
		}
	}

	if rev.Name != "" {
		varMap["_CI_NO_REVISION"] = "0"
	}
	if settings.ClusterTopology == resource.MultiProject {
		varMap["_CI_TRUSTED_GCP_PROJECTS"] = trustedGCPProjects
	}

	for k, v := range varMap {
		log.Printf("Setting envvar %s=%s", k, v)
		envvars = append(envvars, fmt.Sprintf("%s=%s", k, v))
	}

	return envvars
}

// commonASMCLIInstallFlags should be appended to any asmcli invocation's flags
func commonASMCLIInstallFlags(settings *resource.Settings) []string {
	var flags []string

	// HubIDNS tests need to specify fleet ID to pass the fleet verification in ASMCLI
	if settings.ClusterType == resource.GKEOnGCP {
		flags = append(flags, "--fleet_id", kube.GKEClusterSpecFromContext(settings.KubeContexts[0]).ProjectID)
	}

	flags = append(flags, "--output_dir", ASMOutputDir())
	return flags
}

// generateASMInstallFlags returns the flags required when running the install
// script to install ASM.
func generateASMInstallFlags(settings *resource.Settings, rev *revision.Config, pkgPath string, cluster *kube.GKEClusterSpec) []string {
	var installFlags []string
	if settings.UseASMCLI {
		installFlags = append(installFlags, "install")
		installFlags = append(installFlags, commonASMCLIInstallFlags(settings)...)
	} else {
		installFlags = append(installFlags, "--mode", "install")
	}

	installFlags = append(installFlags,
		"--project_id", cluster.ProjectID,
		"--cluster_name", cluster.Name,
		"--cluster_location", cluster.Location,
		"--verbose",
		"--option", "audit-authorizationpolicy",
		"--option", "cni-gcp",
	)
	installFlags = append(installFlags, getInstallEnableFlags()...)

	// Use the CA from revision config for the revision we're installing
	ca := settings.CA
	if rev.CA != "" {
		ca = resource.CAType(rev.CA)
	}
	if ca == resource.MeshCA {
		installFlags = append(installFlags, "--ca", "mesh_ca")
	} else if ca == resource.PrivateCA {
		issuingCaPoolId := fmt.Sprintf("%s-%s-%s", subCaIdPrefix, os.Getenv("BUILD_ID"), cluster.Name)
		caName := fmt.Sprintf("projects/%s/locations/%s/caPools/%s",
			cluster.ProjectID, cluster.Location, issuingCaPoolId)
		installFlags = append(installFlags, "--ca", "gcp_cas")
		installFlags = append(installFlags, "--ca_pool", caName)
	} else if ca == resource.Citadel {
		installFlags = append(installFlags,
			"--ca", "citadel")
		// if no revision or the revision specifies to use custom certs, add the Citadel flags
		if rev.Name == "" || rev.CustomCerts {
			installFlags = append(installFlags, "--ca_cert", "samples/certs/ca-cert.pem",
				"--ca_key", "samples/certs/ca-key.pem",
				"--root_cert", "samples/certs/root-cert.pem",
				"--cert_chain", "samples/certs/cert-chain.pem")
		}
	}

	// Set kpt overlays
	overlays := []string{
		filepath.Join(pkgPath, "overlay/default.yaml"),
	}

	// Apply per-revision overlay customizations
	if rev.Overlay != "" {
		overlays = append(overlays, filepath.Join(pkgPath, rev.Overlay))
	}
	if settings.FeatureToTest == resource.UserAuth {
		overlays = append(overlays, filepath.Join(pkgPath, "overlay/user-auth.yaml"))
	}
	if os.Getenv(cloudAPIEndpointOverrides) == testEndpoint {
		overlays = append(overlays, filepath.Join(pkgPath, "overlay/meshca-test-gke.yaml"))
	}
	if os.Getenv(cloudAPIEndpointOverrides) == stagingEndpoint {
		overlays = append(overlays, filepath.Join(pkgPath, "overlay/meshca-staging-gke.yaml"))
	}
	if os.Getenv(cloudAPIEndpointOverrides) == staging2Endpoint {
		overlays = append(overlays, filepath.Join(pkgPath, "overlay/meshca-staging2-gke.yaml"))
	}
	if settings.InstallCloudESF {
		overlays = append(overlays, filepath.Join(pkgPath, "overlay/cloudesf-e2e.yaml"))
	}

	installFlags = append(installFlags, "--custom_overlay", strings.Join(overlays, ","))

	// Set the revision name if specified on the per-revision config
	// note that this flag only exists on newer install script versions
	if rev.Name != "" {
		installFlags = append(installFlags, "--revision_name", rev.Name)
	}

	// Other random options
	if settings.ClusterTopology == resource.MultiProject {
		installFlags = append(installFlags, "--option", "multiproject")
	}
	if settings.WIP == resource.HUBWorkloadIdentityPool {
		installFlags = append(installFlags, "--option", "hub-meshca")
	}
	if settings.UseVMs {
		installFlags = append(installFlags, "--option", "vm")
	}

	return installFlags
}

// generateASMCreateMeshFlags returns the flags required when running the asmcli
// script to register clusters and install remote secrets
func generateASMCreateMeshFlags(settings *resource.Settings) []string {
	contexts := settings.KubeContexts
	var createMeshFlags []string
	createMeshFlags = append(createMeshFlags, "create-mesh", kube.GKEClusterSpecFromContext(contexts[0]).ProjectID)

	for _, context := range contexts {
		cluster := kube.GKEClusterSpecFromContext(context)
		createMeshFlags = append(createMeshFlags, fmt.Sprintf("%s/%s/%s",
			cluster.ProjectID, cluster.Location, cluster.Name))
	}

	createMeshFlags = append(createMeshFlags, "--verbose")

	return createMeshFlags
}
