#!/usr/bin/env bash
#
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


usage() {
  echo "Usage: $0 [HUBENV] [LOCATION] [DURATION]"
  echo "e.g. $0 \"https://gkehub.googleapis.com/\" \"tairan-asm-multi-cloud-dev\" \"12 hour\""
}

delete_admin_membership(){
  output=$(curl -H "Authorization: Bearer $(gcloud auth application-default print-access-token)" \
    -H "X-GFE-SSL: yes" \
    "$1/v1alpha/projects/$2/locations/global/memberships:listAdmin")

  if [ "$output" = "{}" ]; then
      return
  fi

  for row in $(echo "$output" | jq -c '.adminClusterMemberships[]'); do
    name=$(echo "$row" | jq -r .name)
    last_connection_time=$(echo "$row" | jq -r .lastConnectionTime)
    if [ "$(date -d "$last_connection_time + $3" +%s)" -le "$(date +%s)" ]; then
        gcloud container hub memberships --project "$2" --quiet delete "$name"
    fi
  done
}

HUBENV="${1}"
LOCATION="${2}"
DURATION="${3}"

if [[ -z "${HUBENV}" || -z "${LOCATION}" || -z "${DURATION}" ]]; then
  usage;
  exit 1;
else
  gcloud config set api_endpoint_overrides/gkehub "${HUBENV}"
  delete_admin_membership "${HUBENV}" "${LOCATION}" "${DURATION}"
  gcloud config unset api_endpoint_overrides/gkehub
fi

