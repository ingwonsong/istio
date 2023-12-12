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

package metrics

import (
	"k8s.io/client-go/util/workqueue"

	"istio.io/istio/pkg/monitoring"
)

var (
	_ workqueue.MetricsProvider     = &metricsProvider{}
	_ workqueue.GaugeMetric         = &gaugeMetric{}
	_ workqueue.CounterMetric       = &counterMetric{}
	_ workqueue.HistogramMetric     = &histogramMetric{}
	_ workqueue.SettableGaugeMetric = &settableGaugeMetric{}
)

func NewMetricsProvider() workqueue.MetricsProvider {
	return new(metricsProvider)
}

// Implements workqueue.MetricsProvider, which generates various metrics used by the queue.
type metricsProvider struct{}

func (mp *metricsProvider) NewDepthMetric(name string) workqueue.GaugeMetric {
	return &gaugeMetric{
		monitoring.NewGauge(
			name,
			"Metrics provider depth.",
		),
	}
}

func (mp *metricsProvider) NewAddsMetric(name string) workqueue.CounterMetric {
	return &counterMetric{
		monitoring.NewSum(
			name,
			"Metrics provider adds.",
		),
	}
}

func (mp *metricsProvider) NewLatencyMetric(name string) workqueue.HistogramMetric {
	bounds := []float64{}
	for i := 0; i <= 16; i++ {
		val := 1 << i
		bounds = append(bounds, float64(val))
	}

	return &histogramMetric{
		monitoring.NewDistribution(
			name,
			"Metrics provider latency.",
			bounds,
		),
	}
}

func (mp *metricsProvider) NewWorkDurationMetric(name string) workqueue.HistogramMetric {
	bounds := []float64{}
	for i := 0; i <= 16; i++ {
		val := 1 << i
		bounds = append(bounds, float64(val))
	}

	return &histogramMetric{
		monitoring.NewDistribution(
			name,
			"Metrics provider duration.",
			bounds,
		),
	}
}

func (mp *metricsProvider) NewUnfinishedWorkSecondsMetric(name string) workqueue.SettableGaugeMetric {
	return &settableGaugeMetric{
		monitoring.NewGauge(
			name,
			"Metrics provider unfinished work seconds.",
		),
	}
}

func (mp *metricsProvider) NewLongestRunningProcessorSecondsMetric(name string) workqueue.SettableGaugeMetric {
	return &settableGaugeMetric{
		monitoring.NewGauge(
			name,
			"Metrics provider longest running processor.",
			monitoring.WithUnit(monitoring.Unit("seconds")),
		),
	}
}

func (mp *metricsProvider) NewRetriesMetric(name string) workqueue.CounterMetric {
	return &counterMetric{
		m: monitoring.NewSum(
			name,
			"Metrics provider retries metric.",
			monitoring.WithUnit(monitoring.Unit("retries"))),
	}
}

type gaugeMetric struct {
	m monitoring.Metric
}

func (gm *gaugeMetric) Inc() {
	gm.m.Increment()
}

func (gm *gaugeMetric) Dec() {
	gm.m.Decrement()
}

type counterMetric struct {
	m monitoring.Metric
}

func (cm *counterMetric) Inc() {
	cm.m.Increment()
}

type histogramMetric struct {
	m monitoring.Metric
}

func (hm *histogramMetric) Observe(val float64) {
	hm.m.Record(val)
}

type settableGaugeMetric struct {
	m monitoring.Metric
}

func (sgm *settableGaugeMetric) Set(val float64) {
	sgm.m.Record(val)
}
