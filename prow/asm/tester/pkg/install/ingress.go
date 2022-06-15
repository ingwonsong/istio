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
	"os"
	"path/filepath"
	"strings"

	"istio.io/istio/prow/asm/tester/pkg/exec"
	"istio.io/istio/prow/asm/tester/pkg/install/revision"
	"istio.io/istio/prow/asm/tester/pkg/resource"
)

const (
	gatewayNamespace             = "istio-system"
	ingressGatewayServiceAccount = "istio-ingressgateway"
	ingressSamples               = "/samples/gateways/istio-ingressgateway"
	egressGatewayServiceAccount  = "istio-egressgateway"
	egressSamples                = "/samples/gateways/istio-egressgateway"
)

type gatewaySA struct {
	ingressSA bool
	egressSA  bool
}

// TODO we don't want the command to memorize the entire list of files
func gatewayDir(rev *revision.Config, isIngress bool) (string, error) {
	outputDir, err := ASMOutputDir(rev)
	if err != nil {
		return "", err
	}
	if !isIngress {
		return filepath.Join(outputDir, egressSamples), nil
	}
	return filepath.Join(outputDir, ingressSamples), nil
}

func (c *installer) installGateways(settings *resource.Settings, rev *revision.Config, context, kubeconfig string, idx int) error {
	// enabling NetworkAttachmentDefinition to gatewayNamespace for openshift cluster
	if settings.ClusterType == resource.Openshift {
		yaml := filepath.Join(settings.ConfigDir, "openshift_ns_modification.yaml")
		cmd1 := fmt.Sprintf("kubectl apply -f %s -n %s", yaml, gatewayNamespace)
		if err := exec.Run(cmd1); err != nil {
			return fmt.Errorf("unable to enable NetworkAttachmentDefinition for gateway namespace: %w", err)
		}
	}

	// TODO(samnaser) this prevents us from deploying gateways for older versions. Long-term we should come up with
	// a better approach here.
	if rev != nil && rev.Version != "" && rev.Name != "" {
		return nil
	}
	if len(c.settings.ClusterProxy) != 0 && settings.ClusterProxy[idx] != "" {
		os.Setenv("HTTPS_PROXY", settings.ClusterProxy[idx])
		defer os.Unsetenv("HTTPS_PROXY")
	}

	// TODO resource.Settings should have an easy way to fetch this for a given cluster
	ctxFlags := getCtxFlags(context, kubeconfig)

	if err := enableGatewayInjection(ctxFlags, rev); err != nil {
		return err
	}
	gatewayManifests, err := listGatewayInstallationFiles(ctxFlags, rev)
	if err != nil {
		return err
	}

	applyArgs := append(ctxFlags, "-n", gatewayNamespace)
	applyArgs = append(applyArgs, strings.Split("-f "+strings.Join(gatewayManifests, " -f "), " ")...)
	if err := exec.Run("kubectl apply", exec.WithAdditionalArgs(applyArgs)); err != nil {
		return fmt.Errorf("error installing gateways: %w", err)
	}

	return nil
}

// enableGatewayInjection sets either istio-injection or istio.io/rev label on the gatewayNamespace to allow
// using gateway injection
func enableGatewayInjection(kubectlFlags []string, rev *revision.Config) error {
	var revision string
	if rev != nil {
		revision = rev.Name
	} else {
		// detect revision - used in enabling injection
		var err error
		revision, err = exec.RunWithOutput(
			"kubectl get deploy -n istio-system -l app=istiod -o jsonpath='{.items[*].metadata.labels.istio\\.io\\/rev}'",
			exec.WithAdditionalArgs(kubectlFlags),
		)
		if err != nil {
			return fmt.Errorf("error getting istiod revision: %w", err)
		}
	}

	// enable gateway injection
	var injectCmd string
	if revision != "" && revision != "default" {
		injectCmd = fmt.Sprintf("kubectl label namespace %s istio-injection- istio.io/rev=%s --overwrite", gatewayNamespace, revision)
	} else {
		injectCmd = fmt.Sprintf("kubectl label namespace %s istio-injection=enabled --overwrite", gatewayNamespace)
	}
	if err := exec.Run(injectCmd, exec.WithAdditionalArgs(kubectlFlags)); err != nil {
		return fmt.Errorf("error labeling gateway namespace: %w", err)
	}
	return nil
}

// listGatewayInstallationFiles gets all items in that directory except
// the service account (to avoid overwriting customized parts of the SA) is SA is already present
// TODO we should be able to just use some merge strategry to avoid overwriting
func listGatewayInstallationFiles(kubectlFlags []string, rev *revision.Config) ([]string, error) {
	saExists, err := checkForGatewaySA(kubectlFlags)
	gatewayManifests := []string{}
	if err != nil {
		return nil, err
	} else {
		ingressDir, err := gatewayDir(rev, true)
		if err != nil {
			return nil, fmt.Errorf("error retreiving ingress gateway dir: %v", err)
		}
		ingressFiles, err := os.ReadDir(ingressDir)
		if err != nil {
			return nil, err
		}
		for _, f := range ingressFiles {
			if strings.Contains(f.Name(), "serviceaccount") && saExists.ingressSA {
				continue
			}
			// TODO(iamwen) remove this part when we test on 1.23+ clusters
			if strings.Contains(f.Name(), "autoscalingv2") {
				continue
			}
			gatewayManifests = append(gatewayManifests, filepath.Join(ingressDir, f.Name()))
		}

		egressDir, err := gatewayDir(rev, false)
		if err != nil {
			return nil, fmt.Errorf("error retreiving egress gateway dir: %v", err)
		}
		egressFiles, err := os.ReadDir(egressDir)
		if err != nil {
			return nil, err
		}
		for _, f := range egressFiles {
			if strings.Contains(f.Name(), "serviceaccount") && saExists.egressSA {
				continue
			}
			// TODO(iamwen) remove this part when we test on 1.23+ clusters
			if strings.Contains(f.Name(), "autoscaling-v2") {
				continue
			}
			gatewayManifests = append(gatewayManifests, filepath.Join(egressDir, f.Name()))
		}
		return gatewayManifests, nil
	}
}

// ccheckForGatewaySA returns true if the serviceAccount exists in the gatewayNamespace
func checkForGatewaySA(kubectlFlags []string) (gatewaySA, error) {
	gatewaySA := gatewaySA{ingressSA: false, egressSA: false}
	err := exec.Run(
		fmt.Sprintf("kubectl -n %s get sa %s", gatewayNamespace, ingressGatewayServiceAccount),
		exec.WithAdditionalArgs(kubectlFlags),
	)
	if err == nil {
		gatewaySA.ingressSA = true
	} else if !strings.Contains(err.Error(), "NotFound") {
		return gatewaySA, err
	}
	err = exec.Run(
		fmt.Sprintf("kubectl -n %s get sa %s", gatewayNamespace, egressGatewayServiceAccount),
		exec.WithAdditionalArgs(kubectlFlags),
	)
	if err == nil {
		gatewaySA.egressSA = true
	} else if !strings.Contains(err.Error(), "NotFound") {
		return gatewaySA, err
	}
	return gatewaySA, nil
}

func getCtxFlags(context, kubeconfig string) []string {
	var ctxFlags []string
	if context != "" {
		ctxFlags = append(ctxFlags, "--context", context)
	}
	if kubeconfig != "" {
		ctxFlags = append(ctxFlags, "--kubeconfig", kubeconfig)
	}
	return ctxFlags
}
