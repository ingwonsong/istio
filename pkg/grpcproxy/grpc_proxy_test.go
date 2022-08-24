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

package grpcproxy

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"istio.io/pkg/log"
)

type proxyServer struct {
	t            *testing.T
	lis          net.Listener
	in           net.Conn
	out          net.Conn
	conRequest   atomic.Bool
	requestCheck func(*http.Request) error
}

func (p *proxyServer) run() {
	in, err := p.lis.Accept()
	if err != nil {
		return
	}
	p.in = in

	req, err := http.ReadRequest(bufio.NewReader(in))
	if err != nil {
		p.t.Errorf("failed to read CONNECT req: %v", err)
		return
	}
	if err := p.requestCheck(req); err != nil {
		resp := http.Response{StatusCode: http.StatusMethodNotAllowed}
		resp.Write(p.in)
		p.in.Close()
		p.t.Errorf("get wrong CONNECT req: %+v, error: %v", req, err)
		return
	}
	p.conRequest.Store(true)
	out, err := net.Dial("tcp", req.URL.Host)
	if err != nil {
		p.t.Errorf("failed to dial to server: %v", err)
		return
	}
	resp := http.Response{StatusCode: http.StatusOK, Proto: "HTTP/1.0"}
	resp.Write(p.in)
	p.out = out
	go io.Copy(p.in, p.out)
	go io.Copy(p.out, p.in)
}

func (p *proxyServer) stop() {
	p.lis.Close()
	if p.in != nil {
		p.in.Close()
	}
	if p.out != nil {
		p.out.Close()
	}
}

func testGRPCConnect(t *testing.T, proxyURL string, proxyReqCheck func(*http.Request) error) {
	pURL, err := url.Parse(proxyURL)
	if err != nil {
		t.Fatalf("unable to parse proxyURL: %v", proxyURL)
	}
	plis, err := net.Listen("tcp", pURL.Host)
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	p := &proxyServer{
		t:            t,
		lis:          plis,
		requestCheck: proxyReqCheck,
	}
	go p.run()
	defer p.stop()

	blis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	s := grpc.NewServer()
	go func() {
		if err := s.Serve(blis); err != nil {
			log.Errorf("Server exited with error: %v", err)
		}
	}()

	conn, err := grpc.Dial(blis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		GetGrpcProxyDialerOption(proxyURL))
	if err != nil {
		t.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()
	// Check connection to proxy has been made
	time.Sleep(1 * time.Second)
	if !p.conRequest.Load() {
		t.Fatalf("connection request bypassed proxy!")
	}
}

func TestGRPCConnect(t *testing.T) {
	const (
		user     = "notAUser"
		password = "notAPassword"
	)
	testGRPCConnect(t,
		fmt.Sprintf("http://%v:%v@localhost:5678", user, password),
		func(req *http.Request) error {
			if req.Method != http.MethodConnect {
				return fmt.Errorf("unexpected Method %q, want %q", req.Method, http.MethodConnect)
			}
			wantProxyAuthStr := "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+password))
			if got := req.Header.Get(proxyAuthHeaderKey); got != wantProxyAuthStr {
				gotDecoded, _ := base64.StdEncoding.DecodeString(got)
				wantDecoded, _ := base64.StdEncoding.DecodeString(wantProxyAuthStr)
				return fmt.Errorf("unexpected auth %q (%q), want %q (%q)", got, gotDecoded, wantProxyAuthStr, wantDecoded)
			}
			return nil
		},
	)
}
