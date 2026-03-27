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
)

// VPCSpec defines the desired state of VPC.
// +kubebuilder:validation:XValidation:rule="self.tenant == oldSelf.tenant",message="tenant is immutable"
type VPCSpec struct {
	// Tenant is the owning account/tenant of this VPC
	Tenant string `json:"tenant,omitempty"`

	// Tags are labels associated with the VPC
	// +kubebuilder:validation:Optional
	Tags []string `json:"tags,omitempty"`

	// Region specifies the region for the VPC
	// +kubebuilder:validation:Required
	// +kubebuilder:default="ITBG-Bergamo"
	Region string `json:"region"`

	// ProjectReference references the Project that owns this VPC
	// +kubebuilder:validation:Required
	ProjectReference ResourceReference `json:"projectReference"`
}

// VPCStatus defines the observed state of VPC.
type VPCStatus struct {
	ResourceStatus `json:",inline"`

	// ProjectID is the project ID where this VPC is created
	// +kubebuilder:validation:Optional
	ProjectID string `json:"projectID,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=vpc;aruvpc
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Resource ID",type="string",JSONPath=".status.resourceID"
// +kubebuilder:printcolumn:name="Message",type="string",JSONPath=".status.message"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// VPC is the Schema for the vpcs API.
type VPC struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VPCSpec   `json:"spec,omitempty"`
	Status VPCStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// VPCList contains a list of VPC.
type VPCList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VPC `json:"items"`
}

func init() {
	SchemeBuilder.Register(&VPC{}, &VPCList{})
}

func (b *VPC) GetResourceStatus() *ResourceStatus {
	return &b.Status.ResourceStatus
}

func (b *VPC) GetTenant() string {
	return b.Spec.Tenant
}
