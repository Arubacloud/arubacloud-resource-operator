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

// SubnetReconciler reconciles a Subnet object
type SubnetReconciler struct {
	*reconciler.Reconciler
	Object *v1alpha1.Subnet
}

// NewSubnetReconciler creates a new SubnetReconciler
func NewSubnetReconciler(reconciler *reconciler.Reconciler) *SubnetReconciler {
	return &SubnetReconciler{
		Reconciler: reconciler,
	}
}

// +kubebuilder:rbac:groups=arubacloud.com,resources=subnets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=arubacloud.com,resources=subnets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=arubacloud.com,resources=subnets/finalizers,verbs=update
// +kubebuilder:rbac:groups=arubacloud.com,resources=projects,verbs=get;list;watch
// +kubebuilder:rbac:groups=arubacloud.com,resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=arubacloud.com,resources=configmaps,verbs=get;list;watch

func (r *SubnetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.Object = &v1alpha1.Subnet{}
	r.Reconciler.Object = r.Object
	r.ResourceStatus = &r.Object.Status.ResourceStatus
	r.ResourceReconciler = r
	return r.Reconciler.Reconcile(ctx, req, &r.Object.Spec.Tenant)
}

// SetupWithManager sets up the controller with the Manager.
func (r *SubnetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Subnet{}).
		Named("subnet").
		Complete(r)
}

const (
	subnetFinalizerName = "subnet.arubacloud.com/finalizer"
)

func (r *SubnetReconciler) Init(ctx context.Context) (ctrl.Result, error) {
	return r.InitializeResource(ctx, subnetFinalizerName)
}

func (r *SubnetReconciler) Creating(ctx context.Context) (ctrl.Result, error) {
	return r.HandleCreating(ctx, func(ctx context.Context) (string, string, error) {
		projectID, err := r.GetProjectID(ctx, r.Object.Spec.ProjectReference.Name, r.Object.Spec.ProjectReference.Namespace)
		if err != nil {
			return "", "", err
		}

		vpcID, err := r.GetVpcID(ctx, r.Object.Spec.VpcReference.Name, r.Object.Spec.VpcReference.Namespace)
		if err != nil {
			return "", "", err
		}

		subnetReq := client.SubnetRequest{
			Metadata: client.SubnetMetadata{
				Name: r.Object.Name,
				Tags: r.Object.Spec.Tags,
			},
			Properties: client.SubnetProperties{
				Type:    r.Object.Spec.Type,
				Default: r.Object.Spec.Default,
				Network: client.SubnetNetwork{
					Address: r.Object.Spec.Network.Address,
				},
				DHCP: client.SubnetDHCP{
					Enabled: r.Object.Spec.DHCP.Enabled,
				},
			},
		}

		subnetResp, err := r.HelperClient.CreateSubnet(ctx, projectID, vpcID, subnetReq)
		if err != nil {
			return "", "", err
		}

		r.Object.Status.ProjectID = projectID
		r.Object.Status.VpcID = vpcID

		state := ""
		if subnetResp.Status != nil {
			state = subnetResp.Status.State
		}

		return subnetResp.Metadata.ID, state, nil
	})
}

func (r *SubnetReconciler) Provisioning(ctx context.Context) (ctrl.Result, error) {
	return r.HandleProvisioning(ctx, func(ctx context.Context) (string, error) {
		subnetResp, err := r.HelperClient.GetSubnet(ctx, r.Object.Status.ProjectID, r.Object.Status.VpcID, r.Object.Status.ResourceID)
		if err != nil {
			return "", err
		}

		if subnetResp.Status != nil {
			return subnetResp.Status.State, nil
		}
		return "", nil
	})
}

func (r *SubnetReconciler) Updating(ctx context.Context) (ctrl.Result, error) {
	return r.HandleUpdating(ctx, func(ctx context.Context) error {
		subnetReq := client.SubnetRequest{
			Metadata: client.SubnetMetadata{
				Name: r.Object.Name,
				Tags: r.Object.Spec.Tags,
			},
			Properties: client.SubnetProperties{
				Type:    r.Object.Spec.Type,
				Default: r.Object.Spec.Default,
				Network: client.SubnetNetwork{
					Address: r.Object.Spec.Network.Address,
				},
				DHCP: client.SubnetDHCP{
					Enabled: r.Object.Spec.DHCP.Enabled,
				},
			},
		}

		_, err := r.HelperClient.UpdateSubnet(ctx, r.Object.Status.ProjectID, r.Object.Status.VpcID, r.Object.Status.ResourceID, subnetReq)
		return err
	})
}

func (r *SubnetReconciler) Created(ctx context.Context) (ctrl.Result, error) {
	return r.CheckForUpdates(ctx)
}

func (r *SubnetReconciler) Deleting(ctx context.Context) (ctrl.Result, error) {
	return r.HandleDeletion(ctx, subnetFinalizerName, func(ctx context.Context) error {
		return r.HelperClient.DeleteSubnet(ctx, r.Object.Status.ProjectID, r.Object.Status.VpcID, r.Object.Status.ResourceID)
	})
}
