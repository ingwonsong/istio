// Copyright Istio Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package bootstrap provides a functionalities required in preparing envoy bootstrap file for ASM-MCP.
package bootstrap

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	core "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"

	"istio.io/istio/pkg/bootstrap/platform"
	istiokeepalive "istio.io/istio/pkg/keepalive"
	"istio.io/istio/pkg/log"
	"istio.io/istio/pkg/model"
	"istio.io/istio/pkg/util/protomarshal"
)

// GenerateLoadStatsConfig returns a json blob for configuring `load_stats_config` in the bootstrap json.
// All the config values required to fill out the load stats config are obtained from the given node.
//
// WARNING: currently BOOTSTRAP_XDS_AGENT should not be true if LRS is enabled, because currently `load_stats_config`
// is supported in Istio Agent side, not Istiod side.
func GenerateLoadStatsConfig(node *model.Node) (string, bool) {
	if en, ok := node.RawMetadata["ENABLE_MCP_LRS"].(string); !ok || !strings.EqualFold(en, "true") {
		return "", false
	}

	if strings.EqualFold(os.Getenv("BOOTSTRAP_XDS_AGENT"), "true") {
		log.Error("BOOTSTRAP_XDS_AGENT and ENABLE_MCP_LRS are enabled together, but it is not supported now")
		return "", false
	}

	stsPort, err := strconv.Atoi(node.Metadata.StsPort)
	if err != nil || stsPort < 1 {
		log.Warnf("Invalid STS Port (%q) was given, so LRS will be disabled.")
		return "", false
	}

	// Always report the load stats with the project ID which the agent is running on.
	projectID := platform.NewGCP().Metadata()[platform.GCPProject]
	if projectID == "" {
		log.Warn("Failed to get Project ID, so LRS will be disabled.")
		return "", false
	}

	// Use the default trust cert bundle.
	sslCredential := &core.GrpcService_GoogleGrpc_SslCredentials{}
	if rootCertPath := os.Getenv("TEST_ONLY_MCP_LRS_ROOT_CERT_PATH"); rootCertPath != "" {
		// This root cert overriding should be used in testing only.
		sslCredential.RootCerts = &core.DataSource{
			Specifier: &core.DataSource_Filename{
				Filename: rootCertPath,
			},
		}
	}

	// Use the same address with ADS, which means Thetis.
	lrsAddress := node.Metadata.ProxyConfig.DiscoveryAddress
	if addr := os.Getenv("TEST_ONLY_MCP_LRS_ADDRESS"); addr != "" {
		// This should be used in testing only.
		lrsAddress = addr
	}

	intValue := func(val int64) *core.GrpcService_GoogleGrpc_ChannelArgs_Value {
		return &core.GrpcService_GoogleGrpc_ChannelArgs_Value{ValueSpecifier: &core.GrpcService_GoogleGrpc_ChannelArgs_Value_IntValue{IntValue: val}}
	}

	lrsConfig := &core.ApiConfigSource{
		ApiType:             core.ApiConfigSource_GRPC,
		TransportApiVersion: core.ApiVersion_V3,
		GrpcServices: []*core.GrpcService{
			{
				TargetSpecifier: &core.GrpcService_GoogleGrpc_{
					GoogleGrpc: &core.GrpcService_GoogleGrpc{
						TargetUri: lrsAddress,
						// Here, we used the same stat prefix with the grpc_envoy_bootstrap.json.
						StatPrefix: "googlegrpcxds",
						ChannelCredentials: &core.GrpcService_GoogleGrpc_ChannelCredentials{
							CredentialSpecifier: &core.GrpcService_GoogleGrpc_ChannelCredentials_SslCredentials{
								SslCredentials: sslCredential,
							},
						},
						CallCredentials: []*core.GrpcService_GoogleGrpc_CallCredentials{
							{
								CredentialSpecifier: &core.GrpcService_GoogleGrpc_CallCredentials_StsService_{
									StsService: &core.GrpcService_GoogleGrpc_CallCredentials_StsService{
										TokenExchangeServiceUri: fmt.Sprintf("http://localhost:%d/token", stsPort),
										SubjectTokenPath:        "./var/run/secrets/tokens/istio-token",
										SubjectTokenType:        "urn:ietf:params:oauth:token-type:jwt",
										Scope:                   "https://www.googleapis.com/auth/cloud-platform",
									},
								},
							},
						},
						ChannelArgs: &core.GrpcService_GoogleGrpc_ChannelArgs{
							Args: map[string]*core.GrpcService_GoogleGrpc_ChannelArgs_Value{
								"grpc.http2.max_pings_without_data": intValue(0),
								"grpc.keepalive_time_ms":            intValue(istiokeepalive.DefaultOption().Time.Milliseconds()),
								"grpc.keepalive_timeout_ms":         intValue(istiokeepalive.DefaultOption().Timeout.Milliseconds()),
							},
						},
					},
				},
				InitialMetadata: []*core.HeaderValue{
					{
						Key:   "x-goog-user-project",
						Value: projectID,
					},
				},
			},
		},
	}

	jsonBlob, err := protomarshal.ToJSON(lrsConfig)
	if err != nil {
		log.Warnf("JSON marshaling is failed, so LRS will be disabled: %v", err)
		return "", false
	}
	log.Info("LRS for MCP is enabled")
	return jsonBlob, true
}
