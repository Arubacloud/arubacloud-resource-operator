package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Arubacloud/sdk-go/pkg/aruba"
	arubamt "github.com/Arubacloud/sdk-go/pkg/multitenant"
	arubatypes "github.com/Arubacloud/sdk-go/pkg/types"

	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
)

// newTestReconciler creates a base Reconciler for unit testing. It seeds a real
// arubamt.Multitenant cache with the given mockArubaClient under the "test-tenant" key,
// so that controllers calling r.ArubaClient("test-tenant") receive the mock without
// requiring real credentials or a ctrl.Manager.
func newTestReconciler(_ GinkgoTInterface, mockArubaClient aruba.Client) *reconciler.Reconciler {
	mt := arubamt.New()
	mt.Add("test-tenant", mockArubaClient)
	return reconciler.NewReconcilerForTest(k8sClient, k8sClient.Scheme(), mt)
}

func strPtr(s string) *string { return &s }

var _ = Describe("AssesCSPResourceStateNature", func() {
	It("returns Undetermined for nil status", func() {
		Expect(AssesCSPResourceStateNature(nil)).To(Equal(CSPResourceStateNatureUndetermined))
	})

	It("returns Undetermined when State is nil", func() {
		s := &arubatypes.ResourceStatus{}
		Expect(AssesCSPResourceStateNature(s)).To(Equal(CSPResourceStateNatureUndetermined))
	})

	DescribeTable("transitory states",
		func(state string) {
			s := &arubatypes.ResourceStatus{State: strPtr(state)}
			Expect(AssesCSPResourceStateNature(s)).To(Equal(CSPResourceStateNatureTransitory))
		},
		Entry("InCreation", CSPResourceStateInCreation),
		Entry("Creating", CSPResourceStateCreating),
		Entry("Updating", CSPResourceStateUpdating),
		Entry("Deleting", CSPResourceStateDeleting),
		Entry("Provisioning", CSPResourceStateProvisioning),
		Entry("Disabling", CSPResourceStateDisabling),
		Entry("Enabling", CSPResourceStateEnabling),
	)

	DescribeTable("final states",
		func(state string) {
			s := &arubatypes.ResourceStatus{State: strPtr(state)}
			Expect(AssesCSPResourceStateNature(s)).To(Equal(CSPResourceStateNatureFinal))
		},
		Entry("Active", CSPResourceStateActive),
		Entry("NotUsed", CSPResourceStateNotUsed),
		Entry("InUse", CSPResourceStateInUse),
		Entry("Used", CSPResourceStateUsed),
		Entry("Stopped", CSPResourceStateStopped),
		Entry("Running", CSPResourceStateRunning),
		Entry("Disabled", CSPResourceStateDisabled),
		Entry("Failed", CSPResourceStateFailed),
	)

	It("returns Invalid for unknown state", func() {
		s := &arubatypes.ResourceStatus{State: strPtr("UnknownState")}
		Expect(AssesCSPResourceStateNature(s)).To(Equal(CSPResourceStateNatureInvalid))
	})
})

var _ = Describe("cmpErrorDetails", func() {
	It("returns all 'na' for nil error", func() {
		result := cmpErrorDetails(nil)
		Expect(result).To(Equal("title=na,detail=na,instance=na"))
	})

	It("returns formatted string when all fields are set", func() {
		title, detail, instance := "Bad Request", "resource not found", "urn:uuid:123"
		err := &arubatypes.ErrorResponse{
			Title:    &title,
			Detail:   &detail,
			Instance: &instance,
		}
		result := cmpErrorDetails(err)
		Expect(result).To(Equal("title=Bad Request,detail=resource not found,instance=urn:uuid:123"))
	})

	It("returns 'na' for missing fields", func() {
		title := "Some Error"
		err := &arubatypes.ErrorResponse{Title: &title}
		result := cmpErrorDetails(err)
		Expect(result).To(Equal("title=Some Error,detail=na,instance=na"))
	})

	It("returns all 'na' for empty ErrorResponse", func() {
		err := &arubatypes.ErrorResponse{}
		result := cmpErrorDetails(err)
		Expect(result).To(Equal("title=na,detail=na,instance=na"))
	})
})

var _ = Describe("tagsAreEqual", func() {
	It("returns true for same order", func() {
		Expect(tagsAreEqual([]string{"a", "b", "c"}, []string{"a", "b", "c"})).To(BeTrue())
	})

	It("returns true for different order", func() {
		Expect(tagsAreEqual([]string{"c", "a", "b"}, []string{"a", "b", "c"})).To(BeTrue())
	})

	It("returns false for different tags", func() {
		Expect(tagsAreEqual([]string{"a", "b"}, []string{"a", "c"})).To(BeFalse())
	})

	It("returns true for both empty", func() {
		Expect(tagsAreEqual([]string{}, []string{})).To(BeTrue())
	})

	It("returns true for both nil", func() {
		Expect(tagsAreEqual(nil, nil)).To(BeTrue())
	})

	It("returns false for different lengths", func() {
		Expect(tagsAreEqual([]string{"a", "b"}, []string{"a"})).To(BeFalse())
	})
})
