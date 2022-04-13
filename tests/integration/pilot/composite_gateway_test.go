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

package pilot

import (
	"context"
	"fmt"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	"istio.io/istio/pilot/pkg/model/kstatus"
	"istio.io/istio/pkg/test/echo/check"
	"istio.io/istio/pkg/test/echo/common/scheme"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/label"
	"istio.io/istio/pkg/test/framework/resource"
	"istio.io/istio/pkg/test/util/retry"
)

func TestCompositeGateway(t *testing.T) {
	framework.
		NewTest(t).
		Label(label.CompositeGateway).
		Features("traffic.ingress.gateway").
		Run(func(t framework.TestContext) {
			// TODO(b/223442146): GKE Gateway Controller requires both v1alpha1 and v1alpha2
			// CRD in order to use v1alpha2 Gateway. In the future, Gateway CRDs will be automatically
			// installed by GKE.
			if err := t.ConfigIstio().File("", "testdata/gateway-api-crd.yaml").Apply(resource.NoCleanup); err != nil && !apierrors.IsAlreadyExists(err) {
				t.Fatal(err)
			}
			if err := t.ConfigIstio().File("", "testdata/gateway-api-v1alpha1-crd.yaml").Apply(resource.NoCleanup); err != nil && !apierrors.IsAlreadyExists(err) {
				t.Fatal(err)
			}
			gwName := "composite-gateway"
			retry.UntilSuccessOrFail(t, func() error {
				err := t.ConfigIstio().YAML("", fmt.Sprintf(`
apiVersion: gateway.networking.k8s.io/v1alpha2
kind: GatewayClass
metadata:
  name: asm-l7-gxlb
spec:
  controllerName: mesh.cloud.google.com/gateway
---
apiVersion: gateway.networking.k8s.io/v1alpha2
kind: Gateway
metadata:
  name: %s
  namespace: istio-system
spec:
  gatewayClassName: asm-l7-gxlb
  listeners:
  - name: http
    port: 80
    protocol: HTTP
    allowedRoutes:
      namespaces:
        from: All
  - name: https
    port: 443
    protocol: HTTPS
    allowedRoutes:
      namespaces:
        from: All
    tls:
      mode: Terminate
      options:
        networking.gke.io/pre-shared-certs: self-signed-cert-for-test
---`, gwName)).Apply()
				return err
			}, retry.Delay(time.Second*10), retry.Timeout(time.Second*90))
			retry.UntilSuccessOrFail(t, func() error {
				err := t.ConfigIstio().YAML(apps.Namespace.Name(), `
apiVersion: gateway.networking.k8s.io/v1alpha2
kind: HTTPRoute
metadata:
  name: http
spec:
  parentRefs:
  - name: composite-gateway
    namespace: istio-system
  rules:
  - matches:
    - path:
        type: PathPrefix
        value: /
    backendRefs:
    - name: b
      port: 80
`).Apply()
				return err
			}, retry.Delay(time.Second*10), retry.Timeout(time.Second*90))
			gwClient := t.Clusters().Kube().Default().GatewayAPI().GatewayV1alpha2().Gateways("istio-system")
			t.NewSubTest("Istio").Run(func(t framework.TestContext) {
				t.NewSubTest("READY").Run(func(t framework.TestContext) {
					retry.UntilSuccessOrFail(t, func() error {
						gw, err := gwClient.Get(context.Background(), fmt.Sprintf("asm-gw-istio-%s", gwName), metav1.GetOptions{})
						if err != nil {
							return err
						}
						if s := kstatus.GetCondition(gw.Status.Conditions, string(gatewayv1alpha2.GatewayConditionReady)).Status; s != metav1.ConditionTrue {
							return fmt.Errorf("expected Istio Gateway status %q, got %q", metav1.ConditionTrue, s)
						}
						return nil
					}, retry.Delay(5*time.Second), retry.Timeout(10*time.Minute))
				})
				t.NewSubTest("HTTP").Run(func(t framework.TestContext) {
					apps.B[0].CallOrFail(t, echo.CallOptions{
						Port:    echo.Port{ServicePort: 80},
						Scheme:  scheme.HTTP,
						Address: fmt.Sprintf("asm-gw-istio-%s.%s.svc.cluster.local", gwName, "istio-system"),
						Check:   check.OK(),
						Retry: echo.Retry{
							Options: []retry.Option{retry.Timeout(time.Minute)},
						},
					})
				})
			})
			t.NewSubTest("GKE").Run(func(t framework.TestContext) {
				var gw *gatewayv1alpha2.Gateway
				t.NewSubTest("READY").Run(func(t framework.TestContext) {
					retry.UntilSuccessOrFail(t, func() error {
						var err error
						gw, err = gwClient.Get(context.Background(), fmt.Sprintf("asm-gw-gke-%s", gwName), metav1.GetOptions{})
						if err != nil {
							return err
						}
						if len(gw.Status.Addresses) == 0 {
							return fmt.Errorf("expected non-zero GKE Gateway address, got %q", len(gw.Status.Addresses))
						}
						return nil
					}, retry.Delay(5*time.Second), retry.Timeout(10*time.Minute))
				})
				t.NewSubTest("HTTP").Run(func(t framework.TestContext) {
					apps.B[0].CallOrFail(t, echo.CallOptions{
						Port:    echo.Port{ServicePort: 80},
						Scheme:  scheme.HTTP,
						Address: gw.Status.Addresses[0].Value,
						Check:   check.OK(),
						Retry: echo.Retry{
							Options: []retry.Option{retry.Timeout(time.Minute * 10)},
						},
					})
				})
				// HTTPS request may take a few minute to work.
				// Before that, client will receive "OpenSSL SSL_connect: SSL_ERROR_SYSCALL".
				t.NewSubTest("HTTPS").Run(func(t framework.TestContext) {
					apps.B[0].CallOrFail(t, echo.CallOptions{
						Port:   echo.Port{ServicePort: 443},
						Scheme: scheme.HTTPS,
						TLS: echo.TLS{
							InsecureSkipVerify: true,
						},
						Address: gw.Status.Addresses[0].Value,
						Check:   check.OK(),
						Retry: echo.Retry{
							Options: []retry.Option{retry.Timeout(time.Minute * 10)},
						},
					})
				})
			})
		})
}
