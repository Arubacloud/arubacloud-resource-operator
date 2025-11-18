package util

import (
	"context"
	"fmt"
	"slices"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"gitlab.aruba.it/ingegneria/seca/operators/aruba-operator/api/v1alpha1"
	arubaClient "gitlab.aruba.it/ingegneria/seca/operators/aruba-operator/internal/client"
)

const (
	requeueAfter = 20 * time.Second
	// maxPhaseTimeout defines the maximum time a resource can remain in a non-final phase
	maxPhaseTimeout = 5 * time.Minute
)

// PhaseManager provides common phase management functionality
type PhaseManager struct {
	Client client.Client
	Object client.Object
	Status *v1alpha1.ArubaResourceStatus
}

// HandlePhaseTimeout transitions the resource to failed state due to timeout
func (pm *PhaseManager) HandlePhaseTimeout(ctx context.Context) (bool, ctrl.Result, error) {
	isTimeout := false

	if pm.Status.PhaseStartTime == nil {
		return isTimeout, ctrl.Result{}, nil
	}

	transitioningPhases := []v1alpha1.ArubaResourcePhase{
		v1alpha1.ArubaResourcePhaseCreating,
		v1alpha1.ArubaResourcePhaseProvisioning,
		v1alpha1.ArubaResourcePhaseUpdating,
		v1alpha1.ArubaResourcePhaseDeleting,
	}

	if !slices.Contains(transitioningPhases, pm.Status.Phase) {
		return isTimeout, ctrl.Result{}, nil
	}

	elapsed := time.Since(pm.Status.PhaseStartTime.Time)
	isTimeout = elapsed > maxPhaseTimeout

	if !isTimeout {
		return isTimeout, ctrl.Result{}, nil
	}

	phaseLogger := ctrl.Log.WithValues("Phase", pm.Status.Phase, "Kind", pm.Object.GetObjectKind().GroupVersionKind().Kind, "Name", pm.Object.GetName())
	message := fmt.Sprintf("Reconciliation took too much time (timeout: %+v)", maxPhaseTimeout)
	phaseLogger.Info(message)

	nextCtrlResult, err := pm.Next(
		ctx,
		v1alpha1.ArubaResourcePhaseFailed,
		metav1.ConditionFalse,
		"ReconciliationTimeout",
		message,
		false,
	)

	return isTimeout, nextCtrlResult, err
}

func (pm *PhaseManager) HandleToDelete(ctx context.Context) (bool, ctrl.Result, error) {
	shouldBeDeleted := pm.Status.Phase != v1alpha1.ArubaResourcePhaseDeleting &&
		pm.Status.Phase != v1alpha1.ArubaResourcePhaseFailed &&
		!pm.Object.GetDeletionTimestamp().IsZero()

	if !shouldBeDeleted {
		return shouldBeDeleted, ctrl.Result{}, nil
	}

	nextCtrlResult, err := pm.Next(
		ctx,
		v1alpha1.ArubaResourcePhaseDeleting,
		metav1.ConditionFalse,
		"ToBeDeleted",
		"deletion timestamp detected",
		true,
	)
	return shouldBeDeleted, nextCtrlResult, err
}

// Next transitions to the next phase with message and condition updates
func (pm *PhaseManager) Next(
	ctx context.Context,
	nextPhase v1alpha1.ArubaResourcePhase,
	status metav1.ConditionStatus,
	reason, message string,
	requeue bool,
) (ctrl.Result, error) {
	phase := pm.Status.Phase
	if phase == "" {
		phase = "Initializing"
	}

	phaseLogger := ctrl.Log.WithValues("Phase", phase, "NextPhase", nextPhase, "Kind", pm.Object.GetObjectKind().GroupVersionKind().Kind, "Name", pm.Object.GetName())
	// Debouncing logic: if this is a retry (requeue=true) with the same phase, check timing
	if requeue && phase == nextPhase && pm.Status.PhaseStartTime != nil {
		timeSincePhaseStart := time.Since(pm.Status.PhaseStartTime.Time)

		// For debouncing, we need to track time since the last attempt, not phase start
		// Since PhaseStartTime is only updated on phase changes, we can use it as a baseline
		// and add our own debouncing logic based on the frequency of calls

		intervalsElapsed := int(timeSincePhaseStart / requeueAfter)
		nextInterval := time.Duration(intervalsElapsed+1) * requeueAfter
		timeToNextInterval := nextInterval - timeSincePhaseStart

		// If we haven't reached the next interval yet, wait
		phaseLogger.Info("Reconcile Debounce",
			"reason", reason,
			"message", message,
			"timeSincePhaseStart", timeSincePhaseStart,
			"timeToNextInterval", timeToNextInterval,
			"intervalsElapsed", intervalsElapsed,
			"requeueAfter", requeueAfter)
		if timeToNextInterval > 0 && timeToNextInterval < requeueAfter {
			return ctrl.Result{RequeueAfter: timeToNextInterval}, nil
		}
	}

	// Update phase start time ONLY if phase is changing or not set
	if pm.Status.PhaseStartTime == nil || phase != nextPhase {
		now := metav1.Now()
		pm.Status.PhaseStartTime = &now
	}
	pm.Status.Phase = nextPhase
	pm.Status.Message = message
	pm.Status.ObservedGeneration = pm.Object.GetGeneration()
	pm.Status.Conditions = UpdateConditions(pm.Status.Conditions, v1alpha1.ConditionTypeSynchronized, status, reason, message)

	if err := pm.Client.Status().Update(ctx, pm.Object); err != nil {
		phaseLogger.Error(err, "failed to update status")
		return ctrl.Result{}, err
	}

	phaseLogger.Info(message)
	return ctrl.Result{Requeue: requeue, RequeueAfter: requeueAfter}, nil
}

// NextToFailedOnApiError handles API errors with proper 4xx/5xx logic and condition management
func (pm *PhaseManager) NextToFailedOnApiError(ctx context.Context, err error) (ctrl.Result, error) {
	if apiErr, ok := err.(*arubaClient.ApiError); ok {
		statusCode := apiErr.Status
		message := apiErr.Error()

		// Handle notReady/invalidStatus errors during transitioning phases - should retry
		if apiErr.IsInvalidStatus() {
			return pm.Next(
				ctx,
				pm.Status.Phase,
				metav1.ConditionFalse,
				"ResourceNotReady",
				fmt.Sprintf("Remote resource is not ready, will retry: %s", message),
				true,
			)
		}

		// Handle other 4xx errors (client errors) - fail immediately
		if statusCode >= 400 && statusCode < 500 {
			return pm.Next(
				ctx,
				v1alpha1.ArubaResourcePhaseFailed,
				metav1.ConditionFalse,
				"ClientError",
				fmt.Sprintf("Client error encountered, transitioning to failed state: %s", message),
				false,
			)
		}

		// Handle 5xx and other errors (server errors) - retry later
		return pm.Next(
			ctx,
			pm.Status.Phase,
			metav1.ConditionFalse,
			"ServerError",
			fmt.Sprintf("Server error encountered, will retry: %s", message),
			true,
		)
	}

	// Non-API errors - retry later
	return pm.Next(
		ctx,
		pm.Status.Phase,
		metav1.ConditionFalse,
		"ReconcileError",
		fmt.Sprintf("Reconcile error encountered, will retry: %s", err.Error()),
		true,
	)
}

// InitializeResource handles the initialization phase with finalizer management
func (pm *PhaseManager) InitializeResource(ctx context.Context, finalizerName string) (ctrl.Result, error) {
	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(pm.Object, finalizerName) {
		controllerutil.AddFinalizer(pm.Object, finalizerName)
		err := pm.Client.Update(ctx, pm.Object)
		if err != nil {
			return pm.NextToFailedOnApiError(ctx, err)
		}
	}

	return pm.Next(ctx, v1alpha1.ArubaResourcePhaseCreating, metav1.ConditionFalse, "Initialized", "Resource initialized successfully", true)

}

// HandleDeletion handles the deletion phase with finalizer removal
func (pm *PhaseManager) HandleDeletion(ctx context.Context, finalizerName string, deleteFunc func(context.Context) error) (ctrl.Result, error) {
	err := deleteFunc(ctx)
	if err != nil {
		return pm.NextToFailedOnApiError(ctx, err)
	}

	// Remove finalizer to allow Kubernetes to delete the resource
	if controllerutil.ContainsFinalizer(pm.Object, finalizerName) {
		controllerutil.RemoveFinalizer(pm.Object, finalizerName)
		err := pm.Client.Update(ctx, pm.Object)
		if err != nil {
			return pm.NextToFailedOnApiError(ctx, err)
		}
	}

	return ctrl.Result{}, nil
}

// HandleCreating handles the resource creation phase
func (pm *PhaseManager) HandleCreating(ctx context.Context, createFunc func(context.Context) (string, string, error)) (ctrl.Result, error) {
	resourceID, state, err := createFunc(ctx)
	if err != nil {
		return pm.NextToFailedOnApiError(ctx, err)
	}

	// Update status with resource ID
	pm.Status.ResourceID = resourceID

	if state == "InCreation" || state == "Provisioning" {
		return pm.Next(
			ctx,
			v1alpha1.ArubaResourcePhaseProvisioning,
			metav1.ConditionFalse,
			"Provisioning",
			"Resource is being provisioned",
			true,
		)
	}

	return pm.Next(
		ctx,
		v1alpha1.ArubaResourcePhaseCreated,
		metav1.ConditionTrue,
		"Created",
		"Resource created successfully",
		true,
	)
}

// HandleUpdating handles the resource update phase
func (pm *PhaseManager) HandleUpdating(ctx context.Context, updateFunc func(context.Context) error) (ctrl.Result, error) {
	err := updateFunc(ctx)
	if err != nil {
		return pm.NextToFailedOnApiError(ctx, err)
	}

	return pm.Next(
		ctx,
		v1alpha1.ArubaResourcePhaseCreated,
		metav1.ConditionTrue,
		"Updated",
		"Resource updated successfully",
		true,
	)

}

// HandleProvisioning handles the provisioning state check with configurable state transitions
func (pm *PhaseManager) HandleProvisioning(ctx context.Context, getStatusFunc func(context.Context) (string, error)) (ctrl.Result, error) {
	state, err := getStatusFunc(ctx)
	if err != nil {
		return pm.NextToFailedOnApiError(ctx, err)
	}

	message := ""
	switch state {
	case "Available", "Active", "NotUsed", "Used":
		return pm.Next(
			ctx,
			v1alpha1.ArubaResourcePhaseCreated,
			metav1.ConditionTrue,
			"Created",
			"Resource created successfully",
			true,
		)
	case "Failed", "Error":
		return pm.Next(
			ctx,
			v1alpha1.ArubaResourcePhaseFailed,
			metav1.ConditionTrue,
			"ProvisioningFailed",
			message,
			false,
		)
	default:
		return pm.Next(
			ctx,
			v1alpha1.ArubaResourcePhaseProvisioning,
			metav1.ConditionTrue,
			"Provisioning",
			message,
			true,
		)
	}

}

// CheckForUpdates checks if resource needs update based on generation
func (pm *PhaseManager) CheckForUpdates(ctx context.Context) (ctrl.Result, error) {
	phaseLogger := ctrl.Log.WithValues("Phase", pm.Status.Phase, "Kind", pm.Object.GetObjectKind().GroupVersionKind().Kind, "Name", pm.Object.GetName())

	// Check if resource needs update
	if pm.Status.ObservedGeneration != pm.Object.GetGeneration() {
		phaseLogger.Info("resource needs update - generation mismatch detected",
			"generation", pm.Object.GetGeneration(),
			"observedGeneration", pm.Status.ObservedGeneration)

		return pm.Next(
			ctx,
			v1alpha1.ArubaResourcePhaseUpdating,
			metav1.ConditionFalse,
			"Updating",
			"Resource update initiated",
			true,
		)
	}

	phaseLogger.Info("resource is up to date")
	return ctrl.Result{}, nil
}
