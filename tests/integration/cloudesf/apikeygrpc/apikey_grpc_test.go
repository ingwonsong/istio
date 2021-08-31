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

package apikeygrpc

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	kubeApiCore "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"istio.io/istio/pkg/test/env"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/istio"
	"istio.io/istio/pkg/test/framework/image"
	"istio.io/istio/pkg/test/framework/resource"
	"istio.io/istio/pkg/test/shell"
	"istio.io/istio/pkg/test/util/retry"
)

var (
	i istio.Instance

	cloudESFApiKeyGrpcTestFolder = filepath.Join(env.IstioSrc, "tests/integration/cloudesf/apikeygrpc")

	// The test client image has to run with certain IAM roles, in order to generate
	// access token by impersonating other identities.
	//
	//
	// The workload identity binding is precreated by
	//	gcloud iam service-accounts add-iam-policy-binding \
	//  --role roles/iam.workloadIdentityUser \
	//  --member "serviceAccount:cloudesf-testing.svc.id.goog[cloudesf-test-client-ns/cloudesf-test-client-ksa]" \
	//  cloudesf-asm-e2e-sa@cloudesf-testing.iam.gserviceaccount.com
	clientNamespace = "cloudesf-test-client-ns"
	clientKSA       = "cloudesf-test-client-ksa"
	clientGSA       = " iam.gke.io/gcp-service-account=cloudesf-asm-e2e-sa@cloudesf-testing.iam.gserviceaccount.com"
	clientPod       = "cloudesf-test-client-pod"
	clientContainer = "apikey-grpc-test-client"
	// This image wraps CloudESF test logic and its source is located at
	// https://source.corp.google.com/piper///depot/google3/apiserving/cloudesf/tests/e2e/cep/clients/BUILD;rcl=392444512;l=39
	cloudESFTestClientImage = "us.gcr.io/cloudesf-testing/e2e_apikey_grpc_test_client:cloudesf_e2eeac51e02"

	defaultHub = "gcr.io/cloudesf-testing/asm"
	defaultTag = "dev-stable"
)

func TestMain(m *testing.M) {
	framework.
		NewSuite(m).
		Setup(istio.Setup(&i, func(r resource.Context, cfg *istio.Config) {
			cfg.PrimaryClusterIOPFile = "prow/asm/tester/configs/kpt-pkg/overlay/cloudesf-e2e.yaml"
			cfg.Values["global.proxy.tracer"] = "none"
		})).
		Run()
}

func TestCloudESFApiKeyGrpc(t *testing.T) {
	framework.
		NewTest(t).
		Features("cloudesf.apikeygrpc").
		Run(func(t framework.TestContext) {
			// Patch Istio and set CloudESF as ingress gateway.
			// TODO(b/195717464): remove those kubectl patch after installing CloudESF as ingress gateway is compatible with ASM.
			//
			// This disabling tracer can be done with the latest istio using overlay but
			// the install_asm is not synced with that version yet, so for now, use kubectl
			// patch to workaround.
			executeShell(t, "disabling tracing zipkin",
				fmt.Sprintf(`kubectl patch configmap/istio -n istio-system --type merge --patch-file %s`, cloudESFApiKeyGrpcTestFolder+"/configs/asm/zipkin.yaml"))
			executeShell(t, "setting `readOnlyRootFilesystem: false`",
				fmt.Sprintf(`kubectl  patch deployment istio-ingressgateway -n istio-system --type strategic --patch-file %s`,
					cloudESFApiKeyGrpcTestFolder+"/configs/asm/read_only_root.yaml"))
			executeShell(t, "swapping ingress gateway",
				fmt.Sprintf("kubectl patch deployment istio-ingressgateway -n istio-system --type strategic --patch \"%s\"",
					proxyPatchConfig()))
			executeShell(t, "adding initContainer",
				fmt.Sprintf(`kubectl  patch deployment istio-ingressgateway -n istio-system --type merge --patch-file %s`,
					cloudESFApiKeyGrpcTestFolder+"/configs/asm/init_container.yaml"))

			// Deploy CloudESF config.
			configPaths := []string{
				cloudESFApiKeyGrpcTestFolder + "/configs/cloudesf/apikey_grpc_codelab_asm_envoyfilter.json",
				cloudESFApiKeyGrpcTestFolder + "/configs/cloudesf/apikey_grpc_codelab_asm_gateway.json",
				cloudESFApiKeyGrpcTestFolder + "/configs/cloudesf/apikey_grpc_codelab_asm_virtual_service.json",
				cloudESFApiKeyGrpcTestFolder + "/configs/cloudesf/asm_backend.yaml",
			}
			for _, configPath := range configPaths {
				retry.UntilSuccessOrFail(t, func() error {
					if err := t.Clusters().Default().ApplyYAMLFiles("", configPath); err != nil {
						return fmt.Errorf("fail to deploy CloudESF config %s: %v", configPath, err)
					}
					return nil
				}, retry.Delay(5*time.Second), retry.Timeout(60*time.Second))
			}

			// Get the ingress address.
			address, _ := i.IngressFor(t.Clusters().Default()).HTTPAddress()
			t.Logf("The ingress address is: %v", address)

			// Wait for CloudESF to be healthy.
			//
			// The common ingress gateway healthcheck(:15021/healthz/ready) won't work
			// as the CloudESF's related filters may not be ready(returns 503).
			// Workaround by calling the path exposed by CloudESF's test services and
			// it should return 401 as expected.
			// TODO(b/197691552): ASM CloudESF is healthy while the CloudESF exposed paths return 503
			healthCheck(t, fmt.Sprintf("http://%s:80/v1/projects/random-project/apiKeys", address), 401)

			// Create test client namespace.
			defer func() {
				t.Clusters().Default().CoreV1().Namespaces().Delete(context.TODO(), clientNamespace, metav1.DeleteOptions{})
			}()
			t.Clusters().Default().CoreV1().Namespaces().Create(context.TODO(), &kubeApiCore.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: clientNamespace},
			}, metav1.CreateOptions{})

			// Create KSA inside the cluster and bind it with the precreated identity.
			executeShell(t, "create KSA",
				fmt.Sprintf(`kubectl create serviceaccount --namespace %s %s`, clientNamespace, clientKSA))
			executeShell(t, "annotate KSA",
				fmt.Sprintf(`kubectl annotate serviceaccount --namespace %s %s %s`, clientNamespace, clientKSA, clientGSA))

			// Start the test client container.
			yamlConfig := fmt.Sprintf(`
apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: %s
spec:
  restartPolicy: Never
  serviceAccountName: %s
  containers:
  - image: %s
    name: %s
    args: ["-host=%s:80", "--only_validate_resp_error_code"]
`, clientPod, clientNamespace, clientKSA, cloudESFTestClientImage, clientContainer, address)
			t.Logf("test client config:\n%s", yamlConfig)
			if err := t.Config().ApplyYAML(clientNamespace, yamlConfig); err != nil {
				t.Fatalf("Fail to run the test client: %v", err)
			}

			// Wait the test client container finish.
			retry.UntilSuccessOrFail(t, func() error {
				pod, err := t.Clusters().Default().Kube().CoreV1().Pods(clientNamespace).Get(context.TODO(), clientPod, metav1.GetOptions{})
				if err != nil {
					t.Fatalf("Fail to get the test client pod: %v", err)
				}
				t.Logf("Get status: %v", pod.Status.Phase)
				switch pod.Status.Phase {
				case kubeApiCore.PodSucceeded:
					return nil
				case kubeApiCore.PodFailed:
					failError := "Fail to run the test client container"
					if log, err := t.Clusters().Default().PodLogs(context.TODO(), clientPod, clientNamespace, clientContainer, false); err == nil {
						failError = fmt.Sprintf("%s with log:\n%s", failError, log)
					} else {
						t.Errorf("Fail to get test client container log: %v", err)
					}
					t.Fatal(failError)
				default:
				}
				return fmt.Errorf(" The test client container isn't completed yet")
			}, retry.Delay(3*time.Second), retry.Timeout(60*time.Second))
		})
}

func proxyPatchConfig() string {
	config := `
spec:
 template:
   spec:
     containers:
     - name: istio-proxy
       image: %s/cloudesf:%s`
	s, _ := image.SettingsFromCommandLine()
	hub := defaultHub
	if s.Hub != image.DefaultHub {
		hub = s.Hub
	}

	tag := defaultTag
	if s.Tag != image.DefaultTag {
		tag = s.Tag
	}

	return fmt.Sprintf(config, hub, tag)
}

func healthCheck(t framework.TestContext, address string, expectedStatusCode int) {
	retry.UntilSuccessOrFail(t, func() error {
		resp, err := http.Get(address)
		t.Logf("%vth health check on %s, got resp: %v, error: %v", i, address, resp, err)
		if resp != nil && resp.StatusCode == expectedStatusCode {
			t.Logf("Ingress gateway is healthy")
			return nil
		}
		return fmt.Errorf("ingress gateway is still unhealthy")
	}, retry.Delay(5*time.Second), retry.Timeout(60*time.Second))
}

func executeShell(t framework.TestContext, operation, cmd string) string {
	t.Logf("start %s", operation)
	t.Logf("cmd is:\n%s", cmd)
	var ret string
	var err error
	if ret, err = shell.Execute(true, cmd); err != nil {
		t.Fatalf("fail to %s, result: %s, err: %v", operation, ret, err)
	}

	t.Logf("succeed %s with result:\n%s", operation, ret)
	return ret
}
