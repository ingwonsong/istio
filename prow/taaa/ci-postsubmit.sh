#!/bin/bash
# Copyright Google
# not licensed under Apache License, Version 2
# Must be called when CWD is repo root
set -euxo pipefail

SELFPATH="$(realpath "$(dirname "${BASH_SOURCE[0]}")")"
# shellcheck disable=SC1090
. "${SELFPATH}/ci.sh"

pushd tests/taaa/integration-tests
# TODO(coryrc): while waiting for the tests-as-an-artifact to be setup correctly, don't push image
#mage build:push master-asm
mage build:artifact
popd

pushd tests/taaa/install
#mage build:push master-asm
mage build:artifact
popd
