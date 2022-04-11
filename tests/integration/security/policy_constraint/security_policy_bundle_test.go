//go:build integ
// +build integ

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

package policyconstaint

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/GoogleCloudPlatform/cloud-foundation-toolkit/infra/blueprint-test/pkg/utils"

	"istio.io/istio/pkg/config"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/shell"
)

const (
	retry              = 30
	intervalSecs       = 10
	consecutivePassReq = 5
)

var expectedExemptionsMap = buildViolationsExemptionsMap()

func TestSecurityPolicyBundle(t *testing.T) {
	framework.
		NewTest(t).
		Features("security.acm.policy-constraint").
		Run(func(ctx framework.TestContext) {
			ctx.Log("Policy Constraint Test!")
			applyResources(ctx)

			// get constraints
			violationMap := getConstraints(ctx)

			// retry until there are matching No. of violations consistently, otherwise fail
			violationsReady := false
			passCnt := 0

			for i := 0; i < retry; i++ {
				if reconcileViolationsToExemptionsMap(ctx, violationMap, expectedExemptionsMap) {
					passCnt++
					if passCnt == consecutivePassReq {
						violationsReady = true
						break
					}
					ctx.Logf("verify passed %d times consecutively, re-running in %d seconds...", passCnt)
				} else {
					passCnt = 0
					ctx.Logf("verify failed, re-trying in %d seconds...", intervalSecs)
				}
				time.Sleep(time.Duration(intervalSecs) * time.Second)
			}

			// log the final constraints details
			for constraint, violationMsgs := range violationMap {
				violationCount := len(violationMsgs)
				ctx.Log(fmt.Sprintf("Constraint: %s", constraint.String()))
				ctx.Log(fmt.Sprintf("Total violations: %d", violationCount))
				for _, msg := range violationMsgs {
					ctx.Log(msg)
				}
			}

			if !violationsReady {
				ctx.Fatalf("Violations are not populated within the retry times")
			}
		})
}

func applyResources(ctx framework.TestContext) {
	applyCmd := "kubectl apply -f ./testdata/"
	if _, err := shell.Execute(true, applyCmd); err != nil {
		ctx.Fatalf("failed to apply test data: %v", err)
	}
}

// getConstraints will initialize the violationMap by constraints ResID as the key
func getConstraints(ctx framework.TestContext) map[ResID][]string {
	getConstraintsCmd := "kubectl get constraints -o json"
	constaintsJSON, err := shell.Execute(true, getConstraintsCmd)
	if err != nil {
		ctx.Fatalf("failed to get Constraints: %v", err)
	}
	violationMap := make(map[ResID][]string)
	allConstraintItems := utils.ParseJSONResult(ctx, constaintsJSON).Get("items").Array()

	for _, item := range allConstraintItems {
		itemName := item.Get("metadata.name").String()

		gvk := config.GroupVersionKind{
			Group:   strings.Split(item.Get("apiVersion").String(), "/")[0],
			Version: strings.Split(item.Get("apiVersion").String(), "/")[1],
			Kind:    item.Get("kind").String(),
		}

		itemID := NewResID(gvk, itemName)
		violationMap[itemID] = make([]string, 0)
	}

	return violationMap
}

func getViolationsForConstraint(ctx framework.TestContext, resourceID ResID) []string {
	getConstraintCmd := fmt.Sprintf("kubectl get %s %s -o json", resourceID.Gvk().Kind, resourceID.Name())
	constraintJSON, err := shell.Execute(true, getConstraintCmd)
	if err != nil {
		ctx.Fatalf("failed to get Constraint: %v", err)
	}

	violationMsgs := make([]string, 0)
	constraintItem := utils.ParseJSONResult(ctx, constraintJSON)
	if constraintItem.Get("status.totalViolations").Exists() && constraintItem.Get("status.totalViolations").Int() != 0 {
		violationItems := constraintItem.Get("status.violations").Array()
		for _, violationItem := range violationItems {
			violationKind := violationItem.Get("kind")
			violationName := violationItem.Get("name")
			violationMessage := violationItem.Get("message")
			violationMsgs = append(violationMsgs, fmt.Sprintf("kind: %s | name: %s | message: %s", violationKind, violationName, violationMessage))
		}
	}

	return violationMsgs
}

// This builds a known exemptions map for violations that are excepted to occur
// when the test resources are applied
func buildViolationsExemptionsMap() map[string]int {
	// this exemption map keys off the Kind for the Constraint and the no. of violations
	// it is expected to have when the test is run
	exemptionsMap := make(map[string]int)

	exemptionsMap["AsmPeerAuthnMeshStrictMtls"] = 0
	exemptionsMap["AsmPeerAuthnStrictMtls"] = 2

	exemptionsMap["AsmAuthzPolicyDefaultDeny"] = 0
	exemptionsMap["AsmAuthzPolicySafePattern"] = 3
	exemptionsMap["AsmAuthzPolicyNormalization"] = 3

	// AsmIngressgatewayLabel has 3 violations are not intended but not avoidable
	// since the constraints check the proxy image from "gcr.io/gke-release/asm/proxyv2:"
	// but in the prow test, images are built runtime with "HUB=gcr.io/asm-boskos-XXX/asm/XXX".
	exemptionsMap["AsmIngressgatewayLabel"] = 4
	exemptionsMap["AsmSidecarInjection"] = 1

	return exemptionsMap
}

func reconcileViolationsToExemptionsMap(ctx framework.TestContext, violationMap map[ResID][]string, exemptionsMap map[string]int) bool {
	for constraint := range violationMap {
		violationMap[constraint] = getViolationsForConstraint(ctx, constraint)
	}
	for constraint, violationMsgs := range violationMap {
		violationExpect := exemptionsMap[constraint.Gvk().Kind]
		violationCount := len(violationMsgs)
		if violationCount != violationExpect {
			ctx.Logf("Violations for %s %s is %d, expected to be %d", constraint.Gvk().Kind, constraint.Name(), violationCount, violationExpect)
			return false
		}
	}
	return true
}
