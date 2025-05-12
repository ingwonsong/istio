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
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/go-multierror"

	"istio.io/istio/pkg/config/protocol"
	echotest "istio.io/istio/pkg/test/echo"
	"istio.io/istio/pkg/test/echo/common/scheme"
	"istio.io/istio/pkg/test/env"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/echo/check"
	"istio.io/istio/pkg/test/framework/components/echo/common/deployment"
	"istio.io/istio/pkg/test/framework/components/namespace"
	"istio.io/istio/pkg/test/framework/resource/config/apply"
	"istio.io/istio/pkg/test/kube"
	"istio.io/istio/pkg/test/scopes"
	"istio.io/istio/pkg/test/util/retry"
	"istio.io/istio/pkg/test/util/tmpl"
)

const (
	defaultTimeUnit                 string = "minute"
	defaultRateLimitRequestsPerUnit int    = 1

	rateLimitConfigMapName                                string = "ratelimit-config"
	configMapTmplFilePath                                 string = "testdata/global_rate_limit/configmap.yaml.tmpl"
	ratelimitServiceEnvoyfilterName                       string = "ratelimit-svc"
	ratelimitServiceEnvoyfilterTmplFilePath               string = "testdata/global_rate_limit/rate_limit_svc_config_envoyfilter.yaml.tmpl"
	rateLimitConfigEnvoyfilterName                        string = "ratelimit-config"
	rateLimitConfigEnvoyfilterTmplFilePath                string = "testdata/global_rate_limit/rate_limit_envoyfilter.yaml.tmpl"
	rateLimitSvcName                                      string = "ratelimit"
	headerToMetadataFilterForGlobalRateLimitName          string = "header-to-metadata-filter"
	headerToMetadataFilterForGlobalRateLimitTmplFilePaths string = "testdata/global_rate_limit/header_to_metadata_envoyfilter.yaml.tmpl"
	rateLimitSvcGrpcPort                                  string = "8081"
	rateLimitPerMinDescriptorVal                          string = "requests_per_minute"
)

type globalRateLimitValidation struct {
	isPositiveTest bool
	requestHeaders map[string][]string
}

func TestGlobalRateLimitEnvoyfilter(t *testing.T) {
	framework.NewTest(t).
		Run(func(t framework.TestContext) {
			clientNS, serverNs := clientAndServerEchoNs(t)
			aClientInstance := clientNS.A[0]
			aServerInstance := serverNs.A[0]
			ratelimitServerFQDN := fmt.Sprintf("%s.%s.svc.cluster.local", rateLimitSvcName, rateLimitNS.Name())
			newRateLimitServer(t, rateLimitNS)

			subtests := []struct {
				name                                     string
				rateLimitCmOverrideParams                map[string]any
				rateLimitSvcEnvoyfilterOverrideParams    map[string]any
				rateLimitConfigEnvoyfilterOverrideParams map[string]any
				headerToMetadataOverrideParams           map[string]any
				applyHeaderToMetadataFilter              bool
				validations                              []globalRateLimitValidation
			}{
				{
					// Test envoy grpc field with basic ratelimit on path
					name: "rateLimitService_gRpcService_envoyGrpc",
					rateLimitCmOverrideParams: map[string]any{
						"isEnvoyGrpc":          true,
						"isGoogleGrpc":         false,
						"clusterName":          ratelimitServerFQDN,
						"rateLimitSvcGrpcPort": rateLimitSvcGrpcPort,
						"authority":            ratelimitServerFQDN,
					},
					applyHeaderToMetadataFilter: false,
					validations: []globalRateLimitValidation{
						{
							isPositiveTest: true,
						},
					},
				},
				{
					// Test envoy grpc field with basic ratelimit on path
					name: "rateLimitService_gRpcService_googleGrpc",
					rateLimitSvcEnvoyfilterOverrideParams: map[string]any{
						"isEnvoyGrpc":          false,
						"isGoogleGrpc":         true,
						"targetUri":            ratelimitServerFQDN,
						"rateLimitSvcGrpcPort": rateLimitSvcGrpcPort,
						"statPrefix":           "rate_limit_callout",
					},
					applyHeaderToMetadataFilter: false,
					validations: []globalRateLimitValidation{
						{
							isPositiveTest: true,
						},
					},
				},
				{
					// Test headerValueMatch action field on a given path
					name:                        "action_headerValueMatch",
					applyHeaderToMetadataFilter: false,
					validations: []globalRateLimitValidation{
						{
							isPositiveTest: true,
						},
					},
				},
				{
					// Test metadata action field on for a given request metadata (populated using header-to-metadata envoyfilter)
					name: "action_metadata",
					rateLimitCmOverrideParams: map[string]any{
						"descriptorKey":   "appType",
						"descriptorValue": "backend",
					},
					rateLimitConfigEnvoyfilterOverrideParams: map[string]any{
						"headerValueMatchAction": false,
						"metadata_action":        true,
						"metadataDescriptorKey":  "appType",
						"metadataKeyNameSuffix":  "app",
						"metadataKeyPath":        "appType",
					},
					applyHeaderToMetadataFilter: true,
					validations: []globalRateLimitValidation{
						{
							isPositiveTest: true,
							requestHeaders: map[string][]string{
								"x-metadata-key": {"backend"},
							},
						},
						{
							isPositiveTest: false,
							requestHeaders: map[string][]string{
								"x-metadata-key": {"frontend"},
							},
						},
						{
							isPositiveTest: false,
						},
					},
				},
			}

			for _, test := range subtests {
				t.NewSubTest("TestGlobalRateLimit_" + test.name).Run(func(t framework.TestContext) {
					rateLimitParams := overrideParams(defaultRateLimitCmParams(rateLimitNS), test.rateLimitCmOverrideParams)
					ratelimitSvcEnvoyfilterParams := overrideParams(
						defaultRatelimitSvcEnvoyfilterParams(serverNs, ratelimitServerFQDN), test.rateLimitConfigEnvoyfilterOverrideParams,
					)
					headerToMetadataParams := overrideParams(defaultHeaderToMetadataParams(serverNs), test.headerToMetadataOverrideParams)
					ratelimitConfigEnvoyfilterParams := overrideParams(defaultRatelimitConfigEnvoyfilterParams(serverNs), test.rateLimitConfigEnvoyfilterOverrideParams)

					configureRateLimitConfigmap(t, rateLimitParams)
					applyRateLimitEnvoyfilters(t, serverNs, ratelimitSvcEnvoyfilterParams, ratelimitConfigEnvoyfilterParams)
					if test.applyHeaderToMetadataFilter {
						applyEnvoyFilter(
							t,
							headerToMetadataFilterForGlobalRateLimitName,
							headerToMetadataFilterForGlobalRateLimitTmplFilePaths,
							serverNs,
							headerToMetadataParams,
						)
					}
					for _, validation := range test.validations {
						// Cool down time to reset rate limit requests:
						time.Sleep(time.Duration(defaultRateLimitRequestsPerUnit)*time.Minute + 1)
						validateGlobalRateLimitResponse(
							t,
							aClientInstance,
							aServerInstance,
							validation.isPositiveTest,
							defaultRateLimitRequestsPerUnit,
							validation.requestHeaders,
						)
					}
				})
			}
		})
}

// This function creates  a basic configmap which limits requests for a given path and deploys a ratelimit deployment
func newRateLimitServer(t framework.TestContext, rateLimitNS namespace.Instance) error {
	t.Helper()
	start := time.Now()
	scopes.Framework.Info("=== BEGIN: Deploy ratelimit server ===")
	var err error
	defer func() {
		if err != nil {
			scopes.Framework.Error("=== FAILED: Deploy ratelimit server ===")
			scopes.Framework.Error(err)
		} else {
			scopes.Framework.Infof("=== SUCCEEDED: Deploy ratelimit server in %v ===", time.Since(start))
		}
	}()
	// Apply basic ratelimit configmap
	// This is required as ratelimit pod expect this configmap to be present to initialize
	configureRateLimitConfigmap(t, map[string]any{
		"namespace":                rateLimitNS.Name(),
		"descriptorKey":            "PATH",
		"descriptorValue":          rateLimitPerMinDescriptorVal,
		"rateLimitUnit":            defaultTimeUnit,
		"rateLimitRequestsPerUnit": strconv.Itoa(defaultRateLimitRequestsPerUnit),
	})
	// Deploy the ratelimit server.
	filePath := fmt.Sprintf("%s/samples/ratelimit/rate-limit-service.yaml", env.IstioSrc)
	rateLimitYAML, err := os.ReadFile(filePath)
	yamlText := string(rateLimitYAML)
	yamlText = strings.ReplaceAll(yamlText, "rateLimitNS", rateLimitNS.Name())
	if err = t.ConfigKube(t.Clusters()...).
		YAML(rateLimitNS.Name(), yamlText).
		Apply(apply.CleanupConditionally); err != nil {
		return err
	}

	// Wait for the endpoints to be ready.
	var g multierror.Group
	for _, c := range t.Clusters() {
		g.Go(func() error {
			_, _, err := kube.WaitUntilServiceEndpointsAreReady(c.Kube(), rateLimitNS.Name(), rateLimitSvcName)
			return err
		})
	}

	err = g.Wait().ErrorOrNil()
	if err != nil {
		return err
	}

	return nil
}

func configureRateLimitConfigmap(t framework.TestContext, params map[string]any) {
	t.Helper()
	configMapTmpl, err := os.ReadFile(configMapTmplFilePath)
	if err != nil {
		t.Fatalf("error while reading %v configmap template file: %v", rateLimitConfigMapName, err)
	}
	configmap, err := tmpl.Evaluate(string(configMapTmpl), params)
	if err != nil {
		t.Fatalf("error while evaluating %v configmap template file: %v", rateLimitConfigMapName, err)
	}
	t.ConfigIstio().YAML(rateLimitNS.Name(), configmap).ApplyOrFail(t, apply.Wait)
}

func defaultRateLimitCmParams(rateLimitNS namespace.Instance) map[string]any {
	return map[string]any{
		"namespace":                rateLimitNS.Name(),
		"descriptorKey":            "PATH",
		"descriptorValue":          rateLimitPerMinDescriptorVal,
		"rateLimitUnit":            defaultTimeUnit,
		"rateLimitRequestsPerUnit": strconv.Itoa(defaultRateLimitRequestsPerUnit),
	}
}

func defaultRatelimitSvcEnvoyfilterParams(serverNs deployment.EchoNamespace, ratelimitServerFQDN string) map[string]any {
	return map[string]any{
		"namespace":            serverNs.Namespace.Name(),
		"isEnvoyGrpc":          true,
		"isGoogleGrpc":         false,
		"clusterName":          ratelimitServerFQDN,
		"rateLimitSvcGrpcPort": rateLimitSvcGrpcPort,
		"authority":            ratelimitServerFQDN,
		"timeout":              "5s",
		"transportApiVersion":  "V3",
		"label":                labelAppA, // Assuming labelAppA is accessible or defined globally/passed in
	}
}

func defaultRatelimitConfigEnvoyfilterParams(serverNs deployment.EchoNamespace) map[string]any {
	return map[string]any{
		"namespace":                        serverNs.Namespace.Name(),
		"headerValueMatchAction":           true,
		"headerValueMatchDescriptorKey":    "PATH",
		"headerValueMatchDescriptorVal":    rateLimitPerMinDescriptorVal,
		"headerValueMatchHeaderName":       ":path",
		"headerValueMatchStringMatchExact": "/",
		"label":                            labelAppA, // Assuming labelAppA is accessible or defined globally/passed in
	}
}

func defaultHeaderToMetadataParams(serverNs deployment.EchoNamespace) map[string]any {
	return map[string]any{
		"namespace":      serverNs.Namespace.Name(),
		"headerName":     "x-metadata-key",
		"label":          labelAppA, // Assuming labelAppA is accessible or defined globally/passed in
		"nameSuffix":     "app",
		"headerKey":      "appType",
		"headerDatatype": "STRING",
	}
}

func applyRateLimitEnvoyfilters(
	t framework.TestContext,
	serverNs deployment.EchoNamespace,
	ratelimitSvcConfigParams map[string]any,
	rateLimitConfigParams map[string]any,
) {
	t.Helper()
	envoyFilterFiles := []struct {
		filePath string
		params   map[string]any
		name     string
	}{
		{
			filePath: ratelimitServiceEnvoyfilterTmplFilePath,
			params:   ratelimitSvcConfigParams,
			name:     ratelimitServiceEnvoyfilterName,
		},
		{
			filePath: rateLimitConfigEnvoyfilterTmplFilePath,
			params:   rateLimitConfigParams,
			name:     rateLimitConfigEnvoyfilterName,
		},
	}
	for _, envoyfilter := range envoyFilterFiles {
		applyEnvoyFilter(t, envoyfilter.name, envoyfilter.filePath, serverNs, envoyfilter.params)
	}
}

func validateGlobalRateLimitResponse(
	t framework.TestContext,
	client echo.Instance,
	server echo.Instance,
	isPositiveTest bool,
	rateLimitCount int,
	headers map[string][]string,
) {
	t.Helper()
	var responses echotest.Responses
	callOptions := echo.CallOptions{
		To: server,
		Port: echo.Port{
			Name:     "http",
			Protocol: protocol.HTTP,
		},
		Count:  1,
		Scheme: scheme.HTTP,
		HTTP: echo.HTTP{
			Path: "/",
		},
		Retry: echo.Retry{
			NoRetry: false,
			Options: []retry.Option{
				retry.Timeout(echoCallRetryTimeout),
				retry.MaxAttempts(echoCallRetryMaxAttempts),
				retry.Delay(echoCallRetryDelay),
			},
		},
		Timeout: echoCallTimeout,
		Check: check.Or(
			check.OK(),
			check.Status(http.StatusTooManyRequests),
		),
	}
	if len(headers) > 0 {
		callOptions.HTTP.Headers = headers
	}
	for i := 0; i < (rateLimitCount + 1); i++ {
		res, err := client.Call(callOptions)
		if err != nil {
			t.Fatalf("Error calling echoserver: %v", err)
		}
		responses = append(responses, res.Responses...)
	}
	okCount, tooManyRequestsCount := 0, 0
	for _, res := range responses {
		if res.Code == strconv.Itoa(http.StatusOK) {
			okCount++
		}
		if res.Code == strconv.Itoa(http.StatusTooManyRequests) {
			tooManyRequestsCount++
		}
	}
	if isPositiveTest && tooManyRequestsCount == 0 {
		t.Fatalf("Rate limit NOT enforced as expected")
	}
	if !isPositiveTest && tooManyRequestsCount > 0 {
		t.Fatalf("NOT expected rate limit enforced")
	}
}
