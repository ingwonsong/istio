// This is a very basic program to test the image and install ASM with it.
package main

// Copyright Google
// not licensed under Apache License, Version 2

import (
	"os"

	"gke-internal.git.corp.google.com/taaa/lib.git/pkg/local"
	asm "gke-internal.git.corp.google.com/taaa/protobufs.git/asm_installer"
)

const asmImagePath = "gcr.io/taaa-project/host/gke-internal/istio/istio/install:latest"

func main() {
	asmpb := &asm.ASMInstaller{}
	if err := local.NewLocalRun(asmpb, asmImagePath).Execute(); err != nil {
		os.Exit(1)
	}
}
