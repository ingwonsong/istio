#!/bin/bash

set -euxo pipefail

export BRANCH="${BRANCH:-$(git rev-parse --abbrev-ref HEAD)}"
export ASM_BRANCH_SUFFIX="-asm"
export UPSTREAM_BRANCH=${BRANCH/${ASM_BRANCH_SUFFIX}/}
export ISTIO_DIR="$(git rev-parse --show-toplevel)"
export PROXY_DIR="${ISTIO_DIR}/../proxy"

function fix_proxy() {
  pushd "${ISTIO_DIR}"
  COMMIT_MESSAGE="gcb sync bot: update proxy sha"
  GCB_SYNC_BOT_PROXY_SHA_COMMIT=$(git log --pretty=oneline | grep "${COMMIT_MESSAGE}" | cut -d' ' -f 1 | head -n1)
  ISTIO_DEP_SHA=$(git show ${UPSTREAM_BRANCH}:istio.deps | grep PROXY_REPO_SHA -A 4 | grep lastStableSHA | cut -f 4 -d '"' )
  # Check when the proxy SHA was last updated.
  # if there are no commit from sync bot use latest commit from a log to
  # get a timestamp
  if [[ ${GCB_SYNC_BOT_PROXY_SHA_COMMIT} == "" ]]; then
  LATEST_PROXY_SHA_UPDATE_COMMIT=$(git log -n 1 --pretty=format:%H istio.deps)
  else
    LATEST_PROXY_SHA_UPDATE_COMMIT=${GCB_SYNC_BOT_PROXY_SHA_COMMIT}
  fi
  PROXY_SHA_COMMIT_DATE=$(git show -s --format=%at ${LATEST_PROXY_SHA_UPDATE_COMMIT})
  DELTA_SEC=$(expr $(date +%s)-${PROXY_SHA_COMMIT_DATE})


  # find latest proxy sha that has corresponding envoy binary pushed to gcs.
  PROXY_SHA=""
  pushd ${PROXY_DIR}
  git fetch origin
  git checkout origin/${BRANCH}
  git reset --hard origin/${BRANCH}
  COMMIT="$(git rev-parse HEAD)"
  if gsutil stat "gs://asm-testing/istio/dev/envoy-alpha-${COMMIT}.tar.gz"; then
  PROXY_SHA=${COMMIT}
  else
    echo "envoy binary for ${COMMIT} not found from gcs, please check proxy build flow"
    exit 1
  fi
  popd
  if [[ ${PROXY_SHA} != "" && ${PROXY_SHA} != ${ISTIO_DEP_SHA} ]]; then
    # make a commit with updated sha
    jq ".[0].lastStableSHA = \"${PROXY_SHA}\"" istio.deps -Mr > istio.deps.new
    mv istio.deps.new istio.deps
  fi
  popd
}

fix_proxy
