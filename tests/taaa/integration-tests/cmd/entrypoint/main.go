package main

// Copyright Google
// not licensed under Apache License, Version 2

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/davecgh/go-spew/spew"
	shell "github.com/kballard/go-shellquote"
	"github.com/magefile/mage/sh"
	"github.com/spf13/cobra"
	"gke-internal.git.corp.google.com/taaa/lib.git/pkg/entrypoint"
	"gke-internal.git.corp.google.com/taaa/lib.git/pkg/magetools"
	"gke-internal.git.corp.google.com/taaa/lib.git/pkg/registry"
	asmpb "gke-internal.git.corp.google.com/taaa/protobufs.git/asm_integration"

	pkgexec "istio.io/istio/prow/asm/tester/pkg/exec"
	"istio.io/istio/prow/asm/tester/pkg/pipeline/env"
	"istio.io/istio/prow/asm/tester/pkg/resource"
	"istio.io/istio/prow/asm/tester/pkg/tests"
	"istio.io/istio/tests/taaa/test-artifact/internal"
)

var testerSetting = &resource.Settings{
	RepoRootDir:  internal.RepoCopyRoot,
	ClusterType:  resource.GKEOnGCP,
	ControlPlane: resource.Unmanaged,
	CA:           resource.MeshCA,
	WIP:          resource.GKEWorkloadIdentityPool,
	TestTarget:   "test.integration.asm.networking",
}

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
	if err := os.MkdirAll(entrypoint.OutputDirectory, 0o755); err != nil {
		log.Fatalf("Failed to find or create output directory, error: %s\n", err)
	}

	// Create the XML contents according to error details available.
	var xmlContent string
	if cmdRunErr == nil {
		xmlContent = successXML
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

	outputFileWriter, err := os.OpenFile(xmlPath, os.O_RDWR|os.O_CREATE, 0o755)
	if err != nil {
		log.Fatalf("cannot open file %q, got error %v", xmlPath, err)
	}
	_, err = outputFileWriter.WriteString(xmlContent)
	if err != nil {
		log.Fatalf("cannot write file %q, got error %v", xmlPath, err)
	}
}

func main() {
	// The args format is supposed to be `entrypoint xxx`.
	// If the subcommand is not taaa, we'll run asm_tester directly without
	// going into the taaa-specific logic.
	if len(os.Args) > 1 && os.Args[1] != "taaa" {
		if gac := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); gac != "" {
			out, err := exec.Command("gcloud", "auth", "activate-service-account", fmt.Sprintf("--key-file=%s", gac)).CombinedOutput()
			if err != nil {
				log.Fatalf("failed to activate service account for gcloud %v:\n%s", err, string(out))
			}
			log.Printf("Activated service account from %q: %v", gac, string(out))
		}
		asmTesterArgs := append([]string{"--setup-env", "--setup-system"}, os.Args[1:]...)
		if err := pkgexec.Run(fmt.Sprintf("asm_tester %s", shell.Join(asmTesterArgs...))); err != nil {
			log.Fatalf("error running asm_tester: %v", err)
		}

		return
	}

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
		// Only support for clusters in single network right now.
		testerSetting.GKENetworkName = clusters[0].GetGkeNetwork()

		kubeConfigs, mergedKubeconfig, err := createKubeConfigs(m)
		if err != nil {
			return fmt.Errorf("failed creating kubeconfig files with error: %s", err)
		}
		testerSetting.Kubeconfig = mergedKubeconfig
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

		istioTestTagBytes, err := os.ReadFile(internal.ImageTagFile)
		if err != nil {
			return fmt.Errorf("/IMAGE_TAG file needed to know which image tag to use: %v", err)
		}
		istioTestTag := string(istioTestTagBytes)
		log.Printf("Using image tag: [%s]", istioTestTag)
		if err := testerSetting.InstallOverride.Set(istioTestHub + ":" + istioTestTag + ":gke-release-staging"); err != nil {
			log.Fatalf("Failed to create install override struct: %s", err)
		}
		log.Printf("Install override structure: %+v", testerSetting.InstallOverride)
		// Run set up for clusters.
		if err := doSetup(m); err != nil {
			return err
		}
		// Set up environment variables for tests.
		envVars := map[string]string{
			"REPO_ROOT":  internal.RepoCopyRoot,
			"KUBECONFIG": mergedKubeconfig,
			"ARTIFACTS":  entrypoint.OutputDirectory,
		}
		if ci.GetEndpointOverride() != "" {
			envVars["CLOUDSDK_API_ENDPOINT_OVERRIDES_CONTAINER"] = ci.GetEndpointOverride()
			if strings.Contains(ci.GetEndpointOverride(), "test") {
				envVars["CLOUDSDK_API_ENDPOINT_OVERRIDES_GKEHUB"] = "https://autopush-gkehub.sandbox.googleapis.com/"
			}
		}
		for name, val := range envVars {
			log.Printf("Set env var: %s=%s", name, val)
			if err := os.Setenv(name, val); err != nil {
				return fmt.Errorf("error setting env var %q to %q", name, val)
			}
		}

		// Run install
		if m.Execution == asmpb.ASM_INSTALL || m.Execution == asmpb.ASM_BOTH {
			log.Println("Performing ASM install via Tester.")
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
			// The Setup function does more than we need to, but it's the most effective way to get
			// the information set in some key environment variables:
			// TEST_SELECT, and INTEGRATION_TEST_FLAGS
			if err := env.Setup(testerSetting); err != nil {
				return fmt.Errorf("error during env set up: %s", err)
			}
			if err := tests.Setup(testerSetting); err != nil {
				return fmt.Errorf("error during test argument set up: %s", err)
			}
			testSelect := os.Getenv("TEST_SELECT")
			// This set of arguments was obtained by extracting the test
			// command from a log and making substitutions as necessary.
			// There is currently no forcing function for keeping these in sync.
			testFlags := []string{
				// -p doesn't apply to single test binaries
				//"--test.p=1",
				"--test.timeout=30m",
				"--istio.test.ci",
				"--istio.test.pullpolicy=IfNotPresent",
				"--istio.test.hub", istioTestHub,
				"--istio.test.tag", istioTestTag,
				"--istio.test.kube.config", strings.Join(kubeConfigs, ","),
				"--istio.test.select", testSelect,
				"--log_output_level=tf:debug",
				// Disabling this test since it creates a service account.
				// TODO(efiturri): Fix enable this.
				"--istio.test.skip=TestBadRemoteSecret",
			}
			for _, testArg := range strings.Split(os.Getenv("INTEGRATION_TEST_FLAGS"), " ") {
				testFlags = append(testFlags, strings.ReplaceAll(testArg, "\"", ""))
			}
			log.Printf("Base test flags:\n%q", testFlags)
			workDir, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("error getting working directory: %w", err)
			}
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
				newWorkDir := path.Join(internal.RepoCopyRoot, internal.IntegrationTestRoot, bin)
				if err := os.Chdir(newWorkDir); err != nil {
					return fmt.Errorf("error changing working directory: %w", err)
				}
				passedFlags := append(testFlags,
					"--istio.test.work_dir="+path.Join(entrypoint.OutputDirectory, bin),
				)
				log.Printf("Running test binary: %s\n  Args: %q\n  Working Dir: %q", bin, passedFlags, newWorkDir)
				err := entrypoint.GoTest(
					fmt.Sprintf("/usr/bin/%s.test", bin),
					"istio.io/istio/tests/integration/"+bin,
					passedFlags...,
				)
				if err != nil {
					overall_err = err
				}
			}
			os.Chdir(workDir)

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
	mergedKubeConfigFile, err := os.CreateTemp("/tmp/", "taaa.kubeconfigFile.merged.*.yaml")
	if err != nil {
		return nil, "", fmt.Errorf("error creating merged kubeconfig file: %w", err)
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
	return nil
}

func runInstall(env map[string]string) error {
	_, errorOutput, cmdRunErr := magetools.BothOutputLogWith(env,
		"asm_tester",
		"--setup-env",
		"--setup-system",
		"--repo-root-dir", internal.RepoCopyRoot,
		"--install-from", testerSetting.InstallOverride.String(),
		"--wip", testerSetting.WIP.String(),
		"--gcp-projects", strings.Join(testerSetting.GCPProjects, ","),
		"--ca", testerSetting.CA.String(),
		"--cluster-type", testerSetting.ClusterType.String(),
		"--cluster-topology", testerSetting.ClusterTopology.String(),
		"--gke-network-name", testerSetting.GKENetworkName,
		"--test", testerSetting.TestTarget)
	createCmdJUnit(errorOutput, cmdRunErr)
	return cmdRunErr
}
