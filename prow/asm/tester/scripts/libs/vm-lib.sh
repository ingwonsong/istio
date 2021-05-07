#!/bin/bash

# Copyright Istio Authors
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


# retries the given command N times, terminating and retrying after a given time period.
# There is a 2 second backoff between attempts.
# Parameters: $1 - Max attempts
#             $2 - Time period to allow command to run for
#             $@ - Remaining arguments make up the command to run.
retry() {
  local MAX_TRIES="${1}"; shift 1
  local TIMEOUT="${1}"; shift 1

  for i in $(seq 0 "${MAX_TRIES}"); do
    if [[ "${i}" -eq "${MAX_TRIES}" ]]; then
      false
      return
    fi
    if [ -n "${TIMEOUT}" ]; then
      { timeout "${TIMEOUT}" "${@}" && return 0; } || true
    else
      { "${@}" && return 0; } || true
    fi
    echo "Failed, retrying...($((i+1)) of ${MAX_TRIES})"
    sleep 2
  done
  false
}

# Allow traffic from the allowable source ranges (including the Prow job Pod and
# the test clusters) to all VMs tagged `gcevm` in the PROJECT_ID where ASM/VMs
# live.
# Allow all the TCP traffic because some integration uses random ports, and we
# cannot limit the usage of them.
 # Parameters: $1 - Project ID where the VMs reside
function firewall_rule() {
  gcloud compute firewall-rules create \
    --project "$PROJECT_ID" \
    --allow=tcp \
    --source-ranges="$ALLOWABLE_SOURCE_RANGES" \
    --target-tags=prow-test-vm \
    --network=prow-test-network \
    prow-to-static-vms
}

function setup_gce_vms() {
  local FILE="$1"
  local CONTEXT="$2"
  IFS="_" read -r -a VALS <<< "${CONTEXT}"
  local PROJECT_ID="${VALS[1]}"
  local LOCATION="${VALS[2]}"
  local CLUSTER_NAME="${VALS[3]}"
  local REVISION
  REVISION="$(kubectl --context="${CONTEXT}" -n istio-system get service istio-eastwestgateway -ojsonpath='{.metadata.labels.istio\.io/rev}')"

  local PROJECT_NUMBER
  PROJECT_NUMBER=$(gcloud projects describe "${PROJECT_ID}" --format="value(projectNumber)")
  firewall_rule "${PROJECT_ID}"

  cat << EOF >> "${FILE}"
- kind: ASMVM
  clusterName: asm-vms
  primaryClusterName: "cn-${PROJECT_ID}-${LOCATION}-${CLUSTER_NAME}"
  meta:
    project: ${PROJECT_ID}
    projectNumber: ${PROJECT_NUMBER}
    gkeLocation: ${LOCATION}
    gkeCluster: ${CLUSTER_NAME}
    gkeNetwork: prow-test-network
    asm_vm: ${VM_SCRIPT}
    firewallTag: prow-test-vm
    env:
    - key: ASM_REVISION_PREFIX
      value: ${REVISION}
    - key: SERVICE_PROXY_AGENT_BUCKET
      value: ${VM_AGENT_BUCKET}
    - key: _CI_ASM_IMAGE_TAG
      value: ${TAG}
EOF
}
