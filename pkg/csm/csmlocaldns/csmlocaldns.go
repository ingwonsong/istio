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

package csmlocaldns

import (
	"time"

	"github.com/miekg/dns"

	"istio.io/istio/pkg/env"
)

var (
	defaultClientForLocalEnvoy = &dns.Client{
		Net:          "udp",
		DialTimeout:  5 * time.Second,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	enableDNSQueryToLocalEnvoy = env.RegisterBoolVar("CSM_ENABLE_ENABLE_DNS_QUERY_TO_LOCAL_ENVOY", false,
		"If this is set to true, the local DNS server will try to make a query to the local Enovy DNS server before going to the original DNS upstream server.").Get()
)

func Query(req *dns.Msg) (*dns.Msg, error) {
	if !enableDNSQueryToLocalEnvoy {
		return &dns.Msg{}, nil
	}
	resp, _, err := defaultClientForLocalEnvoy.Exchange(req, "127.0.0.1:15053")
	return resp, err
}
