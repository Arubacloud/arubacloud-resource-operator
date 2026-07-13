package controller

// TODO: Remove this file once the CMP API name:eq() filter is fixed for
// network-domain resources (VPC, Subnet, SecurityGroup, SecurityRule, ElasticIP).

import (
	"github.com/go-logr/logr"

	arubatypes "github.com/Arubacloud/sdk-go/pkg/types"
)

// filterByName filters a slice keeping only elements whose extracted name
// matches target. It is the client-side workaround for the CMP API ignoring
// name:eq() filters on network-domain List endpoints.
func filterByName[T any](values []T, target string, nameExtractor func(T) *string) []T {
	filtered := make([]T, 0, len(values))
	for _, v := range values {
		name := nameExtractor(v)
		if name != nil && *name == target {
			filtered = append(filtered, v)
		}
	}
	return filtered
}

// applyNameFilterToVPCList filters the VPC list response client-side by name.
// TODO: Remove once CMP API name:eq() filter is fixed (issue https://jira.aruba.it/browse/DEV-66643).
func applyNameFilterToVPCList(resp *arubatypes.Response[arubatypes.VPCListResponse], name string, logger logr.Logger) {
	if resp == nil || resp.Data == nil {
		return
	}
	originalTotal := resp.Data.Total
	resp.Data.Values = filterByName(resp.Data.Values, name, func(v arubatypes.VPCResponse) *string {
		return v.Metadata.Name
	})
	resp.Data.Total = int64(len(resp.Data.Values))
	if resp.Data.Total != originalTotal {
		logger.Info("applied client-side name filter workaround for CMP API bug",
			"resourceType", "VPC",
			"filterName", name,
			"originalTotal", originalTotal,
			"filteredTotal", resp.Data.Total,
		)
	}
}

// applyNameFilterToSubnetList filters the Subnet list response client-side by name.
// TODO: Remove once CMP API name:eq() filter is fixed (issue https://jira.aruba.it/browse/DEV-66643).
func applyNameFilterToSubnetList(resp *arubatypes.Response[arubatypes.SubnetListResponse], name string, logger logr.Logger) {
	if resp == nil || resp.Data == nil {
		return
	}
	originalTotal := resp.Data.Total
	resp.Data.Values = filterByName(resp.Data.Values, name, func(v arubatypes.SubnetResponse) *string {
		return v.Metadata.Name
	})
	resp.Data.Total = int64(len(resp.Data.Values))
	if resp.Data.Total != originalTotal {
		logger.Info("applied client-side name filter workaround for CMP API bug",
			"resourceType", "Subnet",
			"filterName", name,
			"originalTotal", originalTotal,
			"filteredTotal", resp.Data.Total,
		)
	}
}

// applyNameFilterToSecurityGroupList filters the SecurityGroup list response client-side by name.
// TODO: Remove once CMP API name:eq() filter is fixed (issue https://jira.aruba.it/browse/DEV-66643).
func applyNameFilterToSecurityGroupList(resp *arubatypes.Response[arubatypes.SecurityGroupListResponse], name string, logger logr.Logger) {
	if resp == nil || resp.Data == nil {
		return
	}
	originalTotal := resp.Data.Total
	resp.Data.Values = filterByName(resp.Data.Values, name, func(v arubatypes.SecurityGroupResponse) *string {
		return v.Metadata.Name
	})
	resp.Data.Total = int64(len(resp.Data.Values))
	if resp.Data.Total != originalTotal {
		logger.Info("applied client-side name filter workaround for CMP API bug",
			"resourceType", "SecurityGroup",
			"filterName", name,
			"originalTotal", originalTotal,
			"filteredTotal", resp.Data.Total,
		)
	}
}

// applyNameFilterToSecurityRuleList filters the SecurityRule list response client-side by name.
// TODO: Remove once CMP API name:eq() filter is fixed (issue https://jira.aruba.it/browse/DEV-66643).
func applyNameFilterToSecurityRuleList(resp *arubatypes.Response[arubatypes.SecurityRuleListResponse], name string, logger logr.Logger) {
	if resp == nil || resp.Data == nil {
		return
	}
	originalTotal := resp.Data.Total
	resp.Data.Values = filterByName(resp.Data.Values, name, func(v arubatypes.SecurityRuleResponse) *string {
		return v.Metadata.Name
	})
	resp.Data.Total = int64(len(resp.Data.Values))
	if resp.Data.Total != originalTotal {
		logger.Info("applied client-side name filter workaround for CMP API bug",
			"resourceType", "SecurityRule",
			"filterName", name,
			"originalTotal", originalTotal,
			"filteredTotal", resp.Data.Total,
		)
	}
}

// applyNameFilterToElasticIPList filters the ElasticIP list response client-side by name.
// TODO: Remove once CMP API name:eq() filter is fixed (issue https://jira.aruba.it/browse/DEV-66643).
func applyNameFilterToElasticIPList(resp *arubatypes.Response[arubatypes.ElasticIPListResponse], name string, logger logr.Logger) {
	if resp == nil || resp.Data == nil {
		return
	}
	originalTotal := resp.Data.Total
	resp.Data.Values = filterByName(resp.Data.Values, name, func(v arubatypes.ElasticIPResponse) *string {
		return v.Metadata.Name
	})
	resp.Data.Total = int64(len(resp.Data.Values))
	if resp.Data.Total != originalTotal {
		logger.Info("applied client-side name filter workaround for CMP API bug",
			"resourceType", "ElasticIP",
			"filterName", name,
			"originalTotal", originalTotal,
			"filteredTotal", resp.Data.Total,
		)
	}
}
