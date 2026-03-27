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

func buildSubnetResponse(id, name, state string) *arubatypes.SubnetResponse {
	dhcp := &arubatypes.SubnetDHCP{Enabled: true}
	network := &arubatypes.SubnetNetwork{Address: "192.168.1.0/24"}
	location := &arubatypes.LocationResponse{Value: "ITBG-Bergamo"}
	return &arubatypes.SubnetResponse{
		Metadata: arubatypes.ResourceMetadataResponse{
			ID:               &id,
			Name:             &name,
			LocationResponse: location,
		},
		Properties: arubatypes.SubnetPropertiesResponse{
			Type:    arubatypes.SubnetTypeAdvanced,
			Default: false,
			Network: network,
			DHCP:    dhcp,
		},
		Status: arubatypes.ResourceStatus{
			State: &state,
		},
	}
}

func buildSubnetList(responses ...*arubatypes.SubnetResponse) *arubatypes.Response[arubatypes.SubnetList] {
	list := &arubatypes.SubnetList{}
	for _, r := range responses {
		list.Values = append(list.Values, *r)
		list.Total++
	}
	return &arubatypes.Response[arubatypes.SubnetList]{
		Data:       list,
		StatusCode: http.StatusOK,
	}
}

func buildSubnetCRUDResponse(statusCode int) *arubatypes.Response[arubatypes.SubnetResponse] {
	return &arubatypes.Response[arubatypes.SubnetResponse]{
		StatusCode: statusCode,
	}
}

func buildVpcListForSubnet(vpcID, vpcName string) *arubatypes.Response[arubatypes.VPCList] {
	id := vpcID
	name := vpcName
	v := arubatypes.VPCResponse{
		Metadata: arubatypes.ResourceMetadataResponse{
			ID:   &id,
			Name: &name,
		},
	}
	list := &arubatypes.VPCList{}
	list.Values = append(list.Values, v)
	list.Total = 1
	return &arubatypes.Response[arubatypes.VPCList]{
		Data:       list,
		StatusCode: http.StatusOK,
	}
}

func buildProjectListForSubnet(projectID, projectName string) *arubatypes.Response[arubatypes.ProjectList] {
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

func defaultSubnetSpec(projectName, vpcName string) v1alpha1.SubnetSpec {
	return v1alpha1.SubnetSpec{
		Tenant: "test-tenant",
		Tags:   []string{"tag1"},
		Region: "ITBG-Bergamo",
		Type:   "Advanced",
		CIDR:   "192.168.1.0/24",
		DHCP: v1alpha1.SubnetDHCP{
			Enabled: true,
		},
		ProjectReference: v1alpha1.ResourceReference{
			Name:      projectName,
			Namespace: "default",
		},
		VPCReference: v1alpha1.ResourceReference{
			Name:      vpcName,
			Namespace: "default",
		},
	}
}

func createTestSubnet(ctx context.Context, name string, spec v1alpha1.SubnetSpec) *v1alpha1.Subnet {
	subnet := &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: spec,
	}
	ExpectWithOffset(1, k8sClient.Create(ctx, subnet)).To(Succeed())
	return subnet
}

func setSubnetStatus(ctx context.Context, subnet *v1alpha1.Subnet, phase v1alpha1.ResourcePhase, reason string, resourceID string, projectID string, vpcID string, observedGen int64, conditionTime time.Time) {
	s := subnet.DeepCopy()
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), s)).To(Succeed())
	s.Status.Phase = phase
	s.Status.ResourceID = resourceID
	s.Status.ProjectID = projectID
	s.Status.VPCID = vpcID
	s.Status.ObservedGeneration = observedGen
	if phase != "" {
		s.Status.Conditions = []metav1.Condition{
			{
				Type:               string(phase),
				Status:             metav1.ConditionTrue,
				Reason:             reason,
				LastTransitionTime: metav1.NewTime(conditionTime),
				Message:            string(phase) + " " + reason + " - OK",
			},
		}
	}
	ExpectWithOffset(1, k8sClient.Status().Update(ctx, s)).To(Succeed())
	ExpectWithOffset(1, k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), subnet)).To(Succeed())
}

// --- Mock setup ---

type subnetMocks struct {
	r              *SubnetReconciler
	mockAruba      *arubamocks.MockClient
	mockProject    *arubamocks.MockProjectClient
	mockNetwork    *arubamocks.MockNetworkClient
	mockVPCsClient *arubamocks.MockVPCsClient
	mockSubnets    *arubamocks.MockSubnetsClient
}

func newSubnetReconcilerWithMocks(t GinkgoTInterface) *subnetMocks {
	mockAruba := arubamocks.NewMockClient(t)
	mockProject := arubamocks.NewMockProjectClient(t)
	mockNetwork := arubamocks.NewMockNetworkClient(t)
	mockVPCsClient := arubamocks.NewMockVPCsClient(t)
	mockSubnets := arubamocks.NewMockSubnetsClient(t)

	r := NewSubnetReconciler(newTestReconciler(t, mockAruba))

	return &subnetMocks{
		r:              r,
		mockAruba:      mockAruba,
		mockProject:    mockProject,
		mockNetwork:    mockNetwork,
		mockVPCsClient: mockVPCsClient,
		mockSubnets:    mockSubnets,
	}
}

func (m *subnetMocks) expectProjectList(projectID, projectName string) {
	m.mockAruba.EXPECT().FromProject().Return(m.mockProject)
	m.mockProject.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectListForSubnet(projectID, projectName), nil)
}

func (m *subnetMocks) expectVpcList(projectID, vpcID, vpcName string) {
	m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
	m.mockNetwork.EXPECT().VPCs().Return(m.mockVPCsClient)
	m.mockVPCsClient.EXPECT().List(mock.Anything, projectID, mock.Anything).Return(buildVpcListForSubnet(vpcID, vpcName), nil)
}

func (m *subnetMocks) expectSubnetList(projectID, vpcID string, responses ...*arubatypes.SubnetResponse) {
	m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
	m.mockNetwork.EXPECT().Subnets().Return(m.mockSubnets)
	m.mockSubnets.EXPECT().List(mock.Anything, projectID, vpcID, mock.Anything).Return(buildSubnetList(responses...), nil)
}

// --- Tests ---

var _ = Describe("SubnetReconciler", func() {
	const (
		subnetProjectName = "test-subnet-project-ref"
		subnetProjectID   = "subnet-proj-id-1"
		subnetVpcName     = "test-subnet-vpc-ref"
		subnetVpcID       = "subnet-vpc-id-1"
	)

	var (
		ctx    context.Context
		subnet *v1alpha1.Subnet
	)

	BeforeEach(func() {
		ctx = context.Background()
	})

	AfterEach(func() {
		if subnet != nil {
			s := &v1alpha1.Subnet{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), s); err == nil {
				s.Finalizers = nil
				_ = k8sClient.Update(ctx, s)
				_ = k8sClient.Delete(ctx, s)
			}
			subnet = nil
		}
	})

	Describe("First reconciliation", func() {
		It("transitions to Creating+ShallSynchronize when CMP has no Subnet", func() {
			m := newSubnetReconcilerWithMocks(GinkgoT())
			subnet = createTestSubnet(ctx, "test-subnet-first", defaultSubnetSpec(subnetProjectName, subnetVpcName))

			m.expectProjectList(subnetProjectID, subnetProjectName)
			m.expectVpcList(subnetProjectID, subnetVpcID, subnetVpcName)
			m.expectSubnetList(subnetProjectID, subnetVpcID)

			_, err := m.r.HandleReconcile(ctx, subnet)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
		})
	})

	Describe("Create on CMP", func() {
		It("transitions to Creating+Synchronizing after successful CMP create", func() {
			m := newSubnetReconcilerWithMocks(GinkgoT())
			subnet = createTestSubnet(ctx, "test-subnet-create-cmp", defaultSubnetSpec(subnetProjectName, subnetVpcName))
			setSubnetStatus(ctx, subnet, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, "", "", "", 0, time.Now())

			m.expectProjectList(subnetProjectID, subnetProjectName)
			m.expectVpcList(subnetProjectID, subnetVpcID, subnetVpcName)
			m.expectSubnetList(subnetProjectID, subnetVpcID)
			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
			m.mockNetwork.EXPECT().Subnets().Return(m.mockSubnets)
			m.mockSubnets.EXPECT().Create(mock.Anything, subnetProjectID, subnetVpcID, mock.Anything, mock.Anything).Return(buildSubnetCRUDResponse(http.StatusCreated), nil)

			_, err := m.r.HandleReconcile(ctx, subnet)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
		})
	})

	Describe("Waiting creation (Subnet not yet in CMP)", func() {
		It("returns LongRequeue", func() {
			m := newSubnetReconcilerWithMocks(GinkgoT())
			subnet = createTestSubnet(ctx, "test-subnet-wait-create", defaultSubnetSpec(subnetProjectName, subnetVpcName))
			setSubnetStatus(ctx, subnet, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "", "", "", 0, time.Now())

			m.expectProjectList(subnetProjectID, subnetProjectName)
			m.expectVpcList(subnetProjectID, subnetVpcID, subnetVpcName)
			m.expectSubnetList(subnetProjectID, subnetVpcID)

			result, err := m.r.HandleReconcile(ctx, subnet)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("Waiting creation (Subnet in transitory CMP state)", func() {
		It("returns LongRequeue when CMP state is Creating", func() {
			m := newSubnetReconcilerWithMocks(GinkgoT())
			subnet = createTestSubnet(ctx, "test-subnet-wait-create-transitory", defaultSubnetSpec(subnetProjectName, subnetVpcName))
			setSubnetStatus(ctx, subnet, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "", "", "", 0, time.Now())

			cmpSubnet := buildSubnetResponse("subnet-id-1", "test-subnet-wait-create-transitory", CSPResourceStateCreating)
			m.expectProjectList(subnetProjectID, subnetProjectName)
			m.expectVpcList(subnetProjectID, subnetVpcID, subnetVpcName)
			m.expectSubnetList(subnetProjectID, subnetVpcID, cmpSubnet)

			result, err := m.r.HandleReconcile(ctx, subnet)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("Creation confirmed on CMP", func() {
		It("transitions to Creating+Synchronized when CMP Subnet is active", func() {
			m := newSubnetReconcilerWithMocks(GinkgoT())
			subnet = createTestSubnet(ctx, "test-subnet-creation-confirmed", defaultSubnetSpec(subnetProjectName, subnetVpcName))
			setSubnetStatus(ctx, subnet, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "", "", "", 0, time.Now())

			cmpSubnet := buildSubnetResponse("subnet-id-1", "test-subnet-creation-confirmed", CSPResourceStateActive)
			m.expectProjectList(subnetProjectID, subnetProjectName)
			m.expectVpcList(subnetProjectID, subnetVpcID, subnetVpcName)
			m.expectSubnetList(subnetProjectID, subnetVpcID, cmpSubnet)

			_, err := m.r.HandleReconcile(ctx, subnet)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronized))
		})
	})

	Describe("Creation accomplished", func() {
		It("transitions to Active+Synchronized and sets ResourceID", func() {
			m := newSubnetReconcilerWithMocks(GinkgoT())
			subnet = createTestSubnet(ctx, "test-subnet-creation-accomplished", defaultSubnetSpec(subnetProjectName, subnetVpcName))
			setSubnetStatus(ctx, subnet, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized, "", "", "", 0, time.Now())

			cmpSubnet := buildSubnetResponse("subnet-id-1", "test-subnet-creation-accomplished", CSPResourceStateActive)
			m.expectProjectList(subnetProjectID, subnetProjectName)
			m.expectVpcList(subnetProjectID, subnetVpcID, subnetVpcName)
			m.expectSubnetList(subnetProjectID, subnetVpcID, cmpSubnet)

			_, err := m.r.HandleReconcile(ctx, subnet)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseActive))
			Expect(updated.Status.ResourceID).To(Equal("subnet-id-1"))
		})
	})

	Describe("HasDeniedChanges", func() {
		It("returns LongRequeue when immutable field (location) is changed", func() {
			m := newSubnetReconcilerWithMocks(GinkgoT())
			subnet = createTestSubnet(ctx, "test-subnet-denied-location", defaultSubnetSpec(subnetProjectName, subnetVpcName))
			setSubnetStatus(ctx, subnet, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "subnet-id-1", subnetProjectID, subnetVpcID, 1, time.Now())

			// Force generation change with different location
			sFetch := &v1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), sFetch)).To(Succeed())
			sFetch.Spec.Region = "ITMI-Milan"
			Expect(k8sClient.Update(ctx, sFetch)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), subnet)).To(Succeed())

			// CMP still has original location ITBG-Bergamo
			cmpSubnet := buildSubnetResponse("subnet-id-1", "test-subnet-denied-location", CSPResourceStateActive)
			m.expectProjectList(subnetProjectID, subnetProjectName)
			m.expectVpcList(subnetProjectID, subnetVpcID, subnetVpcName)
			m.expectSubnetList(subnetProjectID, subnetVpcID, cmpSubnet)

			result, err := m.r.HandleReconcile(ctx, subnet)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})

		It("returns LongRequeue when immutable field (network.address) is changed", func() {
			m := newSubnetReconcilerWithMocks(GinkgoT())
			subnet = createTestSubnet(ctx, "test-subnet-denied-changes", defaultSubnetSpec(subnetProjectName, subnetVpcName))
			setSubnetStatus(ctx, subnet, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "subnet-id-1", subnetProjectID, subnetVpcID, 1, time.Now())

			// Force generation change with different network address
			sFetch := &v1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), sFetch)).To(Succeed())
			sFetch.Spec.CIDR = "10.0.0.0/24"
			Expect(k8sClient.Update(ctx, sFetch)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), subnet)).To(Succeed())

			// CMP still has original network address 192.168.1.0/24
			cmpSubnet := buildSubnetResponse("subnet-id-1", "test-subnet-denied-changes", CSPResourceStateActive)
			m.expectProjectList(subnetProjectID, subnetProjectName)
			m.expectVpcList(subnetProjectID, subnetVpcID, subnetVpcName)
			m.expectSubnetList(subnetProjectID, subnetVpcID, cmpSubnet)

			result, err := m.r.HandleReconcile(ctx, subnet)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("SpecAlreadyInSyncWithCMP", func() {
		It("re-stamps ObservedGeneration when spec hasn't actually changed", func() {
			m := newSubnetReconcilerWithMocks(GinkgoT())
			subnet = createTestSubnet(ctx, "test-subnet-spec-in-sync", defaultSubnetSpec(subnetProjectName, subnetVpcName))
			setSubnetStatus(ctx, subnet, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "subnet-id-1", subnetProjectID, subnetVpcID, 1, time.Now())

			// Trigger generation bump with same tags
			sFetch := &v1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), sFetch)).To(Succeed())
			sFetch.Spec.Tags = []string{"tag1"}
			Expect(k8sClient.Update(ctx, sFetch)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), subnet)).To(Succeed())

			// CMP matches: same tags, same DHCP
			cmpSubnet := buildSubnetResponse("subnet-id-1", "test-subnet-spec-in-sync", CSPResourceStateActive)
			cmpSubnet.Metadata.Tags = []string{"tag1"}
			m.expectProjectList(subnetProjectID, subnetProjectName)
			m.expectVpcList(subnetProjectID, subnetVpcID, subnetVpcName)
			m.expectSubnetList(subnetProjectID, subnetVpcID, cmpSubnet)

			_, err := m.r.HandleReconcile(ctx, subnet)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseActive))
			Expect(updated.Status.ObservedGeneration).To(Equal(subnet.Generation))
		})
	})

	Describe("ShouldBeUpdated", func() {
		It("transitions to Updating+ShallSynchronize when tags differ", func() {
			m := newSubnetReconcilerWithMocks(GinkgoT())
			subnet = createTestSubnet(ctx, "test-subnet-should-update", defaultSubnetSpec(subnetProjectName, subnetVpcName))
			setSubnetStatus(ctx, subnet, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "subnet-id-1", subnetProjectID, subnetVpcID, 1, time.Now())

			// Change tags to trigger update
			sFetch := &v1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), sFetch)).To(Succeed())
			sFetch.Spec.Tags = []string{"tag1", "tag2"}
			Expect(k8sClient.Update(ctx, sFetch)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), subnet)).To(Succeed())

			// CMP has old tags
			cmpSubnet := buildSubnetResponse("subnet-id-1", "test-subnet-should-update", CSPResourceStateActive)
			cmpSubnet.Metadata.Tags = []string{"tag1"}
			m.expectProjectList(subnetProjectID, subnetProjectName)
			m.expectVpcList(subnetProjectID, subnetVpcID, subnetVpcName)
			m.expectSubnetList(subnetProjectID, subnetVpcID, cmpSubnet)

			_, err := m.r.HandleReconcile(ctx, subnet)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseUpdating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseUpdating))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
		})
	})

	Describe("Update on CMP", func() {
		It("transitions to Updating+Synchronizing after successful CMP update", func() {
			m := newSubnetReconcilerWithMocks(GinkgoT())
			subnet = createTestSubnet(ctx, "test-subnet-update-cmp", defaultSubnetSpec(subnetProjectName, subnetVpcName))
			setSubnetStatus(ctx, subnet, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize, "subnet-id-1", subnetProjectID, subnetVpcID, 1, time.Now())

			cmpSubnet := buildSubnetResponse("subnet-id-1", "test-subnet-update-cmp", CSPResourceStateActive)
			m.expectProjectList(subnetProjectID, subnetProjectName)
			m.expectVpcList(subnetProjectID, subnetVpcID, subnetVpcName)
			m.expectSubnetList(subnetProjectID, subnetVpcID, cmpSubnet)
			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
			m.mockNetwork.EXPECT().Subnets().Return(m.mockSubnets)
			m.mockSubnets.EXPECT().Update(mock.Anything, subnetProjectID, subnetVpcID, "subnet-id-1", mock.Anything, mock.Anything).Return(buildSubnetCRUDResponse(http.StatusOK), nil)

			_, err := m.r.HandleReconcile(ctx, subnet)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseUpdating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseUpdating))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
		})
	})

	Describe("Should delete", func() {
		It("transitions to Deleting+ShallSynchronize when deletion is requested on Active Subnet", func() {
			m := newSubnetReconcilerWithMocks(GinkgoT())
			subnet = createTestSubnet(ctx, "test-subnet-should-delete", defaultSubnetSpec(subnetProjectName, subnetVpcName))
			sFetch := &v1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), sFetch)).To(Succeed())
			sFetch.Finalizers = []string{subnetFinalizerName}
			Expect(k8sClient.Update(ctx, sFetch)).To(Succeed())
			setSubnetStatus(ctx, subnet, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "subnet-id-1", subnetProjectID, subnetVpcID, 1, time.Now())
			Expect(k8sClient.Delete(ctx, subnet)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), subnet)).To(Succeed())

			cmpSubnet := buildSubnetResponse("subnet-id-1", "test-subnet-should-delete", CSPResourceStateActive)
			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
			m.mockNetwork.EXPECT().Subnets().Return(m.mockSubnets)
			m.mockSubnets.EXPECT().List(mock.Anything, subnetProjectID, subnetVpcID, mock.Anything).Return(buildSubnetList(cmpSubnet), nil)

			_, err := m.r.HandleReconcile(ctx, subnet)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
		})
	})

	Describe("Delete on CMP", func() {
		It("transitions to Deleting+Synchronizing after successful CMP delete", func() {
			m := newSubnetReconcilerWithMocks(GinkgoT())
			subnet = createTestSubnet(ctx, "test-subnet-delete-cmp", defaultSubnetSpec(subnetProjectName, subnetVpcName))
			sFetch := &v1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), sFetch)).To(Succeed())
			sFetch.Finalizers = []string{subnetFinalizerName}
			Expect(k8sClient.Update(ctx, sFetch)).To(Succeed())
			setSubnetStatus(ctx, subnet, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, "subnet-id-1", subnetProjectID, subnetVpcID, 1, time.Now())
			Expect(k8sClient.Delete(ctx, subnet)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), subnet)).To(Succeed())

			cmpSubnet := buildSubnetResponse("subnet-id-1", "test-subnet-delete-cmp", CSPResourceStateActive)
			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
			m.mockNetwork.EXPECT().Subnets().Return(m.mockSubnets)
			m.mockSubnets.EXPECT().List(mock.Anything, subnetProjectID, subnetVpcID, mock.Anything).Return(buildSubnetList(cmpSubnet), nil)
			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
			m.mockNetwork.EXPECT().Subnets().Return(m.mockSubnets)
			m.mockSubnets.EXPECT().Delete(mock.Anything, subnetProjectID, subnetVpcID, "subnet-id-1", mock.Anything).Return(buildDeleteResponse(http.StatusOK), nil)

			_, err := m.r.HandleReconcile(ctx, subnet)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
		})
	})

	Describe("CMP transitory during deletion", func() {
		It("returns LongRequeue when CMP state is Deleting", func() {
			m := newSubnetReconcilerWithMocks(GinkgoT())
			subnet = createTestSubnet(ctx, "test-subnet-deleting-transitory", defaultSubnetSpec(subnetProjectName, subnetVpcName))
			sFetch := &v1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), sFetch)).To(Succeed())
			sFetch.Finalizers = []string{subnetFinalizerName}
			Expect(k8sClient.Update(ctx, sFetch)).To(Succeed())
			setSubnetStatus(ctx, subnet, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronizing, "subnet-id-1", subnetProjectID, subnetVpcID, 1, time.Now())
			Expect(k8sClient.Delete(ctx, subnet)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), subnet)).To(Succeed())

			cmpSubnet := buildSubnetResponse("subnet-id-1", "test-subnet-deleting-transitory", CSPResourceStateDeleting)
			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
			m.mockNetwork.EXPECT().Subnets().Return(m.mockSubnets)
			m.mockSubnets.EXPECT().List(mock.Anything, subnetProjectID, subnetVpcID, mock.Anything).Return(buildSubnetList(cmpSubnet), nil)

			result, err := m.r.HandleReconcile(ctx, subnet)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("Deletion accomplished", func() {
		It("transitions to Deleted phase when CMP Subnet is gone", func() {
			m := newSubnetReconcilerWithMocks(GinkgoT())
			subnet = createTestSubnet(ctx, "test-subnet-deletion-accomplished", defaultSubnetSpec(subnetProjectName, subnetVpcName))
			sFetch := &v1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), sFetch)).To(Succeed())
			sFetch.Finalizers = []string{subnetFinalizerName}
			Expect(k8sClient.Update(ctx, sFetch)).To(Succeed())
			setSubnetStatus(ctx, subnet, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronized, "subnet-id-1", subnetProjectID, subnetVpcID, 1, time.Now())
			Expect(k8sClient.Delete(ctx, subnet)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), subnet)).To(Succeed())

			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
			m.mockNetwork.EXPECT().Subnets().Return(m.mockSubnets)
			m.mockSubnets.EXPECT().List(mock.Anything, subnetProjectID, subnetVpcID, mock.Anything).Return(buildSubnetList(), nil)

			_, err := m.r.HandleReconcile(ctx, subnet)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleted))
		})
	})

	Describe("IsInError", func() {
		It("transitions to Failed+Synchronized when CMP state is Failed", func() {
			m := newSubnetReconcilerWithMocks(GinkgoT())
			subnet = createTestSubnet(ctx, "test-subnet-in-error", defaultSubnetSpec(subnetProjectName, subnetVpcName))
			setSubnetStatus(ctx, subnet, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "subnet-id-1", subnetProjectID, subnetVpcID, 1, time.Now())

			cmpSubnet := buildSubnetResponse("subnet-id-1", "test-subnet-in-error", CSPResourceStateFailed)
			m.expectProjectList(subnetProjectID, subnetProjectName)
			m.expectVpcList(subnetProjectID, subnetVpcID, subnetVpcName)
			m.expectSubnetList(subnetProjectID, subnetVpcID, cmpSubnet)

			_, err := m.r.HandleReconcile(ctx, subnet)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseFailed))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronized))
		})
	})

	Describe("Phase timeout", func() {
		It("transitions to Failed when stuck in transitory phase too long", func() {
			m := newSubnetReconcilerWithMocks(GinkgoT())
			subnet = createTestSubnet(ctx, "test-subnet-timeout", defaultSubnetSpec(subnetProjectName, subnetVpcName))
			setSubnetStatus(ctx, subnet, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, "", subnetProjectID, subnetVpcID,
				0, time.Now().Add(-(reconciler.MaxPhaseTimeout + time.Minute)))

			cmpSubnet := buildSubnetResponse("subnet-id-1", "test-subnet-timeout", CSPResourceStateActive)
			m.expectProjectList(subnetProjectID, subnetProjectName)
			m.expectVpcList(subnetProjectID, subnetVpcID, subnetVpcName)
			m.expectSubnetList(subnetProjectID, subnetVpcID, cmpSubnet)

			_, err := m.r.HandleReconcile(ctx, subnet)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
		})
	})

	Describe("Project not found yet", func() {
		It("returns LongRequeue when project doesn't exist in CMP yet", func() {
			m := newSubnetReconcilerWithMocks(GinkgoT())
			subnet = createTestSubnet(ctx, "test-subnet-no-project", defaultSubnetSpec(subnetProjectName, subnetVpcName))

			m.mockAruba.EXPECT().FromProject().Return(m.mockProject)
			m.mockProject.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectList(), nil)

			result, err := m.r.HandleReconcile(ctx, subnet)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("VPC not found yet", func() {
		It("returns LongRequeue when VPC doesn't exist in CMP yet", func() {
			m := newSubnetReconcilerWithMocks(GinkgoT())
			subnet = createTestSubnet(ctx, "test-subnet-no-vpc", defaultSubnetSpec(subnetProjectName, subnetVpcName))

			m.expectProjectList(subnetProjectID, subnetProjectName)

			emptyVpcList := &arubatypes.Response[arubatypes.VPCList]{
				Data:       &arubatypes.VPCList{},
				StatusCode: http.StatusOK,
			}
			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
			m.mockNetwork.EXPECT().VPCs().Return(m.mockVPCsClient)
			m.mockVPCsClient.EXPECT().List(mock.Anything, subnetProjectID, mock.Anything).Return(emptyVpcList, nil)

			result, err := m.r.HandleReconcile(ctx, subnet)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("ProjectID and VpcID stamped in status", func() {
		It("stamps ProjectID and VpcID on status when first transitioning", func() {
			m := newSubnetReconcilerWithMocks(GinkgoT())
			subnet = createTestSubnet(ctx, "test-subnet-ids-stamped", defaultSubnetSpec(subnetProjectName, subnetVpcName))

			m.expectProjectList(subnetProjectID, subnetProjectName)
			m.expectVpcList(subnetProjectID, subnetVpcID, subnetVpcName)
			m.expectSubnetList(subnetProjectID, subnetVpcID)

			_, err := m.r.HandleReconcile(ctx, subnet)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), updated)).To(Succeed())
			Expect(updated.Status.ProjectID).To(Equal(subnetProjectID))
			Expect(updated.Status.VPCID).To(Equal(subnetVpcID))
		})
	})

	Describe("CMP error handling", func() {
		DescribeTable("CMP create fails — preserves Creating+ShallSynchronize, surfaces error in condition",
			func(name string, statusCode int, expectedRequeue time.Duration) {
				m := newSubnetReconcilerWithMocks(GinkgoT())
				subnet = createTestSubnet(ctx, name, defaultSubnetSpec(subnetProjectName, subnetVpcName))
				setSubnetStatus(ctx, subnet, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, "", "", "", 0, time.Now())

				m.expectProjectList(subnetProjectID, subnetProjectName)
				m.expectVpcList(subnetProjectID, subnetVpcID, subnetVpcName)
				m.expectSubnetList(subnetProjectID, subnetVpcID)
				m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
				m.mockNetwork.EXPECT().Subnets().Return(m.mockSubnets)
				m.mockSubnets.EXPECT().Create(mock.Anything, subnetProjectID, subnetVpcID, mock.Anything, mock.Anything).Return(buildSubnetCRUDResponse(statusCode), nil)

				result, err := m.r.HandleReconcile(ctx, subnet)
				Expect(err).To(Succeed())
				Expect(result.RequeueAfter).To(Equal(expectedRequeue))

				updated := &v1alpha1.Subnet{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), updated)).To(Succeed())
				Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
				cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
				Expect(cond).NotTo(BeNil())
				Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
				Expect(cond.Message).To(ContainSubstring("ERROR"))
			},
			Entry("4xx → LongRequeueAfter, no phase change", "subnet-cmp-err-create-400", http.StatusBadRequest, reconciler.LongRequeueAfter),
			Entry("5xx → ShortRequeueAfter, no phase change", "subnet-cmp-err-create-500", http.StatusInternalServerError, reconciler.ShortRequeueAfter),
		)

		DescribeTable("CMP update fails — preserves Updating+ShallSynchronize, surfaces error in condition",
			func(name string, statusCode int, expectedRequeue time.Duration) {
				m := newSubnetReconcilerWithMocks(GinkgoT())
				subnet = createTestSubnet(ctx, name, defaultSubnetSpec(subnetProjectName, subnetVpcName))
				setSubnetStatus(ctx, subnet, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize, "subnet-id-1", subnetProjectID, subnetVpcID, 1, time.Now())

				cmpSubnet := buildSubnetResponse("subnet-id-1", name, CSPResourceStateActive)
				m.expectProjectList(subnetProjectID, subnetProjectName)
				m.expectVpcList(subnetProjectID, subnetVpcID, subnetVpcName)
				m.expectSubnetList(subnetProjectID, subnetVpcID, cmpSubnet)
				m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
				m.mockNetwork.EXPECT().Subnets().Return(m.mockSubnets)
				m.mockSubnets.EXPECT().Update(mock.Anything, subnetProjectID, subnetVpcID, "subnet-id-1", mock.Anything, mock.Anything).Return(buildSubnetCRUDResponse(statusCode), nil)

				result, err := m.r.HandleReconcile(ctx, subnet)
				Expect(err).To(Succeed())
				Expect(result.RequeueAfter).To(Equal(expectedRequeue))

				updated := &v1alpha1.Subnet{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), updated)).To(Succeed())
				Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseUpdating))
				cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseUpdating))
				Expect(cond).NotTo(BeNil())
				Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
				Expect(cond.Message).To(ContainSubstring("ERROR"))
			},
			Entry("4xx → LongRequeueAfter, no phase change", "subnet-cmp-err-update-400", http.StatusBadRequest, reconciler.LongRequeueAfter),
			Entry("5xx → ShortRequeueAfter, no phase change", "subnet-cmp-err-update-500", http.StatusInternalServerError, reconciler.ShortRequeueAfter),
		)

		DescribeTable("CMP delete fails — preserves Deleting+ShallSynchronize, surfaces error in condition",
			func(name string, statusCode int, expectedRequeue time.Duration) {
				m := newSubnetReconcilerWithMocks(GinkgoT())
				subnet = createTestSubnet(ctx, name, defaultSubnetSpec(subnetProjectName, subnetVpcName))
				sFetch := &v1alpha1.Subnet{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), sFetch)).To(Succeed())
				sFetch.Finalizers = []string{subnetFinalizerName}
				Expect(k8sClient.Update(ctx, sFetch)).To(Succeed())
				setSubnetStatus(ctx, subnet, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, "subnet-id-1", subnetProjectID, subnetVpcID, 1, time.Now())
				Expect(k8sClient.Delete(ctx, subnet)).To(Succeed())
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), subnet)).To(Succeed())

				cmpSubnet := buildSubnetResponse("subnet-id-1", name, CSPResourceStateActive)
				m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
				m.mockNetwork.EXPECT().Subnets().Return(m.mockSubnets)
				m.mockSubnets.EXPECT().List(mock.Anything, subnetProjectID, subnetVpcID, mock.Anything).Return(buildSubnetList(cmpSubnet), nil)
				m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
				m.mockNetwork.EXPECT().Subnets().Return(m.mockSubnets)
				m.mockSubnets.EXPECT().Delete(mock.Anything, subnetProjectID, subnetVpcID, "subnet-id-1", mock.Anything).Return(buildDeleteResponse(statusCode), nil)

				result, err := m.r.HandleReconcile(ctx, subnet)
				Expect(err).To(Succeed())
				Expect(result.RequeueAfter).To(Equal(expectedRequeue))

				updated := &v1alpha1.Subnet{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), updated)).To(Succeed())
				Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
				cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))
				Expect(cond).NotTo(BeNil())
				Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
				Expect(cond.Message).To(ContainSubstring("ERROR"))
			},
			Entry("4xx → LongRequeueAfter, no phase change", "subnet-cmp-err-delete-400", http.StatusBadRequest, reconciler.LongRequeueAfter),
			Entry("5xx → ShortRequeueAfter, no phase change", "subnet-cmp-err-delete-500", http.StatusInternalServerError, reconciler.ShortRequeueAfter),
		)
	})
})
