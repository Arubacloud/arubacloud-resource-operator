package client

import (
	"context"
	"fmt"
)

type BlockStorageStatus struct {
	State        string `json:"state"`
	CreationDate string `json:"creationDate"`
}

type BlockStorageCategory struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Typology struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"typology"`
}

type BlockStorageLocation struct {
	Code    string `json:"code,omitempty"`
	Country string `json:"country,omitempty"`
	City    string `json:"city,omitempty"`
	Name    string `json:"name,omitempty"`
	Value   string `json:"value"`
}

type BlockStorageProject struct {
	ID string `json:"id"`
}

type BlockStorageMetadata struct {
	ID           string                `json:"id,omitempty"`
	URI          string                `json:"uri,omitempty"`
	Name         string                `json:"name"`
	Tags         []string              `json:"tags,omitempty"`
	Location     BlockStorageLocation  `json:"location"`
	Project      *BlockStorageProject  `json:"project,omitempty"`
	Category     *BlockStorageCategory `json:"category,omitempty"`
	CreationDate string                `json:"creationDate,omitempty"`
	CreatedBy    string                `json:"createdBy,omitempty"`
	UpdateDate   string                `json:"updateDate,omitempty"`
	UpdatedBy    string                `json:"updatedBy,omitempty"`
	Version      string                `json:"version,omitempty"`
}

type BlockStorageProperties struct {
	SizeGb        int32  `json:"sizeGb"`
	BillingPeriod string `json:"billingPeriod"`
	DataCenter    string `json:"dataCenter"`
	Type          string `json:"type,omitempty"`
	Bootable      bool   `json:"bootable,omitempty"`
	Image         string `json:"image,omitempty"`
}

type BlockStorageRequest struct {
	Metadata   BlockStorageMetadata   `json:"metadata"`
	Properties BlockStorageProperties `json:"properties"`
}

type BlockStorageResponse struct {
	Metadata   BlockStorageMetadata   `json:"metadata"`
	Properties BlockStorageProperties `json:"properties"`
	Status     *BlockStorageStatus    `json:"status,omitempty"`
}

type BlockStorageListResponse struct {
	Total  int                    `json:"total"`
	Values []BlockStorageResponse `json:"values"`
}

// CreateBlockStorage creates a new block storage via API
func (c *HelperClient) CreateBlockStorage(ctx context.Context, projectID string, req BlockStorageRequest) (*BlockStorageResponse, error) {
	endpoint := fmt.Sprintf("/projects/%s/providers/Aruba.Storage/blockStorages", projectID)
	var blockStorageResp BlockStorageResponse
	if err := c.DoAPIRequest(ctx, "POST", endpoint, req, &blockStorageResp); err != nil {
		return nil, err
	}
	return &blockStorageResp, nil
}

// GetBlockStorage retrieves a block storage via API
func (c *HelperClient) GetBlockStorage(ctx context.Context, projectID, blockStorageID string) (*BlockStorageResponse, error) {
	endpoint := fmt.Sprintf("/projects/%s/providers/Aruba.Storage/blockStorages/%s", projectID, blockStorageID)
	var blockStorageResp BlockStorageResponse
	if err := c.DoAPIRequest(ctx, "GET", endpoint, nil, &blockStorageResp); err != nil {
		return nil, err
	}
	return &blockStorageResp, nil
}

// UpdateBlockStorage updates an existing block storage via API
func (c *HelperClient) UpdateBlockStorage(ctx context.Context, projectID, blockStorageID string, req BlockStorageRequest) (*BlockStorageResponse, error) {
	endpoint := fmt.Sprintf("/projects/%s/providers/Aruba.Storage/blockStorages/%s", projectID, blockStorageID)
	var blockStorageResp BlockStorageResponse
	if err := c.DoAPIRequest(ctx, "PUT", endpoint, req, &blockStorageResp); err != nil {
		return nil, err
	}
	return &blockStorageResp, nil
}

// DeleteBlockStorage deletes a block storage via API
func (c *HelperClient) DeleteBlockStorage(ctx context.Context, projectID, blockStorageID string) error {
	endpoint := fmt.Sprintf("/projects/%s/providers/Aruba.Storage/blockStorages/%s", projectID, blockStorageID)
	return c.DoAPIRequest(ctx, "DELETE", endpoint, nil, nil)
}

// ListBlockStorages lists all block storages in a project
func (c *HelperClient) ListBlockStorages(ctx context.Context, projectID string) (*BlockStorageListResponse, error) {
	endpoint := fmt.Sprintf("/projects/%s/providers/Aruba.Storage/blockStorages", projectID)
	var blockStorageList BlockStorageListResponse
	if err := c.DoAPIRequest(ctx, "GET", endpoint, nil, &blockStorageList); err != nil {
		return nil, err
	}
	return &blockStorageList, nil
}
