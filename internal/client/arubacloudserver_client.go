package client

import (
	"context"
	"fmt"
)

type CloudServerStatus struct {
	State        string `json:"state"`
	CreationDate string `json:"creationDate"`
}

type CloudServerCategory struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Typology struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"typology"`
}

type CloudServerLocation struct {
	Code    string `json:"code,omitempty"`
	Country string `json:"country,omitempty"`
	City    string `json:"city,omitempty"`
	Name    string `json:"name,omitempty"`
	Value   string `json:"value"`
}

type CloudServerProject struct {
	ID string `json:"id"`
}

type CloudServerResourceReference struct {
	URI string `json:"uri"`
}

type CloudServerMetadata struct {
	ID           string               `json:"id,omitempty"`
	URI          string               `json:"uri,omitempty"`
	Name         string               `json:"name"`
	Tags         []string             `json:"tags,omitempty"`
	Location     CloudServerLocation  `json:"location"`
	Project      *CloudServerProject  `json:"project,omitempty"`
	Category     *CloudServerCategory `json:"category,omitempty"`
	CreationDate string               `json:"creationDate,omitempty"`
	CreatedBy    string               `json:"createdBy,omitempty"`
	UpdateDate   string               `json:"updateDate,omitempty"`
	UpdatedBy    string               `json:"updatedBy,omitempty"`
	Version      string               `json:"version,omitempty"`
}

type CloudServerProperties struct {
	DataCenter     string                         `json:"dataCenter"`
	VPC            CloudServerResourceReference   `json:"vpc"`
	BootVolume     CloudServerResourceReference   `json:"bootVolume"`
	VpcPreset      bool                           `json:"vpcPreset,omitempty"`
	FlavorName     string                         `json:"flavorName"`
	ElasticIp      *CloudServerResourceReference  `json:"elasticIp,omitempty"`
	KeyPair        CloudServerResourceReference   `json:"keyPair"`
	Subnets        []CloudServerResourceReference `json:"subnets"`
	SecurityGroups []CloudServerResourceReference `json:"securityGroups"`
	IPAddress      string                         `json:"ipAddress,omitempty"`
}

type CloudServerRequest struct {
	Metadata   CloudServerMetadata   `json:"metadata"`
	Properties CloudServerProperties `json:"properties"`
}

type CloudServerResponse struct {
	Metadata   CloudServerMetadata   `json:"metadata"`
	Properties CloudServerProperties `json:"properties"`
	Status     *CloudServerStatus    `json:"status,omitempty"`
}

type CloudServerListResponse struct {
	Total  int                   `json:"total"`
	Values []CloudServerResponse `json:"values"`
}

// CreateCloudServer creates a new cloud server via API
func (c *HelperClient) CreateCloudServer(ctx context.Context, projectID string, req CloudServerRequest) (*CloudServerResponse, error) {
	endpoint := fmt.Sprintf("/projects/%s/providers/Aruba.Compute/cloudServers?api-version=1.1", projectID)
	var cloudServerResp CloudServerResponse
	if err := c.DoAPIRequest(ctx, "POST", endpoint, req, &cloudServerResp); err != nil {
		return nil, err
	}
	return &cloudServerResp, nil
}

// GetCloudServer retrieves a cloud server via API
func (c *HelperClient) GetCloudServer(ctx context.Context, projectID, cloudServerID string) (*CloudServerResponse, error) {
	endpoint := fmt.Sprintf("/projects/%s/providers/Aruba.Compute/cloudServers/%s", projectID, cloudServerID)
	var cloudServerResp CloudServerResponse
	if err := c.DoAPIRequest(ctx, "GET", endpoint, nil, &cloudServerResp); err != nil {
		return nil, err
	}
	return &cloudServerResp, nil
}

// UpdateCloudServer updates an existing cloud server via API
func (c *HelperClient) UpdateCloudServer(ctx context.Context, projectID, cloudServerID string, req CloudServerRequest) (*CloudServerResponse, error) {
	endpoint := fmt.Sprintf("/projects/%s/providers/Aruba.Compute/cloudServers/%s", projectID, cloudServerID)
	var cloudServerResp CloudServerResponse
	if err := c.DoAPIRequest(ctx, "PUT", endpoint, req, &cloudServerResp); err != nil {
		return nil, err
	}
	return &cloudServerResp, nil
}

// DeleteCloudServer deletes a cloud server via API
func (c *HelperClient) DeleteCloudServer(ctx context.Context, projectID, cloudServerID string) error {
	endpoint := fmt.Sprintf("/projects/%s/providers/Aruba.Compute/cloudServers/%s", projectID, cloudServerID)
	return c.DoAPIRequest(ctx, "DELETE", endpoint, nil, nil)
}

type AttachDetachDataVolumesRequest struct {
	VolumesToAttach []CloudServerResourceReference `json:"volumesToAttach"`
	VolumesToDetach []CloudServerResourceReference `json:"volumesToDetach"`
}

// AttachDetachDataVolumes manages data volumes for a cloud server via API
func (c *HelperClient) AttachDetachDataVolumes(ctx context.Context, projectID, cloudServerID string, req AttachDetachDataVolumesRequest) (*CloudServerResponse, error) {
	endpoint := fmt.Sprintf("/projects/%s/providers/Aruba.Compute/cloudServers/%s/attachDetachDataVolumes", projectID, cloudServerID)
	var cloudServerResp CloudServerResponse
	if err := c.DoAPIRequest(ctx, "POST", endpoint, req, &cloudServerResp); err != nil {
		return nil, err
	}
	return &cloudServerResp, nil
}

// ListCloudServers lists all cloud servers in a project
func (c *HelperClient) ListCloudServers(ctx context.Context, projectID string) (*CloudServerListResponse, error) {
	endpoint := fmt.Sprintf("/projects/%s/providers/Aruba.Compute/cloudServers", projectID)
	var cloudServerList CloudServerListResponse
	if err := c.DoAPIRequest(ctx, "GET", endpoint, nil, &cloudServerList); err != nil {
		return nil, err
	}
	return &cloudServerList, nil
}
