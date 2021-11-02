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

	"istio.io/istio/pkg/test/framework/util"

	"istio.io/istio/prow/asm/tester/pkg/exec"
	"istio.io/istio/prow/asm/tester/pkg/gcp"
	"istio.io/istio/prow/asm/tester/pkg/kube"
	"istio.io/istio/prow/asm/tester/pkg/resource"
)

func (c *installer) installASMManagedControlPlaneAFC() error {
	contexts := c.settings.KubeContexts

	// ASM MCP VPCSC with AFC test requires the latest, as of 10/13/2021, unreleased gcloud binary .
	// TODO(ruigu): Remove this part after the http://b/204468175.
	if c.settings.FeatureToTest == resource.VPCSC {
		if err := util.UpdateCloudSDKToPiperHead(); err != nil {
			return err
		}
	} else {
		// ASM MCP Prow job (except VPCSC) should use staging AFC since we should alert before
		// issues reach production.
		if err := exec.Run("gcloud config set api_endpoint_overrides/gkehub https://staging-gkehub.sandbox.googleapis.com/"); err != nil {
			return fmt.Errorf("error setting gke hub endpoint to staging: %w", err)
		}
	}

	// AFC uses staging GKE hub. Clean up staging GKE Hub membership from previous test runs.
	// TODO(ruigu): Remove this when we're able to delete staging hub memberships in boskos. b/202133285
	if err := exec.Run(`bash -c 'gcloud container hub memberships list --format="value(name)" | while read line ; do gcloud container hub memberships delete $line --location global --quiet ; done'`); err != nil {
		return fmt.Errorf("error clean up gke hub endpoint in staging: %w", err)
	}

	// Use the first project as the environ name
	// must do this here because each installation depends on the value
	projectID := c.settings.GCPProjects[0]
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

		log.Println("Downloading ASM script for the installation...")
		if !c.settings.UseASMCLI {
			return fmt.Errorf("asmcli must be used for afc: %w", err)
		}
		scriptPath, err := downloadInstallScript(c.settings, nil)
		if err != nil {
			return fmt.Errorf("failed to download the install script: %w", err)
		}

		contextLogger.Println("Running installation using install script...")
		if err := exec.Run(scriptPath,
			exec.WithAdditionalEnvs(generateAFCInstallEnvvars(c.settings)),
			exec.WithAdditionalArgs(generateAFCInstallFlags(c.settings, cluster))); err != nil {
			return fmt.Errorf("MCP installation via AFC failed: %w", err)
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

func generateAFCInstallFlags(settings *resource.Settings, cluster *kube.GKEClusterSpec) []string {
	installFlags := []string{
		"x",
		"install",
		"--project_id", cluster.ProjectID,
		"--cluster_location", cluster.Location,
		"--cluster_name", cluster.Name,
		"--managed",
		// Fix the channel to rapid since the go test needs to know injection label beforehand.
		// Without this, AFC will use GKE channel which can change when we bump the cluster version.
		// The test will overwrite the istiod/proxyv2 image with test image built on-the-fly if
		// staging environment is used.
		"--channel", "rapid",
		"--enable-all", // We can't use getInstallEnableFlags() since it apparently doesn't match what AFC expects
		"--verbose",
	}
	if settings.FeatureToTest == resource.VPCSC {
		installFlags = append(installFlags, "--use_vpcsc")
	}
	return installFlags
}

func generateAFCInstallEnvvars(settings *resource.Settings) []string {
	// _CI_ASM_PKG_LOCATION _CI_ASM_IMAGE_LOCATION are required for unreleased
	// ASM and its install script (master and staging branch).
	envvars := []string{
		"_CI_ASM_KPT_BRANCH=" + settings.NewtaroCommit,
	}
	if settings.InstallOverride.IsSet() {
		envvars = append(envvars,
			"_CI_ASM_IMAGE_LOCATION="+settings.InstallOverride.Hub,
			"_CI_ASM_IMAGE_TAG="+settings.InstallOverride.Tag,
			"_CI_ASM_PKG_LOCATION="+settings.InstallOverride.ASMImageBucket,
		)
	} else {
		// ASM MCP VPCSC test is required to use production by VPCSC integration.
		// Unfortunately, production meshconfig control plane doesn't have access
		// to asm-staging-images. So we'll skip any image overwrite for this particular
		// test.
		if settings.FeatureToTest != resource.VPCSC {
			envvars = append(envvars,
				"_CI_ASM_IMAGE_LOCATION="+os.Getenv("HUB"),
				"_CI_ASM_IMAGE_TAG="+os.Getenv("TAG"),
				"_CI_ASM_PKG_LOCATION="+resource.DefaultASMImageBucket,
			)
		}
	}
	return envvars
}
