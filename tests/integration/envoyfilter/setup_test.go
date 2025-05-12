//go:build integ
// +build integ

// Copyright Istio Authors. All Rights Reserved.
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

package envoyfiltertest

import (
	"os"
	"testing"
	"time"

	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/authz"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/echo/common/deployment"
	"istio.io/istio/pkg/test/framework/components/istio"
	"istio.io/istio/pkg/test/framework/components/namespace"
	"istio.io/istio/pkg/test/framework/label"
	"istio.io/istio/pkg/test/framework/resource/config/apply"
	"istio.io/istio/pkg/test/util/tmpl"
)

var (
	ist istio.Instance

	// Namespaces
	echoClientNS namespace.Instance
	echoServerNS namespace.Instance
	extAuthzNS   namespace.Instance
	rateLimitNS  namespace.Instance

	// Deployments
	apps        deployment.TwoNamespaceView
	authzServer authz.Server
)

const (
	echoClientNsPrefix string = "echo-client"
	echoServerNsPrefix string = "echo-server"
	extAuthzNSPrefix   string = "ext-authz"
	rateLimitNSPrefix  string = "ratelimit"

	echoCallTimeout          time.Duration = 5 * time.Second
	echoCallRetryTimeout     time.Duration = 5 * time.Minute
	echoCallRetryMaxAttempts int           = 10
	echoCallRetryDelay       time.Duration = 10 * time.Second

	labelAppA string = "app: a"
)

func TestMain(m *testing.M) {
	framework.NewSuite(m).
		Label(label.CustomSetup).
		Setup(istio.Setup(&ist, nil)).
		SetupParallel(
			namespace.Setup(&echoClientNS, namespace.Config{Prefix: echoClientNsPrefix, Inject: true}),
			namespace.Setup(&echoServerNS, namespace.Config{Prefix: echoServerNsPrefix, Inject: true}),
			namespace.Setup(&extAuthzNS, namespace.Config{Prefix: extAuthzNSPrefix, Inject: true}),
			namespace.Setup(&rateLimitNS, namespace.Config{Prefix: rateLimitNSPrefix, Inject: true}),
		).SetupParallel(
		deployment.SetupTwoNamespaces(&apps, deployment.Config{
			Namespaces: []namespace.Getter{
				namespace.Future(&echoClientNS),
				namespace.Future(&echoServerNS),
			},
		}),
		authz.Setup(&authzServer, namespace.Future(&extAuthzNS)),
	).Run()
}

func clientAndServerEchoNs(t framework.TestContext) (deployment.EchoNamespace, deployment.EchoNamespace) {
	t.Helper()
	var clientEchoNs, serverEchoNs deployment.EchoNamespace
	if apps.Ns1.Namespace.Prefix() == echoClientNsPrefix {
		clientEchoNs = apps.Ns1
		serverEchoNs = apps.Ns2
	} else {
		clientEchoNs = apps.Ns2
		serverEchoNs = apps.Ns1
	}
	return clientEchoNs, serverEchoNs
}

func applyEnvoyFilter(t framework.TestContext, envoyfilterName string, tmplFilePath string, serverNs deployment.EchoNamespace, params map[string]any) {
	t.Helper()
	envoyFilterTmpl, err := os.ReadFile(tmplFilePath)
	if err != nil {
		t.Fatalf("Error reading %v envoyfilter template file: %v", envoyfilterName, err)
	}
	envoyFilter, err := tmpl.Evaluate(string(envoyFilterTmpl), params)
	if err != nil {
		t.Fatalf("Error evaluating %v envoyfilter template file: %v", envoyfilterName, err)
	}
	t.ConfigIstio().YAML(serverNs.Namespace.Name(), envoyFilter).ApplyOrFail(t, apply.Wait)
	t.Cleanup(func() {
		// Delete envoyfilter
		t.ConfigIstio().YAML(serverNs.Namespace.Name(), envoyFilter).DeleteOrFail(t)
	})
}

func echoAInstanceFromNs(t framework.TestContext, ns deployment.EchoNamespace) echo.Instance {
	t.Helper()
	if len(ns.A) == 0 {
		t.Fatalf("No echo instance found in namespace: %s", ns.Namespace.Name())
	}
	return ns.A[0]
}

func overrideParams(params, overrides map[string]any) map[string]any {
	for k, v := range overrides {
		params[k] = v
	}
	return params
}
