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

	"github.com/Arubacloud/sdk-go/pkg/aruba"
	arubatypes "github.com/Arubacloud/sdk-go/pkg/types"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
)

const (
	elasticIpFinalizerName = "elasticip.arubacloud.com/finalizer"
)

// ElasticIpReconciler reconciles a ElasticIp object
type ElasticIpReconciler struct {
	*reconciler.Reconciler
	ts *TransitionSet[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse]
}

// NewElasticIpReconciler creates a new ElasticIpReconciler
func NewElasticIpReconciler(baseReconciler *reconciler.Reconciler) *ElasticIpReconciler {
	r := &ElasticIpReconciler{
		Reconciler: baseReconciler,
	}

	r.ts = r.newTransitionSet()

	return r
}

// +kubebuilder:rbac:groups=arubacloud.com,resources=elasticips,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=arubacloud.com,resources=elasticips/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=arubacloud.com,resources=elasticips/finalizers,verbs=update
// +kubebuilder:rbac:groups=arubacloud.com,resources=projects,verbs=get;list;watch

func (r *ElasticIpReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.Reconciler.Reconcile(ctx, req, r)
}

func (r *ElasticIpReconciler) Object() reconciler.ResourceObject {
	return &v1alpha1.ElasticIp{}
}

func (r *ElasticIpReconciler) Finalizer() string {
	return elasticIpFinalizerName
}

func (r *ElasticIpReconciler) HandleReconcile(ctx context.Context, obj reconciler.ResourceObject) (ctrl.Result, error) {
	kubeEip, ok := obj.(*v1alpha1.ElasticIp)
	if !ok {
		return ctrl.Result{}, errors.New("obj is not a *v1alpha1.ElasticIp")
	}

	arubaClient, err := r.ArubaClient(kubeEip.Spec.Tenant)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get Aruba client: %w", err)
	}

	if kubeEip.Spec.ProjectReference.Name == "" {
		return ctrl.Result{}, fmt.Errorf("project reference is not valid")
	}

	eipName, projectName := kubeEip.Name, kubeEip.Spec.ProjectReference.Name
	eipFilter := fmt.Sprintf(`name:eq("%s")`, eipName)
	prjFilter := fmt.Sprintf(`name:eq("%s")`, projectName)

	var prjID string

	if !kubeEip.GetDeletionTimestamp().IsZero() && kubeEip.Status.ProjectID != "" {
		prjID = kubeEip.Status.ProjectID
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
		if cmpProjectList.Data.Total == 0 && kubeEip.Status.ProjectID != "" {
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

	if kubeEip.Status.ProjectID != "" && kubeEip.Status.ProjectID != prjID {
		return ctrl.Result{}, fmt.Errorf(
			"inconsistent project id in elasticip: eip_name: '%s', eip_project_id: '%s', project_name: '%s', project_id: '%s'",
			eipName, kubeEip.Status.ProjectID, projectName, prjID,
		)
	}

	cmpEipList, err := arubaClient.FromNetwork().ElasticIPs().List(ctx, prjID, &arubatypes.RequestParameters{Filter: &eipFilter})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf(
			"failed to find elasticip in Aruba cloud: %w, eip_name: '%s', eip_filter: '%s', project_name: '%s'",
			err, eipName, eipFilter, projectName,
		)
	}
	if cmpEipList.IsError() && cmpEipList.StatusCode != http.StatusNotFound {
		return ctrl.Result{}, fmt.Errorf(
			"failed to find elasticip in Aruba cloud: status_code: %d, eip_name: '%s', project_name: '%s'",
			cmpEipList.StatusCode, eipName, projectName,
		)
	}

	if !cmpEipList.IsError() && (cmpEipList.Data.Total < 0 || cmpEipList.Data.Total > 1) {
		return ctrl.Result{}, fmt.Errorf(
			"inconsistent data in elasticip list: eip_name: '%s', eip_filter: '%s', project_name: '%s', instances: %d",
			eipName, eipFilter, projectName, cmpEipList.Data.Total,
		)
	}

	var cmpEip *arubatypes.ElasticIPResponse
	if cmpEipList.Data != nil && cmpEipList.Data.Total == 1 {
		cmpEip = &cmpEipList.Data.Values[0]
	}

	ctx = context.WithValue(ctx, projectIDKey, prjID)
	ctx = context.WithValue(ctx, reconciler.ArubaClientKey, arubaClient)

	return r.ts.Run(ctx, kubeEip, cmpEip)
}

// Transition Set Builder

func (r *ElasticIpReconciler) newTransitionSet() *TransitionSet[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse] {
	ts := &TransitionSet[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse]{
		defaultRequeue:        NoRequeue[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		defaultRequeueOnError: NoRequeueButIgnoreError[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
	}

	// 0. PhaseTimedOut — safety net: fail if stuck in a transitory phase too long
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse]{
		name:           "PhaseTimedOut",
		kCondition:     kubePhaseTimedOut[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		aCondition:     AlwaysTrue[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		kAction:        r.kubeSetFailedOnTimeout,
		requeue:        NoRequeue[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
	})

	// 1. ShouldBeDeleted
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse]{
		name:           "ShouldBeDeleted",
		kCondition:     kubeShouldDelete[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		aCondition:     cmpElasticIpIsFinal,
		kAction:        r.kubeMarkToDelete,
		requeue:        ShortRequeue[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
	})

	// 2. ShouldDeleteTimedOut — enter deletion flow for timed-out resources
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse]{
		name:           "ShouldDeleteTimedOut",
		kCondition:     kubeShouldDeleteTimedOut[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		aCondition:     AlwaysTrue[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		kAction:        r.kubeMarkToDelete,
		requeue:        ShortRequeue[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
	})

	// 3. ShouldBeDeletedOnCMP
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse]{
		name:              "ShouldBeDeletedOnCMP",
		kCondition:        kubeShouldBeDeletedOnCMP[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		aCondition:        cmpElasticIpIsFinal,
		aAction:           r.cmpDelete,
		kActionOnASuccess: r.kubeMarkDeleting,
		kActionOnAError:   kubeSetErrorMessageOnCMPError[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse](r.Client),
		requeue:           ShortRequeue[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		requeueOnError:    SmartRequeueOnError[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
	})

	// 4. DeletionOnCMPNotNeeded — resource marked for deletion but CMP resource doesn't exist
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse]{
		name:           "DeletionOnCMPNotNeeded",
		kCondition:     kubeShouldBeDeletedOnCMP[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		aCondition:     cmpElasticIpNotExists,
		kAction:        r.kubeMarkDeletingDone,
		requeue:        ShortRequeue[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
	})

	// 5. WaitingDeletionOnCMP
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse]{
		name:           "WaitingDeletionOnCMP",
		kCondition:     kubeWaitingDeletionOnCMP[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		aCondition:     cmpElasticIpIsTransitory,
		requeue:        LongRequeue[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
	})

	// 6. DeletionConfirmedOnCMP
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse]{
		name:           "DeletionConfirmedOnCMP",
		kCondition:     kubeWaitingDeletionOnCMP[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		aCondition:     cmpElasticIpNotExists,
		kAction:        r.kubeMarkDeletingDone,
		requeue:        ShortRequeue[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
	})

	// 7. DeletionAccomplished
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse]{
		name:           "DeletionAccomplished",
		kCondition:     kubeDeletionAccomplished[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		aCondition:     cmpElasticIpNotExists,
		kAction:        r.kubeMarkDeleted,
		requeue:        ShortRequeue[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
	})

	// 8. HasDeniedChanges — surface immutable field violations before attempting update
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse]{
		name:       "HasDeniedChanges",
		kCondition: kubeElasticIpHasDeniedChanges,
		aCondition: cmpElasticIpIsFinal,
		kAction: func(ctx context.Context, kubeEip *v1alpha1.ElasticIp, cmpEip *arubatypes.ElasticIPResponse) error {
			return fmt.Errorf("elasticip update rejected: %w", checkElasticIpDeniedChanges(kubeEip, cmpEip))
		},
		requeue:        NoRequeue[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		requeueOnError: LongRequeueAndIgnoreError[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
	})

	// 9. SpecAlreadyInSyncWithCMP — generation changed but spec identical to CMP; just re-stamp ObservedGeneration
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse]{
		name:           "SpecAlreadyInSyncWithCMP",
		kCondition:     kubeElasticIpSpecInSyncWithCMP,
		aCondition:     cmpElasticIpIsActive,
		kAction:        r.kubeSetActiveAndSetID,
		requeue:        NoRequeue[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
	})

	// 10. ShouldBeUpdated — spec changed and CMP is ready
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse]{
		name:           "ShouldBeUpdated",
		kCondition:     kubeElasticIpShouldUpdate,
		aCondition:     cmpElasticIpIsFinal,
		kAction:        r.kubeMarkToUpdate,
		requeue:        ShortRequeue[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
	})

	// 11. ShouldBeUpdatedOnCMP — send update to CMP
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse]{
		name:              "ShouldBeUpdatedOnCMP",
		kCondition:        kubeShouldBeUpdatedOnCMP[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		aCondition:        cmpElasticIpIsFinal,
		aAction:           r.cmpUpdate,
		kActionOnASuccess: r.kubeMarkUpdating,
		kActionOnAError:   kubeSetErrorMessageOnCMPError[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse](r.Client),
		requeue:           ShortRequeue[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		requeueOnError:    SmartRequeueOnError[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
	})

	// 12. WaitingUpdateOnCMP — CMP is still processing the update
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse]{
		name:           "WaitingUpdateOnCMP",
		kCondition:     kubeWaitingUpdateOnCMP[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		aCondition:     cmpElasticIpIsTransitory,
		requeue:        LongRequeue[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
	})

	// 13. UpdateConfirmedOnCMP — CMP has settled; advance to Synchronized
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse]{
		name:           "UpdateConfirmedOnCMP",
		kCondition:     kubeWaitingUpdateOnCMP[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		aCondition:     cmpElasticIpIsFinal,
		kAction:        r.kubeMarkUpdatingDone,
		requeue:        ShortRequeue[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
	})

	// 14. UpdateAccomplished — transition back to Active and stamp generation
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse]{
		name:           "UpdateAccomplished",
		kCondition:     kubeUpdateAccomplished[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		aCondition:     cmpElasticIpIsActive,
		kAction:        r.kubeSetActiveAndSetID,
		requeue:        NoRequeue[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
	})

	// 15. ShouldBeCreated
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse]{
		name:           "ShouldBeCreated",
		kCondition:     kubeIsFirstReconciliation[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		aCondition:     cmpElasticIpNotExists,
		kAction:        r.kubeMarkToCreate,
		requeue:        ShortRequeue[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
	})

	// 16. ShouldBeCreatedInCMP
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse]{
		name:              "ShouldBeCreatedInCMP",
		kCondition:        kubeShouldBeCreatedOnCMP[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		aCondition:        cmpElasticIpNotExists,
		aAction:           r.cmpCreate,
		kActionOnASuccess: r.kubeMarkCreating,
		kActionOnAError:   kubeSetErrorMessageOnCMPError[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse](r.Client),
		requeue:           ShortRequeue[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		requeueOnError:    SmartRequeueOnError[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
	})

	// 17. WaitingCreationInCMP
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse]{
		name:           "WaitingCreationInCMP",
		kCondition:     kubeWaitingCreationInCMP[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		aCondition:     cmpElasticIpNotExistsOrTransitory,
		requeue:        LongRequeue[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
	})

	// 18. CreationConfirmedOnCMP
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse]{
		name:           "CreationConfirmedOnCMP",
		kCondition:     kubeWaitingCreationInCMP[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		aCondition:     cmpElasticIpIsActive,
		kAction:        r.kubeMarkCreatingDone,
		requeue:        ShortRequeue[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
	})

	// 19. CreationAccomplished
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse]{
		name:           "CreationAccomplished",
		kCondition:     kubeIsCreatedOnCMP[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		aCondition:     cmpElasticIpIsActive,
		kAction:        r.kubeSetActiveAndSetID,
		requeue:        NoRequeue[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
	})

	// 20. IsInError
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse]{
		name:           "IsInError",
		kCondition:     AlwaysTrue[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		aCondition:     cmpElasticIpIsFailed,
		kAction:        r.kubeSetFailed,
		requeue:        NoRequeue[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.ElasticIp, *arubatypes.ElasticIPResponse],
	})

	return ts
}

// Resource-specific condition functions

func kubeElasticIpHasDeniedChanges(kubeEip *v1alpha1.ElasticIp, cmpEip *arubatypes.ElasticIPResponse) bool {
	if !kubeEip.DeletionTimestamp.IsZero() {
		return false
	}
	if cmpEip == nil {
		return false
	}
	return checkElasticIpDeniedChanges(kubeEip, cmpEip) != nil
}

func kubeElasticIpSpecInSyncWithCMP(kubeEip *v1alpha1.ElasticIp, cmpEip *arubatypes.ElasticIPResponse) bool {
	return kubeActiveAndGenerationChanged(kubeEip, cmpEip) &&
		checkElasticIpDeniedChanges(kubeEip, cmpEip) == nil &&
		!kubeElasticIpNeedsUpdate(kubeEip, cmpEip)
}

func kubeElasticIpShouldUpdate(kubeEip *v1alpha1.ElasticIp, cmpEip *arubatypes.ElasticIPResponse) bool {
	return kubeActiveAndGenerationChanged(kubeEip, cmpEip) &&
		checkElasticIpDeniedChanges(kubeEip, cmpEip) == nil &&
		kubeElasticIpNeedsUpdate(kubeEip, cmpEip)
}

func cmpElasticIpNotExists(_ *v1alpha1.ElasticIp, cmpEip *arubatypes.ElasticIPResponse) bool {
	return cmpEip == nil
}

func cmpElasticIpIsFinal(_ *v1alpha1.ElasticIp, cmpEip *arubatypes.ElasticIPResponse) bool {
	if cmpEip == nil || cmpEip.Status.State == nil {
		return false
	}
	return AssesCSPResourceStateNature(&cmpEip.Status) == CSPResourceStateNatureFinal
}

func cmpElasticIpIsTransitory(_ *v1alpha1.ElasticIp, cmpEip *arubatypes.ElasticIPResponse) bool {
	if cmpEip == nil || cmpEip.Status.State == nil {
		return false
	}
	return AssesCSPResourceStateNature(&cmpEip.Status) == CSPResourceStateNatureTransitory
}

func cmpElasticIpNotExistsOrTransitory(_ *v1alpha1.ElasticIp, cmpEip *arubatypes.ElasticIPResponse) bool {
	if cmpEip == nil {
		return true
	}
	if cmpEip.Status.State == nil {
		return false
	}
	return AssesCSPResourceStateNature(&cmpEip.Status) == CSPResourceStateNatureTransitory
}

func cmpElasticIpIsActive(_ *v1alpha1.ElasticIp, cmpEip *arubatypes.ElasticIPResponse) bool {
	if cmpEip == nil || cmpEip.Status.State == nil {
		return false
	}
	switch *cmpEip.Status.State {
	case CSPResourceStateActive, CSPResourceStateNotUsed, CSPResourceStateInUse, CSPResourceStateUsed:
		return true
	}
	return false
}

func cmpElasticIpIsFailed(_ *v1alpha1.ElasticIp, cmpEip *arubatypes.ElasticIPResponse) bool {
	return cmpEip != nil && cmpEip.Status.State != nil && *cmpEip.Status.State == CSPResourceStateFailed
}

// Kube action methods

func (r *ElasticIpReconciler) kubeSetPhaseAndCondition(ctx context.Context, kubeEip *v1alpha1.ElasticIp, phase v1alpha1.ResourcePhase, reason string, actionErr error) error {
	return setPhaseAndCondition(r.Client, ctx, kubeEip, phase, reason, actionErr, func(eip *v1alpha1.ElasticIp) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && eip.Status.ProjectID == "" {
			eip.Status.ProjectID = prjID
		}
	})
}

func (r *ElasticIpReconciler) kubeMarkToDelete(ctx context.Context, kubeEip *v1alpha1.ElasticIp, _ *arubatypes.ElasticIPResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeEip, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *ElasticIpReconciler) kubeMarkDeleting(ctx context.Context, kubeEip *v1alpha1.ElasticIp, _ *arubatypes.ElasticIPResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeEip, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *ElasticIpReconciler) kubeMarkDeletingDone(ctx context.Context, kubeEip *v1alpha1.ElasticIp, _ *arubatypes.ElasticIPResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeEip, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *ElasticIpReconciler) kubeMarkDeleted(ctx context.Context, kubeEip *v1alpha1.ElasticIp, _ *arubatypes.ElasticIPResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeEip, v1alpha1.ResourcePhaseDeleted, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *ElasticIpReconciler) kubeMarkToUpdate(ctx context.Context, kubeEip *v1alpha1.ElasticIp, _ *arubatypes.ElasticIPResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeEip, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *ElasticIpReconciler) kubeMarkUpdating(ctx context.Context, kubeEip *v1alpha1.ElasticIp, _ *arubatypes.ElasticIPResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeEip, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *ElasticIpReconciler) kubeMarkUpdatingDone(ctx context.Context, kubeEip *v1alpha1.ElasticIp, _ *arubatypes.ElasticIPResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeEip, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *ElasticIpReconciler) kubeMarkToCreate(ctx context.Context, kubeEip *v1alpha1.ElasticIp, _ *arubatypes.ElasticIPResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeEip, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *ElasticIpReconciler) kubeMarkCreating(ctx context.Context, kubeEip *v1alpha1.ElasticIp, _ *arubatypes.ElasticIPResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeEip, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *ElasticIpReconciler) kubeMarkCreatingDone(ctx context.Context, kubeEip *v1alpha1.ElasticIp, _ *arubatypes.ElasticIPResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeEip, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *ElasticIpReconciler) kubeSetActiveAndSetID(ctx context.Context, kubeEip *v1alpha1.ElasticIp, cmpEip *arubatypes.ElasticIPResponse) error {
	cmpID := ""
	if cmpEip != nil && cmpEip.Metadata.ID != nil {
		cmpID = *cmpEip.Metadata.ID
	}
	return setActiveAndSetID(r.Client, ctx, kubeEip, cmpID, nil, func(eip *v1alpha1.ElasticIp) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && eip.Status.ProjectID != "" {
			eip.Status.ProjectID = prjID
		}
	})
}

func (r *ElasticIpReconciler) kubeSetFailedOnTimeout(ctx context.Context, kubeEip *v1alpha1.ElasticIp, _ *arubatypes.ElasticIPResponse) error {
	return setFailedOnTimeout(r.Client, ctx, kubeEip, func(eip *v1alpha1.ElasticIp) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && eip.Status.ProjectID == "" {
			eip.Status.ProjectID = prjID
		}
	})
}

func (r *ElasticIpReconciler) kubeSetFailed(ctx context.Context, kubeEip *v1alpha1.ElasticIp, _ *arubatypes.ElasticIPResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeEip, v1alpha1.ResourcePhaseFailed, v1alpha1.ConditionReasonSynchronized, nil)
}

// CMP action methods

func (r *ElasticIpReconciler) cmpDelete(ctx context.Context, _ *v1alpha1.ElasticIp, cmpEip *arubatypes.ElasticIPResponse) error {
	prjID := ctx.Value(projectIDKey).(string)
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)

	cmpEipResp, err := arubaClient.FromNetwork().ElasticIPs().Delete(ctx, prjID, *cmpEip.Metadata.ID, nil)
	if err != nil {
		return cmpTransportError("delete", *cmpEip.Metadata.Name, err)
	}
	return cmpCheckResponse("delete", *cmpEip.Metadata.Name, cmpEipResp,
		http.StatusOK, http.StatusAccepted, http.StatusNoContent, http.StatusNotFound)
}

func (r *ElasticIpReconciler) cmpUpdate(ctx context.Context, kubeEip *v1alpha1.ElasticIp, cmpEip *arubatypes.ElasticIPResponse) error {
	prjID := ctx.Value(projectIDKey).(string)
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)

	// Guard: should have been caught by HasDeniedChanges, but double-check.
	if err := checkElasticIpDeniedChanges(kubeEip, cmpEip); err != nil {
		return err
	}

	request := buildElasticIpUpdateRequest(kubeEip, cmpEip)

	cmpEipResp, err := arubaClient.FromNetwork().ElasticIPs().Update(ctx, prjID, *cmpEip.Metadata.ID, *request, nil)
	if err != nil {
		return cmpTransportError("update", kubeEip.Name, err)
	}
	return cmpCheckResponse("update", kubeEip.Name, cmpEipResp,
		http.StatusOK, http.StatusAccepted, http.StatusNoContent)
}

func (r *ElasticIpReconciler) cmpCreate(ctx context.Context, kubeEip *v1alpha1.ElasticIp, _ *arubatypes.ElasticIPResponse) error {
	prjID := ctx.Value(projectIDKey).(string)
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)

	cmpEipResp, err := arubaClient.FromNetwork().ElasticIPs().Create(ctx, prjID, *cmpElasticIpRequestFromKube(kubeEip), nil)
	if err != nil {
		return cmpTransportError("create", kubeEip.Name, err)
	}
	return cmpCheckResponse("create", kubeEip.Name, cmpEipResp,
		http.StatusOK, http.StatusCreated, http.StatusAccepted)
}

// Helper functions

func checkElasticIpDeniedChanges(kubeEip *v1alpha1.ElasticIp, cmpEip *arubatypes.ElasticIPResponse) error {
	if cmpEip == nil {
		return nil
	}

	locationValue := ""
	if cmpEip.Metadata.LocationResponse != nil {
		locationValue = cmpEip.Metadata.LocationResponse.Value
	}
	if kubeEip.Spec.Location.Value != locationValue {
		return fmt.Errorf("%w: %w", ErrNotAllowedChanges, errors.New("change the 'location' is not allowed"))
	}

	return nil
}

func kubeElasticIpNeedsUpdate(kubeEip *v1alpha1.ElasticIp, cmpEip *arubatypes.ElasticIPResponse) bool {
	if cmpEip == nil {
		return false
	}
	if !tagsAreEqual(kubeEip.Spec.Tags, cmpEip.Metadata.Tags) {
		return true
	}
	if kubeEip.Spec.BillingPlan.BillingPeriod != cmpEip.Properties.BillingPlan.BillingPeriod {
		return true
	}
	return false
}

func buildElasticIpUpdateRequest(kubeEip *v1alpha1.ElasticIp, cmpEip *arubatypes.ElasticIPResponse) *arubatypes.ElasticIPRequest {
	request := cmpElasticIpRequestFromCMP(cmpEip)
	if request == nil {
		return nil
	}
	tags := make([]string, len(kubeEip.Spec.Tags))
	copy(tags, kubeEip.Spec.Tags)
	request.Metadata.Tags = tags
	request.Properties.BillingPlan.BillingPeriod = kubeEip.Spec.BillingPlan.BillingPeriod
	return request
}

func cmpElasticIpRequestFromCMP(cmpEip *arubatypes.ElasticIPResponse) *arubatypes.ElasticIPRequest {
	if cmpEip == nil {
		return nil
	}
	name := ""
	if cmpEip.Metadata.Name != nil {
		name = *cmpEip.Metadata.Name
	}
	tags := make([]string, len(cmpEip.Metadata.Tags))
	copy(tags, cmpEip.Metadata.Tags)
	location := arubatypes.LocationRequest{Value: ""}
	if cmpEip.Metadata.LocationResponse != nil {
		location.Value = cmpEip.Metadata.LocationResponse.Value
	}
	return &arubatypes.ElasticIPRequest{
		Metadata: arubatypes.RegionalResourceMetadataRequest{
			ResourceMetadataRequest: arubatypes.ResourceMetadataRequest{
				Name: name,
				Tags: tags,
			},
			Location: location,
		},
		Properties: arubatypes.ElasticIPPropertiesRequest{
			BillingPlan: arubatypes.BillingPeriodResource{
				BillingPeriod: cmpEip.Properties.BillingPlan.BillingPeriod,
			},
		},
	}
}

func cmpElasticIpRequestFromKube(kubeEip *v1alpha1.ElasticIp) *arubatypes.ElasticIPRequest {
	return &arubatypes.ElasticIPRequest{
		Metadata: arubatypes.RegionalResourceMetadataRequest{
			ResourceMetadataRequest: arubatypes.ResourceMetadataRequest{
				Name: kubeEip.Name,
				Tags: kubeEip.Spec.Tags,
			},
			Location: arubatypes.LocationRequest(kubeEip.Spec.Location),
		},
		Properties: arubatypes.ElasticIPPropertiesRequest{
			BillingPlan: arubatypes.BillingPeriodResource{
				BillingPeriod: kubeEip.Spec.BillingPlan.BillingPeriod,
			},
		},
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *ElasticIpReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.ElasticIp{}).
		Named("elasticip").
		Complete(r)
}
