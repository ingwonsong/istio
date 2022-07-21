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
	"istio.io/istio/pkg/test/framework/components/echo/check"
	"istio.io/istio/pkg/test/framework/components/echo/deployment"
	"istio.io/istio/pkg/test/framework/components/echo/match"
	"istio.io/istio/pkg/test/framework/components/istio"
	"istio.io/istio/pkg/test/framework/components/namespace"
	"istio.io/istio/pkg/test/framework/label"
	"istio.io/istio/pkg/test/framework/resource"
	"istio.io/istio/tests/integration/security/util"
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

func checkConnectivity(t *testing.T, ctx framework.TestContext, a echo.Instances, b echo.Instances, testPrefix string) {
	t.Helper()
	ctx.NewSubTest(testPrefix).Run(func(t framework.TestContext) {
		srcList := []echo.Instance{a[0], b[0]}
		dstList := []echo.Instance{b[0], a[0]}
		for index := range srcList {
			src := srcList[index]
			dst := dstList[index]
			callOptions := echo.CallOptions{
				To:     dst,
				Port:   echo.Port{Name: "http"},
				Scheme: scheme.HTTP,
				Count:  1,
			}
			callOptions.Check = check.OK()
			src.CallOrFail(t, callOptions)
		}
	})
}

// TestIstiodToMeshCAMigration: test zero downtime migration from Istiod CA to Google Mesh CA
func TestIstiodToMeshCAMigration(t *testing.T) {
	// nolint: staticcheck
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

			builder := deployment.New(ctx)

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
			a := match.And(match.ServiceName(echo.NamespacedName{Name: ASvc, Namespace: nsA}), match.Cluster(cluster)).GetMatches(echos)
			b := match.And(match.ServiceName(echo.NamespacedName{Name: BSvc, Namespace: nsB}), match.Cluster(cluster)).GetMatches(echos)
			c := match.And(match.ServiceName(echo.NamespacedName{Name: CSvc, Namespace: nsC}), match.Cluster(cluster)).GetMatches(echos)
			d := match.And(match.ServiceName(echo.NamespacedName{Name: DSvc, Namespace: nsD}), match.Cluster(cluster)).GetMatches(echos)

			// 1. Setup Test
			checkConnectivity(t, ctx, b, c, "init-test-cross-ca-mtls")

			// 2: Migration test

			err = nsB.SetLabel("istio.io/rev", MeshCARevision)
			if err != nil {
				t.Fatalf("unable to annotate namespace %v with label %v: %v",
					nsB.Name(), MeshCARevision, err)
			}

			if err := b[0].Restart(); err != nil {
				t.Fatalf("revisioned instance rollout failed with: %v", err)
			}
			checkConnectivity(t, ctx, b, c, "post-migrate-test-same-ca-mtls")
			checkConnectivity(t, ctx, a, b, "post-migrate-test-cross-ca-mtls")

			// 3: Rollback Test

			err = nsC.SetLabel("istio.io/rev", IstioCARevision)
			if err != nil {
				t.Fatalf("unable to annotate namespace %v with label %v: %v",
					nsC.Name(), IstioCARevision, err)
			}
			if err := c[0].Restart(); err != nil {
				t.Fatalf("revisioned instance rollout failed with: %v", err)
			}
			checkConnectivity(t, ctx, a, c, "post-rollback-test-same-ca-mtls")

			// 4: Test removal of trust anchor: Same ca connectvity will work, cross ca connectivity will not

			systemNs, err := istio.ClaimSystemNamespace(ctx)
			if err != nil {
				t.Fatalf("unable to retrieve istio-system namespace: %v", err)
			}
			// Remove trust roots of Istio CA
			err = cluster.Kube().CoreV1().Secrets(systemNs.Name()).Delete(context.TODO(), "istio-ca-secret", metav1.DeleteOptions{})
			if err != nil && !errors.IsNotFound(err) {
				t.Fatalf("unable to delete secret %v from the cluster. Encountered error: %v", "istio-ca-secret", err)
			}
			err = cluster.Kube().CoreV1().Secrets(systemNs.Name()).Delete(context.TODO(), "cacerts", metav1.DeleteOptions{})
			if err != nil && !errors.IsNotFound(err) {
				t.Fatalf("unable to delete secret %v from the cluster. Encountered error: %v", "cacerts", err)
			}

			// Restart MeshCa Control plane to handle removal of trust roots
			deploymentsClient := cluster.Kube().AppsV1().Deployments(systemNs.Name())
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
			checkConnectivity(t, ctx, b, d, "post-trust-test-same-ca-conn")
			// TODO(shankgan): removing check temporarily. takes too much time. Need to find
			// a better way to confirm trust removal.
			// checkConnectivity(t, ctx, a, b, "post-trust-test-cross-ca-conn")
		})
}

func TestMain(t *testing.M) {
	// Integration test for testing migration of workloads from Istiod Ca based control plane to
	// Google Mesh Ca based control plane
	// This tests Canary CA migration, where workloads are migrated between control planes
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
