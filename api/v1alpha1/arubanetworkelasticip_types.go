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

// BillingPlan represents the billing configuration
type BillingPlan struct {
	// BillingPeriod defines the billing period (Hour, Month, etc.)
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Hour;Month
	BillingPeriod string `json:"billingPeriod"`
}

// ArubaNetworkElasticIpSpec defines the desired state of ArubaNetworkElasticIp.
type ArubaNetworkElasticIpSpec struct {
	// Tenant is the owning account/tenant of this elastic IP
	// +kubebuilder:validation:Required
	Tenant string `json:"tenant"`

	// Tags are labels associated with the elastic IP
	// +kubebuilder:validation:Optional
	Tags []string `json:"tags,omitempty"`

	// Location specifies the location for the elastic IP
	// +kubebuilder:validation:Required
	Location Location `json:"location"`

	// BillingPlan specifies the billing configuration
	// +kubebuilder:validation:Required
	BillingPlan BillingPlan `json:"billingPlan"`

	// ProjectReference references the ArubaProject that owns this elastic IP
	// +kubebuilder:validation:Required
	ProjectReference ResourceReference `json:"projectReference"`
}

// ArubaNetworkElasticIpStatus defines the observed state of ArubaNetworkElasticIp.
type ArubaNetworkElasticIpStatus struct {
	ArubaResourceStatus `json:",inline"`

	// ProjectID is the project ID where this elastic IP is created
	// +kubebuilder:validation:Optional
	ProjectID string `json:"projectID,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=aeip
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Resource ID",type="string",JSONPath=".status.resourceID"
// +kubebuilder:printcolumn:name="Message",type="string",JSONPath=".status.message"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// ArubaNetworkElasticIp is the Schema for the arubanetworkelasticips API.
type ArubaNetworkElasticIp struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ArubaNetworkElasticIpSpec   `json:"spec,omitempty"`
	Status ArubaNetworkElasticIpStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ArubaNetworkElasticIpList contains a list of ArubaNetworkElasticIp.
type ArubaNetworkElasticIpList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ArubaNetworkElasticIp `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ArubaNetworkElasticIp{}, &ArubaNetworkElasticIpList{})
}
