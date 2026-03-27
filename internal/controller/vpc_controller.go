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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/Arubacloud/sdk-go/pkg/aruba"
	arubatypes "github.com/Arubacloud/sdk-go/pkg/types"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
)

const (
	vpcFinalizerName = "vpc.arubacloud.com/finalizer"
)

// VpcReconciler reconciles a Vpc object
type VpcReconciler struct {
	*reconciler.Reconciler
	ts *TransitionSet[*v1alpha1.Vpc, *arubatypes.VPCResponse]
}

// NewVpcReconciler creates a new VpcReconciler
func NewVpcReconciler(baseReconciler *reconciler.Reconciler) *VpcReconciler {
	r := &VpcReconciler{
		Reconciler: baseReconciler,
	}

	r.ts = r.newTransitionSet()

	return r
}

// +kubebuilder:rbac:groups=arubacloud.com,resources=vpcs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=arubacloud.com,resources=vpcs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=arubacloud.com,resources=vpcs/finalizers,verbs=update
// +kubebuilder:rbac:groups=arubacloud.com,resources=projects,verbs=get;list;watch
// +kubebuilder:rbac:groups=arubacloud.com,resources=subnets,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups=arubacloud.com,resources=securitygroups,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

func (r *VpcReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.Reconciler.Reconcile(ctx, req, r)
}

func (r *VpcReconciler) Object() reconciler.ResourceObject {
	return &v1alpha1.Vpc{}
}

func (r *VpcReconciler) Finalizer() string {
	return vpcFinalizerName
}

//nolint:gocyclo // complexity is intentional to maintain locality of behavior
func (r *VpcReconciler) HandleReconcile(ctx context.Context, obj reconciler.ResourceObject) (ctrl.Result, error) {
	kubeVpc, ok := obj.(*v1alpha1.Vpc)
	if !ok {
		return ctrl.Result{}, errors.New("obj is not a *v1alpha1.Vpc")
	}

	logger := log.FromContext(ctx).WithValues("tenant", kubeVpc.Spec.Tenant)
	logger.Info("reconciling VPC")

	arubaClient, err := r.ArubaClient(kubeVpc.Spec.Tenant)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get Aruba client: %w", err)
	}

	if kubeVpc.Spec.ProjectReference.Name == "" {
		return ctrl.Result{}, fmt.Errorf("project reference is not valid")
	}

	// Set OwnerReference to Project (skipped during deletion — the resource is going away).
	if kubeVpc.GetDeletionTimestamp().IsZero() {
		kubeProject := &v1alpha1.Project{}
		if err := resolveOwnerObject(ctx, r.Client, kubeVpc.Spec.ProjectReference, kubeVpc.Namespace, kubeProject); err != nil {
			if !apierrors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("resolving parent project for owner reference: %w", err)
			}
			logger.V(1).Info("parent project not found for owner reference setup, skipping",
				"projectName", kubeVpc.Spec.ProjectReference.Name)
		} else {
			requeue, err := ensureOwnerReference(ctx, r.Client, r.Scheme, kubeProject, kubeVpc)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("setting owner reference on vpc: %w", err)
			}
			if requeue {
				return ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
			}
		}
	}

	vpcName, projectName := kubeVpc.Name, kubeVpc.Spec.ProjectReference.Name
	vpcFilter := fmt.Sprintf(`name:eq("%s")`, vpcName)
	prjFilter := fmt.Sprintf(`name:eq("%s")`, projectName)

	var prjID string

	if !kubeVpc.GetDeletionTimestamp().IsZero() && kubeVpc.Status.ProjectID != "" {
		prjID = kubeVpc.Status.ProjectID
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
		if cmpProjectList.Data.Total == 0 && kubeVpc.Status.ProjectID != "" {
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

	if kubeVpc.Status.ProjectID != "" && kubeVpc.Status.ProjectID != prjID {
		return ctrl.Result{}, fmt.Errorf(
			"inconsistent project id in vpc: vpc_name: '%s', vpc_project_id: '%s', project_name: '%s', project_id: '%s'",
			vpcName, kubeVpc.Status.ProjectID, projectName, prjID,
		)
	}

	cmpVpcList, err := arubaClient.FromNetwork().VPCs().List(ctx, prjID, &arubatypes.RequestParameters{Filter: &vpcFilter})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf(
			"failed to find vpc in Aruba cloud: %w, vpc_name: '%s', vpc_filter: '%s', project_name: '%s'",
			err, vpcName, vpcFilter, projectName,
		)
	}
	if cmpVpcList.IsError() && cmpVpcList.StatusCode != http.StatusNotFound {
		return ctrl.Result{}, fmt.Errorf(
			"failed to find vpc in Aruba cloud: status_code: %d, vpc_name: '%s', project_name: '%s'",
			cmpVpcList.StatusCode, vpcName, projectName,
		)
	}
	// TODO: Remove once CMP API name:eq() filter is fixed (issue https://jira.aruba.it/browse/DEV-66643).
	applyNameFilterToVPCList(cmpVpcList, vpcName, logger)

	if !cmpVpcList.IsError() && (cmpVpcList.Data.Total < 0 || cmpVpcList.Data.Total > 1) {
		return ctrl.Result{}, fmt.Errorf(
			"inconsistent data in vpc list: vpc_name: '%s', vpc_filter: '%s', project_name: '%s', instances: %d",
			vpcName, vpcFilter, projectName, cmpVpcList.Data.Total,
		)
	}

	var cmpVpc *arubatypes.VPCResponse
	if cmpVpcList.Data != nil && cmpVpcList.Data.Total == 1 {
		cmpVpc = &cmpVpcList.Data.Values[0]
	}
	logger.V(1).Info("CMP VPC state", "found", cmpVpc != nil, "projectID", prjID)

	ctx = context.WithValue(ctx, projectIDKey, prjID)
	ctx = context.WithValue(ctx, reconciler.ArubaClientKey, arubaClient)
	ctx = log.IntoContext(ctx, logger)

	return r.ts.Run(ctx, kubeVpc, cmpVpc)
}

// kubeVpcHasOwnedChildren returns true when any Kubernetes resource directly owned
// by the VPC still exists. Used by the WaitingChildrenDeletion transition.
func (r *VpcReconciler) kubeVpcHasOwnedChildren(k *v1alpha1.Vpc, _ *arubatypes.VPCResponse) bool {
	labelKey, _ := ownerLabelKey(r.Scheme, k)
	has, err := hasOwnedChildren(context.Background(), r.Client, k, labelKey,
		&v1alpha1.SubnetList{},
		&v1alpha1.SecurityGroupList{},
	)
	if err != nil {
		ctrl.Log.Error(err, "checking owned children for vpc", "vpc", k.GetName())
		return true // conservative: assume children exist on error
	}
	return has
}

// kubeVpcDeleteOwnedChildren deletes all K8s children of the VPC that have not yet
// received a deletionTimestamp. Called by the WaitingChildrenDeletion action.
func (r *VpcReconciler) kubeVpcDeleteOwnedChildren(ctx context.Context, k *v1alpha1.Vpc, _ *arubatypes.VPCResponse) error {
	labelKey, _ := ownerLabelKey(r.Scheme, k)
	return deleteOwnedChildren(ctx, r.Client, k, labelKey,
		&v1alpha1.SubnetList{},
		&v1alpha1.SecurityGroupList{},
	)
}

// Transition Set Builder

func (r *VpcReconciler) newTransitionSet() *TransitionSet[*v1alpha1.Vpc, *arubatypes.VPCResponse] {
	ts := &TransitionSet[*v1alpha1.Vpc, *arubatypes.VPCResponse]{
		defaultRequeue:        NoRequeue[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		defaultRequeueOnError: NoRequeueButIgnoreError[*v1alpha1.Vpc, *arubatypes.VPCResponse],
	}

	// 0. PhaseTimedOut — safety net: fail if stuck in a transitory phase too long
	ts.Add(&AbstractTransition[*v1alpha1.Vpc, *arubatypes.VPCResponse]{
		name:           "PhaseTimedOut",
		kCondition:     kubePhaseTimedOut[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		aCondition:     AlwaysTrue[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		kAction:        r.kubeSetFailedOnTimeout,
		requeue:        NoRequeue[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Vpc, *arubatypes.VPCResponse],
	})

	// 1. ShouldBeDeleted
	ts.Add(&AbstractTransition[*v1alpha1.Vpc, *arubatypes.VPCResponse]{
		name:           "ShouldBeDeleted",
		kCondition:     kubeShouldDelete[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		aCondition:     cmpVpcIsFinal,
		kAction:        r.kubeMarkToDelete,
		requeue:        ShortRequeue[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Vpc, *arubatypes.VPCResponse],
	})

	// 2. ShouldDeleteTimedOut — enter deletion flow for timed-out resources (except those that timed out during Deleting)
	ts.Add(&AbstractTransition[*v1alpha1.Vpc, *arubatypes.VPCResponse]{
		name:           "ShouldDeleteTimedOut",
		kCondition:     kubeShouldDeleteTimedOut[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		aCondition:     AlwaysTrue[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		kAction:        r.kubeMarkToDelete,
		requeue:        ShortRequeue[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Vpc, *arubatypes.VPCResponse],
	})

	// 3. WaitingChildrenDeletion — block CMP delete until all owned K8s children are gone.
	// The kAction explicitly deletes children because the K8s GC only cascades after the
	// owner is fully removed from etcd (impossible while the VPC finalizer is present).
	ts.Add(&AbstractTransition[*v1alpha1.Vpc, *arubatypes.VPCResponse]{
		name: "WaitingChildrenDeletion",
		kCondition: func(k *v1alpha1.Vpc, a *arubatypes.VPCResponse) bool {
			return kubeShouldBeDeletedOnCMP(k, a) && r.kubeVpcHasOwnedChildren(k, a)
		},
		aCondition:     cmpVpcIsFinal,
		kAction:        r.kubeVpcDeleteOwnedChildren,
		requeue:        LongRequeue[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		requeueOnError: ShortRequeueAndIgnoreError[*v1alpha1.Vpc, *arubatypes.VPCResponse],
	})

	// 4. ShouldBeDeletedOnCMP
	ts.Add(&AbstractTransition[*v1alpha1.Vpc, *arubatypes.VPCResponse]{
		name:              "ShouldBeDeletedOnCMP",
		kCondition:        kubeShouldBeDeletedOnCMP[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		aCondition:        cmpVpcIsFinal,
		aAction:           r.cmpDelete,
		kActionOnASuccess: r.kubeMarkDeleting,
		kActionOnAError:   kubeSetErrorMessageOnCMPError[*v1alpha1.Vpc, *arubatypes.VPCResponse](r.Client),
		requeue:           ShortRequeue[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		requeueOnError:    SmartRequeueOnError[*v1alpha1.Vpc, *arubatypes.VPCResponse],
	})

	// 5. DeletionOnCMPNotNeeded — resource marked for deletion but CMP resource doesn't exist; skip CMP delete
	ts.Add(&AbstractTransition[*v1alpha1.Vpc, *arubatypes.VPCResponse]{
		name:           "DeletionOnCMPNotNeeded",
		kCondition:     kubeShouldBeDeletedOnCMP[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		aCondition:     cmpVpcNotExists,
		kAction:        r.kubeMarkDeletingDone,
		requeue:        ShortRequeue[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Vpc, *arubatypes.VPCResponse],
	})

	// 6. WaitingDeletionOnCMP
	ts.Add(&AbstractTransition[*v1alpha1.Vpc, *arubatypes.VPCResponse]{
		name:           "WaitingDeletionOnCMP",
		kCondition:     kubeWaitingDeletionOnCMP[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		aCondition:     cmpVpcIsTransitory,
		requeue:        LongRequeue[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Vpc, *arubatypes.VPCResponse],
	})

	// 7. DeletionConfirmedOnCMP
	ts.Add(&AbstractTransition[*v1alpha1.Vpc, *arubatypes.VPCResponse]{
		name:           "DeletionConfirmedOnCMP",
		kCondition:     kubeWaitingDeletionOnCMP[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		aCondition:     cmpVpcNotExists,
		kAction:        r.kubeMarkDeletingDone,
		requeue:        ShortRequeue[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Vpc, *arubatypes.VPCResponse],
	})

	// 8. DeletionAccomplished
	ts.Add(&AbstractTransition[*v1alpha1.Vpc, *arubatypes.VPCResponse]{
		name:           "DeletionAccomplished",
		kCondition:     kubeDeletionAccomplished[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		aCondition:     cmpVpcNotExists,
		kAction:        r.kubeMarkDeleted,
		requeue:        ShortRequeue[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Vpc, *arubatypes.VPCResponse],
	})

	// 8. HasDeniedChanges — surface immutable field violations before attempting update
	ts.Add(&AbstractTransition[*v1alpha1.Vpc, *arubatypes.VPCResponse]{
		name:       "HasDeniedChanges",
		kCondition: kubeVpcHasDeniedChanges,
		aCondition: cmpVpcIsFinal,
		kAction: func(ctx context.Context, kubeVpc *v1alpha1.Vpc, cmpVpc *arubatypes.VPCResponse) error {
			return fmt.Errorf("vpc update rejected: %w", checkVpcDeniedChanges(kubeVpc, cmpVpc))
		},
		requeue:        NoRequeue[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		requeueOnError: LongRequeueAndIgnoreError[*v1alpha1.Vpc, *arubatypes.VPCResponse],
	})

	// 9. SpecAlreadyInSyncWithCMP — generation changed but spec identical to CMP; just re-stamp ObservedGeneration
	ts.Add(&AbstractTransition[*v1alpha1.Vpc, *arubatypes.VPCResponse]{
		name:           "SpecAlreadyInSyncWithCMP",
		kCondition:     kubeVpcSpecInSyncWithCMP,
		aCondition:     cmpVpcIsActive,
		kAction:        r.kubeSetActiveAndSetID,
		requeue:        NoRequeue[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Vpc, *arubatypes.VPCResponse],
	})

	// 10. ShouldBeUpdated — spec changed and CMP is ready
	ts.Add(&AbstractTransition[*v1alpha1.Vpc, *arubatypes.VPCResponse]{
		name:           "ShouldBeUpdated",
		kCondition:     kubeVpcShouldUpdate,
		aCondition:     cmpVpcIsFinal,
		kAction:        r.kubeMarkToUpdate,
		requeue:        ShortRequeue[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Vpc, *arubatypes.VPCResponse],
	})

	// 11. ShouldBeUpdatedOnCMP — send update to CMP
	ts.Add(&AbstractTransition[*v1alpha1.Vpc, *arubatypes.VPCResponse]{
		name:              "ShouldBeUpdatedOnCMP",
		kCondition:        kubeShouldBeUpdatedOnCMP[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		aCondition:        cmpVpcIsFinal,
		aAction:           r.cmpUpdate,
		kActionOnASuccess: r.kubeMarkUpdating,
		kActionOnAError:   kubeSetErrorMessageOnCMPError[*v1alpha1.Vpc, *arubatypes.VPCResponse](r.Client),
		requeue:           ShortRequeue[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		requeueOnError:    SmartRequeueOnError[*v1alpha1.Vpc, *arubatypes.VPCResponse],
	})

	// 12. WaitingUpdateOnCMP — CMP is still processing the update
	ts.Add(&AbstractTransition[*v1alpha1.Vpc, *arubatypes.VPCResponse]{
		name:           "WaitingUpdateOnCMP",
		kCondition:     kubeWaitingUpdateOnCMP[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		aCondition:     cmpVpcIsTransitory,
		requeue:        LongRequeue[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Vpc, *arubatypes.VPCResponse],
	})

	// 13. UpdateConfirmedOnCMP — CMP has settled; advance to Synchronized
	ts.Add(&AbstractTransition[*v1alpha1.Vpc, *arubatypes.VPCResponse]{
		name:           "UpdateConfirmedOnCMP",
		kCondition:     kubeWaitingUpdateOnCMP[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		aCondition:     cmpVpcIsFinal,
		kAction:        r.kubeMarkUpdatingDone,
		requeue:        ShortRequeue[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Vpc, *arubatypes.VPCResponse],
	})

	// 14. UpdateAccomplished — transition back to Active and stamp generation
	ts.Add(&AbstractTransition[*v1alpha1.Vpc, *arubatypes.VPCResponse]{
		name:           "UpdateAccomplished",
		kCondition:     kubeUpdateAccomplished[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		aCondition:     cmpVpcIsActive,
		kAction:        r.kubeSetActiveAndSetID,
		requeue:        NoRequeue[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Vpc, *arubatypes.VPCResponse],
	})

	// 15. ShouldBeCreated
	ts.Add(&AbstractTransition[*v1alpha1.Vpc, *arubatypes.VPCResponse]{
		name:           "ShouldBeCreated",
		kCondition:     kubeIsFirstReconciliation[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		aCondition:     cmpVpcNotExists,
		kAction:        r.kubeMarkToCreate,
		requeue:        ShortRequeue[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Vpc, *arubatypes.VPCResponse],
	})

	// 16. ShouldBeCreatedInCMP
	ts.Add(&AbstractTransition[*v1alpha1.Vpc, *arubatypes.VPCResponse]{
		name:              "ShouldBeCreatedInCMP",
		kCondition:        kubeShouldBeCreatedOnCMP[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		aCondition:        cmpVpcNotExists,
		aAction:           r.cmpCreate,
		kActionOnASuccess: r.kubeMarkCreating,
		kActionOnAError:   kubeSetErrorMessageOnCMPError[*v1alpha1.Vpc, *arubatypes.VPCResponse](r.Client),
		requeue:           ShortRequeue[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		requeueOnError:    SmartRequeueOnError[*v1alpha1.Vpc, *arubatypes.VPCResponse],
	})

	// 17. WaitingCreationInCMP
	ts.Add(&AbstractTransition[*v1alpha1.Vpc, *arubatypes.VPCResponse]{
		name:           "WaitingCreationInCMP",
		kCondition:     kubeWaitingCreationInCMP[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		aCondition:     cmpVpcNotExistsOrTransitory,
		requeue:        LongRequeue[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Vpc, *arubatypes.VPCResponse],
	})

	// 18. CreationConfirmedOnCMP
	ts.Add(&AbstractTransition[*v1alpha1.Vpc, *arubatypes.VPCResponse]{
		name:           "CreationConfirmedOnCMP",
		kCondition:     kubeWaitingCreationInCMP[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		aCondition:     cmpVpcIsActive,
		kAction:        r.kubeMarkCreatingDone,
		requeue:        ShortRequeue[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Vpc, *arubatypes.VPCResponse],
	})

	// 19. CreationAccomplished
	ts.Add(&AbstractTransition[*v1alpha1.Vpc, *arubatypes.VPCResponse]{
		name:           "CreationAccomplished",
		kCondition:     kubeIsCreatedOnCMP[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		aCondition:     cmpVpcIsActive,
		kAction:        r.kubeSetActiveAndSetID,
		requeue:        NoRequeue[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Vpc, *arubatypes.VPCResponse],
	})

	// 20. IsInError
	ts.Add(&AbstractTransition[*v1alpha1.Vpc, *arubatypes.VPCResponse]{
		name:           "IsInError",
		kCondition:     AlwaysTrue[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		aCondition:     cmpVpcIsFailed,
		kAction:        r.kubeSetFailed,
		requeue:        NoRequeue[*v1alpha1.Vpc, *arubatypes.VPCResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Vpc, *arubatypes.VPCResponse],
	})

	return ts
}

// Resource-specific condition functions

func kubeVpcHasDeniedChanges(kubeVpc *v1alpha1.Vpc, cmpVpc *arubatypes.VPCResponse) bool {
	if !kubeVpc.DeletionTimestamp.IsZero() {
		return false
	}
	if cmpVpc == nil {
		return false
	}
	return checkVpcDeniedChanges(kubeVpc, cmpVpc) != nil
}

func kubeVpcSpecInSyncWithCMP(kubeVpc *v1alpha1.Vpc, cmpVpc *arubatypes.VPCResponse) bool {
	return kubeActiveAndGenerationChanged(kubeVpc, cmpVpc) &&
		checkVpcDeniedChanges(kubeVpc, cmpVpc) == nil &&
		!kubeVpcNeedsUpdate(kubeVpc, cmpVpc)
}

func kubeVpcShouldUpdate(kubeVpc *v1alpha1.Vpc, cmpVpc *arubatypes.VPCResponse) bool {
	return kubeActiveAndGenerationChanged(kubeVpc, cmpVpc) &&
		checkVpcDeniedChanges(kubeVpc, cmpVpc) == nil &&
		kubeVpcNeedsUpdate(kubeVpc, cmpVpc)
}

func cmpVpcNotExists(_ *v1alpha1.Vpc, cmpVpc *arubatypes.VPCResponse) bool {
	return cmpVpc == nil
}

func cmpVpcIsFinal(_ *v1alpha1.Vpc, cmpVpc *arubatypes.VPCResponse) bool {
	if cmpVpc == nil || cmpVpc.Status.State == nil {
		return false
	}
	return AssesCSPResourceStateNature(&cmpVpc.Status) == CSPResourceStateNatureFinal
}

func cmpVpcIsTransitory(_ *v1alpha1.Vpc, cmpVpc *arubatypes.VPCResponse) bool {
	if cmpVpc == nil || cmpVpc.Status.State == nil {
		return false
	}
	return AssesCSPResourceStateNature(&cmpVpc.Status) == CSPResourceStateNatureTransitory
}

func cmpVpcNotExistsOrTransitory(_ *v1alpha1.Vpc, cmpVpc *arubatypes.VPCResponse) bool {
	if cmpVpc == nil {
		return true
	}
	if cmpVpc.Status.State == nil {
		return false
	}
	return AssesCSPResourceStateNature(&cmpVpc.Status) == CSPResourceStateNatureTransitory
}

func cmpVpcIsActive(_ *v1alpha1.Vpc, cmpVpc *arubatypes.VPCResponse) bool {
	return cmpVpc != nil && cmpVpc.Status.State != nil &&
		*cmpVpc.Status.State == CSPResourceStateActive
}

func cmpVpcIsFailed(_ *v1alpha1.Vpc, cmpVpc *arubatypes.VPCResponse) bool {
	return cmpVpc != nil && cmpVpc.Status.State != nil && *cmpVpc.Status.State == CSPResourceStateFailed
}

// Kube action methods

func (r *VpcReconciler) kubeSetPhaseAndCondition(ctx context.Context, kubeVpc *v1alpha1.Vpc, phase v1alpha1.ResourcePhase, reason string, _ error) error {
	return setPhaseAndCondition(r.Client, ctx, kubeVpc, phase, reason, nil, func(vpc *v1alpha1.Vpc) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && vpc.Status.ProjectID == "" {
			vpc.Status.ProjectID = prjID
		}
	})
}

func (r *VpcReconciler) kubeMarkToDelete(ctx context.Context, kubeVpc *v1alpha1.Vpc, _ *arubatypes.VPCResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeVpc, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *VpcReconciler) kubeMarkDeleting(ctx context.Context, kubeVpc *v1alpha1.Vpc, _ *arubatypes.VPCResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeVpc, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *VpcReconciler) kubeMarkDeletingDone(ctx context.Context, kubeVpc *v1alpha1.Vpc, _ *arubatypes.VPCResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeVpc, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *VpcReconciler) kubeMarkDeleted(ctx context.Context, kubeVpc *v1alpha1.Vpc, _ *arubatypes.VPCResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeVpc, v1alpha1.ResourcePhaseDeleted, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *VpcReconciler) kubeMarkToUpdate(ctx context.Context, kubeVpc *v1alpha1.Vpc, _ *arubatypes.VPCResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeVpc, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *VpcReconciler) kubeMarkUpdating(ctx context.Context, kubeVpc *v1alpha1.Vpc, _ *arubatypes.VPCResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeVpc, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *VpcReconciler) kubeMarkUpdatingDone(ctx context.Context, kubeVpc *v1alpha1.Vpc, _ *arubatypes.VPCResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeVpc, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *VpcReconciler) kubeMarkToCreate(ctx context.Context, kubeVpc *v1alpha1.Vpc, _ *arubatypes.VPCResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeVpc, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *VpcReconciler) kubeMarkCreating(ctx context.Context, kubeVpc *v1alpha1.Vpc, _ *arubatypes.VPCResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeVpc, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *VpcReconciler) kubeMarkCreatingDone(ctx context.Context, kubeVpc *v1alpha1.Vpc, _ *arubatypes.VPCResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeVpc, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *VpcReconciler) kubeSetActiveAndSetID(ctx context.Context, kubeVpc *v1alpha1.Vpc, cmpVpc *arubatypes.VPCResponse) error {
	cmpID := ""
	if cmpVpc != nil && cmpVpc.Metadata.ID != nil {
		cmpID = *cmpVpc.Metadata.ID
	}
	return setActiveAndSetID(r.Client, ctx, kubeVpc, cmpID, nil, func(vpc *v1alpha1.Vpc) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && vpc.Status.ProjectID != "" {
			vpc.Status.ProjectID = prjID
		}
	})
}

func (r *VpcReconciler) kubeSetFailedOnTimeout(ctx context.Context, kubeVpc *v1alpha1.Vpc, _ *arubatypes.VPCResponse) error {
	return setFailedOnTimeout(r.Client, ctx, kubeVpc, func(vpc *v1alpha1.Vpc) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && vpc.Status.ProjectID == "" {
			vpc.Status.ProjectID = prjID
		}
	})
}

func (r *VpcReconciler) kubeSetFailed(ctx context.Context, kubeVpc *v1alpha1.Vpc, _ *arubatypes.VPCResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeVpc, v1alpha1.ResourcePhaseFailed, v1alpha1.ConditionReasonSynchronized, nil)
}

// CMP action methods

func (r *VpcReconciler) cmpDelete(ctx context.Context, _ *v1alpha1.Vpc, cmpVpc *arubatypes.VPCResponse) error {
	prjID := ctx.Value(projectIDKey).(string)
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)

	cmpVpcResp, err := arubaClient.FromNetwork().VPCs().Delete(ctx, prjID, *cmpVpc.Metadata.ID, nil)
	if err != nil {
		return cmpTransportError("delete", *cmpVpc.Metadata.Name, err)
	}
	return cmpCheckResponse("delete", *cmpVpc.Metadata.Name, cmpVpcResp,
		http.StatusOK, http.StatusAccepted, http.StatusNoContent, http.StatusNotFound)
}

func (r *VpcReconciler) cmpUpdate(ctx context.Context, kubeVpc *v1alpha1.Vpc, cmpVpc *arubatypes.VPCResponse) error {
	prjID := ctx.Value(projectIDKey).(string)
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)

	// Guard: should have been caught by HasDeniedChanges, but double-check.
	if err := checkVpcDeniedChanges(kubeVpc, cmpVpc); err != nil {
		return err
	}

	request := buildVpcUpdateRequest(kubeVpc, cmpVpc)

	cmpVpcResp, err := arubaClient.FromNetwork().VPCs().Update(ctx, prjID, *cmpVpc.Metadata.ID, *request, nil)
	if err != nil {
		return cmpTransportError("update", kubeVpc.Name, err)
	}
	return cmpCheckResponse("update", kubeVpc.Name, cmpVpcResp,
		http.StatusOK, http.StatusAccepted, http.StatusNoContent)
}

func (r *VpcReconciler) cmpCreate(ctx context.Context, kubeVpc *v1alpha1.Vpc, _ *arubatypes.VPCResponse) error {
	prjID := ctx.Value(projectIDKey).(string)
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)

	cmpVpcResp, err := arubaClient.FromNetwork().VPCs().Create(ctx, prjID, *cmpVpcRequestFromKube(kubeVpc), nil)
	if err != nil {
		return cmpTransportError("create", kubeVpc.Name, err)
	}
	return cmpCheckResponse("create", kubeVpc.Name, cmpVpcResp,
		http.StatusOK, http.StatusCreated, http.StatusAccepted)
}

// Helper functions

func checkVpcDeniedChanges(kubeVpc *v1alpha1.Vpc, cmpVpc *arubatypes.VPCResponse) error {
	if cmpVpc == nil {
		return nil
	}

	locationValue := ""
	if cmpVpc.Metadata.LocationResponse != nil {
		locationValue = cmpVpc.Metadata.LocationResponse.Value
	}
	if kubeVpc.Spec.Location.Value != locationValue {
		return fmt.Errorf("%w: %w", ErrNotAllowedChanges, errors.New("change the 'location' is not allowed"))
	}

	return nil
}

func kubeVpcNeedsUpdate(kubeVpc *v1alpha1.Vpc, cmpVpc *arubatypes.VPCResponse) bool {
	if cmpVpc == nil {
		return false
	}
	return !tagsAreEqual(kubeVpc.Spec.Tags, cmpVpc.Metadata.Tags)
}

func buildVpcUpdateRequest(kubeVpc *v1alpha1.Vpc, cmpVpc *arubatypes.VPCResponse) *arubatypes.VPCRequest {
	request := cmpVpcRequestFromCMP(cmpVpc)
	if request == nil {
		return nil
	}
	tags := make([]string, len(kubeVpc.Spec.Tags))
	copy(tags, kubeVpc.Spec.Tags)
	request.Metadata.Tags = tags
	return request
}

func cmpVpcRequestFromCMP(cmpVpc *arubatypes.VPCResponse) *arubatypes.VPCRequest {
	if cmpVpc == nil {
		return nil
	}
	name := ""
	if cmpVpc.Metadata.Name != nil {
		name = *cmpVpc.Metadata.Name
	}
	tags := make([]string, len(cmpVpc.Metadata.Tags))
	copy(tags, cmpVpc.Metadata.Tags)
	location := arubatypes.LocationRequest{Value: ""}
	if cmpVpc.Metadata.LocationResponse != nil {
		location.Value = cmpVpc.Metadata.LocationResponse.Value
	}
	return &arubatypes.VPCRequest{
		Metadata: arubatypes.RegionalResourceMetadataRequest{
			ResourceMetadataRequest: arubatypes.ResourceMetadataRequest{
				Name: name,
				Tags: tags,
			},
			Location: location,
		},
		Properties: arubatypes.VPCPropertiesRequest{},
	}
}

func cmpVpcRequestFromKube(kubeVpc *v1alpha1.Vpc) *arubatypes.VPCRequest {
	falseVal := false
	return &arubatypes.VPCRequest{
		Metadata: arubatypes.RegionalResourceMetadataRequest{
			ResourceMetadataRequest: arubatypes.ResourceMetadataRequest{
				Name: kubeVpc.Name,
				Tags: kubeVpc.Spec.Tags,
			},
			Location: arubatypes.LocationRequest(kubeVpc.Spec.Location),
		},
		Properties: arubatypes.VPCPropertiesRequest{
			Properties: &arubatypes.VPCProperties{
				Default: &falseVal,
				Preset:  &falseVal,
			},
		},
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *VpcReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Vpc{}).
		Watches(&v1alpha1.Subnet{}, handler.EnqueueRequestsFromMapFunc(
			childToParentMapFunc(func(o client.Object) *v1alpha1.ResourceReference {
				if v, ok := o.(*v1alpha1.Subnet); ok {
					return &v.Spec.VpcReference
				}
				return nil
			}))).
		Watches(&v1alpha1.SecurityGroup{}, handler.EnqueueRequestsFromMapFunc(
			childToParentMapFunc(func(o client.Object) *v1alpha1.ResourceReference {
				if v, ok := o.(*v1alpha1.SecurityGroup); ok {
					return &v.Spec.VpcReference
				}
				return nil
			}))).
		Named("vpc").
		Complete(r)
}
