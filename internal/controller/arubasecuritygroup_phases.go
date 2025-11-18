package controller

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	"gitlab.aruba.it/ingegneria/seca/operators/aruba-operator/internal/client"
	"gitlab.aruba.it/ingegneria/seca/operators/aruba-operator/internal/util"
)

const (
	securityGroupFinalizerName = "arubasecuritygroup.cloud.aruba.it/finalizer"
)

func (r *ArubaSecurityGroupReconciler) Init(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.InitializeResource(ctx, securityGroupFinalizerName)
}

func (r *ArubaSecurityGroupReconciler) Creating(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.HandleCreating(ctx, func(ctx context.Context) (string, string, error) {
		projectID, err := r.getProjectID(
			ctx,
			r.arubaObj.Spec.ProjectReference.Name,
			r.arubaObj.Spec.ProjectReference.Namespace,
		)
		if err != nil {
			return "", "", err
		}

		vpcID, err := r.getVpcID(ctx, r.arubaObj.Spec.VpcReference.Name, r.arubaObj.Spec.VpcReference.Namespace)
		if err != nil {
			return "", "", err
		}

		securityGroupReq := client.SecurityGroupRequest{
			Metadata: client.SecurityGroupMetadata{
				Name: r.arubaObj.Name,
				Tags: r.arubaObj.Spec.Tags,
				Location: client.SecurityGroupLocation{
					Value: r.arubaObj.Spec.Location.Value,
				},
			},
			Properties: client.SecurityGroupProperties{
				Default: r.arubaObj.Spec.Default,
			},
		}

		securityGroupResp, err := r.HelperClient.CreateSecurityGroup(ctx, projectID, vpcID, securityGroupReq)
		if err != nil {
			return "", "", err
		}

		r.arubaObj.Status.ProjectID = projectID
		r.arubaObj.Status.VpcID = vpcID

		state := ""
		if securityGroupResp.Status != nil {
			state = securityGroupResp.Status.State
		}

		return securityGroupResp.Metadata.ID, state, nil
	})
}

func (r *ArubaSecurityGroupReconciler) Provisioning(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.HandleProvisioning(ctx, func(ctx context.Context) (string, error) {
		securityGroupResp, err := r.HelperClient.GetSecurityGroup(ctx, r.arubaObj.Status.ProjectID, r.arubaObj.Status.VpcID, r.arubaObj.Status.ResourceID)
		if err != nil {
			return "", err
		}

		if securityGroupResp.Status != nil {
			return securityGroupResp.Status.State, nil
		}
		return "", nil
	})
}

func (r *ArubaSecurityGroupReconciler) Updating(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.HandleUpdating(ctx, func(ctx context.Context) error {
		securityGroupReq := client.SecurityGroupRequest{
			Metadata: client.SecurityGroupMetadata{
				Name: r.arubaObj.Name,
				Tags: r.arubaObj.Spec.Tags,
				Location: client.SecurityGroupLocation{
					Value: r.arubaObj.Spec.Location.Value,
				},
			},
			Properties: client.SecurityGroupProperties{
				Default: r.arubaObj.Spec.Default,
			},
		}

		_, err := r.HelperClient.UpdateSecurityGroup(ctx, r.arubaObj.Status.ProjectID, r.arubaObj.Status.VpcID, r.arubaObj.Status.ResourceID, securityGroupReq)
		return err
	})
}

func (r *ArubaSecurityGroupReconciler) Created(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.CheckForUpdates(ctx)
}

func (r *ArubaSecurityGroupReconciler) Deleting(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.HandleDeletion(ctx, securityGroupFinalizerName, func(ctx context.Context) error {
		return r.HelperClient.DeleteSecurityGroup(ctx, r.arubaObj.Status.ProjectID, r.arubaObj.Status.VpcID, r.arubaObj.Status.ResourceID)
	})
}
