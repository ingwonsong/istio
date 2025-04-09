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

package envoyfiltertest

import (
	"fmt"
	"net/http"
	"testing"

	"istio.io/istio/pkg/config/protocol"
	"istio.io/istio/pkg/test/echo/common/scheme"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/echo/check"
	"istio.io/istio/pkg/test/framework/components/echo/common/deployment"
	"istio.io/istio/pkg/test/util/retry"
)

const (
	extAuthzEnvoyfilterTemplFile = "testdata/ext_authz/ext_authz_envoyfilter.yaml.tmpl"
	extAuthzEnvoyfilterName      = "ext-authz"
)

func TestExtAuthzEnvoyfilter(t *testing.T) {
	framework.NewTest(t).
		Run(func(t framework.TestContext) {
			clientNS, serverNs := clientAndServerEchoNs(t)
			aClientInstance := echoAInstanceFromNs(t, clientNS)
			aServerInstance := echoAInstanceFromNs(t, serverNs)
			authzServerFQDN := fmt.Sprintf("%s.%s.svc.cluster.local", extAuthzEnvoyfilterName, authzServer.Namespace().Name())

			subtests := []struct {
				name           string
				overrideParams map[string]any
				validationFn   func(t framework.TestContext)
			}{
				{
					name: "envoyGrpc",
					overrideParams: map[string]any{
						"isEnvoyGrpc":      true,
						"isGoogleGrpc":     false,
						"clusterName":      authzServerFQDN,
						"extAuthzGrpcPort": "9000",
						"authority":        authzServerFQDN,
					},
					validationFn: func(t framework.TestContext) {
						validateExtAuthZResponse(t, aClientInstance, aServerInstance, "allow", http.StatusOK)
						validateExtAuthZResponse(t, aClientInstance, aServerInstance, "deny", http.StatusForbidden)
					},
				},
				{
					name: "googleGrpc",
					overrideParams: map[string]any{
						"isEnvoyGrpc":      false,
						"isGoogleGrpc":     true,
						"targetUri":        authzServerFQDN,
						"extAuthzGrpcPort": "9000",
						"statPrefix":       "ext_authz_callout",
					},
					validationFn: func(t framework.TestContext) {
						validateExtAuthZResponse(t, aClientInstance, aServerInstance, "allow", http.StatusOK)
						validateExtAuthZResponse(t, aClientInstance, aServerInstance, "deny", http.StatusForbidden)
					},
				},
			}

			for _, test := range subtests {
				t.NewSubTest("TestExtAuthz_" + test.name).Run(func(t framework.TestContext) {
					params := defaultExtAuthzFilterTemplateParams(serverNs)
					for k, v := range test.overrideParams {
						params[k] = v
					}
					applyEnvoyFilter(t, extAuthzEnvoyfilterName, extAuthzEnvoyfilterTemplFile, serverNs, params)
					test.validationFn(t)
				})
			}
		})
}

func defaultExtAuthzFilterTemplateParams(serverNs deployment.EchoNamespace) map[string]any {
	return map[string]any{
		"envoyfilterName":     extAuthzEnvoyfilterName,
		"namespace":           serverNs.Namespace.Name(),
		"isEnvoyGrpc":         true,
		"isGoogleGrpc":        false,
		"timeout":             "5s",
		"transportApiVersion": "V3",
		"label":               "app: a",
	}
}

func validateExtAuthZResponse(t framework.TestContext, client echo.Instance, server echo.Instance, extAuthzHeader string, checkStatus int) {
	t.Helper()
	_, err := client.Call(echo.CallOptions{
		To: server,
		Port: echo.Port{
			Name:     "http",
			Protocol: protocol.HTTP,
		},
		Scheme: scheme.HTTP,
		HTTP: echo.HTTP{
			Path: "/",
			Headers: map[string][]string{
				"x-ext-authz": {extAuthzHeader},
			},
		},
		Timeout: echoCallTimeout,
		Retry: echo.Retry{
			Options: []retry.Option{retry.Timeout(echoCallRetryTimeout), retry.MaxAttempts(echoCallRetryMaxAttempts), retry.Delay(echoCallRetryDelay)},
		},
		Check: check.Status(checkStatus),
	})
	if err != nil {
		t.Fatalf("Error calling echoserver: %v", err)
	}
}
