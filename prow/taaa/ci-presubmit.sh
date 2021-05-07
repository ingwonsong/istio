#!/bin/bash
# Copyright Google
# not licensed under Apache License, Version 2
# Must be called when CWD is repo root
set -euxo pipefail

SELFPATH="$(realpath "$(dirname "${BASH_SOURCE[0]}")")"
# shellcheck disable=SC1090
. "${SELFPATH}/ci.sh"

mage build:artifact

