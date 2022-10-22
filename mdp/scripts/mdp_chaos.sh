#!/usr/bin/env bash

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

set -eu # Print out all commands, exit on failed commands
set -o pipefail

CLUSTER_NAME="mdp-prober-chaos"
BASE_NAME="mdp-prober-chaos"
NUM_NODES=2
MIN_NODES=2
MAX_NODES=3
MACHINE_TYPE=${MACHINE_TYPE:-e2-standard-4}
CLUSTER_LOCATION="us-central1-a"
PROJECT_ID="iamwen-gke-dev"
CLUSTERS_INTERVAL=30s
gcloud config set project ${PROJECT_ID}

# if staging
gcloud config set api_endpoint_overrides/gkehub https://staging-gkehub.sandbox.googleapis.com/
#trap 'gcloud config unset api_endpoint_overrides/gkehub' EXIT
## else
#gcloud config unset api_endpoint_overrides/gkehub

function create_cluster() {
    local CLUSTER_NAME=$1
    echo "creating cluster ${CLUSTER_NAME}"
    gcloud container clusters create "${CLUSTER_NAME}" --project "${PROJECT_ID}" --zone "${CLUSTER_LOCATION}" \
    --num-nodes "${NUM_NODES}" --enable-autoscaling --min-nodes "${MIN_NODES}" --max-nodes "${MAX_NODES}" --machine-type "${MACHINE_TYPE}" \
     --workload-pool=${PROJECT_ID}.svc.id.goog || true
    gcloud container clusters get-credentials "${CLUSTER_NAME}" --zone "${CLUSTER_LOCATION}" --project "${PROJECT_ID}"
}

function install_asm() {
  local CLUSTER_NAME=$1
  echo "installing asm"
  # asmcli or fleet api
  gcloud alpha container hub mesh update \
     --control-plane automatic \
     --membership "${CLUSTER_NAME}" --project "${PROJECT_ID}" --quiet || true
}

function delete_cluster() {
  local CLUSTER_NAME=$1
  echo "deleting cluster ${CLUSTER_NAME}"
  gcloud container clusters delete "${CLUSTER_NAME}" --project "${PROJECT_ID}" --zone "${CLUSTER_LOCATION}" -q
}

function setup_membership_enable_feature() {
  local CLUSTER_NAME=$1
  echo "setting up membership and enabling feature"
  gcloud container hub memberships register "${CLUSTER_NAME}" \
   --gke-cluster="${CLUSTER_LOCATION}/${CLUSTER_NAME}" \
   --enable-workload-identity --project "${PROJECT_ID}" -q
  gcloud container hub mesh enable --project=${PROJECT_ID} -q
}

function delete_membership () {
  local CLUSTER_NAME=$1
  echo "deleting membership ${CLUSTER_NAME}"
  gcloud container hub memberships delete "${CLUSTER_NAME}" -q  || true
}

function creation_flow () {
    local name=$1
    create_cluster "${name}"
    setup_membership_enable_feature "${name}"
    install_asm "${name}"
}

function membership_oprations_only_cluster() {
  name="mdp-prober-chaos-membership-only"
  creation_flow "${name}"
  c=1
  while [[ "${c}" -le 1000 ]]
  do
    echo "Run membership deletion/creation loop $c times"
    delete_membership "${name}"
    sleep 15
    setup_membership_enable_feature "${name}"
  done
}

# the parallelism is controlled by the cloudrun tasks.
# so we do not need to create a loop here
# add a random suffix so multiple tasks would not collide with each other
suffix=$(cut -d '-' -f 1 /proc/sys/kernel/random/uuid)

for channel in regular rapid stable;do
  CLUSTER="${BASE_NAME}-${channel}-${suffix}"
  creation_flow "${CLUSTER}"
  sleep "${CLUSTERS_INTERVAL}"
done
for channel in regular rapid stable;do
  CLUSTER="${BASE_NAME}-${channel}-${suffix}"
  delete_cluster "${CLUSTER}"
  delete_membership "${CLUSTER}"
  sleep "${CLUSTERS_INTERVAL}"
done
