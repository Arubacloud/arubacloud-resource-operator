package client

import (
	"context"
	"fmt"
)

type ProjectMetadata struct {
	ID   string   `json:"id,omitempty"`
	Name string   `json:"name"`
	Tags []string `json:"tags,omitempty"`
}

type ProjectProperties struct {
	Description string `json:"description,omitempty"`
	Default     bool   `json:"default,omitempty"`
}

type ProjectRequest struct {
	Metadata   ProjectMetadata   `json:"metadata"`
	Properties ProjectProperties `json:"properties"`
}

type ProjectResponse struct {
	Metadata   ProjectMetadata   `json:"metadata"`
	Properties ProjectProperties `json:"properties"`
}

// CreateProject creates a new project via API
func (c *HelperClient) CreateProject(ctx context.Context, req ProjectRequest) (*ProjectResponse, error) {
	var projectResp ProjectResponse
	if err := c.DoAPIRequest(ctx, "POST", "/projects", req, &projectResp); err != nil {
		return nil, err
	}
	return &projectResp, nil
}

// UpdateProject updates an existing project via API
func (c *HelperClient) UpdateProject(ctx context.Context, projectID string, req ProjectRequest) (*ProjectResponse, error) {
	endpoint := fmt.Sprintf("/projects/%s", projectID)
	var projectResp ProjectResponse
	if err := c.DoAPIRequest(ctx, "PUT", endpoint, req, &projectResp); err != nil {
		return nil, err
	}
	return &projectResp, nil
}

// DeleteProject deletes a project via API
func (c *HelperClient) DeleteProject(ctx context.Context, projectID string) error {
	endpoint := fmt.Sprintf("/projects/%s", projectID)
	return c.DoAPIRequest(ctx, "DELETE", endpoint, nil, nil)
}
