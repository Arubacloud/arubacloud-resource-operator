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
	securityRuleFinalizerName            = "securityrule.arubacloud.com/finalizer"
	securityGroupIDKey        contextKey = "securityGroupID"
)

// SecurityRuleReconciler reconciles a SecurityRule object
type SecurityRuleReconciler struct {
	*reconciler.Reconciler
	ts *TransitionSet[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]
}

// NewSecurityRuleReconciler creates a new SecurityRuleReconciler
func NewSecurityRuleReconciler(baseReconciler *reconciler.Reconciler) *SecurityRuleReconciler {
	r := &SecurityRuleReconciler{
		Reconciler: baseReconciler,
	}

	r.ts = r.newTransitionSet()

	return r
}

// +kubebuilder:rbac:groups=arubacloud.com,resources=securityrules,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=arubacloud.com,resources=securityrules/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=arubacloud.com,resources=securityrules/finalizers,verbs=update
// +kubebuilder:rbac:groups=arubacloud.com,resources=projects,verbs=get;list;watch
// +kubebuilder:rbac:groups=arubacloud.com,resources=vpcs,verbs=get;list;watch
// +kubebuilder:rbac:groups=arubacloud.com,resources=securitygroups,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

func (r *SecurityRuleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.Reconciler.Reconcile(ctx, req, r)
}

func (r *SecurityRuleReconciler) Object() reconciler.ResourceObject {
	return &v1alpha1.SecurityRule{}
}

func (r *SecurityRuleReconciler) Finalizer() string {
	return securityRuleFinalizerName
}

// SetupWithManager sets up the controller with the Manager.
func (r *SecurityRuleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.SecurityRule{}).
		Named("securityrule").
		Complete(r)
}

//nolint:gocyclo // complexity is intentional to maintain locality of behavior
func (r *SecurityRuleReconciler) HandleReconcile(ctx context.Context, obj reconciler.ResourceObject) (ctrl.Result, error) {
	kubeSR, ok := obj.(*v1alpha1.SecurityRule)
	if !ok {
		return ctrl.Result{}, errors.New("obj is not a *v1alpha1.SecurityRule")
	}

	logger := log.FromContext(ctx).WithValues("tenant", kubeSR.Spec.Tenant)
	logger.Info("reconciling SecurityRule")

	arubaClient, err := r.ArubaClient(kubeSR.Spec.Tenant)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get Aruba client: %w", err)
	}

	if kubeSR.Spec.ProjectReference.Name == "" {
		return ctrl.Result{}, fmt.Errorf("project reference is not valid")
	}
	if kubeSR.Spec.VpcReference.Name == "" {
		return ctrl.Result{}, fmt.Errorf("vpc reference is not valid")
	}
	if kubeSR.Spec.SecurityGroupReference.Name == "" {
		return ctrl.Result{}, fmt.Errorf("security group reference is not valid")
	}

	// Set OwnerReference to SecurityGroup (skipped during deletion — the resource is going away).
	if kubeSR.GetDeletionTimestamp().IsZero() {
		kubeSG := &v1alpha1.SecurityGroup{}
		if err := resolveOwnerObject(ctx, r.Client, kubeSR.Spec.SecurityGroupReference, kubeSR.Namespace, kubeSG); err != nil {
			if !apierrors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("resolving parent security group for owner reference: %w", err)
			}
			logger.V(1).Info("parent security group not found for owner reference setup, skipping",
				"securityGroupName", kubeSR.Spec.SecurityGroupReference.Name)
		} else {
			requeue, err := ensureOwnerReference(ctx, r.Client, r.Scheme, kubeSG, kubeSR)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("setting owner reference on security rule: %w", err)
			}
			if requeue {
				return ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
			}
		}
	}

	srName := kubeSR.Name
	projectName := kubeSR.Spec.ProjectReference.Name
	vpcName := kubeSR.Spec.VpcReference.Name
	sgName := kubeSR.Spec.SecurityGroupReference.Name
	prjFilter := fmt.Sprintf(`name:eq("%s")`, projectName)
	vpcFilter := fmt.Sprintf(`name:eq("%s")`, vpcName)
	sgFilter := fmt.Sprintf(`name:eq("%s")`, sgName)
	srFilter := fmt.Sprintf(`name:eq("%s")`, srName)

	// --- Resolve Project ID ---

	var prjID string

	if !kubeSR.GetDeletionTimestamp().IsZero() && kubeSR.Status.ProjectID != "" {
		prjID = kubeSR.Status.ProjectID
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
		if cmpProjectList.Data.Total == 0 && kubeSR.Status.ProjectID != "" {
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

	if kubeSR.Status.ProjectID != "" && kubeSR.Status.ProjectID != prjID {
		return ctrl.Result{}, fmt.Errorf(
			"inconsistent project id in security rule: sr_name: '%s', sr_project_id: '%s', project_name: '%s', project_id: '%s'",
			srName, kubeSR.Status.ProjectID, projectName, prjID,
		)
	}

	// --- Resolve VPC ID ---

	var vpcID string

	if !kubeSR.GetDeletionTimestamp().IsZero() && kubeSR.Status.VpcID != "" {
		vpcID = kubeSR.Status.VpcID
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
		if cmpVpcList.Data.Total == 0 && kubeSR.Status.VpcID != "" {
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

	if kubeSR.Status.VpcID != "" && kubeSR.Status.VpcID != vpcID {
		return ctrl.Result{}, fmt.Errorf(
			"inconsistent vpc id in security rule: sr_name: '%s', sr_vpc_id: '%s', vpc_name: '%s', vpc_id: '%s'",
			srName, kubeSR.Status.VpcID, vpcName, vpcID,
		)
	}

	// --- Resolve SecurityGroup ID ---

	var sgID string

	if !kubeSR.GetDeletionTimestamp().IsZero() && kubeSR.Status.SecurityGroupID != "" {
		sgID = kubeSR.Status.SecurityGroupID
	} else {
		cmpSGList, err := arubaClient.FromNetwork().SecurityGroups().List(ctx, prjID, vpcID, &arubatypes.RequestParameters{Filter: &sgFilter})
		if err != nil {
			return ctrl.Result{}, fmt.Errorf(
				"failed to find security group in Aruba cloud: %w, sg_name: '%s', sg_filter: '%s', project_name: '%s', vpc_name: '%s'",
				err, sgName, sgFilter, projectName, vpcName,
			)
		}
		if cmpSGList.IsError() {
			return ctrl.Result{}, fmt.Errorf(
				"failed to find security group in Aruba cloud: status_code: %d, sg_name: '%s', project_name: '%s', vpc_name: '%s'",
				cmpSGList.StatusCode, sgName, projectName, vpcName,
			)
		}
		// TODO: Remove once CMP API name:eq() filter is fixed (issue https://jira.aruba.it/browse/DEV-66643).
		applyNameFilterToSecurityGroupList(cmpSGList, sgName, logger)
		if cmpSGList.Data.Total == 0 && kubeSR.Status.SecurityGroupID != "" {
			return ctrl.Result{}, fmt.Errorf(
				"inconsistent data in security group list: expected: 1, sg not found: sg_name: '%s', sg_filter: '%s'", sgName, sgFilter,
			)
		}
		if cmpSGList.Data.Total > 1 {
			return ctrl.Result{}, fmt.Errorf(
				"inconsistent data in security group list: expected: 1, found: %d, sg_name: '%s', sg_filter: '%s'",
				cmpSGList.Data.Total, sgName, sgFilter,
			)
		}
		if cmpSGList.Data.Total == 0 {
			logger.V(1).Info("parent security group not found on CMP, requeuing", "sgName", sgName)
			return ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
		}
		sgID = *(cmpSGList.Data.Values[0].Metadata.ID)
	}

	if kubeSR.Status.SecurityGroupID != "" && kubeSR.Status.SecurityGroupID != sgID {
		return ctrl.Result{}, fmt.Errorf(
			"inconsistent security group id in security rule: sr_name: '%s', sr_sg_id: '%s', sg_name: '%s', sg_id: '%s'",
			srName, kubeSR.Status.SecurityGroupID, sgName, sgID,
		)
	}

	// --- Fetch CMP SecurityRule ---

	cmpSRList, err := arubaClient.FromNetwork().SecurityGroupRules().List(ctx, prjID, vpcID, sgID, &arubatypes.RequestParameters{Filter: &srFilter})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf(
			"failed to find security rule in Aruba cloud: %w, sr_name: '%s', sr_filter: '%s', project_name: '%s', vpc_name: '%s', sg_name: '%s'",
			err, srName, srFilter, projectName, vpcName, sgName,
		)
	}
	if cmpSRList.IsError() && cmpSRList.StatusCode != http.StatusNotFound {
		return ctrl.Result{}, fmt.Errorf(
			"failed to find security rule in Aruba cloud: status_code: %d, sr_name: '%s', project_name: '%s', vpc_name: '%s', sg_name: '%s'",
			cmpSRList.StatusCode, srName, projectName, vpcName, sgName,
		)
	}
	// TODO: Remove once CMP API name:eq() filter is fixed (issue https://jira.aruba.it/browse/DEV-66643).
	applyNameFilterToSecurityRuleList(cmpSRList, srName, logger)
	if !cmpSRList.IsError() && (cmpSRList.Data.Total < 0 || cmpSRList.Data.Total > 1) {
		return ctrl.Result{}, fmt.Errorf(
			"inconsistent data in security rule list: sr_name: '%s', sr_filter: '%s', project_name: '%s', vpc_name: '%s', sg_name: '%s', instances: %d",
			srName, srFilter, projectName, vpcName, sgName, cmpSRList.Data.Total,
		)
	}

	var cmpSR *arubatypes.SecurityRuleResponse
	if cmpSRList.Data != nil && cmpSRList.Data.Total == 1 {
		cmpSR = &cmpSRList.Data.Values[0]
	}
	logger.V(1).Info("CMP SecurityRule state", "found", cmpSR != nil, "projectID", prjID, "vpcID", vpcID, "sgID", sgID)

	ctx = context.WithValue(ctx, projectIDKey, prjID)
	ctx = context.WithValue(ctx, vpcIDKey, vpcID)
	ctx = context.WithValue(ctx, securityGroupIDKey, sgID)
	ctx = context.WithValue(ctx, reconciler.ArubaClientKey, arubaClient)
	ctx = log.IntoContext(ctx, logger)

	return r.ts.Run(ctx, kubeSR, cmpSR)
}

// Transition Set Builder

func (r *SecurityRuleReconciler) newTransitionSet() *TransitionSet[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse] {
	ts := &TransitionSet[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		defaultRequeue:        NoRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		defaultRequeueOnError: NoRequeueButIgnoreError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	}

	// 0. PhaseTimedOut — safety net: fail if stuck in a transitory phase too long
	ts.Add(&AbstractTransition[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		name:           "PhaseTimedOut",
		kCondition:     kubePhaseTimedOut[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		aCondition:     AlwaysTrue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		kAction:        r.kubeSetFailedOnTimeout,
		requeue:        NoRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	})

	// 1. ShouldBeDeleted
	ts.Add(&AbstractTransition[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		name:           "ShouldBeDeleted",
		kCondition:     kubeShouldDelete[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		aCondition:     cmpSecurityRuleIsFinal,
		kAction:        r.kubeMarkToDelete,
		requeue:        ShortRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	})

	// 2. ShouldDeleteTimedOut — enter deletion flow for timed-out resources (except those that timed out during Deleting)
	ts.Add(&AbstractTransition[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		name:           "ShouldDeleteTimedOut",
		kCondition:     kubeShouldDeleteTimedOut[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		aCondition:     AlwaysTrue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		kAction:        r.kubeMarkToDelete,
		requeue:        ShortRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	})

	// 3. ShouldBeDeletedOnCMP
	ts.Add(&AbstractTransition[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		name:              "ShouldBeDeletedOnCMP",
		kCondition:        kubeShouldBeDeletedOnCMP[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		aCondition:        cmpSecurityRuleIsFinal,
		aAction:           r.cmpDelete,
		kActionOnASuccess: r.kubeMarkDeleting,
		kActionOnAError:   kubeSetErrorMessageOnCMPError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse](r.Client),
		requeue:           ShortRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		requeueOnError:    SmartRequeueOnError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	})

	// 4. DeletionOnCMPNotNeeded — resource marked for deletion but CMP resource doesn't exist; skip CMP delete
	ts.Add(&AbstractTransition[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		name:           "DeletionOnCMPNotNeeded",
		kCondition:     kubeShouldBeDeletedOnCMP[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		aCondition:     cmpSecurityRuleNotExists,
		kAction:        r.kubeMarkDeletingDone,
		requeue:        ShortRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	})

	// 5. WaitingDeletionOnCMP
	ts.Add(&AbstractTransition[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		name:           "WaitingDeletionOnCMP",
		kCondition:     kubeWaitingDeletionOnCMP[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		aCondition:     cmpSecurityRuleIsTransitory,
		requeue:        LongRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	})

	// 6. DeletionConfirmedOnCMP
	ts.Add(&AbstractTransition[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		name:           "DeletionConfirmedOnCMP",
		kCondition:     kubeWaitingDeletionOnCMP[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		aCondition:     cmpSecurityRuleNotExists,
		kAction:        r.kubeMarkDeletingDone,
		requeue:        ShortRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	})

	// 7. DeletionAccomplished
	ts.Add(&AbstractTransition[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		name:           "DeletionAccomplished",
		kCondition:     kubeDeletionAccomplished[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		aCondition:     cmpSecurityRuleNotExists,
		kAction:        r.kubeMarkDeleted,
		requeue:        ShortRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	})

	// 8. HasDeniedChanges — surface immutable field violations before attempting update
	ts.Add(&AbstractTransition[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		name:       "HasDeniedChanges",
		kCondition: kubeSecurityRuleHasDeniedChanges,
		aCondition: cmpSecurityRuleIsFinal,
		kAction: func(ctx context.Context, kubeSR *v1alpha1.SecurityRule, cmpSR *arubatypes.SecurityRuleResponse) error {
			return fmt.Errorf("security rule update rejected: %w", checkSecurityRuleDeniedChanges(kubeSR, cmpSR))
		},
		requeue:        NoRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		requeueOnError: LongRequeueAndIgnoreError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	})

	// 9. SpecAlreadyInSyncWithCMP — generation changed but spec identical to CMP; just re-stamp ObservedGeneration
	ts.Add(&AbstractTransition[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		name:           "SpecAlreadyInSyncWithCMP",
		kCondition:     kubeSecurityRuleSpecInSyncWithCMP,
		aCondition:     cmpSecurityRuleIsActive,
		kAction:        r.kubeSetActiveAndSetID,
		requeue:        NoRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	})

	// 10. ShouldBeUpdated — spec changed and CMP is ready
	ts.Add(&AbstractTransition[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		name:           "ShouldBeUpdated",
		kCondition:     kubeSecurityRuleShouldUpdate,
		aCondition:     cmpSecurityRuleIsFinal,
		kAction:        r.kubeMarkToUpdate,
		requeue:        ShortRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	})

	// 11. ShouldBeUpdatedOnCMP — send update to CMP
	ts.Add(&AbstractTransition[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		name:              "ShouldBeUpdatedOnCMP",
		kCondition:        kubeShouldBeUpdatedOnCMP[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		aCondition:        cmpSecurityRuleIsFinal,
		aAction:           r.cmpUpdate,
		kActionOnASuccess: r.kubeMarkUpdating,
		kActionOnAError:   kubeSetErrorMessageOnCMPError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse](r.Client),
		requeue:           ShortRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		requeueOnError:    SmartRequeueOnError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	})

	// 12. WaitingUpdateOnCMP — CMP is still processing the update
	ts.Add(&AbstractTransition[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		name:           "WaitingUpdateOnCMP",
		kCondition:     kubeWaitingUpdateOnCMP[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		aCondition:     cmpSecurityRuleIsTransitory,
		requeue:        LongRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	})

	// 13. UpdateConfirmedOnCMP — CMP has settled; advance to Synchronized
	ts.Add(&AbstractTransition[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		name:           "UpdateConfirmedOnCMP",
		kCondition:     kubeWaitingUpdateOnCMP[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		aCondition:     cmpSecurityRuleIsFinal,
		kAction:        r.kubeMarkUpdatingDone,
		requeue:        ShortRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	})

	// 14. UpdateAccomplished — transition back to Active and stamp generation
	ts.Add(&AbstractTransition[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		name:           "UpdateAccomplished",
		kCondition:     kubeUpdateAccomplished[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		aCondition:     cmpSecurityRuleIsActive,
		kAction:        r.kubeSetActiveAndSetID,
		requeue:        NoRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	})

	// 15. ShouldBeCreated
	ts.Add(&AbstractTransition[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		name:           "ShouldBeCreated",
		kCondition:     kubeIsFirstReconciliation[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		aCondition:     cmpSecurityRuleNotExists,
		kAction:        r.kubeMarkToCreate,
		requeue:        ShortRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	})

	// 16. ShouldBeCreatedInCMP
	ts.Add(&AbstractTransition[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		name:              "ShouldBeCreatedInCMP",
		kCondition:        kubeShouldBeCreatedOnCMP[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		aCondition:        cmpSecurityRuleNotExists,
		aAction:           r.cmpCreate,
		kActionOnASuccess: r.kubeMarkCreating,
		kActionOnAError:   kubeSetErrorMessageOnCMPError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse](r.Client),
		requeue:           ShortRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		requeueOnError:    SmartRequeueOnError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	})

	// 17. WaitingCreationInCMP
	ts.Add(&AbstractTransition[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		name:           "WaitingCreationInCMP",
		kCondition:     kubeWaitingCreationInCMP[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		aCondition:     cmpSecurityRuleNotExistsOrTransitory,
		requeue:        LongRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	})

	// 18. CreationConfirmedOnCMP
	ts.Add(&AbstractTransition[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		name:           "CreationConfirmedOnCMP",
		kCondition:     kubeWaitingCreationInCMP[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		aCondition:     cmpSecurityRuleIsActive,
		kAction:        r.kubeMarkCreatingDone,
		requeue:        ShortRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	})

	// 19. CreationAccomplished
	ts.Add(&AbstractTransition[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		name:           "CreationAccomplished",
		kCondition:     kubeIsCreatedOnCMP[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		aCondition:     cmpSecurityRuleIsActive,
		kAction:        r.kubeSetActiveAndSetID,
		requeue:        NoRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	})

	// 20. IsInError
	ts.Add(&AbstractTransition[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		name:           "IsInError",
		kCondition:     AlwaysTrue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		aCondition:     cmpSecurityRuleIsFailed,
		kAction:        r.kubeSetFailed,
		requeue:        NoRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	})

	return ts
}

// Resource-specific condition functions

func kubeSecurityRuleHasDeniedChanges(kubeSR *v1alpha1.SecurityRule, cmpSR *arubatypes.SecurityRuleResponse) bool {
	if !kubeSR.DeletionTimestamp.IsZero() {
		return false
	}
	if cmpSR == nil {
		return false
	}
	return checkSecurityRuleDeniedChanges(kubeSR, cmpSR) != nil
}

func kubeSecurityRuleSpecInSyncWithCMP(kubeSR *v1alpha1.SecurityRule, cmpSR *arubatypes.SecurityRuleResponse) bool {
	return kubeActiveAndGenerationChanged(kubeSR, cmpSR) &&
		checkSecurityRuleDeniedChanges(kubeSR, cmpSR) == nil &&
		!kubeSecurityRuleNeedsUpdate(kubeSR, cmpSR)
}

func kubeSecurityRuleShouldUpdate(kubeSR *v1alpha1.SecurityRule, cmpSR *arubatypes.SecurityRuleResponse) bool {
	return kubeActiveAndGenerationChanged(kubeSR, cmpSR) &&
		checkSecurityRuleDeniedChanges(kubeSR, cmpSR) == nil &&
		kubeSecurityRuleNeedsUpdate(kubeSR, cmpSR)
}

func cmpSecurityRuleNotExists(_ *v1alpha1.SecurityRule, cmpSR *arubatypes.SecurityRuleResponse) bool {
	return cmpSR == nil
}

func cmpSecurityRuleIsFinal(_ *v1alpha1.SecurityRule, cmpSR *arubatypes.SecurityRuleResponse) bool {
	if cmpSR == nil || cmpSR.Status.State == nil {
		return false
	}
	return AssesCSPResourceStateNature(&cmpSR.Status) == CSPResourceStateNatureFinal
}

func cmpSecurityRuleIsTransitory(_ *v1alpha1.SecurityRule, cmpSR *arubatypes.SecurityRuleResponse) bool {
	if cmpSR == nil || cmpSR.Status.State == nil {
		return false
	}
	return AssesCSPResourceStateNature(&cmpSR.Status) == CSPResourceStateNatureTransitory
}

func cmpSecurityRuleNotExistsOrTransitory(_ *v1alpha1.SecurityRule, cmpSR *arubatypes.SecurityRuleResponse) bool {
	if cmpSR == nil {
		return true
	}
	if cmpSR.Status.State == nil {
		return false
	}
	return AssesCSPResourceStateNature(&cmpSR.Status) == CSPResourceStateNatureTransitory
}

func cmpSecurityRuleIsActive(_ *v1alpha1.SecurityRule, cmpSR *arubatypes.SecurityRuleResponse) bool {
	return cmpSR != nil && cmpSR.Status.State != nil &&
		*cmpSR.Status.State == CSPResourceStateActive
}

func cmpSecurityRuleIsFailed(_ *v1alpha1.SecurityRule, cmpSR *arubatypes.SecurityRuleResponse) bool {
	return cmpSR != nil && cmpSR.Status.State != nil && *cmpSR.Status.State == CSPResourceStateFailed
}

// Kube action methods

func (r *SecurityRuleReconciler) kubeSetPhaseAndCondition(ctx context.Context, kubeSR *v1alpha1.SecurityRule, phase v1alpha1.ResourcePhase, reason string, _ error) error {
	return setPhaseAndCondition(r.Client, ctx, kubeSR, phase, reason, nil, func(sr *v1alpha1.SecurityRule) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && sr.Status.ProjectID == "" {
			sr.Status.ProjectID = prjID
		}
		if vID, ok := ctx.Value(vpcIDKey).(string); ok && sr.Status.VpcID == "" {
			sr.Status.VpcID = vID
		}
		if sgID, ok := ctx.Value(securityGroupIDKey).(string); ok && sr.Status.SecurityGroupID == "" {
			sr.Status.SecurityGroupID = sgID
		}
	})
}

func (r *SecurityRuleReconciler) kubeMarkToDelete(ctx context.Context, kubeSR *v1alpha1.SecurityRule, _ *arubatypes.SecurityRuleResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSR, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *SecurityRuleReconciler) kubeMarkDeleting(ctx context.Context, kubeSR *v1alpha1.SecurityRule, _ *arubatypes.SecurityRuleResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSR, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *SecurityRuleReconciler) kubeMarkDeletingDone(ctx context.Context, kubeSR *v1alpha1.SecurityRule, _ *arubatypes.SecurityRuleResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSR, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *SecurityRuleReconciler) kubeMarkDeleted(ctx context.Context, kubeSR *v1alpha1.SecurityRule, _ *arubatypes.SecurityRuleResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSR, v1alpha1.ResourcePhaseDeleted, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *SecurityRuleReconciler) kubeMarkToUpdate(ctx context.Context, kubeSR *v1alpha1.SecurityRule, _ *arubatypes.SecurityRuleResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSR, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *SecurityRuleReconciler) kubeMarkUpdating(ctx context.Context, kubeSR *v1alpha1.SecurityRule, _ *arubatypes.SecurityRuleResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSR, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *SecurityRuleReconciler) kubeMarkUpdatingDone(ctx context.Context, kubeSR *v1alpha1.SecurityRule, _ *arubatypes.SecurityRuleResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSR, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *SecurityRuleReconciler) kubeMarkToCreate(ctx context.Context, kubeSR *v1alpha1.SecurityRule, _ *arubatypes.SecurityRuleResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSR, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *SecurityRuleReconciler) kubeMarkCreating(ctx context.Context, kubeSR *v1alpha1.SecurityRule, _ *arubatypes.SecurityRuleResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSR, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *SecurityRuleReconciler) kubeMarkCreatingDone(ctx context.Context, kubeSR *v1alpha1.SecurityRule, _ *arubatypes.SecurityRuleResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSR, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *SecurityRuleReconciler) kubeSetActiveAndSetID(ctx context.Context, kubeSR *v1alpha1.SecurityRule, cmpSR *arubatypes.SecurityRuleResponse) error {
	cmpID := ""
	if cmpSR != nil && cmpSR.Metadata.ID != nil {
		cmpID = *cmpSR.Metadata.ID
	}
	return setActiveAndSetID(r.Client, ctx, kubeSR, cmpID, nil, func(sr *v1alpha1.SecurityRule) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && sr.Status.ProjectID != "" {
			sr.Status.ProjectID = prjID
		}
		if vID, ok := ctx.Value(vpcIDKey).(string); ok && sr.Status.VpcID != "" {
			sr.Status.VpcID = vID
		}
		if sgID, ok := ctx.Value(securityGroupIDKey).(string); ok && sr.Status.SecurityGroupID != "" {
			sr.Status.SecurityGroupID = sgID
		}
	})
}

func (r *SecurityRuleReconciler) kubeSetFailedOnTimeout(ctx context.Context, kubeSR *v1alpha1.SecurityRule, _ *arubatypes.SecurityRuleResponse) error {
	return setFailedOnTimeout(r.Client, ctx, kubeSR, func(sr *v1alpha1.SecurityRule) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && sr.Status.ProjectID == "" {
			sr.Status.ProjectID = prjID
		}
		if vID, ok := ctx.Value(vpcIDKey).(string); ok && sr.Status.VpcID == "" {
			sr.Status.VpcID = vID
		}
		if sgID, ok := ctx.Value(securityGroupIDKey).(string); ok && sr.Status.SecurityGroupID == "" {
			sr.Status.SecurityGroupID = sgID
		}
	})
}

func (r *SecurityRuleReconciler) kubeSetFailed(ctx context.Context, kubeSR *v1alpha1.SecurityRule, _ *arubatypes.SecurityRuleResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSR, v1alpha1.ResourcePhaseFailed, v1alpha1.ConditionReasonSynchronized, nil)
}

// CMP action methods

func (r *SecurityRuleReconciler) cmpDelete(ctx context.Context, _ *v1alpha1.SecurityRule, cmpSR *arubatypes.SecurityRuleResponse) error {
	prjID := ctx.Value(projectIDKey).(string)
	vID := ctx.Value(vpcIDKey).(string)
	sgID := ctx.Value(securityGroupIDKey).(string)
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)

	cmpResp, err := arubaClient.FromNetwork().SecurityGroupRules().Delete(ctx, prjID, vID, sgID, *cmpSR.Metadata.ID, nil)
	if err != nil {
		return cmpTransportError("delete", *cmpSR.Metadata.Name, err)
	}
	return cmpCheckResponse("delete", *cmpSR.Metadata.Name, cmpResp,
		http.StatusOK, http.StatusAccepted, http.StatusNoContent, http.StatusNotFound)
}

func (r *SecurityRuleReconciler) cmpUpdate(ctx context.Context, kubeSR *v1alpha1.SecurityRule, cmpSR *arubatypes.SecurityRuleResponse) error {
	prjID := ctx.Value(projectIDKey).(string)
	vID := ctx.Value(vpcIDKey).(string)
	sgID := ctx.Value(securityGroupIDKey).(string)
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)

	// Guard: should have been caught by HasDeniedChanges, but double-check.
	if err := checkSecurityRuleDeniedChanges(kubeSR, cmpSR); err != nil {
		return err
	}

	request := buildSecurityRuleUpdateRequest(kubeSR, cmpSR)

	cmpResp, err := arubaClient.FromNetwork().SecurityGroupRules().Update(ctx, prjID, vID, sgID, *cmpSR.Metadata.ID, *request, nil)
	if err != nil {
		return cmpTransportError("update", kubeSR.Name, err)
	}
	return cmpCheckResponse("update", kubeSR.Name, cmpResp,
		http.StatusOK, http.StatusAccepted, http.StatusNoContent)
}

func (r *SecurityRuleReconciler) cmpCreate(ctx context.Context, kubeSR *v1alpha1.SecurityRule, _ *arubatypes.SecurityRuleResponse) error {
	prjID := ctx.Value(projectIDKey).(string)
	vID := ctx.Value(vpcIDKey).(string)
	sgID := ctx.Value(securityGroupIDKey).(string)
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)

	cmpResp, err := arubaClient.FromNetwork().SecurityGroupRules().Create(ctx, prjID, vID, sgID, *cmpSecurityRuleRequestFromKube(kubeSR), nil)
	if err != nil {
		return cmpTransportError("create", kubeSR.Name, err)
	}
	return cmpCheckResponse("create", kubeSR.Name, cmpResp,
		http.StatusOK, http.StatusCreated, http.StatusAccepted)
}

// Helper functions

func checkSecurityRuleDeniedChanges(kubeSR *v1alpha1.SecurityRule, cmpSR *arubatypes.SecurityRuleResponse) error {
	if cmpSR == nil {
		return nil
	}

	if cmpSR.Metadata.LocationResponse != nil &&
		cmpSR.Metadata.LocationResponse.Value != "" &&
		kubeSR.Spec.Location.Value != cmpSR.Metadata.LocationResponse.Value {
		return fmt.Errorf("%w: %w", ErrNotAllowedChanges, errors.New("change the 'location' is not allowed"))
	}

	return nil
}

func kubeSecurityRuleNeedsUpdate(kubeSR *v1alpha1.SecurityRule, cmpSR *arubatypes.SecurityRuleResponse) bool {
	if cmpSR == nil {
		return false
	}
	if !tagsAreEqual(kubeSR.Spec.Tags, cmpSR.Metadata.Tags) {
		return true
	}
	if kubeSR.Spec.Protocol != cmpSR.Properties.Protocol {
		return true
	}
	if kubeSR.Spec.Port != cmpSR.Properties.Port {
		return true
	}
	if arubatypes.RuleDirection(kubeSR.Spec.Direction) != cmpSR.Properties.Direction {
		return true
	}
	if cmpSR.Properties.Target == nil {
		return true
	}
	if arubatypes.EndpointTypeDto(kubeSR.Spec.Target.Kind) != cmpSR.Properties.Target.Kind {
		return true
	}
	if kubeSR.Spec.Target.Value != cmpSR.Properties.Target.Value {
		return true
	}
	return false
}

func cmpSecurityRuleRequestFromKube(kubeSR *v1alpha1.SecurityRule) *arubatypes.SecurityRuleRequest {
	tags := make([]string, len(kubeSR.Spec.Tags))
	copy(tags, kubeSR.Spec.Tags)
	return &arubatypes.SecurityRuleRequest{
		Metadata: arubatypes.RegionalResourceMetadataRequest{
			ResourceMetadataRequest: arubatypes.ResourceMetadataRequest{
				Name: kubeSR.Name,
				Tags: tags,
			},
			Location: arubatypes.LocationRequest{
				Value: kubeSR.Spec.Location.Value,
			},
		},
		Properties: arubatypes.SecurityRulePropertiesRequest{
			Protocol:  kubeSR.Spec.Protocol,
			Port:      kubeSR.Spec.Port,
			Direction: arubatypes.RuleDirection(kubeSR.Spec.Direction),
			Target: &arubatypes.RuleTarget{
				Kind:  arubatypes.EndpointTypeDto(kubeSR.Spec.Target.Kind),
				Value: kubeSR.Spec.Target.Value,
			},
		},
	}
}

func cmpSecurityRuleRequestFromCMP(cmpSR *arubatypes.SecurityRuleResponse) *arubatypes.SecurityRuleRequest {
	if cmpSR == nil {
		return nil
	}
	name := ""
	if cmpSR.Metadata.Name != nil {
		name = *cmpSR.Metadata.Name
	}
	tags := make([]string, len(cmpSR.Metadata.Tags))
	copy(tags, cmpSR.Metadata.Tags)
	locationValue := ""
	if cmpSR.Metadata.LocationResponse != nil {
		locationValue = cmpSR.Metadata.LocationResponse.Value
	}
	req := &arubatypes.SecurityRuleRequest{
		Metadata: arubatypes.RegionalResourceMetadataRequest{
			ResourceMetadataRequest: arubatypes.ResourceMetadataRequest{
				Name: name,
				Tags: tags,
			},
			Location: arubatypes.LocationRequest{
				Value: locationValue,
			},
		},
		Properties: arubatypes.SecurityRulePropertiesRequest{
			Protocol:  cmpSR.Properties.Protocol,
			Port:      cmpSR.Properties.Port,
			Direction: cmpSR.Properties.Direction,
		},
	}
	if cmpSR.Properties.Target != nil {
		req.Properties.Target = &arubatypes.RuleTarget{
			Kind:  cmpSR.Properties.Target.Kind,
			Value: cmpSR.Properties.Target.Value,
		}
	}
	return req
}

func buildSecurityRuleUpdateRequest(kubeSR *v1alpha1.SecurityRule, cmpSR *arubatypes.SecurityRuleResponse) *arubatypes.SecurityRuleRequest {
	request := cmpSecurityRuleRequestFromCMP(cmpSR)
	if request == nil {
		return nil
	}
	tags := make([]string, len(kubeSR.Spec.Tags))
	copy(tags, kubeSR.Spec.Tags)
	request.Metadata.Tags = tags
	request.Properties.Protocol = kubeSR.Spec.Protocol
	request.Properties.Port = kubeSR.Spec.Port
	request.Properties.Direction = arubatypes.RuleDirection(kubeSR.Spec.Direction)
	request.Properties.Target = &arubatypes.RuleTarget{
		Kind:  arubatypes.EndpointTypeDto(kubeSR.Spec.Target.Kind),
		Value: kubeSR.Spec.Target.Value,
	}
	return request
}
