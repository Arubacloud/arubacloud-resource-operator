/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	arubatypes "github.com/Arubacloud/sdk-go/pkg/types"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
)

const (
	keyPairFinalizerName = "keypair.arubacloud.com/finalizer"
)

// KeyPairReconciler reconciles a KeyPair object
type KeyPairReconciler struct {
	*reconciler.Reconciler
	ts *TransitionSet[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]
}

// NewKeyPairReconciler creates a new KeyPairReconciler
func NewKeyPairReconciler(baseReconciler *reconciler.Reconciler) *KeyPairReconciler {
	r := &KeyPairReconciler{
		Reconciler: baseReconciler,
	}

	r.ts = r.newTransitionSet()

	return r
}

// +kubebuilder:rbac:groups=arubacloud.com,resources=keypairs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=arubacloud.com,resources=keypairs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=arubacloud.com,resources=keypairs/finalizers,verbs=update
// +kubebuilder:rbac:groups=arubacloud.com,resources=projects,verbs=get;list;watch

func (r *KeyPairReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.Reconciler.Reconcile(ctx, req, r)
}

func (r *KeyPairReconciler) Object() reconciler.ResourceObject {
	return &v1alpha1.KeyPair{}
}

func (r *KeyPairReconciler) Finalizer() string {
	return keyPairFinalizerName
}

func (r *KeyPairReconciler) HandleReconcile(ctx context.Context, obj reconciler.ResourceObject) (ctrl.Result, error) {
	kubeKp, ok := obj.(*v1alpha1.KeyPair)
	if !ok {
		return ctrl.Result{}, errors.New("obj is not a *v1alpha1.KeyPair")
	}

	if kubeKp.Spec.ProjectReference.Name == "" {
		return ctrl.Result{}, fmt.Errorf("project reference is not valid")
	}

	kpName, projectName := kubeKp.Name, kubeKp.Spec.ProjectReference.Name
	kpFilter := fmt.Sprintf(`name:eq("%s")`, kpName)
	prjFilter := fmt.Sprintf(`name:eq("%s")`, projectName)

	var prjID string

	if !kubeKp.GetDeletionTimestamp().IsZero() && kubeKp.Status.ProjectID != "" {
		prjID = kubeKp.Status.ProjectID
	} else {
		cmpProjectList, err := r.ArubaClient.FromProject().List(ctx, &arubatypes.RequestParameters{Filter: &prjFilter})
		if err != nil {
			return ctrl.Result{}, fmt.Errorf(
				"failed to find project in Aruba cloud: %w, project_name: '%s', project_filter: '%s'",
				err, projectName, prjFilter,
			)
		}
		if cmpProjectList.IsError() {
			return ctrl.Result{}, fmt.Errorf(
				"failed to find project in Aruba cloud: status_code: %d, project_name: '%s', project_filter: '%s'",
				cmpProjectList.StatusCode, projectName, prjFilter,
			)
		}
		if cmpProjectList.Data.Total == 0 && kubeKp.Status.ProjectID != "" {
			return ctrl.Result{}, fmt.Errorf(
				"inconsistent data in project list: expected: 1, project not found: project_name: '%s', project_filter: '%s'", projectName, prjFilter,
			)
		}

		if cmpProjectList.Data.Total > 1 {
			return ctrl.Result{}, fmt.Errorf(
				"inconsistent data in project list: expected: 1, found: %d, project_name: '%s', project_filter: '%s'",
				cmpProjectList.Data.Total, projectName, prjFilter,
			)
		}

		if cmpProjectList.Data.Total == 0 {
			return ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
		}

		prjID = *(cmpProjectList.Data.Values[0].Metadata.ID)
	}

	if kubeKp.Status.ProjectID != "" && kubeKp.Status.ProjectID != prjID {
		return ctrl.Result{}, fmt.Errorf(
			"inconsistent project id in keypair: kp_name: '%s', kp_project_id: '%s', project_name: '%s', project_id: '%s'",
			kpName, kubeKp.Status.ProjectID, projectName, prjID,
		)
	}

	cmpKpList, err := r.ArubaClient.FromCompute().KeyPairs().List(ctx, prjID, &arubatypes.RequestParameters{Filter: &kpFilter})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf(
			"failed to find keypair in Aruba cloud: %w, kp_name: '%s', kp_filter: '%s', project_name: '%s'",
			err, kpName, kpFilter, projectName,
		)
	}
	if cmpKpList.IsError() && cmpKpList.StatusCode != http.StatusNotFound {
		return ctrl.Result{}, fmt.Errorf(
			"failed to find keypair in Aruba cloud: status_code: %d, kp_name: '%s', project_name: '%s'",
			cmpKpList.StatusCode, kpName, projectName,
		)
	}

	if !cmpKpList.IsError() && (cmpKpList.Data.Total < 0 || cmpKpList.Data.Total > 1) {
		return ctrl.Result{}, fmt.Errorf(
			"inconsistent data in keypair list: kp_name: '%s', kp_filter: '%s', project_name: '%s', instances: %d",
			kpName, kpFilter, projectName, cmpKpList.Data.Total,
		)
	}

	var cmpKp *arubatypes.KeyPairResponse
	if cmpKpList.Data != nil && cmpKpList.Data.Total == 1 {
		cmpKp = &cmpKpList.Data.Values[0]
	}

	ctx = context.WithValue(ctx, projectIDKey, prjID)

	return r.ts.Run(ctx, kubeKp, cmpKp)
}

// Transition Set Builder

func (r *KeyPairReconciler) newTransitionSet() *TransitionSet[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse] {
	ts := &TransitionSet[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		defaultRequeue:        NoRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		defaultRequeueOnError: NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	}

	// 0. PhaseTimedOut — safety net: fail if stuck in a transitory phase too long
	ts.Add(&AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		name:           "PhaseTimedOut",
		kCondition:     kubePhaseTimedOut[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		aCondition:     AlwaysTrue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		kAction:        r.kubeSetFailedOnTimeout,
		requeue:        NoRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 1. ShouldBeDeleted — DeletionTimestamp set + active → mark Deleting+ShallSynchronize
	ts.Add(&AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		name:           "ShouldBeDeleted",
		kCondition:     kubeShouldDelete[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		aCondition:     cmpKeyPairExists,
		kAction:        r.kubeMarkToDelete,
		requeue:        ShortRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 2. ShouldDeleteTimedOut — enter deletion flow for timed-out resources
	ts.Add(&AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		name:           "ShouldDeleteTimedOut",
		kCondition:     kubeShouldDeleteTimedOut[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		aCondition:     AlwaysTrue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		kAction:        r.kubeMarkToDelete,
		requeue:        ShortRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 3. ShouldBeDeletedOnCMP — marked Deleting+ShallSynchronize + CMP exists → dispatch delete
	ts.Add(&AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		name:              "ShouldBeDeletedOnCMP",
		kCondition:        kubeShouldBeDeletedOnCMP[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		aCondition:        cmpKeyPairExists,
		aAction:           r.cmpDelete,
		kActionOnASuccess: r.kubeMarkDeleting,
		requeue:           ShortRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		requeueOnError:    LongRequeueAndIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 4. DeletionOnCMPNotNeeded — marked Deleting+ShallSynchronize but CMP already gone
	ts.Add(&AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		name:           "DeletionOnCMPNotNeeded",
		kCondition:     kubeShouldBeDeletedOnCMP[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		aCondition:     cmpKeyPairNotExists,
		kAction:        r.kubeMarkDeletingDone,
		requeue:        ShortRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 5. WaitingDeletionOnCMP — marked Deleting+Synchronizing + CMP still exists → poll
	ts.Add(&AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		name:           "WaitingDeletionOnCMP",
		kCondition:     kubeWaitingDeletionOnCMP[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		aCondition:     cmpKeyPairExists,
		requeue:        LongRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 6. DeletionConfirmedOnCMP — marked Deleting+Synchronizing + CMP gone → advance to Synchronized
	ts.Add(&AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		name:           "DeletionConfirmedOnCMP",
		kCondition:     kubeWaitingDeletionOnCMP[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		aCondition:     cmpKeyPairNotExists,
		kAction:        r.kubeMarkDeletingDone,
		requeue:        ShortRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 7. DeletionAccomplished — marked Deleting+Synchronized + CMP gone → mark Deleted
	ts.Add(&AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		name:           "DeletionAccomplished",
		kCondition:     kubeDeletionAccomplished[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		aCondition:     cmpKeyPairNotExists,
		kAction:        r.kubeMarkDeleted,
		requeue:        ShortRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 8. ShouldBeUpdated — generation changed while Active → enter Updating phase
	ts.Add(&AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		name:           "ShouldBeUpdated",
		kCondition:     kubeActiveAndGenerationChanged[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		aCondition:     cmpKeyPairExists,
		kAction:        r.kubeMarkToUpdate,
		requeue:        ShortRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 9. UpdateNotSupported — Updating+ShallSynchronize + CMP exists → signal failure
	ts.Add(&AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		name:           "UpdateNotSupported",
		kCondition:     kubeShouldBeUpdatedOnCMP[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		aCondition:     cmpKeyPairExists,
		kAction:        r.kubeMarkUpdatingFailed,
		requeue:        ShortRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 10. UpdateRollback — Updating+Failed + CMP exists → rollback spec and return to Active
	ts.Add(&AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		name:           "UpdateRollback",
		kCondition:     kubeKeyPairUpdatingFailed,
		aCondition:     cmpKeyPairExists,
		kAction:        r.kubeRollbackSpecAndSetActive,
		requeue:        NoRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 11. ShouldBeCreated — first reconciliation + CMP not found → mark Creating+ShallSynchronize
	ts.Add(&AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		name:           "ShouldBeCreated",
		kCondition:     kubeIsFirstReconciliation[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		aCondition:     cmpKeyPairNotExists,
		kAction:        r.kubeMarkToCreate,
		requeue:        ShortRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 12. ShouldBeCreatedInCMP — Creating+ShallSynchronize + CMP not found → dispatch create
	ts.Add(&AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		name:              "ShouldBeCreatedInCMP",
		kCondition:        kubeShouldBeCreatedOnCMP[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		aCondition:        cmpKeyPairNotExists,
		aAction:           r.cmpCreate,
		kActionOnASuccess: r.kubeMarkCreating,
		requeue:           ShortRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		requeueOnError:    LongRequeueAndIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 13. WaitingCreationInCMP — Creating+Synchronizing + CMP not found yet → poll
	ts.Add(&AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		name:           "WaitingCreationInCMP",
		kCondition:     kubeWaitingCreationInCMP[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		aCondition:     cmpKeyPairNotExists,
		requeue:        LongRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 14. CreationConfirmedOnCMP — Creating+Synchronizing + CMP found → mark Creating+Synchronized
	ts.Add(&AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		name:           "CreationConfirmedOnCMP",
		kCondition:     kubeWaitingCreationInCMP[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		aCondition:     cmpKeyPairExists,
		kAction:        r.kubeMarkCreatingDone,
		requeue:        ShortRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 15. CreationAccomplished — Creating+Synchronized + CMP found → set Active + store ResourceID
	ts.Add(&AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		name:           "CreationAccomplished",
		kCondition:     kubeIsCreatedOnCMP[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		aCondition:     cmpKeyPairExists,
		kAction:        r.kubeSetActiveAndSetID,
		requeue:        NoRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	return ts
}

// Resource-specific condition functions

func cmpKeyPairExists(_ *v1alpha1.KeyPair, cmpKp *arubatypes.KeyPairResponse) bool {
	return cmpKp != nil
}

func cmpKeyPairNotExists(_ *v1alpha1.KeyPair, cmpKp *arubatypes.KeyPairResponse) bool {
	return cmpKp == nil
}

// kubeKeyPairUpdatingFailed returns true when the resource is in Updating phase
// with a Failed condition reason, indicating a rollback is needed.
func kubeKeyPairUpdatingFailed(kubeKp *v1alpha1.KeyPair, _ *arubatypes.KeyPairResponse) bool {
	if !kubeKp.GetDeletionTimestamp().IsZero() {
		return false
	}
	rs := kubeKp.GetResourceStatus()
	if rs == nil || rs.Phase != v1alpha1.ResourcePhaseUpdating {
		return false
	}
	condition := meta.FindStatusCondition(rs.Conditions, string(v1alpha1.ResourcePhaseUpdating))
	return condition != nil &&
		condition.Status == metav1.ConditionTrue &&
		condition.Reason == v1alpha1.ConditionReasonFailed
}

// Kube action methods

func (r *KeyPairReconciler) kubeSetPhaseAndCondition(ctx context.Context, kubeKp *v1alpha1.KeyPair, phase v1alpha1.ResourcePhase, reason string, actionErr error) error {
	return setPhaseAndCondition(r.Client, ctx, kubeKp, phase, reason, actionErr, func(kp *v1alpha1.KeyPair) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && kp.Status.ProjectID == "" {
			kp.Status.ProjectID = prjID
		}
	})
}

func (r *KeyPairReconciler) kubeSetFailedOnTimeout(ctx context.Context, kubeKp *v1alpha1.KeyPair, _ *arubatypes.KeyPairResponse) error {
	return setFailedOnTimeout(r.Client, ctx, kubeKp, func(kp *v1alpha1.KeyPair) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && kp.Status.ProjectID == "" {
			kp.Status.ProjectID = prjID
		}
	})
}

func (r *KeyPairReconciler) kubeMarkToDelete(ctx context.Context, kubeKp *v1alpha1.KeyPair, _ *arubatypes.KeyPairResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeKp, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *KeyPairReconciler) kubeMarkDeleting(ctx context.Context, kubeKp *v1alpha1.KeyPair, _ *arubatypes.KeyPairResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeKp, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *KeyPairReconciler) kubeMarkDeletingDone(ctx context.Context, kubeKp *v1alpha1.KeyPair, _ *arubatypes.KeyPairResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeKp, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *KeyPairReconciler) kubeMarkDeleted(ctx context.Context, kubeKp *v1alpha1.KeyPair, _ *arubatypes.KeyPairResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeKp, v1alpha1.ResourcePhaseDeleted, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *KeyPairReconciler) kubeMarkToUpdate(ctx context.Context, kubeKp *v1alpha1.KeyPair, _ *arubatypes.KeyPairResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeKp, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize, nil)
}

// kubeMarkUpdatingFailed sets the Updating phase with a Failed reason, signalling
// that the update is not supported for KeyPair resources.
func (r *KeyPairReconciler) kubeMarkUpdatingFailed(ctx context.Context, kubeKp *v1alpha1.KeyPair, _ *arubatypes.KeyPairResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeKp, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonFailed,
		errors.New("updating KeyPair resources is not supported"))
}

func (r *KeyPairReconciler) kubeMarkToCreate(ctx context.Context, kubeKp *v1alpha1.KeyPair, _ *arubatypes.KeyPairResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeKp, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *KeyPairReconciler) kubeMarkCreating(ctx context.Context, kubeKp *v1alpha1.KeyPair, _ *arubatypes.KeyPairResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeKp, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *KeyPairReconciler) kubeMarkCreatingDone(ctx context.Context, kubeKp *v1alpha1.KeyPair, _ *arubatypes.KeyPairResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeKp, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *KeyPairReconciler) kubeSetActiveAndSetID(ctx context.Context, kubeKp *v1alpha1.KeyPair, cmpKp *arubatypes.KeyPairResponse) error {
	cmpID := ""
	if cmpKp != nil && cmpKp.Metadata.ID != nil {
		cmpID = *cmpKp.Metadata.ID
	}
	return setActiveAndSetID(r.Client, ctx, kubeKp, cmpID, nil, func(kp *v1alpha1.KeyPair) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && kp.Status.ProjectID == "" {
			kp.Status.ProjectID = prjID
		}
	})
}

// kubeRollbackSpecAndSetActive restores the spec fields from the CMP response and
// then sets the resource back to Active phase.
func (r *KeyPairReconciler) kubeRollbackSpecAndSetActive(ctx context.Context, kubeKp *v1alpha1.KeyPair, cmpKp *arubatypes.KeyPairResponse) error {
	// Step 1: rollback spec to match CMP values (object patch, not status patch)
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		kpCopy := kubeKp.DeepCopy()
		if err := r.Client.Get(ctx, client.ObjectKeyFromObject(kubeKp), kpCopy); err != nil {
			return err
		}

		kpPatch := kpCopy.DeepCopy()
		kpPatch.Spec.Tags = cmpKp.Metadata.Tags
		if cmpKp.Metadata.LocationResponse != nil {
			kpPatch.Spec.Location.Value = cmpKp.Metadata.LocationResponse.Value
		}
		kpPatch.Spec.Value = cmpKp.Properties.Value

		return r.Client.Patch(ctx, kpPatch, client.MergeFrom(kpCopy))
	}); err != nil {
		return fmt.Errorf("failed to rollback keypair '%s' spec: %w", kubeKp.Name, err)
	}

	// Step 2: set Active — reads fresh object (with new generation from spec patch)
	return r.kubeSetActiveAndSetID(ctx, kubeKp, cmpKp)
}

// CMP action methods

func (r *KeyPairReconciler) cmpCreate(ctx context.Context, kubeKp *v1alpha1.KeyPair, _ *arubatypes.KeyPairResponse) error {
	prjID := ctx.Value(projectIDKey).(string)

	cmpKpResp, err := r.ArubaClient.FromCompute().KeyPairs().Create(ctx, prjID, cmpKeyPairRequestFromKube(kubeKp), nil)
	if err != nil {
		return fmt.Errorf("failed to create keypair '%s' in Aruba CMP: error: '%w'", kubeKp.Name, err)
	}

	switch cmpKpResp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusAccepted:
		// Do nothing, we can consider the create request as successful

	case http.StatusBadRequest:
		return fmt.Errorf(
			"failed to create keypair '%s' in Aruba CMP: status_code: %d, error_nature: 'semantic or precondition error', error: '%s'",
			kubeKp.Name, cmpKpResp.StatusCode, cmpErrorDetails(cmpKpResp.Error),
		)

	default:
		return fmt.Errorf(
			"failed to create keypair '%s' in Aruba CMP: status_code: %d, error_nature: 'internal error', error: '%s'",
			kubeKp.Name, cmpKpResp.StatusCode, cmpErrorDetails(cmpKpResp.Error),
		)
	}

	return nil
}

func (r *KeyPairReconciler) cmpDelete(ctx context.Context, kubeKp *v1alpha1.KeyPair, cmpKp *arubatypes.KeyPairResponse) error {
	prjID := ctx.Value(projectIDKey).(string)

	cmpKpResp, err := r.ArubaClient.FromCompute().KeyPairs().Delete(ctx, prjID, *cmpKp.Metadata.ID, nil)
	if err != nil {
		return fmt.Errorf("failed to delete keypair '%s' in Aruba CMP: error: '%w'", *cmpKp.Metadata.Name, err)
	}

	switch cmpKpResp.StatusCode {
	case http.StatusOK, http.StatusAccepted, http.StatusNoContent, http.StatusNotFound:
		// Do nothing, we can consider the delete request as successful

	case http.StatusBadRequest:
		return fmt.Errorf(
			"failed to delete keypair '%s' in Aruba CMP: status_code: %d, error_nature: 'semantic or precondition error', error: '%s'",
			*cmpKp.Metadata.Name, cmpKpResp.StatusCode, cmpErrorDetails(cmpKpResp.Error),
		)

	default:
		return fmt.Errorf(
			"failed to delete keypair '%s' in Aruba CMP: status_code: %d, error_nature: 'internal error', error: '%s'",
			*cmpKp.Metadata.Name, cmpKpResp.StatusCode, cmpErrorDetails(cmpKpResp.Error),
		)
	}

	return nil
}

// Helper functions

func cmpKeyPairRequestFromKube(kubeKp *v1alpha1.KeyPair) arubatypes.KeyPairRequest {
	return arubatypes.KeyPairRequest{
		Metadata: arubatypes.RegionalResourceMetadataRequest{
			ResourceMetadataRequest: arubatypes.ResourceMetadataRequest{
				Name: kubeKp.Name,
				Tags: kubeKp.Spec.Tags,
			},
			Location: arubatypes.LocationRequest{
				Value: kubeKp.Spec.Location.Value,
			},
		},
		Properties: arubatypes.KeyPairPropertiesRequest{
			Value: kubeKp.Spec.Value,
		},
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *KeyPairReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.KeyPair{}).
		Named("keypair").
		Complete(r)
}
