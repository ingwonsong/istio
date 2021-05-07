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

package install

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"istio.io/istio/prow/asm/tester/pkg/exec"
	"istio.io/istio/prow/asm/tester/pkg/gcp"
	"istio.io/istio/prow/asm/tester/pkg/install/revision"
	"istio.io/istio/prow/asm/tester/pkg/kube"
	"istio.io/istio/prow/asm/tester/pkg/resource"
)

const scriptaroRepoBase = "https://raw.githubusercontent.com/GoogleCloudPlatform/anthos-service-mesh-packages"

func downloadScriptaro(commit string, rev *revision.Config) (string, error) {
	scriptaroURL := fmt.Sprintf("%s/%s/scripts/asm-installer/install_asm", scriptaroRepoBase, commit)
	if rev != nil && rev.Version != "" {
		scriptaroURL = fmt.Sprintf("%s/release-%s-asm/scripts/asm-installer/install_asm", scriptaroRepoBase, rev.Version)
	}
	scriptaroName := "install_asm"

	log.Printf("Downloading scriptaro from %s...", scriptaroURL)
	resp, err := http.Get(scriptaroURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("scriptaro not found at URL: %s", scriptaroURL)
	}

	f, err := os.OpenFile(scriptaroName, os.O_WRONLY|os.O_CREATE, 0o555)
	if err != nil {
		return "", err
	}

	_, err = io.Copy(f, resp.Body)
	if err != nil {
		return "", err
	}
	f.Close()

	path, err := filepath.Abs(scriptaroName)
	if err != nil {
		return "", err
	}

	return path, nil
}

// createRemoteSecrets creates remote secrets for each cluster to each other cluster
func createRemoteSecrets(settings *resource.Settings, contexts []string) error {
	for _, context := range contexts {
		for _, otherContext := range contexts {
			if context == otherContext {
				continue
			}
			otherCluster := kube.GKEClusterSpecFromContext(otherContext)
			log.Printf("creating remote secret with context %s to cluster %s",
				context, otherCluster.Name)
			createRemoteSecretCmd := fmt.Sprintf("istioctl x create-remote-secret"+
				" --context %s --name %s", otherContext, otherCluster.Name)
			secretContents, err := exec.RunWithOutput(createRemoteSecretCmd)
			if err != nil {
				return fmt.Errorf("failed creating remote secret: %w", err)
			}
			secretFileName := fmt.Sprintf("%s_%s_%s.secret",
				otherCluster.ProjectID, otherCluster.Location, otherCluster.Name)
			if err := os.WriteFile(secretFileName, []byte(secretContents), 0o644); err != nil {
				return fmt.Errorf("failed to write secret to file: %w", err)
			}

			// for private clusters, convert the cluster master public IP to private IP
			if settings.FeatureToTest == resource.VPCSC {
				privateIPCmd := fmt.Sprintf("gcloud container clusters describe %s"+
					" --project %s --zone %s --format \"value(privateClusterConfig.privateEndpoint)\"",
					otherCluster.Name, otherCluster.ProjectID, otherCluster.Location)
				privateIP, err := exec.RunWithOutput(privateIPCmd)
				if err != nil {
					return fmt.Errorf("failed to retrieve private IP: %w", err)
				}
				sedCmd := fmt.Sprintf("sed -i 's/server\\:.*/server\\: https:\\/\\/%s/' %s",
					strings.TrimSpace(privateIP), secretFileName)
				if err := exec.Run(sedCmd); err != nil {
					return fmt.Errorf("sed command failed: %w", err)
				}
			}

			kubeCreateSecretCmd := fmt.Sprintf("kubectl apply -f %s --context %s",
				secretFileName, context)
			if err := exec.Run(kubeCreateSecretCmd); err != nil {
				return fmt.Errorf("failed to create remote secret: %w", err)
			}
			if settings.UseVMs {
				// TODO(landow) this is temporary until we have a user-facing way to enable multi-cluster + VMs
				// we have to wait for new pods, but we do this later so the pods can come up in parallel per-cluster
				patchWLECmd := fmt.Sprintf("kubectl -n istio-system set env deployment/istiod"+
					" --context %s PILOT_ENABLE_CROSS_CLUSTER_WORKLOAD_ENTRY=true", context)
				if err := exec.Run(patchWLECmd); err != nil {
					return fmt.Errorf("failed to patch istiod: %w", err)
				}
			}
		}
	}
	return nil
}

func setupPermissions(settings *resource.Settings) error {
	if settings.ControlPlane == resource.Unmanaged {
		if settings.ClusterType == resource.GKEOnGCP {
			log.Print("Set permissions to allow the Pods on the GKE clusters to pull images...")
			return setGcpPermissions(settings)
		} else {
			log.Print("Set permissions to allow the Pods on the multicloud clusters to pull images...")
			return setMulticloudPermissions(settings)
		}
	}
	return nil
}

func setGcpPermissions(settings *resource.Settings) error {
	cs := kube.GKEClusterSpecsFromContexts(settings.KubectlContexts)
	for _, c := range cs {
		if c.ProjectID != settings.GCRProject {
			projectNum, err := gcp.GetProjectNumber(c.ProjectID)
			if err != nil {
				return err
			}
			err = exec.Run(
				fmt.Sprintf("gcloud projects add-iam-policy-binding %s "+
					"--member=serviceAccount:%s-compute@developer.gserviceaccount.com "+
					"--role=roles/storage.objectViewer",
					settings.GCRProject,
					projectNum),
			)
			if err != nil {
				return fmt.Errorf("error adding the binding for the service account to access GCR: %w", err)
			}
		}
	}
	return nil
}

// TODO: use kubernetes client-go library instead of kubectl.
func setMulticloudPermissions(settings *resource.Settings) error {
	if settings.ClusterType == resource.BareMetal || settings.ClusterType == resource.GKEOnAWS || settings.ClusterType == resource.APM {
		os.Setenv("HTTP_PROXY", os.Getenv("MC_HTTP_PROXY"))
		defer os.Unsetenv("HTTP_PROXY")
	}

	secretName := "test-gcr-secret"
	cred := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	configs := filepath.SplitList(settings.Kubeconfig)
	for i, config := range configs {
		err := exec.Run(
			fmt.Sprintf("kubectl create ns istio-system --kubeconfig=%s", config),
		)
		if err != nil {
			return fmt.Errorf("Error at 'kubectl create ns ...': %w", err)
		}

		// Create the secret that can be used to pull images from GCR.
		err = exec.Run(
			fmt.Sprintf(
				"bash -c 'kubectl create secret -n istio-system docker-registry %s "+
					"--docker-server=https://gcr.io "+
					"--docker-username=_json_key "+
					"--docker-email=\"$(gcloud config get-value account)\" "+
					"--docker-password=\"$(cat %s)\" "+
					"--kubeconfig=%s'",
				secretName,
				cred,
				config,
			),
		)
		if err != nil {
			return fmt.Errorf("Error at 'kubectl create secret ...': %w", err)
		}

		// Save secret data once (to be passed into the test framework),
		// deleting the line that contains 'namespace'.
		if i == 0 {
			err = exec.Run(
				fmt.Sprintf(
					"bash -c 'kubectl -n istio-system get secrets %s --kubeconfig=%s -o yaml "+
						"| sed \"/namespace/d\" > %s'",
					secretName,
					config,
					fmt.Sprintf("%s/test_image_pull_secret.yaml", os.Getenv("ARTIFACTS")),
				),
			)
			if err != nil {
				return fmt.Errorf("Error at 'kubectl get secrets ...': %w", err)
			}
		}

		// Patch the service accounts to use imagePullSecrets. Should do this for each revision on cluster.
		rev := revision.RevisionLabel()
		serviceAccts := []string{
			"default",
			"istio-ingressgateway-service-account",
			// TODO(samnaser) remove suffix if we move forward with using single service account with aggregated
			// permissions for istio reader (https://github.com/istio/istio/pull/32888)
			"istio-reader-" + rev,
			"istiod-" + rev,
		}
		for _, serviceAcct := range serviceAccts {
			err = exec.Run(
				fmt.Sprintf(`bash -c 'cat <<EOF | kubectl --kubeconfig=%s apply -f -
apiVersion: v1
kind: ServiceAccount
metadata:
  name: %s
  namespace: istio-system
imagePullSecrets:
- name: %s
EOF'`,
					config,
					serviceAcct,
					secretName,
				),
			)
			if err != nil {
				return fmt.Errorf("Error at 'kubectl apply ...': %s", err)
			}
		}
	}

	return nil
}
