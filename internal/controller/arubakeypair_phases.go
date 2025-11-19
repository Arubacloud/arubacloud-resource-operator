package controller

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/Arubacloud/arubacloud-resource-operator/internal/client"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/util"
)

const (
	keyPairFinalizerName = "arubakeypair.cloud.aruba.it/finalizer"
)

func (r *ArubaKeyPairReconciler) Init(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.InitializeResource(ctx, keyPairFinalizerName)
}

func (r *ArubaKeyPairReconciler) Creating(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.HandleCreating(ctx, func(ctx context.Context) (string, string, error) {
		projectID, err := r.getProjectID(
			ctx,
			r.arubaObj.Spec.ProjectReference.Name,
			r.arubaObj.Spec.ProjectReference.Namespace,
		)
		if err != nil {
			return "", "", err
		}

		keyPairReq := client.KeyPairRequest{
			Metadata: client.KeyPairMetadata{
				Name: r.arubaObj.Name,
				Tags: r.arubaObj.Spec.Tags,
				Location: client.KeyPairLocation{
					Value: r.arubaObj.Spec.Location.Value,
				},
			},
			Properties: client.KeyPairProperties{
				Value: r.arubaObj.Spec.Value,
			},
		}

		keyPairResp, err := r.HelperClient.CreateKeyPair(ctx, projectID, keyPairReq)
		if err != nil {
			return "", "", err
		}

		r.arubaObj.Status.ProjectID = projectID

		state := ""
		if keyPairResp.Status != nil {
			state = keyPairResp.Status.State
		}

		return keyPairResp.Metadata.ID, state, nil
	})
}

func (r *ArubaKeyPairReconciler) Provisioning(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.HandleProvisioning(ctx, func(ctx context.Context) (string, error) {
		keyPairResp, err := r.HelperClient.GetKeyPair(ctx, r.arubaObj.Status.ProjectID, r.arubaObj.Status.ResourceID)
		if err != nil {
			return "", err
		}

		if keyPairResp.Status != nil {
			return keyPairResp.Status.State, nil
		}
		return "", nil
	})
}

func (r *ArubaKeyPairReconciler) Updating(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.HandleUpdating(ctx, func(ctx context.Context) error {
		keyPairReq := client.KeyPairUpdateRequest{
			Metadata: client.KeyPairMetadata{
				Name: r.arubaObj.Name,
				Tags: r.arubaObj.Spec.Tags,
				Location: client.KeyPairLocation{
					Value: r.arubaObj.Spec.Location.Value,
				},
			},
		}

		_, err := r.HelperClient.UpdateKeyPair(ctx, r.arubaObj.Status.ProjectID, r.arubaObj.Status.ResourceID, keyPairReq)
		return err
	})
}

func (r *ArubaKeyPairReconciler) Created(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.CheckForUpdates(ctx)
}

func (r *ArubaKeyPairReconciler) Deleting(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.HandleDeletion(ctx, keyPairFinalizerName, func(ctx context.Context) error {
		return r.HelperClient.DeleteKeyPair(ctx, r.arubaObj.Status.ProjectID, r.arubaObj.Status.ResourceID)
	})
}
