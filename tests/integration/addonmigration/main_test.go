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
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"istio.io/istio/pkg/config/constants"
	"istio.io/istio/pkg/config/protocol"
	"istio.io/istio/pkg/test/echo/common/scheme"
	"istio.io/istio/pkg/test/env"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/echo/echoboot"
	"istio.io/istio/pkg/test/framework/components/echo/util/traffic"
	"istio.io/istio/pkg/test/framework/components/istio"
	"istio.io/istio/pkg/test/framework/components/namespace"
	"istio.io/istio/pkg/test/framework/image"
	"istio.io/istio/pkg/test/framework/label"
	"istio.io/istio/pkg/test/kube"
	"istio.io/istio/pkg/test/util/retry"
	"istio.io/istio/pkg/test/util/tmpl"
	"istio.io/istio/tests/integration/security/util"
	"istio.io/istio/tests/integration/security/util/connection"
	"istio.io/pkg/log"
)

const (
	migrationStateConfigMap = "asm-addon-migration-state"
	migrationSuccessState   = "SUCCESS"
)

var inst istio.Instance

func checkConnectivity(t framework.TestContext, a echo.Instances, b echo.Instances) {
	t.Helper()
	srcList := []echo.Instance{a[0], b[0]}
	dstList := []echo.Instance{b[0], a[0]}
	for index := range srcList {
		src := srcList[index]
		dst := dstList[index]
		t.NewSubTest(fmt.Sprintf("post-migration/%s.%s->%s.%s",
			src.Config().Service, src.Config().Namespace.Prefix(),
			dst.Config().Service, dst.Config().Namespace.Prefix())).
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
			// This test will have two namespaces per addon version. The will both start out using the addon. We will
			// continuously send traffic:
			// * Within the "stable-x" namespace, to ensure the migration doesn't impact existing pods
			// * Between the "stable-x" namespace and the "migration-x" namespace, to ensure old pods can connect to new pods
			// * From ingress to the "migration-x" namespace, to ensure ingress is zero downtime
			// * From "stable-14" -> "migration-16" and "stable-16" -> "migration-14" to ensure the cross version communication works
			// Because the pods restart, we do not send continuous traffic from migration -> addon, but we do send traffic once the migration is complete.
			ingress := inst.IngressFor(t.Clusters().Default())
			stable14Namespace := namespace.NewOrFail(t, t, namespace.Config{
				Prefix:   "stable-14",
				Inject:   true,
				Revision: "default",
			})
			t.ConfigKube().ApplyYAMLOrFail(t, stable14Namespace.Name(), fmt.Sprintf(mtlsDr, stable14Namespace.Name()))
			stable16Namespace := namespace.NewOrFail(t, t, namespace.Config{
				Prefix:   "stable-16",
				Inject:   true,
				Revision: "istio-1611",
			})
			t.ConfigKube().ApplyYAMLOrFail(t, stable16Namespace.Name(), fmt.Sprintf(mtlsDr, stable16Namespace.Name()))

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
				t.ConfigKube().ApplyYAMLOrFail(t, ns.Name(), fmt.Sprintf(mtlsDr, ns.Name()))
				t.ConfigKube().ApplyYAMLOrFail(t, ns.Name(), fmt.Sprintf(`apiVersion: networking.istio.io/v1alpha3
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
    - "%s.example.com"
---
apiVersion: networking.istio.io/v1alpha3
kind: VirtualService
metadata:
  name: app
spec:
  hosts:
  - "%s.example.com"
  gateways:
  - app
  http:
  - route:
    - destination:
        host: migration
        port:
          number: 80`, ns.Name(), ns.Name()))
			}

			builder := echoboot.NewBuilder(t)

			// Create workloads in namespaces served by both CA's
			echos := builder.
				WithClusters(t.Clusters()...).
				WithConfig(util.EchoConfig("addon", stable14Namespace, false, nil)).
				WithConfig(util.EchoConfig("addon", stable16Namespace, false, nil)).
				WithConfig(util.EchoConfig("migration", migration14Namespace, false, nil)).
				WithConfig(util.EchoConfig("migration", migration16Namespace, false, nil)).
				BuildOrFail(t)

			stable14 := echos.Match(echo.Service("addon").And(echo.Namespace(stable14Namespace.Name())))
			migration14 := echos.Match(echo.Service("migration").And(echo.Namespace(migration14Namespace.Name())))
			stable16 := echos.Match(echo.Service("addon").And(echo.Namespace(stable16Namespace.Name())))
			migration16 := echos.Match(echo.Service("migration").And(echo.Namespace(migration16Namespace.Name())))

			t.Log("starting traffic...")
			selfCheck14 := traffic.NewGenerator(t, traffic.Config{
				Source: stable14[0],
				Options: echo.CallOptions{
					Target:   stable14[0],
					PortName: "http",
				},
				Interval: 200 * time.Millisecond,
			}).Start()

			crossCheck14 := traffic.NewGenerator(t, traffic.Config{
				Source: stable14[0],
				Options: echo.CallOptions{
					Target:   migration14[0],
					PortName: "http",
				},
				Interval: 200 * time.Millisecond,
			}).Start()

			selfCheck16 := traffic.NewGenerator(t, traffic.Config{
				Source: stable16[0],
				Options: echo.CallOptions{
					Target:   stable16[0],
					PortName: "http",
				},
				Interval: 200 * time.Millisecond,
			}).Start()

			crossCheck16 := traffic.NewGenerator(t, traffic.Config{
				Source: stable16[0],
				Options: echo.CallOptions{
					Target:   migration16[0],
					PortName: "http",
				},
				Interval: 200 * time.Millisecond,
			}).Start()

			// Traffic here is from the go client to the migration namespace, via ingress
			ingressCheck14 := traffic.NewGenerator(t, traffic.Config{
				Source: ingress,
				Options: echo.CallOptions{
					Port: &echo.Port{
						Protocol: protocol.HTTP,
					},
					Path: "/",
					Headers: map[string][]string{
						"Host": {fmt.Sprintf("%s.example.com", migration14Namespace.Name())},
					},
				},
			}).Start()
			ingressCheck16 := traffic.NewGenerator(t, traffic.Config{
				Source: ingress,
				Options: echo.CallOptions{
					Port: &echo.Port{
						Protocol: protocol.HTTP,
					},
					Path: "/",
					Headers: map[string][]string{
						"Host": {fmt.Sprintf("%s.example.com", migration16Namespace.Name())},
					},
				},
				Interval: 200 * time.Millisecond,
			}).Start()

			crossCheck14to16 := traffic.NewGenerator(t, traffic.Config{
				Source: stable14[0],
				Options: echo.CallOptions{
					Target:   migration16[0],
					PortName: "http",
				},
				Interval: 200 * time.Millisecond,
			}).Start()

			crossCheck16to14 := traffic.NewGenerator(t, traffic.Config{
				Source: stable16[0],
				Options: echo.CallOptions{
					Target:   migration14[0],
					PortName: "http",
				},
				Interval: 200 * time.Millisecond,
			}).Start()

			t.Logf("starting migration")
			// replace with deploy migration job
			bt, err := ioutil.ReadFile("testdata/migration_deployment.yaml")
			if err != nil {
				t.Fatalf("failed to read migration job yaml: %v", err)
			}
			cls, err := image.SettingsFromCommandLine()
			if err != nil {
				t.Fatalf("failed to get settings from CLI: %v", err)
			}
			md := tmpl.EvaluateOrFail(t, string(bt), map[string]string{"HUB": cls.Hub, "TAG": cls.Tag})
			if err := t.ConfigIstio().ApplyYAMLNoCleanup("istio-system", md); err != nil {
				t.Fatalf("failed to apply migration job manifest: %v", err)
			}
			defer dumpMigrationJobPod(t)
			cs := t.Clusters().Default()
			_, err = kube.WaitUntilPodsAreReady(kube.NewSinglePodFetch(cs, "istio-system", "istio=addon-migration"))
			if err != nil {
				t.Fatalf("migration pod not ready: %v", err)
			}

			// check the configMap for success status to make sure auto migration is done
			verifyMigrationStateCM(t, t.Clusters().Default(), migrationSuccessState)

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

			// Check mTLS connectivity works after workload A is signed by meshca
			checkConnectivity(t, stable14, migration14)
			checkConnectivity(t, stable16, migration16)
			checkConnectivity(t, stable16, migration14)
			checkConnectivity(t, stable14, migration16)

			// Check both our continuous traffic to ensure we have zero downtime
			t.NewSubTest("continuous-traffic-addon-14").Run(func(t framework.TestContext) {
				// Addon traffic should always succeed
				selfCheck14.Stop().CheckSuccessRate(t, 1)
			})
			t.NewSubTest("continuous-traffic-addon-16").Run(func(t framework.TestContext) {
				// Addon traffic should always succeed
				selfCheck16.Stop().CheckSuccessRate(t, 1)
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

			t.Logf("starting cleanup")
			cleanupCheck14 := traffic.NewGenerator(t, traffic.Config{
				Source: ingress,
				Options: echo.CallOptions{
					Port: &echo.Port{
						Protocol: protocol.HTTP,
					},
					Path: "/",
					Headers: map[string][]string{
						"Host": {fmt.Sprintf("%s.example.com", migration14Namespace.Name())},
					},
				},
				Interval: 200 * time.Millisecond,
			}).Start()
			cleanupCheck16 := traffic.NewGenerator(t, traffic.Config{
				Source: ingress,
				Options: echo.CallOptions{
					Port: &echo.Port{
						Protocol: protocol.HTTP,
					},
					Path: "/",
					Headers: map[string][]string{
						"Host": {fmt.Sprintf("%s.example.com", migration16Namespace.Name())},
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
			// TODO: delete ca-secrets/amend meshConfig to remove trustanchors and ensure that traffic from the 1.4 workloads stops
		})
}

// verifyMigrationStateCM verifies the migration state configMap
// TODO(iamwen): add check for migratedNamespace fields of the CM, currently only checks for migrationStatus
func verifyMigrationStateCM(t framework.TestContext, a kubernetes.Interface, expectedStatus string) {
	retry.UntilSuccessOrFail(t, func() error {
		t.Log("Checking migration status configMap...")
		fetched, err := a.CoreV1().ConfigMaps("istio-system").Get(context.TODO(), migrationStateConfigMap, v1.GetOptions{})
		if err != nil {
			return err
		}
		data := fetched.Data
		if status, ok := data["migrationStatus"]; ok && status == expectedStatus {
			t.Log("verified migration state configMap")
			return nil
		}
		msg := fmt.Sprintf("got unexpected migrationStatus: %v", data["migrationStatus"])
		t.Log(msg)
		return fmt.Errorf(msg)
	}, retry.Timeout(time.Minute*12), retry.BackoffDelay(time.Second*1))
}

func dumpMigrationJobPod(t framework.TestContext) {
	kube.DumpPods(t, t.CreateTmpDirectoryOrFail("addon-migration"), constants.IstioSystemNamespace, []string{"k8s-app=addon-migration"})
}

func TestMain(t *testing.M) {
	// Integration test for testing migration of workloads from Istio on GKE (with citadel) to
	// Google Mesh CA based managed control plane
	framework.NewSuite(t).
		Label(label.CustomSetup).
		Setup(istio.Setup(&inst, nil)).
		Run()
}
