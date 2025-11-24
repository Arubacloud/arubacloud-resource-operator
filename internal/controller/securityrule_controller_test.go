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

var _ = Describe("SecurityRule Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default", // TODO(user):Modify as needed
		}
		arubasecurityrule := &v1alpha1.SecurityRule{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind SecurityRule")
			err := k8sClient.Get(ctx, typeNamespacedName, arubasecurityrule)
			if err != nil && errors.IsNotFound(err) {
				resource := &v1alpha1.SecurityRule{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: v1alpha1.SecurityRuleSpec{
						Tenant: "test-tenant",
						Tags:   []string{"test", "security-rule"},
						Location: v1alpha1.Location{
							Value: "ITBG-Bergamo",
						},
						Protocol:  "TCP",
						Port:      "80",
						Direction: "Ingress",
						Target: v1alpha1.SecurityRuleTarget{
							Kind:  "Ip",
							Value: "0.0.0.0/0",
						},
						SecurityGroupReference: v1alpha1.ResourceReference{
							Name:      "test-security-group",
							Namespace: "default",
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
			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			resource := &v1alpha1.SecurityRule{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance SecurityRule")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
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

			baseResourceReconciler := &reconciler.Reconciler{
				Client:       k8sClient,
				Scheme:       k8sClient.Scheme(),
				HelperClient: helperClient,
				TokenManager: auth,
			}

			resourceReconciler := &SecurityRuleReconciler{
				Reconciler: baseResourceReconciler,
			}

			_, err := resourceReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			// TODO(user): Add more specific assertions depending on your controller's reconciliation logic.
			// Example: If you expect a certain status condition after reconciliation, verify it here.
		})
	})
})
