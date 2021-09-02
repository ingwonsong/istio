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
	Tests           = []string{"pilot"}
	TestSupplements = []string{
		"/pkg/test/framework/features/allowlist.txt",
		"/pkg/test/framework/features/features.yaml",
		"/tests/integration/iop-externalistiod-config-integration-test-defaults.yaml",
		"/tests/integration/iop-externalistiod-primary-integration-test-defaults.yaml",
		"/tests/integration/iop-externalistiod-remote-integration-test-defaults.yaml",
		"/tests/integration/iop-externalistiod-remote-integration-test-gateways.yaml",
		"/tests/integration/iop-integration-test-defaults.yaml",
		"/tests/integration/iop-istiodless-remote-integration-test-defaults.yaml",
		"/tests/integration/iop-remote-integration-test-defaults.yaml",
	}
	TesterDirs = []string{
		"/prow/asm/tester/configs",
		"/prow/asm/tester/scripts",
		"/manifests/addons",
		"/manifests/charts",
		"/manifests/profiles",
	}
)
