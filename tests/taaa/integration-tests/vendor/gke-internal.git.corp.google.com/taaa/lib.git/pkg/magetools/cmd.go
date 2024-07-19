package magetools

import (
	"bufio"
	"io"
	"log"
	"os"
	"strings"

	"github.com/magefile/mage/sh"
)

var (
	logger *log.Logger = log.Default()
)

// SetLogTemplate will modify the calls to BothOutputLogWith so it will use log
// prefixes and flags that are the same as the provided logger.
func SetLogTemplate(newLogger *log.Logger) {
	logger = log.New(newLogger.Writer(), newLogger.Prefix(), newLogger.Flags())
}

// BothOutputLogWith provides a magefile/sh like interface to run a command
// with its output pushed through the logger.
// It returns the StdOut and StdErr of the command respectively.
// The error returned will store the exit code if the command ran.
func BothOutputLogWith(env map[string]string, cmd string, args ...string) (string, string, error) {
	// These store our shell output no matter what.
	outMirror := new(strings.Builder)
	errMirror := new(strings.Builder)

	// Set up the writers used by the loggers for normal and error output.
	var outIo, errIo io.Writer
	outIo = io.MultiWriter(os.Stdout, outMirror)
	errIo = io.MultiWriter(os.Stderr, errMirror)
	// Create our loggers that the shell command will indirectly via a io.writer
	// that proxies them.
	// We use the current logger as a template to build the new ones.
	outLogger := NewLogProxyWriter(log.New(outIo, logger.Prefix(), logger.Flags()))
	errLogger := NewLogProxyWriter(log.New(errIo, logger.Prefix(), logger.Flags()))
	defer outLogger.Close()
	defer errLogger.Close()

	// Finally run the command now that writers are hooked.
	_, err := sh.Exec(env, outLogger, errLogger, cmd, args...)
	return outMirror.String(), errMirror.String(), err
}

// NewLogProxyWriter creates a WriteCloser.
// Lines written to the return value will be sent to the logger.
// Close should be called on the WriteCloser when finished.
func NewLogProxyWriter(l *log.Logger) io.WriteCloser {
	r, w := io.Pipe()
	scanner := bufio.NewScanner(r)
	go func() {
		for scanner.Scan() {
			l.Print(scanner.Text())
		}
	}()
	return w
}
