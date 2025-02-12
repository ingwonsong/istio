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

package bootstrap

import (
	"context"
	"net/http"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"istio.io/istio/pkg/kube"
)

func SetKubeClient(c kube.Client) func(*Server) {
	return func(server *Server) {
		server.kubeClient = c
	}
}

const (
	mcpBackendHeaderName  = "x-internal-mcp-backend"
	mcpBackendHeaderValue = "istiod"
)

// TODO(igsong): Remove this function after migrating all clusters to use HTTPOverGRPC.
func mcpInjectResponseHeaders(w http.ResponseWriter) {
	w.Header().Set(mcpBackendHeaderName, mcpBackendHeaderValue)
}

var mcpMetadata = metadata.Pairs(mcpBackendHeaderName, mcpBackendHeaderValue)

func mcpUnaryInterceptorForInjectingResponseHeader() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		resp, err := handler(ctx, req)
		if err != nil {
			return nil, err
		}
		err = grpc.SetHeader(ctx, mcpMetadata)
		if err != nil {
			return nil, err
		}
		return resp, nil
	}
}

func mcpStreamInterceptorForInjectingResponseHeader() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		err := grpc.SetHeader(ss.Context(), mcpMetadata)
		if err != nil {
			return err
		}
		return handler(srv, ss)
	}
}
