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
	"fmt"
	"reflect"

	"github.com/hashicorp/go-multierror"
	"google.golang.org/protobuf/types/known/structpb"
	wrappers "google.golang.org/protobuf/types/known/wrapperspb"

	meshconfig "istio.io/api/mesh/v1alpha1"
	"istio.io/api/networking/v1alpha3"
	"istio.io/istio/pkg/log"
	"istio.io/istio/pkg/util/protomarshal"
)

var unsupportedProxyMetadata = map[string]any{
	"CA_ROOT_CA":                       "",
	"HTTPS_PROXY":                      "",
	"HTTP_PROXY":                       "",
	"ISTIO_META_PROXY_XDS_VIA_AGENT":   "",
	"ISTO_META_ENABLE_NATIVE_SIDECARS": "",
	"PILOT_JWT_ENABLE_REMOTE_JWKS":     "",
	"PROXY_CONFIG_XDS_AGENT":           "",
	"TRUST_DOMAIN":                     "",
	"XDS_AUTH_PROVIDER":                "",
	"XDS_HEADER_Cloud-Run-Enable-H2":   "",
	"XDS_ROOT_CA":                      "",
}

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

// mcpValidateProxyConfig validates any change to the default proxyconfig
func mcpValidateProxyConfig(proxyConfig *meshconfig.ProxyConfig) map[string]any {
	unsupportedFields := map[string]any{}
	if proxyConfig == nil {
		return unsupportedFields
	}
	gc := DefaultProxyConfig()
	if proxyConfig.GetBinaryPath() != gc.GetBinaryPath() {
		unsupportedFields["proxyconfig.binaryPath"] = struct{}{}
	}
	if proxyConfig.GetConfigPath() != gc.GetConfigPath() {
		unsupportedFields["proxyconfig.configPath"] = struct{}{}
	}
	if proxyConfig.GetControlPlaneAuthPolicy() != gc.GetControlPlaneAuthPolicy() {
		unsupportedFields["proxyconfig.controlPlaneAuthPolicy"] = struct{}{}
	}
	if proxyConfig.GetCustomConfigFile() != gc.GetCustomConfigFile() {
		unsupportedFields["proxyconfig.customConfigFile"] = struct{}{}
	}
	if proxyConfig.GetDiscoveryAddress() != gc.GetDiscoveryAddress() {
		unsupportedFields["proxyconfig.discoveryAddress"] = struct{}{}
	}
	if proxyConfig.GetEnvoyAccessLogService() != gc.GetEnvoyAccessLogService() {
		unsupportedFields["proxyconfig.envoyAccessLogService"] = struct{}{}
	}
	if proxyConfig.GetEnvoyMetricsService() != gc.GetEnvoyMetricsService() {
		unsupportedFields["proxyconfig.envoyMetricsService"] = struct{}{}
	}
	if proxyConfig.GetMeshId() != gc.GetMeshId() {
		unsupportedFields["proxyconfig.meshId"] = struct{}{}
	}
	if proxyConfig.GetPrivateKeyProvider() != gc.GetPrivateKeyProvider() {
		unsupportedFields["proxyconfig.privateKeyProvider"] = struct{}{}
	}
	if proxyConfig.GetProxyBootstrapTemplatePath() != gc.GetProxyBootstrapTemplatePath() {
		unsupportedFields["proxyconfig.proxyBootstrapTemplatePath"] = struct{}{}
	}
	proxyHeaders := proxyConfig.GetProxyHeaders()
	if proxyHeaders.GetServer().GetValue() != "" {
		unsupportedFields["proxyconfig.proxyHeaders.server.value"] = struct{}{}
	}
	proxyMetadata := proxyConfig.GetProxyMetadata()
	for k := range proxyMetadata {
		if _, ok := unsupportedProxyMetadata[k]; ok {
			unsupportedFields[fmt.Sprintf("proxyconfig.proxyMetadata.%v", k)] = struct{}{}
		}
	}
	if proxyConfig.GetReadinessProbe() != gc.GetReadinessProbe() {
		unsupportedFields["proxyconfig.readinessProbe"] = struct{}{}
	}

	if proxyConfig.GetServiceCluster() != gc.GetServiceCluster() {
		unsupportedFields["proxyconfig.serviceCluster"] = struct{}{}
	}
	if proxyConfig.GetStatNameLength() != gc.GetStatNameLength() {
		unsupportedFields["proxyconfig.statNameLength"] = struct{}{}
	}
	if proxyConfig.GetStatsdUdpAddress() != gc.GetStatsdUdpAddress() {
		unsupportedFields["proxyconfig.statsdUdpAddress"] = struct{}{}
	}
	if proxyConfig.GetStatusPort() != gc.GetStatusPort() {
		unsupportedFields["proxyconfig.statusPort"] = struct{}{}
	}
	if proxyConfig.GetTracing() != nil {
		if proxyConfig.GetTracing().GetLightstep() != nil {
			unsupportedFields["proxyconfig.tracing.lightstep"] = struct{}{}
		}
		if proxyConfig.GetTracing().GetOpenCensusAgent().GetContext() != nil {
			unsupportedFields["proxyconfig.tracing.openCensusAgent.context"] = struct{}{}
		}
		if proxyConfig.GetTracing().GetTlsSettings() != nil {
			unsupportedFields["proxyconfig.tracing.tlsSettings"] = struct{}{}
		}
	}
	return unsupportedFields
}

// mcpValidateMeshConfig validates any change to the default meshconfig
func mcpValidateMeshConfig(mc *meshconfig.MeshConfig) error {
	unsupportedToErr := func(fields map[string]any) error {
		var err error
		for k := range fields {
			err = multierror.Append(err, fmt.Errorf("unsupported api usage: %v", k))
		}
		return err
	}
	unsupportedFields := map[string]any{}
	if mc == nil {
		return unsupportedToErr(unsupportedFields)
	}
	gc := DefaultMeshConfig()
	if mc.GetCa().GetIstiodSide() != gc.GetCa().GetIstiodSide() {
		unsupportedFields["meshconfig.ca.istiodSide"] = struct{}{}
	}
	if mc.GetCa().GetTlsSettings() != gc.GetCa().GetTlsSettings() {
		unsupportedFields["meshconfig.ca.tlsSettings"] = struct{}{}
	}
	if !reflect.DeepEqual(mc.GetCaCertificates(), gc.GetCaCertificates()) {
		unsupportedFields["meshconfig.caCertificates"] = struct{}{}
	}
	if !reflect.DeepEqual(mc.GetConfigSources(), gc.GetConfigSources()) {
		unsupportedFields["meshconfig.configSources"] = struct{}{}
	}
	if mc.GetIngressClass() != gc.GetIngressClass() {
		unsupportedFields["meshconfig.ingressClass"] = struct{}{}
	}
	if mc.GetIngressControllerMode() != gc.GetIngressControllerMode() {
		unsupportedFields["meshconfig.ingressControllerMode"] = struct{}{}
	}
	if mc.GetIngressService() != gc.GetIngressService() {
		unsupportedFields["meshconfig.ingressService"] = struct{}{}
	}
	if mc.GetIngressSelector() != gc.GetIngressSelector() {
		unsupportedFields["meshconfig.ingressSelector"] = struct{}{}
	}
	if mc.GetProxyHttpPort() != gc.GetProxyHttpPort() {
		unsupportedFields["meshconfig.proxyHttpPort"] = struct{}{}
	}
	if mc.GetProxyListenPort() != gc.GetProxyListenPort() {
		unsupportedFields["meshconfig.proxyListenPort"] = struct{}{}
	}
	if mc.GetProxyInboundListenPort() != gc.GetProxyInboundListenPort() {
		unsupportedFields["meshconfig.proxyInboundListenPort"] = struct{}{}
	}
	for _, distribute := range mc.GetLocalityLbSetting().GetDistribute() {
		if len(distribute.GetTo()) > 0 {
			unsupportedFields["meshconfig.localityLbSetting.distribute.to"] = struct{}{}
		}
	}

	for _, ext := range mc.GetExtensionProviders() {
		if ext.GetDatadog().GetMaxTagLength() != 0 {
			unsupportedFields["meshconfig.extensionProviders.datadog.maxTagLength"] = struct{}{}
		}
		if ext.GetEnvoyHttpAls() != nil {
			unsupportedFields["meshconfig.extensionProviders.envoyHttpAls"] = struct{}{}
		}
		if ext.GetEnvoyOtelAls().GetLogName() != "" {
			unsupportedFields["meshconfig.extensionProviders.envoyOtelAls.logName"] = struct{}{}
		}
		if ext.GetEnvoyOtelAls().GetLogFormat().GetText() != "" {
			unsupportedFields["meshconfig.extensionProviders.envoyOtelAls.logFormat.text"] = struct{}{}
		}
		if ext.GetEnvoyOtelAls().GetLogFormat().GetLabels().GetFields() != nil {
			unsupportedFields["meshconfig.extensionProviders.envoyOtelAls.logFormat.labels.fields"] = struct{}{}
		}
		if ext.GetEnvoyTcpAls() != nil {
			unsupportedFields["meshconfig.extensionProviders.envoyTcpAls"] = struct{}{}
		}
		if ext.GetOpentelemetry().GetMaxTagLength() != 0 {
			unsupportedFields["meshconfig.extensionProviders.opentelemetry.maxTagLength"] = struct{}{}
		}
		if ext.GetSkywalking() != nil {
			unsupportedFields["meshconfig.extensionProviders.skywalking"] = struct{}{}
		}
		if ext.GetZipkin().GetMaxTagLength() != 0 {
			unsupportedFields["meshconfig.extensionProviders.zipkin.maxTagLength"] = struct{}{}
		}
		if ext.GetZipkin().GetEnable_64BitTraceId() {
			unsupportedFields["meshconfig.extensionProviders.zipkin.enable64BitTraceId"] = struct{}{}
		}
	}
	for k := range mcpValidateProxyConfig(mc.GetDefaultConfig()) {
		unsupportedFields[k] = struct{}{}
	}
	return unsupportedToErr(unsupportedFields)
}
