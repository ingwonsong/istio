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

package exec

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"

	"github.com/hashicorp/go-multierror"
	shell "github.com/kballard/go-shellquote"
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

// WithWorkingDir returns an option that sets the working directory for the
// given command.
func WithWorkingDir(dir string) Option {
	return func(c *exec.Cmd) {
		c.Dir = dir
	}
}

// WithWriter returns an option that sets custom stdout and stderr for the
// given command.
func WithWriter(outWriter, errWriter io.Writer) Option {
	return func(c *exec.Cmd) {
		c.Stdout = outWriter
		c.Stderr = errWriter
	}
}

// Start will run the command with the given options.
// It will be running the command in the background.
func Start(rawCommand string, options ...Option) error {
	cmd, err := parseAndBuildCmd(rawCommand, options...)
	if err != nil {
		return err
	}
	return cmd.Start()
}

// Run will run the command with the given options.
// It will wait until the command is finished.
func Run(rawCommand string, options ...Option) error {
	cmd, err := parseAndBuildCmd(rawCommand, options...)
	if err != nil {
		return err
	}
	return cmd.Run()
}

// Runf will format the command according to a format specifier and then run it.
// It will wait until the command is finished.
func Runf(format string, a ...interface{}) error {
	rawCommand := fmt.Sprintf(format, a)
	return Run(rawCommand)
}

// RunMultiple will run the commands with the given options and stop if there is an error.
// It will wait until the command is finished.
func RunMultiple(rawCommands []string, options ...Option) error {
	var err error
	for _, cmd := range rawCommands {
		err = Run(cmd, options...)
		if err != nil {
			return fmt.Errorf("command %q failed with error: %w", cmd, err)
		}
	}
	return nil
}

// RunMultipleNoStop will run the commands with the given options and does *not* stop if there is an error in the middle.
// It will wait until all the commands are finished. The returned error will be a list of errors.
func RunMultipleNoStop(rawCommands []string, options ...Option) error {
	var errList error
	for _, cmd := range rawCommands {
		err := Run(cmd, options...)
		if err != nil {
			errList = multierror.Append(errList, fmt.Errorf("command %q failed with error: %w", cmd, err))
		}
	}
	return errList
}

// Output will run the command with the given args and options, and
// return the output string.
// It will wait until the command is finished.
func Output(rawCommand string, options ...Option) ([]byte, error) {
	cmd, err := parseAndBuildCmd(rawCommand, options...)
	if err != nil {
		return nil, err
	}
	// The Output function call requires Stdout to be set as nil.
	cmd.Stdout = nil
	return cmd.Output()
}

// CombinedOutput will run the command with the given args and options, and
// return the output and stderr strings.
// It will wait until the command is finished.
func CombinedOutput(rawCommand string, options ...Option) ([]byte, error) {
	var buf bytes.Buffer
	if err := Run(rawCommand,
		WithWriter(io.MultiWriter(os.Stdout, &buf), io.MultiWriter(os.Stderr, &buf))); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Pipe will run the given two commands with a pipe, and return the output
// string for the second command.
func Pipe(cmd1, cmd2 *exec.Cmd) ([]byte, error) {
	// Get cmd1's stdout and attach it to cmd2's stdin.
	pipe, _ := cmd1.StdoutPipe()
	defer pipe.Close()

	cmd2.Stdin = pipe

	// Run cmd1 first.
	cmd1.Start()

	// Run and get the output of cmd2.
	return cmd2.Output()
}

func parseAndBuildCmd(rawCommand string, options ...Option) (*exec.Cmd, error) {
	log.Printf("⚙️ %s", rawCommand)
	cmdSplit, err := shell.Split(rawCommand)
	if len(cmdSplit) == 0 || err != nil {
		return nil, fmt.Errorf("error parsing the command %q: %w", rawCommand, err)
	}
	cmd := exec.Command(cmdSplit[0], cmdSplit[1:]...)
	cmd.Env = os.Environ()
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	for _, option := range options {
		option(cmd)
	}

	return cmd, nil
}
