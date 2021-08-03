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

package app

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"

	istioctlcmd "istio.io/istio/istioctl/cmd"
	"istio.io/pkg/log"
)

func newMCPCheckCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp-check",
		Short: "Run pre-check commands for MCP",
		RunE: func(c *cobra.Command, args []string) error {
			params, err := mcpCheckParametersFromEnv()
			if err != nil {
				return err
			}

			log.Info("running precheck")
			if err := constructKubeConfigFile(params.project, params.location, params.cluster); err != nil {
				return fmt.Errorf("construct kube config: %v", err)
			}
			o, err := runIstioctl("/tmp/kubeconfig.yaml", []string{"x", "precheck"})
			log.Warnf("precheck output: %v", o)
			code := http.StatusOK
			if err != nil {
				code = http.StatusPreconditionFailed
			}
			addr := fmt.Sprintf(":%s", params.port)
			log.Infof("Listening on: %s", addr)
			return http.ListenAndServe(addr, constantHTTPHandler(o, code))
		},
	}
}

func runIstioctl(kubeconfig string, args []string) (string, error) {
	cmdArgs := append([]string{
		"--kubeconfig",
		kubeconfig,
	}, args...)

	var out strings.Builder
	rootCmd := istioctlcmd.GetRootCmd(cmdArgs)
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	fErr := rootCmd.Execute()
	resetLogs()
	return out.String(), fErr
}

func constantHTTPHandler(response string, code int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(code)
		w.Write([]byte(response)) // nolint: errcheck
	})
}

func resetLogs() {
	// Istioctl run tampers with log levels, set them all back
	for _, s := range log.Scopes() {
		s.SetOutputLevel(log.InfoLevel)
	}
}

type mcpCheckParameters struct {
	project  string
	location string
	cluster  string
	port     string
}

// nolint: envvarlint
func mcpCheckParametersFromEnv() (mcpCheckParameters, error) {
	p := mcpCheckParameters{}
	p.project = os.Getenv("PROJECT")
	if p.project == "" {
		return mcpCheckParameters{}, fmt.Errorf("PROJECT is a required environment variable")
	}
	p.location = os.Getenv("LOCATION")
	if p.location == "" {
		return mcpCheckParameters{}, fmt.Errorf("LOCATION is a required environment variable")
	}
	p.cluster = os.Getenv("CLUSTER")
	if p.cluster == "" {
		return mcpCheckParameters{}, fmt.Errorf("CLUSTER is a required environment variable")
	}
	p.port = os.Getenv("PORT")
	if p.port == "" {
		p.port = "8080"
	}
	return p, nil
}
