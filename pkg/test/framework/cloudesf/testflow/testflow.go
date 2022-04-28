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

package testflow

import (
	"context"
	"fmt"
	"net/http"
	"time"

	kubeApiCore "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/cloudesf"
	"istio.io/istio/pkg/test/framework/components/istio"
	"istio.io/istio/pkg/test/framework/resource"
	"istio.io/istio/pkg/test/framework/resource/config/apply"
	"istio.io/istio/pkg/test/shell"
	"istio.io/istio/pkg/test/util/retry"
)

var (
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
	clientGSA       = "iam.gke.io/gcp-service-account=cloudesf-asm-e2e-sa@cloudesf-testing.iam.gserviceaccount.com"
	clientPod       = "cloudesf-test-client-pod"
	clientContainer = "cloudesf-test-client-container"

	// The gateway config is based on the sample template from
	// https://github.com/GoogleCloudPlatform/anthos-service-mesh-packages/tree/main/samples/gateways/istio-ingressgateway
	// plus Cloud ESF customization.
	// See https://cloud.google.com/service-mesh/docs/unified-install/install#install_gateways
	// for general documents for installing ASM gateways.
	gatewayTemplate = `
apiVersion: v1
kind: ServiceAccount
metadata:
  name: istio-ingressgateway-service-account
  namespace: istio-system
---
apiVersion: apps/v1
kind: Deployment
metadata:
  annotations:
    deployment.kubernetes.io/revision: "1"
  generation: 2
  labels:
    app: istio-ingressgateway
    istio: ingressgateway
    istio.io/rev: default
    release: istio
  name: istio-ingressgateway
  namespace: istio-system
spec:
  selector:
    matchLabels:
      app: istio-ingressgateway
      istio: ingressgateway
  strategy:
    rollingUpdate:
      maxSurge: 100%
      maxUnavailable: 25%
  template:
    metadata:
      annotations:
        inject.istio.io/templates: gateway
        sidecar.istio.io/logLevel: debug
      labels:
        app: istio-ingressgateway
        istio: ingressgateway
        istio.io/rev: default
    spec:
      affinity:
        nodeAffinity:
          preferredDuringSchedulingIgnoredDuringExecution:
          - preference:
              matchExpressions:
              - key: kubernetes.io/arch
                operator: In
                values:
                - amd64
            weight: 2
          - preference:
              matchExpressions:
              - key: kubernetes.io/arch
                operator: In
                values:
                - ppc64le
            weight: 2
          - preference:
              matchExpressions:
              - key: kubernetes.io/arch
                operator: In
                values:
                - s390x
            weight: 2
          requiredDuringSchedulingIgnoredDuringExecution:
            nodeSelectorTerms:
            - matchExpressions:
              - key: kubernetes.io/arch
                operator: In
                values:
                - amd64
                - ppc64le
                - s390x
      initContainers:
      - name: init-container
        image: {{ .initContainerImage }}
        volumeMounts:
        - mountPath: /etc/istio/proxy
          name: istio-envoy
      containers:
      - env:
        - name: ISTIO_META_UNPRIVILEGED_POD
          value: "true"
        - name: ISTIO_META_ROUTER_MODE
          value: standard
        - name: ENABLE_CLOUD_ESF
          value: "true"
        image: {{ .gatewayImage }}
        imagePullPolicy: Always
        name: istio-proxy
        ports:
        - containerPort: 15021
          protocol: TCP
        - containerPort: 8080
          protocol: TCP
        - containerPort: 8443
          protocol: TCP
        - containerPort: 15012
          protocol: TCP
        - containerPort: 15443
          protocol: TCP
        - containerPort: 15090
          name: http-envoy-prom
          protocol: TCP
        resources:
          limits:
            cpu: "2"
            memory: 1Gi
          requests:
            cpu: 100m
            memory: 128Mi
        securityContext:
          allowPrivilegeEscalation: false
          capabilities:
            drop:
            - ALL
          privileged: false
          # TODO(b/199536494): verify this is not needed after logtostderr is true.
          readOnlyRootFilesystem: false
        volumeMounts:
        - mountPath: /etc/istio/ingressgateway-certs
          name: ingressgateway-certs
          readOnly: true
        - mountPath: /etc/istio/ingressgateway-ca-certs
          name: ingressgateway-ca-certs
          readOnly: true
        - mountPath: /etc/istio/proxy
          name: istio-envoy
      securityContext:
        fsGroup: 1337
        runAsGroup: 1337
        runAsNonRoot: true
        runAsUser: 1337
      serviceAccount: istio-ingressgateway-service-account
      serviceAccountName: istio-ingressgateway-service-account
      volumes:
      - name: ingressgateway-certs
        secret:
          optional: true
          secretName: istio-ingressgateway-certs
      - name: ingressgateway-ca-certs
        secret:
          optional: true
          secretName: istio-ingressgateway-ca-certs
      - name: istio-envoy
        emptyDir: {}
---
apiVersion: policy/v1beta1
kind: PodDisruptionBudget
metadata:
  name: istio-ingressgateway
  namespace: istio-system
spec:
  minAvailable: 1
  selector:
    matchLabels:
      istio: ingressgateway
      app: istio-ingressgateway
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: istio-ingressgateway-sds
  namespace: istio-system
rules:
- apiGroups: [""]
  resources: ["secrets"]
  verbs: ["get", "watch", "list"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: istio-ingressgateway-sds
  namespace: istio-system
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: istio-ingressgateway-sds
subjects:
- kind: ServiceAccount
  name: istio-ingressgateway-service-account
---
apiVersion: autoscaling/v2beta1
kind: HorizontalPodAutoscaler
metadata:
  name: istio-ingressgateway
  namespace: istio-system
spec:
  maxReplicas: 1
  metrics:
  - resource:
      name: cpu
      targetAverageUtilization: 80
    type: Resource
  minReplicas: 1
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: istio-ingressgateway
---
apiVersion: v1
kind: Service
metadata:
  name: istio-ingressgateway
  namespace: istio-system
spec:
  ports:
  - name: status-port
    port: 15021
    protocol: TCP
    targetPort: 15021
  - name: http2
    port: 80
    protocol: TCP
    targetPort: 8080
  - name: https
    port: 443
    protocol: TCP
    targetPort: 8443
  - name: tcp-istiod
    port: 15012
    protocol: TCP
    targetPort: 15012
  - name: tls
    port: 15443
    protocol: TCP
    targetPort: 15443
  selector:
    istio: ingressgateway
    app: istio-ingressgateway
  type: LoadBalancer`
)

func GenTestFlow(i istio.Instance, cloudESFConfigs []string, initContainerImageAddr,
	healthCheckPath, testClientImageAddr string, testClientImageExtraArgs string) func(t framework.TestContext) {
	return func(t framework.TestContext) {
		// Deploy CloudESF config.
		for _, configPath := range cloudESFConfigs {
			retry.UntilSuccessOrFail(t, func() error {
				t.Logf("deploy config %s", configPath)
				if err := t.Clusters().Default().ApplyYAMLFiles("", configPath); err != nil {
					return fmt.Errorf("fail to deploy CloudESF config %s: %v", configPath, err)
				}
				return nil
			}, retry.Delay(5*time.Second), retry.Timeout(60*time.Second))
		}

		templateParams := map[string]string{
			"initContainerImage": initContainerImageAddr,
			"gatewayImage":       cloudEsfImage(),
		}

		t.Logf("Deploying Cloud ESF based ingress gateway.")
		t.ConfigKube().Eval("istio-system", templateParams, gatewayTemplate).ApplyOrFail(t, apply.Wait, apply.NoCleanup)

		// Get the ingress address.
		address, _ := i.CustomIngressFor(t.Clusters().Default(), "istio-ingressgateway", "ingressgateway").HTTPAddress()
		t.Logf("The ingress address is: %v", address)

		// Wait for CloudESF to be healthy.
		//
		// The common ingress gateway healthcheck(:15021/healthz/ready) won't work
		// as the CloudESF's related filters may not be ready(returns 503).
		// Workaround by calling the path exposed by CloudESF's test services and
		// it should return 401 as expected.
		// TODO(b/197691552): ASM CloudESF is healthy while the CloudESF exposed paths return 503
		if healthCheckPath != "" {
			// Expected status code is 400 because of missing consumer ID.
			healthCheck(t, i, fmt.Sprintf(healthCheckPath, address), 400)
		} else {
			// Just simply sleep instead of doing healthcheck on grpc-echo, otherwise we need to
			// introduce its grpc library from google3.
			time.Sleep(time.Second * 60)
		}

		// Create test client namespace.
		defer func() {
			_ = t.Clusters().Default().CoreV1().Namespaces().Delete(context.TODO(), clientNamespace, metav1.DeleteOptions{})
		}()

		if _, err := t.Clusters().Default().CoreV1().Namespaces().Create(context.TODO(), &kubeApiCore.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: clientNamespace},
		}, metav1.CreateOptions{}); err != nil {
			t.Fatalf("fail to create test client namespace(%s)  , err: %v", clientNamespace, err)
		}

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
  - image: %s:%s
    name: %s
    args: ["-host=%s:80", %s]
`, clientPod, clientNamespace, clientKSA, testClientImageAddr, cloudesf.Version(), clientContainer, address, testClientImageExtraArgs)
		t.Logf("test client config:\n%s", yamlConfig)
		t.ConfigKube().YAML(clientNamespace, yamlConfig).ApplyOrFail(t)

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
		}, retry.Delay(5*time.Second), retry.Timeout(600*time.Second))
	}
}

func cloudEsfImage() string {
	s, _ := resource.SettingsFromCommandLine("cloudesf")
	hub := "gcr.io/cloudesf-testing/asm"
	if s.Image.Hub != "gcr.io/istio-testing" {
		hub = s.Image.Hub
	}

	tag := "dev-stable"
	if s.Image.Tag != "latest" {
		tag = s.Image.Tag
	}

	return fmt.Sprintf("%s/cloudesf:%s", hub, tag)
}

func healthCheck(t framework.TestContext, i istio.Instance, address string, expectedStatusCode int) {
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
