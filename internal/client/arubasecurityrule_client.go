package client

import (
	"context"
	"fmt"
)

type SecurityRuleStatus struct {
	State        string `json:"state"`
	CreationDate string `json:"creationDate"`
}

type SecurityRuleTarget struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type SecurityRuleLocation struct {
	Code    string `json:"code,omitempty"`
	Country string `json:"country,omitempty"`
	City    string `json:"city,omitempty"`
	Name    string `json:"name,omitempty"`
	Value   string `json:"value"`
}

type SecurityRuleMetadata struct {
	ID           string               `json:"id,omitempty"`
	URI          string               `json:"uri,omitempty"`
	Name         string               `json:"name"`
	Tags         []string             `json:"tags,omitempty"`
	Location     SecurityRuleLocation `json:"location"`
	CreationDate string               `json:"creationDate,omitempty"`
	CreatedBy    string               `json:"createdBy,omitempty"`
	UpdateDate   string               `json:"updateDate,omitempty"`
	UpdatedBy    string               `json:"updatedBy,omitempty"`
	Version      string               `json:"version,omitempty"`
}

type SecurityRuleProperties struct {
	Protocol  string             `json:"protocol"`
	Port      string             `json:"port"`
	Direction string             `json:"direction"`
	Target    SecurityRuleTarget `json:"target"`
}

type SecurityRuleRequest struct {
	Metadata   SecurityRuleMetadata   `json:"metadata"`
	Properties SecurityRuleProperties `json:"properties"`
}

type SecurityRuleResponse struct {
	Metadata   SecurityRuleMetadata   `json:"metadata"`
	Properties SecurityRuleProperties `json:"properties"`
	Status     *SecurityRuleStatus    `json:"status,omitempty"`
}

type SecurityRuleListResponse struct {
	Total  int                    `json:"total"`
	Values []SecurityRuleResponse `json:"values"`
}

// CreateSecurityRule creates a new security rule via API
func (c *HelperClient) CreateSecurityRule(ctx context.Context, projectID, vpcID, securityGroupID string, req SecurityRuleRequest) (*SecurityRuleResponse, error) {
	endpoint := fmt.Sprintf("/projects/%s/providers/Aruba.Network/vpcs/%s/securityGroups/%s/securityRules", projectID, vpcID, securityGroupID)
	var securityRuleResp SecurityRuleResponse
	if err := c.DoAPIRequest(ctx, "POST", endpoint, req, &securityRuleResp); err != nil {
		return nil, err
	}
	return &securityRuleResp, nil
}

// GetSecurityRule retrieves a security rule via API
func (c *HelperClient) GetSecurityRule(ctx context.Context, projectID, vpcID, securityGroupID, securityRuleID string) (*SecurityRuleResponse, error) {
	endpoint := fmt.Sprintf("/projects/%s/providers/Aruba.Network/vpcs/%s/securityGroups/%s/securityRules/%s", projectID, vpcID, securityGroupID, securityRuleID)
	var securityRuleResp SecurityRuleResponse
	if err := c.DoAPIRequest(ctx, "GET", endpoint, nil, &securityRuleResp); err != nil {
		return nil, err
	}
	return &securityRuleResp, nil
}

// UpdateSecurityRule updates an existing security rule via API
func (c *HelperClient) UpdateSecurityRule(ctx context.Context, projectID, vpcID, securityGroupID, securityRuleID string, req SecurityRuleRequest) (*SecurityRuleResponse, error) {
	endpoint := fmt.Sprintf("/projects/%s/providers/Aruba.Network/vpcs/%s/securityGroups/%s/securityRules/%s", projectID, vpcID, securityGroupID, securityRuleID)
	var securityRuleResp SecurityRuleResponse
	if err := c.DoAPIRequest(ctx, "PUT", endpoint, req, &securityRuleResp); err != nil {
		return nil, err
	}
	return &securityRuleResp, nil
}

// DeleteSecurityRule deletes a security rule via API
func (c *HelperClient) DeleteSecurityRule(ctx context.Context, projectID, vpcID, securityGroupID, securityRuleID string) error {
	endpoint := fmt.Sprintf("/projects/%s/providers/Aruba.Network/vpcs/%s/securityGroups/%s/securityRules/%s", projectID, vpcID, securityGroupID, securityRuleID)
	return c.DoAPIRequest(ctx, "DELETE", endpoint, nil, nil)
}

// ListSecurityRules lists all security rules in a security group
func (c *HelperClient) ListSecurityRules(ctx context.Context, projectID, vpcID, securityGroupID string) (*SecurityRuleListResponse, error) {
	endpoint := fmt.Sprintf("/projects/%s/providers/Aruba.Network/vpcs/%s/securityGroups/%s/securityRules", projectID, vpcID, securityGroupID)
	var securityRuleList SecurityRuleListResponse
	if err := c.DoAPIRequest(ctx, "GET", endpoint, nil, &securityRuleList); err != nil {
		return nil, err
	}
	return &securityRuleList, nil
}
