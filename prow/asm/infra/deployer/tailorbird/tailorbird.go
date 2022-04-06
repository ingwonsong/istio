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
	"strings"
	"text/template"

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
	kubetest2TailorbirdPath = "gs://tailorbird-artifacts/staging/kubetest2-tailorbird/2022-01-13-230609/kubetest2-tailorbird"

	installawsIamAuthenticatorCmd = `curl -o aws-iam-authenticator https://amazon-eks.s3.us-west-2.amazonaws.com/1.19.6/2021-01-05/bin/linux/amd64/aws-iam-authenticator \
			&& chmod +x ./aws-iam-authenticator \
			&& mv ./aws-iam-authenticator /usr/local/bin/aws-iam-authenticator`

	terraformVersion = "0.13.6"

	// the host HUB project for testing with on-prem
	onPremHubDevProject = "tairan-asm-multi-cloud-dev"
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
	rep := struct {
		GCSBucket     string
		Version       string
		VersionPrefix string
	}{
		GCSBucket:     d.getGCSBucket(),
		Version:       version,
		VersionPrefix: versionPrefix,
	}

	// Execute the template and store the result in the temp file.
	if err := tmpl.Execute(tmpFile, rep); err != nil {
		return "", fmt.Errorf("error executing the Tailorbird rookery template: %w", err)
	}

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
	}
	return platform
}

func (d *Instance) newGkeUpgradeHandler() (func(http.ResponseWriter, *http.Request), error) {

	var upgradeCmds []string

	tbFile, err := ioutil.ReadFile(d.cfg.Rookery)
	if err != nil {
		return nil, fmt.Errorf("couldn't read file %s: %v", d.cfg.Rookery, err)
	}

	var rook Rookery

	if err = yaml.Unmarshal(tbFile, &rook); err != nil {
		return nil, fmt.Errorf("unable to unmarshall file %s: %v", tbFile, err)
	}

	for _, knest := range rook.Spec.Knests {
		for _, cluster := range knest.Spec.Clusters {
			log.Printf("cluster to upgrade: %s", cluster.Metadata.Name)
			for version := range d.cfg.UpgradeClusterVersion {
				upgradeCmds = append(upgradeCmds, fmt.Sprintf(`kubetest2-tailorbird --up --upgrade-cluster --upgrade-cluster-name %s --upgrade-target-platform-version %d`,
					cluster.Metadata.Name, version))
			}
		}
	}

	upgradeFunc := func(w http.ResponseWriter, _ *http.Request) {

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
	if len(d.cfg.UpgradeClusterVersion) != 0 {
		supportedHandler[common.UpgradePath] = d.newGkeUpgradeHandler
	}
	return supportedHandler
}
