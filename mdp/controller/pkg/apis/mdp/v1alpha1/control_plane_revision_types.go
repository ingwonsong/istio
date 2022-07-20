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

// Mirrored from google3/cloud/csm/hub/api/servicemesh/api/v1beta1/control_plane_revision_types.go
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ControlPlaneRevisionType describes how the revision is managed.
// +kubebuilder:validation:Enum=managed_service
type ControlPlaneRevisionType string

// ReleaseChannel determines the aggressiveness of upgrades.
// +kubebuilder:validation:Enum=regular;rapid;stable
type ReleaseChannel string

// ConditionType determines the type of the status condition.
type ConditionType string

const (
	// ControlPlaneRevisionTypeManagedService means that the revision is a managed service outside the cluster.
	ControlPlaneRevisionTypeManagedService ControlPlaneRevisionType = "managed_service"

	// ChannelRegular means upgrades will be applied at the regular pace.
	ChannelRegular ReleaseChannel = "regular"
	// ChannelRapid means upgrades will be applied at a rapid pace.
	ChannelRapid ReleaseChannel = "rapid"
	// ChannelStable means upgrades will be applied at a slower pace.
	ChannelStable ReleaseChannel = "stable"

	// ConditionTypeReconciled determines if the controller has successfully reconciled the CR.
	ConditionTypeReconciled ConditionType = "Reconciled"
	// ConditionTypeProvisioningFinished determines if the controller has finished the provisioning process (and either failed or succeeded).
	ConditionTypeProvisioningFinished ConditionType = "ProvisioningFinished"
	// ConditionTypeStalled determines if reconciliation will not complete without some intervention.
	ConditionTypeStalled ConditionType = "Stalled"
)

// ControlPlaneRevisionSpec defines the desired state of ControlPlaneRevision.
type ControlPlaneRevisionSpec struct {
	// TODO(b/178634666) set default values for ControlPlaneRevisionType and ReleaseChannel

	// ControlPlaneRevisionType determines how the revision should be managed.
	ControlPlaneRevisionType ControlPlaneRevisionType `json:"type,omitempty"`

	// ReleaseChannel determines the aggressiveness of upgrades.
	// +required
	// +kubebuilder:validation:Required
	Channel ReleaseChannel `json:"channel,omitempty"`
}

// ControlPlaneRevisionStatus defines the observed state of ControlPlaneRevision.
type ControlPlaneRevisionStatus struct {
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []ControlPlaneRevisionCondition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// ControlPlaneRevisionCondition is a repeated struct defining the current conditions of a ControlPlaneRevision.
type ControlPlaneRevisionCondition struct {
	// Type is the type of the condition.
	Type ConditionType `json:"type,omitempty"`

	// Status is the status of the condition, it can be True, False, or Unknown.
	Status string `json:"status,omitempty"`

	// Reason is a unique, one-word, CamelCase reason for the condition's last transition.
	Reason string `json:"reason,omitempty"`

	// Message is a human-readable message indicating details about last transition.
	Message string `json:"message,omitempty"`

	// LastTransitionTime is the last time the condition transitioned from one status to another.
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=cpr;cprs
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Reconciled",type=string,JSONPath=`.status.conditions[?(@.type=="Reconciled")].status`
// +kubebuilder:printcolumn:name="Stalled",type=string,JSONPath=`.status.conditions[?(@.type=="Stalled")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ControlPlaneRevision is the Schema for the ControlPlaneRevision API.
type ControlPlaneRevision struct {
	metav1.TypeMeta
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ControlPlaneRevisionSpec   `json:"spec,omitempty"`
	Status ControlPlaneRevisionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ControlPlaneRevisionList contains a list of ControlPlaneRevision.
type ControlPlaneRevisionList struct {
	metav1.TypeMeta
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ControlPlaneRevision `json:"items"`
}
