package client

import (
	"context"
	"fmt"
)

type KeyPairStatus struct {
	State        string `json:"state"`
	CreationDate string `json:"creationDate"`
}

type KeyPairCategory struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Typology struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"typology"`
}

type KeyPairLocation struct {
	Code    string `json:"code,omitempty"`
	Country string `json:"country,omitempty"`
	City    string `json:"city,omitempty"`
	Name    string `json:"name,omitempty"`
	Value   string `json:"value"`
}

type KeyPairProject struct {
	ID string `json:"id"`
}

type KeyPairMetadata struct {
	ID           string           `json:"id,omitempty"`
	URI          string           `json:"uri,omitempty"`
	Name         string           `json:"name"`
	Tags         []string         `json:"tags,omitempty"`
	Location     KeyPairLocation  `json:"location"`
	Project      *KeyPairProject  `json:"project,omitempty"`
	Category     *KeyPairCategory `json:"category,omitempty"`
	CreationDate string           `json:"creationDate,omitempty"`
	CreatedBy    string           `json:"createdBy,omitempty"`
	UpdateDate   string           `json:"updateDate,omitempty"`
	UpdatedBy    string           `json:"updatedBy,omitempty"`
	Version      string           `json:"version,omitempty"`
}

type KeyPairProperties struct {
	Value string `json:"value"`
}

type KeyPairRequest struct {
	Metadata   KeyPairMetadata   `json:"metadata"`
	Properties KeyPairProperties `json:"properties"`
}

type KeyPairUpdateRequest struct {
	Metadata KeyPairMetadata `json:"metadata"`
}

type KeyPairResponse struct {
	Metadata   KeyPairMetadata    `json:"metadata"`
	Properties *KeyPairProperties `json:"properties,omitempty"`
	Status     *KeyPairStatus     `json:"status,omitempty"`
}

type KeyPairListResponse struct {
	Total  int               `json:"total"`
	Values []KeyPairResponse `json:"values"`
}

// CreateKeyPair creates a new keypair via API
func (c *HelperClient) CreateKeyPair(ctx context.Context, projectID string, req KeyPairRequest) (*KeyPairResponse, error) {
	endpoint := fmt.Sprintf("/projects/%s/providers/Aruba.Compute/keyPairs", projectID)
	var keyPairResp KeyPairResponse
	if err := c.DoAPIRequest(ctx, "POST", endpoint, req, &keyPairResp); err != nil {
		return nil, err
	}
	return &keyPairResp, nil
}

// GetKeyPair retrieves a keypair via API
func (c *HelperClient) GetKeyPair(ctx context.Context, projectID, keyPairID string) (*KeyPairResponse, error) {
	endpoint := fmt.Sprintf("/projects/%s/providers/Aruba.Compute/keyPairs/%s", projectID, keyPairID)
	var keyPairResp KeyPairResponse
	if err := c.DoAPIRequest(ctx, "GET", endpoint, nil, &keyPairResp); err != nil {
		return nil, err
	}
	return &keyPairResp, nil
}

// UpdateKeyPair updates an existing keypair via API
func (c *HelperClient) UpdateKeyPair(ctx context.Context, projectID, keyPairID string, req KeyPairUpdateRequest) (*KeyPairResponse, error) {
	endpoint := fmt.Sprintf("/projects/%s/providers/Aruba.Compute/keyPairs/%s", projectID, keyPairID)
	var keyPairResp KeyPairResponse
	if err := c.DoAPIRequest(ctx, "PUT", endpoint, req, &keyPairResp); err != nil {
		return nil, err
	}
	return &keyPairResp, nil
}

// DeleteKeyPair deletes a keypair via API
func (c *HelperClient) DeleteKeyPair(ctx context.Context, projectID, keyPairID string) error {
	endpoint := fmt.Sprintf("/projects/%s/providers/Aruba.Compute/keyPairs/%s", projectID, keyPairID)
	return c.DoAPIRequest(ctx, "DELETE", endpoint, nil, nil)
}

// ListKeyPairs lists all keypairs in a project
func (c *HelperClient) ListKeyPairs(ctx context.Context, projectID string) (*KeyPairListResponse, error) {
	endpoint := fmt.Sprintf("/projects/%s/providers/Aruba.Compute/keyPairs", projectID)
	var keyPairList KeyPairListResponse
	if err := c.DoAPIRequest(ctx, "GET", endpoint, nil, &keyPairList); err != nil {
		return nil, err
	}
	return &keyPairList, nil
}
