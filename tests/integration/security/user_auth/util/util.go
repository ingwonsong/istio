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
	"istio.io/istio/pkg/test/util/tmpl"
	ingressutil "istio.io/istio/tests/integration/security/sds_ingress/util"
)

const OriginalUserAuthConfig = `
apiVersion: security.anthos.io/v1beta1
kind: UserAuthConfig
metadata:
  name: user-auth-config
  namespace: asm-user-auth
spec:
  authentication:
    oidc:
      certificateAuthorityData: ""
      issuerURI: "https://accounts.google.com"
      proxy: ""
      oauthCredentialsSecret:
        name: "oauth-secret"
        namespace: "asm-user-auth"
      redirectURIHost: ""
      redirectURIPath: "/_gcp_anthos_callback"
      scopes: ""
      groupsClaim: ""
  outputJWTAudience: "test_audience"
`

const NoAudUserAuthConfig = `
apiVersion: security.anthos.io/v1beta1
kind: UserAuthConfig
metadata:
  name: user-auth-config
  namespace: asm-user-auth
spec:
  authentication:
    oidc:
      certificateAuthorityData: ""
      issuerURI: "https://accounts.google.com"
      proxy: ""
      oauthCredentialsSecret:
        name: "oauth-secret"
        namespace: "asm-user-auth"
      redirectURIHost: ""
      redirectURIPath: "/_gcp_anthos_callback"
      scopes: ""
      groupsClaim: ""
`
const GoogleIDPRootCA = "LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSURkVENDQWwyZ0F3SUJBZ0lMQkFBQUFBQUJGVXRhdzVRd0RRWUpLb1pJaHZjTkFRRUZCUUF3VnpFTE1Ba0cKQTFVRUJoTUNRa1V4R1RBWEJnTlZCQW9URUVkc2IySmhiRk5wWjI0Z2JuWXRjMkV4RURBT0JnTlZCQXNUQjFKdgpiM1FnUTBFeEd6QVpCZ05WQkFNVEVrZHNiMkpoYkZOcFoyNGdVbTl2ZENCRFFUQWVGdzA1T0RBNU1ERXhNakF3Ck1EQmFGdzB5T0RBeE1qZ3hNakF3TURCYU1GY3hDekFKQmdOVkJBWVRBa0pGTVJrd0Z3WURWUVFLRXhCSGJHOWkKWVd4VGFXZHVJRzUyTFhOaE1SQXdEZ1lEVlFRTEV3ZFNiMjkwSUVOQk1Sc3dHUVlEVlFRREV4SkhiRzlpWVd4VAphV2R1SUZKdmIzUWdRMEV3Z2dFaU1BMEdDU3FHU0liM0RRRUJBUVVBQTRJQkR3QXdnZ0VLQW9JQkFRRGFEdWFaCmpjNmo0MCtLZnZ2eGk0TWxhK3BJSC9FcXNMbVZFUVM5OEdQUjRtZG16eHpkenh0SUsrNk5pWTZhcnltQVphdnAKeHkwU3k2c2NUSEFIb1QwS01NMFZqVS80M2RTTVVCVWM3MUR1eEM3My9PbFM4cEY5NEczVk5UQ09Ya056OGtIcAoxV3Jqc29rNlZqazRid1k4aUdsYktrM0ZwMVM0YkluTW0vazh5dVg5aWZVU1BKSjRsdGJjZEc2VFJHSFJqY2RHCnNuVU9odWdaaXRWdGJOVjRGcFdpNmNnS09PdnlKQk5QYzFTVEU0VTZHN3dlTkxXTEJZeTVkNHV4Mng4Z2thc0oKVTI2UXpuczNkTGx3UjVFaVVXTVdlYTZ4cmtFbUNNZ1pLOUZHcWtqV1pDclhnelQvTENyQmJCbERTZ2VGNTlOOAo5aUZvNytyeVVwOS9rNURQQWdNQkFBR2pRakJBTUE0R0ExVWREd0VCL3dRRUF3SUJCakFQQmdOVkhSTUJBZjhFCkJUQURBUUgvTUIwR0ExVWREZ1FXQkJSZ2UyWWFSUTJYeW9sUUwzMEV6VFNvLy96OVN6QU5CZ2txaGtpRzl3MEIKQVFVRkFBT0NBUUVBMW5QbmZFOTIwSTIvN0xxaXZqVEZLREsxZlB4c25Dd3J2UW1lVTc5clhxb1JTTGJsQ0tPegp5ajFoVGROR0NiTSt3NkRqWTFVYjhycnZyVG5oUTdrNG8rWXZpaVk3NzZCUVZ2bkdDdjA0emNRTGNGR1VsNWdFCjM4TmZsTlVWeVJSQm5NUmRkV1FWRGY5Vk1PeUdqLzhON3l5NVkwYjJxdnpmdkduOUxoSklaSnJnbGZDbTd5bVAKQWJFVnRRd2RwZjVwTEdra2VCNnpweHh4WXU3S3lKZXNGMTJLd3ZoSGhtNHF4Rll4bGRCbmlZVXIrV3ltWFVhZApES3FDNUpsUjNYQzMyMVk5WWVScTRWelc5djQ5M2tITUI2NWpVcjlUVS9RcjZjZjl0dmVDWDRYU1FSamJnYk1FCkhNVWZwSUJ2RlNESjNneUlDaDNXWmxYaS9FakpLU1pwNEE9PQotLS0tLUVORCBDRVJUSUZJQ0FURS0tLS0t" // nolint: lll

type UserAuthConfigFields struct {
	CA           string
	IssuerURI    string
	Proxy        string
	RedirectHost string
	RedirectPath string
	Scopes       string
	GroupsClaim  string
	Aud          string
}

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

func NewUserAuthConfig(userAuthConfigFields UserAuthConfigFields) string {
	tpl := `
apiVersion: security.anthos.io/v1beta1
kind: UserAuthConfig
metadata:
  name: user-auth-config
  namespace: asm-user-auth
spec:
  authentication:
    oidc:
      certificateAuthorityData: {{ .CA }}
      issuerURI: {{ .IssuerURI }}
      proxy: {{ .Proxy }}
      oauthCredentialsSecret:
        name: "oauth-secret"
        namespace: "asm-user-auth"
      redirectURIHost: {{ .RedirectHost }}
      redirectURIPath: {{ .RedirectPath }}
      scopes: {{ .Scopes }}
      groupsClaim: {{ .GroupsClaim }}
  outputJWTAudience: {{ .Aud }}
`
	return tmpl.MustEvaluate(tpl, userAuthConfigFields)
}
