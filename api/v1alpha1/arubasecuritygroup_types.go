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

// ArubaSecurityGroupSpec defines the desired state of ArubaSecurityGroup.
type ArubaSecurityGroupSpec struct {
	// Tenant is the owning account/tenant of this security group
	// +kubebuilder:validation:Required
	Tenant string `json:"tenant"`

	// Tags are labels associated with the security group
	// +kubebuilder:validation:Optional
	Tags []string `json:"tags,omitempty"`

	// Location specifies the location for the security group
	// +kubebuilder:validation:Required
	Location Location `json:"location"`

	// Default indicates whether this is a default security group
	// +kubebuilder:validation:Optional
	Default bool `json:"default,omitempty"`

	// VpcReference references the ArubaVpc that owns this security group
	// +kubebuilder:validation:Required
	VpcReference ResourceReference `json:"vpcReference"`

	// ProjectReference references the ArubaProject that owns this security group
	// +kubebuilder:validation:Required
	ProjectReference ResourceReference `json:"projectReference"`
}

// ArubaSecurityGroupStatus defines the observed state of ArubaSecurityGroup.
type ArubaSecurityGroupStatus struct {
	ArubaResourceStatus `json:",inline"`

	// ProjectID is the project ID where this security group is created
	// +kubebuilder:validation:Optional
	ProjectID string `json:"projectID,omitempty"`

	// VpcID is the VPC ID where this security group is created
	// +kubebuilder:validation:Optional
	VpcID string `json:"vpcID,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=asg
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Resource ID",type="string",JSONPath=".status.resourceID"
// +kubebuilder:printcolumn:name="Message",type="string",JSONPath=".status.message"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// ArubaSecurityGroup is the Schema for the arubasecuritygroups API.
type ArubaSecurityGroup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ArubaSecurityGroupSpec   `json:"spec,omitempty"`
	Status ArubaSecurityGroupStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ArubaSecurityGroupList contains a list of ArubaSecurityGroup.
type ArubaSecurityGroupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ArubaSecurityGroup `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ArubaSecurityGroup{}, &ArubaSecurityGroupList{})
}
