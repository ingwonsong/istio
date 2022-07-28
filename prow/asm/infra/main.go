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
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	flag "github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/util/sets"

	"istio.io/istio/prow/asm/infra/config"
	"istio.io/istio/prow/asm/infra/deployer"
	"istio.io/istio/prow/asm/infra/deployer/common"
	"istio.io/istio/prow/asm/infra/exec"
	"istio.io/istio/prow/asm/infra/types"
)

func main() {
	cfg := config.Default()

	features := []string{}
	upgradeVersions := []string{}
	flag.StringVar(&cfg.RepoRootDir, "repo-root-dir", cfg.RepoRootDir,
		"the repo's root directory (required). Used as the working directory for running the kubetest2 command")
	flag.StringVar(&cfg.ExtraDeployerFlags, "deployer-flags", cfg.ExtraDeployerFlags,
		"extra flags corresponding to the deployer being used (optional). Supported flags can be"+
			" checked by running `kubetest2 [deployer] --help`")
	flag.StringVar(&cfg.GcloudExtraFlags, "gcloud-extra-flags", cfg.GcloudExtraFlags, "Extra gcloud flags to pass when creating the clusters.")
	flag.StringVar(&cfg.TestScript, "test-script", cfg.TestScript,
		"the script to run the tests after clusters are created")
	flag.StringVar(&cfg.TestFlags, "test-flags", cfg.TestFlags,
		"flags to pass through to the test script (optional)")
	flag.StringVar((*string)(&cfg.ReleaseChannel), "release-channel", string(cfg.ReleaseChannel),
		fmt.Sprintf("the GKE release channel to be used when creating clusters. Can be one of %v. "+
			"If not specified, a default release channel will be chosen for the cluster-version",
			types.SupportedReleaseChannels))
	flag.StringVar(&cfg.ClusterVersion, "cluster-version", cfg.ClusterVersion,
		"version for the the clusters (optional). Defaults to `latest`")
	flag.IntVar(&cfg.TRACPlatformIndex, "trac-platform-index", cfg.TRACPlatformIndex,
		"Anthos platform release index (as defined by go/anthos-trac). Selects the `latest-Nth` version that will be tested. "+
			"Both (or neither) of --trac-platform-index and --trac-component-index must be defined. "+
			"If <0 (the default), the manually crafted Tailorbird Rookery files in the ASM repo will be used instead.")
	flag.IntVar(&cfg.TRACComponentIndex, "trac-component-index", cfg.TRACComponentIndex,
		"ASM release index (as defined by go/anthos-trac). Selects the `latest-Nth` version that will be tested. "+
			"Both (or neither) of --trac-platform-index and --trac-component-index must be defined. "+
			"If <0 (the default), the manually crafted Tailorbird Rookery files in the ASM repo will be used instead.")
	flag.StringSliceVar(&upgradeVersions, "upgrade-cluster-version", []string{},
		"comma seperated list of versions that clusters will be upgraded to, formatted as x.y1.z,x.y2.z. Clusters will run for a short duration to ensure functionality between the cluster upgrades.")
	flag.StringVar((*string)(&cfg.Cluster), "cluster-type", string(cfg.Cluster),
		fmt.Sprintf("the cluster type, can be one of %v", types.SupportedClusters))
	flag.BoolVar(&cfg.UseOnePlatform, "use-oneplatform", cfg.UseOnePlatform, "whether to use One Platform API to provision the cluster")
	flag.BoolVar(&cfg.UseKubevirtVM, "use-kubevirt-vm", cfg.UseKubevirtVM, "To enable Kubevirt runtime in ABM cluster and run ASM tests on KubeVM APIs")
	flag.StringVar((*string)(&cfg.Topology), "topology", string(cfg.Topology),
		fmt.Sprintf("the cluster topology for the SUT (optional). Can be one of %v", types.SupportedTopologies))
	flag.StringVar((*string)(&cfg.WIP), "wip", string(cfg.WIP),
		fmt.Sprintf("Workload Identity Pool, can be one of %v", types.SupportedWIPs))
	flag.StringSliceVar(&features, "feature", []string{},
		fmt.Sprintf("the feature to test for ASM (optional). Can be one or multiple of %v", types.SupportedFeatures))
	flag.StringVar(&cfg.GCSBucket, "gcs-bucket", cfg.GCSBucket,
		"the GCS bucket to be used for the platform (optional). Supported values vary per platform")
	flag.BoolVar(&cfg.IsCloudESFTest, "is-cloudesf-test", cfg.IsCloudESFTest, "whether it is the test using CloudESF as ingress gateway")
	flag.StringSliceVar(&cfg.GCPProjects, "gcp-projects", cfg.GCPProjects, "the GCP projects used for creating GKE clusters (optional)")
	flag.StringVar((*string)(&cfg.Environment), "environment", string(cfg.Environment),
		fmt.Sprintf("Container API endpoint to use (optional). Can be one of %v", types.SupportedEnvironments))
	flag.BoolVar(&cfg.SyncUpgrade, "sync-upgrade", cfg.SyncUpgrade, "whether the cluster upgrade process should be done synchronously")
	flag.Parse()
	cfg.Features = sets.NewString(features...)
	cfg.UpgradeClusterVersion = upgradeVersions

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
	hackClientAuthentication()
}

const (
	oldClientAuthentication = "client.authentication.k8s.io/v1alpha1"
	newClientAuthentication = "client.authentication.k8s.io/v1beta1"
)

// TODO this is a hack to workaround  https://chat.google.com/room/AAAA4bSPYm4/bkCLisFqnUs
func hackClientAuthentication() {
	kubeconfigPath := os.Getenv("KUBECONFIG")
	if kubeconfigPath == "" {
		return
	}
	kubeconfig, err := ioutil.ReadFile(kubeconfigPath)
	if err != nil {
		log.Printf("failed reading kubeconfig %q: %v", kubeconfigPath, err)
	}
	strings.ReplaceAll(string(kubeconfig), oldClientAuthentication, newClientAuthentication)
	err = ioutil.WriteFile(kubeconfigPath, kubeconfig, 0644)
	if err != nil {
		log.Printf("failed writing kubeconfig %q: %v", kubeconfigPath, err)
	}
}
