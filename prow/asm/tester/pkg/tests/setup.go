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
		// required for bare metal and multicloud environments
		"HTTP_PROXY":  os.Getenv("MC_HTTP_PROXY"),
		"HTTPS_PROXY": os.Getenv("MC_HTTP_PROXY"),
		// required for gke upgrade test
		"TEST_START_EVENT_URL": fmt.Sprintf("http://localhost:%s/%s", settings.TestStartEventPort, settings.TestStartEventPath),
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
	for i, kubeconfig := range strings.Split(settings.Kubeconfig, string(os.PathListSeparator)) {
		var clusterName string
		if settings.ClusterType == resource.GKEOnGCP {
			cs := kube.GKEClusterSpecFromContext(settings.KubeContexts[i])
			clusterName = fmt.Sprintf("cn-%s-%s-%s", cs.ProjectID, cs.Location, cs.Name)
		} else {
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
		// Disable using simulated Pod-based "VMs" when testing real VMs
		if settings.UseGCEVMs || settings.VMStaticConfigDir != "" {
			cc += "\n    fakeVM: false"
		}
		// Add network name for multicloud cluster config.
		// TODO: confirm if it's needed or not.
		if settings.ClusterType != resource.GKEOnGCP {
			cc += fmt.Sprintf("\n  network: network%d", i)
		}

		if err := topology.AddClusterConfig(cc); err != nil {
			return err
		}
	}
	return nil
}
