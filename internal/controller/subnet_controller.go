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
	subnetFinalizerName            = "subnet.arubacloud.com/finalizer"
	vpcIDKey            contextKey = "vpcID"
)

// SubnetReconciler reconciles a Subnet object
type SubnetReconciler struct {
	*reconciler.Reconciler
	ts *TransitionSet[*v1alpha1.Subnet, *arubatypes.SubnetResponse]
}

// NewSubnetReconciler creates a new SubnetReconciler
func NewSubnetReconciler(baseReconciler *reconciler.Reconciler) *SubnetReconciler {
	r := &SubnetReconciler{
		Reconciler: baseReconciler,
	}

	r.ts = r.newTransitionSet()

	return r
}

// +kubebuilder:rbac:groups=arubacloud.com,resources=subnets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=arubacloud.com,resources=subnets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=arubacloud.com,resources=subnets/finalizers,verbs=update
// +kubebuilder:rbac:groups=arubacloud.com,resources=projects,verbs=get;list;watch
// +kubebuilder:rbac:groups=arubacloud.com,resources=vpcs,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

func (r *SubnetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.Reconciler.Reconcile(ctx, req, r)
}

func (r *SubnetReconciler) Object() reconciler.ResourceObject {
	return &v1alpha1.Subnet{}
}

func (r *SubnetReconciler) Finalizer() string {
	return subnetFinalizerName
}

//nolint:gocyclo // complexity is intentional to maintain locality of behavior
func (r *SubnetReconciler) HandleReconcile(ctx context.Context, obj reconciler.ResourceObject) (ctrl.Result, error) {
	kubeSubnet, ok := obj.(*v1alpha1.Subnet)
	if !ok {
		return ctrl.Result{}, errors.New("obj is not a *v1alpha1.Subnet")
	}

	logger := log.FromContext(ctx).WithValues("tenant", kubeSubnet.Spec.Tenant)
	logger.Info("reconciling Subnet")

	arubaClient, err := r.ArubaClient(kubeSubnet.Spec.Tenant)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get Aruba client: %w", err)
	}

	if kubeSubnet.Spec.ProjectReference.Name == "" {
		return ctrl.Result{}, fmt.Errorf("project reference is not valid")
	}
	if kubeSubnet.Spec.VPCReference.Name == "" {
		return ctrl.Result{}, fmt.Errorf("vpc reference is not valid")
	}

	if kubeSubnet.GetDeletionTimestamp().IsZero() {
		kubeVpc := &v1alpha1.VPC{}
		if err := resolveOwnerObject(ctx, r.Client, kubeSubnet.Spec.VPCReference, kubeSubnet.Namespace, kubeVpc); err != nil {
			if !apierrors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("resolving parent vpc for owner reference: %w", err)
			}
			logger.V(1).Info("parent vpc not found for owner reference setup, skipping", "vpcName", kubeSubnet.Spec.VPCReference.Name)
		} else {
			requeue, err := ensureOwnerReference(ctx, r.Client, r.Scheme, kubeVpc, kubeSubnet)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("setting owner reference on subnet: %w", err)
			}
			if requeue {
				return ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
			}
		}
	}

	subnetName := kubeSubnet.Name
	projectName := kubeSubnet.Spec.ProjectReference.Name
	vpcName := kubeSubnet.Spec.VPCReference.Name
	prjFilter := fmt.Sprintf(`name:eq("%s")`, projectName)
	vpcFilter := fmt.Sprintf(`name:eq("%s")`, vpcName)
	subnetFilter := fmt.Sprintf(`name:eq("%s")`, subnetName)

	// --- Resolve Project ID ---

	var prjID string

	if !kubeSubnet.GetDeletionTimestamp().IsZero() && kubeSubnet.Status.ProjectID != "" {
		prjID = kubeSubnet.Status.ProjectID
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
		if cmpProjectList.Data.Total == 0 && kubeSubnet.Status.ProjectID != "" {
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

	if kubeSubnet.Status.ProjectID != "" && kubeSubnet.Status.ProjectID != prjID {
		return ctrl.Result{}, fmt.Errorf(
			"inconsistent project id in subnet: subnet_name: '%s', subnet_project_id: '%s', project_name: '%s', project_id: '%s'",
			subnetName, kubeSubnet.Status.ProjectID, projectName, prjID,
		)
	}

	// --- Resolve VPC ID ---

	var vpcID string

	if !kubeSubnet.GetDeletionTimestamp().IsZero() && kubeSubnet.Status.VPCID != "" {
		vpcID = kubeSubnet.Status.VPCID
	} else {
		cmpVpcList, err := arubaClient.FromNetwork().VPCs().List(ctx, prjID, &arubatypes.RequestParameters{Filter: &vpcFilter})
		if err != nil {
			return ctrl.Result{}, fmt.Errorf(
				"failed to find vpc in Aruba cloud: %w, vpc_name: '%s', vpc_filter: '%s', project_name: '%s'",
				err, vpcName, vpcFilter, projectName,
			)
		}
		if cmpVpcList.IsError() {
			return ctrl.Result{}, fmt.Errorf(
				"failed to find vpc in Aruba cloud: status_code: %d, vpc_name: '%s', project_name: '%s'",
				cmpVpcList.StatusCode, vpcName, projectName,
			)
		}
		// TODO: Remove once CMP API name:eq() filter is fixed (issue https://jira.aruba.it/browse/DEV-66643).
		applyNameFilterToVPCList(cmpVpcList, vpcName, logger)
		if cmpVpcList.Data.Total == 0 && kubeSubnet.Status.VPCID != "" {
			return ctrl.Result{}, fmt.Errorf(
				"inconsistent data in vpc list: expected: 1, vpc not found: vpc_name: '%s', vpc_filter: '%s'", vpcName, vpcFilter,
			)
		}
		if cmpVpcList.Data.Total > 1 {
			return ctrl.Result{}, fmt.Errorf(
				"inconsistent data in vpc list: expected: 1, found: %d, vpc_name: '%s', vpc_filter: '%s'",
				cmpVpcList.Data.Total, vpcName, vpcFilter,
			)
		}
		if cmpVpcList.Data.Total == 0 {
			logger.V(1).Info("parent vpc not found on CMP, requeuing", "vpcName", vpcName)
			return ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
		}
		vpcID = *(cmpVpcList.Data.Values[0].Metadata.ID)
	}

	if kubeSubnet.Status.VPCID != "" && kubeSubnet.Status.VPCID != vpcID {
		return ctrl.Result{}, fmt.Errorf(
			"inconsistent vpc id in subnet: subnet_name: '%s', subnet_vpc_id: '%s', vpc_name: '%s', vpc_id: '%s'",
			subnetName, kubeSubnet.Status.VPCID, vpcName, vpcID,
		)
	}

	// --- Fetch CMP Subnet ---

	cmpSubnetList, err := arubaClient.FromNetwork().Subnets().List(ctx, prjID, vpcID, &arubatypes.RequestParameters{Filter: &subnetFilter})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf(
			"failed to find subnet in Aruba cloud: %w, subnet_name: '%s', subnet_filter: '%s', project_name: '%s', vpc_name: '%s'",
			err, subnetName, subnetFilter, projectName, vpcName,
		)
	}
	if cmpSubnetList.IsError() && cmpSubnetList.StatusCode != http.StatusNotFound {
		return ctrl.Result{}, fmt.Errorf(
			"failed to find subnet in Aruba cloud: status_code: %d, subnet_name: '%s', project_name: '%s', vpc_name: '%s'",
			cmpSubnetList.StatusCode, subnetName, projectName, vpcName,
		)
	}
	// TODO: Remove once CMP API name:eq() filter is fixed (issue https://jira.aruba.it/browse/DEV-66643).
	applyNameFilterToSubnetList(cmpSubnetList, subnetName, logger)
	if !cmpSubnetList.IsError() && (cmpSubnetList.Data.Total < 0 || cmpSubnetList.Data.Total > 1) {
		return ctrl.Result{}, fmt.Errorf(
			"inconsistent data in subnet list: subnet_name: '%s', subnet_filter: '%s', project_name: '%s', vpc_name: '%s', instances: %d",
			subnetName, subnetFilter, projectName, vpcName, cmpSubnetList.Data.Total,
		)
	}

	var cmpSubnet *arubatypes.SubnetResponse
	if cmpSubnetList.Data != nil && cmpSubnetList.Data.Total == 1 {
		cmpSubnet = &cmpSubnetList.Data.Values[0]
	}
	logger.V(1).Info("CMP Subnet state", "found", cmpSubnet != nil, "projectID", prjID, "vpcID", vpcID)

	ctx = context.WithValue(ctx, projectIDKey, prjID)
	ctx = context.WithValue(ctx, vpcIDKey, vpcID)
	ctx = context.WithValue(ctx, reconciler.ArubaClientKey, arubaClient)
	ctx = log.IntoContext(ctx, logger)

	return r.ts.Run(ctx, kubeSubnet, cmpSubnet)
}

// Transition Set Builder

func (r *SubnetReconciler) newTransitionSet() *TransitionSet[*v1alpha1.Subnet, *arubatypes.SubnetResponse] {
	ts := &TransitionSet[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		defaultRequeue:        NoRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		defaultRequeueOnError: NoRequeueButIgnoreError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	}

	// 0. PhaseTimedOut — safety net: fail if stuck in a transitory phase too long
	ts.Add(&AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		name:           "PhaseTimedOut",
		kCondition:     kubePhaseTimedOut[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		aCondition:     AlwaysTrue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		kAction:        r.kubeSetFailedOnTimeout,
		requeue:        NoRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 1. ShouldBeDeleted
	ts.Add(&AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		name:           "ShouldBeDeleted",
		kCondition:     kubeShouldDelete[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		aCondition:     cmpSubnetIsFinal,
		kAction:        r.kubeMarkToDelete,
		requeue:        ShortRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 2. ShouldDeleteTimedOut — enter deletion flow for timed-out resources (except those that timed out during Deleting)
	ts.Add(&AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		name:           "ShouldDeleteTimedOut",
		kCondition:     kubeShouldDeleteTimedOut[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		aCondition:     AlwaysTrue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		kAction:        r.kubeMarkToDelete,
		requeue:        ShortRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 3. ShouldBeDeletedOnCMP
	ts.Add(&AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		name:              "ShouldBeDeletedOnCMP",
		kCondition:        kubeShouldBeDeletedOnCMP[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		aCondition:        cmpSubnetIsFinal,
		aAction:           r.cmpDelete,
		kActionOnASuccess: r.kubeMarkDeleting,
		kActionOnAError:   kubeSetErrorMessageOnCMPError[*v1alpha1.Subnet, *arubatypes.SubnetResponse](r.Client),
		requeue:           ShortRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		requeueOnError:    SmartRequeueOnError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 4. DeletionOnCMPNotNeeded — resource marked for deletion but CMP resource doesn't exist; skip CMP delete
	ts.Add(&AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		name:           "DeletionOnCMPNotNeeded",
		kCondition:     kubeShouldBeDeletedOnCMP[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		aCondition:     cmpSubnetNotExists,
		kAction:        r.kubeMarkDeletingDone,
		requeue:        ShortRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 5. WaitingDeletionOnCMP
	ts.Add(&AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		name:           "WaitingDeletionOnCMP",
		kCondition:     kubeWaitingDeletionOnCMP[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		aCondition:     cmpSubnetIsTransitory,
		requeue:        LongRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 6. DeletionConfirmedOnCMP
	ts.Add(&AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		name:           "DeletionConfirmedOnCMP",
		kCondition:     kubeWaitingDeletionOnCMP[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		aCondition:     cmpSubnetNotExists,
		kAction:        r.kubeMarkDeletingDone,
		requeue:        ShortRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 7. DeletionAccomplished
	ts.Add(&AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		name:           "DeletionAccomplished",
		kCondition:     kubeDeletionAccomplished[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		aCondition:     cmpSubnetNotExists,
		kAction:        r.kubeMarkDeleted,
		requeue:        ShortRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 8. HasDeniedChanges — surface immutable field violations before attempting update
	ts.Add(&AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		name:       "HasDeniedChanges",
		kCondition: kubeSubnetHasDeniedChanges,
		aCondition: cmpSubnetIsFinal,
		kAction: func(ctx context.Context, kubeSubnet *v1alpha1.Subnet, cmpSubnet *arubatypes.SubnetResponse) error {
			return fmt.Errorf("subnet update rejected: %w", checkSubnetDeniedChanges(kubeSubnet, cmpSubnet))
		},
		requeue:        NoRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		requeueOnError: LongRequeueAndIgnoreError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 9. SpecAlreadyInSyncWithCMP — generation changed but spec identical to CMP; just re-stamp ObservedGeneration
	ts.Add(&AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		name:           "SpecAlreadyInSyncWithCMP",
		kCondition:     kubeSubnetSpecInSyncWithCMP,
		aCondition:     cmpSubnetIsActive,
		kAction:        r.kubeSetActiveAndSetID,
		requeue:        NoRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 10. ShouldBeUpdated — spec changed and CMP is ready
	ts.Add(&AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		name:           "ShouldBeUpdated",
		kCondition:     kubeSubnetShouldUpdate,
		aCondition:     cmpSubnetIsFinal,
		kAction:        r.kubeMarkToUpdate,
		requeue:        ShortRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 11. ShouldBeUpdatedOnCMP — send update to CMP
	ts.Add(&AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		name:              "ShouldBeUpdatedOnCMP",
		kCondition:        kubeShouldBeUpdatedOnCMP[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		aCondition:        cmpSubnetIsFinal,
		aAction:           r.cmpUpdate,
		kActionOnASuccess: r.kubeMarkUpdating,
		kActionOnAError:   kubeSetErrorMessageOnCMPError[*v1alpha1.Subnet, *arubatypes.SubnetResponse](r.Client),
		requeue:           ShortRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		requeueOnError:    SmartRequeueOnError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 12. WaitingUpdateOnCMP — CMP is still processing the update
	ts.Add(&AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		name:           "WaitingUpdateOnCMP",
		kCondition:     kubeWaitingUpdateOnCMP[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		aCondition:     cmpSubnetIsTransitory,
		requeue:        LongRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 13. UpdateConfirmedOnCMP — CMP has settled; advance to Synchronized
	ts.Add(&AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		name:           "UpdateConfirmedOnCMP",
		kCondition:     kubeWaitingUpdateOnCMP[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		aCondition:     cmpSubnetIsFinal,
		kAction:        r.kubeMarkUpdatingDone,
		requeue:        ShortRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 14. UpdateAccomplished — transition back to Active and stamp generation
	ts.Add(&AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		name:           "UpdateAccomplished",
		kCondition:     kubeUpdateAccomplished[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		aCondition:     cmpSubnetIsActive,
		kAction:        r.kubeSetActiveAndSetID,
		requeue:        NoRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 15. ShouldBeCreated
	ts.Add(&AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		name:           "ShouldBeCreated",
		kCondition:     kubeIsFirstReconciliation[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		aCondition:     cmpSubnetNotExists,
		kAction:        r.kubeMarkToCreate,
		requeue:        ShortRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 16. ShouldBeCreatedInCMP
	ts.Add(&AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		name:              "ShouldBeCreatedInCMP",
		kCondition:        kubeShouldBeCreatedOnCMP[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		aCondition:        cmpSubnetNotExists,
		aAction:           r.cmpCreate,
		kActionOnASuccess: r.kubeMarkCreating,
		kActionOnAError:   kubeSetErrorMessageOnCMPError[*v1alpha1.Subnet, *arubatypes.SubnetResponse](r.Client),
		requeue:           ShortRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		requeueOnError:    SmartRequeueOnError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 17. WaitingCreationInCMP
	ts.Add(&AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		name:           "WaitingCreationInCMP",
		kCondition:     kubeWaitingCreationInCMP[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		aCondition:     cmpSubnetNotExistsOrTransitory,
		requeue:        LongRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 18. CreationConfirmedOnCMP
	ts.Add(&AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		name:           "CreationConfirmedOnCMP",
		kCondition:     kubeWaitingCreationInCMP[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		aCondition:     cmpSubnetIsActive,
		kAction:        r.kubeMarkCreatingDone,
		requeue:        ShortRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 19. CreationAccomplished
	ts.Add(&AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		name:           "CreationAccomplished",
		kCondition:     kubeIsCreatedOnCMP[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		aCondition:     cmpSubnetIsActive,
		kAction:        r.kubeSetActiveAndSetID,
		requeue:        NoRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 20. IsInError
	ts.Add(&AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		name:           "IsInError",
		kCondition:     AlwaysTrue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		aCondition:     cmpSubnetIsFailed,
		kAction:        r.kubeSetFailed,
		requeue:        NoRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	return ts
}

// Resource-specific condition functions

func kubeSubnetHasDeniedChanges(kubeSubnet *v1alpha1.Subnet, cmpSubnet *arubatypes.SubnetResponse) bool {
	if !kubeSubnet.DeletionTimestamp.IsZero() {
		return false
	}
	if cmpSubnet == nil {
		return false
	}
	return checkSubnetDeniedChanges(kubeSubnet, cmpSubnet) != nil
}

func kubeSubnetSpecInSyncWithCMP(kubeSubnet *v1alpha1.Subnet, cmpSubnet *arubatypes.SubnetResponse) bool {
	return kubeActiveAndGenerationChanged(kubeSubnet, cmpSubnet) &&
		checkSubnetDeniedChanges(kubeSubnet, cmpSubnet) == nil &&
		!kubeSubnetNeedsUpdate(kubeSubnet, cmpSubnet)
}

func kubeSubnetShouldUpdate(kubeSubnet *v1alpha1.Subnet, cmpSubnet *arubatypes.SubnetResponse) bool {
	return kubeActiveAndGenerationChanged(kubeSubnet, cmpSubnet) &&
		checkSubnetDeniedChanges(kubeSubnet, cmpSubnet) == nil &&
		kubeSubnetNeedsUpdate(kubeSubnet, cmpSubnet)
}

func cmpSubnetNotExists(_ *v1alpha1.Subnet, cmpSubnet *arubatypes.SubnetResponse) bool {
	return cmpSubnet == nil
}

func cmpSubnetIsFinal(_ *v1alpha1.Subnet, cmpSubnet *arubatypes.SubnetResponse) bool {
	if cmpSubnet == nil || cmpSubnet.Status.State == nil {
		return false
	}
	return AssessCSPResourceStateNature(&cmpSubnet.Status) == CSPResourceStateNatureFinal
}

func cmpSubnetIsTransitory(_ *v1alpha1.Subnet, cmpSubnet *arubatypes.SubnetResponse) bool {
	if cmpSubnet == nil || cmpSubnet.Status.State == nil {
		return false
	}
	return AssessCSPResourceStateNature(&cmpSubnet.Status) == CSPResourceStateNatureTransitory
}

func cmpSubnetNotExistsOrTransitory(_ *v1alpha1.Subnet, cmpSubnet *arubatypes.SubnetResponse) bool {
	if cmpSubnet == nil {
		return true
	}
	if cmpSubnet.Status.State == nil {
		return false
	}
	return AssessCSPResourceStateNature(&cmpSubnet.Status) == CSPResourceStateNatureTransitory
}

func cmpSubnetIsActive(_ *v1alpha1.Subnet, cmpSubnet *arubatypes.SubnetResponse) bool {
	return cmpSubnet != nil && cmpSubnet.Status.State != nil &&
		*cmpSubnet.Status.State == CSPResourceStateActive
}

func cmpSubnetIsFailed(_ *v1alpha1.Subnet, cmpSubnet *arubatypes.SubnetResponse) bool {
	return cmpSubnet != nil && cmpSubnet.Status.State != nil && *cmpSubnet.Status.State == CSPResourceStateFailed
}

// Kube action methods

func (r *SubnetReconciler) kubeSetPhaseAndCondition(ctx context.Context, kubeSubnet *v1alpha1.Subnet, phase v1alpha1.ResourcePhase, reason string, _ error) error {
	return setPhaseAndCondition(r.Client, ctx, kubeSubnet, phase, reason, nil, func(subnet *v1alpha1.Subnet) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && subnet.Status.ProjectID == "" {
			subnet.Status.ProjectID = prjID
		}
		if vID, ok := ctx.Value(vpcIDKey).(string); ok && subnet.Status.VPCID == "" {
			subnet.Status.VPCID = vID
		}
	})
}

func (r *SubnetReconciler) kubeMarkToDelete(ctx context.Context, kubeSubnet *v1alpha1.Subnet, _ *arubatypes.SubnetResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSubnet, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *SubnetReconciler) kubeMarkDeleting(ctx context.Context, kubeSubnet *v1alpha1.Subnet, _ *arubatypes.SubnetResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSubnet, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *SubnetReconciler) kubeMarkDeletingDone(ctx context.Context, kubeSubnet *v1alpha1.Subnet, _ *arubatypes.SubnetResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSubnet, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *SubnetReconciler) kubeMarkDeleted(ctx context.Context, kubeSubnet *v1alpha1.Subnet, _ *arubatypes.SubnetResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSubnet, v1alpha1.ResourcePhaseDeleted, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *SubnetReconciler) kubeMarkToUpdate(ctx context.Context, kubeSubnet *v1alpha1.Subnet, _ *arubatypes.SubnetResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSubnet, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *SubnetReconciler) kubeMarkUpdating(ctx context.Context, kubeSubnet *v1alpha1.Subnet, _ *arubatypes.SubnetResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSubnet, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *SubnetReconciler) kubeMarkUpdatingDone(ctx context.Context, kubeSubnet *v1alpha1.Subnet, _ *arubatypes.SubnetResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSubnet, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *SubnetReconciler) kubeMarkToCreate(ctx context.Context, kubeSubnet *v1alpha1.Subnet, _ *arubatypes.SubnetResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSubnet, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *SubnetReconciler) kubeMarkCreating(ctx context.Context, kubeSubnet *v1alpha1.Subnet, _ *arubatypes.SubnetResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSubnet, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *SubnetReconciler) kubeMarkCreatingDone(ctx context.Context, kubeSubnet *v1alpha1.Subnet, _ *arubatypes.SubnetResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSubnet, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *SubnetReconciler) kubeSetActiveAndSetID(ctx context.Context, kubeSubnet *v1alpha1.Subnet, cmpSubnet *arubatypes.SubnetResponse) error {
	cmpID := ""
	if cmpSubnet != nil && cmpSubnet.Metadata.ID != nil {
		cmpID = *cmpSubnet.Metadata.ID
	}
	return setActiveAndSetID(r.Client, ctx, kubeSubnet, cmpID, nil, func(subnet *v1alpha1.Subnet) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && subnet.Status.ProjectID != "" {
			subnet.Status.ProjectID = prjID
		}
		if vID, ok := ctx.Value(vpcIDKey).(string); ok && subnet.Status.VPCID != "" {
			subnet.Status.VPCID = vID
		}
	})
}

func (r *SubnetReconciler) kubeSetFailedOnTimeout(ctx context.Context, kubeSubnet *v1alpha1.Subnet, _ *arubatypes.SubnetResponse) error {
	return setFailedOnTimeout(r.Client, ctx, kubeSubnet, func(subnet *v1alpha1.Subnet) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && subnet.Status.ProjectID == "" {
			subnet.Status.ProjectID = prjID
		}
		if vID, ok := ctx.Value(vpcIDKey).(string); ok && subnet.Status.VPCID == "" {
			subnet.Status.VPCID = vID
		}
	})
}

func (r *SubnetReconciler) kubeSetFailed(ctx context.Context, kubeSubnet *v1alpha1.Subnet, _ *arubatypes.SubnetResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSubnet, v1alpha1.ResourcePhaseFailed, v1alpha1.ConditionReasonSynchronized, nil)
}

// CMP action methods

func (r *SubnetReconciler) cmpDelete(ctx context.Context, _ *v1alpha1.Subnet, cmpSubnet *arubatypes.SubnetResponse) error {
	prjID := ctx.Value(projectIDKey).(string)
	vID := ctx.Value(vpcIDKey).(string)
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)

	cmpSubnetResp, err := arubaClient.FromNetwork().Subnets().Delete(ctx, prjID, vID, *cmpSubnet.Metadata.ID, nil)
	if err != nil {
		return cmpTransportError("delete", *cmpSubnet.Metadata.Name, err)
	}
	return cmpCheckResponse("delete", *cmpSubnet.Metadata.Name, cmpSubnetResp,
		http.StatusOK, http.StatusAccepted, http.StatusNoContent, http.StatusNotFound)
}

func (r *SubnetReconciler) cmpUpdate(ctx context.Context, kubeSubnet *v1alpha1.Subnet, cmpSubnet *arubatypes.SubnetResponse) error {
	prjID := ctx.Value(projectIDKey).(string)
	vID := ctx.Value(vpcIDKey).(string)
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)

	// Guard: should have been caught by HasDeniedChanges, but double-check.
	if err := checkSubnetDeniedChanges(kubeSubnet, cmpSubnet); err != nil {
		return err
	}

	request := buildSubnetUpdateRequest(kubeSubnet, cmpSubnet)

	cmpSubnetResp, err := arubaClient.FromNetwork().Subnets().Update(ctx, prjID, vID, *cmpSubnet.Metadata.ID, *request, nil)
	if err != nil {
		return cmpTransportError("update", kubeSubnet.Name, err)
	}
	return cmpCheckResponse("update", kubeSubnet.Name, cmpSubnetResp,
		http.StatusOK, http.StatusAccepted, http.StatusNoContent)
}

func (r *SubnetReconciler) cmpCreate(ctx context.Context, kubeSubnet *v1alpha1.Subnet, _ *arubatypes.SubnetResponse) error {
	prjID := ctx.Value(projectIDKey).(string)
	vID := ctx.Value(vpcIDKey).(string)
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)

	cmpSubnetResp, err := arubaClient.FromNetwork().Subnets().Create(ctx, prjID, vID, *cmpSubnetRequestFromKube(kubeSubnet), nil)
	if err != nil {
		return cmpTransportError("create", kubeSubnet.Name, err)
	}
	return cmpCheckResponse("create", kubeSubnet.Name, cmpSubnetResp,
		http.StatusOK, http.StatusCreated, http.StatusAccepted)
}

// Helper functions

func checkSubnetDeniedChanges(kubeSubnet *v1alpha1.Subnet, cmpSubnet *arubatypes.SubnetResponse) error {
	if cmpSubnet == nil {
		return nil
	}

	locationValue := ""
	if cmpSubnet.Metadata.LocationResponse != nil {
		locationValue = cmpSubnet.Metadata.LocationResponse.Value
	}
	if kubeSubnet.Spec.Region != locationValue {
		return fmt.Errorf("%w: %w", ErrNotAllowedChanges, errors.New("change the 'location' is not allowed"))
	}

	if cmpSubnet.Properties.Network != nil && kubeSubnet.Spec.CIDR != cmpSubnet.Properties.Network.Address {
		return fmt.Errorf("%w: %w", ErrNotAllowedChanges, errors.New("change the 'network.address' is not allowed"))
	}

	if string(cmpSubnet.Properties.Type) != "" && kubeSubnet.Spec.Type != string(cmpSubnet.Properties.Type) {
		return fmt.Errorf("%w: %w", ErrNotAllowedChanges, errors.New("change the 'type' is not allowed"))
	}

	return nil
}

func kubeSubnetNeedsUpdate(kubeSubnet *v1alpha1.Subnet, cmpSubnet *arubatypes.SubnetResponse) bool {
	if cmpSubnet == nil {
		return false
	}
	if !tagsAreEqual(kubeSubnet.Spec.Tags, cmpSubnet.Metadata.Tags) {
		return true
	}
	if cmpSubnet.Properties.DHCP != nil && kubeSubnet.Spec.DHCP.Enabled != cmpSubnet.Properties.DHCP.Enabled {
		return true
	}
	return false
}

func buildSubnetUpdateRequest(kubeSubnet *v1alpha1.Subnet, cmpSubnet *arubatypes.SubnetResponse) *arubatypes.SubnetRequest {
	request := cmpSubnetRequestFromCMP(cmpSubnet)
	if request == nil {
		return nil
	}
	tags := make([]string, len(kubeSubnet.Spec.Tags))
	copy(tags, kubeSubnet.Spec.Tags)
	request.Metadata.Tags = tags
	if request.Properties.DHCP != nil {
		request.Properties.DHCP.Enabled = kubeSubnet.Spec.DHCP.Enabled
	} else {
		request.Properties.DHCP = &arubatypes.SubnetDHCP{Enabled: kubeSubnet.Spec.DHCP.Enabled}
	}
	return request
}

func cmpSubnetRequestFromCMP(cmpSubnet *arubatypes.SubnetResponse) *arubatypes.SubnetRequest {
	if cmpSubnet == nil {
		return nil
	}
	name := ""
	if cmpSubnet.Metadata.Name != nil {
		name = *cmpSubnet.Metadata.Name
	}
	tags := make([]string, len(cmpSubnet.Metadata.Tags))
	copy(tags, cmpSubnet.Metadata.Tags)

	location := arubatypes.LocationRequest{}
	if cmpSubnet.Metadata.LocationResponse != nil {
		location.Value = cmpSubnet.Metadata.LocationResponse.Value
	}

	req := &arubatypes.SubnetRequest{
		Metadata: arubatypes.RegionalResourceMetadataRequest{
			ResourceMetadataRequest: arubatypes.ResourceMetadataRequest{
				Name: name,
				Tags: tags,
			},
			Location: location,
		},
		Properties: arubatypes.SubnetPropertiesRequest{
			Type:    cmpSubnet.Properties.Type,
			Default: cmpSubnet.Properties.Default,
		},
	}
	if cmpSubnet.Properties.Network != nil {
		req.Properties.Network = &arubatypes.SubnetNetwork{
			Address: cmpSubnet.Properties.Network.Address,
		}
	}
	if cmpSubnet.Properties.DHCP != nil {
		req.Properties.DHCP = &arubatypes.SubnetDHCP{
			Enabled: cmpSubnet.Properties.DHCP.Enabled,
		}
	}
	return req
}

func cmpSubnetRequestFromKube(kubeSubnet *v1alpha1.Subnet) *arubatypes.SubnetRequest {
	tags := make([]string, len(kubeSubnet.Spec.Tags))
	copy(tags, kubeSubnet.Spec.Tags)
	return &arubatypes.SubnetRequest{
		Metadata: arubatypes.RegionalResourceMetadataRequest{
			ResourceMetadataRequest: arubatypes.ResourceMetadataRequest{
				Name: kubeSubnet.Name,
				Tags: tags,
			},
			Location: arubatypes.LocationRequest{Value: kubeSubnet.Spec.Region},
		},
		Properties: arubatypes.SubnetPropertiesRequest{
			Type:    arubatypes.SubnetType(kubeSubnet.Spec.Type),
			Default: false,
			Network: &arubatypes.SubnetNetwork{
				Address: kubeSubnet.Spec.CIDR,
			},
			DHCP: &arubatypes.SubnetDHCP{
				Enabled: kubeSubnet.Spec.DHCP.Enabled,
			},
		},
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *SubnetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Subnet{}).
		Named("subnet").
		Complete(r)
}
