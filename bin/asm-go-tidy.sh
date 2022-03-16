#!/bin/bash

export ISTIO_DIR="$(git rev-parse --show-toplevel)"

function fix_gomod() {
  cd "${ISTIO_DIR}"
  find . -iname go.mod | grep -v 'vendor' | while read x; do
    pushd "$(dirname $x)"
    go mod tidy
    go mod vendor
    popd
  done
}

fix_gomod
