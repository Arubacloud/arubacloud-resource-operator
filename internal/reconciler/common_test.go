package reconciler

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	arubatypes "github.com/Arubacloud/sdk-go/pkg/types"
)

var _ = Describe("AssessCSPResourceStateNature", func() {
	It("returns Undetermined for nil status", func() {
		Expect(AssessCSPResourceStateNature(nil)).To(Equal(CSPResourceStateNatureUndetermined))
	})

	It("returns Undetermined when State is nil", func() {
		s := &arubatypes.ResourceStatusResponse{}
		Expect(AssessCSPResourceStateNature(s)).To(Equal(CSPResourceStateNatureUndetermined))
	})

	DescribeTable("transitory states",
		func(state arubatypes.State) {
			s := &arubatypes.ResourceStatusResponse{State: &state}
			Expect(AssessCSPResourceStateNature(s)).To(Equal(CSPResourceStateNatureTransitory))
		},
		Entry("InCreation", arubatypes.StateInCreation),
		Entry("Creating", arubatypes.StateCreating),
		Entry("Updating", arubatypes.StateUpdating),
		Entry("Deleting", arubatypes.StateDeleting),
		Entry("Provisioning", arubatypes.StateProvisioning),
		Entry("Disabling", arubatypes.StateDisabling),
		Entry("Enabling", arubatypes.StateEnabling),
	)

	DescribeTable("final states",
		func(state arubatypes.State) {
			s := &arubatypes.ResourceStatusResponse{State: &state}
			Expect(AssessCSPResourceStateNature(s)).To(Equal(CSPResourceStateNatureFinal))
		},
		Entry("Active", arubatypes.StateActive),
		Entry("NotUsed", arubatypes.StateNotUsed),
		Entry("InUse", arubatypes.StateInUse),
		Entry("Used", arubatypes.StateUsed),
		Entry("Stopped", arubatypes.StateStopped),
		Entry("Running", arubatypes.StateRunning),
		Entry("Disabled", arubatypes.StateDisabled),
		Entry("Failed", arubatypes.StateFailed),
	)

	It("returns Invalid for unknown state", func() {
		unknown := arubatypes.State("UnknownState")
		s := &arubatypes.ResourceStatusResponse{State: &unknown}
		Expect(AssessCSPResourceStateNature(s)).To(Equal(CSPResourceStateNatureInvalid))
	})
})
