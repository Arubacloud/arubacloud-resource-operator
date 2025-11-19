package controller

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/Arubacloud/arubacloud-resource-operator/internal/client"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/util"
)

const (
	elasticIpFinalizerName = "arubanetworkelasticip.cloud.aruba.it/finalizer"
)

func (r *ArubaNetworkElasticIpReconciler) Init(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.InitializeResource(ctx, elasticIpFinalizerName)
}

func (r *ArubaNetworkElasticIpReconciler) Creating(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.HandleCreating(ctx, func(ctx context.Context) (string, string, error) {
		projectID, err := r.getProjectID(
			ctx,
			r.arubaObj.Spec.ProjectReference.Name,
			r.arubaObj.Spec.ProjectReference.Namespace,
		)
		if err != nil {
			return "", "", err
		}

		elasticIpReq := client.ElasticIpRequest{
			Metadata: client.ElasticIpMetadata{
				Name: r.arubaObj.Name,
				Tags: r.arubaObj.Spec.Tags,
				Location: client.ElasticIpLocation{
					Value: r.arubaObj.Spec.Location.Value,
				},
			},
			Properties: client.ElasticIpProperties{
				BillingPlan: client.ElasticIpBillingPlan{
					BillingPeriod: r.arubaObj.Spec.BillingPlan.BillingPeriod,
				},
			},
		}

		elasticIpResp, err := r.HelperClient.CreateElasticIp(ctx, projectID, elasticIpReq)
		if err != nil {
			return "", "", err
		}

		r.arubaObj.Status.ProjectID = projectID

		state := ""
		if elasticIpResp.Status != nil {
			state = elasticIpResp.Status.State
		}

		return elasticIpResp.Metadata.ID, state, nil
	})
}

func (r *ArubaNetworkElasticIpReconciler) Provisioning(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.HandleProvisioning(ctx, func(ctx context.Context) (string, error) {
		elasticIpResp, err := r.HelperClient.GetElasticIp(ctx, r.arubaObj.Status.ProjectID, r.arubaObj.Status.ResourceID)
		if err != nil {
			return "", err
		}

		if elasticIpResp.Status != nil {
			return elasticIpResp.Status.State, nil
		}
		return "", nil
	})
}

func (r *ArubaNetworkElasticIpReconciler) Updating(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.HandleUpdating(ctx, func(ctx context.Context) error {
		elasticIpReq := client.ElasticIpRequest{
			Metadata: client.ElasticIpMetadata{
				Name: r.arubaObj.Name,
				Tags: r.arubaObj.Spec.Tags,
				Location: client.ElasticIpLocation{
					Value: r.arubaObj.Spec.Location.Value,
				},
			},
			Properties: client.ElasticIpProperties{
				BillingPlan: client.ElasticIpBillingPlan{
					BillingPeriod: r.arubaObj.Spec.BillingPlan.BillingPeriod,
				},
			},
		}

		_, err := r.HelperClient.UpdateElasticIp(ctx, r.arubaObj.Status.ProjectID, r.arubaObj.Status.ResourceID, elasticIpReq)
		return err
	})
}

func (r *ArubaNetworkElasticIpReconciler) Created(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.CheckForUpdates(ctx)
}

func (r *ArubaNetworkElasticIpReconciler) Deleting(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return pm.HandleDeletion(ctx, elasticIpFinalizerName, func(ctx context.Context) error {
		return r.HelperClient.DeleteElasticIp(ctx, r.arubaObj.Status.ProjectID, r.arubaObj.Status.ResourceID)
	})
}
