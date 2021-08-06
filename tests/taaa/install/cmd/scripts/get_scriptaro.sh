#!/bin/bash
# Copyright Google
# not licensed under Apache License, Version 2

# This script expects  2 numbers to specify major and minor ASM version numbers in that order.
# It will download an install the release of scriptaro with that number as install_asm_${MAJOR}.${MINOR}
# http://go/scriptaro is the internal name of the install_asm script available on Github.

set -euo pipefail

if [ $# -ne 2 ]; then
    >&2 echo "Expects 2 number args in order of major and minor version numbers"
    exit 1
fi

verMajor="$1"
verMinor="$2"

tmpdir=$(mktemp -d)

pushd "${tmpdir}"
echo "Creating git repo and downloading scriptaro into ${tmpdir}"

git init --quiet
git remote add origin https://github.com/GoogleCloudPlatform/anthos-service-mesh-packages.git
git fetch --quiet origin "release-${verMajor}.${verMinor}-asm"
git checkout "origin/release-${verMajor}.${verMinor}-asm" -- scripts/asm-installer/install_asm
install -o root -g root -m 0755 scripts/asm-installer/install_asm "/usr/local/bin/install_asm_${verMajor}.${verMinor}"

popd

echo "Removing temporary directory."
rm -rf "${tmpdir}"
echo "Sucessfully installed scriptaro as install_asm_${verMajor}.${verMinor}"
