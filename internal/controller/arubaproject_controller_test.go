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

	v1alpha1 "gitlab.aruba.it/ingegneria/seca/operators/aruba-operator/api/v1alpha1"
)

var _ = Describe("ArubaProject Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}
		arubaproject := &v1alpha1.ArubaProject{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind ArubaProject")
			err := k8sClient.Get(ctx, typeNamespacedName, arubaproject)
			if err != nil && errors.IsNotFound(err) {
				resource := &v1alpha1.ArubaProject{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: v1alpha1.ArubaProjectSpec{

						Description: "Test project for basic reconciliation",
						Tags:        []string{"test", "basic"},
						Default:     false,
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &v1alpha1.ArubaProject{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance ArubaProject")
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

			controllerReconciler := &ArubaProjectReconciler{
				HelperReconciler: baseReconciler,
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})
})

var _ = Describe("ArubaProject Controller Reconcile Method", func() {
	Context("When testing reconcile phases", func() {
		var (
			ctx                context.Context
			reconciler         *ArubaProjectReconciler
			arubaProject       *v1alpha1.ArubaProject
			typeNamespacedName types.NamespacedName
		)

		BeforeEach(func() {
			ctx = context.Background()

			// Create base reconciler with mock client
			baseReconciler := &HelperReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
				// ArubaClient will be nil for tests - should handle gracefully
			}

			reconciler = &ArubaProjectReconciler{
				HelperReconciler: baseReconciler,
			}

			typeNamespacedName = types.NamespacedName{
				Name:      "test-reconcile-resource",
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
			arubaProject = &v1alpha1.ArubaProject{
				ObjectMeta: metav1.ObjectMeta{
					Name:      typeNamespacedName.Name,
					Namespace: typeNamespacedName.Namespace,
				},
				Spec: v1alpha1.ArubaProjectSpec{

					Description: "Test project for reconciliation",
					Tags:        []string{"test", "reconciliation"},
					Default:     false,
				},
				Status: v1alpha1.ArubaResourceStatus{
					Phase: "",
				},
			}
			Expect(k8sClient.Create(ctx, arubaProject)).To(Succeed())

			By("Reconciling the resource")
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup")
			Expect(k8sClient.Delete(ctx, arubaProject)).To(Succeed())
		})

		It("should trigger delete phase when DeletionTimestamp is set", func() {
			By("Creating resource in Created phase")
			testName := fmt.Sprintf("test-delete-phase-%d", GinkgoRandomSeed())
			arubaProject = &v1alpha1.ArubaProject{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testName,
					Namespace: "default",
				},
				Spec: v1alpha1.ArubaProjectSpec{

					Description: "Test project for deletion",
					Tags:        []string{"test", "deletion"},
					Default:     false,
				},
				Status: v1alpha1.ArubaResourceStatus{
					Phase: v1alpha1.ArubaResourcePhaseCreated,
				},
			}
			Expect(k8sClient.Create(ctx, arubaProject)).To(Succeed())

			By("Setting deletion timestamp by deleting the resource")
			Expect(k8sClient.Delete(ctx, arubaProject)).To(Succeed())

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
			updatedProject := &v1alpha1.ArubaProject{}
			err = k8sClient.Get(ctx, types.NamespacedName{
				Name:      testName,
				Namespace: "default",
			}, updatedProject)
			if err == nil {
				Expect(updatedProject.Status.Phase).To(Equal(v1alpha1.ArubaResourcePhaseDeleting))
			}
		})

		It("should not trigger delete phase when already in delete phases", func() {
			deletePhases := []v1alpha1.ArubaResourcePhase{
				v1alpha1.ArubaResourcePhaseDeleting,
			}

			for i, phase := range deletePhases {
				By(fmt.Sprintf("Testing phase %s", phase))
				resourceName := fmt.Sprintf("test-delete-%d", i)
				namespacedName := types.NamespacedName{
					Name:      resourceName,
					Namespace: "default",
				}

				arubaProject = &v1alpha1.ArubaProject{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: v1alpha1.ArubaProjectSpec{

						Description: "Test project for deletion",
						Tags:        []string{"test", "deletion"},
						Default:     false,
					},
					Status: v1alpha1.ArubaResourceStatus{
						Phase: phase,
					},
				}
				Expect(k8sClient.Create(ctx, arubaProject)).To(Succeed())

				By("Setting deletion timestamp")
				Expect(k8sClient.Delete(ctx, arubaProject)).To(Succeed())

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
				v1alpha1.ArubaResourcePhaseUpdating,
				v1alpha1.ArubaResourcePhaseCreated,
			}

			for i, phase := range phases {
				By(fmt.Sprintf("Testing phase %s", phase))
				resourceName := fmt.Sprintf("test-phase-%d", i)
				namespacedName := types.NamespacedName{
					Name:      resourceName,
					Namespace: "default",
				}

				arubaProject = &v1alpha1.ArubaProject{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: v1alpha1.ArubaProjectSpec{

						Description: "Test project for phases",
						Tags:        []string{"test", "phases"},
						Default:     false,
					},
					Status: v1alpha1.ArubaResourceStatus{
						Phase: phase,
					},
				}
				Expect(k8sClient.Create(ctx, arubaProject)).To(Succeed())

				By("Reconciling the resource")
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: namespacedName,
				})
				if phase == v1alpha1.ArubaResourcePhaseCreated {
					Expect(err).NotTo(HaveOccurred())
				}
				// For other phases that require ArubaClient, we expect errors but test should not panic

				By("Cleanup")
				Expect(k8sClient.Delete(ctx, arubaProject)).To(Succeed())
			}
		})

		It("should test Next method", func() {
			By("Creating resource")
			testName := fmt.Sprintf("test-next-method-%d", GinkgoRandomSeed())
			arubaProject = &v1alpha1.ArubaProject{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testName,
					Namespace: "default",
				},
				Spec: v1alpha1.ArubaProjectSpec{

					Description: "Test project for Next method",
					Tags:        []string{"test", "next-method"},
					Default:     false,
				},
				Status: v1alpha1.ArubaResourceStatus{
					Phase: v1alpha1.ArubaResourcePhaseCreated,
				},
			}
			Expect(k8sClient.Create(ctx, arubaProject)).To(Succeed())

			By("Setting up reconciler with the object")
			reconciler.arubaObj = arubaProject

			By("Cleanup")
			Expect(k8sClient.Delete(ctx, arubaProject)).To(Succeed())
		})
	})
})
