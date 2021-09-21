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
	"regexp"
	"strings"

	"istio.io/istio/prow/asm/tester/pkg/exec"
	"istio.io/istio/prow/asm/tester/pkg/gcp"
	"istio.io/istio/prow/asm/tester/pkg/install/revision"
	"istio.io/istio/prow/asm/tester/pkg/kube"
	"istio.io/istio/prow/asm/tester/pkg/resource"
)

const scriptRepoBase = "https://raw.githubusercontent.com/GoogleCloudPlatform/anthos-service-mesh-packages"

var enableOptionsArgs = []string{}

func downloadInstallScript(settings *resource.Settings, rev *revision.Config) (string, error) {
	var scriptBaseName string
	var scriptURL string
	if settings.UseASMCLI {
		scriptBaseName = "asmcli"
		scriptURL = fmt.Sprintf("%s/%s/asmcli/%s", scriptRepoBase, settings.NewtaroCommit, scriptBaseName)
	} else {
		scriptBaseName = "install_asm"
		scriptURL = fmt.Sprintf("%s/%s/scripts/asm-installer/%s", scriptRepoBase, settings.ScriptaroCommit, scriptBaseName)
		if rev != nil && rev.Version != "" {
			scriptURL = fmt.Sprintf("%s/release-%s-asm/scripts/asm-installer/%s", scriptRepoBase, rev.Version, scriptBaseName)
		}
	}

	log.Printf("Downloading script from %s...", scriptURL)
	resp, err := http.Get(scriptURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("script not found at URL: %s", scriptURL)
	}
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	f, err := os.OpenFile(scriptBaseName, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o555)
	if err != nil {
		return "", err
	}
	_, err = f.Write(bodyBytes)
	if err != nil {
		return "", err
	}
	err = f.Close()
	if err != nil {
		return "", err
	}

	path, err := filepath.Abs(scriptBaseName)
	if err != nil {
		return "", err
	}

	allOptions, err := exec.RunWithOutput(path + " --help")
	if err != nil {
		return "", err
	}
	enableFinder := regexp.MustCompile(`--enable_[^\s]+`)
	// The --enable_* options most installs should pass.
	// They consist of everything except gcp iam role modifications.
	disallowedEnableFlags := map[string]bool{
		"--enable_all":           true,
		"--enable_gcp_iam_roles": true,
	}
	foundOptionsSet := map[string]bool{} // avoids duplicates
	enableOptionsArgs = []string{}
	for _, foundFlag := range enableFinder.FindAll([]byte(allOptions), -1) {
		foundFlagString := string(foundFlag)
		if foundOptionsSet[foundFlagString] || disallowedEnableFlags[foundFlagString] {
			continue
		}
		foundOptionsSet[foundFlagString] = true
		enableOptionsArgs = append(enableOptionsArgs, foundFlagString)
	}

	return path, nil
}

func getInstallEnableFlags() []string {
	return enableOptionsArgs
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
		}
	}
	return nil
}

func setupPermissions(settings *resource.Settings, rev *revision.Config) error {
	if settings.ControlPlane == resource.Unmanaged {
		if settings.ClusterType == resource.GKEOnGCP {
			log.Print("Set permissions to allow the Pods on the GKE clusters to pull images...")
			return setGcpPermissions(settings)
		} else {
			log.Print("Set permissions to allow the Pods on the multicloud clusters to pull images...")
			return setMulticloudPermissions(settings, rev)
		}
	}
	return nil
}

func setGcpPermissions(settings *resource.Settings) error {
	if settings.InstallOverride.IsSet() {
		log.Print("No need to set IAM permission if the images are from a specified registry.")
		return nil
	}
	cs := kube.GKEClusterSpecsFromContexts(settings.KubeContexts)
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
func setMulticloudPermissions(settings *resource.Settings, rev *revision.Config) error {
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
		if err != nil && !strings.Contains(err.Error(), "AlreadyExists") {
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
		if err != nil && !strings.Contains(err.Error(), "AlreadyExists") {
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
		var istiodSvcAccount string
		if rev.Name != "" {
			istiodSvcAccount = fmt.Sprintf("istiod-%s", rev.Name)
		} else if settings.UseASMCLI {
			// asmcli install uses _CI_NO_REVISION or the "default" revision so no need for suffix
			istiodSvcAccount = "istiod"
		} else {
			// only the bash based install will use the auto-generated revision
			istiodSvcAccount = fmt.Sprintf("istiod-%s", revision.RevisionLabel())
		}
		serviceAccts := []string{
			"default",
			ingressGatewayServiceAccount,
			"istio-eastwestgateway-service-account",
			"istio-reader-service-account",
			istiodSvcAccount,
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
