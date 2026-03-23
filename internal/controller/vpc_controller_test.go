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

// func buildVpcResponse(id, name, state string) *arubatypes.VPCResponse {
// 	location := &arubatypes.LocationResponse{Value: "ITBG-Bergamo"}
// 	return &arubatypes.VPCResponse{
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

// func buildVpcList(responses ...*arubatypes.VPCResponse) *arubatypes.Response[arubatypes.VPCList] {
// 	list := &arubatypes.VPCList{}
// 	for _, r := range responses {
// 		list.Values = append(list.Values, *r)
// 		list.Total++
// 	}
// 	return &arubatypes.Response[arubatypes.VPCList]{
// 		Data:       list,
// 		StatusCode: http.StatusOK,
// 	}
// }

// func buildVpcCRUDResponse(statusCode int) *arubatypes.Response[arubatypes.VPCResponse] {
// 	return &arubatypes.Response[arubatypes.VPCResponse]{
// 		StatusCode: statusCode,
// 	}
// }

// func buildProjectListForVpc(projectID, projectName string) *arubatypes.Response[arubatypes.ProjectList] {
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

// func defaultVpcSpec(projectName string) v1alpha1.VpcSpec {
// 	return v1alpha1.VpcSpec{
// 		Tenant:   "test-tenant",
// 		Location: v1alpha1.Location{Value: "ITBG-Bergamo"},
// 		Tags:     []string{"tag1"},
// 		ProjectReference: v1alpha1.ResourceReference{
// 			Name:      projectName,
// 			Namespace: "default",
// 		},
// 	}
// }

// func createTestVpc(ctx context.Context, name string, spec v1alpha1.VpcSpec) *v1alpha1.Vpc {
// 	vpc := &v1alpha1.Vpc{
// 		ObjectMeta: metav1.ObjectMeta{
// 			Name:      name,
// 			Namespace: "default",
// 		},
// 		Spec: spec,
// 	}
// 	ExpectWithOffset(1, k8sClient.Create(ctx, vpc)).To(Succeed())
// 	return vpc
// }

// func setVpcStatus(ctx context.Context, vpc *v1alpha1.Vpc, phase v1alpha1.ResourcePhase, reason string, resourceID string, projectID string, observedGen int64, conditionTime time.Time) {
// 	v := vpc.DeepCopy()
// 	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), v)).To(Succeed())
// 	v.Status.Phase = phase
// 	v.Status.ResourceID = resourceID
// 	v.Status.ProjectID = projectID
// 	v.Status.ObservedGeneration = observedGen
// 	if phase != "" {
// 		v.Status.Conditions = []metav1.Condition{
// 			{
// 				Type:               string(phase),
// 				Status:             metav1.ConditionTrue,
// 				Reason:             reason,
// 				LastTransitionTime: metav1.NewTime(conditionTime),
// 				Message:            string(phase) + " " + reason + " - OK",
// 			},
// 		}
// 	}
// 	ExpectWithOffset(1, k8sClient.Status().Update(ctx, v)).To(Succeed())
// 	ExpectWithOffset(1, k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), vpc)).To(Succeed())
// }

// type vpcMocks struct {
// 	r              *VpcReconciler
// 	mockAruba      *arubamocks.MockClient
// 	mockProject    *arubamocks.MockProjectClient
// 	mockNetwork    *arubamocks.MockNetworkClient
// 	mockVPCsClient *arubamocks.MockVPCsClient
// }

// func newVpcReconcilerWithMocks(t GinkgoTInterface) *vpcMocks {
// 	mockAruba := arubamocks.NewMockClient(t)
// 	mockProject := arubamocks.NewMockProjectClient(t)
// 	mockNetwork := arubamocks.NewMockNetworkClient(t)
// 	mockVPCsClient := arubamocks.NewMockVPCsClient(t)

// 	r := NewVpcReconciler(&reconciler.Reconciler{
// 		Client:      k8sClient,
// 		Scheme:      k8sClient.Scheme(),
// 		ArubaClient: mockAruba,
// 	})

// 	return &vpcMocks{
// 		r:              r,
// 		mockAruba:      mockAruba,
// 		mockProject:    mockProject,
// 		mockNetwork:    mockNetwork,
// 		mockVPCsClient: mockVPCsClient,
// 	}
// }

// func (m *vpcMocks) expectProjectList(projectID, projectName string) {
// 	m.mockAruba.EXPECT().FromProject().Return(m.mockProject)
// 	m.mockProject.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectListForVpc(projectID, projectName), nil)
// }

// func (m *vpcMocks) expectVpcList(projectID string, responses ...*arubatypes.VPCResponse) {
// 	m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
// 	m.mockNetwork.EXPECT().VPCs().Return(m.mockVPCsClient)
// 	m.mockVPCsClient.EXPECT().List(mock.Anything, projectID, mock.Anything).Return(buildVpcList(responses...), nil)
// }

// // --- Tests ---

// var _ = Describe("VpcReconciler", func() {
// 	const (
// 		vpcProjectName = "test-vpc-project-ref"
// 		vpcProjectID   = "vpc-proj-id-1"
// 	)

// 	var (
// 		ctx context.Context
// 		vpc *v1alpha1.Vpc
// 	)

// 	BeforeEach(func() {
// 		ctx = context.Background()
// 	})

// 	AfterEach(func() {
// 		if vpc != nil {
// 			v := &v1alpha1.Vpc{}
// 			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), v); err == nil {
// 				v.Finalizers = nil
// 				_ = k8sClient.Update(ctx, v)
// 				_ = k8sClient.Delete(ctx, v)
// 			}
// 			vpc = nil
// 		}
// 	})

// 	Describe("First reconciliation", func() {
// 		It("transitions to Creating+ShallSynchronize when CMP has no VPC", func() {
// 			m := newVpcReconcilerWithMocks(GinkgoT())
// 			vpc = createTestVpc(ctx, "test-vpc-first", defaultVpcSpec(vpcProjectName))

// 			m.expectProjectList(vpcProjectID, vpcProjectName)
// 			m.expectVpcList(vpcProjectID)

// 			_, err := m.r.HandleReconcile(ctx, vpc)
// 			Expect(err).To(Succeed())

// 			updated := &v1alpha1.Vpc{}
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), updated)).To(Succeed())
// 			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
// 			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
// 			Expect(cond).NotTo(BeNil())
// 			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
// 		})
// 	})

// 	Describe("Create on CMP", func() {
// 		It("transitions to Creating+Synchronizing after successful CMP create", func() {
// 			m := newVpcReconcilerWithMocks(GinkgoT())
// 			vpc = createTestVpc(ctx, "test-vpc-create-cmp", defaultVpcSpec(vpcProjectName))
// 			setVpcStatus(ctx, vpc, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, "", "", 0, time.Now())

// 			m.expectProjectList(vpcProjectID, vpcProjectName)
// 			m.expectVpcList(vpcProjectID)
// 			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
// 			m.mockNetwork.EXPECT().VPCs().Return(m.mockVPCsClient)
// 			m.mockVPCsClient.EXPECT().Create(mock.Anything, vpcProjectID, mock.Anything, mock.Anything).Return(buildVpcCRUDResponse(http.StatusCreated), nil)

// 			_, err := m.r.HandleReconcile(ctx, vpc)
// 			Expect(err).To(Succeed())

// 			updated := &v1alpha1.Vpc{}
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), updated)).To(Succeed())
// 			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
// 			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
// 			Expect(cond).NotTo(BeNil())
// 			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
// 		})
// 	})

// 	Describe("Waiting creation (VPC not yet in CMP)", func() {
// 		It("returns LongRequeue", func() {
// 			m := newVpcReconcilerWithMocks(GinkgoT())
// 			vpc = createTestVpc(ctx, "test-vpc-wait-create", defaultVpcSpec(vpcProjectName))
// 			setVpcStatus(ctx, vpc, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "", "", 0, time.Now())

// 			m.expectProjectList(vpcProjectID, vpcProjectName)
// 			m.expectVpcList(vpcProjectID)

// 			result, err := m.r.HandleReconcile(ctx, vpc)
// 			Expect(err).To(Succeed())
// 			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
// 		})
// 	})

// 	Describe("Waiting creation (VPC in transitory CMP state)", func() {
// 		It("returns LongRequeue when CMP state is Creating", func() {
// 			m := newVpcReconcilerWithMocks(GinkgoT())
// 			vpc = createTestVpc(ctx, "test-vpc-wait-create-transitory", defaultVpcSpec(vpcProjectName))
// 			setVpcStatus(ctx, vpc, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "", "", 0, time.Now())

// 			cmpVpc := buildVpcResponse("vpc-id-1", "test-vpc-wait-create-transitory", CSPResourceStateCreating)
// 			m.expectProjectList(vpcProjectID, vpcProjectName)
// 			m.expectVpcList(vpcProjectID, cmpVpc)

// 			result, err := m.r.HandleReconcile(ctx, vpc)
// 			Expect(err).To(Succeed())
// 			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
// 		})
// 	})

// 	Describe("Creation confirmed on CMP", func() {
// 		It("transitions to Creating+Synchronized when CMP VPC is active", func() {
// 			m := newVpcReconcilerWithMocks(GinkgoT())
// 			vpc = createTestVpc(ctx, "test-vpc-creation-confirmed", defaultVpcSpec(vpcProjectName))
// 			setVpcStatus(ctx, vpc, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "", "", 0, time.Now())

// 			cmpVpc := buildVpcResponse("vpc-id-1", "test-vpc-creation-confirmed", CSPResourceStateActive)
// 			m.expectProjectList(vpcProjectID, vpcProjectName)
// 			m.expectVpcList(vpcProjectID, cmpVpc)

// 			_, err := m.r.HandleReconcile(ctx, vpc)
// 			Expect(err).To(Succeed())

// 			updated := &v1alpha1.Vpc{}
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), updated)).To(Succeed())
// 			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
// 			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
// 			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronized))
// 		})
// 	})

// 	Describe("Creation accomplished", func() {
// 		It("transitions to Active+Synchronized and sets ResourceID", func() {
// 			m := newVpcReconcilerWithMocks(GinkgoT())
// 			vpc = createTestVpc(ctx, "test-vpc-creation-accomplished", defaultVpcSpec(vpcProjectName))
// 			setVpcStatus(ctx, vpc, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized, "", "", 0, time.Now())

// 			cmpVpc := buildVpcResponse("vpc-id-1", "test-vpc-creation-accomplished", CSPResourceStateActive)
// 			m.expectProjectList(vpcProjectID, vpcProjectName)
// 			m.expectVpcList(vpcProjectID, cmpVpc)

// 			_, err := m.r.HandleReconcile(ctx, vpc)
// 			Expect(err).To(Succeed())

// 			updated := &v1alpha1.Vpc{}
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), updated)).To(Succeed())
// 			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseActive))
// 			Expect(updated.Status.ResourceID).To(Equal("vpc-id-1"))
// 		})
// 	})

// 	Describe("HasDeniedChanges", func() {
// 		It("returns LongRequeue when immutable field (location) is changed", func() {
// 			m := newVpcReconcilerWithMocks(GinkgoT())
// 			vpc = createTestVpc(ctx, "test-vpc-denied-changes", defaultVpcSpec(vpcProjectName))
// 			setVpcStatus(ctx, vpc, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "vpc-id-1", vpcProjectID, 1, time.Now())

// 			// Force generation change with different location
// 			vFetch := &v1alpha1.Vpc{}
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), vFetch)).To(Succeed())
// 			vFetch.Spec.Location = v1alpha1.Location{Value: "different-location"}
// 			Expect(k8sClient.Update(ctx, vFetch)).To(Succeed())
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), vpc)).To(Succeed())

// 			// CMP still has original location ITBG-Bergamo
// 			cmpVpc := buildVpcResponse("vpc-id-1", "test-vpc-denied-changes", CSPResourceStateActive)
// 			m.expectProjectList(vpcProjectID, vpcProjectName)
// 			m.expectVpcList(vpcProjectID, cmpVpc)

// 			result, err := m.r.HandleReconcile(ctx, vpc)
// 			Expect(err).To(Succeed())
// 			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
// 		})
// 	})

// 	Describe("SpecAlreadyInSyncWithCMP", func() {
// 		It("re-stamps ObservedGeneration when spec hasn't actually changed", func() {
// 			m := newVpcReconcilerWithMocks(GinkgoT())
// 			vpc = createTestVpc(ctx, "test-vpc-spec-in-sync", defaultVpcSpec(vpcProjectName))
// 			setVpcStatus(ctx, vpc, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "vpc-id-1", vpcProjectID, 1, time.Now())

// 			// Trigger generation bump with same tags
// 			vFetch := &v1alpha1.Vpc{}
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), vFetch)).To(Succeed())
// 			vFetch.Spec.Tags = []string{"tag1"}
// 			Expect(k8sClient.Update(ctx, vFetch)).To(Succeed())
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), vpc)).To(Succeed())

// 			// CMP matches: same tags, same location
// 			cmpVpc := buildVpcResponse("vpc-id-1", "test-vpc-spec-in-sync", CSPResourceStateActive)
// 			cmpVpc.Metadata.Tags = []string{"tag1"}
// 			m.expectProjectList(vpcProjectID, vpcProjectName)
// 			m.expectVpcList(vpcProjectID, cmpVpc)

// 			_, err := m.r.HandleReconcile(ctx, vpc)
// 			Expect(err).To(Succeed())

// 			updated := &v1alpha1.Vpc{}
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), updated)).To(Succeed())
// 			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseActive))
// 			Expect(updated.Status.ObservedGeneration).To(Equal(vpc.Generation))
// 		})
// 	})

// 	Describe("ShouldBeUpdated", func() {
// 		It("transitions to Updating+ShallSynchronize when tags differ", func() {
// 			m := newVpcReconcilerWithMocks(GinkgoT())
// 			vpc = createTestVpc(ctx, "test-vpc-should-update", defaultVpcSpec(vpcProjectName))
// 			setVpcStatus(ctx, vpc, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "vpc-id-1", vpcProjectID, 1, time.Now())

// 			// Change tags to trigger update
// 			vFetch := &v1alpha1.Vpc{}
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), vFetch)).To(Succeed())
// 			vFetch.Spec.Tags = []string{"tag1", "tag2"}
// 			Expect(k8sClient.Update(ctx, vFetch)).To(Succeed())
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), vpc)).To(Succeed())

// 			// CMP has old tags
// 			cmpVpc := buildVpcResponse("vpc-id-1", "test-vpc-should-update", CSPResourceStateActive)
// 			cmpVpc.Metadata.Tags = []string{"tag1"}
// 			m.expectProjectList(vpcProjectID, vpcProjectName)
// 			m.expectVpcList(vpcProjectID, cmpVpc)

// 			_, err := m.r.HandleReconcile(ctx, vpc)
// 			Expect(err).To(Succeed())

// 			updated := &v1alpha1.Vpc{}
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), updated)).To(Succeed())
// 			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseUpdating))
// 			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseUpdating))
// 			Expect(cond).NotTo(BeNil())
// 			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
// 		})
// 	})

// 	Describe("Update on CMP", func() {
// 		It("transitions to Updating+Synchronizing after successful CMP update", func() {
// 			m := newVpcReconcilerWithMocks(GinkgoT())
// 			vpc = createTestVpc(ctx, "test-vpc-update-cmp", defaultVpcSpec(vpcProjectName))
// 			setVpcStatus(ctx, vpc, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize, "vpc-id-1", vpcProjectID, 1, time.Now())

// 			cmpVpc := buildVpcResponse("vpc-id-1", "test-vpc-update-cmp", CSPResourceStateActive)
// 			m.expectProjectList(vpcProjectID, vpcProjectName)
// 			m.expectVpcList(vpcProjectID, cmpVpc)
// 			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
// 			m.mockNetwork.EXPECT().VPCs().Return(m.mockVPCsClient)
// 			m.mockVPCsClient.EXPECT().Update(mock.Anything, vpcProjectID, "vpc-id-1", mock.Anything, mock.Anything).Return(buildVpcCRUDResponse(http.StatusOK), nil)

// 			_, err := m.r.HandleReconcile(ctx, vpc)
// 			Expect(err).To(Succeed())

// 			updated := &v1alpha1.Vpc{}
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), updated)).To(Succeed())
// 			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseUpdating))
// 			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseUpdating))
// 			Expect(cond).NotTo(BeNil())
// 			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
// 		})
// 	})

// 	Describe("Should delete", func() {
// 		It("transitions to Deleting+ShallSynchronize when deletion is requested on Active VPC", func() {
// 			m := newVpcReconcilerWithMocks(GinkgoT())
// 			vpc = createTestVpc(ctx, "test-vpc-should-delete", defaultVpcSpec(vpcProjectName))
// 			vFetch := &v1alpha1.Vpc{}
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), vFetch)).To(Succeed())
// 			vFetch.Finalizers = []string{vpcFinalizerName}
// 			Expect(k8sClient.Update(ctx, vFetch)).To(Succeed())
// 			setVpcStatus(ctx, vpc, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "vpc-id-1", vpcProjectID, 1, time.Now())
// 			Expect(k8sClient.Delete(ctx, vpc)).To(Succeed())
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), vpc)).To(Succeed())

// 			cmpVpc := buildVpcResponse("vpc-id-1", "test-vpc-should-delete", CSPResourceStateActive)
// 			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
// 			m.mockNetwork.EXPECT().VPCs().Return(m.mockVPCsClient)
// 			m.mockVPCsClient.EXPECT().List(mock.Anything, vpcProjectID, mock.Anything).Return(buildVpcList(cmpVpc), nil)

// 			_, err := m.r.HandleReconcile(ctx, vpc)
// 			Expect(err).To(Succeed())

// 			updated := &v1alpha1.Vpc{}
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), updated)).To(Succeed())
// 			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
// 			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))
// 			Expect(cond).NotTo(BeNil())
// 			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
// 		})
// 	})

// 	Describe("Delete on CMP", func() {
// 		It("transitions to Deleting+Synchronizing after successful CMP delete", func() {
// 			m := newVpcReconcilerWithMocks(GinkgoT())
// 			vpc = createTestVpc(ctx, "test-vpc-delete-cmp", defaultVpcSpec(vpcProjectName))
// 			vFetch := &v1alpha1.Vpc{}
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), vFetch)).To(Succeed())
// 			vFetch.Finalizers = []string{vpcFinalizerName}
// 			Expect(k8sClient.Update(ctx, vFetch)).To(Succeed())
// 			setVpcStatus(ctx, vpc, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, "vpc-id-1", vpcProjectID, 1, time.Now())
// 			Expect(k8sClient.Delete(ctx, vpc)).To(Succeed())
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), vpc)).To(Succeed())

// 			cmpVpc := buildVpcResponse("vpc-id-1", "test-vpc-delete-cmp", CSPResourceStateActive)
// 			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
// 			m.mockNetwork.EXPECT().VPCs().Return(m.mockVPCsClient)
// 			m.mockVPCsClient.EXPECT().List(mock.Anything, vpcProjectID, mock.Anything).Return(buildVpcList(cmpVpc), nil)
// 			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
// 			m.mockNetwork.EXPECT().VPCs().Return(m.mockVPCsClient)
// 			m.mockVPCsClient.EXPECT().Delete(mock.Anything, vpcProjectID, "vpc-id-1", mock.Anything).Return(buildDeleteResponse(http.StatusOK), nil)

// 			_, err := m.r.HandleReconcile(ctx, vpc)
// 			Expect(err).To(Succeed())

// 			updated := &v1alpha1.Vpc{}
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), updated)).To(Succeed())
// 			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
// 			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))
// 			Expect(cond).NotTo(BeNil())
// 			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
// 		})
// 	})

// 	Describe("CMP transitory during deletion", func() {
// 		It("returns LongRequeue when CMP state is Deleting", func() {
// 			m := newVpcReconcilerWithMocks(GinkgoT())
// 			vpc = createTestVpc(ctx, "test-vpc-deleting-transitory", defaultVpcSpec(vpcProjectName))
// 			vFetch := &v1alpha1.Vpc{}
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), vFetch)).To(Succeed())
// 			vFetch.Finalizers = []string{vpcFinalizerName}
// 			Expect(k8sClient.Update(ctx, vFetch)).To(Succeed())
// 			setVpcStatus(ctx, vpc, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronizing, "vpc-id-1", vpcProjectID, 1, time.Now())
// 			Expect(k8sClient.Delete(ctx, vpc)).To(Succeed())
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), vpc)).To(Succeed())

// 			cmpVpc := buildVpcResponse("vpc-id-1", "test-vpc-deleting-transitory", CSPResourceStateDeleting)
// 			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
// 			m.mockNetwork.EXPECT().VPCs().Return(m.mockVPCsClient)
// 			m.mockVPCsClient.EXPECT().List(mock.Anything, vpcProjectID, mock.Anything).Return(buildVpcList(cmpVpc), nil)

// 			result, err := m.r.HandleReconcile(ctx, vpc)
// 			Expect(err).To(Succeed())
// 			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
// 		})
// 	})

// 	Describe("Deletion accomplished", func() {
// 		It("transitions to Deleted phase when CMP VPC is gone", func() {
// 			m := newVpcReconcilerWithMocks(GinkgoT())
// 			vpc = createTestVpc(ctx, "test-vpc-deletion-accomplished", defaultVpcSpec(vpcProjectName))
// 			vFetch := &v1alpha1.Vpc{}
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), vFetch)).To(Succeed())
// 			vFetch.Finalizers = []string{vpcFinalizerName}
// 			Expect(k8sClient.Update(ctx, vFetch)).To(Succeed())
// 			setVpcStatus(ctx, vpc, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronized, "vpc-id-1", vpcProjectID, 1, time.Now())
// 			Expect(k8sClient.Delete(ctx, vpc)).To(Succeed())
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), vpc)).To(Succeed())

// 			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
// 			m.mockNetwork.EXPECT().VPCs().Return(m.mockVPCsClient)
// 			m.mockVPCsClient.EXPECT().List(mock.Anything, vpcProjectID, mock.Anything).Return(buildVpcList(), nil)

// 			_, err := m.r.HandleReconcile(ctx, vpc)
// 			Expect(err).To(Succeed())

// 			updated := &v1alpha1.Vpc{}
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), updated)).To(Succeed())
// 			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleted))
// 		})
// 	})

// 	Describe("IsInError", func() {
// 		It("transitions to Failed+Synchronized when CMP state is Failed", func() {
// 			m := newVpcReconcilerWithMocks(GinkgoT())
// 			vpc = createTestVpc(ctx, "test-vpc-in-error", defaultVpcSpec(vpcProjectName))
// 			setVpcStatus(ctx, vpc, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "vpc-id-1", vpcProjectID, 1, time.Now())

// 			cmpVpc := buildVpcResponse("vpc-id-1", "test-vpc-in-error", CSPResourceStateFailed)
// 			m.expectProjectList(vpcProjectID, vpcProjectName)
// 			m.expectVpcList(vpcProjectID, cmpVpc)

// 			_, err := m.r.HandleReconcile(ctx, vpc)
// 			Expect(err).To(Succeed())

// 			updated := &v1alpha1.Vpc{}
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), updated)).To(Succeed())
// 			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
// 			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseFailed))
// 			Expect(cond).NotTo(BeNil())
// 			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronized))
// 		})
// 	})

// 	Describe("Phase timeout", func() {
// 		It("transitions to Failed when stuck in transitory phase too long", func() {
// 			m := newVpcReconcilerWithMocks(GinkgoT())
// 			vpc = createTestVpc(ctx, "test-vpc-timeout", defaultVpcSpec(vpcProjectName))
// 			setVpcStatus(ctx, vpc, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, "", vpcProjectID,
// 				0, time.Now().Add(-(reconciler.MaxPhaseTimeout + time.Minute)))

// 			cmpVpc := buildVpcResponse("vpc-id-1", "test-vpc-timeout", CSPResourceStateActive)
// 			m.expectProjectList(vpcProjectID, vpcProjectName)
// 			m.expectVpcList(vpcProjectID, cmpVpc)

// 			_, err := m.r.HandleReconcile(ctx, vpc)
// 			Expect(err).To(Succeed())

// 			updated := &v1alpha1.Vpc{}
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), updated)).To(Succeed())
// 			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
// 		})
// 	})

// 	Describe("Project not found yet", func() {
// 		It("returns LongRequeue when project doesn't exist in CMP yet", func() {
// 			m := newVpcReconcilerWithMocks(GinkgoT())
// 			vpc = createTestVpc(ctx, "test-vpc-no-project", defaultVpcSpec(vpcProjectName))

// 			m.mockAruba.EXPECT().FromProject().Return(m.mockProject)
// 			m.mockProject.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectList(), nil)

// 			result, err := m.r.HandleReconcile(ctx, vpc)
// 			Expect(err).To(Succeed())
// 			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
// 		})
// 	})

// 	Describe("ProjectID set in status via prePatch callback", func() {
// 		It("stamps ProjectID on status when first transitioning", func() {
// 			m := newVpcReconcilerWithMocks(GinkgoT())
// 			vpc = createTestVpc(ctx, "test-vpc-project-id", defaultVpcSpec(vpcProjectName))

// 			m.expectProjectList(vpcProjectID, vpcProjectName)
// 			m.expectVpcList(vpcProjectID)

// 			_, err := m.r.HandleReconcile(ctx, vpc)
// 			Expect(err).To(Succeed())

// 			updated := &v1alpha1.Vpc{}
// 			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), updated)).To(Succeed())
// 			Expect(updated.Status.ProjectID).To(Equal(vpcProjectID))
// 		})
// 	})
// })
