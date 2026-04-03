package reconciler

import (
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
)

// KubePhaseTimedOut returns true when the resource has been stuck in a transitory
// phase with reason ShallSynchronize or Synchronizing for longer than MaxPhaseTimeout.
func KubePhaseTimedOut[K ResourceObject, A any](k K, _ A) bool {
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

	elapsed := time.Since(condition.LastTransitionTime.Time)
	timed := elapsed > MaxPhaseTimeout
	ctrl.Log.V(2).Info("checking phase timeout",
		"resource", k.GetNamespace()+"/"+k.GetName(),
		"phase", string(rs.Phase),
		"elapsed", elapsed.String(),
		"timeout", MaxPhaseTimeout.String(),
		"timedOut", timed,
	)
	return timed
}

// kubeHasPhaseAndReason is a shared helper that checks the common pattern:
// DeletionTimestamp state + Phase + Condition Reason.
func kubeHasPhaseAndReason(obj ResourceObject, expectDeleting bool, phase v1alpha1.ResourcePhase, reason string) bool {
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

// KubeShouldDelete checks whether the resource should begin the deletion flow.
// It is deleting, in a final phase (not Deleted), and the current condition is Synchronized.
func KubeShouldDelete[K ResourceObject, A any](k K, _ A) bool {
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

// KubeShouldDeleteTimedOut checks whether a timed-out resource (Phase=Failed, Reason=Failed)
// should enter the deletion flow. Returns false when the timed-out phase was Deleting,
// since re-entering deletion for an already-failing delete is pointless.
func KubeShouldDeleteTimedOut[K ResourceObject, A any](k K, _ A) bool {
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

// KubeShouldBeDeletedOnCMP checks: deleting, Phase=Deleting, Reason=ShallSynchronize.
func KubeShouldBeDeletedOnCMP[K ResourceObject, A any](k K, _ A) bool {
	return kubeHasPhaseAndReason(k, true, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize)
}

// KubeWaitingDeletionOnCMP checks: deleting, Phase=Deleting, Reason=Synchronizing.
func KubeWaitingDeletionOnCMP[K ResourceObject, A any](k K, _ A) bool {
	return kubeHasPhaseAndReason(k, true, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronizing)
}

// KubeDeletionAccomplished checks: deleting, Phase=Deleting, Reason=Synchronized.
func KubeDeletionAccomplished[K ResourceObject, A any](k K, _ A) bool {
	return kubeHasPhaseAndReason(k, true, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronized)
}

// KubeIsFirstReconciliation checks: not deleting, no ResourceID, Phase=Pending,
// Pending condition with Reason=Synchronized. This matches resources that have
// had their finalizer and initial Pending status set by the base reconciler but have
// not yet been processed by HandleReconcile.
func KubeIsFirstReconciliation[K ResourceObject, A any](k K, _ A) bool {
	rs := k.GetResourceStatus()
	if rs == nil {
		return false
	}
	return k.GetDeletionTimestamp().IsZero() &&
		rs.ResourceID == "" &&
		kubeHasPhaseAndReason(k, false, v1alpha1.ResourcePhasePending, v1alpha1.ConditionReasonSynchronized)
}

// KubePendingAndDeleting returns true when the resource is being deleted while still in
// Pending phase (no CMP resource was ever created). The deletion flow can skip all CMP
// interaction and transition directly to Deleted.
func KubePendingAndDeleting[K ResourceObject, A any](k K, _ A) bool {
	rs := k.GetResourceStatus()
	if rs == nil {
		return false
	}
	return !k.GetDeletionTimestamp().IsZero() &&
		rs.Phase == v1alpha1.ResourcePhasePending &&
		rs.ResourceID == ""
}

// KubeShouldBeCreatedOnCMP checks: not deleting, no ResourceID, Phase=Creating, Reason=ShallSynchronize.
func KubeShouldBeCreatedOnCMP[K ResourceObject, A any](k K, _ A) bool {
	rs := k.GetResourceStatus()
	if rs == nil {
		return false
	}
	return k.GetDeletionTimestamp().IsZero() &&
		rs.ResourceID == "" &&
		kubeHasPhaseAndReason(k, false, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize)
}

// KubeWaitingCreationInCMP checks: not deleting, no ResourceID, Phase=Creating, Reason=Synchronizing.
func KubeWaitingCreationInCMP[K ResourceObject, A any](k K, _ A) bool {
	rs := k.GetResourceStatus()
	if rs == nil {
		return false
	}
	return k.GetDeletionTimestamp().IsZero() &&
		rs.ResourceID == "" &&
		kubeHasPhaseAndReason(k, false, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing)
}

// KubeIsCreatedOnCMP checks: not deleting, no ResourceID, Phase=Creating, Reason=Synchronized.
func KubeIsCreatedOnCMP[K ResourceObject, A any](k K, _ A) bool {
	rs := k.GetResourceStatus()
	if rs == nil {
		return false
	}
	return k.GetDeletionTimestamp().IsZero() &&
		rs.ResourceID == "" &&
		kubeHasPhaseAndReason(k, false, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized)
}

// KubeShouldBeUpdatedOnCMP checks: not deleting, Phase=Updating, Reason=ShallSynchronize.
func KubeShouldBeUpdatedOnCMP[K ResourceObject, A any](k K, _ A) bool {
	return kubeHasPhaseAndReason(k, false, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize)
}

// KubeWaitingUpdateOnCMP checks: not deleting, Phase=Updating, Reason=Synchronizing.
func KubeWaitingUpdateOnCMP[K ResourceObject, A any](k K, _ A) bool {
	return kubeHasPhaseAndReason(k, false, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronizing)
}

// KubeUpdateAccomplished checks: not deleting, Phase=Updating, Reason=Synchronized.
func KubeUpdateAccomplished[K ResourceObject, A any](k K, _ A) bool {
	return kubeHasPhaseAndReason(k, false, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronized)
}

// IsResourceReady reports whether obj is Active+Synchronized and not being deleted.
// It is used by child controllers to gate reconciliation until all K8s dependencies
// have reached a stable state.
func IsResourceReady(obj ResourceObject) bool {
	return kubeHasPhaseAndReason(obj, false, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized)
}

// IsValidationFailed reports whether obj is in Failed+ValidationFailed — the state
// set when the CMP API returns a semantic error (4xx with field-level validation
// details). Used by IsCMPValidationFailedAndSpecChanged to gate recovery.
func IsValidationFailed(obj ResourceObject) bool {
	return kubeHasPhaseAndReason(obj, false, v1alpha1.ResourcePhaseFailed, v1alpha1.ConditionReasonValidationFailed)
}

// IsIntentionValidationFailed reports whether obj is in Failed+IntentionValidationFailed
// — the state set when the K8s-only intention validator (ivs) detects a cross-resource
// inconsistency (e.g. region/zone/tenant mismatch). Recovery fires when ivs passes on
// a subsequent reconcile.
func IsIntentionValidationFailed(obj ResourceObject) bool {
	return kubeHasPhaseAndReason(obj, false, v1alpha1.ResourcePhaseFailed, v1alpha1.ConditionReasonIntentionValidationFailed)
}

// IsPostValidationFailed reports whether obj is in Failed+PostValidationFailed — the
// state set when the CMP-aware post-validator (vs) detects a cross-resource
// inconsistency. Recovery fires when vs passes on a subsequent reconcile.
func IsPostValidationFailed(obj ResourceObject) bool {
	return kubeHasPhaseAndReason(obj, false, v1alpha1.ResourcePhaseFailed, v1alpha1.ConditionReasonPostValidationFailed)
}

// IsCMPValidationFailedAndSpecChanged reports whether obj is in Failed+ValidationFailed
// (a CMP semantic error) AND the spec has been modified since the failure was recorded.
// This is the recovery gate for CMP semantic errors: recovery only fires when the user
// has corrected the spec (generation incremented), preventing pointless retries.
func IsCMPValidationFailedAndSpecChanged(obj ResourceObject) bool {
	if !IsValidationFailed(obj) {
		return false
	}
	rs := obj.GetResourceStatus()
	cond := meta.FindStatusCondition(rs.Conditions, string(v1alpha1.ResourcePhaseFailed))
	if cond == nil {
		return false
	}
	return cond.ObservedGeneration != obj.GetGeneration()
}

// IsAnyValidationFailedAndDeleting reports whether the resource has a deletion
// timestamp and is stuck in any *ValidationFailed state. Used by controllers to
// unblock deletion: resources are reset to a deletable phase so that KubeShouldDelete
// or KubePendingAndDeleting can match on the next reconcile.
func IsAnyValidationFailedAndDeleting(obj ResourceObject) bool {
	rs := obj.GetResourceStatus()
	if rs == nil || rs.Phase != v1alpha1.ResourcePhaseFailed || obj.GetDeletionTimestamp().IsZero() {
		return false
	}
	cond := meta.FindStatusCondition(rs.Conditions, string(v1alpha1.ResourcePhaseFailed))
	if cond == nil || cond.Status != metav1.ConditionTrue {
		return false
	}
	return cond.Reason == v1alpha1.ConditionReasonValidationFailed ||
		cond.Reason == v1alpha1.ConditionReasonIntentionValidationFailed ||
		cond.Reason == v1alpha1.ConditionReasonPostValidationFailed
}

// KubeAnyValidationFailedAndDeleting wraps IsAnyValidationFailedAndDeleting as a
// ConditionFunc so it can be used directly in an AbstractTransition.
func KubeAnyValidationFailedAndDeleting[K ResourceObject, A any](k K, _ A) bool {
	return IsAnyValidationFailedAndDeleting(k)
}

// KubeActiveAndGenerationChanged checks: not deleting, Phase=Active, ResourceID set,
// ObservedGeneration != Generation, Active condition is True+Synchronized.
func KubeActiveAndGenerationChanged[K ResourceObject, A any](k K, _ A) bool {
	rs := k.GetResourceStatus()
	if rs == nil {
		return false
	}
	condition := meta.FindStatusCondition(rs.Conditions, string(v1alpha1.ResourcePhaseActive))

	changed := k.GetDeletionTimestamp().IsZero() &&
		rs.Phase == v1alpha1.ResourcePhaseActive &&
		rs.ResourceID != "" &&
		rs.ObservedGeneration != k.GetGeneration() &&
		condition != nil &&
		condition.Status == metav1.ConditionTrue &&
		condition.Reason == v1alpha1.ConditionReasonSynchronized
	ctrl.Log.V(2).Info("checking generation change",
		"resource", k.GetNamespace()+"/"+k.GetName(),
		"observedGeneration", rs.ObservedGeneration,
		"generation", k.GetGeneration(),
		"changed", changed,
	)
	return changed
}
