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
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"istio.io/istio/prow/asm/tester/pkg/install/revision"
	"istio.io/istio/prow/asm/tester/pkg/kube"
	"istio.io/istio/prow/asm/tester/pkg/resource"
)

// generateTestFlags returns an array containing options to be passed
// when running the test target.
func generateTestFlags(settings *resource.Settings) ([]string, error) {
	testFlags := []string{"--istio.test.kube.deploy=false"}
	if settings.ControlPlane != resource.Unmanaged {
		if !settings.FeaturesToTest.Has(string(resource.VPCSC)) {
			testFlags = append(testFlags, "--istio.test.skipDelta")
			if settings.UseAutoCPManagement {
				// Auto CP management determines the MCP channel from the GKE release channel.
				// Assume if there are multiple clusters, the channels are the same.
				revisionLabel := "asm-managed"
				channel, err := kube.GKEClusterChannelFromContext(settings.KubeContexts[0])
				if err == nil && channel != "" && channel != "REGULAR" {
					revisionLabel += "-" + strings.ToLower(channel)
				}
				testFlags = append(testFlags,
					"--istio.test.revision="+revisionLabel)
			} else {
				testFlags = append(testFlags,
					// install_asm will install the image to all three channels.
					// So all the revision labels should work.
					// However, AFC currently only installs one rapid. Change the test
					// revision to rapid to work with both cases.
					"--istio.test.revision=asm-managed-rapid")
			}
		} else {
			testFlags = append(testFlags,
				// TODO(b/208667932) VPC-SC does not run using latest config
				"--istio.test.revisions=asm-managed-rapid=1.11.2", "--istio.test.skipDelta")
		}
	}

	// multicloud settings
	if settings.ClusterType != resource.GKEOnGCP {
		// going from 20s to 100s for the total retry timeout (all attempts)
		testFlags = append(testFlags, "--istio.test.echo.callTimeout=100s")
		// going from 5s to 30s for individual ForwardEchoRequests (bounds total all calls in req.Count)
		testFlags = append(testFlags, "--istio.test.echo.requestTimeout=30s")

		// make echo deployments use an image pull secret
		testFlags = append(testFlags,
			fmt.Sprintf("--istio.test.imagePullSecret=%s/%s",
				os.Getenv("ARTIFACTS"), imagePullSecretFile))
	}

	if !settings.UseVMs {
		testFlags = append(testFlags, "--istio.test.skipVM=true")
	}
	if settings.UseVMs || settings.UseGCEVMs {
		// TODO these are the only security tests that excercise VMs. The other tests are written in a way
		// they panic with StaticVMs.
		if settings.TestTarget == "test.integration.asm.security" {
			enabledTests := []string{
				"TestReachability",
				"TestMtlsStrictK8sCA",
				"TestPassThroughFilterChain",
				"TestAuthz_Principal",
				"TestAuthz_DenyPrincipal",
				"TestAuthz_Namespace",
				"TestAuthz_DenyNamespace",
				"TestAuthz_NotNamespace",
				"TestAuthz_NotHost",
				"TestAuthz_NotMethod",
				"TestAuthz_NotPort",
				"TestAuthz_DenyPlaintext",
				"TestAuthz_JWT",
				"TestAuthz_WorkloadSelector",
				"TestAuthz_PathPrecedence",
				"TestAuthz_IngressGateway",
				"TestAuthz_EgressGateway",
				"TestAuthz_Conditions",
				"TestAuthz_PathNormalization",
				"TestAuthz_CustomServer",
			}
			enableTestCMD := fmt.Sprintf("-run=%s", strings.Join(enabledTests, "\\|"))
			testFlags = append(testFlags, enableTestCMD)
		}
	}

	if settings.FeaturesToTest.Has(string(resource.Autopilot)) {
		testFlags = append(testFlags, "--istio.test.skipTProxy=true")

		// Autopilot could start repairing itself as it sees more workloads
		// which takes more time for the pods to become ready
		testFlags = append(testFlags, "--istio.test.echo.readinessTimeout=40m")
		testFlags = append(testFlags, "--istio.test.echo.callTimeout=40m")
		testFlags = append(testFlags, "--istio.test.echo.requestTimeout=40m")
	}

	if settings.FeaturesToTest.Has(string(resource.CNI)) {
		// Managed Control Plane will install the managed CNI via AFC.
		if settings.ControlPlane == resource.Unmanaged {
			testFlags = append(testFlags, " --istio.test.istio.enableCNI=true ")
		}
	}

	// openshift doesn't support TProxy mode
	if settings.ClusterType == resource.Openshift {
		testFlags = append(testFlags, "--istio.test.skipTProxy=true")
	}

	// Need to pass the revisions and versions to test framework if specified
	if settings.RevisionConfig != "" {
		revisionConfigPath := filepath.Join(settings.RepoRootDir, resource.ConfigDirPath, "revision-deployer", settings.RevisionConfig)
		revisionConfig, err := revision.ParseConfig(revisionConfigPath)
		if err != nil {
			return nil, err
		}
		if revisionFlag := generateRevisionsFlag(revisionConfig); revisionFlag != "" {
			testFlags = append(testFlags, generateRevisionsFlag(revisionConfig))
		}
	}

	if settings.UseDefaultInjectionLabels {
		testFlags = append(testFlags, "--istio.test.useDefaultInjectionLabels=true")
	}

	if settings.UseKubevirtVM {
		testFlags = append(testFlags, "--istio.test.echo.kube.template.deployment=kubevirt_vm_deployment.yaml")
	}

	return testFlags, nil
}

// generateTestSelect returns an array containing options to be passed
// when running the test target.
func generateTestSelect(settings *resource.Settings) string {
	const (
		mcPresubmitTarget   = "test.integration.multicluster.kube.presubmit"
		asmSecurityTarget   = "test.integration.asm.security"
		asmNetworkingTarget = "test.integration.asm.networking"
		migrationTarget     = "test.integration.asm.addon-migration"
	)

	testSelect := ""
	if settings.ControlPlane != resource.Managed {
		// TODO(nmittler): remove this once we no longer run the multicluster tests.
		//if settings.TestTarget == mcPresubmitTarget {
		//	testSelect = "+multicluster"
		//}
		if settings.TestTarget == asmSecurityTarget ||
			settings.TestTarget == asmNetworkingTarget {
			if testSelect == "" {
				testSelect = "-customsetup,-postsubmit"
			}
		}
		if settings.FeaturesToTest.Has(string(resource.UserAuth)) {
			testSelect = "+userauth"
		}
		if settings.FeaturesToTest.Has(string(resource.PolicyConstraint)) {
			testSelect = "+policyconstraint"
		}
	} else if settings.ControlPlane == resource.Managed {
		testSelect = "-customsetup"
		if settings.TestTarget == migrationTarget {
			testSelect = ""
		}
		//if settings.ClusterTopology == resource.MultiCluster {
		//	if settings.TestTarget == mcPresubmitTarget {
		//		testSelect += ",+multicluster"
		//	}
		//}
	}
	if !settings.FeaturesToTest.Has(string(resource.CompositeGateway)) {
		if testSelect != "" {
			testSelect += ","
		}
		testSelect += "-compositegateway"
	}

	return testSelect
}

// generateRevisionsFlag takes a set of revision configs and generates the --istio.test.revisions flag.
func generateRevisionsFlag(revisions *revision.Configs) string {
	var terms []string
	multiversion := false
	for _, rev := range revisions.Configs {
		if rev.Version != "" {
			multiversion = true
			terms = append(terms, fmt.Sprintf("%s=%s", rev.Name, rev.Version))
		} else {
			terms = append(terms, rev.Name)
		}
	}
	if !multiversion {
		return ""
	}

	return fmt.Sprintf("--istio.test.revisions=%s",
		strings.Join(terms, ","))
}
