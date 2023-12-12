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

// Package metrics defines metrics and monitoring functionality
// used throughout operator.
package metrics

import (
	"istio.io/istio/pkg/log"
	"istio.io/istio/pkg/monitoring"
)

const hourInMS = 3600000

// For stackdriver metrics report. New metrics must be added to
var (
	proxyVersionLabel     = monitoring.CreateLabel("proxy_version")
	fromProxyVersionLabel = monitoring.CreateLabel("from_proxy_version")
	toProxyVersionLabel   = monitoring.CreateLabel("to_proxy_version")
	resultLabel           = monitoring.CreateLabel("result")
	ownerLabel            = monitoring.CreateLabel("owner")
	stateLabel            = monitoring.CreateLabel("state")
	revisionLabel         = monitoring.CreateLabel("revision")
	errorReasonLabel      = monitoring.CreateLabel("error_reason")

	proxyPercentageTarget = monitoring.NewGauge(
		"proxy_percentage_targets",
		"Expected percentages of each proxy version",
	)

	proxies = monitoring.NewGauge(
		"proxies",
		"Count of the proxies watched by MDP controller",
	)

	reconcileLoopsCount = monitoring.NewSum(
		"reconcile_loops_count",
		"Count of the MDP controller reconcile loops",
	)

	rebuildCacheCount = monitoring.NewSum(
		"rebuild_cache_count",
		"Count of the pod cache rebuild events",
	)

	upgradedProxiesCount = monitoring.NewSum(
		"upgraded_proxies_count",
		"Count of the proxies upgraded by MDP controller",
	)

	reconcileState = monitoring.NewGauge(
		"reconcile_state",
		"MDP controller reconcile state",
	)

	servingState = monitoring.NewGauge(
		"serving_state",
		"MDP controller serving state",
	)

	// TODO(iamwen): replace with derivedGauge after deprecating exportView: b/190761802
	upTime = monitoring.NewGauge(
		"uptime",
		"Uptime of the MDP Controller in seconds",
	)

	reconcileDuration = monitoring.NewDistribution(
		"reconcile_duration",
		"The time that the MDP controller takes to reconcile a cluster to target provy version basis points",
		[]float64{
			1 * hourInMS, 2 * hourInMS, 4 * hourInMS, 8 * hourInMS,
			16 * hourInMS, 32 * hourInMS, 64 * hourInMS,
		},
		monitoring.WithUnit(monitoring.Milliseconds),
	)

	removePodCount = monitoring.NewSum(
		"remove_pod",
		"Count of pod removal events",
	)

	addPodCount = monitoring.NewSum(
		"add_pod",
		"Count of pod addition events",
	)

	rsMetrics = []monitoring.Metric{
		proxyPercentageTarget,
		proxies,
		reconcileLoopsCount,
		upgradedProxiesCount,
		reconcileState,
		servingState,
		upTime,
		reconcileDuration,
		removePodCount,
		addPodCount,
	}
	viewMap = make(map[string]bool)
)

func registerMetrics() error {
	for _, m := range rsMetrics {
		log.Infof("registering MDP metric %s", m.Name())
		if err := m.Register(); err != nil {
			log.Errorf("error registering MDP metric %s: %s", m.Name(), err.Error())
			return err
		}
	}

	return nil
}

func init() {
	for _, mc := range rsMetrics {
		viewMap[mc.Name()] = true
	}
}
