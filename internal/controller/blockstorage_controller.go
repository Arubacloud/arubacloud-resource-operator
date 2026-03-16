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

	r.ts = r.newBlockStorageTransisionSet()

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
	k8sBs, ok := obj.(*v1alpha1.BlockStorage)
	if !ok {
		return ctrl.Result{}, errors.New("obj is not a *v1alpha1.BlockStorage") // TODO: better error handling
	}

	if k8sBs.Spec.ProjectReference.Name == "" {
		return ctrl.Result{}, fmt.Errorf("project reference is not valid") // TODO: better error handling
	}

	bsName, prjName := k8sBs.Name, k8sBs.Spec.ProjectReference.Name
	bsFilter := fmt.Sprintf(`name:eq("%s")`, bsName)
	prjFilter := fmt.Sprintf(`name:eq("%s")`, prjName)

	prjResp, err := r.ArubaClient.FromProject().List(ctx, &arubatypes.RequestParameters{Filter: &prjFilter})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf(
			"failed to find project in Aruba cloud: %w, project_name: '%s', project_filter: '%s'",
			err, prjName, prjFilter,
		)
	}
	if prjResp.IsError() {
		return ctrl.Result{}, fmt.Errorf(
			"failed to find project in Aruba cloud: status_code: %d, project_name: '%s', project_filter: '%s'",
			prjResp.StatusCode, prjName, prjFilter,
		)
	}
	if prjResp.Data.Total == 0 && k8sBs.Status.ProjectID != "" {
		return ctrl.Result{}, fmt.Errorf(
			"inconsistent data in project list: expected: 1, project not found: project_name: '%s', project_filter: '%s'", prjName, prjFilter,
		)
	}

	if prjResp.Data.Total > 1 {
		return ctrl.Result{}, fmt.Errorf(
			"inconsistent data in project list: expected: 1, found: %d, project_name: '%s', project_filter: '%s'",
			prjResp.Data.Total, prjName, prjFilter,
		)
	}

	if prjResp.Data.Total == 0 {
		// Wait for the project to be created
		return ctrl.Result{RequeueAfter: reconciler.RequeueAfter}, nil
	}

	prjID := *(prjResp.Data.Values[0].Metadata.ID)

	if k8sBs.Status.ProjectID != "" && k8sBs.Status.ProjectID != prjID {
		return ctrl.Result{}, fmt.Errorf(
			"inconsistent project id in blockstorage: blockstorage_name: '%s', blockstorage_project_id: '%s', project_name: '%s', project_id: '%s'",
			bsName, k8sBs.Status.ProjectID, prjName, prjID,
		)
	}

	bsResp, err := r.ArubaClient.FromStorage().Volumes().List(ctx, prjID, &arubatypes.RequestParameters{Filter: &bsFilter})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf(
			"failed to find blockstorage in Aruba cloud: %w, blockstorage_name: '%s', blockstorage_filter: '%s', project_name: '%s'",
			err, bsName, bsFilter, prjName,
		)
	}
	if bsResp.IsError() {
		return ctrl.Result{}, fmt.Errorf(
			"failed to find blockstorage in Aruba cloud: status_code: %d, blockstorage_name: '%s', project_name: '%s'",
			bsResp.StatusCode, bsName, prjName,
		)
	}

	if bsResp.Data.Total < 0 || bsResp.Data.Total > 1 {
		return ctrl.Result{}, fmt.Errorf(
			"inconsistent data in blockstorage list: blockstorage_name: '%s', blockstorage_filter: '%s', project_name: '%s', instances: %d",
			bsName, bsFilter, prjName, len(bsResp.Data.Values),
		)
	}

	var arubaBS *arubatypes.BlockStorageResponse
	if bsResp.Data.Total == 1 {
		arubaBS = &bsResp.Data.Values[0]
	}

	ctx = context.WithValue(ctx, projectIDKey, prjID)

	return r.ts.Run(ctx, k8sBs, arubaBS)
}

//
// Transition Set Functions
//

// Kubernetes BlockStorage Conditions

func kBsShouldBeDeleted(k *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) bool {
	return !k.DeletionTimestamp.IsZero() &&
		k.Status.Phase != v1alpha1.ResourcePhaseDeleting &&
		k.Status.Phase != v1alpha1.ResourcePhaseDeleted
}

func kBsDeletingInProgress(k *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) bool {
	return !k.DeletionTimestamp.IsZero() &&
		k.Status.Phase == v1alpha1.ResourcePhaseDeleting
}

func kBsDoesNotExistsInBoth(k *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) bool {
	return k.DeletionTimestamp.IsZero() &&
		k.Status.Phase == "" &&
		k.Status.ResourceID == ""
}

func kBsDoesNotExistsInCMP(k *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) bool {
	return k.DeletionTimestamp.IsZero() &&
		k.Status.Phase == v1alpha1.ResourcePhaseCreating &&
		k.Status.ResourceID == ""
}

func kBsWasRemovedFromCMP(k *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) bool {
	return k.DeletionTimestamp.IsZero() &&
		k.Status.Phase != "" &&
		k.Status.ResourceID != ""
}

func kBsCreationInProgress(k *v1alpha1.BlockStorage, a *arubatypes.BlockStorageResponse) bool {
	return k.DeletionTimestamp.IsZero() &&
		k.Status.Phase != "" &&
		(k.Status.ResourceID == "" || (a != nil && k.Status.ResourceID == *a.Metadata.ID))
}

func kBsIsActive(k *v1alpha1.BlockStorage, a *arubatypes.BlockStorageResponse) bool {
	return k.DeletionTimestamp.IsZero() &&
		k.Status.Phase != "" &&
		k.Status.Phase != v1alpha1.ResourcePhaseUpdating &&
		(k.Status.ResourceID == "" || (a != nil && k.Status.ResourceID == *a.Metadata.ID))
}

func kBsHasDeniedChanges(k *v1alpha1.BlockStorage, a *arubatypes.BlockStorageResponse) bool {
	if !k.DeletionTimestamp.IsZero() {
		return false
	}
	if a == nil {
		return false
	}
	_, _, err := convertAndCheckForUpdate(k, a)
	return err != nil
}

func kBsShouldBeUpdated(k *v1alpha1.BlockStorage, a *arubatypes.BlockStorageResponse) bool {
	if !k.DeletionTimestamp.IsZero() || k.Status.Phase == v1alpha1.ResourcePhaseUpdating {
		return false
	}
	if a == nil {
		return false
	}
	_, mustUpdate, err := convertAndCheckForUpdate(k, a)
	return err == nil && mustUpdate
}

func kBsUpdatingInProgress(k *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) bool {
	return k.DeletionTimestamp.IsZero() && k.Status.Phase == v1alpha1.ResourcePhaseUpdating
}

func kBsUpdated(k *v1alpha1.BlockStorage, a *arubatypes.BlockStorageResponse) bool {
	if !k.DeletionTimestamp.IsZero() || k.Status.Phase != v1alpha1.ResourcePhaseUpdating {
		return false
	}
	if a == nil {
		return false
	}
	_, mustUpdate, err := convertAndCheckForUpdate(k, a)
	return err == nil && !mustUpdate
}

// Aruba CMP BlockStorage Conditions

func aBsStateNatureFinal(_ *v1alpha1.BlockStorage, a *arubatypes.BlockStorageResponse) bool {
	if a == nil || a.Status.State == nil {
		return false
	}
	if *a.Status.State == CSPResourceStateDeleting || *a.Status.State == CSPResourceStateDeleted {
		return false
	}
	return AssesCSPResourceStateNature(&a.Status) == CSPResourceStateNatureFinal
}

func aBsStateDeleting(_ *v1alpha1.BlockStorage, a *arubatypes.BlockStorageResponse) bool {
	return a != nil && a.Status.State != nil && *a.Status.State == CSPResourceStateDeleting
}

func aBsNotExists(_ *v1alpha1.BlockStorage, a *arubatypes.BlockStorageResponse) bool {
	return a == nil
}

func aBsStateCreating(_ *v1alpha1.BlockStorage, a *arubatypes.BlockStorageResponse) bool {
	return a != nil && a.Status.State != nil &&
		(*a.Status.State == CSPResourceStateCreating ||
			*a.Status.State == CSPResourceStateInCreation ||
			*a.Status.State == CSPResourceStateProvisioning)
}

func aBsStateActive(_ *v1alpha1.BlockStorage, a *arubatypes.BlockStorageResponse) bool {
	return a != nil && a.Status.State != nil &&
		(*a.Status.State == CSPResourceStateActive ||
			*a.Status.State == CSPResourceStateNotUsed ||
			*a.Status.State == CSPResourceStateInUse ||
			*a.Status.State == CSPResourceStateUsed)
}

func aBsIsInError(_ *v1alpha1.BlockStorage, a *arubatypes.BlockStorageResponse) bool {
	return a != nil && a.Status.State != nil && *a.Status.State == CSPResourceStateFailed
}

func aBsStateUpdating(_ *v1alpha1.BlockStorage, a *arubatypes.BlockStorageResponse) bool {
	return a != nil && a.Status.State != nil && *a.Status.State == CSPResourceStateUpdating
}

func aBsStateNatureFinalForUpdate(_ *v1alpha1.BlockStorage, a *arubatypes.BlockStorageResponse) bool {
	return a != nil && AssesCSPResourceStateNature(&a.Status) == CSPResourceStateNatureFinal
}

// Kubernetes Actions

func (r *BlockStorageReconciler) setKBsState(ctx context.Context, k *v1alpha1.BlockStorage, state v1alpha1.ResourcePhase) error {
	kCopy := k.DeepCopy()
	kCopy.Status.Phase = state

	if prjID, ok := ctx.Value(projectIDKey).(string); ok {
		kCopy.Status.ProjectID = prjID
	}

	if err := r.Status().Patch(ctx, kCopy, client.MergeFrom(k)); err != nil {
		return fmt.Errorf("failed to update blockstorage '%s/%s' state to '%v': %w", kCopy.Namespace, kCopy.Name, state, err)
	}
	return nil
}

func (r *BlockStorageReconciler) setBsToDeletingOnK8s(ctx context.Context, k *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) error {
	return r.setKBsState(ctx, k, v1alpha1.ResourcePhaseDeleting)
}

func (r *BlockStorageReconciler) setBsToDeletedOnK8s(ctx context.Context, k *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) error {
	return r.setKBsState(ctx, k, v1alpha1.ResourcePhaseDeleted)
}

func (r *BlockStorageReconciler) setBsToCreatingOnK8s(ctx context.Context, k *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) error {
	return r.setKBsState(ctx, k, v1alpha1.ResourcePhaseCreating)
}

func (r *BlockStorageReconciler) setBsToUpdatingOnK8s(ctx context.Context, k *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) error {
	return r.setKBsState(ctx, k, v1alpha1.ResourcePhaseUpdating)
}

func (r *BlockStorageReconciler) setBsToCreatingAndUnsetResourceIDOnK8s(ctx context.Context, k *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) error {
	kCopy := k.DeepCopy()
	kCopy.Status.Phase = v1alpha1.ResourcePhaseCreating
	kCopy.Status.ResourceID = ""

	if prjID, ok := ctx.Value(projectIDKey).(string); ok {
		kCopy.Status.ProjectID = prjID
	}

	if err := r.Status().Patch(ctx, kCopy, client.MergeFrom(k)); err != nil {
		return fmt.Errorf("failed to update blockstorage '%s/%s' state to '%v': %w", kCopy.Namespace, kCopy.Name, v1alpha1.ResourcePhaseCreating, err)
	}
	return nil
}

func (r *BlockStorageReconciler) setBsToActiveAndSetResourceIDOnK8s(ctx context.Context, k *v1alpha1.BlockStorage, a *arubatypes.BlockStorageResponse) error {
	kCopy := k.DeepCopy()
	kCopy.Status.Phase = v1alpha1.ResourcePhaseActive
	if a != nil {
		kCopy.Status.ResourceID = *a.Metadata.ID
	}

	if prjID, ok := ctx.Value(projectIDKey).(string); ok {
		kCopy.Status.ProjectID = prjID
	}

	if err := r.Status().Patch(ctx, kCopy, client.MergeFrom(k)); err != nil {
		return fmt.Errorf("failed to update blockstorage '%s/%s' state to '%v': %w", kCopy.Namespace, kCopy.Name, v1alpha1.ResourcePhaseActive, err)
	}
	return nil
}

func (r *BlockStorageReconciler) setBsToCreatingAndSetResourceIDOnK8s(ctx context.Context, k *v1alpha1.BlockStorage, a *arubatypes.BlockStorageResponse) error {
	kCopy := k.DeepCopy()
	kCopy.Status.Phase = v1alpha1.ResourcePhaseCreating
	if a != nil {
		kCopy.Status.ResourceID = *a.Metadata.ID
	}

	if prjID, ok := ctx.Value(projectIDKey).(string); ok {
		kCopy.Status.ProjectID = prjID
	}

	if err := r.Status().Patch(ctx, kCopy, client.MergeFrom(k)); err != nil {
		return fmt.Errorf("failed to update blockstorage '%s/%s' state to '%v': %w", kCopy.Namespace, kCopy.Name, v1alpha1.ResourcePhaseCreating, err)
	}
	return nil
}

func (r *BlockStorageReconciler) setBsToFailedOnK8s(ctx context.Context, k *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) error {
	return r.setKBsState(ctx, k, v1alpha1.ResourcePhaseFailed)
}

func (r *BlockStorageReconciler) setBsToActiveOnK8s(ctx context.Context, k *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) error {
	return r.setKBsState(ctx, k, v1alpha1.ResourcePhaseActive)
}

func (r *BlockStorageReconciler) setBsToFailedOn400(ctx context.Context, k *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse, err error) error {
	if strings.Contains(err.Error(), "status_code: 400") {
		return r.setKBsState(ctx, k, v1alpha1.ResourcePhaseFailed)
	}
	return nil
}

// Aruba CMP Actions

func (r *BlockStorageReconciler) deleteBsFromCMP(ctx context.Context, _ *v1alpha1.BlockStorage, a *arubatypes.BlockStorageResponse) error {
	prjID := ctx.Value(projectIDKey).(string)
	bsResp, err := r.ArubaClient.FromStorage().Volumes().Delete(ctx, prjID, *a.Metadata.ID, nil)
	if err != nil {
		return fmt.Errorf("failed to delete blockstorage '%s' in Aruba CMP: error: '%w'", *a.Metadata.Name, err)
	}

	switch bsResp.StatusCode {
	case http.StatusOK, http.StatusAccepted, http.StatusNoContent, http.StatusNotFound:
		// Do nothing, we can consider the delete request as successful
	case http.StatusBadRequest:
		return fmt.Errorf("failed to delete blockstorage '%s' in Aruba CMP: status_code: %d, error: 'semantic or precondition error'", *a.Metadata.Name, bsResp.StatusCode)
	default:
		return fmt.Errorf("failed to delete blockstorage '%s' in Aruba CMP: status_code: %d, error: 'internal error'", *a.Metadata.Name, bsResp.StatusCode)
	}
	return nil
}

func (r *BlockStorageReconciler) createBsInCMP(ctx context.Context, k *v1alpha1.BlockStorage, _ *arubatypes.BlockStorageResponse) error {
	prjID := ctx.Value(projectIDKey).(string)
	bsCreateResp, err := r.ArubaClient.FromStorage().Volumes().Create(ctx, prjID, *blockStorageRequestFromK8s(k), nil)
	if err != nil {
		return fmt.Errorf("failed to create blockstorage '%s' in Aruba CMP: error: '%w'", k.Name, err)
	}

	switch bsCreateResp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusAccepted:
		// Success
	case http.StatusBadRequest:
		return fmt.Errorf("status_code: 400, failed to create blockstorage '%s' in Aruba CMP: semantic or precondition error", k.Name)
	default:
		return fmt.Errorf("status_code: %d, failed to create blockstorage '%s' in Aruba CMP: internal error", bsCreateResp.StatusCode, k.Name)
	}
	return nil
}

func (r *BlockStorageReconciler) updateBsInCMP(ctx context.Context, k *v1alpha1.BlockStorage, a *arubatypes.BlockStorageResponse) error {
	prjID := ctx.Value(projectIDKey).(string)
	request, _, err := convertAndCheckForUpdate(k, a)
	if err != nil {
		return err // Should be caught by ResourceHasDeniedChanges beforehand
	}

	updateResp, err := r.ArubaClient.FromStorage().Volumes().Update(ctx, prjID, *a.Metadata.ID, *request, nil)
	if err != nil {
		return fmt.Errorf("failed to update blockstorage '%s' in Aruba CMP: %w", k.Name, err)
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
		return fmt.Errorf("failed to update blockstorage '%s' in Aruba CMP: %s", k.Name, errDetail)
	}

	return nil
}

// Transition Set Builder

func (r *BlockStorageReconciler) newBlockStorageTransisionSet() *TransitionSet[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse] {
	ts := &TransitionSet[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		defaultKAction:         NoAction[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		defaultAAction:         NoAction[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		defaultKActionOnAError: NoActionOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		defaultRequeue:         DefaultRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		defaultRequeueOnError:  DefaultRequeueOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	}

	// 1. ResourceShouldBeDeleted
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:            "ResourceShouldBeDeleted",
		kCondition:      kBsShouldBeDeleted,
		aCondition:      aBsStateNatureFinal,
		kAction:         r.setBsToDeletingOnK8s,
		aAction:         r.deleteBsFromCMP,
		kActionOnAError: NoActionOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeue:         DefaultRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError:  DefaultRequeueOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 2. ResourceDeletingInProgress
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:            "ResourceDeletingInProgress",
		kCondition:      kBsDeletingInProgress,
		aCondition:      aBsStateDeleting,
		kAction:         NoAction[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		aAction:         NoAction[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		kActionOnAError: NoActionOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeue:         DefaultRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError:  NoRequeueOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 3. ResourceDeletionAccomplished
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:            "ResourceDeletionAccomplished",
		kCondition:      kBsDeletingInProgress,
		aCondition:      aBsNotExists,
		kAction:         r.setBsToDeletedOnK8s,
		aAction:         NoAction[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		kActionOnAError: NoActionOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeue:         NoRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError:  NoRequeueOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 4. ResourceDoesNotExistsInBoth
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:            "ResourceDoesNotExistsInBoth",
		kCondition:      kBsDoesNotExistsInBoth,
		aCondition:      aBsNotExists,
		kAction:         r.setBsToCreatingOnK8s,
		aAction:         r.createBsInCMP,
		kActionOnAError: r.setBsToFailedOn400,
		requeue:         DefaultRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError:  DefaultRequeueOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 5. ResourceDoesNotExistsInCMP
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:            "ResourceDoesNotExistsInCMP",
		kCondition:      kBsDoesNotExistsInCMP,
		aCondition:      aBsNotExists,
		kAction:         NoAction[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		aAction:         r.createBsInCMP,
		kActionOnAError: NoActionOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeue:         DefaultRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError:  DefaultRequeueOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 6. ResourceWasRemovedFromCMP
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:            "ResourceWasRemovedFromCMP",
		kCondition:      kBsWasRemovedFromCMP,
		aCondition:      aBsNotExists,
		kAction:         r.setBsToCreatingAndUnsetResourceIDOnK8s,
		aAction:         r.createBsInCMP,
		kActionOnAError: r.setBsToFailedOn400,
		requeue:         DefaultRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError:  DefaultRequeueOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 7. ResourceCreationInProgress
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:            "ResourceCreationInProgress",
		kCondition:      kBsCreationInProgress,
		aCondition:      aBsStateCreating,
		kAction:         r.setBsToCreatingAndSetResourceIDOnK8s,
		aAction:         NoAction[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		kActionOnAError: NoActionOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeue:         DefaultRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError:  DefaultRequeueOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 8. ResourceIsActive
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:            "ResourceIsActive",
		kCondition:      kBsIsActive,
		aCondition:      aBsStateActive,
		kAction:         r.setBsToActiveAndSetResourceIDOnK8s,
		aAction:         NoAction[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		kActionOnAError: NoActionOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeue:         NoRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError:  DefaultRequeueOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 9. ResourceIsInError
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:            "ResourceIsInError",
		kCondition:      AlwaysTrue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		aCondition:      aBsIsInError,
		kAction:         r.setBsToFailedOnK8s,
		aAction:         NoAction[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		kActionOnAError: NoActionOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeue:         NoRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError:  NoRequeueOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 9b. ResourceHasDeniedChanges (intercept before ResourceShouldBeUpdated to surface the error)
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:            "ResourceHasDeniedChanges",
		kCondition:      kBsHasDeniedChanges,
		aCondition:      AlwaysTrue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		kAction: func(ctx context.Context, k *v1alpha1.BlockStorage, a *arubatypes.BlockStorageResponse) error {
			_, _, err := convertAndCheckForUpdate(k, a)
			return fmt.Errorf("failed to convert and check blockstorage: %w", err)
		},
		aAction:         NoAction[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		kActionOnAError: NoActionOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeue:         NoRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse], // Don't requeue if denied changes
		requeueOnError:  DefaultRequeueOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 10. ResourceShouldBeUpdated
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:            "ResourceShouldBeUpdated",
		kCondition:      kBsShouldBeUpdated,
		aCondition:      aBsStateNatureFinalForUpdate,
		kAction:         r.setBsToUpdatingOnK8s,
		aAction:         r.updateBsInCMP,
		kActionOnAError: NoActionOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeue:         DefaultRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError:  DefaultRequeueOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 11. ResourceUpdatingInProgress
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:            "ResourceUpdatingInProgress",
		kCondition:      kBsUpdatingInProgress,
		aCondition:      aBsStateUpdating,
		kAction:         NoAction[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		aAction:         NoAction[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		kActionOnAError: NoActionOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeue:         DefaultRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError:  DefaultRequeueOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	// 12. ResourceUpdated
	ts.Add(&AbstractTransition[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse]{
		name:            "ResourceUpdated",
		kCondition:      kBsUpdated,
		aCondition:      aBsStateActive,
		kAction:         r.setBsToActiveOnK8s,
		aAction:         NoAction[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		kActionOnAError: NoActionOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeue:         NoRequeue[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
		requeueOnError:  DefaultRequeueOnError[*v1alpha1.BlockStorage, *arubatypes.BlockStorageResponse],
	})

	return ts
}

func blockStorageRequestFromK8s(k8sBs *v1alpha1.BlockStorage) *arubatypes.BlockStorageRequest {
	return &arubatypes.BlockStorageRequest{
		Metadata: arubatypes.RegionalResourceMetadataRequest{
			ResourceMetadataRequest: arubatypes.ResourceMetadataRequest{
				Name: k8sBs.Name,
				Tags: k8sBs.Spec.Tags,
			},
			Location: arubatypes.LocationRequest(k8sBs.Spec.Location),
		},
		Properties: arubatypes.BlockStoragePropertiesRequest{
			SizeGB:        int(k8sBs.Spec.SizeGb),
			BillingPeriod: k8sBs.Spec.BillingPeriod,
			Zone:          &k8sBs.Spec.DataCenter,
			Bootable:      &k8sBs.Spec.Bootable,
			Image:         &k8sBs.Spec.Image,
			Type:          arubatypes.BlockStorageType(k8sBs.Spec.Type),
		},
	}
}

func convertAndCheckForUpdate(
	k8sObj *v1alpha1.BlockStorage,
	arubaObj *arubatypes.BlockStorageResponse,
) (*arubatypes.BlockStorageRequest, bool, error) {
	// TODO: think about the possibility to split this function in two: lokk for changes and conversion
	request := blockStorageRequestFromResponse(arubaObj)
	if request == nil {
		return nil, false, fmt.Errorf("block storage request from response is nil")
	}

	//
	// Not allowed cases
	//
	// An error is returned to block the reconciliation if a single not
	// allowed condition is found

	errs := []error{}

	if k8sObj.Spec.Bootable != *request.Properties.Bootable {
		errs = append(errs, errors.New("change the 'bootable' is not allowed"))
	}

	if k8sObj.Spec.Image != *request.Properties.Image {
		errs = append(errs, errors.New("change the 'image' is not allowed"))
	}

	if k8sObj.Spec.Type != string(request.Properties.Type) {
		errs = append(errs, errors.New("change the 'type' is not allowed"))
	}

	if k8sObj.Spec.Location.Value != request.Metadata.Location.Value {
		errs = append(errs, errors.New("change the 'location' is not allowed"))
	}

	if len(errs) > 0 {
		return nil, false, fmt.Errorf("%w: %w", ErrNotAllowedChanges, errors.Join(errs...))
	}

	//
	// Updating cases
	//
	// We allow the reconciliation to continue when we find the first valid
	// case

	if k8sObj.Spec.BillingPeriod != request.Properties.BillingPeriod ||
		k8sObj.Spec.Bootable != *request.Properties.Bootable ||
		k8sObj.Spec.DataCenter != *request.Properties.Zone ||
		k8sObj.Spec.SizeGb != int32(request.Properties.SizeGB) ||
		!blockStorageTagsAreEquals(k8sObj, request) {
		request.Properties.BillingPeriod = k8sObj.Spec.BillingPeriod
		bootable := k8sObj.Spec.Bootable
		request.Properties.Bootable = &bootable
		zone := k8sObj.Spec.DataCenter
		request.Properties.Zone = &zone
		request.Properties.SizeGB = int(k8sObj.Spec.SizeGb)
		tags := make([]string, len(k8sObj.Spec.Tags))
		copy(tags, k8sObj.Spec.Tags)
		request.Metadata.Tags = tags
		return request, true, nil
	}

	// If we do not find any allowed updating condition, so we signal the
	// caller to not proceed the reconciliation
	return nil, false, nil
}

func blockStorageRequestFromResponse(response *arubatypes.BlockStorageResponse) *arubatypes.BlockStorageRequest {
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

func blockStorageTagsAreEquals(k8sObj *v1alpha1.BlockStorage, request *arubatypes.BlockStorageRequest) bool {
	// TODO: generalize this function
	if request == nil {
		return false
	}
	if len(k8sObj.Spec.Tags) != len(request.Metadata.Tags) {
		return false
	}

	slices.Sort(k8sObj.Spec.Tags)
	slices.Sort(request.Metadata.Tags)

	for i, tag := range k8sObj.Spec.Tags {
		if tag != request.Metadata.Tags[i] {
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
