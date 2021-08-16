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
	"istio.io/istio/prow/asm/tester/pkg/install/revision"
	"istio.io/istio/prow/asm/tester/pkg/kube"
	"istio.io/istio/prow/asm/tester/pkg/resource"
)

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
	environProjectNumber, err := exec.RunWithOutput(fmt.Sprintf(
		"gcloud projects describe %s --format=\"value(projectNumber)\"",
		kube.GKEClusterSpecFromContext(contexts[0]).ProjectID))
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
			}

			// Need to set kpt values per install
			if ca == resource.PrivateCA {
				subordinateCaId := fmt.Sprintf("%s-%s-%s",
					subCaIdPrefix, os.Getenv("BUILD_ID"), cluster.Name)
				caName := fmt.Sprintf("projects/%s/locations/%s/certificateAuthorities/%s",
					cluster.ProjectID, cluster.Location, subordinateCaId)
				if err := exec.RunMultiple([]string{
					fmt.Sprintf("%s anthos.servicemesh.external_ca.ca_name %s", kptSetPrefix, caName),
					fmt.Sprintf("%s gcloud.core.project %s", kptSetPrefix, cluster.ProjectID),
				}); err != nil {
					return err
				}
			}
		}

		contextLogger.Println("Running installation using install script...")
		if err := exec.Run(scriptPath,
			exec.WithAdditionalEnvs(generateASMInstallEnvvars(c.settings, rev, trustedGCPProjects)),
			exec.WithAdditionalArgs(generateASMInstallFlags(c.settings, rev, pkgPath, cluster))); err != nil {
			return fmt.Errorf("ASM installation using script failed: %w", err)
		}
	}

	if err := createRemoteSecrets(c.settings, contexts); err != nil {
		return fmt.Errorf("failed to create remote secrets: %w", err)
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
			"_CI_ISTIOCTL_REL_PATH":  filepath.Join(settings.RepoRootDir, istioctlPath),
			"_CI_ASM_IMAGE_LOCATION": os.Getenv("HUB"),
			"_CI_ASM_IMAGE_TAG":      os.Getenv("TAG"),
			"_CI_ASM_PKG_LOCATION":   "asm-staging-images",
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

// generateASMInstallFlags returns the flags required when running the install
// script to install ASM.
func generateASMInstallFlags(settings *resource.Settings, rev *revision.Config, pkgPath string, cluster *kube.GKEClusterSpec) []string {
	var installFlags []string
	if settings.UseASMCLI {
		installFlags = append(installFlags, "install")
	} else {
		installFlags = append(installFlags, "--mode", "install")
	}

	installFlags = append(installFlags,
		"--project_id", cluster.ProjectID,
		"--cluster_name", cluster.Name,
		"--cluster_location", cluster.Location,
		"--enable-all",
		"--verbose",
		"--option", "audit-authorizationpolicy",
		"--option", "cni-gcp")

	// Use the CA from revision config for the revision we're installing
	ca := settings.CA
	if rev.CA != "" {
		ca = resource.CAType(rev.CA)
	}
	if ca == resource.MeshCA || ca == resource.PrivateCA {
		installFlags = append(installFlags, "--ca", "mesh_ca")
	} else if ca == resource.Citadel {
		installFlags = append(installFlags,
			"--ca", "citadel",
			"--ca_cert", "samples/certs/ca-cert.pem",
			"--ca_key", "samples/certs/ca-key.pem",
			"--root_cert", "samples/certs/root-cert.pem",
			"--cert_chain", "samples/certs/cert-chain.pem")
	}

	// Set kpt overlays
	overlays := []string{
		filepath.Join(pkgPath, "overlay/default.yaml"),
	}

	// Apply per-revision overlay customizations
	if rev.Overlay != "" {
		overlays = append(overlays, filepath.Join(pkgPath, rev.Overlay))
	}
	if ca == resource.PrivateCA && settings.WIP != resource.HUBWorkloadIdentityPool {
		overlays = append(overlays, filepath.Join(pkgPath, "overlay/private-ca.yaml"))
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

// generateASMMultiCloudInstallFlags returns the flags required when running the install
// script to install ASM on multi cloud.
func generateASMMultiCloudInstallFlags(settings *resource.Settings, kubeconfig string) []string {
	var installFlags []string
	installFlags = append(installFlags, "install",
		"--kubeconfig", kubeconfig,
		"--fleet_id", "tailorbird",
		"--platform", "multicloud",
		"--service-account", "prow-gob-storage@istio-prow-build.iam.gserviceaccount.com",
		"--key-file", "/etc/service-account/service-account.json",
		"--enable-all",
		"--verbose",
	)
	ca := settings.CA
	if ca == resource.MeshCA {
		installFlags = append(installFlags,
			"--ca", "mesh_ca",
		)
	} else {
		installFlags = append(installFlags,
			"--ca", "citadel",
		)
	}
	return installFlags
}

func (c *installer) installASMOnProxiedClusters(rev *revision.Config) error {
	if c.settings.UseASMCLI {
		kubeconfigs := strings.Split(c.settings.Kubeconfig, ",")
		log.Println("Downloading ASM script for the installation...")
		scriptPath, err := downloadInstallScript(c.settings, rev)
		if err != nil {
			return fmt.Errorf("failed to download the install script: %w", err)
		}

		// Use the first project as the environ name
		// must do this here because each installation depends on the value
		environProjectNumber, err := exec.RunWithOutput(fmt.Sprintf(
			"gcloud projects describe %s --format=\"value(projectNumber)\"",
			"tailorbird"))
		if err != nil {
			return fmt.Errorf("failed to read environ number: %w", err)
		}
		os.Setenv("_CI_ENVIRON_PROJECT_NUMBER", strings.TrimSpace(environProjectNumber))

		for _, kubeconfig := range kubeconfigs {
			kubeconfigLogger := log.New(os.Stdout,
				fmt.Sprintf("[kubeconfig: %s] ", kubeconfig), log.Ldate|log.Ltime)
			kubeconfigLogger.Println("Performing ASM installation...")

			kubeconfigLogger.Println("Running installation using install script...")
			if err := exec.Run(scriptPath,
				exec.WithAdditionalEnvs(generateASMInstallEnvvars(c.settings, rev, "")),
				exec.WithAdditionalEnvs([]string{
					fmt.Sprintf("HTTPS_PROXY=%s", os.Getenv("MC_HTTP_PROXY")),
				}),
				exec.WithAdditionalArgs(generateASMMultiCloudInstallFlags(c.settings, kubeconfig))); err != nil {
				return fmt.Errorf("ASM installation using script failed: %w", err)
			}
		}
		return nil
	} else {
		return exec.Dispatch(
			c.settings.RepoRootDir,
			"install_asm_on_proxied_clusters",
			nil,
			exec.WithAdditionalEnvs([]string{
				fmt.Sprintf("HTTP_PROXY=%s", os.Getenv("MC_HTTP_PROXY")),
				fmt.Sprintf("HTTPS_PROXY=%s", os.Getenv("MC_HTTP_PROXY")),
			}),
		)
	}
}

func (c *installer) installASMOnMulticloud() error {
	return exec.Dispatch(
		c.settings.RepoRootDir,
		"install_asm_on_multicloud",
		[]string{
			string(c.settings.WIP),
		})
}
