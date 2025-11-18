package controller

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	"gitlab.aruba.it/ingegneria/seca/operators/aruba-operator/internal/client"
	"gitlab.aruba.it/ingegneria/seca/operators/aruba-operator/internal/util"
)

const (
	vpcFinalizerName = "arubavpc.cloud.aruba.it/finalizer"
)

func (r *ArubaVpcReconciler) Init(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.InitializeResource(ctx, vpcFinalizerName)
}

func (r *ArubaVpcReconciler) Creating(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.HandleCreating(ctx, func(ctx context.Context) (string, string, error) {
		projectID, err := r.getProjectID(
			ctx,
			r.arubaObj.Spec.ProjectReference.Name,
			r.arubaObj.Spec.ProjectReference.Namespace,
		)
		if err != nil {
			return "", "", err
		}

		vpcReq := client.VpcRequest{
			Metadata: client.VpcMetadata{
				Name: r.arubaObj.Name,
				Tags: r.arubaObj.Spec.Tags,
				Location: client.VpcLocation{
					Value: r.arubaObj.Spec.Location.Value,
				},
			},
			Properties: client.VPCProperties{
				Default: false,
				Preset:  false,
			},
		}

		vpcResp, err := r.HelperClient.CreateVpc(ctx, projectID, vpcReq)
		if err != nil {
			return "", "", err
		}

		r.arubaObj.Status.ProjectID = projectID

		state := ""
		if vpcResp.Status != nil {
			state = vpcResp.Status.State
		}

		return vpcResp.Metadata.ID, state, nil
	})
}

func (r *ArubaVpcReconciler) Provisioning(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.HandleProvisioning(ctx, func(ctx context.Context) (string, error) {
		vpcResp, err := r.HelperClient.GetVpc(ctx, r.arubaObj.Status.ProjectID, r.arubaObj.Status.ResourceID)
		if err != nil {
			return "", err
		}

		if vpcResp.Status != nil {
			return vpcResp.Status.State, nil
		}
		return "", nil
	})
}

func (r *ArubaVpcReconciler) Updating(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.HandleUpdating(ctx, func(ctx context.Context) error {
		vpcReq := client.VpcRequest{
			Metadata: client.VpcMetadata{
				Name: r.arubaObj.Name,
				Tags: r.arubaObj.Spec.Tags,
				Location: client.VpcLocation{
					Value: r.arubaObj.Spec.Location.Value,
				},
			},
		}

		_, err := r.HelperClient.UpdateVpc(ctx, r.arubaObj.Status.ProjectID, r.arubaObj.Status.ResourceID, vpcReq)
		return err
	})
}

func (r *ArubaVpcReconciler) Created(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.CheckForUpdates(ctx)
}

func (r *ArubaVpcReconciler) Deleting(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.HandleDeletion(ctx, vpcFinalizerName, func(ctx context.Context) error {
		return r.HelperClient.DeleteVpc(ctx, r.arubaObj.Status.ProjectID, r.arubaObj.Status.ResourceID)
	})
}
