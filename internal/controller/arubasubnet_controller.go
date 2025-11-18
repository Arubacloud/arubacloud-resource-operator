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
)

// ArubaSubnetReconciler reconciles a ArubaSubnet object
type ArubaSubnetReconciler struct {
	*HelperReconciler
	arubaObj *v1alpha1.ArubaSubnet
}

// NewArubaSubnetReconciler creates a new ArubaSubnetReconciler
func NewArubaSubnetReconciler(baseReconciler *HelperReconciler) *ArubaSubnetReconciler {
	return &ArubaSubnetReconciler{
		HelperReconciler: baseReconciler,
	}
}

// +kubebuilder:rbac:groups=cloud.aruba.it,resources=arubasubnets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cloud.aruba.it,resources=arubasubnets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cloud.aruba.it,resources=arubasubnets/finalizers,verbs=update
// +kubebuilder:rbac:groups=cloud.aruba.it,resources=arubavpcs,verbs=get;list;watch

func (r *ArubaSubnetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.arubaObj = &v1alpha1.ArubaSubnet{}
	return r.CommonReconcile(ctx, req, r.arubaObj, &r.arubaObj.Status.ArubaResourceStatus, &r.arubaObj.Spec.Tenant, r)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ArubaSubnetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.ArubaSubnet{}).
		Named("arubasubnet").
		Complete(r)
}
