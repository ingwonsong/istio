//+build mage

package main

// Copyright Google
// not licensed under Apache License, Version 2

import (
	"fmt"
	"os"

	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"
	"gke-internal.git.corp.google.com/taaa/lib.git/pkg/git"
	"gke-internal.git.corp.google.com/taaa/lib.git/pkg/magetools"
)

const (
	imgPath = "gcr.io/taaa-project/host/gke-internal/istio/istio/install"
	outPath = "./out/"
)

type Build mg.Namespace

// Build the entire Test Artifact and dependencies.
func (Build) Artifact() error {
	mg.Deps(Build.Entrypoint)
	// Define a tag for the image to use.
	imageTag, err := git.DescribeCWD()
	if err != nil {
		return fmt.Errorf("error running git describe to get image tag: %s", err)
	}
	err = sh.RunV("docker", "build",
		"--pull",
		"-t", imgPath+":"+imageTag,
		"-f", "Dockerfile",
		outPath,
	)
	if err != nil {
		return err
	}
	return sh.RunV("docker", "tag", imgPath+":"+imageTag, imgPath+":latest")
}

// Compile the entrypoint binary.
func (Build) Entrypoint() error {
	if err := os.MkdirAll(outPath+"usr/bin", 0775); err != nil {
		return fmt.Errorf("%s", err)
	}
	// Copy over scriptaro installation script first.
	err := magetools.CopyFile("./cmd/scripts/get_scriptaro.sh", outPath+"usr/bin/get_scriptaro.sh", 0755)
	if err != nil {
		return fmt.Errorf("error when copying scriptaro install script: %s", err)
	}
	// Now compile the the entrypoint into the out directory.
	err = sh.Run("go", "build", "-o", outPath+"usr/bin/entrypoint", "./cmd/entrypoint")
	if err != nil {
		return err
	}
	return sh.Run("go", "build", "-o", outPath+"usr/bin/asmclusterinstall", "./cmd/install")
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
		return fmt.Errorf("error running git describe to get image tag: %s", err)
	}
	err = sh.RunV("docker", "tag", imgPath+":"+imgTag, imgPath+":"+branchName)
	if err != nil {
		return fmt.Errorf("error tagging created image: %s", err)
	}
	err = sh.RunV("docker", "push", imgPath+":"+imgTag)
	if err != nil {
		return fmt.Errorf("error pushing tagged image: %s", err)
	}
	return sh.RunV("docker", "push", imgPath+":"+branchName)
}

func Clean() error {
	return sh.Run("rm", "-fR", outPath)
}
