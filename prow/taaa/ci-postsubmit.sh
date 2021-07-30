#!/bin/bash
# Copyright Google
# not licensed under Apache License, Version 2
# Must be called when CWD is repo root
set -euxo pipefail

SELFPATH="$(realpath "$(dirname "${BASH_SOURCE[0]}")")"
# shellcheck disable=SC1090
. "${SELFPATH}/ci.sh"

# TODO(coryrc): while waiting for the taaa-project to be setup correctly, don't push image
#mage build:push master-asm
mage build:artifact
