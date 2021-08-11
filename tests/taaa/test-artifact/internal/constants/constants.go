package constants

// Copyright Google
// not licensed under Apache License, Version 2

const (
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
	}
)
