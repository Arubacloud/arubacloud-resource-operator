package controller

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/client"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
)

// ElasticIpReconciler reconciles a ElasticIp object
type ElasticIpReconciler struct {
	*reconciler.Reconciler
	Object *v1alpha1.ElasticIp
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
	r.Object = &v1alpha1.ElasticIp{}
	r.Reconciler.Object = r.Object
	r.ResourceStatus = &r.Object.Status.ResourceStatus
	r.ResourceReconciler = r
	return r.Reconciler.Reconcile(ctx, req, &r.Object.Spec.Tenant)
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

func (r *ElasticIpReconciler) Init(ctx context.Context) (ctrl.Result, error) {
	return r.InitializeResource(ctx, elasticIpFinalizerName)
}

func (r *ElasticIpReconciler) Creating(ctx context.Context) (ctrl.Result, error) {
	return r.HandleCreating(ctx, func(ctx context.Context) (string, string, error) {
		projectID, err := r.GetProjectID(
			ctx,
			r.Object.Spec.ProjectReference.Name,
			r.Object.Spec.ProjectReference.Namespace,
		)
		if err != nil {
			return "", "", err
		}

		elasticIpReq := client.ElasticIpRequest{
			Metadata: client.ElasticIpMetadata{
				Name: r.Object.Name,
				Tags: r.Object.Spec.Tags,
				Location: client.ElasticIpLocation{
					Value: r.Object.Spec.Location.Value,
				},
			},
			Properties: client.ElasticIpProperties{
				BillingPlan: client.ElasticIpBillingPlan{
					BillingPeriod: r.Object.Spec.BillingPlan.BillingPeriod,
				},
			},
		}

		elasticIpResp, err := r.CreateElasticIp(ctx, projectID, elasticIpReq)
		if err != nil {
			return "", "", err
		}

		r.Object.Status.ProjectID = projectID

		state := ""
		if elasticIpResp.Status != nil {
			state = elasticIpResp.Status.State
		}

		return elasticIpResp.Metadata.ID, state, nil
	})
}

func (r *ElasticIpReconciler) Provisioning(ctx context.Context) (ctrl.Result, error) {
	return r.HandleProvisioning(ctx, func(ctx context.Context) (string, error) {
		elasticIpResp, err := r.GetElasticIp(ctx, r.Object.Status.ProjectID, r.Object.Status.ResourceID)
		if err != nil {
			return "", err
		}

		if elasticIpResp.Status != nil {
			return elasticIpResp.Status.State, nil
		}
		return "", nil
	})
}

func (r *ElasticIpReconciler) Updating(ctx context.Context) (ctrl.Result, error) {
	return r.HandleUpdating(ctx, func(ctx context.Context) error {
		elasticIpReq := client.ElasticIpRequest{
			Metadata: client.ElasticIpMetadata{
				Name: r.Object.Name,
				Tags: r.Object.Spec.Tags,
				Location: client.ElasticIpLocation{
					Value: r.Object.Spec.Location.Value,
				},
			},
			Properties: client.ElasticIpProperties{
				BillingPlan: client.ElasticIpBillingPlan{
					BillingPeriod: r.Object.Spec.BillingPlan.BillingPeriod,
				},
			},
		}

		_, err := r.UpdateElasticIp(ctx, r.Object.Status.ProjectID, r.Object.Status.ResourceID, elasticIpReq)
		return err
	})
}

func (r *ElasticIpReconciler) Created(ctx context.Context) (ctrl.Result, error) {
	return r.CheckForUpdates(ctx)
}

func (r *ElasticIpReconciler) Deleting(ctx context.Context) (ctrl.Result, error) {
	return r.HandleDeletion(ctx, elasticIpFinalizerName, func(ctx context.Context) error {
		return r.DeleteElasticIp(ctx, r.Object.Status.ProjectID, r.Object.Status.ResourceID)
	})
}
