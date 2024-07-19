// Package registry provides functions for archiving Docker images.
//
// It provides functions for starting a Docker registry, archiving
//  images, and bringing the registry back up in the entrypoint.
//
// The problem it solves: tests may themselves need image artifacts.
// Normally when using `go test`, for example, one might build and deploy images just before running the test.
// Ideally for TaaA you don't have any source code in the test artifact, so how do you build and deploy those test images?
// This library makes it very easy to collect all those test images while building the test artifact. It's agnostic to what tool builds the images, because it just saves everything you build.
// The second part of the library allows you, during test execution, to use or copy those images to someplace useful just prior to running the tests themselves.
//
// All-in-all, usual usage of these functions is:
// During test artifact build phase, you will be calling:
//  1. c := registry.Create()
//  2. Build your tests' images
//  3. c.Shutdown()
//  4. registry.Archive()
//
// In the entrypoint, you will usually call:
//  1. c := registry.StartRegistry()
//  2. c.CopyOut()
//  3. c.Shutdown()
//  4. Proceed with running tests
package registry

const (
	registryImage = "registry:2"
)
