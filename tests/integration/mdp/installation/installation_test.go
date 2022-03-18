//go:build integ
// +build integ

//  Copyright Istio Authors
//
//  Licensed under the Apache License, Version 2.0 (the "License");
//  you may not use this file except in compliance with the License.
//  You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
//  Unless required by applicable law or agreed to in writing, software
//  distributed under the License is distributed on an "AS IS" BASIS,
//  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//  See the License for the specific language governing permissions and
//  limitations under the License.

package installation_test

import (
	"context"
	"strings"
	"testing"

	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/echo/common/ports"
	"istio.io/istio/pkg/test/framework/components/echo/deployment"
	"istio.io/istio/pkg/test/framework/components/namespace"
	"istio.io/istio/pkg/test/kube"
)

func TestInstallSuccess(t *testing.T) {
	framework.NewTest(t).
		Features("mdp.install").
		Run(func(tc framework.TestContext) {
			cs := tc.Clusters().Default()

			ns := namespace.NewOrFail(t, tc, namespace.Config{
				Prefix: "mdp-workload",
				Inject: true,
			})
			if err := ns.SetAnnotation("mesh.cloud.google.com/proxy", `{"managed":"true"}`); err != nil {
				t.Fatalf("could not set managed annotation: %v", err)
			}

			cniPods, err := kube.WaitUntilPodsAreReady(kube.NewSinglePodFetch(cs, "kube-system", "k8s-app=istio-cni-node"))
			if err != nil {
				t.Fatalf("no cni pods became ready: %v", err)
			}

			for _, pod := range cniPods {
				logs, err := cs.PodLogs(context.TODO(), pod.Name, pod.Namespace, "mdp-controller", false)
				if err != nil {
					t.Fatalf("could not find logs for mdp-controller: %v", err)
				}
				if strings.Contains(logs, "Starting the server") { // TODO (dougreid): is there a better indication of the health?
					continue
				}
				t.Fatal("MDP Controller failed to start")
			}

			builder := deployment.New(tc, cs)
			builder = builder.WithConfig(echo.Config{
				Namespace: ns,
				Service:   "example-workload",
				Ports:     ports.All(),
				Subsets:   []echo.SubsetConfig{{}},
			})
			instances := builder.BuildOrFail(t)
			for _, instance := range instances {
				workloads := instance.WorkloadsOrFail(t)
				for _, workload := range workloads {
					workload.Sidecar().InfoOrFail(t)
				}
			}
		})
}

func TestMain(m *testing.M) {
	framework.NewSuite(m).Run()
}
