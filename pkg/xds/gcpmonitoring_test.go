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

package xds

import (
	"fmt"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"go.opencensus.io/stats/view" // nolint: depguard
	"go.opencensus.io/tag"        // nolint: depguard

	mon "istio.io/istio/pkg/monitoring"
	"istio.io/istio/pkg/test/util/retry"
)

var typeTestTag = tag.MustNewKey("type")

// TestExporter is used for GCP monitoring test.
type TestExporter struct {
	sync.Mutex

	Rows        map[string][]*view.Row
	invalidTags bool
}

// ExportView exports test views.
func (t *TestExporter) ExportView(d *view.Data) {
	t.Lock()
	defer t.Unlock()
	for _, tk := range d.View.TagKeys {
		if len(tk.Name()) < 1 {
			t.invalidTags = true
		}
	}
	t.Rows[d.View.Name] = append(t.Rows[d.View.Name], d.Rows...)
}

func TestGCPMonitoringXDSMetrics(t *testing.T) {
	// The following is the workaround for linter since "nolint" is ignored.
	// b/340344387 is a tracking bug.
	t.Skip("https://github.com/istio/istio/issues/340344387")
	os.Setenv("ENABLE_STACKDRIVER_MONITORING", "true")
	defer os.Unsetenv("ENABLE_STACKDRIVER_MONITORING")
	exp := &TestExporter{Rows: make(map[string][]*view.Row)}
	view.RegisterExporter(exp)
	view.SetReportingPeriod(1 * time.Millisecond)

	cases := []struct {
		name       string
		m          mon.Metric
		increment  bool
		recordVal  float64
		wantMetric string
		wantVal    *view.Row
	}{
		{"cdsReject", cdsReject, true, 0, "control/rejected_config_count", &view.Row{
			Tags: []tag.Tag{{Key: typeTestTag, Value: "CDS"}}, Data: &view.SumData{Value: 1.0},
		}},
		{"edsReject", edsReject, true, 0, "control/rejected_config_count", &view.Row{
			Tags: []tag.Tag{{Key: typeTestTag, Value: "EDS"}}, Data: &view.SumData{Value: 1.0},
		}},
		{"ldsReject", ldsReject, true, 0, "control/rejected_config_count", &view.Row{
			Tags: []tag.Tag{{Key: typeTestTag, Value: "LDS"}}, Data: &view.SumData{Value: 1.0},
		}},
		{"rdsReject", rdsReject, true, 0, "control/rejected_config_count", &view.Row{
			Tags: []tag.Tag{{Key: typeTestTag, Value: "RDS"}}, Data: &view.SumData{Value: 1.0},
		}},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			exp.Lock()
			exp.Rows = make(map[string][]*view.Row)
			exp.Unlock()
			if tt.increment {
				tt.m.Increment()
			} else {
				tt.m.Record(tt.recordVal)
			}
			if err := retry.UntilSuccess(func() error {
				exp.Lock()
				defer exp.Unlock()
				if len(exp.Rows[tt.wantMetric]) < 1 {
					return fmt.Errorf("wanted metrics %v not received", tt.wantMetric)
				}
				for _, got := range exp.Rows[tt.wantMetric] {
					if len(got.Tags) != len(tt.wantVal.Tags) ||
						(len(tt.wantVal.Tags) != 0 && !reflect.DeepEqual(got.Tags, tt.wantVal.Tags)) {
						continue
					}
					switch v := tt.wantVal.Data.(type) {
					case *view.SumData:
						if int64(v.Value) <= int64(got.Data.(*view.SumData).Value) {
							return nil
						}
					case *view.LastValueData:
						if int64(v.Value) <= int64(got.Data.(*view.LastValueData).Value) {
							return nil
						}
					case *view.DistributionData:
						gotDist := got.Data.(*view.DistributionData)
						if reflect.DeepEqual(gotDist.CountPerBucket, v.CountPerBucket) {
							return nil
						}
					}
				}
				return fmt.Errorf("metrics %v does not have expected values, want %+v", tt.m.Name(), tt.wantVal)
			}); err != nil {
				t.Fatalf("failed to get expected metric: %v", err)
			}
		})
	}
}
