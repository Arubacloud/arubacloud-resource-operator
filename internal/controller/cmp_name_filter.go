package controller

// TODO: Remove this file once the CMP API name:eq() filter is fixed for
// network-domain resources (VPC, Subnet, SecurityGroup, SecurityRule, ElasticIP).
// Issue https://jira.aruba.it/browse/DEV-66643.

import (
	"errors"
	"net/http"

	"github.com/Arubacloud/sdk-go/pkg/aruba"
)

// isCMPNotFound reports whether err is an SDK HTTP error carrying a 404 status.
// Network-domain List endpoints occasionally answer 404 (rather than an empty
// list) when the parent scope has no resources; callers treat that as "no match"
// instead of a hard failure.
func isCMPNotFound(err error) bool {
	var httpErr *aruba.HTTPError
	return errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound
}

// filterByName keeps only the wrappers whose name matches target. It is the
// client-side workaround for the CMP API ignoring name:eq() filters on
// network-domain List endpoints — the SDK List call returns every resource in
// scope, so controllers narrow the result set here using each wrapper's Name().
func filterByName[T any](items []T, target string, name func(T) string) []T {
	filtered := make([]T, 0, len(items))
	for _, item := range items {
		if name(item) == target {
			filtered = append(filtered, item)
		}
	}
	return filtered
}
