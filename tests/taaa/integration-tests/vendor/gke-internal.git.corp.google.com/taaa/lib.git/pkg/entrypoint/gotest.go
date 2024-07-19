package entrypoint

import (
	"context"
	"log"
	"os"
	"path/filepath"

	"gke-internal.git.corp.google.com/taaa/lib.git/internal/utilities"
	"gke-internal.git.corp.google.com/taaa/lib.git/pkg/gotest"
)

// GoTestContext runs a compiled Go test binary.
// binaryPath is the path for the compiled Go test binary.
// testPackageName is the name of the package being tested i.e. "knative.dev/serving/test/e2e"
// A log and JUnit xml are written into the output directory (as set when used as part of a TaaA entrypoint program).
// See gke-internal.git.corp.google.com/taaa/lib.git/pkg/gotest:RunContext for more details.
func GoTestContext(ctx context.Context, binaryPath, testPackageName string, testFlags ...string) *gotest.RunReturn {
	randSuffix := utilities.RandStr(6)
	log.Printf("Running go test binary %q to %s.log\n", binaryPath, randSuffix)
	rerr := gotest.RunContext(ctx, binaryPath, testPackageName, os.Stdout, filepath.Join(OutputDirectory, randSuffix+".log"), filepath.Join(OutputDirectory, "junit_"+randSuffix+".xml"), testFlags...)
	return rerr
}

// GoTestContext runs a compiled Go test binary.
// See GoTestContext for more details.
func GoTest(binaryPath, testPackageName string, testFlags ...string) *gotest.RunReturn {
	return GoTestContext(context.Background(), binaryPath, testPackageName, testFlags...)
}
