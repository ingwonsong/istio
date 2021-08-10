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

package asm

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"istio.io/pkg/log"
)

func Cmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "asm",
		Short: "Command group used to interact with ASM",
		Long:  `Command group used to interact with ASM.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 0 {
				return fmt.Errorf("unknown subcommand %q", args[0])
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.HelpFunc()(cmd, args)
			return nil
		},
	}

	cmd.AddCommand(mcpCheckCommand())

	return cmd
}

// errorUnsupportedConfig is a signal unsupported configuration is found and we should exit with non-zero error code
var errorUnsupportedConfig = fmt.Errorf("unsupported configuration found")

func mcpCheckCommand() *cobra.Command {
	for _, s := range log.Scopes() {
		// Hide irrelevant logs. We print, not log
		s.SetOutputLevel(log.WarnLevel)
	}
	var filenames []string
	outDir := "asm-generated-configs"
	revision := "asm-managed-rapid"
	cmd := &cobra.Command{
		Use:   "mcp-migrate",
		Short: "Tool to help migrate from an IstioOperator configuration file to Managed Control Plane",
		Long: `'istioctl asm mcp-migrate' helps migrate from an IstioOperator configuration file to configuration for
ASM Managed Control Plane.

This operator is non-destructive and does not access a Kubernetes cluster; all checks are done against the provided local files.`,
		Example: `  # Check a single file
  istioctl asm mcp-migrate -f istio-config.yaml

  # Check multiple overlays and output to a specified directory
  istioctl asm mcp-migrate -f istio-config.yaml -f enable-access-logs.yaml --out-dir /tmp/config`,
		RunE: func(cmd *cobra.Command, args []string) error {
			err := runMcpCheck(cmd.OutOrStdout(), filenames, outDir, revision)
			if err == errorUnsupportedConfig {
				os.Exit(2)
			}
			return err
		},
	}
	filenameFlagHelpStr := `Path to file containing IstioOperator custom resource
This flag can be specified multiple times to overlay multiple files. Multiple files are overlaid in left to right order.`
	cmd.PersistentFlags().StringSliceVarP(&filenames, "filename", "f", filenames, filenameFlagHelpStr)
	_ = cmd.MarkPersistentFlagRequired("filename")
	cmd.PersistentFlags().StringVarP(&outDir, "out-dir", "o", outDir,
		"Output directory. If not set, the current directory is used.")
	cmd.PersistentFlags().StringVar(&revision, "revision", revision,
		"ASM channel to generate configuration for. One of asm-managed-stable, asm-managed, asm-managed-rapid.")
	return cmd
}
