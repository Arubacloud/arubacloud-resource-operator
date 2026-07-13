package controller

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/ginkgo/v2"

	arubaclient "github.com/Arubacloud/arubacloud-resource-operator/internal/client"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
)

// newTestReconciler creates a base Reconciler for unit testing. It seeds the
// per-tenant port-client cache with the given mock client under the "test-tenant"
// key, so that controllers calling r.ArubaClient("test-tenant") receive the mock
// without requiring real credentials or a ctrl.Manager.
func newTestReconciler(_ GinkgoTInterface, mockArubaClient arubaclient.Client) *reconciler.Reconciler {
	return reconciler.NewReconcilerForTest(
		k8sClient, k8sClient.Scheme(),
		map[string]arubaclient.Client{"test-tenant": mockArubaClient},
	)
}

// findCondition is a test helper.
func findCondition(conditions []metav1.Condition, condType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}
