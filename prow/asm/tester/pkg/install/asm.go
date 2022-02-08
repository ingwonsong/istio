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
	contexts := strings.Split(c.settings.KubectlContexts, ",")
	log.Println("Downloading Scriptaro for the installation...")
	scriptaroPath, err := downloadScriptaro(c.settings.ScriptaroCommit, rev)
	if err != nil {
		return fmt.Errorf("failed to download Scriptaro: %w", err)
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

			// b/177358640: for Prow jobs running with GKE staging/staging2 clusters, overwrite
			// GKE_CLUSTER_URL with a custom overlay to fix the issue in installing ASM
			// with MeshCA.
			// TODO(samnaser) setting KPT properties cannot be done in parallel, must copy kpt directories
			// for each installation
			if os.Getenv(cloudAPIEndpointOverrides) == stagingEndpoint ||
				os.Getenv(cloudAPIEndpointOverrides) == staging2Endpoint {
				contextLogger.Println("Setting KPT for GKE staging/staging2 clusters...")
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

		contextLogger.Println("Running Scriptaro installation...")
		if err := exec.Run(scriptaroPath,
			exec.WithAdditionalEnvs(generateASMInstallEnvvars(c.settings, rev, trustedGCPProjects)),
			exec.WithAdditionalArgs(generateASMInstallFlags(c.settings, rev, pkgPath, cluster))); err != nil {
			return fmt.Errorf("scriptaro ASM installation failed: %w", err)
		}
	}

	if err := createRemoteSecrets(c.settings, contexts); err != nil {
		return fmt.Errorf("failed to create remote secrets: %w", err)
	}
	return nil
}

// generateASMInstallEnvvars generates the environment variables needed when
// running install_asm script to install ASM.
func generateASMInstallEnvvars(settings *resource.Settings, rev *revision.Config, trustedGCPProjects string) []string {
	var envvars []string
	varMap := map[string]string{
		"_CI_NO_VALIDATE": "1",
		"_CI_NO_REVISION": "1",
	}

	// For installations from master we point scriptaro to the images we just built, however, for
	// installations of older releases, we leave these vars out.
	if rev.Version == "" {
		masterVars := map[string]string{
			"_CI_ISTIOCTL_REL_PATH":  filepath.Join(settings.RepoRootDir, istioctlPath),
			"_CI_ASM_IMAGE_LOCATION": os.Getenv("HUB"),
			"_CI_ASM_IMAGE_TAG":      os.Getenv("TAG"),
			"_CI_ASM_KPT_BRANCH":     settings.ScriptaroCommit,
			"_CI_ASM_PKG_LOCATION":   "asm-staging-images",
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

// generateASMInstallFlags returns the flags required when running install_asm
// script to install ASM.
func generateASMInstallFlags(settings *resource.Settings, rev *revision.Config, pkgPath string, cluster *kube.GKEClusterSpec) []string {
	installFlags := []string{
		"--project_id", cluster.ProjectID,
		"--cluster_name", cluster.Name,
		"--cluster_location", cluster.Location,
		"--mode", "install",
		"--enable-all",
		"--verbose",
		"--option", "audit-authorizationpolicy",
		"--option", "cni-gcp",
	}

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
	if os.Getenv(cloudAPIEndpointOverrides) == stagingEndpoint {
		overlays = append(overlays, filepath.Join(pkgPath, "overlay/meshca-staging-gke.yaml"))
	}
	if os.Getenv(cloudAPIEndpointOverrides) == staging2Endpoint {
		overlays = append(overlays, filepath.Join(pkgPath, "overlay/meshca-staging2-gke.yaml"))
	}
	installFlags = append(installFlags, "--custom_overlay", strings.Join(overlays, ","))

	// Set the revision name if specified on the per-revision config
	// note that this flag only exists on newer Scriptaro versions
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

func (c *installer) installASMOnProxiedClusters() error {
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

func (c *installer) installASMOnMulticloud() error {
	return exec.Dispatch(
		c.settings.RepoRootDir,
		"install_asm_on_multicloud",
		[]string{
			string(c.settings.WIP),
		})
}
