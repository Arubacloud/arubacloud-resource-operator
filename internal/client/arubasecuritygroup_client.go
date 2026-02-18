package client

import (
	"context"
	"fmt"
)

type SecurityGroupStatus struct {
	State        string `json:"state"`
	CreationDate string `json:"creationDate"`
}

type SecurityGroupLocation struct {
	Code    string `json:"code,omitempty"`
	Country string `json:"country,omitempty"`
	City    string `json:"city,omitempty"`
	Name    string `json:"name,omitempty"`
	Value   string `json:"value"`
}

type SecurityGroupMetadata struct {
	ID           string                `json:"id,omitempty"`
	URI          string                `json:"uri,omitempty"`
	Name         string                `json:"name"`
	Tags         []string              `json:"tags,omitempty"`
	Location     SecurityGroupLocation `json:"location"`
	CreationDate string                `json:"creationDate,omitempty"`
	CreatedBy    string                `json:"createdBy,omitempty"`
	UpdateDate   string                `json:"updateDate,omitempty"`
	UpdatedBy    string                `json:"updatedBy,omitempty"`
	Version      string                `json:"version,omitempty"`
}

type SecurityGroupProperties struct {
	Default bool `json:"default"`
}

type SecurityGroupRequest struct {
	Metadata   SecurityGroupMetadata   `json:"metadata"`
	Properties SecurityGroupProperties `json:"properties"`
}

type SecurityGroupResponse struct {
	Metadata   SecurityGroupMetadata   `json:"metadata"`
	Properties SecurityGroupProperties `json:"properties"`
	Status     *SecurityGroupStatus    `json:"status,omitempty"`
}

type SecurityGroupListResponse struct {
	Total  int                     `json:"total"`
	Values []SecurityGroupResponse `json:"values"`
}

// CreateSecurityGroup creates a new security group via API
func (c *HelperClient) CreateSecurityGroup(ctx context.Context, projectID, vpcID string, req SecurityGroupRequest) (*SecurityGroupResponse, error) {
	endpoint := fmt.Sprintf("/projects/%s/providers/Aruba.Network/vpcs/%s/securityGroups", projectID, vpcID)
	var securityGroupResp SecurityGroupResponse
	if err := c.DoAPIRequest(ctx, "POST", endpoint, req, &securityGroupResp); err != nil {
		return nil, err
	}
	return &securityGroupResp, nil
}

// GetSecurityGroup retrieves a security group via API
func (c *HelperClient) GetSecurityGroup(ctx context.Context, projectID, vpcID, securityGroupID string) (*SecurityGroupResponse, error) {
	endpoint := fmt.Sprintf("/projects/%s/providers/Aruba.Network/vpcs/%s/securityGroups/%s", projectID, vpcID, securityGroupID)
	var securityGroupResp SecurityGroupResponse
	if err := c.DoAPIRequest(ctx, "GET", endpoint, nil, &securityGroupResp); err != nil {
		return nil, err
	}
	return &securityGroupResp, nil
}

// UpdateSecurityGroup updates an existing security group via API
func (c *HelperClient) UpdateSecurityGroup(ctx context.Context, projectID, vpcID, securityGroupID string, req SecurityGroupRequest) (*SecurityGroupResponse, error) {
	endpoint := fmt.Sprintf("/projects/%s/providers/Aruba.Network/vpcs/%s/securityGroups/%s", projectID, vpcID, securityGroupID)
	var securityGroupResp SecurityGroupResponse
	if err := c.DoAPIRequest(ctx, "PUT", endpoint, req, &securityGroupResp); err != nil {
		return nil, err
	}
	return &securityGroupResp, nil
}

// DeleteSecurityGroup deletes a security group via API
func (c *HelperClient) DeleteSecurityGroup(ctx context.Context, projectID, vpcID, securityGroupID string) error {
	endpoint := fmt.Sprintf("/projects/%s/providers/Aruba.Network/vpcs/%s/securityGroups/%s", projectID, vpcID, securityGroupID)
	return c.DoAPIRequest(ctx, "DELETE", endpoint, nil, nil)
}

// ListSecurityGroups lists all security groups in a VPC
func (c *HelperClient) ListSecurityGroups(ctx context.Context, projectID, vpcID string) (*SecurityGroupListResponse, error) {
	endpoint := fmt.Sprintf("/projects/%s/providers/Aruba.Network/vpcs/%s/securityGroups", projectID, vpcID)
	var securityGroupList SecurityGroupListResponse
	if err := c.DoAPIRequest(ctx, "GET", endpoint, nil, &securityGroupList); err != nil {
		return nil, err
	}
	return &securityGroupList, nil
}
