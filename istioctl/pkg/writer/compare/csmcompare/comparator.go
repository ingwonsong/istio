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

package csmcompare

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	cluster "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	listener "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	route "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	tlspb "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/tls/v3"
	csds "github.com/envoyproxy/go-control-plane/envoy/service/status/v3"
	"github.com/pmezard/go-difflib/difflib"
	anypb "google.golang.org/protobuf/types/known/anypb"

	"istio.io/istio/istioctl/pkg/util/configdump"
	v3 "istio.io/istio/pilot/pkg/xds/v3"
	"istio.io/istio/pkg/log"
	"istio.io/istio/pkg/slices"
)

// Comparator prints diffs between a config dump from TD and one from Envoy

const (
	// Cluster config types
	upstreamTLSContextType   = "type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.UpstreamTlsContext"
	downstreamTLSContextType = "type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.DownstreamTlsContext"
	metadataExchangeType     = "type.googleapis.com/envoy.tcp.metadataexchange.config.MetadataExchange"

	// Listener config types
	envoyFiltersNetworkWasmType           = "type.googleapis.com/envoy.extensions.filters.network.wasm.v3.Wasm"
	envoyFiltersHTTPConnectionManagerType = "type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager"
)

type Comparator struct {
	envoy        *configdump.Wrapper
	w            io.Writer
	context      int
	location     string
	tdConfigDump *configDump
}

type configDump struct {
	tdClusterConfigs  []*cluster.Cluster
	tdListenerConfigs []*listener.Listener
	tdRouteConfigs    []*route.RouteConfiguration
}

// Diff prints a diff between TD and Envoy to the passed writer
func (c *Comparator) Diff() error {
	if err := c.ClusterDiff(); err != nil {
		return err
	} else if err = c.ListenerDiff(); err != nil {
		return err
	}
	return c.RouteDiff()
}

// NewXdsComparator is a comparator constructor
func NewComparator(w io.Writer, csdsResponses map[string]*csds.ClientStatusResponse, envoyResponse []byte) (*Comparator, error) {
	envoyDump := &configdump.Wrapper{}
	err := json.Unmarshal(envoyResponse, envoyDump)
	if err != nil {
		return nil, err
	}
	return &Comparator{
		envoy:        envoyDump,
		w:            w,
		context:      7,
		location:     "Local", // the time.Location for formatting time.Time instances
		tdConfigDump: extractTDConfigDump(csdsResponses),
	}, nil
}

// CSDS API wraps the config dump inside genericXdsConfigs; to make it comparable to envoy,
// unmarshal it to Cluster, Listener, Route config.
func extractTDConfigDump(csdsResponses map[string]*csds.ClientStatusResponse) *configDump {
	cd := &configDump{
		tdClusterConfigs:  []*cluster.Cluster{},
		tdListenerConfigs: []*listener.Listener{},
		tdRouteConfigs:    []*route.RouteConfiguration{},
	}
	for _, resp := range csdsResponses {
		for _, gc := range resp.GetConfig()[0].GetGenericXdsConfigs() {
			if gc.GetTypeUrl() == v3.ClusterType {
				cluster := &cluster.Cluster{}
				if gc.GetXdsConfig() == nil {
					// no xds config, which is possible in CSDS API response
					continue
				}
				err := gc.GetXdsConfig().UnmarshalTo(cluster)
				if err != nil {
					log.Warnf("Failed to unmarshal cluster", err)
					continue
				}
				cd.tdClusterConfigs = append(cd.tdClusterConfigs, cluster)
			} else if gc.GetTypeUrl() == v3.ListenerType {
				l := &listener.Listener{}
				if gc.GetXdsConfig() == nil {
					// no xds config
					continue
				}
				err := gc.GetXdsConfig().UnmarshalTo(l)
				if err != nil {
					log.Warnf("Failed to unmarshal listener", err)
					continue
				}
				// gc.VersionInfo is not needed because VersionInfo is stripped from istio envoy config dump
				cd.tdListenerConfigs = append(cd.tdListenerConfigs, l)
			} else if gc.GetTypeUrl() == v3.RouteType {
				r := &route.RouteConfiguration{}
				if gc.GetXdsConfig() == nil {
					// no xds config
					continue
				}
				err := gc.GetXdsConfig().UnmarshalTo(r)
				if err != nil {
					log.Warnf("Failed to unmarshal listener", err)
					continue
				}
				cd.tdRouteConfigs = append(cd.tdRouteConfigs, r)
			}
		}
	}
	return cd
}

func (c *Comparator) ClusterDiff() error {
	if len(c.tdConfigDump.tdClusterConfigs) == 0 {
		return nil
	}
	isIdentical := true
	envoyClusterDump, err := c.envoy.GetDynamicClusterDump(true)
	if err != nil {
		return err
	}
	envoyConfigs := map[string]*cluster.Cluster{}
	for _, e := range envoyClusterDump.DynamicActiveClusters {
		cluster := &cluster.Cluster{}
		err = e.Cluster.UnmarshalTo(cluster)
		if err != nil {
			return err
		}
		envoyConfigs[cluster.Name] = cluster
	}

	for _, clusterConfig := range c.tdConfigDump.tdClusterConfigs {
		name := clusterConfig.Name
		envoyConfig, ok := envoyConfigs[name]
		if !ok {
			continue
		}
		// unmarshal the config TransportSocket field specifically for both envoy and td
		for _, ts := range append(envoyConfig.GetTransportSocketMatches(), clusterConfig.GetTransportSocketMatches()...) {
			if ts.GetTransportSocket() != nil && ts.GetTransportSocket().GetTypedConfig() != nil {
				tlsb := ts.GetTransportSocket().GetTypedConfig()
				if tlsb.TypeUrl == upstreamTLSContextType {
					tlsContext := &tlspb.UpstreamTlsContext{}
					if err := tlsb.UnmarshalTo(tlsContext); err != nil {
						log.Errorf("Unable to unmarshal to tls", err)
						continue
					}
					ntlsb, err := anypb.New(tlsContext)
					if err != nil {
						log.Errorf("Unable to unmarshal to tls", err)
						continue
					}
					ts.GetTransportSocket().ConfigType = &corev3.TransportSocket_TypedConfig{
						TypedConfig: ntlsb,
					}
				} else if tlsb.TypeUrl == downstreamTLSContextType {
					tlsContext := &tlspb.DownstreamTlsContext{}
					if err := tlsb.UnmarshalTo(tlsContext); err != nil {
						log.Errorf("Unable to unmarshal to tls", err)
						continue
					}
					ntlsb, err := anypb.New(tlsContext)
					if err != nil {
						log.Errorf("Unable to unmarshal to tls", err)
						continue
					}
					ts.GetTransportSocket().ConfigType = &corev3.TransportSocket_TypedConfig{
						TypedConfig: ntlsb,
					}
				}
			}
		}

		// Remove MetadataExchange. It's not useful because
		// TD always returns protocol:  "istio-peer-exchange"
		// Envoy always returns empty
		for _, f := range append(envoyConfig.GetFilters(), clusterConfig.GetFilters()...) {
			if f.TypedConfig.GetTypeUrl() == metadataExchangeType {
				f.GetTypedConfig().Value = []byte{}
			}
		}

		// TODO(siyiwang): Figure out Unable to use protomarshal.ToJsonWithIndent() returning errors
		// compare
		envoyBytes, err := json.MarshalIndent(envoyConfig, "", "\t")
		if err != nil {
			log.Errorf("Failed to parse envoy", err)
			continue
		}
		tdBytes, _ := json.MarshalIndent(clusterConfig, "", "\t")
		if err != nil {
			log.Errorf("Failed to parse csds cluster config", err)
			continue
		}

		diff := difflib.UnifiedDiff{
			FromFile: "TD Configs",
			A:        difflib.SplitLines(string(tdBytes)),
			ToFile:   "Envoy Configs",
			B:        difflib.SplitLines(string(envoyBytes)),
			Context:  c.context,
		}
		text, err := difflib.GetUnifiedDiffString(diff)
		if err != nil {
			continue
		}
		if text != "" {
			if isIdentical {
				isIdentical = false
				fmt.Fprintln(c.w, "Cluster resources config diff prints below:")
			}
			fmt.Fprintln(c.w, "Printing cluster config diff for: "+envoyConfig.Name)
			fmt.Fprintln(c.w, text)
		}
	}
	if isIdentical {
		fmt.Fprintln(c.w, "Cluster resources are identical between envoy and the control plane")
	}
	return nil
}

func (c *Comparator) ListenerDiff() error {
	if len(c.tdConfigDump.tdListenerConfigs) == 0 {
		return nil
	}
	isIdentical := true
	fmt.Fprintln(c.w, "Listener resources config diff prints below:")
	envoyListenerDump, err := c.envoy.GetDynamicListenerDump(true)
	if err != nil {
		return err
	}
	// Extract the listener config dump content
	envoyConfigs := map[string]*listener.Listener{}
	for _, e := range envoyListenerDump.DynamicListeners {
		listener := &listener.Listener{}
		err = e.ActiveState.Listener.UnmarshalTo(listener)
		if err != nil {
			return err
		}
		envoyConfigs[listener.Name] = listener
	}
	for _, listenerConfig := range c.tdConfigDump.tdListenerConfigs {
		envoyConfig, ok := envoyConfigs[listenerConfig.Name]
		if !ok {
			continue
		}

		for _, f := range append(envoyConfig.GetFilterChains(), listenerConfig.GetFilterChains()...) {
			for _, c := range f.GetFilters() {
				if c.GetTypedConfig().GetTypeUrl() == envoyFiltersNetworkWasmType ||
					c.GetTypedConfig().GetTypeUrl() == envoyFiltersHTTPConnectionManagerType ||
					c.GetTypedConfig().GetTypeUrl() == metadataExchangeType {
					// Even the config is synced, this is different. Skip this check
					c.GetTypedConfig().Value = []byte{}
				}
			}

			if f.GetTransportSocket() != nil && f.GetTransportSocket().GetTypedConfig() != nil {
				if f.GetTransportSocket().GetTypedConfig().GetTypeUrl() == upstreamTLSContextType ||
					f.GetTransportSocket().GetTypedConfig().GetTypeUrl() == downstreamTLSContextType {
					// Even the config is synced, this is different. Skip this check for now
					f.GetTransportSocket().GetTypedConfig().Value = []byte{}
				}
			}
		}

		for _, f := range append(envoyConfig.GetDefaultFilterChain().GetFilters(), listenerConfig.GetDefaultFilterChain().GetFilters()...) {
			if f.GetTypedConfig().GetTypeUrl() == envoyFiltersNetworkWasmType ||
				f.GetTypedConfig().GetTypeUrl() == envoyFiltersHTTPConnectionManagerType ||
				f.GetTypedConfig().GetTypeUrl() == metadataExchangeType {
				// Even the config is synced, this is different. Skip this check for now
				f.GetTypedConfig().Value = []byte{}
			}
		}

		envoyBytes, err := json.MarshalIndent(envoyConfig, "", "\t")
		if err != nil {
			log.Errorf("Failed to parse envoy", err)
			continue
		}

		tdBytes, err := json.MarshalIndent(listenerConfig, "", "\t")
		if err != nil {
			log.Errorf("Failed to parse csds listener config", err)
			continue
		}

		diff := difflib.UnifiedDiff{
			FromFile: "TD Configs",
			A:        difflib.SplitLines(string(tdBytes)),
			ToFile:   "Envoy Configs",
			B:        difflib.SplitLines(string(envoyBytes)),
			Context:  c.context,
		}
		text, err := difflib.GetUnifiedDiffString(diff)
		if err != nil {
			return err
		}
		if text != "" {
			if isIdentical {
				isIdentical = false
				fmt.Fprintln(c.w, "Listener resources config diff prints below:")
			}
			fmt.Fprintln(c.w, "Printing listener config diff for: "+envoyConfig.Name)
			fmt.Fprintln(c.w, text)
		} else {
			fmt.Fprintln(c.w, "Listener Config Match: "+envoyConfig.Name)
		}
	}
	if isIdentical {
		fmt.Fprintln(c.w, "Listener resources are identical between envoy and the control plane")
	}
	return nil
}

func (c *Comparator) RouteDiff() error {
	if len(c.tdConfigDump.tdRouteConfigs) == 0 {
		return nil
	}
	isIdentical := true
	fmt.Fprintln(c.w, "Route resources config diff prints below: ")
	envoyRouteDump, err := c.envoy.GetDynamicRouteDump(true)
	if err != nil {
		return err
	}
	// Extract the route config dump content
	envoyConfigs := map[string]*route.RouteConfiguration{}
	for _, e := range envoyRouteDump.DynamicRouteConfigs {
		r := &route.RouteConfiguration{}
		if err = e.RouteConfig.UnmarshalTo(r); err != nil {
			return err
		}
		envoyConfigs[r.Name] = r
	}
	for _, routeConfig := range c.tdConfigDump.tdRouteConfigs {
		envoyConfig, ok := envoyConfigs[routeConfig.Name]
		if !ok {
			continue
		}

		// Specific processing before certain config fields before comparing
		if envoyConfig.GetVirtualHosts() != nil && routeConfig.GetVirtualHosts() != nil {
			slices.SortFunc(envoyConfig.GetVirtualHosts(), compareVirtualHost)
			slices.SortFunc(routeConfig.GetVirtualHosts(), compareVirtualHost)
		}

		for _, f := range append(envoyConfig.GetVirtualHosts(), routeConfig.GetVirtualHosts()...) {
			for _, c := range f.GetRoutes() {
				// Even the config is synced, this is different. Skip this check for now
				c.TypedPerFilterConfig = nil
			}
		}

		envoyBytes, err := json.MarshalIndent(envoyConfig, "", "\t")
		if err != nil {
			log.Errorf("Failed to parse envoy", err)
			continue
		}

		tdBytes, err := json.MarshalIndent(routeConfig, "", "\t")
		if err != nil {
			log.Errorf("Failed to parse csds route config", err)
			continue
		}

		diff := difflib.UnifiedDiff{
			FromFile: "TD Configs",
			A:        difflib.SplitLines(string(tdBytes)),
			ToFile:   "Envoy Configs",
			B:        difflib.SplitLines(string(envoyBytes)),
			Context:  c.context,
		}
		text, err := difflib.GetUnifiedDiffString(diff)
		if err != nil {
			return err
		}
		if text != "" {
			if isIdentical {
				isIdentical = false
				fmt.Fprintln(c.w, "Route resources config diff prints below:")
			}
			fmt.Fprintln(c.w, "Printing route config diff for: "+envoyConfig.Name)
			fmt.Fprintln(c.w, text)
		} else {
			fmt.Fprintln(c.w, "Route Config Match: "+envoyConfig.Name)
		}
	}
	if isIdentical {
		fmt.Fprintln(c.w, "Route resources are identical between envoy and the control plane")
	}
	return nil
}

func compareVirtualHost(i, j *route.VirtualHost) int {
	return strings.Compare(i.Name, j.Name)
}
