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
)

// TODO we don't want the command to memorize the entire list of files
func gatewayDir(rev *revision.Config) (string, error) {
	outputDir, err := ASMOutputDir(rev)
	if err != nil {
		return "", err
	}
	return filepath.Join(outputDir, ingressSamples), nil
}

func (c *installer) installIngressGateway(settings *resource.Settings, rev *revision.Config, context, kubeconfig string, idx int) error {
	// TODO(samnaser) this prevents us from deploying ingresses for older versions. Long-term we should come up with
	// a better approach here.
	if rev != nil && rev.Version != "" {
		return nil
	}
	if len(c.settings.ClusterProxy) != 0 {
		os.Setenv("HTTPS_PROXY", settings.ClusterProxy[idx])
		os.Setenv("http_proxy", settings.ClusterProxy[idx])
		defer os.Unsetenv("HTTPS_PROXY")
		defer os.Unsetenv("http_proxy")
	}

	// TODO resource.Settings should have an easy way to fetch this for a given cluster
	var ctxFlags []string
	if context != "" {
		ctxFlags = append(ctxFlags, "--context", context)
	}
	if kubeconfig != "" {
		ctxFlags = append(ctxFlags, "--kubeconfig", kubeconfig)
	}

	if err := enableGatewayInjection(ctxFlags, rev); err != nil {
		return err
	}
	gatewayManifests, err := listIngressFiles(ctxFlags, rev)
	if err != nil {
		return err
	}

	applyArgs := append(ctxFlags, "-n", gatewayNamespace)
	applyArgs = append(applyArgs, strings.Split("-f "+strings.Join(gatewayManifests, " -f "), " ")...)
	if err := exec.Run("kubectl apply", exec.WithAdditionalArgs(applyArgs)); err != nil {
		return fmt.Errorf("error installing ingress gateway: %w", err)
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

// listIngressFiles gets either the directory containing the ingress manifests or
// if the ingress service account already exists, it gets all items in that directory except
// the service account (to avoid overwriting customized parts of the SA)
// TODO we should be able to just use some merge strategry to avoid overwriting
func listIngressFiles(kubectlFlags []string, rev *revision.Config) ([]string, error) {
	gwDir, err := gatewayDir(rev)
	if err != nil {
		return nil, fmt.Errorf("error retreiving gateway dir: %v", err)
	}
	gatewayManifests := []string{gwDir}
	if saExists, err := checkForIngressSA(kubectlFlags); err == nil && saExists {
		// relies on extglob to include all but the serviceaccount
		gatewayManifests = []string{}
		files, err := os.ReadDir(gwDir)
		if err != nil {
			return nil, err
		}
		// TODO maybe use `extglob` and `awk` to generate multiple `-f`?
		for _, f := range files {
			if strings.Contains(f.Name(), "serviceaccount") {
				continue
			}
			gatewayManifests = append(gatewayManifests, filepath.Join(gwDir, f.Name()))
		}
	} else if err != nil {
		return nil, err
	}
	return gatewayManifests, nil
}

// checkForIngressSA returns true if the istio-ingressgateway-service-account exists in the gatewayNamespace
func checkForIngressSA(kubectlFlags []string) (bool, error) {
	err := exec.Run(
		fmt.Sprintf("kubectl -n %s get sa %s", gatewayNamespace, ingressGatewayServiceAccount),
		exec.WithAdditionalArgs(kubectlFlags),
	)
	if err == nil {
		return true, nil
	}
	if strings.Contains(err.Error(), "NotFound") {
		return false, nil
	}
	return false, err
}
