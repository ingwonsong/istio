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
	"bufio"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/util/uuid"

	"istio.io/istio/pkg/config/protocol"
	"istio.io/istio/pkg/log"
	"istio.io/istio/pkg/test/echo/common/scheme"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/echo/check"
	"istio.io/istio/pkg/test/framework/components/echo/common/deployment"
	"istio.io/istio/pkg/test/util/retry"
)

const (
	extAuthzEnvoyfilterTemplFile                   string = "testdata/ext_authz/ext_authz_envoyfilter.yaml.tmpl"
	extAuthzEnvoyfilterName                        string = "ext-authz"
	extAuthzContainerName                          string = "ext-authz"
	headerToMetadataFilterNameForExtAuthzTest      string = "header-to-metadata-filter"
	headerToMetadataFilterTemplFileForExtAuthzTest string = "testdata/ext_authz/header_to_metadata_envoyfilter.yaml.tmpl"
	extAuthzPodLabel                               string = "app=ext-authz"
	contextMetadataNamespaceVal                    string = "envoy.filters.http.header-to-metadata.token"
	contextMetadataHeaderName                      string = "metadata"
	contextMetadataHeaderKey                       string = "authz"
	extAuthzGrpcPort                               string = "9000"
	extAuthzAllowHeader                            string = "allow"
	extAuthzDenyHeader                             string = "deny"
)

func TestExtAuthzEnvoyfilter(t *testing.T) {
	framework.NewTest(t).
		Run(func(t framework.TestContext) {
			clientNS, serverNs := clientAndServerEchoNs(t)
			aClientInstance := echoAInstanceFromNs(t, clientNS)
			aServerInstance := echoAInstanceFromNs(t, serverNs)
			authzServerFQDN := fmt.Sprintf("%s.%s.svc.cluster.local", extAuthzEnvoyfilterName, authzServer.Namespace().Name())

			subtests := []struct {
				name                         string
				overrideParams               map[string]any
				headerToMetadataFilterParams map[string]any
				validationFn                 func(t framework.TestContext)
			}{
				{
					name: "envoyGrpc",
					overrideParams: map[string]any{
						"isEnvoyGrpc":      true,
						"isGoogleGrpc":     false,
						"clusterName":      authzServerFQDN,
						"extAuthzGrpcPort": extAuthzGrpcPort,
						"authority":        authzServerFQDN,
					},
					validationFn: func(t framework.TestContext) {
						validateExtAuthZResponse(t, aClientInstance, aServerInstance, extAuthzAllowHeader, http.Header{}, http.StatusOK)
						validateExtAuthZResponse(t, aClientInstance, aServerInstance, extAuthzDenyHeader, http.Header{}, http.StatusForbidden)
					},
				},
				{
					name: "googleGrpc",
					overrideParams: map[string]any{
						"isEnvoyGrpc":      false,
						"isGoogleGrpc":     true,
						"targetUri":        authzServerFQDN,
						"extAuthzGrpcPort": extAuthzGrpcPort,
						"statPrefix":       "ext_authz_callout",
					},
					validationFn: func(t framework.TestContext) {
						validateExtAuthZResponse(t, aClientInstance, aServerInstance, extAuthzAllowHeader, http.Header{}, http.StatusOK)
						validateExtAuthZResponse(t, aClientInstance, aServerInstance, extAuthzDenyHeader, http.Header{}, http.StatusForbidden)
					},
				},
				{
					name: "metadataContextNamespaces",
					overrideParams: map[string]any{
						"metadataContextNamespaces": contextMetadataNamespaceVal,
					},
					headerToMetadataFilterParams: map[string]any{
						"namespace":      serverNs.Namespace.Name(),
						"label":          labelAppA,
						"nameSuffix":     "token",
						"headerName":     contextMetadataHeaderName,
						"headerKey":      contextMetadataHeaderKey,
						"headerDatatype": "STRING",
					},
					validationFn: func(t framework.TestContext) {
						validateContextMetadataNamespace(t, aClientInstance, aServerInstance)
					},
				},
			}

			for _, test := range subtests {
				t.NewSubTest("TestExtAuthz_" + test.name).Run(func(t framework.TestContext) {
					params := defaultExtAuthzFilterTemplateParams(serverNs, authzServerFQDN)
					for k, v := range test.overrideParams {
						params[k] = v
					}
					if len(test.headerToMetadataFilterParams) > 0 {
						applyEnvoyFilter(t,
							headerToMetadataFilterNameForExtAuthzTest,
							headerToMetadataFilterTemplFileForExtAuthzTest,
							serverNs,
							test.headerToMetadataFilterParams)
					}
					applyEnvoyFilter(t, extAuthzEnvoyfilterName, extAuthzEnvoyfilterTemplFile, serverNs, params)
					test.validationFn(t)
				})
			}
		})
}

func validateContextMetadataNamespace(t framework.TestContext, aClientInstance echo.Instance, aServerInstance echo.Instance) {
	t.Helper()
	attempt := 0
	startTime := time.Now()
	retry.UntilSuccessOrFail(t, func() error {
		attempt++
		if attempt == 1 {
			startTime = time.Now()
		}
		log.Infof("Validation attempt no. %d. Time since first attempt: %v\n", attempt, time.Since(startTime))
		uniqueID := string(uuid.NewUUID())
		validateExtAuthZResponse(t,
			aClientInstance,
			aServerInstance,
			extAuthzAllowHeader,
			map[string][]string{contextMetadataHeaderName: {"secret"}, "requestId": {uniqueID}},
			http.StatusOK)
		return validateMetadataContextLog(t, uniqueID)
	}, retry.Timeout(time.Second*3))
}

func validateMetadataContextLog(t framework.TestContext, uniqueID string) error {
	t.Helper()
	client := t.Clusters().Default()
	fetch, err := client.PodsForSelector(t.Context(), extAuthzNS.Name(), extAuthzPodLabel)
	if err != nil {
		return fmt.Errorf("Error fetching pods: %v", err)
	}
	if len(fetch.Items) == 0 {
		return fmt.Errorf("Ext-authz pod not found")
	}
	p := fetch.Items[0]
	logs, err := client.PodLogs(t.Context(), p.Name, p.Namespace, extAuthzContainerName, false)
	if err != nil {
		return fmt.Errorf("Error fetching pod logs: %v", err)
	}
	found := containsSubStrings(logs, []string{uniqueID, contextMetadataNamespaceVal})
	if !found {
		return fmt.Errorf("Metadata context log not found in ext-authz pod logs")
	}
	return nil
}

func containsSubStrings(multilineLog string, substrings []string) bool {
	if len(multilineLog) == 0 {
		return false
	}
	if len(substrings) == 0 {
		return false
	}

	scanner := bufio.NewScanner(strings.NewReader(multilineLog))
	for scanner.Scan() {
		line := scanner.Text()
		allFound := true
		for _, substring := range substrings {
			if !strings.Contains(line, substring) {
				allFound = false
				break
			}
		}
		if allFound {
			return true
		}
	}
	return false
}

func defaultExtAuthzFilterTemplateParams(serverNs deployment.EchoNamespace, authzServerFQDN string) map[string]any {
	return map[string]any{
		"envoyfilterName":           extAuthzEnvoyfilterName,
		"namespace":                 serverNs.Namespace.Name(),
		"isEnvoyGrpc":               true,
		"isGoogleGrpc":              false,
		"clusterName":               authzServerFQDN,
		"extAuthzGrpcPort":          extAuthzGrpcPort,
		"authority":                 authzServerFQDN,
		"timeout":                   "5s",
		"transportApiVersion":       "V3",
		"label":                     labelAppA,
		"metadataContextNamespaces": nil,
	}
}

func validateExtAuthZResponse(t framework.TestContext,
	client echo.Instance,
	server echo.Instance,
	extAuthzHeader string,
	additionalHeaders http.Header,
	checkStatus int,
) {
	t.Helper()
	callOptions := echo.CallOptions{
		To: server,
		Port: echo.Port{
			Name:     "http",
			Protocol: protocol.HTTP,
		},
		Scheme: scheme.HTTP,
		HTTP: echo.HTTP{
			Path: "/",
			Headers: http.Header{
				"x-ext-authz": {extAuthzHeader},
			},
		},
		Count:   1,
		Timeout: echoCallTimeout,
		Retry: echo.Retry{
			Options: []retry.Option{retry.Timeout(echoCallRetryTimeout), retry.MaxAttempts(echoCallRetryMaxAttempts), retry.Delay(echoCallRetryDelay)},
		},
		Check: check.Status(checkStatus),
	}
	for k, v := range additionalHeaders {
		callOptions.HTTP.Headers[k] = v
	}
	_, err := client.Call(callOptions)
	if err != nil {
		t.Fatalf("Error calling echoserver: %v", err)
	}
}
