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

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
)

var _ = Describe("Subnet Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-subnet"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}
		arubaSubnet := &v1alpha1.Subnet{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind Subnet")
			err := k8sClient.Get(ctx, typeNamespacedName, arubaSubnet)
			if err != nil && errors.IsNotFound(err) {
				resource := &v1alpha1.Subnet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: v1alpha1.SubnetSpec{

						Tags:    []string{"test"},
						Type:    "Advanced",
						Default: false,
						Network: v1alpha1.SubnetNetwork{
							Address: "192.168.1.0/24",
						},
						DHCP: v1alpha1.SubnetDHCP{
							Enabled: true,
						},
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
			resource := &v1alpha1.Subnet{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance Subnet")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})

		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")

			baseReconciler := &reconciler.Reconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
				// ArubaClient will be nil for tests - should handle gracefully
			}

			controllerReconciler := &SubnetReconciler{
				Reconciler: baseReconciler,
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
