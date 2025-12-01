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

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/client"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/util"
)

// +kubebuilder:rbac:groups=arubacloud.com,resources=cloudservers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=arubacloud.com,resources=cloudservers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=arubacloud.com,resources=cloudservers/finalizers,verbs=update
// +kubebuilder:rbac:groups=arubacloud.com,resources=projects,verbs=get;list;watch
// +kubebuilder:rbac:groups=arubacloud.com,resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=arubacloud.com,resources=configmaps,verbs=get;list;watch

// BlockStorageReconciler reconciles a BlockStorage object
type CloudServerReconciler struct {
	*reconciler.Reconciler
	Object *v1alpha1.CloudServer
}

// NewCloudServerReconciler creates a new CloudServerReconciler
func NewCloudServerReconciler(reconciler *reconciler.Reconciler) *CloudServerReconciler {
	return &CloudServerReconciler{
		Reconciler: reconciler,
	}
}

func (r *CloudServerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.Object = &v1alpha1.CloudServer{}
	r.Reconciler.Object = r.Object
	r.ResourceStatus = &r.Object.Status.ResourceStatus
	r.ResourceReconciler = r
	return r.Reconciler.Reconcile(ctx, req, &r.Object.Spec.Tenant)
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

func (r *CloudServerReconciler) Init(ctx context.Context) (ctrl.Result, error) {
	return r.InitializeResource(ctx, cloudServerFinalizerName)
}

func (r *CloudServerReconciler) Creating(ctx context.Context) (ctrl.Result, error) {
	return r.HandleCreating(ctx, func(ctx context.Context) (string, string, error) {
		projectID, err := r.GetProjectID(ctx, r.Object.Spec.ProjectReference.Name, r.Object.Spec.ProjectReference.Namespace)
		if err != nil {
			return "", "", err
		}

		vpcID, err := r.GetVpcID(ctx, r.Object.Spec.VpcReference.Name, r.Object.Spec.VpcReference.Namespace)
		if err != nil {
			return "", "", err
		}

		bootVolumeID, err := r.GetBlockStorageID(ctx, r.Object.Spec.BootVolumeReference.Name, r.Object.Spec.BootVolumeReference.Namespace)
		if err != nil {
			return "", "", err
		}

		keyPairID, err := r.GetKeyPairID(ctx, r.Object.Spec.KeyPairReference.Name, r.Object.Spec.KeyPairReference.Namespace)
		if err != nil {
			return "", "", err
		}

		// Resolve subnet IDs
		subnetIDs := make([]string, len(r.Object.Spec.SubnetReferences))
		for i, subnetRef := range r.Object.Spec.SubnetReferences {
			subnetID, err := r.GetSubnetID(ctx, subnetRef.Name, subnetRef.Namespace)
			if err != nil {
				return "", "", fmt.Errorf("failed to get subnet ID for %s/%s: %w", subnetRef.Namespace, subnetRef.Name, err)
			}
			subnetIDs[i] = subnetID
		}

		// Resolve security group IDs
		securityGroupIDs := make([]string, len(r.Object.Spec.SecurityGroupReferences))
		for i, sgRef := range r.Object.Spec.SecurityGroupReferences {
			sgID, err := r.GetSecurityGroupID(ctx, sgRef.Name, sgRef.Namespace)
			if err != nil {
				return "", "", fmt.Errorf("failed to get security group ID for %s/%s: %w", sgRef.Namespace, sgRef.Name, err)
			}
			securityGroupIDs[i] = sgID
		}

		// Create cloud server via API
		cloudServerReq := client.CloudServerRequest{
			Metadata: client.CloudServerMetadata{
				Name: r.Object.Name,
				Tags: r.Object.Spec.Tags,
				Location: client.CloudServerLocation{
					Value: r.Object.Spec.Location.Value,
				},
			},
			Properties: client.CloudServerProperties{
				DataCenter: r.Object.Spec.DataCenter,
				VPC:        client.CloudServerResourceReference{URI: r.buildVpcURI(projectID, vpcID)},
				BootVolume: client.CloudServerResourceReference{URI: r.buildVolumeURI(projectID, bootVolumeID)},
				VpcPreset:  r.Object.Spec.VpcPreset,
				FlavorName: r.Object.Spec.FlavorName,
				KeyPair:    client.CloudServerResourceReference{URI: r.buildKeyPairURI(projectID, keyPairID)},
			},
		}

		// Add optional elastic IP
		var elasticIpID string
		if r.Object.Spec.ElasticIpReference != nil {
			elasticIpID, err = r.GetElasticIpID(ctx, r.Object.Spec.ElasticIpReference.Name, r.Object.Spec.ElasticIpReference.Namespace)
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

		cloudServerResp, err := r.CreateCloudServer(ctx, projectID, cloudServerReq)
		if err != nil {
			return "", "", err
		}

		// Update status with cloud server ID and all resolved IDs
		r.Object.Status.ProjectID = projectID
		r.Object.Status.VpcID = vpcID
		r.Object.Status.SubnetIDs = subnetIDs
		r.Object.Status.SecurityGroupIDs = securityGroupIDs
		r.Object.Status.BootVolumeID = bootVolumeID
		if elasticIpID != "" {
			r.Object.Status.ElasticIpID = elasticIpID
		}
		r.Object.Status.KeyPairID = keyPairID

		state := ""
		if cloudServerResp.Status != nil {
			state = cloudServerResp.Status.State
		}

		return cloudServerResp.Metadata.ID, state, nil
	})
}

// Provisioning handles checking remote state during provisioning
func (r *CloudServerReconciler) Provisioning(ctx context.Context) (ctrl.Result, error) {
	return r.HandleProvisioning(ctx, func(ctx context.Context) (string, error) {
		cloudServerResp, err := r.GetCloudServer(ctx, r.Object.Status.ProjectID, r.Object.Status.ResourceID)
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
func (r *CloudServerReconciler) Updating(ctx context.Context) (ctrl.Result, error) {
	return r.HandleUpdating(ctx, func(ctx context.Context) error {
		// Re-resolve all IDs in case references changed
		projectID := r.Object.Status.ProjectID
		vpcID := r.Object.Status.VpcID

		// Check if we need to update cloud server properties (generation mismatch)
		needsPropertyUpdate := r.ObservedGeneration != r.Object.GetGeneration()

		if needsPropertyUpdate {
			// Resolve subnet IDs
			subnetIDs := make([]string, len(r.Object.Spec.SubnetReferences))
			for i, subnetRef := range r.Object.Spec.SubnetReferences {
				subnetID, err := r.GetSubnetID(ctx, subnetRef.Name, subnetRef.Namespace)
				if err != nil {
					return fmt.Errorf("failed to get subnet ID for %s/%s: %w", subnetRef.Namespace, subnetRef.Name, err)
				}
				subnetIDs[i] = subnetID
			}

			// Resolve security group IDs
			securityGroupIDs := make([]string, len(r.Object.Spec.SecurityGroupReferences))
			for i, sgRef := range r.Object.Spec.SecurityGroupReferences {
				sgID, err := r.GetSecurityGroupID(ctx, sgRef.Name, sgRef.Namespace)
				if err != nil {
					return fmt.Errorf("failed to get security group ID for %s/%s: %w", sgRef.Namespace, sgRef.Name, err)
				}
				securityGroupIDs[i] = sgID
			}

			// Update cloud server via API
			cloudServerReq := client.CloudServerRequest{
				Metadata: client.CloudServerMetadata{
					Name: r.Object.Name,
					Tags: r.Object.Spec.Tags,
					Location: client.CloudServerLocation{
						Value: r.Object.Spec.Location.Value,
					},
				},
			}

			// Add optional fields
			var elasticIpID string
			if r.Object.Spec.ElasticIpReference != nil {
				elasticIpID, err := r.GetElasticIpID(ctx, r.Object.Spec.ElasticIpReference.Name, r.Object.Spec.ElasticIpReference.Namespace)
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

			_, err := r.UpdateCloudServer(ctx, r.Object.Status.ProjectID, r.Object.Status.ResourceID, cloudServerReq)
			if err != nil {
				return err
			}

			// Update status with new resolved IDs
			r.Object.Status.SubnetIDs = subnetIDs
			r.Object.Status.SecurityGroupIDs = securityGroupIDs
			if elasticIpID != "" {
				r.Object.Status.ElasticIpID = elasticIpID
			} else {
				r.Object.Status.ElasticIpID = ""
			}
		}

		// Now handle data volume management
		return r.manageDataVolumesInUpdate(ctx, projectID)
	})
}

// manageDataVolumesInUpdate handles attaching and detaching data volumes during update phase
func (r *CloudServerReconciler) manageDataVolumesInUpdate(ctx context.Context, projectID string) error {
	phaseLogger := ctrl.Log.WithValues("Phase", "Updating", "Kind", r.Object.GetObjectKind().GroupVersionKind().Kind, "Name", r.Object.GetName())

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
	_, err = r.AttachDetachDataVolumes(ctx, projectID, r.Object.Status.ResourceID, req)
	if err != nil {
		return err
	}

	// Update status with new data volume IDs
	r.Object.Status.DataVolumeIDs = desiredVolumeIDs

	phaseLogger.Info("Data volumes managed successfully")
	return nil
}

// Created handles the steady state
func (r *CloudServerReconciler) Created(ctx context.Context) (ctrl.Result, error) {
	phaseLogger := ctrl.Log.WithValues("Phase", r.Phase, "Kind", r.Object.GetObjectKind().GroupVersionKind().Kind, "Name", r.Object.GetName())

	// Check if data volumes need to be managed
	_, toAttach, toDetach, err := r.resolveAndCheckDataVolumes(ctx)

	needsVolumeUpdate := len(toAttach) > 0 || len(toDetach) > 0
	if err != nil {
		phaseLogger.Error(err, "failed to check data volume update status")
		return r.NextToFailedOnApiError(ctx, err)
	}

	if needsVolumeUpdate {
		phaseLogger.Info("Data volumes need to be updated, transitioning to Updating phase")
		return r.Next(
			ctx,
			v1alpha1.ResourcePhaseUpdating,
			metav1.ConditionFalse,
			"UpdatingDataVolumes",
			"Data volumes need to be updated",
			true,
		)
	}

	// Check for other updates (generation mismatch)
	return r.CheckForUpdates(ctx)
}

// checkDataVolumesNeedUpdate checks if data volumes need to be attached or detached
// Returns: needsUpdate (bool), desiredVolumeIDs ([]string), error
func (r *CloudServerReconciler) resolveAndCheckDataVolumes(ctx context.Context) ([]string, []string, []string, error) {
	// Resolve desired data volume IDs from spec
	desiredVolumeIDs := make([]string, 0, len(r.Object.Spec.DataVolumeReferences))
	for _, volumeRef := range r.Object.Spec.DataVolumeReferences {
		volumeID, err := r.GetBlockStorageID(ctx, volumeRef.Name, volumeRef.Namespace)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to get data volume ID for %s/%s: %w", volumeRef.Namespace, volumeRef.Name, err)
		}
		desiredVolumeIDs = append(desiredVolumeIDs, volumeID)
	}

	// Calculate volumes to attach and detach
	toAttach, toDetach := util.CalculateVolumeChanges(desiredVolumeIDs, r.Object.Status.DataVolumeIDs)

	return desiredVolumeIDs, toAttach, toDetach, nil
}

// Deleting handles the actual cloud server deletion
func (r *CloudServerReconciler) Deleting(ctx context.Context) (ctrl.Result, error) {
	return r.HandleDeletion(ctx, cloudServerFinalizerName, func(ctx context.Context) error {
		return r.DeleteCloudServer(ctx, r.Object.Status.ProjectID, r.Object.Status.ResourceID)
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
