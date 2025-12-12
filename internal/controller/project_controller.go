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

// ProjectReconciler reconciles a Project object
type ProjectReconciler struct {
	*reconciler.Reconciler
}

// NewProjectReconciler creates a new ProjectReconciler
func NewProjectReconciler(reconciler *reconciler.Reconciler) *ProjectReconciler {
	return &ProjectReconciler{
		Reconciler: reconciler,
	}
}

// +kubebuilder:rbac:groups=arubacloud.com,resources=projects,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=arubacloud.com,resources=projects/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=arubacloud.com,resources=projects/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

func (r *ProjectReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := &v1alpha1.Project{}
	return r.Reconciler.Reconcile(ctx, req, obj, &obj.Status, r, &obj.Spec.Tenant)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ProjectReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Project{}).
		Named("project").
		Complete(r)
}

const (
	projectFinalizerName = "project.arubacloud.com/finalizer"
)

func (r *ProjectReconciler) Init(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	return r.InitializeResource(ctx, obj, status, projectFinalizerName)
}

func (r *ProjectReconciler) Creating(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	project := obj.(*v1alpha1.Project)
	return r.HandleCreating(ctx, obj, status, func(ctx context.Context) (string, string, error) {
		projectReq := arubaClient.ProjectRequest{
			Metadata: arubaClient.ProjectMetadata{
				Name: project.Name,
				Tags: project.Spec.Tags,
			},
			Properties: arubaClient.ProjectProperties{
				Description: project.Spec.Description,
				Default:     project.Spec.Default,
			},
		}

		projectResp, err := r.CreateProject(ctx, projectReq)
		if err != nil {
			return "", "", err
		}

		return projectResp.Metadata.ID, "", nil
	})
}

func (r *ProjectReconciler) Provisioning(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	return ctrl.Result{}, nil
}

func (r *ProjectReconciler) Updating(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	project := obj.(*v1alpha1.Project)
	return r.HandleUpdating(ctx, obj, status, func(ctx context.Context) error {
		projectReq := arubaClient.ProjectRequest{
			Metadata: arubaClient.ProjectMetadata{
				Name: project.Name,
				Tags: project.Spec.Tags,
			},
			Properties: arubaClient.ProjectProperties{
				Description: project.Spec.Description,
				Default:     project.Spec.Default,
			},
		}

		_, err := r.UpdateProject(ctx, status.ResourceID, projectReq)
		return err
	})
}

func (r *ProjectReconciler) Created(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	return r.CheckForUpdates(ctx, obj, status)
}

func (r *ProjectReconciler) Deleting(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	return r.HandleDeletion(ctx, obj, status, projectFinalizerName, func(ctx context.Context) error {
		return r.DeleteProject(ctx, status.ResourceID)
	})
}
