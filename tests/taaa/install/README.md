# ASM Installer

This directory contains the files for a TaaA docker image that can install ASM clusters using information provided by a [protobuf](https://gke-internal.googlesource.com/taaa/protobufs/+/refs/heads/main/asm_installer/).

The currently images built by the code here are created and pushed manually by `mage build:push`. See [magefile](magefile.go) for details.
