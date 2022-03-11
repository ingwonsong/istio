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
	"time"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"

	"istio.io/istio/pkg/config/constants"
	"istio.io/istio/pkg/test/echo/check"
	"istio.io/istio/pkg/test/echo/common/scheme"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/resource"
	"istio.io/istio/pkg/test/kube"
	"istio.io/istio/pkg/test/util/retry"
	"istio.io/pkg/log"
)

const (
	migrationStateConfigMap = "asm-addon-migration-state"
	migrationSuccessState   = "SUCCESS"
	regularChannel          = "regular"
	stableChannel           = "stable"
	regularRevision         = "asm-managed"
	stableRevision          = "asm-managed-stable"
	mcpChannelEnv           = "TEST_MIGRATION_MCP_CHANNEL"
	testRollbackEnv         = "TEST_MIGRATION_ROLLBACK"
)

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

const gwVS = `apiVersion: networking.istio.io/v1alpha3
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
          number: 80
`

var cprGVR = schema.GroupVersionResource{
	Group:    "mesh.cloud.google.com",
	Version:  "v1beta1",
	Resource: "controlplanerevisions",
}

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
			Run(func(t framework.TestContext) {
				callOptions := echo.CallOptions{
					To:     dst,
					Port:   echo.Port{Name: "http"},
					Scheme: scheme.HTTP,
					Count:  1,
					Check:  check.OK(),
				}
				src.CallOrFail(t, callOptions)
			})
	}
}

func deployAutomigration(t framework.TestContext, channel, revision string) {
	// replace with deploy migration job
	cls, err := resource.SettingsFromCommandLine("addon")
	if err != nil {
		t.Fatalf("failed to get settings from CLI: %v", err)
	}
	cs := t.Clusters().Default()
	t.ConfigIstio().
		EvalFile(map[string]string{"HUB": cls.Image.Hub, "TAG": cls.Image.Tag, "MCP_CHANNEL": channel}, "testdata/migration_deployment.yaml").
		ApplyOrFail(t, "istio-system", resource.NoCleanup)
	defer dumpMigrationJobPod(t)
	_, err = kube.WaitUntilPodsAreReady(kube.NewSinglePodFetch(cs, "istio-system", "istio=addon-migration"))
	if err != nil {
		t.Fatalf("migration pod of cluster: %s not ready: %v", cs.Name(), err)
	}

	// check the configMap for success status to make sure auto migration is done
	verifyMigrationStateCM(t, cs, migrationSuccessState, revision)
}

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

// verifyMigrationStateCM verifies the migration state configMap
func verifyMigrationStateCM(t framework.TestContext, a kubernetes.Interface, expectedStatus, cprName string) {
	cs := t.Clusters().Default()
	err := retry.UntilSuccess(func() error {
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
	if err != nil {
		cpr, cerr := cs.Dynamic().Resource(cprGVR).Get(context.TODO(), cprName, v1.GetOptions{})
		if cerr != nil {
			t.Errorf("failed to dump ControlPlaneRevision %v", cerr)
		}
		t.Fatalf("retry.UntilSuccess failed: %v\n, ControlPlaneRevision: %v", err, cpr)
	}
}

func dumpMigrationJobPod(t framework.TestContext) {
	kube.DumpPods(t, t.CreateTmpDirectoryOrFail("addon-migration"), constants.IstioSystemNamespace, []string{"k8s-app=addon-migration"})
}
