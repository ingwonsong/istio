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

package wasm

import (
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"testing"
	"time"

	udpa "github.com/cncf/xds/go/udpa/type/v1"
	core "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	rbac "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/rbac/v3"
	wasm "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/wasm/v3"
	v3 "github.com/envoyproxy/go-control-plane/envoy/extensions/wasm/v3"
	"github.com/envoyproxy/go-control-plane/pkg/conversion"
	resource "github.com/envoyproxy/go-control-plane/pkg/resource/v3"
	"google.golang.org/protobuf/proto"
	anypb "google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"

	extensions "istio.io/api/extensions/v1alpha1"
	"istio.io/istio/pilot/pkg/model"
	"istio.io/istio/pilot/pkg/util/protoconv"
	"istio.io/istio/pkg/config/xds"
)

type mockCache struct {
	wantSecret []byte
	wantPolicy extensions.PullPolicy
}

func (c *mockCache) Get(
	downloadURL, checksum, resourceName, resourceVersion string,
	timeout time.Duration, pullSecret []byte, pullPolicy extensions.PullPolicy,
) (string, error) {
	url, _ := url.Parse(downloadURL)
	query := url.Query()

	module := query.Get("module")
	errMsg := query.Get("error")
	var err error
	if errMsg != "" {
		err = errors.New(errMsg)
	}
	if c.wantSecret != nil && !reflect.DeepEqual(c.wantSecret, pullSecret) {
		return "", fmt.Errorf("wrong secret for %v, got %q want %q", downloadURL, string(pullSecret), c.wantSecret)
	}
	if c.wantPolicy != pullPolicy {
		return "", fmt.Errorf("wrong pull policy for %v, got %v want %v", downloadURL, pullPolicy, c.wantPolicy)
	}

	return module, err
}
func (c *mockCache) Cleanup() {}

func TestWasmConvert(t *testing.T) {
	cases := []struct {
		name       string
		input      []configSpec
		wantOutput []configSpec
		wantNack   bool
	}{
		{
			name:       "remote-load-success",
			input:      []configSpec{{tName: "remote-load-success"}},
			wantOutput: []configSpec{{tName: "remote-load-success-local-file"}},
			wantNack:   false,
		},
		{
			name:       "remote-load-fail",
			input:      []configSpec{{tName: "remote-load-fail"}},
			wantOutput: []configSpec{{tName: "remote-load-deny"}},
			wantNack:   true,
		},
		{
			name: "mix",
			input: []configSpec{
				{tName: "remote-load-fail", eName: "remote-load-mix-fail"},
				{tName: "remote-load-success", eName: "remote-load-mix-success"}},
			wantOutput: []configSpec{
				{tName: "remote-load-deny", eName: "remote-load-mix-fail"},
				{tName: "remote-load-success-local-file", eName: "remote-load-mix-success"}},
			wantNack: true,
		},
		{
			name:       "remote-load-fail-open",
			input:      []configSpec{{tName: "remote-load-fail-open"}},
			wantOutput: []configSpec{{tName: "remote-load-allow"}},
			wantNack:   false,
		},
		{
			name:       "no-typed-struct",
			input:      []configSpec{{tName: "empty"}},
			wantOutput: []configSpec{{tName: "empty"}},
			wantNack:   false,
		},
		{
			name:       "no-wasm",
			input:      []configSpec{{tName: "no-wasm"}},
			wantOutput: []configSpec{{tName: "no-wasm"}},
			wantNack:   false,
		},
		{
			name:       "no-remote-load",
			input:      []configSpec{{tName: "no-remote-load"}},
			wantOutput: []configSpec{{tName: "no-remote-load"}},
			wantNack:   false,
		},
		{
			name:       "no-http-uri",
			input:      []configSpec{{tName: "no-http-uri"}},
			wantOutput: []configSpec{{tName: "remote-load-deny"}},
			wantNack:   true,
		},
		{
			name:       "remote-load-secret",
			input:      []configSpec{{tName: "remote-load-secret"}},
			wantOutput: []configSpec{{tName: "remote-load-success-local-file"}},
			wantNack:   false,
		},
		{
			// First part of "fail after success test"
			// This should run before the test below
			name:       "remote-load-fail-after-success-1",
			input:      []configSpec{{tName: "remote-load-success", eName: "fail-after-success"}},
			wantOutput: []configSpec{{tName: "remote-load-success-local-file", eName: "fail-after-success"}},
			wantNack:   false,
		},
		{
			// Second part of "fail after success test"
			// This should run after the test above
			//
			// After success for a filter, ECDS should not be sent if downloading fails.
			name: "remote-load-fail-after-success-2",
			input: []configSpec{
				{tName: "remote-load-fail", eName: "fail-after-success"},
				{tName: "remote-load-success", eName: "success-anyway"}},
			wantOutput: []configSpec{{tName: "remote-load-success-local-file", eName: "success-anyway"}},
			wantNack:   true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resources := make([]*anypb.Any, 0, len(c.input))
			for _, input := range c.input {
				if len(input.eName) == 0 {
					input.eName = c.name
				}
				resources = append(resources, protoconv.MessageToAny(getExtensionConfig(t, input)))
			}
			mc := &mockCache{}
			converted, gotNack := MaybeConvertWasmExtensionConfig(resources, mc)
			if len(converted) != len(c.wantOutput) {
				t.Fatalf("wasm config conversion number of configuration got %v want %v", len(converted), len(c.wantOutput))
			}
			for i, output := range converted {
				ec := &core.TypedExtensionConfig{}
				if err := output.UnmarshalTo(ec); err != nil {
					t.Errorf("wasm config conversion output %v failed to unmarshal", output)
					continue
				}
				if len(c.wantOutput[i].eName) == 0 {
					c.wantOutput[i].eName = c.name
				}
				if !proto.Equal(ec, getExtensionConfig(t, c.wantOutput[i])) {
					t.Errorf("wasm config conversion output index %d got %v want %v", i, ec, c.wantOutput[i])
				}
			}
			if gotNack != c.wantNack {
				t.Errorf("wasm config conversion send nack got %v want %v", gotNack, c.wantNack)
			}
		})
	}
}
func buildTypedStructExtensionConfig(wasm *wasm.Wasm) *core.TypedExtensionConfig {
	ws, _ := conversion.MessageToStruct(wasm)
	return &core.TypedExtensionConfig{
		TypedConfig: protoconv.MessageToAny(
			&udpa.TypedStruct{
				TypeUrl: xds.WasmHTTPFilterType,
				Value:   ws,
			},
		),
	}
}

func buildAnyExtensionConfig(msg proto.Message) *core.TypedExtensionConfig {
	return &core.TypedExtensionConfig{
		TypedConfig: protoconv.MessageToAny(msg),
	}
}

type configSpec struct {
	tName string
	eName string
}

func getExtensionConfig(t *testing.T, ioParam configSpec) *core.TypedExtensionConfig {
	tc := proto.Clone(extensionConfigTemplateMap[ioParam.tName]).(*core.TypedExtensionConfig)
	if tc == nil {
		t.Fatalf("configSpec %+v is not valid", t)
	}
	tc.Name = ioParam.eName
	return tc
}

var extensionConfigTemplateMap = map[string]*core.TypedExtensionConfig{
	"empty": {
		Name: "empty",
		TypedConfig: protoconv.MessageToAny(
			&structpb.Struct{},
		),
	},
	"no-wasm": {
		Name: "no-wasm",
		TypedConfig: protoconv.MessageToAny(
			&udpa.TypedStruct{TypeUrl: resource.APITypePrefix + "sometype"},
		),
	},
	"no-remote-load": buildTypedStructExtensionConfig(&wasm.Wasm{
		Config: &v3.PluginConfig{
			Vm: &v3.PluginConfig_VmConfig{
				VmConfig: &v3.VmConfig{
					Runtime: "envoy.wasm.runtime.null",
					Code: &core.AsyncDataSource{Specifier: &core.AsyncDataSource_Local{
						Local: &core.DataSource{
							Specifier: &core.DataSource_InlineString{
								InlineString: "envoy.wasm.metadata_exchange",
							},
						},
					}},
				},
			},
		},
	}),
	"no-http-uri": buildTypedStructExtensionConfig(&wasm.Wasm{
		Config: &v3.PluginConfig{
			Vm: &v3.PluginConfig_VmConfig{
				VmConfig: &v3.VmConfig{
					Code: &core.AsyncDataSource{Specifier: &core.AsyncDataSource_Remote{
						Remote: &core.RemoteDataSource{},
					}},
				},
			},
		},
	}),
	"remote-load-success": buildTypedStructExtensionConfig(&wasm.Wasm{
		Config: &v3.PluginConfig{
			Vm: &v3.PluginConfig_VmConfig{
				VmConfig: &v3.VmConfig{
					Code: &core.AsyncDataSource{Specifier: &core.AsyncDataSource_Remote{
						Remote: &core.RemoteDataSource{
							HttpUri: &core.HttpUri{
								Uri: "http://test?module=test.wasm",
							},
						},
					}},
				},
			},
		},
	}),
	"remote-load-success-local-file": buildAnyExtensionConfig(&wasm.Wasm{
		Config: &v3.PluginConfig{
			Vm: &v3.PluginConfig_VmConfig{
				VmConfig: &v3.VmConfig{
					Code: &core.AsyncDataSource{Specifier: &core.AsyncDataSource_Local{
						Local: &core.DataSource{
							Specifier: &core.DataSource_Filename{
								Filename: "test.wasm",
							},
						},
					}},
				},
			},
		},
	}),
	"remote-load-fail": buildTypedStructExtensionConfig(&wasm.Wasm{
		Config: &v3.PluginConfig{
			Vm: &v3.PluginConfig_VmConfig{
				VmConfig: &v3.VmConfig{
					Code: &core.AsyncDataSource{Specifier: &core.AsyncDataSource_Remote{
						Remote: &core.RemoteDataSource{
							HttpUri: &core.HttpUri{
								Uri: "http://test?module=test.wasm&error=download-error",
							},
						},
					}},
				},
			},
		},
	}),
	"remote-load-fail-open": buildTypedStructExtensionConfig(&wasm.Wasm{
		Config: &v3.PluginConfig{
			Vm: &v3.PluginConfig_VmConfig{
				VmConfig: &v3.VmConfig{
					Code: &core.AsyncDataSource{Specifier: &core.AsyncDataSource_Remote{
						Remote: &core.RemoteDataSource{
							HttpUri: &core.HttpUri{
								Uri: "http://test?module=test.wasm&error=download-error",
							},
						},
					}},
				},
			},
			FailOpen: true,
		},
	}),
	"remote-load-allow": buildAnyExtensionConfig(&rbac.RBAC{}),
	"remote-load-deny":  buildAnyExtensionConfig(denyWasmHTTPFilter),
	"remote-load-secret": buildTypedStructExtensionConfig(&wasm.Wasm{
		Config: &v3.PluginConfig{
			Vm: &v3.PluginConfig_VmConfig{
				VmConfig: &v3.VmConfig{
					Code: &core.AsyncDataSource{Specifier: &core.AsyncDataSource_Remote{
						Remote: &core.RemoteDataSource{
							HttpUri: &core.HttpUri{
								Uri: "http://test?module=test.wasm",
							},
						},
					}},
					EnvironmentVariables: &v3.EnvironmentVariables{
						KeyValues: map[string]string{
							model.WasmSecretEnv: "secret",
						},
					},
				},
			},
		},
	}),
}
