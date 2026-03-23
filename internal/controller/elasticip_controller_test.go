package controller

// import (
// 	"context"
// 	"net/http"
// 	"time"

// 	. "github.com/onsi/ginkgo/v2"
// 	. "github.com/onsi/gomega"
// 	"github.com/stretchr/testify/mock"
// 	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
// 	"sigs.k8s.io/controller-runtime/pkg/client"

// 	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
// 	arubamocks "github.com/Arubacloud/arubacloud-resource-operator/internal/mocks/aruba"
// 	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
// 	arubatypes "github.com/Arubacloud/sdk-go/pkg/types"
// )

// // --- Builder helpers ---

// func buildElasticIpResponse(id, name, state string) *arubatypes.ElasticIPResponse {
// 	location := &arubatypes.LocationResponse{Value: "ITBG-Bergamo"}
// 	return &arubatypes.ElasticIPResponse{
// 		Metadata: arubatypes.ResourceMetadataResponse{
// 			ID:               &id,
// 			Name:             &name,
// 			LocationResponse: location,
// 		},
// 		Status: arubatypes.ResourceStatus{
// 			State: &state,
// 		},
// 	}
// }

// func buildElasticIpList(responses ...*arubatypes.ElasticIPResponse) *arubatypes.Response[arubatypes.ElasticList] {
// 	list := &arubatypes.ElasticList{}
// 	for _, r := range responses {
// 		list.Values = append(list.Values, *r)
// 		list.Total++
// 	}
// 	return &arubatypes.Response[arubatypes.ElasticList]{
// 		Data:       list,
// 		StatusCode: http.StatusOK,
// 	}
// }

// func buildElasticIpCRUDResponse(statusCode int) *arubatypes.Response[arubatypes.ElasticIPResponse] {
// 	return &arubatypes.Response[arubatypes.ElasticIPResponse]{
// 		StatusCode: statusCode,
// 	}
// }

// func buildProjectListForElasticIp(projectID, projectName string) *arubatypes.Response[arubatypes.ProjectList] {
// 	id := projectID
// 	name := projectName
// 	proj := arubatypes.ProjectResponse{
// 		Metadata: arubatypes.ResourceMetadataResponse{
// 			ID:   &id,
// 			Name: &name,
// 		},
// 	}
// 	list := &arubatypes.ProjectList{}
// 	list.Values = append(list.Values, proj)
// 	list.Total = 1
// 	return &arubatypes.Response[arubatypes.ProjectList]{
// 		Data:       list,
// 		StatusCode: http.StatusOK,
// 	}
// }

// // --- Test fixture helpers ---

// func defaultElasticIpSpec(projectName string) v1alpha1.ElasticIpSpec {
// 	return v1alpha1.ElasticIpSpec{
// 		Tenant:   "test-tenant",
// 		Location: v1alpha1.Location{Value: "ITBG-Bergamo"},
// 		Tags:     []string{"tag1"},
// 		BillingPlan: v1alpha1.BillingPlan{
// 			BillingPeriod: "Hour",
// 		},
// 		ProjectReference: v1alpha1.ResourceReference{
// 			Name:      projectName,
// 			Namespace: "default",
// 		},
// 	}
// }

// func createTestElasticIp(ctx context.Context, name string, spec v1alpha1.ElasticIpSpec) *v1alpha1.ElasticIp {
// 	eip := &v1alpha1.ElasticIp{
// 		ObjectMeta: metav1.ObjectMeta{
// 			Name:      name,
// 			Namespace: "default",
// 		},
// 		Spec: spec,
// 	}
// 	ExpectWithOffset(1, k8sClient.Create(ctx, eip)).To(Succeed())
// 	return eip
// }

// func setElasticIpStatus(ctx context.Context, eip *v1alpha1.ElasticIp, phase v1alpha1.ResourcePhase, reason string, resourceID string, projectID string, observedGen int64, conditionTime time.Time) {
// 	e := eip.DeepCopy()
// 	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), e)).To(Succeed())
// 	e.Status.Phase = phase
// 	e.Status.ResourceID = resourceID
// 	e.Status.ProjectID = projectID
// 	e.Status.ObservedGeneration = observedGen
// 	if phase != "" {
// 		e.Status.Conditions = []metav1.Condition{
// 			{
// 				Type:               string(phase),
// 				Status:             metav1.ConditionTrue,
// 				Reason:             reason,
// 				LastTransitionTime: metav1.NewTime(conditionTime),
// 				Message:            string(phase) + " " + reason + " - OK",
// 			},
// 		}
// 	}
// 	ExpectWithOffset(1, k8sClient.Status().Update(ctx, e)).To(Succeed())
// 	ExpectWithOffset(1, k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), eip)).To(Succeed())
// }

// // --- Mock struct ---

// type eipMocks struct {
// 	r              *ElasticIpReconciler
// 	mockAruba      *arubamocks.MockClient
// 	mockProject    *arubamocks.MockProjectClient
// 	mockNetwork    *arubamocks.MockNetworkClient
// 	mockElasticIPs *arubamocks.MockElasticIPsClient
// }

// func newEipReconcilerWithMocks(t GinkgoTInterface) *eipMocks {
// 	mockAruba := arubamocks.NewMockClient(t)
// 	mockProject := arubamocks.NewMockProjectClient(t)
// 	mockNetwork := arubamocks.NewMockNetworkClient(t)
// 	mockElasticIPs := arubamocks.NewMockElasticIPsClient(t)

// 	r := NewElasticIpReconciler(&reconciler.Reconciler{
// 		Client:      k8sClient,
// 		Scheme:      k8sClient.Scheme(),
// 		ArubaClient: mockAruba,
// 	})

// 	return &eipMocks{
// 		r:              r,
// 		mockAruba:      mockAruba,
// 		mockProject:    mockProject,
// 		mockNetwork:    mockNetwork,
// 		mockElasticIPs: mockElasticIPs,
// 	}
// }

// func (m *eipMocks) expectProjectList(projectID, projectName string) {
// 	m.mockAruba.EXPECT().FromProject().Return(m.mockProject)
// 	m.mockProject.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectListForElasticIp(projectID, projectName), nil)
// }

// func (m *eipMocks) expectElasticIpList(projectID string, responses ...*arubatypes.ElasticIPResponse) {
// 	m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
// 	m.mockNetwork.EXPECT().ElasticIPs().Return(m.mockElasticIPs)
// 	m.mockElasticIPs.EXPECT().List(mock.Anything, projectID, mock.Anything).Return(buildElasticIpList(responses...), nil)
// }

// // --- Tests ---

// var _ = Describe("ElasticIpReconciler", func() {
// 	const (
// 		eipProjectName = "test-eip-project-ref"
// 		eipProjectID   = "eip-proj-id-1"
// 	)

// 	var (
// 		ctx context.Context
// 		eip *v1alpha1.ElasticIp
// 	)

// 	BeforeEach(func() {
// 		ctx = context.Background()
// 	})

// 	AfterEach(func() {
// 		if eip != nil {
// 			e := &v1alpha1.ElasticIp{}
// 			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), e); err == nil {
// 				e.Finalizers = nil
// 				_ = k8sClient.Update(ctx, e)
// 				_ = k8sClient.Delete(ctx, e)
// 			}
// 			eip = nil
// 		}
// 	})

// 	Describe("First reconciliation", func() {
// 		It("transitions to Creating+ShallSynchronize when CMP has no ElasticIp", func() {
// 			m := newEipReconcilerWithMocks(GinkgoT())
// 			eip = createTestElasticIp(ctx, "test-eip-first", defaultElasticIpSpec(eipProjectName))

// 			m.expectProjectList(eipProjectID, eipProjectName)
// 			m.expectElasticIpList(eipProjectID)

// 			_, err := m.r.HandleReconcile(ctx, eip)
// 			Expect(err).To(Succeed())

// 			updated := &v1alpha1.ElasticIp{}
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), updated)).To(Succeed())
// 			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
// 			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
// 			Expect(cond).NotTo(BeNil())
// 			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
// 		})
// 	})

// 	Describe("Create on CMP", func() {
// 		It("transitions to Creating+Synchronizing after successful CMP create", func() {
// 			m := newEipReconcilerWithMocks(GinkgoT())
// 			eip = createTestElasticIp(ctx, "test-eip-create-cmp", defaultElasticIpSpec(eipProjectName))
// 			setElasticIpStatus(ctx, eip, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, "", "", 0, time.Now())

// 			m.expectProjectList(eipProjectID, eipProjectName)
// 			m.expectElasticIpList(eipProjectID)
// 			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
// 			m.mockNetwork.EXPECT().ElasticIPs().Return(m.mockElasticIPs)
// 			m.mockElasticIPs.EXPECT().Create(mock.Anything, eipProjectID, mock.Anything, mock.Anything).Return(buildElasticIpCRUDResponse(http.StatusCreated), nil)

// 			_, err := m.r.HandleReconcile(ctx, eip)
// 			Expect(err).To(Succeed())

// 			updated := &v1alpha1.ElasticIp{}
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), updated)).To(Succeed())
// 			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
// 			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
// 			Expect(cond).NotTo(BeNil())
// 			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
// 		})
// 	})

// 	Describe("Waiting creation (ElasticIp not yet in CMP)", func() {
// 		It("returns LongRequeue", func() {
// 			m := newEipReconcilerWithMocks(GinkgoT())
// 			eip = createTestElasticIp(ctx, "test-eip-wait-create", defaultElasticIpSpec(eipProjectName))
// 			setElasticIpStatus(ctx, eip, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "", "", 0, time.Now())

// 			m.expectProjectList(eipProjectID, eipProjectName)
// 			m.expectElasticIpList(eipProjectID)

// 			result, err := m.r.HandleReconcile(ctx, eip)
// 			Expect(err).To(Succeed())
// 			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
// 		})
// 	})

// 	Describe("Waiting creation (ElasticIp in transitory CMP state)", func() {
// 		It("returns LongRequeue when CMP state is Creating", func() {
// 			m := newEipReconcilerWithMocks(GinkgoT())
// 			eip = createTestElasticIp(ctx, "test-eip-wait-create-transitory", defaultElasticIpSpec(eipProjectName))
// 			setElasticIpStatus(ctx, eip, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "", "", 0, time.Now())

// 			cmpEip := buildElasticIpResponse("eip-id-1", "test-eip-wait-create-transitory", CSPResourceStateCreating)
// 			m.expectProjectList(eipProjectID, eipProjectName)
// 			m.expectElasticIpList(eipProjectID, cmpEip)

// 			result, err := m.r.HandleReconcile(ctx, eip)
// 			Expect(err).To(Succeed())
// 			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
// 		})
// 	})

// 	Describe("Creation confirmed on CMP", func() {
// 		It("transitions to Creating+Synchronized when CMP ElasticIp is active", func() {
// 			m := newEipReconcilerWithMocks(GinkgoT())
// 			eip = createTestElasticIp(ctx, "test-eip-creation-confirmed", defaultElasticIpSpec(eipProjectName))
// 			setElasticIpStatus(ctx, eip, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "", "", 0, time.Now())

// 			cmpEip := buildElasticIpResponse("eip-id-1", "test-eip-creation-confirmed", CSPResourceStateNotUsed)
// 			m.expectProjectList(eipProjectID, eipProjectName)
// 			m.expectElasticIpList(eipProjectID, cmpEip)

// 			_, err := m.r.HandleReconcile(ctx, eip)
// 			Expect(err).To(Succeed())

// 			updated := &v1alpha1.ElasticIp{}
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), updated)).To(Succeed())
// 			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
// 			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
// 			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronized))
// 		})
// 	})

// 	Describe("Creation accomplished", func() {
// 		It("transitions to Active+Synchronized and sets ResourceID", func() {
// 			m := newEipReconcilerWithMocks(GinkgoT())
// 			eip = createTestElasticIp(ctx, "test-eip-creation-accomplished", defaultElasticIpSpec(eipProjectName))
// 			setElasticIpStatus(ctx, eip, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized, "", "", 0, time.Now())

// 			cmpEip := buildElasticIpResponse("eip-id-1", "test-eip-creation-accomplished", CSPResourceStateNotUsed)
// 			m.expectProjectList(eipProjectID, eipProjectName)
// 			m.expectElasticIpList(eipProjectID, cmpEip)

// 			_, err := m.r.HandleReconcile(ctx, eip)
// 			Expect(err).To(Succeed())

// 			updated := &v1alpha1.ElasticIp{}
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), updated)).To(Succeed())
// 			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseActive))
// 			Expect(updated.Status.ResourceID).To(Equal("eip-id-1"))
// 		})
// 	})

// 	Describe("HasDeniedChanges", func() {
// 		It("returns LongRequeue when immutable field (location) is changed", func() {
// 			m := newEipReconcilerWithMocks(GinkgoT())
// 			eip = createTestElasticIp(ctx, "test-eip-denied-changes", defaultElasticIpSpec(eipProjectName))
// 			setElasticIpStatus(ctx, eip, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "eip-id-1", eipProjectID, 1, time.Now())

// 			// Force generation change with different location
// 			eFetch := &v1alpha1.ElasticIp{}
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), eFetch)).To(Succeed())
// 			eFetch.Spec.Location = v1alpha1.Location{Value: "different-location"}
// 			Expect(k8sClient.Update(ctx, eFetch)).To(Succeed())
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), eip)).To(Succeed())

// 			// CMP still has original location ITBG-Bergamo
// 			cmpEip := buildElasticIpResponse("eip-id-1", "test-eip-denied-changes", CSPResourceStateNotUsed)
// 			m.expectProjectList(eipProjectID, eipProjectName)
// 			m.expectElasticIpList(eipProjectID, cmpEip)

// 			result, err := m.r.HandleReconcile(ctx, eip)
// 			Expect(err).To(Succeed())
// 			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
// 		})
// 	})

// 	Describe("SpecAlreadyInSyncWithCMP", func() {
// 		It("re-stamps ObservedGeneration when spec hasn't actually changed", func() {
// 			m := newEipReconcilerWithMocks(GinkgoT())
// 			eip = createTestElasticIp(ctx, "test-eip-spec-in-sync", defaultElasticIpSpec(eipProjectName))
// 			setElasticIpStatus(ctx, eip, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "eip-id-1", eipProjectID, 1, time.Now())

// 			// Trigger generation bump with same tags
// 			eFetch := &v1alpha1.ElasticIp{}
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), eFetch)).To(Succeed())
// 			eFetch.Spec.Tags = []string{"tag1"}
// 			Expect(k8sClient.Update(ctx, eFetch)).To(Succeed())
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), eip)).To(Succeed())

// 			// CMP matches: same tags, same location, same billing period
// 			cmpEip := buildElasticIpResponse("eip-id-1", "test-eip-spec-in-sync", CSPResourceStateNotUsed)
// 			cmpEip.Metadata.Tags = []string{"tag1"}
// 			cmpEip.Properties.BillingPlan = arubatypes.BillingPeriodResource{BillingPeriod: "Hour"}
// 			m.expectProjectList(eipProjectID, eipProjectName)
// 			m.expectElasticIpList(eipProjectID, cmpEip)

// 			_, err := m.r.HandleReconcile(ctx, eip)
// 			Expect(err).To(Succeed())

// 			updated := &v1alpha1.ElasticIp{}
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), updated)).To(Succeed())
// 			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseActive))
// 			Expect(updated.Status.ObservedGeneration).To(Equal(eip.Generation))
// 		})
// 	})

// 	Describe("ShouldBeUpdated", func() {
// 		It("transitions to Updating+ShallSynchronize when tags differ", func() {
// 			m := newEipReconcilerWithMocks(GinkgoT())
// 			eip = createTestElasticIp(ctx, "test-eip-should-update", defaultElasticIpSpec(eipProjectName))
// 			setElasticIpStatus(ctx, eip, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "eip-id-1", eipProjectID, 1, time.Now())

// 			// Change tags to trigger update
// 			eFetch := &v1alpha1.ElasticIp{}
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), eFetch)).To(Succeed())
// 			eFetch.Spec.Tags = []string{"tag1", "tag2"}
// 			Expect(k8sClient.Update(ctx, eFetch)).To(Succeed())
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), eip)).To(Succeed())

// 			// CMP has old tags
// 			cmpEip := buildElasticIpResponse("eip-id-1", "test-eip-should-update", CSPResourceStateNotUsed)
// 			cmpEip.Metadata.Tags = []string{"tag1"}
// 			cmpEip.Properties.BillingPlan = arubatypes.BillingPeriodResource{BillingPeriod: "Hour"}
// 			m.expectProjectList(eipProjectID, eipProjectName)
// 			m.expectElasticIpList(eipProjectID, cmpEip)

// 			_, err := m.r.HandleReconcile(ctx, eip)
// 			Expect(err).To(Succeed())

// 			updated := &v1alpha1.ElasticIp{}
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), updated)).To(Succeed())
// 			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseUpdating))
// 			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseUpdating))
// 			Expect(cond).NotTo(BeNil())
// 			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
// 		})
// 	})

// 	Describe("Update on CMP", func() {
// 		It("transitions to Updating+Synchronizing after successful CMP update", func() {
// 			m := newEipReconcilerWithMocks(GinkgoT())
// 			eip = createTestElasticIp(ctx, "test-eip-update-cmp", defaultElasticIpSpec(eipProjectName))
// 			setElasticIpStatus(ctx, eip, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize, "eip-id-1", eipProjectID, 1, time.Now())

// 			cmpEip := buildElasticIpResponse("eip-id-1", "test-eip-update-cmp", CSPResourceStateNotUsed)
// 			m.expectProjectList(eipProjectID, eipProjectName)
// 			m.expectElasticIpList(eipProjectID, cmpEip)
// 			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
// 			m.mockNetwork.EXPECT().ElasticIPs().Return(m.mockElasticIPs)
// 			m.mockElasticIPs.EXPECT().Update(mock.Anything, eipProjectID, "eip-id-1", mock.Anything, mock.Anything).Return(buildElasticIpCRUDResponse(http.StatusOK), nil)

// 			_, err := m.r.HandleReconcile(ctx, eip)
// 			Expect(err).To(Succeed())

// 			updated := &v1alpha1.ElasticIp{}
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), updated)).To(Succeed())
// 			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseUpdating))
// 			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseUpdating))
// 			Expect(cond).NotTo(BeNil())
// 			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
// 		})
// 	})

// 	Describe("Should delete", func() {
// 		It("transitions to Deleting+ShallSynchronize when deletion is requested on Active ElasticIp", func() {
// 			m := newEipReconcilerWithMocks(GinkgoT())
// 			eip = createTestElasticIp(ctx, "test-eip-should-delete", defaultElasticIpSpec(eipProjectName))
// 			eFetch := &v1alpha1.ElasticIp{}
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), eFetch)).To(Succeed())
// 			eFetch.Finalizers = []string{elasticIpFinalizerName}
// 			Expect(k8sClient.Update(ctx, eFetch)).To(Succeed())
// 			setElasticIpStatus(ctx, eip, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "eip-id-1", eipProjectID, 1, time.Now())
// 			Expect(k8sClient.Delete(ctx, eip)).To(Succeed())
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), eip)).To(Succeed())

// 			cmpEip := buildElasticIpResponse("eip-id-1", "test-eip-should-delete", CSPResourceStateNotUsed)
// 			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
// 			m.mockNetwork.EXPECT().ElasticIPs().Return(m.mockElasticIPs)
// 			m.mockElasticIPs.EXPECT().List(mock.Anything, eipProjectID, mock.Anything).Return(buildElasticIpList(cmpEip), nil)

// 			_, err := m.r.HandleReconcile(ctx, eip)
// 			Expect(err).To(Succeed())

// 			updated := &v1alpha1.ElasticIp{}
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), updated)).To(Succeed())
// 			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
// 			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))
// 			Expect(cond).NotTo(BeNil())
// 			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
// 		})
// 	})

// 	Describe("Delete on CMP", func() {
// 		It("transitions to Deleting+Synchronizing after successful CMP delete", func() {
// 			m := newEipReconcilerWithMocks(GinkgoT())
// 			eip = createTestElasticIp(ctx, "test-eip-delete-cmp", defaultElasticIpSpec(eipProjectName))
// 			eFetch := &v1alpha1.ElasticIp{}
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), eFetch)).To(Succeed())
// 			eFetch.Finalizers = []string{elasticIpFinalizerName}
// 			Expect(k8sClient.Update(ctx, eFetch)).To(Succeed())
// 			setElasticIpStatus(ctx, eip, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, "eip-id-1", eipProjectID, 1, time.Now())
// 			Expect(k8sClient.Delete(ctx, eip)).To(Succeed())
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), eip)).To(Succeed())

// 			cmpEip := buildElasticIpResponse("eip-id-1", "test-eip-delete-cmp", CSPResourceStateNotUsed)
// 			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
// 			m.mockNetwork.EXPECT().ElasticIPs().Return(m.mockElasticIPs)
// 			m.mockElasticIPs.EXPECT().List(mock.Anything, eipProjectID, mock.Anything).Return(buildElasticIpList(cmpEip), nil)
// 			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
// 			m.mockNetwork.EXPECT().ElasticIPs().Return(m.mockElasticIPs)
// 			m.mockElasticIPs.EXPECT().Delete(mock.Anything, eipProjectID, "eip-id-1", mock.Anything).Return(buildDeleteResponse(http.StatusOK), nil)

// 			_, err := m.r.HandleReconcile(ctx, eip)
// 			Expect(err).To(Succeed())

// 			updated := &v1alpha1.ElasticIp{}
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), updated)).To(Succeed())
// 			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
// 			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))
// 			Expect(cond).NotTo(BeNil())
// 			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
// 		})
// 	})

// 	Describe("CMP transitory during deletion", func() {
// 		It("returns LongRequeue when CMP state is Deleting", func() {
// 			m := newEipReconcilerWithMocks(GinkgoT())
// 			eip = createTestElasticIp(ctx, "test-eip-deleting-transitory", defaultElasticIpSpec(eipProjectName))
// 			eFetch := &v1alpha1.ElasticIp{}
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), eFetch)).To(Succeed())
// 			eFetch.Finalizers = []string{elasticIpFinalizerName}
// 			Expect(k8sClient.Update(ctx, eFetch)).To(Succeed())
// 			setElasticIpStatus(ctx, eip, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronizing, "eip-id-1", eipProjectID, 1, time.Now())
// 			Expect(k8sClient.Delete(ctx, eip)).To(Succeed())
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), eip)).To(Succeed())

// 			cmpEip := buildElasticIpResponse("eip-id-1", "test-eip-deleting-transitory", CSPResourceStateDeleting)
// 			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
// 			m.mockNetwork.EXPECT().ElasticIPs().Return(m.mockElasticIPs)
// 			m.mockElasticIPs.EXPECT().List(mock.Anything, eipProjectID, mock.Anything).Return(buildElasticIpList(cmpEip), nil)

// 			result, err := m.r.HandleReconcile(ctx, eip)
// 			Expect(err).To(Succeed())
// 			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
// 		})
// 	})

// 	Describe("Deletion accomplished", func() {
// 		It("transitions to Deleted phase when CMP ElasticIp is gone", func() {
// 			m := newEipReconcilerWithMocks(GinkgoT())
// 			eip = createTestElasticIp(ctx, "test-eip-deletion-accomplished", defaultElasticIpSpec(eipProjectName))
// 			eFetch := &v1alpha1.ElasticIp{}
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), eFetch)).To(Succeed())
// 			eFetch.Finalizers = []string{elasticIpFinalizerName}
// 			Expect(k8sClient.Update(ctx, eFetch)).To(Succeed())
// 			setElasticIpStatus(ctx, eip, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronized, "eip-id-1", eipProjectID, 1, time.Now())
// 			Expect(k8sClient.Delete(ctx, eip)).To(Succeed())
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), eip)).To(Succeed())

// 			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
// 			m.mockNetwork.EXPECT().ElasticIPs().Return(m.mockElasticIPs)
// 			m.mockElasticIPs.EXPECT().List(mock.Anything, eipProjectID, mock.Anything).Return(buildElasticIpList(), nil)

// 			_, err := m.r.HandleReconcile(ctx, eip)
// 			Expect(err).To(Succeed())

// 			updated := &v1alpha1.ElasticIp{}
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), updated)).To(Succeed())
// 			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleted))
// 		})
// 	})

// 	Describe("IsInError", func() {
// 		It("transitions to Failed+Synchronized when CMP state is Failed", func() {
// 			m := newEipReconcilerWithMocks(GinkgoT())
// 			eip = createTestElasticIp(ctx, "test-eip-in-error", defaultElasticIpSpec(eipProjectName))
// 			setElasticIpStatus(ctx, eip, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "eip-id-1", eipProjectID, 1, time.Now())

// 			cmpEip := buildElasticIpResponse("eip-id-1", "test-eip-in-error", CSPResourceStateFailed)
// 			m.expectProjectList(eipProjectID, eipProjectName)
// 			m.expectElasticIpList(eipProjectID, cmpEip)

// 			_, err := m.r.HandleReconcile(ctx, eip)
// 			Expect(err).To(Succeed())

// 			updated := &v1alpha1.ElasticIp{}
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), updated)).To(Succeed())
// 			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
// 			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseFailed))
// 			Expect(cond).NotTo(BeNil())
// 			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronized))
// 		})
// 	})

// 	Describe("Phase timeout", func() {
// 		It("transitions to Failed when stuck in transitory phase too long", func() {
// 			m := newEipReconcilerWithMocks(GinkgoT())
// 			eip = createTestElasticIp(ctx, "test-eip-timeout", defaultElasticIpSpec(eipProjectName))
// 			setElasticIpStatus(ctx, eip, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, "", eipProjectID,
// 				0, time.Now().Add(-(reconciler.MaxPhaseTimeout + time.Minute)))

// 			cmpEip := buildElasticIpResponse("eip-id-1", "test-eip-timeout", CSPResourceStateNotUsed)
// 			m.expectProjectList(eipProjectID, eipProjectName)
// 			m.expectElasticIpList(eipProjectID, cmpEip)

// 			_, err := m.r.HandleReconcile(ctx, eip)
// 			Expect(err).To(Succeed())

// 			updated := &v1alpha1.ElasticIp{}
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), updated)).To(Succeed())
// 			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
// 		})
// 	})

// 	Describe("Project not found yet", func() {
// 		It("returns LongRequeue when project doesn't exist in CMP yet", func() {
// 			m := newEipReconcilerWithMocks(GinkgoT())
// 			eip = createTestElasticIp(ctx, "test-eip-no-project", defaultElasticIpSpec(eipProjectName))

// 			m.mockAruba.EXPECT().FromProject().Return(m.mockProject)
// 			m.mockProject.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectList(), nil)

// 			result, err := m.r.HandleReconcile(ctx, eip)
// 			Expect(err).To(Succeed())
// 			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
// 		})
// 	})

// 	Describe("ProjectID set in status via prePatch callback", func() {
// 		It("stamps ProjectID on status when first transitioning", func() {
// 			m := newEipReconcilerWithMocks(GinkgoT())
// 			eip = createTestElasticIp(ctx, "test-eip-project-id", defaultElasticIpSpec(eipProjectName))

// 			m.expectProjectList(eipProjectID, eipProjectName)
// 			m.expectElasticIpList(eipProjectID)

// 			_, err := m.r.HandleReconcile(ctx, eip)
// 			Expect(err).To(Succeed())

// 			updated := &v1alpha1.ElasticIp{}
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), updated)).To(Succeed())
// 			Expect(updated.Status.ProjectID).To(Equal(eipProjectID))
// 		})
// 	})
// })
