#!/usr/bin/env bash

CT_PREFIX="asm-prow-testci-cert-template"
PROJECT="asm-prow-build"
#PROJECT="liwenhao-project"

declare -a regions=("asia-east1" "asia-northeast1" "asia-southeast1"
   "australia-southeast1"
   "europe-north1" "europe-west1" "europe-west2" "europe-west3" "europe-west4"
   "us-central1" "us-east1" "us-east4" "us-west1" "us-west2" "us-west3" "us-west4")

#declare -a regions=("us-west1")

for region in "${regions[@]}"
do
  echo "---------------"
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

  echo "created cert_template ${ct_id}"
done

echo "Finished cert_template creation..."
