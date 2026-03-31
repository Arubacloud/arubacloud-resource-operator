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
	// CMPErrorCategorySemantic represents true field-level validation errors (HTTP 4xx with a
	// non-empty Errors array in the ErrorResponse). These are permanent — the resource spec
	// is invalid and must be corrected before the CMP will accept it.
	CMPErrorCategorySemantic CMPErrorCategory = iota + 1
	// CMPErrorCategoryTransient represents temporary 4xx errors where the CMP ErrorResponse
	// carries no field-level validation details (empty Errors array). These are caused by
	// transient conditions (e.g. a dependency resource in a wrong state) and are candidates
	// for long-interval retry without changing the resource phase.
	CMPErrorCategoryTransient
	// CMPErrorCategoryTechnical represents infrastructure or transient errors
	// (network failures, HTTP 5xx). These are candidates for short-interval retry.
	CMPErrorCategoryTechnical
)

// String returns a human-readable name for the category.
func (c CMPErrorCategory) String() string {
	switch c {
	case CMPErrorCategorySemantic:
		return "semantic"
	case CMPErrorCategoryTransient:
		return "transient"
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
// For HTTP 4xx: Semantic when ErrorResponse.Errors is non-empty (field-level validation
// failures), Transient otherwise (temporary condition, e.g. dependency in wrong state).
// For everything else: Technical.
func cmpResponseError(operation, resource string, statusCode int, errResp *arubatypes.ErrorResponse) *CMPError {
	category := CMPErrorCategoryTechnical
	if statusCode >= 400 && statusCode < 500 {
		if errResp != nil && len(errResp.Errors) > 0 {
			category = CMPErrorCategorySemantic
		} else {
			category = CMPErrorCategoryTransient
		}
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
		if len(errResp.Errors) > 0 {
			parts := make([]string, 0, len(errResp.Errors))
			for _, ve := range errResp.Errors {
				switch {
				case ve.Field != "" && ve.Message != "":
					parts = append(parts, ve.Field+": "+ve.Message)
				case ve.Message != "":
					parts = append(parts, ve.Message)
				case ve.Field != "":
					parts = append(parts, ve.Field+": invalid")
				}
			}
			if len(parts) > 0 {
				validationDetail := sanitizeCMPString(strings.Join(parts, "; "))
				if detail != "" {
					detail = detail + " | Validation: " + validationDetail
				} else {
					detail = "Validation: " + validationDetail
				}
			}
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
// with category Semantic (HTTP 4xx with field-level validation errors). These represent
// permanent spec validation failures that require user intervention.
func CMPErrorIsSemantic(err error) bool {
	var cmpErr *CMPError
	return errors.As(err, &cmpErr) && cmpErr.Category == CMPErrorCategorySemantic
}

// CMPErrorIsTransient reports whether err (or any error in its chain) is a *CMPError
// with category Transient (HTTP 4xx without field-level validation errors). These represent
// temporary conditions (e.g. a dependency resource in a wrong state) that may resolve
// without user intervention.
func CMPErrorIsTransient(err error) bool {
	var cmpErr *CMPError
	return errors.As(err, &cmpErr) && cmpErr.Category == CMPErrorCategoryTransient
}

// CMPErrorIsTechnical reports whether err (or any error in its chain) is a *CMPError
// with category Technical (5xx or network failure). These are transient infrastructure
// failures that warrant a short-interval retry.
func CMPErrorIsTechnical(err error) bool {
	var cmpErr *CMPError
	return errors.As(err, &cmpErr) && cmpErr.Category == CMPErrorCategoryTechnical
}
