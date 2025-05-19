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

	corev1 "k8s.io/api/core/v1"
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

func TestProxyVersion(t *testing.T) {
	tests := []struct {
		name        string
		pod         *corev1.Pod
		wantVersion string
	}{
		{
			name: "regular version",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Image: "test",
						},
						{
							Image: "gcr.io/gke-release/asm/proxyv2:1.16.7-asm.10",
						},
					},
				},
			},
			wantVersion: "1.16.7-asm.10",
		},
		{
			name: "distroless version",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Image: "test",
						},
						{
							Image: "gcr.io/gke-release/asm/proxyv2:1.16.7-asm.10-distroless",
						},
					},
				},
			},
			wantVersion: "1.16.7-asm.10",
		},
		{
			name: "version including @sha",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Image: "test",
						},
						{
							Image: "gcr.io/gke-release/asm/proxyv2:1.16.7-asm.10-distroless@sha256:f65123ouiop432908sdfasfawes4r5123112234uo1i3uop12i34j2op41o34ui12iop3",
						},
					},
				},
			},
			wantVersion: "1.16.7-asm.10",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ProxyVersion(tt.pod)
			if !ok {
				t.Errorf("Parsing proxy version in the pod %v failed.", tt.pod)
			}
			if got != tt.wantVersion {
				t.Errorf("Got version %s, but want %s", got, tt.wantVersion)
			}
		})
	}
}
