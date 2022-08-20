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
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	ocprom "contrib.go.opencensus.io/exporter/prometheus"
	"contrib.go.opencensus.io/exporter/stackdriver"
	"contrib.go.opencensus.io/exporter/stackdriver/monitoredresource/gcp"
	"github.com/prometheus/client_golang/prometheus"
	"go.opencensus.io/stats/view"
	"google.golang.org/api/option"

	"istio.io/istio/pilot/pkg/security/model"
	"istio.io/istio/pkg/asm"
	"istio.io/istio/pkg/bootstrap/platform"
	"istio.io/istio/security/pkg/stsservice/tokenmanager"
	"istio.io/istio/security/pkg/util"
	"istio.io/pkg/env"
	"istio.io/pkg/log"
	"istio.io/pkg/version"
)

const (
	authScope                 = "https://www.googleapis.com/auth/cloud-platform"
	workloadIdentitySuffix    = "svc.id.goog"
	hubWorkloadIdentitySuffix = "hub.id.goog"
	cpMetricsPrefix           = "istio.io/control/"
	istiodContainerName       = "discovery"
	cniMetricsPrefix          = "istio.io/internal/mdp/cni/"
	mdpMetricsPrefix          = "istio.io/internal/mdp/controller/"
	mdpContainerName          = "mdp"
	cniInstallContainerName   = "install_cni"
)

var (
	trustDomain  = ""
	podName      = ""
	podNamespace = ""
	meshUID      = ""

	managedRevisionVar = env.RegisterStringVar("REV", "", "name of the managed revision, e.g. asm, asmca, ossmanaged")
)

// ASMExporter is a stats exporter used for ASM control plane metrics.
// It wraps a prometheus exporter and a stackdriver exporter, and exports two types of views.
type ASMExporter struct {
	PromExporter *ocprom.Exporter
	sdExporter   *stackdriver.Exporter
	sdViewMap    map[string]bool
}

type sdExporterConfigs struct {
	labels        *stackdriver.Labels
	containerName string
	metricsPrefix string
	sdEnabled     bool
	sdViewMap     map[string]bool
}

// SetTrustDomain sets GCP trust domain, which is used to fetch GCP metrics.
// Use this function instead of passing trust domain string around to avoid conflicting with OSS changes.
func SetTrustDomain(td string) {
	trustDomain = td
}

// SetPodName sets k8s pod name, which is used in metrics monitored resource.
func SetPodName(pn string) {
	podName = pn
}

// SetPodNamespace sets k8s pod namesapce, which is used in metrics monitored resource.
func SetPodNamespace(pn string) {
	podNamespace = pn
}

// SetMeshUID sets UID of the mesh that this control plane runs in.
func SetMeshUID(uid string) {
	meshUID = uid
}

func AuthenticateClient(gcpMetadata map[string]string) []option.ClientOption {
	clientOptions := []option.ClientOption{}
	// The audience from the token should tell us (in addition to the trust domain)
	// whether or not we should do the token exchange.
	// It's fine to fail for reading from the token because we will use the trust domain as a fallback.
	subjectToken, err := os.ReadFile(model.K8sSATrustworthyJwtFileName)
	var audience string
	if err != nil {
		log.Errorf("Cannot read third party jwt token file: %v", err)
		return clientOptions
	}
	if audiences, _ := util.GetAud(string(subjectToken)); len(audiences) == 1 {
		audience = audiences[0]
	}

	// Do the token exchange if either the trust domain or the audience of the token has the workload identity suffix.
	// The trust domain may not have the workload identity suffix if the user changes to use a different trust domain
	// but the audience of the token should always have if the workload identity is enabled, otherwise it means something
	// wrong in the installation.
	if strings.HasSuffix(trustDomain, workloadIdentitySuffix) || strings.HasSuffix(audience, workloadIdentitySuffix) ||
		strings.HasSuffix(trustDomain, hubWorkloadIdentitySuffix) || strings.HasSuffix(audience, hubWorkloadIdentitySuffix) {
		tm, err := tokenmanager.CreateTokenManager(tokenmanager.GoogleTokenExchange, tokenmanager.Config{TrustDomain: trustDomain})
		if err != nil {
			log.Errorf("Cannot create token manager: %v", err)
			return clientOptions
		}
		// Workload identity is enabled and P4SA access token is used.
		ts := NewTokenSource(tm, string(subjectToken), authScope)
		clientOptions = append(clientOptions, option.WithTokenSource(ts), option.WithQuotaProject(gcpMetadata[platform.GCPProject]))
		// Set up goroutine to read token file periodically and refresh subject token with new expiry.
		go func() {
			for range time.Tick(5 * time.Minute) {
				if subjectToken, err := os.ReadFile(model.K8sSATrustworthyJwtFileName); err == nil {
					ts.RefreshSubjectToken(string(subjectToken))
				} else {
					log.Debugf("Cannot refresh subject token for sts token source: %v", err)
				}
			}
		}()
	}
	return clientOptions
}

// NewControlPlaneExporter is a helper function to get exporter for control plane
func NewControlPlaneExporter() (*ASMExporter, error) {
	gcpMetadata := platform.NewGCP().Metadata()
	labels := &stackdriver.Labels{}
	if meshUID == "" {
		meshUID = meshUIDFromPlatformMeta(gcpMetadata)
	}
	labels.Set("mesh_uid", meshUID, "ID for Mesh")
	labels.Set("revision", version.Info.Version, "Control plane revision")
	// control_plane_version was introduced when "revision" was
	// changed to report the MCP revision instead of the version.
	// Since the revision change was a breaking one, it was
	// revered back to just the version, but we're keeping
	// control_plane_version around even though it's identical to
	// revision now.
	labels.Set("control_plane_version", version.Info.Version, "Control plane version")

	s := sdExporterConfigs{
		labels:        labels,
		metricsPrefix: cpMetricsPrefix,
		containerName: istiodContainerName,
		sdEnabled:     EnableSD,
		sdViewMap:     cpViewMap,
	}
	return newASMExporter(s)
}

// NewMDPExporter is a helper function to get exporter for MDP
func NewMDPExporter(mdpViewMap map[string]bool) (*ASMExporter, error) {
	labels := &stackdriver.Labels{}

	labels.Set("controller_version", version.Info.Version, "MDP controller version")
	s := sdExporterConfigs{
		labels:        labels,
		metricsPrefix: mdpMetricsPrefix,
		containerName: mdpContainerName,
		sdEnabled:     true,
		sdViewMap:     mdpViewMap,
	}
	return newASMExporter(s)
}

// NewCNIExporter is a helper function to get exporter for CNI
func NewCNIExporter(viewMap map[string]bool, vs string) (*ASMExporter, error) {
	labels := &stackdriver.Labels{}

	labels.Set("cni_version", vs, "CNI version")
	s := sdExporterConfigs{
		labels:        labels,
		metricsPrefix: cniMetricsPrefix,
		containerName: cniInstallContainerName,
		sdEnabled:     EnableSD,
		sdViewMap:     viewMap,
	}
	return newASMExporter(s)
}

// newASMExporter creates an ASM opencensus exporter.
func newASMExporter(s sdExporterConfigs) (*ASMExporter, error) {
	pe, err := ocprom.NewExporter(ocprom.Options{Registry: prometheus.DefaultRegisterer.(*prometheus.Registry)})
	if err != nil {
		return nil, fmt.Errorf("could not set up prometheus exporter: %v", err)
	}
	if !s.sdEnabled {
		// Stackdriver monitoring is not enabled, return early with only prometheus exporter initialized.
		return &ASMExporter{
			PromExporter: pe,
		}, nil
	}
	gcpMetadata := platform.NewGCP().Metadata()
	mr := &gcp.GKEContainer{
		ProjectID:                  gcpMetadata[platform.GCPProject],
		ClusterName:                gcpMetadata[platform.GCPCluster],
		Zone:                       gcpMetadata[platform.GCPLocation],
		NamespaceID:                podNamespace,
		PodID:                      podName,
		ContainerName:              s.containerName,
		LoggingMonitoringV2Enabled: true,
	}
	if asm.IsCloudRun() {
		mr.ContainerName = "cr-" + managedRevisionVar.Get()
	}
	clientOptions := []option.ClientOption{}
	if !asm.IsCloudRun() {
		clientOptions = AuthenticateClient(gcpMetadata)
	}
	if ep := endpointOverride.Get(); ep != "" {
		clientOptions = append(clientOptions, option.WithEndpoint(ep))
	}
	// b/237576270: to prevent MDP controller from crashing incase projectID not found from mds.
	var projectID string
	switch {
	case gcpMetadata[platform.GCPProject] != "":
		projectID = gcpMetadata[platform.GCPProject]
	case gcpMetadata[platform.GCPProjectNumber] != "":
		projectID = gcpMetadata[platform.GCPProjectNumber]
	default:
		projectID = "UNSPECIFIED"
	}
	se, err := stackdriver.NewExporter(stackdriver.Options{
		ProjectID:               projectID,
		Location:                gcpMetadata[platform.GCPLocation],
		MetricPrefix:            s.metricsPrefix,
		MonitoringClientOptions: clientOptions,
		TraceClientOptions:      clientOptions,
		GetMetricType: func(view *view.View) string {
			return s.metricsPrefix + view.Name
		},
		MonitoredResource:       mr,
		DefaultMonitoringLabels: s.labels,
		ReportingInterval:       60 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("fail to initialize Stackdriver exporter: %v", err)
	}

	if asm.IsCloudRun() {
		return &ASMExporter{
			sdExporter: se,
			sdViewMap:  s.sdViewMap,
		}, nil
	}
	return &ASMExporter{
		PromExporter: pe,
		sdExporter:   se,
		sdViewMap:    s.sdViewMap,
	}, nil
}

// ExportView exports all views collected by control plane process.
// This function distinguished views for Stackdriver and views for Prometheus and exporting them separately.
func (e *ASMExporter) ExportView(vd *view.Data) {
	if _, ok := e.sdViewMap[vd.View.Name]; ok && e.sdExporter != nil {
		// This indicates that this is a stackdriver view
		log.Debugf("asmExporter exporting view: %s", vd.View.Name)
		e.sdExporter.ExportView(vd)
	} else if e.PromExporter != nil {
		// nolint: staticcheck
		e.PromExporter.ExportView(vd)
	}
}

// TestExporter is used for GCP monitoring test.
type TestExporter struct {
	sync.Mutex

	Rows        map[string][]*view.Row
	invalidTags bool
}

// ExportView exports test views.
func (t *TestExporter) ExportView(d *view.Data) {
	t.Lock()
	defer t.Unlock()
	for _, tk := range d.View.TagKeys {
		if len(tk.Name()) < 1 {
			t.invalidTags = true
		}
	}
	t.Rows[d.View.Name] = append(t.Rows[d.View.Name], d.Rows...)
}

func revisionLabel() string {
	if asm.IsCloudRun() {
		return managedRevisionVar.Get()
	}
	return version.Info.Version
}
