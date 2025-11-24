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

// VpcReconciler reconciles a Vpc object
type VpcReconciler struct {
	*reconciler.Reconciler
	Object *v1alpha1.Vpc
}

// NewVpcReconciler creates a new VpcReconciler
func NewVpcReconciler(reconciler *reconciler.Reconciler) *VpcReconciler {
	return &VpcReconciler{
		Reconciler: reconciler,
	}
}

// +kubebuilder:rbac:groups=arubacloud.com,resources=vpcs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=arubacloud.com,resources=vpcs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=arubacloud.com,resources=vpcs/finalizers,verbs=update
// +kubebuilder:rbac:groups=arubacloud.com,resources=projects,verbs=get;list;watch
// +kubebuilder:rbac:groups=arubacloud.com,resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=arubacloud.com,resources=configmaps,verbs=get;list;watch

func (r *VpcReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.Object = &v1alpha1.Vpc{}
	r.Reconciler.Object = r.Object
	r.ResourceStatus = &r.Object.Status.ResourceStatus
	r.ResourceReconciler = r
	return r.Reconciler.Reconcile(ctx, req, &r.Object.Spec.Tenant)
}

// SetupWithManager sets up the controller with the Manager.
func (r *VpcReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Vpc{}).
		Named("vpc").
		Complete(r)
}

const (
	vpcFinalizerName = "vpc.arubacloud.com/finalizer"
)

func (r *VpcReconciler) Init(ctx context.Context) (ctrl.Result, error) {
	return r.InitializeResource(ctx, vpcFinalizerName)
}

func (r *VpcReconciler) Creating(ctx context.Context) (ctrl.Result, error) {
	return r.HandleCreating(ctx, func(ctx context.Context) (string, string, error) {
		projectID, err := r.GetProjectID(
			ctx,
			r.Object.Spec.ProjectReference.Name,
			r.Object.Spec.ProjectReference.Namespace,
		)
		if err != nil {
			return "", "", err
		}

		vpcReq := client.VpcRequest{
			Metadata: client.VpcMetadata{
				Name: r.Object.Name,
				Tags: r.Object.Spec.Tags,
				Location: client.VpcLocation{
					Value: r.Object.Spec.Location.Value,
				},
			},
			Properties: client.VPCProperties{
				Default: false,
				Preset:  false,
			},
		}

		vpcResp, err := r.HelperClient.CreateVpc(ctx, projectID, vpcReq)
		if err != nil {
			return "", "", err
		}

		r.Object.Status.ProjectID = projectID

		state := ""
		if vpcResp.Status != nil {
			state = vpcResp.Status.State
		}

		return vpcResp.Metadata.ID, state, nil
	})
}

func (r *VpcReconciler) Provisioning(ctx context.Context) (ctrl.Result, error) {
	return r.HandleProvisioning(ctx, func(ctx context.Context) (string, error) {
		vpcResp, err := r.HelperClient.GetVpc(ctx, r.Object.Status.ProjectID, r.Object.Status.ResourceID)
		if err != nil {
			return "", err
		}

		if vpcResp.Status != nil {
			return vpcResp.Status.State, nil
		}
		return "", nil
	})
}

func (r *VpcReconciler) Updating(ctx context.Context) (ctrl.Result, error) {
	return r.HandleUpdating(ctx, func(ctx context.Context) error {
		vpcReq := client.VpcRequest{
			Metadata: client.VpcMetadata{
				Name: r.Object.Name,
				Tags: r.Object.Spec.Tags,
				Location: client.VpcLocation{
					Value: r.Object.Spec.Location.Value,
				},
			},
		}

		_, err := r.HelperClient.UpdateVpc(ctx, r.Object.Status.ProjectID, r.Object.Status.ResourceID, vpcReq)
		return err
	})
}

func (r *VpcReconciler) Created(ctx context.Context) (ctrl.Result, error) {
	return r.CheckForUpdates(ctx)
}

func (r *VpcReconciler) Deleting(ctx context.Context) (ctrl.Result, error) {
	return r.HandleDeletion(ctx, vpcFinalizerName, func(ctx context.Context) error {
		return r.HelperClient.DeleteVpc(ctx, r.Object.Status.ProjectID, r.Object.Status.ResourceID)
	})
}
