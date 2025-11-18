package client

import (
	"context"
	"fmt"
)

type ElasticIpStatus struct {
	State        string `json:"state"`
	CreationDate string `json:"creationDate"`
}

type ElasticIpCategory struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Typology struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"typology"`
}

type ElasticIpLocation struct {
	Code    string `json:"code,omitempty"`
	Country string `json:"country,omitempty"`
	City    string `json:"city,omitempty"`
	Name    string `json:"name,omitempty"`
	Value   string `json:"value"`
}

type ElasticIpProject struct {
	ID string `json:"id"`
}

type ElasticIpBillingPlan struct {
	BillingPeriod string `json:"billingPeriod"`
}

type ElasticIpMetadata struct {
	ID           string             `json:"id,omitempty"`
	URI          string             `json:"uri,omitempty"`
	Name         string             `json:"name"`
	Tags         []string           `json:"tags,omitempty"`
	Location     ElasticIpLocation  `json:"location"`
	Project      *ElasticIpProject  `json:"project,omitempty"`
	Category     *ElasticIpCategory `json:"category,omitempty"`
	CreationDate string             `json:"creationDate,omitempty"`
	CreatedBy    string             `json:"createdBy,omitempty"`
	UpdateDate   string             `json:"updateDate,omitempty"`
	UpdatedBy    string             `json:"updatedBy,omitempty"`
	Version      string             `json:"version,omitempty"`
}

type ElasticIpProperties struct {
	BillingPlan ElasticIpBillingPlan `json:"billingPlan"`
	IPAddress   string               `json:"address,omitempty"` // Note: API uses "address" not "ipAddress"
}

type ElasticIpRequest struct {
	Metadata   ElasticIpMetadata   `json:"metadata"`
	Properties ElasticIpProperties `json:"properties"`
}

type ElasticIpResponse struct {
	Metadata   ElasticIpMetadata   `json:"metadata"`
	Properties ElasticIpProperties `json:"properties"`
	Status     *ElasticIpStatus    `json:"status,omitempty"`
}

type ElasticIpListResponse struct {
	Total  int                 `json:"total"`
	Values []ElasticIpResponse `json:"values"`
}

// CreateElasticIp creates a new elastic IP via API
func (c *HelperClient) CreateElasticIp(ctx context.Context, projectID string, req ElasticIpRequest) (*ElasticIpResponse, error) {
	endpoint := fmt.Sprintf("/projects/%s/providers/Aruba.Network/elasticIps", projectID)
	var elasticIpResp ElasticIpResponse
	if err := c.DoAPIRequest(ctx, "POST", endpoint, req, &elasticIpResp); err != nil {
		return nil, err
	}
	return &elasticIpResp, nil
}

// GetElasticIp retrieves an elastic IP via API
func (c *HelperClient) GetElasticIp(ctx context.Context, projectID, elasticIpID string) (*ElasticIpResponse, error) {
	endpoint := fmt.Sprintf("/projects/%s/providers/Aruba.Network/elasticIps/%s", projectID, elasticIpID)
	var elasticIpResp ElasticIpResponse
	if err := c.DoAPIRequest(ctx, "GET", endpoint, nil, &elasticIpResp); err != nil {
		return nil, err
	}
	return &elasticIpResp, nil
}

// UpdateElasticIp updates an existing elastic IP via API
func (c *HelperClient) UpdateElasticIp(ctx context.Context, projectID, elasticIpID string, req ElasticIpRequest) (*ElasticIpResponse, error) {
	endpoint := fmt.Sprintf("/projects/%s/providers/Aruba.Network/elasticIps/%s", projectID, elasticIpID)
	var elasticIpResp ElasticIpResponse
	if err := c.DoAPIRequest(ctx, "PUT", endpoint, req, &elasticIpResp); err != nil {
		return nil, err
	}
	return &elasticIpResp, nil
}

// DeleteElasticIp deletes an elastic IP via API
func (c *HelperClient) DeleteElasticIp(ctx context.Context, projectID, elasticIpID string) error {
	endpoint := fmt.Sprintf("/projects/%s/providers/Aruba.Network/elasticIps/%s", projectID, elasticIpID)
	return c.DoAPIRequest(ctx, "DELETE", endpoint, nil, nil)
}

// ListElasticIps lists all elastic IPs in a project
func (c *HelperClient) ListElasticIps(ctx context.Context, projectID string) (*ElasticIpListResponse, error) {
	endpoint := fmt.Sprintf("/projects/%s/providers/Aruba.Network/elasticIps", projectID)
	var elasticIpList ElasticIpListResponse
	if err := c.DoAPIRequest(ctx, "GET", endpoint, nil, &elasticIpList); err != nil {
		return nil, err
	}
	return &elasticIpList, nil
}
