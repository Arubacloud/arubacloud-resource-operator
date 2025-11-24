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

var _ = Describe("KeyPair Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-keypair"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}
		arubaKeyPair := &v1alpha1.KeyPair{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind KeyPair")
			err := k8sClient.Get(ctx, typeNamespacedName, arubaKeyPair)
			if err != nil && errors.IsNotFound(err) {
				resource := &v1alpha1.KeyPair{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: v1alpha1.KeyPairSpec{
						Tenant: "test-tenant",
						Tags:   []string{"test"},
						Location: v1alpha1.Location{
							Value: "ITBG-Bergamo",
						},
						Value: "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQC6JqByfkVm64u7emAJMmHx3gNTuorCn/RmLvozgz67MWUCygTtcRHBBS8WAANkSRLCN/r/VDGFBB5N9PzK5V5ONE/VSFGD63V861vu8mslpNHtL6gN2y1mqDzl3vi0ebZv2t6ArsdFKPx1gqsP6kavIAos7ZFgbJsmRNO2V71dK+YPeubxpMPezVBrMxDSLmA0In6z3foFTGB7iZDnQ2Yj0u/Kukf7SfPgaWaegSu/yQVDG+wLQ84d6ti6vdRyjauGvqQjdYldcvdjoG7OlAxC/TRCdwFeq4u6p73IVZoz9Xq99smnOtLu7qGCzW6g/+RNHSPSpz9+R6AKGjUPPFj29+WKJRnesdnb6rRmTyUDsezuu8z/rbthlgDYI3GaT+Sauap9lwuoVcSKKCt1GvMUC180csxVGAMz3MPN0X+pvbAqjNJmGt5lMaRrZ4BROL+PI3PDTTEPniOW+8doQEWZUA3HPthwneQ3emuqGSL3i1W5uJgSvTbAv+nXnrDK2qk=",
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
			resource := &v1alpha1.KeyPair{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance KeyPair")
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

			// Create base reconciler with mock client
			baseResourceReconciler := &reconciler.Reconciler{
				Client:       k8sClient,
				Scheme:       k8sClient.Scheme(),
				TokenManager: auth,
				HelperClient: helperClient,
			}

			resourceReconciler := &KeyPairReconciler{
				Reconciler: baseResourceReconciler,
			}

			_, err := resourceReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
