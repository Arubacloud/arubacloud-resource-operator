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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1 "gitlab.aruba.it/ingegneria/seca/operators/aruba-operator/api/v1alpha1"
)

var _ = Describe("ArubaSecurityGroup Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-security-group"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}
		arubaSecurityGroup := &v1alpha1.ArubaSecurityGroup{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind ArubaSecurityGroup")
			err := k8sClient.Get(ctx, typeNamespacedName, arubaSecurityGroup)
			if err != nil && errors.IsNotFound(err) {
				resource := &v1alpha1.ArubaSecurityGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: v1alpha1.ArubaSecurityGroupSpec{

						Tags: []string{"test"},
						Location: v1alpha1.Location{
							Value: "ITBG-Bergamo",
						},
						Default: false,
						VpcReference: v1alpha1.ResourceReference{
							Name:      "test-vpc",
							Namespace: "default",
						},
						ProjectReference: v1alpha1.ResourceReference{
							Name:      "test-project",
							Namespace: "default",
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &v1alpha1.ArubaSecurityGroup{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance ArubaSecurityGroup")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})

		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")

			baseReconciler := &HelperReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
				// ArubaClient will be nil for tests - should handle gracefully
			}

			controllerReconciler := &ArubaSecurityGroupReconciler{
				HelperReconciler: baseReconciler,
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
