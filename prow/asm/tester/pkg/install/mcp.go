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
	"path/filepath"
	"strings"

	"istio.io/istio/prow/asm/tester/pkg/exec"
	"istio.io/istio/prow/asm/tester/pkg/gcp"
	"istio.io/istio/prow/asm/tester/pkg/install/revision"
	"istio.io/istio/prow/asm/tester/pkg/kube"
	"istio.io/istio/prow/asm/tester/pkg/resource"
)

func (c *installer) installASMManagedControlPlane(rev *revision.Config) error {
	contexts := c.settings.KubeContexts

	log.Println("Downloading ASM script for the installation...")
	scriptPath, err := downloadInstallScript(c.settings, nil)
	if err != nil {
		return fmt.Errorf("failed to download the install script: %w", err)
	}

	// Addon migration tests migration logic + prod AFC.
	if !c.settings.FeaturesToTest.Has(string(resource.Addon)) {
		// ASM MCP Prow job (except VPCSC) should use staging AFC since we should alert before
		// issues reach production.
		if err := exec.Run("gcloud config set api_endpoint_overrides/gkehub https://staging-gkehub.sandbox.googleapis.com/"); err != nil {
			return fmt.Errorf("error setting gke hub endpoint to staging: %w", err)
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

		outputDir, err := ASMOutputDir(rev)
		if err != nil {
			return fmt.Errorf("MCP create output dir failed: %w", err)
		}
		if err := exec.Run(scriptPath,
			exec.WithAdditionalEnvs(generateAFCInstallEnvvars(c.settings)),
			exec.WithAdditionalArgs(generateAFCBuildOfflineFlags(outputDir))); err != nil {
			return fmt.Errorf("MCP build offline pacakge failed: %w", err)
		}

		if c.settings.FeaturesToTest.Has(string(resource.Addon)) {
			// Enable access logs to make debugging possible
			if err := exec.Run(fmt.Sprintf("bash -c 'kubectl --context=%s get cm istio -n istio-system -o yaml | sed \"s/accessLogFile\\:.*$/accessLogFile\\: \\/dev\\/stdout/g\" | kubectl replace -f -'", context)); err != nil {
				return fmt.Errorf("error enabling access logs for testing with Addon: %w", err)
			}
			extraFlags := generateMCPInstallFlags(c.settings, cluster, outputDir)
			extraFlags = append(extraFlags, "--only_enable")
			if err := exec.Run(scriptPath,
				exec.WithAdditionalArgs(extraFlags)); err != nil {
				return fmt.Errorf("setup prerequsite failed: %w", err)
			}
			contextLogger.Println("Running asmcli to enable prerequisites only, use migration tool to perform install instead")
			continue
		}

		// VPC-SC only tests production so no need to patch CPRs.
		if !c.settings.FeaturesToTest.Has(string(resource.VPCSC)) {
			contextLogger.Println("Patching CPR file to change image...")
			if err := filepath.Walk(filepath.Join(outputDir, "asm", "control-plane-revision"), patchCPRWithImageWalkFn); err != nil {
				return fmt.Errorf("MCP patch ControlPlaneRevision with custom image failed: %w", err)
			}
		}
		contextLogger.Println("Running installation using install script...")
		if err := exec.Run(scriptPath,
			exec.WithAdditionalEnvs(generateMCPInstallEnvvars(c.settings)),
			exec.WithAdditionalArgs(generateMCPInstallFlags(c.settings, cluster, outputDir))); err != nil {
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
  name: istio-asm-managed-rapid
  namespace: istio-system
EOF'`, context)); err != nil {
			return fmt.Errorf("error enabling access logging to help with debugging tests")
		}

		if c.settings.FeaturesToTest.Has(string(resource.Addon)) {
			contextLogger.Println("Skipping gateway, already installed by addon")
		} else {
			if err := exec.Run("kubectl apply -f tools/packaging/knative/gateway -n istio-system --context=" + context); err != nil {
				return fmt.Errorf("error installing injected-gateway: %w", err)
			}
		}
	}

	if err := createRemoteSecrets(c.settings, rev, scriptPath); err != nil {
		return fmt.Errorf("failed to create remote secrets: %w", err)
	}

	return nil
}

func generateMCPInstallFlags(settings *resource.Settings, cluster *kube.GKEClusterSpec, outputDir string) []string {
	installFlags := []string{"install"}
	installFlags = append(installFlags, "--legacy")

	// Addon migration tests controls its own label
	if !settings.FeaturesToTest.Has(string(resource.Addon)) {
		installFlags = append(installFlags, "--channel", "rapid")
	}

	installFlags = append(installFlags,
		"--project_id", cluster.ProjectID,
		"--cluster_location", cluster.Location,
		"--cluster_name", cluster.Name,
		"--managed",
		"--enable_cluster_labels",
		"--enable_namespace_creation",
		"--enable_registration",
		"--output_dir", outputDir,
		"--offline",
		"--verbose")

	caFlags, _ := GenCaFlags(settings.CA, settings, cluster, false)
	installFlags = append(installFlags, caFlags...)
	installFlags = append(installFlags, "--fleet_id", settings.GCRProject)

	if settings.FeaturesToTest.Has(string(resource.CNI)) || settings.FeaturesToTest.Has(string(resource.Addon)) {
		// Addon always will use CNI
		installFlags = append(installFlags, "--option", "cni-managed")
	}
	if settings.FeaturesToTest.Has(string(resource.Addon)) {
		if os.Getenv("TEST_MIGRATION_MCP_CHANNEL") == "stable" {
			installFlags = append(installFlags, "--channel", "stable")
		} else {
			installFlags = append(installFlags, "--channel", "regular")
		}
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
		// ASM MCP VPCSC test is required to use production by VPCSC integration.
		// Unfortunately, production meshconfig control plane doesn't have access
		// to asm-staging-images. So we'll skip any image overwrite for this particular
		// test.
		if !settings.FeaturesToTest.Has(string(resource.VPCSC)) {
			envvars = append(envvars,
				"_CI_ASM_IMAGE_LOCATION="+os.Getenv("HUB"),
				"_CI_ASM_IMAGE_TAG="+os.Getenv("TAG"),
				"_CI_ASM_PKG_LOCATION="+resource.DefaultASMImageBucket,
				"_CI_CLOUDRUN_IMAGE_HUB="+os.Getenv("HUB")+"/cloudrun",
				"_CI_CLOUDRUN_IMAGE_TAG="+os.Getenv("TAG"),
				// Use CRDs from our branch instead of the KPT branch
				"_CI_BASE_REL_PATH="+filepath.Join(settings.RepoRootDir, basePath),
			)
		}
	}
	envvars = append(envvars, "_CI_ASM_KPT_BRANCH="+settings.NewtaroCommit)
	return envvars
}
