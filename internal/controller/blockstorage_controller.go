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

// BlockStorageReconciler reconciles a BlockStorage object
type BlockStorageReconciler struct {
	*reconciler.Reconciler
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
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

func (r *BlockStorageReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := &v1alpha1.BlockStorage{}
	return r.Reconciler.Reconcile(ctx, req, obj, &obj.Status.ResourceStatus, r, &obj.Spec.Tenant)
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

func (r *BlockStorageReconciler) Init(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	return r.InitializeResource(ctx, obj, status, blockStorageFinalizerName)
}

func (r *BlockStorageReconciler) Creating(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	blockStorage := obj.(*v1alpha1.BlockStorage)
	return r.HandleCreating(ctx, obj, status, func(ctx context.Context) (string, string, error) {
		projectID, err := r.GetProjectID(ctx, blockStorage.Spec.ProjectReference.Name, blockStorage.Spec.ProjectReference.Namespace)
		if err != nil {
			return "", "", err
		}

		blockStorageReq := arubaClient.BlockStorageRequest{
			Metadata: arubaClient.BlockStorageMetadata{
				Name: blockStorage.Name,
				Tags: blockStorage.Spec.Tags,
				Location: arubaClient.BlockStorageLocation{
					Value: blockStorage.Spec.Location.Value,
				},
			},
			Properties: arubaClient.BlockStorageProperties{
				SizeGb:        blockStorage.Spec.SizeGb,
				BillingPeriod: blockStorage.Spec.BillingPeriod,
				DataCenter:    blockStorage.Spec.DataCenter,
				Type:          blockStorage.Spec.Type,
				Bootable:      blockStorage.Spec.Bootable,
				Image:         blockStorage.Spec.Image,
			},
		}

		blockStorageResp, err := r.CreateBlockStorage(ctx, projectID, blockStorageReq)
		if err != nil {
			return "", "", err
		}

		blockStorage.Status.ProjectID = projectID

		state := ""
		if blockStorageResp.Status != nil {
			state = blockStorageResp.Status.State
		}

		return blockStorageResp.Metadata.ID, state, nil
	})
}

func (r *BlockStorageReconciler) Provisioning(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	blockStorage := obj.(*v1alpha1.BlockStorage)
	return r.HandleProvisioning(ctx, obj, status, func(ctx context.Context) (string, error) {
		blockStorageResp, err := r.GetBlockStorage(ctx, blockStorage.Status.ProjectID, status.ResourceID)
		if err != nil {
			return "", err
		}

		if blockStorageResp.Status != nil {
			return blockStorageResp.Status.State, nil
		}
		return "", nil
	})
}

func (r *BlockStorageReconciler) Updating(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	blockStorage := obj.(*v1alpha1.BlockStorage)
	return r.HandleUpdating(ctx, obj, status, func(ctx context.Context) error {
		blockStorageReq := arubaClient.BlockStorageRequest{
			Metadata: arubaClient.BlockStorageMetadata{
				Name: blockStorage.Name,
				Tags: blockStorage.Spec.Tags,
				Location: arubaClient.BlockStorageLocation{
					Value: blockStorage.Spec.Location.Value,
				},
			},
			Properties: arubaClient.BlockStorageProperties{
				SizeGb:        blockStorage.Spec.SizeGb,
				BillingPeriod: blockStorage.Spec.BillingPeriod,
				DataCenter:    blockStorage.Spec.DataCenter,
			},
		}

		_, err := r.UpdateBlockStorage(ctx, blockStorage.Status.ProjectID, status.ResourceID, blockStorageReq)
		return err
	})
}

func (r *BlockStorageReconciler) Created(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	return r.CheckForUpdates(ctx, obj, status)
}

func (r *BlockStorageReconciler) Deleting(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	blockStorage := obj.(*v1alpha1.BlockStorage)
	return r.HandleDeletion(ctx, obj, status, blockStorageFinalizerName, func(ctx context.Context) error {
		return r.DeleteBlockStorage(ctx, blockStorage.Status.ProjectID, status.ResourceID)
	})
}
