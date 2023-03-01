#!/bin/bash
set +e
set -x

### README  ####################################################################################################
# Creates CAS pools in the ASM CI project to be used commonly for all testing
###############################################################################################################

TEST_PROJECT="istio-prow-build"
ASM_ROOT_POOL_PREFIX="asm-testci-root-pool"
ASM_SUB_POOL_PREFIX="asm-testci-sub-pool"
ASM_ROOT_CA_PREFIX="asm-testci-root-ca"
ASM_SUB_CA_PREFIX="asm-testci-sub-ca"
ASM_ROOT_POOL_LOC="us-central1"
ASM_ROOT_CA_NUM=4
ASM_SUB_CA_NUM=4

function init_root() {
  gcloud privateca pools create ${ASM_ROOT_POOL_PREFIX} --location ${ASM_ROOT_POOL_LOC} --project ${TEST_PROJECT}
  for i in $(seq 1 1 ${ASM_ROOT_CA_NUM}); do
    SUFFIX1="--location ${ASM_ROOT_POOL_LOC} --project ${TEST_PROJECT} --pool ${ASM_ROOT_POOL_PREFIX}"
    SUFFIX2="--subject CN=${ASM_ROOT_CA_PREFIX}-${i},O=ASM-TEST-CI --auto-enable --quiet"
    gcloud privateca roots create ${ASM_ROOT_CA_PREFIX}-${i} ${SUFFIX1} ${SUFFIX2}
  done

}

function init_sub() {
  LOCATION="$1"
  gcloud privateca pools create "${ASM_SUB_POOL_PREFIX}-${LOCATION}" --location ${LOCATION} --project ${TEST_PROJECT}
  if [ -f "policy.yaml" ]; then
    gcloud privateca pools update "${ASM_SUB_POOL_PREFIX}-${LOCATION}" --location ${LOCATION} --project ${TEST_PROJECT} --issuance-policy policy.yaml
  fi
  for i in $(seq 1 1 ${ASM_SUB_CA_NUM}); do
    SUFFIX1="--location ${LOCATION} --project ${TEST_PROJECT} --pool ${ASM_SUB_POOL_PREFIX}-${LOCATION}"
    SUFFIX2="--issuer-pool ${ASM_ROOT_POOL_PREFIX} --issuer-location ${ASM_ROOT_POOL_LOC}"
    SUFFIX3="--subject CN=${ASM_SUB_CA_PREFIX}-${LOCATION}-${i},O=ASM-TEST-CI --auto-enable --quiet"
    gcloud privateca subordinates create ${ASM_SUB_CA_PREFIX}-${LOCATION}-${i} ${SUFFIX1} ${SUFFIX2} ${SUFFIX3}
    sleep 5
  done

}

function setup() {
  init_root
  if [[ -z "${1}" ]]; then
    echo "Please input region file"
  fi

  while IFS= read -r region; do
      echo "Processing region $region .."
      init_sub "${region}"
  done <$1
}

setup "$1"
