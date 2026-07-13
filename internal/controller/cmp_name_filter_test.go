package controller

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/log"

	arubatypes "github.com/Arubacloud/sdk-go/pkg/types"
)

var _ = Describe("filterByName", func() {
	type item struct {
		name *string
		val  int
	}
	nameOf := func(i item) *string { return i.name }
	ptr := func(s string) *string { return &s }

	It("keeps only the item with the matching name", func() {
		items := []item{{ptr("foo"), 1}, {ptr("bar"), 2}, {ptr("foo"), 3}}
		result := filterByName(items, "bar", nameOf)
		Expect(result).To(HaveLen(1))
		Expect(result[0].val).To(Equal(2))
	})

	It("returns empty slice when no item matches", func() {
		items := []item{{ptr("foo"), 1}, {ptr("bar"), 2}}
		result := filterByName(items, "baz", nameOf)
		Expect(result).To(BeEmpty())
	})

	It("keeps all items when all match", func() {
		items := []item{{ptr("x"), 1}, {ptr("x"), 2}}
		result := filterByName(items, "x", nameOf)
		Expect(result).To(HaveLen(2))
	})

	It("skips items with nil name without panicking", func() {
		items := []item{{nil, 1}, {ptr("target"), 2}}
		result := filterByName(items, "target", nameOf)
		Expect(result).To(HaveLen(1))
		Expect(result[0].val).To(Equal(2))
	})

	It("returns empty slice for empty input", func() {
		result := filterByName([]item{}, "x", nameOf)
		Expect(result).To(BeEmpty())
	})
})

var _ = Describe("applyNameFilterToVPCList", func() {
	logger := log.Log

	buildVpc := func(name string) arubatypes.VPCResponse {
		return arubatypes.VPCResponse{Metadata: arubatypes.ResourceMetadataResponse{Name: &name}}
	}

	It("does nothing when resp is nil", func() {
		Expect(func() { applyNameFilterToVPCList(nil, "x", logger) }).NotTo(Panic())
	})

	It("does nothing when Data is nil", func() {
		resp := &arubatypes.Response[arubatypes.VPCListResponse]{StatusCode: http.StatusOK}
		Expect(func() { applyNameFilterToVPCList(resp, "x", logger) }).NotTo(Panic())
	})

	It("is a no-op when the list already contains only the matching item", func() {
		resp := buildVPCList(buildVPCResponse("id1", "target", arubatypes.StateActive))
		applyNameFilterToVPCList(resp, "target", logger)
		Expect(resp.Data.Total).To(Equal(int64(1)))
		Expect(resp.Data.Values).To(HaveLen(1))
	})

	It("filters out non-matching items and updates Total", func() {
		resp := &arubatypes.Response[arubatypes.VPCListResponse]{
			StatusCode: http.StatusOK,
			Data: &arubatypes.VPCListResponse{
				ListResponse: arubatypes.ListResponse{Total: 3},
				Values:       []arubatypes.VPCResponse{buildVpc("other"), buildVpc("target"), buildVpc("another")},
			},
		}
		applyNameFilterToVPCList(resp, "target", logger)
		Expect(resp.Data.Total).To(Equal(int64(1)))
		Expect(resp.Data.Values).To(HaveLen(1))
		Expect(*resp.Data.Values[0].Metadata.Name).To(Equal("target"))
	})

	It("sets Total to 0 when no item matches", func() {
		resp := &arubatypes.Response[arubatypes.VPCListResponse]{
			StatusCode: http.StatusOK,
			Data: &arubatypes.VPCListResponse{
				ListResponse: arubatypes.ListResponse{Total: 2},
				Values:       []arubatypes.VPCResponse{buildVpc("a"), buildVpc("b")},
			},
		}
		applyNameFilterToVPCList(resp, "notfound", logger)
		Expect(resp.Data.Total).To(Equal(int64(0)))
		Expect(resp.Data.Values).To(BeEmpty())
	})
})

var _ = Describe("applyNameFilterToSubnetList", func() {
	logger := log.Log

	buildSubnet := func(name string) arubatypes.SubnetResponse {
		return arubatypes.SubnetResponse{Metadata: arubatypes.ResourceMetadataResponse{Name: &name}}
	}

	It("does nothing when resp or Data is nil", func() {
		Expect(func() { applyNameFilterToSubnetList(nil, "x", logger) }).NotTo(Panic())
		resp := &arubatypes.Response[arubatypes.SubnetListResponse]{StatusCode: http.StatusOK}
		Expect(func() { applyNameFilterToSubnetList(resp, "x", logger) }).NotTo(Panic())
	})

	It("filters correctly and updates Total", func() {
		resp := &arubatypes.Response[arubatypes.SubnetListResponse]{
			StatusCode: http.StatusOK,
			Data: &arubatypes.SubnetListResponse{
				ListResponse: arubatypes.ListResponse{Total: 2},
				Values:       []arubatypes.SubnetResponse{buildSubnet("other"), buildSubnet("target")},
			},
		}
		applyNameFilterToSubnetList(resp, "target", logger)
		Expect(resp.Data.Total).To(Equal(int64(1)))
		Expect(*resp.Data.Values[0].Metadata.Name).To(Equal("target"))
	})
})

var _ = Describe("applyNameFilterToSecurityGroupList", func() {
	logger := log.Log

	buildSG := func(name string) arubatypes.SecurityGroupResponse {
		return arubatypes.SecurityGroupResponse{Metadata: arubatypes.ResourceMetadataResponse{Name: &name}}
	}

	It("does nothing when resp or Data is nil", func() {
		Expect(func() { applyNameFilterToSecurityGroupList(nil, "x", logger) }).NotTo(Panic())
		resp := &arubatypes.Response[arubatypes.SecurityGroupListResponse]{StatusCode: http.StatusOK}
		Expect(func() { applyNameFilterToSecurityGroupList(resp, "x", logger) }).NotTo(Panic())
	})

	It("filters correctly and updates Total", func() {
		resp := &arubatypes.Response[arubatypes.SecurityGroupListResponse]{
			StatusCode: http.StatusOK,
			Data: &arubatypes.SecurityGroupListResponse{
				ListResponse: arubatypes.ListResponse{Total: 3},
				Values:       []arubatypes.SecurityGroupResponse{buildSG("a"), buildSG("target"), buildSG("b")},
			},
		}
		applyNameFilterToSecurityGroupList(resp, "target", logger)
		Expect(resp.Data.Total).To(Equal(int64(1)))
		Expect(*resp.Data.Values[0].Metadata.Name).To(Equal("target"))
	})
})

var _ = Describe("applyNameFilterToSecurityRuleList", func() {
	logger := log.Log

	buildSR := func(name string) arubatypes.SecurityRuleResponse {
		return arubatypes.SecurityRuleResponse{Metadata: arubatypes.ResourceMetadataResponse{Name: &name}}
	}

	It("does nothing when resp or Data is nil", func() {
		Expect(func() { applyNameFilterToSecurityRuleList(nil, "x", logger) }).NotTo(Panic())
		resp := &arubatypes.Response[arubatypes.SecurityRuleListResponse]{StatusCode: http.StatusOK}
		Expect(func() { applyNameFilterToSecurityRuleList(resp, "x", logger) }).NotTo(Panic())
	})

	It("filters correctly and updates Total", func() {
		resp := &arubatypes.Response[arubatypes.SecurityRuleListResponse]{
			StatusCode: http.StatusOK,
			Data: &arubatypes.SecurityRuleListResponse{
				ListResponse: arubatypes.ListResponse{Total: 2},
				Values:       []arubatypes.SecurityRuleResponse{buildSR("other"), buildSR("target")},
			},
		}
		applyNameFilterToSecurityRuleList(resp, "target", logger)
		Expect(resp.Data.Total).To(Equal(int64(1)))
		Expect(*resp.Data.Values[0].Metadata.Name).To(Equal("target"))
	})
})

var _ = Describe("applyNameFilterToElasticIPList", func() {
	logger := log.Log

	buildEIP := func(name string) arubatypes.ElasticIPResponse {
		return arubatypes.ElasticIPResponse{Metadata: arubatypes.ResourceMetadataResponse{Name: &name}}
	}

	It("does nothing when resp or Data is nil", func() {
		Expect(func() { applyNameFilterToElasticIPList(nil, "x", logger) }).NotTo(Panic())
		resp := &arubatypes.Response[arubatypes.ElasticIPListResponse]{StatusCode: http.StatusOK}
		Expect(func() { applyNameFilterToElasticIPList(resp, "x", logger) }).NotTo(Panic())
	})

	It("filters correctly and updates Total", func() {
		resp := &arubatypes.Response[arubatypes.ElasticIPListResponse]{
			StatusCode: http.StatusOK,
			Data: &arubatypes.ElasticIPListResponse{
				ListResponse: arubatypes.ListResponse{Total: 2},
				Values:       []arubatypes.ElasticIPResponse{buildEIP("other"), buildEIP("target")},
			},
		}
		applyNameFilterToElasticIPList(resp, "target", logger)
		Expect(resp.Data.Total).To(Equal(int64(1)))
		Expect(*resp.Data.Values[0].Metadata.Name).To(Equal("target"))
	})
})
