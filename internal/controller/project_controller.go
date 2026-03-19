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
	projectFinalizerName = "project.arubacloud.com/finalizer"
)

// ProjectReconciler reconciles a Project object
type ProjectReconciler struct {
	*reconciler.Reconciler
	ts *TransitionSet[*v1alpha1.Project, *arubatypes.ProjectResponse]
}

// NewProjectReconciler creates a new ProjectReconciler
func NewProjectReconciler(baseReconciler *reconciler.Reconciler) *ProjectReconciler {
	r := &ProjectReconciler{
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

func (r *ProjectReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.Reconciler.Reconcile(ctx, req, r)
}

func (r *ProjectReconciler) Object() reconciler.ResourceObject {
	return &v1alpha1.Project{}
}

func (r *ProjectReconciler) Finalizer() string {
	return projectFinalizerName
}

func (r *ProjectReconciler) HandleReconcile(ctx context.Context, obj reconciler.ResourceObject) (ctrl.Result, error) {
	kubeProject, ok := obj.(*v1alpha1.Project)
	if !ok {
		return ctrl.Result{}, errors.New("obj is not a *v1alpha1.Project")
	}

	projectName := kubeProject.Name
	projectFilter := fmt.Sprintf(`name:eq("%s")`, projectName)

	cmpProjectList, err := r.ArubaClient.FromProject().List(ctx, &arubatypes.RequestParameters{Filter: &projectFilter})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf(
			"failed to find project in Aruba cloud: %w, project_name: '%s', project_filter: '%s'",
			err, projectName, projectFilter,
		)
	}
	if cmpProjectList.IsError() {
		return ctrl.Result{}, fmt.Errorf(
			"failed to find project in Aruba cloud: status_code: %d, project_name: '%s', project_filter: '%s'",
			cmpProjectList.StatusCode, projectName, projectFilter,
		)
	}

	if cmpProjectList.Data.Total < 0 || cmpProjectList.Data.Total > 1 {
		return ctrl.Result{}, fmt.Errorf(
			"inconsistent data: total: %d, status_code: %d, project_name: '%s', project_filter: '%s'",
			cmpProjectList.Data.Total, cmpProjectList.StatusCode, projectName, projectFilter,
		)
	}

	var cmpProject *arubatypes.ProjectResponse
	if cmpProjectList.Data.Total == 1 {
		cmpProject = &cmpProjectList.Data.Values[0]
	}

	return r.ts.Run(ctx, kubeProject, cmpProject)
}

// Transition Set Functions

// Kubernetes Project Conditions

func kubeProjectShouldDelete(kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) bool {
	condition := meta.FindStatusCondition(kubeProj.Status.Conditions, string(kubeProj.Status.Phase))

	return !kubeProj.DeletionTimestamp.IsZero() &&
		kubeProj.Status.AssessPhaseNature() == v1alpha1.PhaseNatureFinal &&
		kubeProj.Status.Phase != v1alpha1.ResourcePhaseDeleted &&
		condition != nil &&
		condition.Status == metav1.ConditionTrue &&
		condition.Reason == v1alpha1.ConditionReasonSynchronized
}

func kubeProjectShouldBeDeletedOnCMP(kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) bool {
	condition := meta.FindStatusCondition(kubeProj.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))

	return !kubeProj.DeletionTimestamp.IsZero() &&
		kubeProj.Status.Phase == v1alpha1.ResourcePhaseDeleting &&
		condition != nil &&
		condition.Status == metav1.ConditionTrue &&
		condition.Reason == v1alpha1.ConditionReasonShallSynchronize
}

func kubeProjectWaitingDeletionOnCMP(kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) bool {
	condition := meta.FindStatusCondition(kubeProj.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))

	return !kubeProj.DeletionTimestamp.IsZero() &&
		kubeProj.Status.Phase == v1alpha1.ResourcePhaseDeleting &&
		condition != nil &&
		condition.Status == metav1.ConditionTrue &&
		condition.Reason == v1alpha1.ConditionReasonSynchronizing
}

func kubeProjectDeletionAcomplished(kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) bool {
	condition := meta.FindStatusCondition(kubeProj.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))

	return !kubeProj.DeletionTimestamp.IsZero() &&
		kubeProj.Status.Phase == v1alpha1.ResourcePhaseDeleting &&
		condition != nil &&
		condition.Status == metav1.ConditionTrue &&
		condition.Reason == v1alpha1.ConditionReasonSynchronized
}

func kubeProjectIsFirstReconciliation(kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) bool {
	return kubeProj.DeletionTimestamp.IsZero() &&
		kubeProj.Status.ResourceID == "" &&
		kubeProj.Status.Phase == "" &&
		len(kubeProj.Status.Conditions) == 0

}

func kubeProjectShouldBeCreatedOnCMP(kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) bool {
	condition := meta.FindStatusCondition(kubeProj.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))

	return kubeProj.DeletionTimestamp.IsZero() &&
		kubeProj.Status.ResourceID == "" &&
		kubeProj.Status.Phase == v1alpha1.ResourcePhaseCreating &&
		condition != nil &&
		condition.Status == metav1.ConditionTrue &&
		condition.Reason == v1alpha1.ConditionReasonShallSynchronize
}

func kubeProjectWaitingCreationInCMP(kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) bool {
	condition := meta.FindStatusCondition(kubeProj.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))

	return kubeProj.DeletionTimestamp.IsZero() &&
		kubeProj.Status.ResourceID == "" &&
		kubeProj.Status.Phase == v1alpha1.ResourcePhaseCreating &&
		condition != nil &&
		condition.Status == metav1.ConditionTrue &&
		condition.Reason == v1alpha1.ConditionReasonSynchronizing
}

func kubeProjectIsCreatedOnCMP(kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) bool {
	condition := meta.FindStatusCondition(kubeProj.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))

	return kubeProj.DeletionTimestamp.IsZero() &&
		kubeProj.Status.ResourceID == "" &&
		kubeProj.Status.Phase == v1alpha1.ResourcePhaseCreating &&
		condition != nil &&
		condition.Status == metav1.ConditionTrue &&
		condition.Reason == v1alpha1.ConditionReasonSynchronized
}

// kubeProjectSpecInSyncWithCMP is a fast-path guard: generation changed but the spec is
// semantically identical to the CMP state, so we only need to stamp ObservedGeneration.
func kubeProjectSpecInSyncWithCMP(kubeProj *v1alpha1.Project, cmpProj *arubatypes.ProjectResponse) bool {
	condition := meta.FindStatusCondition(kubeProj.Status.Conditions, string(v1alpha1.ResourcePhaseActive))

	return kubeProj.DeletionTimestamp.IsZero() &&
		kubeProj.Status.Phase == v1alpha1.ResourcePhaseActive &&
		kubeProj.Status.ResourceID != "" &&
		kubeProj.Status.ObservedGeneration != kubeProj.Generation &&
		!kubeProjectNeedsUpdate(kubeProj, cmpProj) &&
		condition != nil &&
		condition.Status == metav1.ConditionTrue &&
		condition.Reason == v1alpha1.ConditionReasonSynchronized
}

func kubeProjectShouldUpdate(kubeProj *v1alpha1.Project, cmpProj *arubatypes.ProjectResponse) bool {
	condition := meta.FindStatusCondition(kubeProj.Status.Conditions, string(v1alpha1.ResourcePhaseActive))

	return kubeProj.DeletionTimestamp.IsZero() &&
		kubeProj.Status.Phase == v1alpha1.ResourcePhaseActive &&
		kubeProj.Status.ResourceID != "" &&
		kubeProj.Status.ObservedGeneration != kubeProj.Generation &&
		kubeProjectNeedsUpdate(kubeProj, cmpProj) &&
		condition != nil &&
		condition.Status == metav1.ConditionTrue &&
		condition.Reason == v1alpha1.ConditionReasonSynchronized
}

func kubeProjectShouldBeUpdatedOnCMP(kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) bool {
	condition := meta.FindStatusCondition(kubeProj.Status.Conditions, string(v1alpha1.ResourcePhaseUpdating))

	return kubeProj.DeletionTimestamp.IsZero() &&
		kubeProj.Status.Phase == v1alpha1.ResourcePhaseUpdating &&
		condition != nil &&
		condition.Status == metav1.ConditionTrue &&
		condition.Reason == v1alpha1.ConditionReasonShallSynchronize
}

func kubeProjectWaitingUpdateOnCMP(kubeProj *v1alpha1.Project, cmpProj *arubatypes.ProjectResponse) bool {
	condition := meta.FindStatusCondition(kubeProj.Status.Conditions, string(v1alpha1.ResourcePhaseUpdating))

	return kubeProj.DeletionTimestamp.IsZero() &&
		kubeProj.Status.Phase == v1alpha1.ResourcePhaseUpdating &&
		kubeProjectNeedsUpdate(kubeProj, cmpProj) &&
		condition != nil &&
		condition.Status == metav1.ConditionTrue &&
		condition.Reason == v1alpha1.ConditionReasonSynchronizing
}

func kubeProjectUpdateConfirmedOnCMP(kubeProj *v1alpha1.Project, cmpProj *arubatypes.ProjectResponse) bool {
	condition := meta.FindStatusCondition(kubeProj.Status.Conditions, string(v1alpha1.ResourcePhaseUpdating))

	return kubeProj.DeletionTimestamp.IsZero() &&
		kubeProj.Status.Phase == v1alpha1.ResourcePhaseUpdating &&
		!kubeProjectNeedsUpdate(kubeProj, cmpProj) &&
		condition != nil &&
		condition.Status == metav1.ConditionTrue &&
		condition.Reason == v1alpha1.ConditionReasonSynchronizing
}

func kubeProjectUpdateAccomplished(kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) bool {
	condition := meta.FindStatusCondition(kubeProj.Status.Conditions, string(v1alpha1.ResourcePhaseUpdating))

	return kubeProj.DeletionTimestamp.IsZero() &&
		kubeProj.Status.Phase == v1alpha1.ResourcePhaseUpdating &&
		condition != nil &&
		condition.Status == metav1.ConditionTrue &&
		condition.Reason == v1alpha1.ConditionReasonSynchronized
}

// Aruba CMP Project Conditions

func cmpProjectExists(_ *v1alpha1.Project, cmpProj *arubatypes.ProjectResponse) bool {
	return cmpProj != nil
}

func cmpProjectNotExists(_ *v1alpha1.Project, cmpProj *arubatypes.ProjectResponse) bool {
	return cmpProj == nil
}

// Kubernetes Project Actions

func (r *ProjectReconciler) kubeMarkToDelete(ctx context.Context, kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeProj, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize)
}

func (r *ProjectReconciler) kubeMarkDeleting(ctx context.Context, kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeProj, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronizing)
}

func (r *ProjectReconciler) kubeMarkDeletingDone(ctx context.Context, kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeProj, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronized)
}

func (r *ProjectReconciler) kubeMarkDeleted(ctx context.Context, kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeProj, v1alpha1.ResourcePhaseDeleted, v1alpha1.ConditionReasonSynchronized)
}

func (r *ProjectReconciler) kubeMarkToCreate(ctx context.Context, kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeProj, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize)
}

func (r *ProjectReconciler) kubeMarkCreating(ctx context.Context, kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeProj, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing)
}

func (r *ProjectReconciler) kubeMarkCreatingDone(ctx context.Context, kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeProj, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized)
}

func (r *ProjectReconciler) kubeMarkToUpdate(ctx context.Context, kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeProj, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize)
}

func (r *ProjectReconciler) kubeMarkUpdating(ctx context.Context, kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeProj, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronizing)
}

func (r *ProjectReconciler) kubeMarkUpdatingDone(ctx context.Context, kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeProj, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronized)
}

func (r *ProjectReconciler) kubeSetActiveAndSetID(ctx context.Context, kubeProj *v1alpha1.Project, cmpProj *arubatypes.ProjectResponse) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		kubeProjCopy := kubeProj.DeepCopy()
		if err := r.Get(ctx, client.ObjectKeyFromObject(kubeProj), kubeProjCopy); err != nil {
			return err
		}

		kubeProjPatch := kubeProjCopy.DeepCopy()
		kubeProjPatch.Status.Phase = v1alpha1.ResourcePhaseActive
		if kubeProjPatch.Status.ResourceID == "" && cmpProj != nil && cmpProj.Metadata.ID != nil {
			kubeProjPatch.Status.ResourceID = *cmpProj.Metadata.ID
		}
		kubeProjPatch.Status.ObservedGeneration = kubeProjCopy.Generation

		for i := range kubeProjPatch.Status.Conditions {
			kubeProjPatch.Status.Conditions[i].Status = metav1.ConditionFalse
		}

		meta.SetStatusCondition(
			&kubeProjPatch.Status.Conditions,
			metav1.Condition{
				Type:               string(v1alpha1.ResourcePhaseActive),
				Status:             metav1.ConditionTrue,
				Reason:             v1alpha1.ConditionReasonSynchronized,
				Message:            fmt.Sprintf("%s %s", string(v1alpha1.ResourcePhaseActive), v1alpha1.ConditionReasonSynchronized),
				LastTransitionTime: metav1.Now(),
			},
		)

		if err := r.Status().Patch(ctx, kubeProjPatch, client.MergeFrom(kubeProjCopy)); err != nil {
			return fmt.Errorf(
				"failed to update project '%s/%s' state to '%v': %w",
				kubeProjPatch.Namespace, kubeProjPatch.Name, v1alpha1.ResourcePhaseActive, err,
			)
		}

		return nil
	})
}

// Aruba CMP Project Actions

func (r *ProjectReconciler) cmpDelete(ctx context.Context, _ *v1alpha1.Project, cmpProj *arubatypes.ProjectResponse) error {
	cmpProjList, err := r.ArubaClient.FromProject().Delete(ctx, *cmpProj.Metadata.ID, nil)
	if err != nil {
		return fmt.Errorf("failed to delete project '%s' in Aruba CMP: error: '%w'", *cmpProj.Metadata.Name, err)
	}

	switch cmpProjList.StatusCode {
	case http.StatusOK, http.StatusAccepted, http.StatusNoContent, http.StatusNotFound:
		// Do nothing, we can consider the delete request as successful

	case http.StatusBadRequest:
		return fmt.Errorf(
			"failed to delete project '%s' in Aruba CMP: status_code: %d, error: 'semantic or precondition error'",
			*cmpProj.Metadata.Name, cmpProjList.StatusCode,
		)

	default:
		return fmt.Errorf(
			"failed to delete project '%s' in Aruba CMP: status_code: %d, error: 'internal error'",
			*cmpProj.Metadata.Name, cmpProjList.StatusCode,
		)
	}

	return nil
}

func (r *ProjectReconciler) cmpUpdate(ctx context.Context, kubeProj *v1alpha1.Project, cmpProj *arubatypes.ProjectResponse) error {
	// Seed the request from the current CMP state to preserve any CMP-managed fields,
	// then overwrite only the mutable spec fields.
	request := cmpProjectRequestFromCMP(cmpProj)
	request.Metadata.Tags = kubeProj.Spec.Tags
	request.Properties.Description = &kubeProj.Spec.Description
	request.Properties.Default = kubeProj.Spec.Default

	cmpProjResp, err := r.ArubaClient.FromProject().Update(ctx, kubeProj.Status.ResourceID, *request, nil)
	if err != nil {
		return fmt.Errorf("failed to update project '%s' in Aruba CMP: error: '%w'", kubeProj.Name, err)
	}

	switch cmpProjResp.StatusCode {
	case http.StatusOK, http.StatusAccepted, http.StatusNoContent:
		// Do nothing, we can consider the update request as successful

	case http.StatusBadRequest:
		return fmt.Errorf(
			"failed to update project '%s' in Aruba CMP: status_code: %d, error: 'semantic or precondition error'",
			kubeProj.Name, cmpProjResp.StatusCode,
		)

	default:
		return fmt.Errorf(
			"failed to update project '%s' in Aruba CMP: status_code: %d, error: 'internal error'",
			kubeProj.Name, cmpProjResp.StatusCode,
		)
	}

	return nil
}

func (r *ProjectReconciler) cmpCreate(ctx context.Context, kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) error {
	cmpProjList, err := r.ArubaClient.FromProject().Create(ctx, *cmpProjectRequestFromKube(kubeProj), nil)
	if err != nil {
		return fmt.Errorf("failed to create project '%s' in Aruba CMP: error: '%w'", kubeProj.Name, err)
	}

	switch cmpProjList.StatusCode {
	case http.StatusOK, http.StatusCreated:
		// Do nothing, we can consider the create request as successful

	case http.StatusBadRequest:
		return fmt.Errorf(
			"failed to create project '%s' in Aruba CMP: status_code: %d, error: 'semantic or precondition error'",
			kubeProj.Name, cmpProjList.StatusCode,
		)

	default:
		return fmt.Errorf(
			"failed to create project '%s' in Aruba CMP: status_code: %d, error: 'internal error'",
			kubeProj.Name, cmpProjList.StatusCode,
		)
	}

	return nil
}

// Transition Set Builder

func (r *ProjectReconciler) newTransitionSet() *TransitionSet[*v1alpha1.Project, *arubatypes.ProjectResponse] {
	ts := &TransitionSet[*v1alpha1.Project, *arubatypes.ProjectResponse]{
		defaultRequeue:        NoRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
		defaultRequeueOnError: NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
	}

	// Project should be deleted (but not in "Deleting")
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:           "ProjectShouldBeDeleted",
			kCondition:     kubeProjectShouldDelete,
			aCondition:     cmpProjectExists,
			kAction:        r.kubeMarkToDelete, // Mark as "Deleting + ShallSynchronize"
			requeue:        ShortRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project should be deleted on CMP (marked as "Deleting + ShallSynchronize")
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:              "ProjectShouldBeDeletedOnCMP",
			kCondition:        kubeProjectShouldBeDeletedOnCMP,
			aCondition:        cmpProjectExists,
			aAction:           r.cmpDelete,
			kActionOnASuccess: r.kubeMarkDeleting,
			requeue:           ShortRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError:    LongRequeueAndIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project waiting for deletion on CMP (marked as "Deleting + Synchronizing")
	// but project still exists on CMP
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:           "ProjectWaitingDeletionOnCMP",
			kCondition:     kubeProjectWaitingDeletionOnCMP,
			aCondition:     cmpProjectExists,
			requeue:        LongRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project deletion confirmed on CMP (marked as "Deleting + Synchronizing")
	// but project does not exists on CMP
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:           "ProjectDeletionConfirmedOnCMP",
			kCondition:     kubeProjectWaitingDeletionOnCMP,
			aCondition:     cmpProjectNotExists,
			kAction:        r.kubeMarkDeletingDone,
			requeue:        ShortRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project deletion accomplished (marked as "Deleting + Synchronized")
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:           "ProjectDeletionAccomplished",
			kCondition:     kubeProjectDeletionAcomplished,
			aCondition:     cmpProjectNotExists,
			kAction:        r.kubeMarkDeleted,
			requeue:        ShortRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Generation changed but spec is semantically identical to CMP — just re-stamp ObservedGeneration.
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:           "ProjectSpecAlreadyInSyncWithCMP",
			kCondition:     kubeProjectSpecInSyncWithCMP,
			aCondition:     cmpProjectExists,
			kAction:        r.kubeSetActiveAndSetID, // re-stamps ObservedGeneration, keeps Active+Synchronized
			requeue:        NoRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project spec has changed and needs to be updated on CMP (currently Active + Synchronized)
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:           "ProjectShouldBeUpdated",
			kCondition:     kubeProjectShouldUpdate,
			aCondition:     cmpProjectExists,
			kAction:        r.kubeMarkToUpdate, // Mark as "Updating + ShallSynchronize"
			requeue:        ShortRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project should be updated on CMP (marked as "Updating + ShallSynchronize")
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:              "ProjectShouldBeUpdatedOnCMP",
			kCondition:        kubeProjectShouldBeUpdatedOnCMP,
			aCondition:        cmpProjectExists,
			aAction:           r.cmpUpdate,
			kActionOnASuccess: r.kubeMarkUpdating, // Mark as "Updating + Synchronizing"
			requeue:           ShortRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError:    LongRequeueAndIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project waiting for update on CMP (marked as "Updating + Synchronizing")
	// CMP still diverges from kube spec — keep polling
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:           "ProjectWaitingUpdateOnCMP",
			kCondition:     kubeProjectWaitingUpdateOnCMP,
			aCondition:     cmpProjectExists,
			requeue:        LongRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project update confirmed on CMP (marked as "Updating + Synchronizing")
	// CMP now matches kube spec — advance to Synchronized
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:           "ProjectUpdateConfirmedOnCMP",
			kCondition:     kubeProjectUpdateConfirmedOnCMP,
			aCondition:     cmpProjectExists,
			kAction:        r.kubeMarkUpdatingDone,
			requeue:        ShortRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project update accomplished (marked as "Updating + Synchronized")
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:           "ProjectUpdateAccomplished",
			kCondition:     kubeProjectUpdateAccomplished,
			aCondition:     cmpProjectExists,
			kAction:        r.kubeSetActiveAndSetID,
			requeue:        NoRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project should be created
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:           "ProjectShouldBeCreated",
			kCondition:     kubeProjectIsFirstReconciliation,
			aCondition:     cmpProjectNotExists,
			kAction:        r.kubeMarkToCreate,
			requeue:        ShortRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project should be created in CMP
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:              "ProjectShouldBeCreatedInCMP",
			kCondition:        kubeProjectShouldBeCreatedOnCMP,
			aCondition:        cmpProjectNotExists,
			aAction:           r.cmpCreate,
			kActionOnASuccess: r.kubeMarkCreating,
			requeue:           ShortRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError:    LongRequeueAndIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project waiting creation in CMP
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:           "ProjectWaitingCreationInCMP",
			kCondition:     kubeProjectWaitingCreationInCMP,
			aCondition:     cmpProjectNotExists,
			requeue:        LongRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project creation synchronization accomplished
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:           "ProjectCreationConfirmedOnCMP",
			kCondition:     kubeProjectWaitingCreationInCMP,
			aCondition:     cmpProjectExists,
			kAction:        r.kubeMarkCreatingDone,
			requeue:        ShortRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project creation accomplished
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:           "ProjectCreationAccomplished",
			kCondition:     kubeProjectIsCreatedOnCMP,
			aCondition:     cmpProjectExists,
			kAction:        r.kubeSetActiveAndSetID,
			requeue:        NoRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	return ts
}

// Helper Functions

func (r *ProjectReconciler) kubeSetPhaseAndCondition(ctx context.Context, kubeProj *v1alpha1.Project, phase v1alpha1.ResourcePhase, reason string) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		kubeProjCopy := kubeProj.DeepCopy()
		if err := r.Get(ctx, client.ObjectKeyFromObject(kubeProj), kubeProjCopy); err != nil {
			return err
		}

		kubeProjPatch := kubeProjCopy.DeepCopy()
		kubeProjPatch.Status.Phase = phase

		for i := range kubeProjPatch.Status.Conditions {
			kubeProjPatch.Status.Conditions[i].Status = metav1.ConditionFalse
		}

		meta.SetStatusCondition(
			&kubeProjPatch.Status.Conditions,
			metav1.Condition{
				Type:               string(phase),
				Status:             metav1.ConditionTrue,
				Reason:             reason,
				Message:            fmt.Sprintf("%s %s", string(phase), reason),
				LastTransitionTime: metav1.Now(),
			},
		)

		if err := r.Status().Patch(ctx, kubeProjPatch, client.MergeFrom(kubeProjCopy)); err != nil {
			return fmt.Errorf(
				"failed to update project '%s/%s' state to '%v': %w",
				kubeProjPatch.Namespace, kubeProjPatch.Name, phase, err,
			)
		}

		return nil
	})
}

func cmpProjectRequestFromKube(kubeProj *v1alpha1.Project) *arubatypes.ProjectRequest {
	return &arubatypes.ProjectRequest{
		Metadata: arubatypes.ResourceMetadataRequest{
			Name: kubeProj.Name,
			Tags: kubeProj.Spec.Tags,
		},

		Properties: arubatypes.ProjectPropertiesRequest{
			Description: &kubeProj.Spec.Description,
			Default:     kubeProj.Spec.Default,
		},
	}
}

// cmpProjectRequestFromCMP creates an update request seeded from the current CMP state,
// preserving CMP-managed fields that the operator does not own.
func cmpProjectRequestFromCMP(cmpProj *arubatypes.ProjectResponse) *arubatypes.ProjectRequest {
	if cmpProj == nil {
		return &arubatypes.ProjectRequest{}
	}
	name := ""
	if cmpProj.Metadata.Name != nil {
		name = *cmpProj.Metadata.Name
	}
	tags := make([]string, len(cmpProj.Metadata.Tags))
	copy(tags, cmpProj.Metadata.Tags)
	return &arubatypes.ProjectRequest{
		Metadata: arubatypes.ResourceMetadataRequest{
			Name: name,
			Tags: tags,
		},
		Properties: arubatypes.ProjectPropertiesRequest{
			Description: cmpProj.Properties.Description,
			Default:     cmpProj.Properties.Default,
		},
	}
}

// kubeProjectTagsAreEqual returns true when the kube spec tags and the CMP tags
// contain the same elements regardless of order.
func kubeProjectTagsAreEqual(kubeProj *v1alpha1.Project, cmpTags []string) bool {
	if len(kubeProj.Spec.Tags) != len(cmpTags) {
		return false
	}
	kubeTags := make([]string, len(kubeProj.Spec.Tags))
	copy(kubeTags, kubeProj.Spec.Tags)
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

// kubeProjectNeedsUpdate returns true when at least one mutable spec field differs
// from the current CMP state.
func kubeProjectNeedsUpdate(kubeProj *v1alpha1.Project, cmpProj *arubatypes.ProjectResponse) bool {
	if cmpProj == nil {
		return false
	}
	descriptionDiffers := cmpProj.Properties.Description == nil ||
		kubeProj.Spec.Description != *cmpProj.Properties.Description
	return descriptionDiffers ||
		kubeProj.Spec.Default != cmpProj.Properties.Default ||
		!kubeProjectTagsAreEqual(kubeProj, cmpProj.Metadata.Tags)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ProjectReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Project{}).
		Named("project").
		Complete(r)
}
