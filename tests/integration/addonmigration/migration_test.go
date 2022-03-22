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
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"istio.io/istio/pilot/pkg/model"
	"istio.io/istio/pkg/config/protocol"
	"istio.io/istio/pkg/http/headers"
	"istio.io/istio/pkg/test/env"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/echo/deployment"
	"istio.io/istio/pkg/test/framework/components/echo/match"
	"istio.io/istio/pkg/test/framework/components/echo/util/traffic"
	"istio.io/istio/pkg/test/framework/components/namespace"
	"istio.io/istio/pkg/test/kube"
	"istio.io/istio/tests/integration/security/util"
)

// TestIstioOnGKEToMeshCA: test zero downtime migration from Istio on GKE to Google Mesh CA
func TestIstioOnGKEToMeshCA(t *testing.T) {
	framework.NewTest(t).
		RequiresSingleCluster().
		Features("security.migrationca.citadel-meshca").
		Run(func(t framework.TestContext) {
			ingress := inst.IngressFor(t.Clusters().Default())
			stable14Namespace := namespace.NewOrFail(t, t, namespace.Config{
				Prefix:   "stable-14",
				Inject:   true,
				Revision: "default",
			})
			t.ConfigKube().YAML(stable14Namespace.Name(), fmt.Sprintf(mtlsDr, stable14Namespace.Name())).ApplyOrFail(t)
			stable16Namespace := namespace.NewOrFail(t, t, namespace.Config{
				Prefix:   "stable-16",
				Inject:   true,
				Revision: "istio-1611",
			})
			t.ConfigKube().YAML(stable16Namespace.Name(), fmt.Sprintf(mtlsDr, stable16Namespace.Name())).ApplyOrFail(t)

			migration14Namespace := namespace.NewOrFail(t, t, namespace.Config{
				Prefix:   "migration-14",
				Inject:   true,
				Revision: "default", // Start with default revision, then we swap it later
			})

			migration16Namespace := namespace.NewOrFail(t, t, namespace.Config{
				Prefix:   "migration-16",
				Inject:   true,
				Revision: "istio-1611", // Start with default revision, then we swap it later
			})
			// Setup DR to test mtls; the addon didn't have auto mtls enabled
			for _, ns := range []namespace.Instance{migration14Namespace, migration16Namespace} {
				t.ConfigKube().YAML(ns.Name(), fmt.Sprintf(mtlsDr, ns.Name())).ApplyOrFail(t)
				t.ConfigKube().YAML(ns.Name(), fmt.Sprintf(gwVS, ns.Name(), ns.Name())).ApplyOrFail(t)
			}

			builder := deployment.New(t)

			// Create workloads in namespaces served by both CA's
			echos := builder.
				WithClusters(t.Clusters()...).
				WithConfig(util.EchoConfig("addon", stable14Namespace, false, nil)).
				WithConfig(util.EchoConfig("addon", stable16Namespace, false, nil)).
				WithConfig(util.EchoConfig("migration", migration14Namespace, false, nil)).
				WithConfig(util.EchoConfig("migration", migration16Namespace, false, nil)).
				BuildOrFail(t)
			stable14 := match.And(match.ServiceName(model.NamespacedName{Name: "addon", Namespace: stable14Namespace.Name()})).GetMatches(echos)
			migration14 := match.And(match.ServiceName(model.NamespacedName{Name: "migration", Namespace: migration14Namespace.Name()})).GetMatches(echos)
			stable16 := match.And(match.ServiceName(model.NamespacedName{Name: "addon", Namespace: stable16Namespace.Name()})).GetMatches(echos)
			migration16 := match.And(match.ServiceName(model.NamespacedName{Name: "migration", Namespace: migration16Namespace.Name()})).GetMatches(echos)

			t.Log("starting traffic...")
			selfCheck14 := traffic.NewGenerator(t, traffic.Config{
				Source: stable14[0],
				Options: echo.CallOptions{
					To:   stable14[0],
					Port: echo.Port{Name: "http"},
				},
				Interval: 200 * time.Millisecond,
			}).Start()

			crossCheck14 := traffic.NewGenerator(t, traffic.Config{
				Source: stable14[0],
				Options: echo.CallOptions{
					To:   migration14[0],
					Port: echo.Port{Name: "http"},
				},
				Interval: 200 * time.Millisecond,
			}).Start()

			selfCheck16 := traffic.NewGenerator(t, traffic.Config{
				Source: stable16[0],
				Options: echo.CallOptions{
					To:   stable16[0],
					Port: echo.Port{Name: "http"},
				},
				Interval: 200 * time.Millisecond,
			}).Start()

			crossCheck16 := traffic.NewGenerator(t, traffic.Config{
				Source: stable16[0],
				Options: echo.CallOptions{
					To:   migration16[0],
					Port: echo.Port{Name: "http"},
				},
				Interval: 200 * time.Millisecond,
			}).Start()

			// Traffic here is from the go client to the migration namespace, via ingress
			ingressCheck14 := traffic.NewGenerator(t, traffic.Config{
				Source: ingress,
				Options: echo.CallOptions{
					Port: echo.Port{
						Protocol: protocol.HTTP,
					},
					HTTP: echo.HTTP{
						Path:    "/",
						Headers: headers.New().WithHost(fmt.Sprintf("%s.example.com", migration14Namespace.Name())).Build(),
					},
				},
			}).Start()
			ingressCheck16 := traffic.NewGenerator(t, traffic.Config{
				Source: ingress,
				Options: echo.CallOptions{
					Port: echo.Port{
						Protocol: protocol.HTTP,
					},
					HTTP: echo.HTTP{
						Path:    "/",
						Headers: headers.New().WithHost(fmt.Sprintf("%s.example.com", migration16Namespace.Name())).Build(),
					},
				},
				Interval: 200 * time.Millisecond,
			}).Start()

			crossCheck14to16 := traffic.NewGenerator(t, traffic.Config{
				Source: stable14[0],
				Options: echo.CallOptions{
					To:   migration16[0],
					Port: echo.Port{Name: "http"},
				},
				Interval: 200 * time.Millisecond,
			}).Start()

			crossCheck16to14 := traffic.NewGenerator(t, traffic.Config{
				Source: stable16[0],
				Options: echo.CallOptions{
					To:   migration14[0],
					Port: echo.Port{Name: "http"},
				},
				Interval: 200 * time.Millisecond,
			}).Start()

			// mcpChannelEnv is set from prow job configs to run against different channels
			channel, revision := regularChannel, regularRevision
			if c := os.Getenv(mcpChannelEnv); c == stableChannel {
				channel, revision = c, stableRevision
			}
			t.Logf("starting migration to MCP: %s", channel)

			deployAutomigration(t, channel, revision)

			// for 1.6 we use istio.io/rev label so istioctl x revision tag set default does not cover
			// nolint: staticcheck
			if err := migration16Namespace.SetLabel("istio.io/rev", revision); err != nil {
				t.Fatal(err)
			}
			tp := t.CreateTmpDirectoryOrFail("addon-migration-workload")
			kube.DumpPods(t, tp, "istio-system", []string{"app=istio-ingressgateway"})
			for _, i := range migration14 {
				if err := i.Restart(); err != nil {
					t.Fatal(err)
				}
			}
			for _, i := range migration16 {
				if err := i.Restart(); err != nil {
					t.Fatal(err)
				}
			}
			// A separate Prow test to be added for rollback because it contradicts with the clean up process
			testRollback := false
			if c := os.Getenv(testRollbackEnv); c == "true" {
				testRollback = true
				t.Logf("starting rolling back")
				command := exec.Command(filepath.Join(env.IstioSrc, "tools/packaging/knative/migrate-addon.sh"), "-y", "--command", "rollback-all")
				command.Stdout = loggingWriter{}
				command.Stderr = loggingWriter{}
				if err := command.Run(); err != nil {
					t.Fatal(err)
				}
				time.Sleep(time.Second * 60)
			}

			// Check mTLS connectivity works after workload A is signed by meshca
			checkConnectivity(t, stable14, migration14)
			checkConnectivity(t, stable16, migration16)
			checkConnectivity(t, stable16, migration14)
			checkConnectivity(t, stable14, migration16)

			// Check both our continuous traffic to ensure we have zero downtime
			t.NewSubTest("continuous-traffic-addon-14").Run(func(t framework.TestContext) {
				// Addon traffic should always succeed
				// Tolerate one random request timeout at the end that are not related to server error
				selfCheck14.Stop().CheckSuccessRate(t, 0.999)
			})
			t.NewSubTest("continuous-traffic-addon-16").Run(func(t framework.TestContext) {
				// Addon traffic should always succeed
				// Tolerate one random request timeout at the end that are not related to server error
				selfCheck16.Stop().CheckSuccessRate(t, 0.999)
			})
			t.NewSubTest("continuous-traffic-cross-14").Run(func(t framework.TestContext) {
				// We allow a small buffer here as the addon did not have graceful shutdown
				crossCheck14.Stop().CheckSuccessRate(t, 0.95)
			})
			t.NewSubTest("continuous-traffic-cross-16").Run(func(t framework.TestContext) {
				// We allow a small buffer here as the addon did not have graceful shutdown
				crossCheck16.Stop().CheckSuccessRate(t, 0.95)
			})
			t.NewSubTest("continuous-traffic-cross-14-16").Run(func(t framework.TestContext) {
				// We allow a small buffer here as the addon did not have graceful shutdown
				crossCheck14to16.Stop().CheckSuccessRate(t, 0.95)
			})
			t.NewSubTest("continuous-traffic-cross-16-14").Run(func(t framework.TestContext) {
				// We allow a small buffer here as the addon did not have graceful shutdown
				crossCheck16to14.Stop().CheckSuccessRate(t, 0.95)
			})
			t.NewSubTest("continuous-traffic-ingress-14").Run(func(t framework.TestContext) {
				// We allow a small buffer here as the addon did not have graceful shutdown
				ingressCheck14.Stop().CheckSuccessRate(t, 0.95)
			})
			t.NewSubTest("continuous-traffic-ingress-16").Run(func(t framework.TestContext) {
				// We allow a small buffer here as the addon did not have graceful shutdown
				ingressCheck16.Stop().CheckSuccessRate(t, 0.95)
			})

			kube.DumpPods(t, tp, migration16Namespace.Name(), []string{"app=migration"})
			kube.DumpPods(t, tp, stable16Namespace.Name(), []string{"app=migration"})
			kube.DumpPods(t, tp, migration14Namespace.Name(), []string{"app=migration"})
			kube.DumpPods(t, tp, stable14Namespace.Name(), []string{"app=migration"})

			// test cleanup process if it is not rolled back
			if !testRollback {
				cleanupCheck14 := traffic.NewGenerator(t, traffic.Config{
					Source: ingress,
					Options: echo.CallOptions{
						Port: echo.Port{
							Protocol: protocol.HTTP,
						},
						HTTP: echo.HTTP{
							Path:    "/",
							Headers: headers.New().WithHost(fmt.Sprintf("%s.example.com", migration14Namespace.Name())).Build(),
						},
					},
					Interval: 200 * time.Millisecond,
				}).Start()
				cleanupCheck16 := traffic.NewGenerator(t, traffic.Config{
					Source: ingress,
					Options: echo.CallOptions{
						Port: echo.Port{
							Protocol: protocol.HTTP,
						},
						HTTP: echo.HTTP{
							Path:    "/",
							Headers: headers.New().WithHost(fmt.Sprintf("%s.example.com", migration16Namespace.Name())).Build(),
						},
					},
					Interval: 200 * time.Millisecond,
				}).Start()

				// Check cleanup
				c := exec.Command(filepath.Join(env.IstioSrc, "tools/packaging/knative/migrate-addon.sh"), "-y", "--command", "cleanup")
				c.Stdout = loggingWriter{}
				c.Stderr = loggingWriter{}
				if err := c.Run(); err != nil {
					t.Fatal(err)
				}
				t.NewSubTest("continuous-traffic-ingress-cleanup-14").Run(func(t framework.TestContext) {
					// We allow a small buffer here as the addon did not have graceful shutdown
					cleanupCheck14.Stop().CheckSuccessRate(t, 0.95)
				})
				t.NewSubTest("continuous-traffic-ingress-cleanup-16").Run(func(t framework.TestContext) {
					// We allow a small buffer here as the addon did not have graceful shutdown
					cleanupCheck16.Stop().CheckSuccessRate(t, 0.95)
				})
			}
			// TODO: delete ca-secrets/amend meshConfig to remove trustanchors and ensure that traffic from the 1.4 workloads stops
		})
}
