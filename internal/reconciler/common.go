package reconciler

import (
	"errors"

	arubatypes "github.com/Arubacloud/sdk-go/pkg/types"
)

var (
	ErrNotAllowedChanges = errors.New("not allowed change")
)

// CSPResourceStateNature classifies a CMP (Aruba-side) resource state as
// transitory (an operation is in progress) or final (settled, including
// failure). The transition engine uses it to decide when it is safe to act on
// a resource. Compare raw states directly against the SDK's arubatypes.State*
// constants.
type CSPResourceStateNature int

const (
	CSPResourceStateNatureInvalid CSPResourceStateNature = iota
	CSPResourceStateNatureTransitory
	CSPResourceStateNatureFinal
	CSPResourceStateNatureUndetermined
)

func AssessCSPResourceStateNature(status *arubatypes.ResourceStatusResponse) CSPResourceStateNature {
	if status == nil || status.State == nil {
		return CSPResourceStateNatureUndetermined
	}

	// IsTransitory() covers exactly the transitory set (InCreation, Creating,
	// Updating, Deleting, Provisioning, Disabling, Enabling).
	if status.State.IsTransitory() {
		return CSPResourceStateNatureTransitory
	}

	switch *status.State {
	case arubatypes.StateActive,
		arubatypes.StateNotUsed,
		arubatypes.StateInUse,
		arubatypes.StateUsed,
		arubatypes.StateStopped,
		arubatypes.StateRunning,
		arubatypes.StateDisabled,
		arubatypes.StateFailed:
		return CSPResourceStateNatureFinal
	case arubatypes.StateInCreation, arubatypes.StateCreating, arubatypes.StateUpdating, arubatypes.StateProvisioning, arubatypes.StateDeleting, arubatypes.StateDisabling, arubatypes.StateEnabling, arubatypes.StateReserved, arubatypes.StateDeleted, arubatypes.StateError:
		return CSPResourceStateNatureInvalid
	default:
		return CSPResourceStateNatureInvalid
	}
}
