package reconciler

import (
	"errors"

	"github.com/Arubacloud/sdk-go/pkg/aruba"
)

var (
	ErrNotAllowedChanges = errors.New("not allowed change")
)

// IsFinalState reports whether a CMP resource lifecycle state is settled — i.e.
// non-empty and not transitory. Settled covers both healthy steady states
// (Active, Running, NotUsed, InUse, …) and failure states (Failed, Error,
// Disabled). It is the classification the operator uses to decide the CMP
// resource is stable enough to act on (dispatch a delete or an update).
//
// It replaces the former AssessCSPResourceStateNature "Final" nature, delegating
// the transitory/settled distinction to the SDK's aruba.State.IsTransitory().
func IsFinalState(s aruba.State) bool {
	return s != "" && !s.IsTransitory()
}

// IsTransitoryState reports whether a CMP resource lifecycle state indicates an
// operation in progress (InCreation, Creating, Updating, Deleting, …). Thin
// wrapper over aruba.State.IsTransitory() for symmetry with IsFinalState.
func IsTransitoryState(s aruba.State) bool {
	return s.IsTransitory()
}
