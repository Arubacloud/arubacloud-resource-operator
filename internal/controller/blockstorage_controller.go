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

// BlockStorageReconciler reconciles a BlockStorage object
type BlockStorageReconciler struct {
	*reconciler.Reconciler
	Object *v1alpha1.BlockStorage
}

// NewBlockStorageReconciler creates a new BlockStorageReconciler
func NewBlockStorageReconciler(baseReconciler *reconciler.Reconciler) *BlockStorageReconciler {
	return &BlockStorageReconciler{
		Reconciler: baseReconciler,
	}
}

// +kubebuilder:rbac:groups=arubacloud.com,resources=blockstorages,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=arubacloud.com,resources=blockstorages/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=arubacloud.com,resources=blockstorages/finalizers,verbs=update
// +kubebuilder:rbac:groups=arubacloud.com,resources=projects,verbs=get;list;watch
// +kubebuilder:rbac:groups=arubacloud.com,resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=arubacloud.com,resources=configmaps,verbs=get;list;watch

func (r *BlockStorageReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.Object = &v1alpha1.BlockStorage{}
	r.Reconciler.Object = r.Object
	r.ResourceStatus = &r.Object.Status.ResourceStatus
	r.ResourceReconciler = r
	return r.Reconciler.Reconcile(ctx, req, &r.Object.Spec.Tenant)
}

// SetupWithManager sets up the controller with the Manager.
func (r *BlockStorageReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.BlockStorage{}).
		Named("blockstorage").
		Complete(r)
}

const (
	blockStorageFinalizerName = "blockstorage.arubacloud.com/finalizer"
)

func (r *BlockStorageReconciler) Init(ctx context.Context) (ctrl.Result, error) {
	return r.InitializeResource(ctx, blockStorageFinalizerName)
}

func (r *BlockStorageReconciler) Creating(ctx context.Context) (ctrl.Result, error) {
	return r.HandleCreating(ctx, func(ctx context.Context) (string, string, error) {
		projectID, err := r.GetProjectID(ctx, r.Object.Spec.ProjectReference.Name, r.Object.Spec.ProjectReference.Namespace)
		if err != nil {
			return "", "", err
		}

		blockStorageReq := client.BlockStorageRequest{
			Metadata: client.BlockStorageMetadata{
				Name: r.Object.Name,
				Tags: r.Object.Spec.Tags,
				Location: client.BlockStorageLocation{
					Value: r.Object.Spec.Location.Value,
				},
			},
			Properties: client.BlockStorageProperties{
				SizeGb:        r.Object.Spec.SizeGb,
				BillingPeriod: r.Object.Spec.BillingPeriod,
				DataCenter:    r.Object.Spec.DataCenter,
				Type:          r.Object.Spec.Type,
				Bootable:      r.Object.Spec.Bootable,
				Image:         r.Object.Spec.Image,
			},
		}

		blockStorageResp, err := r.HelperClient.CreateBlockStorage(ctx, projectID, blockStorageReq)
		if err != nil {
			return "", "", err
		}

		r.Object.Status.ProjectID = projectID

		state := ""
		if blockStorageResp.Status != nil {
			state = blockStorageResp.Status.State
		}

		return blockStorageResp.Metadata.ID, state, nil
	})
}

func (r *BlockStorageReconciler) Provisioning(ctx context.Context) (ctrl.Result, error) {
	return r.HandleProvisioning(ctx, func(ctx context.Context) (string, error) {
		blockStorageResp, err := r.HelperClient.GetBlockStorage(ctx, r.Object.Status.ProjectID, r.Object.Status.ResourceID)
		if err != nil {
			return "", err
		}

		if blockStorageResp.Status != nil {
			return blockStorageResp.Status.State, nil
		}
		return "", nil
	})
}

func (r *BlockStorageReconciler) Updating(ctx context.Context) (ctrl.Result, error) {
	return r.HandleUpdating(ctx, func(ctx context.Context) error {
		blockStorageReq := client.BlockStorageRequest{
			Metadata: client.BlockStorageMetadata{
				Name: r.Object.Name,
				Tags: r.Object.Spec.Tags,
				Location: client.BlockStorageLocation{
					Value: r.Object.Spec.Location.Value,
				},
			},
			Properties: client.BlockStorageProperties{
				SizeGb:        r.Object.Spec.SizeGb,
				BillingPeriod: r.Object.Spec.BillingPeriod,
				DataCenter:    r.Object.Spec.DataCenter,
			},
		}

		_, err := r.HelperClient.UpdateBlockStorage(ctx, r.Object.Status.ProjectID, r.Object.Status.ResourceID, blockStorageReq)
		return err
	})
}

func (r *BlockStorageReconciler) Created(ctx context.Context) (ctrl.Result, error) {
	return r.CheckForUpdates(ctx)
}

func (r *BlockStorageReconciler) Deleting(ctx context.Context) (ctrl.Result, error) {
	return r.HandleDeletion(ctx, blockStorageFinalizerName, func(ctx context.Context) error {
		return r.HelperClient.DeleteBlockStorage(ctx, r.Object.Status.ProjectID, r.Object.Status.ResourceID)
	})
}
