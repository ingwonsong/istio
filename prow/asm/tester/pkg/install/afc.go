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
	"log"
	"os"
	"strings"
	"time"

	"istio.io/istio/prow/asm/tester/pkg/exec"
	"istio.io/istio/prow/asm/tester/pkg/gcp"
	"istio.io/istio/prow/asm/tester/pkg/kube"
)

func (c *installer) installASMManagedControlPlaneAFC() error {
	contexts := strings.Split(c.settings.KubectlContexts, ",")

	// ASM MCP Prow job should use staging AFC since we should alert before
	// issues reach production.
	if err := exec.Run("gcloud config set api_endpoint_overrides/gkehub https://staging-gkehub.sandbox.googleapis.com/"); err != nil {
		return fmt.Errorf("error setting gke hub endpoint to staging: %w", err)
	}

	projectID := c.settings.GCPProjects[0]
	// Use the first project as the environ name
	// must do this here because each installation depends on the value

	environProjectNumber, err := gcp.GetProjectNumber(projectID)
	if err != nil {
		return fmt.Errorf("failed to read environ number: %w", err)
	}
	os.Setenv("_CI_ENVIRON_PROJECT_NUMBER", strings.TrimSpace(environProjectNumber))

	for _, context := range contexts {
		contextLogger := log.New(os.Stdout,
			fmt.Sprintf("[kubeContext: %s] ", context), log.Ldate|log.Ltime)
		contextLogger.Println("Performing ASM installation via AFC...")
		cluster := kube.GKEClusterSpecFromContext(context)

		exec.Run("gcloud container hub memberships delete prow-test -q")

		if err := exec.Run(fmt.Sprintf("gcloud beta container hub memberships register %s --gke-cluster=%s/%s --enable-workload-identity", cluster.Name, cluster.Location, cluster.Name)); err != nil {
			return fmt.Errorf("scriptaro MCP installation failed: %w", err)
		}

		if err := exec.Run("gcloud components install alpha"); err != nil {
			return fmt.Errorf("installing alpha failed: %w", err)
		}

		if err := exec.Run(fmt.Sprintf(`gcloud alpha container hub mesh enable --project=%s`, projectID)); err != nil {
			return fmt.Errorf("enabling servicemesh feature failed: %w", err)
		}

		if err := exec.Run("kubectl create ns istio-system --context " + context); err != nil {
			return fmt.Errorf("creating istio-system namespace failed: %w", err)
		}

		for i := 0; i < 10; i++ {
			time.Sleep(10 * time.Second)
			err = exec.Run("kubectl wait --for condition=established --timeout=10s crd/controlplanerevisions.mesh.cloud.google.com --context " + context)
			if err == nil {
				break
			}
		}
		if err != nil {
			return fmt.Errorf("waiting for crd failed: %w", err)
		}

		if err := exec.Run(
			fmt.Sprintf(`bash -c 'cat <<EOF | kubectl apply --context=%s -f -
apiVersion: mesh.cloud.google.com/v1alpha1
kind: ControlPlaneRevision
metadata:
    name: asm-managed
    namespace: istio-system
    annotations:
        mesh.cloud.google.com/image: %s
spec:
    type: managed_service
    channel: regular
EOF'`, context, os.Getenv("HUB")+"/cloudrun:"+os.Getenv("TAG"))); err != nil {
			return fmt.Errorf("error applying CPRevision CR: %w", err)
		}

		if err := exec.Run("kubectl wait --for=condition=ProvisioningFinished controlplanerevision asm-managed -n istio-system  --timeout 240s --context " + context); err != nil {
			return fmt.Errorf("waiting for ProvisioningFinished condition failed: %w", err)
		}

		if err := exec.Run(
			fmt.Sprintf(`bash -c 'cat <<EOF | kubectl apply --context=%s -f -
apiVersion: v1
data:
  mesh: |-
    accessLogFile: /dev/stdout
kind: ConfigMap
metadata:
  name: asm
  namespace: istio-system
EOF'`, context)); err != nil {
			return fmt.Errorf("error enabling access logging to help with debugging tests")
		}

		// Install Gateway
		if err := exec.Run("kubectl apply -f tools/packaging/knative/gateway -n istio-system --context=" + context); err != nil {
			return fmt.Errorf("error installing injected-gateway: %w", err)
		}

		contextLogger.Println("Done installing MCP via AFC...")
	}

	if err := createRemoteSecrets(c.settings, contexts); err != nil {
		return fmt.Errorf("failed to create remote secrets: %w", err)
	}

	return nil
}
