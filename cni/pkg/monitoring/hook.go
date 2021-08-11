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

package monitoring

import (
	"context"

	"go.opencensus.io/stats"
	"go.opencensus.io/tag"

	"istio.io/pkg/log"
	"istio.io/pkg/monitoring"
)

const (
	// These are the metrics name defined in OSS
	pluginInstallsCountName = "istio_cni_installs_total"
	installReadyName        = "istio_cni_install_ready"
	raceRepairsCountName    = "istio_cni_repair_pods_repaired_total"

	installReadyState   = "READY"
	installUnreadyState = "UNREADY"
	installUnknownState = "UNKNOWN"
)

var resultHookTag = tag.MustNewKey("result")

type cniRecordHook struct{}

func (r cniRecordHook) OnRecordInt64Measure(i *stats.Int64Measure, tags []tag.Mutator, value int64) {
	log.Debugf("CNI metric: %s does not have corresponding hook", i.Name())
}

var _ monitoring.RecordHook = &cniRecordHook{}

func (r cniRecordHook) OnRecordFloat64Measure(f *stats.Float64Measure, tags []tag.Mutator, value float64) {
	switch f.Name() {
	case pluginInstallsCountName:
		onPluginInstallCount(f, tags, value)
	case installReadyName:
		onInstallReady(value)
	case raceRepairsCountName:
		onRaceRepairsCount(f, tags, value)
	}
}

func registerHook() {
	hook := cniRecordHook{}
	monitoring.RegisterRecordHook(pluginInstallsCountName, hook)
	monitoring.RegisterRecordHook(installReadyName, hook)
	monitoring.RegisterRecordHook(raceRepairsCountName, hook)
}

func onPluginInstallCount(_ *stats.Float64Measure, tags []tag.Mutator, value float64) {
	tm := getOriginalTagMap(tags)
	if tm == nil {
		return
	}
	r, found := tm.Value(resultHookTag)
	if !found {
		return
	}

	pluginInstallCount.With(resultLabel.Value(r)).RecordInt(int64(value))
}

func onInstallReady(value float64) {
	state := installUnknownState
	if value == 1 {
		state = installReadyState
	} else if value == 0 {
		state = installUnreadyState
	}

	for _, s := range []string{
		installReadyState, installUnreadyState, installUnknownState,
	} {
		v := 0
		if s == state {
			v = 1
		}
		installState.
			With(stateLabel.Value(s)).
			RecordInt(int64(v))
	}
}

func onRaceRepairsCount(_ *stats.Float64Measure, tags []tag.Mutator, value float64) {
	tm := getOriginalTagMap(tags)
	if tm == nil {
		return
	}
	r, found := tm.Value(resultHookTag)
	if !found {
		return
	}
	// TODO: type label is absent in SD now
	raceRepairsCount.With(resultLabel.Value(r)).RecordInt(int64(value))
}

func getOriginalTagMap(tags []tag.Mutator) *tag.Map {
	originalCtx, err := tag.New(context.Background(), tags...)
	if err != nil {
		log.Warn("fail to initialize original tag context")
		return nil
	}
	return tag.FromContext(originalCtx)
}
