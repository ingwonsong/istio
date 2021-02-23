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

package system

import (
	"fmt"
	"log"
	"os"

	"istio.io/istio/prow/asm/tester/pkg/exec"
	"istio.io/istio/prow/asm/tester/pkg/resource"
)

func Teardown(settings *resource.Settings) error {
	log.Println("🎬 start cleaning up ASM control plane installation...")

	if settings.ControlPlane == resource.Unmanaged {
		if settings.ClusterType == resource.GKEOnGCP && settings.CA == resource.PrivateCA {
			if err := exec.Dispatch(
				settings.RepoRootDir,
				"cleanup_private_ca",
				nil); err != nil {
				return err
			}
		}

		if settings.WIP == resource.HUBWorkloadIdentityPool && settings.ClusterType != resource.OnPrem {
			if err := exec.Dispatch(
				settings.RepoRootDir,
				"cleanup_hub_setup",
				[]string{
					settings.GCRProject,
					settings.KubectlContexts,
				}); err != nil {
				return err
			}
		}

		cleanUpImages()
	} else {
		cleanUpImagesForManagedControlPlane()
	}

	return nil
}

// Clean up temporary images created for the e2e test.
// It's based on best-effort and does not return an error if deletion fails.
func cleanUpImages() {
	hub := os.Getenv("HUB")
	tag := os.Getenv("TAG")
	exec.RunMultiple([]string{
		fmt.Sprintf("gcloud beta container images delete %s/app:%s --force-delete-tags --quiet", hub, tag),
		fmt.Sprintf("gcloud beta container images delete %s/pilot:%s --force-delete-tags --quiet", hub, tag),
		fmt.Sprintf("gcloud beta container images delete %s/proxyv2:%s --force-delete-tags --quiet", hub, tag),
		fmt.Sprintf("gcloud beta container images delete %s/stackdriver-prometheus-sidecar:%s --force-delete-tags --quiet", hub, tag),
	})
}

// Clean up temporary images created for the managed control plane e2e test.
// It's based on best-effort and does not return an error if deletion fails.
func cleanUpImagesForManagedControlPlane() {
	hub := os.Getenv("HUB")
	tag := os.Getenv("TAG")
	exec.RunMultiple([]string{
		fmt.Sprintf("gcloud beta container images delete %s/cloudrun:%s --force-delete-tags --quiet", hub, tag),
		fmt.Sprintf("gcloud beta container images delete %s/proxyv2:%s --force-delete-tags --quiet", hub, tag),
	})
}
