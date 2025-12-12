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
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
	arubaClient "github.com/Arubacloud/arubacloud-resource-operator/internal/client"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/util"
)

// +kubebuilder:rbac:groups=arubacloud.com,resources=cloudservers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=arubacloud.com,resources=cloudservers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=arubacloud.com,resources=cloudservers/finalizers,verbs=update
// +kubebuilder:rbac:groups=arubacloud.com,resources=projects,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

// CloudServerReconciler reconciles a CloudServer object
type CloudServerReconciler struct {
	*reconciler.Reconciler
}

// NewCloudServerReconciler creates a new CloudServerReconciler
func NewCloudServerReconciler(reconciler *reconciler.Reconciler) *CloudServerReconciler {
	return &CloudServerReconciler{
		Reconciler: reconciler,
	}
}

func (r *CloudServerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := &v1alpha1.CloudServer{}
	return r.Reconciler.Reconcile(ctx, req, obj, &obj.Status.ResourceStatus, r, &obj.Spec.Tenant)
}

// SetupWithManager sets up the controller with the Manager.
func (r *CloudServerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.CloudServer{}).
		Named("cloudserver").
		Complete(r)
}

const (
	cloudServerFinalizerName = "cloudserver.arubacloud.com/finalizer"
)

func (r *CloudServerReconciler) Init(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	return r.InitializeResource(ctx, obj, status, cloudServerFinalizerName)
}

func (r *CloudServerReconciler) Creating(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	cloudServer := obj.(*v1alpha1.CloudServer)
	return r.HandleCreating(ctx, obj, status, func(ctx context.Context) (string, string, error) {
		projectID, err := r.GetProjectID(ctx, cloudServer.Spec.ProjectReference.Name, cloudServer.Spec.ProjectReference.Namespace)
		if err != nil {
			return "", "", err
		}

		vpcID, err := r.GetVpcID(ctx, cloudServer.Spec.VpcReference.Name, cloudServer.Spec.VpcReference.Namespace)
		if err != nil {
			return "", "", err
		}

		bootVolumeID, err := r.GetBlockStorageID(ctx, cloudServer.Spec.BootVolumeReference.Name, cloudServer.Spec.BootVolumeReference.Namespace)
		if err != nil {
			return "", "", err
		}

		keyPairID, err := r.GetKeyPairID(ctx, cloudServer.Spec.KeyPairReference.Name, cloudServer.Spec.KeyPairReference.Namespace)
		if err != nil {
			return "", "", err
		}

		// Resolve subnet IDs
		subnetIDs := make([]string, len(cloudServer.Spec.SubnetReferences))
		for i, subnetRef := range cloudServer.Spec.SubnetReferences {
			subnetID, err := r.GetSubnetID(ctx, subnetRef.Name, subnetRef.Namespace)
			if err != nil {
				return "", "", fmt.Errorf("failed to get subnet ID for %s/%s: %w", subnetRef.Namespace, subnetRef.Name, err)
			}
			subnetIDs[i] = subnetID
		}

		// Resolve security group IDs
		securityGroupIDs := make([]string, len(cloudServer.Spec.SecurityGroupReferences))
		for i, sgRef := range cloudServer.Spec.SecurityGroupReferences {
			sgID, err := r.GetSecurityGroupID(ctx, sgRef.Name, sgRef.Namespace)
			if err != nil {
				return "", "", fmt.Errorf("failed to get security group ID for %s/%s: %w", sgRef.Namespace, sgRef.Name, err)
			}
			securityGroupIDs[i] = sgID
		}

		// Create cloud server via API
		cloudServerReq := arubaClient.CloudServerRequest{
			Metadata: arubaClient.CloudServerMetadata{
				Name: cloudServer.Name,
				Tags: cloudServer.Spec.Tags,
				Location: arubaClient.CloudServerLocation{
					Value: cloudServer.Spec.Location.Value,
				},
			},
			Properties: arubaClient.CloudServerProperties{
				DataCenter: cloudServer.Spec.DataCenter,
				VPC:        arubaClient.CloudServerResourceReference{URI: r.buildVpcURI(projectID, vpcID)},
				BootVolume: arubaClient.CloudServerResourceReference{URI: r.buildVolumeURI(projectID, bootVolumeID)},
				VpcPreset:  cloudServer.Spec.VpcPreset,
				FlavorName: cloudServer.Spec.FlavorName,
				KeyPair:    arubaClient.CloudServerResourceReference{URI: r.buildKeyPairURI(projectID, keyPairID)},
			},
		}

		// Add optional elastic IP
		var elasticIpID string
		if cloudServer.Spec.ElasticIpReference != nil {
			elasticIpID, err = r.GetElasticIpID(ctx, cloudServer.Spec.ElasticIpReference.Name, cloudServer.Spec.ElasticIpReference.Namespace)
			if err != nil {
				return "", "", fmt.Errorf("failed to get elastic IP ID: %w", err)
			}
			cloudServerReq.Properties.ElasticIp = &arubaClient.CloudServerResourceReference{URI: r.buildElasticIpURI(projectID, elasticIpID)}
		}

		// Add subnets
		for _, subnetID := range subnetIDs {
			cloudServerReq.Properties.Subnets = append(cloudServerReq.Properties.Subnets,
				arubaClient.CloudServerResourceReference{URI: r.buildSubnetURI(projectID, vpcID, subnetID)})
		}

		// Add security groups
		for _, sgID := range securityGroupIDs {
			cloudServerReq.Properties.SecurityGroups = append(cloudServerReq.Properties.SecurityGroups,
				arubaClient.CloudServerResourceReference{URI: r.buildSecurityGroupURI(projectID, vpcID, sgID)})
		}

		cloudServerResp, err := r.CreateCloudServer(ctx, projectID, cloudServerReq)
		if err != nil {
			return "", "", err
		}

		// Update status with cloud server ID and all resolved IDs
		cloudServer.Status.ProjectID = projectID
		cloudServer.Status.VpcID = vpcID
		cloudServer.Status.SubnetIDs = subnetIDs
		cloudServer.Status.SecurityGroupIDs = securityGroupIDs
		cloudServer.Status.BootVolumeID = bootVolumeID
		if elasticIpID != "" {
			cloudServer.Status.ElasticIpID = elasticIpID
		}
		cloudServer.Status.KeyPairID = keyPairID

		state := ""
		if cloudServerResp.Status != nil {
			state = cloudServerResp.Status.State
		}

		return cloudServerResp.Metadata.ID, state, nil
	})
}

// Provisioning handles checking remote state during provisioning
func (r *CloudServerReconciler) Provisioning(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	cloudServer := obj.(*v1alpha1.CloudServer)
	return r.HandleProvisioning(ctx, obj, status, func(ctx context.Context) (string, error) {
		cloudServerResp, err := r.GetCloudServer(ctx, cloudServer.Status.ProjectID, status.ResourceID)
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
func (r *CloudServerReconciler) Updating(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	cloudServer := obj.(*v1alpha1.CloudServer)
	return r.HandleUpdating(ctx, obj, status, func(ctx context.Context) error {
		// Re-resolve all IDs in case references changed
		projectID := cloudServer.Status.ProjectID
		vpcID := cloudServer.Status.VpcID

		// Check if we need to update cloud server properties (generation mismatch)
		needsPropertyUpdate := status.ObservedGeneration != cloudServer.GetGeneration()

		if needsPropertyUpdate {
			// Resolve subnet IDs
			subnetIDs := make([]string, len(cloudServer.Spec.SubnetReferences))
			for i, subnetRef := range cloudServer.Spec.SubnetReferences {
				subnetID, err := r.GetSubnetID(ctx, subnetRef.Name, subnetRef.Namespace)
				if err != nil {
					return fmt.Errorf("failed to get subnet ID for %s/%s: %w", subnetRef.Namespace, subnetRef.Name, err)
				}
				subnetIDs[i] = subnetID
			}

			// Resolve security group IDs
			securityGroupIDs := make([]string, len(cloudServer.Spec.SecurityGroupReferences))
			for i, sgRef := range cloudServer.Spec.SecurityGroupReferences {
				sgID, err := r.GetSecurityGroupID(ctx, sgRef.Name, sgRef.Namespace)
				if err != nil {
					return fmt.Errorf("failed to get security group ID for %s/%s: %w", sgRef.Namespace, sgRef.Name, err)
				}
				securityGroupIDs[i] = sgID
			}

			// Update cloud server via API
			cloudServerReq := arubaClient.CloudServerRequest{
				Metadata: arubaClient.CloudServerMetadata{
					Name: cloudServer.Name,
					Tags: cloudServer.Spec.Tags,
					Location: arubaClient.CloudServerLocation{
						Value: cloudServer.Spec.Location.Value,
					},
				},
			}

			// Add optional fields
			var elasticIpID string
			if cloudServer.Spec.ElasticIpReference != nil {
				elasticIpID, err := r.GetElasticIpID(ctx, cloudServer.Spec.ElasticIpReference.Name, cloudServer.Spec.ElasticIpReference.Namespace)
				if err != nil {
					return fmt.Errorf("failed to get elastic IP ID: %w", err)
				}
				cloudServerReq.Properties.ElasticIp = &arubaClient.CloudServerResourceReference{URI: r.buildElasticIpURI(projectID, elasticIpID)}
			}

			// Add subnets and security groups
			for _, subnetID := range subnetIDs {
				cloudServerReq.Properties.Subnets = append(cloudServerReq.Properties.Subnets,
					arubaClient.CloudServerResourceReference{URI: r.buildSubnetURI(projectID, vpcID, subnetID)})
			}
			for _, sgID := range securityGroupIDs {
				cloudServerReq.Properties.SecurityGroups = append(cloudServerReq.Properties.SecurityGroups,
					arubaClient.CloudServerResourceReference{URI: r.buildSecurityGroupURI(projectID, vpcID, sgID)})
			}

			_, err := r.UpdateCloudServer(ctx, cloudServer.Status.ProjectID, status.ResourceID, cloudServerReq)
			if err != nil {
				return err
			}

			// Update status with new resolved IDs
			cloudServer.Status.SubnetIDs = subnetIDs
			cloudServer.Status.SecurityGroupIDs = securityGroupIDs
			if elasticIpID != "" {
				cloudServer.Status.ElasticIpID = elasticIpID
			} else {
				cloudServer.Status.ElasticIpID = ""
			}
		}

		// Now handle data volume management
		return r.manageDataVolumesInUpdate(ctx, cloudServer, projectID)
	})
}

// manageDataVolumesInUpdate handles attaching and detaching data volumes during update phase
func (r *CloudServerReconciler) manageDataVolumesInUpdate(ctx context.Context, cloudServer *v1alpha1.CloudServer, projectID string) error {
	phaseLogger := ctrl.Log.WithValues("Phase", "Updating", "Kind", cloudServer.GetObjectKind().GroupVersionKind().Kind, "Name", cloudServer.GetName())

	// Resolve and calculate volume changes
	desiredVolumeIDs, toAttach, toDetach, err := r.resolveAndCheckDataVolumes(ctx, cloudServer)
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
	req := arubaClient.AttachDetachDataVolumesRequest{
		VolumesToAttach: make([]arubaClient.CloudServerResourceReference, 0, len(toAttach)),
		VolumesToDetach: make([]arubaClient.CloudServerResourceReference, 0, len(toDetach)),
	}

	for _, volumeID := range toAttach {
		req.VolumesToAttach = append(req.VolumesToAttach, arubaClient.CloudServerResourceReference{
			URI: r.buildVolumeURI(projectID, volumeID),
		})
	}

	for _, volumeID := range toDetach {
		req.VolumesToDetach = append(req.VolumesToDetach, arubaClient.CloudServerResourceReference{
			URI: r.buildVolumeURI(projectID, volumeID),
		})
	}

	// Call API to attach/detach volumes
	_, err = r.AttachDetachDataVolumes(ctx, projectID, cloudServer.Status.ResourceID, req)
	if err != nil {
		return err
	}

	// Update status with new data volume IDs
	cloudServer.Status.DataVolumeIDs = desiredVolumeIDs

	phaseLogger.Info("Data volumes managed successfully")
	return nil
}

// Created handles the steady state
func (r *CloudServerReconciler) Created(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	cloudServer := obj.(*v1alpha1.CloudServer)
	phaseLogger := ctrl.Log.WithValues("Phase", status.Phase, "Kind", cloudServer.GetObjectKind().GroupVersionKind().Kind, "Name", cloudServer.GetName())

	// Check if data volumes need to be managed
	_, toAttach, toDetach, err := r.resolveAndCheckDataVolumes(ctx, cloudServer)

	needsVolumeUpdate := len(toAttach) > 0 || len(toDetach) > 0
	if err != nil {
		phaseLogger.Error(err, "failed to check data volume update status")
		return r.NextToFailedOnApiError(ctx, obj, status, err)
	}

	if needsVolumeUpdate {
		phaseLogger.Info("Data volumes need to be updated, transitioning to Updating phase")
		return r.Next(
			ctx,
			obj,
			status,
			v1alpha1.ResourcePhaseUpdating,
			metav1.ConditionFalse,
			"UpdatingDataVolumes",
			"Data volumes need to be updated",
			true,
		)
	}

	// Check for other updates (generation mismatch)
	return r.CheckForUpdates(ctx, obj, status)
}

// checkDataVolumesNeedUpdate checks if data volumes need to be attached or detached
// Returns: needsUpdate (bool), desiredVolumeIDs ([]string), error
func (r *CloudServerReconciler) resolveAndCheckDataVolumes(ctx context.Context, cloudServer *v1alpha1.CloudServer) ([]string, []string, []string, error) {
	// Resolve desired data volume IDs from spec
	desiredVolumeIDs := make([]string, 0, len(cloudServer.Spec.DataVolumeReferences))
	for _, volumeRef := range cloudServer.Spec.DataVolumeReferences {
		volumeID, err := r.GetBlockStorageID(ctx, volumeRef.Name, volumeRef.Namespace)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to get data volume ID for %s/%s: %w", volumeRef.Namespace, volumeRef.Name, err)
		}
		desiredVolumeIDs = append(desiredVolumeIDs, volumeID)
	}

	// Calculate volumes to attach and detach
	toAttach, toDetach := util.CalculateVolumeChanges(desiredVolumeIDs, cloudServer.Status.DataVolumeIDs)

	return desiredVolumeIDs, toAttach, toDetach, nil
}

// Deleting handles the actual cloud server deletion
func (r *CloudServerReconciler) Deleting(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	cloudServer := obj.(*v1alpha1.CloudServer)
	return r.HandleDeletion(ctx, obj, status, cloudServerFinalizerName, func(ctx context.Context) error {
		return r.DeleteCloudServer(ctx, cloudServer.Status.ProjectID, status.ResourceID)
	})
}

// Helper methods that build URIs using IDs
func (r *CloudServerReconciler) buildVpcURI(projectID, vpcID string) string {
	return fmt.Sprintf("/projects/%s/providers/Aruba.Network/vpcs/%s", projectID, vpcID)
}

func (r *CloudServerReconciler) buildKeyPairURI(projectID, keyPairID string) string {
	return fmt.Sprintf("/projects/%s/providers/Aruba.Compute/keyPairs/%s", projectID, keyPairID)
}

func (r *CloudServerReconciler) buildElasticIpURI(projectID, elasticIpID string) string {
	return fmt.Sprintf("/projects/%s/providers/Aruba.Network/elasticIps/%s", projectID, elasticIpID)
}

func (r *CloudServerReconciler) buildSubnetURI(projectID, vpcID, subnetID string) string {
	return fmt.Sprintf("/projects/%s/providers/Aruba.Network/vpcs/%s/subnets/%s", projectID, vpcID, subnetID)
}

func (r *CloudServerReconciler) buildSecurityGroupURI(projectID, vpcID, securityGroupID string) string {
	return fmt.Sprintf("/projects/%s/providers/Aruba.Network/vpcs/%s/securityGroups/%s", projectID, vpcID, securityGroupID)
}

func (r *CloudServerReconciler) buildVolumeURI(projectID, volumeID string) string {
	return fmt.Sprintf("/projects/%s/providers/Aruba.Storage/blockStorages/%s", projectID, volumeID)
}
