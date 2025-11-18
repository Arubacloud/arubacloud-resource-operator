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

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// Common phases for all Aruba resources
type ArubaResourcePhase string

const (
	// ArubaResourcePhaseCreating indicates the resource is being created
	ArubaResourcePhaseCreating ArubaResourcePhase = "Creating"
	// ArubaResourcePhaseProvisioning indicates the resource is being provisioned remotely
	ArubaResourcePhaseProvisioning ArubaResourcePhase = "Provisioning"
	// ArubaResourcePhaseCreated indicates the resource has been created successfully
	ArubaResourcePhaseCreated ArubaResourcePhase = "Created"
	// ArubaResourcePhaseUpdating indicates the resource is being updated
	ArubaResourcePhaseUpdating ArubaResourcePhase = "Updating"
	// ArubaResourcePhaseDeleting indicates the resource is being deleted
	ArubaResourcePhaseDeleting ArubaResourcePhase = "Deleting"
	// ArubaResourcePhaseDeleted indicates the resource has been deleted
	ArubaResourcePhaseDeleted ArubaResourcePhase = "Deleted"
	// ArubaResourcePhaseFailed indicates the resource has failed
	ArubaResourcePhaseFailed ArubaResourcePhase = "Failed"
)

// Condition types for Aruba resources
const (
	// ConditionTypeSynchronized indicates whether the resource is synchronized with the remote system
	ConditionTypeSynchronized = "Synchronized"
)

// Location specifies the location for resources
type Location struct {
	// Value is the location identifier (e.g., "ITBG-Bergamo")
	// +kubebuilder:validation:Required
	Value string `json:"value"`
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

// Common status for all Aruba resources
type ArubaResourceStatus struct {
	// Phase represents the current phase of the resource
	// +kubebuilder:validation:Optional
	Phase ArubaResourcePhase `json:"phase,omitempty"`

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

	// Conditions represent the latest available observations of the Aruba Resource state
	// +listType=map
	// +listMapKey=type
	// +patchStrategy=merge
	// +patchMergeKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
}

// ArubaObject is the common Schema for the aruba API.
type ArubaObject struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Status ArubaResourceStatus `json:"status,omitempty"`
}
