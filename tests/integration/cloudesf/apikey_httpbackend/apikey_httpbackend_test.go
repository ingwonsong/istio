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

package apikeyhttpbackend

import (
	"path/filepath"
	"testing"

	"istio.io/istio/pkg/test/env"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/cloudesf"
	"istio.io/istio/pkg/test/framework/cloudesf/testflow"
	"istio.io/istio/pkg/test/framework/components/istio"
	"istio.io/istio/pkg/test/framework/resource"
)

var (
	i                                 istio.Instance
	apiKeyHTTPBackendTestConfigFolder = filepath.Join(env.IstioSrc, "cloudesf/testconfigs/apikey_httpbackend")

	// This image wraps CloudESF test logic and its source is located at
	// https://source.corp.google.com/piper///depot/google3/apiserving/cloudesf/tests/e2e/cep/clients/BUILD;rcl=392444512;l=39
	cloudESFTestClientImage = "us.gcr.io/cloudesf-testing/e2e_apikey_grpc_test_client"
)

func TestMain(m *testing.M) {
	framework.
		NewSuite(m).
		Setup(istio.Setup(&i, func(r resource.Context, cfg *istio.Config) {
			cfg.PrimaryClusterIOPFile = "prow/asm/tester/configs/kpt-pkg/overlay/cloudesf-e2e.yaml"
			cfg.DeployEastWestGW = false
			cfg.Values["global.proxy.tracer"] = "none"
		})).
		Run()
}

func TestCloudESFApiKeyHttpBackend(t *testing.T) {
	framework.
		NewTest(t).
		Run(
			testflow.GenTestFlow(
				i,
				[]string{
					apiKeyHTTPBackendTestConfigFolder + "/apikey_grpc_asm_e2e_http_backend_config_envoyfilter.json",
					apiKeyHTTPBackendTestConfigFolder + "/apikey_grpc_asm_e2e_http_backend_config_gateway.json",
					apiKeyHTTPBackendTestConfigFolder + "/apikey_grpc_asm_e2e_http_backend_config_virtual_service.json",
					apiKeyHTTPBackendTestConfigFolder + "/apikey_grpc_asm_e2e_http_backend_config_service_entry.json",
					apiKeyHTTPBackendTestConfigFolder + "/apikey_grpc_asm_e2e_http_backend_config_custom_bootstrap.json",
				},
				"gcr.io/cloudesf-testing/apikey_grpc_asm_e2e_http_backend_config_ic_image",
				cloudESFTestClientImage,
				[]string{"/apikey_http_test"},
				[]string{
					"--skip_overrides_by_request_protocol_tests",
				},
				"us.gcr.io/cloudesf-testing/e2e_apikey_grpc_test_server:"+cloudesf.Version(),
			))
}
