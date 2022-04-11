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

package policyconstraint

import (
	"fmt"

	"istio.io/istio/prow/asm/tester/pkg/exec"
	"istio.io/istio/prow/asm/tester/pkg/kube"
	"istio.io/istio/prow/asm/tester/pkg/resource"
)

// Teardown cleans up the test setups for policy controller tests.
func Teardown(settings *resource.Settings) error {
	if err := cleanupConstraints(settings); err != nil {
		return fmt.Errorf("error cleaning up constraints: %w", err)
	}
	if err := cleanupACM(settings); err != nil {
		return fmt.Errorf("error cleaning up ACM: %w", err)
	}
	return nil
}

func cleanupConstraints(settings *resource.Settings) error {
	repoPath := settings.RepoRootDir + "/../acm/policy-controller-constraint-library"
	cmds := []string{
		fmt.Sprintf("bash -c 'kubectl delete constraints --all || true'"),
		fmt.Sprintf("bash -c 'kubectl delete constrainttemplates --all || true'"),
		fmt.Sprintf("rm -rf %s", repoPath),
	}
	return exec.RunMultiple(cmds)
}

func cleanupACM(settings *resource.Settings) error {
	cs := kube.GKEClusterSpecFromContext(settings.KubeContexts[0])
	membership := cs.Name
	cmds := []string{
		fmt.Sprintf("gcloud beta container hub config-management unmanage --project=%s --membership=%s", cs.ProjectID, membership),
		"bash -c 'kubectl delete configmanagement --all || true'",
		"kubectl get ns",
		"bash -c 'kubectl delete ns config-management-system gatekeeper-system config-management-monitoring || true'",
		"bash -c 'kubectl delete crd configmanagements.configmanagement.gke.io || true'",
	}
	return exec.RunMultiple(cmds)
}
