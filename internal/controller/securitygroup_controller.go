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
	securityGroupFinalizerName = "securitygroup.arubacloud.com/finalizer"
)

// SecurityGroupReconciler reconciles a SecurityGroup object
type SecurityGroupReconciler struct {
	*reconciler.Reconciler
	ts *reconciler.TransitionSet[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse]
}

// NewSecurityGroupReconciler creates a new SecurityGroupReconciler
func NewSecurityGroupReconciler(baseReconciler *reconciler.Reconciler) *SecurityGroupReconciler {
	r := &SecurityGroupReconciler{
		Reconciler: baseReconciler,
	}

	r.ts = r.newTransitionSet()

	return r
}

// +kubebuilder:rbac:groups=arubacloud.com,resources=securitygroups,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=arubacloud.com,resources=securitygroups/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=arubacloud.com,resources=securitygroups/finalizers,verbs=update
// +kubebuilder:rbac:groups=arubacloud.com,resources=projects,verbs=get;list;watch
// +kubebuilder:rbac:groups=arubacloud.com,resources=vpcs,verbs=get;list;watch
// +kubebuilder:rbac:groups=arubacloud.com,resources=securityrules,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

func (r *SecurityGroupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.Reconciler.Reconcile(ctx, req, r)
}

func (r *SecurityGroupReconciler) Object() reconciler.ResourceObject {
	return &v1alpha1.SecurityGroup{}
}

func (r *SecurityGroupReconciler) Finalizer() string {
	return securityGroupFinalizerName
}

// SetupWithManager sets up the controller with the Manager.
func (r *SecurityGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.SecurityGroup{}).
		Watches(&v1alpha1.SecurityRule{}, handler.EnqueueRequestsFromMapFunc(
			childToParentMapFunc(func(o client.Object) *v1alpha1.ResourceReference {
				if v, ok := o.(*v1alpha1.SecurityRule); ok {
					return &v.Spec.SecurityGroupReference
				}
				return nil
			}))).
		Named("securitygroup").
		Complete(r)
}

//nolint:gocyclo // complexity is intentional to maintain locality of behavior
func (r *SecurityGroupReconciler) HandleReconcile(ctx context.Context, obj reconciler.ResourceObject) (ctrl.Result, error) {
	kubeSG, ok := obj.(*v1alpha1.SecurityGroup)
	if !ok {
		return ctrl.Result{}, errors.New("obj is not a *v1alpha1.SecurityGroup")
	}

	logger := log.FromContext(ctx).WithValues("tenant", kubeSG.Spec.Tenant)
	logger.Info("reconciling SecurityGroup")

	arubaClient, err := r.ArubaClient(kubeSG.Spec.Tenant)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get Aruba client: %w", err)
	}

	if kubeSG.Spec.ProjectReference.Name == "" {
		return ctrl.Result{}, fmt.Errorf("project reference is not valid")
	}
	if kubeSG.Spec.VPCReference.Name == "" {
		return ctrl.Result{}, fmt.Errorf("vpc reference is not valid")
	}

	// Set OwnerReference to VPC (skipped during deletion — the resource is going away).
	if kubeSG.GetDeletionTimestamp().IsZero() {
		kubeVpc := &v1alpha1.VPC{}
		if err := resolveOwnerObject(ctx, r.Client, kubeSG.Spec.VPCReference, kubeSG.Namespace, kubeVpc); err != nil {
			if !apierrors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("resolving parent vpc for owner reference: %w", err)
			}
			logger.V(1).Info("parent vpc not found for owner reference setup, skipping",
				"vpcName", kubeSG.Spec.VPCReference.Name)
		} else {
			requeue, err := ensureOwnerReference(ctx, r.Client, r.Scheme, kubeVpc, kubeSG)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("setting owner reference on security group: %w", err)
			}
			if requeue {
				return ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
			}
		}
	}

	sgName := kubeSG.Name
	projectName := kubeSG.Spec.ProjectReference.Name
	vpcName := kubeSG.Spec.VPCReference.Name
	prjFilter := fmt.Sprintf(`name:eq("%s")`, projectName)
	vpcFilter := fmt.Sprintf(`name:eq("%s")`, vpcName)
	sgFilter := fmt.Sprintf(`name:eq("%s")`, sgName)

	// --- Resolve Project ID ---

	var prjID string

	if !kubeSG.GetDeletionTimestamp().IsZero() && kubeSG.Status.ProjectID != "" {
		prjID = kubeSG.Status.ProjectID
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
		if cmpProjectList.Data.Total == 0 && kubeSG.Status.ProjectID != "" {
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

	if kubeSG.Status.ProjectID != "" && kubeSG.Status.ProjectID != prjID {
		return ctrl.Result{}, fmt.Errorf(
			"inconsistent project id in security group: sg_name: '%s', sg_project_id: '%s', project_name: '%s', project_id: '%s'",
			sgName, kubeSG.Status.ProjectID, projectName, prjID,
		)
	}

	// --- Resolve VPC ID ---

	var vpcID string

	if !kubeSG.GetDeletionTimestamp().IsZero() && kubeSG.Status.VPCID != "" {
		vpcID = kubeSG.Status.VPCID
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
		if cmpVpcList.Data.Total == 0 && kubeSG.Status.VPCID != "" {
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

	if kubeSG.Status.VPCID != "" && kubeSG.Status.VPCID != vpcID {
		return ctrl.Result{}, fmt.Errorf(
			"inconsistent vpc id in security group: sg_name: '%s', sg_vpc_id: '%s', vpc_name: '%s', vpc_id: '%s'",
			sgName, kubeSG.Status.VPCID, vpcName, vpcID,
		)
	}

	// --- Fetch CMP SecurityGroup ---

	cmpSGList, err := arubaClient.FromNetwork().SecurityGroups().List(ctx, prjID, vpcID, &arubatypes.RequestParameters{Filter: &sgFilter})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf(
			"failed to find security group in Aruba cloud: %w, sg_name: '%s', sg_filter: '%s', project_name: '%s', vpc_name: '%s'",
			err, sgName, sgFilter, projectName, vpcName,
		)
	}
	if cmpSGList.IsError() && cmpSGList.StatusCode != http.StatusNotFound {
		return ctrl.Result{}, fmt.Errorf(
			"failed to find security group in Aruba cloud: status_code: %d, sg_name: '%s', project_name: '%s', vpc_name: '%s'",
			cmpSGList.StatusCode, sgName, projectName, vpcName,
		)
	}
	// TODO: Remove once CMP API name:eq() filter is fixed (issue https://jira.aruba.it/browse/DEV-66643).
	applyNameFilterToSecurityGroupList(cmpSGList, sgName, logger)
	if !cmpSGList.IsError() && (cmpSGList.Data.Total < 0 || cmpSGList.Data.Total > 1) {
		return ctrl.Result{}, fmt.Errorf(
			"inconsistent data in security group list: sg_name: '%s', sg_filter: '%s', project_name: '%s', vpc_name: '%s', instances: %d",
			sgName, sgFilter, projectName, vpcName, cmpSGList.Data.Total,
		)
	}

	var cmpSG *arubatypes.SecurityGroupResponse
	if cmpSGList.Data != nil && cmpSGList.Data.Total == 1 {
		cmpSG = &cmpSGList.Data.Values[0]
	}
	logger.V(1).Info("CMP SecurityGroup state", "found", cmpSG != nil, "projectID", prjID, "vpcID", vpcID)

	ctx = context.WithValue(ctx, projectIDKey, prjID)
	ctx = context.WithValue(ctx, vpcIDKey, vpcID)
	ctx = context.WithValue(ctx, reconciler.ArubaClientKey, arubaClient)
	ctx = log.IntoContext(ctx, logger)

	return r.ts.Run(ctx, kubeSG, cmpSG)
}

// kubeSecurityGroupHasOwnedChildren returns true when any Kubernetes resource directly owned
// by the SecurityGroup still exists. Used by the WaitingChildrenDeletion transition.
func (r *SecurityGroupReconciler) kubeSecurityGroupHasOwnedChildren(k *v1alpha1.SecurityGroup, _ *arubatypes.SecurityGroupResponse) bool {
	labelKey, _ := ownerLabelKey(r.Scheme, k)
	has, err := hasOwnedChildren(context.Background(), r.Client, k, labelKey,
		&v1alpha1.SecurityRuleList{},
	)
	if err != nil {
		ctrl.Log.Error(err, "checking owned children for security group", "securityGroup", k.GetName())
		return true // conservative: assume children exist on error
	}
	return has
}

// kubeSecurityGroupDeleteOwnedChildren deletes all K8s children of the SecurityGroup that
// have not yet received a deletionTimestamp. Called by the WaitingChildrenDeletion action.
func (r *SecurityGroupReconciler) kubeSecurityGroupDeleteOwnedChildren(ctx context.Context, k *v1alpha1.SecurityGroup, _ *arubatypes.SecurityGroupResponse) error {
	labelKey, _ := ownerLabelKey(r.Scheme, k)
	return deleteOwnedChildren(ctx, r.Client, k, labelKey,
		&v1alpha1.SecurityRuleList{},
	)
}

// Transition Set Builder

func (r *SecurityGroupReconciler) newTransitionSet() *reconciler.TransitionSet[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse] {
	ts := &reconciler.TransitionSet[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse]{
		DefaultRequeue: reconciler.NoRequeue[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		DefaultRequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
	}

	// 0. PhaseTimedOut — safety net: fail if stuck in a transitory phase too long
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse]{
		Name: "PhaseTimedOut",
		KCondition: reconciler.KubePhaseTimedOut[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		ACondition: reconciler.AlwaysTrue[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		KAction: r.kubeSetFailedOnTimeout,
		Requeue: reconciler.NoRequeue[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
	})

	// 1. ShouldBeDeleted
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse]{
		Name: "ShouldBeDeleted",
		KCondition: reconciler.KubeShouldDelete[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		ACondition: cmpSecurityGroupIsFinal,
		KAction: r.kubeMarkToDelete,
		Requeue: reconciler.ShortRequeue[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
	})

	// 2. ShouldDeleteTimedOut — enter deletion flow for timed-out resources (except those that timed out during Deleting)
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse]{
		Name: "ShouldDeleteTimedOut",
		KCondition: reconciler.KubeShouldDeleteTimedOut[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		ACondition: reconciler.AlwaysTrue[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		KAction: r.kubeMarkToDelete,
		Requeue: reconciler.ShortRequeue[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
	})

	// 3. WaitingChildrenDeletion — block CMP delete until all owned K8s children are gone.
	// The kAction explicitly deletes children because the K8s GC only cascades after the
	// owner is fully removed from etcd (impossible while the SecurityGroup finalizer is present).
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse]{
		Name: "WaitingChildrenDeletion",
		KCondition: func(k *v1alpha1.SecurityGroup, a *arubatypes.SecurityGroupResponse) bool {
			return reconciler.KubeShouldBeDeletedOnCMP(k, a) && r.kubeSecurityGroupHasOwnedChildren(k, a)
		},
		ACondition: cmpSecurityGroupIsFinal,
		KAction: r.kubeSecurityGroupDeleteOwnedChildren,
		Requeue: reconciler.LongRequeue[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		RequeueOnError: reconciler.ShortRequeueAndIgnoreError[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
	})

	// 4. ShouldBeDeletedOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse]{
		Name: "ShouldBeDeletedOnCMP",
		KCondition: reconciler.KubeShouldBeDeletedOnCMP[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		ACondition: cmpSecurityGroupIsFinal,
		AAction: r.cmpDelete,
		KActionOnASuccess: r.kubeMarkDeleting,
		KActionOnAError: reconciler.KubeSetErrorMessageOnCMPError[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse](r.Client),
		Requeue: reconciler.ShortRequeue[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		RequeueOnError: reconciler.SmartRequeueOnError[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
	})

	// 4. DeletionOnCMPNotNeeded — resource marked for deletion but CMP resource doesn't exist; skip CMP delete
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse]{
		Name: "DeletionOnCMPNotNeeded",
		KCondition: reconciler.KubeShouldBeDeletedOnCMP[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		ACondition: cmpSecurityGroupNotExists,
		KAction: r.kubeMarkDeletingDone,
		Requeue: reconciler.ShortRequeue[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
	})

	// 5. WaitingDeletionOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse]{
		Name: "WaitingDeletionOnCMP",
		KCondition: reconciler.KubeWaitingDeletionOnCMP[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		ACondition: cmpSecurityGroupIsTransitory,
		Requeue: reconciler.LongRequeue[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
	})

	// 6. DeletionConfirmedOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse]{
		Name: "DeletionConfirmedOnCMP",
		KCondition: reconciler.KubeWaitingDeletionOnCMP[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		ACondition: cmpSecurityGroupNotExists,
		KAction: r.kubeMarkDeletingDone,
		Requeue: reconciler.ShortRequeue[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
	})

	// 7. DeletionAccomplished
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse]{
		Name: "DeletionAccomplished",
		KCondition: reconciler.KubeDeletionAccomplished[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		ACondition: cmpSecurityGroupNotExists,
		KAction: r.kubeMarkDeleted,
		Requeue: reconciler.ShortRequeue[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
	})

	// 8. HasDeniedChanges — surface immutable field violations before attempting update
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse]{
		Name: "HasDeniedChanges",
		KCondition: kubeSecurityGroupHasDeniedChanges,
		ACondition: cmpSecurityGroupIsFinal,
		KAction: func(ctx context.Context, kubeSG *v1alpha1.SecurityGroup, cmpSG *arubatypes.SecurityGroupResponse) error {
			return fmt.Errorf("security group update rejected: %w", checkSecurityGroupDeniedChanges(kubeSG, cmpSG))
		},
		Requeue: reconciler.NoRequeue[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		RequeueOnError: reconciler.LongRequeueAndIgnoreError[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
	})

	// 9. SpecAlreadyInSyncWithCMP — generation changed but spec identical to CMP; just re-stamp ObservedGeneration
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse]{
		Name: "SpecAlreadyInSyncWithCMP",
		KCondition: kubeSecurityGroupSpecInSyncWithCMP,
		ACondition: cmpSecurityGroupIsActive,
		KAction: r.kubeSetActiveAndSetID,
		Requeue: reconciler.NoRequeue[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
	})

	// 10. ShouldBeUpdated — spec changed and CMP is ready
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse]{
		Name: "ShouldBeUpdated",
		KCondition: kubeSecurityGroupShouldUpdate,
		ACondition: cmpSecurityGroupIsFinal,
		KAction: r.kubeMarkToUpdate,
		Requeue: reconciler.ShortRequeue[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
	})

	// 11. ShouldBeUpdatedOnCMP — send update to CMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse]{
		Name: "ShouldBeUpdatedOnCMP",
		KCondition: reconciler.KubeShouldBeUpdatedOnCMP[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		ACondition: cmpSecurityGroupIsFinal,
		AAction: r.cmpUpdate,
		KActionOnASuccess: r.kubeMarkUpdating,
		KActionOnAError: reconciler.KubeSetErrorMessageOnCMPError[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse](r.Client),
		Requeue: reconciler.ShortRequeue[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		RequeueOnError: reconciler.SmartRequeueOnError[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
	})

	// 12. WaitingUpdateOnCMP — CMP is still processing the update
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse]{
		Name: "WaitingUpdateOnCMP",
		KCondition: reconciler.KubeWaitingUpdateOnCMP[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		ACondition: cmpSecurityGroupIsTransitory,
		Requeue: reconciler.LongRequeue[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
	})

	// 13. UpdateConfirmedOnCMP — CMP has settled; advance to Synchronized
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse]{
		Name: "UpdateConfirmedOnCMP",
		KCondition: reconciler.KubeWaitingUpdateOnCMP[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		ACondition: cmpSecurityGroupIsFinal,
		KAction: r.kubeMarkUpdatingDone,
		Requeue: reconciler.ShortRequeue[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
	})

	// 14. UpdateAccomplished — transition back to Active and stamp generation
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse]{
		Name: "UpdateAccomplished",
		KCondition: reconciler.KubeUpdateAccomplished[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		ACondition: cmpSecurityGroupIsActive,
		KAction: r.kubeSetActiveAndSetID,
		Requeue: reconciler.NoRequeue[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
	})

	// 15. ShouldBeCreated
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse]{
		Name: "ShouldBeCreated",
		KCondition: reconciler.KubeIsFirstReconciliation[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		ACondition: cmpSecurityGroupNotExists,
		KAction: r.kubeMarkToCreate,
		Requeue: reconciler.ShortRequeue[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
	})

	// 16. ShouldBeCreatedInCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse]{
		Name: "ShouldBeCreatedInCMP",
		KCondition: reconciler.KubeShouldBeCreatedOnCMP[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		ACondition: cmpSecurityGroupNotExists,
		AAction: r.cmpCreate,
		KActionOnASuccess: r.kubeMarkCreating,
		KActionOnAError: reconciler.KubeSetErrorMessageOnCMPError[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse](r.Client),
		Requeue: reconciler.ShortRequeue[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		RequeueOnError: reconciler.SmartRequeueOnError[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
	})

	// 17. WaitingCreationInCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse]{
		Name: "WaitingCreationInCMP",
		KCondition: reconciler.KubeWaitingCreationInCMP[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		ACondition: cmpSecurityGroupNotExistsOrTransitory,
		Requeue: reconciler.LongRequeue[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
	})

	// 18. CreationConfirmedOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse]{
		Name: "CreationConfirmedOnCMP",
		KCondition: reconciler.KubeWaitingCreationInCMP[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		ACondition: cmpSecurityGroupIsActive,
		KAction: r.kubeMarkCreatingDone,
		Requeue: reconciler.ShortRequeue[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
	})

	// 19. CreationAccomplished
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse]{
		Name: "CreationAccomplished",
		KCondition: reconciler.KubeIsCreatedOnCMP[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		ACondition: cmpSecurityGroupIsActive,
		KAction: r.kubeSetActiveAndSetID,
		Requeue: reconciler.NoRequeue[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
	})

	// 20. IsInError
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse]{
		Name: "IsInError",
		KCondition: reconciler.AlwaysTrue[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		ACondition: cmpSecurityGroupIsFailed,
		KAction: r.kubeSetFailed,
		Requeue: reconciler.NoRequeue[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityGroup, *arubatypes.SecurityGroupResponse],
	})

	return ts
}

// Resource-specific condition functions

func kubeSecurityGroupHasDeniedChanges(kubeSG *v1alpha1.SecurityGroup, cmpSG *arubatypes.SecurityGroupResponse) bool {
	if !kubeSG.DeletionTimestamp.IsZero() {
		return false
	}
	if cmpSG == nil {
		return false
	}
	return checkSecurityGroupDeniedChanges(kubeSG, cmpSG) != nil
}

func kubeSecurityGroupSpecInSyncWithCMP(kubeSG *v1alpha1.SecurityGroup, cmpSG *arubatypes.SecurityGroupResponse) bool {
	return reconciler.KubeActiveAndGenerationChanged(kubeSG, cmpSG) &&
		checkSecurityGroupDeniedChanges(kubeSG, cmpSG) == nil &&
		!kubeSecurityGroupNeedsUpdate(kubeSG, cmpSG)
}

func kubeSecurityGroupShouldUpdate(kubeSG *v1alpha1.SecurityGroup, cmpSG *arubatypes.SecurityGroupResponse) bool {
	return reconciler.KubeActiveAndGenerationChanged(kubeSG, cmpSG) &&
		checkSecurityGroupDeniedChanges(kubeSG, cmpSG) == nil &&
		kubeSecurityGroupNeedsUpdate(kubeSG, cmpSG)
}

func cmpSecurityGroupNotExists(_ *v1alpha1.SecurityGroup, cmpSG *arubatypes.SecurityGroupResponse) bool {
	return cmpSG == nil
}

func cmpSecurityGroupIsFinal(_ *v1alpha1.SecurityGroup, cmpSG *arubatypes.SecurityGroupResponse) bool {
	if cmpSG == nil || cmpSG.Status.State == nil {
		return false
	}
	return reconciler.AssessCSPResourceStateNature(&cmpSG.Status) == reconciler.CSPResourceStateNatureFinal
}

func cmpSecurityGroupIsTransitory(_ *v1alpha1.SecurityGroup, cmpSG *arubatypes.SecurityGroupResponse) bool {
	if cmpSG == nil || cmpSG.Status.State == nil {
		return false
	}
	return reconciler.AssessCSPResourceStateNature(&cmpSG.Status) == reconciler.CSPResourceStateNatureTransitory
}

func cmpSecurityGroupNotExistsOrTransitory(_ *v1alpha1.SecurityGroup, cmpSG *arubatypes.SecurityGroupResponse) bool {
	if cmpSG == nil {
		return true
	}
	if cmpSG.Status.State == nil {
		return false
	}
	return reconciler.AssessCSPResourceStateNature(&cmpSG.Status) == reconciler.CSPResourceStateNatureTransitory
}

func cmpSecurityGroupIsActive(_ *v1alpha1.SecurityGroup, cmpSG *arubatypes.SecurityGroupResponse) bool {
	return cmpSG != nil && cmpSG.Status.State != nil &&
		*cmpSG.Status.State == reconciler.CSPResourceStateActive
}

func cmpSecurityGroupIsFailed(_ *v1alpha1.SecurityGroup, cmpSG *arubatypes.SecurityGroupResponse) bool {
	return cmpSG != nil && cmpSG.Status.State != nil && *cmpSG.Status.State == reconciler.CSPResourceStateFailed
}

// Kube action methods

func (r *SecurityGroupReconciler) kubeSetPhaseAndCondition(ctx context.Context, kubeSG *v1alpha1.SecurityGroup, phase v1alpha1.ResourcePhase, reason string, _ error) error {
	return reconciler.SetPhaseAndCondition(r.Client, ctx, kubeSG, phase, reason, nil, func(sg *v1alpha1.SecurityGroup) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && sg.Status.ProjectID == "" {
			sg.Status.ProjectID = prjID
		}
		if vID, ok := ctx.Value(vpcIDKey).(string); ok && sg.Status.VPCID == "" {
			sg.Status.VPCID = vID
		}
	})
}

func (r *SecurityGroupReconciler) kubeMarkToDelete(ctx context.Context, kubeSG *v1alpha1.SecurityGroup, _ *arubatypes.SecurityGroupResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSG, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *SecurityGroupReconciler) kubeMarkDeleting(ctx context.Context, kubeSG *v1alpha1.SecurityGroup, _ *arubatypes.SecurityGroupResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSG, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *SecurityGroupReconciler) kubeMarkDeletingDone(ctx context.Context, kubeSG *v1alpha1.SecurityGroup, _ *arubatypes.SecurityGroupResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSG, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *SecurityGroupReconciler) kubeMarkDeleted(ctx context.Context, kubeSG *v1alpha1.SecurityGroup, _ *arubatypes.SecurityGroupResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSG, v1alpha1.ResourcePhaseDeleted, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *SecurityGroupReconciler) kubeMarkToUpdate(ctx context.Context, kubeSG *v1alpha1.SecurityGroup, _ *arubatypes.SecurityGroupResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSG, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *SecurityGroupReconciler) kubeMarkUpdating(ctx context.Context, kubeSG *v1alpha1.SecurityGroup, _ *arubatypes.SecurityGroupResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSG, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *SecurityGroupReconciler) kubeMarkUpdatingDone(ctx context.Context, kubeSG *v1alpha1.SecurityGroup, _ *arubatypes.SecurityGroupResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSG, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *SecurityGroupReconciler) kubeMarkToCreate(ctx context.Context, kubeSG *v1alpha1.SecurityGroup, _ *arubatypes.SecurityGroupResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSG, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *SecurityGroupReconciler) kubeMarkCreating(ctx context.Context, kubeSG *v1alpha1.SecurityGroup, _ *arubatypes.SecurityGroupResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSG, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *SecurityGroupReconciler) kubeMarkCreatingDone(ctx context.Context, kubeSG *v1alpha1.SecurityGroup, _ *arubatypes.SecurityGroupResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSG, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *SecurityGroupReconciler) kubeSetActiveAndSetID(ctx context.Context, kubeSG *v1alpha1.SecurityGroup, cmpSG *arubatypes.SecurityGroupResponse) error {
	cmpID := ""
	if cmpSG != nil && cmpSG.Metadata.ID != nil {
		cmpID = *cmpSG.Metadata.ID
	}
	return reconciler.SetActiveAndSetID(r.Client, ctx, kubeSG, cmpID, nil, func(sg *v1alpha1.SecurityGroup) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && sg.Status.ProjectID != "" {
			sg.Status.ProjectID = prjID
		}
		if vID, ok := ctx.Value(vpcIDKey).(string); ok && sg.Status.VPCID != "" {
			sg.Status.VPCID = vID
		}
	})
}

func (r *SecurityGroupReconciler) kubeSetFailedOnTimeout(ctx context.Context, kubeSG *v1alpha1.SecurityGroup, _ *arubatypes.SecurityGroupResponse) error {
	return reconciler.SetFailedOnTimeout(r.Client, ctx, kubeSG, func(sg *v1alpha1.SecurityGroup) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && sg.Status.ProjectID == "" {
			sg.Status.ProjectID = prjID
		}
		if vID, ok := ctx.Value(vpcIDKey).(string); ok && sg.Status.VPCID == "" {
			sg.Status.VPCID = vID
		}
	})
}

func (r *SecurityGroupReconciler) kubeSetFailed(ctx context.Context, kubeSG *v1alpha1.SecurityGroup, _ *arubatypes.SecurityGroupResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSG, v1alpha1.ResourcePhaseFailed, v1alpha1.ConditionReasonSynchronized, nil)
}

// CMP action methods

func (r *SecurityGroupReconciler) cmpDelete(ctx context.Context, _ *v1alpha1.SecurityGroup, cmpSG *arubatypes.SecurityGroupResponse) error {
	prjID := ctx.Value(projectIDKey).(string)
	vID := ctx.Value(vpcIDKey).(string)
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)

	cmpResp, err := arubaClient.FromNetwork().SecurityGroups().Delete(ctx, prjID, vID, *cmpSG.Metadata.ID, nil)
	if err != nil {
		return reconciler.CMPTransportError("delete", *cmpSG.Metadata.Name, err)
	}
	return reconciler.CMPCheckResponse("delete", *cmpSG.Metadata.Name, cmpResp,
		http.StatusOK, http.StatusAccepted, http.StatusNoContent, http.StatusNotFound)
}

func (r *SecurityGroupReconciler) cmpUpdate(ctx context.Context, kubeSG *v1alpha1.SecurityGroup, cmpSG *arubatypes.SecurityGroupResponse) error {
	prjID := ctx.Value(projectIDKey).(string)
	vID := ctx.Value(vpcIDKey).(string)
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)

	// Guard: should have been caught by HasDeniedChanges, but double-check.
	if err := checkSecurityGroupDeniedChanges(kubeSG, cmpSG); err != nil {
		return err
	}

	request := buildSecurityGroupUpdateRequest(kubeSG, cmpSG)

	cmpResp, err := arubaClient.FromNetwork().SecurityGroups().Update(ctx, prjID, vID, *cmpSG.Metadata.ID, *request, nil)
	if err != nil {
		return reconciler.CMPTransportError("update", kubeSG.Name, err)
	}
	return reconciler.CMPCheckResponse("update", kubeSG.Name, cmpResp,
		http.StatusOK, http.StatusAccepted, http.StatusNoContent)
}

func (r *SecurityGroupReconciler) cmpCreate(ctx context.Context, kubeSG *v1alpha1.SecurityGroup, _ *arubatypes.SecurityGroupResponse) error {
	prjID := ctx.Value(projectIDKey).(string)
	vID := ctx.Value(vpcIDKey).(string)
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)

	cmpResp, err := arubaClient.FromNetwork().SecurityGroups().Create(ctx, prjID, vID, *cmpSecurityGroupRequestFromKube(kubeSG), nil)
	if err != nil {
		return reconciler.CMPTransportError("create", kubeSG.Name, err)
	}
	return reconciler.CMPCheckResponse("create", kubeSG.Name, cmpResp,
		http.StatusOK, http.StatusCreated, http.StatusAccepted)
}

// Helper functions

func checkSecurityGroupDeniedChanges(kubeSG *v1alpha1.SecurityGroup, cmpSG *arubatypes.SecurityGroupResponse) error {
	if cmpSG == nil {
		return nil
	}

	if cmpSG.Metadata.LocationResponse != nil &&
		cmpSG.Metadata.LocationResponse.Value != "" &&
		kubeSG.Spec.Region != cmpSG.Metadata.LocationResponse.Value {
		return fmt.Errorf("%w: %w", reconciler.ErrNotAllowedChanges, errors.New("change the 'location' is not allowed"))
	}

	return nil
}

func kubeSecurityGroupNeedsUpdate(kubeSG *v1alpha1.SecurityGroup, cmpSG *arubatypes.SecurityGroupResponse) bool {
	if cmpSG == nil {
		return false
	}
	if !reconciler.TagsAreEqual(kubeSG.Spec.Tags, cmpSG.Metadata.Tags) {
		return true
	}
	return false
}

func cmpSecurityGroupRequestFromKube(kubeSG *v1alpha1.SecurityGroup) *arubatypes.SecurityGroupRequest {
	tags := make([]string, len(kubeSG.Spec.Tags))
	copy(tags, kubeSG.Spec.Tags)
	defaultVal := false
	return &arubatypes.SecurityGroupRequest{
		Metadata: arubatypes.ResourceMetadataRequest{
			Name: kubeSG.Name,
			Tags: tags,
		},
		Properties: arubatypes.SecurityGroupPropertiesRequest{
			Default: &defaultVal,
		},
	}
}

func cmpSecurityGroupRequestFromCMP(cmpSG *arubatypes.SecurityGroupResponse) *arubatypes.SecurityGroupRequest {
	if cmpSG == nil {
		return nil
	}
	name := ""
	if cmpSG.Metadata.Name != nil {
		name = *cmpSG.Metadata.Name
	}
	tags := make([]string, len(cmpSG.Metadata.Tags))
	copy(tags, cmpSG.Metadata.Tags)
	defaultVal := cmpSG.Properties.Default
	return &arubatypes.SecurityGroupRequest{
		Metadata: arubatypes.ResourceMetadataRequest{
			Name: name,
			Tags: tags,
		},
		Properties: arubatypes.SecurityGroupPropertiesRequest{
			Default: &defaultVal,
		},
	}
}

func buildSecurityGroupUpdateRequest(kubeSG *v1alpha1.SecurityGroup, cmpSG *arubatypes.SecurityGroupResponse) *arubatypes.SecurityGroupRequest {
	request := cmpSecurityGroupRequestFromCMP(cmpSG)
	if request == nil {
		return nil
	}
	tags := make([]string, len(kubeSG.Spec.Tags))
	copy(tags, kubeSG.Spec.Tags)
	request.Metadata.Tags = tags
	defaultVal := false
	request.Properties.Default = &defaultVal
	return request
}
