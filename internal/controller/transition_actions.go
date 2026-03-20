package controller

import (
	"context"
	"fmt"
	"slices"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
)

// DeepCopyableObject extends ResourceObject with a typed DeepCopy method.
type DeepCopyableObject[K any] interface {
	reconciler.ResourceObject
	DeepCopy() K
}

// setPhaseAndCondition performs a retry-on-conflict status patch that sets the
// given phase and condition reason. actionErr carries the AAction outcome:
// nil means the action succeeded (" - OK"), non-nil appends the error message.
// Optional prePatch callbacks are applied to the patched copy before the status write.
func setPhaseAndCondition[K DeepCopyableObject[K]](
	c client.Client, ctx context.Context, obj K,
	phase v1alpha1.ResourcePhase, reason string,
	actionErr error,
	prePatch ...func(K),
) error {
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
		rs.Phase = phase

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
				Type:               string(phase),
				Status:             metav1.ConditionTrue,
				Reason:             reason,
				Message:            fmt.Sprintf("%s %s%s", string(phase), reason, msgSuffix),
				LastTransitionTime: metav1.Now(),
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

// setActiveAndSetID performs a retry-on-conflict status patch that sets the
// resource to Active phase, stamps ObservedGeneration, and optionally sets the
// ResourceID from the CMP response. actionErr carries the AAction outcome
// for the condition message. Optional prePatch callbacks are applied to the
// patched copy before the status write.
func setActiveAndSetID[K DeepCopyableObject[K]](
	c client.Client, ctx context.Context, obj K,
	cmpResourceID string,
	actionErr error,
	prePatch ...func(K),
) error {
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

// setFailedOnTimeout performs a retry-on-conflict status patch that moves a
// resource stuck in a transitory phase to Failed, recording the timeout reason
// on both the previous phase's condition and the new Failed condition.
func setFailedOnTimeout[K DeepCopyableObject[K]](
	c client.Client, ctx context.Context, obj K,
	prePatch ...func(K),
) error {
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
		timeoutMsg := fmt.Sprintf("phase timeout exceeded (%s)", reconciler.MaxPhaseTimeout)

		// Set ALL existing conditions to ConditionFalse
		for i := range rs.Conditions {
			rs.Conditions[i].Status = metav1.ConditionFalse
		}

		// Update the timed-out phase condition: Status=False, Reason=Failed
		meta.SetStatusCondition(&rs.Conditions, metav1.Condition{
			Type:    string(previousPhase),
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha1.ConditionReasonFailed,
			Message: fmt.Sprintf("%s %s - %s", string(previousPhase), v1alpha1.ConditionReasonFailed, timeoutMsg),
		})

		// Set phase to Failed
		rs.Phase = v1alpha1.ResourcePhaseFailed

		// Add Failed condition: Status=True, Reason=Failed, same message as the timed-out phase
		meta.SetStatusCondition(&rs.Conditions, metav1.Condition{
			Type:    string(v1alpha1.ResourcePhaseFailed),
			Status:  metav1.ConditionTrue,
			Reason:  v1alpha1.ConditionReasonFailed,
			Message: fmt.Sprintf("%s %s - %s", string(previousPhase), v1alpha1.ConditionReasonFailed, timeoutMsg),
		})

		return c.Status().Patch(ctx, objPatch, client.MergeFrom(objCopy))
	})
}

// tagsAreEqual returns true when both tag slices contain the same elements
// regardless of order.
func tagsAreEqual(a, b []string) bool {
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
