#!/bin/bash

CT_PREFIX="asm-testci-cert-template"
PROJECT="istio-prow-build"
 
declare -a regions=("asia-east1" "asia-northeast1" "asia-southeast1"
  "australia-southeast1" "europe-north1" "europe-west1" "europe-west2" "europe-west3" "europe-west4"
  "us-central1" "us-east1" "us-east4" "us-west1" "us-west2" "us-west3" "us-west4")


for region in "${regions[@]}"
do
  echo "${region}"
  ct_id="${CT_PREFIX}-${region}"
  echo "${ct_id}"
  gcloud privateca templates create "${ct_id}" \
                --predefined-values-file cert_template.yaml \
                --copy-sans --no-copy-subject \
                --identity-cel-expression "subject_alt_names.all(san, san.type == URI && san.value.startsWith('spiffe://'))" \
                --location="${region}" \
                --project="${PROJECT}" \
                --copy-known-extensions="base-key-usage,extended-key-usage,ca-options"
done
echo "Finished cert_template creation..."
