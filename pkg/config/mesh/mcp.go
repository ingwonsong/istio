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
	"google.golang.org/protobuf/types/known/structpb"
	wrappers "google.golang.org/protobuf/types/known/wrapperspb"

	meshconfig "istio.io/api/mesh/v1alpha1"
	"istio.io/api/networking/v1alpha3"
	"istio.io/istio/pkg/util/protomarshal"
	"istio.io/pkg/log"
)

// MCPDefaultProxyConfig provides defaults for proxy config when running in MCP.
// This is used, rather than the local file mesh config, when we want users to be able to override settings.
// The hierarchy is go defaults < users in-cluster mesh config < file mesh config.
// Placing them here puts them at the lowest priority.
func MCPDefaultProxyConfig(pc *meshconfig.ProxyConfig) *meshconfig.ProxyConfig {
	// No tracing configured by default
	pc.Tracing = nil
	return pc
}

func MCPDefaultMeshConfig(mc *meshconfig.MeshConfig) *meshconfig.MeshConfig {
	// Disable locality LB by default, but users can still turn it on
	mc.LocalityLbSetting = &v1alpha3.LocalityLoadBalancerSetting{
		Enabled: &wrappers.BoolValue{Value: true},
	}
	mc.EnablePrometheusMerge = &wrappers.BoolValue{Value: true}
	mc.DefaultProviders = &meshconfig.MeshConfig_DefaultProviders{
		// By default, we enable metrics and access logging for MCP
		Metrics:       []string{"prometheus", "stackdriver"},
		AccessLogging: []string{"stackdriver"},
	}
	return mc
}

const legacyEnvoyLogProvider = "asm-internal-envoy-legacy"

// amendLogging corrects MCP's mesh config semantics around access logging to align with OSS. For
// OSS, we want accessLogFile (legacy) to take effect when there is no Telemetry API configured. Note
// that defaultProvider is part of "Telemetry API configured". However, for MCP we want StackDriver
// logging enabled by default. This means "Telemetry API configured" is always 'true', and users can
// never set accessLogFile. to work around this, we hack things quite a bit to make the SD enablement
// transparent to users. If a user has accessLogFile set and is not explicitly configuring
// defaultProviders.accessLogging, we will mutate the merge MeshConfig result, adding a new
// legacyEnvoyLogProvider provider which has settings configured based on the legacy settings. This
// is added as a defaultProvider. The end result is that users setting only accessLogFile will
// continue to see this work.
func amendLogging(mc *meshconfig.MeshConfig, raw map[string]interface{}) {
	// If they haven't set AccessLogFile, no need to enable things.
	// If they have set defaultConfig.accessLogging, they are overriding - also no need to enable.
	if mc.AccessLogFile == "" || accessLogProviderSet(raw) {
		return
	}

	// Append the provider if its not already found
	found := false
	for _, p := range mc.DefaultProviders.AccessLogging {
		if p == legacyEnvoyLogProvider {
			found = true
			break
		}
	}
	if !found {
		mc.DefaultProviders.AccessLogging = append(mc.DefaultProviders.AccessLogging, legacyEnvoyLogProvider)
	}

	// Now construct our provider
	prov := &meshconfig.MeshConfig_ExtensionProvider_EnvoyFileAccessLogProvider{
		// Copy the path from their config
		Path: mc.AccessLogFile,
	}

	switch mc.AccessLogEncoding {
	case meshconfig.MeshConfig_JSON:
		// nil format is fine - this means to use JSON with default format
		var fmt *structpb.Struct
		if mc.AccessLogFormat != "" {
			fmt = &structpb.Struct{}
			if err := protomarshal.ApplyYAML(mc.AccessLogFormat, fmt); err != nil {
				log.Errorf("failed to apply AccessLogFormat: %v", err)
			}
		}
		prov.LogFormat = &meshconfig.MeshConfig_ExtensionProvider_EnvoyFileAccessLogProvider_LogFormat{
			LogFormat: &meshconfig.MeshConfig_ExtensionProvider_EnvoyFileAccessLogProvider_LogFormat_Labels{
				Labels: fmt,
			},
		}
	default:
		if mc.AccessLogFormat != "" {
			prov.LogFormat = &meshconfig.MeshConfig_ExtensionProvider_EnvoyFileAccessLogProvider_LogFormat{
				LogFormat: &meshconfig.MeshConfig_ExtensionProvider_EnvoyFileAccessLogProvider_LogFormat_Text{
					Text: mc.AccessLogFormat,
				},
			}
		}
	}

	// Clone list and insert our new provider
	mc.ExtensionProviders = append([]*meshconfig.MeshConfig_ExtensionProvider{}, mc.ExtensionProviders...)
	mc.ExtensionProviders = append(mc.ExtensionProviders, &meshconfig.MeshConfig_ExtensionProvider{
		Name: legacyEnvoyLogProvider,
		Provider: &meshconfig.MeshConfig_ExtensionProvider_EnvoyFileAccessLog{
			EnvoyFileAccessLog: prov,
		},
	})
}

func accessLogProviderSet(raw map[string]interface{}) bool {
	dp, ok := raw["defaultProviders"].(map[string]interface{})
	if !ok {
		return false
	}
	_, f := dp["accessLogging"]
	return f
}
