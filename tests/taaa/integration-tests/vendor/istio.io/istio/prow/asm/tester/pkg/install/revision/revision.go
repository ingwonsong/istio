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

package revision

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"istio.io/istio/prow/asm/tester/pkg/exec"

	"github.com/hashicorp/go-multierror"
	"gopkg.in/yaml.v2"
)

// Configs carries the Config for all ASM control plane revisions.
// Revision configuration files are unmarshalled into this struct.
type Configs struct {
	Configs []Config `yaml:"revisions"`
}

// Config carries config for an ASM control plane revision.
// Tests that require multiple revisions with different configurations or
// versions may use this to configure their SUT.
type Config struct {
	// Name determines the revision's name and injection label.
	Name string `yaml:"name"`
	// Version is the ASM version to use for this revision. Expected in the form "1.x",
	// as we only support installing the latest 1.x minor version releases.
	Version string `yaml:"version"`
	// CA is the CA to use for this revision, either `CITADEL` or `MESHCA`.
	// Defaults to CITADEL.
	CA string `yaml:"ca"`
	// CustomCerts determines whether we should specify the CA cert, key, and chain to the installer
	CustomCerts bool
	// Overlay is a path to additional configuration for this revision.
	Overlay string `yaml:"overlay"`
}

func ParseConfig(path string) (*Configs, error) {
	yamlContents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read revision config file %q: %w",
			path, err)
	}
	configs := new(Configs)
	err = yaml.Unmarshal(yamlContents, configs)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal revision config from file %q: %w",
			path, err)
	}

	var errs error
	if len(configs.Configs) == 0 {
		errs = fmt.Errorf("revision config must have at least 1 revision specified")
	}
	for _, config := range configs.Configs {
		if err := config.Validate(); err != nil {
			errs = multierror.Append(errs, err).ErrorOrNil()
		}
	}
	if errs != nil {
		return nil, fmt.Errorf("failed to validate revision config: %w", errs)
	}

	return configs, nil
}

func (c *Config) Validate() error {
	const majorMinorRegex = "[0-9]+\\.[0-9]+"
	if c.Version != "" {
		match, err := regexp.MatchString(majorMinorRegex, c.Version)
		if err != nil {
			return err
		}
		if !match {
			return fmt.Errorf("%s could not be parsed as version", c.Version)
		}
	}

	if c.Name == "" {
		return fmt.Errorf("revision must have name")
	}

	return nil
}

// RevisionLabel generates a revision label name from the istioctl version.
func RevisionLabel() string {
	istioVersion, _ := exec.RunWithOutput(
		"bash -c \"istioctl version --remote=false -o json | jq -r '.clientVersion.tag'\"")
	versionParts := strings.Split(istioVersion, "-")
	version := fmt.Sprintf("asm-%s-%s", versionParts[0], versionParts[1])
	r := strings.NewReplacer(".", "", "\n", "")
	return r.Replace(version)
}
