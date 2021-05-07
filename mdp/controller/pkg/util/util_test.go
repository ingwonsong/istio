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

package util

import (
	"testing"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestLabelSelectorCache(t *testing.T) {
	l := NewLSCache()
	type args struct {
		sel    *v1.LabelSelector
		labels map[string]string
	}
	tests := []struct {
		name      string
		args      args
		want      bool
		wantInits int32
		wantCalls int32
	}{
		{
			name: "first",
			args: args{
				sel: &v1.LabelSelector{
					MatchExpressions: []v1.LabelSelectorRequirement{
						{
							Key:      "istio.io/rev",
							Operator: v1.LabelSelectorOpIn,
							Values:   []string{"default"},
						},
					},
				},
				labels: map[string]string{"foo": "bar", "istio.io/rev": "default"},
			},
			want:      true,
			wantInits: 1,
			wantCalls: 1,
		}, {
			name: "second",
			args: args{
				sel: &v1.LabelSelector{
					MatchExpressions: []v1.LabelSelectorRequirement{
						{
							Key:      "istio.io/rev",
							Operator: v1.LabelSelectorOpIn,
							Values:   []string{"default"},
						},
					},
				},
				labels: map[string]string{"foo": "bar", "istio.io/rev": "default"},
			},
			want:      true,
			wantInits: 1,
			wantCalls: 1,
		}, {
			name: "third",
			args: args{
				sel: &v1.LabelSelector{
					MatchExpressions: []v1.LabelSelectorRequirement{
						{
							Key:      "istio.io/rev",
							Operator: v1.LabelSelectorOpIn,
							Values:   []string{"default"},
						},
					},
				},
				labels: map[string]string{"foo": "foo", "istio.io/rev": "defaults"},
			},
			want:      false,
			wantInits: 1,
			wantCalls: 2,
		}, {
			name: "fourth",
			args: args{
				sel: &v1.LabelSelector{
					MatchExpressions: []v1.LabelSelectorRequirement{
						{
							Key:      "istio.io/rev",
							Operator: v1.LabelSelectorOpDoesNotExist,
						}, {
							Key:      "istio-injection",
							Operator: v1.LabelSelectorOpDoesNotExist,
						},
					},
				},
				labels: map[string]string{"foo": "bar", "istio.io/rev": "default"},
			},
			want:      false,
			wantInits: 2,
			wantCalls: 3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := l.Matches(tt.args.sel, tt.args.labels)
			if err != nil {
				t.Errorf("Matches() error = %v", err)
				return
			}
			if got != tt.want {
				t.Errorf("Matches() got = %v, want %v", got, tt.want)
			}
			if l.matchCalls != tt.wantCalls {
				t.Errorf("Got %d calls to labels.Selector.Match(), want %d", l.matchCalls, tt.wantCalls)
			}
			if l.selectorInits != tt.wantInits {
				t.Errorf("Got %d calls to labels.LabelSelectorAsSelector(), want %d", l.selectorInits, tt.wantInits)
			}
		})
	}
}
