#!/usr/bin/env bash

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
