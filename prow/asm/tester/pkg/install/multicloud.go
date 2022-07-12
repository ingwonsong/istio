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
	"strconv"
	"strings"

	"istio.io/istio/prow/asm/tester/pkg/exec"
	"istio.io/istio/prow/asm/tester/pkg/gcp"
	"istio.io/istio/prow/asm/tester/pkg/install/revision"
	"istio.io/istio/prow/asm/tester/pkg/resource"
)

const (
	// Use personal test project since there is no project pool for multi-cloud.
	OnPremFleetProject         = "tairan-asm-multi-cloud-dev"
	ProxiedClusterFleetProject = "tailorbird"
)

func (c *installer) installASMOnMulticloudClusters(rev *revision.Config) error {
	kubeconfigs := filepath.SplitList(c.settings.Kubeconfig)
	log.Println("Downloading ASM script for the installation...")
	scriptPath, err := downloadInstallScript(c.settings, rev)
	if err != nil {
		return fmt.Errorf("failed to download the install script: %w", err)
	}

	// Set the _CI_ENVIRON_PROJECT_NUMBER as the project where fleet is registered
	// TODO(chizhg): use the same project for all multicloud.
	environProject := ProxiedClusterFleetProject
	if c.settings.ClusterType == resource.OnPrem ||
		c.settings.ClusterType == resource.HybridGKEAndBareMetal ||
		c.settings.ClusterType == resource.HybridGKEAndEKS {
		environProject = OnPremFleetProject
	}
	if c.settings.MulticloudOverrideEnvironProject {
		environProject = c.settings.GCPProjects[0]
	}
	environProjectNumber, err := gcp.GetProjectNumber(environProject)
	if err != nil {
		return fmt.Errorf("failed to read environ number: %w", err)
	}
	os.Setenv("_CI_ENVIRON_PROJECT_NUMBER", strings.TrimSpace(environProjectNumber))

	// users are required to pass this as well for AKS; the ux may change in the future
	if c.settings.ClusterType == resource.AKS {
		os.Setenv("HUB_REGISTRATION_EXTRA_FLAGS", "--has-private-issuer")
	}

	for i, kubeconfig := range kubeconfigs {
		kubeconfigLogger := log.New(os.Stdout,
			fmt.Sprintf("[kubeconfig: %s] ", kubeconfig), log.Ldate|log.Ltime)
		kubeconfigLogger.Println("Performing ASM installation...")
		kubeconfigLogger.Println("Running installation using install script...")

		networkID := "network" + strconv.Itoa(i)
		clusterID := "cluster" + strconv.Itoa(i)
		additionalFlags, err := generateASMMultiCloudInstallFlags(c.settings, rev,
			kubeconfig, environProject)
		if err != nil {
			return fmt.Errorf("error generating multicloud install flags: %w", err)
		}
		// set the custom option for openshift cluster
		if c.settings.ClusterType == resource.Openshift {
			additionalFlags = append(additionalFlags, "--option", "openshift")
		}

		additionalFlags = append(additionalFlags, "--network_id", networkID)

		additionalEnvVars := generateASMInstallEnvvars(c.settings, rev, "")
		if i < len(c.settings.ClusterProxy) && c.settings.ClusterProxy[i] != "" {
			additionalEnvVars = append(additionalEnvVars, "HTTPS_PROXY="+c.settings.ClusterProxy[i])
		}

		if err := exec.Run(scriptPath,
			exec.WithAdditionalEnvs(additionalEnvVars),
			exec.WithAdditionalArgs(additionalFlags)); err != nil {
			return fmt.Errorf("ASM installation using script failed: %w", err)
		}

		if err := c.installGateways(c.settings, rev, "", kubeconfig, i); err != nil {
			return err
		}
		// enable uid for gateways in openshift
		if c.settings.ClusterType == resource.Openshift {
			cmd := "./oc -n istio-system expose svc/istio-ingressgateway --port=http2"
			if err := exec.Run(cmd); err != nil {
				return fmt.Errorf("error enabling the uid for ingress gateway: %w", err)
			}
			cmd = "./oc -n istio-system expose svc/istio-egressgateway --port=http2"
			if err := exec.Run(cmd); err != nil {
				return fmt.Errorf("error enabling the uid for egress gateway: %w", err)
			}
			for _, ns := range c.settings.WorkloadNamespaces {
				log.Printf("Setting up workload namespace %q...", ns)
				if err := setupNamespaceForOpenshift(c.settings.RepoRootDir, ns); err != nil {
					return fmt.Errorf("failed to set up workload namespace: %v", err)
				}
			}
		}

		if err := installExpansionGateway(c.settings, rev, clusterID, networkID, kubeconfig, i); err != nil {
			return fmt.Errorf("failed to install expansion gateway for the cluster: %w", err)
		}
		// Do not configure external IP for single cluster
		// BM single cluster creation does not provide VIP IP by default
		if len(kubeconfigs) > 1 {
			if err := configureExternalIP(c.settings, kubeconfig, i); err != nil {
				return fmt.Errorf("failed to configure external IP for the cluster: %w", err)
			}
		}
	}

	if len(kubeconfigs) > 1 {
		if c.settings.ClusterType == resource.BareMetal {
			return exec.Dispatch(
				c.settings.RepoRootDir,
				"configure_remote_secrets_for_baremetal",
				nil,
				exec.WithAdditionalEnvs([]string{
					fmt.Sprintf("HTTP_PROXY_LIST=%s", strings.Join(c.settings.ClusterProxy, ",")),
				}),
			)
		}

		// TODO(samnaser) should we use `asmcli create-mesh`?
		if c.settings.ClusterType == resource.OnPrem {
			return createRemoteSecretsMulticloud(c.settings, kubeconfigs)
		} else if c.settings.ClusterType == resource.HybridGKEAndBareMetal {
			return exec.Dispatch(
				c.settings.RepoRootDir,
				"configure_remote_secrets_for_gcp_baremetal_hybrid",
				nil,
				exec.WithAdditionalEnvs([]string{
					fmt.Sprintf("HTTP_PROXY_LIST=%s", strings.Join(c.settings.ClusterProxy, ",")),
				}),
			)
		} else if c.settings.ClusterType == resource.HybridGKEAndEKS {
			return createRemoteSecrets(c.settings, rev, scriptPath)
		}
	}

	return nil
}

// generateASMMultiCloudInstallFlags returns the flags required when running the install
// script to install ASM on multi cloud.
func generateASMMultiCloudInstallFlags(settings *resource.Settings, rev *revision.Config, kubeconfig string, environProject string) ([]string, error) {
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

	installFlags = append(installFlags, "--fleet_id", environProject)

	ca := settings.CA
	if rev.CA != "" {
		ca = resource.CAType(rev.CA)
	}
	// Tairan: for Citadel multicluster, cacerts need to be created ahead so that two clusters have the same root trust
	citadelPluginCerts := false
	if len(filepath.SplitList(settings.Kubeconfig)) > 1 {
		citadelPluginCerts = true
	}
	caFlags, _ := GenCaFlags(ca, settings, nil, citadelPluginCerts)
	installFlags = append(installFlags, commonASMCLIInstallFlags(settings, rev)...)
	installFlags = append(installFlags, caFlags...)
	return installFlags, nil
}

// installExpansionGateway performs the steps documented at https://cloud.google.com/service-mesh/docs/on-premises-multi-cluster-setup
func installExpansionGateway(settings *resource.Settings, rev *revision.Config, cluster, network, kubeconfig string, idx int) error {
	if len(settings.ClusterProxy) != 0 && settings.ClusterProxy[idx] != "" {
		os.Setenv("HTTPS_PROXY", settings.ClusterProxy[idx])
		defer os.Unsetenv("HTTPS_PROXY")
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
	var gwInstallCmd string
	if settings.CA == resource.MeshCA || settings.CA == resource.PrivateCA {
		gwInstallCmd = fmt.Sprintf("istioctl install -y -f %s --set spec.values.global.pilotCertProvider=kubernetes --set hub=%q --set tag=%q --kubeconfig %s",
			gwIopFileName, os.Getenv("HUB"), os.Getenv("TAG"), kubeconfig)
	} else {
		gwInstallCmd = fmt.Sprintf("istioctl install -y -f %s --set hub=%q --set tag=%q --kubeconfig %s",
			gwIopFileName, os.Getenv("HUB"), os.Getenv("TAG"), kubeconfig)
	}
	if err := exec.Run(gwInstallCmd); err != nil {
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
	if settings.ClusterType == resource.BareMetal { // Patch BM
		if err := exec.Dispatch(settings.RepoRootDir, "baremetal::configure_external_ip",
			[]string{kubeconfig},
			exec.WithAdditionalEnvs(
				[]string{fmt.Sprintf("HTTPS_PROXY=%s", settings.ClusterProxy[idx])},
			),
		); err != nil {
			return err
		}
		return nil
	} else if settings.ClusterType == resource.OnPrem { // Patch onprem
		const herculesLab = "atl_shared"
		if err := exec.Dispatch(settings.RepoRootDir, "onprem::configure_ingress_ip",
			[]string{kubeconfig},
			exec.WithAdditionalEnvs(
				[]string{fmt.Sprintf("HERCULES_CLI_LAB=%s", herculesLab)})); err != nil {
			return err
		}
		if err := exec.Dispatch(settings.RepoRootDir, "onprem::configure_expansion_ip",
			[]string{kubeconfig},
			exec.WithAdditionalEnvs(
				[]string{fmt.Sprintf("HERCULES_CLI_LAB=%s", herculesLab)})); err != nil {
			return err
		}
		return nil
	} else if settings.ClusterType == resource.HybridGKEAndBareMetal { // Patch BM in hybrid setup
		if err := exec.Dispatch(settings.RepoRootDir, "baremetal::hybrid_configure_external_ip",
			[]string{kubeconfig},
			exec.WithAdditionalEnvs(
				[]string{fmt.Sprintf("HTTPS_PROXY=%s", settings.ClusterProxy[idx])},
			),
		); err != nil {
			return err
		}
		return nil
	}
	return nil
}

func setupNamespaceForOpenshift(repoRoot string, namespace string) error {
	// Assumes oc is installed by fixOpenshift().
	cmd1 := fmt.Sprintf("./oc adm policy add-scc-to-group anyuid system:serviceaccounts:%s", namespace)
	err := exec.Run(cmd1)
	if err != nil {
		return fmt.Errorf("error enabling the user id 1337 on application namespaces %s with error %s", namespace, err)
	}
	yaml := filepath.Join(repoRoot, "prow/asm/tester/configs", "openshift_ns_modification.yaml")
	cmd2 := fmt.Sprintf("kubectl apply -f %s -n %s", yaml, namespace)
	err = exec.Run(cmd2)
	if err != nil {
		return fmt.Errorf("unable to fullfil the network attachment requirement for namespace %s with error %s", namespace, err)
	}
	return nil
}
