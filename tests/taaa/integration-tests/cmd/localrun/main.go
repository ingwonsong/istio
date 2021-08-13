// This is a very basic program to run test artifacts on clusters with ASM installed.
package main

// Copyright Google
// not licensed under Apache License, Version 2

import (
	"os"

	"gke-internal.git.corp.google.com/taaa/lib.git/pkg/local"
	asm "gke-internal.git.corp.google.com/taaa/protobufs.git/asm"
	"istio.io/istio/tests/taaa/test-artifact/internal"
)

func main() {
	asmpb := &asm.ASM{}
	if err := local.NewLocalRun(asmpb, internal.ImgPath+":latest").Execute(); err != nil {
		os.Exit(1)
	}
}
