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

package migration

import (
	"context"
	"fmt"
	"os"
	"time"

	"contrib.go.opencensus.io/exporter/stackdriver"
	"contrib.go.opencensus.io/exporter/stackdriver/monitoredresource/gcp"
	"go.opencensus.io/stats/view"
	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"istio.io/istio/pilot/pkg/gcpmonitoring"
	"istio.io/istio/pilot/pkg/security/model"
	"istio.io/istio/pkg/bootstrap/platform"
	"istio.io/pkg/log"
	"istio.io/pkg/monitoring"
)

const (
	containerName        = "addon-migration"
	istioSystemNamespace = "istio-system"
	serviceAccount       = "istio-addon-migration-service-account"
	tokenDir             = "/var/run/secrets/tokens"
	wlPoolFormat         = "%s.svc.id.goog"
	metricsPrefix        = "istio.io/internal/migration/addon/"
)

var (
	projectID  string
	stateLabel = monitoring.MustCreateLabel("state")
)

func (m *migrationWorker) InitializeMonitoring(clusterName, location string) (*MGExporter, error) {
	// common practice for metrics to set pod name/namespace to unknown if actual names not found
	podName := getEnv("POD_NAME", "unknown")
	podNamespace := getEnv("POD_NAMESPACE", "unknown")
	gcpMetadata := platform.NewGCP().Metadata()
	if pi, ok := gcpMetadata[platform.GCPProject]; ok {
		projectID = pi
	}
	if err := os.MkdirAll(tokenDir, os.FileMode(0o744)); err != nil {
		return nil, fmt.Errorf("failed to create token dir: %v", err)
	}
	if err := m.RefreshToken(); err != nil {
		return nil, fmt.Errorf("failed to create new token: %v", err)
	}

	labels := &stackdriver.Labels{}
	clientOptions := gcpmonitoring.AuthenticateClient(gcpMetadata)
	mr := &gcp.GKEContainer{
		ProjectID:                  projectID,
		ClusterName:                clusterName,
		Zone:                       location,
		NamespaceID:                podNamespace,
		PodID:                      podName,
		ContainerName:              containerName,
		LoggingMonitoringV2Enabled: true,
	}
	se, err := stackdriver.NewExporter(stackdriver.Options{
		ProjectID:               projectID,
		Location:                location,
		MetricPrefix:            metricsPrefix,
		MonitoringClientOptions: clientOptions,
		TraceClientOptions:      clientOptions,
		GetMetricType: func(view *view.View) string {
			return metricsPrefix + view.Name
		},
		MonitoredResource:       mr,
		DefaultMonitoringLabels: labels,
		ReportingInterval:       60 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("fail to initialize Stackdriver exporter: %v", err)
	}
	return &MGExporter{sdExporter: se}, nil
}

func (m *migrationWorker) RefreshToken() error {
	var tokenDuration int64 = 43200
	wlPool := fmt.Sprintf(wlPoolFormat, projectID)
	token := &authenticationv1.TokenRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceAccount,
			Namespace: istioSystemNamespace,
		},
		Spec: authenticationv1.TokenRequestSpec{
			Audiences:         []string{wlPool},
			ExpirationSeconds: &tokenDuration,
		},
	}

	tokenReq, err := m.kubeClient.CoreV1().ServiceAccounts(istioSystemNamespace).CreateToken(context.Background(), serviceAccount, token, metav1.CreateOptions{})
	// errors if the token could not be created with the given service account in the given namespace
	if err != nil {
		return fmt.Errorf("failed to create a token under service account %s in namespace: %v", serviceAccount, err)
	}
	if err := os.WriteFile(model.K8sSATrustworthyJwtFileName, []byte(tokenReq.Status.Token), os.FileMode(0o744)); err != nil {
		return fmt.Errorf("failed to write jwt to local fs: %v", err)
	}
	log.Info("Security token for service account has been generated and stored")
	return nil
}

func getEnv(envName, fallback string) string {
	val, found := os.LookupEnv(envName)
	if !found {
		return fallback
	}
	return val
}

type MGExporter struct {
	sdExporter *stackdriver.Exporter
}

func (e *MGExporter) ExportView(vd *view.Data) {
	if e.sdExporter != nil {
		log.Debugf("MigrationExporter exporting view: %s", vd.View.Name)
		e.sdExporter.ExportView(vd)
	}
}
