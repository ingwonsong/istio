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

package kube

import (
	"fmt"
	"path/filepath"

	"istio.io/istio/prow/asm/tester/pkg/exec"
)

// Contexts returns the kubectl contexts name.
func Contexts(kubeconfigsStr string) ([]string, error) {
	kubeconfigs := filepath.SplitList(kubeconfigsStr)
	// Get all contexts of the clusters.
	var kubeContexts []string
	for _, kc := range kubeconfigs {
		var err error
		kubeContext, err := exec.RunWithOutput(`kubectl config view -o 'jsonpath={.contexts[0].name}' --kubeconfig=` + kc)
		if err != nil {
			return nil, fmt.Errorf("error getting the kubectl contexts: %w", err)
		}
		kubeContexts = append(kubeContexts, kubeContext)
	}
	return kubeContexts, nil
}
