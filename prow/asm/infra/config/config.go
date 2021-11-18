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

package config

import (
	"fmt"
	"net"
	"strings"

	shell "github.com/kballard/go-shellquote"
	"k8s.io/apimachinery/pkg/util/sets"

	"istio.io/istio/prow/asm/infra/types"
)

var (
	baseDeployerFlags = []string{
		"--up",
		"--skip-test-junit-report",
	}
	baseTesterFlags = []string{
		"--setup-env",
		"--teardown-env",
		"--setup-system",
		"--teardown-system",
		"--setup-tests",
		"--teardown-tests",
		"--run-tests",
	}
)

// Instance of a deployer configuration.
type Instance struct {
	RepoRootDir           string
	ExtraDeployerFlags    string
	GcloudExtraFlags      string
	TestScript            string
	TestFlags             string
	GCPProjects           []string
	ClusterVersion        string
	TRACPlatformIndex     int
	TRACComponentIndex    int
	Cluster               types.Cluster
	UseOnePlatform        bool
	UpgradeClusterVersion []string
	GCSBucket             string
	IsCloudESFTest        bool
	Topology              types.Topology
	WIP                   types.WIP
	ReleaseChannel        types.ReleaseChannel
	Features              sets.String
	Rookery               string
}

// Default provides a config Instance with defaults filled in.
func Default() Instance {
	return Instance{
		Cluster:            types.GKEOnGCP,
		Topology:           types.SingleCluster,
		WIP:                types.GKE,
		TRACPlatformIndex:  -1,
		TRACComponentIndex: -1,
	}
}

func (c Instance) GetDeployerFlags() ([]string, error) {
	var extraDeployerFlagArr []string
	var err error
	if c.ExtraDeployerFlags != "" {
		extraDeployerFlagArr, err = shell.Split(c.ExtraDeployerFlags)
		if err != nil {
			return nil, fmt.Errorf("error parsing the deployer flags %q: %v", c.ExtraDeployerFlags, err)
		}
	}

	return append(baseDeployerFlags, extraDeployerFlagArr...), nil
}

func (c Instance) GetTesterFlags() ([]string, error) {
	var extraTestFlagArr []string
	var err error
	if c.TestFlags != "" {
		extraTestFlagArr, err = shell.Split(c.TestFlags)
		if err != nil {
			return nil, fmt.Errorf("error parsing the test flags %q: %v", c.TestFlags, err)
		}
	}

	testerFlags := append(baseTesterFlags, "--repo-root-dir="+c.RepoRootDir)
	testerFlags = append(testerFlags,
		"--gcp-projects="+strings.Join(c.GCPProjects, ","),
		"--cluster-type="+string(c.Cluster),
		"--cluster-topology="+string(c.Topology),
		"--wip="+string(c.WIP),
		"--feature="+strings.Join(c.Features.List(), ","))
	if c.UseOnePlatform {
		testerFlags = append(testerFlags, "--use-oneplatform")
	}
	return append(testerFlags, extraTestFlagArr...), nil
}

// GetWebServerFlags contains flags needed to setup the infra webserver and to call it from the Test suite
func (c Instance) GetWebServerFlags(lis net.Listener) []string {
	return []string{fmt.Sprintf("--test-start-event-port=%d", lis.Addr().(*net.TCPAddr).Port)}
}
