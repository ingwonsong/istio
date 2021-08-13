// This is a very basic program to test the image and install ASM with it.
package main

// Copyright Google
// not licensed under Apache License, Version 2

import (
	"os"

	"gke-internal.git.corp.google.com/taaa/lib.git/pkg/local"
	asm "gke-internal.git.corp.google.com/taaa/protobufs.git/asm_installer"
	"istio.io/istio/tests/taaa/install/internal"
)

func main() {
	asmpb := &asm.ASMInstaller{}
	if err := local.NewLocalRun(asmpb, internal.ImgPath+":latest").Execute(); err != nil {
		os.Exit(1)
	}
}
