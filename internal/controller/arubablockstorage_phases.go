package controller

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	"gitlab.aruba.it/ingegneria/seca/operators/aruba-operator/internal/client"
	"gitlab.aruba.it/ingegneria/seca/operators/aruba-operator/internal/util"
)

const (
	blockStorageFinalizerName = "arubablockstorage.cloud.aruba.it/finalizer"
)

func (r *ArubaBlockStorageReconciler) Init(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.InitializeResource(ctx, blockStorageFinalizerName)
}

func (r *ArubaBlockStorageReconciler) Creating(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.HandleCreating(ctx, func(ctx context.Context) (string, string, error) {
		projectID, err := r.getProjectID(ctx, r.arubaObj.Spec.ProjectReference.Name, r.arubaObj.Spec.ProjectReference.Namespace)
		if err != nil {
			return "", "", err
		}

		blockStorageReq := client.BlockStorageRequest{
			Metadata: client.BlockStorageMetadata{
				Name: r.arubaObj.Name,
				Tags: r.arubaObj.Spec.Tags,
				Location: client.BlockStorageLocation{
					Value: r.arubaObj.Spec.Location.Value,
				},
			},
			Properties: client.BlockStorageProperties{
				SizeGb:        r.arubaObj.Spec.SizeGb,
				BillingPeriod: r.arubaObj.Spec.BillingPeriod,
				DataCenter:    r.arubaObj.Spec.DataCenter,
				Type:          r.arubaObj.Spec.Type,
				Bootable:      r.arubaObj.Spec.Bootable,
				Image:         r.arubaObj.Spec.Image,
			},
		}

		blockStorageResp, err := r.HelperClient.CreateBlockStorage(ctx, projectID, blockStorageReq)
		if err != nil {
			return "", "", err
		}

		r.arubaObj.Status.ProjectID = projectID

		state := ""
		if blockStorageResp.Status != nil {
			state = blockStorageResp.Status.State
		}

		return blockStorageResp.Metadata.ID, state, nil
	})
}

func (r *ArubaBlockStorageReconciler) Provisioning(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.HandleProvisioning(ctx, func(ctx context.Context) (string, error) {
		blockStorageResp, err := r.HelperClient.GetBlockStorage(ctx, r.arubaObj.Status.ProjectID, r.arubaObj.Status.ResourceID)
		if err != nil {
			return "", err
		}

		if blockStorageResp.Status != nil {
			return blockStorageResp.Status.State, nil
		}
		return "", nil
	})
}

func (r *ArubaBlockStorageReconciler) Updating(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.HandleUpdating(ctx, func(ctx context.Context) error {
		blockStorageReq := client.BlockStorageRequest{
			Metadata: client.BlockStorageMetadata{
				Name: r.arubaObj.Name,
				Tags: r.arubaObj.Spec.Tags,
				Location: client.BlockStorageLocation{
					Value: r.arubaObj.Spec.Location.Value,
				},
			},
			Properties: client.BlockStorageProperties{
				SizeGb:        r.arubaObj.Spec.SizeGb,
				BillingPeriod: r.arubaObj.Spec.BillingPeriod,
				DataCenter:    r.arubaObj.Spec.DataCenter,
			},
		}

		_, err := r.HelperClient.UpdateBlockStorage(ctx, r.arubaObj.Status.ProjectID, r.arubaObj.Status.ResourceID, blockStorageReq)
		return err
	})
}

func (r *ArubaBlockStorageReconciler) Created(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.CheckForUpdates(ctx)
}

func (r *ArubaBlockStorageReconciler) Deleting(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.HandleDeletion(ctx, blockStorageFinalizerName, func(ctx context.Context) error {
		return r.HelperClient.DeleteBlockStorage(ctx, r.arubaObj.Status.ProjectID, r.arubaObj.Status.ResourceID)
	})
}
