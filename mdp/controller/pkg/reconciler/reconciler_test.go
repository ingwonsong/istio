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
	version = "0.0.1"
	myrev   = "myrev"
)

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
	upgradeCount int
}

func (f *FakeUpgradeWorker) Start(_ context.Context) {
}

func (f *FakeUpgradeWorker) Stop() {
}

func (f *FakeUpgradeWorker) EnqueueNUpdates(n int, targetVersion string) int {
	f.upgradeCount += n
	return n
}

func (f *FakeUpgradeWorker) Len() int {
	return f.upgradeCount
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

func buildClient() client.Client {
	myrevCfg := &v1alpha1.DataPlaneControl{
		ObjectMeta: v12.ObjectMeta{
			Name:      myrev,
			Namespace: myrev,
		},
		Spec: v1alpha1.DataPlaneControlSpec{
			Revision:               myrev,
			ProxyVersion:           version,
			ProxyTargetBasisPoints: 8000,
		},
	}

	envCM := &v1.ConfigMap{
		ObjectMeta: v12.ObjectMeta{
			Name:      fmt.Sprintf("env-%s", myrev),
			Namespace: "istio-system",
		},
		Data: map[string]string{dpTagKey: version},
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
	fu := &FakeUpgradeWorker{}
	workerBuilder = func(revision, version string, limit rate.Limit, burst int, upgrader proxyupdater.DataPlaneUpgrader,
		podCache revision.ReadPodCache, client client.Client, eventRecorder record.EventRecorder,
	) proxyupdater.UpdateWorker {
		return fu
	}
	upgraderBuilder = func(cs *kubernetes.Clientset) proxyupdater.DataPlaneUpgrader {
		return FakeUpdater{}
	}
	cl := buildClient()
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
	g.Expect(fu.upgradeCount).To(gomega.Equal(1))

	res, err = r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{
		Namespace: myrev,
		Name:      myrev,
	}})
	g.Expect(err).NotTo(gomega.HaveOccurred())
	g.Expect(res.Requeue).NotTo(gomega.BeTrue())
	g.Expect(res.RequeueAfter).To(gomega.Equal(time.Duration(0)))
	g.Expect(fu.upgradeCount).To(gomega.Equal(1))

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
