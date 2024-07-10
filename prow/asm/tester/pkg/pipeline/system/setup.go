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

package system

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/hashicorp/go-multierror"
	"istio.io/istio/pkg/test/util/retry"
	"istio.io/istio/prow/asm/tester/pkg/exec"

	"istio.io/istio/prow/asm/tester/pkg/install"
	"istio.io/istio/prow/asm/tester/pkg/kube"
	"istio.io/istio/prow/asm/tester/pkg/resource"
)

var (
	systemLogDir     = filepath.Join(os.Getenv("ARTIFACTS"), "system-pod-logs")
	systemNamespaces = map[string]string{
		"istio-system": "",
		"asm-system":   "",
		"kube-system":  "k8s-app=istio-cni-node",
	}
)

func Setup(settings *resource.Settings) error {
	log.Println("🎬 start installing ASM control plane...")

	err := install.Install(settings)
	if err != nil {
		// Export the Pod logs if the tests are run on CI.
		if os.Getenv("CI") == "true" {
			if len(settings.ClusterProxy) != 0 {
				os.Setenv("HTTPS_PROXY", settings.ClusterProxy[0])
				defer os.Unsetenv("HTTPS_PROXY")
			}

			log.Printf("######### Print all env vars #########")
			for _, e := range os.Environ() {
				log.Println(e)
			}
			log.Printf("######### Done printing #########")
			for _, kubeconfig := range filepath.SplitList(settings.Kubeconfig) {
				for ns, selector := range systemNamespaces {
					exportLogErr := kube.ExportLogs(kubeconfig, ns, selector, systemLogDir)
					err = multierror.Append(err, exportLogErr)
				}
			}
			log.Printf("ERROR: system installation failed, logs can be found at %q", systemLogDir)
		}
	}

	// Check to see if there is an ingress gateway resource present, if not then don't kick in the polling logic
	if ingress, err1 := exec.RunWithOutput("kubectl get service istio-ingressgateway -n istio-system -o jsonpath='{.metadata.name}'"); ingress == "" || err1 != nil {
		return multierror.Flatten(err)
	}

	retry.UntilSuccess(func() error {
		ip, err1 := exec.RunWithOutput("kubectl get service istio-ingressgateway -n istio-system -o jsonpath='{.status.loadBalancer.ingress[0].ip}'")
		host, err2 := exec.RunWithOutput("kubectl get service istio-ingressgateway -n istio-system -o jsonpath='{.status.loadBalancer.ingress[0].hostname}'")
		if (err1 != nil || ip == "") && (err2 != nil || host == "") {
			log.Printf("ingress gateway not ready yet...")
			return fmt.Errorf("ingress gateway not ready yet...")
		}
		return nil
	}, retry.Timeout(20*time.Minute), retry.Delay(5*time.Second))

	return multierror.Flatten(err)
}
