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

package bootstrap

import (
	"fmt"
	"net"
	"net/http"
	"time"

	"go.opencensus.io/stats/view"

	"istio.io/istio/pilot/pkg/gcpmonitoring"
	"istio.io/istio/pkg/asm"
	"istio.io/pkg/log"
	"istio.io/pkg/monitoring"
	"istio.io/pkg/version"
)

type monitor struct {
	monitoringServer *http.Server
}

const (
	metricsPath = "/metrics"
	versionPath = "/version"
)

var (
	serverStart = time.Now()

	uptime = monitoring.NewDerivedGauge( // nolint: deadcode, varcheck
		"istiod_uptime_seconds",
		"Current istiod server uptime in seconds",
	)
)

func init() {
	uptime.ValueFrom(func() float64 {
		return time.Since(serverStart).Seconds()
	})
}

func addMonitor(mux *http.ServeMux) error {
	asmExporter, err := gcpmonitoring.NewControlPlaneExporter()
	if err != nil {
		return err
	}
	view.RegisterExporter(asmExporter)
	mux.Handle(metricsPath, asmExporter.PromExporter)

	mux.HandleFunc(versionPath, func(out http.ResponseWriter, req *http.Request) {
		if _, err := out.Write([]byte(version.Info.String())); err != nil {
			log.Errorf("Unable to write version string: %v", err)
		}
	})

	return nil
}

// Deprecated: we shouldn't have 2 http ports. Will be removed after code using
// this port is removed.
func startMonitor(addr string, mux *http.ServeMux) (*monitor, error) {
	m := &monitor{}

	// get the network stuff setup
	var listener net.Listener
	if addr != "" {
		var err error
		if listener, err = net.Listen("tcp", addr); err != nil {
			return nil, fmt.Errorf("unable to listen on socket: %v", err)
		}
	}

	// NOTE: this is a temporary solution to provide bare-bones debug functionality
	// for pilot. a full design / implementation of self-monitoring and reporting
	// is coming. that design will include proper coverage of statusz/healthz type
	// functionality, in addition to how pilot reports its own metrics.
	if err := addMonitor(mux); err != nil {
		return nil, fmt.Errorf("could not establish self-monitoring: %v", err)
	}
	if addr != "" {
		m.monitoringServer = &http.Server{
			Addr:        listener.Addr().String(),
			Handler:     mux,
			IdleTimeout: 90 * time.Second, // matches http.DefaultTransport keep-alive timeout
			ReadTimeout: 30 * time.Second,
		}
	}

	version.Info.RecordComponentBuildTag("pilot")

	if addr != "" {
		go func() {
			_ = m.monitoringServer.Serve(listener)
		}()
	}

	return m, nil
}

func (m *monitor) Close() error {
	if m.monitoringServer != nil {
		return m.monitoringServer.Close()
	}
	return nil
}

// initMonitor initializes the configuration for the pilot monitoring server.
func (s *Server) initMonitor(addr string) error { // nolint: unparam
	s.addStartFunc(func(stop <-chan struct{}) error {
		// For MCP, we skip this as we do it instead at the very start of the process, to ensure ALL logs get handled by stackdriver
		// For in cluster we need mesh config access which requires us to configure things later.
		if !asm.IsCloudRun() {
			gcpmonitoring.SetTrustDomain(s.environment.Mesh().TrustDomain)
			gcpmonitoring.SetPodName(PodName)
			gcpmonitoring.SetPodNamespace(PodNamespace)
			if s.environment.Mesh().DefaultConfig != nil {
				gcpmonitoring.SetMeshUID(s.environment.Mesh().DefaultConfig.MeshId)
			}
			loggingOpts := gcpmonitoring.ASMLogOptions(log.DefaultOptions())
			if err := log.Configure(loggingOpts); err != nil {
				log.Warnf("Logging not configured: %v", err)
				return err
			}
		}
		monitor, err := startMonitor(addr, s.monitoringMux)
		if err != nil {
			return err
		}
		go func() {
			<-stop
			err := monitor.Close()
			log.Debugf("Monitoring server terminated: %v", err)
		}()
		return nil
	})
	return nil
}
