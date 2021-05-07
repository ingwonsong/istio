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
	pluginInstallsCountName = "plugin_installs_count"
	installStateName        = "install_state"
	raceRepairsCountName    = "race_repairs_count"
)

var (
	stateHookTag  = tag.MustNewKey("state")
	resultHookTag = tag.MustNewKey("result")
)

type cniRecordHook struct{}

func (r cniRecordHook) OnRecordInt64Measure(i *stats.Int64Measure, tags []tag.Mutator, value int64) {
	switch i.Name() {
	case pluginInstallsCountName:
		onPluginInstallCount(i, tags, value)
	case installStateName:
		onInstallState(i, tags)
	case raceRepairsCountName:
		onRaceRepairsCount(i, tags, value)
	}
}

var _ monitoring.RecordHook = &cniRecordHook{}

func (r cniRecordHook) OnRecordFloat64Measure(f *stats.Float64Measure, tags []tag.Mutator, value float64) {
	panic("OnRecordFloat64Measure: implement me")
}

func registerHook() {
	hook := cniRecordHook{}
	monitoring.RegisterRecordHook(pluginInstallsCountName, hook)
	monitoring.RegisterRecordHook(installStateName, hook)
	monitoring.RegisterRecordHook(raceRepairsCountName, hook)
}

func onPluginInstallCount(_ *stats.Int64Measure, tags []tag.Mutator, value int64) {
	tm := getOriginalTagMap(tags)
	if tm == nil {
		return
	}
	r, found := tm.Value(resultHookTag)
	if !found {
		return
	}

	pluginInstallCount.With(resultLabel.Value(r)).RecordInt(value)
}

func onInstallState(_ *stats.Int64Measure, tags []tag.Mutator) {
	tm := getOriginalTagMap(tags)
	if tm == nil {
		return
	}
	s, found := tm.Value(stateHookTag)
	if !found {
		return
	}
	installState.With(stateLabel.Value(s)).Increment()
}

func onRaceRepairsCount(_ *stats.Int64Measure, tags []tag.Mutator, value int64) {
	tm := getOriginalTagMap(tags)
	if tm == nil {
		return
	}
	r, found := tm.Value(resultHookTag)
	if !found {
		return
	}
	raceRepairsCount.With(resultLabel.Value(r)).RecordInt(value)
}

func getOriginalTagMap(tags []tag.Mutator) *tag.Map {
	originalCtx, err := tag.New(context.Background(), tags...)
	if err != nil {
		log.Warn("fail to initialize original tag context")
		return nil
	}
	return tag.FromContext(originalCtx)
}
