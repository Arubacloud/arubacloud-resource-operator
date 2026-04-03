package controller

import (
	"github.com/Arubacloud/sdk-go/pkg/aruba"
	arubamt "github.com/Arubacloud/sdk-go/pkg/multitenant"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/ginkgo/v2"

	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
)

// newTestReconciler creates a base Reconciler for unit testing. It seeds a real
// arubamt.Multitenant cache with the given mockArubaClient under the "test-tenant" key,
// so that controllers calling r.ArubaClient("test-tenant") receive the mock without
// requiring real credentials or a ctrl.Manager.
func newTestReconciler(_ GinkgoTInterface, mockArubaClient aruba.Client) *reconciler.Reconciler {
	mt := arubamt.New()
	mt.Add("test-tenant", mockArubaClient)
	return reconciler.NewReconcilerForTest(k8sClient, k8sClient.Scheme(), mt)
}

func strPtr(s string) *string { return &s }

// findCondition is a test helper.
func findCondition(conditions []metav1.Condition, condType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}
