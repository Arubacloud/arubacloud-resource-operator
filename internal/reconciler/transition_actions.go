package reconciler

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
)

// DeepCopyableObject extends ResourceObject with a typed DeepCopy method.
type DeepCopyableObject[K any] interface {
	ResourceObject
	DeepCopy() K
}

// KubeSetErrorMessageOnCMPError returns an ActionOnErrorFunc that handles CMP errors
// on the Kubernetes resource status. Behavior depends on the error category:
//   - Semantic (4xx with validation errors) during Creating or Updating: moves the resource
//     to Failed+ValidationFailed so the user gets immediate feedback. The existing
//     IsValidationFailed recovery block in HandleReconcile resets the phase once the user
//     fixes the spec.
//   - Transient (4xx without validation errors) or Technical (5xx/network): surfaces the
//     error message in the condition without changing the resource's phase or reason.
//     Only timeouts (PhaseTimedOut) may eventually move these to Failed.
func KubeSetErrorMessageOnCMPError[K DeepCopyableObject[K], A any](c client.Client) ActionOnErrorFunc[K, A] {
	return func(ctx context.Context, k K, _ A, err error) error {
		logger := log.FromContext(ctx)
		rs := k.GetResourceStatus()
		phase := rs.Phase
		currentReason := v1alpha1.ConditionReasonShallSynchronize // safe fallback
		for _, cond := range rs.Conditions {
			if cond.Status == metav1.ConditionTrue {
				currentReason = cond.Reason
				break
			}
		}
		var cmpErr *CMPError
		if errors.As(err, &cmpErr) {
			logger.Error(err, "CMP error surfaced on resource condition",
				"resource", fmt.Sprintf("%s/%s", k.GetNamespace(), k.GetName()),
				"phase", string(rs.Phase),
				"cmpErrorCategory", cmpErr.Category,
				"cmpStatusCode", cmpErr.StatusCode,
			)
			if cmpErr.Category == CMPErrorCategorySemantic &&
				(rs.Phase == v1alpha1.ResourcePhaseCreating || rs.Phase == v1alpha1.ResourcePhaseUpdating) {
				phase = v1alpha1.ResourcePhaseFailed
				currentReason = v1alpha1.ConditionReasonValidationFailed
			}
		} else {
			logger.Error(err, "CMP error surfaced on resource condition",
				"resource", fmt.Sprintf("%s/%s", k.GetNamespace(), k.GetName()),
				"phase", string(rs.Phase),
			)
		}
		return SetPhaseAndCondition(c, ctx, k, phase, currentReason, err)
	}
}

// SetPhaseAndCondition performs a retry-on-conflict status patch that sets the
// given phase and condition reason. actionErr carries the AAction outcome:
// nil means the action succeeded (" - OK"), non-nil appends the error message.
// Optional prePatch callbacks are applied to the patched copy before the status write.
func SetPhaseAndCondition[K DeepCopyableObject[K]](
	c client.Client, ctx context.Context, obj K,
	phase v1alpha1.ResourcePhase, reason string,
	actionErr error,
	prePatch ...func(K),
) error {
	logger := log.FromContext(ctx)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		objCopy := obj.DeepCopy()
		if err := c.Get(ctx, client.ObjectKeyFromObject(obj), objCopy); err != nil {
			return err
		}

		objPatch := objCopy.DeepCopy()
		for _, fn := range prePatch {
			fn(objPatch)
		}

		rs := objPatch.GetResourceStatus()
		fromPhase := rs.Phase
		rs.Phase = phase
		logger.Info("phase transition",
			"resource", fmt.Sprintf("%s/%s", objPatch.GetNamespace(), objPatch.GetName()),
			"fromPhase", string(fromPhase),
			"toPhase", string(phase),
			"reason", reason,
		)

		for i := range rs.Conditions {
			rs.Conditions[i].Status = metav1.ConditionFalse
		}

		msgSuffix := " - OK"
		if actionErr != nil {
			var cmpErr *CMPError
			if errors.As(actionErr, &cmpErr) {
				msgSuffix = fmt.Sprintf(" - ERROR [%s]: %s (status: %d, detail: %s)",
					cmpErr.Category, cmpErr.Title, cmpErr.StatusCode, cmpErr.Detail)
			} else {
				msgSuffix = fmt.Sprintf(" - ERROR: %s", actionErr.Error())
			}
		}

		meta.SetStatusCondition(
			&rs.Conditions,
			metav1.Condition{
				Type:               string(phase),
				Status:             metav1.ConditionTrue,
				Reason:             reason,
				Message:            fmt.Sprintf("%s %s%s", string(phase), reason, msgSuffix),
				LastTransitionTime: metav1.Now(),
				ObservedGeneration: objPatch.GetGeneration(),
			},
		)

		if err := c.Status().Patch(ctx, objPatch, client.MergeFrom(objCopy)); err != nil {
			return fmt.Errorf(
				"failed to update resource '%s/%s' state to '%v': %w",
				objPatch.GetNamespace(), objPatch.GetName(), phase, err,
			)
		}

		return nil
	})
}

// SetActiveAndSetID performs a retry-on-conflict status patch that sets the
// resource to Active phase, stamps ObservedGeneration, and optionally sets the
// ResourceID from the CMP response. actionErr carries the AAction outcome
// for the condition message. Optional prePatch callbacks are applied to the
// patched copy before the status write.
func SetActiveAndSetID[K DeepCopyableObject[K]](
	c client.Client, ctx context.Context, obj K,
	cmpResourceID string,
	actionErr error,
	prePatch ...func(K),
) error {
	logger := log.FromContext(ctx)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		objCopy := obj.DeepCopy()
		if err := c.Get(ctx, client.ObjectKeyFromObject(obj), objCopy); err != nil {
			return err
		}

		objPatch := objCopy.DeepCopy()
		for _, fn := range prePatch {
			fn(objPatch)
		}

		rs := objPatch.GetResourceStatus()
		rs.Phase = v1alpha1.ResourcePhaseActive
		if rs.ResourceID == "" && cmpResourceID != "" {
			rs.ResourceID = cmpResourceID
		}
		logger.Info("resource is active",
			"resource", fmt.Sprintf("%s/%s", objPatch.GetNamespace(), objPatch.GetName()),
			"resourceID", rs.ResourceID,
		)
		rs.ObservedGeneration = objPatch.GetGeneration()

		for i := range rs.Conditions {
			rs.Conditions[i].Status = metav1.ConditionFalse
		}

		msgSuffix := " - OK"
		if actionErr != nil {
			msgSuffix = fmt.Sprintf(" - ERROR: %s", actionErr.Error())
		}
		meta.SetStatusCondition(
			&rs.Conditions,
			metav1.Condition{
				Type:               string(v1alpha1.ResourcePhaseActive),
				Status:             metav1.ConditionTrue,
				Reason:             v1alpha1.ConditionReasonSynchronized,
				Message:            fmt.Sprintf("%s %s%s", string(v1alpha1.ResourcePhaseActive), v1alpha1.ConditionReasonSynchronized, msgSuffix),
				LastTransitionTime: metav1.Now(),
				ObservedGeneration: objPatch.GetGeneration(),
			},
		)

		if err := c.Status().Patch(ctx, objPatch, client.MergeFrom(objCopy)); err != nil {
			return fmt.Errorf(
				"failed to update resource '%s/%s' state to '%v': %w",
				objPatch.GetNamespace(), objPatch.GetName(), v1alpha1.ResourcePhaseActive, err,
			)
		}

		return nil
	})
}

// SetFailedOnTimeout performs a retry-on-conflict status patch that moves a
// resource stuck in a transitory phase to Failed, recording the timeout reason
// on both the previous phase's condition and the new Failed condition.
func SetFailedOnTimeout[K DeepCopyableObject[K]](
	c client.Client, ctx context.Context, obj K,
	prePatch ...func(K),
) error {
	logger := log.FromContext(ctx)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		objCopy := obj.DeepCopy()
		if err := c.Get(ctx, client.ObjectKeyFromObject(obj), objCopy); err != nil {
			return err
		}

		objPatch := objCopy.DeepCopy()
		for _, fn := range prePatch {
			fn(objPatch)
		}

		rs := objPatch.GetResourceStatus()
		previousPhase := rs.Phase
		timeoutMsg := fmt.Sprintf("phase timeout exceeded (%s)", MaxPhaseTimeout)
		logger.Info("resource timed out in transitory phase",
			"resource", fmt.Sprintf("%s/%s", objPatch.GetNamespace(), objPatch.GetName()),
			"phase", string(previousPhase),
			"timeout", MaxPhaseTimeout.String(),
		)

		// Set ALL existing conditions to ConditionFalse
		for i := range rs.Conditions {
			rs.Conditions[i].Status = metav1.ConditionFalse
		}

		// Update the timed-out phase condition: Status=False, Reason=Failed
		meta.SetStatusCondition(&rs.Conditions, metav1.Condition{
			Type:               string(previousPhase),
			Status:             metav1.ConditionFalse,
			Reason:             v1alpha1.ConditionReasonFailed,
			Message:            fmt.Sprintf("%s %s - %s", string(previousPhase), v1alpha1.ConditionReasonFailed, timeoutMsg),
			ObservedGeneration: objPatch.GetGeneration(),
		})

		// Set phase to Failed
		rs.Phase = v1alpha1.ResourcePhaseFailed

		// Add Failed condition: Status=True, Reason=Failed, same message as the timed-out phase
		meta.SetStatusCondition(&rs.Conditions, metav1.Condition{
			Type:               string(v1alpha1.ResourcePhaseFailed),
			Status:             metav1.ConditionTrue,
			Reason:             v1alpha1.ConditionReasonFailed,
			Message:            fmt.Sprintf("%s %s - %s", string(previousPhase), v1alpha1.ConditionReasonFailed, timeoutMsg),
			ObservedGeneration: objPatch.GetGeneration(),
		})

		return c.Status().Patch(ctx, objPatch, client.MergeFrom(objCopy))
	})
}

// KubeDeleteFromPending returns a KAction that transitions a Pending resource directly to
// Deleted, skipping the entire Deleting flow. This is safe because Pending resources have
// no CMP-side representation yet.
func KubeDeleteFromPending[K DeepCopyableObject[K], A any](c client.Client) ActionFunc[K, A] {
	return func(ctx context.Context, k K, _ A) error {
		return SetPhaseAndCondition(c, ctx, k,
			v1alpha1.ResourcePhaseDeleted, v1alpha1.ConditionReasonSynchronized, nil,
		)
	}
}

// KubeResetValidationFailedForDeletion resets a resource stuck in any *ValidationFailed
// state to a deletable phase (Pending or Active + Synchronized), unblocking normal
// deletion flow on the next reconcile.
func KubeResetValidationFailedForDeletion[K DeepCopyableObject[K], A any](c client.Client) ActionFunc[K, A] {
	return func(ctx context.Context, k K, _ A) error {
		resetPhase := v1alpha1.ResourcePhasePending
		if k.GetResourceStatus().ResourceID != "" {
			resetPhase = v1alpha1.ResourcePhaseActive
		}
		return SetPhaseAndCondition(c, ctx, k,
			resetPhase, v1alpha1.ConditionReasonSynchronized, nil,
		)
	}
}

// TagsAreEqual returns true when both tag slices contain the same elements
// regardless of order.
func TagsAreEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	aCopy := make([]string, len(a))
	copy(aCopy, a)
	bCopy := make([]string, len(b))
	copy(bCopy, b)
	slices.Sort(aCopy)
	slices.Sort(bCopy)
	for i, tag := range aCopy {
		if tag != bCopy[i] {
			return false
		}
	}
	return true
}
