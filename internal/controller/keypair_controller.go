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

// KeyPairReconciler reconciles a KeyPair object
type KeyPairReconciler struct {
	*reconciler.Reconciler
}

// NewKeyPairReconciler creates a new KeyPairReconciler
func NewKeyPairReconciler(reconciler *reconciler.Reconciler) *KeyPairReconciler {
	return &KeyPairReconciler{
		Reconciler: reconciler,
	}
}

// +kubebuilder:rbac:groups=arubacloud.com,resources=keypairs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=arubacloud.com,resources=keypairs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=arubacloud.com,resources=keypairs/finalizers,verbs=update
// +kubebuilder:rbac:groups=arubacloud.com,resources=projects,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

func (r *KeyPairReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := &v1alpha1.KeyPair{}
	return r.Reconciler.Reconcile(ctx, req, obj, &obj.Status.ResourceStatus, r, &obj.Spec.Tenant)
}

// SetupWithManager sets up the controller with the Manager.
func (r *KeyPairReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.KeyPair{}).
		Named("keypair").
		Complete(r)
}

const (
	keyPairFinalizerName = "keypair.arubacloud.com/finalizer"
)

func (r *KeyPairReconciler) Init(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	return r.InitializeResource(ctx, obj, status, keyPairFinalizerName)
}

func (r *KeyPairReconciler) Creating(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	keyPair := obj.(*v1alpha1.KeyPair)
	return r.HandleCreating(ctx, obj, status, func(ctx context.Context) (string, string, error) {
		projectID, err := r.GetProjectID(
			ctx,
			keyPair.Spec.ProjectReference.Name,
			keyPair.Spec.ProjectReference.Namespace,
		)
		if err != nil {
			return "", "", err
		}

		keyPairReq := arubaClient.KeyPairRequest{
			Metadata: arubaClient.KeyPairMetadata{
				Name: keyPair.Name,
				Tags: keyPair.Spec.Tags,
				Location: arubaClient.KeyPairLocation{
					Value: keyPair.Spec.Location.Value,
				},
			},
			Properties: arubaClient.KeyPairProperties{
				Value: keyPair.Spec.Value,
			},
		}

		keyPairResp, err := r.CreateKeyPair(ctx, projectID, keyPairReq)
		if err != nil {
			return "", "", err
		}

		keyPair.Status.ProjectID = projectID

		state := ""
		if keyPairResp.Status != nil {
			state = keyPairResp.Status.State
		}

		return keyPairResp.Metadata.ID, state, nil
	})
}

func (r *KeyPairReconciler) Provisioning(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	keyPair := obj.(*v1alpha1.KeyPair)
	return r.HandleProvisioning(ctx, obj, status, func(ctx context.Context) (string, error) {
		keyPairResp, err := r.GetKeyPair(ctx, keyPair.Status.ProjectID, status.ResourceID)
		if err != nil {
			return "", err
		}

		if keyPairResp.Status != nil {
			return keyPairResp.Status.State, nil
		}
		return "", nil
	})
}

func (r *KeyPairReconciler) Updating(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	keyPair := obj.(*v1alpha1.KeyPair)
	return r.HandleUpdating(ctx, obj, status, func(ctx context.Context) error {
		keyPairReq := arubaClient.KeyPairUpdateRequest{
			Metadata: arubaClient.KeyPairMetadata{
				Name: keyPair.Name,
				Tags: keyPair.Spec.Tags,
				Location: arubaClient.KeyPairLocation{
					Value: keyPair.Spec.Location.Value,
				},
			},
		}

		_, err := r.UpdateKeyPair(ctx, keyPair.Status.ProjectID, status.ResourceID, keyPairReq)
		return err
	})
}

func (r *KeyPairReconciler) Created(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	return r.CheckForUpdates(ctx, obj, status)
}

func (r *KeyPairReconciler) Deleting(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	keyPair := obj.(*v1alpha1.KeyPair)
	return r.HandleDeletion(ctx, obj, status, keyPairFinalizerName, func(ctx context.Context) error {
		return r.DeleteKeyPair(ctx, keyPair.Status.ProjectID, status.ResourceID)
	})
}
