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

package gke

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"istio.io/istio/prow/asm/infra/boskos"
	"istio.io/istio/prow/asm/infra/config"
	"istio.io/istio/prow/asm/infra/deployer/common"
	"istio.io/istio/prow/asm/infra/exec"
	"istio.io/istio/prow/asm/infra/types"
)

const (
	name = "gke"

	// These names correspond to the resources configured in
	// https://gke-internal.googlesource.com/istio/test-infra-internal/+/refs/heads/master/boskos/config/resources.yaml
	sharedVPCHostBoskosResource      = "shared-vpc-host-gke-project"
	sharedVPCSVCBoskosResource       = "shared-vpc-svc-gke-project"
	vpcSCBoskosResource              = "vpc-sc-gke-project"
	vpcSCSharedVPCHostBoskosResource = "vpc-sc-shared-vpc-host-gke-project"
	vpcSCSharedVPCSVCBoskosResource  = "vpc-sc-shared-vpc-svc-gke-project"
	commonBoskosResource             = "gke-project"

	networkName = "prow-test-network"
)

var (
	retryableErrorPatterns = `.*does not have enough resources available to fulfill.*` +
		`,.*only \d+ nodes out of \d+ have registered; this is likely due to Nodes failing to start correctly.*` +
		`,.*All cluster resources were brought up.+ but: component .+ from endpoint .+ is unhealthy.*`

	baseFlags = []string{
		"--machine-type=e2-standard-4",
		"--num-nodes=2",
		"--region=us-central1,us-west1,us-east1",
		"--network=" + networkName,
		"--enable-workload-identity",
		"--ignore-gcp-ssh-key=true",
		"-v=2",
		"--gcp-service-account=" + os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"),
		"--retryable-error-patterns='" + retryableErrorPatterns + "'",
	}
)

func NewInstance(cfg config.Instance) *Instance {
	return &Instance{
		cfg: cfg,
	}
}

type Instance struct {
	cfg config.Instance
}

func (d *Instance) Name() string {
	return name
}

func (d *Instance) Run() error {
	log.Println("Will run kubetest2 gke deployer to create the clusters...")

	// Get the flags for the GKE deployer.
	flags, err := d.flags()
	if err != nil {
		return err
	}

	lis, err := common.NewWebServer(d.supportedHandlers())

	// Get the flags for the tester.
	testFlags, err := d.cfg.GetTesterFlags()
	if err != nil {
		return err
	}

	webServerFlags := d.cfg.GetWebServerFlags(lis)
	testFlags = append(testFlags, webServerFlags...)

	// Prepare full list of flags for kubetest2.
	cmd := fmt.Sprintf("kubetest2 %s", strings.Join(append(flags, testFlags...), " "))

	// Run the command.
	var buf bytes.Buffer
	if err = exec.Run(cmd,
		exec.WithWorkingDir(d.cfg.RepoRootDir),
		exec.WithWriter(io.MultiWriter(os.Stdout, &buf), io.MultiWriter(os.Stderr, &buf))); err != nil {
		return err
	}

	return nil
}

func (d *Instance) flags() ([]string, error) {
	// The name of the deployer is first in the list.
	flags := []string{d.Name()}

	// Get the base flags from the options.
	cfgFlags, err := d.cfg.GetDeployerFlags()
	if err != nil {
		return nil, err
	}
	flags = append(flags, cfgFlags...)

	// Append the base GKE flags.
	flags = append(flags, baseFlags...)

	// Append the extra deployer flags.
	flags = append(flags, d.getExtraDeployerFlags()...)

	// Get the release channel to use.
	releaseChannel, err := d.getReleaseChannel()
	if err != nil {
		return nil, err
	}

	// Append flags for the specific topology.
	topologyFlags, err := d.getTopologyFlags(releaseChannel)
	if err != nil {
		return nil, err
	}
	flags = append(flags, topologyFlags...)

	if d.cfg.Feature != "" {
		var featureFlags []string
		switch d.cfg.Feature {
		case types.VPCServiceControls:
			featureFlags, err = featureVPCSCPresetup(d.cfg.GCPProjects, d.cfg.Topology)
		case types.UserAuth:
		case types.Addon:
		case types.PrivateClusterUnrestrictedAccess:
		case types.PrivateClusterLimitedAccess:
		case types.PrivateClusterNoAccess:
		case types.ContainerNetworkInterface:
		case types.Autopilot:
		default:
			err = fmt.Errorf("feature %q is not supported", d.cfg.Feature)
		}
		flags = append(flags, featureFlags...)
	}

	flags = append(flags, "--test=exec", "--", d.cfg.TestScript)

	return flags, err
}

func (d *Instance) getTopologyFlags(releaseChannel types.ReleaseChannel) ([]string, error) {
	switch d.cfg.Topology {
	case types.SingleCluster:
		return d.singleClusterFlags(releaseChannel)
	case types.MultiCluster:
		return d.multiClusterFlags(releaseChannel)
	case types.MultiProject:
		return d.multiProjectMultiClusterFlags(releaseChannel)
	default:
		return nil, fmt.Errorf("cluster topology %q is not supported", d.cfg.Topology)
	}
}

func (d *Instance) getExtraDeployerFlags() []string {
	if d.cfg.Feature == types.Autopilot {
		return []string{"--autopilot=true", "--gcloud-command-group=beta"}
	}

	addonFlag := ""
	if d.cfg.Feature == types.Addon {
		addonFlag = " --addons=Istio"
	}
	if d.cfg.GcloudExtraFlags != "" {
		addonFlag = fmt.Sprintf("%s %s", addonFlag, d.cfg.GcloudExtraFlags)
	}

	// kubetest2's `create-command` flag will make its `gcloud-extra-flags` skipped so append those flags into `create-command` directly.
	return []string{"--gcloud-command-group=beta", fmt.Sprintf("--gcloud-extra-flags='--enable-network-policy%s'", addonFlag)}
}

func (d *Instance) getClusterVersion() string {
	if d.cfg.ClusterVersion == "" {
		return "latest"
	}
	return d.cfg.ClusterVersion
}

func (d *Instance) getReleaseChannel() (types.ReleaseChannel, error) {
	if d.cfg.Feature == types.Addon {
		// We only support clusters that have EnsureExists, currently available on rapid only
		return types.Rapid, nil
	}

	if d.cfg.ReleaseChannel != "" {
		return d.cfg.ReleaseChannel, nil
	}

	return types.None, nil
}

// singleClusterFlags returns the kubetest2 flags for single-cluster setup.
func (d *Instance) singleClusterFlags(releaseChannel types.ReleaseChannel) ([]string, error) {
	boskosResourceType := commonBoskosResource
	// Testing with VPC-SC requires a different project type.
	if d.cfg.Feature == types.VPCServiceControls {
		boskosResourceType = vpcSCBoskosResource
	}
	flags, err := d.getProjectFlag(func() (string, error) {
		return acquireBoskosProjectAndSetBilling(boskosResourceType)
	})
	if err != nil {
		return nil, fmt.Errorf("error acquiring GCP projects for singlecluster setup: %w", err)
	}
	flags = append(flags,
		"--cluster-name=prow-test")
	flags = append(flags,
		"--release-channel="+string(releaseChannel),
		"--version="+d.getClusterVersion())

	switch d.cfg.Feature {
	case types.PrivateClusterUnrestrictedAccess:
		flags = append(flags, "--private-cluster-access-level=unrestricted", "--private-cluster-master-ip-range=172.16.0.32/28,173.16.0.32/28,174.16.0.32/28")
	case types.PrivateClusterLimitedAccess:
		flags = append(flags, "--private-cluster-access-level=limited", "--private-cluster-master-ip-range=172.16.0.32/28,173.16.0.32/28,174.16.0.32/28")
	case types.PrivateClusterNoAccess:
		flags = append(flags, "--private-cluster-access-level=no", "--private-cluster-master-ip-range=172.16.0.32/28,173.16.0.32/28,174.16.0.32/28")
	}

	return flags, nil
}

// multiClusterFlags returns the kubetest2 flags for single-project
// multi-cluster setup.
func (d *Instance) multiClusterFlags(releaseChannel types.ReleaseChannel) ([]string, error) {
	boskosResourceType := commonBoskosResource
	// Testing with VPC-SC requires a different project type.
	if d.cfg.Feature == types.VPCServiceControls {
		boskosResourceType = vpcSCBoskosResource
	}

	flags, err := d.getProjectFlag(func() (string, error) {
		return acquireBoskosProjectAndSetBilling(boskosResourceType)
	})
	if err != nil {
		return nil, fmt.Errorf("error acquiring GCP projects for multicluster setup: %w", err)
	}
	flags = append(flags,
		"--cluster-name=prow-test1,prow-test2")
	flags = append(flags,
		"--release-channel="+string(releaseChannel),
		"--version="+d.getClusterVersion())

	return flags, nil
}

// featureVPCSCPresetup runs the presetup for VPC Service Control and returns the extra kubetest2 flags for creating the clusters,
// as per the instructions in https://docs.google.com/document/d/11yYDxxI-fbbqlpvUYRtJiBmGdY_nIKPJLbssM3YQtKI/edit#heading=h.e2laig460f1d
func featureVPCSCPresetup(gcpProjects []string, topology types.Topology) ([]string, error) {
	// networkProject is the project where the network is created.
	networkProject := gcpProjects[0]
	if err := exec.Run("gcloud config set project " + networkProject); err != nil {
		return nil, err
	}
	subnetMode := "auto"
	if len(gcpProjects) > 1 {
		subnetMode = "custom"
	}
	if err := exec.Run(fmt.Sprintf("gcloud compute networks create %s --subnet-mode=%s", networkName, subnetMode)); err != nil {
		return nil, err
	}
	// Set up the private access to Google APIs and services as per the
	// instructions in
	// https://docs.google.com/document/d/11yYDxxI-fbbqlpvUYRtJiBmGdY_nIKPJLbssM3YQtKI/edit#heading=h.xbyst5e8ialr
	// 1. Configure a route `restricted-vip` in the network to restricted.googleapis.com
	createRouteCmd := fmt.Sprintf(`gcloud compute routes create restricted-vip --network=%s --destination-range=199.36.153.4/30 \
		--next-hop-gateway=default-internet-gateway`, networkName)
	if err := exec.Run(createRouteCmd); err != nil {
		return nil, fmt.Errorf("error creating the restricted-vip route for VPC-SC testing: %w", err)
	}

	// Best effort cleanups for the previously created Cloud DNS managed-zone.
	// TODO(chizhg): in the future these cleanups should be handled by Boskos
	// 				 Janitor - https://github.com/kubernetes-sigs/boskos/issues/88
	exec.RunMultipleNoStop([]string{
		"gcloud dns record-sets transaction start --zone=googleapis-zone",
		`gcloud dns record-sets transaction remove --name=*.googleapis.com. \
			--type=CNAME restricted.googleapis.com. \
			--zone=googleapis-zone \
			--ttl=300`,
		`gcloud dns record-sets transaction remove --name=restricted.googleapis.com. \
			--type=A 199.36.153.4 199.36.153.5 199.36.153.6 199.36.153.7 \
			--zone=googleapis-zone \
			--ttl=300`,
		"gcloud dns record-sets transaction execute --zone=googleapis-zone",
		"gcloud beta dns managed-zones delete googleapis-zone",
	})
	exec.RunMultipleNoStop([]string{
		"gcloud dns record-sets transaction start --zone=gcr-zone",
		`gcloud dns record-sets transaction remove \
			--name=*.gcr.io. \
			--type=CNAME gcr.io. \
			--zone=gcr-zone \
			--ttl=300`,
		`gcloud dns record-sets transaction remove \
			--name=gcr.io. \
			--type=A 199.36.153.4 199.36.153.5 199.36.153.6 199.36.153.7 \
			--zone=gcr-zone \
			--ttl=300`,
		"gcloud dns record-sets transaction execute --zone gcr-zone",
		"gcloud beta dns managed-zones delete gcr-zone",
	})
	time.Sleep(1 * time.Minute)

	// TODO(ruigu): This is a temporary hack. Tracking bug: http://b/195134581
	// Before MCP officially support VPC-SC, private access to meshconfig.googleapis.com
	// will not work. For now, only MCP VPC-SC tests uses single cluster setup. It
	// will skip the private access related DNS routing. Also, adding this DNS
	// route maybe not required for VPCSC testing.
	if topology != types.SingleCluster {
		// 2. Set up the cloud DNS for Google APIs.
		if err := exec.RunMultiple(
			[]string{fmt.Sprintf(`gcloud beta dns managed-zones create googleapis-zone --visibility=private \
			--networks=https://www.googleapis.com/compute/v1/projects/%s/global/networks/%s \
			--description="Zone for googleapis.com" --dns-name=googleapis.com`, networkProject, networkName),
				"gcloud dns record-sets transaction start --zone=googleapis-zone",
				`gcloud dns record-sets transaction add --name=*.googleapis.com. \
		--type=CNAME restricted.googleapis.com. \
		--zone=googleapis-zone \
		--ttl=300`,
				`gcloud dns record-sets transaction add --name=restricted.googleapis.com. \
		--type=A 199.36.153.4 199.36.153.5 199.36.153.6 199.36.153.7 \
		--zone=googleapis-zone \
		--ttl=300`,
				"gcloud dns record-sets transaction execute --zone=googleapis-zone",
			}); err != nil {
			return nil, fmt.Errorf("error seting up the cloud DNS for Google APIs for the %q network: %w", networkName, err)
		}
	}
	// 3. Set up the cloud DNS for Google Container Registry service gcr.io.
	if err := exec.RunMultiple(
		[]string{fmt.Sprintf(`gcloud beta dns managed-zones create gcr-zone \
			--visibility=private \
			--networks=https://www.googleapis.com/compute/v1/projects/%s/global/networks/%s \
			--description="Zone for GCR" \
			--dns-name=gcr.io`, networkProject, networkName),
			"gcloud dns record-sets transaction start --zone=gcr-zone",
			`gcloud dns record-sets transaction add \
		--name=*.gcr.io. \
		--type=CNAME gcr.io. \
		--zone=gcr-zone \
		--ttl=300`,
			`gcloud dns record-sets transaction add \
		--name=gcr.io. \
		--type=A 199.36.153.4 199.36.153.5 199.36.153.6 199.36.153.7 \
		--zone=gcr-zone \
		--ttl=300`,
			"gcloud dns record-sets transaction execute --zone gcr-zone",
		}); err != nil {
		return nil, fmt.Errorf("error setting up the cloud DNS for Google Container Registry service gcr.io: %w", err)
	}

	flags := []string{"--private-cluster-access-level=limited"}
	switch topology {
	case types.SingleCluster:
		flags = append(flags, "--private-cluster-master-ip-range=172.16.0.32/28,173.16.0.32/28,174.16.0.32/28")
	case types.MultiCluster, types.MultiProject:
		flags = append(flags, "--private-cluster-master-ip-range=173.16.0.32/28,172.16.0.32/28,174.16.0.32/28,175.16.0.32/28,176.16.0.32/28,177.16.0.32/28")
	default:
		return nil, fmt.Errorf("topology %v for VPCSC is unsupported", topology)
	}
	return flags, nil
}

// multiProjectMultiClusterFlags returns the kubetest2 flags for multi-project
// multi-cluster setup.
func (d *Instance) multiProjectMultiClusterFlags(releaseChannel types.ReleaseChannel) ([]string, error) {
	hostBoskosResourceType := sharedVPCHostBoskosResource
	svcBoskosResourceType := sharedVPCSVCBoskosResource
	if d.cfg.Feature == types.VPCServiceControls {
		hostBoskosResourceType = vpcSCSharedVPCHostBoskosResource
		svcBoskosResourceType = vpcSCSharedVPCSVCBoskosResource
	}

	flags, err := d.getProjectFlag(func() (string, error) {
		return acquireMultiGCPProjects(hostBoskosResourceType, svcBoskosResourceType)
	})
	if err != nil {
		return nil, fmt.Errorf("error acquiring GCP projects for multi-project multi-cluster setup: %w", err)
	}
	flags = append(flags,
		"--cluster-name=prow-test1:1,prow-test2:2")
	flags = append(flags,
		"--subnetwork-ranges='172.16.4.0/22 172.16.16.0/20 172.20.0.0/14,10.0.4.0/22 10.0.32.0/20 10.4.0.0/14,173.16.4.0/22 173.16.16.0/20 173.20.0.0/14,11.0.4.0/22 11.0.32.0/20 11.4.0.0/14,174.16.4.0/22 174.16.16.0/20 174.20.0.0/14,12.0.4.0/22 12.0.32.0/20 12.4.0.0/14'")
	flags = append(flags,
		"--release-channel="+string(releaseChannel),
		"--version="+d.getClusterVersion())

	return flags, nil
}

// getProjectFlag configures the --project flag value for the kubetest2
// command to create GKE clusters.
func (d *Instance) getProjectFlag(acquireBoskosProject func() (string, error)) ([]string, error) {
	// Only acquire the GCP project from Boskos if it's running in CI.
	if common.IsRunningOnCI() {
		project, err := acquireBoskosProject()
		if err != nil {
			return nil, fmt.Errorf("error acquiring GCP projects from Boskos: %w", err)
		}
		d.cfg.GCPProjects = strings.Split(project, ",")

		return []string{"--project=" + project}, nil
	}
	return nil, nil
}

// acquire GCP projects for multi-project multi-cluster setup.
// These projects are mananged by the boskos project rental pool as configured in
// https://gke-internal.googlesource.com/istio/test-infra-internal/+/refs/heads/master/boskos/config/resources.yaml#105
func acquireMultiGCPProjects(hostBoskosResourceType string, svcBoskosResourceType string) (string, error) {
	// Acquire a host project from the project rental pool and set it as the
	// billing project.
	hostProject, err := acquireBoskosProjectAndSetBilling(hostBoskosResourceType)
	if err != nil {
		return "", fmt.Errorf("error acquiring a host project: %w", err)
	}
	// Remove all projects that are currently associated with this host project.
	associatedProjects, err := exec.Output(fmt.Sprintf("gcloud beta compute shared-vpc"+
		" associated-projects list %s --format=value(RESOURCE_ID)", hostProject))
	if err != nil {
		return "", fmt.Errorf("error getting the associated projects for %q: %w", hostProject, err)
	}
	associatedProjectsStr := strings.TrimSpace(string(associatedProjects))
	if associatedProjectsStr != "" {
		for _, project := range strings.Split(associatedProjectsStr, "\n") {
			// Sometimes this command returns error like below:
			// 	ERROR: (gcloud.beta.compute.shared-vpc.associated-projects.remove) Could not disable resource [asm-boskos-shared-vpc-svc-144] as an associated resource for project [asm-boskos-shared-vpc-host-69]:
			//    - Invalid resource usage: 'The resource 'projects/asm-boskos-shared-vpc-svc-144' is not linked to shared VPC host 'projects/asm-boskos-shared-vpc-host-69'.'.
			// but it's uncertain why this happens. Ignore the error for now
			// since the error says the project has already been dissociated.
			// TODO(chizhg): enable the error check after figuring out the cause.
			_ = exec.Run(fmt.Sprintf("gcloud beta compute shared-vpc"+
				" associated-projects remove %s --host-project=%s", project, hostProject))
		}
	}

	// Acquire two service projects from the project rental pool.
	serviceProjects := make([]string, 2)
	for i := 0; i < len(serviceProjects); i++ {
		sp, err := boskos.AcquireBoskosResource(svcBoskosResourceType)
		if err != nil {
			return "", fmt.Errorf("error acquiring a service project: %w", err)
		}
		serviceProjects[i] = sp
	}
	// gcloud requires one service project can only be associated with one host
	// project, so if the acquired service projects have already been associated
	// with one host project, remove the association.
	for _, sp := range serviceProjects {
		associatedHostProject, err := exec.Output(fmt.Sprintf("gcloud beta compute shared-vpc"+
			" get-host-project %s --format=value(name)", sp))
		if err != nil {
			return "", fmt.Errorf("error getting the associated host project for %q: %w", sp, err)
		}
		associatedHostProjectStr := strings.TrimSpace(string(associatedHostProject))
		if associatedHostProjectStr != "" {
			// TODO(chizhg): enable the error check after figuring out the cause.
			_ = exec.Run(fmt.Sprintf("gcloud beta compute shared-vpc"+
				" associated-projects remove %s --host-project=%s", sp, associatedHostProjectStr))
		}
	}

	return strings.Join(append([]string{hostProject}, serviceProjects...), ","), nil
}

func acquireBoskosProjectAndSetBilling(projectType string) (string, error) {
	project, err := boskos.AcquireBoskosResource(projectType)
	if err != nil {
		return "", fmt.Errorf("error acquiring a project with type %q: %w", projectType, err)
	}
	if err = exec.Run("gcloud config set billing/quota_project " + project); err != nil {
		return "", fmt.Errorf("error setting billing/quota_project to %q: %w", project, err)
	}

	return project, nil
}

func (d *Instance) newGkeUpgradeHandler() (func(http.ResponseWriter, *http.Request), error) {

	upgradeFunc := func(w http.ResponseWriter, _ *http.Request) {

		projects := d.cfg.GCPProjects
		if d.cfg.Topology == types.MultiProject {
			projects = projects[1:]
		}

		var upgradeCmds []string
		for _, project := range projects {
			clusters, err := exec.Output("gcloud container clusters list --format='value(name,location)' --project=" + project)
			if err != nil {
				log.Printf("error listing the clusters in the project: %v", err)
			}

			for _, clusterRegion := range strings.Split(strings.TrimSpace(string(clusters)), "\n") {
				arr := strings.Split(clusterRegion, "\t")
				clusterName := arr[0]
				region := arr[1]
				upgradeCmds = append(upgradeCmds, fmt.Sprintf(`gcloud container clusters upgrade %s --project %s --region %s \
					--cluster-version %s --master --quiet`, clusterName, project, region, d.cfg.UpgradeClusterVersion))
			}

		}

		for _, upgradeCmd := range upgradeCmds {
			if err := exec.Run(upgradeCmd); err != nil {
				log.Printf("error: %+v, while upgrading the cluster to version %s", err.Error(), d.cfg.UpgradeClusterVersion)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
	}

	return upgradeFunc, nil
}

func (d *Instance) supportedHandlers() map[string]func() (func(http.ResponseWriter, *http.Request), error) {
	supportedHandler := map[string]func() (func(http.ResponseWriter, *http.Request), error){}
	if d.cfg.UpgradeClusterVersion != "" {
		supportedHandler[common.UpgradePath] = d.newGkeUpgradeHandler
	}
	return supportedHandler
}
