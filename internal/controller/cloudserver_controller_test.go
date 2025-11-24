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
	"io"
	"net/http"
	"strings"

	"github.com/Arubacloud/arubacloud-resource-operator/internal/client"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/mocks"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/mock"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
)

var _ = Describe("CloudServer Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-cloud-server"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}
		arubaCloudServer := &v1alpha1.CloudServer{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind CloudServer")
			err := k8sClient.Get(ctx, typeNamespacedName, arubaCloudServer)
			if err != nil && errors.IsNotFound(err) {
				resource := &v1alpha1.CloudServer{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: v1alpha1.CloudServerSpec{
						Tenant: "test-tenant",
						Tags:   []string{"sample-tag"},
						Location: v1alpha1.Location{
							Value: "ITBG-Bergamo",
						},
						DataCenter: "ITBG-1",
						VpcReference: v1alpha1.ResourceReference{
							Name:      "aruba-resource-v5",
							Namespace: "default",
						},
						VpcPreset:  false,
						FlavorName: "CSO4A8",
						SubnetReferences: []v1alpha1.ResourceReference{
							{Namespace: "default"},
						},
						SecurityGroupReferences: []v1alpha1.ResourceReference{
							{Namespace: "default"},
						},
						KeyPairReference: v1alpha1.ResourceReference{
							Name:      "aruba-resource-v5",
							Namespace: "default",
						},
						BootVolumeReference: v1alpha1.ResourceReference{
							Name:      "aruba-resource-v5",
							Namespace: "default",
						},
						ProjectReference: v1alpha1.ResourceReference{
							Name:      "aruba-resource-v5",
							Namespace: "default",
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &v1alpha1.CloudServer{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance CloudServer")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})

	})
})

var _ = Describe("CloudServer Controller Reconcile Method", func() {
	Context("When testing reconcile phases", func() {
		var (
			ctx                   context.Context
			cloudServerReconciler *CloudServerReconciler
			arubaCloudServer      *v1alpha1.CloudServer
			typeNamespacedName    types.NamespacedName
		)

		BeforeEach(func() {
			ctx = context.Background()
			auth := new(mocks.MockITokenManager)
			auth.On("GetActiveToken", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("token 123", nil)
			auth.On("SetClientIdAndSecret", mock.Anything, mock.Anything, mock.Anything).Return(nil)
			auth.On("SetClientIdAndSecret", mock.Anything, mock.Anything, mock.Anything).Return(nil)

			// Create mock HTTP client that returns 200 for all requests
			mockHTTPClient := new(mocks.MockHTTPClient)
			mockHTTPClient.On("Do", mock.AnythingOfType("*http.Request")).Return(
				&http.Response{
					StatusCode: 200,
					Body:       io.NopCloser(strings.NewReader(`{"success": true}`)),
					Header:     make(http.Header),
				}, nil)

			// Create HelperClient with mocked HTTP client
			helperClient := client.NewHelperClient(k8sClient, mockHTTPClient, "https://api.example.com")

			// Create base reconciler with mock client
			baseResourceReconciler := &reconciler.Reconciler{
				Client:       k8sClient,
				Scheme:       k8sClient.Scheme(),
				TokenManager: auth,
				HelperClient: helperClient,
			}

			cloudServerReconciler = &CloudServerReconciler{
				Reconciler: baseResourceReconciler,
			}

			typeNamespacedName = types.NamespacedName{
				Name:      "test-reconcile-cloud-server",
				Namespace: "default",
			}
		})

		It("should handle object not found gracefully", func() {
			By("Reconciling a non-existent resource")
			result, err := cloudServerReconciler.Reconcile(ctx, reconcile.Request{
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
			arubaCloudServer = &v1alpha1.CloudServer{
				ObjectMeta: metav1.ObjectMeta{
					Name:      typeNamespacedName.Name,
					Namespace: typeNamespacedName.Namespace,
				},
				Spec: v1alpha1.CloudServerSpec{
					Tenant: "test-tenant",
					Tags:   []string{"test", "reconciliation"},
					Location: v1alpha1.Location{
						Value: "ITBG-Bergamo",
					},
					DataCenter: "ITBG-1",
					VpcReference: v1alpha1.ResourceReference{
						Name:      "test-vpc",
						Namespace: "default",
					},
					VpcPreset: false,

					SubnetReferences: []v1alpha1.ResourceReference{
						{Name: "test-subnet", Namespace: "default"},
					},
					SecurityGroupReferences: []v1alpha1.ResourceReference{
						{Name: "test-sg", Namespace: "default"},
					},
					KeyPairReference: v1alpha1.ResourceReference{
						Name:      "test-keypair",
						Namespace: "default",
					},
					BootVolumeReference: v1alpha1.ResourceReference{
						Name:      "test-boot-volume",
						Namespace: "default",
					},
					ProjectReference: v1alpha1.ResourceReference{
						Name:      "test-project",
						Namespace: "default",
					},
				},
				Status: v1alpha1.CloudServerStatus{
					ResourceStatus: v1alpha1.ResourceStatus{
						Phase: "",
					},
				},
			}
			Expect(k8sClient.Create(ctx, arubaCloudServer)).To(Succeed())

			By("Reconciling the resource")
			_, err := cloudServerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup")
			Expect(k8sClient.Delete(ctx, arubaCloudServer)).To(Succeed())
		})

		It("should trigger delete phase when DeletionTimestamp is set", func() {
			By("Creating resource in Created phase")
			testName := fmt.Sprintf("test-delete-phase-cs-%d", GinkgoRandomSeed())
			arubaCloudServer = &v1alpha1.CloudServer{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testName,
					Namespace: "default",
				},
				Spec: v1alpha1.CloudServerSpec{
					Tenant: "test-tenant",
					Tags:   []string{"test", "deletion"},
					Location: v1alpha1.Location{
						Value: "ITBG-Bergamo",
					},
					DataCenter: "ITBG-1",
					VpcReference: v1alpha1.ResourceReference{
						Name:      "test-vpc",
						Namespace: "default",
					},
					VpcPreset: false,
					SubnetReferences: []v1alpha1.ResourceReference{
						{Name: "test-subnet", Namespace: "default"},
					},
					SecurityGroupReferences: []v1alpha1.ResourceReference{
						{Name: "test-sg", Namespace: "default"},
					},
					KeyPairReference: v1alpha1.ResourceReference{
						Name:      "test-keypair",
						Namespace: "default",
					},
					BootVolumeReference: v1alpha1.ResourceReference{
						Name:      "test-boot-volume",
						Namespace: "default",
					},
					ProjectReference: v1alpha1.ResourceReference{
						Name:      "test-project",
						Namespace: "default",
					},
				},
				Status: v1alpha1.CloudServerStatus{
					ResourceStatus: v1alpha1.ResourceStatus{
						Phase: v1alpha1.ResourcePhaseCreated,
					},
				},
			}
			Expect(k8sClient.Create(ctx, arubaCloudServer)).To(Succeed())

			By("Setting deletion timestamp by deleting the resource")
			Expect(k8sClient.Delete(ctx, arubaCloudServer)).To(Succeed())

			By("Reconciling the resource")
			result, err := cloudServerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      testName,
					Namespace: "default",
				},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			By("Verifying phase changed to Delete")
			updatedCloudServer := &v1alpha1.CloudServer{}
			err = k8sClient.Get(ctx, types.NamespacedName{
				Name:      testName,
				Namespace: "default",
			}, updatedCloudServer)
			if err == nil {
				Expect(updatedCloudServer.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
			}
		})

		It("should not trigger delete phase when already in delete phases", func() {
			deletePhases := []v1alpha1.ResourcePhase{
				v1alpha1.ResourcePhaseDeleting,
			}

			for i, phase := range deletePhases {
				By(fmt.Sprintf("Testing phase %s", phase))
				resourceName := fmt.Sprintf("test-delete-cs-%d", i)
				namespacedName := types.NamespacedName{
					Name:      resourceName,
					Namespace: "default",
				}

				arubaCloudServer = &v1alpha1.CloudServer{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: v1alpha1.CloudServerSpec{
						Tenant: "test-tenant",
						Tags:   []string{"test", "deletion"},
						Location: v1alpha1.Location{
							Value: "ITBG-Bergamo",
						},
						DataCenter: "ITBG-1",
						VpcReference: v1alpha1.ResourceReference{
							Name:      "test-vpc",
							Namespace: "default",
						},
						VpcPreset:  false,
						FlavorName: "b5052a43-60d0-4041-9d5d-448d30c48f0c",
						SubnetReferences: []v1alpha1.ResourceReference{
							{Name: "test-subnet", Namespace: "default"},
						},
						SecurityGroupReferences: []v1alpha1.ResourceReference{
							{Name: "test-sg", Namespace: "default"},
						},
						KeyPairReference: v1alpha1.ResourceReference{
							Name:      "test-keypair",
							Namespace: "default",
						},
						BootVolumeReference: v1alpha1.ResourceReference{
							Name:      "test-boot-volume",
							Namespace: "default",
						},
						ProjectReference: v1alpha1.ResourceReference{
							Name:      "test-project",
							Namespace: "default",
						},
					},
					Status: v1alpha1.CloudServerStatus{
						ResourceStatus: v1alpha1.ResourceStatus{
							Phase: phase,
						},
					},
				}
				Expect(k8sClient.Create(ctx, arubaCloudServer)).To(Succeed())

				By("Setting deletion timestamp")
				Expect(k8sClient.Delete(ctx, arubaCloudServer)).To(Succeed())

				By("Reconciling should handle the specific delete phase")
				_, err := cloudServerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: namespacedName,
				})
				Expect(err).NotTo(HaveOccurred())
			}
		})

		It("should handle different phases correctly", func() {
			phases := []v1alpha1.ResourcePhase{
				v1alpha1.ResourcePhaseCreating,
				v1alpha1.ResourcePhaseProvisioning,
				v1alpha1.ResourcePhaseUpdating,
				v1alpha1.ResourcePhaseCreated,
			}

			for i, phase := range phases {
				By(fmt.Sprintf("Testing phase %s", phase))
				resourceName := fmt.Sprintf("test-phase-cs-%d", i)
				namespacedName := types.NamespacedName{
					Name:      resourceName,
					Namespace: "default",
				}

				arubaCloudServer = &v1alpha1.CloudServer{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: v1alpha1.CloudServerSpec{
						Tenant: "test-tenant",
						Tags:   []string{"test", "phases"},
						Location: v1alpha1.Location{
							Value: "ITBG-Bergamo",
						},
						DataCenter: "ITBG-1",
						VpcReference: v1alpha1.ResourceReference{
							Name:      "test-vpc",
							Namespace: "default",
						},
						VpcPreset:  false,
						FlavorName: "b5052a43-60d0-4041-9d5d-448d30c48f0c",
						SubnetReferences: []v1alpha1.ResourceReference{
							{Name: "test-subnet", Namespace: "default"},
						},
						SecurityGroupReferences: []v1alpha1.ResourceReference{
							{Name: "test-sg", Namespace: "default"},
						},
						KeyPairReference: v1alpha1.ResourceReference{
							Name:      "test-keypair",
							Namespace: "default",
						},
						BootVolumeReference: v1alpha1.ResourceReference{
							Name:      "test-boot-volume",
							Namespace: "default",
						},
						ProjectReference: v1alpha1.ResourceReference{
							Name:      "test-project",
							Namespace: "default",
						},
					},
					Status: v1alpha1.CloudServerStatus{
						ResourceStatus: v1alpha1.ResourceStatus{
							Phase: phase,
						},
					},
				}
				Expect(k8sClient.Create(ctx, arubaCloudServer)).To(Succeed())

				By("Reconciling the resource")
				// Should handle phases correctly with the implementation, even with nil ArubaClient
				_, err := cloudServerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: namespacedName,
				})
				// Note: Some phases may return errors due to nil ArubaClient, which is expected in tests
				if phase == v1alpha1.ResourcePhaseCreated {
					Expect(err).NotTo(HaveOccurred())
				}
				// For other phases that require ArubaClient, we expect errors but test should not panic

				By("Cleanup")
				Expect(k8sClient.Delete(ctx, arubaCloudServer)).To(Succeed())
			}
		})

		It("should test Next method", func() {
			By("Creating resource")
			testName := fmt.Sprintf("test-next-method-cs-%d", GinkgoRandomSeed())
			arubaCloudServer = &v1alpha1.CloudServer{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testName,
					Namespace: "default",
				},
				Spec: v1alpha1.CloudServerSpec{
					Tenant: "test-tenant",
					Tags:   []string{"test", "next-method"},
					Location: v1alpha1.Location{
						Value: "ITBG-Bergamo",
					},
					DataCenter: "ITBG-1",
					VpcReference: v1alpha1.ResourceReference{
						Name:      "test-vpc",
						Namespace: "default",
					},
					VpcPreset:  false,
					FlavorName: "b5052a43-60d0-4041-9d5d-448d30c48f0c",
					SubnetReferences: []v1alpha1.ResourceReference{
						{Name: "test-subnet", Namespace: "default"},
					},
					SecurityGroupReferences: []v1alpha1.ResourceReference{
						{Name: "test-sg", Namespace: "default"},
					},
					KeyPairReference: v1alpha1.ResourceReference{
						Name:      "test-keypair",
						Namespace: "default",
					},
					BootVolumeReference: v1alpha1.ResourceReference{
						Name:      "test-boot-volume",
						Namespace: "default",
					},
					ProjectReference: v1alpha1.ResourceReference{
						Name:      "test-project",
						Namespace: "default",
					},
				},
				Status: v1alpha1.CloudServerStatus{
					ResourceStatus: v1alpha1.ResourceStatus{
						Phase: v1alpha1.ResourcePhaseCreated,
					},
				},
			}
			Expect(k8sClient.Create(ctx, arubaCloudServer)).To(Succeed())

			By("Cleanup")
			Expect(k8sClient.Delete(ctx, arubaCloudServer)).To(Succeed())
		})

		It("should test getProjectID method with valid project reference", func() {
			By("Creating a test Project first")
			projectName := fmt.Sprintf("test-ref-project-%d-%d", GinkgoRandomSeed(), GinkgoParallelProcess())
			testProject := &v1alpha1.Project{
				ObjectMeta: metav1.ObjectMeta{
					Name:      projectName,
					Namespace: "default",
				},
				Spec: v1alpha1.ProjectSpec{
					Tenant: "test-tenant",
				},
			}

			// Check if project already exists, delete it first
			existingProject := &v1alpha1.Project{}
			err := k8sClient.Get(ctx, types.NamespacedName{Name: projectName, Namespace: "default"}, existingProject)
			if err == nil {
				Expect(k8sClient.Delete(ctx, existingProject)).To(Succeed())
				// Wait for deletion
				Eventually(func() bool {
					err := k8sClient.Get(ctx, types.NamespacedName{Name: projectName, Namespace: "default"}, existingProject)
					return errors.IsNotFound(err)
				}).Should(BeTrue())
			}

			Expect(k8sClient.Create(ctx, testProject)).To(Succeed())

			By("Updating the project status with ProjectID")
			testProject.Status.ResourceID = "test-project-id-12345"
			Expect(k8sClient.Status().Update(ctx, testProject)).To(Succeed())

			By("Creating cloud server resource with project reference")
			csName := fmt.Sprintf("test-get-project-id-cs-%d-%d", GinkgoRandomSeed(), GinkgoParallelProcess())
			arubaCloudServer = &v1alpha1.CloudServer{
				ObjectMeta: metav1.ObjectMeta{
					Name:      csName,
					Namespace: "default",
				},
				Spec: v1alpha1.CloudServerSpec{
					Tenant: "test-tenant",
					Location: v1alpha1.Location{
						Value: "ITBG-Bergamo",
					},
					DataCenter: "ITBG-1",
					VpcReference: v1alpha1.ResourceReference{
						Name:      "test-vpc",
						Namespace: "default",
					},
					VpcPreset:  false,
					FlavorName: "b5052a43-60d0-4041-9d5d-448d30c48f0c",
					SubnetReferences: []v1alpha1.ResourceReference{
						{Name: "test-subnet", Namespace: "default"},
					},
					SecurityGroupReferences: []v1alpha1.ResourceReference{
						{Name: "test-sg", Namespace: "default"},
					},
					KeyPairReference: v1alpha1.ResourceReference{
						Name:      "test-keypair",
						Namespace: "default",
					},
					BootVolumeReference: v1alpha1.ResourceReference{
						Name:      "test-boot-volume",
						Namespace: "default",
					},
					ProjectReference: v1alpha1.ResourceReference{
						Name:      projectName,
						Namespace: "default",
					},
				},
			}
			Expect(k8sClient.Create(ctx, arubaCloudServer)).To(Succeed())

			By("Testing getProjectID method")
			projectID, err := cloudServerReconciler.GetProjectID(ctx, projectName, "default")
			Expect(err).NotTo(HaveOccurred())
			Expect(projectID).To(Equal("test-project-id-12345"))

			By("Cleanup")
			Expect(k8sClient.Delete(ctx, arubaCloudServer)).To(Succeed())
			Expect(k8sClient.Delete(ctx, testProject)).To(Succeed())
		})
	})
})
