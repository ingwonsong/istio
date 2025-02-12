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
	"context"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"

	hogpb "istio.io/istio/pkg/asm/mcphttpovergrpc/proto/v1"
)

type httpRequest struct {
	Method string
	Header http.Header
	URL    *url.URL
	Body   []byte
}

func convertToInternalRequest(t *testing.T, req *http.Request) *httpRequest {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("failed to read request body")
	}
	defer req.Body.Close()
	return &httpRequest{
		Method: req.Method,
		Header: req.Header,
		URL:    req.URL,
		Body:   body,
	}
}

type httpResponse struct {
	statusCode int
	header     http.Header
	body       []byte
}

func (resp *httpResponse) writeTo(w http.ResponseWriter) {
	for k, v := range resp.header {
		w.Header()[k] = v
	}
	w.WriteHeader(resp.statusCode)
	w.Write(resp.body)
}

func mustURLParse(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatalf("failed to parse URL: %s", s)
	}
	return u
}

func TestHTTPRequest(t *testing.T) {
	tests := []struct {
		name         string
		grpcReq      *hogpb.Request
		wantHTTPReq  *httpRequest
		httpResp     *httpResponse
		wantGRPCResp *hogpb.Response
	}{
		{
			name: "BaseCase",
			grpcReq: &hogpb.Request{
				Method: "POST",
				Headers: []*hogpb.Header{
					{Key: "test-Header", Values: []string{"test-Value1"}},
					{Key: "Test-Header", Values: []string{"Test-Value2"}},
					{Key: "lower-lower", Values: []string{"lower-lower-value"}},
					{Key: "Upper-Upper", Values: []string{"Upper-Upper-value"}},
					{Key: "multi-values", Values: []string{"v1", "v2"}},
				},
				Url:  "https://server-address/test?query=queryValue",
				Body: []byte("request-body"),
			},
			wantHTTPReq: &httpRequest{
				Method: "POST",
				Header: http.Header{
					"test-Header":  []string{"test-Value1"},
					"Test-Header":  []string{"Test-Value2"},
					"lower-lower":  []string{"lower-lower-value"},
					"Upper-Upper":  []string{"Upper-Upper-value"},
					"multi-values": []string{"v1", "v2"},
				},
				URL:  mustURLParse(t, "https://server-address/test?query=queryValue"),
				Body: []byte("request-body"),
			},
			httpResp: &httpResponse{
				statusCode: 200,
				header: http.Header{
					"resp-Header": []string{"response1"},
					"Resp-Header": []string{"Response2"},
				},
				body: []byte("response-body"),
			},
			wantGRPCResp: &hogpb.Response{
				Status: 200,
				Headers: []*hogpb.Header{
					{Key: "resp-Header", Values: []string{"response1"}},
					{Key: "Resp-Header", Values: []string{"Response2"}},
				},
				Body: []byte("response-body"),
			},
		},
		{
			name: "NilHeader",
			grpcReq: &hogpb.Request{
				Method:  "POST",
				Headers: nil,
				Url:     "https://server-address/test?query=queryValue",
				Body:    []byte("request-body"),
			},
			wantHTTPReq: &httpRequest{
				Method: "POST",
				Header: http.Header{},
				URL:    mustURLParse(t, "https://server-address/test?query=queryValue"),
				Body:   []byte("request-body"),
			},
			httpResp: &httpResponse{
				statusCode: 400,
				header:     http.Header{},
				body:       []byte("response-body"),
			},
			wantGRPCResp: &hogpb.Response{
				Status:  400,
				Headers: []*hogpb.Header{},
				Body:    []byte("response-body"),
			},
		},
		{
			name: "NilBody",
			grpcReq: &hogpb.Request{
				Method:  "PUT",
				Headers: nil,
				Url:     "https://server-address/test?query=queryValue",
				Body:    nil,
			},
			wantHTTPReq: &httpRequest{
				Method: "PUT",
				Header: http.Header{},
				URL:    mustURLParse(t, "https://server-address/test?query=queryValue"),
				Body:   []byte(""),
			},
			httpResp: &httpResponse{
				statusCode: 400,
				header:     nil,
				body:       nil,
			},
			wantGRPCResp: &hogpb.Response{
				Status:  400,
				Headers: nil,
				Body:    nil,
			},
		},
	}
	ctx := context.Background()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var handler http.HandlerFunc = func(w http.ResponseWriter, r *http.Request) {
				gotHTTPReq := convertToInternalRequest(t, r)
				if diff := cmp.Diff(tc.wantHTTPReq, gotHTTPReq); diff != "" {
					t.Errorf("Got unexpected HTTP request: (-want +got):\n %s", diff)
				}
				tc.httpResp.writeTo(w)
			}
			s := &Server{
				handler: handler,
			}
			gotGRPCResp, err := s.HTTPRequest(ctx, tc.grpcReq)
			if err != nil {
				t.Fatalf("unexpected error from HTTPRequest: %v", err)
			}
			opts := cmp.Options{
				protocmp.Transform(),
				protocmp.SortRepeated(func(x, y *hogpb.Header) bool {
					return x.Key < y.Key
				}),
			}
			if diff := cmp.Diff(tc.wantGRPCResp, gotGRPCResp, opts); diff != "" {
				t.Errorf("Got unexpected gRPC response: (-want +got):\n %s", diff)
			}
		})
	}
}

func TestResponseWriter(t *testing.T) {
	tests := []struct {
		name string
		run  func(*responseWriter)
		want *hogpb.Response
	}{
		{
			name: "basicflow",
			run: func(r *responseWriter) {
				r.Header()["hdr1"] = []string{"value1"}
				r.WriteHeader(400)
				r.Write([]byte("test-body"))
			},
			want: &hogpb.Response{
				Status: 400,
				Headers: []*hogpb.Header{
					{Key: "hdr1", Values: []string{"value1"}},
				},
				Body: []byte("test-body"),
			},
		},
		{
			name: "noWriteHeader",
			run: func(r *responseWriter) {
				r.Header()["hdr1"] = []string{"value1"}
				r.Write([]byte("test-body"))
			},
			want: &hogpb.Response{
				Status: 200,
				Headers: []*hogpb.Header{
					{Key: "hdr1", Values: []string{"value1"}},
				},
				Body: []byte("test-body"),
			},
		},
		{
			name: "changeHeaderBeforeWriteHeader",
			run: func(r *responseWriter) {
				r.Header()["hdr1"] = []string{"value1"}
				r.Header()["hdr1"] = []string{"value2"}
				r.WriteHeader(400)
				r.Write([]byte("test-body"))
			},
			want: &hogpb.Response{
				Status: 400,
				Headers: []*hogpb.Header{
					{Key: "hdr1", Values: []string{"value2"}},
				},
				Body: []byte("test-body"),
			},
		},
		{
			name: "changeHeaderAfterWriteHeader",
			run: func(r *responseWriter) {
				r.Header()["hdr1"] = []string{"value1"}
				r.WriteHeader(400)
				r.Header()["hdr1"] = []string{"value2"}
				r.Write([]byte("test-body"))
			},
			want: &hogpb.Response{
				Status: 400,
				Headers: []*hogpb.Header{
					{Key: "hdr1", Values: []string{"value1"}},
				},
				Body: []byte("test-body"),
			},
		},
		{
			name: "setHeaderTwoTimes",
			run: func(r *responseWriter) {
				r.Header()["hdr1"] = []string{"value1"}
				r.WriteHeader(400)
				r.WriteHeader(300)
				r.Write([]byte("test-body"))
			},
			want: &hogpb.Response{
				Status: 400,
				Headers: []*hogpb.Header{
					{Key: "hdr1", Values: []string{"value1"}},
				},
				Body: []byte("test-body"),
			},
		},
		{
			name: "noWriteNoWriteHeader",
			run: func(r *responseWriter) {
				r.Header()["hdr1"] = []string{"value1"}
			},
			want: &hogpb.Response{
				Status: 200,
				Headers: []*hogpb.Header{
					{Key: "hdr1", Values: []string{"value1"}},
				},
				Body: nil,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := newResponseWriter()
			tc.run(got)
			opts := cmp.Options{
				protocmp.Transform(),
				protocmp.SortRepeated(func(x, y *hogpb.Header) bool {
					return x.Key < y.Key
				}),
			}
			if diff := cmp.Diff(tc.want, got.response(), opts); diff != "" {
				t.Errorf("Got unexpected responseWriter (-want, +got):\n %s", diff)
			}
		})
	}
}
