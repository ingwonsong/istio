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

package canonicalservices

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/echo/echoboot"
	"istio.io/istio/pkg/test/framework/components/namespace"
	"istio.io/istio/pkg/test/framework/resource"
	"istio.io/istio/pkg/test/util/retry"
)

var echoNames = []string{"foo", "bar"}

func TestCanonicalServices(t *testing.T) {
	framework.
		NewTest(t).
		Features("observability.telemetry.canonical-services").
		Run(func(ctx framework.TestContext) {
			retry.UntilSuccessOrFail(ctx, func() error {
				return verifyCanonicalServices(ctx, echoNames)
			}, retry.Delay(time.Second*2), retry.Timeout(time.Second*120))
		})
}

func TestMain(m *testing.M) {
	framework.
		NewSuite(m).
		Setup(setupEchoes(echoNames)).
		Run()
}

func setupEchoes(echoNames []string) resource.SetupFn {
	return func(ctx resource.Context) error {
		ns, err := namespace.New(ctx, namespace.Config{
			Inject: true,
			Prefix: "canonical-service",
		})
		if err != nil {
			return err
		}
		builder := echoboot.NewBuilder(ctx, ctx.Clusters()...)
		for _, name := range echoNames {
			builder.WithConfig(echo.Config{
				Namespace: ns,
				Service:   name,
			})
		}
		_, err = builder.Build()
		return err
	}
}

// verifyCanonicalServices ensures that the canonical service controller generated canonical
// service resources for each echo instance.
func verifyCanonicalServices(ctx framework.TestContext, echoNames []string) error {
	const canonicalServicePath = "/apis/anthos.cloud.google.com/v1beta1/canonicalservices"
	for _, c := range ctx.Clusters().Kube() {
		data, err := c.CoreV1().RESTClient().
			Get().AbsPath(canonicalServicePath).DoRaw(context.TODO())
		if err != nil {
			return fmt.Errorf("failed to retrieve canonical services: %v", err)
		}
		canonicalServices := string(data)
		for _, name := range echoNames {
			if !strings.Contains(canonicalServices, fmt.Sprintf("\"name\":\"%s\"", name)) {
				return fmt.Errorf("expected canonical service output to contain service %q, got %s",
					name, canonicalServices)
			}
		}
	}
	return nil
}
