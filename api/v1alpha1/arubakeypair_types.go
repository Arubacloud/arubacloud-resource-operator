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

// ArubaKeyPairSpec defines the desired state of ArubaKeyPair.
type ArubaKeyPairSpec struct {
	// Tenant is the owning account/tenant of this keypair
	// +kubebuilder:validation:Required
	Tenant string `json:"tenant"`

	// Tags are labels associated with the keypair
	// +kubebuilder:validation:Optional
	Tags []string `json:"tags,omitempty"`

	// Location specifies the location for the keypair
	// +kubebuilder:validation:Required
	Location Location `json:"location"`

	// Value specifies the SSH public key value
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Value string `json:"value"`

	// ProjectReference references the ArubaProject that owns this keypair
	// +kubebuilder:validation:Required
	ProjectReference ResourceReference `json:"projectReference"`
}

// ArubaKeyPairStatus defines the observed state of ArubaKeyPair.
type ArubaKeyPairStatus struct {
	ArubaResourceStatus `json:",inline"`

	// ProjectID is the project ID where this keypair is created
	// +kubebuilder:validation:Optional
	ProjectID string `json:"projectID,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=akp
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Resource ID",type="string",JSONPath=".status.resourceID"
// +kubebuilder:printcolumn:name="Message",type="string",JSONPath=".status.message"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// ArubaKeyPair is the Schema for the arubakeypairs API.
type ArubaKeyPair struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ArubaKeyPairSpec   `json:"spec,omitempty"`
	Status ArubaKeyPairStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ArubaKeyPairList contains a list of ArubaKeyPair.
type ArubaKeyPairList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ArubaKeyPair `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ArubaKeyPair{}, &ArubaKeyPairList{})
}
