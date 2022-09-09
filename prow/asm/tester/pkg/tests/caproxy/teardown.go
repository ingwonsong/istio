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

package caproxy

import (
	"fmt"

	"istio.io/istio/prow/asm/tester/pkg/exec"
	"istio.io/istio/prow/asm/tester/pkg/resource"
)

// Teardown cleans up the test setups for usrauth tests.
func Teardown(settings *resource.Settings) error {

	for _, context := range settings.KubeContexts {
		// delete squid-proxy namespace that deletes all resources in the namespace
		cmds := []string{}
		cmds = append(cmds, fmt.Sprintf("kubectl delete ns %s --context %s",
			squidProxyNs, context))
		//TODO(shankgan): Tear down networking policy in workload namespaces
		// all other resources in istio-system namespace
		if err := exec.RunMultiple(cmds); err != nil {
			return err
		}
	}
	return nil
}
