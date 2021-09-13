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

	"istio.io/istio/prow/asm/tester/pkg/exec"
	"istio.io/istio/prow/asm/tester/pkg/gcp"
	"istio.io/istio/prow/asm/tester/pkg/kube"
	"istio.io/istio/prow/asm/tester/pkg/resource"
)

func (c *installer) installASMManagedControlPlane() error {
	contexts := c.settings.KubeContexts

	log.Println("Downloading ASM script for the installation...")
	scriptPath, err := downloadInstallScript(c.settings, nil)
	if err != nil {
		return fmt.Errorf("failed to download the install script: %w", err)
	}

	// Most (except VPC-SC) ASM MCP Prow jobs connect to staging MeshConfig API.
	// We use these jobs to test/alert our staging ADS proxy.
	if !c.settings.UseProdMeshConfigAPI {
		if err := exec.Run("sed -i 's/meshconfig\\.googleapis\\.com/staging-meshconfig.sandbox.googleapis.com/g' " + scriptPath); err != nil {
			return fmt.Errorf("error replacing the meshconfig to staging-meshconfig API for MCP installation: %w", err)
		}
	}

	// Use the first project as the environ name
	// must do this here because each installation depends on the value
	environProjectNumber, err := gcp.GetProjectNumber(kube.GKEClusterSpecFromContext(contexts[0]).ProjectID)
	if err != nil {
		return fmt.Errorf("failed to read environ number: %w", err)
	}
	os.Setenv("_CI_ENVIRON_PROJECT_NUMBER", strings.TrimSpace(environProjectNumber))

	for _, context := range contexts {
		contextLogger := log.New(os.Stdout,
			fmt.Sprintf("[kubeContext: %s] ", context), log.Ldate|log.Ltime)
		contextLogger.Println("Performing ASM installation...")
		cluster := kube.GKEClusterSpecFromContext(context)

		if c.settings.FeatureToTest == resource.Addon {
			// Enable access logs to make debugging possible
			if err := exec.Run(fmt.Sprintf("bash -c 'kubectl --context=%s get cm istio -n istio-system -o yaml | sed \"s/accessLogFile\\:.*$/accessLogFile\\: \\/dev\\/stdout/g\" | kubectl replace -f -'", context)); err != nil {
				return fmt.Errorf("error enabling access logs for testing with Addon: %w", err)
			}
		}

		contextLogger.Println("Running installation using install script...")
		if err := exec.Run(scriptPath,
			exec.WithAdditionalEnvs(generateMCPInstallEnvvars(c.settings)),
			exec.WithAdditionalArgs(generateMCPInstallFlags(c.settings, cluster))); err != nil {
			return fmt.Errorf("MCP installation using script failed: %w", err)
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

		if c.settings.FeatureToTest == resource.Addon {
			contextLogger.Println("Skipping gateway, already installed by addon")
		} else {
			if err := exec.Run("kubectl apply -f tools/packaging/knative/gateway -n istio-system --context=" + context); err != nil {
				return fmt.Errorf("error installing injected-gateway: %w", err)
			}
		}
	}

	if err := createRemoteSecrets(c.settings, contexts); err != nil {
		return fmt.Errorf("failed to create remote secrets: %w", err)
	}

	return nil
}

func generateMCPInstallFlags(settings *resource.Settings, cluster *kube.GKEClusterSpec) []string {
	var installFlags []string
	if settings.UseASMCLI {
		installFlags = append(installFlags, "install")
	} else {
		installFlags = append(installFlags, "--mode", "install")
	}

	installFlags = append(installFlags,
		"--project_id", cluster.ProjectID,
		"--cluster_location", cluster.Location,
		"--cluster_name", cluster.Name,
		"--managed",
		"--enable_cluster_labels",
		"--enable_namespace_creation",
		"--enable_registration",
		// Currently, MCP only uses mesh CA.
		"--ca", "mesh_ca",
		"--verbose")

	if settings.FeatureToTest == resource.CNI || settings.FeatureToTest == resource.Addon {
		// Addon always will use CNI
		installFlags = append(installFlags, "--option", "cni-managed")
	}
	if settings.UseVMs {
		installFlags = append(installFlags, "--option", "vm")
	}
	return installFlags
}

func generateMCPInstallEnvvars(settings *resource.Settings) []string {
	// _CI_ASM_PKG_LOCATION _CI_ASM_IMAGE_LOCATION are required for unreleased
	// ASM and its install script (master and staging branch).
	// For sidecar proxy and Istiod, _CI_CLOUDRUN_IMAGE_HUB and
	// _CI_CLOUDRUN_IMAGE_TAG are used.
	envvars := []string{}
	if settings.InstallOverride.IsSet() {
		envvars = append(envvars,
			"_CI_ASM_IMAGE_LOCATION="+settings.InstallOverride.Hub,
			"_CI_ASM_IMAGE_TAG="+settings.InstallOverride.Tag,
			"_CI_ASM_PKG_LOCATION="+settings.InstallOverride.ASMImageBucket,
			"_CI_CLOUDRUN_IMAGE_HUB="+settings.InstallOverride.Hub+"/cloudrun",
			"_CI_CLOUDRUN_IMAGE_TAG="+settings.InstallOverride.Tag,
		)
	} else {
		envvars = append(envvars,
			"_CI_ASM_IMAGE_LOCATION="+os.Getenv("HUB"),
			"_CI_ASM_IMAGE_TAG="+os.Getenv("TAG"),
			"_CI_ASM_PKG_LOCATION="+resource.DefaultASMImageBucket,
			"_CI_CLOUDRUN_IMAGE_HUB="+os.Getenv("HUB")+"/cloudrun",
			"_CI_CLOUDRUN_IMAGE_TAG="+os.Getenv("TAG"),
		)
	}
	if settings.UseASMCLI {
		envvars = append(envvars, "_CI_ASM_KPT_BRANCH="+settings.NewtaroCommit)
	} else {
		envvars = append(envvars, "_CI_ASM_KPT_BRANCH="+settings.ScriptaroCommit)
	}
	return envvars
}
