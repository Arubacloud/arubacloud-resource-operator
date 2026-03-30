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

func buildElasticIPResponse(id, name, state string) *arubatypes.ElasticIPResponse {
	location := &arubatypes.LocationResponse{Value: "ITBG-Bergamo"}
	return &arubatypes.ElasticIPResponse{
		Metadata: arubatypes.ResourceMetadataResponse{
			ID:               &id,
			Name:             &name,
			LocationResponse: location,
		},
		Status: arubatypes.ResourceStatus{
			State: &state,
		},
	}
}

func buildElasticIPList(responses ...*arubatypes.ElasticIPResponse) *arubatypes.Response[arubatypes.ElasticList] {
	list := &arubatypes.ElasticList{}
	for _, r := range responses {
		list.Values = append(list.Values, *r)
		list.Total++
	}
	return &arubatypes.Response[arubatypes.ElasticList]{
		Data:       list,
		StatusCode: http.StatusOK,
	}
}

func buildElasticIPCRUDResponse(statusCode int) *arubatypes.Response[arubatypes.ElasticIPResponse] {
	return &arubatypes.Response[arubatypes.ElasticIPResponse]{
		StatusCode: statusCode,
	}
}

func buildProjectListForElasticIP(projectID, projectName string) *arubatypes.Response[arubatypes.ProjectList] {
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

func defaultElasticIPSpec(projectName string) v1alpha1.ElasticIPSpec {
	return v1alpha1.ElasticIPSpec{
		Tenant:        "test-tenant",
		Region:        "ITBG-Bergamo",
		Tags:          []string{"tag1"},
		BillingPeriod: "Hour",
		ProjectReference: v1alpha1.ResourceReference{
			Name:      projectName,
			Namespace: "default",
		},
	}
}

func createTestElasticIP(ctx context.Context, name string, spec v1alpha1.ElasticIPSpec) *v1alpha1.ElasticIP {
	eip := &v1alpha1.ElasticIP{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: spec,
	}
	ExpectWithOffset(1, k8sClient.Create(ctx, eip)).To(Succeed())
	return eip
}

func setElasticIPStatus(ctx context.Context, eip *v1alpha1.ElasticIP, phase v1alpha1.ResourcePhase, reason string, resourceID string, projectID string, observedGen int64, conditionTime time.Time) {
	e := eip.DeepCopy()
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), e)).To(Succeed())
	e.Status.Phase = phase
	e.Status.ResourceID = resourceID
	e.Status.ProjectID = projectID
	e.Status.ObservedGeneration = observedGen
	if phase != "" {
		e.Status.Conditions = []metav1.Condition{
			{
				Type:               string(phase),
				Status:             metav1.ConditionTrue,
				Reason:             reason,
				LastTransitionTime: metav1.NewTime(conditionTime),
				Message:            string(phase) + " " + reason + " - OK",
			},
		}
	}
	ExpectWithOffset(1, k8sClient.Status().Update(ctx, e)).To(Succeed())
	ExpectWithOffset(1, k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), eip)).To(Succeed())
}

// --- Mock struct ---

type eipMocks struct {
	r              *ElasticIPReconciler
	mockAruba      *arubamocks.MockClient
	mockProject    *arubamocks.MockProjectClient
	mockNetwork    *arubamocks.MockNetworkClient
	mockElasticIPs *arubamocks.MockElasticIPsClient
}

func newEipReconcilerWithMocks(t GinkgoTInterface) *eipMocks {
	mockAruba := arubamocks.NewMockClient(t)
	mockProject := arubamocks.NewMockProjectClient(t)
	mockNetwork := arubamocks.NewMockNetworkClient(t)
	mockElasticIPs := arubamocks.NewMockElasticIPsClient(t)

	r := NewElasticIPReconciler(newTestReconciler(t, mockAruba))

	return &eipMocks{
		r:              r,
		mockAruba:      mockAruba,
		mockProject:    mockProject,
		mockNetwork:    mockNetwork,
		mockElasticIPs: mockElasticIPs,
	}
}

func (m *eipMocks) expectProjectList(projectID, projectName string) {
	m.mockAruba.EXPECT().FromProject().Return(m.mockProject)
	m.mockProject.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectListForElasticIP(projectID, projectName), nil)
}

func (m *eipMocks) expectElasticIPList(projectID string, responses ...*arubatypes.ElasticIPResponse) {
	m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
	m.mockNetwork.EXPECT().ElasticIPs().Return(m.mockElasticIPs)
	m.mockElasticIPs.EXPECT().List(mock.Anything, projectID, mock.Anything).Return(buildElasticIPList(responses...), nil)
}

// --- Tests ---

var _ = Describe("ElasticIPReconciler", func() {
	const (
		eipProjectName = "test-eip-project-ref"
		eipProjectID   = "eip-proj-id-1"
	)

	var (
		ctx context.Context
		eip *v1alpha1.ElasticIP
	)

	BeforeEach(func() {
		ctx = context.Background()
	})

	AfterEach(func() {
		if eip != nil {
			e := &v1alpha1.ElasticIP{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), e); err == nil {
				e.Finalizers = nil
				_ = k8sClient.Update(ctx, e)
				_ = k8sClient.Delete(ctx, e)
			}
			eip = nil
		}
	})

	Describe("First reconciliation", func() {
		It("transitions to Creating+ShallSynchronize when CMP has no ElasticIp", func() {
			m := newEipReconcilerWithMocks(GinkgoT())
			eip = createTestElasticIP(ctx, "test-eip-first", defaultElasticIPSpec(eipProjectName))
			setElasticIPStatus(ctx, eip, v1alpha1.ResourcePhasePending, v1alpha1.ConditionReasonSynchronized, "", "", 0, time.Now())

			m.expectProjectList(eipProjectID, eipProjectName)
			m.expectElasticIPList(eipProjectID)

			_, err := m.r.HandleReconcile(ctx, eip)
			Expect(err).To(Succeed())

			updated := &v1alpha1.ElasticIP{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))

			pendingCond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhasePending))
			Expect(pendingCond).NotTo(BeNil())
			Expect(pendingCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(pendingCond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronized))
		})
	})

	Describe("PendingAndDeleting", func() {
		It("transitions directly to Deleted when resource is in Pending and is being deleted", func() {
			m := newEipReconcilerWithMocks(GinkgoT())
			eip = createTestElasticIP(ctx, "test-eip-pending-deleting", defaultElasticIPSpec(eipProjectName))
			setElasticIPStatus(ctx, eip, v1alpha1.ResourcePhasePending, v1alpha1.ConditionReasonSynchronized, "", "", 0, time.Now())

			m.expectProjectList(eipProjectID, eipProjectName)
			m.expectElasticIPList(eipProjectID)

			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), eip)).To(Succeed())
			eip.Finalizers = []string{elasticIpFinalizerName}
			Expect(k8sClient.Update(ctx, eip)).To(Succeed())

			Expect(k8sClient.Delete(ctx, eip)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), eip)).To(Succeed())

			_, err := m.r.HandleReconcile(ctx, eip)
			Expect(err).To(Succeed())

			updated := &v1alpha1.ElasticIP{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleted))

			pendingCond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhasePending))
			Expect(pendingCond).NotTo(BeNil())
			Expect(pendingCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(pendingCond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronized))
		})
	})

	Describe("Create on CMP", func() {
		It("transitions to Creating+Synchronizing after successful CMP create", func() {
			m := newEipReconcilerWithMocks(GinkgoT())
			eip = createTestElasticIP(ctx, "test-eip-create-cmp", defaultElasticIPSpec(eipProjectName))
			setElasticIPStatus(ctx, eip, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, "", "", 0, time.Now())

			m.expectProjectList(eipProjectID, eipProjectName)
			m.expectElasticIPList(eipProjectID)
			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
			m.mockNetwork.EXPECT().ElasticIPs().Return(m.mockElasticIPs)
			m.mockElasticIPs.EXPECT().Create(mock.Anything, eipProjectID, mock.Anything, mock.Anything).Return(buildElasticIPCRUDResponse(http.StatusCreated), nil)

			_, err := m.r.HandleReconcile(ctx, eip)
			Expect(err).To(Succeed())

			updated := &v1alpha1.ElasticIP{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
		})
	})

	Describe("Waiting creation (ElasticIp not yet in CMP)", func() {
		It("returns LongRequeue", func() {
			m := newEipReconcilerWithMocks(GinkgoT())
			eip = createTestElasticIP(ctx, "test-eip-wait-create", defaultElasticIPSpec(eipProjectName))
			setElasticIPStatus(ctx, eip, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "", "", 0, time.Now())

			m.expectProjectList(eipProjectID, eipProjectName)
			m.expectElasticIPList(eipProjectID)

			result, err := m.r.HandleReconcile(ctx, eip)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("Waiting creation (ElasticIp in transitory CMP state)", func() {
		It("returns LongRequeue when CMP state is Creating", func() {
			m := newEipReconcilerWithMocks(GinkgoT())
			eip = createTestElasticIP(ctx, "test-eip-wait-create-transitory", defaultElasticIPSpec(eipProjectName))
			setElasticIPStatus(ctx, eip, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "", "", 0, time.Now())

			cmpEip := buildElasticIPResponse("eip-id-1", "test-eip-wait-create-transitory", reconciler.CSPResourceStateCreating)
			m.expectProjectList(eipProjectID, eipProjectName)
			m.expectElasticIPList(eipProjectID, cmpEip)

			result, err := m.r.HandleReconcile(ctx, eip)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("Creation confirmed on CMP", func() {
		It("transitions to Creating+Synchronized when CMP ElasticIp is active", func() {
			m := newEipReconcilerWithMocks(GinkgoT())
			eip = createTestElasticIP(ctx, "test-eip-creation-confirmed", defaultElasticIPSpec(eipProjectName))
			setElasticIPStatus(ctx, eip, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "", "", 0, time.Now())

			cmpEip := buildElasticIPResponse("eip-id-1", "test-eip-creation-confirmed", reconciler.CSPResourceStateNotUsed)
			m.expectProjectList(eipProjectID, eipProjectName)
			m.expectElasticIPList(eipProjectID, cmpEip)

			_, err := m.r.HandleReconcile(ctx, eip)
			Expect(err).To(Succeed())

			updated := &v1alpha1.ElasticIP{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronized))
		})
	})

	Describe("Creation accomplished", func() {
		It("transitions to Active+Synchronized and sets ResourceID", func() {
			m := newEipReconcilerWithMocks(GinkgoT())
			eip = createTestElasticIP(ctx, "test-eip-creation-accomplished", defaultElasticIPSpec(eipProjectName))
			setElasticIPStatus(ctx, eip, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized, "", "", 0, time.Now())

			cmpEip := buildElasticIPResponse("eip-id-1", "test-eip-creation-accomplished", reconciler.CSPResourceStateNotUsed)
			m.expectProjectList(eipProjectID, eipProjectName)
			m.expectElasticIPList(eipProjectID, cmpEip)

			_, err := m.r.HandleReconcile(ctx, eip)
			Expect(err).To(Succeed())

			updated := &v1alpha1.ElasticIP{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseActive))
			Expect(updated.Status.ResourceID).To(Equal("eip-id-1"))
		})
	})

	Describe("HasDeniedChanges", func() {
		It("returns LongRequeue when immutable field (location) is changed", func() {
			m := newEipReconcilerWithMocks(GinkgoT())
			eip = createTestElasticIP(ctx, "test-eip-denied-changes", defaultElasticIPSpec(eipProjectName))
			setElasticIPStatus(ctx, eip, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "eip-id-1", eipProjectID, 1, time.Now())

			// Force generation change with different location
			eFetch := &v1alpha1.ElasticIP{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), eFetch)).To(Succeed())
			eFetch.Spec.Region = "different-location"
			Expect(k8sClient.Update(ctx, eFetch)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), eip)).To(Succeed())

			// CMP still has original location ITBG-Bergamo
			cmpEip := buildElasticIPResponse("eip-id-1", "test-eip-denied-changes", reconciler.CSPResourceStateNotUsed)
			m.expectProjectList(eipProjectID, eipProjectName)
			m.expectElasticIPList(eipProjectID, cmpEip)

			result, err := m.r.HandleReconcile(ctx, eip)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("SpecAlreadyInSyncWithCMP", func() {
		It("re-stamps ObservedGeneration when spec hasn't actually changed", func() {
			m := newEipReconcilerWithMocks(GinkgoT())
			eip = createTestElasticIP(ctx, "test-eip-spec-in-sync", defaultElasticIPSpec(eipProjectName))
			setElasticIPStatus(ctx, eip, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "eip-id-1", eipProjectID, 1, time.Now())

			// Trigger generation bump with same tags
			eFetch := &v1alpha1.ElasticIP{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), eFetch)).To(Succeed())
			eFetch.Spec.Tags = []string{"tag1"}
			Expect(k8sClient.Update(ctx, eFetch)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), eip)).To(Succeed())

			// CMP matches: same tags, same location, same billing period
			cmpEip := buildElasticIPResponse("eip-id-1", "test-eip-spec-in-sync", reconciler.CSPResourceStateNotUsed)
			cmpEip.Metadata.Tags = []string{"tag1"}
			cmpEip.Properties.BillingPlan = arubatypes.BillingPeriodResource{BillingPeriod: "Hour"}
			m.expectProjectList(eipProjectID, eipProjectName)
			m.expectElasticIPList(eipProjectID, cmpEip)

			_, err := m.r.HandleReconcile(ctx, eip)
			Expect(err).To(Succeed())

			updated := &v1alpha1.ElasticIP{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseActive))
			Expect(updated.Status.ObservedGeneration).To(Equal(eip.Generation))
		})
	})

	Describe("ShouldBeUpdated", func() {
		It("transitions to Updating+ShallSynchronize when tags differ", func() {
			m := newEipReconcilerWithMocks(GinkgoT())
			eip = createTestElasticIP(ctx, "test-eip-should-update", defaultElasticIPSpec(eipProjectName))
			setElasticIPStatus(ctx, eip, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "eip-id-1", eipProjectID, 1, time.Now())

			// Change tags to trigger update
			eFetch := &v1alpha1.ElasticIP{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), eFetch)).To(Succeed())
			eFetch.Spec.Tags = []string{"tag1", "tag2"}
			Expect(k8sClient.Update(ctx, eFetch)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), eip)).To(Succeed())

			// CMP has old tags
			cmpEip := buildElasticIPResponse("eip-id-1", "test-eip-should-update", reconciler.CSPResourceStateNotUsed)
			cmpEip.Metadata.Tags = []string{"tag1"}
			cmpEip.Properties.BillingPlan = arubatypes.BillingPeriodResource{BillingPeriod: "Hour"}
			m.expectProjectList(eipProjectID, eipProjectName)
			m.expectElasticIPList(eipProjectID, cmpEip)

			_, err := m.r.HandleReconcile(ctx, eip)
			Expect(err).To(Succeed())

			updated := &v1alpha1.ElasticIP{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseUpdating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseUpdating))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
		})
	})

	Describe("Update on CMP", func() {
		It("transitions to Updating+Synchronizing after successful CMP update", func() {
			m := newEipReconcilerWithMocks(GinkgoT())
			eip = createTestElasticIP(ctx, "test-eip-update-cmp", defaultElasticIPSpec(eipProjectName))
			setElasticIPStatus(ctx, eip, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize, "eip-id-1", eipProjectID, 1, time.Now())

			cmpEip := buildElasticIPResponse("eip-id-1", "test-eip-update-cmp", reconciler.CSPResourceStateNotUsed)
			m.expectProjectList(eipProjectID, eipProjectName)
			m.expectElasticIPList(eipProjectID, cmpEip)
			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
			m.mockNetwork.EXPECT().ElasticIPs().Return(m.mockElasticIPs)
			m.mockElasticIPs.EXPECT().Update(mock.Anything, eipProjectID, "eip-id-1", mock.Anything, mock.Anything).Return(buildElasticIPCRUDResponse(http.StatusOK), nil)

			_, err := m.r.HandleReconcile(ctx, eip)
			Expect(err).To(Succeed())

			updated := &v1alpha1.ElasticIP{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseUpdating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseUpdating))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
		})
	})

	Describe("Should delete", func() {
		It("transitions to Deleting+ShallSynchronize when deletion is requested on Active ElasticIp", func() {
			m := newEipReconcilerWithMocks(GinkgoT())
			eip = createTestElasticIP(ctx, "test-eip-should-delete", defaultElasticIPSpec(eipProjectName))
			eFetch := &v1alpha1.ElasticIP{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), eFetch)).To(Succeed())
			eFetch.Finalizers = []string{elasticIpFinalizerName}
			Expect(k8sClient.Update(ctx, eFetch)).To(Succeed())
			setElasticIPStatus(ctx, eip, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "eip-id-1", eipProjectID, 1, time.Now())
			Expect(k8sClient.Delete(ctx, eip)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), eip)).To(Succeed())

			cmpEip := buildElasticIPResponse("eip-id-1", "test-eip-should-delete", reconciler.CSPResourceStateNotUsed)
			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
			m.mockNetwork.EXPECT().ElasticIPs().Return(m.mockElasticIPs)
			m.mockElasticIPs.EXPECT().List(mock.Anything, eipProjectID, mock.Anything).Return(buildElasticIPList(cmpEip), nil)

			_, err := m.r.HandleReconcile(ctx, eip)
			Expect(err).To(Succeed())

			updated := &v1alpha1.ElasticIP{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
		})
	})

	Describe("Delete on CMP", func() {
		It("transitions to Deleting+Synchronizing after successful CMP delete", func() {
			m := newEipReconcilerWithMocks(GinkgoT())
			eip = createTestElasticIP(ctx, "test-eip-delete-cmp", defaultElasticIPSpec(eipProjectName))
			eFetch := &v1alpha1.ElasticIP{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), eFetch)).To(Succeed())
			eFetch.Finalizers = []string{elasticIpFinalizerName}
			Expect(k8sClient.Update(ctx, eFetch)).To(Succeed())
			setElasticIPStatus(ctx, eip, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, "eip-id-1", eipProjectID, 1, time.Now())
			Expect(k8sClient.Delete(ctx, eip)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), eip)).To(Succeed())

			cmpEip := buildElasticIPResponse("eip-id-1", "test-eip-delete-cmp", reconciler.CSPResourceStateNotUsed)
			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
			m.mockNetwork.EXPECT().ElasticIPs().Return(m.mockElasticIPs)
			m.mockElasticIPs.EXPECT().List(mock.Anything, eipProjectID, mock.Anything).Return(buildElasticIPList(cmpEip), nil)
			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
			m.mockNetwork.EXPECT().ElasticIPs().Return(m.mockElasticIPs)
			m.mockElasticIPs.EXPECT().Delete(mock.Anything, eipProjectID, "eip-id-1", mock.Anything).Return(buildDeleteResponse(http.StatusOK), nil)

			_, err := m.r.HandleReconcile(ctx, eip)
			Expect(err).To(Succeed())

			updated := &v1alpha1.ElasticIP{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
		})
	})

	Describe("CMP transitory during deletion", func() {
		It("returns LongRequeue when CMP state is Deleting", func() {
			m := newEipReconcilerWithMocks(GinkgoT())
			eip = createTestElasticIP(ctx, "test-eip-deleting-transitory", defaultElasticIPSpec(eipProjectName))
			eFetch := &v1alpha1.ElasticIP{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), eFetch)).To(Succeed())
			eFetch.Finalizers = []string{elasticIpFinalizerName}
			Expect(k8sClient.Update(ctx, eFetch)).To(Succeed())
			setElasticIPStatus(ctx, eip, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronizing, "eip-id-1", eipProjectID, 1, time.Now())
			Expect(k8sClient.Delete(ctx, eip)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), eip)).To(Succeed())

			cmpEip := buildElasticIPResponse("eip-id-1", "test-eip-deleting-transitory", reconciler.CSPResourceStateDeleting)
			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
			m.mockNetwork.EXPECT().ElasticIPs().Return(m.mockElasticIPs)
			m.mockElasticIPs.EXPECT().List(mock.Anything, eipProjectID, mock.Anything).Return(buildElasticIPList(cmpEip), nil)

			result, err := m.r.HandleReconcile(ctx, eip)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("Deletion accomplished", func() {
		It("transitions to Deleted phase when CMP ElasticIp is gone", func() {
			m := newEipReconcilerWithMocks(GinkgoT())
			eip = createTestElasticIP(ctx, "test-eip-deletion-accomplished", defaultElasticIPSpec(eipProjectName))
			eFetch := &v1alpha1.ElasticIP{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), eFetch)).To(Succeed())
			eFetch.Finalizers = []string{elasticIpFinalizerName}
			Expect(k8sClient.Update(ctx, eFetch)).To(Succeed())
			setElasticIPStatus(ctx, eip, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronized, "eip-id-1", eipProjectID, 1, time.Now())
			Expect(k8sClient.Delete(ctx, eip)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), eip)).To(Succeed())

			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
			m.mockNetwork.EXPECT().ElasticIPs().Return(m.mockElasticIPs)
			m.mockElasticIPs.EXPECT().List(mock.Anything, eipProjectID, mock.Anything).Return(buildElasticIPList(), nil)

			_, err := m.r.HandleReconcile(ctx, eip)
			Expect(err).To(Succeed())

			updated := &v1alpha1.ElasticIP{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleted))
		})
	})

	Describe("IsInError", func() {
		It("transitions to Failed+Synchronized when CMP state is Failed", func() {
			m := newEipReconcilerWithMocks(GinkgoT())
			eip = createTestElasticIP(ctx, "test-eip-in-error", defaultElasticIPSpec(eipProjectName))
			setElasticIPStatus(ctx, eip, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "eip-id-1", eipProjectID, 1, time.Now())

			cmpEip := buildElasticIPResponse("eip-id-1", "test-eip-in-error", reconciler.CSPResourceStateFailed)
			m.expectProjectList(eipProjectID, eipProjectName)
			m.expectElasticIPList(eipProjectID, cmpEip)

			_, err := m.r.HandleReconcile(ctx, eip)
			Expect(err).To(Succeed())

			updated := &v1alpha1.ElasticIP{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseFailed))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronized))
		})
	})

	Describe("Phase timeout", func() {
		It("transitions to Failed when stuck in transitory phase too long", func() {
			m := newEipReconcilerWithMocks(GinkgoT())
			eip = createTestElasticIP(ctx, "test-eip-timeout", defaultElasticIPSpec(eipProjectName))
			setElasticIPStatus(ctx, eip, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, "", eipProjectID,
				0, time.Now().Add(-(reconciler.MaxPhaseTimeout + time.Minute)))

			cmpEip := buildElasticIPResponse("eip-id-1", "test-eip-timeout", reconciler.CSPResourceStateNotUsed)
			m.expectProjectList(eipProjectID, eipProjectName)
			m.expectElasticIPList(eipProjectID, cmpEip)

			_, err := m.r.HandleReconcile(ctx, eip)
			Expect(err).To(Succeed())

			updated := &v1alpha1.ElasticIP{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
		})
	})

	Describe("Project not found yet", func() {
		It("returns LongRequeue when project doesn't exist in CMP yet", func() {
			m := newEipReconcilerWithMocks(GinkgoT())
			eip = createTestElasticIP(ctx, "test-eip-no-project", defaultElasticIPSpec(eipProjectName))

			m.mockAruba.EXPECT().FromProject().Return(m.mockProject)
			m.mockProject.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectList(), nil)

			result, err := m.r.HandleReconcile(ctx, eip)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("ProjectID set in status via prePatch callback", func() {
		It("stamps ProjectID on status when first transitioning", func() {
			m := newEipReconcilerWithMocks(GinkgoT())
			eip = createTestElasticIP(ctx, "test-eip-project-id", defaultElasticIPSpec(eipProjectName))
			setElasticIPStatus(ctx, eip, v1alpha1.ResourcePhasePending, v1alpha1.ConditionReasonSynchronized, "", "", 0, time.Now())

			m.expectProjectList(eipProjectID, eipProjectName)
			m.expectElasticIPList(eipProjectID)

			_, err := m.r.HandleReconcile(ctx, eip)
			Expect(err).To(Succeed())

			updated := &v1alpha1.ElasticIP{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), updated)).To(Succeed())
			Expect(updated.Status.ProjectID).To(Equal(eipProjectID))
		})
	})

	Describe("CMP error handling", func() {
		DescribeTable("CMP create fails — preserves Creating+ShallSynchronize, surfaces error in condition",
			func(name string, statusCode int, expectedRequeue time.Duration) {
				m := newEipReconcilerWithMocks(GinkgoT())
				eip = createTestElasticIP(ctx, name, defaultElasticIPSpec(eipProjectName))
				setElasticIPStatus(ctx, eip, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, "", eipProjectID, 0, time.Now())

				m.expectProjectList(eipProjectID, eipProjectName)
				m.expectElasticIPList(eipProjectID)
				m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
				m.mockNetwork.EXPECT().ElasticIPs().Return(m.mockElasticIPs)
				m.mockElasticIPs.EXPECT().Create(mock.Anything, eipProjectID, mock.Anything, mock.Anything).Return(buildElasticIPCRUDResponse(statusCode), nil)

				result, err := m.r.HandleReconcile(ctx, eip)
				Expect(err).To(Succeed())
				Expect(result.RequeueAfter).To(Equal(expectedRequeue))

				updated := &v1alpha1.ElasticIP{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), updated)).To(Succeed())
				Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
				cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
				Expect(cond).NotTo(BeNil())
				Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
				Expect(cond.Message).To(ContainSubstring("ERROR"))
			},
			Entry("4xx → LongRequeueAfter, no phase change", "eip-cmp-err-create-400", http.StatusBadRequest, reconciler.LongRequeueAfter),
			Entry("5xx → ShortRequeueAfter, no phase change", "eip-cmp-err-create-500", http.StatusInternalServerError, reconciler.ShortRequeueAfter),
		)

		DescribeTable("CMP update fails — preserves Updating+ShallSynchronize, surfaces error in condition",
			func(name string, statusCode int, expectedRequeue time.Duration) {
				m := newEipReconcilerWithMocks(GinkgoT())
				eip = createTestElasticIP(ctx, name, defaultElasticIPSpec(eipProjectName))
				setElasticIPStatus(ctx, eip, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize, "eip-id-1", eipProjectID, 1, time.Now())

				cmpEip := buildElasticIPResponse("eip-id-1", name, reconciler.CSPResourceStateNotUsed)
				m.expectProjectList(eipProjectID, eipProjectName)
				m.expectElasticIPList(eipProjectID, cmpEip)
				m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
				m.mockNetwork.EXPECT().ElasticIPs().Return(m.mockElasticIPs)
				m.mockElasticIPs.EXPECT().Update(mock.Anything, eipProjectID, "eip-id-1", mock.Anything, mock.Anything).Return(buildElasticIPCRUDResponse(statusCode), nil)

				result, err := m.r.HandleReconcile(ctx, eip)
				Expect(err).To(Succeed())
				Expect(result.RequeueAfter).To(Equal(expectedRequeue))

				updated := &v1alpha1.ElasticIP{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), updated)).To(Succeed())
				Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseUpdating))
				cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseUpdating))
				Expect(cond).NotTo(BeNil())
				Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
				Expect(cond.Message).To(ContainSubstring("ERROR"))
			},
			Entry("4xx → LongRequeueAfter, no phase change", "eip-cmp-err-update-400", http.StatusBadRequest, reconciler.LongRequeueAfter),
			Entry("5xx → ShortRequeueAfter, no phase change", "eip-cmp-err-update-500", http.StatusInternalServerError, reconciler.ShortRequeueAfter),
		)

		DescribeTable("CMP delete fails — preserves Deleting+ShallSynchronize, surfaces error in condition",
			func(name string, statusCode int, expectedRequeue time.Duration) {
				m := newEipReconcilerWithMocks(GinkgoT())
				eip = createTestElasticIP(ctx, name, defaultElasticIPSpec(eipProjectName))
				eFetch := &v1alpha1.ElasticIP{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), eFetch)).To(Succeed())
				eFetch.Finalizers = []string{elasticIpFinalizerName}
				Expect(k8sClient.Update(ctx, eFetch)).To(Succeed())
				setElasticIPStatus(ctx, eip, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, "eip-id-1", eipProjectID, 1, time.Now())
				Expect(k8sClient.Delete(ctx, eip)).To(Succeed())
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), eip)).To(Succeed())

				cmpEip := buildElasticIPResponse("eip-id-1", name, reconciler.CSPResourceStateNotUsed)
				m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
				m.mockNetwork.EXPECT().ElasticIPs().Return(m.mockElasticIPs)
				m.mockElasticIPs.EXPECT().List(mock.Anything, eipProjectID, mock.Anything).Return(buildElasticIPList(cmpEip), nil)
				m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
				m.mockNetwork.EXPECT().ElasticIPs().Return(m.mockElasticIPs)
				m.mockElasticIPs.EXPECT().Delete(mock.Anything, eipProjectID, "eip-id-1", mock.Anything).Return(buildDeleteResponse(statusCode), nil)

				result, err := m.r.HandleReconcile(ctx, eip)
				Expect(err).To(Succeed())
				Expect(result.RequeueAfter).To(Equal(expectedRequeue))

				updated := &v1alpha1.ElasticIP{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), updated)).To(Succeed())
				Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
				cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))
				Expect(cond).NotTo(BeNil())
				Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
				Expect(cond.Message).To(ContainSubstring("ERROR"))
			},
			Entry("4xx → LongRequeueAfter, no phase change", "eip-cmp-err-delete-400", http.StatusBadRequest, reconciler.LongRequeueAfter),
			Entry("5xx → ShortRequeueAfter, no phase change", "eip-cmp-err-delete-500", http.StatusInternalServerError, reconciler.ShortRequeueAfter),
		)
	})
})
