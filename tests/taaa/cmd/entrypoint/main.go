package main

// Copyright Google
// not licensed under Apache License, Version 2

import (
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"strings"

	"github.com/davecgh/go-spew/spew"
	"github.com/magefile/mage/sh"
	"github.com/spf13/cobra"
	"gke-internal.git.corp.google.com/taaa/lib.git/pkg/entrypoint"
	"gke-internal.git.corp.google.com/taaa/lib.git/pkg/registry"
	asmpb "gke-internal.git.corp.google.com/taaa/protobufs.git/asm"
	k8spb "gke-internal.git.corp.google.com/taaa/protobufs.git/k8sbase"
	"istio.io/istio/tests/taaa/internal/constants"
)

func main() {
	entrypoint.RunCmd.RunE = func(cmd *cobra.Command, args []string) error {
		m := asmpb.ASM{}
		if err := entrypoint.ReadProto(m); err != nil {
			return err
		}

		// Read from protobuf and create kubeconfigs
		// TODO(coryrc): we should generate kubeconfigs on the host side
		clusters := m.GetClusters()
		if len(clusters) == 0 {
			return errors.New("at least one cluster must be specified")
		}

		var kubeconfigs []string
		for _, cluster := range clusters {
			k, err := getKubeConfig(cluster.GetClusterInformation())
			if err != nil {
				return err
			}
			kubeconfigs = append(kubeconfigs, k)
		}
		project := clusters[0].GetClusterInformation().GetProject()
		if project == "" {
			return errors.New("project name mustn't be blank")
		}

		// Upload test images.
		// ASM tests use the flags "--istio.test.tag" and "--istio.test.hub" to find images.
		istioTestHub := fmt.Sprintf("gcr.io/%s/asm-test-images", project)
		server, err := registry.StartRegistry(constants.RegistryDestinationDirectory)
		if err != nil {
			return fmt.Errorf("cannot start registry server: %v", err)
		}
		if err := server.CopyOut(istioTestHub); err != nil {
			return fmt.Errorf("cannot copy out images from registry server: %v", err)
		}
		if err := server.Shutdown(); err != nil {
			log.Printf("Warning: registry cannot be killed, might want to figure out why, error %v", err)
		}

		istioTestTagBytes, err := ioutil.ReadFile(constants.ImageTagFile)
		if err != nil {
			return fmt.Errorf("/IMAGE_TAG file needed to know which image tag to use: %v", err)
		}
		istioTestTag := string(istioTestTagBytes)

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
			for _, bin := range constants.Tests {
				// This set of arguments was obtained by extracting the test command from a log and making substitutions as necessary.
				// There is currently no forcing function for keeping these in sync.
				err := entrypoint.GoTest(
					fmt.Sprintf("/usr/bin/%s.test", bin),
					"istio.io/istio/tests/integration/"+bin,
					"--test.p=1",
					"--timeout=30m",
					"--istio.test.kube.deploy=false",
					"--istio.test.skipVM",
					"--istio.test.ci",
					"--istio.test.pullpolicy=IfNotPresent",
					fmt.Sprintf("--istio.test.work_dir=%s/%s", entrypoint.OutputDirectory, bin),
					"--istio.test.hub="+istioTestHub,
					"--istio.test.tag="+istioTestTag,
					"--istio.test.kube.config="+strings.Join(kubeconfigs, ","),
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

func getKubeConfig(cred *k8spb.CredentialRequirements) (string, error) {
	cluster, project := cred.GetCluster(), cred.GetProject()
	if cluster == "" || project == "" {
		return "", fmt.Errorf("cluster or project is blank; c: %q; p: %q", cluster, project)
	}
	gcloudCredentialArgs := []string{"container", "clusters", "get-credentials", cluster, "--project=" + project}
	if cred.GetRegion() != "" {
		gcloudCredentialArgs = append(gcloudCredentialArgs, "--region="+cred.GetRegion())
	} else {
		gcloudCredentialArgs = append(gcloudCredentialArgs, "--zone="+cred.GetZone())
	}
	env := make(map[string]string)
	if cred.EndpointOverride != "" {
		env["CLOUDSDK_API_ENDPOINT_OVERRIDES_CONTAINER"] = cred.EndpointOverride
	}
	ret := "/tmp/kubeconfig-" + cluster
	env["KUBECONFIG"] = ret
	err := sh.RunWithV(env, "gcloud", gcloudCredentialArgs...)
	if err != nil {
		return "", err
	}
	return ret, nil
}
