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

package grpcecho

import (
	"path/filepath"
	"testing"

	"istio.io/istio/pkg/test/env"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/cloudesf/testflow"
	"istio.io/istio/pkg/test/framework/components/istio"
	"istio.io/istio/pkg/test/framework/resource"
)

var (
	i                        istio.Instance
	grpcEchoTestConfigFolder = filepath.Join(env.IstioSrc, "cloudesf/testconfigs/grpcecho")
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

func TestCloudESFGrpcEcho(t *testing.T) {
	framework.
		NewTest(t).
		Features("cloudesf.grpcecho").
		Run(
			testflow.GenTestFlow(
				i,
				[]string{
					grpcEchoTestConfigFolder + "/grpc_echo_asm_e2e_config_envoyfilter.json",
					grpcEchoTestConfigFolder + "/grpc_echo_asm_e2e_config_gateway.json",
					grpcEchoTestConfigFolder + "/grpc_echo_asm_e2e_config_virtual_service.json",
					grpcEchoTestConfigFolder + "/asm_backend.yaml",
				},
				"gcr.io/cloudesf-testing/grpc_echo_asm_e2e_config_ic_image",
				"",
				"us.gcr.io/cloudesf-testing/e2e_grpc_echo_test_client",
				"",
			))
}
