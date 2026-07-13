package client

import (
	"context"
	"errors"
	"net/http"

	"github.com/Arubacloud/sdk-go/pkg/aruba"
	"github.com/Arubacloud/sdk-go/pkg/types"
)

// New wraps a raw SDK client in the operator's port. The returned Client speaks
// the request-in / wire-response-out surface the reconciliation engine expects,
// translating to and from the SDK's wrapper objects internally.
func New(sdk aruba.Client) Client { return &clientAdapter{sdk: sdk} }

type clientAdapter struct{ sdk aruba.Client }

func (c *clientAdapter) FromProject() ProjectClient { return projectAdapter{c.sdk.FromProject()} }
func (c *clientAdapter) FromNetwork() NetworkClient { return networkAdapter{c.sdk.FromNetwork()} }
func (c *clientAdapter) FromCompute() ComputeClient { return computeAdapter{c.sdk.FromCompute()} }
func (c *clientAdapter) FromStorage() StorageClient { return storageAdapter{c.sdk.FromStorage()} }

// ---------------------------------------------------------------------------
// Shared translation helpers
// ---------------------------------------------------------------------------

// opts converts the operator's wire request parameters into SDK call options.
func opts(params *types.RequestParameters) []aruba.CallOption {
	if params == nil {
		return nil
	}
	return []aruba.CallOption{aruba.WithRawParameters(params)}
}

// projectRef builds a Ref that resolves to the given project ID.
func projectRef(projectID string) aruba.Ref { return aruba.URI("/projects/" + projectID) }

// classify splits a wrapper-call error. An *aruba.HTTPError (a non-2xx reply)
// is surfaced through the wire Response — matching the previous SDK, where HTTP
// errors were carried in the response rather than returned as an error. Any
// other error is a genuine transport failure returned to the caller.
func classify(err error) (status int, errResp *types.ErrorResponse, transport error) {
	if err == nil {
		return 0, nil, nil
	}
	var he *aruba.HTTPError
	if errors.As(err, &he) {
		return he.StatusCode, he.ErrResp, nil
	}
	return 0, nil, err
}

// wireEnvelope is satisfied by every SDK resource wrapper: it exposes the HTTP
// envelope plus the underlying wire response.
type wireEnvelope[R any] interface {
	StatusCode() int
	RawError() *types.ErrorResponse
	Raw() *R
}

// objResp converts a single-resource wrapper reply into a wire Response.
func objResp[R any](w wireEnvelope[R], err error) (*types.Response[R], error) {
	resp := &types.Response[R]{}
	if w != nil {
		resp.StatusCode = w.StatusCode()
		resp.Error = w.RawError()
		resp.Data = w.Raw()
	}
	if status, errResp, transport := classify(err); transport != nil {
		return resp, transport
	} else if status != 0 {
		resp.StatusCode = status
		resp.Error = errResp
	}
	return resp, nil
}

// listResp converts a paginated wrapper list into a wire Response. On success
// Data is always non-nil so callers can read Total/Values without a nil check.
func listResp[L any, W aruba.Wrapper](l *aruba.List[W], err error) (*types.Response[L], error) {
	resp := &types.Response[L]{}
	if l != nil {
		resp.StatusCode = l.StatusCode()
		resp.Error = l.RawError()
		resp.Headers = l.Headers()
		if raw, ok := l.Raw().(*L); ok {
			resp.Data = raw
		}
	}
	if status, errResp, transport := classify(err); transport != nil {
		return resp, transport
	} else if status != 0 {
		resp.StatusCode = status
		resp.Error = errResp
	}
	if resp.Data == nil && resp.Error == nil {
		resp.Data = new(L)
	}
	return resp, nil
}

// deleteResp maps a wrapper Delete (which returns only an error) onto the wire
// Response the engine's error helpers expect. A nil error is a 2xx success.
func deleteResp(err error) (*types.Response[any], error) {
	if status, errResp, transport := classify(err); transport != nil {
		return &types.Response[any]{}, transport
	} else if status != 0 {
		return &types.Response[any]{StatusCode: status, Error: errResp}, nil
	}
	return &types.Response[any]{StatusCode: http.StatusOK}, nil
}

// hydrateErr converts a Get() used to hydrate a wrapper before Update into an
// early return. done=true means the caller should return (resp, err) as-is.
func hydrateErr[R any](w wireEnvelope[R], err error) (resp *types.Response[R], out error, done bool) {
	if err == nil {
		return nil, nil, false
	}
	r, e := objResp(w, err)
	return r, e, true
}

// urisToRefs turns a slice of wire references into SDK Refs.
func urisToRefs(refs []types.ReferenceResourceCommon) []aruba.Ref {
	out := make([]aruba.Ref, 0, len(refs))
	for _, r := range refs {
		out = append(out, aruba.URI(r.URI))
	}
	return out
}

// ---------------------------------------------------------------------------
// Project
// ---------------------------------------------------------------------------

type projectAdapter struct{ c aruba.ProjectClient }

func (a projectAdapter) List(ctx context.Context, params *types.RequestParameters) (*types.Response[types.ProjectListResponse], error) {
	l, err := a.c.List(ctx, opts(params)...)
	return listResp[types.ProjectListResponse](l, err)
}

func (a projectAdapter) Create(ctx context.Context, body types.ProjectRequest, params *types.RequestParameters) (*types.Response[types.ProjectResponse], error) {
	w := aruba.NewProject()
	applyProject(w, body)
	res, err := a.c.Create(ctx, w, opts(params)...)
	return objResp(res, err)
}

func (a projectAdapter) Update(ctx context.Context, projectID string, body types.ProjectRequest, params *types.RequestParameters) (*types.Response[types.ProjectResponse], error) {
	w, err := a.c.Get(ctx, projectRef(projectID), opts(params)...)
	if resp, out, done := hydrateErr(w, err); done {
		return resp, out
	}
	applyProject(w, body)
	res, err := a.c.Update(ctx, w, opts(params)...)
	return objResp(res, err)
}

func (a projectAdapter) Delete(ctx context.Context, projectID string, params *types.RequestParameters) (*types.Response[any], error) {
	return deleteResp(a.c.Delete(ctx, projectRef(projectID), opts(params)...))
}

func applyProject(w *aruba.Project, b types.ProjectRequest) {
	w.Named(b.Metadata.Name).RetaggedAs(b.Metadata.Tags...)
	if b.Properties.Description != nil {
		w.DescribedAs(*b.Properties.Description)
	}
	if b.Properties.Default {
		w.AsDefault()
	} else {
		w.NotDefault()
	}
}

// ---------------------------------------------------------------------------
// Network
// ---------------------------------------------------------------------------

type networkAdapter struct{ c aruba.NetworkClient }

func (a networkAdapter) VPCs() VPCsClient       { return vpcsAdapter{a.c.VPCs()} }
func (a networkAdapter) Subnets() SubnetsClient { return subnetsAdapter{a.c.Subnets()} }
func (a networkAdapter) SecurityGroups() SecurityGroupsClient {
	return securityGroupsAdapter{a.c.SecurityGroups()}
}
func (a networkAdapter) SecurityGroupRules() SecurityGroupRulesClient {
	return securityGroupRulesAdapter{a.c.SecurityGroupRules()}
}
func (a networkAdapter) ElasticIPs() ElasticIPsClient { return elasticIPsAdapter{a.c.ElasticIPs()} }

// ---- VPCs ----

type vpcsAdapter struct{ c aruba.VPCsClient }

func (a vpcsAdapter) List(ctx context.Context, projectID string, params *types.RequestParameters) (*types.Response[types.VPCListResponse], error) {
	l, err := a.c.List(ctx, projectRef(projectID), opts(params)...)
	return listResp[types.VPCListResponse](l, err)
}

func (a vpcsAdapter) Create(ctx context.Context, projectID string, body types.VPCRequest, params *types.RequestParameters) (*types.Response[types.VPCResponse], error) {
	w := aruba.NewVPC().InProject(projectRef(projectID))
	applyVPC(w, body)
	res, err := a.c.Create(ctx, w, opts(params)...)
	return objResp(res, err)
}

func (a vpcsAdapter) Update(ctx context.Context, projectID, vpcID string, body types.VPCRequest, params *types.RequestParameters) (*types.Response[types.VPCResponse], error) {
	w, err := a.c.Get(ctx, aruba.VPCRef(projectID, vpcID), opts(params)...)
	if resp, out, done := hydrateErr(w, err); done {
		return resp, out
	}
	applyVPC(w, body)
	res, err := a.c.Update(ctx, w, opts(params)...)
	return objResp(res, err)
}

func (a vpcsAdapter) Delete(ctx context.Context, projectID, vpcID string, params *types.RequestParameters) (*types.Response[any], error) {
	return deleteResp(a.c.Delete(ctx, aruba.VPCRef(projectID, vpcID), opts(params)...))
}

func applyVPC(w *aruba.VPC, b types.VPCRequest) {
	w.Named(b.Metadata.Name).RetaggedAs(b.Metadata.Tags...).InRegion(b.Metadata.Location.Value)
	if p := b.Properties.Properties; p != nil {
		if p.Default != nil {
			if *p.Default {
				w.AsDefault()
			} else {
				w.NotDefault()
			}
		}
		if p.Preset != nil {
			if *p.Preset {
				w.WithPreset()
			} else {
				w.WithoutPreset()
			}
		}
	}
}

// ---- Subnets ----

type subnetsAdapter struct{ c aruba.SubnetsClient }

func (a subnetsAdapter) List(ctx context.Context, projectID, vpcID string, params *types.RequestParameters) (*types.Response[types.SubnetListResponse], error) {
	l, err := a.c.List(ctx, aruba.VPCRef(projectID, vpcID), opts(params)...)
	return listResp[types.SubnetListResponse](l, err)
}

func (a subnetsAdapter) Create(ctx context.Context, projectID, vpcID string, body types.SubnetRequest, params *types.RequestParameters) (*types.Response[types.SubnetResponse], error) {
	w := aruba.NewSubnet().InVPC(aruba.VPCRef(projectID, vpcID))
	applySubnet(w, body)
	res, err := a.c.Create(ctx, w, opts(params)...)
	return objResp(res, err)
}

func (a subnetsAdapter) Update(ctx context.Context, projectID, vpcID, subnetID string, body types.SubnetRequest, params *types.RequestParameters) (*types.Response[types.SubnetResponse], error) {
	w, err := a.c.Get(ctx, aruba.SubnetRef(projectID, vpcID, subnetID), opts(params)...)
	if resp, out, done := hydrateErr(w, err); done {
		return resp, out
	}
	applySubnet(w, body)
	res, err := a.c.Update(ctx, w, opts(params)...)
	return objResp(res, err)
}

func (a subnetsAdapter) Delete(ctx context.Context, projectID, vpcID, subnetID string, params *types.RequestParameters) (*types.Response[any], error) {
	return deleteResp(a.c.Delete(ctx, aruba.SubnetRef(projectID, vpcID, subnetID), opts(params)...))
}

func applySubnet(w *aruba.Subnet, b types.SubnetRequest) {
	w.Named(b.Metadata.Name).RetaggedAs(b.Metadata.Tags...).InRegion(b.Metadata.Location.Value)
	if b.Properties.Type != "" {
		w.OfType(b.Properties.Type)
	}
	if b.Properties.Default != nil {
		if *b.Properties.Default {
			w.AsDefault()
		} else {
			w.NotDefault()
		}
	}
	if b.Properties.Network != nil {
		w.WithCIDR(b.Properties.Network.Address)
	}
	if b.Properties.DHCP != nil {
		d := aruba.NewSubnetDHCP()
		if b.Properties.DHCP.Enabled {
			d.Enabled()
		}
		w.WithDHCP(d)
	}
}

// ---- Security Groups ----

type securityGroupsAdapter struct{ c aruba.SecurityGroupsClient }

func (a securityGroupsAdapter) List(ctx context.Context, projectID, vpcID string, params *types.RequestParameters) (*types.Response[types.SecurityGroupListResponse], error) {
	l, err := a.c.List(ctx, aruba.VPCRef(projectID, vpcID), opts(params)...)
	return listResp[types.SecurityGroupListResponse](l, err)
}

func (a securityGroupsAdapter) Create(ctx context.Context, projectID, vpcID string, body types.SecurityGroupRequest, params *types.RequestParameters) (*types.Response[types.SecurityGroupResponse], error) {
	w := aruba.NewSecurityGroup().InVPC(aruba.VPCRef(projectID, vpcID))
	applySecurityGroup(w, body)
	res, err := a.c.Create(ctx, w, opts(params)...)
	return objResp(res, err)
}

func (a securityGroupsAdapter) Update(ctx context.Context, projectID, vpcID, securityGroupID string, body types.SecurityGroupRequest, params *types.RequestParameters) (*types.Response[types.SecurityGroupResponse], error) {
	w, err := a.c.Get(ctx, aruba.SecurityGroupRef(projectID, vpcID, securityGroupID), opts(params)...)
	if resp, out, done := hydrateErr(w, err); done {
		return resp, out
	}
	applySecurityGroup(w, body)
	res, err := a.c.Update(ctx, w, opts(params)...)
	return objResp(res, err)
}

func (a securityGroupsAdapter) Delete(ctx context.Context, projectID, vpcID, securityGroupID string, params *types.RequestParameters) (*types.Response[any], error) {
	return deleteResp(a.c.Delete(ctx, aruba.SecurityGroupRef(projectID, vpcID, securityGroupID), opts(params)...))
}

func applySecurityGroup(w *aruba.SecurityGroup, b types.SecurityGroupRequest) {
	w.Named(b.Metadata.Name).RetaggedAs(b.Metadata.Tags...)
	if b.Properties.Default != nil && *b.Properties.Default {
		w.AsDefault()
	} else {
		w.NotDefault()
	}
}

// ---- Security Group Rules ----

type securityGroupRulesAdapter struct {
	c aruba.SecurityGroupRulesClient
}

func (a securityGroupRulesAdapter) List(ctx context.Context, projectID, vpcID, securityGroupID string, params *types.RequestParameters) (*types.Response[types.SecurityRuleListResponse], error) {
	l, err := a.c.List(ctx, aruba.SecurityGroupRef(projectID, vpcID, securityGroupID), opts(params)...)
	return listResp[types.SecurityRuleListResponse](l, err)
}

func (a securityGroupRulesAdapter) Create(ctx context.Context, projectID, vpcID, securityGroupID string, body types.SecurityRuleRequest, params *types.RequestParameters) (*types.Response[types.SecurityRuleResponse], error) {
	w := aruba.NewSecurityRule().InSecurityGroup(aruba.SecurityGroupRef(projectID, vpcID, securityGroupID))
	applySecurityRule(w, body)
	res, err := a.c.Create(ctx, w, opts(params)...)
	return objResp(res, err)
}

func (a securityGroupRulesAdapter) Delete(ctx context.Context, projectID, vpcID, securityGroupID, securityGroupRuleID string, params *types.RequestParameters) (*types.Response[any], error) {
	return deleteResp(a.c.Delete(ctx, aruba.SecurityRuleRef(projectID, vpcID, securityGroupID, securityGroupRuleID), opts(params)...))
}

func applySecurityRule(w *aruba.SecurityRule, b types.SecurityRuleRequest) {
	w.Named(b.Metadata.Name).RetaggedAs(b.Metadata.Tags...).InRegion(b.Metadata.Location.Value)
	if b.Properties.Direction != "" {
		w.WithDirection(b.Properties.Direction)
	}
	if b.Properties.Protocol != "" {
		w.WithProtocol(b.Properties.Protocol)
	}
	if b.Properties.Port != "" {
		w.WithPort(b.Properties.Port)
	}
	if t := b.Properties.Target; t != nil {
		if t.Kind == types.EndpointTypeSecurityGroup {
			w.TargetingSecurityGroup(aruba.URI(t.Value))
		} else {
			w.TargetingCIDR(t.Value)
		}
	}
}

// ---- Elastic IPs ----

type elasticIPsAdapter struct{ c aruba.ElasticIPsClient }

func (a elasticIPsAdapter) List(ctx context.Context, projectID string, params *types.RequestParameters) (*types.Response[types.ElasticIPListResponse], error) {
	l, err := a.c.List(ctx, projectRef(projectID), opts(params)...)
	return listResp[types.ElasticIPListResponse](l, err)
}

func (a elasticIPsAdapter) Create(ctx context.Context, projectID string, body types.ElasticIPRequest, params *types.RequestParameters) (*types.Response[types.ElasticIPResponse], error) {
	w := aruba.NewElasticIP().InProject(projectRef(projectID))
	applyElasticIP(w, body)
	res, err := a.c.Create(ctx, w, opts(params)...)
	return objResp(res, err)
}

func (a elasticIPsAdapter) Update(ctx context.Context, projectID, elasticIPID string, body types.ElasticIPRequest, params *types.RequestParameters) (*types.Response[types.ElasticIPResponse], error) {
	w, err := a.c.Get(ctx, aruba.ElasticIPRef(projectID, elasticIPID), opts(params)...)
	if resp, out, done := hydrateErr(w, err); done {
		return resp, out
	}
	applyElasticIP(w, body)
	res, err := a.c.Update(ctx, w, opts(params)...)
	return objResp(res, err)
}

func (a elasticIPsAdapter) Delete(ctx context.Context, projectID, elasticIPID string, params *types.RequestParameters) (*types.Response[any], error) {
	return deleteResp(a.c.Delete(ctx, aruba.ElasticIPRef(projectID, elasticIPID), opts(params)...))
}

func applyElasticIP(w *aruba.ElasticIP, b types.ElasticIPRequest) {
	w.Named(b.Metadata.Name).RetaggedAs(b.Metadata.Tags...).InRegion(b.Metadata.Location.Value)
	if bp := b.Properties.BillingPlanCommon; bp != nil && bp.BillingPeriod != nil {
		w.BilledBy(*bp.BillingPeriod)
	}
}

// ---------------------------------------------------------------------------
// Compute
// ---------------------------------------------------------------------------

type computeAdapter struct{ c aruba.ComputeClient }

func (a computeAdapter) CloudServers() CloudServersClient {
	return cloudServersAdapter{a.c.CloudServers()}
}
func (a computeAdapter) KeyPairs() KeyPairsClient { return keyPairsAdapter{a.c.KeyPairs()} }

// ---- Cloud Servers ----

type cloudServersAdapter struct{ c aruba.CloudServersClient }

func (a cloudServersAdapter) List(ctx context.Context, projectID string, params *types.RequestParameters) (*types.Response[types.CloudServerListResponse], error) {
	l, err := a.c.List(ctx, projectRef(projectID), opts(params)...)
	return listResp[types.CloudServerListResponse](l, err)
}

func (a cloudServersAdapter) Create(ctx context.Context, projectID string, body types.CloudServerRequest, params *types.RequestParameters) (*types.Response[types.CloudServerResponse], error) {
	w := aruba.NewCloudServer().InProject(projectRef(projectID))
	applyCloudServer(w, body)
	res, err := a.c.Create(ctx, w, opts(params)...)
	return objResp(res, err)
}

func (a cloudServersAdapter) Update(ctx context.Context, projectID, cloudServerID string, body types.CloudServerRequest, params *types.RequestParameters) (*types.Response[types.CloudServerResponse], error) {
	w, err := a.c.Get(ctx, aruba.URI("/projects/"+projectID+"/cloudServers/"+cloudServerID), opts(params)...)
	if resp, out, done := hydrateErr(w, err); done {
		return resp, out
	}
	applyCloudServer(w, body)
	res, err := a.c.Update(ctx, w, opts(params)...)
	return objResp(res, err)
}

func (a cloudServersAdapter) Delete(ctx context.Context, projectID, cloudServerID string, params *types.RequestParameters) (*types.Response[any], error) {
	return deleteResp(a.c.Delete(ctx, aruba.URI("/projects/"+projectID+"/cloudServers/"+cloudServerID), opts(params)...))
}

func applyCloudServer(w *aruba.CloudServer, b types.CloudServerRequest) {
	w.Named(b.Metadata.Name).RetaggedAs(b.Metadata.Tags...).InRegion(b.Metadata.Location.Value)
	p := b.Properties
	if p.Zone != "" {
		w.InZone(p.Zone)
	}
	if p.FlavorName != nil && *p.FlavorName != "" {
		w.OfFlavor(*p.FlavorName)
	}
	if p.VPCPreset {
		w.WithVPCPreset()
	} else {
		w.WithoutVPCPreset()
	}
	if p.VPC.URI != "" {
		w.WithVPC(aruba.URI(p.VPC.URI))
	}
	if p.BootVolume.URI != "" {
		w.BootingFrom(aruba.URI(p.BootVolume.URI))
	}
	if len(p.Subnets) > 0 {
		w.OnSubnets(urisToRefs(p.Subnets)...)
	}
	if len(p.SecurityGroups) > 0 {
		w.WithSecurityGroups(urisToRefs(p.SecurityGroups)...)
	}
	if p.KeyPair.URI != "" {
		w.UsingKeyPair(aruba.URI(p.KeyPair.URI))
	}
	if p.ElasticIP.URI != "" {
		w.WithElasticIP(aruba.URI(p.ElasticIP.URI))
	}
}

// ---- Key Pairs ----

type keyPairsAdapter struct{ c aruba.KeyPairsClient }

func (a keyPairsAdapter) List(ctx context.Context, projectID string, params *types.RequestParameters) (*types.Response[types.KeyPairListResponse], error) {
	l, err := a.c.List(ctx, projectRef(projectID), opts(params)...)
	return listResp[types.KeyPairListResponse](l, err)
}

func (a keyPairsAdapter) Create(ctx context.Context, projectID string, body types.KeyPairRequest, params *types.RequestParameters) (*types.Response[types.KeyPairResponse], error) {
	w := aruba.NewKeyPair().InProject(projectRef(projectID))
	applyKeyPair(w, body)
	res, err := a.c.Create(ctx, w, opts(params)...)
	return objResp(res, err)
}

func (a keyPairsAdapter) Delete(ctx context.Context, projectID, keyPairID string, params *types.RequestParameters) (*types.Response[any], error) {
	return deleteResp(a.c.Delete(ctx, aruba.URI("/projects/"+projectID+"/keyPairs/"+keyPairID), opts(params)...))
}

func applyKeyPair(w *aruba.KeyPair, b types.KeyPairRequest) {
	w.Named(b.Metadata.Name).RetaggedAs(b.Metadata.Tags...).InRegion(b.Metadata.Location.Value)
	if b.Properties.Value != "" {
		w.WithPublicKey(b.Properties.Value)
	}
}

// ---------------------------------------------------------------------------
// Storage
// ---------------------------------------------------------------------------

type storageAdapter struct{ c aruba.StorageClient }

func (a storageAdapter) Volumes() VolumesClient { return volumesAdapter{a.c.Volumes()} }

type volumesAdapter struct{ c aruba.VolumesClient }

func (a volumesAdapter) List(ctx context.Context, projectID string, params *types.RequestParameters) (*types.Response[types.BlockStorageListResponse], error) {
	l, err := a.c.List(ctx, projectRef(projectID), opts(params)...)
	return listResp[types.BlockStorageListResponse](l, err)
}

func (a volumesAdapter) Create(ctx context.Context, projectID string, body types.BlockStorageRequest, params *types.RequestParameters) (*types.Response[types.BlockStorageResponse], error) {
	w := aruba.NewBlockStorage().InProject(projectRef(projectID))
	applyBlockStorage(w, body)
	res, err := a.c.Create(ctx, w, opts(params)...)
	return objResp(res, err)
}

func (a volumesAdapter) Update(ctx context.Context, projectID, volumeID string, body types.BlockStorageRequest, params *types.RequestParameters) (*types.Response[types.BlockStorageResponse], error) {
	w, err := a.c.Get(ctx, aruba.URI("/projects/"+projectID+"/blockStorages/"+volumeID), opts(params)...)
	if resp, out, done := hydrateErr(w, err); done {
		return resp, out
	}
	applyBlockStorage(w, body)
	res, err := a.c.Update(ctx, w, opts(params)...)
	return objResp(res, err)
}

func (a volumesAdapter) Delete(ctx context.Context, projectID, volumeID string, params *types.RequestParameters) (*types.Response[any], error) {
	return deleteResp(a.c.Delete(ctx, aruba.URI("/projects/"+projectID+"/blockStorages/"+volumeID), opts(params)...))
}

func applyBlockStorage(w *aruba.BlockStorage, b types.BlockStorageRequest) {
	w.Named(b.Metadata.Name).RetaggedAs(b.Metadata.Tags...).InRegion(b.Metadata.Location.Value)
	w.SizedGB(b.Properties.SizeGB)
	if b.Properties.Type != "" {
		w.OfType(b.Properties.Type)
	}
	if b.Properties.BillingPeriod != nil {
		w.BilledBy(*b.Properties.BillingPeriod)
	}
	if b.Properties.Zone != nil && *b.Properties.Zone != "" {
		w.InZone(*b.Properties.Zone)
	}
	if b.Properties.Image != nil && *b.Properties.Image != "" {
		w.FromImage(*b.Properties.Image)
	}
	if b.Properties.Bootable != nil && *b.Properties.Bootable {
		w.AsBootable()
	} else {
		w.NotBootable()
	}
}
