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
	"log"
	"strings"
	"time"

	"istio.io/istio/prow/asm/tester/pkg/exec"
	"istio.io/istio/prow/asm/tester/pkg/kube"
	"istio.io/istio/prow/asm/tester/pkg/resource"
)

// Policy Constraints test is only on single cluster

// Setup runs the test setups for policy controller tests.
func Setup(settings *resource.Settings) error {
	clonePolicyControllerRepo(settings)
	installPolicyController(settings)
	deployConstraintTemplatesAndBundle(settings)
	return nil
}

func clonePolicyControllerRepo(settings *resource.Settings) error {
	userName := "ci-robot"
	userEmail := "ci-robot@k8s.io"
	httpCookieFile := "/secrets/cookiefile/cookies"
	repoUrl := "https://team-review.googlesource.com/nomos-team/policy-controller-constraint-library"
	repoPath := settings.RepoRootDir + "/../acm/policy-controller-constraint-library"

	log.Println("Cloning ACM Policy Controller Girrit Repository...")
	cmds := []string{
		fmt.Sprintf("git config --global user.name %s", userName),
		fmt.Sprintf("git config --global user.email %s", userEmail),
		fmt.Sprintf("git config --global http.cookiefile %s", httpCookieFile),
		fmt.Sprintf("git clone %s --single-branch --branch master %s", repoUrl, repoPath),
		fmt.Sprintf("ls -lrt %s", repoPath),
	}
	if err := exec.RunMultiple(cmds); err != nil {
		return err
	}

	return nil
}

func installPolicyController(settings *resource.Settings) error {
	log.Println("Installing Policy Controller...")
	cs := kube.GKEClusterSpecFromContext(settings.KubeContexts[0])
	membership := cs.Name
	manifestPath := settings.ConfigDir + "/policy-constraint/acm-manifest.yaml"

	cmds := []string{
		fmt.Sprintf("gcloud config list"),
		fmt.Sprintf("gcloud services enable krmapihosting.googleapis.com container.googleapis.com cloudresourcemanager.googleapis.com anthos.googleapis.com anthosconfigmanagement.googleapis.com  --project %s", cs.ProjectID),
		fmt.Sprintf("gcloud beta container hub config-management enable --project %s", cs.ProjectID),
		fmt.Sprintf("gcloud beta container hub config-management apply --membership=%s --config=%s --project %s", membership, manifestPath, cs.ProjectID),
	}
	if err := exec.RunMultiple(cmds); err != nil {
		log.Print(err)
		return err
	}

	policyControllerInstalled := false
	reTry := 10
	for i := 0; i < reTry; i++ {
		status, err := exec.RunWithOutput(fmt.Sprintf(`bash -c "gcloud beta container hub config-management status --project=%s --format json"`, cs.ProjectID))
		if err != nil {
			return err
		}
		if strings.Contains(status, `"policy_controller_state": "INSTALLED"`) {
			policyControllerInstalled = true
			break
		}
		time.Sleep(60 * time.Second)
	}

	if !policyControllerInstalled {
		return fmt.Errorf("Failed to install Policy Controller")
	}

	// enable referential constraints
	gatekeeperConfigPath := settings.ConfigDir + "/policy-constraint/gatekeeper-config.yaml"
	cmds = []string{
		"kubectl get ns --show-labels",
		"kubectl get pods -A",
		fmt.Sprintf("kubectl apply -f %s", gatekeeperConfigPath),
	}
	if err := exec.RunMultiple(cmds); err != nil {
		log.Print(err)
		return err
	}

	return nil
}

func deployConstraintTemplatesAndBundle(settings *resource.Settings) error {
	log.Println("Installing Constraint Templates...")
	repoPath := settings.RepoRootDir + "/../acm/policy-controller-constraint-library"
	bundleName := "asm-policy-v0.0.1"

	err := exec.Dispatch(settings.RepoRootDir, "deploy_constraint_templates_and_bundle", []string{repoPath, bundleName})
	if err != nil {
		return err
	}

	return nil
}
