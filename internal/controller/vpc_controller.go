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

// VpcReconciler reconciles a Vpc object
type VpcReconciler struct {
	*reconciler.Reconciler
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
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

func (r *VpcReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := &v1alpha1.Vpc{}
	return r.Reconciler.Reconcile(ctx, req, obj, &obj.Status.ResourceStatus, r, &obj.Spec.Tenant)
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

func (r *VpcReconciler) Init(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	return r.InitializeResource(ctx, obj, status, vpcFinalizerName)
}

func (r *VpcReconciler) Creating(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	vpc := obj.(*v1alpha1.Vpc)
	return r.HandleCreating(ctx, obj, status, func(ctx context.Context) (string, string, error) {
		projectID, err := r.GetProjectID(
			ctx,
			vpc.Spec.ProjectReference.Name,
			vpc.Spec.ProjectReference.Namespace,
		)
		if err != nil {
			return "", "", err
		}

		vpcReq := arubaClient.VpcRequest{
			Metadata: arubaClient.VpcMetadata{
				Name: vpc.Name,
				Tags: vpc.Spec.Tags,
				Location: arubaClient.VpcLocation{
					Value: vpc.Spec.Location.Value,
				},
			},
			Properties: arubaClient.VPCProperties{
				Default: false,
				Preset:  false,
			},
		}

		vpcResp, err := r.CreateVpc(ctx, projectID, vpcReq)
		if err != nil {
			return "", "", err
		}

		vpc.Status.ProjectID = projectID

		state := ""
		if vpcResp.Status != nil {
			state = vpcResp.Status.State
		}

		return vpcResp.Metadata.ID, state, nil
	})
}

func (r *VpcReconciler) Provisioning(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	vpc := obj.(*v1alpha1.Vpc)
	return r.HandleProvisioning(ctx, obj, status, func(ctx context.Context) (string, error) {
		vpcResp, err := r.GetVpc(ctx, vpc.Status.ProjectID, status.ResourceID)
		if err != nil {
			return "", err
		}

		if vpcResp.Status != nil {
			return vpcResp.Status.State, nil
		}
		return "", nil
	})
}

func (r *VpcReconciler) Updating(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	vpc := obj.(*v1alpha1.Vpc)
	return r.HandleUpdating(ctx, obj, status, func(ctx context.Context) error {
		vpcReq := arubaClient.VpcRequest{
			Metadata: arubaClient.VpcMetadata{
				Name: vpc.Name,
				Tags: vpc.Spec.Tags,
				Location: arubaClient.VpcLocation{
					Value: vpc.Spec.Location.Value,
				},
			},
		}

		_, err := r.UpdateVpc(ctx, vpc.Status.ProjectID, status.ResourceID, vpcReq)
		return err
	})
}

func (r *VpcReconciler) Created(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	return r.CheckForUpdates(ctx, obj, status)
}

func (r *VpcReconciler) Deleting(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	vpc := obj.(*v1alpha1.Vpc)
	return r.HandleDeletion(ctx, obj, status, vpcFinalizerName, func(ctx context.Context) error {
		return r.DeleteVpc(ctx, vpc.Status.ProjectID, status.ResourceID)
	})
}
