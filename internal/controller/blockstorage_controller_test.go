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

func buildBlockStorageResponse(id, name, state string) *arubatypes.BlockStorageResponse {
	location := &arubatypes.LocationResponse{Value: "ITBG-Bergamo"}
	return &arubatypes.BlockStorageResponse{
		Metadata: arubatypes.ResourceMetadataResponse{
			ID:               &id,
			Name:             &name,
			LocationResponse: location,
		},
		Properties: arubatypes.BlockStoragePropertiesResponse{
			SizeGB:        10,
			BillingPeriod: "Hour",
			Zone:          "zone1",
			Type:          arubatypes.BlockStorageTypeStandard,
		},
		Status: arubatypes.ResourceStatus{
			State: &state,
		},
	}
}

func buildBlockStorageList(responses ...*arubatypes.BlockStorageResponse) *arubatypes.Response[arubatypes.BlockStorageList] {
	list := &arubatypes.BlockStorageList{}
	for _, r := range responses {
		list.Values = append(list.Values, *r)
		list.Total++
	}
	return &arubatypes.Response[arubatypes.BlockStorageList]{
		Data:       list,
		StatusCode: http.StatusOK,
	}
}

func buildBSCRUDResponse(statusCode int) *arubatypes.Response[arubatypes.BlockStorageResponse] {
	return &arubatypes.Response[arubatypes.BlockStorageResponse]{
		StatusCode: statusCode,
	}
}

func buildProjectListForBS(projectID, projectName string) *arubatypes.Response[arubatypes.ProjectList] {
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

func defaultBSSpec(projectName string) v1alpha1.BlockStorageSpec {
	return v1alpha1.BlockStorageSpec{
		Tenant:        "test-tenant",
		Location:      v1alpha1.Location{Value: "ITBG-Bergamo"},
		SizeGb:        10,
		BillingPeriod: "Hour",
		DataCenter:    "zone1",
		Type:          "Standard",
		Tags:          []string{"tag1"},
		ProjectReference: v1alpha1.ResourceReference{
			Name:      projectName,
			Namespace: "default",
		},
	}
}

func createTestBlockStorage(ctx context.Context, name string, spec v1alpha1.BlockStorageSpec) *v1alpha1.BlockStorage {
	bs := &v1alpha1.BlockStorage{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: spec,
	}
	ExpectWithOffset(1, k8sClient.Create(ctx, bs)).To(Succeed())
	return bs
}

func setBSStatus(ctx context.Context, bs *v1alpha1.BlockStorage, phase v1alpha1.ResourcePhase, reason string, resourceID string, projectID string, observedGen int64, conditionTime time.Time) {
	b := bs.DeepCopy()
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), b)).To(Succeed())
	b.Status.Phase = phase
	b.Status.ResourceID = resourceID
	b.Status.ProjectID = projectID
	b.Status.ObservedGeneration = observedGen
	if phase != "" {
		b.Status.Conditions = []metav1.Condition{
			{
				Type:               string(phase),
				Status:             metav1.ConditionTrue,
				Reason:             reason,
				LastTransitionTime: metav1.NewTime(conditionTime),
				Message:            string(phase) + " " + reason + " - OK",
			},
		}
	}
	ExpectWithOffset(1, k8sClient.Status().Update(ctx, b)).To(Succeed())
	ExpectWithOffset(1, k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), bs)).To(Succeed())
}

type bsMocks struct {
	r           *BlockStorageReconciler
	mockAruba   *arubamocks.MockClient
	mockProject *arubamocks.MockProjectClient
	mockStorage *arubamocks.MockStorageClient
	mockVolumes *arubamocks.MockVolumesClient
}

func newBSReconcilerWithMocks(t GinkgoTInterface) *bsMocks {
	mockAruba := arubamocks.NewMockClient(t)
	mockProject := arubamocks.NewMockProjectClient(t)
	mockStorage := arubamocks.NewMockStorageClient(t)
	mockVolumes := arubamocks.NewMockVolumesClient(t)

	r := NewBlockStorageReconciler(newTestReconciler(t, mockAruba))

	return &bsMocks{
		r:           r,
		mockAruba:   mockAruba,
		mockProject: mockProject,
		mockStorage: mockStorage,
		mockVolumes: mockVolumes,
	}
}

func (m *bsMocks) expectProjectList(projectID, projectName string) {
	m.mockAruba.EXPECT().FromProject().Return(m.mockProject)
	m.mockProject.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectListForBS(projectID, projectName), nil)
}

func (m *bsMocks) expectBSList(projectID string, responses ...*arubatypes.BlockStorageResponse) {
	m.mockAruba.EXPECT().FromStorage().Return(m.mockStorage)
	m.mockStorage.EXPECT().Volumes().Return(m.mockVolumes)
	m.mockVolumes.EXPECT().List(mock.Anything, projectID, mock.Anything).Return(buildBlockStorageList(responses...), nil)
}

// --- Tests ---

var _ = Describe("BlockStorageReconciler", func() {
	const (
		bsProjectName = "test-project-ref"
		bsProjectID   = "proj-id-1"
	)

	var (
		ctx context.Context
		bs  *v1alpha1.BlockStorage
	)

	BeforeEach(func() {
		ctx = context.Background()
	})

	AfterEach(func() {
		if bs != nil {
			b := &v1alpha1.BlockStorage{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), b); err == nil {
				b.Finalizers = nil
				_ = k8sClient.Update(ctx, b)
				_ = k8sClient.Delete(ctx, b)
			}
			bs = nil
		}
	})

	Describe("First reconciliation", func() {
		It("transitions to Creating+ShallSynchronize when CMP has no BS", func() {
			m := newBSReconcilerWithMocks(GinkgoT())
			bs = createTestBlockStorage(ctx, "test-bs-first", defaultBSSpec(bsProjectName))

			m.expectProjectList(bsProjectID, bsProjectName)
			m.expectBSList(bsProjectID)

			_, err := m.r.HandleReconcile(ctx, bs)
			Expect(err).To(Succeed())

			updated := &v1alpha1.BlockStorage{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
		})
	})

	Describe("Create on CMP", func() {
		It("transitions to Creating+Synchronizing after successful CMP create", func() {
			m := newBSReconcilerWithMocks(GinkgoT())
			bs = createTestBlockStorage(ctx, "test-bs-create-cmp", defaultBSSpec(bsProjectName))
			setBSStatus(ctx, bs, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, "", "", 0, time.Now())

			m.expectProjectList(bsProjectID, bsProjectName)
			m.expectBSList(bsProjectID)
			m.mockAruba.EXPECT().FromStorage().Return(m.mockStorage)
			m.mockStorage.EXPECT().Volumes().Return(m.mockVolumes)
			m.mockVolumes.EXPECT().Create(mock.Anything, bsProjectID, mock.Anything, mock.Anything).Return(buildBSCRUDResponse(http.StatusCreated), nil)

			_, err := m.r.HandleReconcile(ctx, bs)
			Expect(err).To(Succeed())

			updated := &v1alpha1.BlockStorage{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
		})
	})

	Describe("Waiting creation (BS not yet in CMP)", func() {
		It("returns LongRequeue", func() {
			m := newBSReconcilerWithMocks(GinkgoT())
			bs = createTestBlockStorage(ctx, "test-bs-wait-create", defaultBSSpec(bsProjectName))
			setBSStatus(ctx, bs, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "", "", 0, time.Now())

			m.expectProjectList(bsProjectID, bsProjectName)
			m.expectBSList(bsProjectID)

			result, err := m.r.HandleReconcile(ctx, bs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("Waiting creation (BS in transitory CMP state)", func() {
		It("returns LongRequeue when CMP state is Creating", func() {
			m := newBSReconcilerWithMocks(GinkgoT())
			bs = createTestBlockStorage(ctx, "test-bs-wait-create-transitory", defaultBSSpec(bsProjectName))
			setBSStatus(ctx, bs, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "", "", 0, time.Now())

			cmpBS := buildBlockStorageResponse("bs-id-1", "test-bs-wait-create-transitory", CSPResourceStateCreating)
			m.expectProjectList(bsProjectID, bsProjectName)
			m.expectBSList(bsProjectID, cmpBS)

			result, err := m.r.HandleReconcile(ctx, bs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("Creation confirmed on CMP", func() {
		It("transitions to Creating+Synchronized when CMP BS is active", func() {
			m := newBSReconcilerWithMocks(GinkgoT())
			bs = createTestBlockStorage(ctx, "test-bs-creation-confirmed", defaultBSSpec(bsProjectName))
			setBSStatus(ctx, bs, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "", "", 0, time.Now())

			cmpBS := buildBlockStorageResponse("bs-id-1", "test-bs-creation-confirmed", CSPResourceStateActive)
			m.expectProjectList(bsProjectID, bsProjectName)
			m.expectBSList(bsProjectID, cmpBS)

			_, err := m.r.HandleReconcile(ctx, bs)
			Expect(err).To(Succeed())

			updated := &v1alpha1.BlockStorage{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronized))
		})
	})

	Describe("Creation accomplished", func() {
		It("transitions to Active+Synchronized and sets ResourceID", func() {
			m := newBSReconcilerWithMocks(GinkgoT())
			bs = createTestBlockStorage(ctx, "test-bs-creation-accomplished", defaultBSSpec(bsProjectName))
			setBSStatus(ctx, bs, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized, "", "", 0, time.Now())

			cmpBS := buildBlockStorageResponse("bs-id-1", "test-bs-creation-accomplished", CSPResourceStateActive)
			m.expectProjectList(bsProjectID, bsProjectName)
			m.expectBSList(bsProjectID, cmpBS)

			_, err := m.r.HandleReconcile(ctx, bs)
			Expect(err).To(Succeed())

			updated := &v1alpha1.BlockStorage{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseActive))
			Expect(updated.Status.ResourceID).To(Equal("bs-id-1"))
		})
	})

	Describe("HasDeniedChanges", func() {
		It("returns LongRequeue when immutable field (size decrease) is changed", func() {
			m := newBSReconcilerWithMocks(GinkgoT())
			bs = createTestBlockStorage(ctx, "test-bs-denied-changes", defaultBSSpec(bsProjectName))
			setBSStatus(ctx, bs, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "bs-id-1", bsProjectID, 1, time.Now())

			// Force generation change with smaller size
			bFetch := &v1alpha1.BlockStorage{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), bFetch)).To(Succeed())
			bFetch.Spec.SizeGb = 1 // decrease from 10 to 1
			Expect(k8sClient.Update(ctx, bFetch)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), bs)).To(Succeed())

			// CMP has size 10 (larger than new spec 1)
			cmpBS := buildBlockStorageResponse("bs-id-1", "test-bs-denied-changes", CSPResourceStateActive)
			m.expectProjectList(bsProjectID, bsProjectName)
			m.expectBSList(bsProjectID, cmpBS)

			result, err := m.r.HandleReconcile(ctx, bs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("IsInError", func() {
		It("transitions to Failed+Synchronized when CMP state is Failed", func() {
			m := newBSReconcilerWithMocks(GinkgoT())
			bs = createTestBlockStorage(ctx, "test-bs-in-error", defaultBSSpec(bsProjectName))
			setBSStatus(ctx, bs, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "bs-id-1", bsProjectID, 1, time.Now())

			cmpBS := buildBlockStorageResponse("bs-id-1", "test-bs-in-error", CSPResourceStateFailed)
			m.expectProjectList(bsProjectID, bsProjectName)
			m.expectBSList(bsProjectID, cmpBS)

			_, err := m.r.HandleReconcile(ctx, bs)
			Expect(err).To(Succeed())

			updated := &v1alpha1.BlockStorage{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseFailed))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronized))
		})
	})

	Describe("CMP transitory during deletion", func() {
		It("returns LongRequeue when CMP state is Deleting", func() {
			m := newBSReconcilerWithMocks(GinkgoT())
			bs = createTestBlockStorage(ctx, "test-bs-deleting-transitory", defaultBSSpec(bsProjectName))
			bFetch := &v1alpha1.BlockStorage{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), bFetch)).To(Succeed())
			bFetch.Finalizers = []string{blockStorageFinalizerName}
			Expect(k8sClient.Update(ctx, bFetch)).To(Succeed())
			setBSStatus(ctx, bs, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronizing, "bs-id-1", bsProjectID, 1, time.Now())
			Expect(k8sClient.Delete(ctx, bs)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), bs)).To(Succeed())

			cmpBS := buildBlockStorageResponse("bs-id-1", "test-bs-deleting-transitory", CSPResourceStateDeleting)
			m.mockAruba.EXPECT().FromStorage().Return(m.mockStorage)
			m.mockStorage.EXPECT().Volumes().Return(m.mockVolumes)
			m.mockVolumes.EXPECT().List(mock.Anything, bsProjectID, mock.Anything).Return(buildBlockStorageList(cmpBS), nil)

			result, err := m.r.HandleReconcile(ctx, bs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("Project not found yet", func() {
		It("returns LongRequeue when project doesn't exist in CMP yet", func() {
			m := newBSReconcilerWithMocks(GinkgoT())
			bs = createTestBlockStorage(ctx, "test-bs-no-project", defaultBSSpec(bsProjectName))

			m.mockAruba.EXPECT().FromProject().Return(m.mockProject)
			m.mockProject.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectList(), nil)

			result, err := m.r.HandleReconcile(ctx, bs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("ProjectID set in status via prePatch callback", func() {
		It("stamps ProjectID on status when first transitioning", func() {
			m := newBSReconcilerWithMocks(GinkgoT())
			bs = createTestBlockStorage(ctx, "test-bs-project-id", defaultBSSpec(bsProjectName))

			m.expectProjectList(bsProjectID, bsProjectName)
			m.expectBSList(bsProjectID)

			_, err := m.r.HandleReconcile(ctx, bs)
			Expect(err).To(Succeed())

			updated := &v1alpha1.BlockStorage{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), updated)).To(Succeed())
			Expect(updated.Status.ProjectID).To(Equal(bsProjectID))
		})
	})

	Describe("Should delete", func() {
		It("transitions to Deleting+ShallSynchronize when deletion is requested on Active BS", func() {
			m := newBSReconcilerWithMocks(GinkgoT())
			bs = createTestBlockStorage(ctx, "test-bs-should-delete", defaultBSSpec(bsProjectName))
			bFetch := &v1alpha1.BlockStorage{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), bFetch)).To(Succeed())
			bFetch.Finalizers = []string{blockStorageFinalizerName}
			Expect(k8sClient.Update(ctx, bFetch)).To(Succeed())
			setBSStatus(ctx, bs, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "bs-id-1", bsProjectID, 1, time.Now())
			Expect(k8sClient.Delete(ctx, bs)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), bs)).To(Succeed())

			cmpBS := buildBlockStorageResponse("bs-id-1", "test-bs-should-delete", CSPResourceStateActive)
			m.mockAruba.EXPECT().FromStorage().Return(m.mockStorage)
			m.mockStorage.EXPECT().Volumes().Return(m.mockVolumes)
			m.mockVolumes.EXPECT().List(mock.Anything, bsProjectID, mock.Anything).Return(buildBlockStorageList(cmpBS), nil)

			_, err := m.r.HandleReconcile(ctx, bs)
			Expect(err).To(Succeed())

			updated := &v1alpha1.BlockStorage{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
		})
	})

	Describe("Delete on CMP", func() {
		It("transitions to Deleting+Synchronizing after successful CMP delete", func() {
			m := newBSReconcilerWithMocks(GinkgoT())
			bs = createTestBlockStorage(ctx, "test-bs-delete-cmp", defaultBSSpec(bsProjectName))
			bFetch := &v1alpha1.BlockStorage{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), bFetch)).To(Succeed())
			bFetch.Finalizers = []string{blockStorageFinalizerName}
			Expect(k8sClient.Update(ctx, bFetch)).To(Succeed())
			setBSStatus(ctx, bs, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, "bs-id-1", bsProjectID, 1, time.Now())
			Expect(k8sClient.Delete(ctx, bs)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), bs)).To(Succeed())

			cmpBS := buildBlockStorageResponse("bs-id-1", "test-bs-delete-cmp", CSPResourceStateActive)
			m.mockAruba.EXPECT().FromStorage().Return(m.mockStorage)
			m.mockStorage.EXPECT().Volumes().Return(m.mockVolumes)
			m.mockVolumes.EXPECT().List(mock.Anything, bsProjectID, mock.Anything).Return(buildBlockStorageList(cmpBS), nil)
			m.mockAruba.EXPECT().FromStorage().Return(m.mockStorage)
			m.mockStorage.EXPECT().Volumes().Return(m.mockVolumes)
			m.mockVolumes.EXPECT().Delete(mock.Anything, bsProjectID, "bs-id-1", mock.Anything).Return(buildDeleteResponse(http.StatusOK), nil)

			_, err := m.r.HandleReconcile(ctx, bs)
			Expect(err).To(Succeed())

			updated := &v1alpha1.BlockStorage{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
		})
	})

	Describe("Deletion accomplished", func() {
		It("transitions to Deleted phase when CMP BS is gone", func() {
			m := newBSReconcilerWithMocks(GinkgoT())
			bs = createTestBlockStorage(ctx, "test-bs-deletion-accomplished", defaultBSSpec(bsProjectName))
			bFetch := &v1alpha1.BlockStorage{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), bFetch)).To(Succeed())
			bFetch.Finalizers = []string{blockStorageFinalizerName}
			Expect(k8sClient.Update(ctx, bFetch)).To(Succeed())
			setBSStatus(ctx, bs, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronized, "bs-id-1", bsProjectID, 1, time.Now())
			Expect(k8sClient.Delete(ctx, bs)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), bs)).To(Succeed())

			m.mockAruba.EXPECT().FromStorage().Return(m.mockStorage)
			m.mockStorage.EXPECT().Volumes().Return(m.mockVolumes)
			m.mockVolumes.EXPECT().List(mock.Anything, bsProjectID, mock.Anything).Return(buildBlockStorageList(), nil)

			_, err := m.r.HandleReconcile(ctx, bs)
			Expect(err).To(Succeed())

			updated := &v1alpha1.BlockStorage{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleted))
		})
	})

	Describe("Update on CMP", func() {
		It("transitions to Updating+Synchronizing after successful CMP update", func() {
			m := newBSReconcilerWithMocks(GinkgoT())
			bs = createTestBlockStorage(ctx, "test-bs-update-cmp", defaultBSSpec(bsProjectName))
			setBSStatus(ctx, bs, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize, "bs-id-1", bsProjectID, 1, time.Now())

			cmpBS := buildBlockStorageResponse("bs-id-1", "test-bs-update-cmp", CSPResourceStateActive)
			m.expectProjectList(bsProjectID, bsProjectName)
			m.expectBSList(bsProjectID, cmpBS)
			m.mockAruba.EXPECT().FromStorage().Return(m.mockStorage)
			m.mockStorage.EXPECT().Volumes().Return(m.mockVolumes)
			m.mockVolumes.EXPECT().Update(mock.Anything, bsProjectID, "bs-id-1", mock.Anything, mock.Anything).Return(buildBSCRUDResponse(http.StatusOK), nil)

			_, err := m.r.HandleReconcile(ctx, bs)
			Expect(err).To(Succeed())

			updated := &v1alpha1.BlockStorage{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseUpdating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseUpdating))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
		})
	})

	Describe("Phase timeout", func() {
		It("transitions to Failed when stuck in transitory phase too long", func() {
			m := newBSReconcilerWithMocks(GinkgoT())
			bs = createTestBlockStorage(ctx, "test-bs-timeout", defaultBSSpec(bsProjectName))
			setBSStatus(ctx, bs, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, "", bsProjectID,
				0, time.Now().Add(-(reconciler.MaxPhaseTimeout + time.Minute)))

			cmpBS := buildBlockStorageResponse("bs-id-1", "test-bs-timeout", CSPResourceStateActive)
			m.expectProjectList(bsProjectID, bsProjectName)
			m.expectBSList(bsProjectID, cmpBS)

			_, err := m.r.HandleReconcile(ctx, bs)
			Expect(err).To(Succeed())

			updated := &v1alpha1.BlockStorage{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
		})
	})
})
