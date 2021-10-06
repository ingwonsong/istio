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

// Package util Current setup is based on the httpbin is deployed by the asm-lib.sh.
// TODO: Install httpbin in Go code or use echo
package util

import (
	"fmt"
	"net"
	"time"

	"istio.io/istio/pkg/kube"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/istio"
	"istio.io/istio/pkg/test/shell"
	ingressutil "istio.io/istio/tests/integration/security/sds_ingress/util"
)

// SetupConfig Setup following items assuming httpbin is deployed to default namespace:
// 1. Create k8s secret using existing cert
func SetupConfig(ctx framework.TestContext) {
	// setup secrets
	ingressutil.CreateIngressKubeSecret(ctx, "userauth-tls-cert", ingressutil.TLS, ingressutil.IngressCredentialA, false)
}

// ValidatePortForward Verify the port-forward from localhost to cluster
func ValidatePortForward(ctx framework.TestContext, port string) {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("localhost", port), time.Second)
	if err != nil {
		ctx.Fatalf("port-forward is not available: %v", err)
	}
	if conn != nil {
		defer conn.Close()
		ctx.Logf("port-forward is available: %v", net.JoinHostPort("localhost", "8443"))
	}
}

// GetIngressPortForwarderOrFail Get a pod forwarder to ingress gateway's first pod with specified port
func GetIngressPortForwarderOrFail(ctx framework.TestContext, ist istio.Instance, localPort int, podPort int) kube.PortForwarder {
	cluster := ctx.Clusters().Default()
	ing := ist.IngressFor(cluster)
	ingressPod, err := ing.PodID(0)
	if err != nil {
		ctx.Fatalf("Could not get Ingress Pod ID: %v", err)
	}
	forwarder, err := cluster.NewPortForwarder(ingressPod, ing.Namespace(), "", localPort, podPort)
	if err != nil {
		ctx.Fatalf("failed creating new port forwarder for pod %s/%s: %v", ing.Namespace(), ingressPod, err)
	}
	return forwarder
}

// RestartDeploymentOrFail performs a `kubectl rollout restart` on deployments and waits for `kubectl rollout status` to complete.
func RestartDeploymentOrFail(ctx framework.TestContext, deployments []string, namespace string) {
	for _, deploymentName := range deployments {
		wlType := "deployment"
		rolloutCmd := fmt.Sprintf("kubectl rollout restart %s/%s -n %s",
			wlType, deploymentName, namespace)
		if _, err := shell.Execute(true, rolloutCmd); err != nil {
			ctx.Fatalf("failed to rollout restart %v/%v: %v", namespace, deploymentName, err)
		}
		waitCmd := fmt.Sprintf("kubectl rollout status %s/%s -n %s", wlType, deploymentName, namespace)
		if _, err := shell.Execute(true, waitCmd); err != nil {
			ctx.Fatalf("failed to wait rollout status for %v/%v: %v", namespace, deploymentName, err)
		}
	}
}
