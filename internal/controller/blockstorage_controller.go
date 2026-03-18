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
	"slices"
	"strings"

	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	arubatypes "github.com/Arubacloud/sdk-go/pkg/types"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
)

const (
	blockStorageFinalizerName = "blockstorage.arubacloud.com/finalizer"
)

var (
	errBlockStorageNotFound = errors.New("blockstorage not found")
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

	if kubeBlockStorage.Spec.ProjectReference.Name == "" {
		return ctrl.Result{}, fmt.Errorf("project reference is not valid")
	}

	blockStorageName, projectName := kubeBlockStorage.Name, kubeBlockStorage.Spec.ProjectReference.Name
	bsFilter := fmt.Sprintf(`name:eq("%s")`, blockStorageName)
	prjFilter := fmt.Sprintf(`name:eq("%s")`, projectName)

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
		return ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
	}

	prjID := *(cmpProjectList.Data.Values[0].Metadata.ID)

	if kubeBlockStorage.Status.ProjectID != "" && kubeBlockStorage.Status.ProjectID != prjID {
		return ctrl.Result{}, fmt.Errorf(
			"inconsistent project id in blockstorage: blockstorage_name: '%s', blockstorage_project_id: '%s', project_name: '%s', project_id: '%s'",
			blockStorageName, kubeBlockStorage.Status.ProjectID, projectName, prjID,
		)
	}

	cmpBlockStorageList, err := r.ArubaClient.FromStorage().Volumes().List(ctx, prjID, &arubatypes.RequestParameters{Filter: &bsFilter})
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

	ctx = context.WithValue(ctx, projectIDKey, prjID)

	return r.ts.Run(ctx, kubeBlockStorage, cmpBlockStorage)
}

//
// Transition Set Functions
//

// Kubernetes BlockStorage Conditions

func kubeBlockStorageShouldDelete(kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) bool {
	return !kubeBS.DeletionTimestamp.IsZero() &&
		kubeBS.Status.Phase != v1alpha1.ResourcePhaseDeleting &&
		kubeBS.Status.Phase != v1alpha1.ResourcePhaseDeleted
}

func kubeBlockStorageIsDeleting(kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) bool {
	return !kubeBS.DeletionTimestamp.IsZero() &&
		kubeBS.Status.Phase == v1alpha1.ResourcePhaseDeleting
}

func kubeBlockStorageIsDeleted(kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) bool {
	return !kubeBS.DeletionTimestamp.IsZero() &&
		kubeBS.Status.Phase == v1alpha1.ResourcePhaseDeleted
}

func kubeBlockStorageNotExists(kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) bool {
	return kubeBS.DeletionTimestamp.IsZero() &&
		kubeBS.Status.Phase == "" &&
		kubeBS.Status.ResourceID == ""
}

func kubeBlockStorageIsCreating(kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) bool {
	return kubeBS.DeletionTimestamp.IsZero() &&
		kubeBS.Status.Phase == v1alpha1.ResourcePhaseCreating &&
		kubeBS.Status.ResourceID == ""
}

func kubeBlockStorageWasRemoved(kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) bool {
	return kubeBS.DeletionTimestamp.IsZero() &&
		kubeBS.Status.Phase != "" &&
		kubeBS.Status.ResourceID != ""
}

func kubeBlockStorageIsCreatingInCMP(kubeBS *v1alpha1.BlockStorage, cmpBS *arubatypes.BlockStorageResponse) bool {
	return kubeBS.DeletionTimestamp.IsZero() &&
		kubeBS.Status.Phase != "" &&
		(kubeBS.Status.ResourceID == "" || (cmpBS != nil && kubeBS.Status.ResourceID == *cmpBS.Metadata.ID))
}

func kubeBlockStorageIsActive(kubeBS *v1alpha1.BlockStorage, cmpBS *arubatypes.BlockStorageResponse) bool {
	return kubeBS.DeletionTimestamp.IsZero() &&
		kubeBS.Status.Phase != "" &&
		kubeBS.Status.Phase != v1alpha1.ResourcePhaseUpdating &&
		(kubeBS.Status.ResourceID == "" || (cmpBS != nil && kubeBS.Status.ResourceID == *cmpBS.Metadata.ID))
}

func kubeBlockStorageHasDeniedChanges(kubeBS *v1alpha1.BlockStorage, cmpBS *arubatypes.BlockStorageResponse) bool {
	if !kubeBS.DeletionTimestamp.IsZero() {
		return false
	}
	if cmpBS == nil {
		return false
	}
	return checkBlockStorageDeniedChanges(kubeBS, cmpBS) != nil
}

func kubeBlockStorageShouldUpdate(kubeBS *v1alpha1.BlockStorage, cmpBS *arubatypes.BlockStorageResponse) bool {
	if !kubeBS.DeletionTimestamp.IsZero() || kubeBS.Status.Phase == v1alpha1.ResourcePhaseUpdating {
		return false
	}
	if cmpBS == nil {
		return false
	}
	return checkBlockStorageDeniedChanges(kubeBS, cmpBS) == nil && kubeBlockStorageNeedsUpdate(kubeBS, cmpBS)
}

func kubeBlockStorageIsUpdating(kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) bool {
	return kubeBS.DeletionTimestamp.IsZero() && kubeBS.Status.Phase == v1alpha1.ResourcePhaseUpdating
}

func kubeBlockStorageHasUpdated(kubeBS *v1alpha1.BlockStorage, cmpBS *arubatypes.BlockStorageResponse) bool {
	if !kubeBS.DeletionTimestamp.IsZero() || kubeBS.Status.Phase != v1alpha1.ResourcePhaseUpdating {
		return false
	}
	if cmpBS == nil {
		return false
	}
	return checkBlockStorageDeniedChanges(kubeBS, cmpBS) == nil && !kubeBlockStorageNeedsUpdate(kubeBS, cmpBS)
}

// Aruba CMP BlockStorage Conditions

func cmpBlockStorageIsFinal(_ *v1alpha1.BlockStorage, cmpBS *arubatypes.BlockStorageResponse) bool {
	if cmpBS == nil || cmpBS.Status.State == nil {
		return false
	}
	if *cmpBS.Status.State == CSPResourceStateDeleting || *cmpBS.Status.State == CSPResourceStateDeleted {
		return false
	}
	return AssesCSPResourceStateNature(&cmpBS.Status) == CSPResourceStateNatureFinal
}

func cmpBlockStorageIsDeleting(_ *v1alpha1.BlockStorage, cmpBS *arubatypes.BlockStorageResponse) bool {
	return cmpBS != nil && cmpBS.Status.State != nil && *cmpBS.Status.State == CSPResourceStateDeleting
}

func cmpBlockStorageNotExists(_ *v1alpha1.BlockStorage, cmpBS *arubatypes.BlockStorageResponse) bool {
	return cmpBS == nil
}

func cmpBlockStorageIsCreating(_ *v1alpha1.BlockStorage, cmpBS *arubatypes.BlockStorageResponse) bool {
	return cmpBS != nil && cmpBS.Status.State != nil &&
		(*cmpBS.Status.State == CSPResourceStateCreating ||
			*cmpBS.Status.State == CSPResourceStateInCreation ||
			*cmpBS.Status.State == CSPResourceStateProvisioning)
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

func cmpBlockStorageIsUpdating(_ *v1alpha1.BlockStorage, cmpBS *arubatypes.BlockStorageResponse) bool {
	return cmpBS != nil && cmpBS.Status.State != nil && *cmpBS.Status.State == CSPResourceStateUpdating
}

func cmpBlockStorageIsFinalForUpdate(_ *v1alpha1.BlockStorage, cmpBS *arubatypes.BlockStorageResponse) bool {
	return cmpBS != nil && AssesCSPResourceStateNature(&cmpBS.Status) == CSPResourceStateNatureFinal
}

// Kubernetes Actions

func (r *BlockStorageReconciler) kubeSetState(ctx context.Context, kubeBS *v1alpha1.BlockStorage, state v1alpha1.ResourcePhase) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		kubeBSCopy := kubeBS.DeepCopy()
		if err := r.Get(ctx, client.ObjectKeyFromObject(kubeBS), kubeBSCopy); err != nil {
			return err
		}

		kubeBSPatch := kubeBSCopy.DeepCopy()
		kubeBSPatch.Status.Phase = state

		if prjID, ok := ctx.Value(projectIDKey).(string); ok && kubeBSPatch.Status.ProjectID != "" {
			kubeBSPatch.Status.ProjectID = prjID
		}

		if err := r.Status().Patch(ctx, kubeBSPatch, client.MergeFrom(kubeBSCopy)); err != nil {
			return fmt.Errorf("failed to update blockstorage '%s/%s' state to '%v': %w", kubeBSPatch.Namespace, kubeBSPatch.Name, state, err)
		}

		return nil
	})
}

func (r *BlockStorageReconciler) kubeSetDeleting(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) error {
	return r.kubeSetState(ctx, kubeBS, v1alpha1.ResourcePhaseDeleting)
}

func (r *BlockStorageReconciler) kubeSetDeleted(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) error {
	return r.kubeSetState(ctx, kubeBS, v1alpha1.ResourcePhaseDeleted)
}

func (r *BlockStorageReconciler) kubeSetCreating(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) error {
	return r.kubeSetState(ctx, kubeBS, v1alpha1.ResourcePhaseCreating)
}

func (r *BlockStorageReconciler) kubeSetUpdating(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) error {
	return r.kubeSetState(ctx, kubeBS, v1alpha1.ResourcePhaseUpdating)
}

func (r *BlockStorageReconciler) kubeSetCreatingAndUnsetID(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		kubeBSCopy := kubeBS.DeepCopy()
		if err := r.Get(ctx, client.ObjectKeyFromObject(kubeBS), kubeBSCopy); err != nil {
			return err
		}

		kubeBSPatch := kubeBSCopy.DeepCopy()
		kubeBSPatch.Status.Phase = v1alpha1.ResourcePhaseCreating
		kubeBSPatch.Status.ResourceID = ""

		if prjID, ok := ctx.Value(projectIDKey).(string); ok && kubeBSPatch.Status.ProjectID != "" {
			kubeBSPatch.Status.ProjectID = prjID
		}

		if err := r.Status().Patch(ctx, kubeBSPatch, client.MergeFrom(kubeBSCopy)); err != nil {
			return fmt.Errorf("failed to update blockstorage '%s/%s' state to '%v': %w", kubeBSPatch.Namespace, kubeBSPatch.Name, v1alpha1.ResourcePhaseCreating, err)
		}

		return nil
	})
}

func (r *BlockStorageReconciler) kubeSetActiveAndSetID(ctx context.Context, kubeBS *v1alpha1.BlockStorage, cmpBS *arubatypes.BlockStorageResponse) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		kubeBSCopy := kubeBS.DeepCopy()
		if err := r.Get(ctx, client.ObjectKeyFromObject(kubeBS), kubeBSCopy); err != nil {
			return err
		}

		kubeBSPatch := kubeBSCopy.DeepCopy()
		kubeBSPatch.Status.Phase = v1alpha1.ResourcePhaseActive

		if kubeBSPatch.Status.ResourceID != "" && cmpBS != nil && cmpBS.Metadata.ID != nil {
			kubeBSPatch.Status.ResourceID = *cmpBS.Metadata.ID
		}

		if prjID, ok := ctx.Value(projectIDKey).(string); ok && kubeBSPatch.Status.ProjectID != "" {
			kubeBSPatch.Status.ProjectID = prjID
		}

		if err := r.Status().Patch(ctx, kubeBSPatch, client.MergeFrom(kubeBSCopy)); err != nil {
			return fmt.Errorf("failed to update blockstorage '%s/%s' state to '%v': %w", kubeBSPatch.Namespace, kubeBSPatch.Name, v1alpha1.ResourcePhaseActive, err)
		}

		return nil
	})
}

func (r *BlockStorageReconciler) kubeSetCreatingAndSetID(ctx context.Context, kubeBS *v1alpha1.BlockStorage, cmpBS *arubatypes.BlockStorageResponse) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		kubeBSCopy := kubeBS.DeepCopy()
		if err := r.Get(ctx, client.ObjectKeyFromObject(kubeBS), kubeBSCopy); err != nil {
			return err
		}

		kubeBSPatch := kubeBSCopy.DeepCopy()
		kubeBSPatch.Status.Phase = v1alpha1.ResourcePhaseCreating

		if kubeBSPatch.Status.ResourceID != "" && cmpBS != nil && cmpBS.Metadata.ID != nil {
			kubeBSPatch.Status.ResourceID = *cmpBS.Metadata.ID
		}

		if prjID, ok := ctx.Value(projectIDKey).(string); ok && kubeBSPatch.Status.ProjectID != "" {
			kubeBSPatch.Status.ProjectID = prjID
		}

		if err := r.Status().Patch(ctx, kubeBSPatch, client.MergeFrom(kubeBSCopy)); err != nil {
			return fmt.Errorf("failed to update blockstorage '%s/%s' state to '%v': %w", kubeBSPatch.Namespace, kubeBSPatch.Name, v1alpha1.ResourcePhaseCreating, err)
		}
		return nil
	})
}

func (r *BlockStorageReconciler) kubeSetFailed(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) error {
	return r.kubeSetState(ctx, kubeBS, v1alpha1.ResourcePhaseFailed)
}

func (r *BlockStorageReconciler) kubeSetActive(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) error {
	return r.kubeSetState(ctx, kubeBS, v1alpha1.ResourcePhaseActive)
}

func (r *BlockStorageReconciler) kubeSetFailedOn400(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse, err error) error {
	if strings.Contains(err.Error(), "status_code: 400") {
		return r.kubeSetState(ctx, kubeBS, v1alpha1.ResourcePhaseFailed)
	}
	return nil
}

// Aruba CMP Actions

func (r *BlockStorageReconciler) cmpDelete(ctx context.Context, _ *v1alpha1.BlockStorage, cmpBS *arubatypes.BlockStorageResponse) error {
	prjID := ctx.Value(projectIDKey).(string)
	bsResp, err := r.ArubaClient.FromStorage().Volumes().Delete(ctx, prjID, *cmpBS.Metadata.ID, nil)
	if err != nil {
		return fmt.Errorf("failed to delete blockstorage '%s' in Aruba CMP: error: '%w'", *cmpBS.Metadata.Name, err)
	}

	switch bsResp.StatusCode {
	case http.StatusOK, http.StatusAccepted, http.StatusNoContent, http.StatusNotFound:
		// Do nothing, we can consider the delete request as successful
	case http.StatusBadRequest:
		return fmt.Errorf("failed to delete blockstorage '%s' in Aruba CMP: status_code: %d, error: 'semantic or precondition error'", *cmpBS.Metadata.Name, bsResp.StatusCode)
	default:
		return fmt.Errorf("failed to delete blockstorage '%s' in Aruba CMP: status_code: %d, error: 'internal error'", *cmpBS.Metadata.Name, bsResp.StatusCode)
	}
	return nil
}

func (r *BlockStorageReconciler) cmpCreate(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) error {
	prjID := ctx.Value(projectIDKey).(string)
	bsCreateResp, err := r.ArubaClient.FromStorage().Volumes().Create(ctx, prjID, *cmpBlockStorageRequestFromKube(kubeBS), nil)
	if err != nil {
		return fmt.Errorf("failed to create blockstorage '%s' in Aruba CMP: error: '%w'", kubeBS.Name, err)
	}

	switch bsCreateResp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusAccepted:
		// Success
	case http.StatusBadRequest:
		return fmt.Errorf("status_code: 400, failed to create blockstorage '%s' in Aruba CMP: semantic or precondition error", kubeBS.Name)
	default:
		return fmt.Errorf("status_code: %d, failed to create blockstorage '%s' in Aruba CMP: internal error", bsCreateResp.StatusCode, kubeBS.Name)
	}
	return nil
}

func (r *BlockStorageReconciler) cmpUpdate(ctx context.Context, kubeBS *v1alpha1.BlockStorage, cmpBS *arubatypes.BlockStorageResponse) error {
	prjID := ctx.Value(projectIDKey).(string)

	if err := checkBlockStorageDeniedChanges(kubeBS, cmpBS); err != nil {
		return err // Should be caught by ResourceHasDeniedChanges beforehand
	}

	request := buildBlockStorageUpdateRequest(kubeBS, cmpBS)

	updateResp, err := r.ArubaClient.FromStorage().Volumes().Update(ctx, prjID, *cmpBS.Metadata.ID, *request, nil)
	if err != nil {
		return fmt.Errorf("failed to update blockstorage '%s' in Aruba CMP: %w", kubeBS.Name, err)
	}

	if updateResp != nil && updateResp.IsError() {
		errDetail := ""
		if updateResp.Error != nil {
			var status int32
			title, detail := "", ""
			if updateResp.Error.Status != nil {
				status = *updateResp.Error.Status
			}
			if updateResp.Error.Title != nil {
				title = *updateResp.Error.Title
			}
			if updateResp.Error.Detail != nil {
				detail = *updateResp.Error.Detail
			}
			errDetail = fmt.Sprintf("status_code: '%d', title: '%s', detail: '%s'", status, title, detail)
		}
		return fmt.Errorf("failed to update blockstorage '%s' in Aruba CMP: %s", kubeBS.Name, errDetail)
	}

	return nil
}

// Transition Set Builder

func (r *BlockStorageReconciler) newTransitionSet() *TransitionSet[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse] {
	ts := &TransitionSet[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		defaultKAction:         NoAction[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		defaultAAction:         NoAction[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		defaultKActionOnAError: NoActionOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		defaultRequeue:         LongRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		defaultRequeueOnError:  LongRequeueAndIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	}

	// 1. BlockStorageShouldBeDeleted
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:            "BlockStorageShouldBeDeleted",
		kCondition:      kubeBlockStorageShouldDelete,
		aCondition:      cmpBlockStorageIsFinal,
		kAction:         r.kubeSetDeleting,
		aAction:         r.cmpDelete,
		kActionOnAError: NoActionOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeue:         LongRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError:  LongRequeueAndIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 2. BlockStorageDeletingInProgress
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:            "BlockStorageDeletingInProgress",
		kCondition:      kubeBlockStorageIsDeleting,
		aCondition:      cmpBlockStorageIsDeleting,
		kAction:         NoAction[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		aAction:         NoAction[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		kActionOnAError: NoActionOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeue:         LongRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError:  NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 3. BlockStorageDeletionAccomplishedInCMP
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:            "BlockStorageDeletionAccomplishedInCMP",
		kCondition:      kubeBlockStorageIsDeleting,
		aCondition:      cmpBlockStorageNotExists,
		kAction:         r.kubeSetDeleted,
		aAction:         NoAction[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		kActionOnAError: NoActionOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeue:         LongRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError:  NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 3.bis BlockStorageDeletionAccomplished
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:            "BlockStorageDeletionAccomplished",
		kCondition:      kubeBlockStorageIsDeleted,
		aCondition:      cmpBlockStorageNotExists,
		kAction:         NoAction[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		aAction:         NoAction[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		kActionOnAError: NoActionOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeue:         NoRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError:  NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 4. BlockStorageDoesNotExistsInBoth
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:            "BlockStorageDoesNotExistsInBoth",
		kCondition:      kubeBlockStorageNotExists,
		aCondition:      cmpBlockStorageNotExists,
		kAction:         r.kubeSetCreating,
		aAction:         r.cmpCreate,
		kActionOnAError: r.kubeSetFailedOn400,
		requeue:         LongRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError:  LongRequeueAndIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 5. BlockStorageDoesNotExistsInCMP
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:            "BlockStorageDoesNotExistsInCMP",
		kCondition:      kubeBlockStorageIsCreating,
		aCondition:      cmpBlockStorageNotExists,
		kAction:         NoAction[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		aAction:         r.cmpCreate,
		kActionOnAError: NoActionOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeue:         LongRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError:  LongRequeueAndIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 6. BlockStorageWasRemovedFromCMP
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:            "BlockStorageWasRemovedFromCMP",
		kCondition:      kubeBlockStorageWasRemoved,
		aCondition:      cmpBlockStorageNotExists,
		kAction:         r.kubeSetCreatingAndUnsetID,
		aAction:         r.cmpCreate,
		kActionOnAError: r.kubeSetFailedOn400,
		requeue:         LongRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError:  LongRequeueAndIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 7. BlockStorageCreationInProgress
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:            "BlockStorageCreationInProgress",
		kCondition:      kubeBlockStorageIsCreatingInCMP,
		aCondition:      cmpBlockStorageIsCreating,
		kAction:         r.kubeSetCreatingAndSetID,
		aAction:         NoAction[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		kActionOnAError: NoActionOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeue:         LongRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError:  LongRequeueAndIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 8. BlockStorageIsActive
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:            "BlockStorageIsActive",
		kCondition:      kubeBlockStorageIsActive,
		aCondition:      cmpBlockStorageIsActive,
		kAction:         r.kubeSetActiveAndSetID,
		aAction:         NoAction[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		kActionOnAError: NoActionOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeue:         NoRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError:  LongRequeueAndIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 9. BlockStorageIsInError
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:            "BlockStorageIsInError",
		kCondition:      AlwaysTrue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		aCondition:      cmpBlockStorageIsFailed,
		kAction:         r.kubeSetFailed,
		aAction:         NoAction[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		kActionOnAError: NoActionOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeue:         NoRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError:  NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 9b. BlockStorageHasDeniedChanges (intercept before BlockStorageShouldBeUpdated to surface the error)
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:       "BlockStorageHasDeniedChanges",
		kCondition: kubeBlockStorageHasDeniedChanges,
		aCondition: AlwaysTrue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		kAction: func(ctx context.Context, kubeBS *v1alpha1.BlockStorage, cmpBS *arubatypes.BlockStorageResponse) error {
			return fmt.Errorf("failed to convert and check blockstorage: %w", checkBlockStorageDeniedChanges(kubeBS, cmpBS))
		},
		aAction:         NoAction[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		kActionOnAError: NoActionOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeue:         NoRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse], // Don't requeue if denied changes
		requeueOnError:  LongRequeueAndIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 10. BlockStorageShouldBeUpdated
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:            "BlockStorageShouldBeUpdated",
		kCondition:      kubeBlockStorageShouldUpdate,
		aCondition:      cmpBlockStorageIsFinalForUpdate,
		kAction:         r.kubeSetUpdating,
		aAction:         r.cmpUpdate,
		kActionOnAError: NoActionOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeue:         LongRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError:  LongRequeueAndIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 11. BlockStorageUpdatingInProgress
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:            "BlockStorageUpdatingInProgress",
		kCondition:      kubeBlockStorageIsUpdating,
		aCondition:      cmpBlockStorageIsUpdating,
		kAction:         NoAction[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		aAction:         NoAction[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		kActionOnAError: NoActionOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeue:         LongRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError:  LongRequeueAndIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 12. BlockStorageUpdated
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:            "BlockStorageUpdated",
		kCondition:      kubeBlockStorageHasUpdated,
		aCondition:      cmpBlockStorageIsActive,
		kAction:         r.kubeSetActive,
		aAction:         NoAction[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		kActionOnAError: NoActionOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeue:         NoRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError:  LongRequeueAndIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	return ts
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

func checkBlockStorageDeniedChanges(kubeBS *v1alpha1.BlockStorage, cmpBS *arubatypes.BlockStorageResponse) error {
	if cmpBS == nil {
		return nil
	}

	errs := []error{}

	if kubeBS.Spec.Bootable != *cmpBS.Properties.Bootable {
		errs = append(errs, errors.New("change the 'bootable' is not allowed"))
	}

	if kubeBS.Spec.Image != *cmpBS.Properties.Image {
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
		kubeBS.Spec.Bootable != *cmpBS.Properties.Bootable ||
		kubeBS.Spec.DataCenter != cmpBS.Properties.Zone ||
		kubeBS.Spec.SizeGb != int32(cmpBS.Properties.SizeGB) ||
		!kubeBlockStorageTagsAreEqual(kubeBS, cmpBS.Metadata.Tags)
}

func buildBlockStorageUpdateRequest(kubeBS *v1alpha1.BlockStorage, cmpBS *arubatypes.BlockStorageResponse) *arubatypes.BlockStorageRequest {
	request := cmpBlockStorageRequestFromResponse(cmpBS)
	if request == nil {
		return nil
	}

	request.Properties.BillingPeriod = kubeBS.Spec.BillingPeriod
	bootable := kubeBS.Spec.Bootable
	request.Properties.Bootable = &bootable
	zone := kubeBS.Spec.DataCenter
	request.Properties.Zone = &zone
	request.Properties.SizeGB = int(kubeBS.Spec.SizeGb)

	tags := make([]string, len(kubeBS.Spec.Tags))
	copy(tags, kubeBS.Spec.Tags)
	request.Metadata.Tags = tags

	return request
}

func cmpBlockStorageRequestFromResponse(response *arubatypes.BlockStorageResponse) *arubatypes.BlockStorageRequest {
	if response == nil {
		return nil
	}
	name := ""
	if response.Metadata.Name != nil {
		name = *response.Metadata.Name
	}
	tags := make([]string, len(response.Metadata.Tags))
	copy(tags, response.Metadata.Tags)
	location := arubatypes.LocationRequest{Value: ""}
	if response.Metadata.LocationResponse != nil {
		location.Value = response.Metadata.LocationResponse.Value
	}
	zone := response.Properties.Zone
	return &arubatypes.BlockStorageRequest{
		Metadata: arubatypes.RegionalResourceMetadataRequest{
			ResourceMetadataRequest: arubatypes.ResourceMetadataRequest{
				Name: name,
				Tags: tags,
			},
			Location: location,
		},
		Properties: arubatypes.BlockStoragePropertiesRequest{
			SizeGB:        response.Properties.SizeGB,
			BillingPeriod: response.Properties.BillingPeriod,
			Zone:          &zone,
			Type:          response.Properties.Type,
			Bootable:      response.Properties.Bootable,
			Image:         response.Properties.Image,
		},
	}
}

func kubeBlockStorageTagsAreEqual(kubeBS *v1alpha1.BlockStorage, tags []string) bool {
	if len(kubeBS.Spec.Tags) != len(tags) {
		return false
	}

	kubeTags := make([]string, len(kubeBS.Spec.Tags))
	copy(kubeTags, kubeBS.Spec.Tags)

	cmpTags := make([]string, len(tags))
	copy(cmpTags, tags)

	slices.Sort(kubeTags)
	slices.Sort(cmpTags)

	for i, tag := range kubeTags {
		if tag != cmpTags[i] {
			return false
		}
	}

	return true
}

// SetupWithManager sets up the controller with the Manager.
func (r *BlockStorageReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.BlockStorage{}).
		Named("blockstorage").
		Complete(r)
}
