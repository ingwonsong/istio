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

const (
	squidProxyNs = "squid-proxy-ns"
	squidCmName  = "squid-config"
)

// Setup runs the test setups for ca proxy tests.
func Setup(settings *resource.Settings) error {
	prefix := fmt.Sprintf("%s/caproxy", settings.ConfigDir)
	k8sFileName := fmt.Sprintf("%s/squid_k8s.yaml", prefix)
	proxyConfigFileName := fmt.Sprintf("%s/proxyConfig.yaml", prefix)

	for _, context := range settings.KubeContexts {
		cmds := []string{}
		// create squid proxy ns
		cmds = append(cmds, fmt.Sprintf("kubectl create ns %s --context %s", squidProxyNs, context))

		// create squid proxy config map
		cmFileName := fmt.Sprintf("%s/squid.conf", prefix)
		cmds = append(cmds, fmt.Sprintf("kubectl create cm %s --from-file=squid=%s -n %s --context %s",
			squidCmName, cmFileName, squidProxyNs, context))

		// create squid proxy deployment and service
		cmds = append(cmds, fmt.Sprintf("kubectl apply -f %s -n %s --context %s",
			k8sFileName, squidProxyNs, context))

		// TODO(shankgan): create kubernetes networking policy
		// Pending: policy needs to selectively be applied on those namespaces where workloads are deployed
		// create proxyConfig to inject proxy env
		cmds = append(cmds, fmt.Sprintf("kubectl apply -f %s -n istio-system --context %s",
			proxyConfigFileName, context))
		if err := exec.RunMultiple(cmds); err != nil {
			return err
		}

		cmds = []string{
			fmt.Sprintf("kubectl wait --for=condition=Ready --timeout=2m -n %s --all pod --context %s",
				squidProxyNs, context),
		}
		if err := exec.RunMultiple(cmds); err != nil {
			return err
		}
	}
	return nil
}
