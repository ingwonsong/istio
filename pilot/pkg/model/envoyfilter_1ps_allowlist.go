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
	"reflect"

	v1xdsudpatypepb "github.com/cncf/xds/go/udpa/type/v1"
	listener "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	lua "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/lua/v3"
	hcm "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	"google.golang.org/protobuf/proto"

	metav1alpha1 "istio.io/api/meta/v1alpha1"
	"istio.io/istio/pilot/pkg/model/status"
	"istio.io/istio/pkg/config"
	"istio.io/istio/pkg/monitoring"
)

type typeURL string

// List of fully and partially supported type URLs.
const (
	Lua                       typeURL = "type.googleapis.com/envoy.extensions.filters.http.lua.v3.Lua"
	Wasm                      typeURL = "type.googleapis.com/envoy.extensions.filters.http.wasm.v3.Wasm"
	GrpcWeb                   typeURL = "type.googleapis.com/envoy.extensions.filters.http.grpc_web.v3.GrpcWeb"
	ExtAuthz                  typeURL = "type.googleapis.com/envoy.extensions.filters.http.ext_authz.v3.ExtAuthz"
	GrpcJSONTranscoder        typeURL = "type.googleapis.com/envoy.extensions.filters.http.grpc_json_transcoder.v3.GrpcJsonTranscoder"
	GrpcFieldExtractionConfig typeURL = "type.googleapis.com/envoy.extensions.filters.http.grpc_field_extraction.v3.GrpcFieldExtractionConfig"
	HeaderToMetadata          typeURL = "type.googleapis.com/envoy.extensions.filters.http.header_to_metadata.v3.Config"
	HTTPConnectionManager     typeURL = "type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager"
	RateLimit                 typeURL = "type.googleapis.com/envoy.extensions.filters.http.ratelimit.v3.RateLimit"
	UDPATypedStruct           typeURL = "type.googleapis.com/udpa.type.v1.TypedStruct"

	supportedLuaScript string = `function envoy_on_response(response_handle)
  location = response_handle:headers():get("location")
  if location ~= nil and location:find("http:") ~= nil then
    response_handle:headers():replace("location", location:gsub("http:", "https:", 1))
  end
end
`
)

var (
	// Only select Lua patches are supported with other usages being blocked.
	supportedLuaPatch = &lua.Lua{
		InlineCode: supportedLuaScript,
	}
	// Only HTTP->SPDY upgrade patch is supported for the HCM filter.
	supportedHCMPatch = &hcm.HttpConnectionManager{
		UpgradeConfigs: []*hcm.HttpConnectionManager_UpgradeConfig{
			{
				UpgradeType: "SPDY/3.1",
			},
		},
	}
)

// Checks the incoming type URL and value against the allowlist patch types and their configuration.
func comparePatchAgainstAllowlist(url string, value []byte) (bool, error) {
	switch typeURL(url) {
	case Lua:
		inputConfig := &lua.Lua{}
		if err := proto.Unmarshal(value, inputConfig); err != nil {
			return false, fmt.Errorf("unmarshal value %q as Lua config: %v", string(value), err)
		}
		// Advanced comparison using syntax parsing is intentionally omitted here for simplicity,
		// especially since Lua scripts are planned to be migrated soon.
		// The `inlineCode` field is deprecated in newer versions of Envoy. However, this logic will be backported
		// to older Istio versions. Skip lint check here since the field is already in use (http://shortn/_uuFRAJ5CHX).
		return supportedLuaPatch.GetInlineCode() == inputConfig.GetInlineCode(), nil //nolint:all
	case HTTPConnectionManager:
		inputConfig := &hcm.HttpConnectionManager{}
		if err := proto.Unmarshal(value, inputConfig); err != nil {
			return false, fmt.Errorf("unmarshal value %q as HttpConnectionManager config: %v", string(value), err)
		}
		// Validate that the user-applied HCM patch doesn't have unsupported values and/or additional fields set
		// apart from the single supported HCM patch.
		return proto.Equal(supportedHCMPatch, inputConfig), nil
	case Wasm, GrpcWeb, ExtAuthz, GrpcJSONTranscoder, GrpcFieldExtractionConfig, HeaderToMetadata, RateLimit:
		// All configurations are supported for these extensions.
		return true, nil
	default:
		// No match found against supported 1P EnvoyFilters.
		return false, nil
	}
}

// Returns true if the incoming type url corresponds to a fully supported EnvoyFilter
// or if the url and value match a partially supported filter (Lua and HCM).
func validateTypeURLAndValue(url string, value []byte) (bool, error) {
	ok, err := comparePatchAgainstAllowlist(url, value)
	if !ok || (err != nil) {
		blockedPatches.With(patchTypeLabel.Value(url)).Increment()
	}
	return ok, err
}

func isSupportedHTTPFilterPatch(httpFilter *hcm.HttpFilter) (bool, error) {
	filterTypeURL := httpFilter.GetTypedConfig().TypeUrl
	filterValue := httpFilter.GetTypedConfig().Value
	if httpFilter.GetTypedConfig().TypeUrl == string(UDPATypedStruct) {
		tsProto := &v1xdsudpatypepb.TypedStruct{}
		var err error
		if err = proto.Unmarshal(httpFilter.GetTypedConfig().Value, tsProto); err != nil {
			return false, fmt.Errorf("unmarshal into UDPA typed struct: %v", err)
		}
		filterTypeURL = tsProto.GetTypeUrl()
		if filterValue, err = proto.Marshal(tsProto.GetValue()); err != nil {
			return false, fmt.Errorf("marshal UDPA typed struct value: %v", err)
		}
	}
	return validateTypeURLAndValue(filterTypeURL, filterValue)
}

func isPatchSupported(pw *EnvoyFilterConfigPatchWrapper) (bool, error) {
	httpFilter, ok := (pw.Value).(*hcm.HttpFilter)
	if ok {
		return isSupportedHTTPFilterPatch(httpFilter)
	}
	listenerFilter, ok := (pw.Value).(*listener.Filter)
	if ok {
		return validateTypeURLAndValue(listenerFilter.GetTypedConfig().TypeUrl, listenerFilter.GetTypedConfig().Value)
	}
	return false, fmt.Errorf("envoyfilter patch value %+v does not match any supported type", pw.Value)
}

func unsupportedPatchStatus(pw *EnvoyFilterConfigPatchWrapper) *metav1alpha1.IstioCondition {
	return &metav1alpha1.IstioCondition{
		Type:    fmt.Sprintf("Patch %+v is Ready", pw),
		Status:  "False",
		Message: "This EnvoyFilter patch is not supported for 1P services",
	}
}

const (
	envoyfilterPatchesTotal   = "envoyfilter_patches_total"
	envoyfilterPatchesBlocked = "envoyfilter_patches_blocked_total"

	envoyfilterPatchStatus    = "status"
	envoyfilterPatchType      = "patch_type"
	envoyfilterUpdateCRStatus = "update_cr_status"

	envoyfilterSuccessStatus      = "SUCCESS"
	envoyfilterBlockedByAllowlist = "BLOCKED_BY_ALLOWLIST"
	envoyfilterOtherError         = "OTHER_ERROR"
)

var (
	totalPatches = monitoring.NewSum(
		envoyfilterPatchesTotal,
		"Total number of EnvoyFilter Patches",
	)
	blockedPatches = monitoring.NewSum(
		envoyfilterPatchesBlocked,
		"Total number of EnvoyFilter Patches blocked",
	)
	statusLabel         = monitoring.CreateLabel(envoyfilterPatchStatus)
	patchTypeLabel      = monitoring.CreateLabel(envoyfilterPatchType)
	updateCRStatusLabel = monitoring.CreateLabel(envoyfilterUpdateCRStatus)
)

func enforce1PSEnvoyFilterAllowlist(env *Environment, efw *EnvoyFilterWrapper, envoyFilterConfig config.Config) config.Config {
	oldStatus := config.DeepCopy(envoyFilterConfig.Status)
	if oldStatus == nil {
		oldStatus = &metav1alpha1.IstioStatus{}
	}
	envoyFilterConfig.Status = &metav1alpha1.IstioStatus{}
	for applyTo, patchWrappers := range efw.Patches {
		var supportedPatchWrappers []*EnvoyFilterConfigPatchWrapper
		for _, pw := range patchWrappers {
			if ok, err := isPatchSupported(pw); err != nil {
				log.Errorf("Unable to determine supportability of patch %v: %v", pw, err)
				totalPatches.With(statusLabel.Value(envoyfilterOtherError)).Increment()
			} else if ok {
				supportedPatchWrappers = append(supportedPatchWrappers, pw)
				totalPatches.With(statusLabel.Value(envoyfilterSuccessStatus)).Increment()
			} else {
				log.Warnf("Blocking usage of unsupported Envoyfilter patch for 1P services: %+v", pw)
				envoyFilterConfig = status.UpdateConfigCondition(envoyFilterConfig, unsupportedPatchStatus(pw))
				totalPatches.With(statusLabel.Value(envoyfilterBlockedByAllowlist)).Increment()
			}
		}
		efw.Patches[applyTo] = supportedPatchWrappers
	}
	if !reflect.DeepEqual(envoyFilterConfig.Status, oldStatus) {
		if _, err := env.UpdateStatus(envoyFilterConfig); err != nil {
			log.Errorf("Update status for envoyfilter %v: %v", envoyFilterConfig, err)
			totalPatches.With(updateCRStatusLabel.Value(envoyfilterOtherError)).Increment()
		} else {
			totalPatches.With(updateCRStatusLabel.Value(envoyfilterSuccessStatus)).Increment()
		}
	}
	return envoyFilterConfig
}
