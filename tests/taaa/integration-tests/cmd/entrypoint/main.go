package main

// Copyright Google
// not licensed under Apache License, Version 2

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/davecgh/go-spew/spew"
	"github.com/magefile/mage/sh"
	"github.com/spf13/cobra"
	"gke-internal.git.corp.google.com/taaa/lib.git/pkg/entrypoint"
	"gke-internal.git.corp.google.com/taaa/lib.git/pkg/magetools"
	"gke-internal.git.corp.google.com/taaa/lib.git/pkg/registry"
	asmpb "gke-internal.git.corp.google.com/taaa/protobufs.git/asm_integration"
	"istio.io/istio/prow/asm/tester/pkg/resource"
	"istio.io/istio/tests/taaa/test-artifact/internal"
)

// Creates the JUnit to store the result and error of installing ASM on each cluster.
func createCmdJUnit(errorOutput string, cmdRunErr error) {
	const successXML = `<testsuites>
<testsuite tests="1" failures="0">
<testcase classname="asmInstall/cluster"/>
</testsuite>
</testsuites>
`
	const failureXml = `<testsuites>
<testsuite tests="1" failures="1">
<testcase classname="asmInstall/cluster">
<failure>
%s
</failure>
</testcase>
</testsuite>
</testsuites>
`

	// Make output directory if it doesn't exist.
	if err := os.MkdirAll(entrypoint.OutputDirectory, 0755); err != nil {
		log.Fatalf("Failed to find or create output directory, error: %s\n", err)
	}

	// Create the XML contents according to error details available.
	var xmlContent string
	if cmdRunErr == nil {
		xmlContent = fmt.Sprintf(successXML)
	} else {
		rawErrorMessage := fmt.Sprintf("Install failed with error:\n%s\n", cmdRunErr.Error())
		if errorOutput != "" {
			rawErrorMessage += fmt.Sprintf("\nRelevant standard error output:\n%s\n", errorOutput)
		}
		buf := bytes.NewBuffer(make([]byte, 0, len(rawErrorMessage)))
		err := xml.EscapeText(buf, []byte(rawErrorMessage))
		if err != nil {
			log.Fatalf("failed to convert error message for XMl content: %s", rawErrorMessage)
		}
		xmlContent = fmt.Sprintf(failureXml, buf.String())
	}

	// Now create the and write the xml contents to the file.
	xmlPath := filepath.Join(entrypoint.OutputDirectory, "junit_cluster_asm_install.xml")

	outputFileWriter, err := os.OpenFile(xmlPath, os.O_RDWR|os.O_CREATE, 0755)
	if err != nil {
		log.Fatalf("cannot open file %q, got error %v", xmlPath, err)
	}
	_, err = outputFileWriter.WriteString(xmlContent)
	if err != nil {
		log.Fatalf("cannot write file %q, got error %v", xmlPath, err)
	}
}

var testerSetting = &resource.Settings{
	RepoRootDir:  internal.RepoCopyRoot,
	UseASMCLI:    true,
	ClusterType:  resource.GKEOnGCP,
	ControlPlane: resource.Unmanaged,
}

func main() {
	entrypoint.RunCmd.RunE = func(cmd *cobra.Command, args []string) error {
		magetools.SetLogTemplate(log.New(os.Stdout, "", log.Default().Flags()))
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
		if m.Execution == asmpb.ASM_INSTALL || m.Execution == asmpb.ASM_BOTH {
			if err := runInstall(envVars); err != nil {
				return err
			}
			log.Println("Finished ASM install and post install set up.")
		} else {
			log.Println("Skipping ASM install per protobuf `Execution` field.")
		}

		if m.Execution != asmpb.ASM_BOTH && m.Execution != asmpb.ASM_TEST {
			log.Println("Skipping ASM test execution and exiting.")
			return nil
		}

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

func runInstall(env map[string]string) error {
	_, errorOutput, cmdRunErr := magetools.BothOutputLogWith(env,
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
	createCmdJUnit(errorOutput, cmdRunErr)
	return cmdRunErr
}
