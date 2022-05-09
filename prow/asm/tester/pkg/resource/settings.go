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

package resource

import (
	"fmt"

	"k8s.io/apimachinery/pkg/util/sets"
)

// Settings is a struct that uses annotations from github.com/octago/sflags
// to define all the user configurable Tester settings on the command line.
type Settings struct {
	// Root directory of the repository
	RepoRootDir string `flag:"repo-root-dir" desc:"Root directory of the repository."`

	// A list of kubeconfig files that can be used to connnect to the test clusters
	// TODO(chizhg): change it to an array
	Kubeconfig string `flag:"kubeconfig" desc:"A list of kubeconfig files that can be used to connect to the test clusters."`

	// The GCP projects used for creating GKE clusters and other resources
	GCPProjects []string `flag:"gcp-projects" desc:"A list of GCP projects used for creating GKE clusters and other resources."`

	// Type of the cluster
	ClusterType ClusterType `flag:"cluster-type" desc:"Type of the k8s cluster."`

	// Topology of the cluster
	ClusterTopology ClusterToplology `flag:"cluster-topology" desc:"Topology of the k8s clusters."`

	// GKE Network Name
	GKENetworkName string `flag:"gke-network-name" desc:"The name of the GKE Network to use."`

	// Overrides building ASM from source and installing it that way.
	// Is set from a string in the form "${HUB}:${TAG}[:ASM_IMAGE_BUCKET]".
	InstallOverride InstallOverride `flag:"install-from" desc:"Overrides installing from the source if supplied. If non empty will interpretted as \"${HUB}:${TAG}[:ASM_IMAGE_BUCKET]\", and no compilation will be done. The optional ASM_IMAGE_BUCKET segment will override the default asm-staging-images bucket location to look at for downloading ASM during install. Only supported for GKE on GCP installs."`

	// Use OnePlatform API to provision the cluster
	UseOnePlatform bool `flag:"use-oneplatform" desc:"Whether to use oneplatform API to provision k8s clusters."`

	// MulticloudOverrideEnvironProject makes GCPProjects take precedence over hardcoded values for multicloud projects.
	MulticloudOverrideEnvironProject bool `flag:"multicloud-override-environ-project" desc:"When running on multicloud clusters, prefer --gcp-projects to hardcoded project names."`

	// ASMVersion indicates what ASM version this version of ASM belongs to.
	// Format is similar to revision versions, e.g. 1.10, 1.11.
	ASMVersion string `flag:"asm-version" desc:"asm-version pins scripts and istiod versions to release versions, instead of specific commits and locally-built binary. For example: 1.10, 1.11."`

	// A list of http proxy used for multicloud cluster connection
	ClusterProxy []string

	// A list of ssh user to connect to the bootstrap VM of Baremetal/AWS cluster
	ClusterSSHUser []string

	// A list of ssh key to connect to the bootstrap VM of Baremetal/AWS cluster
	ClusterSSHKey []string

	// The additional features to test for this test flow
	FeaturesToTest     sets.String
	TempFeaturesToTest []string `flag:"feature" desc:"Feature to test for this test flow."`

	// UNMANAGED, MANAGED, or MANAGED_LOCAL
	ControlPlane ControlPlaneType `flag:"control-plane" desc:"Type of the control plane, can be one of UNMANAGED, MANAGED or MANAGED_LOCAL."`

	// Use asmcli as the installation script.
	// Deprecated: flag left only to ensure old tests do not fail when using --use-asmcli
	DeprecatedUseASMCLI bool `flag:"use-asmcli" desc:"Use asmcli as the installation script."`

	// Certificate Authority to use, can be one of CITADEL, MESHCA or PRIVATECA
	CA CAType `flag:"ca" desc:"Certificate Authority to use, can be one of CITADEL, MESHCA or PRIVATECA."`

	// Workload Identity Pool, can be one of GKE or HUB
	WIP WIPType `flag:"wip" desc:"Workload Identity Pool, can be one of GKE or HUB."`

	// Path to the revision config file (see revision-deployer/README.md)
	RevisionConfig string `flag:"revision-config" desc:"Path to the revision config file (see revision-deployer/README.md)."`

	// Test target for the make command to run the tests, e.g. test.integration.asm.security
	TestTarget string `flag:"test" desc:"Test target for the make command to run the tests, e.g. test.integration.asm.security."`

	// Test to disable
	DisabledTests string `flag:"disabled-tests" desc:"Tests to disable, should be a regex that matches the test and test suite names."`

	// Path to an event that can be triggered from within the test suite
	TestStartEventPath string `flag:"test-start-event-path" desc:"A path that is used to start an event from within the test suite, it used to make a request to a server residing in the infra code."`

	// Port that clients should use to trigger events from within test suite
	TestStartEventPort string `flag:"test-start-event-port" desc:"Port that clients should use to trigger events occuring in the infra code."`

	// Whether to install CloudESF as ingress gateway.
	InstallCloudESF bool `flag:"install-cloudesf" desc:"Whether to install CloudESF as ingress gateway."`

	// UseDefaultInjectionLabels determines whether to use the "istio-injection=enabled" and
	// "sidecar.istio.io/inject" labels for workload injection
	UseDefaultInjectionLabels bool `flag:"use-default-injection-labels" desc:"If set uses default injection labels for workload injection."`

	// UseAutoCPManagement determines whether to use automatic control plane management to deploy a managed control plane.
	// This option should only be used when ControlPlane is MANAGED or MANAGED_LOCAL.
	UseAutoCPManagement bool `flag:"use-auto-cp-management" desc:"If set uses automatic control plane management to deploy a managed control plane."`

	// Environment determines the Container API endpoint to use, can be one of test, staging, staging2 or prod
	Environment string `flag:"environment" desc:"Container API endpoint to use, one of 'test', 'staging', 'staging2', 'prod'"`

	// UseStackDriver if true, a server-side stackdriver component will be installed. This is useful for off-GCP.
	UseStackDriver bool `flag:"use-stackdriver" desc:"If set installs a server-side StackDriver component. Useful for off-GCP."`

	VMSettings

	MCPSettings

	RuntimeSettings
}

type VMSettings struct {
	// Whether to use VM in the control plane setup
	UseVMs bool `flag:"~vm" desc:"Whether to use VM in the control plane setup."`

	// The GCS bucket path for downloading the service proxy agent binary.
	VMServiceProxyAgentGCSPath string `flag:"~vm-service-agent-gcs-path" desc:"GCS bucket path for downloading the service proxy agent binary."`

	// The GCS bucket path for downloading the service proxy agent installer.
	VMServiceProxyAgentInstallerGCSPath string `flag:"~vm-service-agent-installer-gcs-path" desc:"GCS bucket path for downloading the service proxy agent installer."`

	// The ASM version to be set in VM agent metadata in X.Y.Z format.
	VMServiceProxyAgentASMVersion string `flag:"~vm-service-agent-asm-version" desc:"ASM version to be used in the VM agent metadata."`

	// A directory in echo-vm-provisioner/configs that contains config files for
	// provisioning the VM test environment
	// TODO(landow): fully remove staticvm support
	VMStaticConfigDir string `flag:"~vm-static-config-dir" desc:"A directory in echo-vm-provisioner/configs that contains config files for provisioning the VM test environment."`

	// If set, the Istio Go test framework will spin up GCE VMs based on the
	// configuration in the integration tests.
	UseGCEVMs bool `flag:"~gce-vms" desc:"If set, the Istio Go test framework will spin up GCE VMs based on the configuration in the integration tests."`

	// VM image family. This will be used as the `--image-family` flag value
	// when using `gcloud compute instance-templates create` to create the VMs.
	VMImageFamily string `flag:"~vm-image-family" desc:"VM distribution that will be used as the \"--image-family\" flag value when using \"gcloud compute instance-templates create\" to create the VMs."`

	// VM image project. This will be used as the `--image-project` flag value
	// when using `gcloud compute instance-templates create` to create the VMs.
	VMImageProject string `flag:"~vm-image-project" desc:"VM image project that will be used as the \"--image-project\" flag value when using \"gcloud compute instance-templates create\" to create the VMs."`
}

type MCPSettings struct {
	// Whether to use production MeshConfig API endpoint for Managed Control Plane.
	UseProdMeshConfigAPI bool `flag:"~prod-meshconfig" desc:"Whether to use production MeshConfig API endpoint for Managed Control Plane (MCP)."`

	// Only used if ControlPlane = MANAGED. Determines if AFC is used to install MCP.
	UseAFC bool `flag:"~use-afc" desc:"Only used if ControlPlane = MANAGED. Determines if AFC is used to install MCP."`
}

// RuntimeSettings contains fields that are only populated and shared during the
// test runtime.
// Since these are only generated and used during runetime executions, we do not
// want to expose them with the sflag library.
type RuntimeSettings struct {
	// The kubectl contexts name array for the current test clusters.
	KubeContexts []string `flag:"-"`

	// The directory that stores configuration files for running the tests flows.
	ConfigDir string `flag:"-"`

	// A list of GCP projects for where the GKE clusters are created.
	// They can be used in the test flow for e.g. hosting the test images with the GCRs
	ClusterGCPProjects []string `flag:"-"`

	// The project for the GCR that will be used to host the test images.
	GCRProject string `flag:"-"`

	// The source ranges that are trustable when creating/updating firewall rules.
	TrustableSourceRanges string `flag:"-"`

	// The commit ID of Newtaro repo to use asmcli to install ASM.
	NewtaroCommit string `flag:"-"`
}

func (s *Settings) String() string {
	result := ""
	result += fmt.Sprintf("RepoRootDir:                      %v\n", s.RepoRootDir)
	result += fmt.Sprintf("Kubeconfig:                       %v\n", s.Kubeconfig)
	result += fmt.Sprintf("GCPProjects:                      %v\n", s.GCPProjects)
	result += fmt.Sprintf("ClusterType:                      %v\n", s.ClusterType)
	result += fmt.Sprintf("ClusterTopology:                  %v\n", s.ClusterTopology)
	result += fmt.Sprintf("GKENetworkName:                   %v\n", s.GKENetworkName)
	result += fmt.Sprintf("InstallOverride:                  %v\n", s.InstallOverride)
	result += fmt.Sprintf("UseOnePlatform:                   %v\n", s.UseOnePlatform)
	result += fmt.Sprintf("MulticloudOverrideEnvironProject: %v\n", s.MulticloudOverrideEnvironProject)
	result += fmt.Sprintf("ASMVersion:                       %v\n", s.ASMVersion)
	result += fmt.Sprintf("ClusterProxy:                     %v\n", s.ClusterProxy)
	result += fmt.Sprintf("ClusterSSHUser:                   %v\n", s.ClusterSSHUser)
	result += fmt.Sprintf("ClusterSSHKey:                    %v\n", s.ClusterSSHKey)
	result += fmt.Sprintf("FeaturesToTest:                   %v\n", s.FeaturesToTest)
	result += fmt.Sprintf("TempFeaturesToTest:               %v\n", s.TempFeaturesToTest)
	result += fmt.Sprintf("ControlPlane:                     %v\n", s.ControlPlane)
	result += fmt.Sprintf("CA:                               %v\n", s.CA)
	result += fmt.Sprintf("WIP:                              %v\n", s.WIP)
	result += fmt.Sprintf("RevisionConfig:                   %v\n", s.RevisionConfig)
	result += fmt.Sprintf("TestTarget:                       %v\n", s.TestTarget)
	result += fmt.Sprintf("DisabledTests:                    %v\n", s.DisabledTests)
	result += fmt.Sprintf("TestStartEventPath:               %v\n", s.TestStartEventPath)
	result += fmt.Sprintf("TestStartEventPort:               %v\n", s.TestStartEventPort)
	result += fmt.Sprintf("InstallCloudESF:                  %v\n", s.InstallCloudESF)
	result += fmt.Sprintf("UseDefaultInjectionLabels:        %v\n", s.UseDefaultInjectionLabels)
	result += fmt.Sprintf("UseAutoCPManagement:              %v\n", s.UseAutoCPManagement)
	result += fmt.Sprintf("UseStackDriver:                   %v\n", s.UseStackDriver)
	result += fmt.Sprintf("\n**VMSettings**\n%v\n", s.VMSettings)
	result += fmt.Sprintf("\n**MCPSettings**\n%v\n", s.MCPSettings)
	result += fmt.Sprintf("\n**RuntimeSettings**\n%v\n", s.RuntimeSettings)
	return result
}

func (s VMSettings) String() string {
	result := ""
	result += fmt.Sprintf("UseVMs:                              %v\n", s.UseVMs)
	result += fmt.Sprintf("VMServiceProxyAgentGCSPath:          %v\n", s.VMServiceProxyAgentGCSPath)
	result += fmt.Sprintf("VMServiceProxyAgentInstallerGCSPath: %v\n", s.VMServiceProxyAgentInstallerGCSPath)
	result += fmt.Sprintf("VMServiceProxyAgentASMVersion:       %v\n", s.VMServiceProxyAgentASMVersion)
	result += fmt.Sprintf("VMStaticConfigDir:                   %v\n", s.VMStaticConfigDir)
	result += fmt.Sprintf("UseGCEVms:                           %v\n", s.UseGCEVMs)
	result += fmt.Sprintf("VMImageFamily:                       %v\n", s.VMImageFamily)
	result += fmt.Sprintf("VMImageProject:                      %v\n", s.VMImageProject)
	return result
}

func (s MCPSettings) String() string {
	result := ""
	result += fmt.Sprintf("UseProdMeshConfigAPI: %v\n", s.UseProdMeshConfigAPI)
	result += fmt.Sprintf("UseAFC:               %v\n", s.UseAFC)
	return result
}

func (s RuntimeSettings) String() string {
	result := ""
	result += fmt.Sprintf("KubeContexts:          %v\n", s.KubeContexts)
	result += fmt.Sprintf("ConfigDir:             %v\n", s.ConfigDir)
	result += fmt.Sprintf("ClusterGCPProjects:    %v\n", s.ClusterGCPProjects)
	result += fmt.Sprintf("GCRProject:            %v\n", s.GCRProject)
	result += fmt.Sprintf("TrustableSourceRanges: %v\n", s.TrustableSourceRanges)
	result += fmt.Sprintf("NewtaroCommit:         %v\n", s.NewtaroCommit)
	return result
}
