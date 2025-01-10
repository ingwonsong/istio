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

# Script to retrive the envoy version for CSM. It uses gob-curl to follow
# the repos chain istio/istio -> istio/proxy -> istio/envoy and prints
# Envoy's VERSION.txt.
# Examples:
#  ./envoy_version.sh release-1.24-asm
#  ./envoy_version.sh 1.24.2-asm.1
#  ./envoy_version.sh 50c04e793a54e27dcf4d910979440b4e700226b8

ISTIO_REF=$1
if [[ ${ISTIO_REF} == release-* ]]; then
    GOB_URL="https://gke-internal.googlesource.com/istio/istio/+/refs/heads/${ISTIO_REF}/istio.deps?format=TEXT"
elif [[ ${ISTIO_REF} =~ ^[a-fA-F0-9]{40}$ ]]; then
    GOB_URL="https://gke-internal.googlesource.com/istio/istio/+/${ISTIO_REF}/istio.deps?format=TEXT"
else
    GOB_URL="https://gke-internal.googlesource.com/istio/istio/+/refs/tags/${ISTIO_REF}/istio.deps?format=TEXT"
fi

TMPDIR=$(mktemp -d)
pushd "${TMPDIR}" || return
gob-curl -s "${GOB_URL}" | base64 --decode > istio.deps
ISTIO_PROXY_SHA=$(grep PROXY_REPO_SHA istio.deps  -A 4 | grep lastStableSHA | cut -f 4 -d '"')

PROXY_GOB_URL="https://gke-internal.googlesource.com/istio/proxy/+/${ISTIO_PROXY_SHA}/WORKSPACE?format=TEXT"
gob-curl -s "${PROXY_GOB_URL}" | base64 --decode > WORKSPACE

PROXY_ENVOY_SHA=$(grep -Pom1 "^ENVOY_SHA = \"\K[a-zA-Z0-9]{40}" "WORKSPACE")
ENVOY_GOB_URL="https://gke-internal.googlesource.com/istio/envoy/+/${PROXY_ENVOY_SHA}/VERSION.txt?format=TEXT"
echo "Envoy version: $(gob-curl -s "${ENVOY_GOB_URL}" | base64 --decode)"
echo "Envoy log: https://gke-internal.googlesource.com/istio/envoy/+log/${PROXY_ENVOY_SHA}"