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
	"math/rand"
	"os"
	"path"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"istio.io/istio/prow/asm/infra/config"
	"istio.io/istio/prow/asm/infra/exec"
	"istio.io/istio/prow/asm/infra/types"
)

const (
	name = "tailorbird"

	// the relative dir to find the tailorbird config files
	configRelDir = "deployer/tailorbird/config"

	installawsIamAuthenticatorCmd = `curl -o aws-iam-authenticator https://amazon-eks.s3.us-west-2.amazonaws.com/1.19.6/2021-01-05/bin/linux/amd64/aws-iam-authenticator \
			&& chmod +x ./aws-iam-authenticator \
			&& mv ./aws-iam-authenticator /usr/local/bin/aws-iam-authenticator`

	terraformVersion = "0.13.6"
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

func (d *Instance) Name() string {
	return name
}

func (d *Instance) Run() error {
	log.Println("Will run kubetest2 tailorbird deployer to create the clusters...")

	if err := d.installTools(); err != nil {
		return fmt.Errorf("error installing tools for testing with Tailorbird: %w", err)
	}

	flags, err := d.flags()
	if err != nil {
		return err
	}

	// Run the deployer
	cmd := fmt.Sprintf("kubetest2 %s", strings.Join(flags, " "))
	return exec.Run(cmd, exec.WithWorkingDir(d.cfg.RepoRootDir))
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
	fp, err := d.generateRookeryFile()
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

	clonePath := os.Getenv("GOPATH") + "/src/gke-internal/test-infra"
	if _, err := os.Stat(clonePath); !os.IsNotExist(err) {
		if err := exec.Run(fmt.Sprintf("bash -c 'cd %s && go install ./anthos/tailorbird/cmd/kubetest2-tailorbird'", clonePath)); err != nil {
			return fmt.Errorf("error installing kubetest2 tailorbird deployer: %w", err)
		}
	} else {
		return fmt.Errorf("path %q does not seem to exist, please double check", clonePath)
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

	if d.cfg.Cluster == types.GKEOnEKS {
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
			version = "1.7"
		case types.GKEOnAWS:
			version = "aws-1.7.1-gke.1"
		case types.GKEOnGCPWithAnthosPrivateMode:
			version = "1.8.0-pre.1"
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
		}
	}
	return
}

// generateRookeryFile generates the rookery file for the configuration and returns the full path to the generated file.
func (d *Instance) generateRookeryFile() (string, error) {
	// Get the path to the rookery template file and verify it exists.
	tmplFileName := fmt.Sprintf("%s-%s.yaml", d.cfg.Cluster, d.cfg.Topology)
	if d.cfg.WIP == types.HUB {
		tmplFileName = fmt.Sprintf("%s-%s-%s.yaml", d.cfg.Cluster, strings.ToLower(string(d.cfg.WIP)), d.cfg.Topology)
	}
	tmplFile := filepath.Join(configRelDir, tmplFileName)
	if _, err := os.Stat(tmplFile); err != nil {
		return "", fmt.Errorf("rookery template %q does not exist in %q", tmplFile, configRelDir)
	}

	// Create the template from the template file.
	tmpl, err := template.New(path.Base(tmplFile)).ParseFiles(tmplFile)
	if err != nil {
		return "", fmt.Errorf("error parsing the rookery template file %s: %w", tmplFile, err)
	}

	// Create a temp file to hold the result of the template execution.
	tmpFile, err := ioutil.TempFile("", "tailorbird-config-*.yaml")
	if err != nil {
		return "", fmt.Errorf("error creating the temporary rookery file: %w", err)
	}

	// Struct providing template parameters for the YAML.
	version, versionPrefix := d.getVersionAndPrefix()
	rep := struct {
		GCSBucket     string
		Version       string
		VersionPrefix string
		Random6       string
	}{
		GCSBucket:     d.getGCSBucket(),
		Version:       version,
		VersionPrefix: versionPrefix,
		Random6:       randSeq(6),
	}

	// Execute the template and store the result in the temp file.
	if err := tmpl.Execute(tmpFile, rep); err != nil {
		return "", fmt.Errorf("error executing the rookery template: %w", err)
	}

	return tmpFile.Name(), nil
}

var letters = []rune("abcdefghijklmnopqrstuvwxyz")

func randSeq(n int) string {
	rand.Seed(time.Now().UnixNano())
	b := make([]rune, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}
