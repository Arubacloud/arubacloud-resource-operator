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

	arubaclient "github.com/Arubacloud/arubacloud-resource-operator/internal/client"
	arubatypes "github.com/Arubacloud/sdk-go/pkg/types"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
)

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const (
	projectFinalizerName = "project.arubacloud.com/finalizer"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// ProjectReconciler reconciles a Project object
type ProjectReconciler struct {
	*reconciler.Reconciler
	ts *reconciler.TransitionSet[*v1alpha1.Project, *arubatypes.ProjectResponse]
}

// ---------------------------------------------------------------------------
// Constructor
// ---------------------------------------------------------------------------

// NewProjectReconciler creates a new ProjectReconciler
func NewProjectReconciler(baseReconciler *reconciler.Reconciler) *ProjectReconciler {
	r := &ProjectReconciler{
		Reconciler: baseReconciler,
	}

	r.ts = r.newTransitionSet()

	return r
}

// ---------------------------------------------------------------------------
// Interface methods
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// HandleReconcile
// ---------------------------------------------------------------------------

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

	// CMP validation recovery (generation-gated): reset the phase so the normal
	// create/update flow can retry once the user has corrected the spec.
	// Project has no ivs/vs, so only CMP semantic errors can reach ValidationFailed.
	if reconciler.IsCMPValidationFailedAndSpecChanged(kubeProject) {
		resetPhase := v1alpha1.ResourcePhasePending
		if kubeProject.Status.ResourceID != "" {
			resetPhase = v1alpha1.ResourcePhaseActive
		}
		if setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeProject,
			resetPhase, v1alpha1.ConditionReasonSynchronized, nil); setErr != nil {
			return ctrl.Result{}, setErr
		}
		return ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
	}

	return r.ts.Run(ctx, kubeProject, cmpProject)
}

// ---------------------------------------------------------------------------
// newTransitionSet
// ---------------------------------------------------------------------------

func (r *ProjectReconciler) newTransitionSet() *reconciler.TransitionSet[*v1alpha1.Project, *arubatypes.ProjectResponse] {
	ts := &reconciler.TransitionSet[*v1alpha1.Project, *arubatypes.ProjectResponse]{
		DefaultRequeue:        reconciler.NoRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
		DefaultRequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
	}

	// 0. PhaseTimedOut — safety net: fail if stuck in a transitory phase too long
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
		Name:           "PhaseTimedOut",
		KCondition:     reconciler.KubePhaseTimedOut[*v1alpha1.Project, *arubatypes.ProjectResponse],
		ACondition:     reconciler.AlwaysTrue[*v1alpha1.Project, *arubatypes.ProjectResponse],
		KAction:        r.kubeSetFailedOnTimeout,
		Requeue:        reconciler.NoRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
	})

	// 1. ValidationFailedAndDeleting — unblock deletion for resources stuck in any *ValidationFailed state
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
		Name:           "ValidationFailedAndDeleting",
		KCondition:     reconciler.KubeAnyValidationFailedAndDeleting[*v1alpha1.Project, *arubatypes.ProjectResponse],
		ACondition:     reconciler.AlwaysTrue[*v1alpha1.Project, *arubatypes.ProjectResponse],
		KAction:        reconciler.KubeResetValidationFailedForDeletion[*v1alpha1.Project, *arubatypes.ProjectResponse](r.Client),
		Requeue:        reconciler.ShortRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
		RequeueOnError: reconciler.NoRequeueAndPropagateError[*v1alpha1.Project, *arubatypes.ProjectResponse],
	})

	// 2. PendingAndDeleting — resource deleted while still in Pending; skip CMP entirely
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
		Name:       "PendingAndDeleting",
		KCondition: reconciler.KubePendingAndDeleting[*v1alpha1.Project, *arubatypes.ProjectResponse],
		ACondition: reconciler.AlwaysTrue[*v1alpha1.Project, *arubatypes.ProjectResponse],
		KAction:    reconciler.KubeDeleteFromPending[*v1alpha1.Project, *arubatypes.ProjectResponse](r.Client),
		Requeue:    reconciler.NoRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
	})

	// 3. ShouldBeDeleted — DeletionTimestamp set + active → mark Deleting+ShallSynchronize
	ts.Add(
		&reconciler.AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			Name:           "ShouldBeDeleted",
			KCondition:     reconciler.KubeShouldDelete[*v1alpha1.Project, *arubatypes.ProjectResponse],
			ACondition:     cmpProjectExists,
			KAction:        r.kubeMarkToDelete, // Mark as "Deleting + ShallSynchronize"
			Requeue:        reconciler.ShortRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// 4. ShouldDeleteTimedOut — enter deletion flow for timed-out resources (except those that timed out during Deleting)
	ts.Add(
		&reconciler.AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			Name:           "ShouldDeleteTimedOut",
			KCondition:     reconciler.KubeShouldDeleteTimedOut[*v1alpha1.Project, *arubatypes.ProjectResponse],
			ACondition:     reconciler.AlwaysTrue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			KAction:        r.kubeMarkToDelete,
			Requeue:        reconciler.ShortRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// 5. WaitingChildrenDeletion — block CMP delete until all owned K8s children are gone.
	// The kAction explicitly deletes children because the K8s GC only cascades after the
	// owner is fully removed from etcd (impossible while the project finalizer is present).
	ts.Add(
		&reconciler.AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			Name: "WaitingChildrenDeletion",
			KCondition: func(k *v1alpha1.Project, a *arubatypes.ProjectResponse) bool {
				return reconciler.KubeShouldBeDeletedOnCMP(k, a) && r.kubeProjectHasOwnedChildren(k, a)
			},
			ACondition:     cmpProjectExists,
			KAction:        r.kubeProjectDeleteOwnedChildren,
			Requeue:        reconciler.LongRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			RequeueOnError: reconciler.ShortRequeueAndIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// 6. ShouldBeDeletedOnCMP — marked Deleting+ShallSynchronize + CMP exists → dispatch delete
	ts.Add(
		&reconciler.AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			Name:              "ShouldBeDeletedOnCMP",
			KCondition:        reconciler.KubeShouldBeDeletedOnCMP[*v1alpha1.Project, *arubatypes.ProjectResponse],
			ACondition:        cmpProjectExists,
			AAction:           r.cmpDelete,
			KActionOnASuccess: r.kubeMarkDeleting,
			KActionOnAError:   reconciler.KubeSetErrorMessageOnCMPError[*v1alpha1.Project, *arubatypes.ProjectResponse](r.Client),
			Requeue:           reconciler.ShortRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			RequeueOnError:    reconciler.SmartRequeueOnError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// 7. DeletionOnCMPNotNeeded — resource marked for deletion but CMP resource doesn't exist; skip CMP delete
	ts.Add(
		&reconciler.AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			Name:           "DeletionOnCMPNotNeeded",
			KCondition:     reconciler.KubeShouldBeDeletedOnCMP[*v1alpha1.Project, *arubatypes.ProjectResponse],
			ACondition:     cmpProjectNotExists,
			KAction:        r.kubeMarkDeletingDone,
			Requeue:        reconciler.ShortRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// 8. WaitingDeletionOnCMP — marked Deleting+Synchronizing + CMP still exists → poll
	ts.Add(
		&reconciler.AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			Name:           "WaitingDeletionOnCMP",
			KCondition:     reconciler.KubeWaitingDeletionOnCMP[*v1alpha1.Project, *arubatypes.ProjectResponse],
			ACondition:     cmpProjectExists,
			Requeue:        reconciler.LongRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// 9. DeletionConfirmedOnCMP — marked Deleting+Synchronizing + CMP gone → advance to Synchronized
	ts.Add(
		&reconciler.AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			Name:           "DeletionConfirmedOnCMP",
			KCondition:     reconciler.KubeWaitingDeletionOnCMP[*v1alpha1.Project, *arubatypes.ProjectResponse],
			ACondition:     cmpProjectNotExists,
			KAction:        r.kubeMarkDeletingDone,
			Requeue:        reconciler.ShortRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// 10. DeletionAccomplished — marked Deleting+Synchronized + CMP gone → mark Deleted
	ts.Add(
		&reconciler.AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			Name:           "DeletionAccomplished",
			KCondition:     reconciler.KubeDeletionAccomplished[*v1alpha1.Project, *arubatypes.ProjectResponse],
			ACondition:     cmpProjectNotExists,
			KAction:        r.kubeMarkDeleted,
			Requeue:        reconciler.ShortRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// 11. SpecAlreadyInSyncWithCMP — generation changed but spec identical to CMP; just re-stamp ObservedGeneration
	ts.Add(
		&reconciler.AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			Name:           "SpecAlreadyInSyncWithCMP",
			KCondition:     kubeProjectSpecInSyncWithCMP,
			ACondition:     cmpProjectExists,
			KAction:        r.kubeSetActiveAndSetID, // re-stamps ObservedGeneration, keeps Active+Synchronized
			Requeue:        reconciler.NoRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// 12. ShouldBeUpdated — spec changed and CMP is ready
	ts.Add(
		&reconciler.AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			Name:           "ShouldBeUpdated",
			KCondition:     kubeProjectShouldUpdate,
			ACondition:     cmpProjectExists,
			KAction:        r.kubeMarkToUpdate, // Mark as "Updating + ShallSynchronize"
			Requeue:        reconciler.ShortRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// 13. ShouldBeUpdatedOnCMP — send update to CMP
	ts.Add(
		&reconciler.AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			Name:              "ShouldBeUpdatedOnCMP",
			KCondition:        reconciler.KubeShouldBeUpdatedOnCMP[*v1alpha1.Project, *arubatypes.ProjectResponse],
			ACondition:        cmpProjectExists,
			AAction:           r.cmpUpdate,
			KActionOnASuccess: r.kubeMarkUpdating, // Mark as "Updating + Synchronizing"
			KActionOnAError:   reconciler.KubeSetErrorMessageOnCMPError[*v1alpha1.Project, *arubatypes.ProjectResponse](r.Client),
			Requeue:           reconciler.ShortRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			RequeueOnError:    reconciler.SmartRequeueOnError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// 14. WaitingUpdateOnCMP — CMP is still processing the update
	ts.Add(
		&reconciler.AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			Name:           "WaitingUpdateOnCMP",
			KCondition:     kubeProjectWaitingUpdateOnCMP,
			ACondition:     cmpProjectExists,
			Requeue:        reconciler.LongRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// 15. UpdateConfirmedOnCMP — CMP has settled; advance to Synchronized
	ts.Add(
		&reconciler.AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			Name:           "UpdateConfirmedOnCMP",
			KCondition:     kubeProjectUpdateConfirmedOnCMP,
			ACondition:     cmpProjectExists,
			KAction:        r.kubeMarkUpdatingDone,
			Requeue:        reconciler.ShortRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// 16. UpdateAccomplished — transition back to Active and stamp generation
	ts.Add(
		&reconciler.AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			Name:           "UpdateAccomplished",
			KCondition:     reconciler.KubeUpdateAccomplished[*v1alpha1.Project, *arubatypes.ProjectResponse],
			ACondition:     cmpProjectExists,
			KAction:        r.kubeSetActiveAndSetID,
			Requeue:        reconciler.NoRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// 17. ShouldBeCreated — first reconciliation + CMP not found → mark Creating+ShallSynchronize
	ts.Add(
		&reconciler.AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			Name:           "ShouldBeCreated",
			KCondition:     reconciler.KubeIsFirstReconciliation[*v1alpha1.Project, *arubatypes.ProjectResponse],
			ACondition:     cmpProjectNotExists,
			KAction:        r.kubeMarkToCreate,
			Requeue:        reconciler.ShortRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// 18. ShouldBeCreatedInCMP — Creating+ShallSynchronize + CMP not found → dispatch create
	ts.Add(
		&reconciler.AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			Name:              "ShouldBeCreatedInCMP",
			KCondition:        reconciler.KubeShouldBeCreatedOnCMP[*v1alpha1.Project, *arubatypes.ProjectResponse],
			ACondition:        cmpProjectNotExists,
			AAction:           r.cmpCreate,
			KActionOnASuccess: r.kubeMarkCreating,
			KActionOnAError:   reconciler.KubeSetErrorMessageOnCMPError[*v1alpha1.Project, *arubatypes.ProjectResponse](r.Client),
			Requeue:           reconciler.ShortRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			RequeueOnError:    reconciler.SmartRequeueOnError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// 19. WaitingCreationInCMP — Creating+Synchronizing + CMP not found yet → poll
	ts.Add(
		&reconciler.AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			Name:           "WaitingCreationInCMP",
			KCondition:     reconciler.KubeWaitingCreationInCMP[*v1alpha1.Project, *arubatypes.ProjectResponse],
			ACondition:     cmpProjectNotExists,
			Requeue:        reconciler.LongRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// 20. CreationConfirmedOnCMP — Creating+Synchronizing + CMP found → mark Creating+Synchronized
	ts.Add(
		&reconciler.AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			Name:           "CreationConfirmedOnCMP",
			KCondition:     reconciler.KubeWaitingCreationInCMP[*v1alpha1.Project, *arubatypes.ProjectResponse],
			ACondition:     cmpProjectExists,
			KAction:        r.kubeMarkCreatingDone,
			Requeue:        reconciler.ShortRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	// 21. CreationAccomplished — Creating+Synchronized + CMP found → set Active + store ResourceID
	ts.Add(
		&reconciler.AbstractTransition[*v1alpha1.Project, *arubatypes.ProjectResponse]{
			Name:           "CreationAccomplished",
			KCondition:     reconciler.KubeIsCreatedOnCMP[*v1alpha1.Project, *arubatypes.ProjectResponse],
			ACondition:     cmpProjectExists,
			KAction:        r.kubeSetActiveAndSetID,
			Requeue:        reconciler.NoRequeue[*v1alpha1.Project, *arubatypes.ProjectResponse],
			RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Project, *arubatypes.ProjectResponse],
		},
	)

	return ts
}

// ---------------------------------------------------------------------------
// Owned-children helpers
// ---------------------------------------------------------------------------

// kubeProjectHasOwnedChildren returns true when any Kubernetes resource directly owned
// by the project still exists. Used by the WaitingChildrenDeletion transition to prevent
// CMP deletion before all child CMP resources have been cleaned up.
func (r *ProjectReconciler) kubeProjectHasOwnedChildren(k *v1alpha1.Project, _ *arubatypes.ProjectResponse) bool {
	labelKey, _ := ownerLabelKey(r.Scheme, k)
	has, err := hasOwnedChildren(context.Background(), r.Client, k, labelKey,
		&v1alpha1.VPCList{},
		&v1alpha1.BlockStorageList{},
		&v1alpha1.KeyPairList{},
		&v1alpha1.ElasticIPList{},
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
		&v1alpha1.VPCList{},
		&v1alpha1.BlockStorageList{},
		&v1alpha1.KeyPairList{},
		&v1alpha1.ElasticIPList{},
		&v1alpha1.CloudServerList{},
	)
}

// ---------------------------------------------------------------------------
// Condition functions
// ---------------------------------------------------------------------------

func cmpProjectExists(_ *v1alpha1.Project, cmpProj *arubatypes.ProjectResponse) bool {
	return cmpProj != nil
}

func cmpProjectNotExists(_ *v1alpha1.Project, cmpProj *arubatypes.ProjectResponse) bool {
	return cmpProj == nil
}

func kubeProjectSpecInSyncWithCMP(kubeProj *v1alpha1.Project, cmpProj *arubatypes.ProjectResponse) bool {
	return reconciler.KubeActiveAndGenerationChanged(kubeProj, cmpProj) &&
		!kubeProjectNeedsUpdate(kubeProj, cmpProj)
}

func kubeProjectShouldUpdate(kubeProj *v1alpha1.Project, cmpProj *arubatypes.ProjectResponse) bool {
	return reconciler.KubeActiveAndGenerationChanged(kubeProj, cmpProj) &&
		kubeProjectNeedsUpdate(kubeProj, cmpProj)
}

func kubeProjectWaitingUpdateOnCMP(kubeProj *v1alpha1.Project, cmpProj *arubatypes.ProjectResponse) bool {
	return reconciler.KubeWaitingUpdateOnCMP(kubeProj, cmpProj) &&
		kubeProjectNeedsUpdate(kubeProj, cmpProj)
}

func kubeProjectUpdateConfirmedOnCMP(kubeProj *v1alpha1.Project, cmpProj *arubatypes.ProjectResponse) bool {
	return reconciler.KubeWaitingUpdateOnCMP(kubeProj, cmpProj) &&
		!kubeProjectNeedsUpdate(kubeProj, cmpProj)
}

// ---------------------------------------------------------------------------
// Kube status writers
// ---------------------------------------------------------------------------

func (r *ProjectReconciler) kubeSetPhaseAndCondition(ctx context.Context, kubeProj *v1alpha1.Project, phase v1alpha1.ResourcePhase, reason string, _ error) error {
	return reconciler.SetPhaseAndCondition(r.Client, ctx, kubeProj, phase, reason, nil)
}

func (r *ProjectReconciler) kubeSetFailedOnTimeout(ctx context.Context, kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) error {
	return reconciler.SetFailedOnTimeout(r.Client, ctx, kubeProj)
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
	return reconciler.SetActiveAndSetID(r.Client, ctx, kubeProj, cmpID, nil)
}

// ---------------------------------------------------------------------------
// CMP actions
// ---------------------------------------------------------------------------

func (r *ProjectReconciler) cmpDelete(ctx context.Context, _ *v1alpha1.Project, cmpProj *arubatypes.ProjectResponse) error {
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(arubaclient.Client)

	cmpProjList, err := arubaClient.FromProject().Delete(ctx, *cmpProj.Metadata.ID, nil)
	if err != nil {
		return reconciler.CMPTransportError("delete", *cmpProj.Metadata.Name, err)
	}
	return reconciler.CMPCheckResponse("delete", *cmpProj.Metadata.Name, cmpProjList,
		http.StatusOK, http.StatusAccepted, http.StatusNoContent, http.StatusNotFound)
}

func (r *ProjectReconciler) cmpUpdate(ctx context.Context, kubeProj *v1alpha1.Project, cmpProj *arubatypes.ProjectResponse) error {
	// Seed the request from the current CMP state to preserve any CMP-managed fields,
	// then overwrite only the mutable spec fields.
	request := cmpProjectRequestFromCMP(cmpProj)
	request.Metadata.Tags = kubeProj.Spec.Tags
	request.Properties.Description = &kubeProj.Spec.Description
	request.Properties.Default = false
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(arubaclient.Client)

	cmpProjResp, err := arubaClient.FromProject().Update(ctx, kubeProj.Status.ResourceID, *request, nil)
	if err != nil {
		return reconciler.CMPTransportError("update", kubeProj.Name, err)
	}
	return reconciler.CMPCheckResponse("update", kubeProj.Name, cmpProjResp,
		http.StatusOK, http.StatusAccepted, http.StatusNoContent)
}

func (r *ProjectReconciler) cmpCreate(ctx context.Context, kubeProj *v1alpha1.Project, _ *arubatypes.ProjectResponse) error {
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(arubaclient.Client)
	cmpProjResp, err := arubaClient.FromProject().Create(ctx, *cmpProjectRequestFromKube(kubeProj), nil)
	if err != nil {
		return reconciler.CMPTransportError("create", kubeProj.Name, err)
	}
	return reconciler.CMPCheckResponse("create", kubeProj.Name, cmpProjResp, http.StatusOK, http.StatusCreated)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func kubeProjectNeedsUpdate(kubeProj *v1alpha1.Project, cmpProj *arubatypes.ProjectResponse) bool {
	if cmpProj == nil {
		return false
	}
	descriptionDiffers := cmpProj.Properties.Description == nil ||
		kubeProj.Spec.Description != *cmpProj.Properties.Description
	return descriptionDiffers ||
		!reconciler.TagsAreEqual(kubeProj.Spec.Tags, cmpProj.Metadata.Tags)
}

func cmpProjectRequestFromKube(kubeProj *v1alpha1.Project) *arubatypes.ProjectRequest {
	return &arubatypes.ProjectRequest{
		Metadata: arubatypes.ResourceMetadataRequest{
			Name: kubeProj.Name,
			Tags: kubeProj.Spec.Tags,
		},

		Properties: arubatypes.ProjectPropertiesRequest{
			Description: &kubeProj.Spec.Description,
			Default:     false,
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

// ---------------------------------------------------------------------------
// SetupWithManager
// ---------------------------------------------------------------------------

// SetupWithManager sets up the controller with the Manager.
func (r *ProjectReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Project{}).
		Watches(&v1alpha1.VPC{}, handler.EnqueueRequestsFromMapFunc(
			childToParentMapFunc(func(o client.Object) *v1alpha1.ResourceReference {
				if v, ok := o.(*v1alpha1.VPC); ok {
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
		Watches(&v1alpha1.ElasticIP{}, handler.EnqueueRequestsFromMapFunc(
			childToParentMapFunc(func(o client.Object) *v1alpha1.ResourceReference {
				if v, ok := o.(*v1alpha1.ElasticIP); ok {
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
