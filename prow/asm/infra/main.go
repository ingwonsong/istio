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

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"

	"istio.io/istio/prow/asm/infra/config"
	"istio.io/istio/prow/asm/infra/deployer"
	"istio.io/istio/prow/asm/infra/deployer/common"
	"istio.io/istio/prow/asm/infra/exec"
	"istio.io/istio/prow/asm/infra/types"
)

func main() {
	cfg := config.Default()

	flag.StringVar(&cfg.RepoRootDir, "repo-root-dir", cfg.RepoRootDir,
		"the repo's root directory (required). Used as the working directory for running the kubetest2 command")
	flag.StringVar(&cfg.ExtraDeployerFlags, "deployer-flags", cfg.ExtraDeployerFlags,
		"extra flags corresponding to the deployer being used (optional). Supported flags can be"+
			" checked by running `kubetest2 [deployer] --help`")
	flag.StringVar(&cfg.GcloudExtraFlags, "gcloud-extra-flags", cfg.GcloudExtraFlags, "Extra gcloud flags to pass when creating the clusters.")
	flag.StringVar(&cfg.TestScript, "test-script", cfg.TestScript,
		"the script to run the tests after clusters are created (required for tailorbird)")
	flag.StringVar(&cfg.TestFlags, "test-flags", cfg.TestFlags,
		"flags to pass through to the test script (optional)")
	flag.StringVar((*string)(&cfg.ReleaseChannel), "release-channel", string(cfg.ReleaseChannel),
		fmt.Sprintf("the GKE release channel to be used when creating clusters. Can be one of %v. "+
			"If not specified, a default release channel will be chosen for the cluster-version",
			types.SupportedReleaseChannels))
	flag.StringVar(&cfg.ClusterVersion, "cluster-version", cfg.ClusterVersion,
		"the GKE version to be loaded onto the clusters (optional). Defaults to `latest`")
	flag.StringVar(&cfg.UpgradeClusterVersion, "upgrade-cluster-version", cfg.UpgradeClusterVersion,
		"GKE version that cluster will be upgraded to, formatted as x.y1.z. Clusters will run for a short duration to ensure functionality after the cluster upgrade.")
	flag.StringVar((*string)(&cfg.Cluster), "cluster-type", string(cfg.Cluster),
		fmt.Sprintf("the cluster type, can be one of %v", types.SupportedClusters))
	flag.StringVar((*string)(&cfg.Topology), "topology", string(cfg.Topology),
		fmt.Sprintf("the cluster topology for the SUT (optional). Can be one of %v", types.SupportedTopologies))
	flag.StringVar((*string)(&cfg.WIP), "wip", string(cfg.WIP),
		fmt.Sprintf("Workload Identity Pool, can be one of %v", types.SupportedWIPs))
	flag.StringVar((*string)(&cfg.Feature), "feature", string(cfg.Feature),
		fmt.Sprintf("the feature to test for ASM (optional). Can be one of %v", types.SupportedFeatures))
	flag.StringVar(&cfg.GCSBucket, "gcs-bucket", cfg.GCSBucket,
		"the GCS bucket to be used for the platform (optional). Supported values vary per platform")
	flag.BoolVar(&cfg.IsCloudESFTest, "is-cloudesf-test", cfg.IsCloudESFTest, "whether it is the test using CloudESF as ingress gateway")
	flag.Parse()

	// Special case for testing the Addon.
	if cfg.Feature == types.Addon {
		// We only support clusters that have EnsureExists, currently available on rapid only
		cfg.ReleaseChannel = types.Rapid
		cfg.ClusterVersion = "latest"
	}

	if cfg.IsCloudESFTest {
		cfg.TestFlags = cfg.TestFlags + " --install-cloudesf"
	}

	if err := runTestFlow(cfg); err != nil {
		log.Fatal(err)
	}
}

func runTestFlow(cfg config.Instance) error {
	defer postprocessTestArtifacts()

	d := deployer.New(cfg)
	if err := d.Run(); err != nil {
		return fmt.Errorf("error running the test flow: %w", err)
	}
	return nil
}

// postprocessTestArtifacts will process the test artifacts after the test flow
// is finished.
func postprocessTestArtifacts() {
	if common.IsRunningOnCI() {
		log.Println("Postprocessing JUnit XML files to support aggregated view on Testgrid...")
		_ = exec.Run("git config --global http.cookiefile /secrets/cookiefile/cookies")
		clonePath := os.Getenv("GOPATH") + "/src/gke-internal/knative/cloudrun-test-infra"
		if _, err := os.Stat(clonePath); os.IsNotExist(err) {
			// Clone the repo if the path does not exist.
			_ = exec.Run(fmt.Sprintf("git clone --single-branch --branch main https://gke-internal.googlesource.com/knative/cloudrun-test-infra %s", clonePath))
		}
		_ = exec.Run(fmt.Sprintf("bash -c 'cd %s && go install ./tools/crtest/cmd/crtest'", clonePath))

		_ = filepath.Walk(os.Getenv("ARTIFACTS"), func(path string, info os.FileInfo, err error) error {
			if matched, _ := regexp.MatchString(`^junit.*\.xml`, info.Name()); matched {
				log.Printf("Update file %q", path)
				_ = exec.Run(fmt.Sprintf("crtest xmlpost --file=%s --save --aggregate-subtests", path))
			}
			return nil
		})
	}
}
