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

package vm

import (
	"fmt"

	"istio.io/istio/prow/asm/tester/pkg/exec"
	"istio.io/istio/prow/asm/tester/pkg/gcp"
	"istio.io/istio/prow/asm/tester/pkg/kube"
	"istio.io/istio/prow/asm/tester/pkg/resource"
	"istio.io/istio/prow/asm/tester/pkg/tests/topology"
)

// Setup runs the test setups for VM tests.
func Setup(settings *resource.Settings, kubeContext string) error {
	firewallTag := "prow-test-vm"

	cs := kube.GKEClusterSpecFromContext(kubeContext)
	// Allow traffic from the trustable source ranges (including the Prow job Pod and
	// the test clusters) to all VMs tagged `gcevm` in the PROJECT_ID where ASM/VMs
	// live.
	// Allow all the TCP traffic because some integration uses random ports, and we
	// cannot limit the usage of them.
	if err := exec.Run(fmt.Sprintf(`gcloud compute firewall-rules create \
		--project=%s \
		--allow=tcp \
		--source-ranges=%s \
		--network=%s \
		--target-tags=%s \
		prow-to-static-vms`, cs.ProjectID, settings.TrustableSourceRanges, settings.GKENetworkName, firewallTag)); err != nil {
		return err
	}

	projectNumber, err := gcp.GetProjectNumber(cs.ProjectID)
	if err != nil {
		return err
	}

	primaryClusterName := fmt.Sprintf("cn-%s-%s-%s", cs.ProjectID, cs.Location, cs.Name)
	asmVMConfig := fmt.Sprintf(`- kind: ASMVM
  clusterName: asm-vms
  primaryClusterName: %s
  meta:
    projectNumber: %s
    project: %s
    gkeLocation: %s
    gkeCluster: %s
    gkeNetwork: %s
    firewallTag: %s
    instanceMetadata:
    - key: gce-service-proxy-agent-bucket
      value: %s
    - key: gce-service-proxy-asm-version
      value: %s
    - key: gce-service-proxy-installer-bucket
      value: %s`, primaryClusterName, projectNumber,
		cs.ProjectID, cs.Location, cs.Name,
		settings.GKENetworkName, firewallTag,
		settings.VMServiceProxyAgentGCSPath, settings.VMServiceProxyAgentASMVersion, settings.VMServiceProxyAgentInstallerGCSPath)

	if err := topology.AddClusterConfig(asmVMConfig); err != nil {
		return err
	}

	return nil
}
