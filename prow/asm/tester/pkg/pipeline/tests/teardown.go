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

	"istio.io/istio/prow/asm/tester/pkg/resource"
	"istio.io/istio/prow/asm/tester/pkg/tests/userauth"
)

func Teardown(settings *resource.Settings) error {
	log.Println("🎬 start tearing down the tests...")

	if settings.ControlPlane == resource.Unmanaged && settings.FeatureToTest == resource.UserAuth {
		return userauth.Teardown(settings)
	}

	return nil
}
