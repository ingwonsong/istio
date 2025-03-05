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

package reconciler

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/onsi/gomega"
	"golang.org/x/time/rate"
	v1 "k8s.io/api/core/v1"
	v12 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	"k8s.io/kubectl/pkg/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"istio.io/istio/mdp/controller/pkg/apis"
	"istio.io/istio/mdp/controller/pkg/apis/mdp/v1alpha1"
	"istio.io/istio/mdp/controller/pkg/errors"
	"istio.io/istio/mdp/controller/pkg/proxyupdater"
	"istio.io/istio/mdp/controller/pkg/revision"
	"istio.io/istio/mdp/controller/pkg/set"
	"istio.io/istio/mdp/controller/pkg/status"
)

const (
	version   = "0.0.1"
	versionTD = "0.0.2"

	myrev = "myrev"
)

func versionForServingMode(sm v1alpha1.ServingMode) string {
	switch sm {
	case v1alpha1.ServingMode_SERVING_MODE_TD:
		return versionTD
	case v1alpha1.ServingMode_SERVING_MODE_ISTIOD, v1alpha1.ServingMode_SERVING_MODE_ISTIOD_WITH_LRS, v1alpha1.ServingMode_SERVING_MODE_UNSPECIFIED:
		return version
	}
	panic(fmt.Sprintf("Unsupported serving mode %v", sm))
}

type FakePodCache struct{}

func (f FakePodCache) RemovePodByName(rev, namespace, version, podname string) {
	panic("implement me")
}

func (f FakePodCache) GetProxyVersionCount(rev string) (map[string]int, int) {
	return map[string]int{version: 3}, 5
}

func (f FakePodCache) GetPodsInRevisionOutOfVersion(rev, version string) set.Set {
	panic("implement me")
}

func (f FakePodCache) MarkDirty() {}

type FakeUpgradeWorker struct {
	upgradeCount map[string]int
}

func (f *FakeUpgradeWorker) Start(_ context.Context) {
}

func (f *FakeUpgradeWorker) Stop() {
}

func (f *FakeUpgradeWorker) EnqueueNUpdates(n int, targetVersion string) int {
	f.upgradeCount[targetVersion] += n
	return n
}

func (f *FakeUpgradeWorker) Len() int {
	c := 0
	for _, v := range f.upgradeCount {
		c += v
	}
	return c
}

func (f *FakeUpgradeWorker) FailingLen() int {
	return 0
}

func (f *FakeUpgradeWorker) SetRate(_ rate.Limit, _ int) {
}

type FakeUpdater struct{}

func (f FakeUpdater) PerformUpgrade(_ context.Context, _ types.NamespacedName) error {
	return nil
}

func buildClient(sm v1alpha1.ServingMode) client.Client {
	useTDProxy := false
	injectedVersion := version
	if sm == v1alpha1.ServingMode_SERVING_MODE_TD {
		useTDProxy = true
		injectedVersion = versionTD
	}
	myrevCfg := &v1alpha1.DataPlaneControl{
		ObjectMeta: v12.ObjectMeta{
			Name:      myrev,
			Namespace: myrev,
		},
		Spec: v1alpha1.DataPlaneControlSpec{
			Revision:                 myrev,
			ProxyVersion:             version,
			ProxyTargetBasisPoints:   8000,
			ProxyVersionTD:           versionTD,
			ProxyTargetBasisPointsTD: 8000,
			InjectedProxyVersion:     injectedVersion,
			UseTDProxy:               useTDProxy,
			ServingMode:              sm,
		},
	}

	envCM := &v1.ConfigMap{
		ObjectMeta: v12.ObjectMeta{
			Name:      fmt.Sprintf("env-%s", myrev),
			Namespace: "istio-system",
		},
		Data: map[string]string{dpTagKey: versionForServingMode(sm)},
	}

	s := scheme.Scheme
	_ = apis.AddToScheme(s)
	return fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(myrevCfg, envCM).
		Build()
}

func TestReconcile(t *testing.T) {
	// inject fakes of the functions used to build proxyupdater classes
	fu := &FakeUpgradeWorker{
		upgradeCount: map[string]int{},
	}
	workerBuilder = func(revision, version string, limit rate.Limit, burst int, upgrader proxyupdater.DataPlaneUpgrader,
		podCache revision.ReadPodCache, client client.Client, eventRecorder record.EventRecorder,
	) proxyupdater.UpdateWorker {
		return fu
	}
	upgraderBuilder = func(cs *kubernetes.Clientset) proxyupdater.DataPlaneUpgrader {
		return FakeUpdater{}
	}
	cl := buildClient(v1alpha1.ServingMode_SERVING_MODE_UNSPECIFIED)
	r := NewReconciler{
		ReadPodCache:  FakePodCache{},
		updateworkers: map[types.NamespacedName]proxyupdater.UpdateWorker{},
		Client:        cl,
		statusWorker:  status.NewWorker(rate.Inf, cl),
		metricsRecord: &metricsRecord{firstUnReadyTime: make(timeEntry)},
	}
	res, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{
		Namespace: myrev,
		Name:      myrev,
	}})

	g := gomega.NewWithT(t)
	g.Expect(err).NotTo(gomega.HaveOccurred())
	g.Expect(res.Requeue).NotTo(gomega.BeTrue())
	g.Expect(res.RequeueAfter).To(gomega.Equal(time.Duration(0)))
	g.Expect(fu.upgradeCount[version]).To(gomega.Equal(1))

	res, err = r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{
		Namespace: myrev,
		Name:      myrev,
	}})
	g.Expect(err).NotTo(gomega.HaveOccurred())
	g.Expect(res.Requeue).NotTo(gomega.BeTrue())
	g.Expect(res.RequeueAfter).To(gomega.Equal(time.Duration(0)))
	g.Expect(fu.upgradeCount[version]).To(gomega.Equal(1))

	// expect some status calls
	g.Expect(r.statusWorker.Len()).To(gomega.Equal(1))
	r.statusWorker = status.NewWorker(rate.Inf, cl)
	dpr := &v1alpha1.DataPlaneControl{}
	err = cl.Get(context.Background(), client.ObjectKey{Namespace: myrev, Name: myrev}, dpr)
	g.Expect(err).NotTo(gomega.HaveOccurred())
	dpr.Spec.ProxyVersion = "2.0"
	err = cl.Update(context.Background(), dpr)
	g.Expect(err).NotTo(gomega.HaveOccurred())

	res, err = r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{
		Namespace: myrev,
		Name:      myrev,
	}})
	g.Expect(err).To(gomega.HaveOccurred())
	g.Expect(r.statusWorker.Len()).To(gomega.Equal(1))
}

func TestReconcileServingMode(t *testing.T) {
	tests := []struct {
		name        string
		servingMode v1alpha1.ServingMode
		wantUpdates int
	}{
		{
			name:        "unspecified",
			servingMode: v1alpha1.ServingMode_SERVING_MODE_UNSPECIFIED,
			wantUpdates: 1,
		},
		{
			name:        "istiod",
			servingMode: v1alpha1.ServingMode_SERVING_MODE_ISTIOD,
			wantUpdates: 1,
		},
		{
			name:        "istiod lrs",
			servingMode: v1alpha1.ServingMode_SERVING_MODE_ISTIOD_WITH_LRS,
			wantUpdates: 1,
		},

		{
			name:        "td",
			servingMode: v1alpha1.ServingMode_SERVING_MODE_TD,
			wantUpdates: 4,
		},
	}

	for _, tt := range tests {
		// inject fakes of the functions used to build proxyupdater classes
		fu := &FakeUpgradeWorker{
			upgradeCount: map[string]int{},
		}
		workerBuilder = func(revision, version string, limit rate.Limit, burst int, upgrader proxyupdater.DataPlaneUpgrader,
			podCache revision.ReadPodCache, client client.Client, eventRecorder record.EventRecorder,
		) proxyupdater.UpdateWorker {
			return fu
		}
		upgraderBuilder = func(cs *kubernetes.Clientset) proxyupdater.DataPlaneUpgrader {
			return FakeUpdater{}
		}

		cl := buildClient(tt.servingMode)
		r := NewReconciler{
			ReadPodCache:  FakePodCache{},
			updateworkers: map[types.NamespacedName]proxyupdater.UpdateWorker{},
			Client:        cl,
			statusWorker:  status.NewWorker(rate.Inf, cl),
			metricsRecord: &metricsRecord{firstUnReadyTime: make(timeEntry)},
		}
		res, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{
			Namespace: myrev,
			Name:      myrev,
		}})

		g := gomega.NewGomegaWithT(t)
		g.Expect(err).NotTo(gomega.HaveOccurred())
		g.Expect(res.Requeue).NotTo(gomega.BeTrue())
		g.Expect(res.RequeueAfter).To(gomega.Equal(time.Duration(0)))
		g.Expect(fu.upgradeCount[versionForServingMode(tt.servingMode)]).To(gomega.Equal(tt.wantUpdates))

		// expect some status calls
		g.Expect(r.statusWorker.Len()).To(gomega.Equal(1))
		r.statusWorker = status.NewWorker(rate.Inf, cl)
		dpr := &v1alpha1.DataPlaneControl{}
		err = cl.Get(context.Background(), client.ObjectKey{Namespace: myrev, Name: myrev}, dpr)
		g.Expect(err).NotTo(gomega.HaveOccurred())
		dpr.Spec.ProxyVersion = "2.0"
		err = cl.Update(context.Background(), dpr)
		g.Expect(err).NotTo(gomega.HaveOccurred())
	}
}

func Test_calculateStatus(t *testing.T) {
	testMetricsRecord := &metricsRecord{firstUnReadyTime: make(timeEntry)}
	type args struct {
		dpc             *v1alpha1.DataPlaneControl
		total           int
		actual          int
		failingPodCount int
	}
	tests := []struct {
		name string
		args args
		want v1alpha1.DataPlaneControlStatus
	}{
		{
			name: "ready",
			args: args{
				dpc: &v1alpha1.DataPlaneControl{
					ObjectMeta: v12.ObjectMeta{Generation: 1, UID: "UID-1"},
					Spec:       v1alpha1.DataPlaneControlSpec{ProxyTargetBasisPoints: 5000},
				},
				total:  100,
				actual: 51,
			},
			want: v1alpha1.DataPlaneControlStatus{
				State:                  v1alpha1.Ready,
				ErrorDetails:           nil,
				ProxyTargetBasisPoints: 5100,
				ObservedGeneration:     1,
				ProxyMetrics: &v1alpha1.ProxyMetrics{
					ManagedProxyCount: 100,
				},
			},
		}, {
			name: "reconciling",
			args: args{
				dpc: &v1alpha1.DataPlaneControl{
					ObjectMeta: v12.ObjectMeta{Generation: 1, UID: "UID-2"},
					Spec:       v1alpha1.DataPlaneControlSpec{ProxyTargetBasisPoints: 5000},
				},
				total:  100,
				actual: 49,
			},
			want: v1alpha1.DataPlaneControlStatus{
				State:                  v1alpha1.Reconciling,
				ErrorDetails:           nil,
				ProxyTargetBasisPoints: 4900,
				ObservedGeneration:     1,
				ProxyMetrics: &v1alpha1.ProxyMetrics{
					ManagedProxyCount: 100,
				},
			},
		}, {
			name: "failing",
			args: args{
				dpc: &v1alpha1.DataPlaneControl{
					ObjectMeta: v12.ObjectMeta{Generation: 1, UID: "UID-3"},
					Spec:       v1alpha1.DataPlaneControlSpec{ProxyTargetBasisPoints: 5000},
				},
				total:           100,
				actual:          49,
				failingPodCount: 51,
			},
			want: v1alpha1.DataPlaneControlStatus{
				State: v1alpha1.Error,
				ErrorDetails: &v1alpha1.DataPlaneControlError{
					Code:    errors.TooManyEvictions,
					Message: "One or more PodDistruptionBudgets are preventing upgrade.",
				},
				ProxyTargetBasisPoints: 4900,
				ObservedGeneration:     1,
				ProxyMetrics: &v1alpha1.ProxyMetrics{
					ManagedProxyCount: 100,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := calculateStatus(tt.args.dpc, tt.args.total, tt.args.actual, tt.args.failingPodCount, testMetricsRecord); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("calculateStatus() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMaxTimeToReconcile(t *testing.T) {
	timeNow := time.Now()
	testCases := []struct {
		name           string
		dpc            *v1alpha1.DataPlaneControl
		expectDuration int64
	}{
		{
			name: "duration from dpc unexpired",
			dpc: &v1alpha1.DataPlaneControl{
				Spec: v1alpha1.DataPlaneControlSpec{
					UpgradeDurationValidUntil: timeNow.Add(12 * time.Hour).Format(time.RFC3339),
				},
			},
			expectDuration: int64(12 * time.Hour),
		},
		{
			name: "duration from dpc expired",
			dpc: &v1alpha1.DataPlaneControl{
				Spec: v1alpha1.DataPlaneControlSpec{
					UpgradeDurationValidUntil: timeNow.Add(-time.Hour).Format(time.RFC3339),
				},
			},
			expectDuration: int64(MaxTimeToReconcile),
		},
		{
			name: "duration from dpc capped",
			dpc: &v1alpha1.DataPlaneControl{
				Spec: v1alpha1.DataPlaneControlSpec{
					UpgradeDurationValidUntil: timeNow.Format(time.RFC3339),
				},
			},
			expectDuration: int64(MaxTimeToReconcile),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := maxTimeToReconcile(tc.dpc, timeNow); int64(math.Abs(float64(got-tc.expectDuration))) > time.Second.Nanoseconds() {
				t.Errorf("maxTimeToReconcile(#%v) failed, got %v, want %v, diff %v", tc.dpc, got, tc.expectDuration, int64(math.Abs(float64(got-tc.expectDuration))))
			}
		})
	}
}
