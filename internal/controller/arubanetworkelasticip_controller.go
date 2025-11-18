package controller

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	"gitlab.aruba.it/ingegneria/seca/operators/aruba-operator/api/v1alpha1"
)

// ArubaNetworkElasticIpReconciler reconciles a ArubaNetworkElasticIp object
type ArubaNetworkElasticIpReconciler struct {
	*HelperReconciler
	arubaObj *v1alpha1.ArubaNetworkElasticIp
}

// NewArubaNetworkElasticIpReconciler creates a new ArubaNetworkElasticIpReconciler
func NewArubaNetworkElasticIpReconciler(baseReconciler *HelperReconciler) *ArubaNetworkElasticIpReconciler {
	return &ArubaNetworkElasticIpReconciler{
		HelperReconciler: baseReconciler,
	}
}

// +kubebuilder:rbac:groups=cloud.aruba.it,resources=arubanetworkelasticips,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cloud.aruba.it,resources=arubanetworkelasticips/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cloud.aruba.it,resources=arubanetworkelasticips/finalizers,verbs=update
// +kubebuilder:rbac:groups=cloud.aruba.it,resources=arubaprojects,verbs=get;list;watch

func (r *ArubaNetworkElasticIpReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.arubaObj = &v1alpha1.ArubaNetworkElasticIp{}
	return r.CommonReconcile(ctx, req, r.arubaObj, &r.arubaObj.Status.ArubaResourceStatus, &r.arubaObj.Spec.Tenant, r)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ArubaNetworkElasticIpReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.ArubaNetworkElasticIp{}).
		Named("arubanetworkelasticip").
		Complete(r)
}
