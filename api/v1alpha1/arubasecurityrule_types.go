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

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// SecurityRuleTarget defines the target of a security rule
type SecurityRuleTarget struct {
	// Kind specifies the type of target (e.g., "Ip", "SecurityGroup")
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Ip;SecurityGroup
	Kind string `json:"kind"`

	// Value specifies the target value (e.g., IP address/CIDR or security group reference)
	// +kubebuilder:validation:Required
	Value string `json:"value"`
}

// ArubaSecurityRuleSpec defines the desired state of ArubaSecurityRule.
type ArubaSecurityRuleSpec struct {
	// Tenant is the owning account/tenant of this security rule
	// +kubebuilder:validation:Required
	Tenant string `json:"tenant"`

	// Tags are labels associated with the security rule
	// +kubebuilder:validation:Optional
	Tags []string `json:"tags,omitempty"`

	// Location specifies the location for the security rule
	// +kubebuilder:validation:Required
	Location Location `json:"location"`

	// Protocol specifies the network protocol (TCP, UDP, ICMP, etc.)
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=TCP;UDP;ICMP;ALL
	Protocol string `json:"protocol"`

	// Port specifies the port or port range (e.g., "80", "80-90", "ALL")
	// +kubebuilder:validation:Required
	Port string `json:"port"`

	// Direction specifies the rule direction (Ingress or Egress)
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Ingress;Egress
	Direction string `json:"direction"`

	// Target specifies the target of the security rule
	// +kubebuilder:validation:Required
	Target SecurityRuleTarget `json:"target"`

	// SecurityGroupReference references the ArubaSecurityGroup that owns this rule
	// +kubebuilder:validation:Required
	SecurityGroupReference ResourceReference `json:"securityGroupReference"`

	// VpcReference references the ArubaVpc that contains the security group
	// +kubebuilder:validation:Required
	VpcReference ResourceReference `json:"vpcReference"`

	// ProjectReference references the ArubaProject that owns this security rule
	// +kubebuilder:validation:Required
	ProjectReference ResourceReference `json:"projectReference"`
}

// ArubaSecurityRuleStatus defines the observed state of ArubaSecurityRule.
type ArubaSecurityRuleStatus struct {
	ArubaResourceStatus `json:",inline"`

	// ProjectID is the project ID where this security rule is created
	// +kubebuilder:validation:Optional
	ProjectID string `json:"projectID,omitempty"`

	// VpcID is the VPC ID where this security rule is created
	// +kubebuilder:validation:Optional
	VpcID string `json:"vpcID,omitempty"`

	// SecurityGroupID is the security group ID that contains this rule
	// +kubebuilder:validation:Optional
	SecurityGroupID string `json:"securityGroupID,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=asr
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Resource ID",type="string",JSONPath=".status.resourceID"
// +kubebuilder:printcolumn:name="Protocol",type="string",JSONPath=".spec.protocol"
// +kubebuilder:printcolumn:name="Direction",type="string",JSONPath=".spec.direction"
// +kubebuilder:printcolumn:name="Message",type="string",JSONPath=".status.message"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// ArubaSecurityRule is the Schema for the arubasecurityrules API.
type ArubaSecurityRule struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ArubaSecurityRuleSpec   `json:"spec,omitempty"`
	Status ArubaSecurityRuleStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ArubaSecurityRuleList contains a list of ArubaSecurityRule.
type ArubaSecurityRuleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ArubaSecurityRule `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ArubaSecurityRule{}, &ArubaSecurityRuleList{})
}
