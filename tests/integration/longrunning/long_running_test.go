//go:build integ
// +build integ

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

package longrunning

import (
	"log"
	"net/http"
	"os"
	"testing"
	"time"

	"istio.io/istio/pkg/config/protocol"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/echo/echoboot"
	"istio.io/istio/pkg/test/framework/components/echo/util/traffic"
	"istio.io/istio/pkg/test/framework/components/istio"
	"istio.io/istio/pkg/test/framework/components/namespace"
	"istio.io/istio/pkg/test/framework/label"
	"istio.io/istio/pkg/test/framework/resource"
)

const (
	PodASvc          = "a"
	PodBSvc          = "b"
	runDuration      = 5 * time.Minute
	successThreshold = 0.60
)

var (
	i istio.Instance

	// Below are various preconfigured echo deployments. Whenever possible, tests should utilize these
	// to avoid excessive creation/tear down of deployments. In general, a test should only deploy echo if
	// its doing something unique to that specific test.
	PodA echo.Instances
	PodB echo.Instances
	ns   namespace.Instance
)

func TestMain(m *testing.M) {
	framework.
		NewSuite(m).
		Label(label.CustomSetup).
		Setup(istio.Setup(&i, nil)).
		Setup(setupApps).
		Run()
}

func setupApps(t resource.Context) error {
	var err error
	ns, err = namespace.New(t, namespace.Config{
		Prefix: "echo",
		Inject: true,
	})
	if err != nil {
		return err
	}
	echos, err := echoboot.NewBuilder(t).
		WithClusters(t.Clusters()...).
		WithConfig(echoConfig(ns, PodASvc)).
		WithConfig(echoConfig(ns, PodBSvc)).
		Build()
	if err != nil {
		return err
	}

	PodA = echos.Match(echo.Service(PodASvc))
	PodB = echos.Match(echo.Service(PodBSvc))

	return nil
}

func echoConfig(ns namespace.Instance, name string) echo.Config {
	return echo.Config{
		Service:   name,
		Namespace: ns,
		Ports: []echo.Port{
			{
				Name:     "http",
				Protocol: protocol.HTTP,
				// we use a port > 1024 to not require root
				InstancePort: 18080,
			},
		},
		Subsets: []echo.SubsetConfig{{}},
	}
}

func TestLongRunning(t *testing.T) {
	framework.NewTest(t).
		Features("installation.clusters.upgrade").
		Run(func(t framework.TestContext) {
			g := traffic.NewGenerator(t, traffic.Config{
				Source: PodA[0],
				Options: echo.CallOptions{
					Target:   PodB[0],
					PortName: "http",
				},
			}).Start()

			time.Sleep(runDuration / 2)

			if url := os.Getenv("TEST_START_EVENT_URL"); url != "" {
				client := &http.Client{Timeout: 1 * time.Hour}
				log.Printf("firing test start event to %s", url)
				resp, err := client.Get(url)
				if err != nil {
					t.Fatalf("HTTP called failed: %v", err)
				}
				defer resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					log.Printf("HTTP call (%s) returned non-ok status: %d", url, resp.StatusCode)
				}
			}

			time.Sleep(runDuration / 2)

			// Stop the traffic generator and get the result.
			g.Stop().CheckSuccessRate(t, successThreshold)
		})
}
