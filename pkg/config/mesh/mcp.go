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

package mesh

import (
	"github.com/gogo/protobuf/types"

	meshconfig "istio.io/api/mesh/v1alpha1"
	"istio.io/api/networking/v1alpha3"
)

// MCPDefaultProxyConfig provides defaults for proxy config when running in MCP.
// This is used, rather than the local file mesh config, when we want users to be able to override settings.
// The hierarchy is go defaults < users in-cluster mesh config < file mesh config.
// Placing them here puts them at the lowest priority.
func MCPDefaultProxyConfig(pc meshconfig.ProxyConfig) meshconfig.ProxyConfig {
	// No tracing configured by default
	pc.Tracing = nil
	return pc
}

func MCPDefaultMeshConfig(mc meshconfig.MeshConfig) meshconfig.MeshConfig {
	// Disable locality LB by default, but users can still turn it on
	mc.LocalityLbSetting = &v1alpha3.LocalityLoadBalancerSetting{
		Enabled: &types.BoolValue{Value: true},
	}
	mc.EnablePrometheusMerge = &types.BoolValue{Value: true}
	return mc
}
