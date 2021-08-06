// This is a separate executable to handle installing ASM on a single cluster.
// It should not be run directly. It is called by the main entrypoint.
// It expects scriptaro to already be installed.
package main

// Copyright Google
// not licensed under Apache License, Version 2

import (
	"bytes"
	_ "embed"
	"encoding/xml"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/golang/protobuf/proto"
	"github.com/spf13/cobra"
	"gke-internal.git.corp.google.com/taaa/lib.git/pkg/entrypoint"
	"gke-internal.git.corp.google.com/taaa/lib.git/pkg/magetools"
	asmpb "gke-internal.git.corp.google.com/taaa/protobufs.git/asm_installer"
)

var (
	//go:embed override-overlay.yaml
	overlayYAMLContent  string
	overlayYAMLTemplate *template.Template

	// This process's cluster index.
	clusterId int

	// Path to the scriptaro executable.
	scriptaroCmd string

	// The TaaA proto defining the install request for all clusters.
	clusterProto *asmpb.ASMCluster

	// The environment variables to pass to executables.
	// We disable validation of Scriptaro via env vars.
	// This is done because the Scriptaro checks that we have all the
	// necessary IAM roles by looking only at user roles in it's query.
	// However, this is can be run as a service account and as such
	// scriptaro thinks we have no permissions  when we actually do.
	// Code link to problematic check:
	// http://go/gh/GoogleCloudPlatform/anthos-service-mesh-packages/blob/release-1.8-asm/scripts/asm-installer/install_asm#L1599
	Env map[string]string = map[string]string{"_CI_NO_VALIDATE": "1"}
)

// struct to store results of a cluster's attempt to install ASM.
type installStatus struct {
	errorOutput string
	cmdRunErr   error
}

// Defines the replacements needed in an overlay file
type overlay struct {
	ProjectId string
	Location  string
	Cluster   string
	Hostname  string
}

func init() {
	var err error
	overlayYAMLTemplate, err = template.New("overlay").Parse(overlayYAMLContent)
	if err != nil {
		log.Fatalln("Failed to parse overlay template file, with error: ", err)
	}
}

// Creates the JUnit to store the result and error of installing ASM on each cluster.
func createCmdJUnit(status *installStatus) {
	const successXML = `<testsuites>
<testsuite tests="1" failures="0">
<testcase classname="asmInstall/cluster%d"/>
</testsuite>
</testsuites>
`
	const failureXml = `<testsuites>
<testsuite tests="1" failures="1">
<testcase classname="asmInstall/cluster%d">
<failure>
%s
</failure>
</testcase>
</testsuite>
</testsuites>
`

	// Make output directory if it doesn't exist.
	if err := os.MkdirAll(entrypoint.OutputDirectory, os.ModePerm); err != nil {
		log.Fatalf("Failed to find or create output directory, error: %s\n", err)
	}

	// Create the XML contents according to error details available.
	var xmlContent string
	if status.cmdRunErr == nil {
		xmlContent = fmt.Sprintf(successXML, clusterId)
	} else {
		rawErrorMessage := fmt.Sprintf("Install failed with error:\n%s\n", status.cmdRunErr.Error())
		if status.errorOutput != "" {
			rawErrorMessage += fmt.Sprintf("\nRelevant standard error output:\n%s\n", status.errorOutput)
		}
		buf := bytes.NewBuffer(make([]byte, 0, len(rawErrorMessage)))
		err := xml.EscapeText(buf, []byte(rawErrorMessage))
		if err != nil {
			log.Fatalf("failed to convert error message for XMl content: %s", rawErrorMessage)
		}
		xmlContent = fmt.Sprintf(failureXml, clusterId, buf.String())
	}

	// Now create the and write the xml contents to the file.
	xmlPath := filepath.Join(
		entrypoint.OutputDirectory,
		fmt.Sprintf("junit_cluster_%s_install.xml", clusterProto.GetClusterInformation().GetCluster()))

	outputFileWriter, err := os.OpenFile(xmlPath, os.O_RDWR|os.O_CREATE, 0755)
	if err != nil {
		log.Fatalf("cannot open file %q, got error %v", xmlPath, err)
	}
	_, err = outputFileWriter.WriteString(xmlContent)
	if err != nil {
		log.Fatalf("cannot write file %q, got error %v", xmlPath, err)
	}
}

// Copies the overlay file template and replaces all text according to args.
// o defines the elements to replace in the overlay go template file
func updateOverlay(o overlay) (string, error) {
	newOverlayFile, err := ioutil.TempFile("/tmp/", "taaa.override-overlay.*.yaml")
	if err != nil {
		return "", fmt.Errorf("Failed creating overlay file, got error %v", err)
	}
	defer newOverlayFile.Close()
	overlayNewContent := new(strings.Builder)
	err = overlayYAMLTemplate.Execute(io.MultiWriter(newOverlayFile, overlayNewContent), o)
	if err != nil {
		return "", fmt.Errorf("Failed performing replacements on overlay file, got error %v", err)
	}

	log.Println(
		"Created overlay: ", newOverlayFile.Name(),
		"\n-----------------------\n",
		overlayNewContent.String(),
		"\n-----------------------")
	return newOverlayFile.Name(), nil
}

// Does all the ASM configuration and installation necessary on cluster.
func doInstall() *installStatus {
	ci := clusterProto.GetClusterInformation()
	status := &installStatus{}
	// Written to by all shell commands. Used to produce installStatus.
	var cmdRet error
	var cmdRan bool
	var cmdStdErr string
	// Helper to check for and fill out error info on failure.
	// Takes a cmdStr to relay to user the command that failed.
	processShellError := func(cmdStr string, cmdArgs ...string) bool {
		if cmdRet == nil {
			return false
		}
		log.Printf("Error running %s %s: %s", cmdStr, strings.Join(cmdArgs, " "), cmdRet)
		if cmdRan {
			status.errorOutput = cmdStdErr
		}
		status.cmdRunErr = cmdRet
		return true
	}

	location := ci.GetZone()
	if location == "" {
		location = ci.GetRegion()
	}

	// Scriptaro doesn't support a different endpoint than prod.
	// We need to create an overlay file to fix this.
	overlayFile := ""
	needsOverlay := ci.EndpointOverride != ""
	if needsOverlay {
		Env["CLOUDSDK_API_ENDPOINT_OVERRIDES_CONTAINER"] = ci.EndpointOverride
		u, err := url.Parse(ci.EndpointOverride)
		if err != nil {
			status.errorOutput = fmt.Sprint("Failed to parse API end point override URL, got error: ", ci.EndpointOverride)
			status.cmdRunErr = err
			return status
		}
		overlayFile, err = updateOverlay(
			overlay{
				ProjectId: ci.GetProject(),
				Location:  location,
				Cluster:   ci.GetCluster(),
				Hostname:  u.Hostname(),
			})
		if err != nil {
			status.errorOutput = fmt.Sprint("Failed to create overlay file, got error: ", ci.EndpointOverride)
			status.cmdRunErr = err
			return status
		}
	}
	// Before running scriptaro we need to set up kubectl
	kubeconfigFile, err := entrypoint.GetKubeConfig(ci)
	if err != nil {
		status.errorOutput = fmt.Sprint("Error during kubeconfig creation: ", kubeconfigFile)
		status.cmdRunErr = err
		return status
	}
	Env["KUBECONFIG"] = kubeconfigFile
	// Create istio-system namespace
	createNamespaceArgs := []string{
		"create", "namespace", "istio-system",
	}
	log.Println("Creating istio-system namespace.")
	_, cmdStdErr, cmdRet = magetools.BothOutputLogWith(Env, "kubectl", createNamespaceArgs...)
	if cmdRet != nil &&
		// If the namespace already exits, we don't care. Ignore error.
		!strings.Contains(cmdStdErr, `"istio-system" already exists`) &&
		processShellError("kubectl", createNamespaceArgs...) {
		return status
	}
	// Now we run scriptaro, appending the overlay file argument if we need it.
	asmArgs := []string{
		"--kubeconfig", kubeconfigFile,
		"--mode", "install",
		"--enable_cluster_roles", "--enable_gcp_apis", "--enable_gcp_components", "--enable_cluster_labels",
		"--verbose",
	}
	if needsOverlay {
		asmArgs = append(asmArgs, "--custom_overlay", overlayFile)
	}
	_, cmdStdErr, cmdRet = magetools.BothOutputLogWith(Env, scriptaroCmd, asmArgs...)
	if processShellError(scriptaroCmd, asmArgs...) {
		return status
	}
	status.errorOutput = ""
	status.cmdRunErr = nil
	return status
}

func main() {
	// installs ASM on a single cluster.
	entrypoint.RunCmd.RunE = func(cobraCmd *cobra.Command, args []string) error {
		fullProto := &asmpb.ASMInstaller{}
		if err := entrypoint.ReadProto(fullProto); err != nil {
			return err
		}
		// Grab our cluster's info. This is all we actually care about.
		clusters := fullProto.GetClusters()
		if len(clusters) <= clusterId || clusterId < 0 {
			log.Fatalf("Given out of bounds cluster ID (%d) for the install proto:\n%s",
				clusterId,
				proto.MarshalTextString(fullProto))
		}
		clusterProto = clusters[clusterId]
		// Set up logging prefix for execution.
		log.SetFlags(log.Ldate | log.Ltime | log.Lmsgprefix)
		log.SetPrefix(fmt.Sprintf("Cluster %d: ", clusterId))
		status := doInstall()
		createCmdJUnit(status)
		return status.cmdRunErr
	}
	entrypoint.RunCmd.PersistentFlags().IntVar(&clusterId, "index", -1, "The array index (starting at 0) of the cluster we want to install ASM for the given protobuf's clusters array.")
	entrypoint.RunCmd.MarkFlagRequired("index")

	entrypoint.RunCmd.PersistentFlags().StringVar(&scriptaroCmd, "scriptaro", "", "The executable path of the scriptaro command to use for ASM installation.")
	entrypoint.RunCmd.MarkFlagRequired("scriptaro")

	entrypoint.Execute()
}
