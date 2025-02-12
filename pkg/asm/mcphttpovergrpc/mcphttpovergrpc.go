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

package mcphttpovergrpc

import (
	"bytes"
	"context"
	"net/http"

	"google.golang.org/grpc"

	"istio.io/istio/pkg/asm"
	hogpb "istio.io/istio/pkg/asm/mcphttpovergrpc/proto/v1"
)

type Server struct {
	handler http.Handler
	hogpb.UnimplementedHTTPOverGRPCServer
}

var _ hogpb.HTTPOverGRPCServer = &Server{}

// TODO(igsong): Remove Enabled later. We can use the one flag for determining if it is running on MCP or not.
var Enabled = asm.IsCloudRun()

func Initialize(handler http.Handler, rpcs *grpc.Server) {
	if !Enabled {
		return
	}
	hogpb.RegisterHTTPOverGRPCServer(rpcs, &Server{handler: handler})
}

func (s *Server) HTTPRequest(ctx context.Context, in *hogpb.Request) (*hogpb.Response, error) {
	b := bytes.NewReader(in.Body)
	req, err := http.NewRequestWithContext(ctx, in.Method, in.Url, b)
	if err != nil {
		return nil, err
	}
	req.Header = make(http.Header)
	for _, hdr := range in.Headers {
		req.Header[hdr.Key] = hdr.Values
	}
	w := newResponseWriter()

	s.handler.ServeHTTP(w, req)
	return w.response(), nil
}

type responseWriter struct {
	statusCode      int
	body            bytes.Buffer
	header          http.Header
	committedHeader []*hogpb.Header
}

func newResponseWriter() *responseWriter {
	return &responseWriter{
		header: make(http.Header),
	}
}

func (r *responseWriter) Header() http.Header {
	return r.header
}

func (r *responseWriter) Write(b []byte) (int, error) {
	r.WriteHeader(http.StatusOK)
	return r.body.Write(b)
}

func (r *responseWriter) WriteHeader(statusCode int) {
	if r.committedHeader != nil {
		// If the header was written ever, do not write it again.
		return
	}
	// To prevent the change after WriteHeader, store the current header in commitedHeader.
	hdrs := make([]*hogpb.Header, 0, len(r.committedHeader))
	for k, v := range r.header {
		hdrs = append(hdrs, &hogpb.Header{
			Key:    k,
			Values: v,
		})
	}
	r.committedHeader = hdrs
	r.statusCode = statusCode
}

func (r *responseWriter) response() *hogpb.Response {
	r.WriteHeader(http.StatusOK)
	return &hogpb.Response{
		Status:  int32(r.statusCode),
		Headers: r.committedHeader,
		Body:    r.body.Bytes(),
	}
}
