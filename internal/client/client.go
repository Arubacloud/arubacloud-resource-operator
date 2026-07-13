// Package client is the operator's thin port over the Aruba Cloud SDK.
//
// The SDK (github.com/Arubacloud/sdk-go v1.x) exposes an object-oriented,
// wrapper-based API (fluent builders + hydrated resource objects). The
// operator's reconciliation engine, however, is generic over the wire
// response types (*types.XxxResponse) so that responses can be constructed
// directly in unit tests. This package bridges the two: it presents a
// request-in / wire-response-out surface (mirroring the SDK's previous
// low-level client shape) and, in adapter.go, translates to and from the
// new SDK's wrapper objects.
//
// Controllers depend only on these interfaces; unit tests mock them.
package client

import (
	"context"

	"github.com/Arubacloud/sdk-go/pkg/types"
)

// Client is the root port: one accessor per Aruba Cloud service domain the
// operator uses.
type Client interface {
	FromProject() ProjectClient
	FromNetwork() NetworkClient
	FromCompute() ComputeClient
	FromStorage() StorageClient
}

// ProjectClient manages Projects.
type ProjectClient interface {
	List(ctx context.Context, params *types.RequestParameters) (*types.Response[types.ProjectListResponse], error)
	Create(ctx context.Context, body types.ProjectRequest, params *types.RequestParameters) (*types.Response[types.ProjectResponse], error)
	Update(ctx context.Context, projectID string, body types.ProjectRequest, params *types.RequestParameters) (*types.Response[types.ProjectResponse], error)
	Delete(ctx context.Context, projectID string, params *types.RequestParameters) (*types.Response[any], error)
}

// NetworkClient exposes the network-domain resource clients.
type NetworkClient interface {
	VPCs() VPCsClient
	Subnets() SubnetsClient
	SecurityGroups() SecurityGroupsClient
	SecurityGroupRules() SecurityGroupRulesClient
	ElasticIPs() ElasticIPsClient
}

// VPCsClient manages VPCs (children of a Project).
type VPCsClient interface {
	List(ctx context.Context, projectID string, params *types.RequestParameters) (*types.Response[types.VPCListResponse], error)
	Create(ctx context.Context, projectID string, body types.VPCRequest, params *types.RequestParameters) (*types.Response[types.VPCResponse], error)
	Update(ctx context.Context, projectID, vpcID string, body types.VPCRequest, params *types.RequestParameters) (*types.Response[types.VPCResponse], error)
	Delete(ctx context.Context, projectID, vpcID string, params *types.RequestParameters) (*types.Response[any], error)
}

// SubnetsClient manages Subnets (children of a VPC).
type SubnetsClient interface {
	List(ctx context.Context, projectID, vpcID string, params *types.RequestParameters) (*types.Response[types.SubnetListResponse], error)
	Create(ctx context.Context, projectID, vpcID string, body types.SubnetRequest, params *types.RequestParameters) (*types.Response[types.SubnetResponse], error)
	Update(ctx context.Context, projectID, vpcID, subnetID string, body types.SubnetRequest, params *types.RequestParameters) (*types.Response[types.SubnetResponse], error)
	Delete(ctx context.Context, projectID, vpcID, subnetID string, params *types.RequestParameters) (*types.Response[any], error)
}

// SecurityGroupsClient manages Security Groups (children of a VPC).
type SecurityGroupsClient interface {
	List(ctx context.Context, projectID, vpcID string, params *types.RequestParameters) (*types.Response[types.SecurityGroupListResponse], error)
	Create(ctx context.Context, projectID, vpcID string, body types.SecurityGroupRequest, params *types.RequestParameters) (*types.Response[types.SecurityGroupResponse], error)
	Update(ctx context.Context, projectID, vpcID, securityGroupID string, body types.SecurityGroupRequest, params *types.RequestParameters) (*types.Response[types.SecurityGroupResponse], error)
	Delete(ctx context.Context, projectID, vpcID, securityGroupID string, params *types.RequestParameters) (*types.Response[any], error)
}

// SecurityGroupRulesClient manages Security Rules (children of a Security Group).
type SecurityGroupRulesClient interface {
	List(ctx context.Context, projectID, vpcID, securityGroupID string, params *types.RequestParameters) (*types.Response[types.SecurityRuleListResponse], error)
	Create(ctx context.Context, projectID, vpcID, securityGroupID string, body types.SecurityRuleRequest, params *types.RequestParameters) (*types.Response[types.SecurityRuleResponse], error)
	Delete(ctx context.Context, projectID, vpcID, securityGroupID, securityGroupRuleID string, params *types.RequestParameters) (*types.Response[any], error)
}

// ElasticIPsClient manages Elastic IPs (children of a Project).
type ElasticIPsClient interface {
	List(ctx context.Context, projectID string, params *types.RequestParameters) (*types.Response[types.ElasticIPListResponse], error)
	Create(ctx context.Context, projectID string, body types.ElasticIPRequest, params *types.RequestParameters) (*types.Response[types.ElasticIPResponse], error)
	Update(ctx context.Context, projectID, elasticIPID string, body types.ElasticIPRequest, params *types.RequestParameters) (*types.Response[types.ElasticIPResponse], error)
	Delete(ctx context.Context, projectID, elasticIPID string, params *types.RequestParameters) (*types.Response[any], error)
}

// ComputeClient exposes the compute-domain resource clients.
type ComputeClient interface {
	CloudServers() CloudServersClient
	KeyPairs() KeyPairsClient
}

// CloudServersClient manages Cloud Servers (children of a Project).
type CloudServersClient interface {
	List(ctx context.Context, projectID string, params *types.RequestParameters) (*types.Response[types.CloudServerListResponse], error)
	Create(ctx context.Context, projectID string, body types.CloudServerRequest, params *types.RequestParameters) (*types.Response[types.CloudServerResponse], error)
	Update(ctx context.Context, projectID, cloudServerID string, body types.CloudServerRequest, params *types.RequestParameters) (*types.Response[types.CloudServerResponse], error)
	Delete(ctx context.Context, projectID, cloudServerID string, params *types.RequestParameters) (*types.Response[any], error)
}

// KeyPairsClient manages Key Pairs (children of a Project).
type KeyPairsClient interface {
	List(ctx context.Context, projectID string, params *types.RequestParameters) (*types.Response[types.KeyPairListResponse], error)
	Create(ctx context.Context, projectID string, body types.KeyPairRequest, params *types.RequestParameters) (*types.Response[types.KeyPairResponse], error)
	Delete(ctx context.Context, projectID, keyPairID string, params *types.RequestParameters) (*types.Response[any], error)
}

// StorageClient exposes the storage-domain resource clients.
type StorageClient interface {
	Volumes() VolumesClient
}

// VolumesClient manages Block Storage volumes (children of a Project).
type VolumesClient interface {
	List(ctx context.Context, projectID string, params *types.RequestParameters) (*types.Response[types.BlockStorageListResponse], error)
	Create(ctx context.Context, projectID string, body types.BlockStorageRequest, params *types.RequestParameters) (*types.Response[types.BlockStorageResponse], error)
	Update(ctx context.Context, projectID, volumeID string, body types.BlockStorageRequest, params *types.RequestParameters) (*types.Response[types.BlockStorageResponse], error)
	Delete(ctx context.Context, projectID, volumeID string, params *types.RequestParameters) (*types.Response[any], error)
}
