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

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/client"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
)

// SecurityRuleReconciler reconciles a SecurityRule object
type SecurityRuleReconciler struct {
	*reconciler.Reconciler
	Object *v1alpha1.SecurityRule
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
// +kubebuilder:rbac:groups=arubacloud.com,resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=arubacloud.com,resources=configmaps,verbs=get;list;watch

func (r *SecurityRuleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.Object = &v1alpha1.SecurityRule{}
	r.Reconciler.Object = r.Object
	r.ResourceStatus = &r.Object.Status.ResourceStatus
	r.ResourceReconciler = r
	return r.Reconciler.Reconcile(ctx, req, &r.Object.Spec.Tenant)
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

func (r *SecurityRuleReconciler) Init(ctx context.Context) (ctrl.Result, error) {
	return r.InitializeResource(ctx, securityRuleFinalizerName)
}

func (r *SecurityRuleReconciler) Creating(ctx context.Context) (ctrl.Result, error) {
	return r.HandleCreating(ctx, func(ctx context.Context) (string, string, error) {
		projectID, err := r.GetProjectID(
			ctx,
			r.Object.Spec.ProjectReference.Name,
			r.Object.Spec.ProjectReference.Namespace,
		)
		if err != nil {
			return "", "", err
		}

		vpcID, err := r.GetVpcID(ctx, r.Object.Spec.VpcReference.Name, r.Object.Spec.VpcReference.Namespace)
		if err != nil {
			return "", "", err
		}

		securityGroupID, err := r.GetSecurityGroupID(
			ctx,
			r.Object.Spec.SecurityGroupReference.Name,
			r.Object.Spec.SecurityGroupReference.Namespace,
		)
		if err != nil {
			return "", "", err
		}

		securityRuleReq := client.SecurityRuleRequest{
			Metadata: client.SecurityRuleMetadata{
				Name: r.Object.Name,
				Tags: r.Object.Spec.Tags,
				Location: client.SecurityRuleLocation{
					Value: r.Object.Spec.Location.Value,
				},
			},
			Properties: client.SecurityRuleProperties{
				Protocol:  r.Object.Spec.Protocol,
				Port:      r.Object.Spec.Port,
				Direction: r.Object.Spec.Direction,
				Target: client.SecurityRuleTarget{
					Kind:  r.Object.Spec.Target.Kind,
					Value: r.Object.Spec.Target.Value,
				},
			},
		}

		securityRuleResp, err := r.CreateSecurityRule(ctx, projectID, vpcID, securityGroupID, securityRuleReq)
		if err != nil {
			return "", "", err
		}

		r.Object.Status.ProjectID = projectID
		r.Object.Status.VpcID = vpcID
		r.Object.Status.SecurityGroupID = securityGroupID

		state := ""
		if securityRuleResp.Status != nil {
			state = securityRuleResp.Status.State
		}

		return securityRuleResp.Metadata.ID, state, nil
	})
}

func (r *SecurityRuleReconciler) Provisioning(ctx context.Context) (ctrl.Result, error) {
	return r.HandleProvisioning(ctx, func(ctx context.Context) (string, error) {
		securityRuleResp, err := r.GetSecurityRule(ctx, r.Object.Status.ProjectID, r.Object.Status.VpcID, r.Object.Status.SecurityGroupID, r.Object.Status.ResourceID)
		if err != nil {
			return "", err
		}

		if securityRuleResp.Status != nil {
			return securityRuleResp.Status.State, nil
		}
		return "", nil
	})
}

func (r *SecurityRuleReconciler) Updating(ctx context.Context) (ctrl.Result, error) {
	return r.HandleUpdating(ctx, func(ctx context.Context) error {
		securityRuleReq := client.SecurityRuleRequest{
			Metadata: client.SecurityRuleMetadata{
				Name: r.Object.Name,
				Tags: r.Object.Spec.Tags,
				Location: client.SecurityRuleLocation{
					Value: r.Object.Spec.Location.Value,
				},
			},
			Properties: client.SecurityRuleProperties{
				Protocol:  r.Object.Spec.Protocol,
				Port:      r.Object.Spec.Port,
				Direction: r.Object.Spec.Direction,
				Target: client.SecurityRuleTarget{
					Kind:  r.Object.Spec.Target.Kind,
					Value: r.Object.Spec.Target.Value,
				},
			},
		}

		_, err := r.UpdateSecurityRule(ctx, r.Object.Status.ProjectID, r.Object.Status.VpcID, r.Object.Status.SecurityGroupID, r.Object.Status.ResourceID, securityRuleReq)
		return err
	})
}

func (r *SecurityRuleReconciler) Created(ctx context.Context) (ctrl.Result, error) {
	return r.CheckForUpdates(ctx)
}

func (r *SecurityRuleReconciler) Deleting(ctx context.Context) (ctrl.Result, error) {
	return r.HandleDeletion(ctx, securityRuleFinalizerName, func(ctx context.Context) error {
		return r.DeleteSecurityRule(ctx, r.Object.Status.ProjectID, r.Object.Status.VpcID, r.Object.Status.SecurityGroupID, r.Object.Status.ResourceID)
	})
}
