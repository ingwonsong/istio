#!/bin/bash
# Copyright Google
# not licensed under Apache License, Version 2
# Must be called when CWD is repo root
set -euxo pipefail

SELFPATH="$(realpath "$(dirname "${BASH_SOURCE[0]}")")"
# shellcheck disable=SC1090,SC1091
. "${SELFPATH}/ci.sh"

pushd tests/taaa/integration-tests
mage build:push master-asm
popd
