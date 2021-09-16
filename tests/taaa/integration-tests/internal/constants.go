package internal

// Copyright Google
// not licensed under Apache License, Version 2

const (
	ImgPath                      = "us-docker.pkg.dev/tests-as-an-artifact/gke-internal-istio/istio/integration-tests"
	ImageTagFile                 = "/IMAGE_TAG"
	RegistryDestinationDirectory = "/root"
	// The path in the TaaA image where supplementary files from this
	// repo need to be copied.
	RepoCopyRoot = "/istio"
)

var (
	// The tests in tests/integration/ to compile and package into the TaaA image.
	Tests = []string{
		"pilot",
	}
	// Whole directories to copy minus those matched by in the filters in SupplementFilters.
	TestSupplementDirs = []string{
		"/pkg/test/framework/features/",
		"/tests/integration/",
		"/prow/asm/tester/",
	}
	// The glob patterns used to match code files that we do not want to copy
	// into the TaaA image. We cannot have source code in images as a policy.
	SupplementFilters = []string{
		"*.go",
		"*.sh",
		"go.mod",
		"go.sum",
		"*.mk",
		"Makefile",
	}
)
