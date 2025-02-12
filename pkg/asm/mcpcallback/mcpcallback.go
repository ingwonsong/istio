// Copyright Istio Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package status provides functionalities for reporting the critical issue to Thetis.
package mcpcallback

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/googleapi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"istio.io/istio/pkg/asm"
	"istio.io/istio/pkg/bootstrap/platform"
	"istio.io/istio/pkg/env"
	"istio.io/istio/pkg/log"
	"istio.io/istio/pkg/util/protomarshal"
)

var (
	reporter    statusReporter
	reportTimer *time.Timer
)

type statusReporter struct {
	mu     sync.Mutex
	status *status.Status
	sent   bool
}

func (r *statusReporter) recordStatus(st *status.Status) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.sent {
		r.status = st
		return true
	}
	return false
}

// sendStatus sends the given status to Callback service.
// If statusToSend is nil, the last recorded status is sent.
func (r *statusReporter) sendStatus(ctx context.Context, statusToSend *status.Status) {
	if !enabled {
		return
	}

	r.mu.Lock()
	if r.sent || (statusToSend == nil && r.status == nil) {
		r.mu.Unlock()
		// 1. If a status is already sent, don't send again.
		// 2. If there is no status to report, do not proceed.
		return
	}
	st := statusToSend
	if st == nil {
		st = status.FromProto(r.status.Proto())
	}
	r.mu.Unlock()

	ctx, cfn := context.WithTimeout(ctx, 10*time.Second)
	defer cfn()
	if err := report(ctx, st); err != nil {
		log.Warnf("failed to report to Callback service: %v", err)
	} else {
		r.sent = true
		log.Infof("succeeded to report to Callback service: %s", st.String())
	}
}

// Start starts a timer to trigger sending the last reported error
// to Callback service. Currently, after 230s, it will try to send the
// error.
// CloudRun triggers the timeout after 240s for startup probe.
// To have a enough room for reporting the last error, report it 10s
// before the timeout.
func Start() {
	if enabled {
		log.Infof("Reporting to Callback is enabled.")
		reportTimer = time.AfterFunc(230*time.Second, func() {
			// nil status triggers to send the last reported error.
			reporter.sendStatus(context.Background(), nil)
		})
	}
}

// Stop stops the error report timer which was started by `Start`
func Stop() {
	if reportTimer != nil {
		reportTimer.Stop()
	}
}

// RecordError records the `errorToReport` as a last status.
// The last status will be reported after a certain amount
// of time from the start of the process, to wait for the
// CloudRun startup timeout.
// If `errorToReport` is `nil`, it is not reported.
// When it is needed to report "OK", use SendOK explicitly.
func RecordError(errorToReport error) {
	if errorToReport == nil {
		return
	}
	reporter.recordStatus(transform(errorToReport))
}

// SendOK sends the OK status to Callback service immediately if
// no status was reported.
// The recorded last error will be not sent if it is not already sent.
func SendOK() {
	if reportTimer == nil || reportTimer.Stop() {
		reporter.sendStatus(context.Background(), status.New(codes.OK, "OK"))
	}
}

// SendError sends the error status to Callback service immediately if
// no status was reported.
// The recorded last error will be not sent if it is not already sent.
func SendError(errorToReport error) {
	if reportTimer == nil || reportTimer.Stop() {
		reporter.sendStatus(context.Background(), transform(errorToReport))
	}
}

var (
	startTime = time.Now()
	enabled   = env.RegisterBoolVar("ENABLE_ERROR_PROPAGATION", false, "Enable the error propagation.").Get()
)

func reportStatusURL(p asm.MCPParameters) string {
	api := strings.TrimSuffix(p.XDSAddr, ":443")
	const apiFmt = "https://%s/v1internal/projects/%s/locations/%s/clusters/%s/controlPlanes/%s:reportStatus"
	return fmt.Sprintf(apiFmt, api, p.Project, p.Zone, p.Cluster, p.Revision)
}

func toPayload(st *status.Status, startupDuration time.Duration, rev, instanceID, tenantProject string) (string, error) {
	t, err := protomarshal.ToJSON(durationpb.New(startupDuration))
	if err != nil {
		return "", err
	}
	s, err := protomarshal.ToJSON(st.Proto())
	if err != nil {
		return "", err
	}
	tmpl := `{ "startup_duration": %s, "status": %s, "revision": %q, "instance_id": %q, "tenant_project": "projects/%s" }`
	return fmt.Sprintf(tmpl, t, s, rev, instanceID, tenantProject), nil
}

func report(ctx context.Context, st *status.Status) error {
	p, err := asm.MCPParametersFromEnv()
	if err != nil {
		return fmt.Errorf("failed to get MCPParameters: %w", err)
	}
	md := platform.NewGCP().Metadata()
	payload, err := toPayload(st, time.Since(startTime), p.KRevision, md[platform.GCEInstanceID], md[platform.GCPProjectNumber])
	if err != nil {
		return fmt.Errorf("failed to marshal the payload of a request to Callback: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reportStatusURL(p), strings.NewReader(payload))
	if err != nil {
		return fmt.Errorf("failed to create a HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	cli, err := client(ctx)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	resp, err := cli.Do(req)
	if err != nil {
		return fmt.Errorf("failed to call the Callback API: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		return nil
	}

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to get 200 response code(got %d) and failed to get the body message: %w", resp.StatusCode, err)
	}
	return fmt.Errorf("failed to get 200 response code(got %d): %s", resp.StatusCode, b)
}

func client(ctx context.Context) (*http.Client, error) {
	creds, err := google.FindDefaultCredentials(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return nil, err
	}
	return oauth2.NewClient(ctx, creds.TokenSource), nil
}

type googleErrorEntry struct {
	Message string `json:"message"`
	// Canonical code in string. (e.g., FAILED_PRECONDITION)
	Status string `json:"status"`
}

type googleErrorPayload struct {
	Error googleErrorEntry `json:"error"`
}

func parseGoogleAPIError(googleAPIError *googleapi.Error) (codes.Code, string) {
	msg := googleAPIError.Message
	canonicalCode := toCanonicalCode(googleAPIError.Code)

	// Try to parse the body of the error response. (b/298286518)
	var errorPayloads []googleErrorPayload
	err := json.Unmarshal([]byte(googleAPIError.Body), &errorPayloads)
	if err == nil && len(errorPayloads) > 0 {
		if errorPayloads[0].Error.Status != "" {
			var code codes.Code
			if code.UnmarshalJSON([]byte(errorPayloads[0].Error.Status)) == nil {
				canonicalCode = code
			}
		}
		if errorPayloads[0].Error.Message != "" {
			msg = errorPayloads[0].Error.Message
		}
	}

	if msg == "" {
		// In some cases (e.g., permission error), Message seems to be empty.
		// In that case, use the stringified error message.
		msg = googleAPIError.Error()
	}
	return canonicalCode, msg
}

func transform(errorToReport error) *status.Status {
	if st, ok := status.FromError(errorToReport); ok {
		return st
	}

	// If the error is returned by K8S client, handle it here.
	var apiStatus apierrors.APIStatus
	if errors.As(errorToReport, &apiStatus) {
		st := status.New(toCanonicalCode(int(apiStatus.Status().Code)), apiStatus.Status().Message)
		return maybeWithDetail(st, apiStatus.Status().Details.String())
	}

	// If the error is returned by Google API client, handle it here.
	var googleAPIError *googleapi.Error
	if errors.As(errorToReport, &googleAPIError) {
		return maybeWithDetails(status.New(parseGoogleAPIError(googleAPIError)), googleAPIError.Details)
	}

	return status.New(codes.Internal, errorToReport.Error())
}

var http2canonical = map[int]codes.Code{
	http.StatusOK:                           codes.OK,
	http.StatusBadRequest:                   codes.InvalidArgument,
	http.StatusForbidden:                    codes.PermissionDenied,
	http.StatusNotFound:                     codes.NotFound,
	http.StatusConflict:                     codes.Aborted,
	http.StatusRequestedRangeNotSatisfiable: codes.OutOfRange,
	http.StatusTooManyRequests:              codes.ResourceExhausted,
	499:                                     codes.Canceled,
	http.StatusGatewayTimeout:               codes.DeadlineExceeded,
	http.StatusNotImplemented:               codes.Unimplemented,
	http.StatusServiceUnavailable:           codes.Unavailable,
	http.StatusUnauthorized:                 codes.Unauthenticated,
}

func toCanonicalCode(httpCode int) codes.Code {
	if code, ok := http2canonical[httpCode]; ok {
		return code
	}
	switch {
	case httpCode >= 200 && httpCode < 300:
		return codes.OK
	case httpCode >= 400 && httpCode < 500:
		return codes.FailedPrecondition
	case httpCode >= 500 && httpCode < 600:
		return codes.Internal
	}
	return codes.Unknown
}

func maybeWithDetail(st *status.Status, detail string) *status.Status {
	p := st.Proto()
	anyValue, err := anypb.New(wrapperspb.String(detail))
	if err != nil {
		// Just skip the detail.
		return st
	}
	p.Details = []*anypb.Any{anyValue}
	return status.FromProto(p)
}

func maybeWithDetails(st *status.Status, details []any) *status.Status {
	p := st.Proto()
	for _, detail := range details {
		anyValue, err := anypb.New(wrapperspb.String(fmt.Sprintf("%v", detail)))
		if err != nil {
			// Just skip the detail.
			continue
		}
		p.Details = append(p.Details, anyValue)
	}
	return status.FromProto(p)
}
