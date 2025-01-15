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

package model

import (
	"fmt"
	"strings"
	"testing"

	core "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	listener "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	extauthz "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_authz/v3"
	grpcfieldextractionconfig "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/grpc_field_extraction/v3"
	grpcjsontranscoder "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/grpc_json_transcoder/v3"
	grpcweb "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/grpc_web/v3"
	lua "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/lua/v3"
	ratelimit "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ratelimit/v3"
	wasm "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/wasm/v3"
	hcm "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	headertometadata "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/thrift_proxy/filters/header_to_metadata/v3"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	anypb "google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	metav1alpha1 "istio.io/api/meta/v1alpha1"
	networking "istio.io/api/networking/v1alpha3"
	"istio.io/istio/pkg/config"
	"istio.io/istio/pkg/config/mesh"
	"istio.io/istio/pkg/config/schema/gvk"
	"istio.io/istio/pkg/monitoring/monitortest"
	"istio.io/istio/pkg/slices"
)

func TestUnsupportedPatchStatus(t *testing.T) {
	pw := &EnvoyFilterConfigPatchWrapper{Name: "patch wrapper"}
	want := &metav1alpha1.IstioCondition{
		Type:    fmt.Sprintf("Patch %+v is Ready", pw),
		Status:  "False",
		Message: "This EnvoyFilter patch is not supported for 1P services",
	}
	NewWithT(t).Expect(unsupportedPatchStatus(pw)).To(Equal(want))
}

func TestValidateTypeURLAndValue(t *testing.T) {
	cases := []struct {
		name  string
		url   typeURL
		value proto.Message
		want  bool
	}{
		{
			name: "Unsupported filter",
			url:  "some-type-url",
			want: false,
		},
		{
			name:  "Supported Lua filter",
			url:   Lua,
			value: &lua.Lua{InlineCode: supportedLuaScript},
			want:  true,
		},
		{
			name: "Unsupported Lua filter",
			url:  Lua,
			value: &lua.Lua{
				InlineCode: `function envoy_on_request(request_handle)
  request_handle:logInfo("Hello World.")
end`,
			},
			want: false,
		},
		{
			name: "Supported Lua filter with different formatting",
			url:  Lua,
			value: &lua.Lua{
				// Terminating newline in the script
				InlineCode: fmt.Sprintf(`%s
`, supportedLuaScript),
			},
			want: false,
		},
		{
			name: "Supported HCM filter",
			url:  HTTPConnectionManager,
			value: &hcm.HttpConnectionManager{
				UpgradeConfigs: []*hcm.HttpConnectionManager_UpgradeConfig{
					{
						UpgradeType: "SPDY/3.1",
					},
				},
			},
			want: true,
		},
		{
			name: "Unsupported HCM filter with different upgrade",
			url:  HTTPConnectionManager,
			value: &hcm.HttpConnectionManager{
				UpgradeConfigs: []*hcm.HttpConnectionManager_UpgradeConfig{
					{
						UpgradeType: "TLS/1.2",
					},
				},
			},
			want: false,
		},
		{
			name: "Unsupported HCM filter with additional config",
			url:  HTTPConnectionManager,
			value: &hcm.HttpConnectionManager{
				AddUserAgent: &wrapperspb.BoolValue{Value: true},
				UpgradeConfigs: []*hcm.HttpConnectionManager_UpgradeConfig{
					{
						UpgradeType: "SPDY/3.1",
					},
				},
			},
			want: false,
		},
		// The configs for the filters below are intentionally left empty (instead of a placeholder config).
		// This is because the following filters are fully supported (all configurations).
		{
			name:  "Wasm",
			url:   Wasm,
			value: &wasm.Wasm{},
			want:  true,
		},
		{
			name:  "GrpcWeb",
			url:   GrpcWeb,
			value: &grpcweb.GrpcWeb{},
			want:  true,
		},
		{
			name:  "ExtAuthz",
			url:   ExtAuthz,
			value: &extauthz.ExtAuthz{},
			want:  true,
		},
		{
			name:  "GrpcJSONTranscoder",
			url:   GrpcJSONTranscoder,
			value: &grpcjsontranscoder.GrpcJsonTranscoder{},
			want:  true,
		},
		{
			name:  "GrpcFieldExtractionConfig",
			url:   GrpcFieldExtractionConfig,
			value: &grpcfieldextractionconfig.GrpcFieldExtractionConfig{},
			want:  true,
		},
		{
			name:  "HeaderToMetadata",
			url:   HeaderToMetadata,
			value: &headertometadata.HeaderToMetadata{},
			want:  true,
		},
		{
			name:  "RateLimit",
			url:   RateLimit,
			value: &ratelimit.RateLimit{},
			want:  true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := NewWithT(t)
			v, err := proto.Marshal(c.value)
			g.Expect(err).To(BeNil())
			g.Expect(validateTypeURLAndValue(string(c.url), v)).To(Equal(c.want))
		})
	}
}

func testHCMPatches(t *testing.T, isSupported func(*EnvoyFilterConfigPatchWrapper) (bool, error)) {
	t.Helper()
	cases := []struct {
		name   string
		config *hcm.HttpConnectionManager
		want   bool
	}{
		{
			name: "SPDY upgrade",
			config: &hcm.HttpConnectionManager{
				UpgradeConfigs: []*hcm.HttpConnectionManager_UpgradeConfig{
					{
						UpgradeType: "SPDY/3.1",
					},
				},
			},
			want: true,
		},
		{
			name:   "Non-SPDY-upgrade config",
			config: &hcm.HttpConnectionManager{AddUserAgent: &wrapperspb.BoolValue{Value: true}},
			want:   false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := NewWithT(t)
			v, err := proto.Marshal(c.config)
			g.Expect(err).To(BeNil())
			ok, err := isSupported(&EnvoyFilterConfigPatchWrapper{
				Value: &listener.Filter{
					ConfigType: &listener.Filter_TypedConfig{
						TypedConfig: &anypb.Any{
							TypeUrl: string(HTTPConnectionManager),
							Value:   v,
						},
					},
				},
			})
			g.Expect(err).To(BeNil())
			g.Expect(ok).To(Equal(c.want))
		})
	}
}

func TestIsSupportedHTTPFilterPatch(t *testing.T) {
	cases := []struct {
		name   string
		url    typeURL
		config proto.Message
		want   bool
	}{
		{
			name:   "Lua",
			url:    Lua,
			config: &lua.Lua{InlineCode: supportedLuaScript},
			want:   true,
		},
		{
			name:   "HeaderToMetadata",
			url:    HeaderToMetadata,
			config: &headertometadata.HeaderToMetadata{},
			want:   true,
		},
		{
			name: "Unsupported filter",
			url:  "some-type-url",
			want: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := NewWithT(t)
			v, err := proto.Marshal(c.config)
			g.Expect(err).To(BeNil())
			g.Expect(isSupportedHTTPFilterPatch(&hcm.HttpFilter{
				ConfigType: &hcm.HttpFilter_TypedConfig{
					TypedConfig: &anypb.Any{
						TypeUrl: string(c.url),
						Value:   v,
					},
				},
			},
			)).To(Equal(c.want))
		})
	}
}

func TestIsPatchSupported(t *testing.T) {
	// Test for HCM patches.
	testHCMPatches(t, isPatchSupported)

	// Test for non-HCM HTTP Filter patches.
	cases := []struct {
		name   string
		url    typeURL
		config proto.Message
		want   bool
	}{
		{
			name:   "Lua",
			url:    Lua,
			config: &lua.Lua{InlineCode: supportedLuaScript},
			want:   true,
		},
		{
			name:   "HeaderToMetadata",
			url:    HeaderToMetadata,
			config: &headertometadata.HeaderToMetadata{},
			want:   true,
		},
		{
			name: "Unsupported filter",
			url:  "some-type-url",
			want: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := NewWithT(t)
			v, err := proto.Marshal(c.config)
			g.Expect(err).To(BeNil())
			ok, err := isPatchSupported(&EnvoyFilterConfigPatchWrapper{
				Value: &hcm.HttpFilter{
					ConfigType: &hcm.HttpFilter_TypedConfig{
						TypedConfig: &anypb.Any{
							TypeUrl: string(c.url),
							Value:   v,
						},
					},
				},
			})
			g.Expect(err).To(BeNil())
			g.Expect(ok).To(Equal(c.want))
		})
	}

	// Test for non-HTTP-Filter non-HCM patches.
	t.Run("Unsupported patch", func(t *testing.T) {
		g := NewWithT(t)
		ok, err := isPatchSupported(&EnvoyFilterConfigPatchWrapper{Value: &core.TypedExtensionConfig{}})
		g.Expect(err).To(Not(BeNil()))
		g.Expect(ok).To(BeFalse())
	})
}

func TestEnforce1PSEnvoyFilterAllowlist(t *testing.T) {
	const (
		fullySupportedPatchType     = "type.googleapis.com/envoy.extensions.filters.http.grpc_web.v3.GrpcWeb"
		partiallySupportedPatchType = "type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager"
		unsupportedPatchType        = "type.googleapis.com/envoy.extensions.filters.network.mongo_proxy.v3.MongoProxy"
		supportedHCMUpgradeConfig   = "SPDY/3.1"
		unsupportedHCMUpgradeConfig = "TLS/1.2"
	)
	cfg := config.Config{
		Meta: config.Meta{Name: "multiple-patches", Namespace: "default", GroupVersionKind: gvk.EnvoyFilter},
		Spec: &networking.EnvoyFilter{
			// Ordering of patches is important for extracting `wantPatchConditions` below
			// to simplify parsing logic.
			ConfigPatches: []*networking.EnvoyFilter_EnvoyConfigObjectPatch{
				{
					ApplyTo: networking.EnvoyFilter_HTTP_FILTER,
					Patch: &networking.EnvoyFilter_Patch{
						Value: buildPatchStruct(fmt.Sprintf(`{
								"typed_config": {
									"@type": "%s"
								}
							}`, unsupportedPatchType)),
					},
				},
				{
					ApplyTo: networking.EnvoyFilter_NETWORK_FILTER,
					Patch: &networking.EnvoyFilter_Patch{
						Value: buildPatchStruct(fmt.Sprintf(`{
								"typed_config": {
									"@type": "%s",
									"upgradeConfigs": [
										{
											"upgradeType": "%s"
										}
									]
								}
							}`, partiallySupportedPatchType, unsupportedHCMUpgradeConfig)),
					},
				},
				{
					ApplyTo: networking.EnvoyFilter_HTTP_FILTER,
					Patch: &networking.EnvoyFilter_Patch{
						Value: buildPatchStruct(fmt.Sprintf(`{
								"typed_config": {
									"@type": "%s"
								}
							}`, fullySupportedPatchType)),
					},
				},
				{
					ApplyTo: networking.EnvoyFilter_NETWORK_FILTER,
					Patch: &networking.EnvoyFilter_Patch{
						Value: buildPatchStruct(fmt.Sprintf(`{
								"typed_config": {
									"@type": "%s",
									"upgradeConfigs": [
										{
											"upgradeType": "%s"
										}
									]
								}
							}`, partiallySupportedPatchType, supportedHCMUpgradeConfig)),
					},
				},
			},
		},
	}
	efw := convertToEnvoyFilterWrapper(&cfg)
	env := &Environment{}
	store := NewFakeStore()
	_, _ = store.Create(cfg)
	env.ConfigStore = store
	m := mesh.DefaultMeshConfig()
	env.Watcher = mesh.NewFixedWatcher(m)
	env.Init()
	mt := monitortest.New(t)

	wantPatchConditions := []*metav1alpha1.IstioCondition{}
	configPatches := config.DeepCopy(cfg.Spec.(*networking.EnvoyFilter).ConfigPatches).([]*networking.EnvoyFilter_EnvoyConfigObjectPatch)
	for _, p := range convertToEnvoyFilterWrapper(&config.Config{
		Meta: cfg.Meta,
		Spec: &networking.EnvoyFilter{
			ConfigPatches: configPatches[0:2],
		},
	}).Patches {
		for _, pw := range p {
			wantPatchConditions = append(wantPatchConditions, unsupportedPatchStatus(pw))
		}
	}

	cfg = enforce1PSEnvoyFilterAllowlist(env, efw, cfg)

	gotPatches := 0
	const wantPatches = 2
	for _, p := range efw.Patches {
		for _, pw := range p {
			gotPatches++
			httpFilter, ok1 := (pw.Value).(*hcm.HttpFilter)
			listenerFilter, ok2 := (pw.Value).(*listener.Filter)
			if !ok1 && !ok2 {
				t.Errorf("Unknown type of patch wrapper: %v", pw)
			}
			if ok1 {
				if httpFilter.GetTypedConfig().GetTypeUrl() != fullySupportedPatchType {
					t.Errorf("Got type URL %q; want %q", httpFilter.GetTypedConfig().GetTypeUrl(), fullySupportedPatchType)
				}
			} else {
				if listenerFilter.GetTypedConfig().GetTypeUrl() != partiallySupportedPatchType {
					t.Errorf("Got type URL %q; want %q", listenerFilter.GetTypedConfig().GetTypeUrl(), partiallySupportedPatchType)
				}
				wantConfig := &hcm.HttpConnectionManager{
					UpgradeConfigs: []*hcm.HttpConnectionManager_UpgradeConfig{
						{
							UpgradeType: supportedHCMUpgradeConfig,
						},
					},
				}
				gotConfig := &hcm.HttpConnectionManager{}
				if err := proto.Unmarshal(listenerFilter.GetTypedConfig().GetValue(), gotConfig); err != nil {
					t.Errorf("Unmarshal patch config value: %v", err)
				}
				if !proto.Equal(gotConfig, wantConfig) {
					t.Errorf("Got HCM config %v; want %v", gotConfig, wantConfig)
				}
			}
		}
	}

	if gotPatches != wantPatches {
		t.Fatalf("Got %v patches; Want %v", gotPatches, wantPatches)
	}

	status, ok := cfg.Status.(*metav1alpha1.IstioStatus)
	if !ok {
		t.Fatalf("Expected status of type *metav1alpha1.IstioStatus")
	}
	cmpFn := func(a, b *metav1alpha1.IstioCondition) int {
		return strings.Compare(a.Type, b.Type)
	}
	s1 := slices.SortStableFunc(status.GetConditions(), cmpFn)
	s2 := slices.SortStableFunc(wantPatchConditions, cmpFn)
	if !slices.EqualFunc(s1, s2, func(a, b *metav1alpha1.IstioCondition) bool {
		return a.Type == b.Type && a.Status == b.Status && a.Message == b.Message
	}) {
		t.Fatalf("Got patch conditions %v; Want %v", status.GetConditions(), wantPatchConditions)
	}

	mt.Assert(totalPatches.Name(), map[string]string{envoyfilterPatchStatus: envoyfilterSuccessStatus}, monitortest.Exactly(2))
	mt.Assert(totalPatches.Name(), map[string]string{envoyfilterPatchStatus: envoyfilterBlockedByAllowlist}, monitortest.Exactly(2))
	mt.Assert(blockedPatches.Name(), map[string]string{envoyfilterPatchType: partiallySupportedPatchType}, monitortest.Exactly(1))
	mt.Assert(blockedPatches.Name(), map[string]string{envoyfilterPatchType: unsupportedPatchType}, monitortest.Exactly(1))
}
