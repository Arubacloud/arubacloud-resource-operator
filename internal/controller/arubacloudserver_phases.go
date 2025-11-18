package controller

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"gitlab.aruba.it/ingegneria/seca/operators/aruba-operator/api/v1alpha1"
	"gitlab.aruba.it/ingegneria/seca/operators/aruba-operator/internal/client"
	"gitlab.aruba.it/ingegneria/seca/operators/aruba-operator/internal/util"
)

const (
	cloudServerFinalizerName = "arubacloudserver.cloud.aruba.it/finalizer"
)

func (r *ArubaCloudServerReconciler) Init(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.InitializeResource(ctx, cloudServerFinalizerName)
}

func (r *ArubaCloudServerReconciler) Creating(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.HandleCreating(ctx, func(ctx context.Context) (string, string, error) {
		projectID, err := r.getProjectID(ctx, r.arubaObj.Spec.ProjectReference.Name, r.arubaObj.Spec.ProjectReference.Namespace)
		if err != nil {
			return "", "", err
		}

		vpcID, err := r.getVpcID(ctx, r.arubaObj.Spec.VpcReference.Name, r.arubaObj.Spec.VpcReference.Namespace)
		if err != nil {
			return "", "", err
		}

		bootVolumeID, err := r.getBlockStorageID(ctx, r.arubaObj.Spec.BootVolumeReference.Name, r.arubaObj.Spec.BootVolumeReference.Namespace)
		if err != nil {
			return "", "", err
		}

		keyPairID, err := r.getKeyPairID(ctx, r.arubaObj.Spec.KeyPairReference.Name, r.arubaObj.Spec.KeyPairReference.Namespace)
		if err != nil {
			return "", "", err
		}

		// Resolve subnet IDs
		subnetIDs := make([]string, len(r.arubaObj.Spec.SubnetReferences))
		for i, subnetRef := range r.arubaObj.Spec.SubnetReferences {
			subnetID, err := r.getSubnetID(ctx, subnetRef.Name, subnetRef.Namespace)
			if err != nil {
				return "", "", fmt.Errorf("failed to get subnet ID for %s/%s: %w", subnetRef.Namespace, subnetRef.Name, err)
			}
			subnetIDs[i] = subnetID
		}

		// Resolve security group IDs
		securityGroupIDs := make([]string, len(r.arubaObj.Spec.SecurityGroupReferences))
		for i, sgRef := range r.arubaObj.Spec.SecurityGroupReferences {
			sgID, err := r.getSecurityGroupID(ctx, sgRef.Name, sgRef.Namespace)
			if err != nil {
				return "", "", fmt.Errorf("failed to get security group ID for %s/%s: %w", sgRef.Namespace, sgRef.Name, err)
			}
			securityGroupIDs[i] = sgID
		}

		// Create cloud server via API
		cloudServerReq := client.CloudServerRequest{
			Metadata: client.CloudServerMetadata{
				Name: r.arubaObj.Name,
				Tags: r.arubaObj.Spec.Tags,
				Location: client.CloudServerLocation{
					Value: r.arubaObj.Spec.Location.Value,
				},
			},
			Properties: client.CloudServerProperties{
				DataCenter: r.arubaObj.Spec.DataCenter,
				VPC:        client.CloudServerResourceReference{URI: r.buildVpcURI(projectID, vpcID)},
				BootVolume: client.CloudServerResourceReference{URI: r.buildVolumeURI(projectID, bootVolumeID)},
				VpcPreset:  r.arubaObj.Spec.VpcPreset,
				FlavorName: r.arubaObj.Spec.FlavorName,
				KeyPair:    client.CloudServerResourceReference{URI: r.buildKeyPairURI(projectID, keyPairID)},
			},
		}

		// Add optional elastic IP
		var elasticIpID string
		if r.arubaObj.Spec.ElasticIpReference != nil {
			elasticIpID, err = r.getElasticIpID(ctx, r.arubaObj.Spec.ElasticIpReference.Name, r.arubaObj.Spec.ElasticIpReference.Namespace)
			if err != nil {
				return "", "", fmt.Errorf("failed to get elastic IP ID: %w", err)
			}
			cloudServerReq.Properties.ElasticIp = &client.CloudServerResourceReference{URI: r.buildElasticIpURI(projectID, elasticIpID)}
		}

		// Add subnets
		for _, subnetID := range subnetIDs {
			cloudServerReq.Properties.Subnets = append(cloudServerReq.Properties.Subnets,
				client.CloudServerResourceReference{URI: r.buildSubnetURI(projectID, vpcID, subnetID)})
		}

		// Add security groups
		for _, sgID := range securityGroupIDs {
			cloudServerReq.Properties.SecurityGroups = append(cloudServerReq.Properties.SecurityGroups,
				client.CloudServerResourceReference{URI: r.buildSecurityGroupURI(projectID, vpcID, sgID)})
		}

		cloudServerResp, err := r.HelperClient.CreateCloudServer(ctx, projectID, cloudServerReq)
		if err != nil {
			return "", "", err
		}

		// Update status with cloud server ID and all resolved IDs
		r.arubaObj.Status.ProjectID = projectID
		r.arubaObj.Status.VpcID = vpcID
		r.arubaObj.Status.SubnetIDs = subnetIDs
		r.arubaObj.Status.SecurityGroupIDs = securityGroupIDs
		r.arubaObj.Status.BootVolumeID = bootVolumeID
		if elasticIpID != "" {
			r.arubaObj.Status.ElasticIpID = elasticIpID
		}
		r.arubaObj.Status.KeyPairID = keyPairID

		state := ""
		if cloudServerResp.Status != nil {
			state = cloudServerResp.Status.State
		}

		return cloudServerResp.Metadata.ID, state, nil
	})
}

// Provisioning handles checking remote state during provisioning
func (r *ArubaCloudServerReconciler) Provisioning(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.HandleProvisioning(ctx, func(ctx context.Context) (string, error) {
		cloudServerResp, err := r.HelperClient.GetCloudServer(ctx, r.arubaObj.Status.ProjectID, r.arubaObj.Status.ResourceID)
		if err != nil {
			return "", err
		}

		if cloudServerResp.Status != nil {
			return cloudServerResp.Status.State, nil
		}
		return "", nil
	})
}

// Updating handles cloud server updates
func (r *ArubaCloudServerReconciler) Updating(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.HandleUpdating(ctx, func(ctx context.Context) error {
		// Re-resolve all IDs in case references changed
		projectID := r.arubaObj.Status.ProjectID
		vpcID := r.arubaObj.Status.VpcID

		// Check if we need to update cloud server properties (generation mismatch)
		needsPropertyUpdate := pm.Status.ObservedGeneration != pm.Object.GetGeneration()

		if needsPropertyUpdate {
			// Resolve subnet IDs
			subnetIDs := make([]string, len(r.arubaObj.Spec.SubnetReferences))
			for i, subnetRef := range r.arubaObj.Spec.SubnetReferences {
				subnetID, err := r.getSubnetID(ctx, subnetRef.Name, subnetRef.Namespace)
				if err != nil {
					return fmt.Errorf("failed to get subnet ID for %s/%s: %w", subnetRef.Namespace, subnetRef.Name, err)
				}
				subnetIDs[i] = subnetID
			}

			// Resolve security group IDs
			securityGroupIDs := make([]string, len(r.arubaObj.Spec.SecurityGroupReferences))
			for i, sgRef := range r.arubaObj.Spec.SecurityGroupReferences {
				sgID, err := r.getSecurityGroupID(ctx, sgRef.Name, sgRef.Namespace)
				if err != nil {
					return fmt.Errorf("failed to get security group ID for %s/%s: %w", sgRef.Namespace, sgRef.Name, err)
				}
				securityGroupIDs[i] = sgID
			}

			// Update cloud server via API
			cloudServerReq := client.CloudServerRequest{
				Metadata: client.CloudServerMetadata{
					Name: r.arubaObj.Name,
					Tags: r.arubaObj.Spec.Tags,
					Location: client.CloudServerLocation{
						Value: r.arubaObj.Spec.Location.Value,
					},
				},
			}

			// Add optional fields
			var elasticIpID string
			if r.arubaObj.Spec.ElasticIpReference != nil {
				elasticIpID, err := r.getElasticIpID(ctx, r.arubaObj.Spec.ElasticIpReference.Name, r.arubaObj.Spec.ElasticIpReference.Namespace)
				if err != nil {
					return fmt.Errorf("failed to get elastic IP ID: %w", err)
				}
				cloudServerReq.Properties.ElasticIp = &client.CloudServerResourceReference{URI: r.buildElasticIpURI(projectID, elasticIpID)}
			}

			// Add subnets and security groups
			for _, subnetID := range subnetIDs {
				cloudServerReq.Properties.Subnets = append(cloudServerReq.Properties.Subnets,
					client.CloudServerResourceReference{URI: r.buildSubnetURI(projectID, vpcID, subnetID)})
			}
			for _, sgID := range securityGroupIDs {
				cloudServerReq.Properties.SecurityGroups = append(cloudServerReq.Properties.SecurityGroups,
					client.CloudServerResourceReference{URI: r.buildSecurityGroupURI(projectID, vpcID, sgID)})
			}

			_, err := r.HelperClient.UpdateCloudServer(ctx, r.arubaObj.Status.ProjectID, r.arubaObj.Status.ResourceID, cloudServerReq)
			if err != nil {
				return err
			}

			// Update status with new resolved IDs
			r.arubaObj.Status.SubnetIDs = subnetIDs
			r.arubaObj.Status.SecurityGroupIDs = securityGroupIDs
			if elasticIpID != "" {
				r.arubaObj.Status.ElasticIpID = elasticIpID
			} else {
				r.arubaObj.Status.ElasticIpID = ""
			}
		}

		// Now handle data volume management
		return r.manageDataVolumesInUpdate(ctx, projectID)
	})
}

// manageDataVolumesInUpdate handles attaching and detaching data volumes during update phase
func (r *ArubaCloudServerReconciler) manageDataVolumesInUpdate(ctx context.Context, projectID string) error {
	phaseLogger := ctrl.Log.WithValues("Phase", "Updating", "Kind", r.arubaObj.GetObjectKind().GroupVersionKind().Kind, "Name", r.arubaObj.GetName())

	// Resolve and calculate volume changes
	desiredVolumeIDs, toAttach, toDetach, err := r.resolveAndCheckDataVolumes(ctx)
	if err != nil {
		phaseLogger.Error(err, "failed to resolve data volume references")
		return err
	}

	// If no changes needed, return early
	if len(toAttach) == 0 && len(toDetach) == 0 {
		return nil
	}

	phaseLogger.Info("Managing data volumes", "toAttach", toAttach, "toDetach", toDetach)

	// Build attach/detach request
	req := client.AttachDetachDataVolumesRequest{
		VolumesToAttach: make([]client.CloudServerResourceReference, 0, len(toAttach)),
		VolumesToDetach: make([]client.CloudServerResourceReference, 0, len(toDetach)),
	}

	for _, volumeID := range toAttach {
		req.VolumesToAttach = append(req.VolumesToAttach, client.CloudServerResourceReference{
			URI: r.buildVolumeURI(projectID, volumeID),
		})
	}

	for _, volumeID := range toDetach {
		req.VolumesToDetach = append(req.VolumesToDetach, client.CloudServerResourceReference{
			URI: r.buildVolumeURI(projectID, volumeID),
		})
	}

	// Call API to attach/detach volumes
	_, err = r.HelperClient.AttachDetachDataVolumes(ctx, projectID, r.arubaObj.Status.ResourceID, req)
	if err != nil {
		return err
	}

	// Update status with new data volume IDs
	r.arubaObj.Status.DataVolumeIDs = desiredVolumeIDs

	phaseLogger.Info("Data volumes managed successfully")
	return nil
}

// Created handles the steady state
func (r *ArubaCloudServerReconciler) Created(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	phaseLogger := ctrl.Log.WithValues("Phase", pm.Status.Phase, "Kind", pm.Object.GetObjectKind().GroupVersionKind().Kind, "Name", pm.Object.GetName())

	// Check if data volumes need to be managed
	_, toAttach, toDetach, err := r.resolveAndCheckDataVolumes(ctx)

	needsVolumeUpdate := len(toAttach) > 0 || len(toDetach) > 0
	if err != nil {
		phaseLogger.Error(err, "failed to check data volume update status")
		return pm.NextToFailedOnApiError(ctx, err)
	}

	if needsVolumeUpdate {
		phaseLogger.Info("Data volumes need to be updated, transitioning to Updating phase")
		return pm.Next(
			ctx,
			v1alpha1.ArubaResourcePhaseUpdating,
			metav1.ConditionFalse,
			"UpdatingDataVolumes",
			"Data volumes need to be updated",
			true,
		)
	}

	// Check for other updates (generation mismatch)
	return pm.CheckForUpdates(ctx)
}

// checkDataVolumesNeedUpdate checks if data volumes need to be attached or detached
// Returns: needsUpdate (bool), desiredVolumeIDs ([]string), error
func (r *ArubaCloudServerReconciler) resolveAndCheckDataVolumes(ctx context.Context) ([]string, []string, []string, error) {
	// Resolve desired data volume IDs from spec
	desiredVolumeIDs := make([]string, 0, len(r.arubaObj.Spec.DataVolumeReferences))
	for _, volumeRef := range r.arubaObj.Spec.DataVolumeReferences {
		volumeID, err := r.getBlockStorageID(ctx, volumeRef.Name, volumeRef.Namespace)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to get data volume ID for %s/%s: %w", volumeRef.Namespace, volumeRef.Name, err)
		}
		desiredVolumeIDs = append(desiredVolumeIDs, volumeID)
	}

	// Calculate volumes to attach and detach
	toAttach, toDetach := util.CalculateVolumeChanges(desiredVolumeIDs, r.arubaObj.Status.DataVolumeIDs)

	return desiredVolumeIDs, toAttach, toDetach, nil
}

// Deleting handles the actual cloud server deletion
func (r *ArubaCloudServerReconciler) Deleting(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.HandleDeletion(ctx, cloudServerFinalizerName, func(ctx context.Context) error {
		return r.HelperClient.DeleteCloudServer(ctx, r.arubaObj.Status.ProjectID, r.arubaObj.Status.ResourceID)
	})
}

// Helper methods that build URIs using IDs
func (r *ArubaCloudServerReconciler) buildVpcURI(projectID, vpcID string) string {
	return fmt.Sprintf("/projects/%s/providers/Aruba.Network/vpcs/%s", projectID, vpcID)
}

func (r *ArubaCloudServerReconciler) buildKeyPairURI(projectID, keyPairID string) string {
	return fmt.Sprintf("/projects/%s/providers/Aruba.Compute/keyPairs/%s", projectID, keyPairID)
}

func (r *ArubaCloudServerReconciler) buildElasticIpURI(projectID, elasticIpID string) string {
	return fmt.Sprintf("/projects/%s/providers/Aruba.Network/elasticIps/%s", projectID, elasticIpID)
}

func (r *ArubaCloudServerReconciler) buildSubnetURI(projectID, vpcID, subnetID string) string {
	return fmt.Sprintf("/projects/%s/providers/Aruba.Network/vpcs/%s/subnets/%s", projectID, vpcID, subnetID)
}

func (r *ArubaCloudServerReconciler) buildSecurityGroupURI(projectID, vpcID, securityGroupID string) string {
	return fmt.Sprintf("/projects/%s/providers/Aruba.Network/vpcs/%s/securityGroups/%s", projectID, vpcID, securityGroupID)
}

func (r *ArubaCloudServerReconciler) buildVolumeURI(projectID, volumeID string) string {
	return fmt.Sprintf("/projects/%s/providers/Aruba.Storage/blockStorages/%s", projectID, volumeID)
}
