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

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
	arubaClient "github.com/Arubacloud/arubacloud-resource-operator/internal/client"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
)

// SecurityRuleReconciler reconciles a SecurityRule object
type SecurityRuleReconciler struct {
	*reconciler.Reconciler
}

// NewSecurityRuleReconciler creates a new SecurityRuleReconciler
func NewSecurityRuleReconciler(reconciler *reconciler.Reconciler) *SecurityRuleReconciler {
	return &SecurityRuleReconciler{
		Reconciler: reconciler,
	}
}

// +kubebuilder:rbac:groups=arubacloud.com,resources=securityrules,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=arubacloud.com,resources=securityrules/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=arubacloud.com,resources=securityrules/finalizers,verbs=update
// +kubebuilder:rbac:groups=arubacloud.com,resources=projects,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

func (r *SecurityRuleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := &v1alpha1.SecurityRule{}
	return r.Reconciler.Reconcile(ctx, req, obj, &obj.Status.ResourceStatus, r, &obj.Spec.Tenant)
}

// SetupWithManager sets up the controller with the Manager.
func (r *SecurityRuleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.SecurityRule{}).
		Named("securityrule").
		Complete(r)
}

const (
	securityRuleFinalizerName = "securityrule.arubacloud.com/finalizer"
)

func (r *SecurityRuleReconciler) Init(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	return r.InitializeResource(ctx, obj, status, securityRuleFinalizerName)
}

func (r *SecurityRuleReconciler) Creating(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	securityRule := obj.(*v1alpha1.SecurityRule)
	return r.HandleCreating(ctx, obj, status, func(ctx context.Context) (string, string, error) {
		projectID, err := r.GetProjectID(
			ctx,
			securityRule.Spec.ProjectReference.Name,
			securityRule.Spec.ProjectReference.Namespace,
		)
		if err != nil {
			return "", "", err
		}

		vpcID, err := r.GetVpcID(ctx, securityRule.Spec.VpcReference.Name, securityRule.Spec.VpcReference.Namespace)
		if err != nil {
			return "", "", err
		}

		securityGroupID, err := r.GetSecurityGroupID(
			ctx,
			securityRule.Spec.SecurityGroupReference.Name,
			securityRule.Spec.SecurityGroupReference.Namespace,
		)
		if err != nil {
			return "", "", err
		}

		securityRuleReq := arubaClient.SecurityRuleRequest{
			Metadata: arubaClient.SecurityRuleMetadata{
				Name: securityRule.Name,
				Tags: securityRule.Spec.Tags,
				Location: arubaClient.SecurityRuleLocation{
					Value: securityRule.Spec.Location.Value,
				},
			},
			Properties: arubaClient.SecurityRuleProperties{
				Protocol:  securityRule.Spec.Protocol,
				Port:      securityRule.Spec.Port,
				Direction: securityRule.Spec.Direction,
				Target: arubaClient.SecurityRuleTarget{
					Kind:  securityRule.Spec.Target.Kind,
					Value: securityRule.Spec.Target.Value,
				},
			},
		}

		securityRuleResp, err := r.CreateSecurityRule(ctx, projectID, vpcID, securityGroupID, securityRuleReq)
		if err != nil {
			return "", "", err
		}

		securityRule.Status.ProjectID = projectID
		securityRule.Status.VpcID = vpcID
		securityRule.Status.SecurityGroupID = securityGroupID

		state := ""
		if securityRuleResp.Status != nil {
			state = securityRuleResp.Status.State
		}

		return securityRuleResp.Metadata.ID, state, nil
	})
}

func (r *SecurityRuleReconciler) Provisioning(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	securityRule := obj.(*v1alpha1.SecurityRule)
	return r.HandleProvisioning(ctx, obj, status, func(ctx context.Context) (string, error) {
		securityRuleResp, err := r.GetSecurityRule(ctx, securityRule.Status.ProjectID, securityRule.Status.VpcID, securityRule.Status.SecurityGroupID, status.ResourceID)
		if err != nil {
			return "", err
		}

		if securityRuleResp.Status != nil {
			return securityRuleResp.Status.State, nil
		}
		return "", nil
	})
}

func (r *SecurityRuleReconciler) Updating(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	securityRule := obj.(*v1alpha1.SecurityRule)
	return r.HandleUpdating(ctx, obj, status, func(ctx context.Context) error {
		securityRuleReq := arubaClient.SecurityRuleRequest{
			Metadata: arubaClient.SecurityRuleMetadata{
				Name: securityRule.Name,
				Tags: securityRule.Spec.Tags,
				Location: arubaClient.SecurityRuleLocation{
					Value: securityRule.Spec.Location.Value,
				},
			},
			Properties: arubaClient.SecurityRuleProperties{
				Protocol:  securityRule.Spec.Protocol,
				Port:      securityRule.Spec.Port,
				Direction: securityRule.Spec.Direction,
				Target: arubaClient.SecurityRuleTarget{
					Kind:  securityRule.Spec.Target.Kind,
					Value: securityRule.Spec.Target.Value,
				},
			},
		}

		_, err := r.UpdateSecurityRule(ctx, securityRule.Status.ProjectID, securityRule.Status.VpcID, securityRule.Status.SecurityGroupID, status.ResourceID, securityRuleReq)
		return err
	})
}

func (r *SecurityRuleReconciler) Created(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	return r.CheckForUpdates(ctx, obj, status)
}

func (r *SecurityRuleReconciler) Deleting(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	securityRule := obj.(*v1alpha1.SecurityRule)
	return r.HandleDeletion(ctx, obj, status, securityRuleFinalizerName, func(ctx context.Context) error {
		return r.DeleteSecurityRule(ctx, securityRule.Status.ProjectID, securityRule.Status.VpcID, securityRule.Status.SecurityGroupID, status.ResourceID)
	})
}
