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

// SecurityGroupReconciler reconciles a SecurityGroup object
type SecurityGroupReconciler struct {
	*reconciler.Reconciler
	Object *v1alpha1.SecurityGroup
}

// NewSecurityGroupReconciler creates a new SecurityGroupReconciler
func NewSecurityGroupReconciler(reconciler *reconciler.Reconciler) *SecurityGroupReconciler {
	return &SecurityGroupReconciler{
		Reconciler: reconciler,
	}
}

// +kubebuilder:rbac:groups=arubacloud.com,resources=securitygroups,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=arubacloud.com,resources=securitygroups/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=arubacloud.com,resources=securitygroups/finalizers,verbs=update
// +kubebuilder:rbac:groups=arubacloud.com,resources=projects,verbs=get;list;watch
// +kubebuilder:rbac:groups=arubacloud.com,resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=arubacloud.com,resources=configmaps,verbs=get;list;watch

func (r *SecurityGroupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.Object = &v1alpha1.SecurityGroup{}
	r.Reconciler.Object = r.Object
	r.ResourceStatus = &r.Object.Status.ResourceStatus
	r.ResourceReconciler = r
	return r.Reconciler.Reconcile(ctx, req, &r.Object.Spec.Tenant)
}

// SetupWithManager sets up the controller with the Manager.
func (r *SecurityGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.SecurityGroup{}).
		Named("securitygroup").
		Complete(r)
}

const (
	securityGroupFinalizerName = "securitygroup.arubacloud.com/finalizer"
)

func (r *SecurityGroupReconciler) Init(ctx context.Context) (ctrl.Result, error) {
	return r.InitializeResource(ctx, securityGroupFinalizerName)
}

func (r *SecurityGroupReconciler) Creating(ctx context.Context) (ctrl.Result, error) {
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

		securityGroupReq := client.SecurityGroupRequest{
			Metadata: client.SecurityGroupMetadata{
				Name: r.Object.Name,
				Tags: r.Object.Spec.Tags,
				Location: client.SecurityGroupLocation{
					Value: r.Object.Spec.Location.Value,
				},
			},
			Properties: client.SecurityGroupProperties{
				Default: r.Object.Spec.Default,
			},
		}

		securityGroupResp, err := r.CreateSecurityGroup(ctx, projectID, vpcID, securityGroupReq)
		if err != nil {
			return "", "", err
		}

		r.Object.Status.ProjectID = projectID
		r.Object.Status.VpcID = vpcID

		state := ""
		if securityGroupResp.Status != nil {
			state = securityGroupResp.Status.State
		}

		return securityGroupResp.Metadata.ID, state, nil
	})
}

func (r *SecurityGroupReconciler) Provisioning(ctx context.Context) (ctrl.Result, error) {
	return r.HandleProvisioning(ctx, func(ctx context.Context) (string, error) {
		securityGroupResp, err := r.GetSecurityGroup(ctx, r.Object.Status.ProjectID, r.Object.Status.VpcID, r.Object.Status.ResourceID)
		if err != nil {
			return "", err
		}

		if securityGroupResp.Status != nil {
			return securityGroupResp.Status.State, nil
		}
		return "", nil
	})
}

func (r *SecurityGroupReconciler) Updating(ctx context.Context) (ctrl.Result, error) {
	return r.HandleUpdating(ctx, func(ctx context.Context) error {
		securityGroupReq := client.SecurityGroupRequest{
			Metadata: client.SecurityGroupMetadata{
				Name: r.Object.Name,
				Tags: r.Object.Spec.Tags,
				Location: client.SecurityGroupLocation{
					Value: r.Object.Spec.Location.Value,
				},
			},
			Properties: client.SecurityGroupProperties{
				Default: r.Object.Spec.Default,
			},
		}

		_, err := r.UpdateSecurityGroup(ctx, r.Object.Status.ProjectID, r.Object.Status.VpcID, r.Object.Status.ResourceID, securityGroupReq)
		return err
	})
}

func (r *SecurityGroupReconciler) Created(ctx context.Context) (ctrl.Result, error) {
	return r.CheckForUpdates(ctx)
}

func (r *SecurityGroupReconciler) Deleting(ctx context.Context) (ctrl.Result, error) {
	return r.HandleDeletion(ctx, securityGroupFinalizerName, func(ctx context.Context) error {
		return r.DeleteSecurityGroup(ctx, r.Object.Status.ProjectID, r.Object.Status.VpcID, r.Object.Status.ResourceID)
	})
}
