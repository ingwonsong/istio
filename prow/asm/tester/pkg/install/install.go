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
	"strconv"
	"strings"

	"istio.io/istio/prow/asm/tester/pkg/exec"
	"istio.io/istio/prow/asm/tester/pkg/install/multiversion"
	"istio.io/istio/prow/asm/tester/pkg/install/revision"
	"istio.io/istio/prow/asm/tester/pkg/resource"
)

const (
	istioctlPath = "out/linux_amd64/istioctl"
	basePath = "manifests/charts/base/files/gen-istio-cluster.yaml"

	// Envvar consts
	cloudAPIEndpointOverrides = "CLOUDSDK_API_ENDPOINT_OVERRIDES_CONTAINER"
	testEndpoint              = "https://test-container.sandbox.googleapis.com/"
	stagingEndpoint           = "https://staging-container.sandbox.googleapis.com/"
	staging2Endpoint          = "https://staging2-container.sandbox.googleapis.com/"
)

func (c *installer) install(r *revision.Config) error {
	if c.settings.ControlPlane == resource.Unmanaged {
		switch c.settings.ClusterType {
		case resource.GKEOnGCP:
			log.Println("🏄 performing ASM installation")
			return c.installASM(r)
		case resource.HybridGKEAndBareMetal:
			log.Println("🏄 performing ASM installation on hybrid clusters")
			return c.installASMOnHybridClusters(r)
		default:
			log.Println("🏄 performing ASM installation on multicloud clusters")
			return c.installASMOnMulticloudClusters(r)
		}
	} else if c.settings.ControlPlane == resource.Managed {
		if c.settings.UseAFC {
			log.Println("🏄 performing ASM MCP installation via AFC")
			return c.installASMManagedControlPlaneAFC(r)
		} else {
			log.Println("🏄 performing ASM MCP installation")
			return c.installASMManagedControlPlane(r)
		}
	}

	return fmt.Errorf("unsupported installation for ASM %q on Platform %q", c.settings.ControlPlane, c.settings.ClusterType)
}

// preInstall contains all steps required before performing the direct install
func (c *installer) preInstall(rev *revision.Config) error {
	if !c.settings.InstallOverride.IsSet() {
		if c.settings.ControlPlane == resource.Unmanaged {
			if err := exec.Dispatch(c.settings.RepoRootDir,
				"prepare_images", nil); err != nil {
				return err
			}
		} else {
			var args []string
			if c.settings.FeaturesToTest.Has(string(resource.Addon)) {
				args = append(args, string(resource.Addon))
			}
			if err := exec.Dispatch(c.settings.RepoRootDir,
				"prepare_images_for_managed_control_plane", args); err != nil {
				return err
			}
		}
		if err := exec.Dispatch(c.settings.RepoRootDir,
			"build_istioctl",
			nil); err != nil {
			return err
		}
	}

	// gke-on-prem clusters are registered into Hub during cluster creations in the on-prem Hub CI jobs
	if c.settings.ClusterTopology == resource.MultiProject && c.settings.ClusterType == resource.GKEOnGCP {
		if err := exec.Dispatch(
			c.settings.RepoRootDir,
			"register_clusters_in_hub",
			[]string{
				c.settings.GCRProject,
				strconv.FormatBool(c.settings.UseASMCLI),
				strings.Join(c.settings.KubeContexts, " "),
			}); err != nil {
			return err
		}
		if err := exec.Dispatch(
			c.settings.RepoRootDir,
			"clean_up_multiproject_permissions",
			[]string{
				c.settings.GCRProject,
				strings.Join(c.settings.ClusterGCPProjects, " "),
			}); err != nil {
			return err
		}
	}
	// Setup permissions to allow pulling images from GCR registries.
	// TODO(samnaser) should be in env setup but since service account name
	// depends on revision name, we must have istioctl built before we can do this step.
	if err := setupPermissions(c.settings, rev); err != nil {
		return err
	}
	os.Setenv("ASM_REVISION_LABEL", revision.RevisionLabel())
	return nil
}

// postInstall contains all steps required after installing
func (c *installer) postInstall(rev *revision.Config) error {
	if err := c.processKubeconfigs(); err != nil {
		return err
	}
	// For cross-version compat testing we need to use webhooks with per-revision object
	// selectors. Older Istio versions do not have this so we must manually create them.
	for _, context := range c.settings.KubeContexts {
		if err := multiversion.ReplaceWebhook(rev, context); err != nil {
			return err
		}
		if c.settings.ControlPlane != resource.Managed {
			// for managed cases, we by default enable StackDriver logging. Enable access logs in this method
			// will actually disable the SD logging. Instead, we configure them separately to avoid this issue.
			if err := EnableAccessLogging(context); err != nil {
				return err
			}
		}
	}
	return nil
}

func EnableAccessLogging(context string) error {
	if err := exec.Run(
		fmt.Sprintf(`bash -c 'cat <<EOF | kubectl apply --context=%s -f -
apiVersion: telemetry.istio.io/v1alpha1
kind: Telemetry
metadata:
  name: mesh-default
  namespace: istio-system
spec:
  accessLogging:
  - providers:
    - name: envoy
    disabled: false
EOF'`, context)); err != nil {
		// warn instead of error since some tests do not setup CRDs at all
		log.Println("error enabling access logging to help with debugging tests")
	}
	return nil
}

// processKubeconfigs should perform steps required after running ASM installation
func (c *installer) processKubeconfigs() error {
	return exec.Dispatch(
		c.settings.RepoRootDir,
		"process_kubeconfigs",
		nil)
}
