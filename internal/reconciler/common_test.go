package reconciler

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Arubacloud/sdk-go/pkg/aruba"
)

var _ = Describe("CMP resource state classification", func() {
	DescribeTable("IsTransitoryState",
		func(state aruba.State, expected bool) {
			Expect(IsTransitoryState(state)).To(Equal(expected))
		},
		Entry("InCreation", aruba.StateInCreation, true),
		Entry("Creating", aruba.StateCreating, true),
		Entry("Updating", aruba.StateUpdating, true),
		Entry("Deleting", aruba.StateDeleting, true),
		Entry("Provisioning", aruba.StateProvisioning, true),
		Entry("Disabling", aruba.StateDisabling, true),
		Entry("Enabling", aruba.StateEnabling, true),
		Entry("Active (settled)", aruba.StateActive, false),
		Entry("Failed (settled)", aruba.StateFailed, false),
		Entry("empty", aruba.State(""), false),
	)

	DescribeTable("IsFinalState (settled or failure; non-empty, non-transitory)",
		func(state aruba.State, expected bool) {
			Expect(IsFinalState(state)).To(Equal(expected))
		},
		Entry("Active", aruba.StateActive, true),
		Entry("NotUsed", aruba.StateNotUsed, true),
		Entry("InUse", aruba.StateInUse, true),
		Entry("Used", aruba.StateUsed, true),
		Entry("Stopped", aruba.StateStopped, true),
		Entry("Running", aruba.StateRunning, true),
		Entry("Failed", aruba.StateFailed, true),
		Entry("Disabled", aruba.StateDisabled, true),
		Entry("Creating (transitory)", aruba.StateCreating, false),
		Entry("Deleting (transitory)", aruba.StateDeleting, false),
		Entry("empty", aruba.State(""), false),
	)
})
