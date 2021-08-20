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
	"time"

	"golang.org/x/time/rate"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"istio.io/istio/mdp/controller/pkg/apis/mdp/v1alpha1"
	mdperrors "istio.io/istio/mdp/controller/pkg/errors"
	"istio.io/istio/mdp/controller/pkg/globalerrors"
	"istio.io/istio/mdp/controller/pkg/metrics"
	"istio.io/istio/mdp/controller/pkg/proxyupdater"
	"istio.io/istio/mdp/controller/pkg/revision"
	"istio.io/istio/mdp/controller/pkg/status"
	"istio.io/pkg/log"
)

type NewReconciler struct {
	revision.ReadPodCache
	updateworkers map[types.NamespacedName]proxyupdater.UpdateWorker
	client.Client
	*kubernetes.Clientset
	statusWorker  status.Worker
	eventRecorder record.EventRecorder
	metricsRecord *metricsRecord
}

// timeEntry is a map keyed by dpc UID, value is another map keyed by observedGeneration of status, value is the time.
type timeEntry map[string]map[int64]int

// metricsRecord is used for recording metrics related info.
type metricsRecord struct {
	// firstUnReadyTime records the first time when DPC becomes unready of a specific generation.
	firstUnReadyTime timeEntry
}

// MaxTimeToReconcile is the maximum time we can allow one cluster to reconcile.
var MaxTimeToReconcile = 24 * time.Hour

// var MaxTimeToReconcile = 24*time.Hour
// these vars allow for test injection
var (
	workerBuilder   = proxyupdater.NewWorker
	upgraderBuilder = proxyupdater.NewEvictorUpgrader
)

const (
	totalBasisPoints = 10000
	dpTagKey         = "TAG"
)

func New(podCache revision.ReadPodCache, sw status.Worker, cl client.Client, rc *rest.Config, recorder record.EventRecorder) *NewReconciler {
	return &NewReconciler{
		ReadPodCache:  podCache,
		updateworkers: make(map[types.NamespacedName]proxyupdater.UpdateWorker),
		Client:        cl,
		Clientset:     kubernetes.NewForConfigOrDie(rc),
		statusWorker:  sw,
		eventRecorder: recorder,
		metricsRecord: &metricsRecord{firstUnReadyTime: make(timeEntry)},
	}
}

func (n *NewReconciler) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	result := reconcile.Result{}
	dpc := &v1alpha1.DataPlaneControl{}
	resultMetricLabel := metrics.Unknown
	// Record reconciliation loop count with result label at the end.
	defer func() {
		metrics.ReportReconcileLoopCount(resultMetricLabel, dpc.Spec.Revision)
	}()
	if err := n.Client.Get(ctx, request.NamespacedName, dpc); err != nil {
		resultMetricLabel = metrics.ResourceError
		if errors.IsNotFound(err) {
			// returning an error will requeue the request.  don't do that.
			log.Warnf("DataplaneControl %s appears to have been deleted", request.Name)
			return result, nil
		}
		return result, err
	}
	if err := globalerrors.GetErrorForRevision(dpc.Spec.Revision); err != nil {
		log.Warnf("Cannot reconcile DataPlaneControl %s due to error: %s", dpc.Spec.Revision, err)
		dpc.Status = v1alpha1.DataPlaneControlStatus{
			State: v1alpha1.Error,
			ErrorDetails: &v1alpha1.DataPlaneControlError{
				Code:    mdperrors.InvalidRevision,
				Message: err.Error(),
			},
			ProxyTargetBasisPoints: 0,
			ObservedGeneration:     dpc.Generation,
		}
		n.statusWorker.EnqueueStatus(dpc)
		metrics.ReportReconcileState(dpc.Spec.Revision, dpc.Status.State)
		resultMetricLabel = metrics.ResourceError
		return result, err
	}
	cpVersion, err := getControlPlaneExpectedVersion(ctx, n.Client, dpc.Spec.Revision)
	if err != nil {
		n.stopUpdateWorkerForDPR(request.NamespacedName)
		resultMetricLabel = metrics.ResourceError
		dpc.Status = v1alpha1.DataPlaneControlStatus{
			State: v1alpha1.Error,
			ErrorDetails: &v1alpha1.DataPlaneControlError{
				Code:    mdperrors.VersionMismatch,
				Message: err.Error(),
			},
			ProxyTargetBasisPoints: 0,
			ObservedGeneration:     dpc.Generation,
		}
		n.statusWorker.EnqueueStatus(dpc)
		return result, fmt.Errorf("unable to determine control plane injection version for revision %s, "+
			"cannot reconcile: %v", dpc.Spec.Revision, err)
	}
	if dpc.Spec.ProxyVersion == "" || cpVersion != dpc.Spec.ProxyVersion {
		n.stopUpdateWorkerForDPR(request.NamespacedName)
		resultMetricLabel = metrics.VersionError
		err := fmt.Errorf("DataPlaneControl for revision %s expects version '%s', but Control Plane is "+
			"injecting version '%s', cannot reconcile", dpc.Spec.Revision, dpc.Spec.ProxyVersion, cpVersion)
		dpc.Status = v1alpha1.DataPlaneControlStatus{
			State: v1alpha1.Error,
			ErrorDetails: &v1alpha1.DataPlaneControlError{
				Code:    mdperrors.VersionMismatch,
				Message: err.Error(),
			},
			ProxyTargetBasisPoints: 0,
			ObservedGeneration:     dpc.Generation,
		}
		n.statusWorker.EnqueueStatus(dpc)
		return result, err
	}

	// TODO: if version is unsupported, what do we do?  exit?
	versions, total := n.ReadPodCache.GetProxyVersionCount(dpc.Spec.Revision)
	metrics.ReportProxies(versions, dpc.Spec.Revision)
	if total < 1 {
		log.Infof("no pods in revision %s, nothing to upgrade", dpc.Spec.Revision)
		dpc.Status = calculateStatus(dpc, total, versions[dpc.Spec.ProxyVersion],
			0, n.metricsRecord)
		n.statusWorker.EnqueueStatus(dpc)
		metrics.ReportReconcileState(dpc.Spec.Revision, dpc.Status.State)
		resultMetricLabel = metrics.Success
		return result, nil
	}
	targetPct := float32(dpc.Spec.ProxyTargetBasisPoints*100) / totalBasisPoints
	newVersion := dpc.Spec.ProxyVersion
	log.Infof("target: %v, version: %s", targetPct, newVersion)

	metrics.ReportProxyPercentageTarget(cpVersion, dpc.Spec.Revision, dpc.Spec.ProxyTargetBasisPoints)
	bptsFraction := float32(dpc.Spec.ProxyTargetBasisPoints) / totalBasisPoints
	desired := int(math.Ceil(float64(float32(total) * bptsFraction)))
	if desired < 1 {
		// we have already met our goal, as our goal is zero.  cease updating (if in progress), update status, and exit.
		n.stopUpdateWorkerForDPR(request.NamespacedName)
		dpc.Status = calculateStatus(dpc, total, versions[dpc.Spec.ProxyVersion],
			0, n.metricsRecord)
		n.statusWorker.EnqueueStatus(dpc)
		log.Infof("revision %s meets goal of zero proxies", dpc.Spec.Revision)
		resultMetricLabel = metrics.Success
		return result, nil
	}
	u := n.getOrMakeUpdater(ctx, request.NamespacedName, dpc.Spec.Revision, dpc.Spec.ProxyVersion, limitFor24HourRollout(total))
	projectedActual := versions[dpc.Spec.ProxyVersion] + u.Len()
	log.Debugf("update count projected: %v, desired: %v", projectedActual, desired)
	if projectedActual < desired {
		needed := desired - projectedActual
		enqueued := u.EnqueueNUpdates(needed, dpc.Spec.ProxyVersion)
		if enqueued < needed {
			result.Requeue = true
		}
	} else if u.Len() > 0 &&
		float32(projectedActual)/float32(desired) > 1.1 && dpc.Spec.ProxyTargetBasisPoints < totalBasisPoints {
		// we're projected to overshoot by more than 10%.  Purge the updater.
		log.Infof("Dataplane Update Queue for revision %s is expected to overshoot the desired "+
			"ProxyTargetBasisPoints, and the queue will be restarted.", dpc.Spec.Revision)
		u.Stop()
		delete(n.updateworkers, request.NamespacedName)
		result.Requeue = true
	}
	dpc.Status = calculateStatus(dpc, total, versions[dpc.Spec.ProxyVersion],
		u.FailingLen(), n.metricsRecord)
	n.statusWorker.EnqueueStatus(dpc)
	metrics.ReportReconcileState(dpc.Spec.Revision, dpc.Status.State)

	resultMetricLabel = metrics.Success
	return result, nil
}

func (n *NewReconciler) stopUpdateWorkerForDPR(dprNsName types.NamespacedName) {
	if worker, ok := n.updateworkers[dprNsName]; ok {
		worker.Stop()
		delete(n.updateworkers, dprNsName)
	}
}

func getControlPlaneExpectedVersion(ctx context.Context, cl client.Client, channel string) (string, error) {
	cm := &v1.ConfigMap{}
	err := cl.Get(ctx, client.ObjectKey{Name: fmt.Sprintf("env-%s", channel), Namespace: "istio-system"}, cm)
	if err != nil {
		return "", err
	}
	return cm.Data[dpTagKey], nil
}

func limitFor24HourRollout(podCount int) rate.Limit {
	x := int64(MaxTimeToReconcile)
	return rate.Every(time.Duration(x / int64(podCount)))
}

func (n *NewReconciler) getOrMakeUpdater(ctx context.Context, dprNsName types.NamespacedName, rev, version string, limit rate.Limit) proxyupdater.UpdateWorker {
	if u, ok := n.updateworkers[dprNsName]; ok {
		u.SetRate(limit, 1)
		return u
	}
	u := upgraderBuilder(n.Clientset)
	result := workerBuilder(rev, version, limit, 1, u, n.ReadPodCache, n.Client, n.eventRecorder)
	result.Start(ctx)
	n.updateworkers[dprNsName] = result
	return result
}

func calculateStatus(dpc *v1alpha1.DataPlaneControl, total int, actual int, failingPodCount int, mr *metricsRecord) v1alpha1.DataPlaneControlStatus {
	revision, generation, targetPoints, UID := dpc.Spec.Revision, dpc.Generation, dpc.Spec.ProxyTargetBasisPoints, string(dpc.UID)
	var state v1alpha1.DataPlaneState
	var err *v1alpha1.DataPlaneControlError
	var achievedBpts int32
	if total < 1 {
		achievedBpts = totalBasisPoints
	} else {
		achievedBpts = int32(actual * totalBasisPoints / total)
	}
	if achievedBpts >= targetPoints {
		state = v1alpha1.Ready
		if e, ok := mr.firstUnReadyTime[UID]; ok {
			if nrt, ok := e[generation]; ok {
				metrics.ReportReconcileDuration(revision, nrt)
			}
		}
	} else {
		if (total-failingPodCount)*totalBasisPoints/total < int(targetPoints) {
			state = v1alpha1.Error
			err = &v1alpha1.DataPlaneControlError{
				Code:    mdperrors.TooManyEvictions,
				Message: "One or more PodDistruptionBudgets are preventing upgrade.",
			}
		} else {
			state = v1alpha1.Reconciling
		}
		if _, ok := mr.firstUnReadyTime[UID]; !ok {
			mr.firstUnReadyTime[UID] = make(map[int64]int)
		}
		// only records the first unready time for status of specific generation
		if _, ok := mr.firstUnReadyTime[UID][generation]; !ok {
			mr.firstUnReadyTime[UID][generation] = time.Now().Second()
		}
	}
	return v1alpha1.DataPlaneControlStatus{
		State:                  state,
		ProxyTargetBasisPoints: achievedBpts,
		ObservedGeneration:     generation,
		ErrorDetails:           err,
	}
}
