package controller

import (
	"errors"

	arubatypes "github.com/Arubacloud/sdk-go/pkg/types"
)

var (
	ErrNotAllowedChange = errors.New("not allowed change")
)

const (
	CSPResourceStateInCreation   string = "InCreation"
	CSPResourceStateCreating     string = "Creating"
	CSPResourceStateUpdating     string = "Updating"
	CSPResourceStateDeleting     string = "Deleting"
	CSPResourceStatePending      string = "Pending"
	CSPResourceStateProvisioning string = "Provisioning"
	CSPResourceStateActive       string = "Active"
	CSPResourceStateNotUsed      string = "NotUsed"
	CSPResourceStateInUse        string = "InUse"
	CSPResourceStateUsed         string = "Used"
	CSPResourceStateStopped      string = "Stopped"
	CSPResourceStateRunning      string = "Running"
)

type CSPResourceStateNature int

const (
	CSPResourceStateNatureInvalid CSPResourceStateNature = iota
	CSPResourceStateNatureTransitory
	CSPResourceStateNatureFinal
	CSPResourceStateNatureUndetermined
)

func AssesCSPResourceStateNature(status *arubatypes.ResourceStatus) CSPResourceStateNature {
	if status == nil {
		return CSPResourceStateNatureUndetermined
	}

	if status.State == nil {
		return CSPResourceStateNatureUndetermined
	}

	switch *status.State {
	case CSPResourceStateInCreation,
		CSPResourceStateCreating,
		CSPResourceStateUpdating,
		CSPResourceStateDeleting,
		CSPResourceStatePending,
		CSPResourceStateProvisioning:
		return CSPResourceStateNatureTransitory

	case CSPResourceStateActive,
		CSPResourceStateNotUsed,
		CSPResourceStateInUse,
		CSPResourceStateUsed,
		CSPResourceStateStopped,
		CSPResourceStateRunning:
		return CSPResourceStateNatureFinal
	}

	return CSPResourceStateNatureInvalid
}
