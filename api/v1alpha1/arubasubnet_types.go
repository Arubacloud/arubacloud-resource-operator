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

// SubnetNetwork defines the network configuration for a subnet
type SubnetNetwork struct {
	// Address specifies the network address in CIDR notation
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^([0-9]{1,3}\.){3}[0-9]{1,3}\/[0-9]{1,2}$`
	Address string `json:"address"`
}

// SubnetDHCP defines the DHCP configuration for a subnet
type SubnetDHCP struct {
	// Enabled indicates whether DHCP is enabled for this subnet
	// +kubebuilder:validation:Required
	Enabled bool `json:"enabled"`
}

// ArubaSubnetSpec defines the desired state of ArubaSubnet.
type ArubaSubnetSpec struct {
	// Tenant is the owning account/tenant of this subnet
	// +kubebuilder:validation:Required
	Tenant string `json:"tenant"`

	// Tags are labels associated with the subnet
	// +kubebuilder:validation:Optional
	Tags []string `json:"tags,omitempty"`

	// Type specifies the type of subnet (e.g., "Advanced")
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Advanced;Basic
	Type string `json:"type"`

	// Default indicates whether this is a default subnet
	// +kubebuilder:validation:Optional
	Default bool `json:"default,omitempty"`

	// Network specifies the network configuration
	// +kubebuilder:validation:Required
	Network SubnetNetwork `json:"network"`

	// DHCP specifies the DHCP configuration
	// +kubebuilder:validation:Required
	DHCP SubnetDHCP `json:"dhcp"`

	// VpcReference references the ArubaVpc that owns this subnet
	// +kubebuilder:validation:Required
	VpcReference ResourceReference `json:"vpcReference"`

	// ProjectReference references the ArubaProject that owns this block storage
	// +kubebuilder:validation:Required
	ProjectReference ResourceReference `json:"projectReference"`
}

// ArubaSubnetStatus defines the observed state of ArubaSubnet.
type ArubaSubnetStatus struct {
	ArubaResourceStatus `json:",inline"`

	// ProjectID is the project ID where this subnet is created
	// +kubebuilder:validation:Optional
	ProjectID string `json:"projectID,omitempty"`

	// VpcID is the VPC ID where this subnet is created
	// +kubebuilder:validation:Optional
	VpcID string `json:"vpcID,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=asn
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Resource ID",type="string",JSONPath=".status.resourceID"
// +kubebuilder:printcolumn:name="Message",type="string",JSONPath=".status.message"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// ArubaSubnet is the Schema for the arubasubnets API.
type ArubaSubnet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ArubaSubnetSpec   `json:"spec,omitempty"`
	Status ArubaSubnetStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ArubaSubnetList contains a list of ArubaSubnet.
type ArubaSubnetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ArubaSubnet `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ArubaSubnet{}, &ArubaSubnetList{})
}
