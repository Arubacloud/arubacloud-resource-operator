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

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/Arubacloud/sdk-go/pkg/aruba"
	arubatypes "github.com/Arubacloud/sdk-go/pkg/types"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
)

const (
	blockStorageFinalizerName = "blockstorage.arubacloud.com/finalizer"
)

type contextKey string

const projectIDKey contextKey = "projectID"

// BlockStorageReconciler reconciles a BlockStorage object
type BlockStorageReconciler struct {
	*reconciler.Reconciler
	ts *TransitionSet[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]
}

// NewBlockStorageReconciler creates a new BlockStorageReconciler
func NewBlockStorageReconciler(baseReconciler *reconciler.Reconciler) *BlockStorageReconciler {
	r := &BlockStorageReconciler{
		Reconciler: baseReconciler,
	}

	r.ts = r.newTransitionSet()

	return r
}

// +kubebuilder:rbac:groups=arubacloud.com,resources=blockstorages,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=arubacloud.com,resources=blockstorages/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=arubacloud.com,resources=blockstorages/finalizers,verbs=update
// +kubebuilder:rbac:groups=arubacloud.com,resources=projects,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

func (r *BlockStorageReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.Reconciler.Reconcile(ctx, req, r)
}

func (r *BlockStorageReconciler) Object() reconciler.ResourceObject {
	return &v1alpha1.BlockStorage{}
}

func (r *BlockStorageReconciler) Finalizer() string {
	return blockStorageFinalizerName
}

func (r *BlockStorageReconciler) HandleReconcile(ctx context.Context, obj reconciler.ResourceObject) (ctrl.Result, error) {
	kubeBlockStorage, ok := obj.(*v1alpha1.BlockStorage)
	if !ok {
		return ctrl.Result{}, errors.New("obj is not a *v1alpha1.BlockStorage")
	}

	logger := log.FromContext(ctx).WithValues("tenant", kubeBlockStorage.Spec.Tenant)
	logger.Info("reconciling block storage")

	arubaClient, err := r.ArubaClient(kubeBlockStorage.Spec.Tenant)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get Aruba client: %w", err)
	}

	if kubeBlockStorage.Spec.ProjectReference.Name == "" {
		return ctrl.Result{}, fmt.Errorf("project reference is not valid")
	}

	blockStorageName, projectName := kubeBlockStorage.Name, kubeBlockStorage.Spec.ProjectReference.Name
	bsFilter := fmt.Sprintf(`name:eq("%s")`, blockStorageName)
	prjFilter := fmt.Sprintf(`name:eq("%s")`, projectName)

	var prjID string

	if !kubeBlockStorage.GetDeletionTimestamp().IsZero() && kubeBlockStorage.Status.ProjectID != "" {
		prjID = kubeBlockStorage.Status.ProjectID
	} else {
		cmpProjectList, err := arubaClient.FromProject().List(ctx, &arubatypes.RequestParameters{Filter: &prjFilter})
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
		if cmpProjectList.Data.Total == 0 && kubeBlockStorage.Status.ProjectID != "" {
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
			// Wait for the project to be created
			logger.V(1).Info("parent project not found on CMP, requeuing", "projectName", projectName)
			return ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
		}

		prjID = *(cmpProjectList.Data.Values[0].Metadata.ID)
	}

	if kubeBlockStorage.Status.ProjectID != "" && kubeBlockStorage.Status.ProjectID != prjID {
		return ctrl.Result{}, fmt.Errorf(
			"inconsistent project id in blockstorage: blockstorage_name: '%s', blockstorage_project_id: '%s', project_name: '%s', project_id: '%s'",
			blockStorageName, kubeBlockStorage.Status.ProjectID, projectName, prjID,
		)
	}

	cmpBlockStorageList, err := arubaClient.FromStorage().Volumes().List(ctx, prjID, &arubatypes.RequestParameters{Filter: &bsFilter})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf(
			"failed to find blockstorage in Aruba cloud: %w, blockstorage_name: '%s', blockstorage_filter: '%s', project_name: '%s'",
			err, blockStorageName, bsFilter, projectName,
		)
	}
	if cmpBlockStorageList.IsError() {
		return ctrl.Result{}, fmt.Errorf(
			"failed to find blockstorage in Aruba cloud: status_code: %d, blockstorage_name: '%s', project_name: '%s'",
			cmpBlockStorageList.StatusCode, blockStorageName, projectName,
		)
	}

	if cmpBlockStorageList.Data.Total < 0 || cmpBlockStorageList.Data.Total > 1 {
		return ctrl.Result{}, fmt.Errorf(
			"inconsistent data in blockstorage list: blockstorage_name: '%s', blockstorage_filter: '%s', project_name: '%s', instances: %d",
			blockStorageName, bsFilter, projectName, len(cmpBlockStorageList.Data.Values),
		)
	}

	var cmpBlockStorage *arubatypes.BlockStorageResponse
	if cmpBlockStorageList.Data.Total == 1 {
		cmpBlockStorage = &cmpBlockStorageList.Data.Values[0]
	}
	logger.V(1).Info("CMP block storage state", "found", cmpBlockStorage != nil, "projectID", prjID)

	ctx = context.WithValue(ctx, projectIDKey, prjID)
	ctx = context.WithValue(ctx, reconciler.ArubaClientKey, arubaClient)
	ctx = log.IntoContext(ctx, logger)

	return r.ts.Run(ctx, kubeBlockStorage, cmpBlockStorage)
}

// Transition Set Builder

func (r *BlockStorageReconciler) newTransitionSet() *TransitionSet[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse] {
	ts := &TransitionSet[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		defaultRequeue:        NoRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		defaultRequeueOnError: NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	}

	// 0. PhaseTimedOut — safety net: fail if stuck in a transitory phase too long
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:           "PhaseTimedOut",
		kCondition:     kubePhaseTimedOut[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		aCondition:     AlwaysTrue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		kAction:        r.kubeSetFailedOnTimeout,
		requeue:        NoRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 1. ShouldBeDeleted
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:           "ShouldBeDeleted",
		kCondition:     kubeShouldDelete[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		aCondition:     cmpBlockStorageIsFinal,
		kAction:        r.kubeMarkToDelete,
		requeue:        ShortRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 1b. ShouldDeleteTimedOut — enter deletion flow for timed-out resources (except those that timed out during Deleting)
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:           "ShouldDeleteTimedOut",
		kCondition:     kubeShouldDeleteTimedOut[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		aCondition:     AlwaysTrue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		kAction:        r.kubeMarkToDelete,
		requeue:        ShortRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 2. ShouldBeDeletedOnCMP
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:              "ShouldBeDeletedOnCMP",
		kCondition:        kubeShouldBeDeletedOnCMP[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		aCondition:        cmpBlockStorageIsFinal,
		aAction:           r.cmpDelete,
		kActionOnASuccess: r.kubeMarkDeleting,
		kActionOnAError:   kubeSetErrorMessageOnCMPError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse](r.Client),
		requeue:           ShortRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError:    SmartRequeueOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 2b. DeletionOnCMPNotNeeded — resource marked for deletion but CMP resource doesn't exist; skip CMP delete
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:           "DeletionOnCMPNotNeeded",
		kCondition:     kubeShouldBeDeletedOnCMP[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		aCondition:     cmpBlockStorageNotExists,
		kAction:        r.kubeMarkDeletingDone,
		requeue:        ShortRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 3. WaitingDeletionOnCMP
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:           "WaitingDeletionOnCMP",
		kCondition:     kubeWaitingDeletionOnCMP[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		aCondition:     cmpBlockStorageIsTransitory,
		requeue:        LongRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 4. DeletionConfirmedOnCMP
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:           "DeletionConfirmedOnCMP",
		kCondition:     kubeWaitingDeletionOnCMP[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		aCondition:     cmpBlockStorageNotExists,
		kAction:        r.kubeMarkDeletingDone,
		requeue:        ShortRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 5. DeletionAccomplished
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:           "DeletionAccomplished",
		kCondition:     kubeDeletionAccomplished[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		aCondition:     cmpBlockStorageNotExists,
		kAction:        r.kubeMarkDeleted,
		requeue:        ShortRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 6a. HasDeniedChanges — surface immutable field violations before attempting update
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:       "HasDeniedChanges",
		kCondition: kubeBlockStorageHasDeniedChanges,
		aCondition: cmpBlockStorageIsFinal,
		kAction: func(ctx context.Context, kubeBS *v1alpha1.BlockStorage, cmpBS *arubatypes.BlockStorageResponse) error {
			return fmt.Errorf("blockstorage update rejected: %w", checkBlockStorageDeniedChanges(kubeBS, cmpBS))
		},
		requeue:        NoRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError: LongRequeueAndIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 6b. SpecAlreadyInSyncWithCMP — generation changed but spec identical to CMP; just re-stamp ObservedGeneration
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:           "SpecAlreadyInSyncWithCMP",
		kCondition:     kubeBlockStorageSpecInSyncWithCMP,
		aCondition:     cmpBlockStorageIsActive,
		kAction:        r.kubeSetActiveAndSetID,
		requeue:        NoRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 6c. ShouldBeUpdated — spec changed and CMP is ready
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:           "ShouldBeUpdated",
		kCondition:     kubeBlockStorageShouldUpdate,
		aCondition:     cmpBlockStorageIsFinal,
		kAction:        r.kubeMarkToUpdate,
		requeue:        ShortRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 6d. ShouldBeUpdatedOnCMP — send update to CMP
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:              "ShouldBeUpdatedOnCMP",
		kCondition:        kubeShouldBeUpdatedOnCMP[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		aCondition:        cmpBlockStorageIsFinal,
		aAction:           r.cmpUpdate,
		kActionOnASuccess: r.kubeMarkUpdating,
		kActionOnAError:   kubeSetErrorMessageOnCMPError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse](r.Client),
		requeue:           ShortRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError:    SmartRequeueOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 6e. WaitingUpdateOnCMP — CMP is still processing the update
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:           "WaitingUpdateOnCMP",
		kCondition:     kubeWaitingUpdateOnCMP[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		aCondition:     cmpBlockStorageIsTransitory,
		requeue:        LongRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 6f. UpdateConfirmedOnCMP — CMP has settled; advance to Synchronized
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:           "UpdateConfirmedOnCMP",
		kCondition:     kubeWaitingUpdateOnCMP[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		aCondition:     cmpBlockStorageIsFinal,
		kAction:        r.kubeMarkUpdatingDone,
		requeue:        ShortRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 6g. UpdateAccomplished — transition back to Active and stamp generation
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:           "UpdateAccomplished",
		kCondition:     kubeUpdateAccomplished[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		aCondition:     cmpBlockStorageIsActive,
		kAction:        r.kubeSetActiveAndSetID,
		requeue:        NoRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 7. ShouldBeCreated
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:           "ShouldBeCreated",
		kCondition:     kubeIsFirstReconciliation[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		aCondition:     cmpBlockStorageNotExists,
		kAction:        r.kubeMarkToCreate,
		requeue:        ShortRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 8. ShouldBeCreatedInCMP
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:              "ShouldBeCreatedInCMP",
		kCondition:        kubeShouldBeCreatedOnCMP[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		aCondition:        cmpBlockStorageNotExists,
		aAction:           r.cmpCreate,
		kActionOnASuccess: r.kubeMarkCreating,
		kActionOnAError:   kubeSetErrorMessageOnCMPError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse](r.Client),
		requeue:           ShortRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError:    SmartRequeueOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 9. WaitingCreationInCMP
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:           "WaitingCreationInCMP",
		kCondition:     kubeWaitingCreationInCMP[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		aCondition:     cmpBlockStorageNotExistsOrTransitory,
		requeue:        LongRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 10. CreationConfirmedOnCMP
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:           "CreationConfirmedOnCMP",
		kCondition:     kubeWaitingCreationInCMP[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		aCondition:     cmpBlockStorageIsActive,
		kAction:        r.kubeMarkCreatingDone,
		requeue:        ShortRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 11. CreationAccomplished
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:           "CreationAccomplished",
		kCondition:     kubeIsCreatedOnCMP[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		aCondition:     cmpBlockStorageIsActive,
		kAction:        r.kubeSetActiveAndSetID,
		requeue:        NoRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 12. IsInError
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:           "IsInError",
		kCondition:     AlwaysTrue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		aCondition:     cmpBlockStorageIsFailed,
		kAction:        r.kubeSetFailed,
		requeue:        NoRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	return ts
}

// Resource-specific condition functions

func kubeBlockStorageHasDeniedChanges(kubeBS *v1alpha1.BlockStorage, cmpBS *arubatypes.BlockStorageResponse) bool {
	if !kubeBS.DeletionTimestamp.IsZero() {
		return false
	}
	if cmpBS == nil {
		return false
	}
	return checkBlockStorageDeniedChanges(kubeBS, cmpBS) != nil
}

func kubeBlockStorageSpecInSyncWithCMP(kubeBS *v1alpha1.BlockStorage, cmpBS *arubatypes.BlockStorageResponse) bool {
	return kubeActiveAndGenerationChanged(kubeBS, cmpBS) &&
		checkBlockStorageDeniedChanges(kubeBS, cmpBS) == nil &&
		!kubeBlockStorageNeedsUpdate(kubeBS, cmpBS)
}

func kubeBlockStorageShouldUpdate(kubeBS *v1alpha1.BlockStorage, cmpBS *arubatypes.BlockStorageResponse) bool {
	return kubeActiveAndGenerationChanged(kubeBS, cmpBS) &&
		checkBlockStorageDeniedChanges(kubeBS, cmpBS) == nil &&
		kubeBlockStorageNeedsUpdate(kubeBS, cmpBS)
}

func cmpBlockStorageNotExists(_ *v1alpha1.BlockStorage, cmpBS *arubatypes.BlockStorageResponse) bool {
	return cmpBS == nil
}

func cmpBlockStorageIsFinal(_ *v1alpha1.BlockStorage, cmpBS *arubatypes.BlockStorageResponse) bool {
	if cmpBS == nil || cmpBS.Status.State == nil {
		return false
	}
	return AssesCSPResourceStateNature(&cmpBS.Status) == CSPResourceStateNatureFinal
}

func cmpBlockStorageIsTransitory(_ *v1alpha1.BlockStorage, cmpBS *arubatypes.BlockStorageResponse) bool {
	if cmpBS == nil || cmpBS.Status.State == nil {
		return false
	}
	return AssesCSPResourceStateNature(&cmpBS.Status) == CSPResourceStateNatureTransitory
}

func cmpBlockStorageNotExistsOrTransitory(_ *v1alpha1.BlockStorage, cmpBS *arubatypes.BlockStorageResponse) bool {
	if cmpBS == nil {
		return true
	}
	if cmpBS.Status.State == nil {
		return false
	}
	return AssesCSPResourceStateNature(&cmpBS.Status) == CSPResourceStateNatureTransitory
}

func cmpBlockStorageIsActive(_ *v1alpha1.BlockStorage, cmpBS *arubatypes.BlockStorageResponse) bool {
	return cmpBS != nil && cmpBS.Status.State != nil &&
		(*cmpBS.Status.State == CSPResourceStateActive ||
			*cmpBS.Status.State == CSPResourceStateNotUsed ||
			*cmpBS.Status.State == CSPResourceStateInUse ||
			*cmpBS.Status.State == CSPResourceStateUsed)
}

func cmpBlockStorageIsFailed(_ *v1alpha1.BlockStorage, cmpBS *arubatypes.BlockStorageResponse) bool {
	return cmpBS != nil && cmpBS.Status.State != nil && *cmpBS.Status.State == CSPResourceStateFailed
}

// Kube action methods

func (r *BlockStorageReconciler) kubeSetPhaseAndCondition(ctx context.Context, kubeBS *v1alpha1.BlockStorage, phase v1alpha1.ResourcePhase, reason string, actionErr error) error {
	return setPhaseAndCondition(r.Client, ctx, kubeBS, phase, reason, actionErr, func(bs *v1alpha1.BlockStorage) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && bs.Status.ProjectID == "" {
			bs.Status.ProjectID = prjID
		}
	})
}

func (r *BlockStorageReconciler) kubeMarkToDelete(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeBS, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *BlockStorageReconciler) kubeMarkDeleting(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeBS, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *BlockStorageReconciler) kubeMarkDeletingDone(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeBS, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *BlockStorageReconciler) kubeMarkDeleted(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeBS, v1alpha1.ResourcePhaseDeleted, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *BlockStorageReconciler) kubeMarkToUpdate(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeBS, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *BlockStorageReconciler) kubeMarkUpdating(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeBS, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *BlockStorageReconciler) kubeMarkUpdatingDone(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeBS, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *BlockStorageReconciler) kubeMarkToCreate(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeBS, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *BlockStorageReconciler) kubeMarkCreating(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeBS, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *BlockStorageReconciler) kubeMarkCreatingDone(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeBS, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *BlockStorageReconciler) kubeSetActiveAndSetID(ctx context.Context, kubeBS *v1alpha1.BlockStorage, cmpBS *arubatypes.BlockStorageResponse) error {
	cmpID := ""
	if cmpBS != nil && cmpBS.Metadata.ID != nil {
		cmpID = *cmpBS.Metadata.ID
	}
	return setActiveAndSetID(r.Client, ctx, kubeBS, cmpID, nil, func(bs *v1alpha1.BlockStorage) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && bs.Status.ProjectID != "" {
			bs.Status.ProjectID = prjID
		}
	})
}

func (r *BlockStorageReconciler) kubeSetFailedOnTimeout(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) error {
	return setFailedOnTimeout(r.Client, ctx, kubeBS, func(bs *v1alpha1.BlockStorage) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && bs.Status.ProjectID == "" {
			bs.Status.ProjectID = prjID
		}
	})
}

func (r *BlockStorageReconciler) kubeSetFailed(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeBS, v1alpha1.ResourcePhaseFailed, v1alpha1.ConditionReasonSynchronized, nil)
}

// CMP action methods

func (r *BlockStorageReconciler) cmpDelete(ctx context.Context, _ *v1alpha1.BlockStorage, cmpBS *arubatypes.BlockStorageResponse) error {
	prjID := ctx.Value(projectIDKey).(string)
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)
	cmpBSResp, err := arubaClient.FromStorage().Volumes().Delete(ctx, prjID, *cmpBS.Metadata.ID, nil)
	if err != nil {
		return cmpTransportError("delete", *cmpBS.Metadata.Name, err)
	}
	return cmpCheckResponse("delete", *cmpBS.Metadata.Name, cmpBSResp,
		http.StatusOK, http.StatusAccepted, http.StatusNoContent, http.StatusNotFound)
}

func (r *BlockStorageReconciler) cmpUpdate(ctx context.Context, kubeBS *v1alpha1.BlockStorage, cmpBS *arubatypes.BlockStorageResponse) error {
	prjID := ctx.Value(projectIDKey).(string)
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)

	// Guard: should have been caught by HasDeniedChanges, but double-check.
	if err := checkBlockStorageDeniedChanges(kubeBS, cmpBS); err != nil {
		return err
	}

	request := buildBlockStorageUpdateRequest(kubeBS, cmpBS)

	cmpBSResp, err := arubaClient.FromStorage().Volumes().Update(ctx, prjID, *cmpBS.Metadata.ID, *request, nil)
	if err != nil {
		return cmpTransportError("update", kubeBS.Name, err)
	}
	return cmpCheckResponse("update", kubeBS.Name, cmpBSResp,
		http.StatusOK, http.StatusAccepted, http.StatusNoContent)
}

func (r *BlockStorageReconciler) cmpCreate(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) error {
	prjID := ctx.Value(projectIDKey).(string)
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)

	cmpBSResp, err := arubaClient.FromStorage().Volumes().Create(ctx, prjID, *cmpBlockStorageRequestFromKube(kubeBS), nil)
	if err != nil {
		return cmpTransportError("create", kubeBS.Name, err)
	}
	return cmpCheckResponse("create", kubeBS.Name, cmpBSResp,
		http.StatusOK, http.StatusCreated, http.StatusAccepted)
}

// Helper functions

func checkBlockStorageDeniedChanges(kubeBS *v1alpha1.BlockStorage, cmpBS *arubatypes.BlockStorageResponse) error {
	if cmpBS == nil {
		return nil
	}

	var errs []error

	if cmpBS.Properties.SizeGB > int(kubeBS.Spec.SizeGb) {
		errs = append(errs, errors.New("decreasing the 'size' is not allowed"))
	}
	if cmpBS.Properties.Bootable != nil && kubeBS.Spec.Bootable != *cmpBS.Properties.Bootable {
		errs = append(errs, errors.New("change the 'bootable' is not allowed"))
	}
	if cmpBS.Properties.Image != nil && kubeBS.Spec.Image != *cmpBS.Properties.Image {
		errs = append(errs, errors.New("change the 'image' is not allowed"))
	}
	if kubeBS.Spec.Type != string(cmpBS.Properties.Type) {
		errs = append(errs, errors.New("change the 'type' is not allowed"))
	}
	locationValue := ""
	if cmpBS.Metadata.LocationResponse != nil {
		locationValue = cmpBS.Metadata.LocationResponse.Value
	}
	if kubeBS.Spec.Location.Value != locationValue {
		errs = append(errs, errors.New("change the 'location' is not allowed"))
	}

	if len(errs) > 0 {
		return fmt.Errorf("%w: %w", ErrNotAllowedChanges, errors.Join(errs...))
	}
	return nil
}

func kubeBlockStorageNeedsUpdate(kubeBS *v1alpha1.BlockStorage, cmpBS *arubatypes.BlockStorageResponse) bool {
	if cmpBS == nil {
		return false
	}
	return kubeBS.Spec.BillingPeriod != cmpBS.Properties.BillingPeriod ||
		kubeBS.Spec.DataCenter != cmpBS.Properties.Zone ||
		kubeBS.Spec.SizeGb != int32(cmpBS.Properties.SizeGB) ||
		!tagsAreEqual(kubeBS.Spec.Tags, cmpBS.Metadata.Tags)
}

func buildBlockStorageUpdateRequest(kubeBS *v1alpha1.BlockStorage, cmpBS *arubatypes.BlockStorageResponse) *arubatypes.BlockStorageRequest {
	request := cmpBlockStorageRequestFromCMP(cmpBS)
	if request == nil {
		return nil
	}
	request.Properties.BillingPeriod = kubeBS.Spec.BillingPeriod
	zone := kubeBS.Spec.DataCenter
	request.Properties.Zone = &zone
	request.Properties.SizeGB = int(kubeBS.Spec.SizeGb)
	tags := make([]string, len(kubeBS.Spec.Tags))
	copy(tags, kubeBS.Spec.Tags)
	request.Metadata.Tags = tags
	return request
}

func cmpBlockStorageRequestFromCMP(cmpBS *arubatypes.BlockStorageResponse) *arubatypes.BlockStorageRequest {
	if cmpBS == nil {
		return nil
	}
	name := ""
	if cmpBS.Metadata.Name != nil {
		name = *cmpBS.Metadata.Name
	}
	tags := make([]string, len(cmpBS.Metadata.Tags))
	copy(tags, cmpBS.Metadata.Tags)
	location := arubatypes.LocationRequest{Value: ""}
	if cmpBS.Metadata.LocationResponse != nil {
		location.Value = cmpBS.Metadata.LocationResponse.Value
	}
	zone := cmpBS.Properties.Zone
	return &arubatypes.BlockStorageRequest{
		Metadata: arubatypes.RegionalResourceMetadataRequest{
			ResourceMetadataRequest: arubatypes.ResourceMetadataRequest{
				Name: name,
				Tags: tags,
			},
			Location: location,
		},
		Properties: arubatypes.BlockStoragePropertiesRequest{
			SizeGB:        cmpBS.Properties.SizeGB,
			BillingPeriod: cmpBS.Properties.BillingPeriod,
			Zone:          &zone,
			Type:          cmpBS.Properties.Type,
			Bootable:      cmpBS.Properties.Bootable,
			Image:         cmpBS.Properties.Image,
		},
	}
}

func cmpBlockStorageRequestFromKube(kubeBS *v1alpha1.BlockStorage) *arubatypes.BlockStorageRequest {
	return &arubatypes.BlockStorageRequest{
		Metadata: arubatypes.RegionalResourceMetadataRequest{
			ResourceMetadataRequest: arubatypes.ResourceMetadataRequest{
				Name: kubeBS.Name,
				Tags: kubeBS.Spec.Tags,
			},
			Location: arubatypes.LocationRequest(kubeBS.Spec.Location),
		},
		Properties: arubatypes.BlockStoragePropertiesRequest{
			SizeGB:        int(kubeBS.Spec.SizeGb),
			BillingPeriod: kubeBS.Spec.BillingPeriod,
			Zone:          &kubeBS.Spec.DataCenter,
			Bootable:      &kubeBS.Spec.Bootable,
			Image:         &kubeBS.Spec.Image,
			Type:          arubatypes.BlockStorageType(kubeBS.Spec.Type),
		},
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *BlockStorageReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.BlockStorage{}).
		Named("blockstorage").
		Complete(r)
}
