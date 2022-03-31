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

function usage() {
    local me
    me=$(basename "${BASH_SOURCE[0]}")
    cat <<_EOM
usage: ${me} [--base] [--patch]

Flags:
  -p|--patch ASM patch branch name
  -b|--base  ASM base branch name

Example:
  ${me} -b release-1.13-asm -p release-1.13.1-asm
_EOM
}

function git_repo() {
  [[ $(git remote) =~ "internal" ]] || git remote add internal sso://gke-internal.googlesource.com/istio/istio
}

function diff_chids() {
  local -A chIds_patch
  base_br=$1
  patch_br=$2

  git_repo
  commits_base=$(git rev-list --no-merges internal/"${patch_br}"..internal/"${base_br}")
  commits_patch=$(git rev-list --no-merges internal/"${base_br}"..internal/"${patch_br}")

  # Get Change IDs for commts in patch branch
  for i in $commits_patch; do
    commit_log=$(git rev-list --format=%B --max-count=1 "$i")
    chId=$(echo "$commit_log" | grep -oP '(?<=^Change-Id:\s)(.+)')
    commit_msg=$(echo "$commit_log" | sed -n 2p)
    if [[ -n $chId && $commit_msg != " " ]]; then
      chIds_patch[$chId]=$commit_msg
    fi
  done

  # Check which base branch Change IDs are in patch branch
  for i_base in $commits_base; do
    commit_log_b=$(git rev-list --format=%B --max-count=1 "$i_base")
    chId_b=$(echo "$commit_log_b" | grep -oP '(?<=^Change-Id:\s)(.+)')
    commit_msg_b=$(echo "$commit_log_b" | sed -n 2p)
    if [[ -n $chId_b && -z "${chIds_patch[$chId_b]}" ]]; then
      echo "Missing commit $i_base: $commit_msg_b, $chId_b "
    fi
  done
}

if (( $# == 0 )); then
    usage
    exit 1
fi

while [[ $# -gt 0 ]]; do
  case "$1" in
    -p|--patch_branch )
      patch_branch="$2"
      shift 2
      ;;
    -b|--base_branch )
      base_branch="$2"
      shift 2
      ;;
    * )
      usage
      ;;
  esac
done

echo "Showing commits present in $base_branch but missing in  $patch_branch"

diff_chids "$base_branch" "$patch_branch"