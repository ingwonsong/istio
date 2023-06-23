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

package ratelog

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestLog(t *testing.T) {
	out := &[]string{}
	l := New(10*time.Millisecond, out)

	l.log("1", severityInfo)
	l.log("2", severityInfo)
	time.Sleep(time.Millisecond)
	l.log("1", severityInfo)
	time.Sleep(time.Millisecond)
	l.log("2", severityInfo)
	l.log("3", severityInfo)
	time.Sleep(100 * time.Millisecond)
	l.log("1", severityInfo)
	time.Sleep(time.Millisecond)
	l.log("2", severityInfo)

	if got, want := *out, []string{"info 1", "info 2", "info 3", "info 1", "info 2"}; !cmp.Equal(got, want) {
		t.Fatalf("Testl.log: got: %v, want: %v", got, want)
	}
}

func TestWrappers(t *testing.T) {
	out := &[]string{}
	l := New(10*time.Millisecond, out)

	l.Info("1")
	l.Infof("%s-%d", "2", 2)
	l.Warn("3")
	l.Warnf("%s-%d", "4", 4)
	l.Error("5")
	l.Errorf("%s-%d", "6", 6)

	if got, want := *out, []string{"info 1", "info 2-2", "warn 3", "warn 4-4", "error 5", "error 6-6"}; !cmp.Equal(got, want) {
		t.Fatalf("Testl.log: got: %v, want: %v", got, want)
	}
}
