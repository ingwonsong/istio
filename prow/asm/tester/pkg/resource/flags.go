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
	"log"
	"os"

	"github.com/octago/sflags/gen/gpflag"
	"github.com/spf13/pflag"
	"go.uber.org/multierr"
	"k8s.io/apimachinery/pkg/util/sets"
)

func BindFlags(settings *Settings) *pflag.FlagSet {
	// Default settings assignments
	settings.ControlPlane = Unmanaged
	settings.CA = MeshCA
	settings.WIP = GKEWorkloadIdentityPool
	settings.GCPProjects = []string{}
	settings.GKENetworkName = DefaultGKENetworkName
	settings.TestTarget = "test"
	settings.TestStartEventPath = "upgrade-gke"
	// VM setting defaults
	settings.VMServiceProxyAgentGCSPath = "gs://gce-service-proxy-canary/service-proxy-agent/releases/service-proxy-agent-staging-latest.tgz"
	settings.VMServiceProxyAgentInstallerGCSPath = "gs://gce-service-proxy-canary/service-proxy-agent-installer/releases/installer-staging-latest.tgz"
	settings.VMServiceProxyAgentASMVersion = "1.10.0"
	settings.VMImageFamily = "debian-10"
	settings.VMImageProject = "debian-cloud"
	// Installation script default to `asmcli`
	settings.UseASMCLI = true

	flags := pflag.NewFlagSet("asm pipeline tester", pflag.ExitOnError)
	err := gpflag.ParseTo(settings, flags)
	if err != nil {
		log.Fatalf("err: %v", err)
	}

	// Flag aliases
	// TODO(landow): fully remove staticvm support
	flags.StringVar(&settings.VMStaticConfigDir, "static-vms", "", "a directory in echo-vm-provisioner/configs that contains config files for provisioning the VM test environment")
	// TODO(chizhg): delete after we update the Prow jobs to use --vm-image-family
	flags.StringVar(&settings.VMImageFamily, "vm-distro", "debian-10", "VM distribution that will be used as the `--image-family` flag value when using `gcloud compute instance-templates create` to create the VMs.")
	// TODO(chizhg): delete after we update the Prow jobs to use --vm-image-project
	flags.StringVar(&settings.VMImageProject, "image-project", "debian-cloud", "VM image project that will be used as the `--image-project` flag value when using `gcloud compute instance-templates create` to create the VMs.")
	return flags
}

// ReconcileAndValidateSettings reconciles and performs basic checks for the settings.
func ReconcileAndValidateSettings(settings *Settings) error {
	var errs []error

	if os.Getenv("KUBECONFIG") == "" && settings.Kubeconfig == "" {
		errs = append(errs, errors.New("--kubeconfig must be set when KUBECONFIG env var is empty"))
	}
	// KUBECONFIG env var can be overriden with the --kubeconfig flag.
	if settings.Kubeconfig != "" {
		os.Setenv("KUBECONFIG", settings.Kubeconfig)
	}
	settings.Kubeconfig = os.Getenv("KUBECONFIG")

	settings.FeaturesToTest = sets.NewString(settings.TempFeaturesToTest...)

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
	if settings.UseOnePlatform && settings.ClusterType != GKEOnAWS && settings.ClusterType != GKEOnAzure {
		errs = append(errs, errors.New("--use-oneplatform can only be used with GKE on AWS or GKE on Azure"))
	}

	return multierr.Combine(errs...)
}

func pathExists(path string) bool {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false
	}
	return true
}
