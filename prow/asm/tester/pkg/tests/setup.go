//  Copyright Istio Authors
//
//  Licensed under the Apache License, Version 2.0 (the "License");
//  you may not use this file except in compliance with the License.
//  You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
//  Unless required by applicable law or agreed to in writing, software
//  distributed under the License is distributed on an "AS IS" BASIS,
//  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//  See the License for the specific language governing permissions and
//  limitations under the License.

package tests

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"istio.io/istio/prow/asm/tester/pkg/exec"
	"istio.io/istio/prow/asm/tester/pkg/install"
	"istio.io/istio/prow/asm/tester/pkg/kube"
	"istio.io/istio/prow/asm/tester/pkg/resource"
	"istio.io/istio/prow/asm/tester/pkg/tests/topology"
)

const (
	testSkipConfigFile  = "tests/skip.yaml"
	imagePullSecretFile = "test_image_pull_secret.yaml"
)

func Setup(settings *resource.Settings) error {
	skipConfigPath := filepath.Join(resource.ConfigDirPath, testSkipConfigFile)
	skipConfig, err := ParseSkipConfig(skipConfigPath)
	if err != nil {
		return err
	}
	testSkipFlags, err := testSkipFlags(skipConfig.Tests, settings.DisabledTests, skipLabels(settings))
	if err != nil {
		return err
	}
	packageSkipEnvvar, err := packageSkipEnvvar(skipConfig.Packages, skipLabels(settings))
	if err != nil {
		return err
	}
	testFlags, err := generateTestFlags(settings)
	if err != nil {
		return err
	}
	if err := configureEnvvars(settings, testFlags, packageSkipEnvvar, testSkipFlags); err != nil {
		return err
	}
	if err := genTopologyFile(settings); err != nil {
		return err
	}

	return nil
}

func configureEnvvars(settings *resource.Settings,
	testFlags []string, packageSkipEnvvar string, testSkipFlags []string) error {
	integrationTestFlags := append(testFlags, testSkipFlags...)
	integrationTestFlagsEnvvar := strings.Join(integrationTestFlags, " ")

	gcrProjectID1, gcrProjectID2 := gcrProjectIDs(settings)
	_, caPool := install.GenCaFlags(resource.PrivateCA, settings, nil, false)
	// environment variables required when running the test make target
	envVars := map[string]string{
		// The Makefile passes the path defined in INTEGRATION_TEST_TOPOLOGY_FILE to --istio.test.kube.topology on go test.
		"INTEGRATION_TEST_TOPOLOGY_FILE": fmt.Sprintf("%s/integration_test_topology.yaml", os.Getenv("ARTIFACTS")),
		"INTEGRATION_TEST_FLAGS":         integrationTestFlagsEnvvar,
		"DISABLED_PACKAGES":              packageSkipEnvvar,
		"TEST_SELECT":                    generateTestSelect(settings),
		"JUNIT_OUT":                      fmt.Sprintf("%s/junit_test_result.xml", os.Getenv("ARTIFACTS")),
		// exported GCR_PROJECT_ID_1 and GCR_PROJECT_ID_2
		// for security and telemetry test.
		"GCR_PROJECT_ID_1": gcrProjectID1,
		"GCR_PROJECT_ID_2": gcrProjectID2,
		// required for gke upgrade test
		"TEST_START_EVENT_URL":         fmt.Sprintf("http://localhost:%s/%s", settings.TestStartEventPort, settings.TestStartEventPath),
		"HTTP_PROXY_LIST":              strings.Join(settings.ClusterProxy, ","),
		"BOOTSTRAP_HOST_SSH_USER_LIST": strings.Join(settings.ClusterSSHUser, ","),
		"BOOTSTRAP_HOST_SSH_KEY_LIST":  strings.Join(settings.ClusterSSHKey, ","),
		"CA_POOL":                      caPool,
	}
	// required for bare metal and multicloud environments single cluster jobs
	if len(settings.ClusterProxy) == 1 {
		envVars["HTTPS_PROXY"] = settings.ClusterProxy[0]
	}
	for k, v := range envVars {
		log.Printf("Set env %s=%s", k, v)
		if err := os.Setenv(k, v); err != nil {
			return err
		}
	}
	return nil
}

func gcrProjectIDs(settings *resource.Settings) (gcrProjectID1, gcrProjectID2 string) {
	gcrProjectID1 = settings.GCRProject
	if len(settings.ClusterGCPProjects) == 2 {
		// If it's using multiple gke clusters, set gcrProjectID2 as the project
		// for the second cluster.
		gcrProjectID2 = settings.ClusterGCPProjects[1]
	} else {
		gcrProjectID2 = gcrProjectID1
	}
	// When HUB Workload Identity Pool is used in the case of multi projects setup, clusters in different projects
	// will use the same WIP and P4SA of the Hub host project.
	if settings.WIP == resource.HUBWorkloadIdentityPool && strings.Contains(settings.TestTarget, "security") {
		gcrProjectID2 = gcrProjectID1
	}
	// For onprem with Hub CI jobs, clusters are registered into the environ project
	if settings.WIP == resource.HUBWorkloadIdentityPool && settings.ClusterType == resource.OnPrem {
		gcrProjectID1, _ = kube.GetEnvironProjectID(settings.Kubeconfig)
		gcrProjectID2 = gcrProjectID1
	}
	return
}

// Outputs YAML to the topology file, in the structure of []cluster.Config to inform the test framework of details about
// each cluster under test. cluster.Config is defined in pkg/test/framework/components/cluster/factory.go.
func genTopologyFile(settings *resource.Settings) error {
	configs := filepath.SplitList(settings.Kubeconfig)
	for i, kubeconfig := range configs {
		var clusterName string
		if settings.ClusterType == resource.GKEOnGCP {
			cs := kube.GKEClusterSpecFromContext(settings.KubeContexts[i])
			clusterName = fmt.Sprintf("cn-%s-%s-%s", cs.ProjectID, cs.Location, cs.Name)
		} else {
			if len(settings.ClusterProxy) > 1 {
				os.Setenv("HTTPS_PROXY", settings.ClusterProxy[i])
				defer os.Unsetenv("HTTPS_PROXY")
			}
			istiodPodsJson, err := exec.RunWithOutput("kubectl -n istio-system get pod -l app=istiod -o json --kubeconfig=" + kubeconfig)
			if err != nil || strings.TrimSpace(istiodPodsJson) == "" {
				return fmt.Errorf("error listing the istiod Pods: %w", err)
			}
			type pods struct {
				Items []*corev1.Pod `json:"items,omitempty"`
			}
			istiodPods := &pods{}
			json.Unmarshal([]byte(istiodPodsJson), istiodPods)
			for _, env := range istiodPods.Items[0].Spec.Containers[0].Env {
				if env.Name == "CLUSTER_ID" {
					clusterName = env.Value
					break
				}
			}
		}

		cc := fmt.Sprintf(`- clusterName: %s
  kind: Kubernetes
  meta:
    kubeconfig: %s`, clusterName, kubeconfig)

		// associate the project for the cluster in the metadata for the config
		// TODO: determine if this logic is correct. It is taken from
		// prow/asm/tester/pkg/install/multicloud.go (installASMOnMulticloudClusters)
		if settings.ClusterType == resource.GKEOnGCP {
			proj := kube.GKEClusterSpecFromContext(settings.KubeContexts[i]).ProjectID
			cc += fmt.Sprintf("\n    %s: %s", "gcp_project", proj)
		} else if settings.ClusterType == resource.OnPrem ||
			settings.ClusterType == resource.HybridGKEAndBareMetal ||
			settings.ClusterType == resource.HybridGKEAndEKS {
			cc += fmt.Sprintf("\n    %s: %s", "gcp_project", install.OnPremFleetProject)
		} else if settings.MulticloudOverrideEnvironProject {
			cc += fmt.Sprintf("\n    %s: %s", "gcp_project", settings.GCPProjects[0])
		} else {
			cc += fmt.Sprintf("\n    %s: %s", "gcp_project", install.ProxiedClusterFleetProject)
		}

		// Disable using simulated Pod-based "VMs" when testing real VMs
		if settings.UseGCEVMs || settings.VMStaticConfigDir != "" {
			cc += "\n    fakeVM: false"
		}
		if i < len(settings.ClusterProxy) {
			cc += fmt.Sprintf("\n    sshuser: %s", settings.ClusterSSHUser[i])
			cc += fmt.Sprintf("\n    sshkey: %s", settings.ClusterSSHKey[i])
			cc += fmt.Sprintf("\n  httpProxy: %s", settings.ClusterProxy[i])
			cc += fmt.Sprintf("\n  proxyKubectlOnly: %t", settings.ClusterType == resource.GKEOnAWS)
		}

		// Add network name for multicloud cluster config.
		// TODO(landow): only set this if we actually installed using it
		// TODO (cont): consider storing the cluster.Topology (the model we're generating here) in the resource.Settings
		// TODO(cont): rather than array of kubeconfig/context names.

		if settings.ClusterType != resource.GKEOnGCP && len(configs) > 1 {
			networkID := fmt.Sprintf("network%d", i)
			if settings.ClusterType == resource.HybridGKEAndBareMetal {
				if isBMCluster(kubeconfig) {
					networkID = "network-bm"
				} else {
					networkID = "tairan-asm-multi-cloud-dev-cluster-net"
				}
			} else if settings.ClusterType == resource.HybridGKEAndEKS {
				if isEKSCluster(kubeconfig) {
					networkID = "network-eks"
				} else {
					networkID = "tairan-asm-multi-cloud-dev-cluster-net"
				}
			}
			cc += fmt.Sprintf("\n  network: %s", networkID)
		}

		if err := topology.AddClusterConfig(cc); err != nil {
			return err
		}
	}
	return nil
}

func isBMCluster(kubeconfig string) bool {
	return strings.HasSuffix(kubeconfig, "artifacts/kubeconfig")
}

func isEKSCluster(kubeconfig string) bool {
	return strings.HasSuffix(kubeconfig, "kubeconfig.yaml")
}
