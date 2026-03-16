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

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	arubatypes "github.com/Arubacloud/sdk-go/pkg/types"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
)

const (
	projectFinalizerName = "project.arubacloud.com/finalizer"
)

var (
	errProjectNotFound = errors.New("project not found")
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

	r.ts = r.newProjectTransisionSet()

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
	// 1 - Convert-back the generic resource to the concrete type
	k8sProject, ok := obj.(*v1alpha1.Project)
	if !ok {
		return ctrl.Result{}, errors.New("obj is not a *v1alpha1.Project") // TODO: better error handling
	}

	// 2 - Create the Aruba search parameters to retrieve the desired resource
	// from Aruba API
	projectName := k8sProject.Name
	projectFilter := fmt.Sprintf(`name:eq("%s")`, projectName)

	// 3 - Chech if the desired project exists in Aruba CMP
	prjResp, err := r.ArubaClient.FromProject().List(ctx, &arubatypes.RequestParameters{Filter: &projectFilter})
	// 3.1 - In case we have some technical issue, so we propagate the error
	if err != nil {
		return ctrl.Result{}, fmt.Errorf( // TODO: better error handling
			"failed to find project in Aruba cloud: %w, project_name: '%s', project_filter: '%s'",
			err, projectName, projectFilter,
		)
	}
	// 3.2 - In case we have some server or business issue, so we propagate
	// the error
	if prjResp.IsError() {
		return ctrl.Result{}, fmt.Errorf( // TODO: better error handling
			"failed to find project in Aruba cloud: status_code: %d, project_name: '%s', project_filter: '%s'",
			prjResp.StatusCode, projectName, projectFilter,
		)
	}

	if prjResp.Data.Total < 0 || prjResp.Data.Total > 1 {
		return ctrl.Result{}, fmt.Errorf( // TODO: better error handling
			"inconsistent data: total: %d, status_code: %d, project_name: '%s', project_filter: '%s'",
			prjResp.Data.Total, prjResp.StatusCode, projectName, projectFilter,
		)
	}

	var arubaProject *arubatypes.ProjectResponse
	if prjResp.Data.Total == 1 {
		arubaProject = &prjResp.Data.Values[0]
	}

	return r.ts.Run(ctx, k8sProject, arubaProject)

	/*
		// 3.3 - In case the project was not found but the object still not have a
		// project id on its status, so we consider that the project is still being
		// created and we requeue the reconciliation
		if prjResp.Data.Total == 0 && k8sProject.Status.ResourceID == "" {

			// must be created the project on Aruba CMP before
			k8sProjectCopy := k8sProject.DeepCopy()
			k8sProjectCopy.Status.Phase = v1alpha1.ResourcePhaseCreating
			if err := r.Status().Patch(ctx, k8sProjectCopy, client.MergeFrom(k8sProject)); err != nil {
				return ctrl.Result{}, fmt.Errorf( // TODO: better error handling
					"failed to update project status to 'creating': %w, project_name: '%s'",
					err, projectName,
				)
			}

			prjResp, err := r.ArubaClient.FromProject().Create(ctx, *projectRequestFromK8s(k8sProject), nil)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf( // TODO: better error handling
					"failed to create project in Aruba cloud: %w, project_name: '%s'",
					err, projectName,
				)
			}

			var status v1alpha1.ResourcePhase
			switch prjResp.StatusCode {
			case http.StatusOK, http.StatusCreated:
				k8sProjectCopy.Status.ResourceID = *prjResp.Data.Metadata.ID
				status = v1alpha1.ResourcePhaseActive
			case http.StatusBadRequest, http.StatusInternalServerError:
				status = v1alpha1.ResourcePhaseFailed
			default:
				return ctrl.Result{}, fmt.Errorf( // TODO: better error handling
					"unexpected status code when creating project in Aruba cloud: status_code: %d, project_name: '%s'",
					prjResp.StatusCode, projectName,
				)
			}

			k8sProjectCopy = k8sProject.DeepCopy()
			k8sProjectCopy.Status.Phase = status
			if err := r.Status().Patch(ctx, k8sProjectCopy, client.MergeFrom(k8sProject)); err != nil {
				return ctrl.Result{}, fmt.Errorf( // TODO: better error handling
					"failed to update project status to '%s': %w, project_name: '%s'",
					status, err, projectName)
			}

			return ctrl.Result{RequeueAfter: reconciler.RequeueAfter}, nil
		}
		// 3.4 - In case we find more then a single project, so we consider as an
		// inconsistency
		if prjResp.Data.Total > 1 {
			return ctrl.Result{}, fmt.Errorf( // TODO: better error handling
				"inconsistent data in project list: expected: 1, found: %d, project_name: '%s', project_filter: '%s'",
				prjResp.Data.Total, projectName, projectFilter,
			)
		}

		if prjResp.Data.Total == 0 && k8sProject.Status.Phase == v1alpha1.ResourcePhaseDeleting {
			k8sProjectCopy := k8sProject.DeepCopy()
			k8sProjectCopy.Status.Phase = v1alpha1.ResourcePhaseDeleted
			if err := r.Client.Status().Patch(ctx, k8sProjectCopy, client.MergeFrom(k8sProject)); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed set project state as 'deleted': %w", err) // TODO: better error handling
			}
			// This branch MUST return
			return ctrl.Result{}, nil
		}

		prjID := *(prjResp.Data.Values[0].Metadata.ID)

		// 3.5 - In case the id of the project retrieved using the project name on
		// the object project reference differs from the project id present in the
		// object status, we consider that the user wants to change the reference
		// project of the block storage and then we block this not allowed
		// operation by returning an error
		if k8sProject.Status.ResourceID != "" && k8sProject.Status.ResourceID != prjID {
			return ctrl.Result{}, fmt.Errorf( // TODO: better error handling
				"inconsistent project id in project: project_name: '%s', project_id: '%s', retrieved_project_id: '%s'",
				projectName, k8sProject.Status.ResourceID, prjID,
			)
		}

		toUpdate := k8sProject.GetDeletionTimestamp().IsZero()
		toDelete := !toUpdate

		if toUpdate {

			arubaPRJ := &prjResp.Data.Values[0]
			updateReq, mustUpdate, err := projectConvertAndCheckForUpdate(k8sProject, arubaPRJ)

			if err != nil {
				return ctrl.Result{}, fmt.Errorf( // TODO: better error handling
					"failed to convert project for update: %w, project_name: '%s'",
					err, projectName,
				)
			}

			if mustUpdate {
				if updateReq == nil {
					return ctrl.Result{}, fmt.Errorf("update required but project request is nil, project_name: '%s'", projectName)
				}
				if k8sProject.Status.Phase != v1alpha1.ResourcePhaseUpdating {
					k8sProjectCopy := k8sProject.DeepCopy()
					k8sProjectCopy.Status.Phase = v1alpha1.ResourcePhaseUpdating
					if err := r.Client.Status().Patch(ctx, k8sProjectCopy, client.MergeFrom(k8sProject)); err != nil {
						return ctrl.Result{}, fmt.Errorf("failed set project state as 'updating': %w", err) // TODO: better error handling
					}
				}

				// 5.3.2 - Request the resource update to the Arube CMP
				updateResp, err := r.ArubaClient.FromProject().Update(ctx, prjID, *updateReq, nil)
				if err != nil {
					return ctrl.Result{}, fmt.Errorf("failed update project in Aruba CMP: %w, project_name: '%s'", err, projectName)
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
					return ctrl.Result{}, fmt.Errorf("failed update project in Aruba CMP: %s, project_name: '%s'", errDetail, projectName)
				}

				// 5.3.3 - Requeue the request to wait the results from the
				// Aruba CSP
				return ctrl.Result{RequeueAfter: reconciler.RequeueAfter}, nil
			}
			if k8sProject.Status.Phase == v1alpha1.ResourcePhaseUpdating {
				k8sProjectCopy := k8sProject.DeepCopy()
				k8sProjectCopy.Status.Phase = v1alpha1.ResourcePhaseActive
				if err := r.Client.Status().Patch(ctx, k8sProjectCopy, client.MergeFrom(k8sProject)); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed set project state as 'active': %w", err) // TODO: better error handling
				}
				// This branch MUST return
				return ctrl.Result{}, nil
			}
		}

		if toDelete {
			bsResp, err := r.ArubaClient.FromProject().Delete(ctx, prjID, nil)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf( // TODO: better error handling
					"failed to delete project in Aruba CMP: %w, project_name: '%s'",
					err, projectName)

			}

			switch bsResp.StatusCode {
			case http.StatusOK, http.StatusAccepted, http.StatusNoContent, http.StatusNotFound:
				// Do nothing, we can consider the delete request as successful
			case http.StatusBadRequest:
				return ctrl.Result{RequeueAfter: reconciler.RequeueAfter}, nil // TODO: better error handling, we can consider to requeue the request in order to retry the delete operation

			default:
				return ctrl.Result{RequeueAfter: reconciler.RequeueAfter}, fmt.Errorf( // TODO: better error handling
					"failed to delete project in Aruba CMP: status_code: %d, project_name: '%s'",
					bsResp.StatusCode, projectName)
			}

			k8sProjectCopy := k8sProject.DeepCopy()
			k8sProjectCopy.Status.Phase = v1alpha1.ResourcePhaseDeleting
			if err := r.Client.Status().Patch(ctx, k8sProjectCopy, client.MergeFrom(k8sProject)); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed set project state as 'deleting': %w", err) // TODO: better error handling
			}
			// This branch MUST return
			return ctrl.Result{RequeueAfter: reconciler.RequeueAfter}, nil
		}

		// 4.4 - In case we do not find the resource on the Aruba CMP
		// we need to understand if we are in the "creating" or "deleting" path
		// checking k8s resource status phase and then we need to react accordingly

		return ctrl.Result{RequeueAfter: reconciler.RequeueAfter}, nil
	*/
}

//
//
//
// Transition Set Functions

//
//
// Kubernetes Project Conditions

func kProjectShouldBeDeleted(k *v1alpha1.Project, _ *arubatypes.ProjectResponse) bool {
	return !k.DeletionTimestamp.IsZero() &&
		k.Status.Phase != v1alpha1.ResourcePhaseDeleting &&
		k.Status.Phase != v1alpha1.ResourcePhaseDeleted
}

func kProjectDeletingInProgress(k *v1alpha1.Project, _ *arubatypes.ProjectResponse) bool {
	return !k.DeletionTimestamp.IsZero() &&
		k.Status.Phase == v1alpha1.ResourcePhaseDeleting
}

func kProjectInFirstReconcilation(k *v1alpha1.Project, _ *arubatypes.ProjectResponse) bool {
	return k.DeletionTimestamp.IsZero() &&
		k.Status.Phase == "" &&
		k.Status.ResourceID == ""
}

func kProjectCreationInitiated(k *v1alpha1.Project, _ *arubatypes.ProjectResponse) bool {
	return k.DeletionTimestamp.IsZero() &&
		k.Status.Phase == v1alpha1.ResourcePhaseCreating &&
		k.Status.ResourceID == ""
}

func kProjectExistedButRemoved(k *v1alpha1.Project, _ *arubatypes.ProjectResponse) bool {
	return k.DeletionTimestamp.IsZero() &&
		k.Status.Phase != "" &&
		k.Status.ResourceID != ""
}

func kProjectShouldBeUpdated(k *v1alpha1.Project, a *arubatypes.ProjectResponse) bool {
	return k.DeletionTimestamp.IsZero() &&
		k.Status.Phase != v1alpha1.ResourcePhaseUpdating &&
		projectCheckForUpdate(k, a)
}

func kProjectIsStillUpdating(k *v1alpha1.Project, a *arubatypes.ProjectResponse) bool {
	return k.DeletionTimestamp.IsZero() &&
		k.Status.Phase == v1alpha1.ResourcePhaseUpdating &&
		projectCheckForUpdate(k, a)
}

func kProjectUpdatingAccomplished(k *v1alpha1.Project, a *arubatypes.ProjectResponse) bool {
	return k.DeletionTimestamp.IsZero() &&
		k.Status.Phase == v1alpha1.ResourcePhaseUpdating &&
		!projectCheckForUpdate(k, a)
}

//
//
// Aruba CMP Project Conditions

func aProjectExixts(_ *v1alpha1.Project, a *arubatypes.ProjectResponse) bool { return a != nil }

func aProjectNotExixts(_ *v1alpha1.Project, a *arubatypes.ProjectResponse) bool { return a == nil }

//
//
// Kubernetes Project Actions

func (r *ProjectReconciler) setProjectToDeletingOnK8s(ctx context.Context, k *v1alpha1.Project, _ *arubatypes.ProjectResponse) error {
	return r.setKProjectState(ctx, k, v1alpha1.ResourcePhaseDeleting)
}

func (r *ProjectReconciler) setProjectToDeletedOnK8s(ctx context.Context, k *v1alpha1.Project, _ *arubatypes.ProjectResponse) error {
	return r.setKProjectState(ctx, k, v1alpha1.ResourcePhaseDeleted)
}

func (r *ProjectReconciler) setProjectToCreatingOnK8s(ctx context.Context, k *v1alpha1.Project, _ *arubatypes.ProjectResponse) error {
	return r.setKProjectState(ctx, k, v1alpha1.ResourcePhaseCreating)
}

func (r *ProjectReconciler) setProjectToUpdatingOnK8s(ctx context.Context, k *v1alpha1.Project, _ *arubatypes.ProjectResponse) error {
	return r.setKProjectState(ctx, k, v1alpha1.ResourcePhaseUpdating)
}

func (r *ProjectReconciler) setProjectToCreatingAndUnsetResourceIDOnK8s(ctx context.Context, k *v1alpha1.Project, _ *arubatypes.ProjectResponse) error {
	kCopy := k.DeepCopy()
	kCopy.Status.Phase = v1alpha1.ResourcePhaseCreating
	kCopy.Status.ResourceID = ""

	if err := r.Status().Patch(ctx, kCopy, client.MergeFrom(k)); err != nil {
		return fmt.Errorf(
			"failed to update project '%s/%s' state to '%v': %w",
			kCopy.Namespace, kCopy.Name, v1alpha1.ResourcePhaseCreating, err,
		)
	}

	return nil
}

func (r *ProjectReconciler) setProjectToActiveAndSetResourceIDOnK8s(ctx context.Context, k *v1alpha1.Project, a *arubatypes.ProjectResponse) error {
	kCopy := k.DeepCopy()
	kCopy.Status.Phase = v1alpha1.ResourcePhaseActive
	kCopy.Status.ResourceID = *a.Metadata.ID

	if err := r.Status().Patch(ctx, kCopy, client.MergeFrom(k)); err != nil {
		return fmt.Errorf(
			"failed to update project '%s/%s' state to '%v': %w",
			kCopy.Namespace, kCopy.Name, v1alpha1.ResourcePhaseActive, err,
		)
	}

	return nil
}

//
//
// Aruba CMP Project Actions

func (r *ProjectReconciler) deleteProjectFromCMP(ctx context.Context, _ *v1alpha1.Project, a *arubatypes.ProjectResponse) error {
	prjResp, err := r.ArubaClient.FromProject().Delete(ctx, *a.Metadata.ID, nil)
	if err != nil {
		return fmt.Errorf("failed to delete project '%s' in Aruba CMP: error: '%w'", *a.Metadata.Name, err)
	}

	switch prjResp.StatusCode {
	case http.StatusOK, http.StatusAccepted, http.StatusNoContent, http.StatusNotFound:
		// Do nothing, we can consider the delete request as successful

	case http.StatusBadRequest:
		return fmt.Errorf(
			"failed to delete project '%s' in Aruba CMP: status_code: %d, error: 'semantic or precondition error'",
			*a.Metadata.Name, prjResp.StatusCode,
		)

	default:
		return fmt.Errorf(
			"failed to delete project '%s' in Aruba CMP: status_code: %d, error: 'internal error'",
			*a.Metadata.Name, prjResp.StatusCode,
		)
	}

	return nil
}

func (r *ProjectReconciler) createProjectInCMP(ctx context.Context, k *v1alpha1.Project, a *arubatypes.ProjectResponse) error {
	prjResp, err := r.ArubaClient.FromProject().Create(ctx, *projectRequestFromK8s(k), nil)
	if err != nil {
		return fmt.Errorf("failed to create project '%s' in Aruba CMP: error: '%w'", k.Name, err)
	}

	switch prjResp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		// Do nothing, we can consider the delete request as successful

	case http.StatusBadRequest:
		return fmt.Errorf(
			"failed to create project '%s' in Aruba CMP: status_code: %d, error: 'semantic or precondition error'",
			k.Name, prjResp.StatusCode,
		)

	default:
		return fmt.Errorf(
			"failed to delete create '%s' in Aruba CMP: status_code: %d, error: 'internal error'",
			k.Name, prjResp.StatusCode,
		)
	}

	return nil
}

func (r *ProjectReconciler) updateProjectInCMP(ctx context.Context, k *v1alpha1.Project, a *arubatypes.ProjectResponse) error {
	request := projectRequestFromResponse(a)

	request.Metadata.Tags = k.Spec.Tags
	request.Properties.Description = &k.Spec.Description

	prjResp, err := r.ArubaClient.FromProject().Update(ctx, *a.Metadata.ID, *request, nil)
	if err != nil {
		return fmt.Errorf("failed to create project '%s' in Aruba CMP: error: '%w'", k.Name, err)
	}

	switch prjResp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		// Do nothing, we can consider the delete request as successful

	case http.StatusBadRequest:
		return fmt.Errorf(
			"failed to create project '%s' in Aruba CMP: status_code: %d, error: 'semantic or precondition error'",
			k.Name, prjResp.StatusCode,
		)

	default:
		return fmt.Errorf(
			"failed to delete create '%s' in Aruba CMP: status_code: %d, error: 'internal error'",
			k.Name, prjResp.StatusCode,
		)
	}

	return nil
}

//
//
// Transition Set Builder

func (r *ProjectReconciler) newProjectTransisionSet() *TransitionSet[*v1alpha1.Project, *arubatypes.ProjectResponse] {
	ts := &TransitionSet[*v1alpha1.Project, *arubatypes.ProjectResponse]{
		defaultKAction: NoAction[*v1alpha1.Project, *arubatypes.ProjectResponse],
		defaultAAction: NoAction[*v1alpha1.Project, *arubatypes.ProjectResponse],
		requeue:        NoRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
		requeueOnError: NoRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
	}

	// Project should be deleted
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:           "project_should_be_deleted",
			kCondition:     kProjectShouldBeDeleted,
			aCondition:     aProjectExixts,
			kAction:        r.setProjectToDeletingOnK8s,
			aAction:        r.deleteProjectFromCMP,
			requeue:        DefaultRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError: DefaultRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project deletion in progress
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:           "project_deletion_in_project",
			kCondition:     kProjectDeletingInProgress,
			aCondition:     aProjectExixts,
			kAction:        NoAction[*v1alpha1.Project, *arubatypes.ProjectResponse],
			aAction:        NoAction[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeue:        DefaultRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError: NoRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project deletion accomplished
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:           "project_deletion_accomplished",
			kCondition:     kProjectDeletingInProgress,
			aCondition:     aProjectNotExixts,
			kAction:        r.setProjectToDeletedOnK8s,
			aAction:        NoAction[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeue:        NoRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError: NoRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project does not exists in both
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:           "project_does_not_exists_in_both",
			kCondition:     kProjectInFirstReconcilation,
			aCondition:     aProjectNotExixts,
			kAction:        r.setProjectToCreatingOnK8s,
			aAction:        r.createProjectInCMP,
			requeue:        DefaultRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError: DefaultRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project does not exists in CMP
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:           "project_does_not_exixts_in_cmp",
			kCondition:     kProjectCreationInitiated,
			aCondition:     aProjectNotExixts,
			kAction:        NoAction[*v1alpha1.Project, *arubatypes.ProjectResponse],
			aAction:        r.createProjectInCMP,
			requeue:        DefaultRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError: DefaultRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project existed but was removed in CMP
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:           "project_existed_but_was_removed_from_cmp",
			kCondition:     kProjectExistedButRemoved,
			aCondition:     aProjectNotExixts,
			kAction:        r.setProjectToCreatingAndUnsetResourceIDOnK8s,
			aAction:        r.createProjectInCMP,
			requeue:        DefaultRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError: DefaultRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project creation accomplished
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:           "project_creation_accomplished",
			kCondition:     kProjectCreationInitiated,
			aCondition:     aProjectExixts,
			kAction:        r.setProjectToActiveAndSetResourceIDOnK8s,
			aAction:        NoAction[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeue:        NoRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError: DefaultRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project should be updated
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:           "project_should_be_updated",
			kCondition:     kProjectShouldBeUpdated,
			aCondition:     aProjectExixts,
			kAction:        r.setProjectToUpdatingOnK8s,
			aAction:        r.updateProjectInCMP,
			requeue:        DefaultRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError: DefaultRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project updating is in progress
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:           "project_updating_in_progress",
			kCondition:     kProjectIsStillUpdating,
			aCondition:     aProjectExixts,
			kAction:        NoAction[*v1alpha1.Project, *arubatypes.ProjectResponse],
			aAction:        NoAction[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeue:        DefaultRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError: DefaultRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project updating accomplished
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:           "project_updating_accomplished",
			kCondition:     kProjectUpdatingAccomplished,
			aCondition:     aProjectExixts,
			kAction:        r.setProjectToActiveAndSetResourceIDOnK8s,
			aAction:        NoAction[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeue:        NoRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError: DefaultRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	return ts
}

//
//
//
// Helper Functions

func (r *ProjectReconciler) setKProjectState(ctx context.Context, k *v1alpha1.Project, state v1alpha1.ResourcePhase) error {
	kCopy := k.DeepCopy()
	kCopy.Status.Phase = state

	if err := r.Status().Patch(ctx, kCopy, client.MergeFrom(k)); err != nil {
		return fmt.Errorf(
			"failed to update project '%s/%s' state to '%v': %w",
			kCopy.Namespace, kCopy.Name, state, err,
		)
	}

	return nil
}

func projectRequestFromK8s(k8sProject *v1alpha1.Project) *arubatypes.ProjectRequest {
	return &arubatypes.ProjectRequest{
		Metadata: arubatypes.ResourceMetadataRequest{
			Name: k8sProject.Name,
			Tags: k8sProject.Spec.Tags,
		},

		Properties: arubatypes.ProjectPropertiesRequest{
			Description: &k8sProject.Spec.Description,
			Default:     k8sProject.Spec.Default,
		},
	}
}

func projectTagsAreEquals(k8sObj *v1alpha1.Project, tags []string) bool {
	// TODO: generalize this function
	if len(k8sObj.Spec.Tags) != len(tags) {
		return false
	}

	slices.Sort(k8sObj.Spec.Tags)
	slices.Sort(tags)

	for i, tag := range k8sObj.Spec.Tags {
		if tag != tags[i] {
			return false
		}
	}

	return true
}

func projectConvertAndCheckForUpdate(
	k8sObj *v1alpha1.Project,
	arubaObj *arubatypes.ProjectResponse,
) (*arubatypes.ProjectRequest, bool, error) {
	request := projectRequestFromResponse(arubaObj)

	//
	// Updating cases
	//
	// We allow the reconciliation to continue when we find the first valid
	// case

	if k8sObj.Spec.Description != *arubaObj.Properties.Description ||
		!projectTagsAreEquals(k8sObj, request.Metadata.Tags) {
		// Return desired state from K8s spec for the update (not current API state)
		return projectRequestFromK8s(k8sObj), true, nil
	}

	// If we do not find any allowed updating condition, so we signal the
	// caller to not proceed the reconciliation
	return nil, false, nil
}

func projectCheckForUpdate(k8sObj *v1alpha1.Project, arubaObj *arubatypes.ProjectResponse) bool {
	return !projectTagsAreEquals(k8sObj, arubaObj.Metadata.Tags) ||
		k8sObj.Spec.Description != *arubaObj.Properties.Description
}

func projectRequestFromResponse(response *arubatypes.ProjectResponse) *arubatypes.ProjectRequest {
	if response == nil {
		return nil
	}
	name := ""
	if response.Metadata.Name != nil {
		name = *response.Metadata.Name
	}
	tags := make([]string, len(response.Metadata.Tags))
	copy(tags, response.Metadata.Tags)
	return &arubatypes.ProjectRequest{
		Metadata: arubatypes.ResourceMetadataRequest{
			Name: name,
			Tags: tags,
		},
		Properties: arubatypes.ProjectPropertiesRequest{
			Description: response.Properties.Description,
			Default:     response.Properties.Default,
		},
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *ProjectReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Project{}).
		Named("project").
		Complete(r)
}
