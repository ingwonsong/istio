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

package metrics

import (
	"time"

	"istio.io/istio/mdp/controller/pkg/apis/mdp/v1alpha1"
	"istio.io/istio/mdp/controller/pkg/name"
)

const (
	SuccessLabel = "SUCCESS"
	FailureLabel = "FAILURE"
)

type ReconcileLoopResult string

const (
	Unknown       ReconcileLoopResult = "UNKNOWN"
	ResourceError ReconcileLoopResult = "RESOURCE_ERROR"
	VersionError  ReconcileLoopResult = "VERSION_ERROR"
	Success       ReconcileLoopResult = "SUCCESS"
)

// ReportProxyPercentageTarget reports proxy percentage target from dpc
func ReportProxyPercentageTarget(proxyVersion, revision string, proxyPercentageTargetVal int32) {
	proxyPercentageTarget.With(proxyVersionLabel.Value(proxyVersion),
		revisionLabel.Value(revision)).Record(float64(proxyPercentageTargetVal))
}

// ReportProxies reports proxies observed by mdp
// TODO(iamwen): add the proxy state label
func ReportProxies(versionCount map[string]int, revision string) {
	for ver, count := range versionCount {
		ReportProxiesSingleVersion(ver, revision, count)
	}
}

// ReportProxiesSingleVersion reports proxies of specific version observed by mdp
func ReportProxiesSingleVersion(ver, revision string, count int) {
	proxies.With(proxyVersionLabel.Value(ver),
		ownerLabel.Value(name.MDPOwner),
		revisionLabel.Value(revision)).RecordInt(int64(count))
}

// ReportUpgradedProxiesCount reports upgraded_proxy_count metric
func ReportUpgradedProxiesCount(fromProxyVersion,
	toProxyVersion, result, revision string) {
	upgradedProxiesCount.With(fromProxyVersionLabel.Value(fromProxyVersion),
		toProxyVersionLabel.Value(toProxyVersion),
		resultLabel.Value(result),
		revisionLabel.Value(revision)).Increment()
}

// ReportReconcileLoopCount reports reconcile_loops_count metric
func ReportReconcileLoopCount(result ReconcileLoopResult, revision string) {
	reconcileLoopsCount.
		With(resultLabel.Value(string(result))).
		With(revisionLabel.Value(revision)).
		Increment()
}

// ReportReconcileLoopCount reports reconcile_loops_count metric
func ReportRebuildCacheCount(cacheName string) {
	rebuildCacheCount.
		With(revisionLabel.Value(cacheName)).
		Increment()
}

// ReportReconcileState reports reconcile_state metric
func ReportReconcileState(revision string, state v1alpha1.DataPlaneState) {
	// For a revision there is only one possible state.
	// Set the given state to 1, but all other states to 0.

	// if state is empty, skip reporting.
	// This can happen if reconcile loop fails to get DPControl.
	if revision == "" {
		return
	}

	for _, s := range []v1alpha1.DataPlaneState{
		v1alpha1.Unknown, v1alpha1.Error, v1alpha1.Ready, v1alpha1.Reconciling,
	} {
		v := 0
		if s == state {
			v = 1
		}
		reconcileState.
			With(stateLabel.Value(string(s))).
			With(revisionLabel.Value(revision)).
			RecordInt(int64(v))
	}
}

// ReportReconcileDuration reports reconcile_duration metric.
// it calculates the duration by comparing current ready time
// with the first unready time for a specific generation of DPC.
func ReportReconcileDuration(revision string, firstUnReadyTimeSecond int) {
	reconcileDuration.
		With(revisionLabel.Value(revision)).
		Record(float64(time.Now().Second() - firstUnReadyTimeSecond))
}

// ReportMDPUpTime reports uptime metric
func ReportMDPUpTime(ut float64) {
	upTime.RecordInt(int64(ut))
}

// ReportServingState reports serving_state metric
func ReportServingState(state string) {
	servingState.With(stateLabel.Value(state)).Increment()
}
