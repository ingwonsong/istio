/*
Copyright 2018 The Kubernetes Authors.

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
	"time"

	"golang.org/x/time/rate"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"istio.io/istio/mdp/controller/pkg/metrics"
	"istio.io/istio/mdp/controller/pkg/name"
	"istio.io/istio/mdp/controller/pkg/ratelimiter"
	"istio.io/istio/mdp/controller/pkg/revision"
	"istio.io/istio/mdp/controller/pkg/set"
	"istio.io/pkg/log"
)

// This is the minimum duration allowed between any two calls to update(), defined as 1 day divided by
// (gke_max_pods_per_node * gke_max_nodes), or approximately 1/18 of a second
var (
	maxSpeed          = time.Hour * 24 / (15000 * 110)
	clusterSpeedLimit = &workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(rate.Every(maxSpeed), 2)}
	scope             = log.RegisterScope("mdp", "Managed Data Plane", 0)
	minFail           = 30 * time.Second
	maxFail           = 60 * time.Minute
)

type UpdateWorker interface {
	Start(ctx context.Context)
	Stop()
	EnqueueNUpdates(n int, targetVersion string) int
	Len() int
	FailingLen() int
	SetRate(limit rate.Limit, burst int)
}

type UpdateWorkerImpl struct {
	mu sync.Mutex
	// the revision which this updater serves
	revision string
	// ExpectedVersion is the Proxy Version we expect to see when starting new pods in this revision.
	ExpectedVersion string
	// the queue of pods to evict, including all required rate limiting
	queue ratelimiter.MDPUpdateRateLimiter
	// RateLimitingInterface doesn't provide contains() or proper Len, so we track that here.
	Inqueue set.Set
	// Track which pods in this revision are currently failing, and don't retry unless we have to.
	FailingPods set.Set

	client        client.Client
	podCache      revision.ReadPodCache
	cancel        context.CancelFunc
	upgrader      DataPlaneUpgrader
	eventRecorder record.EventRecorder
}

func NewWorker(revision, version string, limit rate.Limit, burst int, upgrader DataPlaneUpgrader,
	podCache revision.ReadPodCache, client client.Client, eventRecorder record.EventRecorder) UpdateWorker {
	// TODO: set up metrics provider https://pkg.go.dev/k8s.io/client-go/util/workqueue#MetricsProvider
	failureLimiter := workqueue.NewItemExponentialFailureRateLimiter(minFail, maxFail)
	return &UpdateWorkerImpl{
		revision:        revision,
		ExpectedVersion: version,
		queue:           ratelimiter.NewMDPRateLimitingQueueWithSpeedLimit(limit, burst, clusterSpeedLimit, failureLimiter),
		Inqueue:         set.Set{},
		FailingPods:     set.Set{},
		upgrader:        upgrader,
		podCache:        podCache,
		client:          client,
		eventRecorder:   eventRecorder,
	}
}

func (u *UpdateWorkerImpl) Start(ctx context.Context) {
	sctx, can := context.WithCancel(ctx)
	u.cancel = can
	go func() {
		<-sctx.Done()
		u.queue.ShutDown()
	}()
	go func() {
		for u.processNextWorkItem(sctx) {
		}
	}()
}

func (u *UpdateWorkerImpl) Stop() {
	if u.cancel == nil {
		log.Fatalf("Stop() called on unstarted UpdateWorkerImpl")
	}
	u.cancel()
}

func (u *UpdateWorkerImpl) processNextWorkItem(ctx context.Context) bool {
	obj, shutdown := u.queue.Get()
	if shutdown {
		// Stop working
		return false
	}

	// We call Done here so the workqueue knows we have finished
	// processing this item. We also must remember to call Forget if we
	// do not want this work item being re-queued. For example, we do
	// not call Forget if a transient error occurs, instead the item is
	// put back on the workqueue and attempted again after a back-off
	// period.
	defer u.queue.Done(obj)
	item := obj.(revision.PodWorkItem)
	r := item.NamespacedName
	// should we ensure update is still needed?
	if err := u.upgrader.PerformUpgrade(ctx, r); err != nil {
		metrics.ReportUpgradedProxiesCount(item.FromVer, u.ExpectedVersion, string(errors.ReasonForError(err)), u.revision)
		scope.Warnf("Failed to upgrade pod %s/%s: %v", r.Namespace, r.Name, err)
		u.mu.Lock()
		defer u.mu.Unlock()
		// TODO: emit telemetry on error code here
		if errors.IsTooManyRequests(err) {
			u.FailingPods.Insert(r)
			if u.queue.NumRequeues(r) > 5 {
				log.Warnf("pod %s has failed to upgrade 6 consecutive times", r.String())
			}
		}
		eventMessage := fmt.Sprintf("%s: %v", name.EvictionErrorEventMessage, err.Error())
		u.writeFailureEventAndLabels(r, eventMessage)
		// mark this pod as currently failing
		// pick a new pod and enqueue
		eligiblePods := u.podCache.GetPodsInRevisionOutOfVersion(u.revision, u.ExpectedVersion)
		nextPod := pickNonFailingPods(eligiblePods, u.Inqueue, u.FailingPods, 1)
		if len(nextPod) >= 1 {
			u.queueUpdate(nextPod[0].(revision.PodWorkItem))
			u.Inqueue.Delete(r)
		} else {
			// if error is retriable
			if errors.IsTooManyRequests(err) {
				// if no new pods exist, requeue
				scope.Infof("No other pods available, requeueing pod %s/%s.", r.Namespace, r.Name)
				u.queue.AddFailed(obj)
			} else {
				runtime.HandleError(err)
			}
		}
	} else {
		metrics.ReportUpgradedProxiesCount(item.FromVer, u.ExpectedVersion, metrics.SuccessLabel, u.revision)
		u.mu.Lock()
		defer u.mu.Unlock()
		u.queue.Forget(obj)
		u.Inqueue.Delete(obj)
		u.podCache.RemovePodByName(u.revision, r.Namespace, "", r.Name)
		u.FailingPods.Delete(obj)
	}
	return true
}

const maxLen = 1000

func (u *UpdateWorkerImpl) writeFailureEventAndLabels(r types.NamespacedName, eventMessage string) {
	u.recordEvent(r.Name, r.Namespace, eventMessage, name.UpgradeErrorEventReason, v1.EventTypeWarning)
	md := &metav1.PartialObjectMetadata{
		TypeMeta:   metav1.TypeMeta{Kind: "Pod", APIVersion: "v1"},
		ObjectMeta: metav1.ObjectMeta{Name: r.Name, Namespace: r.Namespace, Labels: map[string]string{name.DataplaneUpgradeLabel: "failed"}},
	}
	if err := u.client.Patch(context.TODO(), md, client.Merge); err != nil {
		scope.Errorf("failed to update label for pod %s: %v", r.String(), err)
	}
}

func (u *UpdateWorkerImpl) EnqueueNUpdates(n int, targetVersion string) int {
	u.ExpectedVersion = targetVersion
	// adjust n downward if it would result in more than 1000 pods in queue.  This limit is to protect from buffer
	// overflow at k8s.io/client-go@v0.21.0/util/workqueue/delaying_queue.go:64
	if n+u.Len() > maxLen {
		n = maxLen - u.Len()
	}
	eligiblePods := u.podCache.GetPodsInRevisionOutOfVersion(u.revision, targetVersion)
	u.mu.Lock()
	pickedPods := pickNonFailingPods(eligiblePods, u.Inqueue, u.FailingPods, n)
	var retryFailures []set.T
	// when we can't pick non-failing pods, pick failing ones.
	scope.Infof("enqueued %d updates from fresh pods for revision %s", len(pickedPods), u.revision)
	if len(pickedPods) < n {
		retryFailures = pickFailingPods(eligiblePods, u.Inqueue, u.FailingPods, n-len(pickedPods))
		scope.Infof("enqueued %d updates from failed pods for revision %s", len(pickedPods), u.revision)
	}
	u.Inqueue.Insert(pickedPods...)
	u.Inqueue.Insert(retryFailures...)
	u.mu.Unlock()
	for _, p := range pickedPods {
		u.queue.AddRateLimited(p)
	}
	for _, p := range retryFailures {
		u.queue.AddFailed(p)
	}
	return len(pickedPods) + len(retryFailures)
}

// must call u.mu.Lock before calling this function.
func (u *UpdateWorkerImpl) queueUpdate(pod revision.PodWorkItem) {
	if u.Inqueue.Has(pod) {
		// this request is already queued up
		return
	}
	u.queue.AddRateLimited(pod)
	u.Inqueue.Insert(pod)
}

// Len returns the Length of the queue of pods to update
func (u *UpdateWorkerImpl) Len() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.Inqueue)
}

// FailingLen returns the Length of the set of currently failling pods.
func (u *UpdateWorkerImpl) FailingLen() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.FailingPods)
}

// SetRate adjusts the rate at which newly enqueued updates will occur.
func (u *UpdateWorkerImpl) SetRate(limit rate.Limit, burst int) {
	u.queue.AdjustRateLimit(limit, burst)
}

func (u *UpdateWorkerImpl) recordEvent(name, namespace, message, reason, eventType string) {
	t := metav1.Time{Time: time.Now()}
	ref := &v1.ObjectReference{Name: name, Namespace: namespace}
	ev := &v1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%v", ref.Name),
			Namespace: namespace,
		},
		InvolvedObject: *ref,
		Reason:         reason,
		Message:        message,
		FirstTimestamp: t,
		LastTimestamp:  t,
		Count:          1,
		Type:           eventType,
	}
	u.eventRecorder.Event(ev, eventType, reason, message)
}

func pickNonFailingPods(allPods, inProgress, currentlyFailing set.Set, count int) []set.T {
	result := make([]set.T, 0, count)
	for o := range allPods {
		if len(result) >= count {
			break
		}
		if inProgress.Has(o) || currentlyFailing.Has(o) {
			continue
		}
		result = append(result, o)
	}
	return result
}

func pickFailingPods(allPods, inProgress, currentlyFailing set.Set, count int) []set.T {
	result := make([]set.T, 0, count)
	for o := range currentlyFailing {
		if len(result) >= count {
			break
		}
		if inProgress.Has(o) || !allPods.Has(o) {
			continue
		}
		result = append(result, o)
	}
	return result
}
