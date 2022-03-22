#!/bin/bash
#
# Copyright Istio Authors
#
#   Licensed under the Apache License, Version 2.0 (the "License");
#   you may not use this file except in compliance with the License.
#   You may obtain a copy of the License at
#
#       http://www.apache.org/licenses/LICENSE-2.0
#
#   Unless required by applicable law or agreed to in writing, software
#   distributed under the License is distributed on an "AS IS" BASIS,
#   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
#   See the License for the specific language governing permissions and
#   limitations under the License.
set -eux

MDP_MANIFEST_OUT="mdp/manifest/gen-mdp-manifest.yaml"
helm3 template mdp --namespace kube-system manifests/charts/istio-cni \
      -f mdp/manifest/values.yaml > "${MDP_MANIFEST_OUT}"
sed -i '/release:/d' "${MDP_MANIFEST_OUT}"
sed -i '/install.operator.istio.io\/owning-resource:/d' "${MDP_MANIFEST_OUT}"
sed -i '/operator.istio.io\/component:/d' "${MDP_MANIFEST_OUT}"
sed -i '/istio.io\/rev/d' "${MDP_MANIFEST_OUT}"
# helm is not happy with some field values to contain {{}}
sed -i 's/PROJECT_ID/{{ .PROJECT_ID }}/' "${MDP_MANIFEST_OUT}"

# for release build only
if [[ -d "${TARGET_OUT}/release" ]];then
  cp ${MDP_MANIFEST_OUT} "${TARGET_OUT}/release/gen-mdp-manifest.yaml"
fi
