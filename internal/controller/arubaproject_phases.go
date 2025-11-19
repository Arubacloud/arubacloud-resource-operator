package controller

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/Arubacloud/arubacloud-resource-operator/internal/client"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/util"
)

const (
	projectFinalizerName = "arubaproject.cloud.aruba.it/finalizer"
)

func (r *ArubaProjectReconciler) Init(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.InitializeResource(ctx, projectFinalizerName)
}

func (r *ArubaProjectReconciler) Creating(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.HandleCreating(ctx, func(ctx context.Context) (string, string, error) {
		projectReq := client.ProjectRequest{
			Metadata: client.ProjectMetadata{
				Name: r.arubaObj.Name,
				Tags: r.arubaObj.Spec.Tags,
			},
			Properties: client.ProjectProperties{
				Description: r.arubaObj.Spec.Description,
				Default:     r.arubaObj.Spec.Default,
			},
		}

		projectResp, err := r.HelperClient.CreateProject(ctx, projectReq)
		if err != nil {
			return "", "", err
		}

		return projectResp.Metadata.ID, "", nil
	})
}

func (r *ArubaProjectReconciler) Updating(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.HandleUpdating(ctx, func(ctx context.Context) error {
		projectReq := client.ProjectRequest{
			Metadata: client.ProjectMetadata{
				Name: r.arubaObj.Name,
				Tags: r.arubaObj.Spec.Tags,
			},
			Properties: client.ProjectProperties{
				Description: r.arubaObj.Spec.Description,
				Default:     r.arubaObj.Spec.Default,
			},
		}

		_, err := r.HelperClient.UpdateProject(ctx, r.arubaObj.Status.ResourceID, projectReq)
		return err
	})
}

func (r *ArubaProjectReconciler) Created(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.CheckForUpdates(ctx)
}

func (r *ArubaProjectReconciler) Deleting(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.HandleDeletion(ctx, projectFinalizerName, func(ctx context.Context) error {
		return r.HelperClient.DeleteProject(ctx, r.arubaObj.Status.ResourceID)
	})
}
