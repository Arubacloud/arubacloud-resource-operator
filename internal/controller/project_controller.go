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
	"log"
	"net/http"
	"slices"

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
	return !kubeProj.DeletionTimestamp.IsZero() &&
		kubeProj.Status.Phase != v1alpha1.ResourcePhaseDeleting &&
		kubeProj.Status.Phase != v1alpha1.ResourcePhaseDeleted
}

func kubeProjectIsDeleting(kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) bool {
	return !kubeProj.DeletionTimestamp.IsZero() &&
		kubeProj.Status.Phase == v1alpha1.ResourcePhaseDeleting
}

func kubeProjectIsFirstReconciliation(kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) bool {
	return kubeProj.DeletionTimestamp.IsZero() &&
		kubeProj.Status.Phase == "" &&
		kubeProj.Status.ResourceID == ""
}

func kubeProjectIsCreating(kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) bool {
	return kubeProj.DeletionTimestamp.IsZero() &&
		kubeProj.Status.Phase == v1alpha1.ResourcePhaseCreating &&
		kubeProj.Status.ResourceID == ""
}

func kubeProjectWasRemoved(kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) bool {
	return kubeProj.DeletionTimestamp.IsZero() &&
		kubeProj.Status.Phase != "" &&
		kubeProj.Status.ResourceID != ""
}

func kubeProjectShouldUpdate(kubeProj *v1alpha1.Project, cmpProj *arubatypes.ProjectResponse) bool {
	return kubeProj.DeletionTimestamp.IsZero() &&
		kubeProj.Status.Phase != v1alpha1.ResourcePhaseUpdating &&
		kubeProjectNeedsUpdate(kubeProj, cmpProj)
}

func kubeProjectIsUpdating(kubeProj *v1alpha1.Project, cmpProj *arubatypes.ProjectResponse) bool {
	return kubeProj.DeletionTimestamp.IsZero() &&
		kubeProj.Status.Phase == v1alpha1.ResourcePhaseUpdating &&
		kubeProjectNeedsUpdate(kubeProj, cmpProj)
}

func kubeProjectHasUpdated(kubeProj *v1alpha1.Project, cmpProj *arubatypes.ProjectResponse) bool {
	return kubeProj.DeletionTimestamp.IsZero() &&
		kubeProj.Status.Phase == v1alpha1.ResourcePhaseUpdating &&
		!kubeProjectNeedsUpdate(kubeProj, cmpProj)
}

// Aruba CMP Project Conditions

func cmpProjectExists(_ *v1alpha1.Project, cmpProj *arubatypes.ProjectResponse) bool {
	return cmpProj != nil
}

func cmpProjectNotExists(_ *v1alpha1.Project, cmpProj *arubatypes.ProjectResponse) bool {
	return cmpProj == nil
}

// Kubernetes Project Actions

func (r *ProjectReconciler) kubeSetDeleting(ctx context.Context, kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) error {
	return r.kubeSetState(ctx, kubeProj, v1alpha1.ResourcePhaseDeleting)
}

func (r *ProjectReconciler) kubeSetDeleted(ctx context.Context, kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) error {
	return r.kubeSetState(ctx, kubeProj, v1alpha1.ResourcePhaseDeleted)
}

func (r *ProjectReconciler) kubeSetCreating(ctx context.Context, kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) error {
	return r.kubeSetState(ctx, kubeProj, v1alpha1.ResourcePhaseCreating)
}

func (r *ProjectReconciler) kubeSetUpdating(ctx context.Context, kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) error {
	return r.kubeSetState(ctx, kubeProj, v1alpha1.ResourcePhaseUpdating)
}

func (r *ProjectReconciler) kubeSetCreatingAndUnsetID(ctx context.Context, kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		kubeProjCopy := kubeProj.DeepCopy()

		if err := r.Get(ctx, client.ObjectKeyFromObject(kubeProjCopy), kubeProjCopy); err != nil {
			return err
		}

		kubeProjCopy.Status.Phase = v1alpha1.ResourcePhaseCreating
		kubeProjCopy.Status.ResourceID = ""

		if err := r.Status().Patch(ctx, kubeProjCopy, client.MergeFrom(kubeProj)); err != nil {
			return fmt.Errorf(
				"failed to update project '%s/%s' state to '%v': %w",
				kubeProjCopy.Namespace, kubeProjCopy.Name, v1alpha1.ResourcePhaseCreating, err,
			)
		}

		return nil
	})
}

func (r *ProjectReconciler) kubeSetActiveAndSetID(ctx context.Context, kubeProj *v1alpha1.Project, cmpProj *arubatypes.ProjectResponse) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		kubeProjCopy := kubeProj.DeepCopy()

		if err := r.Get(ctx, client.ObjectKeyFromObject(kubeProjCopy), kubeProjCopy); err != nil {
			return err
		}

		kubeProjCopy.Status.Phase = v1alpha1.ResourcePhaseActive
		kubeProjCopy.Status.ResourceID = *cmpProj.Metadata.ID

		if err := r.Status().Patch(ctx, kubeProjCopy, client.MergeFrom(kubeProj)); err != nil {
			return fmt.Errorf(
				"failed to update project '%s/%s' state to '%v': %w",
				kubeProjCopy.Namespace, kubeProjCopy.Name, v1alpha1.ResourcePhaseActive, err,
			)
		}

		return nil
	})
}

// Kubernetes Action On Errors

func (r *ProjectReconciler) ResetDeletingStateOnError(ctx context.Context, kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse, err error) error {
	log.Printf("error during deleting project '%s/%s': %v, resetting state to 'Active'", kubeProj.Namespace, kubeProj.Name, err)
	return r.kubeSetState(ctx, kubeProj, v1alpha1.ResourcePhaseActive)

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

func (r *ProjectReconciler) cmpUpdate(ctx context.Context, kubeProj *v1alpha1.Project, cmpProj *arubatypes.ProjectResponse) error {
	request := cmpProjectRequestFromResponse(cmpProj)

	request.Metadata.Tags = kubeProj.Spec.Tags
	request.Properties.Description = &kubeProj.Spec.Description

	cmpProjList, err := r.ArubaClient.FromProject().Update(ctx, *cmpProj.Metadata.ID, *request, nil)
	if err != nil {
		return fmt.Errorf("failed to update project '%s' in Aruba CMP: error: '%w'", kubeProj.Name, err)
	}

	switch cmpProjList.StatusCode {
	case http.StatusOK, http.StatusCreated:
		// Do nothing, we can consider the update request as successful

	case http.StatusBadRequest:
		return fmt.Errorf(
			"failed to update project '%s' in Aruba CMP: status_code: %d, error: 'semantic or precondition error'",
			kubeProj.Name, cmpProjList.StatusCode,
		)

	default:
		return fmt.Errorf(
			"failed to update project '%s' in Aruba CMP: status_code: %d, error: 'internal error'",
			kubeProj.Name, cmpProjList.StatusCode,
		)
	}

	return nil
}

// Transition Set Builder

func (r *ProjectReconciler) newTransitionSet() *TransitionSet[*v1alpha1.Project, *arubatypes.ProjectResponse] {
	ts := &TransitionSet[*v1alpha1.Project, *arubatypes.ProjectResponse]{
		defaultKAction:         NoAction[*v1alpha1.Project, *arubatypes.ProjectResponse],
		defaultAAction:         NoAction[*v1alpha1.Project, *arubatypes.ProjectResponse],
		defaultKActionOnAError: NoActionOnError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		defaultRequeue:         NoRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
		defaultRequeueOnError:  NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
	}

	// Project should be deleted
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:            "ProjectShouldBeDeleted",
			kCondition:      kubeProjectShouldDelete,
			aCondition:      cmpProjectExists,
			kAction:         r.kubeSetDeleting,
			aAction:         r.cmpDelete,
			kActionOnAError: r.ResetDeletingStateOnError,
			requeue:         DefaultRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError:  RequeueAndIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project deletion in progress
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:            "ProjectDeletionInProgress",
			kCondition:      kubeProjectIsDeleting,
			aCondition:      cmpProjectExists,
			kAction:         NoAction[*v1alpha1.Project, *arubatypes.ProjectResponse],
			aAction:         NoAction[*v1alpha1.Project, *arubatypes.ProjectResponse],
			kActionOnAError: NoActionOnError[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeue:         DefaultRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError:  NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project deletion accomplished
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:            "ProjectDeletionAccomplished",
			kCondition:      kubeProjectIsDeleting,
			aCondition:      cmpProjectNotExists,
			kAction:         r.kubeSetDeleted,
			aAction:         NoAction[*v1alpha1.Project, *arubatypes.ProjectResponse],
			kActionOnAError: NoActionOnError[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeue:         NoRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError:  NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project does not exists in both
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:            "ProjectDoesNotExistInBoth",
			kCondition:      kubeProjectIsFirstReconciliation,
			aCondition:      cmpProjectNotExists,
			kAction:         r.kubeSetCreating,
			aAction:         r.cmpCreate,
			kActionOnAError: NoActionOnError[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeue:         DefaultRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError:  RequeueAndIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project does not exists in CMP
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:            "ProjectDoesNotExistInCMP",
			kCondition:      kubeProjectIsCreating,
			aCondition:      cmpProjectNotExists,
			kAction:         NoAction[*v1alpha1.Project, *arubatypes.ProjectResponse],
			aAction:         r.cmpCreate,
			kActionOnAError: NoActionOnError[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeue:         DefaultRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError:  RequeueAndIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project existed but was removed in CMP
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:            "ProjectExistedButWasRemovedFromCMP",
			kCondition:      kubeProjectWasRemoved,
			aCondition:      cmpProjectNotExists,
			kAction:         r.kubeSetCreatingAndUnsetID,
			aAction:         r.cmpCreate,
			kActionOnAError: NoActionOnError[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeue:         DefaultRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError:  RequeueAndIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project creation accomplished
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:            "ProjectCreationAccomplished",
			kCondition:      kubeProjectIsCreating,
			aCondition:      cmpProjectExists,
			kAction:         r.kubeSetActiveAndSetID,
			aAction:         NoAction[*v1alpha1.Project, *arubatypes.ProjectResponse],
			kActionOnAError: NoActionOnError[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeue:         NoRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError:  RequeueAndIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project should be updated
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:            "ProjectShouldBeUpdated",
			kCondition:      kubeProjectShouldUpdate,
			aCondition:      cmpProjectExists,
			kAction:         r.kubeSetUpdating,
			aAction:         r.cmpUpdate,
			kActionOnAError: NoActionOnError[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeue:         DefaultRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError:  RequeueAndIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project updating is in progress
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:            "ProjectUpdatingInProgress",
			kCondition:      kubeProjectIsUpdating,
			aCondition:      cmpProjectExists,
			kAction:         NoAction[*v1alpha1.Project, *arubatypes.ProjectResponse],
			aAction:         NoAction[*v1alpha1.Project, *arubatypes.ProjectResponse],
			kActionOnAError: NoActionOnError[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeue:         DefaultRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError:  RequeueAndIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project updating accomplished
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:            "ProjectUpdatingAccomplished",
			kCondition:      kubeProjectHasUpdated,
			aCondition:      cmpProjectExists,
			kAction:         r.kubeSetActiveAndSetID,
			aAction:         NoAction[*v1alpha1.Project, *arubatypes.ProjectResponse],
			kActionOnAError: NoActionOnError[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeue:         NoRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError:  RequeueAndIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	return ts
}

// Helper Functions

func (r *ProjectReconciler) kubeSetState(ctx context.Context, kubeProj *v1alpha1.Project, state v1alpha1.ResourcePhase) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		kubeProjCopy := kubeProj.DeepCopy()

		if err := r.Get(ctx, client.ObjectKeyFromObject(kubeProjCopy), kubeProjCopy); err != nil {
			return err
		}

		kubeProjCopy.Status.Phase = state

		if err := r.Status().Patch(ctx, kubeProjCopy, client.MergeFrom(kubeProj)); err != nil {
			return fmt.Errorf(
				"failed to update project '%s/%s' state to '%v': %w",
				kubeProjCopy.Namespace, kubeProjCopy.Name, state, err,
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

func kubeProjectTagsAreEqual(kubeProj *v1alpha1.Project, tags []string) bool {
	// TODO: generalize this function
	if len(kubeProj.Spec.Tags) != len(tags) {
		return false
	}

	slices.Sort(kubeProj.Spec.Tags)
	slices.Sort(tags)

	for i, tag := range kubeProj.Spec.Tags {
		if tag != tags[i] {
			return false
		}
	}

	return true
}

func kubeProjectNeedsUpdate(kubeProj *v1alpha1.Project, cmpProj *arubatypes.ProjectResponse) bool {
	return !kubeProjectTagsAreEqual(kubeProj, cmpProj.Metadata.Tags) ||
		kubeProj.Spec.Description != *cmpProj.Properties.Description
}

func cmpProjectRequestFromResponse(response *arubatypes.ProjectResponse) *arubatypes.ProjectRequest {
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
