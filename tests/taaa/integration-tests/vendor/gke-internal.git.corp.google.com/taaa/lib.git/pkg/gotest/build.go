package gotest

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jstemmer/go-junit-report/formatter"
	"github.com/jstemmer/go-junit-report/parser"
)

func CompileTest(outputBinaryPath, pathToPackage string, otherGoTestArgs ...string) error {
	outputBinaryPath, err := filepath.Abs(outputBinaryPath)
	if err != nil {
		return err
	}
	pathToPackage, err = filepath.Abs(pathToPackage)
	if err != nil {
		return err
	}
	buildCmd := exec.Command("go", append([]string{"test", "-c", "-o", outputBinaryPath}, append(otherGoTestArgs, ".")...)...)
	buildCmd.Dir = pathToPackage
	buildCmd.Stderr = os.Stderr
	return buildCmd.Run()
}

// RunReturn
type RunReturn struct {
	JunitErr error
	CmdErr   error
	SetupErr error
}

func (r RunReturn) Error() string {
	if r.JunitErr == nil && r.CmdErr == nil && r.SetupErr == nil {
		return ""
	}
	return fmt.Sprintf("errors from creating junit: %v; setting up test execution: %v; running command: %v", r.JunitErr, r.SetupErr, r.CmdErr)
}

// Function RunContext runs a compiled Go test and creates output files and prints log to standard output
// Test flags are passed to the binary
//  If you are unfamiliar with using compiled go test binaries, flags for go test itself must be
//  must be prepended with "test."; i.e. for `go test -parallel 1`, when executing the compiled test binary "a.out" it becomes `./a.out -test.parallel 1`. (btw you can always do -test.parallel 1 even with go test).
//  So when using this function, "-test.parallel", "1" as the last two arguments gets the equivalent functionality of `go test -parallel 1 <yourpkg>`.
// "-test.v" is always passed to the binary because it's needed for junit processing, so don't add it yourself.
// If the file inputs are empty string, no file is created.
// If the optWriter is not included, the test output is not streamed anywhere (besides the files, if given).
// Stderr of the test is diverted to os.Stderr and not logged anywhere.
func RunContext(ctx context.Context, binaryPath, testPackageName string, optWriter io.Writer, goTestOutputFile, junitOutputFile string, testFlags ...string) *RunReturn {
	cmd := exec.CommandContext(ctx, binaryPath, append(testFlags, "--test.v")...)
	var writers []io.Writer
	if optWriter != nil {
		writers = append(writers, optWriter)
	}

	if goTestOutputFile != "" {
		f, err := os.Create(goTestOutputFile)
		if err != nil {
			return &RunReturn{SetupErr: err}
		}
		defer f.Close()
		writers = append(writers, f)
	}

	var wg sync.WaitGroup
	var junitErr error
	var parseWriter *io.PipeWriter // Needs to be closed before wg.Wait()
	if junitOutputFile != "" {
		wg.Add(1)
		f, err := os.Create(junitOutputFile)
		if err != nil {
			return &RunReturn{SetupErr: err}
		}
		defer f.Close()
		var r *io.PipeReader
		r, parseWriter = io.Pipe()
		writers = append(writers, parseWriter)
		go func() {
			defer wg.Done()
			report, err := parser.Parse(r, testPackageName)
			if err != nil {
				junitErr = fmt.Errorf("cannot parse go test output, got error %v", err)
				return
			}
			err = formatter.JUnitReportXML(report, false, "", f)
			if err != nil {
				junitErr = fmt.Errorf("cannot generate junit, got error %v", err)
				return
			}
		}()
	}

	cmd.Stdout = io.MultiWriter(writers...)
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if parseWriter != nil {
		parseWriter.Close()
	}
	wg.Wait()
	if err != nil || junitErr != nil {
		return &RunReturn{JunitErr: junitErr, CmdErr: err}
	}
	return nil
}

func TLogWriter(t *testing.T) io.WriteCloser {
	r, w := io.Pipe()

	go func() {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			t.Log(scanner.Text())
		}
	}()
	return w
}
