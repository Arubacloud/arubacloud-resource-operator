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

// SecurityGroupReconciler reconciles a SecurityGroup object
type SecurityGroupReconciler struct {
	*reconciler.Reconciler
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
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

func (r *SecurityGroupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := &v1alpha1.SecurityGroup{}
	return r.Reconciler.Reconcile(ctx, req, obj, &obj.Status.ResourceStatus, r, &obj.Spec.Tenant)
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

func (r *SecurityGroupReconciler) Init(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	return r.InitializeResource(ctx, obj, status, securityGroupFinalizerName)
}

func (r *SecurityGroupReconciler) Creating(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	securityGroup := obj.(*v1alpha1.SecurityGroup)
	return r.HandleCreating(ctx, obj, status, func(ctx context.Context) (string, string, error) {
		projectID, err := r.GetProjectID(
			ctx,
			securityGroup.Spec.ProjectReference.Name,
			securityGroup.Spec.ProjectReference.Namespace,
		)
		if err != nil {
			return "", "", err
		}

		vpcID, err := r.GetVpcID(ctx, securityGroup.Spec.VpcReference.Name, securityGroup.Spec.VpcReference.Namespace)
		if err != nil {
			return "", "", err
		}

		securityGroupReq := arubaClient.SecurityGroupRequest{
			Metadata: arubaClient.SecurityGroupMetadata{
				Name: securityGroup.Name,
				Tags: securityGroup.Spec.Tags,
				Location: arubaClient.SecurityGroupLocation{
					Value: securityGroup.Spec.Location.Value,
				},
			},
			Properties: arubaClient.SecurityGroupProperties{
				Default: securityGroup.Spec.Default,
			},
		}

		securityGroupResp, err := r.CreateSecurityGroup(ctx, projectID, vpcID, securityGroupReq)
		if err != nil {
			return "", "", err
		}

		securityGroup.Status.ProjectID = projectID
		securityGroup.Status.VpcID = vpcID

		state := ""
		if securityGroupResp.Status != nil {
			state = securityGroupResp.Status.State
		}

		return securityGroupResp.Metadata.ID, state, nil
	})
}

func (r *SecurityGroupReconciler) Provisioning(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	securityGroup := obj.(*v1alpha1.SecurityGroup)
	return r.HandleProvisioning(ctx, obj, status, func(ctx context.Context) (string, error) {
		securityGroupResp, err := r.GetSecurityGroup(ctx, securityGroup.Status.ProjectID, securityGroup.Status.VpcID, status.ResourceID)
		if err != nil {
			return "", err
		}

		if securityGroupResp.Status != nil {
			return securityGroupResp.Status.State, nil
		}
		return "", nil
	})
}

func (r *SecurityGroupReconciler) Updating(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	securityGroup := obj.(*v1alpha1.SecurityGroup)
	return r.HandleUpdating(ctx, obj, status, func(ctx context.Context) error {
		securityGroupReq := arubaClient.SecurityGroupRequest{
			Metadata: arubaClient.SecurityGroupMetadata{
				Name: securityGroup.Name,
				Tags: securityGroup.Spec.Tags,
				Location: arubaClient.SecurityGroupLocation{
					Value: securityGroup.Spec.Location.Value,
				},
			},
			Properties: arubaClient.SecurityGroupProperties{
				Default: securityGroup.Spec.Default,
			},
		}

		_, err := r.UpdateSecurityGroup(ctx, securityGroup.Status.ProjectID, securityGroup.Status.VpcID, status.ResourceID, securityGroupReq)
		return err
	})
}

func (r *SecurityGroupReconciler) Created(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	return r.CheckForUpdates(ctx, obj, status)
}

func (r *SecurityGroupReconciler) Deleting(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	securityGroup := obj.(*v1alpha1.SecurityGroup)
	return r.HandleDeletion(ctx, obj, status, securityGroupFinalizerName, func(ctx context.Context) error {
		return r.DeleteSecurityGroup(ctx, securityGroup.Status.ProjectID, securityGroup.Status.VpcID, status.ResourceID)
	})
}
