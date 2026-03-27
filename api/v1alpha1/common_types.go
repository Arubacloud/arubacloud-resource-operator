/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// Common phases for all resources
type ResourcePhase string

const (
	// ResourcePhaseCreating indicates the resource is being created
	ResourcePhaseCreating ResourcePhase = "Creating"
	// ResourcePhaseProvisioning indicates the resource is being provisioned remotely
	ResourcePhaseProvisioning ResourcePhase = "Provisioning"
	// ResourcePhaseWaitingCondition indicates the resource is waiting for some particular condition
	ResourcePhaseWaitingCondition ResourcePhase = "WaitingCondition"
	// ResourcePhaseActive indicates the resource has been created successfully
	ResourcePhaseActive ResourcePhase = "Active"
	// ResourcePhaseUpdating indicates the resource is being updated
	ResourcePhaseUpdating ResourcePhase = "Updating"
	// ResourcePhaseDeleting indicates the resource is being deleted
	ResourcePhaseDeleting ResourcePhase = "Deleting"
	// ResourcePhaseDeleted indicates the resource has been deleted
	ResourcePhaseDeleted ResourcePhase = "Deleted"
	// ResourcePhaseFailed indicates the resource has failed
	ResourcePhaseFailed ResourcePhase = "Failed"
)

// Condition reasons for resources
const (
	// ConditionReasonShallSynchronize indicates whether the resource needs to
	// be synchronized with the CMP but the process is not started yet
	ConditionReasonShallSynchronize = "ShallSynchronize"
	// ConditionTypeShallSynchronize indicates whether the resource needs to
	// be synchronized with the CMP and that the process is already started
	ConditionReasonSynchronizing = "Synchronizing"
	// ConditionReasonSynchronized indicates whether the resource is
	// synchronized with the CMP
	ConditionReasonSynchronized = "Synchronized"
	// ConditionReasonFailed indicates that the resource has exceeded the maximum
	// allowed time in a transitory phase and has been moved to Failed.
	ConditionReasonFailed = "Failed"
)

// Location specifies the location for resources
type Location struct {
	// Value is the location identifier (e.g., "ITBG-Bergamo")
	// +kubebuilder:validation:Required
	Value string `json:"value"`
}

// ArubaOwnerReference contains the information needed to identify an owning object
// across namespaces. It mirrors metav1.OwnerReference but adds a Namespace field,
// enabling cross-namespace ownership relationships that standard Kubernetes
// OwnerReferences do not support.
type ArubaOwnerReference struct {
	// APIVersion is the API version of the referent.
	APIVersion string `json:"apiVersion"`
	// Kind is the kind of the referent.
	Kind string `json:"kind"`
	// Namespace is the namespace of the referent.
	Namespace string `json:"namespace"`
	// Name is the name of the referent.
	Name string `json:"name"`
	// UID is the UID of the referent.
	UID types.UID `json:"uid"`
	// Controller indicates that this reference designates the managing controller.
	// +optional
	Controller *bool `json:"controller,omitempty"`
	// BlockOwnerDeletion indicates that, if set to true and if the owner has the
	// "foregroundDeletion" finalizer, the owner cannot be deleted until this reference
	// is removed.
	// +optional
	BlockOwnerDeletion *bool `json:"blockOwnerDeletion,omitempty"`
}

// ResourceReference represents a reference to another resource
type ResourceReference struct {
	// Name is the name of the referenced resource
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// Namespace is the namespace of the referenced resource
	// +kubebuilder:validation:Required
	Namespace string `json:"namespace,omitempty"`
}

// Common status for all resources
type ResourceStatus struct {
	// Phase represents the current phase of the resource
	// +kubebuilder:validation:Optional
	Phase ResourcePhase `json:"phase,omitempty"`

	// Message provides human-readable information about the current state
	// +kubebuilder:validation:Optional
	Message string `json:"message,omitempty"`

	// ResourceID is the unique identifier of the resource in the remote system
	// +kubebuilder:validation:Optional
	ResourceID string `json:"resourceID,omitempty"`

	// ObservedGeneration is the most recent generation observed by the controller
	// +kubebuilder:validation:Optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// PhaseStartTime tracks when the current phase started
	// +kubebuilder:validation:Optional
	PhaseStartTime *metav1.Time `json:"phaseStartTime,omitempty"`

	// Conditions represent the latest available observations of the Resource state
	// +listType=map
	// +listMapKey=type
	// +patchStrategy=merge
	// +patchMergeKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
}

type ResourcePhaseNature int

const (
	PhaseNatureInvalid ResourcePhaseNature = iota
	PhaseNatureTransitory
	PhaseNatureFinal
	PhaseNatureUndetermined
)

func (s *ResourceStatus) AssessPhaseNature() ResourcePhaseNature {
	if s == nil {
		return PhaseNatureUndetermined
	}

	switch s.Phase {
	case ResourcePhaseCreating,
		ResourcePhaseProvisioning,
		ResourcePhaseWaitingCondition,
		ResourcePhaseUpdating,
		ResourcePhaseDeleting:
		return PhaseNatureTransitory

	case ResourcePhaseActive,
		ResourcePhaseDeleted,
		ResourcePhaseFailed:
		return PhaseNatureFinal
	}

	return PhaseNatureInvalid
}

// Object is the common Schema for the API.
type Object struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Status ResourceStatus `json:"status,omitempty"`
}
