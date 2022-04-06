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

export ISTIO_DIR="${ISTIO_DIR:-$(git rev-parse --show-toplevel)}"
export PROXY_DIR="${PROXY_DIR:-${ISTIO_DIR}/../proxy}"

export ASM_BRANCH_SUFFIX="-asm"

export BRANCH="${BRANCH:-$(git rev-parse --abbrev-ref HEAD)}"
export REMOTE="${REMOTE:-origin}"

export UPSTREAM_BRANCH="${UPSTREAM_BRANCH:-${BRANCH/${ASM_BRANCH_SUFFIX}/}}"
export UPSTREAM_REMOTE="${UPSTREAM_REMOTE:-upstream}"

function ci_gitconfig() {
  # Setup URL substitutions so that we can import other repos from gke-internal
  # All these commands are directly copied from go/access-gob-from-everywhere#git-configuration-for-automation
  git config --add --global url."https://gke-internal.googlesource.com".insteadOf sso://gke-internal.git.corp.google.com
  git config --add --global url."https://gke-internal.googlesource.com".insteadOf sso://gke-internal
  git config --add --global url."https://gke-internal.googlesource.com".insteadOf https://gke-internal.git.corp.google.com
  git config --add --global url."https://gke-internal.googlesource.com".insteadOf git://gke-internal.git.corp.google.com
  git config --add --global url."https://gke-internal.googlesource.com".insteadOf git://gke-internal.googlesource.com
  git config --add --global url."https://gke-internal.googlesource.com".insteadOf git+ssh://gke-internal.git.corp.google.com
  git config --add --global url."https://gke-internal.googlesource.com".insteadOf git+ssh://gke-internal.googlesource.com
  git config --add --global url."https://gke-internal.googlesource.com".insteadOf ssh://gke-internal.git.corp.google.com
  git config --add --global url."https://gke-internal.googlesource.com".insteadOf ssh://gke-internal.googlesource.com
  git config --add --global url."https://gke-internal.googlesource.com".insteadOf sso://gke-internal.googlesource.com
  # Specify `GOPRIVATE=*.googlesource.com,*.git.corp.google.com` in the environment variables
  export GOPRIVATE='*.googlesource.com,*.git.corp.google.com'
  git config --global credential.helper gcloud.sh
}

function sync() {
  pushd "${ISTIO_DIR}"
  git checkout "${UPSTREAM_BRANCH}"
  git pull --ff-only "${UPSTREAM_REMOTE}" "${UPSTREAM_BRANCH}"

  git checkout "${BRANCH}"
  git checkout -b "${BRANCH}-merge-$(date +%F_%H-%M-%S)"
  git fetch "${REMOTE}" "${BRANCH}"
  git reset --hard FETCH_HEAD

  git merge "${UPSTREAM_REMOTE}/${UPSTREAM_BRANCH}"
  popd
}

function fix_proxy() {
  pushd "${ISTIO_DIR}"
  ISTIO_DEP_SHA="$(git show "${REMOTE}/${UPSTREAM_BRANCH}:istio.deps" | grep PROXY_REPO_SHA -A 4 | grep lastStableSHA | cut -f 4 -d '"' )"

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

function fix_gomod() {
  # do first run in repo root since asm tests use local code
  # i.e. `replace istio.io/istio => ../../..`
  go mod tidy
  cd "${ISTIO_DIR}" || return
  find . -iname go.mod | grep -v 'vendor' | while read -r x; do
    pushd "$(dirname "$x")" || return
    go mod tidy
    popd || return
  done
}

function dispatch() {
  local target="${1% }" # Remove spaces at end of line
  shift

  case "$target" in
    sync)
      sync "$@"
      ;;
    proxy-update)
      fix_proxy "$@"
      ;;
    go-tidy)
      fix_gomod "$@"
      ;;
    ci-config)
      ci_gitconfig "$@"
      ;;
    default)
      echo "No such function '$*' -- see targets in 'dispatch'."
      ;;
  esac
}

case "$(basename "$0")" in
  asm-sync.sh)
    dispatch sync "$@"
    ;;
  asm-proxy-update.sh)
    dispatch proxy-update "$@"
    ;;
  asm-go-tidy.sh)
    dispatch go-tidy "$@"
    ;;
  default)
    dispatch "$@"
    ;;
esac
