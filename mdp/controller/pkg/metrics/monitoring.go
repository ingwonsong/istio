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
	"istio.io/pkg/monitoring"
)

const hourInMS = 3600000

// for stackdriver metrics report
var (
	proxyVersionLabel     = monitoring.MustCreateLabel("proxy_version")
	fromProxyVersionLabel = monitoring.MustCreateLabel("from_proxy_version")
	toProxyVersionLabel   = monitoring.MustCreateLabel("to_proxy_version")
	resultLabel           = monitoring.MustCreateLabel("result")
	ownerLabel            = monitoring.MustCreateLabel("owner")
	stateLabel            = monitoring.MustCreateLabel("state")
	revisionLabel         = monitoring.MustCreateLabel("revision")

	proxyPercentageTarget = monitoring.NewGauge(
		"proxy_percentage_targets",
		"Expected percentages of each proxy version",
		monitoring.WithLabels(proxyVersionLabel, revisionLabel),
	)
	proxies = monitoring.NewGauge(
		"proxies",
		"Count of the proxies watched by MDP controller",
		monitoring.WithLabels(proxyVersionLabel, ownerLabel, stateLabel, revisionLabel),
		monitoring.WithInt64Values(),
	)

	reconcileLoopsCount = monitoring.NewSum(
		"reconcile_loops_count",
		"Count of the MDP controller reconcile loops",
		monitoring.WithLabels(resultLabel, revisionLabel),
		monitoring.WithInt64Values(),
	)
	upgradedProxiesCount = monitoring.NewSum(
		"upgraded_proxies_count",
		"Count of the proxies upgraded by MDP controller",
		monitoring.WithLabels(fromProxyVersionLabel, toProxyVersionLabel, resultLabel, revisionLabel),
		monitoring.WithInt64Values(),
	)
	reconcileState = monitoring.NewGauge(
		"reconcile_state",
		"MDP controller reconcile state",
		monitoring.WithLabels(stateLabel, revisionLabel),
		monitoring.WithInt64Values(),
	)
	servingState = monitoring.NewGauge(
		"serving_state",
		"MDP controller serving state",
		monitoring.WithLabels(stateLabel),
		monitoring.WithInt64Values(),
	)
	// TODO(iamwen): replace with derivedGauge after deprecating exportView: b/190761802
	upTime = monitoring.NewGauge(
		"uptime",
		"Uptime of the MDP Controller in seconds",
		monitoring.WithInt64Values(),
	)
	reconcileDuration = monitoring.NewDistribution(
		"reconcile_duration",
		"The time that the MDP controller takes to reconcile a cluster to target provy version basis points",
		[]float64{
			1 * hourInMS, 2 * hourInMS, 4 * hourInMS, 8 * hourInMS,
			16 * hourInMS, 32 * hourInMS, 64 * hourInMS,
		},
		monitoring.WithLabels(revisionLabel),
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
	}
	viewMap = make(map[string]bool)
)

func init() {
	for _, mc := range rsMetrics {
		monitoring.MustRegister(mc)
		viewMap[mc.Name()] = true
	}
}
