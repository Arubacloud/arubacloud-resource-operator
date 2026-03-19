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
// given phase and condition reason. Optional prePatch callbacks are applied to the
// patched copy before the status write.
func setPhaseAndCondition[K DeepCopyableObject[K]](
	c client.Client, ctx context.Context, obj K,
	phase v1alpha1.ResourcePhase, reason string,
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

		meta.SetStatusCondition(
			&rs.Conditions,
			metav1.Condition{
				Type:               string(phase),
				Status:             metav1.ConditionTrue,
				Reason:             reason,
				Message:            fmt.Sprintf("%s %s", string(phase), reason),
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
// ResourceID from the CMP response. Optional prePatch callbacks are applied to the
// patched copy before the status write.
func setActiveAndSetID[K DeepCopyableObject[K]](
	c client.Client, ctx context.Context, obj K,
	cmpResourceID string,
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

		meta.SetStatusCondition(
			&rs.Conditions,
			metav1.Condition{
				Type:               string(v1alpha1.ResourcePhaseActive),
				Status:             metav1.ConditionTrue,
				Reason:             v1alpha1.ConditionReasonSynchronized,
				Message:            fmt.Sprintf("%s %s", string(v1alpha1.ResourcePhaseActive), v1alpha1.ConditionReasonSynchronized),
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
