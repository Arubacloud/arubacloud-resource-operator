package controller

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
	arubaClient "github.com/Arubacloud/arubacloud-resource-operator/internal/client"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
)

// ElasticIpReconciler reconciles a ElasticIp object
type ElasticIpReconciler struct {
	*reconciler.Reconciler
}

// NewElasticIpReconciler creates a new ElasticIpReconciler
func NewElasticIpReconciler(reconciler *reconciler.Reconciler) *ElasticIpReconciler {
	return &ElasticIpReconciler{
		Reconciler: reconciler,
	}
}

// +kubebuilder:rbac:groups=arubacloud.com,resources=elasticips,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=arubacloud.com,resources=elasticips/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=arubacloud.com,resources=elasticips/finalizers,verbs=update
// +kubebuilder:rbac:groups=arubacloud.com,resources=projects,verbs=get;list;watch

func (r *ElasticIpReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := &v1alpha1.ElasticIp{}
	return r.Reconciler.Reconcile(ctx, req, obj, &obj.Status.ResourceStatus, r, &obj.Spec.Tenant)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ElasticIpReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.ElasticIp{}).
		Named("elasticip").
		Complete(r)
}

const (
	elasticIpFinalizerName = "elasticip.arubacloud.com/finalizer"
)

func (r *ElasticIpReconciler) Init(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	return r.InitializeResource(ctx, obj, status, elasticIpFinalizerName)
}

func (r *ElasticIpReconciler) Creating(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	elasticIp := obj.(*v1alpha1.ElasticIp)
	return r.HandleCreating(ctx, obj, status, func(ctx context.Context) (string, string, error) {
		projectID, err := r.GetProjectID(
			ctx,
			elasticIp.Spec.ProjectReference.Name,
			elasticIp.Spec.ProjectReference.Namespace,
		)
		if err != nil {
			return "", "", err
		}

		elasticIpReq := arubaClient.ElasticIpRequest{
			Metadata: arubaClient.ElasticIpMetadata{
				Name: elasticIp.Name,
				Tags: elasticIp.Spec.Tags,
				Location: arubaClient.ElasticIpLocation{
					Value: elasticIp.Spec.Location.Value,
				},
			},
			Properties: arubaClient.ElasticIpProperties{
				BillingPlan: arubaClient.ElasticIpBillingPlan{
					BillingPeriod: elasticIp.Spec.BillingPlan.BillingPeriod,
				},
			},
		}

		elasticIpResp, err := r.CreateElasticIp(ctx, projectID, elasticIpReq)
		if err != nil {
			return "", "", err
		}

		elasticIp.Status.ProjectID = projectID

		state := ""
		if elasticIpResp.Status != nil {
			state = elasticIpResp.Status.State
		}

		return elasticIpResp.Metadata.ID, state, nil
	})
}

func (r *ElasticIpReconciler) Provisioning(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	elasticIp := obj.(*v1alpha1.ElasticIp)
	return r.HandleProvisioning(ctx, obj, status, func(ctx context.Context) (string, error) {
		elasticIpResp, err := r.GetElasticIp(ctx, elasticIp.Status.ProjectID, status.ResourceID)
		if err != nil {
			return "", err
		}

		if elasticIpResp.Status != nil {
			return elasticIpResp.Status.State, nil
		}
		return "", nil
	})
}

func (r *ElasticIpReconciler) Updating(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	elasticIp := obj.(*v1alpha1.ElasticIp)
	return r.HandleUpdating(ctx, obj, status, func(ctx context.Context) error {
		elasticIpReq := arubaClient.ElasticIpRequest{
			Metadata: arubaClient.ElasticIpMetadata{
				Name: elasticIp.Name,
				Tags: elasticIp.Spec.Tags,
				Location: arubaClient.ElasticIpLocation{
					Value: elasticIp.Spec.Location.Value,
				},
			},
			Properties: arubaClient.ElasticIpProperties{
				BillingPlan: arubaClient.ElasticIpBillingPlan{
					BillingPeriod: elasticIp.Spec.BillingPlan.BillingPeriod,
				},
			},
		}

		_, err := r.UpdateElasticIp(ctx, elasticIp.Status.ProjectID, status.ResourceID, elasticIpReq)
		return err
	})
}

func (r *ElasticIpReconciler) Created(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	return r.CheckForUpdates(ctx, obj, status)
}

func (r *ElasticIpReconciler) Deleting(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	elasticIp := obj.(*v1alpha1.ElasticIp)
	return r.HandleDeletion(ctx, obj, status, elasticIpFinalizerName, func(ctx context.Context) error {
		return r.DeleteElasticIp(ctx, elasticIp.Status.ProjectID, status.ResourceID)
	})
}
