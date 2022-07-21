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
	"istio.io/istio/prow/asm/tester/pkg/gcp"
	"istio.io/istio/prow/asm/tester/pkg/kube"
	"istio.io/istio/prow/asm/tester/pkg/pipeline/env"
	"istio.io/istio/prow/asm/tester/pkg/resource"
)

func Teardown(settings *resource.Settings) error {
	log.Println("🎬 start cleaning up ASM control plane installation...")

	if settings.CA == resource.PrivateCA {
		cleanupPrivateCa(settings)
	}
	if settings.ControlPlane == resource.Unmanaged {
		cleanUpImages()
	} else {
		cleanUpImagesForManagedControlPlane()
	}

	if err := removePermissions(settings); err != nil {
		return fmt.Errorf("error removing gcp permissions: %w", err)
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

func removePermissions(settings *resource.Settings) error {
	if settings.ClusterType == resource.GKEOnGCP && settings.ControlPlane == resource.Unmanaged {
		return removeGcpPermissions(settings)
	}
	return nil
}

func removeGcpPermissions(settings *resource.Settings) error {
	// If installing from another source, no need to handle the GCP permissions.
	if settings.InstallOverride.IsSet() {
		return nil
	}
	for _, projectId := range settings.ClusterGCPProjects {
		if projectId != settings.GCRProject {
			projectNum, err := gcp.GetProjectNumber(projectId)
			if err != nil {
				return err
			}
			err = exec.Run(
				fmt.Sprintf("gcloud projects remove-iam-policy-binding %s "+
					"--member=serviceAccount:%s-compute@developer.gserviceaccount.com "+
					"--role=roles/storage.objectViewer",
					settings.GCRProject,
					projectNum),
			)
			if err != nil {
				return fmt.Errorf("error removing the binding for the service account to access GCR: %w", err)
			}
		}
	}
	return nil
}

func cleanupPrivateCa(settings *resource.Settings) {
	var certTemplate string
	if settings.ClusterType == resource.GKEOnGCP {
		wip := fmt.Sprintf("group:%v.svc.id.goog:/allAuthenticatedUsers/",
			kube.GKEClusterSpecFromContext(settings.KubeContexts[0]).ProjectID)
		for _, context := range settings.KubeContexts {
			cluster := kube.GKEClusterSpecFromContext(context)
			location := "us-central1"
			if cluster != nil && !settings.FeaturesToTest.Has(string(resource.CAMigration)) {
				location = cluster.Location
			}
			caName := gcp.GetPrivateCAPool(env.SharedGCPProject, location)
			if settings.FeaturesToTest.Has(string(resource.CasCertTemplate)) {
				certTemplate = gcp.GetPrivateCACertTemplate(env.SharedGCPProject, location)
			} else {
				certTemplate = ""
			}
			os.Unsetenv("CA_POOL")
			exec.Dispatch(settings.RepoRootDir,
				"amend_privateca_iam",
				[]string{"remove-iam-policy-binding",
					caName,
					cluster.Location,
					wip,
					env.SharedGCPProject,
					certTemplate,
				})

		}
	}
}
