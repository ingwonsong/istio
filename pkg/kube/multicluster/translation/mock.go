// Copyright Istio Authors.
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

package translation

import "k8s.io/client-go/tools/clientcmd/api"

type MockMembershipCache struct {
	ipConfigMapping map[string]api.Config
}

func NewMockMembershipCache(ipConfigMapping map[string]api.Config) *MockMembershipCache {
	if ipConfigMapping == nil {
		ipConfigMapping = map[string]api.Config{}
	}

	return &MockMembershipCache{
		ipConfigMapping: ipConfigMapping,
	}
}

func (m *MockMembershipCache) Get(ip string) (api.Config, bool) {
	config, ok := m.ipConfigMapping[ip]
	if !ok {
		return api.Config{}, false
	}
	return config, ok
}

func (m *MockMembershipCache) Run(stop <-chan struct{}) {
}
