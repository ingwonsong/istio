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

const (
	hybridFleetProject = "tairan-asm-multi-cloud-dev"
)

func (c *installer) installASMOnHybridClusters(rev *revision.Config) error {
	kubeconfigs := filepath.SplitList(c.settings.Kubeconfig)
	log.Println("Downloading ASM script for the installation...")
	scriptPath, err := downloadInstallScript(c.settings, rev)
	if err != nil {
		return fmt.Errorf("failed to download the install script: %w", err)
	}

	// Set the _CI_ENVIRON_PROJECT_NUMBER as the project where fleet is registered
	environProject := hybridFleetProject
	environProjectNumber, err := gcp.GetProjectNumber(environProject)
	if err != nil {
		return fmt.Errorf("failed to read environ number: %w", err)
	}
	os.Setenv("_CI_ENVIRON_PROJECT_NUMBER", strings.TrimSpace(environProjectNumber))

	for i, kubeconfig := range kubeconfigs {
		kubeconfigLogger := log.New(os.Stdout,
			fmt.Sprintf("[kubeconfig: %s] ", kubeconfig), log.Ldate|log.Ltime)
		kubeconfigLogger.Println("Performing ASM installation...")
		kubeconfigLogger.Println("Running installation using install script...")

		networkID := "network-bm"
		clusterID := ""
		if isBMCluster(kubeconfig) {
			clusterID = "cluster-bm"
			additionalFlags, err := generateASMMultiCloudInstallFlags(c.settings, rev,
				kubeconfig, environProject)
			if err != nil {
				return fmt.Errorf("error generating multicloud install flags: %w", err)
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
		} else {
			clusterID = "cluster-gcp"
			networkID = "tairan-asm-multi-cloud-dev-cluster-net"
			gkeContext := ""
			for _, context := range c.settings.KubeContexts {
				if strings.Contains(context, "gke") {
					gkeContext = context
				}
			}
			contextLogger := log.New(os.Stdout,
				fmt.Sprintf("[kubeContext: %s] ", gkeContext), log.Ldate|log.Ltime)
			contextLogger.Println("Performing ASM installation...")
			cluster := kube.GKEClusterSpecFromContext(gkeContext)
			contextLogger.Println("Running installation using install script...")
			pkgPath := filepath.Join(c.settings.RepoRootDir, resource.ConfigDirPath, "kpt-pkg")
			if err := exec.Run(scriptPath,
				exec.WithAdditionalEnvs(generateASMInstallEnvvars(c.settings, rev, "")),
				exec.WithAdditionalArgs(generateASMInstallFlags(c.settings, rev, pkgPath, cluster))); err != nil {
				return fmt.Errorf("ASM installation using script failed: %w", err)
			}
		}
		if err := c.installIngressGateway(c.settings, rev, "", kubeconfig, i); err != nil {
			return err
		}
		if err := installExpansionGateway(c.settings, rev, clusterID, networkID, kubeconfig, i); err != nil {
			return fmt.Errorf("failed to install expansion gateway for the cluster: %w", err)
		}
		if isBMCluster(kubeconfig) {
			if err := configureExternalIP(c.settings, kubeconfig, i); err != nil {
				return fmt.Errorf("failed to configure external IP for the cluster: %w", err)
			}
		}
	}
	if c.settings.ClusterType == resource.HybridGKEAndBareMetal {
		return exec.Dispatch(
			c.settings.RepoRootDir,
			"configure_remote_secrets_for_gcp_baremetal_hybrid",
			nil,
			exec.WithAdditionalEnvs([]string{
				fmt.Sprintf("HTTP_PROXY_LIST=%s", strings.Join(c.settings.ClusterProxy, ",")),
			}),
		)
	}
	return nil
}

func isBMCluster(kubeconfig string) bool {
	return strings.HasSuffix(kubeconfig, "artifacts/kubeconfig")
}
