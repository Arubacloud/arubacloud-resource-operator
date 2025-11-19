package controller

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
)

// ArubaCloudServerReconciler reconciles a ArubaCloudServer object
type ArubaCloudServerReconciler struct {
	*HelperReconciler
	arubaObj *v1alpha1.ArubaCloudServer
}

// NewArubaCloudServerReconciler creates a new ArubaCloudServerReconciler
func NewArubaCloudServerReconciler(baseReconciler *HelperReconciler) *ArubaCloudServerReconciler {
	return &ArubaCloudServerReconciler{
		HelperReconciler: baseReconciler,
	}
}

// +kubebuilder:rbac:groups=cloud.aruba.it,resources=arubacloudservers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cloud.aruba.it,resources=arubacloudservers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cloud.aruba.it,resources=arubacloudservers/finalizers,verbs=update
// +kubebuilder:rbac:groups=cloud.aruba.it,resources=arubaprojects,verbs=get;list;watch

func (r *ArubaCloudServerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.arubaObj = &v1alpha1.ArubaCloudServer{}
	return r.CommonReconcile(ctx, req, r.arubaObj, &r.arubaObj.Status.ArubaResourceStatus, &r.arubaObj.Spec.Tenant, r)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ArubaCloudServerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.ArubaCloudServer{}).
		Named("arubacloudserver").
		Complete(r)
}
