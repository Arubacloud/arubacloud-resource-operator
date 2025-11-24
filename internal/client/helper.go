package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"

	ctrl "sigs.k8s.io/controller-runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// HTTPClient interface abstracts http.Client for testing purposes
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// HelperClientConfig holds configuration for CMP API calls

// HelperClient provides API access to CMP services
type HelperClient struct {
	client.Client
	HTTPClient    HTTPClient
	apiGatewayUrl string
	apiToken      string
}

type TokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

type SecretReference struct {
	Name      string
	Key       string
	Namespace string
}

type ApiError struct {
	Type     string        `json:"type"`
	Title    string        `json:"title"`
	Status   int           `json:"status"`
	Errors   []ErrorDetail `json:"errors"`
	TraceId  string        `json:"traceId"`
	ParentId string        `json:"parentId"`
}

type ErrorDetail struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e *ApiError) Error() string {
	return fmt.Sprintf(
		"API Error: Type=%s, Title=%s, Status=%d, Errors=%v, TraceId=%s, ParentId=%s",
		e.Type, e.Title, e.Status, e.Errors, e.TraceId, e.ParentId,
	)
}

// IsInvalidStatus although, 400 should be a bad request, but they use 400 and 404 even if the resource is not ready
func (e *ApiError) IsInvalidStatus() bool {
	return e.Status == 404 || e.Status == 400
}

// NewHelperClient creates a new HelperClient instance
func NewHelperClient(k8sClient client.Client, httpClient HTTPClient, gw_uri string) *HelperClient {
	if httpClient == nil {
		httpClient = &http.Client{}
	}

	return &HelperClient{
		Client:        k8sClient,
		HTTPClient:    httpClient,
		apiGatewayUrl: gw_uri,
	}
}

func (c *HelperClient) SetAPIToken(token string) {
	c.apiToken = token
}

// DoAPIRequest performs an authenticated API request
func (c *HelperClient) DoAPIRequest(ctx context.Context, method, endpoint string, body, response any) error {
	if c.apiGatewayUrl == "" {
		return fmt.Errorf("api gateway url not loaded")
	}

	url := fmt.Sprintf("%s%s", c.apiGatewayUrl, endpoint)
	clientLog := ctrl.Log.WithValues("Method", method, "Url", url)
	clientLog.Info("API Request")

	var reqBody io.Reader
	if body != nil {
		clientLog.Info("API Request", "Body", fmt.Sprintf("%+v", body))
		jsonData, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reqBody = bytes.NewBuffer(jsonData)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Authorization", "Bearer "+c.apiToken)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}

	clientLog.Info("API Response", "Status", resp.Status)

	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	expectedStatuses := []int{http.StatusOK, http.StatusCreated, http.StatusAccepted, http.StatusNoContent}
	// For DELETE, also consider 404 and 405 as successful responses, that's strange but true for aruba CMP
	if method == "DELETE" {
		expectedStatuses = append(expectedStatuses, http.StatusNotFound)
		expectedStatuses = append(expectedStatuses, http.StatusMethodNotAllowed)
	}
	if slices.Contains(expectedStatuses, resp.StatusCode) {
		if response != nil && len(responseBody) > 0 {
			if err := json.Unmarshal(responseBody, &response); err != nil {
				return fmt.Errorf("failed to read response body: %w", err)
			}
			clientLog.Info("API Response", "Body", fmt.Sprintf("%+v", response))
		}
		return nil
	}

	// For 4xx and 5xx errors, return ApiError with full response body
	if resp.StatusCode >= 400 && resp.StatusCode < 600 {
		var responseErr ApiError
		if len(responseBody) > 0 {
			if err := json.Unmarshal(responseBody, &responseErr); err != nil {
				return fmt.Errorf("failed to read response body: %w", err)
			}
			clientLog.Info("API Error Response", "Body", responseErr.Error())
			return &ApiError{
				Type:     responseErr.Type,
				Title:    responseErr.Title,
				Status:   resp.StatusCode,
				Errors:   responseErr.Errors,
				ParentId: responseErr.ParentId,
				TraceId:  responseErr.TraceId,
			}
		}
		return &ApiError{
			Status: resp.StatusCode,
			Title:  "Unknown API error",
		}
	}

	// For other errors, return standard error
	return fmt.Errorf("request failed with status: %d", resp.StatusCode)
}
