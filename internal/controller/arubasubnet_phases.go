package controller

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/Arubacloud/arubacloud-resource-operator/internal/client"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/util"
)

const (
	subnetFinalizerName = "arubasubnet.cloud.aruba.it/finalizer"
)

func (r *ArubaSubnetReconciler) Init(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.InitializeResource(ctx, subnetFinalizerName)
}

func (r *ArubaSubnetReconciler) Creating(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.HandleCreating(ctx, func(ctx context.Context) (string, string, error) {
		projectID, err := r.getProjectID(ctx, r.arubaObj.Spec.ProjectReference.Name, r.arubaObj.Spec.ProjectReference.Namespace)
		if err != nil {
			return "", "", err
		}

		vpcID, err := r.getVpcID(ctx, r.arubaObj.Spec.VpcReference.Name, r.arubaObj.Spec.VpcReference.Namespace)
		if err != nil {
			return "", "", err
		}

		subnetReq := client.SubnetRequest{
			Metadata: client.SubnetMetadata{
				Name: r.arubaObj.Name,
				Tags: r.arubaObj.Spec.Tags,
			},
			Properties: client.SubnetProperties{
				Type:    r.arubaObj.Spec.Type,
				Default: r.arubaObj.Spec.Default,
				Network: client.SubnetNetwork{
					Address: r.arubaObj.Spec.Network.Address,
				},
				DHCP: client.SubnetDHCP{
					Enabled: r.arubaObj.Spec.DHCP.Enabled,
				},
			},
		}

		subnetResp, err := r.HelperClient.CreateSubnet(ctx, projectID, vpcID, subnetReq)
		if err != nil {
			return "", "", err
		}

		r.arubaObj.Status.ProjectID = projectID
		r.arubaObj.Status.VpcID = vpcID

		state := ""
		if subnetResp.Status != nil {
			state = subnetResp.Status.State
		}

		return subnetResp.Metadata.ID, state, nil
	})
}

func (r *ArubaSubnetReconciler) Provisioning(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.HandleProvisioning(ctx, func(ctx context.Context) (string, error) {
		subnetResp, err := r.HelperClient.GetSubnet(ctx, r.arubaObj.Status.ProjectID, r.arubaObj.Status.VpcID, r.arubaObj.Status.ResourceID)
		if err != nil {
			return "", err
		}

		if subnetResp.Status != nil {
			return subnetResp.Status.State, nil
		}
		return "", nil
	})
}

func (r *ArubaSubnetReconciler) Updating(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.HandleUpdating(ctx, func(ctx context.Context) error {
		subnetReq := client.SubnetRequest{
			Metadata: client.SubnetMetadata{
				Name: r.arubaObj.Name,
				Tags: r.arubaObj.Spec.Tags,
			},
			Properties: client.SubnetProperties{
				Type:    r.arubaObj.Spec.Type,
				Default: r.arubaObj.Spec.Default,
				Network: client.SubnetNetwork{
					Address: r.arubaObj.Spec.Network.Address,
				},
				DHCP: client.SubnetDHCP{
					Enabled: r.arubaObj.Spec.DHCP.Enabled,
				},
			},
		}

		_, err := r.HelperClient.UpdateSubnet(ctx, r.arubaObj.Status.ProjectID, r.arubaObj.Status.VpcID, r.arubaObj.Status.ResourceID, subnetReq)
		return err
	})
}

func (r *ArubaSubnetReconciler) Created(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.CheckForUpdates(ctx)
}

func (r *ArubaSubnetReconciler) Deleting(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.HandleDeletion(ctx, subnetFinalizerName, func(ctx context.Context) error {
		return r.HelperClient.DeleteSubnet(ctx, r.arubaObj.Status.ProjectID, r.arubaObj.Status.VpcID, r.arubaObj.Status.ResourceID)
	})
}
