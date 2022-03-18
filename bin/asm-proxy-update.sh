#!/bin/bash

# Copyright 2022 Istio Authors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#    http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -euxo pipefail

export BRANCH="${BRANCH:-$(git rev-parse --abbrev-ref HEAD)}"
export ASM_BRANCH_SUFFIX="-asm"
export UPSTREAM_BRANCH=${BRANCH/${ASM_BRANCH_SUFFIX}/}
ISTIO_DIR="$(git rev-parse --show-toplevel)"
export ISTIO_DIR
export PROXY_DIR="${ISTIO_DIR}/../proxy"

function fix_proxy() {
  pushd "${ISTIO_DIR}"
  ISTIO_DEP_SHA=$(git show "${UPSTREAM_BRANCH}:istio.deps" | grep PROXY_REPO_SHA -A 4 | grep lastStableSHA | cut -f 4 -d '"' )

  # find latest proxy sha that has corresponding envoy binary pushed to gcs.
  PROXY_SHA=""
  pushd "${PROXY_DIR}"
  git fetch origin
  git checkout "origin/${BRANCH}"
  git reset --hard "origin/${BRANCH}"
  COMMIT="$(git rev-parse HEAD)"
  if gsutil stat "gs://asm-testing/istio/dev/envoy-alpha-${COMMIT}.tar.gz"; then
    PROXY_SHA="${COMMIT}"
  else
    echo "envoy binary for ${COMMIT} not found from gcs, please check proxy build flow"
    exit 1
  fi
  popd
  if [[ "${PROXY_SHA}" != "" && "${PROXY_SHA}" != ${ISTIO_DEP_SHA}"" ]]; then
    # make a commit with updated sha
    jq ".[0].lastStableSHA = \"${PROXY_SHA}\"" istio.deps -Mr > istio.deps.new
    mv istio.deps.new istio.deps
  fi
  popd
}

fix_proxy
