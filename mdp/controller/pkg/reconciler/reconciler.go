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
	"istio.io/istio/mdp/controller/pkg/util/ratelog"
	"istio.io/istio/pkg/log"
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

// these vars allow for test injection
var (
	// MaxTimeToReconcile is the maximum time we can allow one cluster to reconcile.
	MaxTimeToReconcile = 12 * time.Hour

	workerBuilder   = proxyupdater.NewWorker
	upgraderBuilder = proxyupdater.NewEvictorUpgrader

	// nolint: gocritic
	now = func() time.Time {
		return time.Now()
	}
)

var rateLogger = ratelog.New(1*time.Hour, nil)

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
	var cpVersion string
	// Record reconciliation loop count with result label at the end.
	defer func() {
		metrics.ReportReconcileLoopCount(resultMetricLabel, dpc.Spec.Revision)
		if proxyVersionForDPC(dpc) != "" {
			metrics.ReportProxyPercentageTarget(proxyVersionForDPC(dpc), dpc.Spec.Revision, proxyTargetBasisPointsForDPC(dpc))
		}
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

	versions, total := n.ReadPodCache.GetProxyVersionCount(dpc.Spec.Revision)
	metrics.ReportProxies(versions, dpc.Spec.Revision)
	if total < 1 {
		rateLogger.Infof("no pods in revision %s, nothing to upgrade", dpc.Spec.Revision)
		dpc.Status = calculateStatus(dpc, total, versions[proxyVersionForDPC(dpc)],
			0, n.metricsRecord)
		n.statusWorker.EnqueueStatus(dpc)
		metrics.ReportReconcileState(dpc.Spec.Revision, dpc.Status.State)
		resultMetricLabel = metrics.Success
		return result, nil
	}
	rateLogger.Infof("%d pods in revision %s", total, dpc.Spec.Revision)
	var err error
	cpVersion, err = getControlPlaneExpectedVersion(ctx, n.Client, dpc.Spec.Revision)
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
	rateLogger.Infof("MCP is injecting version %s", cpVersion)
	if proxyVersionForDPC(dpc) == "" || !expectedProxyVersion(proxyVersionForDPC(dpc), cpVersion) {
		n.stopUpdateWorkerForDPR(request.NamespacedName)
		resultMetricLabel = metrics.VersionError
		err := fmt.Errorf("DataPlaneControl for revision %s expects version '%s', but Control Plane is "+
			"injecting version '%s', cannot reconcile.  MCP rollout may be in progress", dpc.Spec.Revision, proxyVersionForDPC(dpc), cpVersion)
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

	totalToUpgrade := total - versions[proxyVersionForDPC(dpc)]
	if totalToUpgrade < 1 {
		rateLogger.Infof("All pods have the latest proxy version.")
		return result, nil
	}

	rateLogger.Infof("%d pods are upgraded to %s. %d among %d pods queued and will be upgraded.",
		versions[proxyVersionForDPC(dpc)], proxyVersionForDPC(dpc), totalToUpgrade, total)
	targetPct := float32(proxyTargetBasisPointsForDPC(dpc)*100) / totalBasisPoints
	rateLogger.Infof("target version: %s, percent: %v", proxyVersionForDPC(dpc), targetPct)

	bptsFraction := float32(proxyTargetBasisPointsForDPC(dpc)) / totalBasisPoints
	desired := int(math.Ceil(float64(float32(total) * bptsFraction)))
	if desired < 1 {
		// we have already met our goal, as our goal is zero.  cease updating (if in progress), update status, and exit.
		n.stopUpdateWorkerForDPR(request.NamespacedName)
		dpc.Status = calculateStatus(dpc, total, versions[proxyVersionForDPC(dpc)],
			0, n.metricsRecord)
		n.statusWorker.EnqueueStatus(dpc)
		log.Infof("revision %s meets goal of zero proxies", dpc.Spec.Revision)
		resultMetricLabel = metrics.Success
		return result, nil
	}
	u := n.getOrMakeUpdater(ctx, request.NamespacedName, dpc.Spec.Revision, proxyVersionForDPC(dpc), rateLimitForRollout(dpc, totalToUpgrade))
	projectedActual := versions[proxyVersionForDPC(dpc)] + u.Len()
	log.Debugf("update count projected: %v, desired: %v", projectedActual, desired)
	if projectedActual < desired {
		needed := desired - projectedActual
		enqueued := u.EnqueueNUpdates(needed, proxyVersionForDPC(dpc))
		if enqueued < needed {
			result.Requeue = true
		}
	} else if u.Len() > 0 &&
		float32(projectedActual)/float32(desired) > 1.1 && proxyTargetBasisPointsForDPC(dpc) < totalBasisPoints {
		// we're projected to overshoot by more than 10%.  Purge the updater.
		log.Infof("Dataplane Update Queue for revision %s is expected to overshoot the desired "+
			"ProxyTargetBasisPoints, and the queue will be restarted.", dpc.Spec.Revision)
		u.Stop()
		delete(n.updateworkers, request.NamespacedName)
		result.Requeue = true
	}
	dpc.Status = calculateStatus(dpc, total, versions[proxyVersionForDPC(dpc)],
		u.FailingLen(), n.metricsRecord)
	n.statusWorker.EnqueueStatus(dpc)
	metrics.ReportReconcileState(dpc.Spec.Revision, dpc.Status.State)

	resultMetricLabel = metrics.Success
	return result, nil
}

func proxyVersionForDPC(dpc *v1alpha1.DataPlaneControl) string {
	switch dpc.Spec.ServingMode {
	case v1alpha1.ServingMode_SERVING_MODE_TD:
		return dpc.Spec.ProxyVersionTD
	// UNSPECIFIED is to continue existing behavior before ServingMode is populated.
	case v1alpha1.ServingMode_SERVING_MODE_ISTIOD, v1alpha1.ServingMode_SERVING_MODE_ISTIOD_WITH_LRS, v1alpha1.ServingMode_SERVING_MODE_UNSPECIFIED:
	default:
		log.Errorf("Unexpected serving mode %v, using Istiod proxy version", dpc.Spec.ServingMode)
	}
	return dpc.Spec.ProxyVersion
}

func proxyTargetBasisPointsForDPC(dpc *v1alpha1.DataPlaneControl) int32 {
	switch dpc.Spec.ServingMode {
	case v1alpha1.ServingMode_SERVING_MODE_TD:
		return dpc.Spec.ProxyTargetBasisPointsTD
	// UNSPECIFIED is to continue existing behavior before ServingMode is populated.
	case v1alpha1.ServingMode_SERVING_MODE_ISTIOD, v1alpha1.ServingMode_SERVING_MODE_ISTIOD_WITH_LRS, v1alpha1.ServingMode_SERVING_MODE_UNSPECIFIED:
	default:
		log.Errorf("Unexpected serving mode %v, using Istiod proxy %", dpc.Spec.ProxyTargetBasisPoints)
	}
	return dpc.Spec.ProxyTargetBasisPoints
}

// expectedProxyVersion reports whether the injectedVersion is expected for the given MDP version.
func expectedProxyVersion(mdpProxyVersion, injectedVersion string) bool {
	return injectedVersion == mdpProxyVersion || injectedVersion == revision.DistrolessVersion(mdpProxyVersion)
}

func (n *NewReconciler) stopUpdateWorkerForDPR(dprNsName types.NamespacedName) {
	rateLogger.Info("Stopping update worker")
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

// rateLimitForRollout rate limits based on the duration of the upgrade and the # of pods to be upgraded.
func rateLimitForRollout(dpc *v1alpha1.DataPlaneControl, podCount int) rate.Limit {
	return rate.Every(time.Duration(maxTimeToReconcile(dpc) / int64(podCount)))
}

// maxTimeToReconcile computes the upgrade duration. If InstanceUpgradeDurationHours is set in DPC and unexpired,
// we will use that. Otherwise we will use the default global variable.
func maxTimeToReconcile(dpc *v1alpha1.DataPlaneControl) (maxTimeToReconcile int64) {
	maxTimeToReconcile = int64(MaxTimeToReconcile)
	defer func() {
		if maxTimeToReconcile == int64(MaxTimeToReconcile) {
			rateLogger.Infof("Use the default maximum hours to reconcile proxies: %d", MaxTimeToReconcile)
			return
		}
		rateLogger.Infof("Use the configured maximum hours to reconcile proxies: %d", maxTimeToReconcile)
	}()

	// No configured duration hours. Use default.
	if dpc.Spec.InstanceUpgradeDurationHours < 1 {
		return
	}
	// Get configured duration hours.
	upgradeDurationValidUntil, err := time.Parse(time.RFC3339, dpc.Spec.UpgradeDurationValidUntil)
	if err != nil {
		log.Errorf("Parsing the upgrade duration valid timestamp failed. Falling back to the default value: %w", err)
		return
	}
	if !now().Before(upgradeDurationValidUntil) {
		// Invalid upgrade start timestamp or the duration has expired, revert back to default.
		log.Infof("The upgrade duration %d is already expired. Falling back to the default value.", upgradeDurationValidUntil)
		return
	}
	// Return the gap between Now and upgradeDurationValidUntil.
	maxTimeToReconcile = min(int64(upgradeDurationValidUntil.Sub(now())), int64(MaxTimeToReconcile))
	return
}

func (n *NewReconciler) getOrMakeUpdater(ctx context.Context, dprNsName types.NamespacedName, rev, version string, limit rate.Limit) proxyupdater.UpdateWorker {
	if u, ok := n.updateworkers[dprNsName]; ok {
		u.SetRate(limit, 1)
		return u
	}
	log.Info("Starting update worker.")
	u := upgraderBuilder(n.Clientset)
	result := workerBuilder(rev, version, limit, 1, u, n.ReadPodCache, n.Client, n.eventRecorder)
	result.Start(ctx)
	n.updateworkers[dprNsName] = result
	return result
}

func calculateStatus(dpc *v1alpha1.DataPlaneControl, total int, actual int, failingPodCount int, mr *metricsRecord) v1alpha1.DataPlaneControlStatus {
	revision, generation, targetPoints, UID := dpc.Spec.Revision, dpc.Generation, proxyTargetBasisPointsForDPC(dpc), string(dpc.UID)
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
		ProxyMetrics: &v1alpha1.ProxyMetrics{
			ManagedProxyCount: int32(total),
		},
	}
}

func min(x, y int64) int64 {
	if x < y {
		return x
	}
	return y
}
