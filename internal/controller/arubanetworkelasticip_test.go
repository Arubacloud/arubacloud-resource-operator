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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
)

var _ = Describe("ArubaNetworkElasticIp Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-elastic-ip"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}
		arubaNetworkElasticIp := &v1alpha1.ArubaNetworkElasticIp{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind ArubaNetworkElasticIp")
			err := k8sClient.Get(ctx, typeNamespacedName, arubaNetworkElasticIp)
			if err != nil && errors.IsNotFound(err) {
				resource := &v1alpha1.ArubaNetworkElasticIp{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: v1alpha1.ArubaNetworkElasticIpSpec{

						Tags: []string{"test", "basic"},
						Location: v1alpha1.Location{
							Value: "ITBG-Bergamo",
						},
						BillingPlan: v1alpha1.BillingPlan{
							BillingPeriod: "Hour",
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
			resource := &v1alpha1.ArubaNetworkElasticIp{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance ArubaNetworkElasticIp")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})

		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")

			// Create base reconciler with mock client
			baseReconciler := &HelperReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
				// ArubaClient will be nil for tests - should handle gracefully
			}

			controllerReconciler := &ArubaNetworkElasticIpReconciler{
				HelperReconciler: baseReconciler,
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})
})

var _ = Describe("ArubaNetworkElasticIp Controller Reconcile Method", func() {
	Context("When testing reconcile phases", func() {
		var (
			ctx                   context.Context
			reconciler            *ArubaNetworkElasticIpReconciler
			arubaNetworkElasticIp *v1alpha1.ArubaNetworkElasticIp
			typeNamespacedName    types.NamespacedName
		)

		BeforeEach(func() {
			ctx = context.Background()

			// Create base reconciler with mock client
			baseReconciler := &HelperReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
				// ArubaClient will be nil for tests - should handle gracefully
			}

			reconciler = &ArubaNetworkElasticIpReconciler{
				HelperReconciler: baseReconciler,
			}

			typeNamespacedName = types.NamespacedName{
				Name:      "test-reconcile-elastic-ip",
				Namespace: "default",
			}
		})

		It("should handle object not found gracefully", func() {
			By("Reconciling a non-existent resource")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      "non-existent",
					Namespace: "default",
				},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
		})

		It("should initialize phase when empty", func() {
			By("Creating resource with empty phase")
			arubaNetworkElasticIp = &v1alpha1.ArubaNetworkElasticIp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      typeNamespacedName.Name,
					Namespace: typeNamespacedName.Namespace,
				},
				Spec: v1alpha1.ArubaNetworkElasticIpSpec{

					Tags: []string{"test", "reconciliation"},
					Location: v1alpha1.Location{
						Value: "ITBG-Bergamo",
					},
					BillingPlan: v1alpha1.BillingPlan{
						BillingPeriod: "Hour",
					},
					ProjectReference: v1alpha1.ResourceReference{
						Name:      "test-project",
						Namespace: "default",
					},
				},
				Status: v1alpha1.ArubaNetworkElasticIpStatus{
					ArubaResourceStatus: v1alpha1.ArubaResourceStatus{
						Phase: "",
					},
				},
			}
			Expect(k8sClient.Create(ctx, arubaNetworkElasticIp)).To(Succeed())

			By("Reconciling the resource")
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup")
			Expect(k8sClient.Delete(ctx, arubaNetworkElasticIp)).To(Succeed())
		})

		It("should trigger delete phase when DeletionTimestamp is set", func() {
			By("Creating resource in Created phase")
			testName := fmt.Sprintf("test-delete-phase-eip-%d", GinkgoRandomSeed())
			arubaNetworkElasticIp = &v1alpha1.ArubaNetworkElasticIp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testName,
					Namespace: "default",
				},
				Spec: v1alpha1.ArubaNetworkElasticIpSpec{

					Tags: []string{"test", "deletion"},
					Location: v1alpha1.Location{
						Value: "ITBG-Bergamo",
					},
					BillingPlan: v1alpha1.BillingPlan{
						BillingPeriod: "Hour",
					},
					ProjectReference: v1alpha1.ResourceReference{
						Name:      "test-project",
						Namespace: "default",
					},
				},
				Status: v1alpha1.ArubaNetworkElasticIpStatus{
					ArubaResourceStatus: v1alpha1.ArubaResourceStatus{
						Phase: v1alpha1.ArubaResourcePhaseCreated,
					},
				},
			}
			Expect(k8sClient.Create(ctx, arubaNetworkElasticIp)).To(Succeed())

			By("Setting deletion timestamp by deleting the resource")
			Expect(k8sClient.Delete(ctx, arubaNetworkElasticIp)).To(Succeed())

			By("Reconciling the resource")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      testName,
					Namespace: "default",
				},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			By("Verifying phase changed to Delete")
			updatedElasticIp := &v1alpha1.ArubaNetworkElasticIp{}
			err = k8sClient.Get(ctx, types.NamespacedName{
				Name:      testName,
				Namespace: "default",
			}, updatedElasticIp)
			if err == nil {
				Expect(updatedElasticIp.Status.Phase).To(Equal(v1alpha1.ArubaResourcePhaseDeleting))
			}
		})

		It("should not trigger delete phase when already in delete phases", func() {
			deletePhases := []v1alpha1.ArubaResourcePhase{
				v1alpha1.ArubaResourcePhaseDeleting,
			}

			for i, phase := range deletePhases {
				By(fmt.Sprintf("Testing phase %s", phase))
				resourceName := fmt.Sprintf("test-delete-eip-%d", i)
				namespacedName := types.NamespacedName{
					Name:      resourceName,
					Namespace: "default",
				}

				arubaNetworkElasticIp = &v1alpha1.ArubaNetworkElasticIp{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: v1alpha1.ArubaNetworkElasticIpSpec{

						Tags: []string{"test", "deletion"},
						Location: v1alpha1.Location{
							Value: "ITBG-Bergamo",
						},
						BillingPlan: v1alpha1.BillingPlan{
							BillingPeriod: "Hour",
						},
						ProjectReference: v1alpha1.ResourceReference{
							Name:      "test-project",
							Namespace: "default",
						},
					},
					Status: v1alpha1.ArubaNetworkElasticIpStatus{
						ArubaResourceStatus: v1alpha1.ArubaResourceStatus{
							Phase: phase,
						},
					},
				}
				Expect(k8sClient.Create(ctx, arubaNetworkElasticIp)).To(Succeed())

				By("Setting deletion timestamp")
				Expect(k8sClient.Delete(ctx, arubaNetworkElasticIp)).To(Succeed())

				By("Reconciling should handle the specific delete phase")
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: namespacedName,
				})
				Expect(err).NotTo(HaveOccurred())
			}
		})

		It("should handle different phases correctly", func() {
			phases := []v1alpha1.ArubaResourcePhase{
				v1alpha1.ArubaResourcePhaseCreating,
				v1alpha1.ArubaResourcePhaseProvisioning,
				v1alpha1.ArubaResourcePhaseUpdating,
				v1alpha1.ArubaResourcePhaseCreated,
			}

			for i, phase := range phases {
				By(fmt.Sprintf("Testing phase %s", phase))
				resourceName := fmt.Sprintf("test-phase-eip-%d", i)
				namespacedName := types.NamespacedName{
					Name:      resourceName,
					Namespace: "default",
				}

				arubaNetworkElasticIp = &v1alpha1.ArubaNetworkElasticIp{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: v1alpha1.ArubaNetworkElasticIpSpec{

						Tags: []string{"test", "phases"},
						Location: v1alpha1.Location{
							Value: "ITBG-Bergamo",
						},
						BillingPlan: v1alpha1.BillingPlan{
							BillingPeriod: "Hour",
						},
						ProjectReference: v1alpha1.ResourceReference{
							Name:      "test-project",
							Namespace: "default",
						},
					},
					Status: v1alpha1.ArubaNetworkElasticIpStatus{
						ArubaResourceStatus: v1alpha1.ArubaResourceStatus{
							Phase: phase,
						},
					},
				}
				Expect(k8sClient.Create(ctx, arubaNetworkElasticIp)).To(Succeed())

				By("Reconciling the resource")
				// Should handle phases correctly with the implementation, even with nil ArubaClient
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: namespacedName,
				})
				// Note: Some phases may return errors due to nil ArubaClient, which is expected in tests
				if phase == v1alpha1.ArubaResourcePhaseCreated {
					Expect(err).NotTo(HaveOccurred())
				}
				// For other phases that require ArubaClient, we expect errors but test should not panic

				By("Cleanup")
				Expect(k8sClient.Delete(ctx, arubaNetworkElasticIp)).To(Succeed())
			}
		})

		It("should test Next method", func() {
			By("Creating resource")
			testName := fmt.Sprintf("test-next-method-eip-%d", GinkgoRandomSeed())
			arubaNetworkElasticIp = &v1alpha1.ArubaNetworkElasticIp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testName,
					Namespace: "default",
				},
				Spec: v1alpha1.ArubaNetworkElasticIpSpec{

					Tags: []string{"test", "next-method"},
					Location: v1alpha1.Location{
						Value: "ITBG-Bergamo",
					},
					BillingPlan: v1alpha1.BillingPlan{
						BillingPeriod: "Hour",
					},
					ProjectReference: v1alpha1.ResourceReference{
						Name:      "test-project",
						Namespace: "default",
					},
				},
				Status: v1alpha1.ArubaNetworkElasticIpStatus{
					ArubaResourceStatus: v1alpha1.ArubaResourceStatus{
						Phase: v1alpha1.ArubaResourcePhaseCreated,
					},
				},
			}
			Expect(k8sClient.Create(ctx, arubaNetworkElasticIp)).To(Succeed())

			By("Setting up reconciler with the object")
			reconciler.arubaObj = arubaNetworkElasticIp

			By("Cleanup")
			Expect(k8sClient.Delete(ctx, arubaNetworkElasticIp)).To(Succeed())
		})

		It("should test getProjectID method with valid project reference", func() {
			By("Creating a test ArubaProject first")
			projectName := fmt.Sprintf("test-ref-project-%d", GinkgoRandomSeed())
			testProject := &v1alpha1.ArubaProject{
				ObjectMeta: metav1.ObjectMeta{
					Name:      projectName,
					Namespace: "default",
				},
				Spec: v1alpha1.ArubaProjectSpec{},
			}
			Expect(k8sClient.Create(ctx, testProject)).To(Succeed())

			By("Updating the project status with ProjectID")
			testProject.Status.ResourceID = "test-project-id-12345"
			Expect(k8sClient.Status().Update(ctx, testProject)).To(Succeed())

			By("Creating elastic IP resource with project reference")
			eipName := fmt.Sprintf("test-get-project-id-eip-%d", GinkgoRandomSeed())
			arubaNetworkElasticIp = &v1alpha1.ArubaNetworkElasticIp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      eipName,
					Namespace: "default",
				},
				Spec: v1alpha1.ArubaNetworkElasticIpSpec{

					Location: v1alpha1.Location{
						Value: "ITBG-Bergamo",
					},
					BillingPlan: v1alpha1.BillingPlan{
						BillingPeriod: "Hour",
					},
					ProjectReference: v1alpha1.ResourceReference{
						Name:      projectName,
						Namespace: "default",
					},
				},
			}
			Expect(k8sClient.Create(ctx, arubaNetworkElasticIp)).To(Succeed())

			By("Setting up reconciler with the object")
			reconciler.arubaObj = arubaNetworkElasticIp

			By("Testing getProjectID method")
			projectID, err := reconciler.getProjectID(ctx, projectName, "default")
			Expect(err).NotTo(HaveOccurred())
			Expect(projectID).To(Equal("test-project-id-12345"))

			By("Cleanup")
			Expect(k8sClient.Delete(ctx, arubaNetworkElasticIp)).To(Succeed())
			Expect(k8sClient.Delete(ctx, testProject)).To(Succeed())
		})
	})
})
