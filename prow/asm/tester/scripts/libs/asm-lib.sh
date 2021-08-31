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

readonly ROOT_CA_ID_PREFIX="asm-test-root-ca"
readonly ROOT_CA_LOC="us-central1"
readonly SUB_CA_ID_PREFIX="asm-test-sub-ca"

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
function prepare_images_for_managed_control_plane() {
  buildx-create
  # Configure Docker to authenticate with Container Registry.
  gcloud auth configure-docker
  # Build images from the current branch and push the images to gcr.
  HUB="${HUB}" TAG="${TAG}" DOCKER_TARGETS="docker.cloudrun docker.proxyv2 docker.cloudesf docker.app docker.install-cni docker.mdp" make dockerx.pushx
}

# Build istioctl in the current branch to install ASM.
function build_istioctl() {
  make istioctl
  cp "$PWD/out/linux_amd64/istioctl" "/usr/local/bin"
}


# Clean up stale hub memberships
function cleanup_stale_hub_memberships() {
  echo "Clean up stale hub memberships in the project tairan-asm-multi-cloud-dev"
  # Clean up hub memberships that are older than 8-hour.
  # https://buganizer.corp.google.com/issues/195998781, comment #10
  gcloud --project=tairan-asm-multi-cloud-dev container hub memberships list \
    --format='value(name)' --filter="updateTime<-P8H" \
    | xargs --no-run-if-empty -n1 gcloud --project=tairan-asm-multi-cloud-dev container hub memberships delete --quiet
}

# Register the clusters into the Hub of the Hub host project.
# Parameters: $1 - Hub host project
#             $2 - array of k8s contexts
function register_clusters_in_hub() {
  local GKEHUB_PROJECT_ID=$1; shift
  local CONTEXTS=("${@}")
  local ENVIRON_PROJECT_NUMBER
  ENVIRON_PROJECT_NUMBER=$(gcloud projects describe "${GKEHUB_PROJECT_ID}" --format="value(projectNumber)")

  # Create Hub service account for the Hub host project
  gcloud beta services identity create --service=gkehub.googleapis.com --project="${GKEHUB_PROJECT_ID}"

  for i in "${!CONTEXTS[@]}"; do
    IFS="_" read -r -a VALS <<< "${CONTEXTS[$i]}"
    echo "Hub registration for ${CONTEXTS[$i]}"
    local PROJECT_ID=${VALS[1]}
    local CLUSTER_LOCATION=${VALS[2]}
    local CLUSTER_NAME=${VALS[3]}
    # Add IAM binding for Hub SA in the Hub connect project
    gcloud projects add-iam-policy-binding "${PROJECT_ID}" \
      --member "serviceAccount:service-${ENVIRON_PROJECT_NUMBER}@gcp-sa-gkehub.iam.gserviceaccount.com" \
      --role roles/gkehub.serviceAgent
    # Sleep 2 mins to wait for the IAM binding to take into effect.
    # TODO(chizhg,tairan): remove the sleep after http://b/190864991 is fixed.
    sleep 2m
    # This is the user guide for registering clusters within Hub
    # https://cloud.devsite.corp.google.com/service-mesh/docs/register-cluster
    # Verify two ways of Hub registration
    # if the cluster is in the Hub host project
    if [[ "${PROJECT_ID}" == "${GKEHUB_PROJECT_ID}" ]]; then
      gcloud beta container hub memberships register "${CLUSTER_NAME}" --project="${PROJECT_ID}" \
        --gke-cluster="${CLUSTER_LOCATION}"/"${CLUSTER_NAME}" \
        --enable-workload-identity \
        --quiet
    # if the cluster is in the connect project
    else
      gcloud beta container hub memberships register "${CLUSTER_NAME}" --project="${GKEHUB_PROJECT_ID}" \
        --gke-uri=https://container.googleapis.com/v1/projects/"${PROJECT_ID}"/locations/"${CLUSTER_LOCATION}"/clusters/"${CLUSTER_NAME}" \
        --enable-workload-identity \
        --quiet
    fi
  done
  echo "These are the Hub Memberships within Host project ${GKEHUB_PROJECT_ID}"
  gcloud beta container hub memberships list --project="${GKEHUB_PROJECT_ID}"
}

# Setup the private CAs.
# Parameters: $1 - comma-separated string of k8s contexts
function setup_private_ca() {
  IFS="," read -r -a CONTEXTS <<< "$1"

  local ROOT_CA_ID="${ROOT_CA_ID_PREFIX}-${BUILD_ID}"
  # Create root CA in the central project if it does not exist.
  if ! gcloud beta privateca roots list --project "${SHARED_GCP_PROJECT}" --location "${ROOT_CA_LOC}" | grep -q "${ROOT_CA_ID}"; then
    echo "Creating root CA ${ROOT_CA_ID}..."
    gcloud beta privateca roots create "${ROOT_CA_ID}" \
      --location "${ROOT_CA_LOC}" \
      --project "${SHARED_GCP_PROJECT}" \
      --subject "CN=ASM Test Root CA, O=Google LLC" \
      --tier enterprise \
      --reusable-config root-unconstrained \
      --key-algorithm rsa-pkcs1-4096-sha256
  fi

  # Contains temporary files for subordinate CA activation.
  WORKING_DIR=$(mktemp -d)
  for i in "${!CONTEXTS[@]}"; do
    IFS="_" read -r -a VALS <<< "${CONTEXTS[$i]}"
    PROJECT_ID="${VALS[1]}"
    LOCATION="${VALS[2]}"
    CLUSTER="${VALS[3]}"
    local SUBORDINATE_CA_ID="${SUB_CA_ID_PREFIX}-${BUILD_ID}-${CLUSTER}"
    local CSR_FILE="${WORKING_DIR}/${SUBORDINATE_CA_ID}.csr"
    local CERT_FILE="${WORKING_DIR}/${SUBORDINATE_CA_ID}.crt"
    local WORKLOAD_IDENTITY="$PROJECT_ID.svc.id.goog[istio-system/istiod-service-account]"
    if ! gcloud beta privateca subordinates list --location "${LOCATION}" --project "${PROJECT_ID}" | grep -q "${SUBORDINATE_CA_ID}"; then
      echo "Creating subordinate CA ${SUBORDINATE_CA_ID}..."
      gcloud beta privateca subordinates create "${SUBORDINATE_CA_ID}" \
        --location "${LOCATION}" \
        --project "${PROJECT_ID}" \
        --subject "CN=ASM Test Subordinate CA, O=Google LLC" \
        --tier devops \
        --reusable-config "subordinate-mtls-pathlen-0" \
        --key-algorithm rsa-pkcs1-2048-sha256 \
        --create-csr \
        --csr-output-file "${CSR_FILE}"

      CERT_ID="cert-${SUBORDINATE_CA_ID}"
      echo "Signing subordinate CA certificate ${CERT_ID}.."
      gcloud beta privateca certificates create "${CERT_ID}" \
        --issuer "${ROOT_CA_ID}" \
        --issuer-location "${ROOT_CA_LOC}" \
        --project "${SHARED_GCP_PROJECT}" \
        --csr "${CSR_FILE}" \
        --cert-output-file "${CERT_FILE}" \
        --validity "P3Y" # Change this as needed - 3Y is the default for subordinate CAs.

      echo "Activating subordinate CA.."
      gcloud beta privateca subordinates activate "${SUBORDINATE_CA_ID}" \
        --location "${LOCATION}" \
        --project "${PROJECT_ID}" \
        --pem-chain "${CERT_FILE}"
    fi
    gcloud beta privateca subordinates add-iam-policy-binding "${SUBORDINATE_CA_ID}" \
      --location "${LOCATION}" \
      --project "${PROJECT_ID}" \
      --member "serviceAccount:$WORKLOAD_IDENTITY" \
      --role "roles/privateca.certificateManager" \
      --quiet
  done
}

# Cleanup the private CAs.
function cleanup_private_ca() {
  # Install the uuid tool to generate uuid for the curl command to purge CA in
  # the end.
  apt-get update
  apt-get install uuid -y

  for i in "${!CONTEXTS[@]}"; do
    IFS="_" read -r -a VALS <<< "${CONTEXTS[$i]}"
    PROJECT_ID="${VALS[1]}"
    LOCATION="${VALS[2]}"
    CLUSTER="${VALS[3]}"

    local SUBORDINATE_CA_ID="${SUB_CA_ID_PREFIX}-${BUILD_ID}-${CLUSTER}"
    if gcloud beta privateca subordinates list --project "${PROJECT_ID}" --location "${LOCATION}" | grep -q "${SUBORDINATE_CA_ID}"; then
      echo "Purging subordinate CA $SUBORDINATE_CA_ID.."
      purge-ca "subordinates" "${SUBORDINATE_CA_ID}" "${LOCATION}" "${PROJECT_ID}"
    fi
  done
  local ROOT_CA_ID="${ROOT_CA_ID_PREFIX}-${BUILD_ID}"
  if gcloud beta privateca roots list --project "${SHARED_GCP_PROJECT}" --location "${ROOT_CA_LOC}" | grep -q "${ROOT_CA_ID}"; then
    echo "Purging root CA $ROOT_CA_ID.."
    purge-ca "roots" "${ROOT_CA_ID}" "${ROOT_CA_LOC}" "${SHARED_GCP_PROJECT}"
  fi
}

# Purge the CA.
# Parameters: $1 - the command group (roots|subordinates)
#             $2 - the full CA resource name
#             $3 - location
#             $4 - project ID
function purge-ca() {
  echo "Purging CA '$2'."
  local certs
  # shellcheck disable=SC2207
  certs=($(gcloud beta privateca certificates list \
    --issuer "$2" \
    --format 'table(name.scope(), revocation_details)' \
    --location="$3" --project="$4" | grep -Po '([^\s]+)(?=\s+ACTIVE)' || true))

  echo "> Revoking ${#certs[@]} certificates.." >&2

  for cert in "${certs[@]}"; do
    gcloud beta privateca certificates revoke --certificate "$cert" --quiet
  done

  if gcloud beta privateca "$1" describe "$2" --format "value(state)" --location="$3" --project="$4" | grep -q "ENABLED" ; then
    echo "> Disabling and deleting CA.."
    gcloud beta privateca "$1" disable "$2" --location="$3" --project="$4" --quiet
    # As suggested in
    # https://buganizer.corp.google.com/issues/179162450#comment10, delete root
    # CA with the curl command instead of calling `gcloud beta privateca roots
    # delete`
    if [[ "$4" == "${SHARED_GCP_PROJECT}" ]]; then
      curl \
        -H "Authorization: Bearer $(gcloud auth print-access-token)" \
        -H "Content-Type: application/json" \
        -X POST \
        -d '{ "deletePendingDuration": "0s" }' \
        "https://privateca.googleapis.com/v1beta1/projects/$4/locations/$3/certificateAuthorities/$2:scheduleDelete?requestId=$(uuid)"
    else
      gcloud beta privateca "$1" delete "$2" --location="$3" --project="$4" --quiet
    fi
  fi
}

# Install ASM on the clusters.
# Parameters: $1 - WIP: GKE or HUB
# Depends on env var ${HUB} and ${TAG}
# TODO(gzip) remove this function once b/176177944 is fixed
function install_asm_on_multicloud() {
  local WIP="$1"; shift
  local MESH_ID="test-mesh"

  export HERCULES_CLI_LAB="atl_shared"
  USER=${USER:-prowuser}
  export USER

  for i in "${!MC_CONFIGS[@]}"; do
    install_certs "${MC_CONFIGS[$i]}"

    echo "----------Installing ASM----------"
    if [[ "${WIP}" == "HUB" ]]; then
      local IDENTITY_PROVIDER
      local IDENTITY
      local HUB_MEMBERSHIP_ID
      IDENTITY_PROVIDER="$(kubectl --kubeconfig="${MC_CONFIGS[$i]}" get memberships.hub.gke.io membership -o=jsonpath='{.spec.identity_provider}')"
      IDENTITY="$(echo "${IDENTITY_PROVIDER}" | sed 's/^https:\/\/gkehub.googleapis.com\/projects\/\(.*\)\/locations\/global\/memberships\/\(.*\)$/\1 \2/g')"
      read -r ENVIRON_PROJECT_ID HUB_MEMBERSHIP_ID <<EOF
${IDENTITY}
EOF
      local ENVIRON_PROJECT_NUMBER
      ENVIRON_PROJECT_NUMBER=$(gcloud projects describe "${ENVIRON_PROJECT_ID}" --format="value(projectNumber)")
      local PROJECT_ID="${ENVIRON_PROJECT_ID}"
      local CLUSTER_NAME="${HUB_MEMBERSHIP_ID}"
      local CLUSTER_LOCATION="us-central1-a"
      local MESH_ID="proj-${ENVIRON_PROJECT_NUMBER}"

      kpt pkg get https://github.com/GoogleCloudPlatform/anthos-service-mesh-packages.git/asm@master tmp
      kpt cfg set tmp gcloud.compute.network "network${i}"
      kpt cfg set tmp gcloud.core.project "${PROJECT_ID}"
      kpt cfg set tmp gcloud.project.environProjectNumber "${ENVIRON_PROJECT_NUMBER}"
      kpt cfg set tmp gcloud.container.cluster "${CLUSTER_NAME}"
      kpt cfg set tmp gcloud.compute.location "${CLUSTER_LOCATION}"
      kpt cfg set tmp anthos.servicemesh.rev "${ASM_REVISION_LABEL}"
      kpt cfg set tmp anthos.servicemesh.tag "${TAG}"
      kpt cfg set tmp anthos.servicemesh.hub "${HUB}"
      kpt cfg set tmp anthos.servicemesh.hubTrustDomain "${ENVIRON_PROJECT_ID}.svc.id.goog"
      kpt cfg set tmp anthos.servicemesh.hub-idp-url "${IDENTITY_PROVIDER}"

      echo "----------Istio Operator YAML and Hub Overlay YAML----------"
      cat "tmp/istio/istio-operator.yaml"
      cat "tmp/istio/options/hub-meshca.yaml"

      echo "----------Generating expansion gw YAML----------"
      samples/multicluster/gen-eastwest-gateway.sh \
        --mesh "${MESH_ID}" \
        --cluster "${CLUSTER_NAME}" \
        --revision "${ASM_REVISION_LABEL}" \
        --network "network${i}" > "tmp/eastwest-gateway.yaml"
      cat "tmp/eastwest-gateway.yaml"
      istioctl install -y --kubeconfig="${MC_CONFIGS[$i]}" --context=cluster -f "tmp/istio/istio-operator.yaml" -f "tmp/istio/options/hub-meshca.yaml" -f "tmp/eastwest-gateway.yaml"
    else
      cat <<EOF | istioctl install -y --kubeconfig="${MC_CONFIGS[$i]}" -f -
apiVersion: install.istio.io/v1alpha1
kind: IstioOperator
spec:
  profile: asm-multicloud
  revision: ${ASM_REVISION_LABEL}
  hub: ${HUB}
  tag: ${TAG}
  values:
    global:
      proxy:
        logLevel: debug
        componentLogLevel: "misc:debug,rbac:debug"
      meshID: ${MESH_ID}
      multiCluster:
        clusterName: cluster${i}
      network: network${i}
    pilot:
      env:
        UNSAFE_ENABLE_ADMIN_ENDPOINTS: true
        PILOT_REMOTE_CLUSTER_TIMEOUT: 15s
  meshConfig:
    accessLogFile: /dev/stdout
EOF
      # eastwest gateway is not needed for single cluster installation
      if [[ "${CLUSTER_TOPOLOGY}" != "sc" ]]; then
        install_expansion_gw "${MESH_ID}" "cluster${i}" "network${i}" "${ASM_REVISION_LABEL}" "${HUB}" "${TAG}" "${MC_CONFIGS[$i]}"
      fi
    fi
    # set default network for the cluster, allow detecting network of non-injected pods
    kubectl --kubeconfig="${MC_CONFIGS[$i]}" label namespace istio-system topology.istio.io/network="network${i}"
    expose_services "${MC_CONFIGS[$i]}"
    configure_validating_webhook "${ASM_REVISION_LABEL}" "${MC_CONFIGS[$i]}"

    if [[ "${CLUSTER_TYPE}" == "gke-on-prem" ]]; then
      onprem::configure_external_ip "${MC_CONFIGS[$i]}"
    fi

  done

  configure_remote_secrets
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
function configure_remote_secrets() {
  for i in "${!MC_CONFIGS[@]}"; do
    for j in "${!MC_CONFIGS[@]}"; do
      if [[ "$i" != "$j" ]]; then
        istioctl x create-remote-secret \
          --kubeconfig="${MC_CONFIGS[$j]}" \
          --name="secret-${j}" \
        | kubectl apply --kubeconfig="${MC_CONFIGS[$i]}" -f -
      fi
    done
  done
}

# on-prem specific fucntion to configure external ips for the gateways
# Parameters:
# $1    kubeconfig
function onprem::configure_external_ip() {
  local HERC_ENV_ID
  HERC_ENV_ID=$(echo "$1" | rev | cut -d '/' -f 2 | rev)
  local INGRESS_ID=\"lb-test-ip\"
  local EXPANSION_ID=\"expansion-ip\"
  local INGRESS_IP

  echo "Installing herc CLI..."
  gsutil cp "gs://anthos-hercules-public-artifacts/herc/latest/herc" "/usr/local/bin/" && chmod 755 "/usr/local/bin/herc"

  INGRESS_IP=$(herc getEnvironment "${HERC_ENV_ID}" -o json | \
    jq -r ".environment.resources.vcenter_server.datacenter.networks.fe.ip_addresses.${INGRESS_ID}.ip_address")

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

  # Inject the external IPs for GWs
  echo "----------Configuring external IP for ingress gw----------"
  kubectl patch svc istio-ingressgateway -n istio-system \
    --type='json' -p '[{"op": "add", "path": "/spec/loadBalancerIP", "value": "'"${INGRESS_IP}"'"}]' \
    --kubeconfig="$1"
  echo "----------Configuring external IP for expansion gw----------"
  kubectl patch svc istio-eastwestgateway -n istio-system \
    --type='json' -p '[{"op": "add", "path": "/spec/loadBalancerIP", "value": "'"${EXPANSION_IP}"'"}]' \
    --kubeconfig="$1"
}

# Installs expansion gw
# Parameters:
# $1    mesh id
# $2    cluster
# $3    network
# $4    revision
# $5    hub
# $6    tag
# $7    kubeconfig
function install_expansion_gw() {
  echo "----------Installing expansion gw----------"
  samples/multicluster/gen-eastwest-gateway.sh \
    --mesh "$1" \
    --cluster "$2" \
    --network "$3" \
    --revision "$4" \
  | istioctl install -y -f - \
    --set hub="$5" \
    --set tag="$6" \
    --kubeconfig="$7"
}

# Exposes service in istio-system ns
# Parameters:
# $1    kubeconfig
function expose_services() {
  echo "----------Exposing Services----------"
  kubectl apply -n istio-system -f samples/multicluster/expose-services.yaml --kubeconfig="$1"
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

function install_asm_on_proxied_clusters() {
  local MESH_ID="test-mesh"
  for i in "${!MC_CONFIGS[@]}"; do
    if [[ "${CA}" == "MESHCA" ]]; then
      local IDENTITY_PROVIDER
      local IDENTITY
      local HUB_MEMBERSHIP_ID
      IDENTITY_PROVIDER="$(kubectl --kubeconfig="${MC_CONFIGS[$i]}" get memberships.hub.gke.io membership -o=jsonpath='{.spec.identity_provider}')"
      IDENTITY="$(echo "${IDENTITY_PROVIDER}" | sed 's/^https:\/\/gkehub.googleapis.com\/projects\/\(.*\)\/locations\/global\/memberships\/\(.*\)$/\1 \2/g')"
      read -r ENVIRON_PROJECT_ID HUB_MEMBERSHIP_ID <<EOF
${IDENTITY}
EOF
      local ENVIRON_PROJECT_NUMBER
      ENVIRON_PROJECT_NUMBER=$(env -u HTTP_PROXY -u HTTPS_PROXY gcloud projects describe "${ENVIRON_PROJECT_ID}" --format="value(projectNumber)")
      local PROJECT_ID="${ENVIRON_PROJECT_ID}"
      local CLUSTER_NAME="${HUB_MEMBERSHIP_ID}"
      local CLUSTER_LOCATION="us-central1-a"
      local MESH_ID="proj-${ENVIRON_PROJECT_NUMBER}"

      env -u HTTP_PROXY -u HTTPS_PROXY kpt pkg get https://github.com/GoogleCloudPlatform/anthos-service-mesh-packages.git/asm@master tmp
      kpt cfg set tmp gcloud.compute.network "network${i}"
      kpt cfg set tmp gcloud.core.project "${PROJECT_ID}"
      kpt cfg set tmp gcloud.project.environProjectNumber "${ENVIRON_PROJECT_NUMBER}"
      kpt cfg set tmp gcloud.container.cluster "${CLUSTER_NAME}"
      kpt cfg set tmp gcloud.compute.location "${CLUSTER_LOCATION}"
      kpt cfg set tmp anthos.servicemesh.rev "${ASM_REVISION_LABEL}"
      kpt cfg set tmp anthos.servicemesh.tag "${TAG}"
      kpt cfg set tmp anthos.servicemesh.hub "${HUB}"
      kpt cfg set tmp anthos.servicemesh.hubTrustDomain "${ENVIRON_PROJECT_ID}.svc.id.goog"
      kpt cfg set tmp anthos.servicemesh.hub-idp-url "${IDENTITY_PROVIDER}"

      echo "----------Istio Operator YAML and Hub Overlay YAML----------"
      cat "tmp/istio/istio-operator.yaml"
      cat "tmp/istio/options/hub-meshca.yaml"

      echo "----------Installing ASM----------"
      istioctl install -y --kubeconfig="${MC_CONFIGS[$i]}" -f "tmp/istio/istio-operator.yaml" -f "tmp/istio/options/hub-meshca.yaml" --revision="${ASM_REVISION_LABEL}"
    else
      install_certs "${MC_CONFIGS[$i]}"
      echo "----------Installing ASM----------"
      cat <<EOF | istioctl install -y --kubeconfig="${MC_CONFIGS[$i]}" -f -
apiVersion: install.istio.io/v1alpha1
kind: IstioOperator
spec:
  profile: asm-multicloud
  revision: ${ASM_REVISION_LABEL}
  hub: ${HUB}
  tag: ${TAG}
  values:
    global:
      proxy:
        logLevel: debug
        componentLogLevel: "misc:debug,rbac:debug"
      meshID: ${MESH_ID}
      multiCluster:
        clusterName: cluster${i}
      network: network${i}
    pilot:
      env:
        UNSAFE_ENABLE_ADMIN_ENDPOINTS: true
        PILOT_REMOTE_CLUSTER_TIMEOUT: 15s
  meshConfig:
    accessLogFile: /dev/stdout
EOF
    fi
    configure_validating_webhook "${ASM_REVISION_LABEL}" "${MC_CONFIGS[$i]}"
  done
}
