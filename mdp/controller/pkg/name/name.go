// Copyright Istio Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package name

const (
	MDPNamespace = "istio-system"
)

const (
	IstioRevisionLabel = "istio.io/rev"

	// IstioSystemNamespace is the default Istio system namespace.
	IstioSystemNamespace string = "istio-system"

	// KubeSystemNamespace is the system namespace where we place kubernetes system components.
	KubeSystemNamespace string = "kube-system"

	// ASMSystemNamespace is the system namespace where run ASM service controller
	ASMSystemNamespace string = "asm-system"

	// KubePublicNamespace is the namespace where we place kubernetes public info (ConfigMaps).
	KubePublicNamespace string = "kube-public"

	// KubeNodeLeaseNamespace is the namespace for the lease objects associated with each kubernetes node.
	KubeNodeLeaseNamespace string = "kube-node-lease"

	// MDPEnabledAnnotation indicates if a given object has enabled proxy management.
	MDPEnabledAnnotation string = "mesh.cloud.google.com/proxy"

	// EnablementCMPrefix is used to identify the config map which controls MDP enablement per revision.
	EnablementCMPrefix string = "istio-asm-managed-"

	// IstioProxyImageName is the proxy container's image name.
	IstioProxyImageName = "proxyv2"

	// UpgradeErrorEventReason represents the generic Reason used in different upgrade failure events.
	// But The message part is specific, e.g. EvictionError.
	UpgradeErrorEventReason = "DataPlaneUpgradeFailure"
	// EvictionErrorEventMessage represents the message used for pod eviction error events.
	EvictionErrorEventMessage = "EvictionError"
	// DataplaneUpgradeLabel is used to label pod upgrade status, e.g. dataplane-upgrade=failed
	DataplaneUpgradeLabel = "dataplane-upgrade"

	// MDPOwner is used in metrics reporting to set owner label of proxies
	MDPOwner string = "mdp-managed"
)

var systemNamespaces = map[string]struct{}{
	IstioSystemNamespace:   {},
	KubeSystemNamespace:    {},
	ASMSystemNamespace:     {},
	KubePublicNamespace:    {},
	KubeNodeLeaseNamespace: {},
}

func IsSystemNamespace(ns string) bool {
	_, ok := systemNamespaces[ns]
	return ok
}
