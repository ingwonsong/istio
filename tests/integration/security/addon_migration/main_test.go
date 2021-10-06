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

package addonmigration

import (
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/hashicorp/go-multierror"

	"istio.io/istio/pkg/config/protocol"
	"istio.io/istio/pkg/test/echo/common/scheme"
	"istio.io/istio/pkg/test/env"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/echo/echoboot"
	"istio.io/istio/pkg/test/framework/components/echo/util/traffic"
	"istio.io/istio/pkg/test/framework/components/istio"
	"istio.io/istio/pkg/test/framework/components/namespace"
	"istio.io/istio/pkg/test/framework/label"
	"istio.io/istio/pkg/util/istiomultierror"
	"istio.io/istio/tests/integration/security/util"
	"istio.io/istio/tests/integration/security/util/connection"
	"istio.io/pkg/log"
)

var inst istio.Instance

func checkConnectivity(t framework.TestContext, a echo.Instances, b echo.Instances, testPrefix string) {
	t.Helper()
	srcList := []echo.Instance{a[0], b[0]}
	dstList := []echo.Instance{b[0], a[0]}
	for index := range srcList {
		src := srcList[index]
		dst := dstList[index]
		t.NewSubTest(fmt.Sprintf("%s/%s->%s", testPrefix, src.Config().Service, dst.Config().Service)).
			Run(func(ctx framework.TestContext) {
				callOptions := echo.CallOptions{
					Target:   dst,
					PortName: "http",
					Scheme:   scheme.HTTP,
					Count:    1,
				}
				checker := connection.Checker{
					From:          src,
					Options:       callOptions,
					ExpectSuccess: true,
					DestClusters:  b.Clusters(),
				}
				checker.CheckOrFail(ctx)
			})
	}
}

const mtlsDr = `apiVersion: networking.istio.io/v1alpha3
kind: DestinationRule
metadata:
  name: mtls
spec:
  host: "*.%s.svc.cluster.local"
  trafficPolicy:
    tls:
      mode: ISTIO_MUTUAL
`

type loggingWriter struct{}

func (l loggingWriter) Write(p []byte) (n int, err error) {
	if p[len(p)-1] == '\n' {
		log.Info(string(p[:len(p)-1])) // Strip last character, the new line. log.Info already adds it
	} else {
		log.Info(string(p))
	}
	return len(p), nil
}

var _ io.Writer = loggingWriter{}

// TestIstioOnGKEToMeshCA: test zero downtime migration from Istio on GKE to Google Mesh CA
func TestIstioOnGKEToMeshCA(t *testing.T) {
	framework.NewTest(t).
		RequiresSingleCluster().
		Features("security.migrationca.citadel-meshca").
		Run(func(t framework.TestContext) {
			// This test will have two namespaces. The will both start out using the addon. We will
			// continuously send traffic:
			// * Within the "addon" namespace, to ensure the migration doesn't impact existing pods
			// * Between the "addon" namespace and the "migration" namespace, to ensure old pods can connect to new pods
			// * From ingress to the "migration" namespace, to ensure ingress is zero downtime
			// Because the pods restart, we do not send continuous traffic from migration -> addon, but we do send traffic once the migration is complete.
			ingress := inst.IngressFor(t.Clusters().Default())
			addonNamespace := namespace.NewOrFail(t, t, namespace.Config{
				Prefix:   "addon",
				Inject:   true,
				Revision: "default",
			})
			t.Config().ApplyYAMLOrFail(t, addonNamespace.Name(), fmt.Sprintf(mtlsDr, addonNamespace.Name()))

			migrationNamespace := namespace.NewOrFail(t, t, namespace.Config{
				Prefix:   "migration",
				Inject:   true,
				Revision: "default", // Start with default revision, then we swap it later
			})
			// Setup DR to test mtls; the addon didn't have auto mtls enabled
			t.Config().ApplyYAMLOrFail(t, migrationNamespace.Name(), fmt.Sprintf(mtlsDr, migrationNamespace.Name()))
			t.Config().ApplyYAMLOrFail(t, migrationNamespace.Name(), `apiVersion: networking.istio.io/v1alpha3
kind: Gateway
metadata:
  name: app
spec:
  selector:
    istio: ingressgateway
  servers:
  - port:
      number: 80
      name: http
      protocol: HTTP
    hosts:
    - "example.com"
---
apiVersion: networking.istio.io/v1alpha3
kind: VirtualService
metadata:
  name: app
spec:
  hosts:
  - "example.com"
  gateways:
  - app
  http:
  - route:
    - destination:
        host: migration
        port:
          number: 80`)

			builder := echoboot.NewBuilder(t)

			// Create workloads in namespaces served by both CA's
			echos := builder.
				WithClusters(t.Clusters()...).
				WithConfig(util.EchoConfig("addon", addonNamespace, false, nil)).
				WithConfig(util.EchoConfig("addon-client", addonNamespace, false, nil)).
				WithConfig(util.EchoConfig("migration", migrationNamespace, false, nil)).
				BuildOrFail(t)

			addonClientInstances := echos.Match(echo.Service("addon-client"))
			addonInstances := echos.Match(echo.Service("addon"))
			migrationInstances := echos.Match(echo.Service("migration"))

			addonChecker := traffic.NewGenerator(t, traffic.Config{
				Source: addonClientInstances[0],
				Options: echo.CallOptions{
					Target:   addonInstances[0],
					PortName: "http",
				},
				Interval: 200 * time.Millisecond,
			}).Start()

			crossChecker := traffic.NewGenerator(t, traffic.Config{
				Source: addonClientInstances[0],
				Options: echo.CallOptions{
					Target:   migrationInstances[0],
					PortName: "http",
				},
				Interval: 200 * time.Millisecond,
			}).Start()

			// Traffic here is from the go client to the migration namespace, via ingress
			ingressChecker := traffic.NewGenerator(t, traffic.Config{
				Source: ingress,
				Options: echo.CallOptions{
					Port: &echo.Port{
						Protocol: protocol.HTTP,
					},
					Path: "/",
					Headers: map[string][]string{
						"Host": {"example.com"},
					},
				},
				Interval: 200 * time.Millisecond,
			}).Start()

			t.Logf("starting migration")

			c := exec.Command(filepath.Join(env.IstioSrc, "tools/packaging/knative/migrate-addon.sh"), "-y", "-z", "--command", "run-all")
			c.Stdout = loggingWriter{}
			c.Stderr = loggingWriter{}
			if err := c.Run(); err != nil {
				t.Fatal(err)
			}

			t.Logf("migrating namespace to MCP")
			if err := multierror.Append(istiomultierror.New(),
				// nolint: staticcheck
				migrationNamespace.SetLabel("istio.io/rev", t.Settings().Revision),
				migrationNamespace.RemoveLabel("istio-injection")).ErrorOrNil(); err != nil {
				t.Fatal(err)
			}
			for _, i := range migrationInstances {
				if err := i.Restart(); err != nil {
					t.Fatal(err)
				}
			}

			// Check mTLS connectivity works after workload A is signed by meshca
			checkConnectivity(t, addonInstances, migrationInstances, "post-migration")

			// Check both our continuous traffic to ensure we have zero downtime
			t.NewSubTest("continuous-traffic-addon").Run(func(t framework.TestContext) {
				// Addon traffic should always succeed
				addonChecker.Stop().CheckSuccessRate(t, 1)
			})
			t.NewSubTest("continuous-traffic-cross").Run(func(t framework.TestContext) {
				// We allow a small buffer here as the addon did not have graceful shutdown
				crossChecker.Stop().CheckSuccessRate(t, 0.95)
			})
			t.NewSubTest("continuous-traffic-ingress").Run(func(t framework.TestContext) {
				// We allow a small buffer here as the addon did not have graceful shutdown
				ingressChecker.Stop().CheckSuccessRate(t, 0.95)
			})

			t.Logf("starting cleanup")
			cleanupCheck := traffic.NewGenerator(t, traffic.Config{
				Source: ingress,
				Options: echo.CallOptions{
					Port: &echo.Port{
						Protocol: protocol.HTTP,
					},
					Path: "/",
					Headers: map[string][]string{
						"Host": {"example.com"},
					},
				},
				Interval: 200 * time.Millisecond,
			}).Start()
			// Check cleanup
			c = exec.Command(filepath.Join(env.IstioSrc, "tools/packaging/knative/migrate-addon.sh"), "-y", "--command", "cleanup")
			c.Stdout = loggingWriter{}
			c.Stderr = loggingWriter{}
			if err := c.Run(); err != nil {
				t.Fatal(err)
			}
			t.NewSubTest("continuous-traffic-ingress-cleanup").Run(func(t framework.TestContext) {
				// We allow a small buffer here as the addon did not have graceful shutdown
				cleanupCheck.Stop().CheckSuccessRate(t, 0.95)
			})
			// TODO: delete ca-secrets/amend meshConfig to remove trustanchors and ensure that traffic from the 1.4 workloads stops
		})
}

func TestMain(t *testing.M) {
	// Integration test for testing migration of workloads from Istio on GKE (with citadel) to
	// Google Mesh CA based managed control plane
	framework.NewSuite(t).
		Label(label.CustomSetup).
		Setup(istio.Setup(&inst, nil)).
		Run()
}
