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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DataPlaneControlSpec defines the desired state of data plane revisions.
type DataPlaneControlSpec struct {
	// Revision is the revision for this cluster entry. Clusters may have more than one
	// revision.
	// +optional
	Revision string `json:"revision,omitempty"`

	// ProxyVersion is the target version of the proxy in the cluster.
	// +optional
	ProxyVersion string `json:"proxyVersion,omitempty"`

	// ProxyTargetBasisPoints is the basis points (1/10000) of proxies which
	// should be at ProxyVersion. Only auto-injected proxies belonging to
	// Deployments and ReplicaSets in the revision above are considered in the
	// calculation. 0 means that the controller will not auto upgrade any proxy
	// in the cluster. The actual value may vary depending on quantization due
	// to the total number of proxies. Actual value will be rounded down to
	// the nearest value, but never rounded to zero if positive.
	// +optional
	ProxyTargetBasisPoints int32 `json:"proxyTargetBasisPoints,omitempty"`
}

// DataPlaneControlStatus defines the observed state of data plane revisions.
type DataPlaneControlStatus struct {
	// Current state of the controller.
	State DataPlaneState `json:"state"`
	// Error details if the state is an error
	// +optional
	ErrorDetails *DataPlaneControlError `json:"errorDetails,omitempty"`
	// ProxyTargetBasisPoints is the actual basis points of proxies at the target version.
	// -1 means unknown.
	// +optional
	ProxyTargetBasisPoints int32 `json:"proxyTargetBasisPoints,omitempty"`
	// The generation observed by the data plane controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// ProxyMetrics shows the overall cluster proxy metrics.
	// +optional
	ProxyMetrics *ProxyMetrics `json:"proxyMetrics,omitempty"`
}

// DataPlaneControlError contains details about the error state.
type DataPlaneControlError struct {
	// Error code.
	// +optional
	Code int32 `json:"code,omitempty"`
	// Error message.
	// +optional
	Message string `json:"message,omitempty"`
}

// ProxyMetrics contains details about the overall cluster proxy metrics.
type ProxyMetrics struct {
	// ManagedProxyCount is the number of proxies managed by the controller.
	// +optional
	ManagedProxyCount int32 `json:"managedProxyCount,omitempty"`
	// UnmanagedProxyCount is the number of proxies NOT managed by the controller.
	// +optional
	UnmanagedProxyCount int32 `json:"unmanagedProxyCount,omitempty"`
	// NamespacesEnabledCount is the number of namespaces that have MDP enabled.
	// +optional
	NamespacesEnabledCount int32 `json:"namespacesEnabledCount,omitempty"`
	// PodsEnabledCount is the number of pods that have MDP enabled.
	// +optional
	PodsEnabledCount int32 `json:"podsEnabledCount,omitempty"`
}

// DataPlaneState of the data plane controller controller.
type DataPlaneState string

// The valid controller status values.
const (
	Unknown     DataPlaneState = "Unknown"
	Reconciling DataPlaneState = "Reconciling"
	Ready       DataPlaneState = "Ready"
	Error       DataPlaneState = "Error"
)

// DataPlaneControl is the Schema for the data plane controller API
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:subresource:status
type DataPlaneControl struct {
	metav1.TypeMeta
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              DataPlaneControlSpec   `json:"spec,omitempty"`
	Status            DataPlaneControlStatus `json:"status,omitempty"`
}

// DataPlaneControlList contains a list of DataPlaneControls
// +kubebuilder:object:root=true
type DataPlaneControlList struct {
	metav1.TypeMeta
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DataPlaneControl `json:"items"`
}
