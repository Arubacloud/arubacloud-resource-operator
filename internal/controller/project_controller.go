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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/Arubacloud/sdk-go/pkg/aruba"
	arubatypes "github.com/Arubacloud/sdk-go/pkg/types"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
)

// kubeProjectHasOwnedChildren returns true when any Kubernetes resource directly owned
// by the project still exists. Used by the WaitingChildrenDeletion transition to prevent
// CMP deletion before all child CMP resources have been cleaned up.
func (r *ProjectReconciler) kubeProjectHasOwnedChildren(k *v1alpha1.Project, _ *arubatypes.ProjectResponse) bool {
	labelKey, _ := ownerLabelKey(r.Scheme, k)
	has, err := hasOwnedChildren(context.Background(), r.Client, k, labelKey,
		&v1alpha1.VpcList{},
		&v1alpha1.BlockStorageList{},
		&v1alpha1.KeyPairList{},
		&v1alpha1.ElasticIpList{},
		&v1alpha1.CloudServerList{},
	)
	if err != nil {
		ctrl.Log.Error(err, "checking owned children for project", "project", k.GetName())
		return true // conservative: assume children exist on error
	}
	return has
}

// kubeProjectDeleteOwnedChildren deletes all K8s children of the project that have not
// yet received a deletionTimestamp. Called by the WaitingChildrenDeletion action.
func (r *ProjectReconciler) kubeProjectDeleteOwnedChildren(ctx context.Context, k *v1alpha1.Project, _ *arubatypes.ProjectResponse) error {
	labelKey, _ := ownerLabelKey(r.Scheme, k)
	return deleteOwnedChildren(ctx, r.Client, k, labelKey,
		&v1alpha1.VpcList{},
		&v1alpha1.BlockStorageList{},
		&v1alpha1.KeyPairList{},
		&v1alpha1.ElasticIpList{},
		&v1alpha1.CloudServerList{},
	)
}

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
// +kubebuilder:rbac:groups=arubacloud.com,resources=vpcs,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups=arubacloud.com,resources=cloudservers,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups=arubacloud.com,resources=keypairs,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups=arubacloud.com,resources=elasticips,verbs=get;list;watch;delete
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

	logger := log.FromContext(ctx).WithValues("tenant", kubeProject.Spec.Tenant)
	logger.Info("reconciling project")

	arubaClient, err := r.ArubaClient(kubeProject.Spec.Tenant)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get Aruba client: %w", err)
	}
	projectName := kubeProject.Name
	projectFilter := fmt.Sprintf(`name:eq("%s")`, projectName)

	cmpProjectList, err := arubaClient.FromProject().List(ctx, &arubatypes.RequestParameters{Filter: &projectFilter})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("listing CMP projects: %w", err)
	}
	if cmpProjectList.IsError() {
		return ctrl.Result{}, fmt.Errorf("listing CMP projects: status %d", cmpProjectList.StatusCode)
	}

	if cmpProjectList.Data.Total < 0 || cmpProjectList.Data.Total > 1 {
		return ctrl.Result{}, fmt.Errorf(
			"inconsistent CMP project list: total %d for %q",
			cmpProjectList.Data.Total, projectName,
		)
	}

	var cmpProject *arubatypes.ProjectResponse
	if cmpProjectList.Data.Total == 1 {
		cmpProject = &cmpProjectList.Data.Values[0]
	}
	logger.V(1).Info("CMP project state", "found", cmpProject != nil)

	ctx = context.WithValue(ctx, reconciler.ArubaClientKey, arubaClient)
	ctx = log.IntoContext(ctx, logger)

	return r.ts.Run(ctx, kubeProject, cmpProject)
}

// Transition Set Builder

func (r *ProjectReconciler) newTransitionSet() *TransitionSet[*v1alpha1.Project, *arubatypes.ProjectResponse] {
	ts := &TransitionSet[*v1alpha1.Project, *arubatypes.ProjectResponse]{
		defaultRequeue:        NoRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
		defaultRequeueOnError: NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
	}

	// 0. PhaseTimedOut — safety net: fail if stuck in a transitory phase too long
	ts.Add(&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
		name:           "PhaseTimedOut",
		kCondition:     kubePhaseTimedOut[*v1alpha1.Project, *arubatypes.ProjectResponse],
		aCondition:     AlwaysTrue[*v1alpha1.Project, *arubatypes.ProjectResponse],
		kAction:        r.kubeSetFailedOnTimeout,
		requeue:        NoRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
	})

	// Project should be deleted (but not in "Deleting")
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:           "ShouldBeDeleted",
			kCondition:     kubeShouldDelete[*v1alpha1.Project, *arubatypes.ProjectResponse],
			aCondition:     cmpProjectExists,
			kAction:        r.kubeMarkToDelete, // Mark as "Deleting + ShallSynchronize"
			requeue:        ShortRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// ShouldDeleteTimedOut — enter deletion flow for timed-out resources (except those that timed out during Deleting)
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:           "ShouldDeleteTimedOut",
			kCondition:     kubeShouldDeleteTimedOut[*v1alpha1.Project, *arubatypes.ProjectResponse],
			aCondition:     AlwaysTrue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			kAction:        r.kubeMarkToDelete,
			requeue:        ShortRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// WaitingChildrenDeletion — block CMP delete until all owned K8s children are gone,
	// preventing the CMP API from rejecting the project deletion due to existing child resources.
	// The kAction explicitly deletes children because the K8s GC only cascades after the
	// owner is fully removed from etcd (impossible while the project finalizer is present).
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name: "WaitingChildrenDeletion",
			kCondition: func(k *v1alpha1.Project, a *arubatypes.ProjectResponse) bool {
				return kubeShouldBeDeletedOnCMP(k, a) && r.kubeProjectHasOwnedChildren(k, a)
			},
			aCondition:     cmpProjectExists,
			kAction:        r.kubeProjectDeleteOwnedChildren,
			requeue:        LongRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError: ShortRequeueAndIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project should be deleted on CMP (marked as "Deleting + ShallSynchronize")
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:              "ShouldBeDeletedOnCMP",
			kCondition:        kubeShouldBeDeletedOnCMP[*v1alpha1.Project, *arubatypes.ProjectResponse],
			aCondition:        cmpProjectExists,
			aAction:           r.cmpDelete,
			kActionOnASuccess: r.kubeMarkDeleting,
			kActionOnAError:   kubeSetErrorMessageOnCMPError[*v1alpha1.Project, *arubatypes.ProjectResponse](r.Client),
			requeue:           ShortRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError:    SmartRequeueOnError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// DeletionOnCMPNotNeeded — resource marked for deletion but CMP resource doesn't exist; skip CMP delete
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:           "DeletionOnCMPNotNeeded",
			kCondition:     kubeShouldBeDeletedOnCMP[*v1alpha1.Project, *arubatypes.ProjectResponse],
			aCondition:     cmpProjectNotExists,
			kAction:        r.kubeMarkDeletingDone,
			requeue:        ShortRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project waiting for deletion on CMP (marked as "Deleting + Synchronizing")
	// but project still exists on CMP
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:           "WaitingDeletionOnCMP",
			kCondition:     kubeWaitingDeletionOnCMP[*v1alpha1.Project, *arubatypes.ProjectResponse],
			aCondition:     cmpProjectExists,
			requeue:        LongRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project deletion confirmed on CMP (marked as "Deleting + Synchronizing")
	// but project does not exists on CMP
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:           "DeletionConfirmedOnCMP",
			kCondition:     kubeWaitingDeletionOnCMP[*v1alpha1.Project, *arubatypes.ProjectResponse],
			aCondition:     cmpProjectNotExists,
			kAction:        r.kubeMarkDeletingDone,
			requeue:        ShortRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project deletion accomplished (marked as "Deleting + Synchronized")
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:           "DeletionAccomplished",
			kCondition:     kubeDeletionAccomplished[*v1alpha1.Project, *arubatypes.ProjectResponse],
			aCondition:     cmpProjectNotExists,
			kAction:        r.kubeMarkDeleted,
			requeue:        ShortRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Generation changed but spec is semantically identical to CMP — just re-stamp ObservedGeneration.
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:           "SpecAlreadyInSyncWithCMP",
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
			name:           "ShouldBeUpdated",
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
			name:              "ShouldBeUpdatedOnCMP",
			kCondition:        kubeShouldBeUpdatedOnCMP[*v1alpha1.Project, *arubatypes.ProjectResponse],
			aCondition:        cmpProjectExists,
			aAction:           r.cmpUpdate,
			kActionOnASuccess: r.kubeMarkUpdating, // Mark as "Updating + Synchronizing"
			kActionOnAError:   kubeSetErrorMessageOnCMPError[*v1alpha1.Project, *arubatypes.ProjectResponse](r.Client),
			requeue:           ShortRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError:    SmartRequeueOnError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project waiting for update on CMP (marked as "Updating + Synchronizing")
	// CMP still diverges from kube spec — keep polling
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:           "WaitingUpdateOnCMP",
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
			name:           "UpdateConfirmedOnCMP",
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
			name:           "UpdateAccomplished",
			kCondition:     kubeUpdateAccomplished[*v1alpha1.Project, *arubatypes.ProjectResponse],
			aCondition:     cmpProjectExists,
			kAction:        r.kubeSetActiveAndSetID,
			requeue:        NoRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project should be created
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:           "ShouldBeCreated",
			kCondition:     kubeIsFirstReconciliation[*v1alpha1.Project, *arubatypes.ProjectResponse],
			aCondition:     cmpProjectNotExists,
			kAction:        r.kubeMarkToCreate,
			requeue:        ShortRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project should be created in CMP
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:              "ShouldBeCreatedInCMP",
			kCondition:        kubeShouldBeCreatedOnCMP[*v1alpha1.Project, *arubatypes.ProjectResponse],
			aCondition:        cmpProjectNotExists,
			aAction:           r.cmpCreate,
			kActionOnASuccess: r.kubeMarkCreating,
			kActionOnAError:   kubeSetErrorMessageOnCMPError[*v1alpha1.Project, *arubatypes.ProjectResponse](r.Client),
			requeue:           ShortRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError:    SmartRequeueOnError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project waiting creation in CMP
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:           "WaitingCreationInCMP",
			kCondition:     kubeWaitingCreationInCMP[*v1alpha1.Project, *arubatypes.ProjectResponse],
			aCondition:     cmpProjectNotExists,
			requeue:        LongRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project creation synchronization accomplished
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:           "CreationConfirmedOnCMP",
			kCondition:     kubeWaitingCreationInCMP[*v1alpha1.Project, *arubatypes.ProjectResponse],
			aCondition:     cmpProjectExists,
			kAction:        r.kubeMarkCreatingDone,
			requeue:        ShortRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// Project creation accomplished
	ts.Add(
		&AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			name:           "CreationAccomplished",
			kCondition:     kubeIsCreatedOnCMP[*v1alpha1.Project, *arubatypes.ProjectResponse],
			aCondition:     cmpProjectExists,
			kAction:        r.kubeSetActiveAndSetID,
			requeue:        NoRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	return ts
}

// Resource-specific condition functions

func kubeProjectSpecInSyncWithCMP(kubeProj *v1alpha1.Project, cmpProj *arubatypes.ProjectResponse) bool {
	return kubeActiveAndGenerationChanged(kubeProj, cmpProj) &&
		!kubeProjectNeedsUpdate(kubeProj, cmpProj)
}

func kubeProjectShouldUpdate(kubeProj *v1alpha1.Project, cmpProj *arubatypes.ProjectResponse) bool {
	return kubeActiveAndGenerationChanged(kubeProj, cmpProj) &&
		kubeProjectNeedsUpdate(kubeProj, cmpProj)
}

func kubeProjectWaitingUpdateOnCMP(kubeProj *v1alpha1.Project, cmpProj *arubatypes.ProjectResponse) bool {
	return kubeWaitingUpdateOnCMP(kubeProj, cmpProj) &&
		kubeProjectNeedsUpdate(kubeProj, cmpProj)
}

func kubeProjectUpdateConfirmedOnCMP(kubeProj *v1alpha1.Project, cmpProj *arubatypes.ProjectResponse) bool {
	return kubeWaitingUpdateOnCMP(kubeProj, cmpProj) &&
		!kubeProjectNeedsUpdate(kubeProj, cmpProj)
}

func cmpProjectExists(_ *v1alpha1.Project, cmpProj *arubatypes.ProjectResponse) bool {
	return cmpProj != nil
}

func cmpProjectNotExists(_ *v1alpha1.Project, cmpProj *arubatypes.ProjectResponse) bool {
	return cmpProj == nil
}

// Kube action methods

func (r *ProjectReconciler) kubeSetPhaseAndCondition(ctx context.Context, kubeProj *v1alpha1.Project, phase v1alpha1.ResourcePhase, reason string, _ error) error {
	return setPhaseAndCondition(r.Client, ctx, kubeProj, phase, reason, nil)
}

func (r *ProjectReconciler) kubeSetFailedOnTimeout(ctx context.Context, kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) error {
	return setFailedOnTimeout(r.Client, ctx, kubeProj)
}

func (r *ProjectReconciler) kubeMarkToDelete(ctx context.Context, kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeProj, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *ProjectReconciler) kubeMarkDeleting(ctx context.Context, kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeProj, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *ProjectReconciler) kubeMarkDeletingDone(ctx context.Context, kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeProj, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *ProjectReconciler) kubeMarkDeleted(ctx context.Context, kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeProj, v1alpha1.ResourcePhaseDeleted, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *ProjectReconciler) kubeMarkToUpdate(ctx context.Context, kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeProj, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *ProjectReconciler) kubeMarkUpdating(ctx context.Context, kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeProj, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *ProjectReconciler) kubeMarkUpdatingDone(ctx context.Context, kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeProj, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *ProjectReconciler) kubeMarkToCreate(ctx context.Context, kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeProj, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *ProjectReconciler) kubeMarkCreating(ctx context.Context, kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeProj, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *ProjectReconciler) kubeMarkCreatingDone(ctx context.Context, kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeProj, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *ProjectReconciler) kubeSetActiveAndSetID(ctx context.Context, kubeProj *v1alpha1.Project, cmpProj *arubatypes.ProjectResponse) error {
	cmpID := ""
	if cmpProj != nil && cmpProj.Metadata.ID != nil {
		cmpID = *cmpProj.Metadata.ID
	}
	return setActiveAndSetID(r.Client, ctx, kubeProj, cmpID, nil)
}

// CMP action methods

func (r *ProjectReconciler) cmpDelete(ctx context.Context, _ *v1alpha1.Project, cmpProj *arubatypes.ProjectResponse) error {
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)

	cmpProjList, err := arubaClient.FromProject().Delete(ctx, *cmpProj.Metadata.ID, nil)
	if err != nil {
		return cmpTransportError("delete", *cmpProj.Metadata.Name, err)
	}
	return cmpCheckResponse("delete", *cmpProj.Metadata.Name, cmpProjList,
		http.StatusOK, http.StatusAccepted, http.StatusNoContent, http.StatusNotFound)
}

func (r *ProjectReconciler) cmpUpdate(ctx context.Context, kubeProj *v1alpha1.Project, cmpProj *arubatypes.ProjectResponse) error {
	// Seed the request from the current CMP state to preserve any CMP-managed fields,
	// then overwrite only the mutable spec fields.
	request := cmpProjectRequestFromCMP(cmpProj)
	request.Metadata.Tags = kubeProj.Spec.Tags
	request.Properties.Description = &kubeProj.Spec.Description
	request.Properties.Default = kubeProj.Spec.Default
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)

	cmpProjResp, err := arubaClient.FromProject().Update(ctx, kubeProj.Status.ResourceID, *request, nil)
	if err != nil {
		return cmpTransportError("update", kubeProj.Name, err)
	}
	return cmpCheckResponse("update", kubeProj.Name, cmpProjResp,
		http.StatusOK, http.StatusAccepted, http.StatusNoContent)
}

func (r *ProjectReconciler) cmpCreate(ctx context.Context, kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) error {
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)
	cmpProjResp, err := arubaClient.FromProject().Create(ctx, *cmpProjectRequestFromKube(kubeProj), nil)
	if err != nil {
		return cmpTransportError("create", kubeProj.Name, err)
	}
	return cmpCheckResponse("create", kubeProj.Name, cmpProjResp, http.StatusOK, http.StatusCreated)
}

// Helper functions

func kubeProjectNeedsUpdate(kubeProj *v1alpha1.Project, cmpProj *arubatypes.ProjectResponse) bool {
	if cmpProj == nil {
		return false
	}
	descriptionDiffers := cmpProj.Properties.Description == nil ||
		kubeProj.Spec.Description != *cmpProj.Properties.Description
	return descriptionDiffers ||
		kubeProj.Spec.Default != cmpProj.Properties.Default ||
		!tagsAreEqual(kubeProj.Spec.Tags, cmpProj.Metadata.Tags)
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

// SetupWithManager sets up the controller with the Manager.
func (r *ProjectReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Project{}).
		Watches(&v1alpha1.Vpc{}, handler.EnqueueRequestsFromMapFunc(
			childToParentMapFunc(func(o client.Object) *v1alpha1.ResourceReference {
				if v, ok := o.(*v1alpha1.Vpc); ok {
					return &v.Spec.ProjectReference
				}
				return nil
			}))).
		Watches(&v1alpha1.BlockStorage{}, handler.EnqueueRequestsFromMapFunc(
			childToParentMapFunc(func(o client.Object) *v1alpha1.ResourceReference {
				if v, ok := o.(*v1alpha1.BlockStorage); ok {
					return &v.Spec.ProjectReference
				}
				return nil
			}))).
		Watches(&v1alpha1.KeyPair{}, handler.EnqueueRequestsFromMapFunc(
			childToParentMapFunc(func(o client.Object) *v1alpha1.ResourceReference {
				if v, ok := o.(*v1alpha1.KeyPair); ok {
					return &v.Spec.ProjectReference
				}
				return nil
			}))).
		Watches(&v1alpha1.ElasticIp{}, handler.EnqueueRequestsFromMapFunc(
			childToParentMapFunc(func(o client.Object) *v1alpha1.ResourceReference {
				if v, ok := o.(*v1alpha1.ElasticIp); ok {
					return &v.Spec.ProjectReference
				}
				return nil
			}))).
		Watches(&v1alpha1.CloudServer{}, handler.EnqueueRequestsFromMapFunc(
			childToParentMapFunc(func(o client.Object) *v1alpha1.ResourceReference {
				if v, ok := o.(*v1alpha1.CloudServer); ok {
					return &v.Spec.ProjectReference
				}
				return nil
			}))).
		Named("project").
		Complete(r)
}
