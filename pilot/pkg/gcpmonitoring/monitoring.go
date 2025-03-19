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

package gcpmonitoring

import (
	"go.opencensus.io/stats"      // nolint: depguard
	"go.opencensus.io/stats/view" // nolint: depguard
	"go.opencensus.io/tag"        // nolint: depguard

	"istio.io/istio/pkg/env"
	"istio.io/istio/pkg/log"
	"istio.io/istio/pkg/monitoring"
)

var (
	operationKey    = tag.MustNewKey("operation")
	successKey      = tag.MustNewKey("success")
	successLabel    = monitoring.CreateLabel("success")
	proxyVersionKey = tag.MustNewKey("proxy_version")
	typeKey         = tag.MustNewKey("type")

	// EnableSD will be overridden in pilot-discovery/mcp.go
	EnableSD = env.RegisterBoolVar("ENABLE_STACKDRIVER_MONITORING", false,
		"controls whether to enable control plane stackdriver monitoring, specifiec by GCP profile").Get()
	// used for testing stackdriver monitoring in nonprod environment
	endpointOverride = env.RegisterStringVar("MONITORING_ENDPOINT_OVERRIDE", "",
		"controls override of the default monitoring API endpoint")

	// TODO(bianpengyuan) use monitoring pkg from istio.io/istio/pkg instead opencensus interface once it support integer value.
	configEventMeasure        = stats.Int64("config_event_measure", "The number of user configuration events", "1")
	configValidationMeasuare  = stats.Int64("config_validation_measure", "The number of configuration validation events", "1")
	configPushMeasuare        = stats.Int64("config_push_measure", "The number of xds configuration pushes", "1")
	rejectedConfigMeasuare    = stats.Int64("rejected_config_measure", "The number of rejected configurations", "1")
	configConvergenceMeasuare = stats.Float64("config_convergence_measure",
		"The distribution of latency between application and activation for user configuration", "ms")
	proxyClientsMeasure                   = stats.Int64("proxy_client_measure", "The number of proxies connected to this instance", "1")
	sidecarInjectionMeasure               = stats.Int64("sidecar_injection_measure", "The number of sidecar injection events", "1")
	ipBasedRemoteSecretsMeasure           = stats.Int64("ip_based_remote_secrets", "The number of IP based remote secrets processed", "1")
	ipBasedRemoteSecretsTranslatedMeasure = stats.Int64("ip_based_remote_secrets_translated", "The number of IP based remote secrets translated", "1")

	configEventView = &view.View{
		Name:    "control/config_event_count",
		Measure: configEventMeasure,
		Description: "number of registry event count of resources watched by Istio control plane (e.g. VirtualService, DestinationRule, ServiceEntry)," +
			"operation includes `ADD`, `UPDATE`, `UPDATESAME`, and `DELETE`.",
		Aggregation: view.Sum(),
		TagKeys:     []tag.Key{operationKey, typeKey},
	}
	configValidationView = &view.View{
		Name:        "control/config_validation_count",
		Measure:     configValidationMeasuare,
		Description: "number of control plane validation events of resources watched by Istio control plane (e.g. VirtualService, DestinationRule, ServiceEntry)",
		Aggregation: view.Sum(),
		TagKeys:     []tag.Key{successKey, typeKey},
	}
	configPushView = &view.View{
		Name:        "control/config_push_count",
		Measure:     configPushMeasuare,
		Description: "number of config pushes",
		Aggregation: view.Sum(),
		TagKeys:     []tag.Key{successKey, typeKey},
	}
	rejectedConfigView = &view.View{
		Name:        "control/rejected_config_count",
		Measure:     rejectedConfigMeasuare,
		Description: "number of rejected config",
		Aggregation: view.Sum(),
		TagKeys:     []tag.Key{typeKey},
	}
	configConvergenceView = &view.View{
		Name:        "control/config_convergence_latencies",
		Measure:     configConvergenceMeasuare,
		Description: "Time (in seconds) until applied config is active",
		Aggregation: view.Distribution([]float64{
			0.128, 0.256, 0.512, 1, 2, 4, 8, 16, 32,
			64, 128, 256, 512, 1024, 2048, 4096,
		}...),
		TagKeys: []tag.Key{typeKey},
	}
	proxyClientsView = &view.View{
		Name:        "control/proxy_clients",
		Measure:     proxyClientsMeasure,
		Description: "Number of proxies connected to this instance",
		Aggregation: view.LastValue(),
		TagKeys:     []tag.Key{proxyVersionKey},
	}
	sidecarInjectionView = &view.View{
		Name:        "control/sidecar_injection_count",
		Measure:     sidecarInjectionMeasure,
		Description: "Number of sidecar injection event",
		Aggregation: view.Sum(),
		TagKeys:     []tag.Key{successKey},
	}
	ipBasedRemoteSecretsView = &view.View{
		Name:        "internal/ip_based_remote_secrets",
		Measure:     ipBasedRemoteSecretsMeasure,
		Description: "Number of IP based remote secrets processed",
		Aggregation: view.Sum(),
		TagKeys:     []tag.Key{},
	}
	ipBasedRemoteSecretsTranslatedView = &view.View{
		Name:        "internal/ip_based_remote_secrets_translated",
		Measure:     ipBasedRemoteSecretsTranslatedMeasure,
		Description: "Number of IP based remote secrets processed",
		Aggregation: view.Sum(),
		TagKeys:     []tag.Key{successKey},
	}

	cpViewMap = make(map[string]bool)
)

func registerView(v *view.View) {
	if err := view.Register(v); err != nil {
		log.Warnf("failed to register Opencensus view %v", v.Name)
	}
	cpViewMap[v.Name] = true
}

func init() {
	registerView(configEventView)
	registerView(configValidationView)
	registerView(configPushView)
	registerView(rejectedConfigView)
	registerView(configConvergenceView)
	registerView(proxyClientsView)
	registerView(sidecarInjectionView)
	registerView(ipBasedRemoteSecretsView)
	registerView(ipBasedRemoteSecretsTranslatedView)

	registerHook()
}
