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

package istioagent

import (
	"crypto/tls"

	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	gca "istio.io/istio/csm/caproviders/google"
	cas "istio.io/istio/csm/caproviders/google-cas"
	caclient "istio.io/istio/csm/credentials"
	"istio.io/istio/pkg/grpcproxy"
	"istio.io/istio/pkg/model"
	"istio.io/istio/pkg/security"
)

func createMeshCA(opts *security.Options, _ RootCertProvider) (security.Client, error) {
	// Use a plugin to an external CA - this has direct support for the K8S JWT token
	// This is only used if the proper env variables are injected - otherwise the existing Citadel or Istiod will be
	// used.
	return gca.NewGoogleCAClient(opts.CAEndpoint, opts.CAProxyURL, true, caclient.NewCATokenProvider(opts))
}

func createGoogleCAS(secOpts *security.Options, _ RootCertProvider) (security.Client, error) {
	// Use a plugin
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	model.EnforceGoCompliance(tlsConfig)
	opts := []option.ClientOption{}
	// Both options need not be provided. PerGRPCCredentials need to be removed after testing
	opts = append(opts,
		option.WithTokenSource(caclient.NewCATokenProvider(secOpts)),
		option.WithGRPCDialOption(grpc.WithPerRPCCredentials(caclient.NewCATokenProvider(secOpts))),
		option.WithGRPCDialOption(grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))),
	)
	if secOpts.CAProxyURL != "" {
		opts = append(opts, option.WithGRPCDialOption(grpcproxy.GetGrpcProxyDialerOption(secOpts.CAProxyURL)))
	}
	return cas.NewGoogleCASClient(secOpts.CAEndpoint, opts...)
}

func init() {
	providers[security.GoogleCAProvider] = createMeshCA
	providers[security.GoogleCASProvider] = createGoogleCAS
}
