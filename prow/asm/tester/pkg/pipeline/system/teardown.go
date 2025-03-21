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
	"os"
	"path/filepath"
	"strings"

	"istio.io/istio/pkg/log"
	"istio.io/istio/prow/asm/tester/pkg/exec"
	"istio.io/istio/prow/asm/tester/pkg/gcp"
	"istio.io/istio/prow/asm/tester/pkg/kube"
	"istio.io/istio/prow/asm/tester/pkg/pipeline/env"
	"istio.io/istio/prow/asm/tester/pkg/resource"
)

const (
	CustomFleetProject = "asm-ci-mc"
)

type binding struct {
	member string
	role   string
}

func Teardown(settings *resource.Settings) error {
	log.Info("🎬 start cleaning up ASM control plane installation...\n")

	if settings.CA == resource.PrivateCA {
		cleanupPrivateCa(settings)
	}
	if settings.ControlPlane == resource.Unmanaged {
		cleanUpImages()
	} else {
		cleanUpImagesForManagedControlPlane()
	}
	cleanUpMemberships(settings)
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

func cleanUpMemberships(settings *resource.Settings) {
	cleanupCommands := []string{}
	var environProject string
	switch settings.ClusterType {
	case resource.OnPrem, resource.EKS, resource.AKS:
		environProject = CustomFleetProject
	default:
		log.Infof("CleanUpMemberships: Unsupported cluster type: %s ", settings.ClusterType)
		return
	}

	kubeConfigs := filepath.SplitList(settings.Kubeconfig)
	for i, config := range kubeConfigs {
		membershipDetailsCmd := fmt.Sprintf("kubectl --kubeconfig %s --context %s get memberships.hub.gke.io membership -o=jsonpath={.spec.identity_provider}",
			config, settings.KubeContexts[i])
		membershipDetails, err := exec.RunWithOutput(membershipDetailsCmd)
		if err != nil {
			log.Debugf("failed to get membership name for context %s: %v", settings.KubeContexts[i], err)
			continue
		}
		if membershipDetails == "" {
			continue //Membership likely does not exist.
		}
		lastSlashIndex := strings.LastIndex(membershipDetails, "/")
		if lastSlashIndex == -1 {
			log.Debugf("unexpected format for memerbershipDetails: %s", membershipDetails)
			continue
		}
		membershipName := membershipDetails[lastSlashIndex+1:]
		cleanupCommands = append(cleanupCommands, fmt.Sprintf("gcloud --project=%s container hub memberships delete %s --quiet", environProject, membershipName))
	}

	//delete admin-cluster memberships for on-prem clusters
	if settings.ClusterType == resource.OnPrem {
		for _, config := range kubeConfigs {
			adminConfig := filepath.Join(filepath.Dir(config), "admin-kubeconfig.yaml")
			adminClusterName, err := exec.RunWithOutput(`kubectl config view -o 'jsonpath={.contexts[0].name}' --kubeconfig=` + adminConfig)
			if err != nil {
				log.Debugf(fmt.Sprintf("error getting the admin cluster name: %v", err))
				continue
			}
			adminClusterName = strings.TrimSpace(string(adminClusterName))
			if adminClusterName == "" {
				log.Debugf(fmt.Sprintf("context in admin-kubeconfig.yaml is empty"))
				continue
			}
			cleanupCommands = append(cleanupCommands, fmt.Sprintf("gcloud --project=%s container hub memberships delete %s --quiet", environProject, adminClusterName))
		}
	}
	exec.RunMultiple(cleanupCommands)
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
	for _, projectIdSrc := range settings.ClusterGCPProjects {
		for _, projectIdDest := range settings.ClusterGCPProjects {
			if projectIdDest != projectIdSrc {
				projectNum, err := gcp.GetProjectNumber(projectIdDest)
				if err != nil {
					return err
				}
				bindings := []binding{
					{member: fmt.Sprintf("serviceAccount:%s-compute@developer.gserviceaccount.com", projectNum), role: "roles/storage.objectViewer"},
					{member: fmt.Sprintf("serviceAccount:%s-compute@developer.gserviceaccount.com", projectNum), role: "roles/artifactregistry.reader"},
					{member: fmt.Sprintf("serviceAccount:service-%s@gcp-sa-gkehub.iam.gserviceaccount.com", projectNum), role: "roles/gkehub.serviceAgent"},
					{member: fmt.Sprintf("serviceAccount:service-%s@gcp-sa-staging-gkehub.iam.gserviceaccount.com", projectNum), role: "roles/gkehub.serviceAgent"},
					{member: fmt.Sprintf("serviceAccount:service-%s@gcp-sa-servicemesh.iam.gserviceaccount.com", projectNum), role: "roles/anthosservicemesh.serviceAgent"},
					{member: fmt.Sprintf("serviceAccount:service-%s@gcp-sa-staging-servicemesh.iam.gserviceaccount.com", projectNum), role: "roles/anthosservicemesh.serviceAgent"},
					{member: fmt.Sprintf("serviceAccount:service-%s@container-engine-robot.iam.gserviceaccount.com", projectNum), role: "roles/container.hostServiceAgentUser"},
				}
				for _, b := range bindings {
					cmd := exec.Command("gcloud", "projects", "remove-iam-policy-binding", projectIdSrc,
						"--member="+fmt.Sprintf(b.member),
						"--role="+fmt.Sprintf(b.role))
					if err := cmd.Run(); err != nil {
						log.Warn(fmt.Errorf("error removing gcp permissions: error removing the binding (%s)  (%s) for the service account to access GCR: %w", "--member="+fmt.Sprintf(b.member), "--role="+fmt.Sprintf(b.role), err))
					}
				}
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
