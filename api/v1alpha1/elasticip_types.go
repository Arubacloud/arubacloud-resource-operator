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

// ElasticIPSpec defines the desired state of ElasticIP.
// +kubebuilder:validation:XValidation:rule="self.tenant == oldSelf.tenant",message="tenant is immutable"
type ElasticIPSpec struct {
	// Tenant is the owning account/tenant of this elastic IP
	Tenant string `json:"tenant,omitempty"`

	// Tags are labels associated with the elastic IP
	// +kubebuilder:validation:Optional
	Tags []string `json:"tags,omitempty"`

	// Region specifies the region for the elastic IP
	// +kubebuilder:validation:Required
	// +kubebuilder:default="ITBG-Bergamo"
	Region string `json:"region"`

	// BillingPeriod defines the billing period
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Hour;Month
	// +kubebuilder:default="Hour"
	BillingPeriod string `json:"billingPeriod"`

	// ProjectReference references the Project that owns this elastic IP
	// +kubebuilder:validation:Required
	ProjectReference ResourceReference `json:"projectReference"`
}

// ElasticIPStatus defines the observed state of ElasticIP.
type ElasticIPStatus struct {
	ResourceStatus `json:",inline"`

	// ProjectID is the project ID where this elastic IP is created
	// +kubebuilder:validation:Optional
	ProjectID string `json:"projectID,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=eip;arueip
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Resource ID",type="string",JSONPath=".status.resourceID"
// +kubebuilder:printcolumn:name="Message",type="string",JSONPath=".status.message"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// ElasticIP is the Schema for the elasticips API.
type ElasticIP struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ElasticIPSpec   `json:"spec,omitempty"`
	Status ElasticIPStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ElasticIPList contains a list of ElasticIP.
type ElasticIPList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ElasticIP `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ElasticIP{}, &ElasticIPList{})
}

func (b *ElasticIP) GetResourceStatus() *ResourceStatus {
	return &b.Status.ResourceStatus
}

func (b *ElasticIP) GetTenant() string {
	return b.Spec.Tenant
}
