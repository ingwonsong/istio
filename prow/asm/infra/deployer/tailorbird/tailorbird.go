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

package tailorbird

import (
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v2"

	"istio.io/istio/prow/asm/infra/config"
	"istio.io/istio/prow/asm/infra/deployer/common"
	"istio.io/istio/prow/asm/infra/exec"
	"istio.io/istio/prow/asm/infra/types"
)

const (
	name = "tailorbird"

	// the relative dir to the repo root dir to find the tailorbird custom config files
	configRelDir = "prow/asm/infra/deployer/tailorbird/config"
	// the relative dir from the working dir (istio.io/istio) to the TRAC-generated, ASM-specific config dir
	tracConfigRelDir = "../../team/anthos-trac-team/configs/tailorbird/asm/"

	// GCS path for downloading kubetest2-tailorbird binary
	kubetest2TailorbirdPath = "gs://tailorbird-artifacts/staging/kubetest2-tailorbird/2022-07-27-172450/kubetest2-tailorbird"

	installawsIamAuthenticatorCmd = `curl -o aws-iam-authenticator https://amazon-eks.s3.us-west-2.amazonaws.com/1.19.6/2021-01-05/bin/linux/amd64/aws-iam-authenticator \
			&& chmod +x ./aws-iam-authenticator \
			&& mv ./aws-iam-authenticator /usr/local/bin/aws-iam-authenticator`

	terraformVersion = "0.13.6"

	// the host HUB project for testing with on-prem
	onPremHubDevProject = "tairan-asm-multi-cloud-dev"
	// GKE
	retryableErrorPatterns = ".*does not have enough resources available to fulfill.*" +
		",.*only \\\\d+ nodes out of \\\\d+ have registered; this is likely due to Nodes failing to start correctly.*" +
		",.*All cluster resources were brought up.+ but: component .+ from endpoint .+ is unhealthy.*"

	commonBoskosResource             = "gke-project"
	vpcSCBoskosResource              = "vpc-sc-gke-project"
	vpcSCSharedVPCHostBoskosResource = "vpc-sc-shared-vpc-host-gke-project"
	vpcSCSharedVPCSVCBoskosResource  = "vpc-sc-shared-vpc-svc-gke-project"
	sharedVPCHostBoskosResource      = "shared-vpc-host-gke-project"
	sharedVPCSVCBoskosResource       = "shared-vpc-svc-gke-project"
	networkName                      = "prow-test-network"

	statusCheckInterval = 30
	statusCheckMaxRetry = 140
)

var (
	baseFlags = []string{
		"--down",
		"--status-check-interval=60",
		"--verbose",
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

type TemplateParameters struct {
	GCSBucket                    string
	Version                      string
	VersionPrefix                string
	ProjectName                  string
	ErrorPatterns                string
	ClusterNames                 []string
	BoskosResourceType           []string
	GcloudCommandGroup           string
	GcloudExtraFlags             string
	NetworkName                  string
	PrivateClusterAccessLevel    string
	PrivateClusterMasterIPRanges string
	SubnetworkRanges             string
	PrimaryID                    string
	BoskosProjectsRequested      []int
	WorkloadIdentityEnabled      bool
	IsAutoPilot                  bool
	IsBoskosProjectRequired      bool
	ReleaseChannel               types.ReleaseChannel
	Environment                  types.Environment
}

// Exported types to support unmarshalling rookery Yaml
type Rookery struct {
	Spec Spec
}
type Metadata struct {
	Name string
}
type Spec struct {
	Knests   []Knest
	Clusters []Cluster
}
type Knest struct {
	Spec Spec
}
type Cluster struct {
	Metadata Metadata
}

func (d *Instance) Name() string {
	return name
}

func (d *Instance) Run() error {
	log.Println("Will run kubetest2 tailorbird deployer to create the clusters...")

	// If clustertype is on-prem, clean up stale hub memberships that are older
	// than 8-hour to avoid exceeding quota
	// See http://b/195998781#comment10
	if string(d.cfg.Cluster) == string(types.GKEOnPrem) {
		hubEnvs := []string{
			"https://staging-gkehub.sandbox.googleapis.com/",
			"https://gkehub.googleapis.com/",
		}
		for _, v := range hubEnvs {
			if err := cleanMembership(v); err != nil {
				return err
			}
		}
	}

	if err := d.installTools(); err != nil {
		return fmt.Errorf("error installing tools for testing with Tailorbird: %w", err)
	}

	flags, err := d.flags()
	if err != nil {
		return err
	}

	lis, err := common.NewWebServer(d.supportedHandlers())
	if err != nil {
		return err
	}

	flags = append(flags, d.cfg.GetWebServerFlags(lis)...)

	// Run the deployer
	cmd := fmt.Sprintf("kubetest2 %s", strings.Join(flags, " "))
	return exec.Run(cmd, exec.WithWorkingDir(d.cfg.RepoRootDir))
}

func cleanMembership(hubEnv string) error {
	if err := exec.Run(fmt.Sprintf("gcloud config set api_endpoint_overrides/gkehub %s", hubEnv)); err != nil {
		return fmt.Errorf("error setting gke hub endpoint to %s: %w", hubEnv, err)
	}

	log.Printf("Cleaning up stale hub memberships in the project %s", onPremHubDevProject)
	hms, err := exec.Output(fmt.Sprintf("gcloud container hub memberships list --format='value(name)' --filter='updateTime<-P8H' --project=%s", onPremHubDevProject))
	if err != nil {
		return err
	}

	for _, hm := range strings.Split(strings.TrimSpace(string(hms)), "\n") {
		if strings.TrimSpace(hm) == "" {
			// hm may be empty
			continue
		}
		if err := exec.Run(fmt.Sprintf("gcloud container hub memberships delete %s --quiet --project=%s",
			hm, onPremHubDevProject)); err != nil {
			// Error may be expected and should not cause the program to return, e.g., other test instances
			// may also be cleaning up, which causes an error when deleting a membership that has been deleted.
			log.Printf("Cleaning up %s returns an err: %v", hm, err)
		}
	}

	if err := exec.Run("gcloud config unset api_endpoint_overrides/gkehub"); err != nil {
		return fmt.Errorf("error unsetting gke hub endpoint: %w", err)
	}

	return nil
}

func (d *Instance) flags() ([]string, error) {
	// The name of the deployer is first in the list.
	flags := []string{d.Name()}

	// Add presubmit-related tailorbird-specific flags.
	// Added in the beginning to ensure lower precedence.
	if os.Getenv("JOB_TYPE") == "presubmit" {
		flags = append(flags, "--extra-cluster-labels=testType=presubmit")
	}

	// Get the base flags from the options.
	cfgFlags, err := d.cfg.GetDeployerFlags()
	if err != nil {
		return nil, err
	}
	flags = append(flags, cfgFlags...)

	// Append the base flags for the tailorbird deployer.
	flags = append(flags, baseFlags...)

	// Append the config file flag.
	fp, err := d.rookeryFile()
	if err != nil {
		return nil, fmt.Errorf("error getting the config file for tailorbird: %w", err)
	}
	flags = append(flags, "--tbconfig="+fp)

	// Append the filename of the generated configuration for a request
	// This file is required during upgrade
	flags = append(flags, "--generated-request-file="+d.cfg.RookeryRequestFile)

	// Append the test script.
	flags = append(flags, "--test=exec", "--", d.cfg.TestScript)

	// Append the test flags.
	testerFlags, err := d.cfg.GetTesterFlags()
	if err != nil {
		return nil, err
	}
	flags = append(flags, testerFlags...)

	return flags, nil
}

// InstallTools installs the required tools to enable interacting with Tailorbird.
func (d *Instance) installTools() error {
	log.Println("Installing tools required by tailorbird deployer")

	// Install kubetest2-tailorbird.
	ktPath := "/usr/local/bin/kubetest2-tailorbird"
	if err := exec.RunMultiple([]string{
		fmt.Sprintf("gsutil cp %s %s", kubetest2TailorbirdPath, ktPath),
		"chmod 755 " + ktPath,
	}); err != nil {
		return fmt.Errorf("error installing kubetest2-tailorbird: %w", err)
	}

	// Install gke-gcloud-auth-plugin
	if err := exec.Run("gcloud components install gke-gcloud-auth-plugin --quiet"); err != nil {
		return fmt.Errorf("error installing gke-gcloud-auth-plugin: %w", err)
	}

	// GKE-on-AWS needs terraform for generation of kubeconfigs
	// TODO(chizhg): remove the terraform installation after b/171729099 is solved.
	if d.cfg.Cluster == types.GKEOnAWS {
		if err := exec.Run("bash -c 'apt-get update && apt-get install unzip -y'"); err != nil {
			return err
		}
		installTerraformCmd := fmt.Sprintf(`wget --no-verbose https://releases.hashicorp.com/terraform/%s/terraform_%s_linux_amd64.zip \
		&& unzip terraform_%s_linux_amd64.zip \
		&& mv terraform /usr/local/bin/terraform \
		&& rm terraform_%s_linux_amd64.zip`, terraformVersion, terraformVersion, terraformVersion, terraformVersion)
		if err := exec.Run("bash -c '" + installTerraformCmd + "'"); err != nil {
			return fmt.Errorf("error installing terraform for testing with aws")
		}
	}

	if d.cfg.Cluster == types.GKEOnEKS || d.cfg.Cluster == types.HybridGKEAndEKS {
		if err := exec.Run("bash -c '" + installawsIamAuthenticatorCmd + "'"); err != nil {
			return fmt.Errorf("error installing aws-iam-authenticator for testing with eks")
		}
	}

	return nil
}

func (d *Instance) getGCSBucket() string {
	bucket := d.cfg.GCSBucket
	if bucket == "" {
		// Use a platform-specific default.
		switch d.cfg.Cluster {
		case types.GKEOnAWS:
			return "gke-multi-cloud-staging"
		case types.GKEOnGCPWithAnthosPrivateMode:
			return "anthos-private-mode-staging"
		}
	}

	return bucket
}

func (d *Instance) getVersionAndPrefix() (version string, versionPrefix string) {
	version = d.cfg.ClusterVersion
	if version == "" {
		// Apply a platform-specific default.
		// TODO(nmittler): Can these all just be "latest"?
		switch d.cfg.Cluster {
		case types.GKEOnPrem:
			version = "latest"
		case types.GKEOnBareMetal:
			version = "latest"
			versionPrefix = "0.0"
		case types.GKEOnAWS:
			version = "aws-1.7.1-gke.1"
		case types.GKEOnGCPWithAnthosPrivateMode:
			version = "1.8.0-pre.1"
		case types.GKEOnGCP:
			version = "latest"
		default:
			version = "latest"
		}
	}

	if len(strings.Split(version, ".")) == 2 {
		// Only major.minor was specified. Will try to leverage support for using the latest
		// patch release for each platform.

		// Deal with configuration differences between various platforms.
		switch d.cfg.Cluster {
		case types.GKEOnPrem:
			versionPrefix = version
			version = ""
		case types.GKEOnBareMetal, types.GKEOnAWS:
			versionPrefix = version
			version = "latest"
		case types.HybridGKEAndGKEOnBareMetal:
			versionPrefix = version
			version = "latest"
		}
	}
	return
}

func (d *Instance) getReleaseChannel() types.ReleaseChannel {
	if d.cfg.ReleaseChannel != "" {
		return d.cfg.ReleaseChannel
	}
	return types.None
}

func (d *Instance) getEnvironment() types.Environment {
	if d.cfg.Environment != "" {
		return d.cfg.Environment
	}
	// default env is Prod
	return types.Prod
}

func (d *Instance) applySingleClusterParameters(template *TemplateParameters) {
	template.ClusterNames = []string{"prow-test"}
	template.BoskosProjectsRequested = []int{1}
	template.BoskosResourceType = []string{commonBoskosResource}

	// Testing with VPC-SC requires a different project type.
	if d.cfg.Features.Has(string(types.VPCServiceControls)) {
		template.BoskosResourceType = []string{vpcSCBoskosResource}
	}

	if d.cfg.Features.Has(string(types.PrivateClusterUnrestrictedAccess)) {
		template.PrivateClusterAccessLevel = "unrestricted"
		template.PrivateClusterMasterIPRanges = "172.16.0.32/28,173.16.0.32/28,174.16.0.32/28"
	} else if d.cfg.Features.Has(string(types.PrivateClusterLimitedAccess)) {
		template.PrivateClusterAccessLevel = "limited"
		template.PrivateClusterMasterIPRanges = "172.16.0.32/28,173.16.0.32/28,174.16.0.32/28"
	} else if d.cfg.Features.Has(string(types.PrivateClusterNoAccess)) {
		template.PrivateClusterAccessLevel = "no"
		template.PrivateClusterMasterIPRanges = "172.16.0.32/28,173.16.0.32/28,174.16.0.32/28"
	}
}

func (d *Instance) applyMultiClusterParameters(template *TemplateParameters) {
	template.ClusterNames = []string{"prow-test1", "prow-test2"}
	template.PrimaryID = fmt.Sprintf("cluster-id-%s", uuid.New().String())
	template.BoskosProjectsRequested = []int{1}
	template.BoskosResourceType = []string{commonBoskosResource}

	// Testing with VPC-SC requires a different project type.
	if d.cfg.Features.Has(string(types.VPCServiceControls)) {
		template.BoskosResourceType = []string{vpcSCBoskosResource}
	}
}

func (d *Instance) applyMultiProjectMultiClusterParameters(template *TemplateParameters) {
	template.ClusterNames = []string{"prow-test1:1", "prow-test2:2"}
	template.PrimaryID = fmt.Sprintf("cluster-id-%s", uuid.New().String())
	template.BoskosProjectsRequested = []int{1, 2}
	template.BoskosResourceType = []string{sharedVPCHostBoskosResource, sharedVPCSVCBoskosResource}

	// Testing with VPC-SC requires a different project type.
	if d.cfg.Features.Has(string(types.VPCServiceControls)) {
		template.BoskosResourceType = []string{vpcSCSharedVPCHostBoskosResource, vpcSCSharedVPCSVCBoskosResource}
	}

	template.SubnetworkRanges = "172.16.4.0/22 172.16.16.0/20 172.20.0.0/14," +
		"10.0.4.0/22 10.0.32.0/20 10.4.0.0/14,173.16.4.0/22 173.16.16.0/20 173.20.0.0/14," +
		"11.0.4.0/22 11.0.32.0/20 11.4.0.0/14,174.16.4.0/22 174.16.16.0/20 174.20.0.0/14," +
		"12.0.4.0/22 12.0.32.0/20 12.4.0.0/14"
}

func (d *Instance) getGkeTopologyParameters(template *TemplateParameters) error {
	template.ErrorPatterns = retryableErrorPatterns
	template.NetworkName = networkName
	template.ReleaseChannel = d.getReleaseChannel()
	template.Environment = d.getEnvironment()
	template.GcloudExtraFlags = d.cfg.GcloudExtraFlags
	template.GcloudCommandGroup = "beta"
	template.WorkloadIdentityEnabled = true
	template.IsAutoPilot = false
	// Only acquire the GCP project from Boskos if it's running in CI.
	template.IsBoskosProjectRequired = common.IsRunningOnCI()

	if len(d.cfg.GCPProjects) > 0 {
		template.ProjectName = d.cfg.GCPProjects[0]
	}

	if d.cfg.Features.Has(string(types.ClusterWIOnly)) {
		// Setting it false doesn't mean GKE Workload Identity won't be used.
		// The cluster will be created without GKE WI but asmcli will update the cluster.
		// This is to test ASM is still functional on clusters without the **node** GKE WI.
		template.WorkloadIdentityEnabled = false
	}

	if d.cfg.Features.Has(string(types.Autopilot)) {
		template.IsAutoPilot = true
	} else {
		template.GcloudExtraFlags += " --enable-network-policy"
	}

	if d.cfg.Features.Has(string(types.Addon)) {
		template.GcloudExtraFlags += " --addons=Istio"
	}

	var err error
	for f := range d.cfg.Features {
		feat := types.Feature(f)
		switch feat {
		case types.VPCServiceControls:
			err = featureVPCSCParameters(d.cfg.Topology, template)
		case types.UserAuth:
		case types.Addon:
		case types.PrivateClusterUnrestrictedAccess:
		case types.PrivateClusterLimitedAccess:
		case types.PrivateClusterNoAccess:
		case types.ContainerNetworkInterface:
		case types.ClusterWIOnly:
		case types.Autopilot:
		case types.CasCertTemplate:
		case types.PolicyConstraint:
		case types.CompositeGateway:
		case types.CAMigration:
		default:
			err = fmt.Errorf("feature %q is not supported", feat)
		}
	}
	if err != nil {
		return err
	}
	switch d.cfg.Topology {
	case types.SingleCluster:
		d.applySingleClusterParameters(template)
	case types.MultiCluster:
		d.applyMultiClusterParameters(template)
	case types.MultiProject:
		d.applyMultiProjectMultiClusterParameters(template)
	default:
		return fmt.Errorf("cluster topology %q is not supported", d.cfg.Topology)
	}
	return nil
}

// featureVPCSCParameters returns the extra parameters for creating the clusters
func featureVPCSCParameters(topology types.Topology, template *TemplateParameters) error {
	template.PrivateClusterAccessLevel = "limited"

	switch topology {
	case types.SingleCluster:
		template.PrivateClusterMasterIPRanges = "172.16.0.32/28,173.16.0.32/28,174.16.0.32/28"
	case types.MultiCluster, types.MultiProject:
		template.PrivateClusterMasterIPRanges = "173.16.0.32/28,172.16.0.32/28,174.16.0.32/28,175.16.0.32/28,176.16.0.32/28,177.16.0.32/28"
	default:
		return fmt.Errorf("topology %v for VPCSC is unsupported", topology)
	}
	return nil
}

// rookeryFile returns the full path for the rookery config file.
func (d *Instance) rookeryFile() (string, error) {
	// Use the TRAC-generated rookery file, if specified via config
	if d.cfg.TRACPlatformIndex >= 0 || d.cfg.TRACComponentIndex >= 0 {
		return d.tracRookeryPath()
	}

	// Get the path to the rookery template file and verify it exists.
	tmplFileName := fmt.Sprintf("%s-%s", d.cfg.Cluster, d.cfg.Topology)
	if d.cfg.WIP == types.HUB {
		tmplFileName = fmt.Sprintf("%s-%s-%s", d.cfg.Cluster, strings.ToLower(string(d.cfg.WIP)), d.cfg.Topology)
	}
	if d.cfg.UseOnePlatform {
		tmplFileName = fmt.Sprintf("%s-%s", tmplFileName, "oneplatform")
	}
	if d.cfg.UseKubevirtVM {
		tmplFileName = fmt.Sprintf("%s-%s", tmplFileName, "kubevirt-vm")
	}
	tmplFileName = fmt.Sprintf("%s.%s", tmplFileName, "yaml")
	tmplFile := filepath.Join(d.cfg.RepoRootDir, configRelDir, tmplFileName)
	if _, err := os.Stat(tmplFile); err != nil {
		return "", fmt.Errorf("tailorbird rookery template %q does not exist in %q", tmplFile, configRelDir)
	}

	// Create the template from the template file.
	tmpl, err := template.New(path.Base(tmplFile)).ParseFiles(tmplFile)
	if err != nil {
		return "", fmt.Errorf("error parsing the Tailorbird rookery template file %s: %w", tmplFile, err)
	}

	// Create a temp file to hold the result of the template execution.
	tmpFile, err := ioutil.TempFile("", "tailorbird-config-*.yaml")
	if err != nil {
		return "", fmt.Errorf("error creating the temporary Tailorbird rookery file: %w", err)
	}

	// Struct providing template parameters for the YAML.
	version, versionPrefix := d.getVersionAndPrefix()
	rep := TemplateParameters{
		GCSBucket:     d.getGCSBucket(),
		Version:       version,
		VersionPrefix: versionPrefix,
	}

	if d.cfg.Cluster == types.GKEOnGCP {
		if err := d.getGkeTopologyParameters(&rep); err != nil {
			return "", fmt.Errorf("error getting GKE parameters values: %w", err)
		}
	}
	// Execute the template and store the result in the temp file.
	if err := tmpl.Execute(tmpFile, rep); err != nil {
		return "", fmt.Errorf("error executing the Tailorbird rookery template: %w", err)
	}

	// Create a temp file to store the generated configuration for a request.
	tmpRequestFile, err := ioutil.TempFile("", "tailorbird-request-*.yaml")
	if err != nil {
		return "", fmt.Errorf("error creating the temporary Tailorbird rookery request file: %w", err)
	}
	d.cfg.RookeryRequestFile = tmpRequestFile.Name()

	d.cfg.Rookery = tmpFile.Name()

	return tmpFile.Name(), nil
}

// tracRookeryFile returns the path to the specified Platform x Component Rookery file.
// These files are auto-generated by go/anthos-trac and mirrored into a separate GoB repo for use here.
func (d *Instance) tracRookeryPath() (string, error) {
	if (d.cfg.TRACPlatformIndex >= 0) != (d.cfg.TRACComponentIndex >= 0) {
		return "", fmt.Errorf("must specify both (or neither) TRAC platform index (%d) and component index (%d)",
			d.cfg.TRACPlatformIndex, d.cfg.TRACComponentIndex)
	}

	genFolderName := fmt.Sprintf("gen-%d", d.cfg.TRACComponentIndex)
	platform := platformName(string(d.cfg.Cluster))
	variant := string(d.cfg.Topology)
	if d.cfg.WIP == types.HUB {
		variant = strings.ToLower(string(d.cfg.WIP)) + "-" + string(d.cfg.Topology)
	}

	// Pattern: /$pathToConfigs/gen-$componentIndex/$platform-$variant-$platformIndex.yaml
	rookeryFileName := fmt.Sprintf("%s-%s-%d.yaml", platform, variant, d.cfg.TRACPlatformIndex)
	f := filepath.Join(d.cfg.RepoRootDir, tracConfigRelDir, genFolderName, rookeryFileName)
	if _, err := os.Stat(f); err != nil {
		return "", fmt.Errorf("tailorbird rookery config file %q does not exist in TRAC, "+
			"please follow go/trac-guide to generate the config file correctly", f)
	}
	return f, nil
}

func platformName(cluster string) string {
	platform := cluster
	switch types.Cluster(cluster) {
	case types.GKEOnPrem:
		platform = "gke-on-vmware"
	case types.GKEOnBareMetal:
		platform = "gke-on-baremetal"
	case types.GKEOnAWS:
		platform = "gke-on-aws"
	case types.GKEOnEKS:
		platform = "gke-on-eks"
	case types.GKEOnGCP:
		platform = "gke-on-gcp"
	}
	return platform
}

func (d *Instance) newGkeUpgradeHandler() (func(http.ResponseWriter, *http.Request), error) {

	upgradeFunc := func(w http.ResponseWriter, _ *http.Request) {

		tbFile, err := ioutil.ReadFile(d.cfg.RookeryRequestFile)
		if err != nil {
			log.Printf("error: %+v, couldn't read file %s", err.Error(), d.cfg.RookeryRequestFile)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		var rook Rookery

		if err = yaml.Unmarshal(tbFile, &rook); err != nil {
			log.Printf("error: %+v, unable to unmarshall file %s", err.Error(), tbFile)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		for _, knest := range rook.Spec.Knests {
			for _, cluster := range knest.Spec.Clusters {
				log.Printf("cluster to upgrade: %s", cluster.Metadata.Name)
				for _, version := range d.cfg.UpgradeClusterVersion {
					upgradeCmd := fmt.Sprintf("kubetest2-tailorbird --up "+
						"--verbose --upgrade-cluster --upgrade-cluster-name %s "+
						"--upgrade-target-k8s-version %s --upgrade-resource-config %s",
						cluster.Metadata.Name, version, d.cfg.RookeryRequestFile)

					if err := exec.Run(upgradeCmd); err != nil {
						log.Printf("error: %+v, while upgrading the cluster to version %s", err.Error(), version)
						http.Error(w, err.Error(), http.StatusInternalServerError)
						return
					}

					if d.cfg.SyncUpgrade {
						if err := d.waitForUpgradeToFinish(cluster.Metadata.Name); err != nil {
							log.Printf("error: %+v, fail to get upgrade status", err.Error())
							http.Error(w, err.Error(), http.StatusInternalServerError)
							return
						} else {
							log.Printf("Cluster upgraded successfully!")
						}
					}
				}
			}
		}
		w.WriteHeader(http.StatusOK)
	}

	return upgradeFunc, nil
}

func (d *Instance) supportedHandlers() map[string]func() (func(http.ResponseWriter, *http.Request), error) {
	supportedHandler := map[string]func() (func(http.ResponseWriter, *http.Request), error){}
	if len(d.cfg.UpgradeClusterVersion) != 0 {
		supportedHandler[common.UpgradePath] = d.newGkeUpgradeHandler
	}
	return supportedHandler
}

// waitForUpgradeToFinish waits for the cluster upgrade process to finish.
// Current maximum waiting time is 70 minutes (polling every 30s)
func (d *Instance) waitForUpgradeToFinish(clusterName string) error {
	statusCheckAttempt := 0
	clusterReady := false
	var err error
	for !clusterReady {
		statusCheckAttempt++
		log.Printf("Waiting for the cluster upgrade status to become Succeeded...")
		time.Sleep(time.Duration(statusCheckInterval) * time.Second)
		clusterReady, err = d.IsUpgradeDone(clusterName)
		if err != nil {
			return err
		}

		if clusterReady || (statusCheckAttempt >= statusCheckMaxRetry) {
			break
		}
	}

	return nil
}

// IsUpgradeDone returns true if the upgrade process finished successfully.
// Otherwise, returns false (if status is Pending or Failed). If failed, an
// error is also returned.
func (d *Instance) IsUpgradeDone(clusterName string) (bool, error) {
	s, err := d.getUpgradeStatus(clusterName)
	if err != nil {
		return false, err
	}

	switch s {
	case types.Succeeded:
		return true, nil
	case types.Failed:
		return false, fmt.Errorf("Tailorbird upgrade status is in Failed state.")
	default:
		return false, nil
	}
}

// getUpgradeStatus returns the cluster upgrade process status by calling
// kubetest2-tailorbird
func (d *Instance) getUpgradeStatus(clusterName string) (types.Type, error) {
	workdir := filepath.Dir(d.cfg.RookeryRequestFile)
	upgradeConfigFile := filepath.Join(workdir, fmt.Sprintf("%s-upgrade.yaml", clusterName))
	upgradeStatusCmd := fmt.Sprintf("kubetest2-tailorbird --up "+
		"--verbose --upgrade-cluster --get-upgrade-status "+
		"--upgrade-cluster-name %s --upgrade-resource-config %s --tbconfig %s",
		clusterName, d.cfg.RookeryRequestFile, upgradeConfigFile)

	output, err := exec.CombinedOutput(upgradeStatusCmd)
	if err != nil {
		return types.Failed, err
	}

	// Output format is: [UPGRADE_STATUS]: <cluster name>=<status>
	regex := regexp.MustCompile(`\[UPGRADE_STATUS\]\: .+\=(.+)`)
	matches := regex.FindStringSubmatch(string(output))
	if len(matches) != 2 {
		return types.Failed, fmt.Errorf("fail to find the upgrade status")
	}
	return types.Type(matches[1]), nil
}
