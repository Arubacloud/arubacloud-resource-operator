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

	var prjID string

	if !kubeBlockStorage.GetDeletionTimestamp().IsZero() && kubeBlockStorage.Status.ProjectID != "" {
		prjID = kubeBlockStorage.Status.ProjectID
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

		prjID = *(cmpProjectList.Data.Values[0].Metadata.ID)
	}

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
	condition := meta.FindStatusCondition(kubeBS.Status.Conditions, string(kubeBS.Status.Phase))

	return !kubeBS.DeletionTimestamp.IsZero() &&
		kubeBS.Status.AssessPhaseNature() == v1alpha1.PhaseNatureFinal &&
		kubeBS.Status.Phase != v1alpha1.ResourcePhaseDeleted &&
		condition != nil &&
		condition.Status == metav1.ConditionTrue &&
		condition.Reason == v1alpha1.ConditionReasonSynchronized
}

func kubeBlockStorageShouldBeDeletedOnCMP(kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) bool {
	condition := meta.FindStatusCondition(kubeBS.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))

	return !kubeBS.DeletionTimestamp.IsZero() &&
		kubeBS.Status.Phase == v1alpha1.ResourcePhaseDeleting &&
		condition != nil &&
		condition.Status == metav1.ConditionTrue &&
		condition.Reason == v1alpha1.ConditionReasonShallSynchronize
}

func kubeBlockStorageWaitingDeletionOnCMP(kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) bool {
	condition := meta.FindStatusCondition(kubeBS.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))

	return !kubeBS.DeletionTimestamp.IsZero() &&
		kubeBS.Status.Phase == v1alpha1.ResourcePhaseDeleting &&
		condition != nil &&
		condition.Status == metav1.ConditionTrue &&
		condition.Reason == v1alpha1.ConditionReasonSynchronizing
}

func kubeBlockStorageDeletionAcomplished(kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) bool {
	condition := meta.FindStatusCondition(kubeBS.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))

	return !kubeBS.DeletionTimestamp.IsZero() &&
		kubeBS.Status.Phase == v1alpha1.ResourcePhaseDeleting &&
		condition != nil &&
		condition.Status == metav1.ConditionTrue &&
		condition.Reason == v1alpha1.ConditionReasonSynchronized
}

func kubeBlockStorageIsFirstReconciliation(kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) bool {
	return kubeBS.DeletionTimestamp.IsZero() &&
		kubeBS.Status.ResourceID == "" &&
		kubeBS.Status.Phase == "" &&
		len(kubeBS.Status.Conditions) == 0
}

func kubeBlockStorageShouldBeCreatedOnCMP(kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) bool {
	condition := meta.FindStatusCondition(kubeBS.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))

	return kubeBS.DeletionTimestamp.IsZero() &&
		kubeBS.Status.ResourceID == "" &&
		kubeBS.Status.Phase == v1alpha1.ResourcePhaseCreating &&
		condition != nil &&
		condition.Status == metav1.ConditionTrue &&
		condition.Reason == v1alpha1.ConditionReasonShallSynchronize
}

func kubeBlockStorageWaitingCreationInCMP(kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) bool {
	condition := meta.FindStatusCondition(kubeBS.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))

	return kubeBS.DeletionTimestamp.IsZero() &&
		kubeBS.Status.ResourceID == "" &&
		kubeBS.Status.Phase == v1alpha1.ResourcePhaseCreating &&
		condition != nil &&
		condition.Status == metav1.ConditionTrue &&
		condition.Reason == v1alpha1.ConditionReasonSynchronizing
}

func kubeBlockStorageIsCreatedOnCMP(kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) bool {
	condition := meta.FindStatusCondition(kubeBS.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))

	return kubeBS.DeletionTimestamp.IsZero() &&
		kubeBS.Status.ResourceID == "" &&
		kubeBS.Status.Phase == v1alpha1.ResourcePhaseCreating &&
		condition != nil &&
		condition.Status == metav1.ConditionTrue &&
		condition.Reason == v1alpha1.ConditionReasonSynchronized
}

func kubeBlockStorageHasDeniedChanges(kubeBS *v1alpha1.BlockStorage, cmpBS *arubatypes.BlockStorageResponse) bool {
	if kubeBS.DeletionTimestamp.IsZero() == false {
		return false
	}
	if cmpBS == nil {
		return false
	}
	return checkBlockStorageDeniedChanges(kubeBS, cmpBS) != nil
}

// kubeBlockStorageSpecInSyncWithCMP is a fast-path guard: generation changed but
// the spec is semantically identical to the CMP state (and no denied changes).
func kubeBlockStorageSpecInSyncWithCMP(kubeBS *v1alpha1.BlockStorage, cmpBS *arubatypes.BlockStorageResponse) bool {
	condition := meta.FindStatusCondition(kubeBS.Status.Conditions, string(v1alpha1.ResourcePhaseActive))

	return kubeBS.DeletionTimestamp.IsZero() &&
		kubeBS.Status.Phase == v1alpha1.ResourcePhaseActive &&
		kubeBS.Status.ResourceID != "" &&
		kubeBS.Status.ObservedGeneration != kubeBS.Generation &&
		checkBlockStorageDeniedChanges(kubeBS, cmpBS) == nil &&
		!kubeBlockStorageNeedsUpdate(kubeBS, cmpBS) &&
		condition != nil &&
		condition.Status == metav1.ConditionTrue &&
		condition.Reason == v1alpha1.ConditionReasonSynchronized
}

func kubeBlockStorageShouldUpdate(kubeBS *v1alpha1.BlockStorage, cmpBS *arubatypes.BlockStorageResponse) bool {
	condition := meta.FindStatusCondition(kubeBS.Status.Conditions, string(v1alpha1.ResourcePhaseActive))

	return kubeBS.DeletionTimestamp.IsZero() &&
		kubeBS.Status.Phase == v1alpha1.ResourcePhaseActive &&
		kubeBS.Status.ResourceID != "" &&
		kubeBS.Status.ObservedGeneration != kubeBS.Generation &&
		checkBlockStorageDeniedChanges(kubeBS, cmpBS) == nil &&
		kubeBlockStorageNeedsUpdate(kubeBS, cmpBS) &&
		condition != nil &&
		condition.Status == metav1.ConditionTrue &&
		condition.Reason == v1alpha1.ConditionReasonSynchronized
}

func kubeBlockStorageShouldBeUpdatedOnCMP(kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) bool {
	condition := meta.FindStatusCondition(kubeBS.Status.Conditions, string(v1alpha1.ResourcePhaseUpdating))

	return kubeBS.DeletionTimestamp.IsZero() &&
		kubeBS.Status.Phase == v1alpha1.ResourcePhaseUpdating &&
		condition != nil &&
		condition.Status == metav1.ConditionTrue &&
		condition.Reason == v1alpha1.ConditionReasonShallSynchronize
}

func kubeBlockStorageWaitingUpdateOnCMP(kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) bool {
	condition := meta.FindStatusCondition(kubeBS.Status.Conditions, string(v1alpha1.ResourcePhaseUpdating))

	return kubeBS.DeletionTimestamp.IsZero() &&
		kubeBS.Status.Phase == v1alpha1.ResourcePhaseUpdating &&
		condition != nil &&
		condition.Status == metav1.ConditionTrue &&
		condition.Reason == v1alpha1.ConditionReasonSynchronizing
}

func kubeBlockStorageUpdateAccomplished(kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) bool {
	condition := meta.FindStatusCondition(kubeBS.Status.Conditions, string(v1alpha1.ResourcePhaseUpdating))

	return kubeBS.DeletionTimestamp.IsZero() &&
		kubeBS.Status.Phase == v1alpha1.ResourcePhaseUpdating &&
		condition != nil &&
		condition.Status == metav1.ConditionTrue &&
		condition.Reason == v1alpha1.ConditionReasonSynchronized
}

// Aruba CMP BlockStorage Conditions

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

// Kubernetes Actions

func (r *BlockStorageReconciler) kubeSetPhaseAndCondition(ctx context.Context, kubeBS *v1alpha1.BlockStorage, phase v1alpha1.ResourcePhase, reason string) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		kubeBSCopy := kubeBS.DeepCopy()
		if err := r.Get(ctx, client.ObjectKeyFromObject(kubeBS), kubeBSCopy); err != nil {
			return err
		}

		kubeBSPatch := kubeBSCopy.DeepCopy()
		kubeBSPatch.Status.Phase = phase

		if prjID, ok := ctx.Value(projectIDKey).(string); ok && kubeBSPatch.Status.ProjectID == "" {
			kubeBSPatch.Status.ProjectID = prjID
		}

		for i := range kubeBSPatch.Status.Conditions {
			kubeBSPatch.Status.Conditions[i].Status = metav1.ConditionFalse
		}

		meta.SetStatusCondition(
			&kubeBSPatch.Status.Conditions,
			metav1.Condition{
				Type:               string(phase),
				Status:             metav1.ConditionTrue,
				Reason:             reason,
				Message:            fmt.Sprintf("%s %s", string(phase), reason),
				LastTransitionTime: metav1.Now(),
			},
		)

		if err := r.Status().Patch(ctx, kubeBSPatch, client.MergeFrom(kubeBSCopy)); err != nil {
			return fmt.Errorf(
				"failed to update blockstorage '%s/%s' state to '%v': %w",
				kubeBSPatch.Namespace, kubeBSPatch.Name, phase, err,
			)
		}

		return nil
	})
}

func (r *BlockStorageReconciler) kubeMarkToDelete(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeBS, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize)
}

func (r *BlockStorageReconciler) kubeMarkDeleting(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeBS, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronizing)
}

func (r *BlockStorageReconciler) kubeMarkDeletingDone(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeBS, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronized)
}

func (r *BlockStorageReconciler) kubeMarkDeleted(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeBS, v1alpha1.ResourcePhaseDeleted, v1alpha1.ConditionReasonSynchronized)
}

func (r *BlockStorageReconciler) kubeMarkToCreate(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeBS, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize)
}

func (r *BlockStorageReconciler) kubeMarkCreating(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeBS, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing)
}

func (r *BlockStorageReconciler) kubeMarkCreatingDone(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeBS, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized)
}

func (r *BlockStorageReconciler) kubeMarkToUpdate(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeBS, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize)
}

func (r *BlockStorageReconciler) kubeMarkUpdating(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeBS, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronizing)
}

func (r *BlockStorageReconciler) kubeMarkUpdatingDone(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeBS, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronized)
}

func (r *BlockStorageReconciler) kubeSetActiveAndSetID(ctx context.Context, kubeBS *v1alpha1.BlockStorage, cmpBS *arubatypes.BlockStorageResponse) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		kubeBSCopy := kubeBS.DeepCopy()
		if err := r.Get(ctx, client.ObjectKeyFromObject(kubeBS), kubeBSCopy); err != nil {
			return err
		}

		kubeBSPatch := kubeBSCopy.DeepCopy()
		kubeBSPatch.Status.Phase = v1alpha1.ResourcePhaseActive
		if kubeBSPatch.Status.ResourceID == "" && cmpBS != nil && cmpBS.Metadata.ID != nil {
			kubeBSPatch.Status.ResourceID = *cmpBS.Metadata.ID
		}
		kubeBSPatch.Status.ObservedGeneration = kubeBSCopy.Generation

		if prjID, ok := ctx.Value(projectIDKey).(string); ok && kubeBSPatch.Status.ProjectID != "" {
			kubeBSPatch.Status.ProjectID = prjID
		}

		for i := range kubeBSPatch.Status.Conditions {
			kubeBSPatch.Status.Conditions[i].Status = metav1.ConditionFalse
		}

		meta.SetStatusCondition(
			&kubeBSPatch.Status.Conditions,
			metav1.Condition{
				Type:               string(v1alpha1.ResourcePhaseActive),
				Status:             metav1.ConditionTrue,
				Reason:             v1alpha1.ConditionReasonSynchronized,
				Message:            fmt.Sprintf("%s %s", string(v1alpha1.ResourcePhaseActive), v1alpha1.ConditionReasonSynchronized),
				LastTransitionTime: metav1.Now(),
			},
		)

		if err := r.Status().Patch(ctx, kubeBSPatch, client.MergeFrom(kubeBSCopy)); err != nil {
			return fmt.Errorf(
				"failed to update blockstorage '%s/%s' state to '%v': %w",
				kubeBSPatch.Namespace, kubeBSPatch.Name, v1alpha1.ResourcePhaseActive, err,
			)
		}

		return nil
	})
}

func (r *BlockStorageReconciler) kubeSetFailed(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeBS, v1alpha1.ResourcePhaseFailed, v1alpha1.ConditionReasonSynchronized)
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

	// Guard: should have been caught by BlockStorageHasDeniedChanges, but double-check.
	if err := checkBlockStorageDeniedChanges(kubeBS, cmpBS); err != nil {
		return err
	}

	request := buildBlockStorageUpdateRequest(kubeBS, cmpBS)

	updateResp, err := r.ArubaClient.FromStorage().Volumes().Update(ctx, prjID, *cmpBS.Metadata.ID, *request, nil)
	if err != nil {
		return fmt.Errorf("failed to update blockstorage '%s' in Aruba CMP: %w", kubeBS.Name, err)
	}

	switch updateResp.StatusCode {
	case http.StatusOK, http.StatusAccepted, http.StatusNoContent:
		// Success
	case http.StatusBadRequest:
		return fmt.Errorf(
			"failed to update blockstorage '%s' in Aruba CMP: status_code: %d, error: 'semantic or precondition error'",
			kubeBS.Name, updateResp.StatusCode,
		)
	default:
		return fmt.Errorf(
			"failed to update blockstorage '%s' in Aruba CMP: status_code: %d, error: 'internal error'",
			kubeBS.Name, updateResp.StatusCode,
		)
	}
	return nil
}

// Transition Set Builder

func (r *BlockStorageReconciler) newTransitionSet() *TransitionSet[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse] {
	ts := &TransitionSet[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		defaultRequeue:        NoRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		defaultRequeueOnError: NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	}

	// 1. BlockStorageShouldBeDeleted
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:           "BlockStorageShouldBeDeleted",
		kCondition:     kubeBlockStorageShouldDelete,
		aCondition:     cmpBlockStorageIsFinal,
		kAction:        r.kubeMarkToDelete,
		requeue:        ShortRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 2. BlockStorageShouldBeDeletedOnCMP
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:              "BlockStorageShouldBeDeletedOnCMP",
		kCondition:        kubeBlockStorageShouldBeDeletedOnCMP,
		aCondition:        cmpBlockStorageIsFinal,
		aAction:           r.cmpDelete,
		kActionOnASuccess: r.kubeMarkDeleting,
		requeue:           ShortRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError:    LongRequeueAndIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 3. BlockStorageWaitingDeletionOnCMP
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:           "BlockStorageWaitingDeletionOnCMP",
		kCondition:     kubeBlockStorageWaitingDeletionOnCMP,
		aCondition:     cmpBlockStorageIsTransitory,
		requeue:        LongRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 4. BlockStorageDeletionConfirmedOnCMP
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:           "BlockStorageDeletionConfirmedOnCMP",
		kCondition:     kubeBlockStorageWaitingDeletionOnCMP,
		aCondition:     cmpBlockStorageNotExists,
		kAction:        r.kubeMarkDeletingDone,
		requeue:        ShortRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 5. BlockStorageDeletionAccomplished
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:           "BlockStorageDeletionAccomplished",
		kCondition:     kubeBlockStorageDeletionAcomplished,
		aCondition:     cmpBlockStorageNotExists,
		kAction:        r.kubeMarkDeleted,
		requeue:        ShortRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 6a. BlockStorageHasDeniedChanges — surface immutable field violations before attempting update
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:       "BlockStorageHasDeniedChanges",
		kCondition: kubeBlockStorageHasDeniedChanges,
		aCondition: cmpBlockStorageIsFinal,
		kAction: func(ctx context.Context, kubeBS *v1alpha1.BlockStorage, cmpBS *arubatypes.BlockStorageResponse) error {
			return fmt.Errorf("blockstorage update rejected: %w", checkBlockStorageDeniedChanges(kubeBS, cmpBS))
		},
		requeue:        NoRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError: LongRequeueAndIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 6b. BlockStorageSpecAlreadyInSyncWithCMP — generation changed but spec identical to CMP; just re-stamp ObservedGeneration
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:           "BlockStorageSpecAlreadyInSyncWithCMP",
		kCondition:     kubeBlockStorageSpecInSyncWithCMP,
		aCondition:     cmpBlockStorageIsActive,
		kAction:        r.kubeSetActiveAndSetID,
		requeue:        NoRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 6c. BlockStorageShouldBeUpdated — spec changed and CMP is ready
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:           "BlockStorageShouldBeUpdated",
		kCondition:     kubeBlockStorageShouldUpdate,
		aCondition:     cmpBlockStorageIsFinal,
		kAction:        r.kubeMarkToUpdate,
		requeue:        ShortRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 6d. BlockStorageShouldBeUpdatedOnCMP — send update to CMP
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:              "BlockStorageShouldBeUpdatedOnCMP",
		kCondition:        kubeBlockStorageShouldBeUpdatedOnCMP,
		aCondition:        cmpBlockStorageIsFinal,
		aAction:           r.cmpUpdate,
		kActionOnASuccess: r.kubeMarkUpdating,
		requeue:           ShortRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError:    LongRequeueAndIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 6e. BlockStorageWaitingUpdateOnCMP — CMP is still processing the update
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:           "BlockStorageWaitingUpdateOnCMP",
		kCondition:     kubeBlockStorageWaitingUpdateOnCMP,
		aCondition:     cmpBlockStorageIsTransitory,
		requeue:        LongRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 6f. BlockStorageUpdateConfirmedOnCMP — CMP has settled; advance to Synchronized
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:           "BlockStorageUpdateConfirmedOnCMP",
		kCondition:     kubeBlockStorageWaitingUpdateOnCMP,
		aCondition:     cmpBlockStorageIsFinal,
		kAction:        r.kubeMarkUpdatingDone,
		requeue:        ShortRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 6g. BlockStorageUpdateAccomplished — transition back to Active and stamp generation
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:           "BlockStorageUpdateAccomplished",
		kCondition:     kubeBlockStorageUpdateAccomplished,
		aCondition:     cmpBlockStorageIsActive,
		kAction:        r.kubeSetActiveAndSetID,
		requeue:        NoRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 6. BlockStorageShouldBeCreated
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:           "BlockStorageShouldBeCreated",
		kCondition:     kubeBlockStorageIsFirstReconciliation,
		aCondition:     cmpBlockStorageNotExists,
		kAction:        r.kubeMarkToCreate,
		requeue:        ShortRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 7. BlockStorageShouldBeCreatedInCMP
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:              "BlockStorageShouldBeCreatedInCMP",
		kCondition:        kubeBlockStorageShouldBeCreatedOnCMP,
		aCondition:        cmpBlockStorageNotExists,
		aAction:           r.cmpCreate,
		kActionOnASuccess: r.kubeMarkCreating,
		requeue:           ShortRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError:    LongRequeueAndIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 8. BlockStorageWaitingCreationInCMP
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:           "BlockStorageWaitingCreationInCMP",
		kCondition:     kubeBlockStorageWaitingCreationInCMP,
		aCondition:     cmpBlockStorageNotExistsOrTransitory,
		requeue:        LongRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 9. BlockStorageCreationConfirmedOnCMP
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:           "BlockStorageCreationConfirmedOnCMP",
		kCondition:     kubeBlockStorageWaitingCreationInCMP,
		aCondition:     cmpBlockStorageIsActive,
		kAction:        r.kubeMarkCreatingDone,
		requeue:        ShortRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 10. BlockStorageCreationAccomplished
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:           "BlockStorageCreationAccomplished",
		kCondition:     kubeBlockStorageIsCreatedOnCMP,
		aCondition:     cmpBlockStorageIsActive,
		kAction:        r.kubeSetActiveAndSetID,
		requeue:        NoRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 11. BlockStorageIsInError
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:           "BlockStorageIsInError",
		kCondition:     AlwaysTrue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		aCondition:     cmpBlockStorageIsFailed,
		kAction:        r.kubeSetFailed,
		requeue:        NoRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	return ts
}

// checkBlockStorageDeniedChanges returns a non-nil error if any immutable spec field
// differs from the current CMP state. These fields cannot be changed after creation.
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

// kubeBlockStorageNeedsUpdate returns true when at least one mutable spec field differs
// from the current CMP state.
func kubeBlockStorageNeedsUpdate(kubeBS *v1alpha1.BlockStorage, cmpBS *arubatypes.BlockStorageResponse) bool {
	if cmpBS == nil {
		return false
	}
	return kubeBS.Spec.BillingPeriod != cmpBS.Properties.BillingPeriod ||
		kubeBS.Spec.DataCenter != cmpBS.Properties.Zone ||
		kubeBS.Spec.SizeGb != int32(cmpBS.Properties.SizeGB) ||
		!kubeBlockStorageTagsAreEqual(kubeBS, cmpBS.Metadata.Tags)
}

// kubeBlockStorageTagsAreEqual returns true when spec tags and CMP tags contain
// the same elements regardless of order.
func kubeBlockStorageTagsAreEqual(kubeBS *v1alpha1.BlockStorage, cmpTags []string) bool {
	if len(kubeBS.Spec.Tags) != len(cmpTags) {
		return false
	}
	kubeTags := make([]string, len(kubeBS.Spec.Tags))
	copy(kubeTags, kubeBS.Spec.Tags)
	cmpTagsCopy := make([]string, len(cmpTags))
	copy(cmpTagsCopy, cmpTags)
	slices.Sort(kubeTags)
	slices.Sort(cmpTagsCopy)
	for i, tag := range kubeTags {
		if tag != cmpTagsCopy[i] {
			return false
		}
	}
	return true
}

// cmpBlockStorageRequestFromCMP seeds an update request from the current CMP state,
// preserving CMP-managed fields that the operator does not own.
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

// buildBlockStorageUpdateRequest seeds from CMP state then overwrites the mutable spec fields.
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
