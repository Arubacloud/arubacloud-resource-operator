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

	ctrl "sigs.k8s.io/controller-runtime"

	"gitlab.aruba.it/ingegneria/seca/operators/aruba-operator/api/v1alpha1"
	"gitlab.aruba.it/ingegneria/seca/operators/aruba-operator/internal/util"
)

// ArubaProjectReconciler reconciles a ArubaProject object
type ArubaProjectReconciler struct {
	*HelperReconciler
	arubaObj *v1alpha1.ArubaProject
}

// +kubebuilder:rbac:groups=cloud.aruba.it,resources=arubaprojects,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cloud.aruba.it,resources=arubaprojects/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cloud.aruba.it,resources=arubaprojects/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

func (r *ArubaProjectReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.arubaObj = &v1alpha1.ArubaProject{}
	return r.CommonReconcile(ctx, req, r.arubaObj, &r.arubaObj.Status, &r.arubaObj.Spec.Tenant, r)
}

// NewArubaProjectReconciler creates a new ArubaProjectReconciler
func NewArubaProjectReconciler(baseReconciler *HelperReconciler) *ArubaProjectReconciler {
	return &ArubaProjectReconciler{
		HelperReconciler: baseReconciler,
	}
}

// Provisioning is not used for ArubaProject
func (r *ArubaProjectReconciler) Provisioning(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error) {
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ArubaProjectReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.ArubaProject{}).
		Named("arubaproject").
		Complete(r)
}
