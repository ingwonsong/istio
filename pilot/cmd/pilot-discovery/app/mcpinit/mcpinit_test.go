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

package mcpinit

import (
	"testing"
)

func TestConnectGatewayEndpointFromHubEndpoint(t *testing.T) {
	tests := []struct {
		name                string
		hubEndpoint         string
		location            string
		cgwGlobalEndpoint   string
		regionalCGWEndpoint string
	}{
		{
			name:                "global membership",
			hubEndpoint:         "autopush-gkehub.sandbox.googleapis.com",
			location:            "global",
			cgwGlobalEndpoint:   "https://autopush-connectgateway.sandbox.googleapis.com",
			regionalCGWEndpoint: "https://autopush-connectgateway.sandbox.googleapis.com",
		},
		{
			name:                "regional membership",
			hubEndpoint:         "staging-gkehub.sandbox.googleapis.com",
			location:            "us-central1",
			cgwGlobalEndpoint:   "https://staging-connectgateway.sandbox.googleapis.com",
			regionalCGWEndpoint: "https://us-central1-staging-connectgateway.sandbox.googleapis.com",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := connectGatewayEndpointFromHubEndpoint(test.hubEndpoint, test.location, false)
			if got != test.cgwGlobalEndpoint {
				t.Errorf("Got %s, want %s", got, test.cgwGlobalEndpoint)
			}
			got = connectGatewayEndpointFromHubEndpoint(test.hubEndpoint, test.location, true)
			if got != test.regionalCGWEndpoint {
				t.Errorf("Got %s, want %s", got, test.regionalCGWEndpoint)
			}
		})
	}
}
