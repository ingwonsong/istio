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
	"errors"
	"fmt"
	"os"

	"github.com/spf13/pflag"
	"go.uber.org/multierr"
)

func BindFlags(settings *Settings) *pflag.FlagSet {
	// Default control plane type to Unmanaged
	settings.ControlPlane = Unmanaged
	// Default ca type to MeshCA
	settings.CA = MeshCA
	// Default workload identity pool to GKE
	settings.WIP = GKEWorkloadIdentityPool

	flags := pflag.NewFlagSet("asm pipeline tester", pflag.ExitOnError)
	flags.StringVar(&settings.RepoRootDir, "repo-root-dir", "", "root directory of the repository")
	flags.StringVar(&settings.Kubeconfig, "kubeconfig", "", "a list of kubeconfig files that can be used to connect to the test clusters")
	flags.StringSliceVar(&settings.GCPProjects, "gcp-projects", []string{}, "a list of GCP projects used for creating GKE clusters and other resources")
	flags.Var(&settings.ClusterType, "cluster-type", "type of the k8s cluster")
	flags.Var(&settings.ClusterTopology, "cluster-topology", "topology of the k8s clusters")
	flags.Var(&settings.FeatureToTest, "feature", "feature to test for this test flow")

	flags.Var(&settings.ControlPlane, "control-plane", "type of the control plane, can be one of UNMANAGED or MANAGED")
	flags.BoolVar(&settings.UseProdMeshConfigAPI, "prod-meshconfig", false, "whether to use production MeshConfig API endpoint for Managed Control Plane (MCP)")
	flags.BoolVar(&settings.UseAFC, "use-afc", false, "Only used if ControlPlane = MANAGED. Determines if AFC is used to install MCP.")
	flags.Var(&settings.CA, "ca", "Certificate Authority to use, can be one of CITADEL, MESHCA or PRIVATECA")
	flags.Var(&settings.WIP, "wip", "Workload Identity Pool, can be one of GKE or HUB")
	flags.StringVar(&settings.RevisionConfig, "revision-config", "", "path to the revision config file (see revision-deployer/README.md)")
	flags.StringVar(&settings.TestTarget, "test", "test.integration.multicluster.kube.presubmit", "test target for the make command to run the tests, e.g. test.integration.asm.security")
	flags.StringVar(&settings.DisabledTests, "disabled-tests", "", "tests to disable, should be a regex that matches the test and test suite names")

	flags.BoolVar(&settings.UseVMs, "vm", false, "whether to use VM in the control plane setup")
	flags.StringVar(&settings.VMServiceProxyAgentGCSPath, "vm-service-agent-gcs-path",
		"gs://gce-service-proxy-canary/service-proxy-agent/releases/service-proxy-agent-staging-latest.tgz", "GCS bucket path for downloading the service proxy agent binary")
	flags.StringVar(&settings.VMServiceProxyAgentASMVersion, "vm-service-agent-asm-version", "1.10.0", "ASM version to be used in the VM agent metadata")
	// TODO(landow): fully remove staticvm support
	flags.StringVar(&settings.VMStaticConfigDir, "vm-static-config-dir", "", "a directory in echo-vm-provisioner/configs that contains config files for provisioning the VM test environment")
	// TODO(landow): fully remove staticvm support
	flags.StringVar(&settings.VMStaticConfigDir, "static-vms", "", "a directory in echo-vm-provisioner/configs that contains config files for provisioning the VM test environment")
	flags.BoolVar(&settings.UseGCEVMs, "gce-vms", false, "If set, the Istio Go test framework will spin up GCE VMs based on the configuration in the integration tests.")
	flags.StringVar(&settings.VMImageFamily, "vm-image-family", "debian-10", "VM distribution that will be used as the `--image-family` flag value when using `gcloud compute instance-templates create` to create the VMs.")
	// TODO(chizhg): delete after we update the Prow jobs to use --vm-image-family
	flags.StringVar(&settings.VMImageFamily, "vm-distro", "debian-10", "VM distribution that will be used as the `--image-family` flag value when using `gcloud compute instance-templates create` to create the VMs.")
	flags.StringVar(&settings.VMImageProject, "vm-image-project", "debian-cloud", "VM image project that will be used as the `--image-project` flag value when using `gcloud compute instance-templates create` to create the VMs.")
	// TODO(chizhg): delete after we update the Prow jobs to use --vm-image-project
	flags.StringVar(&settings.VMImageProject, "image-project", "debian-cloud", "VM image project that will be used as the `--image-project` flag value when using `gcloud compute instance-templates create` to create the VMs.")
	flags.StringVar(&settings.TestStartEventPath, "test-start-event-path", "upgrade-gke", "a path that is used to start an event from within the test suite, it used to make a request to a server residing in the infra code")
	flags.StringVar(&settings.TestStartEventPort, "test-start-event-port", "", "port that clients should use to trigger events occuring in the infra code")


	return flags
}

// ValidateSettings performs basic checks for the settings.
func ValidateSettings(settings *Settings) error {
	var errs []error

	if os.Getenv("KUBECONFIG") == "" && settings.Kubeconfig == "" {
		errs = append(errs, errors.New("--kubeconfig must be set when KUBECONFIG env var is empty"))
	}
	// KUBECONFIG env var can be overriden with the --kubeconfig flag.
	if settings.Kubeconfig != "" {
		os.Setenv("KUBECONFIG", settings.Kubeconfig)
	}
	settings.Kubeconfig = os.Getenv("KUBECONFIG")

	if !pathExists(settings.RepoRootDir) {
		errs = append(errs, fmt.Errorf("--repo-root-dir must be set as a valid path, now is %q", settings.RepoRootDir))
	}
	// TODO: verify --revision-config and --vm-static-config-dir to be valid
	// paths.

	if settings.ClusterType == "" {
		errs = append(errs, errors.New("--cluster-type must be set"))
	}
	if settings.ClusterTopology == "" {
		errs = append(errs, errors.New("--cluster-topology must be set"))
	}
	if settings.TestTarget == "" {
		errs = append(errs, errors.New("--test-target must be set"))
	}

	return multierr.Combine(errs...)
}

func pathExists(path string) bool {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false
	}
	return true
}
