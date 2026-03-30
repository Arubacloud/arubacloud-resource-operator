package reconciler

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	arubatypes "github.com/Arubacloud/sdk-go/pkg/types"
)

func strPtr(s string) *string { return &s }

var _ = Describe("AssessCSPResourceStateNature", func() {
	It("returns Undetermined for nil status", func() {
		Expect(AssessCSPResourceStateNature(nil)).To(Equal(CSPResourceStateNatureUndetermined))
	})

	It("returns Undetermined when State is nil", func() {
		s := &arubatypes.ResourceStatus{}
		Expect(AssessCSPResourceStateNature(s)).To(Equal(CSPResourceStateNatureUndetermined))
	})

	DescribeTable("transitory states",
		func(state string) {
			s := &arubatypes.ResourceStatus{State: strPtr(state)}
			Expect(AssessCSPResourceStateNature(s)).To(Equal(CSPResourceStateNatureTransitory))
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
			Expect(AssessCSPResourceStateNature(s)).To(Equal(CSPResourceStateNatureFinal))
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
		Expect(AssessCSPResourceStateNature(s)).To(Equal(CSPResourceStateNatureInvalid))
	})
})
