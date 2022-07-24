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

package tests

import (
	"log"
	"os"

	"istio.io/istio/prow/asm/tester/pkg/resource"
	"istio.io/istio/prow/asm/tester/pkg/tests/kubevirtvm"
	"istio.io/istio/prow/asm/tester/pkg/tests/policyconstraint"
	"istio.io/istio/prow/asm/tester/pkg/tests/userauth"
)

func Teardown(settings *resource.Settings) error {
	log.Println("🎬 start tearing down the tests...")

	if settings.ControlPlane == resource.Unmanaged && settings.FeaturesToTest.Has(string(resource.UserAuth)) {
		return userauth.Teardown(settings)
	}

	if settings.UseKubevirtVM {
		log.Printf("Start running the teardown for kubevirt vm tests")
		if err := kubevirtvm.TearDown(settings); err != nil {
			return err
		}
	}

	if settings.FeaturesToTest.Has(string(resource.PolicyConstraint)) {
		return policyconstraint.Teardown(settings)
	}

	// Unset the proxy if the tests are run on proxied clusters.
	if len(settings.ClusterProxy) != 0 {
		os.Unsetenv("HTTPS_PROXY")
	}

	return nil
}
