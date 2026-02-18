package client

import (
	"context"
	"fmt"
)

type VpcStatus struct {
	State        string `json:"state"`
	CreationDate string `json:"creationDate"`
}

type VpcCategory struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Typology struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"typology"`
}

type VpcLocation struct {
	Code    string `json:"code,omitempty"`
	Country string `json:"country,omitempty"`
	City    string `json:"city,omitempty"`
	Name    string `json:"name,omitempty"`
	Value   string `json:"value"`
}

type VpcProject struct {
	ID string `json:"id"`
}

type VpcMetadata struct {
	ID           string       `json:"id,omitempty"`
	URI          string       `json:"uri,omitempty"`
	Name         string       `json:"name"`
	Tags         []string     `json:"tags,omitempty"`
	Location     VpcLocation  `json:"location"`
	Project      *VpcProject  `json:"project,omitempty"`
	Category     *VpcCategory `json:"category,omitempty"`
	CreationDate string       `json:"creationDate,omitempty"`
	CreatedBy    string       `json:"createdBy,omitempty"`
	UpdateDate   string       `json:"updateDate,omitempty"`
	UpdatedBy    string       `json:"updatedBy,omitempty"`
	Version      string       `json:"version,omitempty"`
}

type VPCProperties struct {
	Default bool `json:"default"`
	Preset  bool `json:"preset"`
}

type VpcRequest struct {
	Metadata   VpcMetadata   `json:"metadata"`
	Properties VPCProperties `json:"properties"`
}

type VpcResponse struct {
	Metadata VpcMetadata `json:"metadata"`
	Status   *VpcStatus  `json:"status,omitempty"`
}

type VpcListResponse struct {
	Total  int           `json:"total"`
	Values []VpcResponse `json:"values"`
}

// CreateVpc creates a new vpc via API
func (c *HelperClient) CreateVpc(ctx context.Context, projectID string, req VpcRequest) (*VpcResponse, error) {
	endpoint := fmt.Sprintf("/projects/%s/providers/Aruba.Network/vpcs", projectID)
	var vpcResp VpcResponse
	if err := c.DoAPIRequest(ctx, "POST", endpoint, req, &vpcResp); err != nil {
		return nil, err
	}
	return &vpcResp, nil
}

// GetVpc retrieves a vpc via API
func (c *HelperClient) GetVpc(ctx context.Context, projectID, vpcID string) (*VpcResponse, error) {
	endpoint := fmt.Sprintf("/projects/%s/providers/Aruba.Network/vpcs/%s", projectID, vpcID)
	var vpcResp VpcResponse
	if err := c.DoAPIRequest(ctx, "GET", endpoint, nil, &vpcResp); err != nil {
		return nil, err
	}
	return &vpcResp, nil
}

// UpdateVpc updates an existing vpc via API
func (c *HelperClient) UpdateVpc(ctx context.Context, projectID, vpcID string, req VpcRequest) (*VpcResponse, error) {
	endpoint := fmt.Sprintf("/projects/%s/providers/Aruba.Network/vpcs/%s", projectID, vpcID)
	var vpcResp VpcResponse
	if err := c.DoAPIRequest(ctx, "PUT", endpoint, req, &vpcResp); err != nil {
		return nil, err
	}
	return &vpcResp, nil
}

// DeleteVpc deletes a vpc via API
func (c *HelperClient) DeleteVpc(ctx context.Context, projectID, vpcID string) error {
	endpoint := fmt.Sprintf("/projects/%s/providers/Aruba.Network/vpcs/%s", projectID, vpcID)
	return c.DoAPIRequest(ctx, "DELETE", endpoint, nil, nil)
}

// ListVpcs lists all vpcs in a project
func (c *HelperClient) ListVpcs(ctx context.Context, projectID string) (*VpcListResponse, error) {
	endpoint := fmt.Sprintf("/projects/%s/providers/Aruba.Network/vpcs", projectID)
	var vpcList VpcListResponse
	if err := c.DoAPIRequest(ctx, "GET", endpoint, nil, &vpcList); err != nil {
		return nil, err
	}
	return &vpcList, nil
}
