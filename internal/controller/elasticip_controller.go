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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/Arubacloud/sdk-go/pkg/aruba"
	arubatypes "github.com/Arubacloud/sdk-go/pkg/types"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
)

const (
	elasticIpFinalizerName = "elasticip.arubacloud.com/finalizer"
)

// ElasticIPReconciler reconciles a ElasticIP object
type ElasticIPReconciler struct {
	*reconciler.Reconciler
	ts *TransitionSet[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]
}

// NewElasticIPReconciler creates a new ElasticIPReconciler
func NewElasticIPReconciler(baseReconciler *reconciler.Reconciler) *ElasticIPReconciler {
	r := &ElasticIPReconciler{
		Reconciler: baseReconciler,
	}

	r.ts = r.newTransitionSet()

	return r
}

// +kubebuilder:rbac:groups=arubacloud.com,resources=elasticips,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=arubacloud.com,resources=elasticips/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=arubacloud.com,resources=elasticips/finalizers,verbs=update
// +kubebuilder:rbac:groups=arubacloud.com,resources=projects,verbs=get;list;watch

func (r *ElasticIPReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.Reconciler.Reconcile(ctx, req, r)
}

func (r *ElasticIPReconciler) Object() reconciler.ResourceObject {
	return &v1alpha1.ElasticIP{}
}

func (r *ElasticIPReconciler) Finalizer() string {
	return elasticIpFinalizerName
}

//nolint:gocyclo // complexity is intentional to maintain locality of behavior
func (r *ElasticIPReconciler) HandleReconcile(ctx context.Context, obj reconciler.ResourceObject) (ctrl.Result, error) {
	kubeEip, ok := obj.(*v1alpha1.ElasticIP)
	if !ok {
		return ctrl.Result{}, errors.New("obj is not a *v1alpha1.ElasticIP")
	}

	logger := log.FromContext(ctx).WithValues("tenant", kubeEip.Spec.Tenant)
	logger.Info("reconciling elastic IP")

	arubaClient, err := r.ArubaClient(kubeEip.Spec.Tenant)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get Aruba client: %w", err)
	}

	if kubeEip.Spec.ProjectReference.Name == "" {
		return ctrl.Result{}, fmt.Errorf("project reference is not valid")
	}

	if kubeEip.GetDeletionTimestamp().IsZero() {
		kubeProject := &v1alpha1.Project{}
		if err := resolveOwnerObject(ctx, r.Client, kubeEip.Spec.ProjectReference, kubeEip.Namespace, kubeProject); err != nil {
			if !apierrors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("resolving parent project for owner reference: %w", err)
			}
			logger.V(1).Info("parent project not found for owner reference setup, skipping", "projectName", kubeEip.Spec.ProjectReference.Name)
		} else {
			requeue, err := ensureOwnerReference(ctx, r.Client, r.Scheme, kubeProject, kubeEip)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("setting owner reference on elasticip: %w", err)
			}
			if requeue {
				return ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
			}
		}
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
			logger.V(1).Info("parent project not found on CMP, requeuing", "projectName", projectName)
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
	// TODO: Remove once CMP API name:eq() filter is fixed (issue https://jira.aruba.it/browse/DEV-66643).
	applyNameFilterToElasticIPList(cmpEipList, eipName, logger)

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
	logger.V(1).Info("CMP elastic IP state", "found", cmpEip != nil, "projectID", prjID)

	ctx = context.WithValue(ctx, projectIDKey, prjID)
	ctx = context.WithValue(ctx, reconciler.ArubaClientKey, arubaClient)
	ctx = log.IntoContext(ctx, logger)

	return r.ts.Run(ctx, kubeEip, cmpEip)
}

// Transition Set Builder

func (r *ElasticIPReconciler) newTransitionSet() *TransitionSet[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse] {
	ts := &TransitionSet[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		defaultRequeue:        NoRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		defaultRequeueOnError: NoRequeueButIgnoreError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	}

	// 0. PhaseTimedOut — safety net: fail if stuck in a transitory phase too long
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		name:           "PhaseTimedOut",
		kCondition:     kubePhaseTimedOut[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		aCondition:     AlwaysTrue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		kAction:        r.kubeSetFailedOnTimeout,
		requeue:        NoRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 1. ShouldBeDeleted
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		name:           "ShouldBeDeleted",
		kCondition:     kubeShouldDelete[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		aCondition:     cmpElasticIpIsFinal,
		kAction:        r.kubeMarkToDelete,
		requeue:        ShortRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 2. ShouldDeleteTimedOut — enter deletion flow for timed-out resources
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		name:           "ShouldDeleteTimedOut",
		kCondition:     kubeShouldDeleteTimedOut[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		aCondition:     AlwaysTrue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		kAction:        r.kubeMarkToDelete,
		requeue:        ShortRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 3. ShouldBeDeletedOnCMP
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		name:              "ShouldBeDeletedOnCMP",
		kCondition:        kubeShouldBeDeletedOnCMP[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		aCondition:        cmpElasticIpIsFinal,
		aAction:           r.cmpDelete,
		kActionOnASuccess: r.kubeMarkDeleting,
		kActionOnAError:   kubeSetErrorMessageOnCMPError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse](r.Client),
		requeue:           ShortRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		requeueOnError:    SmartRequeueOnError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 4. DeletionOnCMPNotNeeded — resource marked for deletion but CMP resource doesn't exist
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		name:           "DeletionOnCMPNotNeeded",
		kCondition:     kubeShouldBeDeletedOnCMP[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		aCondition:     cmpElasticIpNotExists,
		kAction:        r.kubeMarkDeletingDone,
		requeue:        ShortRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 5. WaitingDeletionOnCMP
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		name:           "WaitingDeletionOnCMP",
		kCondition:     kubeWaitingDeletionOnCMP[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		aCondition:     cmpElasticIpIsTransitory,
		requeue:        LongRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 6. DeletionConfirmedOnCMP
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		name:           "DeletionConfirmedOnCMP",
		kCondition:     kubeWaitingDeletionOnCMP[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		aCondition:     cmpElasticIpNotExists,
		kAction:        r.kubeMarkDeletingDone,
		requeue:        ShortRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 7. DeletionAccomplished
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		name:           "DeletionAccomplished",
		kCondition:     kubeDeletionAccomplished[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		aCondition:     cmpElasticIpNotExists,
		kAction:        r.kubeMarkDeleted,
		requeue:        ShortRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 8. HasDeniedChanges — surface immutable field violations before attempting update
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		name:       "HasDeniedChanges",
		kCondition: kubeElasticIPHasDeniedChanges,
		aCondition: cmpElasticIpIsFinal,
		kAction: func(ctx context.Context, kubeEip *v1alpha1.ElasticIP, cmpEip *arubatypes.ElasticIPResponse) error {
			return fmt.Errorf("elasticip update rejected: %w", checkElasticIPDeniedChanges(kubeEip, cmpEip))
		},
		requeue:        NoRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		requeueOnError: LongRequeueAndIgnoreError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 9. SpecAlreadyInSyncWithCMP — generation changed but spec identical to CMP; just re-stamp ObservedGeneration
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		name:           "SpecAlreadyInSyncWithCMP",
		kCondition:     kubeElasticIPSpecInSyncWithCMP,
		aCondition:     cmpElasticIpIsActive,
		kAction:        r.kubeSetActiveAndSetID,
		requeue:        NoRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 10. ShouldBeUpdated — spec changed and CMP is ready
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		name:           "ShouldBeUpdated",
		kCondition:     kubeElasticIPShouldUpdate,
		aCondition:     cmpElasticIpIsFinal,
		kAction:        r.kubeMarkToUpdate,
		requeue:        ShortRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 11. ShouldBeUpdatedOnCMP — send update to CMP
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		name:              "ShouldBeUpdatedOnCMP",
		kCondition:        kubeShouldBeUpdatedOnCMP[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		aCondition:        cmpElasticIpIsFinal,
		aAction:           r.cmpUpdate,
		kActionOnASuccess: r.kubeMarkUpdating,
		kActionOnAError:   kubeSetErrorMessageOnCMPError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse](r.Client),
		requeue:           ShortRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		requeueOnError:    SmartRequeueOnError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 12. WaitingUpdateOnCMP — CMP is still processing the update
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		name:           "WaitingUpdateOnCMP",
		kCondition:     kubeWaitingUpdateOnCMP[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		aCondition:     cmpElasticIpIsTransitory,
		requeue:        LongRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 13. UpdateConfirmedOnCMP — CMP has settled; advance to Synchronized
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		name:           "UpdateConfirmedOnCMP",
		kCondition:     kubeWaitingUpdateOnCMP[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		aCondition:     cmpElasticIpIsFinal,
		kAction:        r.kubeMarkUpdatingDone,
		requeue:        ShortRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 14. UpdateAccomplished — transition back to Active and stamp generation
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		name:           "UpdateAccomplished",
		kCondition:     kubeUpdateAccomplished[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		aCondition:     cmpElasticIpIsActive,
		kAction:        r.kubeSetActiveAndSetID,
		requeue:        NoRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 15. ShouldBeCreated
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		name:           "ShouldBeCreated",
		kCondition:     kubeIsFirstReconciliation[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		aCondition:     cmpElasticIpNotExists,
		kAction:        r.kubeMarkToCreate,
		requeue:        ShortRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 16. ShouldBeCreatedInCMP
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		name:              "ShouldBeCreatedInCMP",
		kCondition:        kubeShouldBeCreatedOnCMP[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		aCondition:        cmpElasticIpNotExists,
		aAction:           r.cmpCreate,
		kActionOnASuccess: r.kubeMarkCreating,
		kActionOnAError:   kubeSetErrorMessageOnCMPError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse](r.Client),
		requeue:           ShortRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		requeueOnError:    SmartRequeueOnError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 17. WaitingCreationInCMP
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		name:           "WaitingCreationInCMP",
		kCondition:     kubeWaitingCreationInCMP[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		aCondition:     cmpElasticIpNotExistsOrTransitory,
		requeue:        LongRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 18. CreationConfirmedOnCMP
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		name:           "CreationConfirmedOnCMP",
		kCondition:     kubeWaitingCreationInCMP[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		aCondition:     cmpElasticIpIsActive,
		kAction:        r.kubeMarkCreatingDone,
		requeue:        ShortRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 19. CreationAccomplished
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		name:           "CreationAccomplished",
		kCondition:     kubeIsCreatedOnCMP[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		aCondition:     cmpElasticIpIsActive,
		kAction:        r.kubeSetActiveAndSetID,
		requeue:        NoRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 20. IsInError
	ts.Add(&AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		name:           "IsInError",
		kCondition:     AlwaysTrue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		aCondition:     cmpElasticIpIsFailed,
		kAction:        r.kubeSetFailed,
		requeue:        NoRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	return ts
}

// Resource-specific condition functions

func kubeElasticIPHasDeniedChanges(kubeEip *v1alpha1.ElasticIP, cmpEip *arubatypes.ElasticIPResponse) bool {
	if !kubeEip.DeletionTimestamp.IsZero() {
		return false
	}
	if cmpEip == nil {
		return false
	}
	return checkElasticIPDeniedChanges(kubeEip, cmpEip) != nil
}

func kubeElasticIPSpecInSyncWithCMP(kubeEip *v1alpha1.ElasticIP, cmpEip *arubatypes.ElasticIPResponse) bool {
	return kubeActiveAndGenerationChanged(kubeEip, cmpEip) &&
		checkElasticIPDeniedChanges(kubeEip, cmpEip) == nil &&
		!kubeElasticIPNeedsUpdate(kubeEip, cmpEip)
}

func kubeElasticIPShouldUpdate(kubeEip *v1alpha1.ElasticIP, cmpEip *arubatypes.ElasticIPResponse) bool {
	return kubeActiveAndGenerationChanged(kubeEip, cmpEip) &&
		checkElasticIPDeniedChanges(kubeEip, cmpEip) == nil &&
		kubeElasticIPNeedsUpdate(kubeEip, cmpEip)
}

func cmpElasticIpNotExists(_ *v1alpha1.ElasticIP, cmpEip *arubatypes.ElasticIPResponse) bool {
	return cmpEip == nil
}

func cmpElasticIpIsFinal(_ *v1alpha1.ElasticIP, cmpEip *arubatypes.ElasticIPResponse) bool {
	if cmpEip == nil || cmpEip.Status.State == nil {
		return false
	}
	return AssessCSPResourceStateNature(&cmpEip.Status) == CSPResourceStateNatureFinal
}

func cmpElasticIpIsTransitory(_ *v1alpha1.ElasticIP, cmpEip *arubatypes.ElasticIPResponse) bool {
	if cmpEip == nil || cmpEip.Status.State == nil {
		return false
	}
	return AssessCSPResourceStateNature(&cmpEip.Status) == CSPResourceStateNatureTransitory
}

func cmpElasticIpNotExistsOrTransitory(_ *v1alpha1.ElasticIP, cmpEip *arubatypes.ElasticIPResponse) bool {
	if cmpEip == nil {
		return true
	}
	if cmpEip.Status.State == nil {
		return false
	}
	return AssessCSPResourceStateNature(&cmpEip.Status) == CSPResourceStateNatureTransitory
}

func cmpElasticIpIsActive(_ *v1alpha1.ElasticIP, cmpEip *arubatypes.ElasticIPResponse) bool {
	if cmpEip == nil || cmpEip.Status.State == nil {
		return false
	}
	switch *cmpEip.Status.State {
	case CSPResourceStateActive, CSPResourceStateNotUsed, CSPResourceStateInUse, CSPResourceStateUsed:
		return true
	}
	return false
}

func cmpElasticIpIsFailed(_ *v1alpha1.ElasticIP, cmpEip *arubatypes.ElasticIPResponse) bool {
	return cmpEip != nil && cmpEip.Status.State != nil && *cmpEip.Status.State == CSPResourceStateFailed
}

// Kube action methods

func (r *ElasticIPReconciler) kubeSetPhaseAndCondition(ctx context.Context, kubeEip *v1alpha1.ElasticIP, phase v1alpha1.ResourcePhase, reason string, _ error) error {
	return setPhaseAndCondition(r.Client, ctx, kubeEip, phase, reason, nil, func(eip *v1alpha1.ElasticIP) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && eip.Status.ProjectID == "" {
			eip.Status.ProjectID = prjID
		}
	})
}

func (r *ElasticIPReconciler) kubeMarkToDelete(ctx context.Context, kubeEip *v1alpha1.ElasticIP, _ *arubatypes.ElasticIPResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeEip, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *ElasticIPReconciler) kubeMarkDeleting(ctx context.Context, kubeEip *v1alpha1.ElasticIP, _ *arubatypes.ElasticIPResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeEip, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *ElasticIPReconciler) kubeMarkDeletingDone(ctx context.Context, kubeEip *v1alpha1.ElasticIP, _ *arubatypes.ElasticIPResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeEip, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *ElasticIPReconciler) kubeMarkDeleted(ctx context.Context, kubeEip *v1alpha1.ElasticIP, _ *arubatypes.ElasticIPResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeEip, v1alpha1.ResourcePhaseDeleted, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *ElasticIPReconciler) kubeMarkToUpdate(ctx context.Context, kubeEip *v1alpha1.ElasticIP, _ *arubatypes.ElasticIPResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeEip, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *ElasticIPReconciler) kubeMarkUpdating(ctx context.Context, kubeEip *v1alpha1.ElasticIP, _ *arubatypes.ElasticIPResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeEip, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *ElasticIPReconciler) kubeMarkUpdatingDone(ctx context.Context, kubeEip *v1alpha1.ElasticIP, _ *arubatypes.ElasticIPResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeEip, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *ElasticIPReconciler) kubeMarkToCreate(ctx context.Context, kubeEip *v1alpha1.ElasticIP, _ *arubatypes.ElasticIPResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeEip, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *ElasticIPReconciler) kubeMarkCreating(ctx context.Context, kubeEip *v1alpha1.ElasticIP, _ *arubatypes.ElasticIPResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeEip, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *ElasticIPReconciler) kubeMarkCreatingDone(ctx context.Context, kubeEip *v1alpha1.ElasticIP, _ *arubatypes.ElasticIPResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeEip, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *ElasticIPReconciler) kubeSetActiveAndSetID(ctx context.Context, kubeEip *v1alpha1.ElasticIP, cmpEip *arubatypes.ElasticIPResponse) error {
	cmpID := ""
	if cmpEip != nil && cmpEip.Metadata.ID != nil {
		cmpID = *cmpEip.Metadata.ID
	}
	return setActiveAndSetID(r.Client, ctx, kubeEip, cmpID, nil, func(eip *v1alpha1.ElasticIP) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && eip.Status.ProjectID != "" {
			eip.Status.ProjectID = prjID
		}
	})
}

func (r *ElasticIPReconciler) kubeSetFailedOnTimeout(ctx context.Context, kubeEip *v1alpha1.ElasticIP, _ *arubatypes.ElasticIPResponse) error {
	return setFailedOnTimeout(r.Client, ctx, kubeEip, func(eip *v1alpha1.ElasticIP) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && eip.Status.ProjectID == "" {
			eip.Status.ProjectID = prjID
		}
	})
}

func (r *ElasticIPReconciler) kubeSetFailed(ctx context.Context, kubeEip *v1alpha1.ElasticIP, _ *arubatypes.ElasticIPResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeEip, v1alpha1.ResourcePhaseFailed, v1alpha1.ConditionReasonSynchronized, nil)
}

// CMP action methods

func (r *ElasticIPReconciler) cmpDelete(ctx context.Context, _ *v1alpha1.ElasticIP, cmpEip *arubatypes.ElasticIPResponse) error {
	prjID := ctx.Value(projectIDKey).(string)
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)

	cmpEipResp, err := arubaClient.FromNetwork().ElasticIPs().Delete(ctx, prjID, *cmpEip.Metadata.ID, nil)
	if err != nil {
		return cmpTransportError("delete", *cmpEip.Metadata.Name, err)
	}
	return cmpCheckResponse("delete", *cmpEip.Metadata.Name, cmpEipResp,
		http.StatusOK, http.StatusAccepted, http.StatusNoContent, http.StatusNotFound)
}

func (r *ElasticIPReconciler) cmpUpdate(ctx context.Context, kubeEip *v1alpha1.ElasticIP, cmpEip *arubatypes.ElasticIPResponse) error {
	prjID := ctx.Value(projectIDKey).(string)
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)

	// Guard: should have been caught by HasDeniedChanges, but double-check.
	if err := checkElasticIPDeniedChanges(kubeEip, cmpEip); err != nil {
		return err
	}

	request := buildElasticIPUpdateRequest(kubeEip, cmpEip)

	cmpEipResp, err := arubaClient.FromNetwork().ElasticIPs().Update(ctx, prjID, *cmpEip.Metadata.ID, *request, nil)
	if err != nil {
		return cmpTransportError("update", kubeEip.Name, err)
	}
	return cmpCheckResponse("update", kubeEip.Name, cmpEipResp,
		http.StatusOK, http.StatusAccepted, http.StatusNoContent)
}

func (r *ElasticIPReconciler) cmpCreate(ctx context.Context, kubeEip *v1alpha1.ElasticIP, _ *arubatypes.ElasticIPResponse) error {
	prjID := ctx.Value(projectIDKey).(string)
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)

	cmpEipResp, err := arubaClient.FromNetwork().ElasticIPs().Create(ctx, prjID, *cmpElasticIPRequestFromKube(kubeEip), nil)
	if err != nil {
		return cmpTransportError("create", kubeEip.Name, err)
	}
	return cmpCheckResponse("create", kubeEip.Name, cmpEipResp,
		http.StatusOK, http.StatusCreated, http.StatusAccepted)
}

// Helper functions

func checkElasticIPDeniedChanges(kubeEip *v1alpha1.ElasticIP, cmpEip *arubatypes.ElasticIPResponse) error {
	if cmpEip == nil {
		return nil
	}

	locationValue := ""
	if cmpEip.Metadata.LocationResponse != nil {
		locationValue = cmpEip.Metadata.LocationResponse.Value
	}
	if kubeEip.Spec.Region != locationValue {
		return fmt.Errorf("%w: %w", ErrNotAllowedChanges, errors.New("change the 'location' is not allowed"))
	}

	return nil
}

func kubeElasticIPNeedsUpdate(kubeEip *v1alpha1.ElasticIP, cmpEip *arubatypes.ElasticIPResponse) bool {
	if cmpEip == nil {
		return false
	}
	if !tagsAreEqual(kubeEip.Spec.Tags, cmpEip.Metadata.Tags) {
		return true
	}
	if kubeEip.Spec.BillingPeriod != cmpEip.Properties.BillingPlan.BillingPeriod {
		return true
	}
	return false
}

func buildElasticIPUpdateRequest(kubeEip *v1alpha1.ElasticIP, cmpEip *arubatypes.ElasticIPResponse) *arubatypes.ElasticIPRequest {
	request := cmpElasticIPRequestFromCMP(cmpEip)
	if request == nil {
		return nil
	}
	tags := make([]string, len(kubeEip.Spec.Tags))
	copy(tags, kubeEip.Spec.Tags)
	request.Metadata.Tags = tags
	request.Properties.BillingPlan.BillingPeriod = kubeEip.Spec.BillingPeriod
	return request
}

func cmpElasticIPRequestFromCMP(cmpEip *arubatypes.ElasticIPResponse) *arubatypes.ElasticIPRequest {
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

func cmpElasticIPRequestFromKube(kubeEip *v1alpha1.ElasticIP) *arubatypes.ElasticIPRequest {
	return &arubatypes.ElasticIPRequest{
		Metadata: arubatypes.RegionalResourceMetadataRequest{
			ResourceMetadataRequest: arubatypes.ResourceMetadataRequest{
				Name: kubeEip.Name,
				Tags: kubeEip.Spec.Tags,
			},
			Location: arubatypes.LocationRequest{Value: kubeEip.Spec.Region},
		},
		Properties: arubatypes.ElasticIPPropertiesRequest{
			BillingPlan: arubatypes.BillingPeriodResource{
				BillingPeriod: kubeEip.Spec.BillingPeriod,
			},
		},
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *ElasticIPReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.ElasticIP{}).
		Named("elasticip").
		Complete(r)
}
