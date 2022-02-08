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
	"path/filepath"

	"istio.io/istio/prow/asm/tester/pkg/exec"
	"istio.io/istio/prow/asm/tester/pkg/resource"
)

func Run(settings *resource.Settings) error {
	log.Println("🎬 start running the tests...")

	// TODO: convert the remainder of the script to Go
	runTestsScript := filepath.Join(settings.RepoRootDir, "prow/asm/tester/scripts/run-tests.sh")
	if err := exec.Run(runTestsScript); err != nil {
		return fmt.Errorf("error running the ASM tests: %w", err)
	}

	makeTarget := settings.TestTarget
	// TODO(samnaser) move this to prow job config
	if settings.ControlPlane == resource.Managed {
		if settings.ClusterTopology == resource.SingleCluster {
			makeTarget = "test.integration.asm.mcp"
		}
	}
	return exec.Run("make " + makeTarget)
}
