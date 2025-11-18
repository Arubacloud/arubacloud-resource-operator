package controller

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	"gitlab.aruba.it/ingegneria/seca/operators/aruba-operator/internal/client"
	"gitlab.aruba.it/ingegneria/seca/operators/aruba-operator/internal/util"
)

const (
	securityRuleFinalizerName = "arubasecurityrule.cloud.aruba.it/finalizer"
)

func (r *ArubaSecurityRuleReconciler) Init(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.InitializeResource(ctx, securityRuleFinalizerName)
}

func (r *ArubaSecurityRuleReconciler) Creating(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
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

		securityGroupID, err := r.getSecurityGroupID(
			ctx,
			r.arubaObj.Spec.SecurityGroupReference.Name,
			r.arubaObj.Spec.SecurityGroupReference.Namespace,
		)
		if err != nil {
			return "", "", err
		}

		securityRuleReq := client.SecurityRuleRequest{
			Metadata: client.SecurityRuleMetadata{
				Name: r.arubaObj.Name,
				Tags: r.arubaObj.Spec.Tags,
				Location: client.SecurityRuleLocation{
					Value: r.arubaObj.Spec.Location.Value,
				},
			},
			Properties: client.SecurityRuleProperties{
				Protocol:  r.arubaObj.Spec.Protocol,
				Port:      r.arubaObj.Spec.Port,
				Direction: r.arubaObj.Spec.Direction,
				Target: client.SecurityRuleTarget{
					Kind:  r.arubaObj.Spec.Target.Kind,
					Value: r.arubaObj.Spec.Target.Value,
				},
			},
		}

		securityRuleResp, err := r.HelperClient.CreateSecurityRule(ctx, projectID, vpcID, securityGroupID, securityRuleReq)
		if err != nil {
			return "", "", err
		}

		r.arubaObj.Status.ProjectID = projectID
		r.arubaObj.Status.VpcID = vpcID
		r.arubaObj.Status.SecurityGroupID = securityGroupID

		state := ""
		if securityRuleResp.Status != nil {
			state = securityRuleResp.Status.State
		}

		return securityRuleResp.Metadata.ID, state, nil
	})
}

func (r *ArubaSecurityRuleReconciler) Provisioning(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.HandleProvisioning(ctx, func(ctx context.Context) (string, error) {
		securityRuleResp, err := r.HelperClient.GetSecurityRule(ctx, r.arubaObj.Status.ProjectID, r.arubaObj.Status.VpcID, r.arubaObj.Status.SecurityGroupID, r.arubaObj.Status.ResourceID)
		if err != nil {
			return "", err
		}

		if securityRuleResp.Status != nil {
			return securityRuleResp.Status.State, nil
		}
		return "", nil
	})
}

func (r *ArubaSecurityRuleReconciler) Updating(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.HandleUpdating(ctx, func(ctx context.Context) error {
		securityRuleReq := client.SecurityRuleRequest{
			Metadata: client.SecurityRuleMetadata{
				Name: r.arubaObj.Name,
				Tags: r.arubaObj.Spec.Tags,
				Location: client.SecurityRuleLocation{
					Value: r.arubaObj.Spec.Location.Value,
				},
			},
			Properties: client.SecurityRuleProperties{
				Protocol:  r.arubaObj.Spec.Protocol,
				Port:      r.arubaObj.Spec.Port,
				Direction: r.arubaObj.Spec.Direction,
				Target: client.SecurityRuleTarget{
					Kind:  r.arubaObj.Spec.Target.Kind,
					Value: r.arubaObj.Spec.Target.Value,
				},
			},
		}

		_, err := r.HelperClient.UpdateSecurityRule(ctx, r.arubaObj.Status.ProjectID, r.arubaObj.Status.VpcID, r.arubaObj.Status.SecurityGroupID, r.arubaObj.Status.ResourceID, securityRuleReq)
		return err
	})
}

func (r *ArubaSecurityRuleReconciler) Created(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.CheckForUpdates(ctx)
}

func (r *ArubaSecurityRuleReconciler) Deleting(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.HandleDeletion(ctx, securityRuleFinalizerName, func(ctx context.Context) error {
		return r.HelperClient.DeleteSecurityRule(ctx, r.arubaObj.Status.ProjectID, r.arubaObj.Status.VpcID, r.arubaObj.Status.SecurityGroupID, r.arubaObj.Status.ResourceID)
	})
}
