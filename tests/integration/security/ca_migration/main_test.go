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

package migrationca

import (
	"context"
	"fmt"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"istio.io/istio/pkg/test/echo/common/scheme"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/echo/echoboot"
	"istio.io/istio/pkg/test/framework/components/istio"
	"istio.io/istio/pkg/test/framework/components/namespace"
	"istio.io/istio/pkg/test/framework/label"
	"istio.io/istio/pkg/test/framework/resource"
	"istio.io/istio/tests/integration/security/util"
	"istio.io/istio/tests/integration/security/util/connection"
)

var inst istio.Instance

const (
	ASvc            = "a"
	BSvc            = "b"
	CSvc            = "c"
	DSvc            = "d"
	IstioCARevision = "asm-revision-istiodca"
	MeshCARevision  = "asm-revision-meshca"
)

func checkConnectivity(t *testing.T, ctx framework.TestContext, a echo.Instances, b echo.Instances,
	expectSuccess bool, testPrefix string) {
	t.Helper()
	ctx.NewSubTest(testPrefix).Run(func(ctx framework.TestContext) {
		srcList := []echo.Instance{a[0], b[0]}
		dstList := []echo.Instance{b[0], a[0]}
		for index := range srcList {
			src := srcList[index]
			dst := dstList[index]
			callOptions := echo.CallOptions{
				Target:   dst,
				PortName: "http",
				Scheme:   scheme.HTTP,
				Count:    1,
			}
			checker := connection.Checker{
				From:          src,
				Options:       callOptions,
				ExpectSuccess: expectSuccess,
				DestClusters:  b.Clusters(),
			}
			checker.CheckOrFail(ctx)
		}
	})
}

// TestIstiodToMeshCAMigration: test zero downtime migration from Istiod CA to Google Mesh CA
func TestIstiodToMeshCAMigration(t *testing.T) {
	framework.NewTest(t).
		RequiresSingleCluster().
		Features("security.migrationca.citadel-meshca").
		Run(func(ctx framework.TestContext) {
			nsA := namespace.NewOrFail(t, ctx, namespace.Config{
				Prefix:   "nsa",
				Inject:   true,
				Revision: IstioCARevision,
			})

			nsB := namespace.NewOrFail(t, ctx, namespace.Config{
				Prefix:   "nsb",
				Inject:   true,
				Revision: IstioCARevision,
			})

			nsC := namespace.NewOrFail(t, ctx, namespace.Config{
				Prefix:   "nsc",
				Inject:   true,
				Revision: MeshCARevision,
			})

			nsD := namespace.NewOrFail(t, ctx, namespace.Config{
				Prefix:   "nsd",
				Inject:   true,
				Revision: MeshCARevision,
			})

			builder := echoboot.NewBuilder(ctx)

			builder.
				WithClusters(ctx.Clusters()...).
				WithConfig(util.EchoConfig(ASvc, nsA, false, nil)).
				WithConfig(util.EchoConfig(BSvc, nsB, false, nil)).
				WithConfig(util.EchoConfig(CSvc, nsC, false, nil)).
				WithConfig(util.EchoConfig(DSvc, nsD, false, nil))

			echos, err := builder.Build()
			if err != nil {
				t.Fatalf("failed to bring up apps for ca_migration: %v", err)
				return
			}
			cluster := ctx.Clusters().Default()
			a := echos.Match(echo.Service(ASvc)).Match(echo.InCluster(cluster))
			b := echos.Match(echo.Service(BSvc)).Match(echo.InCluster(cluster))
			c := echos.Match(echo.Service(CSvc)).Match(echo.InCluster(cluster))
			d := echos.Match(echo.Service(DSvc)).Match(echo.InCluster(cluster))

			// 1. Setup Test
			checkConnectivity(t, ctx, b, c, true, "init-test-cross-ca-mtls")

			// 2: Migration test

			err = nsB.SetLabel("istio.io/rev", MeshCARevision)
			if err != nil {
				t.Fatalf("unable to annotate namespace %v with label %v: %v",
					nsB.Name(), MeshCARevision, err)
			}

			if err := b[0].Restart(); err != nil {
				t.Fatalf("revisioned instance rollout failed with: %v", err)
			}
			checkConnectivity(t, ctx, b, c, true, "post-migrate-test-same-ca-mtls")
			checkConnectivity(t, ctx, a, b, true, "post-migrate-test-cross-ca-mtls")

			// 3: Rollback Test

			err = nsC.SetLabel("istio.io/rev", IstioCARevision)
			if err != nil {
				t.Fatalf("unable to annotate namespace %v with label %v: %v",
					nsC.Name(), IstioCARevision, err)
			}
			if err := c[0].Restart(); err != nil {
				t.Fatalf("revisioned instance rollout failed with: %v", err)
			}
			checkConnectivity(t, ctx, a, c, true, "post-rollback-test-same-ca-mtls")

			// 4: Test removal of trust anchor: Same ca connectvity will work, cross ca connectivity will not

			systemNs, err := istio.ClaimSystemNamespace(ctx)
			if err != nil {
				t.Fatalf("unable to retrieve istio-system namespace: %v", err)
			}
			// Remove trust roots of Istio CA
			err = cluster.CoreV1().Secrets(systemNs.Name()).Delete(context.TODO(), "istio-ca-secret", metav1.DeleteOptions{})
			if err != nil && !errors.IsNotFound(err) {
				t.Fatalf("unable to delete secret %v from the cluster. Encountered error: %v", "istio-ca-secret", err)
			}
			err = cluster.CoreV1().Secrets(systemNs.Name()).Delete(context.TODO(), "cacerts", metav1.DeleteOptions{})
			if err != nil && !errors.IsNotFound(err) {
				t.Fatalf("unable to delete secret %v from the cluster. Encountered error: %v", "cacerts", err)
			}

			// Restart MeshCa Control plane to handle removal of trust roots
			deploymentsClient := cluster.AppsV1().Deployments(systemNs.Name())
			meshcaDeployment, err := deploymentsClient.Get(context.TODO(), fmt.Sprintf("istiod-%s", MeshCARevision), metav1.GetOptions{})
			if err != nil {
				t.Fatalf("unable to retrieve meshca control plane deployment: %v", err)
			}
			meshcaDeployment.Spec.Template.ObjectMeta.Annotations["restart"] = "true"
			_, err = deploymentsClient.Update(context.TODO(), meshcaDeployment, metav1.UpdateOptions{})
			if err != nil {
				t.Fatalf("unable to update meshca control plane deployment: %v", err)
			}
			// TODO: need better way to rollout and restart meshca deployment and wait until pods are active
			time.Sleep(40 * time.Second)
			checkConnectivity(t, ctx, b, d, true, "post-trust-test-same-ca-conn")
			checkConnectivity(t, ctx, a, b, false, "post-trust-test-cross-ca-conn")
		})
}

func TestMain(t *testing.M) {
	// Integration test for testing migration of workloads from Istiod Ca based control plane to
	// Google Mesh Ca based control plane
	framework.NewSuite(t).
		Label(label.CustomSetup).
		Setup(istio.Setup(&inst, setupConfig)).
		Run()
}

func setupConfig(_ resource.Context, cfg *istio.Config) {
	if cfg == nil {
		return
	}
}
