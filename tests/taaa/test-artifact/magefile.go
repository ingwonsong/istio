//+build mage

package main

// Copyright Google
// not licensed under Apache License, Version 2

import (
	"bufio"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"
	"gke-internal.git.corp.google.com/taaa/lib.git/pkg/git"
	"gke-internal.git.corp.google.com/taaa/lib.git/pkg/gotest"
	"gke-internal.git.corp.google.com/taaa/lib.git/pkg/registry"
	"istio.io/istio/tests/taaa/test-artifact/internal/constants"
)

const (
	// TODO(coryrc): fix taaa-project
	//ImgPath  = "gcr.io/taaa-project/host/gke-internal/istio/istio/integration-tests"
	ImgPath    = "gcr.io/gke-prow/gke-internal/istio/istio/integration-tests"
	repoRoot   = "../../../"
	outPath    = "./out/"
	outBinPath = outPath + "usr/bin/"
)

type Build mg.Namespace

// Build the entire Test Artifact and dependencies
func (Build) Artifact() error {
	mg.Deps(Build.Entrypoint, Build.Tests, Build.TestImages)
	mg.Deps(Build.ArtifactNoDeps)
	return nil
}

// TestImages build takes a bloody long time, so bypass the dependencies.
// If this is your first time, please run Artifact at least once.
// Then only run build:Entrypoint/Tests/TestImages depending on what you changed.
func (Build) ArtifactNoDeps() error {
	imgTag, err := git.DescribeCWD()
	if err != nil {
		return err
	}
	err = sh.RunV("docker", "build",
		"--pull",
		"-t", ImgPath+":"+imgTag,
		"-f", "Dockerfile",
		outPath)
	if err != nil {
		return err
	}
	return sh.RunV("docker", "tag", ImgPath+":"+imgTag, ImgPath+":latest")
}

// Docker push the image to its destination.
// Should only be run by CI system. Permissions should stop mortals from pushing.
// Pushes both the generated image path and one with a tag matching the argument to this function.
// If a series of commits merge in a short period of time, there's no guarantee the final status of the :branchName tag matches the very last commit.
// Due to rebasing in ASM we can't check for ancestor to confirm whether we should overwrite.
// This shouldn't be a serious problem.
func (Build) Push(branchName string) error {
	mg.Deps(Build.Artifact)
	imgTag, err := git.DescribeCWD()
	if err != nil {
		return err
	}
	err = sh.RunV("docker", "tag", ImgPath+":"+imgTag, ImgPath+":"+branchName)
	if err != nil {
		return err
	}
	err = sh.RunV("docker", "push", ImgPath+":"+imgTag)
	if err != nil {
		return err
	}
	return sh.RunV("docker", "push", ImgPath+":"+branchName)
}

// Compile the entrypoint binary.
func (Build) Entrypoint() error {
	if err := os.MkdirAll(outBinPath, 0775); err != nil {
		return err
	}
	return sh.Run("go", "build", "-o", outBinPath+"entrypoint", "./cmd/entrypoint")
}

// Compile the tests.
func (Build) Tests() error {
	if err := os.MkdirAll(outBinPath, 0775); err != nil {
		return err
	}
	var wg sync.WaitGroup
	var anyErr error
	for _, intTest := range constants.Tests {
		intTest := intTest
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := fmt.Sprintf(outBinPath+"%s.test", strings.ReplaceAll(intTest, "/", "_"))
			log.Printf("Compiling %s", name)
			err := gotest.CompileTest(
				name,
				repoRoot+"tests/integration/"+intTest,
				"--tags=integ")
			if err != nil {
				anyErr = err
			}
		}()
	}
	wg.Wait()
	return anyErr
}

// Build the images needed during test execution.
func (Build) TestImages() error {
	// Make output directories.
	if err := os.MkdirAll("out"+constants.RegistryDestinationDirectory, 0775); err != nil {
		return err
	}

	dir, err := ioutil.TempDir("/tmp", "taaa-*")
	if err != nil {
		return fmt.Errorf("cannot create temp directory %q", dir)
	}

	// Launch a local registry to hold test images.
	dsr, err := registry.Create(dir)
	if err != nil {
		return fmt.Errorf("failed to start the docker registry image")
	}

	// Define a tag for the images to use.
	imageTag, err := git.DescribeCWD()
	if err != nil {
		return err
	}

	// Store said tag so the artifact knows which to use.
	f, err := os.Create("out" + constants.ImageTagFile)
	if err != nil {
		return err
	}
	_, err = f.WriteString(imageTag)
	if err != nil {
		return err
	}
	f.Close()

	// Actually build those images.
	buildimages := exec.Command(
		"make",
		"dockerx.pushx",
		"HUB="+dsr.URL,
		"TAG="+imageTag,
		"DOCKER_TARGETS=docker.pilot docker.proxyv2 docker.app docker.install-cni docker.mdp",
	)
	buildimages.Dir = repoRoot
	// Note, cannot be = os.Stdout() because of a check in common/run.sh for whether FD is a terminal;
	// docker run will fail with "the input device is not a TTY" if you do.
	buildimages.Stdout = logWriter(os.Stdout)
	buildimages.Stderr = logWriter(os.Stderr)
	err = buildimages.Run()
	if err != nil {
		return err
	}

	// Done building and pushing images. Archive it.
	dsr.Shutdown()
	outputDir, err := registry.Archive(dir)
	if err != nil {
		return fmt.Errorf("cannot archive %q: %v", dir, err)
	}
	outRegistryDir := "out" + constants.RegistryDestinationDirectory
	configYml := outRegistryDir + "/config.yml"
	err = sh.RunV("rsync", outputDir+"/config.yml", configYml)
	if err != nil {
		return err
	}
	err = sh.RunV("rsync", outputDir+"/registry", outRegistryDir+"/registry")
	if err != nil {
		return err
	}
	varLibRegistryDir := outPath + "var/lib/registry"
	if err := os.MkdirAll(varLibRegistryDir, 0775); err != nil {
		return err
	}
	err = sh.RunV("rsync", "--delete", "-r", outputDir+"/varlibregistry/", varLibRegistryDir) // Trailing / is significant.
	if err != nil {
		return err
	}

	// Copying this file doesn't update its timestamp, so do this so we could only rebuild images when necessary.
	return sh.Run("touch", configYml)
}

func Clean() error {
	return sh.Run("rm", "-fR", "out")
}

func logWriter(out io.Writer) io.WriteCloser {
	l := log.New(out, "", log.LstdFlags)
	r, w := io.Pipe()
	go func() {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			l.Print(scanner.Text())
		}
	}()
	return w
}
