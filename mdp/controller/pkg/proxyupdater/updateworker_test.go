/*
 Copyright Istio Authors

 Licensed under the Apache License, Version 2.0 (the "License");
 you may not use this file except in compliance with the License.
 You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

 Unless required by applicable law or agreed to in writing, software
 distributed under the License is distributed on an "AS IS" BASIS,
 WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 See the License for the specific language governing permissions and
 limitations under the License.
*/

package proxyupdater

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/onsi/gomega"
	"golang.org/x/time/rate"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v12 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"istio.io/istio/mdp/controller/pkg/ratelimiter"
	"istio.io/istio/mdp/controller/pkg/revision"
	"istio.io/istio/mdp/controller/pkg/set"
)

type FakeUpgrader struct {
	mu          sync.Mutex
	actions     []types.NamespacedName
	shouldError func(podName types.NamespacedName) error
}

func (f *FakeUpgrader) PerformUpgrade(ctx context.Context, podName types.NamespacedName) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	err := f.shouldError(podName)
	if err != nil {
		return err
	}
	f.actions = append(f.actions, podName)
	return nil
}

type FakeFailureRateLimiter struct {
	failures set.Set
}

func (f FakeFailureRateLimiter) When(item interface{}) time.Duration {
	f.failures.Insert(item)
	return 100 * time.Millisecond
}

func (f FakeFailureRateLimiter) Forget(item interface{}) {
	f.failures.Delete(item)
}

func (f FakeFailureRateLimiter) NumRequeues(item interface{}) int {
	if f.failures.Has(item) {
		return 1
	}
	return 0
}

type FakePodCache struct {
	mu   sync.Mutex
	pods set.Set
}

func (f *FakePodCache) RemovePodByName(rev, namespace, version, podname string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pods.Delete(revision.NewPodWorkItem(podname, namespace, "test-version"))
}

func (f *FakePodCache) GetProxyVersionCount(rev string) (map[string]int, int) {
	panic("implement me")
}

func (f *FakePodCache) GetPodsInRevisionOutOfVersion(rev, version string) set.Set {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pods
}

func (f *FakePodCache) MarkDirty() {
}

func TestUpdateWorker(t *testing.T) {
	const failme = "pod-8"
	failnsn := types.NamespacedName{
		Namespace: "test",
		Name:      failme,
	}
	fu := &FakeUpgrader{
		shouldError: func(podName types.NamespacedName) error {
			if podName.Name == failme {
				return errors.NewTooManyRequests("toomany", 0)
			}
			return nil
		},
	}

	g := gomega.NewGomegaWithT(t)
	rl := FakeFailureRateLimiter{failures: map[set.T]set.Empty{}}
	q := ratelimiter.NewMDPRateLimitingQueueWithSpeedLimit(rate.Inf, 1, &workqueue.BucketRateLimiter{rate.NewLimiter(rate.Inf, 1)}, rl)

	client := fake.NewClientBuilder().Build()
	pc := &FakePodCache{
		pods: set.Set{},
	}
	pc.mu.Lock()
	for i := 0; i < 10; i++ {
		name := fmt.Sprintf("pod-%d", i)
		pc.pods.Insert(revision.NewPodWorkItem(name, "test", "test-version"))
		pod := &v1.Pod{ObjectMeta: v12.ObjectMeta{Name: name, Namespace: "test"}}
		err := client.Create(context.TODO(), pod)
		g.Expect(err).NotTo(gomega.HaveOccurred())
	}
	pc.mu.Unlock()

	sut := UpdateWorkerImpl{
		revision:        "myrev",
		ExpectedVersion: "1.12",
		queue:           q,
		podCache:        pc,
		upgrader:        fu,
		Inqueue:         set.Set{},
		FailingPods:     set.Set{},
		eventRecorder:   record.NewFakeRecorder(20),
		client:          client,
	}

	sut.EnqueueNUpdates(10, "1.12")

	pc.mu.Lock()
	// don't add pod-10 till here to ensure that all 10 pods are selected for upgrade, including pod-8, which fails
	pc.pods.Insert(revision.NewPodWorkItem("pod-10", "test", "test-version"))
	pc.mu.Unlock()

	g.Expect(sut.Len()).To(gomega.Equal(10))
	sut.Start(context.Background())
	// Test should have triggered update for all pods except pod-8
	g.Eventually(func() bool {
		sut.mu.Lock() // pause the worker to see what happened
		defer sut.mu.Unlock()
		return sut.FailingPods.Has(failnsn)
	}).Should(gomega.BeTrue())
	g.Eventually(func() int {
		sut.mu.Lock() // pause the worker to see what happened
		pc.mu.Lock()
		defer pc.mu.Unlock()
		defer sut.mu.Unlock()
		return len(pc.pods)
	}).Should(gomega.Equal(1))
	g.Eventually(func() []types.NamespacedName {
		sut.mu.Lock() // pause the worker to see what happened
		defer sut.mu.Unlock()
		return fu.actions
	}).Should(gomega.HaveLen(10))
	g.Eventually(func() []types.NamespacedName {
		sut.mu.Lock() // pause the worker to see what happened
		defer sut.mu.Unlock()
		return fu.actions
	}).ShouldNot(gomega.ContainElement(failnsn))

	// this should never succeed
	sut.EnqueueNUpdates(1, "1.12")
	g.Consistently(sut.FailingLen()).Should(gomega.Equal(1))
	sut.Stop()
}
