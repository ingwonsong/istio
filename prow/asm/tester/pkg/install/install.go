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
	"istio.io/istio/prow/asm/tester/pkg/gcp"
	"istio.io/istio/prow/asm/tester/pkg/install/multiversion"
	"istio.io/istio/prow/asm/tester/pkg/install/revision"
	"istio.io/istio/prow/asm/tester/pkg/pipeline/env"
	"istio.io/istio/prow/asm/tester/pkg/resource"
)

const (
	istioctlPath = "out/linux_amd64/istioctl"

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
		default:
			log.Println("🏄 performing ASM installation on multicloud clusters")
			return c.installASMOnMulticloudClusters(r)
		}
	} else if c.settings.ControlPlane == resource.Managed {
		if c.settings.UseAFC {
			log.Println("🏄 performing ASM MCP installation via AFC")
			return c.installASMManagedControlPlaneAFC()
		} else {
			log.Println("🏄 performing ASM MCP installation")
			return c.installASMManagedControlPlane()
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
			if err := exec.Dispatch(c.settings.RepoRootDir,
				"prepare_images_for_managed_control_plane", nil); err != nil {
				return err
			}
		}
		if err := exec.Dispatch(c.settings.RepoRootDir,
			"build_istioctl",
			nil); err != nil {
			return err
		}
	}

	if c.settings.CA == resource.PrivateCA {
		if c.settings.ClusterType == resource.GKEOnGCP {
			if err := exec.Dispatch(
				c.settings.RepoRootDir,
				"setup_private_ca",
				[]string{
					strings.Join(c.settings.KubeContexts, ","),
				}); err != nil {
				return err
			}
		} else if c.settings.ClusterType == resource.OnPrem || c.settings.ClusterType == resource.BareMetal {
			if err := exec.Dispatch(
				c.settings.RepoRootDir,
				"setup_private_ca",
				[]string{
					// For Tailorbird projects, create subordinate CAS pools in the tester project
					fmt.Sprintf("context_%s_%s_cluster", env.SharedGCPProject, gcp.CasRootCaLoc),
				}); err != nil {
				return err
			}
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
