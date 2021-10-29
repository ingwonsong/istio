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
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"istio.io/istio/prow/asm/tester/pkg/exec"
	"istio.io/istio/prow/asm/tester/pkg/gcp"
	"istio.io/istio/prow/asm/tester/pkg/install/revision"
	"istio.io/istio/prow/asm/tester/pkg/resource"
)

const (
	onPremFleetProject         = "tairan-asm-multi-cloud-dev"
	proxiedClusterFleetProject = "tailorbird"
)

// generateASMMultiCloudInstallFlags returns the flags required when running the install
// script to install ASM on multi cloud.
func generateASMMultiCloudInstallFlags(settings *resource.Settings, rev *revision.Config, kubeconfig string) ([]string, error) {
	var installFlags []string
	installFlags = append(installFlags, "install",
		"--kubeconfig", kubeconfig,
		"--platform", "multicloud",
		"--verbose",
	)
	installFlags = append(installFlags, getInstallEnableFlags()...)

	if keyfile := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); keyfile != "" {
		installFlags = append(installFlags,
			"--key-file", keyfile)
	} else {
		return nil, fmt.Errorf("could not retrieve key file from GOOGLE_APPLICATION_CREDENTIALS")
	}

	if serviceAccount, err := gcp.GetServiceAccount(); err != nil {
		return nil, fmt.Errorf("failed to retrieve service account: %w", err)
	} else {
		installFlags = append(installFlags,
			"--service-account", serviceAccount)
	}

	if settings.ClusterType == resource.OnPrem {
		installFlags = append(installFlags,
			"--fleet_id", onPremFleetProject)
	} else {
		installFlags = append(installFlags,
			"--fleet_id", proxiedClusterFleetProject)
	}
	ca := settings.CA
	if rev.CA != "" {
		ca = resource.CAType(rev.CA)
	}
	if ca == resource.MeshCA {
		installFlags = append(installFlags,
			"--ca", "mesh_ca",
		)
	} else if ca == resource.Citadel {
		installFlags = append(installFlags,
			"--ca", "citadel",
		)
		if settings.ClusterType == resource.BareMetal {
			installFlags = append(installFlags, "--ca_cert", "samples/certs/ca-cert.pem",
				"--ca_key", "samples/certs/ca-key.pem",
				"--root_cert", "samples/certs/root-cert.pem",
				"--cert_chain", "samples/certs/cert-chain.pem")
		}
	} else {
		return nil, fmt.Errorf("unsupported CA type for multicloud installation: %s", ca)
	}

	if settings.UseASMCLI {
		installFlags = append(installFlags, commonASMCLIInstallFlags(settings)...)
	}

	return installFlags, nil
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
		environProjectNumber, err := gcp.GetProjectNumber(proxiedClusterFleetProject)
		if err != nil {
			return fmt.Errorf("failed to read environ number: %w", err)
		}
		os.Setenv("_CI_ENVIRON_PROJECT_NUMBER", strings.TrimSpace(environProjectNumber))

		for i, kubeconfig := range kubeconfigs {
			kubeconfigLogger := log.New(os.Stdout,
				fmt.Sprintf("[kubeconfig: %s] ", kubeconfig), log.Ldate|log.Ltime)
			kubeconfigLogger.Println("Performing ASM installation...")
			kubeconfigLogger.Println("Running installation using install script...")
			multicloudFlags, err := generateASMMultiCloudInstallFlags(c.settings, rev,
				kubeconfig)
			if err != nil {
				return fmt.Errorf("error generating multicloud install flags: %w", err)
			}
			if err := exec.Run(scriptPath,
				exec.WithAdditionalEnvs(generateASMInstallEnvvars(c.settings, rev, "")),
				exec.WithAdditionalEnvs([]string{
					fmt.Sprintf("HTTPS_PROXY=%s", c.settings.ClusterProxy[i]),
				}),
				exec.WithAdditionalArgs(multicloudFlags)); err != nil {
				return fmt.Errorf("ASM installation using script failed: %w", err)
			}
			if c.settings.UseASMCLI && !c.settings.InstallCloudESF {
				if err := c.installIngressGateway("", kubeconfig, c.settings.ClusterProxy[i]); err != nil {
					return err
				}
			}
		}
		return nil
	} else {
		return exec.Dispatch(
			c.settings.RepoRootDir,
			"install_asm_on_proxied_clusters",
			nil,
			exec.WithAdditionalEnvs([]string{
				fmt.Sprintf("HTTP_PROXY=%s", c.settings.ClusterProxy[0]),
				fmt.Sprintf("HTTPS_PROXY=%s", c.settings.ClusterProxy[0]),
			}),
		)
	}
}

func (c *installer) installASMOnProxyInjectedClusters(rev *revision.Config) error {
	kubeconfigs := strings.Split(c.settings.Kubeconfig, ":")
	log.Println("Downloading ASM script for the installation...")
	scriptPath, err := downloadInstallScript(c.settings, rev)
	if err != nil {
		return fmt.Errorf("failed to download the install script: %w", err)
	}

	// Use the first project as the environ name
	// must do this here because each installation depends on the value
	environProjectNumber, err := gcp.GetProjectNumber(proxiedClusterFleetProject)
	if err != nil {
		return fmt.Errorf("failed to read environ number: %w", err)
	}
	os.Setenv("_CI_ENVIRON_PROJECT_NUMBER", strings.TrimSpace(environProjectNumber))

	for i, kubeconfig := range kubeconfigs {
		kubeconfigLogger := log.New(os.Stdout,
			fmt.Sprintf("[kubeconfig: %s] ", kubeconfig), log.Ldate|log.Ltime)
		kubeconfigLogger.Println("Performing ASM installation...")
		kubeconfigLogger.Println("Running installation using install script...")

		networkID := "network" + strconv.Itoa(i)
		clusterID := fmt.Sprintf("cn-%s-global-%s-cluster", proxiedClusterFleetProject, strings.Split(c.settings.KubeContexts[i], "-")[0])

		multicloudFlags, err := generateASMMultiCloudInstallFlags(c.settings, rev, kubeconfig)
		if err != nil {
			return fmt.Errorf("error generating multicloud install flags: %w", err)
		}
		if err := exec.Run(scriptPath,
			exec.WithAdditionalEnvs(generateASMInstallEnvvars(c.settings, rev, "")),
			exec.WithAdditionalEnvs([]string{
				fmt.Sprintf("HTTPS_PROXY=%s", c.settings.ClusterProxy[i]),
			}),
			exec.WithAdditionalArgs(multicloudFlags),
			exec.WithAdditionalArgs([]string{"--network_id", networkID})); err != nil {
			return fmt.Errorf("ASM installation using script failed: %w", err)
		}
		if err := c.installIngressGateway("", kubeconfig, c.settings.ClusterProxy[i]); err != nil {
			return fmt.Errorf("failed to install ingress gateway for the cluster: %w", err)
		}
		if err := installExpansionGateway(c.settings, rev, clusterID, networkID, kubeconfig, i); err != nil {
			return fmt.Errorf("failed to install expansion gateway for the cluster: %w", err)
		}
		if err := configureExternalIP(c.settings, kubeconfig, i); err != nil {
			return fmt.Errorf("failed to configure external IP for the cluster: %w", err)
		}
	}

	return exec.Dispatch(
		c.settings.RepoRootDir,
		"configure_remote_secrets_for_baremetal",
		nil,
		exec.WithAdditionalEnvs([]string{
			fmt.Sprintf("HTTP_PROXY_LIST=%s", strings.Join(c.settings.ClusterProxy, ",")),
		}),
	)
}

func (c *installer) installASMOnMulticloud(rev *revision.Config) error {
	kubeconfigs := strings.Split(c.settings.Kubeconfig, ":")
	log.Println("Downloading ASM script for the installation...")
	scriptPath, err := downloadInstallScript(c.settings, rev)
	if err != nil {
		return fmt.Errorf("failed to download the install script: %w", err)
	}

	// Use the first project as the environ name
	// must do this here because each installation depends on the value
	environProjectNumber, err := gcp.GetProjectNumber(onPremFleetProject)
	if err != nil {
		return fmt.Errorf("failed to read environ number: %w", err)
	}
	os.Setenv("_CI_ENVIRON_PROJECT_NUMBER", strings.TrimSpace(environProjectNumber))

	for i, kubeconfig := range kubeconfigs {
		kubeconfigLogger := log.New(os.Stdout,
			fmt.Sprintf("[kubeconfig: %s] ", kubeconfig), log.Ldate|log.Ltime)
		kubeconfigLogger.Println("Performing ASM installation...")
		kubeconfigLogger.Println("Running installation using install script...")
		networkID := "network" + strconv.Itoa(i)
		clusterID := "cluster" + strconv.Itoa(i)

		if err := installCerts(c.settings, kubeconfig); err != nil {
			return fmt.Errorf("failed to install certs: %w", err)
		}
		multicloudFlags, err := generateASMMultiCloudInstallFlags(c.settings, rev,
			kubeconfig)
		if err != nil {
			return fmt.Errorf("error generating multicloud install flags: %w", err)
		}
		if err := exec.Run(scriptPath,
			exec.WithAdditionalEnvs(generateASMInstallEnvvars(c.settings, rev, "")),
			exec.WithAdditionalArgs(multicloudFlags),
			exec.WithAdditionalArgs([]string{"--network_id", networkID})); err != nil {
			return fmt.Errorf("ASM installation using script failed: %w", err)
		}
		if err := c.installIngressGateway("", kubeconfig, ""); err != nil {
			return fmt.Errorf("failed to install ingress gateway for on-prem cluster: %w", err)
		}
		if err := installExpansionGateway(c.settings, rev, clusterID, networkID, kubeconfig, i); err != nil {
			return fmt.Errorf("failed to install expansion gateway for on-prem cluster: %w", err)
		}
		if err := configureExternalIP(c.settings, kubeconfig, i); err != nil {
			return fmt.Errorf("failed to configure external IP for on-prem cluster: %w", err)
		}
	}

	// TODO(samnaser) should we use `asmcli create-mesh`?
	if err := createRemoteSecretsMulticloud(c.settings, kubeconfigs); err != nil {
		return err
	}
	return nil
}

// TODO(Monkeyanator) figure out if we need this, the migrated version of install_asm_on_proxied_clusters
// doesn't seem to need it.
func installCerts(settings *resource.Settings, kubeconfig string) error {
	certFiles := []string{"ca-cert.pem", "ca-key.pem", "root-cert.pem", "cert-chain.pem"}
	fileArgs := ""
	for _, f := range certFiles {
		fileArgs += fmt.Sprintf(" --from-file=%s", path.Join(settings.RepoRootDir, "samples/certs", f))
	}
	return exec.Run(fmt.Sprintf("kubectl create secret generic cacerts -n istio-system --kubeconfig=%s %s", kubeconfig, fileArgs))
}

// installExpansionGateway performs the steps documented at https://cloud.google.com/service-mesh/docs/on-premises-multi-cluster-setup
func installExpansionGateway(settings *resource.Settings, rev *revision.Config, cluster, network, kubeconfig string, idx int) error {
	if settings.ClusterType == resource.BareMetal {
		os.Setenv("HTTPS_PROXY", settings.ClusterProxy[idx])
		os.Setenv("http_proxy", settings.ClusterProxy[idx])
		defer os.Unsetenv("HTTPS_PROXY")
		defer os.Unsetenv("http_proxy")
	}
	revName := "default"
	if rev.Name != "" {
		revName = rev.Name
	}
	genGwPath := filepath.Join(settings.RepoRootDir, "samples/multicluster/gen-eastwest-gateway.sh")
	genGwCmd := fmt.Sprintf("%s --mesh %q --cluster %q --network %q --revision %q",
		genGwPath, "test-mesh", cluster, network, revName)
	gwIop, err := exec.RunWithOutput(genGwCmd)
	if err != nil {
		return err
	}
	gwIopFileName := fmt.Sprintf("%s-%s-eastwest-gw-iop.yaml", cluster, revName)
	if err := os.WriteFile(gwIopFileName, []byte(gwIop), 0o644); err != nil {
		return fmt.Errorf("failed to write expansion gateway IOP to file: %w", err)
	}

	// TODO(Monkeyanator) use the correct istioctl version to do this for multiversion testing. Can be found in respective artifacts.
	if err := exec.Run(fmt.Sprintf("istioctl install -y -f %s --set hub=%q --set tag=%q --kubeconfig %s",
		gwIopFileName, os.Getenv("HUB"), os.Getenv("TAG"), kubeconfig)); err != nil {
		return err
	}

	log.Println("Exposing expansion gateway services...")
	if err := exec.Run(fmt.Sprintf("kubectl apply -n istio-system -f %s --kubeconfig %s",
		filepath.Join(settings.RepoRootDir, "samples/multicluster/expose-services.yaml"), kubeconfig)); err != nil {
		return err
	}
	return nil
}

func configureExternalIP(settings *resource.Settings, kubeconfig string, idx int) error {
	// Patch BM
	if settings.ClusterType == resource.BareMetal {
		if err := exec.Dispatch(settings.RepoRootDir, "baremetal::configure_external_ip",
			[]string{kubeconfig},
			exec.WithAdditionalEnvs(
				[]string{fmt.Sprintf("HTTPS_PROXY=%s", settings.ClusterProxy[idx])})); err != nil {
			return err
		}
		return nil
	} else {
		const herculesLab = "atl_shared"
		if err := exec.Dispatch(settings.RepoRootDir, "onprem::configure_external_ip",
			[]string{kubeconfig},
			exec.WithAdditionalEnvs(
				[]string{fmt.Sprintf("HERCULES_CLI_LAB=%s", herculesLab)})); err != nil {
			return err
		}
		return nil
	}
}
