package client

import (
	"context"
	"fmt"
)

type SubnetStatus struct {
	State        string `json:"state"`
	CreationDate string `json:"creationDate"`
}

type SubnetNetwork struct {
	Address string `json:"address"`
}

type SubnetDHCP struct {
	Enabled bool `json:"enabled"`
}

type SubnetMetadata struct {
	ID           string   `json:"id,omitempty"`
	URI          string   `json:"uri,omitempty"`
	Name         string   `json:"name"`
	Tags         []string `json:"tags,omitempty"`
	CreationDate string   `json:"creationDate,omitempty"`
	CreatedBy    string   `json:"createdBy,omitempty"`
	UpdateDate   string   `json:"updateDate,omitempty"`
	UpdatedBy    string   `json:"updatedBy,omitempty"`
	Version      string   `json:"version,omitempty"`
}

type SubnetProperties struct {
	Type    string        `json:"type"`
	Default bool          `json:"default"`
	Network SubnetNetwork `json:"network"`
	DHCP    SubnetDHCP    `json:"dhcp"`
}

type SubnetRequest struct {
	Metadata   SubnetMetadata   `json:"metadata"`
	Properties SubnetProperties `json:"properties"`
}

type SubnetResponse struct {
	Metadata   SubnetMetadata   `json:"metadata"`
	Properties SubnetProperties `json:"properties"`
	Status     *SubnetStatus    `json:"status,omitempty"`
}

type SubnetListResponse struct {
	Total  int              `json:"total"`
	Values []SubnetResponse `json:"values"`
}

// CreateSubnet creates a new subnet via API
func (c *HelperClient) CreateSubnet(ctx context.Context, projectID, vpcID string, req SubnetRequest) (*SubnetResponse, error) {
	endpoint := fmt.Sprintf("/projects/%s/providers/Aruba.Network/vpcs/%s/subnets", projectID, vpcID)
	var subnetResp SubnetResponse
	if err := c.DoAPIRequest(ctx, "POST", endpoint, req, &subnetResp); err != nil {
		return nil, err
	}
	return &subnetResp, nil
}

// GetSubnet retrieves a subnet via API
func (c *HelperClient) GetSubnet(ctx context.Context, projectID, vpcID, subnetID string) (*SubnetResponse, error) {
	endpoint := fmt.Sprintf("/projects/%s/providers/Aruba.Network/vpcs/%s/subnets/%s", projectID, vpcID, subnetID)
	var subnetResp SubnetResponse
	if err := c.DoAPIRequest(ctx, "GET", endpoint, nil, &subnetResp); err != nil {
		return nil, err
	}
	return &subnetResp, nil
}

// UpdateSubnet updates an existing subnet via API
func (c *HelperClient) UpdateSubnet(ctx context.Context, projectID, vpcID, subnetID string, req SubnetRequest) (*SubnetResponse, error) {
	endpoint := fmt.Sprintf("/projects/%s/providers/Aruba.Network/vpcs/%s/subnets/%s", projectID, vpcID, subnetID)
	var subnetResp SubnetResponse
	if err := c.DoAPIRequest(ctx, "PUT", endpoint, req, &subnetResp); err != nil {
		return nil, err
	}
	return &subnetResp, nil
}

// DeleteSubnet deletes a subnet via API
func (c *HelperClient) DeleteSubnet(ctx context.Context, projectID, vpcID, subnetID string) error {
	endpoint := fmt.Sprintf("/projects/%s/providers/Aruba.Network/vpcs/%s/subnets/%s", projectID, vpcID, subnetID)
	return c.DoAPIRequest(ctx, "DELETE", endpoint, nil, nil)
}

// ListSubnets lists all subnets in a VPC
func (c *HelperClient) ListSubnets(ctx context.Context, projectID, vpcID string) (*SubnetListResponse, error) {
	endpoint := fmt.Sprintf("/projects/%s/providers/Aruba.Network/vpcs/%s/subnets", projectID, vpcID)
	var subnetList SubnetListResponse
	if err := c.DoAPIRequest(ctx, "GET", endpoint, nil, &subnetList); err != nil {
		return nil, err
	}
	return &subnetList, nil
}
