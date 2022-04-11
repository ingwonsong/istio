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
	"fmt"
	"log"

	"istio.io/istio/prow/asm/tester/pkg/resource"
	"istio.io/istio/prow/asm/tester/pkg/tests"
	"istio.io/istio/prow/asm/tester/pkg/tests/policyconstraint"
	"istio.io/istio/prow/asm/tester/pkg/tests/userauth"
	"istio.io/istio/prow/asm/tester/pkg/tests/vm"
)

func Setup(settings *resource.Settings) error {
	log.Println("🎬 start running the setups for the tests...")

	if err := tests.Setup(settings); err != nil {
		return fmt.Errorf("error setting up the tests: %w", err)
	}

	if settings.ControlPlane == resource.Unmanaged && settings.FeaturesToTest.Has(string(resource.UserAuth)) {
		log.Printf("Start running the test setup for UserAuth test")
		if err := userauth.Setup(settings); err != nil {
			return err
		}
	}

	if settings.FeaturesToTest.Has(string(resource.PolicyConstraint)) {
		log.Printf("Start running the test setup for PolicyController test")
		if err := policyconstraint.Setup(settings); err != nil {
			return err
		}
	}

	if settings.UseGCEVMs || settings.VMStaticConfigDir != "" {
		log.Printf("Start running the test setup for VM test")
		if err := vm.Setup(settings, settings.KubeContexts[0]); err != nil {
			return err
		}
	}

	return nil
}
