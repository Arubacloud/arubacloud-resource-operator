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

// SubnetReconciler reconciles a Subnet object
type SubnetReconciler struct {
	*reconciler.Reconciler
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
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

func (r *SubnetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := &v1alpha1.Subnet{}
	return r.Reconciler.Reconcile(ctx, req, obj, &obj.Status.ResourceStatus, r, &obj.Spec.Tenant)
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

func (r *SubnetReconciler) Init(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	return r.InitializeResource(ctx, obj, status, subnetFinalizerName)
}

func (r *SubnetReconciler) Creating(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	subnet := obj.(*v1alpha1.Subnet)
	return r.HandleCreating(ctx, obj, status, func(ctx context.Context) (string, string, error) {
		projectID, err := r.GetProjectID(ctx, subnet.Spec.ProjectReference.Name, subnet.Spec.ProjectReference.Namespace)
		if err != nil {
			return "", "", err
		}

		vpcID, err := r.GetVpcID(ctx, subnet.Spec.VpcReference.Name, subnet.Spec.VpcReference.Namespace)
		if err != nil {
			return "", "", err
		}

		subnetReq := arubaClient.SubnetRequest{
			Metadata: arubaClient.SubnetMetadata{
				Name: subnet.Name,
				Tags: subnet.Spec.Tags,
			},
			Properties: arubaClient.SubnetProperties{
				Type:    subnet.Spec.Type,
				Default: subnet.Spec.Default,
				Network: arubaClient.SubnetNetwork{
					Address: subnet.Spec.Network.Address,
				},
				DHCP: arubaClient.SubnetDHCP{
					Enabled: subnet.Spec.DHCP.Enabled,
				},
			},
		}

		subnetResp, err := r.CreateSubnet(ctx, projectID, vpcID, subnetReq)
		if err != nil {
			return "", "", err
		}

		subnet.Status.ProjectID = projectID
		subnet.Status.VpcID = vpcID

		state := ""
		if subnetResp.Status != nil {
			state = subnetResp.Status.State
		}

		return subnetResp.Metadata.ID, state, nil
	})
}

func (r *SubnetReconciler) Provisioning(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	subnet := obj.(*v1alpha1.Subnet)
	return r.HandleProvisioning(ctx, obj, status, func(ctx context.Context) (string, error) {
		subnetResp, err := r.GetSubnet(ctx, subnet.Status.ProjectID, subnet.Status.VpcID, status.ResourceID)
		if err != nil {
			return "", err
		}

		if subnetResp.Status != nil {
			return subnetResp.Status.State, nil
		}
		return "", nil
	})
}

func (r *SubnetReconciler) Updating(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	subnet := obj.(*v1alpha1.Subnet)
	return r.HandleUpdating(ctx, obj, status, func(ctx context.Context) error {
		subnetReq := arubaClient.SubnetRequest{
			Metadata: arubaClient.SubnetMetadata{
				Name: subnet.Name,
				Tags: subnet.Spec.Tags,
			},
			Properties: arubaClient.SubnetProperties{
				Type:    subnet.Spec.Type,
				Default: subnet.Spec.Default,
				Network: arubaClient.SubnetNetwork{
					Address: subnet.Spec.Network.Address,
				},
				DHCP: arubaClient.SubnetDHCP{
					Enabled: subnet.Spec.DHCP.Enabled,
				},
			},
		}

		_, err := r.UpdateSubnet(ctx, subnet.Status.ProjectID, subnet.Status.VpcID, status.ResourceID, subnetReq)
		return err
	})
}

func (r *SubnetReconciler) Created(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	return r.CheckForUpdates(ctx, obj, status)
}

func (r *SubnetReconciler) Deleting(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	subnet := obj.(*v1alpha1.Subnet)
	return r.HandleDeletion(ctx, obj, status, subnetFinalizerName, func(ctx context.Context) error {
		return r.DeleteSubnet(ctx, subnet.Status.ProjectID, subnet.Status.VpcID, status.ResourceID)
	})
}
