package main

// Copyright Google
// not licensed under Apache License, Version 2

import (
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strings"

	"github.com/davecgh/go-spew/spew"
	"github.com/magefile/mage/sh"
	"github.com/spf13/cobra"
	"gke-internal.git.corp.google.com/taaa/lib.git/pkg/entrypoint"
	"gke-internal.git.corp.google.com/taaa/lib.git/pkg/registry"
	asmpb "gke-internal.git.corp.google.com/taaa/protobufs.git/asm"
	"istio.io/istio/prow/asm/tester/pkg/resource"
	"istio.io/istio/tests/taaa/test-artifact/internal"
)

var testerSetting = &resource.Settings{
	RepoRootDir:  internal.RepoCopyRoot,
	UseASMCLI:    true,
	ClusterType:  resource.GKEOnGCP,
	ControlPlane: resource.Unmanaged,
}

func main() {
	entrypoint.RunCmd.RunE = func(cmd *cobra.Command, args []string) error {
		m := &asmpb.ASM{}
		if err := entrypoint.ReadProto(m); err != nil {
			return err
		}

		// We want to work out of the copy of the code repository's root.
		// We don't actually have code here, but config files for tester are assumed
		// to be relative to this directory and are copied in.
		if err := os.Chdir(internal.RepoCopyRoot); err != nil {
			return fmt.Errorf("failed to change to repo root directory: %s", err)
		}

		// Read from protobuf and create kubeconfigs
		// TODO(coryrc): we should generate kubeconfigs on the host side
		clusters := m.GetClusters()
		if len(clusters) == 0 {
			return errors.New("at least one cluster must be specified")
		}

		kubeconfigs, mergedKubeconfig, err := createKubeConfigs(m)
		if err != nil {
			return fmt.Errorf("failed creating kubeconfig files with error: %s", err)
		}
		testerSetting.Kubeconfig = strings.Join(kubeconfigs, ",")
		ci := clusters[0].GetClusterInformation()
		project := ci.GetProject()
		if project == "" {
			return errors.New("project name mustn't be blank")
		}

		// Upload test images.
		// ASM tests use the flags "--istio.test.tag" and "--istio.test.hub" to find images.
		istioTestHub := fmt.Sprintf("gcr.io/%s/asm-test-images", project)
		log.Println("Starting registry server.")
		server, err := registry.StartRegistry(internal.RegistryDestinationDirectory)
		if err != nil {
			return fmt.Errorf("cannot start registry server: %v", err)
		}
		if err := server.CopyOut(istioTestHub); err != nil {
			return fmt.Errorf("cannot copy out images from registry server: %v", err)
		}
		log.Println("Shutting down registry server.")
		if err := server.Shutdown(); err != nil {
			log.Printf("Warning: registry cannot be killed, might want to figure out why, error %v", err)
		}

		istioTestTagBytes, err := ioutil.ReadFile(internal.ImageTagFile)
		if err != nil {
			return fmt.Errorf("/IMAGE_TAG file needed to know which image tag to use: %v", err)
		}
		istioTestTag := string(istioTestTagBytes)
		log.Printf("Using image tag: [%s]", istioTestTag)
		testerSetting.InstallOverride = istioTestHub + ":" + istioTestTag
		// Run set up for clusters.
		if err := doSetup(m); err != nil {
			return err
		}
		// Set up environment variables for tests.
		envVars := map[string]string{
			"REPO_ROOT":  internal.RepoCopyRoot,
			"KUBECONFIG": mergedKubeconfig,
		}
		if ci.EndpointOverride != "" {
			envVars["CLOUDSDK_API_ENDPOINT_OVERRIDES_CONTAINER"] = ci.GetEndpointOverride()
		}
		for name, val := range envVars {
			log.Printf("Set env var: %s=%s", name, val)
			if err := os.Setenv(name, val); err != nil {
				return fmt.Errorf("error setting env var %q to %q", name, val)
			}
		}

		// Run install
		if err := runInstall(m, envVars); err != nil {
			return err
		}
		log.Println("Finished ASM install and post install set up.")

		// We expect ASM to be installed via the `Tester` application.
		// This disables any revision tagging done on the ASM version installed, so
		// we do not pass any revision information to the tests.
		// Run tests.
		var overall_err error
		// Execute networking tests.
		if m.GetTestSuite() == asmpb.ASM_ALL || m.GetTestSuite() == asmpb.ASM_NETWORKING {
			// TODO(coryrc): the rest of the tests
			/*
				ok      istio.io/istio/tests/integration/pilot  0.002s
				ok      istio.io/istio/tests/integration/pilot/analysis 0.095s
				ok      istio.io/istio/tests/integration/pilot/cni      0.002s
				?       istio.io/istio/tests/integration/pilot/common   [no test files]
				ok      istio.io/istio/tests/integration/pilot/endpointslice    0.002s
				ok      istio.io/istio/tests/integration/pilot/mcs      0.003s
				ok      istio.io/istio/tests/integration/pilot/revisioncmd      0.002s
				ok      istio.io/istio/tests/integration/pilot/revisions        0.003s
			*/
			for _, bin := range internal.Tests {
				// This set of arguments was obtained by extracting the test
				// command from a log and making substitutions as necessary.
				// There is currently no forcing function for keeping these in sync.
				err := entrypoint.GoTest(
					fmt.Sprintf("/usr/bin/%s.test", bin),
					"istio.io/istio/tests/integration/"+bin,
					// -p doesn't apply to single test binaries
					//"--test.p=1",
					"--test.timeout=30m",
					"--istio.test.kube.deploy=false",
					"--istio.test.skipVM",
					"--istio.test.ci",
					"--istio.test.pullpolicy=IfNotPresent",
					fmt.Sprintf("--istio.test.work_dir=%s/%s", entrypoint.OutputDirectory, bin),
					"--istio.test.hub="+istioTestHub,
					"--istio.test.tag="+istioTestTag,
					"--istio.test.kube.config="+testerSetting.Kubeconfig,
					"--istio.test.select=-postsubmit,-flaky",
					"--log_output_level=tf:debug,mcp:debug",
				)
				if err != nil {
					overall_err = err
				}
			}

		}
		return overall_err
	}
	// This function is just for debugging assistance.
	entrypoint.DumpProtoCmd.RunE = func(cmd *cobra.Command, args []string) error {
		m := asmpb.ASM{}
		if err := entrypoint.ReadProto(m); err != nil {
			return err
		}
		spew.Dump(m)
		return nil
	}
	entrypoint.Execute()
}

func createKubeConfigs(pb *asmpb.ASM) ([]string, string, error) {
	var kubeconfigs []string
	for _, cluster := range pb.GetClusters() {
		k, err := entrypoint.GetKubeConfig(cluster.GetClusterInformation())
		if err != nil {
			return nil, "", err
		}
		kubeconfigs = append(kubeconfigs, k)
	}
	mergedKubeConfigFile, err := ioutil.TempFile("/tmp/", fmt.Sprintf("taaa.kubeconfigFile.merged.*.yaml"))
	if err != nil {
		return nil, "", fmt.Errorf("Failed to create merged kubeconfig file got error %s", err)
	}
	defer mergedKubeConfigFile.Close()
	mergedContent, err := sh.OutputWith(map[string]string{
		"KUBECONFIG": strings.Join(kubeconfigs, ":"),
	}, "kubectl", "config", "view", "--flatten")
	if err != nil {
		return nil, "", err
	}
	_, err = mergedKubeConfigFile.Write([]byte(mergedContent))
	if err != nil {
		return nil, "", err
	}
	log.Println("Created merged kubeconfigfile: ", mergedKubeConfigFile.Name(),
		"\n-------------------------\n",
		mergedContent,
		"\n-------------------------")
	return kubeconfigs, mergedKubeConfigFile.Name(), nil
}

func doSetup(pb *asmpb.ASM) error {
	clusters := pb.GetClusters()
	// We always assume one project is used for all clusters right now.
	project := clusters[0].ClusterInformation.GetProject()
	testerSetting.GCPProjects = []string{project}
	testerSetting.ClusterGCPProjects = testerSetting.GCPProjects
	if len(clusters) > 1 {
		testerSetting.ClusterTopology = resource.MultiCluster
	} else {
		testerSetting.ClusterTopology = resource.SingleCluster
	}
	if pb.GetTestSuite() == asmpb.ASM_NETWORKING || pb.GetTestSuite() == asmpb.ASM_ALL {
		testerSetting.TestTarget = "test.integration.asm.networking"
	}
	// Remove any prexisting firewall rule.
	// The tester application creates a firewall on the project with the name
	// multicluster-firewall-rule. If it was not cleaned up for any reason before
	// we run the tester, the application will fail since the firewall already exists.
	sh.RunV(
		"gcloud", "compute",
		"firewall-rules", "delete",
		"multicluster-firewall-rule",
		"--project", project,
		"--quiet")
	return nil
}

func runInstall(pb *asmpb.ASM, env map[string]string) error {
	return sh.RunWithV(env,
		"asm_tester",
		"--setup-env",
		"--setup-system",
		"--use-asmcli",
		"--repo-root-dir", internal.RepoCopyRoot,
		"--install-from", testerSetting.InstallOverride,
		"--wip", "GKE",
		"--gcp-projects", strings.Join(testerSetting.GCPProjects, ","),
		"--ca", "MESHCA",
		"--cluster-type", "gke",
		"--cluster-topology", testerSetting.ClusterTopology.String(),
		"--test", "test.integration.asm.networking")
}
