package reconciler

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	arubatypes "github.com/Arubacloud/sdk-go/pkg/types"
)

// CMPErrorCategory classifies a CMP error as semantic or technical.
type CMPErrorCategory int

const (
	// CMPErrorCategorySemantic represents user or configuration errors (HTTP 4xx).
	// These are permanent — retrying without a config change will not help.
	CMPErrorCategorySemantic CMPErrorCategory = iota + 1
	// CMPErrorCategoryTechnical represents infrastructure or transient errors
	// (network failures, HTTP 5xx). These are candidates for short-interval retry.
	CMPErrorCategoryTechnical
)

// String returns a human-readable name for the category.
func (c CMPErrorCategory) String() string {
	switch c {
	case CMPErrorCategorySemantic:
		return "semantic"
	case CMPErrorCategoryTechnical:
		return "technical"
	default:
		return "unknown"
	}
}

// CMPError is a structured error produced by CMP (Aruba API) interactions.
// It carries the error category, HTTP status code, RFC 7807 problem details,
// and the original Go error for transport-level failures.
type CMPError struct {
	// Category indicates whether the error is semantic (4xx) or technical (5xx/network).
	Category CMPErrorCategory
	// StatusCode is the HTTP status code returned by the CMP, or 0 for transport errors.
	StatusCode int
	// Title is the RFC 7807 title field from the CMP error response.
	Title string
	// Detail is the RFC 7807 detail field from the CMP error response.
	Detail string
	// Instance is the RFC 7807 instance field from the CMP error response.
	Instance string
	// Operation names the CMP action that failed ("create", "update", "delete", "list").
	Operation string
	// Resource is the name of the Kubernetes/CMP resource involved.
	Resource string
	// Cause holds the original Go error for transport-level failures; nil for HTTP-status errors.
	Cause error
}

// Error implements the error interface.
func (e *CMPError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf(
			"failed to %s '%s' in Aruba CMP: %v",
			e.Operation, e.Resource, e.Cause,
		)
	}
	return fmt.Sprintf(
		"failed to %s '%s' in Aruba CMP: status_code: %d, category: %s, title: %s, detail: %s, instance: %s",
		e.Operation, e.Resource, e.StatusCode, e.Category, e.Title, e.Detail, e.Instance,
	)
}

// Unwrap returns the underlying transport-level error, enabling errors.Is / errors.As chains.
func (e *CMPError) Unwrap() error {
	return e.Cause
}

// sanitizeCMPString replaces tab and newline characters with a single space,
// then collapses runs of spaces to prevent multi-line noise in condition messages.
func sanitizeCMPString(s string) string {
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return strings.TrimSpace(s)
}

// CMPTransportError creates a CMPError for a Go-level transport or network failure
// (e.g., connection refused, context cancellation). Always classified as Technical.
func CMPTransportError(operation, resource string, err error) *CMPError {
	return &CMPError{
		Category:  CMPErrorCategoryTechnical,
		Operation: operation,
		Resource:  resource,
		Cause:     err,
	}
}

// cmpResponseError creates a CMPError from a non-success HTTP response.
// HTTP 4xx responses are classified as Semantic; 5xx and everything else as Technical.
func cmpResponseError(operation, resource string, statusCode int, errResp *arubatypes.ErrorResponse) *CMPError {
	category := CMPErrorCategoryTechnical
	if statusCode >= 400 && statusCode < 500 {
		category = CMPErrorCategorySemantic
	}

	title, detail, instance := "", "", ""
	if errResp != nil {
		if errResp.Title != nil {
			title = sanitizeCMPString(*errResp.Title)
		}
		if errResp.Detail != nil {
			detail = sanitizeCMPString(*errResp.Detail)
		}
		if errResp.Instance != nil {
			instance = sanitizeCMPString(*errResp.Instance)
		}
	}

	return &CMPError{
		Category:   category,
		StatusCode: statusCode,
		Title:      title,
		Detail:     detail,
		Instance:   instance,
		Operation:  operation,
		Resource:   resource,
	}
}

// CMPCheckResponse inspects a CMP API response and returns nil if the status code
// is in successCodes, or a *CMPError otherwise. This replaces the repeated
// status-code switch blocks in cmpCreate / cmpUpdate / cmpDelete methods.
func CMPCheckResponse[T any](operation, resource string, resp *arubatypes.Response[T], successCodes ...int) error {
	if slices.Contains(successCodes, resp.StatusCode) {
		return nil
	}
	return cmpResponseError(operation, resource, resp.StatusCode, resp.Error)
}

// CMPErrorIsSemantic reports whether err (or any error in its chain) is a *CMPError
// with category Semantic. Use this in RequeueOnError and KActionOnAError to decide
// whether a CMP failure represents a permanent user/config problem.
func CMPErrorIsSemantic(err error) bool {
	var cmpErr *CMPError
	return errors.As(err, &cmpErr) && cmpErr.Category == CMPErrorCategorySemantic
}

// CMPErrorIsTechnical reports whether err (or any error in its chain) is a *CMPError
// with category Technical. Use this to distinguish transient infrastructure failures
// from permanent semantic errors.
func CMPErrorIsTechnical(err error) bool {
	var cmpErr *CMPError
	return errors.As(err, &cmpErr) && cmpErr.Category == CMPErrorCategoryTechnical
}
