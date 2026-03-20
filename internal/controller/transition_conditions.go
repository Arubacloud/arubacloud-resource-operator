package controller

import (
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
)

// kubePhaseTimedOut returns true when the resource has been stuck in a transitory
// phase with reason ShallSynchronize or Synchronizing for longer than MaxPhaseTimeout.
func kubePhaseTimedOut[K reconciler.ResourceObject, A any](k K, _ A) bool {
	rs := k.GetResourceStatus()
	if rs == nil || rs.AssessPhaseNature() != v1alpha1.PhaseNatureTransitory {
		return false
	}

	condition := meta.FindStatusCondition(rs.Conditions, string(rs.Phase))
	if condition == nil || condition.Status != metav1.ConditionTrue {
		return false
	}

	if condition.Reason != v1alpha1.ConditionReasonShallSynchronize &&
		condition.Reason != v1alpha1.ConditionReasonSynchronizing {
		return false
	}

	return time.Since(condition.LastTransitionTime.Time) > reconciler.MaxPhaseTimeout
}

// kubeHasPhaseAndReason is a shared helper that checks the common pattern:
// DeletionTimestamp state + Phase + Condition Reason.
func kubeHasPhaseAndReason(obj reconciler.ResourceObject, expectDeleting bool, phase v1alpha1.ResourcePhase, reason string) bool {
	rs := obj.GetResourceStatus()
	if rs == nil {
		return false
	}

	if expectDeleting && obj.GetDeletionTimestamp().IsZero() {
		return false
	}
	if !expectDeleting && !obj.GetDeletionTimestamp().IsZero() {
		return false
	}

	if rs.Phase != phase {
		return false
	}

	condition := meta.FindStatusCondition(rs.Conditions, string(phase))
	return condition != nil &&
		condition.Status == metav1.ConditionTrue &&
		condition.Reason == reason
}

// kubeShouldDelete checks whether the resource should begin the deletion flow.
// It is deleting, in a final phase (not Deleted), and the current condition is Synchronized.
func kubeShouldDelete[K reconciler.ResourceObject, A any](k K, _ A) bool {
	rs := k.GetResourceStatus()
	if rs == nil {
		return false
	}
	condition := meta.FindStatusCondition(rs.Conditions, string(rs.Phase))

	return !k.GetDeletionTimestamp().IsZero() &&
		rs.AssessPhaseNature() == v1alpha1.PhaseNatureFinal &&
		rs.Phase != v1alpha1.ResourcePhaseDeleted &&
		condition != nil &&
		condition.Status == metav1.ConditionTrue &&
		condition.Reason == v1alpha1.ConditionReasonSynchronized
}

// failedPhase returns the phase that triggered a timeout-driven failure, or "" if none.
// After setFailedOnTimeout, exactly one condition has Status=False + Reason=Failed
// and its Type encodes the phase that timed out.
func failedPhase(rs *v1alpha1.ResourceStatus) v1alpha1.ResourcePhase {
	for _, c := range rs.Conditions {
		if c.Status == metav1.ConditionFalse && c.Reason == v1alpha1.ConditionReasonFailed {
			return v1alpha1.ResourcePhase(c.Type)
		}
	}
	return ""
}

// kubeShouldDeleteTimedOut checks whether a timed-out resource (Phase=Failed, Reason=Failed)
// should enter the deletion flow. Returns false when the timed-out phase was Deleting,
// since re-entering deletion for an already-failing delete is pointless.
func kubeShouldDeleteTimedOut[K reconciler.ResourceObject, A any](k K, _ A) bool {
	rs := k.GetResourceStatus()
	if rs == nil || k.GetDeletionTimestamp().IsZero() {
		return false
	}
	if rs.Phase != v1alpha1.ResourcePhaseFailed {
		return false
	}

	failedCond := meta.FindStatusCondition(rs.Conditions, string(v1alpha1.ResourcePhaseFailed))
	if failedCond == nil || failedCond.Status != metav1.ConditionTrue || failedCond.Reason != v1alpha1.ConditionReasonFailed {
		return false
	}

	prevPhase := failedPhase(rs)
	return prevPhase != "" && prevPhase != v1alpha1.ResourcePhaseDeleting
}

// kubeShouldBeDeletedOnCMP checks: deleting, Phase=Deleting, Reason=ShallSynchronize.
func kubeShouldBeDeletedOnCMP[K reconciler.ResourceObject, A any](k K, _ A) bool {
	return kubeHasPhaseAndReason(k, true, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize)
}

// kubeWaitingDeletionOnCMP checks: deleting, Phase=Deleting, Reason=Synchronizing.
func kubeWaitingDeletionOnCMP[K reconciler.ResourceObject, A any](k K, _ A) bool {
	return kubeHasPhaseAndReason(k, true, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronizing)
}

// kubeDeletionAccomplished checks: deleting, Phase=Deleting, Reason=Synchronized.
func kubeDeletionAccomplished[K reconciler.ResourceObject, A any](k K, _ A) bool {
	return kubeHasPhaseAndReason(k, true, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronized)
}

// kubeIsFirstReconciliation checks: not deleting, no ResourceID, no Phase, no Conditions.
func kubeIsFirstReconciliation[K reconciler.ResourceObject, A any](k K, _ A) bool {
	rs := k.GetResourceStatus()
	if rs == nil {
		return false
	}
	return k.GetDeletionTimestamp().IsZero() &&
		rs.ResourceID == "" &&
		rs.Phase == "" &&
		len(rs.Conditions) == 0
}

// kubeShouldBeCreatedOnCMP checks: not deleting, no ResourceID, Phase=Creating, Reason=ShallSynchronize.
func kubeShouldBeCreatedOnCMP[K reconciler.ResourceObject, A any](k K, _ A) bool {
	rs := k.GetResourceStatus()
	if rs == nil {
		return false
	}
	return k.GetDeletionTimestamp().IsZero() &&
		rs.ResourceID == "" &&
		kubeHasPhaseAndReason(k, false, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize)
}

// kubeWaitingCreationInCMP checks: not deleting, no ResourceID, Phase=Creating, Reason=Synchronizing.
func kubeWaitingCreationInCMP[K reconciler.ResourceObject, A any](k K, _ A) bool {
	rs := k.GetResourceStatus()
	if rs == nil {
		return false
	}
	return k.GetDeletionTimestamp().IsZero() &&
		rs.ResourceID == "" &&
		kubeHasPhaseAndReason(k, false, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing)
}

// kubeIsCreatedOnCMP checks: not deleting, no ResourceID, Phase=Creating, Reason=Synchronized.
func kubeIsCreatedOnCMP[K reconciler.ResourceObject, A any](k K, _ A) bool {
	rs := k.GetResourceStatus()
	if rs == nil {
		return false
	}
	return k.GetDeletionTimestamp().IsZero() &&
		rs.ResourceID == "" &&
		kubeHasPhaseAndReason(k, false, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized)
}

// kubeShouldBeUpdatedOnCMP checks: not deleting, Phase=Updating, Reason=ShallSynchronize.
func kubeShouldBeUpdatedOnCMP[K reconciler.ResourceObject, A any](k K, _ A) bool {
	return kubeHasPhaseAndReason(k, false, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize)
}

// kubeWaitingUpdateOnCMP checks: not deleting, Phase=Updating, Reason=Synchronizing.
func kubeWaitingUpdateOnCMP[K reconciler.ResourceObject, A any](k K, _ A) bool {
	return kubeHasPhaseAndReason(k, false, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronizing)
}

// kubeUpdateAccomplished checks: not deleting, Phase=Updating, Reason=Synchronized.
func kubeUpdateAccomplished[K reconciler.ResourceObject, A any](k K, _ A) bool {
	return kubeHasPhaseAndReason(k, false, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronized)
}

// kubeActiveAndGenerationChanged checks: not deleting, Phase=Active, ResourceID set,
// ObservedGeneration != Generation, Active condition is True+Synchronized.
func kubeActiveAndGenerationChanged[K reconciler.ResourceObject, A any](k K, _ A) bool {
	rs := k.GetResourceStatus()
	if rs == nil {
		return false
	}
	condition := meta.FindStatusCondition(rs.Conditions, string(v1alpha1.ResourcePhaseActive))

	return k.GetDeletionTimestamp().IsZero() &&
		rs.Phase == v1alpha1.ResourcePhaseActive &&
		rs.ResourceID != "" &&
		rs.ObservedGeneration != k.GetGeneration() &&
		condition != nil &&
		condition.Status == metav1.ConditionTrue &&
		condition.Reason == v1alpha1.ConditionReasonSynchronized
}
