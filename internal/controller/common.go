package controller

import (
	"errors"

	arubatypes "github.com/Arubacloud/sdk-go/pkg/types"
)

var (
	ErrNotAllowedChanges = errors.New("not allowed change")
)

const (
	CSPResourceStateInCreation   string = "InCreation"
	CSPResourceStateCreating     string = "Creating"
	CSPResourceStateUpdating     string = "Updating"
	CSPResourceStateDeleting     string = "Deleting"
	CSPResourceStateProvisioning string = "Provisioning"
	CSPResourceStateActive       string = "Active"
	CSPResourceStateNotUsed      string = "NotUsed"
	CSPResourceStateInUse        string = "InUse"
	CSPResourceStateUsed         string = "Used"
	CSPResourceStateStopped      string = "Stopped"
	CSPResourceStateRunning      string = "Running"

	CSPResourceStateDisabling string = "Disabling" //after recharge credits is not succesfully applied, the resource is in "Disabling" state, and then it will be "Disabled"
	CSPResourceStateEnabling  string = "Enabling"  // after recharge credits is not succesfully applied, the resource is in "Enabling" state, and then it will be "Active"

	CSPResourceStateDisabled string = "Disabled" // final state after "Disabling"; customer cannot use the resource until is enabled agaim
	CSPResourceStateFailed   string = "Failed"   // final state when some error occurs during creation or update of the resource; customer cannot use the resource and need to delete it
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
		CSPResourceStateProvisioning,
		CSPResourceStateDisabling,
		CSPResourceStateEnabling:
		return CSPResourceStateNatureTransitory

	case CSPResourceStateActive,
		CSPResourceStateNotUsed,
		CSPResourceStateInUse,
		CSPResourceStateUsed,
		CSPResourceStateStopped,
		CSPResourceStateRunning,
		CSPResourceStateDisabled,
		CSPResourceStateFailed:
		return CSPResourceStateNatureFinal
	}

	return CSPResourceStateNatureInvalid
}
