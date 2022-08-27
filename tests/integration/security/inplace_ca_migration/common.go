//go:build integ
// +build integ

// Copyright Istio Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package inplacecamigration

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"testing"

	shell "github.com/kballard/go-shellquote"

	"istio.io/istio/pkg/test/echo/common/scheme"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/echo/check"
	"istio.io/istio/pkg/test/framework/components/namespace"
)

const (
	script         = "https://raw.githubusercontent.com/GoogleCloudPlatform/anthos-service-mesh-packages/%s/scripts/migration/ca-migration/%s"
	scriptBaseName = "migrate_ca"
)

// Option enables further configuration of a Cmd.
type Option func(cmd *exec.Cmd)

// WithAdditionalEnvs returns an option that adds additional env vars
// for the given Cmd.
func WithAdditionalEnvs(envs []string) Option {
	return func(c *exec.Cmd) {
		c.Env = append(c.Env, envs...)
	}
}

// WithAdditionalArgs returns an option that adds additional env vars
// for the given Cmd.
func WithAdditionalArgs(args []string) Option {
	return func(c *exec.Cmd) {
		c.Args = append(c.Args, args...)
	}
}

// WithWorkingDir returns an option that sets the working directory for the
// given command.
func WithWorkingDir(dir string) Option {
	return func(c *exec.Cmd) {
		c.Dir = dir
	}
}

// Run will run the command with the given options.
// It will wait until the command is finished.
func Run(rawCommand string, options ...Option) error {
	var errBuf bytes.Buffer
	err := commonRun(rawCommand, os.Stdout, io.MultiWriter(os.Stderr, &errBuf), options...)
	if err != nil {
		return fmt.Errorf("%s: %s", err.Error(), errBuf.String())
	}
	return nil
}

// RunWithOutput will run the command with the given options and return the
// output.
// It will wait until the command is finished.
func RunWithOutput(rawCommand string, options ...Option) (string, error) {
	var outBuf bytes.Buffer
	var errBuf bytes.Buffer
	err := commonRun(rawCommand, io.MultiWriter(os.Stdout, &outBuf), io.MultiWriter(os.Stderr, &errBuf), options...)
	if err != nil {
		return outBuf.String(), fmt.Errorf("%s: %s", err.Error(), errBuf.String())
	}
	return outBuf.String(), nil
}

func commonRun(rawCommand string, stdout, stderr io.Writer, options ...Option) error {
	cmdSplit, err := shell.Split(rawCommand)
	if len(cmdSplit) == 0 || err != nil {
		return fmt.Errorf("error parsing the command %q: %w", rawCommand, err)
	}
	cmd := exec.Command(cmdSplit[0], cmdSplit[1:]...)
	cmd.Env = os.Environ()
	cmd.Stdin = os.Stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	for _, option := range options {
		option(cmd)
	}

	log.Printf("⚙️ %s", shell.Join(cmd.Args...))
	return cmd.Run()
}

func checkConnectivity(t *testing.T, ctx framework.TestContext, a echo.Instances, b echo.Instances,
	testPrefix string,
) {
	t.Helper()
	ctx.NewSubTest(testPrefix).Run(func(t framework.TestContext) {
		srcList := []echo.Instance{a[0], b[0]}
		dstList := []echo.Instance{b[0], a[0]}
		for index := range srcList {
			src := srcList[index]
			dst := dstList[index]
			callOptions := echo.CallOptions{
				To:     dst,
				Port:   echo.Port{Name: "http"},
				Scheme: scheme.HTTP,
				Count:  1,
			}
			callOptions.Check = check.OK()
			src.CallOrFail(t, callOptions)
		}
	})
}

func downloadMigrationTool(t *testing.T, workingDir string) string {
	t.Helper()
	toolURL := fmt.Sprintf(script, "main", scriptBaseName)
	t.Logf("Downloading script from %s...", toolURL)
	resp, err := http.Get(toolURL)
	if err != nil {
		t.Fatalf("error: unable to fetch migration tool: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Fatalf("error: unable to find migration tool")
	}
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("error: unable to read migration tool body: %v", err)
	}

	toolPath := path.Join(workingDir, scriptBaseName)
	f, err := os.OpenFile(toolPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o777)
	if err != nil {
		t.Fatalf("error: unable to open file %v to write into: %v", scriptBaseName, err)
	}
	_, err = f.Write(bodyBytes)
	if err != nil {
		t.Fatalf("error: unable to open file %v to write into: %v", scriptBaseName, err)
	}
	err = f.Close()
	if err != nil {
		t.Fatalf("error: unable to close file %v: %v", scriptBaseName, err)
	}
	return toolPath
}

func setupCAMigration(t *testing.T, ctx framework.TestContext,
	oldCA, oldCAName, newCA, newCAName string,
) []string {
	clusterDirs := []string{}
	if fleetProjectID == "" || revision == "" {
		t.Fatalf("invalid env setup - fleet %v, revision %v!", fleetProjectID, revision)
		return clusterDirs
	}
	for _, cluster := range ctx.Clusters() {
		// TODO(shankgan) Doesn't work for multicluster
		workingDir, err := os.MkdirTemp("", fmt.Sprintf("test-%s", cluster.Name()))
		if err != nil {
			t.Fatalf("error: unable to create temp directory ")
		} else {
			t.Logf("tool working directory is %v", workingDir)
		}
		clusterDirs = append(clusterDirs, workingDir)
		toolPath := downloadMigrationTool(t, workingDir)
		cmd := fmt.Sprintf("%s setup --output_dir %v", toolPath, workingDir)
		out, err := RunWithOutput(cmd)
		if err != nil {
			t.Fatalf("setup: failed with error:%v, out: %v", err, out)
		}
		oldCAStr := ""
		if oldCA != "" {
			oldCAStr = fmt.Sprintf("--ca-old %v ", oldCA)
		}
		if oldCAName != "" {
			oldCAStr = fmt.Sprintf("%s --ca-pool-old %v ", oldCAStr, oldCAName)
		}

		// TODO(shankgan): Need to pass the kubecontext correctly for the cluster
		cmd = fmt.Sprintf("%s initialize --ca %v --ca-pool %s %s --fleet_id %v --revision %v --output_dir %v",
			toolPath, newCA, newCAName, oldCAStr, fleetProjectID, revision, workingDir)
		out, err = RunWithOutput(cmd)
		if err != nil {
			t.Fatalf("initialize: failed for cluster %v with error: %v, out: %v",
				cluster.Name(), err, out)
		}
	}
	return clusterDirs
}

func writeCertFile(t *testing.T, workingDir, caCert string) string {
	baseFileName := "root-cert.pem"
	caCertFilePath := path.Join(workingDir, baseFileName)
	f, err := os.OpenFile(caCertFilePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o777)
	if err != nil {
		t.Fatalf("migrateCA: unable to open file %v to write into: %v", caCertFilePath, err)
	}
	_, err = f.Write([]byte(caCert))
	if err != nil {
		t.Fatalf("migrateCA: unable to write trustanchor into file: %v", err)
	}
	return caCertFilePath
}

func migrateCA(t *testing.T, ctx framework.TestContext, workingDirs []string, namespaces string, caCerts []string) {
	// add trust anchors

	for index, cluster := range ctx.Clusters() {
		workingDir := workingDirs[index]
		toolPath := path.Join(workingDir, scriptBaseName)
		for i := 0; i < len(caCerts); i++ {
			caCertFilePath := writeCertFile(t, workingDir, caCerts[i])
			cmd := fmt.Sprintf("%s add-trust-anchor --ca_cert %v --output_dir %v", toolPath, caCertFilePath, workingDir)
			out, err := RunWithOutput(cmd)
			if err != nil {
				t.Fatalf("migrateCA: add-trust-anchor failed for cluster %v, error: %v, out: %v",
					cluster.Name(), err, out)
			}
			cmd = fmt.Sprintf("%s check-trust-anchor --ca_cert %v --namespaces %v --output_dir %v",
				toolPath, caCertFilePath, namespaces, workingDir)
			out, err = RunWithOutput(cmd)
			if err != nil {
				// TODO(shankgan): Optimize the check, Perhaps parse output as well??
				t.Fatalf("migrateCA: check-trust-anchor failed for cluster %v, error: %v, out: %v",
					cluster.Name(), err, out)
			}
		}
	}

	// Only migrate CA when trustAnchors have been distributed to all clusters
	for index, cluster := range ctx.Clusters() {
		workingDir := workingDirs[index]
		toolPath := path.Join(workingDir, scriptBaseName)
		cmd := fmt.Sprintf("%s migrate-ca --output_dir %v",
			toolPath, workingDir)
		out, err := RunWithOutput(cmd)
		if err != nil {
			// TODO(shankgan): Optimize the check, Perhaps parse output as well??
			t.Fatalf("migrateCA:migrate-ca failed for cluster %v, error: %v, out: %v",
				cluster.Name(), err, out)
		}
	}
}

func verifyCA(t *testing.T, ctx framework.TestContext,
	workingDirs []string, namespaces, caCert string,
) error {
	// verify CA is signing certificates
	for index, cluster := range ctx.Clusters() {
		workingDir := workingDirs[index]
		caCertFilePath := writeCertFile(t, workingDir, caCert)
		toolPath := path.Join(workingDir, scriptBaseName)
		cmd := fmt.Sprintf("%s verify-ca --ca_cert %v --namespaces %v --output_dir %v",
			toolPath, caCertFilePath, namespaces, workingDir)
		out, err := RunWithOutput(cmd)
		if err != nil {
			return fmt.Errorf("verifyCA: failed for cluster %s, error: %v, out: %v",
				cluster.Name(), err, out)
		}
	}
	return nil
}

func rollbackCA(t *testing.T, ctx framework.TestContext, workingDirs []string) {
	for index, cluster := range ctx.Clusters() {
		workingDir := workingDirs[index]
		toolPath := path.Join(workingDir, scriptBaseName)
		cmd := fmt.Sprintf("%s rollback --output_dir %v",
			toolPath, workingDir)
		out, err := RunWithOutput(cmd)
		if err != nil {
			// TODO(shankgan): Optimize the check, Perhaps parse output as well??
			t.Fatalf("rollbackCA: failed for cluster %v, error: %v, out: %v",
				cluster.Name(), err, out)
		}
	}
}

func addNamespaceToConfig(config echo.Config, ns namespace.Instance) echo.Config {
	config.Namespace = ns
	return config
}
