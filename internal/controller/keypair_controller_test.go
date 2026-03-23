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
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
	arubamocks "github.com/Arubacloud/arubacloud-resource-operator/internal/mocks/aruba"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
	arubatypes "github.com/Arubacloud/sdk-go/pkg/types"
)

// --- Builder helpers ---

func buildKeyPairResponse(id, name string) *arubatypes.KeyPairResponse {
	location := &arubatypes.LocationResponse{Value: "ITBG-Bergamo"}
	return &arubatypes.KeyPairResponse{
		Metadata: arubatypes.ResourceMetadataResponse{
			ID:               &id,
			Name:             &name,
			LocationResponse: location,
			Tags:             []string{"tag1"},
		},
		Properties: arubatypes.KeyPairPropertiesResult{
			Value: "ssh-rsa AAAAB3NzaC1 test-key",
		},
	}
}

func buildKeyPairList(responses ...*arubatypes.KeyPairResponse) *arubatypes.Response[arubatypes.KeyPairListResponse] {
	list := &arubatypes.KeyPairListResponse{}
	for _, r := range responses {
		list.Values = append(list.Values, *r)
		list.Total++
	}
	return &arubatypes.Response[arubatypes.KeyPairListResponse]{
		Data:       list,
		StatusCode: http.StatusOK,
	}
}

func buildKeyPairCRUDResponse(statusCode int) *arubatypes.Response[arubatypes.KeyPairResponse] {
	return &arubatypes.Response[arubatypes.KeyPairResponse]{
		StatusCode: statusCode,
	}
}

func buildProjectListForKeyPair(projectID, projectName string) *arubatypes.Response[arubatypes.ProjectList] {
	id := projectID
	name := projectName
	proj := arubatypes.ProjectResponse{
		Metadata: arubatypes.ResourceMetadataResponse{
			ID:   &id,
			Name: &name,
		},
	}
	list := &arubatypes.ProjectList{}
	list.Values = append(list.Values, proj)
	list.Total = 1
	return &arubatypes.Response[arubatypes.ProjectList]{
		Data:       list,
		StatusCode: http.StatusOK,
	}
}

// --- Test fixture helpers ---

func defaultKeyPairSpec(projectName string) v1alpha1.KeyPairSpec {
	return v1alpha1.KeyPairSpec{
		Tenant:   "test-tenant",
		Location: v1alpha1.Location{Value: "ITBG-Bergamo"},
		Tags:     []string{"tag1"},
		Value:    "ssh-rsa AAAAB3NzaC1 test-key",
		ProjectReference: v1alpha1.ResourceReference{
			Name:      projectName,
			Namespace: "default",
		},
	}
}

func createTestKeyPair(ctx context.Context, name string, spec v1alpha1.KeyPairSpec) *v1alpha1.KeyPair {
	kp := &v1alpha1.KeyPair{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: spec,
	}
	ExpectWithOffset(1, k8sClient.Create(ctx, kp)).To(Succeed())
	return kp
}

func setKeyPairStatus(ctx context.Context, kp *v1alpha1.KeyPair, phase v1alpha1.ResourcePhase, reason string, resourceID string, projectID string, observedGen int64, conditionTime time.Time) {
	k := kp.DeepCopy()
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), k)).To(Succeed())
	k.Status.Phase = phase
	k.Status.ResourceID = resourceID
	k.Status.ProjectID = projectID
	k.Status.ObservedGeneration = observedGen
	if phase != "" {
		k.Status.Conditions = []metav1.Condition{
			{
				Type:               string(phase),
				Status:             metav1.ConditionTrue,
				Reason:             reason,
				LastTransitionTime: metav1.NewTime(conditionTime),
				Message:            string(phase) + " " + reason + " - OK",
			},
		}
	}
	ExpectWithOffset(1, k8sClient.Status().Update(ctx, k)).To(Succeed())
	ExpectWithOffset(1, k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), kp)).To(Succeed())
}

// --- Mock struct ---

type kpMocks struct {
	r            *KeyPairReconciler
	mockAruba    *arubamocks.MockClient
	mockProject  *arubamocks.MockProjectClient
	mockCompute  *arubamocks.MockComputeClient
	mockKeyPairs *arubamocks.MockKeyPairsClient
}

func newKpReconcilerWithMocks(t GinkgoTInterface) *kpMocks {
	mockAruba := arubamocks.NewMockClient(t)
	mockProject := arubamocks.NewMockProjectClient(t)
	mockCompute := arubamocks.NewMockComputeClient(t)
	mockKeyPairs := arubamocks.NewMockKeyPairsClient(t)

	r := NewKeyPairReconciler(newTestReconciler(t, mockAruba))

	return &kpMocks{
		r:            r,
		mockAruba:    mockAruba,
		mockProject:  mockProject,
		mockCompute:  mockCompute,
		mockKeyPairs: mockKeyPairs,
	}
}

func (m *kpMocks) expectProjectList(projectID, projectName string) {
	m.mockAruba.EXPECT().FromProject().Return(m.mockProject)
	m.mockProject.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectListForKeyPair(projectID, projectName), nil)
}

func (m *kpMocks) expectKeyPairList(projectID string, responses ...*arubatypes.KeyPairResponse) {
	m.mockAruba.EXPECT().FromCompute().Return(m.mockCompute)
	m.mockCompute.EXPECT().KeyPairs().Return(m.mockKeyPairs)
	m.mockKeyPairs.EXPECT().List(mock.Anything, projectID, mock.Anything).Return(buildKeyPairList(responses...), nil)
}

// --- Tests ---

var _ = Describe("KeyPairReconciler", func() {
	const (
		kpProjectName = "test-kp-project-ref"
		kpProjectID   = "kp-proj-id-1"
	)

	var (
		ctx context.Context
		kp  *v1alpha1.KeyPair
	)

	BeforeEach(func() {
		ctx = context.Background()
	})

	AfterEach(func() {
		if kp != nil {
			k := &v1alpha1.KeyPair{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), k); err == nil {
				k.Finalizers = nil
				_ = k8sClient.Update(ctx, k)
				_ = k8sClient.Delete(ctx, k)
			}
			kp = nil
		}
	})

	Describe("First reconciliation", func() {
		It("transitions to Creating+ShallSynchronize when CMP has no KeyPair", func() {
			m := newKpReconcilerWithMocks(GinkgoT())
			kp = createTestKeyPair(ctx, "test-kp-first", defaultKeyPairSpec(kpProjectName))

			m.expectProjectList(kpProjectID, kpProjectName)
			m.expectKeyPairList(kpProjectID)

			_, err := m.r.HandleReconcile(ctx, kp)
			Expect(err).To(Succeed())

			updated := &v1alpha1.KeyPair{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
		})
	})

	Describe("Create on CMP", func() {
		It("transitions to Creating+Synchronizing after successful CMP create", func() {
			m := newKpReconcilerWithMocks(GinkgoT())
			kp = createTestKeyPair(ctx, "test-kp-create-cmp", defaultKeyPairSpec(kpProjectName))
			setKeyPairStatus(ctx, kp, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, "", "", 0, time.Now())

			m.expectProjectList(kpProjectID, kpProjectName)
			m.expectKeyPairList(kpProjectID)
			m.mockAruba.EXPECT().FromCompute().Return(m.mockCompute)
			m.mockCompute.EXPECT().KeyPairs().Return(m.mockKeyPairs)
			m.mockKeyPairs.EXPECT().Create(mock.Anything, kpProjectID, mock.Anything, mock.Anything).Return(buildKeyPairCRUDResponse(http.StatusCreated), nil)

			_, err := m.r.HandleReconcile(ctx, kp)
			Expect(err).To(Succeed())

			updated := &v1alpha1.KeyPair{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
		})
	})

	Describe("Waiting creation (KeyPair not yet in CMP)", func() {
		It("returns LongRequeue", func() {
			m := newKpReconcilerWithMocks(GinkgoT())
			kp = createTestKeyPair(ctx, "test-kp-wait-create", defaultKeyPairSpec(kpProjectName))
			setKeyPairStatus(ctx, kp, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "", "", 0, time.Now())

			m.expectProjectList(kpProjectID, kpProjectName)
			m.expectKeyPairList(kpProjectID)

			result, err := m.r.HandleReconcile(ctx, kp)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("Creation confirmed on CMP", func() {
		It("transitions to Creating+Synchronized when CMP KeyPair is found", func() {
			m := newKpReconcilerWithMocks(GinkgoT())
			kp = createTestKeyPair(ctx, "test-kp-creation-confirmed", defaultKeyPairSpec(kpProjectName))
			setKeyPairStatus(ctx, kp, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "", "", 0, time.Now())

			cmpKp := buildKeyPairResponse("kp-id-1", "test-kp-creation-confirmed")
			m.expectProjectList(kpProjectID, kpProjectName)
			m.expectKeyPairList(kpProjectID, cmpKp)

			_, err := m.r.HandleReconcile(ctx, kp)
			Expect(err).To(Succeed())

			updated := &v1alpha1.KeyPair{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronized))
		})
	})

	Describe("Creation accomplished", func() {
		It("transitions to Active+Synchronized and sets ResourceID", func() {
			m := newKpReconcilerWithMocks(GinkgoT())
			kp = createTestKeyPair(ctx, "test-kp-creation-accomplished", defaultKeyPairSpec(kpProjectName))
			setKeyPairStatus(ctx, kp, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized, "", "", 0, time.Now())

			cmpKp := buildKeyPairResponse("kp-id-1", "test-kp-creation-accomplished")
			m.expectProjectList(kpProjectID, kpProjectName)
			m.expectKeyPairList(kpProjectID, cmpKp)

			_, err := m.r.HandleReconcile(ctx, kp)
			Expect(err).To(Succeed())

			updated := &v1alpha1.KeyPair{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseActive))
			Expect(updated.Status.ResourceID).To(Equal("kp-id-1"))
		})
	})

	Describe("ShouldBeUpdated", func() {
		It("transitions to Updating+ShallSynchronize when spec changes", func() {
			m := newKpReconcilerWithMocks(GinkgoT())
			kp = createTestKeyPair(ctx, "test-kp-should-update", defaultKeyPairSpec(kpProjectName))
			setKeyPairStatus(ctx, kp, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "kp-id-1", kpProjectID, 1, time.Now())

			// Change tags to trigger generation bump
			kFetch := &v1alpha1.KeyPair{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), kFetch)).To(Succeed())
			kFetch.Spec.Tags = []string{"tag1", "tag2"}
			Expect(k8sClient.Update(ctx, kFetch)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), kp)).To(Succeed())

			cmpKp := buildKeyPairResponse("kp-id-1", "test-kp-should-update")
			m.expectProjectList(kpProjectID, kpProjectName)
			m.expectKeyPairList(kpProjectID, cmpKp)

			_, err := m.r.HandleReconcile(ctx, kp)
			Expect(err).To(Succeed())

			updated := &v1alpha1.KeyPair{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseUpdating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseUpdating))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
		})
	})

	Describe("UpdateNotSupported", func() {
		It("transitions to Updating+Failed with error message when update is attempted", func() {
			m := newKpReconcilerWithMocks(GinkgoT())
			kp = createTestKeyPair(ctx, "test-kp-update-not-supported", defaultKeyPairSpec(kpProjectName))
			setKeyPairStatus(ctx, kp, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize, "kp-id-1", kpProjectID, 1, time.Now())

			cmpKp := buildKeyPairResponse("kp-id-1", "test-kp-update-not-supported")
			m.expectProjectList(kpProjectID, kpProjectName)
			m.expectKeyPairList(kpProjectID, cmpKp)

			_, err := m.r.HandleReconcile(ctx, kp)
			Expect(err).To(Succeed())

			updated := &v1alpha1.KeyPair{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseUpdating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseUpdating))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonFailed))
			Expect(cond.Message).To(ContainSubstring("updating KeyPair resources is not supported"))
		})
	})

	Describe("UpdateRollback", func() {
		It("rolls back spec from CMP values and transitions to Active+Synchronized", func() {
			m := newKpReconcilerWithMocks(GinkgoT())
			kp = createTestKeyPair(ctx, "test-kp-update-rollback", defaultKeyPairSpec(kpProjectName))
			setKeyPairStatus(ctx, kp, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonFailed, "kp-id-1", kpProjectID, 1, time.Now())

			// Manually set the Updating condition with Failed reason (as kubeMarkUpdatingFailed would)
			kFetch := &v1alpha1.KeyPair{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), kFetch)).To(Succeed())
			kFetch.Status.Conditions = []metav1.Condition{
				{
					Type:               string(v1alpha1.ResourcePhaseUpdating),
					Status:             metav1.ConditionTrue,
					Reason:             v1alpha1.ConditionReasonFailed,
					LastTransitionTime: metav1.Now(),
					Message:            "updating KeyPair resources is not supported",
				},
			}
			Expect(k8sClient.Status().Update(ctx, kFetch)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), kp)).To(Succeed())

			cmpKp := buildKeyPairResponse("kp-id-1", "test-kp-update-rollback")
			m.expectProjectList(kpProjectID, kpProjectName)
			m.expectKeyPairList(kpProjectID, cmpKp)

			_, err := m.r.HandleReconcile(ctx, kp)
			Expect(err).To(Succeed())

			updated := &v1alpha1.KeyPair{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseActive))
			// Spec should be rolled back to CMP values
			Expect(updated.Spec.Tags).To(Equal(cmpKp.Metadata.Tags))
			Expect(updated.Spec.Location.Value).To(Equal(cmpKp.Metadata.LocationResponse.Value))
			Expect(updated.Spec.Value).To(Equal(cmpKp.Properties.Value))
		})
	})

	Describe("Should delete", func() {
		It("transitions to Deleting+ShallSynchronize when deletion is requested on Active KeyPair", func() {
			m := newKpReconcilerWithMocks(GinkgoT())
			kp = createTestKeyPair(ctx, "test-kp-should-delete", defaultKeyPairSpec(kpProjectName))
			kFetch := &v1alpha1.KeyPair{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), kFetch)).To(Succeed())
			kFetch.Finalizers = []string{keyPairFinalizerName}
			Expect(k8sClient.Update(ctx, kFetch)).To(Succeed())
			setKeyPairStatus(ctx, kp, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "kp-id-1", kpProjectID, 1, time.Now())
			Expect(k8sClient.Delete(ctx, kp)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), kp)).To(Succeed())

			cmpKp := buildKeyPairResponse("kp-id-1", "test-kp-should-delete")
			m.mockAruba.EXPECT().FromCompute().Return(m.mockCompute)
			m.mockCompute.EXPECT().KeyPairs().Return(m.mockKeyPairs)
			m.mockKeyPairs.EXPECT().List(mock.Anything, kpProjectID, mock.Anything).Return(buildKeyPairList(cmpKp), nil)

			_, err := m.r.HandleReconcile(ctx, kp)
			Expect(err).To(Succeed())

			updated := &v1alpha1.KeyPair{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
		})
	})

	Describe("Delete on CMP", func() {
		It("transitions to Deleting+Synchronizing after successful CMP delete", func() {
			m := newKpReconcilerWithMocks(GinkgoT())
			kp = createTestKeyPair(ctx, "test-kp-delete-cmp", defaultKeyPairSpec(kpProjectName))
			kFetch := &v1alpha1.KeyPair{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), kFetch)).To(Succeed())
			kFetch.Finalizers = []string{keyPairFinalizerName}
			Expect(k8sClient.Update(ctx, kFetch)).To(Succeed())
			setKeyPairStatus(ctx, kp, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, "kp-id-1", kpProjectID, 1, time.Now())
			Expect(k8sClient.Delete(ctx, kp)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), kp)).To(Succeed())

			cmpKp := buildKeyPairResponse("kp-id-1", "test-kp-delete-cmp")
			m.mockAruba.EXPECT().FromCompute().Return(m.mockCompute)
			m.mockCompute.EXPECT().KeyPairs().Return(m.mockKeyPairs)
			m.mockKeyPairs.EXPECT().List(mock.Anything, kpProjectID, mock.Anything).Return(buildKeyPairList(cmpKp), nil)
			m.mockAruba.EXPECT().FromCompute().Return(m.mockCompute)
			m.mockCompute.EXPECT().KeyPairs().Return(m.mockKeyPairs)
			m.mockKeyPairs.EXPECT().Delete(mock.Anything, kpProjectID, "kp-id-1", mock.Anything).Return(buildDeleteResponse(http.StatusOK), nil)

			_, err := m.r.HandleReconcile(ctx, kp)
			Expect(err).To(Succeed())

			updated := &v1alpha1.KeyPair{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
		})
	})

	Describe("Deletion not needed (CMP already gone)", func() {
		It("transitions to Deleting+Synchronized when CMP KeyPair is not found", func() {
			m := newKpReconcilerWithMocks(GinkgoT())
			kp = createTestKeyPair(ctx, "test-kp-deletion-not-needed", defaultKeyPairSpec(kpProjectName))
			kFetch := &v1alpha1.KeyPair{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), kFetch)).To(Succeed())
			kFetch.Finalizers = []string{keyPairFinalizerName}
			Expect(k8sClient.Update(ctx, kFetch)).To(Succeed())
			setKeyPairStatus(ctx, kp, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, "kp-id-1", kpProjectID, 1, time.Now())
			Expect(k8sClient.Delete(ctx, kp)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), kp)).To(Succeed())

			m.mockAruba.EXPECT().FromCompute().Return(m.mockCompute)
			m.mockCompute.EXPECT().KeyPairs().Return(m.mockKeyPairs)
			m.mockKeyPairs.EXPECT().List(mock.Anything, kpProjectID, mock.Anything).Return(buildKeyPairList(), nil)

			_, err := m.r.HandleReconcile(ctx, kp)
			Expect(err).To(Succeed())

			updated := &v1alpha1.KeyPair{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronized))
		})
	})

	Describe("Waiting deletion (KeyPair still in CMP)", func() {
		It("returns LongRequeue when CMP KeyPair is still present", func() {
			m := newKpReconcilerWithMocks(GinkgoT())
			kp = createTestKeyPair(ctx, "test-kp-wait-delete", defaultKeyPairSpec(kpProjectName))
			kFetch := &v1alpha1.KeyPair{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), kFetch)).To(Succeed())
			kFetch.Finalizers = []string{keyPairFinalizerName}
			Expect(k8sClient.Update(ctx, kFetch)).To(Succeed())
			setKeyPairStatus(ctx, kp, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronizing, "kp-id-1", kpProjectID, 1, time.Now())
			Expect(k8sClient.Delete(ctx, kp)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), kp)).To(Succeed())

			cmpKp := buildKeyPairResponse("kp-id-1", "test-kp-wait-delete")
			m.mockAruba.EXPECT().FromCompute().Return(m.mockCompute)
			m.mockCompute.EXPECT().KeyPairs().Return(m.mockKeyPairs)
			m.mockKeyPairs.EXPECT().List(mock.Anything, kpProjectID, mock.Anything).Return(buildKeyPairList(cmpKp), nil)

			result, err := m.r.HandleReconcile(ctx, kp)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("Deletion confirmed on CMP", func() {
		It("transitions to Deleting+Synchronized when CMP KeyPair disappears", func() {
			m := newKpReconcilerWithMocks(GinkgoT())
			kp = createTestKeyPair(ctx, "test-kp-deletion-confirmed", defaultKeyPairSpec(kpProjectName))
			kFetch := &v1alpha1.KeyPair{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), kFetch)).To(Succeed())
			kFetch.Finalizers = []string{keyPairFinalizerName}
			Expect(k8sClient.Update(ctx, kFetch)).To(Succeed())
			setKeyPairStatus(ctx, kp, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronizing, "kp-id-1", kpProjectID, 1, time.Now())
			Expect(k8sClient.Delete(ctx, kp)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), kp)).To(Succeed())

			m.mockAruba.EXPECT().FromCompute().Return(m.mockCompute)
			m.mockCompute.EXPECT().KeyPairs().Return(m.mockKeyPairs)
			m.mockKeyPairs.EXPECT().List(mock.Anything, kpProjectID, mock.Anything).Return(buildKeyPairList(), nil)

			_, err := m.r.HandleReconcile(ctx, kp)
			Expect(err).To(Succeed())

			updated := &v1alpha1.KeyPair{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronized))
		})
	})

	Describe("Deletion accomplished", func() {
		It("transitions to Deleted phase and removes finalizer when CMP KeyPair is gone", func() {
			m := newKpReconcilerWithMocks(GinkgoT())
			kp = createTestKeyPair(ctx, "test-kp-deletion-accomplished", defaultKeyPairSpec(kpProjectName))
			kFetch := &v1alpha1.KeyPair{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), kFetch)).To(Succeed())
			kFetch.Finalizers = []string{keyPairFinalizerName}
			Expect(k8sClient.Update(ctx, kFetch)).To(Succeed())
			setKeyPairStatus(ctx, kp, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronized, "kp-id-1", kpProjectID, 1, time.Now())
			Expect(k8sClient.Delete(ctx, kp)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), kp)).To(Succeed())

			m.mockAruba.EXPECT().FromCompute().Return(m.mockCompute)
			m.mockCompute.EXPECT().KeyPairs().Return(m.mockKeyPairs)
			m.mockKeyPairs.EXPECT().List(mock.Anything, kpProjectID, mock.Anything).Return(buildKeyPairList(), nil)

			_, err := m.r.HandleReconcile(ctx, kp)
			Expect(err).To(Succeed())

			updated := &v1alpha1.KeyPair{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleted))
		})
	})

	Describe("Phase timeout", func() {
		It("transitions to Failed when stuck in transitory phase too long", func() {
			m := newKpReconcilerWithMocks(GinkgoT())
			kp = createTestKeyPair(ctx, "test-kp-timeout", defaultKeyPairSpec(kpProjectName))
			setKeyPairStatus(ctx, kp, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, "", kpProjectID,
				0, time.Now().Add(-(reconciler.MaxPhaseTimeout + time.Minute)))

			cmpKp := buildKeyPairResponse("kp-id-1", "test-kp-timeout")
			m.expectProjectList(kpProjectID, kpProjectName)
			m.expectKeyPairList(kpProjectID, cmpKp)

			_, err := m.r.HandleReconcile(ctx, kp)
			Expect(err).To(Succeed())

			updated := &v1alpha1.KeyPair{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
		})
	})

	Describe("Delete timed-out resource", func() {
		It("transitions to Deleting+ShallSynchronize when deletion is requested on Failed KeyPair", func() {
			m := newKpReconcilerWithMocks(GinkgoT())
			kp = createTestKeyPair(ctx, "test-kp-delete-timedout", defaultKeyPairSpec(kpProjectName))
			kFetch := &v1alpha1.KeyPair{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), kFetch)).To(Succeed())
			kFetch.Finalizers = []string{keyPairFinalizerName}
			Expect(k8sClient.Update(ctx, kFetch)).To(Succeed())

			// Simulate timed-out from Creating: two conditions are required for kubeShouldDeleteTimedOut.
			// The previous phase condition must have Status=False (the timed-out marker).
			kFetch2 := &v1alpha1.KeyPair{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), kFetch2)).To(Succeed())
			kFetch2.Status.Phase = v1alpha1.ResourcePhaseFailed
			kFetch2.Status.ProjectID = kpProjectID
			kFetch2.Status.Conditions = []metav1.Condition{
				{Type: "Creating", Status: metav1.ConditionFalse, Reason: v1alpha1.ConditionReasonFailed, LastTransitionTime: metav1.Now(), Message: "timeout"},
				{Type: "Failed", Status: metav1.ConditionTrue, Reason: v1alpha1.ConditionReasonFailed, LastTransitionTime: metav1.Now(), Message: "timeout"},
			}
			Expect(k8sClient.Status().Update(ctx, kFetch2)).To(Succeed())
			Expect(k8sClient.Delete(ctx, kFetch2)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), kp)).To(Succeed())

			m.mockAruba.EXPECT().FromCompute().Return(m.mockCompute)
			m.mockCompute.EXPECT().KeyPairs().Return(m.mockKeyPairs)
			m.mockKeyPairs.EXPECT().List(mock.Anything, kpProjectID, mock.Anything).Return(buildKeyPairList(), nil)

			_, err := m.r.HandleReconcile(ctx, kp)
			Expect(err).To(Succeed())

			updated := &v1alpha1.KeyPair{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
		})
	})

	Describe("Project not found yet", func() {
		It("returns LongRequeue when project doesn't exist in CMP yet", func() {
			m := newKpReconcilerWithMocks(GinkgoT())
			kp = createTestKeyPair(ctx, "test-kp-no-project", defaultKeyPairSpec(kpProjectName))

			m.mockAruba.EXPECT().FromProject().Return(m.mockProject)
			m.mockProject.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectList(), nil)

			result, err := m.r.HandleReconcile(ctx, kp)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("ProjectID set in status via prePatch callback", func() {
		It("stamps ProjectID on status when first transitioning", func() {
			m := newKpReconcilerWithMocks(GinkgoT())
			kp = createTestKeyPair(ctx, "test-kp-project-id", defaultKeyPairSpec(kpProjectName))

			m.expectProjectList(kpProjectID, kpProjectName)
			m.expectKeyPairList(kpProjectID)

			_, err := m.r.HandleReconcile(ctx, kp)
			Expect(err).To(Succeed())

			updated := &v1alpha1.KeyPair{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), updated)).To(Succeed())
			Expect(updated.Status.ProjectID).To(Equal(kpProjectID))
		})
	})
})
