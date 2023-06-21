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

package gcpmonitoring

import (
	"context"
	"strings"

	"go.opencensus.io/stats" // nolint: depguard
	"go.opencensus.io/tag"   // nolint: depguard

	"istio.io/istio/pkg/monitoring"
)

var (
	typeHookLabel     = monitoring.MustCreateLabel("type")
	eventHookLabel    = monitoring.MustCreateLabel("event")
	resourceHookLabel = monitoring.MustCreateLabel("resource")
	versionHookLabel  = monitoring.MustCreateLabel("version")

	pilotK8sCfgEvents              = "pilot_k8s_cfg_events"
	pilotK8sRegEvents              = "pilot_k8s_reg_events"
	galleyValidationPassed         = "galley/validation/passed"
	galleyValidationFailed         = "galley/validation/failed"
	pilotXDSPushes                 = "pilot_xds_pushes"
	pilotXDSEDSReject              = "pilot_xds_eds_reject"
	pilotXDSRDSReject              = "pilot_xds_rds_reject"
	pilotXDSLDSReject              = "pilot_xds_lds_reject"
	pilotXDSCDSReject              = "pilot_xds_cds_reject"
	pilotProxyConvergenceTime      = "pilot_proxy_convergence_time"
	pilotXDS                       = "pilot_xds"
	sidecarInjectionSuccessTotal   = "sidecar_injection_success_total"
	sidecarInjectionFailureTotal   = "sidecar_injection_failure_total"
	sidecarInjectionSkipTotal      = "sidecar_injection_skip_total"
	ipBasedRemoteSecrets           = "ip_based_remote_secrets"
	ipBasedRemoteSecretsTranslated = "ip_based_remote_secrets_translated"
)

type gcpRecordHook struct{}

var _ monitoring.RecordHook = &gcpRecordHook{}

func (r gcpRecordHook) OnRecord(name string, tags monitoring.LabelSet, value float64) {
	switch name {
	case pilotK8sCfgEvents, pilotK8sRegEvents:
		onPilotK8sCfgEvents(tags, value)
	case galleyValidationPassed:
		onGalleyValidationPass(tags, value)
	case galleyValidationFailed:
		onGalleyValidationFailed(tags, value)
	case pilotXDSPushes:
		onPilotXDSPushes(tags, value)
	case pilotXDSEDSReject, pilotXDSRDSReject, pilotXDSLDSReject, pilotXDSCDSReject:
		onPilotXDSReject(name, tags, value)
	case pilotProxyConvergenceTime:
		onPilotConfigConvergence(tags, value)
	case pilotXDS:
		onPilotXDS(tags, value)
	case sidecarInjectionSuccessTotal, sidecarInjectionFailureTotal, sidecarInjectionSkipTotal:
		onSidecarInjection(name, tags, value)
	case ipBasedRemoteSecrets:
		onIPBasedRemoteSecretProcessed(tags, value)
	case ipBasedRemoteSecretsTranslated:
		onIPBasedRemoteSecretTranslated(tags, value)
	}
}

func registerHook() {
	hook := gcpRecordHook{}
	monitoring.RegisterRecordHook(pilotK8sCfgEvents, hook)
	monitoring.RegisterRecordHook(pilotK8sRegEvents, hook)
	monitoring.RegisterRecordHook(galleyValidationPassed, hook)
	monitoring.RegisterRecordHook(galleyValidationFailed, hook)
	monitoring.RegisterRecordHook(pilotXDSPushes, hook)
	monitoring.RegisterRecordHook(pilotXDSEDSReject, hook)
	monitoring.RegisterRecordHook(pilotXDSRDSReject, hook)
	monitoring.RegisterRecordHook(pilotXDSLDSReject, hook)
	monitoring.RegisterRecordHook(pilotXDSCDSReject, hook)
	monitoring.RegisterRecordHook(pilotProxyConvergenceTime, hook)
	monitoring.RegisterRecordHook(pilotXDS, hook)
	monitoring.RegisterRecordHook(sidecarInjectionSuccessTotal, hook)
	monitoring.RegisterRecordHook(sidecarInjectionFailureTotal, hook)
	monitoring.RegisterRecordHook(sidecarInjectionSkipTotal, hook)
	monitoring.RegisterRecordHook(ipBasedRemoteSecrets, hook)
	monitoring.RegisterRecordHook(ipBasedRemoteSecretsTranslated, hook)
}

func onPilotK8sCfgEvents(tags monitoring.LabelSet, value float64) {
	t, found := tags.Value(typeHookLabel)
	if !found {
		return
	}
	e, found := tags.Value(eventHookLabel)
	if !found {
		return
	}
	ctx, err := tag.New(context.Background(), tag.Insert(operationKey, e), tag.Insert(typeKey, t))
	if err != nil {
		return
	}
	stats.Record(ctx, configEventMeasure.M(int64(value)))
}

func onGalleyValidationPass(tags monitoring.LabelSet, value float64) {
	res, found := tags.Value(resourceHookLabel)
	if !found {
		return
	}
	ctx, err := tag.New(context.Background(), tag.Insert(typeKey, res), tag.Insert(successKey, "true"))
	if err != nil {
		return
	}
	stats.Record(ctx, configValidationMeasuare.M(int64(value)))
}

func onGalleyValidationFailed(tags monitoring.LabelSet, value float64) {
	res, found := tags.Value(resourceHookLabel)
	if !found {
		return
	}
	ctx, err := tag.New(context.Background(), tag.Insert(typeKey, res), tag.Insert(successKey, "false"))
	if err != nil {
		return
	}
	stats.Record(ctx, configValidationMeasuare.M(int64(value)))
}

func onPilotXDSPushes(tags monitoring.LabelSet, value float64) {
	t, found := tags.Value(typeHookLabel)
	if !found || len(t) < 3 {
		return
	}
	xdsType := strings.ToUpper(t[0:3])
	status := "true"
	if len(t) > 3 {
		status = "false"
	}

	ctx, err := tag.New(context.Background(), tag.Insert(typeKey, xdsType), tag.Insert(successKey, status))
	if err != nil {
		return
	}
	stats.Record(ctx, configPushMeasuare.M(int64(value)))
}

func onPilotXDSReject(name string, _ monitoring.LabelSet, value float64) {
	// measure name is patterned as pilot_xds_xxx_reject, where xxx is the xds type.
	xdsType := strings.ToUpper(name[10:13])
	ctx, err := tag.New(context.Background(), tag.Insert(typeKey, xdsType))
	if err != nil {
		return
	}
	stats.Record(ctx, rejectedConfigMeasuare.M(int64(value)))
}

func onPilotConfigConvergence(_ monitoring.LabelSet, value float64) {
	stats.Record(context.Background(), configConvergenceMeasuare.M(value))
}

func onPilotXDS(tags monitoring.LabelSet, value float64) {
	res, found := tags.Value(versionHookLabel)
	if !found {
		return
	}
	ctx, err := tag.New(context.Background(), tag.Insert(proxyVersionKey, res))
	if err != nil {
		return
	}
	stats.Record(ctx, proxyClientsMeasure.M(int64(value)))
}

func onSidecarInjection(name string, _ monitoring.LabelSet, value float64) {
	status := ""
	switch name {
	case sidecarInjectionSuccessTotal:
		status = "true"
	case sidecarInjectionFailureTotal:
		status = "false"
	default:
		return
	}
	ctx, err := tag.New(context.Background(), tag.Insert(successKey, status))
	if err != nil {
		return
	}
	stats.Record(ctx, sidecarInjectionMeasure.M(int64(value)))
}

func onIPBasedRemoteSecretProcessed(_ monitoring.LabelSet, value float64) {
	stats.Record(context.Background(), ipBasedRemoteSecretsMeasure.M(int64(value)))
}

func onIPBasedRemoteSecretTranslated(tags monitoring.LabelSet, value float64) {
	success, f := tags.Value(successLabel)
	if !f {
		return
	}

	ctx, err := tag.New(context.Background(), tag.Insert(successKey, success))
	if err != nil {
		return
	}

	stats.Record(ctx, ipBasedRemoteSecretsTranslatedMeasure.M(int64(value)))
}
