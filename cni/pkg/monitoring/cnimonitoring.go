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
package monitoring

import (
	"istio.io/pkg/monitoring"
)

// for stackdriver metrics report
var (
	stateLabel  = monitoring.MustCreateLabel("state")
	resultLabel = monitoring.MustCreateLabel("result")

	pluginInstallCount = monitoring.NewSum(
		"plugin_installs_count_measure",
		"Count of Istio CNI network plugin installations done by an Istio CNI daemonset.",
		monitoring.WithLabels(stateLabel),
	)

	installState = monitoring.NewGauge(
		"install_state_measure",
		"The CNI plugin installation state, one of [READY, UNREADY, UNKNOWN]",
		monitoring.WithLabels(resultLabel),
	)

	raceRepairsCount = monitoring.NewSum(
		"race_repairs_count_measure",
		"Count of pods which are stuck at Istio CNI race condition and repaired by an Istio CNI daemonset",
		monitoring.WithLabels(resultLabel),
	)
	rsMetrics = []monitoring.Metric{
		raceRepairsCount,
		installState,
		pluginInstallCount,
	}
	viewMap = make(map[string]bool)
)

func init() {
	for _, mc := range rsMetrics {
		monitoring.MustRegister(mc)
		viewMap[mc.Name()] = true
	}

	registerHook()
}
