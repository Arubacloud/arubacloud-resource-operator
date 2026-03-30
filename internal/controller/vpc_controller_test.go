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

func buildVPCResponse(id, name, state string) *arubatypes.VPCResponse {
	location := &arubatypes.LocationResponse{Value: "ITBG-Bergamo"}
	return &arubatypes.VPCResponse{
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

func buildVPCList(responses ...*arubatypes.VPCResponse) *arubatypes.Response[arubatypes.VPCList] {
	list := &arubatypes.VPCList{}
	for _, r := range responses {
		list.Values = append(list.Values, *r)
		list.Total++
	}
	return &arubatypes.Response[arubatypes.VPCList]{
		Data:       list,
		StatusCode: http.StatusOK,
	}
}

func buildVPCCRUDResponse(statusCode int) *arubatypes.Response[arubatypes.VPCResponse] {
	return &arubatypes.Response[arubatypes.VPCResponse]{
		StatusCode: statusCode,
	}
}

func buildProjectListForVpc(projectID, projectName string) *arubatypes.Response[arubatypes.ProjectList] {
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

func defaultVPCSpec(projectName string) v1alpha1.VPCSpec {
	return v1alpha1.VPCSpec{
		Tenant: "test-tenant",
		Region: "ITBG-Bergamo",
		Tags:   []string{"tag1"},
		ProjectReference: v1alpha1.ResourceReference{
			Name:      projectName,
			Namespace: "default",
		},
	}
}

func createTestVpc(ctx context.Context, name string, spec v1alpha1.VPCSpec) *v1alpha1.VPC {
	vpc := &v1alpha1.VPC{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: spec,
	}
	ExpectWithOffset(1, k8sClient.Create(ctx, vpc)).To(Succeed())
	return vpc
}

func setVPCStatus(ctx context.Context, vpc *v1alpha1.VPC, phase v1alpha1.ResourcePhase, reason string, resourceID string, projectID string, observedGen int64, conditionTime time.Time) {
	v := vpc.DeepCopy()
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), v)).To(Succeed())
	v.Status.Phase = phase
	v.Status.ResourceID = resourceID
	v.Status.ProjectID = projectID
	v.Status.ObservedGeneration = observedGen
	if phase != "" {
		v.Status.Conditions = []metav1.Condition{
			{
				Type:               string(phase),
				Status:             metav1.ConditionTrue,
				Reason:             reason,
				LastTransitionTime: metav1.NewTime(conditionTime),
				Message:            string(phase) + " " + reason + " - OK",
			},
		}
	}
	ExpectWithOffset(1, k8sClient.Status().Update(ctx, v)).To(Succeed())
	ExpectWithOffset(1, k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), vpc)).To(Succeed())
}

type vpcMocks struct {
	r              *VPCReconciler
	mockAruba      *arubamocks.MockClient
	mockProject    *arubamocks.MockProjectClient
	mockNetwork    *arubamocks.MockNetworkClient
	mockVPCsClient *arubamocks.MockVPCsClient
}

func newVPCReconcilerWithMocks(t GinkgoTInterface) *vpcMocks {
	mockAruba := arubamocks.NewMockClient(t)
	mockProject := arubamocks.NewMockProjectClient(t)
	mockNetwork := arubamocks.NewMockNetworkClient(t)
	mockVPCsClient := arubamocks.NewMockVPCsClient(t)

	r := NewVPCReconciler(newTestReconciler(t, mockAruba))

	return &vpcMocks{
		r:              r,
		mockAruba:      mockAruba,
		mockProject:    mockProject,
		mockNetwork:    mockNetwork,
		mockVPCsClient: mockVPCsClient,
	}
}

func (m *vpcMocks) expectProjectList(projectID, projectName string) {
	m.mockAruba.EXPECT().FromProject().Return(m.mockProject)
	m.mockProject.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectListForVpc(projectID, projectName), nil)
}

func (m *vpcMocks) expectVPCList(projectID string, responses ...*arubatypes.VPCResponse) {
	m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
	m.mockNetwork.EXPECT().VPCs().Return(m.mockVPCsClient)
	m.mockVPCsClient.EXPECT().List(mock.Anything, projectID, mock.Anything).Return(buildVPCList(responses...), nil)
}

// --- Tests ---

var _ = Describe("VPCReconciler", func() {
	const (
		vpcProjectName = "test-vpc-project-ref"
		vpcProjectID   = "vpc-proj-id-1"
	)

	var (
		ctx context.Context
		vpc *v1alpha1.VPC
	)

	BeforeEach(func() {
		ctx = context.Background()
	})

	AfterEach(func() {
		if vpc != nil {
			v := &v1alpha1.VPC{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), v); err == nil {
				v.Finalizers = nil
				_ = k8sClient.Update(ctx, v)
				_ = k8sClient.Delete(ctx, v)
			}
			vpc = nil
		}
	})

	Describe("First reconciliation", func() {
		It("transitions to Creating+ShallSynchronize when CMP has no VPC", func() {
			m := newVPCReconcilerWithMocks(GinkgoT())
			vpc = createTestVpc(ctx, "test-vpc-first", defaultVPCSpec(vpcProjectName))

			m.expectProjectList(vpcProjectID, vpcProjectName)
			m.expectVPCList(vpcProjectID)

			_, err := m.r.HandleReconcile(ctx, vpc)
			Expect(err).To(Succeed())

			updated := &v1alpha1.VPC{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
		})
	})

	Describe("Create on CMP", func() {
		It("transitions to Creating+Synchronizing after successful CMP create", func() {
			m := newVPCReconcilerWithMocks(GinkgoT())
			vpc = createTestVpc(ctx, "test-vpc-create-cmp", defaultVPCSpec(vpcProjectName))
			setVPCStatus(ctx, vpc, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, "", "", 0, time.Now())

			m.expectProjectList(vpcProjectID, vpcProjectName)
			m.expectVPCList(vpcProjectID)
			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
			m.mockNetwork.EXPECT().VPCs().Return(m.mockVPCsClient)
			m.mockVPCsClient.EXPECT().Create(mock.Anything, vpcProjectID, mock.Anything, mock.Anything).Return(buildVPCCRUDResponse(http.StatusCreated), nil)

			_, err := m.r.HandleReconcile(ctx, vpc)
			Expect(err).To(Succeed())

			updated := &v1alpha1.VPC{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
		})
	})

	Describe("Waiting creation (VPC not yet in CMP)", func() {
		It("returns LongRequeue", func() {
			m := newVPCReconcilerWithMocks(GinkgoT())
			vpc = createTestVpc(ctx, "test-vpc-wait-create", defaultVPCSpec(vpcProjectName))
			setVPCStatus(ctx, vpc, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "", "", 0, time.Now())

			m.expectProjectList(vpcProjectID, vpcProjectName)
			m.expectVPCList(vpcProjectID)

			result, err := m.r.HandleReconcile(ctx, vpc)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("Waiting creation (VPC in transitory CMP state)", func() {
		It("returns LongRequeue when CMP state is Creating", func() {
			m := newVPCReconcilerWithMocks(GinkgoT())
			vpc = createTestVpc(ctx, "test-vpc-wait-create-transitory", defaultVPCSpec(vpcProjectName))
			setVPCStatus(ctx, vpc, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "", "", 0, time.Now())

			cmpVPC := buildVPCResponse("vpc-id-1", "test-vpc-wait-create-transitory", reconciler.CSPResourceStateCreating)
			m.expectProjectList(vpcProjectID, vpcProjectName)
			m.expectVPCList(vpcProjectID, cmpVPC)

			result, err := m.r.HandleReconcile(ctx, vpc)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("Creation confirmed on CMP", func() {
		It("transitions to Creating+Synchronized when CMP VPC is active", func() {
			m := newVPCReconcilerWithMocks(GinkgoT())
			vpc = createTestVpc(ctx, "test-vpc-creation-confirmed", defaultVPCSpec(vpcProjectName))
			setVPCStatus(ctx, vpc, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "", "", 0, time.Now())

			cmpVPC := buildVPCResponse("vpc-id-1", "test-vpc-creation-confirmed", reconciler.CSPResourceStateActive)
			m.expectProjectList(vpcProjectID, vpcProjectName)
			m.expectVPCList(vpcProjectID, cmpVPC)

			_, err := m.r.HandleReconcile(ctx, vpc)
			Expect(err).To(Succeed())

			updated := &v1alpha1.VPC{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronized))
		})
	})

	Describe("Creation accomplished", func() {
		It("transitions to Active+Synchronized and sets ResourceID", func() {
			m := newVPCReconcilerWithMocks(GinkgoT())
			vpc = createTestVpc(ctx, "test-vpc-creation-accomplished", defaultVPCSpec(vpcProjectName))
			setVPCStatus(ctx, vpc, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized, "", "", 0, time.Now())

			cmpVPC := buildVPCResponse("vpc-id-1", "test-vpc-creation-accomplished", reconciler.CSPResourceStateActive)
			m.expectProjectList(vpcProjectID, vpcProjectName)
			m.expectVPCList(vpcProjectID, cmpVPC)

			_, err := m.r.HandleReconcile(ctx, vpc)
			Expect(err).To(Succeed())

			updated := &v1alpha1.VPC{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseActive))
			Expect(updated.Status.ResourceID).To(Equal("vpc-id-1"))
		})
	})

	Describe("HasDeniedChanges", func() {
		It("returns LongRequeue when immutable field (location) is changed", func() {
			m := newVPCReconcilerWithMocks(GinkgoT())
			vpc = createTestVpc(ctx, "test-vpc-denied-changes", defaultVPCSpec(vpcProjectName))
			setVPCStatus(ctx, vpc, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "vpc-id-1", vpcProjectID, 1, time.Now())

			// Force generation change with different location
			vFetch := &v1alpha1.VPC{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), vFetch)).To(Succeed())
			vFetch.Spec.Region = "different-location"
			Expect(k8sClient.Update(ctx, vFetch)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), vpc)).To(Succeed())

			// CMP still has original location ITBG-Bergamo
			cmpVPC := buildVPCResponse("vpc-id-1", "test-vpc-denied-changes", reconciler.CSPResourceStateActive)
			m.expectProjectList(vpcProjectID, vpcProjectName)
			m.expectVPCList(vpcProjectID, cmpVPC)

			result, err := m.r.HandleReconcile(ctx, vpc)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("SpecAlreadyInSyncWithCMP", func() {
		It("re-stamps ObservedGeneration when spec hasn't actually changed", func() {
			m := newVPCReconcilerWithMocks(GinkgoT())
			vpc = createTestVpc(ctx, "test-vpc-spec-in-sync", defaultVPCSpec(vpcProjectName))
			setVPCStatus(ctx, vpc, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "vpc-id-1", vpcProjectID, 1, time.Now())

			// Trigger generation bump with same tags
			vFetch := &v1alpha1.VPC{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), vFetch)).To(Succeed())
			vFetch.Spec.Tags = []string{"tag1"}
			Expect(k8sClient.Update(ctx, vFetch)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), vpc)).To(Succeed())

			// CMP matches: same tags, same location
			cmpVPC := buildVPCResponse("vpc-id-1", "test-vpc-spec-in-sync", reconciler.CSPResourceStateActive)
			cmpVPC.Metadata.Tags = []string{"tag1"}
			m.expectProjectList(vpcProjectID, vpcProjectName)
			m.expectVPCList(vpcProjectID, cmpVPC)

			_, err := m.r.HandleReconcile(ctx, vpc)
			Expect(err).To(Succeed())

			updated := &v1alpha1.VPC{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseActive))
			Expect(updated.Status.ObservedGeneration).To(Equal(vpc.Generation))
		})
	})

	Describe("ShouldBeUpdated", func() {
		It("transitions to Updating+ShallSynchronize when tags differ", func() {
			m := newVPCReconcilerWithMocks(GinkgoT())
			vpc = createTestVpc(ctx, "test-vpc-should-update", defaultVPCSpec(vpcProjectName))
			setVPCStatus(ctx, vpc, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "vpc-id-1", vpcProjectID, 1, time.Now())

			// Change tags to trigger update
			vFetch := &v1alpha1.VPC{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), vFetch)).To(Succeed())
			vFetch.Spec.Tags = []string{"tag1", "tag2"}
			Expect(k8sClient.Update(ctx, vFetch)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), vpc)).To(Succeed())

			// CMP has old tags
			cmpVPC := buildVPCResponse("vpc-id-1", "test-vpc-should-update", reconciler.CSPResourceStateActive)
			cmpVPC.Metadata.Tags = []string{"tag1"}
			m.expectProjectList(vpcProjectID, vpcProjectName)
			m.expectVPCList(vpcProjectID, cmpVPC)

			_, err := m.r.HandleReconcile(ctx, vpc)
			Expect(err).To(Succeed())

			updated := &v1alpha1.VPC{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseUpdating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseUpdating))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
		})
	})

	Describe("Update on CMP", func() {
		It("transitions to Updating+Synchronizing after successful CMP update", func() {
			m := newVPCReconcilerWithMocks(GinkgoT())
			vpc = createTestVpc(ctx, "test-vpc-update-cmp", defaultVPCSpec(vpcProjectName))
			setVPCStatus(ctx, vpc, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize, "vpc-id-1", vpcProjectID, 1, time.Now())

			cmpVPC := buildVPCResponse("vpc-id-1", "test-vpc-update-cmp", reconciler.CSPResourceStateActive)
			m.expectProjectList(vpcProjectID, vpcProjectName)
			m.expectVPCList(vpcProjectID, cmpVPC)
			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
			m.mockNetwork.EXPECT().VPCs().Return(m.mockVPCsClient)
			m.mockVPCsClient.EXPECT().Update(mock.Anything, vpcProjectID, "vpc-id-1", mock.Anything, mock.Anything).Return(buildVPCCRUDResponse(http.StatusOK), nil)

			_, err := m.r.HandleReconcile(ctx, vpc)
			Expect(err).To(Succeed())

			updated := &v1alpha1.VPC{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseUpdating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseUpdating))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
		})
	})

	Describe("Should delete", func() {
		It("transitions to Deleting+ShallSynchronize when deletion is requested on Active VPC", func() {
			m := newVPCReconcilerWithMocks(GinkgoT())
			vpc = createTestVpc(ctx, "test-vpc-should-delete", defaultVPCSpec(vpcProjectName))
			vFetch := &v1alpha1.VPC{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), vFetch)).To(Succeed())
			vFetch.Finalizers = []string{vpcFinalizerName}
			Expect(k8sClient.Update(ctx, vFetch)).To(Succeed())
			setVPCStatus(ctx, vpc, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "vpc-id-1", vpcProjectID, 1, time.Now())
			Expect(k8sClient.Delete(ctx, vpc)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), vpc)).To(Succeed())

			cmpVPC := buildVPCResponse("vpc-id-1", "test-vpc-should-delete", reconciler.CSPResourceStateActive)
			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
			m.mockNetwork.EXPECT().VPCs().Return(m.mockVPCsClient)
			m.mockVPCsClient.EXPECT().List(mock.Anything, vpcProjectID, mock.Anything).Return(buildVPCList(cmpVPC), nil)

			_, err := m.r.HandleReconcile(ctx, vpc)
			Expect(err).To(Succeed())

			updated := &v1alpha1.VPC{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
		})
	})

	Describe("Delete on CMP", func() {
		It("transitions to Deleting+Synchronizing after successful CMP delete", func() {
			m := newVPCReconcilerWithMocks(GinkgoT())
			vpc = createTestVpc(ctx, "test-vpc-delete-cmp", defaultVPCSpec(vpcProjectName))
			vFetch := &v1alpha1.VPC{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), vFetch)).To(Succeed())
			vFetch.Finalizers = []string{vpcFinalizerName}
			Expect(k8sClient.Update(ctx, vFetch)).To(Succeed())
			setVPCStatus(ctx, vpc, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, "vpc-id-1", vpcProjectID, 1, time.Now())
			Expect(k8sClient.Delete(ctx, vpc)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), vpc)).To(Succeed())

			cmpVPC := buildVPCResponse("vpc-id-1", "test-vpc-delete-cmp", reconciler.CSPResourceStateActive)
			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
			m.mockNetwork.EXPECT().VPCs().Return(m.mockVPCsClient)
			m.mockVPCsClient.EXPECT().List(mock.Anything, vpcProjectID, mock.Anything).Return(buildVPCList(cmpVPC), nil)
			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
			m.mockNetwork.EXPECT().VPCs().Return(m.mockVPCsClient)
			m.mockVPCsClient.EXPECT().Delete(mock.Anything, vpcProjectID, "vpc-id-1", mock.Anything).Return(buildDeleteResponse(http.StatusOK), nil)

			_, err := m.r.HandleReconcile(ctx, vpc)
			Expect(err).To(Succeed())

			updated := &v1alpha1.VPC{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
		})
	})

	Describe("CMP transitory during deletion", func() {
		It("returns LongRequeue when CMP state is Deleting", func() {
			m := newVPCReconcilerWithMocks(GinkgoT())
			vpc = createTestVpc(ctx, "test-vpc-deleting-transitory", defaultVPCSpec(vpcProjectName))
			vFetch := &v1alpha1.VPC{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), vFetch)).To(Succeed())
			vFetch.Finalizers = []string{vpcFinalizerName}
			Expect(k8sClient.Update(ctx, vFetch)).To(Succeed())
			setVPCStatus(ctx, vpc, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronizing, "vpc-id-1", vpcProjectID, 1, time.Now())
			Expect(k8sClient.Delete(ctx, vpc)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), vpc)).To(Succeed())

			cmpVPC := buildVPCResponse("vpc-id-1", "test-vpc-deleting-transitory", reconciler.CSPResourceStateDeleting)
			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
			m.mockNetwork.EXPECT().VPCs().Return(m.mockVPCsClient)
			m.mockVPCsClient.EXPECT().List(mock.Anything, vpcProjectID, mock.Anything).Return(buildVPCList(cmpVPC), nil)

			result, err := m.r.HandleReconcile(ctx, vpc)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("Deletion accomplished", func() {
		It("transitions to Deleted phase when CMP VPC is gone", func() {
			m := newVPCReconcilerWithMocks(GinkgoT())
			vpc = createTestVpc(ctx, "test-vpc-deletion-accomplished", defaultVPCSpec(vpcProjectName))
			vFetch := &v1alpha1.VPC{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), vFetch)).To(Succeed())
			vFetch.Finalizers = []string{vpcFinalizerName}
			Expect(k8sClient.Update(ctx, vFetch)).To(Succeed())
			setVPCStatus(ctx, vpc, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronized, "vpc-id-1", vpcProjectID, 1, time.Now())
			Expect(k8sClient.Delete(ctx, vpc)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), vpc)).To(Succeed())

			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
			m.mockNetwork.EXPECT().VPCs().Return(m.mockVPCsClient)
			m.mockVPCsClient.EXPECT().List(mock.Anything, vpcProjectID, mock.Anything).Return(buildVPCList(), nil)

			_, err := m.r.HandleReconcile(ctx, vpc)
			Expect(err).To(Succeed())

			updated := &v1alpha1.VPC{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleted))
		})
	})

	Describe("IsInError", func() {
		It("transitions to Failed+Synchronized when CMP state is Failed", func() {
			m := newVPCReconcilerWithMocks(GinkgoT())
			vpc = createTestVpc(ctx, "test-vpc-in-error", defaultVPCSpec(vpcProjectName))
			setVPCStatus(ctx, vpc, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "vpc-id-1", vpcProjectID, 1, time.Now())

			cmpVPC := buildVPCResponse("vpc-id-1", "test-vpc-in-error", reconciler.CSPResourceStateFailed)
			m.expectProjectList(vpcProjectID, vpcProjectName)
			m.expectVPCList(vpcProjectID, cmpVPC)

			_, err := m.r.HandleReconcile(ctx, vpc)
			Expect(err).To(Succeed())

			updated := &v1alpha1.VPC{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseFailed))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronized))
		})
	})

	Describe("Phase timeout", func() {
		It("transitions to Failed when stuck in transitory phase too long", func() {
			m := newVPCReconcilerWithMocks(GinkgoT())
			vpc = createTestVpc(ctx, "test-vpc-timeout", defaultVPCSpec(vpcProjectName))
			setVPCStatus(ctx, vpc, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, "", vpcProjectID,
				0, time.Now().Add(-(reconciler.MaxPhaseTimeout + time.Minute)))

			cmpVPC := buildVPCResponse("vpc-id-1", "test-vpc-timeout", reconciler.CSPResourceStateActive)
			m.expectProjectList(vpcProjectID, vpcProjectName)
			m.expectVPCList(vpcProjectID, cmpVPC)

			_, err := m.r.HandleReconcile(ctx, vpc)
			Expect(err).To(Succeed())

			updated := &v1alpha1.VPC{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
		})
	})

	Describe("Project not found yet", func() {
		It("returns LongRequeue when project doesn't exist in CMP yet", func() {
			m := newVPCReconcilerWithMocks(GinkgoT())
			vpc = createTestVpc(ctx, "test-vpc-no-project", defaultVPCSpec(vpcProjectName))

			m.mockAruba.EXPECT().FromProject().Return(m.mockProject)
			m.mockProject.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectList(), nil)

			result, err := m.r.HandleReconcile(ctx, vpc)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("ProjectID set in status via prePatch callback", func() {
		It("stamps ProjectID on status when first transitioning", func() {
			m := newVPCReconcilerWithMocks(GinkgoT())
			vpc = createTestVpc(ctx, "test-vpc-project-id", defaultVPCSpec(vpcProjectName))

			m.expectProjectList(vpcProjectID, vpcProjectName)
			m.expectVPCList(vpcProjectID)

			_, err := m.r.HandleReconcile(ctx, vpc)
			Expect(err).To(Succeed())

			updated := &v1alpha1.VPC{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), updated)).To(Succeed())
			Expect(updated.Status.ProjectID).To(Equal(vpcProjectID))
		})
	})

	Describe("CMP error handling", func() {
		DescribeTable("CMP create fails — preserves Creating+ShallSynchronize, surfaces error in condition",
			func(name string, statusCode int, expectedRequeue time.Duration) {
				m := newVPCReconcilerWithMocks(GinkgoT())
				vpc = createTestVpc(ctx, name, defaultVPCSpec(vpcProjectName))
				setVPCStatus(ctx, vpc, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, "", "", 0, time.Now())

				m.expectProjectList(vpcProjectID, vpcProjectName)
				m.expectVPCList(vpcProjectID)
				m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
				m.mockNetwork.EXPECT().VPCs().Return(m.mockVPCsClient)
				m.mockVPCsClient.EXPECT().Create(mock.Anything, vpcProjectID, mock.Anything, mock.Anything).Return(buildVPCCRUDResponse(statusCode), nil)

				result, err := m.r.HandleReconcile(ctx, vpc)
				Expect(err).To(Succeed())
				Expect(result.RequeueAfter).To(Equal(expectedRequeue))

				updated := &v1alpha1.VPC{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), updated)).To(Succeed())
				Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
				cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
				Expect(cond).NotTo(BeNil())
				Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
				Expect(cond.Message).To(ContainSubstring("ERROR"))
			},
			Entry("4xx → LongRequeueAfter, no phase change", "vpc-cmp-err-create-400", http.StatusBadRequest, reconciler.LongRequeueAfter),
			Entry("5xx → ShortRequeueAfter, no phase change", "vpc-cmp-err-create-500", http.StatusInternalServerError, reconciler.ShortRequeueAfter),
		)

		DescribeTable("CMP update fails — preserves Updating+ShallSynchronize, surfaces error in condition",
			func(name string, statusCode int, expectedRequeue time.Duration) {
				m := newVPCReconcilerWithMocks(GinkgoT())
				vpc = createTestVpc(ctx, name, defaultVPCSpec(vpcProjectName))
				setVPCStatus(ctx, vpc, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize, "vpc-id-1", vpcProjectID, 1, time.Now())

				cmpVPC := buildVPCResponse("vpc-id-1", name, reconciler.CSPResourceStateActive)
				m.expectProjectList(vpcProjectID, vpcProjectName)
				m.expectVPCList(vpcProjectID, cmpVPC)
				m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
				m.mockNetwork.EXPECT().VPCs().Return(m.mockVPCsClient)
				m.mockVPCsClient.EXPECT().Update(mock.Anything, vpcProjectID, "vpc-id-1", mock.Anything, mock.Anything).Return(buildVPCCRUDResponse(statusCode), nil)

				result, err := m.r.HandleReconcile(ctx, vpc)
				Expect(err).To(Succeed())
				Expect(result.RequeueAfter).To(Equal(expectedRequeue))

				updated := &v1alpha1.VPC{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), updated)).To(Succeed())
				Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseUpdating))
				cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseUpdating))
				Expect(cond).NotTo(BeNil())
				Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
				Expect(cond.Message).To(ContainSubstring("ERROR"))
			},
			Entry("4xx → LongRequeueAfter, no phase change", "vpc-cmp-err-update-400", http.StatusBadRequest, reconciler.LongRequeueAfter),
			Entry("5xx → ShortRequeueAfter, no phase change", "vpc-cmp-err-update-500", http.StatusInternalServerError, reconciler.ShortRequeueAfter),
		)

		DescribeTable("CMP delete fails — preserves Deleting+ShallSynchronize, surfaces error in condition",
			func(name string, statusCode int, expectedRequeue time.Duration) {
				m := newVPCReconcilerWithMocks(GinkgoT())
				vpc = createTestVpc(ctx, name, defaultVPCSpec(vpcProjectName))
				vFetch := &v1alpha1.VPC{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), vFetch)).To(Succeed())
				vFetch.Finalizers = []string{vpcFinalizerName}
				Expect(k8sClient.Update(ctx, vFetch)).To(Succeed())
				setVPCStatus(ctx, vpc, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, "vpc-id-1", vpcProjectID, 1, time.Now())
				Expect(k8sClient.Delete(ctx, vpc)).To(Succeed())
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), vpc)).To(Succeed())

				cmpVPC := buildVPCResponse("vpc-id-1", name, reconciler.CSPResourceStateActive)
				m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
				m.mockNetwork.EXPECT().VPCs().Return(m.mockVPCsClient)
				m.mockVPCsClient.EXPECT().List(mock.Anything, vpcProjectID, mock.Anything).Return(buildVPCList(cmpVPC), nil)
				m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
				m.mockNetwork.EXPECT().VPCs().Return(m.mockVPCsClient)
				m.mockVPCsClient.EXPECT().Delete(mock.Anything, vpcProjectID, "vpc-id-1", mock.Anything).Return(buildDeleteResponse(statusCode), nil)

				result, err := m.r.HandleReconcile(ctx, vpc)
				Expect(err).To(Succeed())
				Expect(result.RequeueAfter).To(Equal(expectedRequeue))

				updated := &v1alpha1.VPC{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), updated)).To(Succeed())
				Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
				cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))
				Expect(cond).NotTo(BeNil())
				Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
				Expect(cond.Message).To(ContainSubstring("ERROR"))
			},
			Entry("4xx → LongRequeueAfter, no phase change", "vpc-cmp-err-delete-400", http.StatusBadRequest, reconciler.LongRequeueAfter),
			Entry("5xx → ShortRequeueAfter, no phase change", "vpc-cmp-err-delete-500", http.StatusInternalServerError, reconciler.ShortRequeueAfter),
		)
	})

	Describe("Tenant immutability", func() {
		It("rejects a tenant change on an existing resource", func() {
			vpc = createTestVpc(ctx, "test-vpc-immutable-tenant", defaultVPCSpec(vpcProjectName))

			vFetch := &v1alpha1.VPC{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), vFetch)).To(Succeed())
			vFetch.Spec.Tenant = "other-tenant"
			err := k8sClient.Update(ctx, vFetch)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("tenant is immutable"))
		})
	})
})
