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

# shellcheck disable=SC2034
# holds multiple kubeconfigs for Multicloud test environments
declare -a MC_CONFIGS
# shellcheck disable=SC2034
IFS=':' read -r -a MC_CONFIGS <<< "${KUBECONFIG}"

function buildx-create() {
  export DOCKER_CLI_EXPERIMENTAL=enabled
  if ! docker buildx ls | grep -q container-builder; then
    docker buildx create --driver-opt network=host,image=gcr.io/istio-testing/buildkit:buildx-stable-1 --name container-builder
    # Pre-warm the builder. If it fails, fetch logs, but continue
    docker buildx inspect --bootstrap container-builder || docker logs buildx_buildkit_container-builder0 || true
  fi
  docker buildx use container-builder
}

# Process kubeconfig files to make sure current-context in each file
# is set correctly.
# Depends on env var ${KUBECONFIG}
function process_kubeconfigs() {
  local KUBECONFIGPATHS
  IFS=":" read -r -a KUBECONFIGPATHS <<< "${KUBECONFIG}"
  # Each kubeconfig file should have one and only one cluster context.
  for i in "${!KUBECONFIGPATHS[@]}"; do
    local CONTEXT_STR
    CONTEXT_STR=$(kubectl config view -o jsonpath="{.contexts[0].name}" --kubeconfig="${KUBECONFIGPATHS[$i]}")
    kubectl config use-context "${CONTEXT_STR}" --kubeconfig="${KUBECONFIGPATHS[$i]}"
  done

  for i in "${!KUBECONFIGPATHS[@]}"; do
    kubectl config view --kubeconfig="${KUBECONFIGPATHS[$i]}"
  done
}

# Prepare images required for the e2e test.
# Depends on env var ${HUB} and ${TAG}
function prepare_images() {
  buildx-create
  # Configure Docker to authenticate with Container Registry.
  gcloud auth configure-docker
  # Build images from the current branch and push the images to gcr.
  make dockerx.pushx HUB="${HUB}" TAG="${TAG}" DOCKER_TARGETS="docker.pilot docker.proxyv2 docker.cloudesf docker.app docker.install-cni docker.mdp"

  docker pull gcr.io/asm-staging-images/asm/stackdriver-prometheus-sidecar:e2e-test
  docker tag gcr.io/asm-staging-images/asm/stackdriver-prometheus-sidecar:e2e-test "${HUB}/stackdriver-prometheus-sidecar:${TAG}"
  docker push "${HUB}/stackdriver-prometheus-sidecar:${TAG}"
}

# Prepare images (istiod, proxyv2) required for the managed control plane e2e test.
# Depends on env var ${HUB} and ${TAG}
# If testing addon migration, addon-migration job will be built as well.
function prepare_images_for_managed_control_plane() {
  local FEATURE_TO_TEST=${1:-};
  local DOCKER_TARGETS="docker.cloudrun docker.proxyv2 docker.cloudesf docker.app docker.install-cni docker.mdp"
  if [[ "${FEATURE_TO_TEST}" == "ADDON" ]];then
    DOCKER_TARGETS="${DOCKER_TARGETS} docker.addon-migration"
  fi
  buildx-create
  # Configure Docker to authenticate with Container Registry.
  gcloud auth configure-docker
  # Build images from the current branch and push the images to gcr.
  HUB="${HUB}" TAG="${TAG}" DOCKER_TARGETS="${DOCKER_TARGETS}" make dockerx.pushx
}

# Build istioctl in the current branch to install ASM.
function build_istioctl() {
  make istioctl
  cp "$PWD/out/linux_amd64/istioctl" "/usr/local/bin"
}

# Register the clusters into the Hub of the Hub host project.
# Parameters: $1 - Hub host project
#             $2 - array of k8s contexts
function register_clusters_in_hub() {
  local GKEHUB_PROJECT_ID=$1; shift
  local CONTEXTS=("${@}")
  local ENVIRON_PROJECT_NUMBER
  ENVIRON_PROJECT_NUMBER=$(gcloud projects describe "${GKEHUB_PROJECT_ID}" --format="value(projectNumber)")

  # The staging hub environments are needed for testing ASM Feature Controller
  # in both staging and prod. Members registered to staging GKE Hub will use
  # staging AFC, and members registered to prod GKE Hub will use prod AFC.

  # Create Hub service account for the Hub host project
  gcloud beta services identity create --service=gkehub.googleapis.com --project="${GKEHUB_PROJECT_ID}"
  gcloud beta services identity create --service=staging-gkehub.sandbox.googleapis.com --project="${GKEHUB_PROJECT_ID}"
  # Create Mesh service account for the Hub host project
  gcloud beta services identity create --service=meshconfig.googleapis.com --project="${GKEHUB_PROJECT_ID}"
  gcloud beta services identity create --service=staging-meshconfig.sandbox.googleapis.com --project="${GKEHUB_PROJECT_ID}"

  for i in "${!CONTEXTS[@]}"; do
    IFS="_" read -r -a VALS <<< "${CONTEXTS[$i]}"
    echo "Hub registration for ${CONTEXTS[$i]}"
    local PROJECT_ID=${VALS[1]}
    local CLUSTER_LOCATION=${VALS[2]}
    local CLUSTER_NAME=${VALS[3]}

    # The staging hub service accounts are needed for testing ASM Feature Controller
    # in both staging and prod. Members registered to staging GKE Hub will use
    # staging AFC, and members registered to prod GKE Hub will use prod AFC.
    # This means we need IAM bindings for both prod and staging SAs.

    # Add IAM binding for Hub SA in the Hub connect project
    gcloud projects add-iam-policy-binding "${PROJECT_ID}" \
      --member "serviceAccount:service-${ENVIRON_PROJECT_NUMBER}@gcp-sa-gkehub.iam.gserviceaccount.com" \
      --role roles/gkehub.serviceAgent
    gcloud projects add-iam-policy-binding "${PROJECT_ID}" \
      --member "serviceAccount:service-${ENVIRON_PROJECT_NUMBER}@gcp-sa-staging-gkehub.iam.gserviceaccount.com" \
      --role roles/gkehub.serviceAgent
    # Add IAM binding for Mesh SA in the Hub connect project
    gcloud projects add-iam-policy-binding "${PROJECT_ID}" \
      --member "serviceAccount:service-${ENVIRON_PROJECT_NUMBER}@gcp-sa-servicemesh.iam.gserviceaccount.com" \
      --role roles/anthosservicemesh.serviceAgent
    gcloud projects add-iam-policy-binding "${PROJECT_ID}" \
      --member "serviceAccount:service-${ENVIRON_PROJECT_NUMBER}@gcp-sa-staging-servicemesh.iam.gserviceaccount.com" \
      --role roles/anthosservicemesh.serviceAgent
    # Sleep 2 mins to wait for the IAM binding to take into effect.
    # TODO(chizhg,tairan): remove the sleep after http://b/190864991 is fixed.
    sleep 2m
  done
  echo "These are the Hub Memberships within Host project ${GKEHUB_PROJECT_ID}"
  gcloud beta container hub memberships list --project="${GKEHUB_PROJECT_ID}"
}

# Clean up multiproject permissions removes excessive project bindings from the
# meshconfig p4sa used in ASM and MCP. Excessive permissions on the P4SA causes
# Meshconfig.Initialize to fail, subsequently causing ASM/MCP installation to fail
# (b/219832876).
# TODO(@aakashshukla): Remove this function once project-level control plane deprovisioning is implemented.
# Parameters: $1: Fleet Project
#             $2: Array of Cluster Projects
function clean_up_multiproject_permissions() {
  local FLEET_ID=$1; shift
  local PROJECTS=("${@}")

  echo "cleaning up excessive bindings on the projects meshdataplane service account"
  for i in "${PROJECTS[@]}"; do
    POST_DATA='{semantics:"REPLACE"}'
    curl --request POST --header 'X-Server-Timeout: 600' --header "Authorization: Bearer $(gcloud auth print-access-token)" \
        --header "Content-Type: application/json"     --data "${POST_DATA}"  \
        https://staging-meshconfig.sandbox.googleapis.com/v1alpha1/projects/"${i}":initialize

    curl --request POST --header 'X-Server-Timeout: 600' --header "Authorization: Bearer $(gcloud auth print-access-token)" \
            --header "Content-Type: application/json"     --data "${POST_DATA}"  \
            https://meshconfig.googleapis.com/v1alpha1/projects/"${i}":initialize
  done
}

# Creates ca certs on the cluster
# $1    kubeconfig
function install_certs() {
  kubectl create secret generic cacerts -n istio-system \
    --kubeconfig="$1" \
    --from-file=samples/certs/ca-cert.pem \
    --from-file=samples/certs/ca-key.pem \
    --from-file=samples/certs/root-cert.pem \
    --from-file=samples/certs/cert-chain.pem
}

# Creates remote secrets for each cluster pair for all the clusters under test
function configure_remote_secrets_for_baremetal() {
  declare -a HTTP_PROXYS
  IFS=',' read -r -a HTTP_PROXYS <<< "${HTTP_PROXY_LIST}"
  declare -a BM_ARTIFACTS_PATH_SET
  declare -a BM_HOST_IP_SET
  declare -a BM_CLUSTER_NAME_SET
  for i in "${!MC_CONFIGS[@]}"; do
    local BM_ARTIFACTS_PATH_LOCAL
    BM_ARTIFACTS_PATH_LOCAL=${MC_CONFIGS[$i]%/*}
    BM_ARTIFACTS_PATH_SET+=( "${BM_ARTIFACTS_PATH_LOCAL}")
    local PORT_NUMBER
    local BM_HOST_IP_LOCAL
    read -r PORT_NUMBER BM_HOST_IP_LOCAL <<<"$(grep "localhost" "${BM_ARTIFACTS_PATH_LOCAL}/tunnel.sh" | sed 's/.*\-L\([0-9]*\):localhost.* root@\([0-9]*\.[0-9]*\.[0-9]*\.[0-9]*\) -N/\1 \2/')"
    BM_HOST_IP_SET+=( "${BM_HOST_IP_LOCAL}")
    local BM_CLUSTER_NAME
    BM_CLUSTER_NAME="cluster${i}"
    BM_CLUSTER_NAME_SET+=( "${BM_CLUSTER_NAME}")
    echo "For index ${i}, BM_CLUSTER_NAME: ${BM_CLUSTER_NAME}, BM_ARTIFACTS_PATH: ${BM_ARTIFACTS_PATH_LOCAL}, BM_HOST_IP: ${BM_HOST_IP_LOCAL}, proxy port: ${PORT_NUMBER}"
  done
  for i in "${!MC_CONFIGS[@]}"; do
    for j in "${!MC_CONFIGS[@]}"; do
      if [[ "$i" != "$j" ]]; then
        HTTPS_PROXY=${HTTP_PROXYS[$j]} istioctl x create-remote-secret \
          --kubeconfig="${MC_CONFIGS[$j]}" \
          --name="${BM_CLUSTER_NAME_SET[$j]}" > "secret-${j}"
        local ORIGINAL_IP
        ORIGINAL_IP=$(grep -oP '(?<=https://)\d+(\.\d+){3}' "secret-${j}")
        ssh -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no -i "${BM_ARTIFACTS_PATH_SET[$j]}"/id_rsa root@"${BM_HOST_IP_SET[$j]}" "iptables -t nat -I PREROUTING 1 -i ens4 -p tcp -m tcp --dport 8118 -j DNAT --to-destination ${ORIGINAL_IP}:443"
        ssh -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no -i "${BM_ARTIFACTS_PATH_SET[$j]}"/id_rsa root@"${BM_HOST_IP_SET[$j]}" "iptables -I FORWARD 1 -d ${ORIGINAL_IP}/32 -p tcp -m tcp --dport 443 -j ACCEPT"
        ssh -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no -i "${BM_ARTIFACTS_PATH_SET[$j]}"/id_rsa root@"${BM_HOST_IP_SET[$j]}" "iptables -t nat -I POSTROUTING 1 -d ${ORIGINAL_IP}/32 -o vxlan0 -j MASQUERADE"
        ssh -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no -i "${BM_ARTIFACTS_PATH_SET[$j]}"/id_rsa root@"${BM_HOST_IP_SET[$j]}" "iptables -A FORWARD -i vxlan0 -m state --state RELATED,ESTABLISHED -j ACCEPT"
        ssh -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no -i "${BM_ARTIFACTS_PATH_SET[$j]}"/id_rsa root@"${BM_HOST_IP_SET[$j]}" "service privoxy restart"
        local REACH_IP
        REACH_IP=$(ssh -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no -i "${BM_ARTIFACTS_PATH_SET[$j]}"/id_rsa root@"${BM_HOST_IP_SET[$j]}" "ip -4 addr show ens4 | grep -oP '(?<=inet\s)\d+(\.\d+){3}'")
        sed -i 's/server\:.*/server\: https:\/\/'"${REACH_IP}:8118"'/' "secret-${j}"
        sed -i 's/certificate-authority-data\:.*/insecure-skip-tls-verify\: true/' "secret-${j}"
        HTTPS_PROXY=${HTTP_PROXYS[$i]} kubectl apply --kubeconfig="${MC_CONFIGS[$i]}" -f "secret-${j}"
      fi
    done
  done
  for i in "${!MC_CONFIGS[@]}"; do
    sed -i 's/certificate-authority-data\:.*/insecure-skip-tls-verify\: true/' "${MC_CONFIGS[$i]}"
  done
}

# Creates remote secrets for each cluster pair for all the clusters under test
function configure_remote_secrets_for_gcp_baremetal_hybrid() {
  declare -a HTTP_PROXYS
  IFS=',' read -r -a HTTP_PROXYS <<< "${HTTP_PROXY_LIST}"
  declare GCP_CONFIG
  declare BM_CONFIG
  declare BM_PROXY

  for i in "${!MC_CONFIGS[@]}"; do
    if [[ ${MC_CONFIGS[$i]} == *"artifacts/kubeconfig"* ]]; then
      BM_CONFIG="${MC_CONFIGS[$i]}"
      BM_PROXY="${HTTP_PROXYS[$i]}"
    else
      GCP_CONFIG="${MC_CONFIGS[$i]}"
    fi
  done

  echo "BM_CONFIG: ${BM_CONFIG}, BM_PROXY: ${BM_PROXY}, GCP_CONFIG: ${GCP_CONFIG}"

  # Generate GCP remote secret
  istioctl x create-remote-secret \
    --kubeconfig="${GCP_CONFIG}" \
    --name="cluster-gcp" > "secret-gcp"

  # Generate BM remote secret
  HTTPS_PROXY=${BM_PROXY} istioctl x create-remote-secret \
    --kubeconfig="${BM_CONFIG}" \
    --name="cluster-bm" > "secret-bm"
  sed -i 's/certificate-authority-data\:.*/insecure-skip-tls-verify\: true/' "secret-bm"

  # Apply GCP secret to BM cluster
  HTTPS_PROXY=${BM_PROXY} kubectl apply --kubeconfig="${BM_CONFIG}" -f "secret-gcp"

  # Apply BM secret to GCP cluster
  kubectl apply --kubeconfig="${GCP_CONFIG}" -f "secret-bm"
}

# on-prem specific fucntion to configure external ips for the ingress gateway
# Parameters:
# $1    kubeconfig
function onprem::configure_ingress_ip() {
  local HERC_ENV_ID
  HERC_ENV_ID=$(echo "$1" | rev | cut -d '/' -f 2 | rev)
  local INGRESS_ID=\"lb-test-ip\"
  local INGRESS_IP

  echo "Installing herc CLI..."
  gsutil cp "gs://anthos-hercules-public-artifacts/herc/latest/herc" "/usr/local/bin/" && chmod 755 "/usr/local/bin/herc"

  INGRESS_IP=$(herc getEnvironment "${HERC_ENV_ID}" -o json | \
    jq -r ".environment.resources.vcenter_server.datacenter.networks.fe.ip_addresses.${INGRESS_ID}.ip_address")

  # Inject the external IP for Ingress GW
  echo "----------Configuring external IP for ingress gw----------"
  kubectl patch svc istio-ingressgateway -n istio-system \
    --type='json' -p '[{"op": "add", "path": "/spec/loadBalancerIP", "value": "'"${INGRESS_IP}"'"}]' \
    --kubeconfig="$1"
}

# on-prem specific fucntion to configure external ips for the expansion gateway
# Parameters:
# $1    kubeconfig
function onprem::configure_expansion_ip() {
  local HERC_ENV_ID
  HERC_ENV_ID=$(echo "$1" | rev | cut -d '/' -f 2 | rev)
  local EXPANSION_ID=\"expansion-ip\"

  echo "Installing herc CLI..."
  gsutil cp "gs://anthos-hercules-public-artifacts/herc/latest/herc" "/usr/local/bin/" && chmod 755 "/usr/local/bin/herc"

  # Request additional external IP for expansion gw
  local HERC_PARENT
  HERC_PARENT=$(herc getEnvironment "${HERC_ENV_ID}" | \
    grep "name: environments.*lb-test-ip$" | awk -F' ' '{print $2}' | sed 's/\/ips\/lb-test-ip//')
  local EXPANSION_IP
  EXPANSION_IP=$(herc getEnvironment "${HERC_ENV_ID}" -o json | \
    jq -r ".environment.resources.vcenter_server.datacenter.networks.fe.ip_addresses.${EXPANSION_ID}.ip_address")
  if [[ -z "${EXPANSION_IP}" || "${EXPANSION_IP}" == "null" ]]; then
    echo "Requesting herc for expansion IP"
    herc allocateIPs --parent "${HERC_PARENT}" -f "${CONFIG_DIR}/herc/expansion-ip.yaml"
    EXPANSION_IP=$(herc getEnvironment "${HERC_ENV_ID}" -o json | \
      jq -r ".environment.resources.vcenter_server.datacenter.networks.fe.ip_addresses.${EXPANSION_ID}.ip_address")
  else
    echo "Using ${EXPANSION_IP} as the expansion IP"
  fi

  # Inject the external IP for expansion GW
  echo "----------Configuring external IP for expansion gw----------"
  kubectl patch svc istio-eastwestgateway -n istio-system \
    --type='json' -p '[{"op": "add", "path": "/spec/loadBalancerIP", "value": "'"${EXPANSION_IP}"'"}]' \
    --kubeconfig="$1"
}

# baremetal specific fucntion to configure external ips for the eastwest gateway
# Parameters:
# $1    kubeconfig
function baremetal::configure_external_ip() {
  local BM_KUBECONFIG="$1"
  local BM_ARTIFACTS_PATH_LOCAL
  BM_ARTIFACTS_PATH_LOCAL=${BM_KUBECONFIG%/*}
  local EXPANSION_IP
  EXPANSION_IP="$(jq '.outputs.full_cluster.value.resources.networks.gce.ips."gce-vip-0"' "$BM_ARTIFACTS_PATH_LOCAL"/terraform.tfstate)"
  echo "----------Configuring external IP for expansion gw----------"
  kubectl patch svc istio-eastwestgateway -n istio-system \
    --type='json' -p '[{"op": "add", "path": "/spec/loadBalancerIP", "value": '"${EXPANSION_IP}"'}]' \
    --kubeconfig="$1"
}

function baremetal::hybrid_configure_external_ip() {
  local BM_KUBECONFIG="$1"
  local BM_ARTIFACTS_PATH_LOCAL
  BM_ARTIFACTS_PATH_LOCAL=${BM_KUBECONFIG%/*}
  local EXPANSION_IP
  EXPANSION_IP="$(jq '.outputs.full_cluster.value.resources.networks.gce.ips."gce-vip-0"' "$BM_ARTIFACTS_PATH_LOCAL"/terraform.tfstate)"
  echo "----------Configuring external IP for expansion gw----------"
  kubectl patch svc istio-eastwestgateway -n istio-system \
    --type='json' -p '[{"op": "add", "path": "/spec/loadBalancerIP", "value": '"${EXPANSION_IP}"'}]' \
    --kubeconfig="$1"
}

# Configures validating webhook for istiod
# Parameters:
# $1    revision
# $2    kubeconfig
function configure_validating_webhook() {
  echo "----------Configuring validating webhook----------"
  cat <<EOF | kubectl apply --kubeconfig="$2" -f -
apiVersion: v1
kind: Service
metadata:
 name: istiod
 namespace: istio-system
 labels:
   istio.io/rev: ${1}
   app: istiod
   istio: pilot
   release: istio
spec:
 ports:
   - port: 15010
     name: grpc-xds # plaintext
     protocol: TCP
   - port: 15012
     name: https-dns # mTLS with k8s-signed cert
     protocol: TCP
   - port: 443
     name: https-webhook # validation and injection
     targetPort: 15017
     protocol: TCP
   - port: 15014
     name: http-monitoring # prometheus stats
     protocol: TCP
 selector:
   app: istiod
   istio.io/rev: ${1}
EOF
}

#1: add/remove IAM policy binding
#2: ca pool name
#3: ca pool location
#4: Workload identity pool
#5: project Id
#6: ca certificate template
function amend_privateca_iam() {
  # Remove IAM bindings on ca-pool and certificate template

  gcloud privateca pools "$1" "$2" --location "$3" --member "$4" --project "$5" \
    --role "roles/privateca.workloadCertificateRequester" --quiet &> /dev/null

  gcloud privateca pools "$1" "$2" --location "$3" --member "$4" --project "$5" \
    --role "roles/privateca.auditor" --quiet &> /dev/null

  if [[ -n "$6" ]]; then
    gcloud privateca templates "$1" "$6" --location "$3" --member "$4" --project "$5" \
      --role "roles/privateca.templateUser" --quiet &> /dev/null
  fi
}
