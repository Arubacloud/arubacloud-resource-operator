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

	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	arubatypes "github.com/Arubacloud/sdk-go/pkg/types"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
	arubamocks "github.com/Arubacloud/arubacloud-resource-operator/internal/mocks/aruba"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// --- Builder helpers ---

func buildCSResponse(id, name string, state arubatypes.State) *arubatypes.CloudServerResponse {
	flavorName := arubatypes.CloudServerFlavor("gp1.small")
	return &arubatypes.CloudServerResponse{
		Metadata: arubatypes.ResourceMetadataResponse{
			ID:   &id,
			Name: &name,
		},
		Properties: arubatypes.CloudServerPropertiesResponse{
			Zone:   "ITBG",
			Flavor: arubatypes.CloudServerFlavorResponse{Name: flavorName},
		},
		Status: arubatypes.ResourceStatusResponse{
			State: &state,
		},
	}
}

func buildCSList(responses ...*arubatypes.CloudServerResponse) *arubatypes.Response[arubatypes.CloudServerListResponse] {
	list := &arubatypes.CloudServerListResponse{}
	for _, r := range responses {
		list.Values = append(list.Values, *r)
		list.Total++
	}
	return &arubatypes.Response[arubatypes.CloudServerListResponse]{
		Data:       list,
		StatusCode: http.StatusOK,
	}
}

func buildCSCRUDResponse(statusCode int) *arubatypes.Response[arubatypes.CloudServerResponse] {
	return &arubatypes.Response[arubatypes.CloudServerResponse]{
		StatusCode: statusCode,
	}
}

func buildProjectListForCS(projectID, projectName string) *arubatypes.Response[arubatypes.ProjectListResponse] {
	id := projectID
	name := projectName
	proj := arubatypes.ProjectResponse{
		Metadata: arubatypes.ResourceMetadataResponse{
			ID:   &id,
			Name: &name,
		},
	}
	list := &arubatypes.ProjectListResponse{}
	list.Values = append(list.Values, proj)
	list.Total = 1
	return &arubatypes.Response[arubatypes.ProjectListResponse]{
		Data:       list,
		StatusCode: http.StatusOK,
	}
}

func buildVpcListForCS(vpcID, vpcName string) *arubatypes.Response[arubatypes.VPCListResponse] {
	id := vpcID
	name := vpcName
	v := arubatypes.VPCResponse{
		Metadata: arubatypes.ResourceMetadataResponse{
			ID:   &id,
			Name: &name,
		},
	}
	list := &arubatypes.VPCListResponse{}
	list.Values = append(list.Values, v)
	list.Total = 1
	return &arubatypes.Response[arubatypes.VPCListResponse]{
		Data:       list,
		StatusCode: http.StatusOK,
	}
}

func buildBootVolumeListForCS(volID, volName string) *arubatypes.Response[arubatypes.BlockStorageListResponse] {
	return buildBootVolumeListForCSWithState(volID, volName, arubatypes.StateActive)
}

// WORKAROUND: used to test the dependency readiness check.
// TODO: remove once the CMP Infra Team fixes the root cause.
func buildBootVolumeListForCSWithState(volID, volName string, state arubatypes.State) *arubatypes.Response[arubatypes.BlockStorageListResponse] {
	id := volID
	name := volName
	vol := arubatypes.BlockStorageResponse{
		Metadata: arubatypes.ResourceMetadataResponse{
			ID:   &id,
			Name: &name,
		},
		Status: arubatypes.ResourceStatusResponse{
			State: &state,
		},
	}
	list := &arubatypes.BlockStorageListResponse{}
	list.Values = append(list.Values, vol)
	list.Total = 1
	return &arubatypes.Response[arubatypes.BlockStorageListResponse]{
		Data:       list,
		StatusCode: http.StatusOK,
	}
}

func buildSubnetListForCS(subnetID, subnetName string) *arubatypes.Response[arubatypes.SubnetListResponse] {
	return buildSubnetListForCSWithState(subnetID, subnetName, arubatypes.StateActive)
}

// WORKAROUND: used to test the dependency readiness check.
// TODO: remove once the CMP Infra Team fixes the root cause.
func buildSubnetListForCSWithState(subnetID, subnetName string, state arubatypes.State) *arubatypes.Response[arubatypes.SubnetListResponse] {
	id := subnetID
	name := subnetName
	subnet := arubatypes.SubnetResponse{
		Metadata: arubatypes.ResourceMetadataResponse{
			ID:   &id,
			Name: &name,
		},
		Status: arubatypes.ResourceStatusResponse{
			State: &state,
		},
	}
	list := &arubatypes.SubnetListResponse{}
	list.Values = append(list.Values, subnet)
	list.Total = 1
	return &arubatypes.Response[arubatypes.SubnetListResponse]{
		Data:       list,
		StatusCode: http.StatusOK,
	}
}

func buildSGListForCS(sgID, sgName string) *arubatypes.Response[arubatypes.SecurityGroupListResponse] {
	id := sgID
	name := sgName
	sg := arubatypes.SecurityGroupResponse{
		Metadata: arubatypes.ResourceMetadataResponse{
			ID:   &id,
			Name: &name,
		},
	}
	list := &arubatypes.SecurityGroupListResponse{}
	list.Values = append(list.Values, sg)
	list.Total = 1
	return &arubatypes.Response[arubatypes.SecurityGroupListResponse]{
		Data:       list,
		StatusCode: http.StatusOK,
	}
}

func buildKeyPairListForCS(kpID, kpName string) *arubatypes.Response[arubatypes.KeyPairListResponse] {
	id := kpID
	name := kpName
	kp := arubatypes.KeyPairResponse{
		Metadata: arubatypes.ResourceMetadataResponse{
			ID:   &id,
			Name: &name,
		},
	}
	list := &arubatypes.KeyPairListResponse{}
	list.Values = append(list.Values, kp)
	list.Total = 1
	return &arubatypes.Response[arubatypes.KeyPairListResponse]{
		Data:       list,
		StatusCode: http.StatusOK,
	}
}

// --- Test fixture helpers ---

func defaultCSSpec(projectName, vpcName, bootVolName, subnetName, sgName string) v1alpha1.CloudServerSpec {
	return v1alpha1.CloudServerSpec{
		Tenant:     "test-tenant",
		Tags:       []string{"tag1"},
		Zone:       "ITBG",
		FlavorName: "gp1.small",
		Region:     "ITBG-Bergamo",
		ProjectReference: v1alpha1.ResourceReference{
			Name:      projectName,
			Namespace: "default",
		},
		VPCReference: v1alpha1.ResourceReference{
			Name:      vpcName,
			Namespace: "default",
		},
		BootVolumeReference: v1alpha1.ResourceReference{
			Name:      bootVolName,
			Namespace: "default",
		},
		KeyPairReference: v1alpha1.ResourceReference{
			Name:      csKPName,
			Namespace: "default",
		},
		SubnetReferences: []v1alpha1.ResourceReference{
			{Name: subnetName, Namespace: "default"},
		},
		SecurityGroupReferences: []v1alpha1.ResourceReference{
			{Name: sgName, Namespace: "default"},
		},
	}
}

func createTestCS(ctx context.Context, name string, spec v1alpha1.CloudServerSpec) *v1alpha1.CloudServer {
	cs := &v1alpha1.CloudServer{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: spec,
	}
	ExpectWithOffset(1, k8sClient.Create(ctx, cs)).To(Succeed())
	return cs
}

func setCSStatus(
	ctx context.Context,
	cs *v1alpha1.CloudServer,
	phase v1alpha1.ResourcePhase,
	reason string,
	resourceID, projectID, vpcID, bootVolumeID, keyPairID string,
	subnetIDs, sgIDs []string,
	observedGen int64,
	conditionTime time.Time,
) {
	s := cs.DeepCopy()
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), s)).To(Succeed())
	s.Status.Phase = phase
	s.Status.ResourceID = resourceID
	s.Status.ProjectID = projectID
	s.Status.VPCID = vpcID
	s.Status.BootVolumeID = bootVolumeID
	s.Status.KeyPairID = keyPairID
	s.Status.SubnetIDs = subnetIDs
	s.Status.SecurityGroupIDs = sgIDs
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
	ExpectWithOffset(1, k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), cs)).To(Succeed())
}

// --- Mock setup ---

type csMocks struct {
	r              *CloudServerReconciler
	mockAruba      *arubamocks.MockClient
	mockProject    *arubamocks.MockProjectClient
	mockNetwork    *arubamocks.MockNetworkClient
	mockVPCs       *arubamocks.MockVPCsClient
	mockSubnets    *arubamocks.MockSubnetsClient
	mockSGs        *arubamocks.MockSecurityGroupsClient
	mockCompute    *arubamocks.MockComputeClient
	mockCSs        *arubamocks.MockCloudServersClient
	mockKPs        *arubamocks.MockKeyPairsClient
	mockStorage    *arubamocks.MockStorageClient
	mockVolumes    *arubamocks.MockVolumesClient
	mockElasticIPs *arubamocks.MockElasticIPsClient
}

func newCSReconcilerWithMocks(t GinkgoTInterface) *csMocks {
	mockAruba := arubamocks.NewMockClient(t)
	mockProject := arubamocks.NewMockProjectClient(t)
	mockNetwork := arubamocks.NewMockNetworkClient(t)
	mockVPCs := arubamocks.NewMockVPCsClient(t)
	mockSubnets := arubamocks.NewMockSubnetsClient(t)
	mockSGs := arubamocks.NewMockSecurityGroupsClient(t)
	mockCompute := arubamocks.NewMockComputeClient(t)
	mockCSs := arubamocks.NewMockCloudServersClient(t)
	mockKPs := arubamocks.NewMockKeyPairsClient(t)
	mockStorage := arubamocks.NewMockStorageClient(t)
	mockVolumes := arubamocks.NewMockVolumesClient(t)
	mockElasticIPs := arubamocks.NewMockElasticIPsClient(t)

	r := NewCloudServerReconciler(newTestReconciler(t, mockAruba))

	return &csMocks{
		r:              r,
		mockAruba:      mockAruba,
		mockProject:    mockProject,
		mockNetwork:    mockNetwork,
		mockVPCs:       mockVPCs,
		mockSubnets:    mockSubnets,
		mockSGs:        mockSGs,
		mockCompute:    mockCompute,
		mockCSs:        mockCSs,
		mockKPs:        mockKPs,
		mockStorage:    mockStorage,
		mockVolumes:    mockVolumes,
		mockElasticIPs: mockElasticIPs,
	}
}

// expectFullDependencies sets up all 7 dependency mock expectations plus the CloudServer list.
// This covers the standard non-delete reconciliation for the default spec (1 subnet, 1 SG, no keypair, no elasticIP).
func (m *csMocks) expectFullDependencies(
	projectID, vpcID, bootVolID, subnetID, sgID, csName string,
	cmpCSResponses ...*arubatypes.CloudServerResponse,
) {
	m.expectProjectList(projectID, "test-cs-project")
	m.expectVpcList(projectID, vpcID, "test-cs-vpc")
	m.expectBootVolumeList(projectID, bootVolID, "test-cs-bootvol")
	m.expectSubnetList(projectID, vpcID, subnetID, "test-cs-subnet")
	m.expectSGList(projectID, vpcID, sgID, "test-cs-sg")
	m.expectKeyPairList(projectID, csKPID, csKPName)
	m.expectCSList(projectID, cmpCSResponses...)
}

func (m *csMocks) expectProjectList(projectID, projectName string) {
	m.mockAruba.EXPECT().FromProject().Return(m.mockProject)
	m.mockProject.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectListForCS(projectID, projectName), nil)
}

func (m *csMocks) expectVpcList(projectID, vpcID, vpcName string) {
	m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
	m.mockNetwork.EXPECT().VPCs().Return(m.mockVPCs)
	m.mockVPCs.EXPECT().List(mock.Anything, projectID, mock.Anything).Return(buildVpcListForCS(vpcID, vpcName), nil)
}

func (m *csMocks) expectBootVolumeList(projectID, volID, volName string) {
	m.mockAruba.EXPECT().FromStorage().Return(m.mockStorage)
	m.mockStorage.EXPECT().Volumes().Return(m.mockVolumes)
	m.mockVolumes.EXPECT().List(mock.Anything, projectID, mock.Anything).Return(buildBootVolumeListForCS(volID, volName), nil)
}

func (m *csMocks) expectSubnetList(projectID, vpcID, subnetID, subnetName string) {
	m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
	m.mockNetwork.EXPECT().Subnets().Return(m.mockSubnets)
	m.mockSubnets.EXPECT().List(mock.Anything, projectID, vpcID, mock.Anything).Return(buildSubnetListForCS(subnetID, subnetName), nil)
}

func (m *csMocks) expectSGList(projectID, vpcID, sgID, sgName string) {
	m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
	m.mockNetwork.EXPECT().SecurityGroups().Return(m.mockSGs)
	m.mockSGs.EXPECT().List(mock.Anything, projectID, vpcID, mock.Anything).Return(buildSGListForCS(sgID, sgName), nil)
}

func (m *csMocks) expectKeyPairList(projectID, kpID, kpName string) {
	m.mockAruba.EXPECT().FromCompute().Return(m.mockCompute)
	m.mockCompute.EXPECT().KeyPairs().Return(m.mockKPs)
	m.mockKPs.EXPECT().List(mock.Anything, projectID, mock.Anything).Return(buildKeyPairListForCS(kpID, kpName), nil)
}

func (m *csMocks) expectCSList(projectID string, responses ...*arubatypes.CloudServerResponse) {
	m.mockAruba.EXPECT().FromCompute().Return(m.mockCompute)
	m.mockCompute.EXPECT().CloudServers().Return(m.mockCSs)
	m.mockCSs.EXPECT().List(mock.Anything, projectID, mock.Anything).Return(buildCSList(responses...), nil)
}

// --- Tests ---

const (
	csProjectName = "test-cs-project"
	csProjectID   = "cs-proj-id-1"
	csVpcName     = "test-cs-vpc"
	csVpcID       = "cs-vpc-id-1"
	csBootVolName = "test-cs-bootvol"
	csBootVolID   = "cs-bootvol-id-1"
	csSubnetName  = "test-cs-subnet"
	csSubnetID    = "cs-subnet-id-1"
	csSGName      = "test-cs-sg"
	csSGID        = "cs-sg-id-1"
	csKPName      = "test-cs-keypair"
	csKPID        = "cs-kp-id-1"
)

var _ = Describe("CloudServerReconciler", func() {
	var (
		ctx context.Context
		cs  *v1alpha1.CloudServer
	)

	BeforeEach(func() {
		ctx = context.Background()
	})

	AfterEach(func() {
		if cs != nil {
			s := &v1alpha1.CloudServer{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), s); err == nil {
				s.Finalizers = nil
				_ = k8sClient.Update(ctx, s)
				_ = k8sClient.Delete(ctx, s)
			}
			cs = nil
		}
	})

	Describe("First reconciliation", func() {
		It("transitions to Creating+ShallSynchronize when CMP has no CloudServer", func() {
			m := newCSReconcilerWithMocks(GinkgoT())
			cs = createTestCS(ctx, "test-cs-first", defaultCSSpec(csProjectName, csVpcName, csBootVolName, csSubnetName, csSGName))
			setCSStatus(ctx, cs, v1alpha1.ResourcePhasePending, v1alpha1.ConditionReasonSynchronized, "", "", "", "", "", nil, nil, 0, time.Now())

			m.expectFullDependencies(csProjectID, csVpcID, csBootVolID, csSubnetID, csSGID, "test-cs-first")

			_, err := m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())

			updated := &v1alpha1.CloudServer{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), updated)).To(Succeed())
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
			m := newCSReconcilerWithMocks(GinkgoT())
			cs = createTestCS(ctx, "test-cs-pending-deleting", defaultCSSpec(csProjectName, csVpcName, csBootVolName, csSubnetName, csSGName))
			setCSStatus(ctx, cs, v1alpha1.ResourcePhasePending, v1alpha1.ConditionReasonSynchronized, "", "", "", "", "", nil, nil, 0, time.Now())

			m.expectFullDependencies(csProjectID, csVpcID, csBootVolID, csSubnetID, csSGID, "test-cs-pending-deleting")

			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), cs)).To(Succeed())
			cs.Finalizers = []string{cloudServerFinalizerName}
			Expect(k8sClient.Update(ctx, cs)).To(Succeed())

			Expect(k8sClient.Delete(ctx, cs)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), cs)).To(Succeed())

			_, err := m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())

			updated := &v1alpha1.CloudServer{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleted))

			pendingCond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhasePending))
			Expect(pendingCond).NotTo(BeNil())
			Expect(pendingCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(pendingCond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronized))
		})
	})

	Describe("Create on CMP", func() {
		It("transitions to Creating+Synchronizing after successful CMP create", func() {
			m := newCSReconcilerWithMocks(GinkgoT())
			cs = createTestCS(ctx, "test-cs-create-cmp", defaultCSSpec(csProjectName, csVpcName, csBootVolName, csSubnetName, csSGName))
			setCSStatus(ctx, cs, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize,
				"", "", "", "", "", nil, nil, 0, time.Now())

			m.expectFullDependencies(csProjectID, csVpcID, csBootVolID, csSubnetID, csSGID, "test-cs-create-cmp")
			m.mockAruba.EXPECT().FromCompute().Return(m.mockCompute)
			m.mockCompute.EXPECT().CloudServers().Return(m.mockCSs)
			m.mockCSs.EXPECT().Create(mock.Anything, csProjectID, mock.Anything, mock.Anything).Return(buildCSCRUDResponse(http.StatusCreated), nil)

			_, err := m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())

			updated := &v1alpha1.CloudServer{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
		})
	})

	Describe("Waiting creation (CloudServer not yet in CMP)", func() {
		It("returns LongRequeue when CMP has no CloudServer", func() {
			m := newCSReconcilerWithMocks(GinkgoT())
			cs = createTestCS(ctx, "test-cs-wait-create", defaultCSSpec(csProjectName, csVpcName, csBootVolName, csSubnetName, csSGName))
			setCSStatus(ctx, cs, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing,
				"", "", "", "", "", nil, nil, 0, time.Now())

			m.expectFullDependencies(csProjectID, csVpcID, csBootVolID, csSubnetID, csSGID, "test-cs-wait-create")

			result, err := m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("Waiting creation (CloudServer in transitory CMP state)", func() {
		It("returns LongRequeue when CMP state is Creating", func() {
			m := newCSReconcilerWithMocks(GinkgoT())
			cs = createTestCS(ctx, "test-cs-wait-create-transitory", defaultCSSpec(csProjectName, csVpcName, csBootVolName, csSubnetName, csSGName))
			setCSStatus(ctx, cs, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing,
				"", "", "", "", "", nil, nil, 0, time.Now())

			cmpCS := buildCSResponse("cs-id-1", "test-cs-wait-create-transitory", arubatypes.StateCreating)
			m.expectFullDependencies(csProjectID, csVpcID, csBootVolID, csSubnetID, csSGID, "test-cs-wait-create-transitory", cmpCS)

			result, err := m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("Creation confirmed on CMP", func() {
		It("transitions to Creating+Synchronized when CMP CloudServer is active", func() {
			m := newCSReconcilerWithMocks(GinkgoT())
			cs = createTestCS(ctx, "test-cs-creation-confirmed", defaultCSSpec(csProjectName, csVpcName, csBootVolName, csSubnetName, csSGName))
			setCSStatus(ctx, cs, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing,
				"", "", "", "", "", nil, nil, 0, time.Now())

			cmpCS := buildCSResponse("cs-id-1", "test-cs-creation-confirmed", arubatypes.StateActive)
			m.expectFullDependencies(csProjectID, csVpcID, csBootVolID, csSubnetID, csSGID, "test-cs-creation-confirmed", cmpCS)

			_, err := m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())

			updated := &v1alpha1.CloudServer{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronized))
		})
	})

	Describe("Creation accomplished", func() {
		It("transitions to Active+Synchronized and stamps ResourceID and parent IDs", func() {
			m := newCSReconcilerWithMocks(GinkgoT())
			cs = createTestCS(ctx, "test-cs-creation-accomplished", defaultCSSpec(csProjectName, csVpcName, csBootVolName, csSubnetName, csSGName))
			setCSStatus(ctx, cs, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized,
				"", "", "", "", "", nil, nil, 0, time.Now())

			cmpCS := buildCSResponse("cs-id-1", "test-cs-creation-accomplished", arubatypes.StateActive)
			m.expectFullDependencies(csProjectID, csVpcID, csBootVolID, csSubnetID, csSGID, "test-cs-creation-accomplished", cmpCS)

			_, err := m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())

			updated := &v1alpha1.CloudServer{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseActive))
			Expect(updated.Status.ResourceID).To(Equal("cs-id-1"))
			Expect(updated.Status.ProjectID).To(Equal(csProjectID))
			Expect(updated.Status.VPCID).To(Equal(csVpcID))
			Expect(updated.Status.BootVolumeID).To(Equal(csBootVolID))
			Expect(updated.Status.SubnetIDs).To(ConsistOf(csSubnetID))
			Expect(updated.Status.SecurityGroupIDs).To(ConsistOf(csSGID))
		})
	})

	Describe("HasDeniedChanges", func() {
		It("returns LongRequeue when immutable field (flavorName) is changed", func() {
			m := newCSReconcilerWithMocks(GinkgoT())
			cs = createTestCS(ctx, "test-cs-denied-changes", defaultCSSpec(csProjectName, csVpcName, csBootVolName, csSubnetName, csSGName))
			setCSStatus(ctx, cs, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized,
				"cs-id-1", csProjectID, csVpcID, csBootVolID, csKPID,
				[]string{csSubnetID}, []string{csSGID}, 1, time.Now())

			// Force generation change with different flavorName
			csFetch := &v1alpha1.CloudServer{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), csFetch)).To(Succeed())
			csFetch.Spec.FlavorName = "gp1.medium"
			Expect(k8sClient.Update(ctx, csFetch)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), cs)).To(Succeed())

			// CMP still has original flavor name "gp1.small"
			cmpCS := buildCSResponse("cs-id-1", "test-cs-denied-changes", arubatypes.StateActive)
			m.expectFullDependencies(csProjectID, csVpcID, csBootVolID, csSubnetID, csSGID, "test-cs-denied-changes", cmpCS)

			result, err := m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("SpecAlreadyInSyncWithCMP", func() {
		It("re-stamps ObservedGeneration when spec hasn't changed", func() {
			m := newCSReconcilerWithMocks(GinkgoT())
			cs = createTestCS(ctx, "test-cs-spec-in-sync", defaultCSSpec(csProjectName, csVpcName, csBootVolName, csSubnetName, csSGName))
			setCSStatus(ctx, cs, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized,
				"cs-id-1", csProjectID, csVpcID, csBootVolID, csKPID,
				[]string{csSubnetID}, []string{csSGID}, 1, time.Now())

			// Trigger generation bump with same tags
			csFetch := &v1alpha1.CloudServer{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), csFetch)).To(Succeed())
			csFetch.Spec.Tags = []string{"tag1"}
			Expect(k8sClient.Update(ctx, csFetch)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), cs)).To(Succeed())

			// CMP matches: same tags, same flavor
			cmpCS := buildCSResponse("cs-id-1", "test-cs-spec-in-sync", arubatypes.StateActive)
			cmpCS.Metadata.Tags = []string{"tag1"}
			m.expectFullDependencies(csProjectID, csVpcID, csBootVolID, csSubnetID, csSGID, "test-cs-spec-in-sync", cmpCS)

			_, err := m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())

			updated := &v1alpha1.CloudServer{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseActive))
			Expect(updated.Status.ObservedGeneration).To(Equal(cs.Generation))
		})
	})

	Describe("ShouldBeUpdated", func() {
		It("transitions to Updating+ShallSynchronize when tags differ", func() {
			m := newCSReconcilerWithMocks(GinkgoT())
			cs = createTestCS(ctx, "test-cs-should-update", defaultCSSpec(csProjectName, csVpcName, csBootVolName, csSubnetName, csSGName))
			setCSStatus(ctx, cs, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized,
				"cs-id-1", csProjectID, csVpcID, csBootVolID, csKPID,
				[]string{csSubnetID}, []string{csSGID}, 1, time.Now())

			// Change tags to trigger update
			csFetch := &v1alpha1.CloudServer{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), csFetch)).To(Succeed())
			csFetch.Spec.Tags = []string{"tag1", "tag2"}
			Expect(k8sClient.Update(ctx, csFetch)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), cs)).To(Succeed())

			// CMP has old tags
			cmpCS := buildCSResponse("cs-id-1", "test-cs-should-update", arubatypes.StateActive)
			cmpCS.Metadata.Tags = []string{"tag1"}
			m.expectFullDependencies(csProjectID, csVpcID, csBootVolID, csSubnetID, csSGID, "test-cs-should-update", cmpCS)

			_, err := m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())

			updated := &v1alpha1.CloudServer{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseUpdating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseUpdating))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
		})
	})

	Describe("Update on CMP", func() {
		It("transitions to Updating+Synchronizing after successful CMP update", func() {
			m := newCSReconcilerWithMocks(GinkgoT())
			cs = createTestCS(ctx, "test-cs-update-cmp", defaultCSSpec(csProjectName, csVpcName, csBootVolName, csSubnetName, csSGName))
			setCSStatus(ctx, cs, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize,
				"cs-id-1", csProjectID, csVpcID, csBootVolID, csKPID,
				[]string{csSubnetID}, []string{csSGID}, 1, time.Now())

			cmpCS := buildCSResponse("cs-id-1", "test-cs-update-cmp", arubatypes.StateActive)
			m.expectFullDependencies(csProjectID, csVpcID, csBootVolID, csSubnetID, csSGID, "test-cs-update-cmp", cmpCS)
			m.mockAruba.EXPECT().FromCompute().Return(m.mockCompute)
			m.mockCompute.EXPECT().CloudServers().Return(m.mockCSs)
			m.mockCSs.EXPECT().Update(mock.Anything, csProjectID, "cs-id-1", mock.Anything, mock.Anything).Return(buildCSCRUDResponse(http.StatusOK), nil)

			_, err := m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())

			updated := &v1alpha1.CloudServer{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseUpdating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseUpdating))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
		})
	})

	Describe("Should delete", func() {
		It("transitions to Deleting+ShallSynchronize when deletion is requested on Active CloudServer", func() {
			m := newCSReconcilerWithMocks(GinkgoT())
			cs = createTestCS(ctx, "test-cs-should-delete", defaultCSSpec(csProjectName, csVpcName, csBootVolName, csSubnetName, csSGName))
			csFetch := &v1alpha1.CloudServer{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), csFetch)).To(Succeed())
			csFetch.Finalizers = []string{cloudServerFinalizerName}
			Expect(k8sClient.Update(ctx, csFetch)).To(Succeed())
			setCSStatus(ctx, cs, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized,
				"cs-id-1", csProjectID, csVpcID, csBootVolID, csKPID,
				[]string{csSubnetID}, []string{csSGID}, 1, time.Now())
			Expect(k8sClient.Delete(ctx, cs)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), cs)).To(Succeed())

			// During deletion, cached IDs are used — only CS list is called
			cmpCS := buildCSResponse("cs-id-1", "test-cs-should-delete", arubatypes.StateActive)
			m.mockAruba.EXPECT().FromCompute().Return(m.mockCompute)
			m.mockCompute.EXPECT().CloudServers().Return(m.mockCSs)
			m.mockCSs.EXPECT().List(mock.Anything, csProjectID, mock.Anything).Return(buildCSList(cmpCS), nil)

			_, err := m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())

			updated := &v1alpha1.CloudServer{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
		})
	})

	Describe("Delete on CMP", func() {
		It("transitions to Deleting+Synchronizing after successful CMP delete", func() {
			m := newCSReconcilerWithMocks(GinkgoT())
			cs = createTestCS(ctx, "test-cs-delete-cmp", defaultCSSpec(csProjectName, csVpcName, csBootVolName, csSubnetName, csSGName))
			csFetch := &v1alpha1.CloudServer{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), csFetch)).To(Succeed())
			csFetch.Finalizers = []string{cloudServerFinalizerName}
			Expect(k8sClient.Update(ctx, csFetch)).To(Succeed())
			setCSStatus(ctx, cs, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize,
				"cs-id-1", csProjectID, csVpcID, csBootVolID, csKPID,
				[]string{csSubnetID}, []string{csSGID}, 1, time.Now())
			Expect(k8sClient.Delete(ctx, cs)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), cs)).To(Succeed())

			cmpCS := buildCSResponse("cs-id-1", "test-cs-delete-cmp", arubatypes.StateActive)
			m.mockAruba.EXPECT().FromCompute().Return(m.mockCompute)
			m.mockCompute.EXPECT().CloudServers().Return(m.mockCSs)
			m.mockCSs.EXPECT().List(mock.Anything, csProjectID, mock.Anything).Return(buildCSList(cmpCS), nil)
			m.mockAruba.EXPECT().FromCompute().Return(m.mockCompute)
			m.mockCompute.EXPECT().CloudServers().Return(m.mockCSs)
			m.mockCSs.EXPECT().Delete(mock.Anything, csProjectID, "cs-id-1", mock.Anything).Return(buildDeleteResponse(http.StatusOK), nil)

			_, err := m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())

			updated := &v1alpha1.CloudServer{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
		})
	})

	Describe("Deletion with CMP already gone", func() {
		It("transitions directly to Deleting+Synchronized without calling CMP delete", func() {
			m := newCSReconcilerWithMocks(GinkgoT())
			cs = createTestCS(ctx, "test-cs-delete-already-gone", defaultCSSpec(csProjectName, csVpcName, csBootVolName, csSubnetName, csSGName))
			csFetch := &v1alpha1.CloudServer{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), csFetch)).To(Succeed())
			csFetch.Finalizers = []string{cloudServerFinalizerName}
			Expect(k8sClient.Update(ctx, csFetch)).To(Succeed())
			setCSStatus(ctx, cs, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize,
				"cs-id-1", csProjectID, csVpcID, csBootVolID, csKPID,
				[]string{csSubnetID}, []string{csSGID}, 1, time.Now())
			Expect(k8sClient.Delete(ctx, cs)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), cs)).To(Succeed())

			// CMP returns empty list (CS already gone)
			m.mockAruba.EXPECT().FromCompute().Return(m.mockCompute)
			m.mockCompute.EXPECT().CloudServers().Return(m.mockCSs)
			m.mockCSs.EXPECT().List(mock.Anything, csProjectID, mock.Anything).Return(buildCSList(), nil)

			_, err := m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())

			updated := &v1alpha1.CloudServer{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronized))
		})
	})

	Describe("Deletion accomplished", func() {
		It("transitions to Deleted when CMP CloudServer is gone after confirmed deletion", func() {
			m := newCSReconcilerWithMocks(GinkgoT())
			cs = createTestCS(ctx, "test-cs-deletion-accomplished", defaultCSSpec(csProjectName, csVpcName, csBootVolName, csSubnetName, csSGName))
			csFetch := &v1alpha1.CloudServer{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), csFetch)).To(Succeed())
			csFetch.Finalizers = []string{cloudServerFinalizerName}
			Expect(k8sClient.Update(ctx, csFetch)).To(Succeed())
			setCSStatus(ctx, cs, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronized,
				"cs-id-1", csProjectID, csVpcID, csBootVolID, csKPID,
				[]string{csSubnetID}, []string{csSGID}, 1, time.Now())
			Expect(k8sClient.Delete(ctx, cs)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), cs)).To(Succeed())

			m.mockAruba.EXPECT().FromCompute().Return(m.mockCompute)
			m.mockCompute.EXPECT().CloudServers().Return(m.mockCSs)
			m.mockCSs.EXPECT().List(mock.Anything, csProjectID, mock.Anything).Return(buildCSList(), nil)

			_, err := m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())

			updated := &v1alpha1.CloudServer{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleted))
		})
	})

	Describe("Phase timeout", func() {
		It("transitions to Failed when stuck in transitory phase too long", func() {
			m := newCSReconcilerWithMocks(GinkgoT())
			cs = createTestCS(ctx, "test-cs-timeout", defaultCSSpec(csProjectName, csVpcName, csBootVolName, csSubnetName, csSGName))
			setCSStatus(ctx, cs, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize,
				"", csProjectID, csVpcID, csBootVolID, "", nil, nil,
				0, time.Now().Add(-(reconciler.MaxPhaseTimeout + time.Minute)))

			cmpCS := buildCSResponse("cs-id-1", "test-cs-timeout", arubatypes.StateActive)
			m.expectFullDependencies(csProjectID, csVpcID, csBootVolID, csSubnetID, csSGID, "test-cs-timeout", cmpCS)

			_, err := m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())

			updated := &v1alpha1.CloudServer{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
		})
	})

	Describe("IsInError", func() {
		It("transitions to Failed+Synchronized when CMP state is Failed", func() {
			m := newCSReconcilerWithMocks(GinkgoT())
			cs = createTestCS(ctx, "test-cs-in-error", defaultCSSpec(csProjectName, csVpcName, csBootVolName, csSubnetName, csSGName))
			setCSStatus(ctx, cs, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing,
				"cs-id-1", csProjectID, csVpcID, csBootVolID, csKPID,
				[]string{csSubnetID}, []string{csSGID}, 1, time.Now())

			cmpCS := buildCSResponse("cs-id-1", "test-cs-in-error", arubatypes.StateFailed)
			m.expectFullDependencies(csProjectID, csVpcID, csBootVolID, csSubnetID, csSGID, "test-cs-in-error", cmpCS)

			_, err := m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())

			updated := &v1alpha1.CloudServer{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseFailed))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronized))
		})
	})

	Describe("Project not found yet", func() {
		It("returns LongRequeue when project doesn't exist in CMP yet", func() {
			m := newCSReconcilerWithMocks(GinkgoT())
			cs = createTestCS(ctx, "test-cs-no-project", defaultCSSpec(csProjectName, csVpcName, csBootVolName, csSubnetName, csSGName))

			m.mockAruba.EXPECT().FromProject().Return(m.mockProject)
			m.mockProject.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectList(), nil)

			result, err := m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("Boot volume not found yet", func() {
		It("returns LongRequeue when boot volume doesn't exist in CMP yet", func() {
			m := newCSReconcilerWithMocks(GinkgoT())
			cs = createTestCS(ctx, "test-cs-no-bootvol", defaultCSSpec(csProjectName, csVpcName, csBootVolName, csSubnetName, csSGName))

			m.expectProjectList(csProjectID, csProjectName)
			m.expectVpcList(csProjectID, csVpcID, csVpcName)

			emptyVolList := &arubatypes.Response[arubatypes.BlockStorageListResponse]{
				Data:       &arubatypes.BlockStorageListResponse{},
				StatusCode: http.StatusOK,
			}
			m.mockAruba.EXPECT().FromStorage().Return(m.mockStorage)
			m.mockStorage.EXPECT().Volumes().Return(m.mockVolumes)
			m.mockVolumes.EXPECT().List(mock.Anything, csProjectID, mock.Anything).Return(emptyVolList, nil)

			result, err := m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	// WORKAROUND: tests for the dependency readiness check.
	// TODO: remove once the CMP Infra Team fixes the root cause.
	Describe("Boot volume not ready yet (WORKAROUND)", func() {
		It("returns LongRequeue when boot volume is in a transitory CMP state during creation", func() {
			m := newCSReconcilerWithMocks(GinkgoT())
			cs = createTestCS(ctx, "test-cs-bootvol-not-ready", defaultCSSpec(csProjectName, csVpcName, csBootVolName, csSubnetName, csSGName))

			m.expectProjectList(csProjectID, csProjectName)
			m.expectVpcList(csProjectID, csVpcID, csVpcName)
			m.mockAruba.EXPECT().FromStorage().Return(m.mockStorage)
			m.mockStorage.EXPECT().Volumes().Return(m.mockVolumes)
			m.mockVolumes.EXPECT().List(mock.Anything, csProjectID, mock.Anything).
				Return(buildBootVolumeListForCSWithState(csBootVolID, csBootVolName, arubatypes.StateInCreation), nil)

			result, err := m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})

		It("proceeds when boot volume is in Failed CMP state during creation (Final state, no block)", func() {
			m := newCSReconcilerWithMocks(GinkgoT())
			cs = createTestCS(ctx, "test-cs-bootvol-failed", defaultCSSpec(csProjectName, csVpcName, csBootVolName, csSubnetName, csSGName))
			setCSStatus(ctx, cs, v1alpha1.ResourcePhasePending, v1alpha1.ConditionReasonSynchronized, "", "", "", "", "", nil, nil, 0, time.Now())

			m.expectProjectList(csProjectID, csProjectName)
			m.expectVpcList(csProjectID, csVpcID, csVpcName)
			m.mockAruba.EXPECT().FromStorage().Return(m.mockStorage)
			m.mockStorage.EXPECT().Volumes().Return(m.mockVolumes)
			m.mockVolumes.EXPECT().List(mock.Anything, csProjectID, mock.Anything).
				Return(buildBootVolumeListForCSWithState(csBootVolID, csBootVolName, arubatypes.StateFailed), nil)
			m.expectSubnetList(csProjectID, csVpcID, csSubnetID, csSubnetName)
			m.expectSGList(csProjectID, csVpcID, csSGID, csSGName)
			m.expectKeyPairList(csProjectID, csKPID, csKPName)
			m.expectCSList(csProjectID)

			_, err := m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())

			updated := &v1alpha1.CloudServer{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
		})

		It("does not block when boot volume is in transitory state during an update (not creation)", func() {
			m := newCSReconcilerWithMocks(GinkgoT())
			cs = createTestCS(ctx, "test-cs-bootvol-update-no-block", defaultCSSpec(csProjectName, csVpcName, csBootVolName, csSubnetName, csSGName))
			setCSStatus(ctx, cs, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized,
				"cs-id-1", csProjectID, csVpcID, csBootVolID, csKPID,
				[]string{csSubnetID}, []string{csSGID}, 1, time.Now())

			cmpCS := buildCSResponse("cs-id-1", "test-cs-bootvol-update-no-block", arubatypes.StateActive)
			m.expectProjectList(csProjectID, csProjectName)
			m.expectVpcList(csProjectID, csVpcID, csVpcName)
			m.mockAruba.EXPECT().FromStorage().Return(m.mockStorage)
			m.mockStorage.EXPECT().Volumes().Return(m.mockVolumes)
			m.mockVolumes.EXPECT().List(mock.Anything, csProjectID, mock.Anything).
				Return(buildBootVolumeListForCSWithState(csBootVolID, csBootVolName, arubatypes.StateInCreation), nil)
			m.expectSubnetList(csProjectID, csVpcID, csSubnetID, csSubnetName)
			m.expectSGList(csProjectID, csVpcID, csSGID, csSGName)
			m.expectKeyPairList(csProjectID, csKPID, csKPName)
			m.expectCSList(csProjectID, cmpCS)

			_, err := m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())

			updated := &v1alpha1.CloudServer{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseActive))
		})
	})

	// WORKAROUND: tests for the dependency readiness check; remove once the CMP Infra Team
	// fixes the root cause (see ai/plan/server_wait_resources.md).
	Describe("Subnet not ready yet (WORKAROUND)", func() {
		It("returns LongRequeue when subnet is in a transitory CMP state during creation", func() {
			m := newCSReconcilerWithMocks(GinkgoT())
			cs = createTestCS(ctx, "test-cs-subnet-not-ready", defaultCSSpec(csProjectName, csVpcName, csBootVolName, csSubnetName, csSGName))

			m.expectProjectList(csProjectID, csProjectName)
			m.expectVpcList(csProjectID, csVpcID, csVpcName)
			m.expectBootVolumeList(csProjectID, csBootVolID, csBootVolName)
			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
			m.mockNetwork.EXPECT().Subnets().Return(m.mockSubnets)
			m.mockSubnets.EXPECT().List(mock.Anything, csProjectID, csVpcID, mock.Anything).
				Return(buildSubnetListForCSWithState(csSubnetID, csSubnetName, arubatypes.StateCreating), nil)

			result, err := m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("Parent IDs stamped in status", func() {
		It("stamps all parent IDs on status when first transitioning", func() {
			m := newCSReconcilerWithMocks(GinkgoT())
			cs = createTestCS(ctx, "test-cs-ids-stamped", defaultCSSpec(csProjectName, csVpcName, csBootVolName, csSubnetName, csSGName))
			setCSStatus(ctx, cs, v1alpha1.ResourcePhasePending, v1alpha1.ConditionReasonSynchronized, "", "", "", "", "", nil, nil, 0, time.Now())

			m.expectFullDependencies(csProjectID, csVpcID, csBootVolID, csSubnetID, csSGID, "test-cs-ids-stamped")

			_, err := m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())

			updated := &v1alpha1.CloudServer{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), updated)).To(Succeed())
			Expect(updated.Status.ProjectID).To(Equal(csProjectID))
			Expect(updated.Status.VPCID).To(Equal(csVpcID))
			Expect(updated.Status.BootVolumeID).To(Equal(csBootVolID))
			Expect(updated.Status.SubnetIDs).To(ConsistOf(csSubnetID))
			Expect(updated.Status.SecurityGroupIDs).To(ConsistOf(csSGID))
		})
	})

	Describe("CMP error handling", func() {
		DescribeTable("CMP create fails — preserves Creating+ShallSynchronize, surfaces error in condition",
			func(name string, statusCode int, expectedRequeue time.Duration) {
				m := newCSReconcilerWithMocks(GinkgoT())
				cs = createTestCS(ctx, name, defaultCSSpec(csProjectName, csVpcName, csBootVolName, csSubnetName, csSGName))
				setCSStatus(ctx, cs, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize,
					"", "", "", "", "", nil, nil, 0, time.Now())

				m.expectFullDependencies(csProjectID, csVpcID, csBootVolID, csSubnetID, csSGID, name)
				m.mockAruba.EXPECT().FromCompute().Return(m.mockCompute)
				m.mockCompute.EXPECT().CloudServers().Return(m.mockCSs)
				m.mockCSs.EXPECT().Create(mock.Anything, csProjectID, mock.Anything, mock.Anything).Return(buildCSCRUDResponse(statusCode), nil)

				result, err := m.r.HandleReconcile(ctx, cs)
				Expect(err).To(Succeed())
				Expect(result.RequeueAfter).To(Equal(expectedRequeue))

				updated := &v1alpha1.CloudServer{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), updated)).To(Succeed())
				Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
				cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
				Expect(cond).NotTo(BeNil())
				Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
				Expect(cond.Message).To(ContainSubstring("ERROR"))
			},
			Entry("4xx → LongRequeueAfter, no phase change", "cs-cmp-err-create-400", http.StatusBadRequest, reconciler.LongRequeueAfter),
			Entry("5xx → ShortRequeueAfter, no phase change", "cs-cmp-err-create-500", http.StatusInternalServerError, reconciler.ShortRequeueAfter),
		)

		DescribeTable("CMP update fails — preserves Updating+ShallSynchronize, surfaces error in condition",
			func(name string, statusCode int, expectedRequeue time.Duration) {
				m := newCSReconcilerWithMocks(GinkgoT())
				cs = createTestCS(ctx, name, defaultCSSpec(csProjectName, csVpcName, csBootVolName, csSubnetName, csSGName))
				setCSStatus(ctx, cs, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize,
					"cs-id-1", csProjectID, csVpcID, csBootVolID, csKPID,
					[]string{csSubnetID}, []string{csSGID}, 1, time.Now())

				cmpCS := buildCSResponse("cs-id-1", name, arubatypes.StateActive)
				m.expectFullDependencies(csProjectID, csVpcID, csBootVolID, csSubnetID, csSGID, name, cmpCS)
				m.mockAruba.EXPECT().FromCompute().Return(m.mockCompute)
				m.mockCompute.EXPECT().CloudServers().Return(m.mockCSs)
				m.mockCSs.EXPECT().Update(mock.Anything, csProjectID, "cs-id-1", mock.Anything, mock.Anything).Return(buildCSCRUDResponse(statusCode), nil)

				result, err := m.r.HandleReconcile(ctx, cs)
				Expect(err).To(Succeed())
				Expect(result.RequeueAfter).To(Equal(expectedRequeue))

				updated := &v1alpha1.CloudServer{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), updated)).To(Succeed())
				Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseUpdating))
				cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseUpdating))
				Expect(cond).NotTo(BeNil())
				Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
				Expect(cond.Message).To(ContainSubstring("ERROR"))
			},
			Entry("4xx → LongRequeueAfter, no phase change", "cs-cmp-err-update-400", http.StatusBadRequest, reconciler.LongRequeueAfter),
			Entry("5xx → ShortRequeueAfter, no phase change", "cs-cmp-err-update-500", http.StatusInternalServerError, reconciler.ShortRequeueAfter),
		)

		DescribeTable("CMP delete fails — preserves Deleting+ShallSynchronize, surfaces error in condition",
			func(name string, statusCode int, expectedRequeue time.Duration) {
				m := newCSReconcilerWithMocks(GinkgoT())
				cs = createTestCS(ctx, name, defaultCSSpec(csProjectName, csVpcName, csBootVolName, csSubnetName, csSGName))
				csFetch := &v1alpha1.CloudServer{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), csFetch)).To(Succeed())
				csFetch.Finalizers = []string{cloudServerFinalizerName}
				Expect(k8sClient.Update(ctx, csFetch)).To(Succeed())
				setCSStatus(ctx, cs, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize,
					"cs-id-1", csProjectID, csVpcID, csBootVolID, csKPID,
					[]string{csSubnetID}, []string{csSGID}, 1, time.Now())
				Expect(k8sClient.Delete(ctx, cs)).To(Succeed())
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), cs)).To(Succeed())

				cmpCS := buildCSResponse("cs-id-1", name, arubatypes.StateActive)
				m.mockAruba.EXPECT().FromCompute().Return(m.mockCompute)
				m.mockCompute.EXPECT().CloudServers().Return(m.mockCSs)
				m.mockCSs.EXPECT().List(mock.Anything, csProjectID, mock.Anything).Return(buildCSList(cmpCS), nil)
				m.mockAruba.EXPECT().FromCompute().Return(m.mockCompute)
				m.mockCompute.EXPECT().CloudServers().Return(m.mockCSs)
				m.mockCSs.EXPECT().Delete(mock.Anything, csProjectID, "cs-id-1", mock.Anything).Return(buildDeleteResponse(statusCode), nil)

				result, err := m.r.HandleReconcile(ctx, cs)
				Expect(err).To(Succeed())
				Expect(result.RequeueAfter).To(Equal(expectedRequeue))

				updated := &v1alpha1.CloudServer{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), updated)).To(Succeed())
				Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
				cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))
				Expect(cond).NotTo(BeNil())
				Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
				Expect(cond.Message).To(ContainSubstring("ERROR"))
			},
			Entry("4xx → LongRequeueAfter, no phase change", "cs-cmp-err-delete-400", http.StatusBadRequest, reconciler.LongRequeueAfter),
			Entry("5xx → ShortRequeueAfter, no phase change", "cs-cmp-err-delete-500", http.StatusInternalServerError, reconciler.ShortRequeueAfter),
		)
	})

	Describe("Pending-phase intention validation", func() {
		// Helper: create a K8s project and set it Active+Synchronized so Stage 3 (parent readiness) passes.
		csIVSProjectName := "cs-ivs-project"
		var kubeIVSProject *v1alpha1.Project

		BeforeEach(func() {
			kubeIVSProject = createTestProject(ctx, csIVSProjectName, v1alpha1.ProjectSpec{
				Tenant:      "test-tenant",
				Description: "ivs test project",
				Tags:        []string{},
			})
			setProjectStatus(ctx, kubeIVSProject, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "ivs-proj-id", 0, time.Now())
		})

		AfterEach(func() {
			_ = k8sClient.Delete(ctx, kubeIVSProject)
		})

		It("sets Failed+ValidationFailed at Pending phase when CloudServer VPC ref differs from Subnet VPC ref (no CMP resource yet)", func() {
			m := newCSReconcilerWithMocks(GinkgoT())

			kubeSubnet := createTestSubnet(ctx, csSubnetName+"-ivs-vpc", v1alpha1.SubnetSpec{
				Tenant: "test-tenant",
				Region: "ITBG-Bergamo",
				Type:   "Advanced",
				CIDR:   "10.0.0.0/24",
				VPCReference: v1alpha1.ResourceReference{
					Name:      "other-vpc",
					Namespace: "default",
				},
				ProjectReference: v1alpha1.ResourceReference{Name: csIVSProjectName, Namespace: "default"},
			})
			defer func() { _ = k8sClient.Delete(ctx, kubeSubnet) }()

			spec := defaultCSSpec(csIVSProjectName, csVpcName, csBootVolName, csSubnetName+"-ivs-vpc", csSGName)
			// CS VPCReference.Name is csVpcName, subnet VPCReference is "other-vpc"
			cs = createTestCS(ctx, "test-cs-pv-vpc-subnet", spec)
			setCSStatus(ctx, cs, v1alpha1.ResourcePhasePending, v1alpha1.ConditionReasonSynchronized, "", "", "", "", "", nil, nil, 0, time.Now())

			result, err := m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.ShortRequeueAfter))

			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), cs)).To(Succeed())

			// ivs fires at Stage 4 (before CMP calls) → validation fails, no CMP expectations needed.
			result, err = m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(BeZero())

			updated := &v1alpha1.CloudServer{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseFailed))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonIntentionValidationFailed))
			Expect(cond.Message).To(ContainSubstring("VPC reference mismatch with Subnets"))
		})

		It("sets Failed+ValidationFailed at Pending phase when CloudServer tenant differs from VPC tenant (no CMP resource yet)", func() {
			m := newCSReconcilerWithMocks(GinkgoT())

			kubeVpc := createTestVpc(ctx, csVpcName+"-ivs-tenant-vpc", v1alpha1.VPCSpec{
				Tenant:           "other-tenant",
				Region:           "ITBG-Bergamo",
				ProjectReference: v1alpha1.ResourceReference{Name: csIVSProjectName, Namespace: "default"},
			})
			defer func() { _ = k8sClient.Delete(ctx, kubeVpc) }()

			spec := defaultCSSpec(csIVSProjectName, csVpcName+"-ivs-tenant-vpc", csBootVolName, csSubnetName, csSGName)
			cs = createTestCS(ctx, "test-cs-pv-tenant-vpc", spec)
			setCSStatus(ctx, cs, v1alpha1.ResourcePhasePending, v1alpha1.ConditionReasonSynchronized, "", "", "", "", "", nil, nil, 0, time.Now())

			result, err := m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.ShortRequeueAfter))

			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), cs)).To(Succeed())

			// ivs fires at Stage 4 (before CMP calls) → validation fails, no CMP expectations needed.
			result, err = m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(BeZero())

			updated := &v1alpha1.CloudServer{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseFailed))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonIntentionValidationFailed))
			Expect(cond.Message).To(ContainSubstring("tenant"))
		})

		It("sets Failed+ValidationFailed at Pending phase when CloudServer tenant differs from BootVolume tenant (no CMP resource yet)", func() {
			m := newCSReconcilerWithMocks(GinkgoT())

			kubeBootVol := createTestBlockStorage(ctx, csBootVolName+"-ivs-tenant-bv", v1alpha1.BlockStorageSpec{
				Tenant:           "other-tenant",
				Region:           "ITBG-Bergamo",
				Zone:             "ITBG",
				SizeGB:           20,
				BillingPeriod:    "Hour",
				ProjectReference: v1alpha1.ResourceReference{Name: csIVSProjectName, Namespace: "default"},
			})
			defer func() { _ = k8sClient.Delete(ctx, kubeBootVol) }()

			spec := defaultCSSpec(csIVSProjectName, csVpcName, csBootVolName+"-ivs-tenant-bv", csSubnetName, csSGName)
			cs = createTestCS(ctx, "test-cs-pv-tenant-bv", spec)
			setCSStatus(ctx, cs, v1alpha1.ResourcePhasePending, v1alpha1.ConditionReasonSynchronized, "", "", "", "", "", nil, nil, 0, time.Now())

			result, err := m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.ShortRequeueAfter))

			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), cs)).To(Succeed())

			// ivs fires at Stage 4 (before CMP calls) → validation fails, no CMP expectations needed.
			result, err = m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(BeZero())

			updated := &v1alpha1.CloudServer{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseFailed))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonIntentionValidationFailed))
			Expect(cond.Message).To(ContainSubstring("tenant"))
		})

		It("sets Failed+ValidationFailed at Pending phase when CloudServer tenant differs from Subnet tenant (no CMP resource yet)", func() {
			m := newCSReconcilerWithMocks(GinkgoT())

			kubeSubnet := createTestSubnet(ctx, csSubnetName+"-ivs-tenant-sn", v1alpha1.SubnetSpec{
				Tenant:           "other-tenant",
				Region:           "ITBG-Bergamo",
				Type:             "Advanced",
				CIDR:             "10.1.0.0/24",
				VPCReference:     v1alpha1.ResourceReference{Name: csVpcName, Namespace: "default"},
				ProjectReference: v1alpha1.ResourceReference{Name: csIVSProjectName, Namespace: "default"},
			})
			defer func() { _ = k8sClient.Delete(ctx, kubeSubnet) }()

			spec := defaultCSSpec(csIVSProjectName, csVpcName, csBootVolName, csSubnetName+"-ivs-tenant-sn", csSGName)
			cs = createTestCS(ctx, "test-cs-pv-tenant-sn", spec)
			setCSStatus(ctx, cs, v1alpha1.ResourcePhasePending, v1alpha1.ConditionReasonSynchronized, "", "", "", "", "", nil, nil, 0, time.Now())

			result, err := m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.ShortRequeueAfter))

			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), cs)).To(Succeed())

			// ivs fires at Stage 4 (before CMP calls) → validation fails, no CMP expectations needed.
			result, err = m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(BeZero())

			updated := &v1alpha1.CloudServer{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseFailed))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonIntentionValidationFailed))
			Expect(cond.Message).To(ContainSubstring("tenant mismatch"))
		})

		It("sets Failed+ValidationFailed at Pending phase when CloudServer project reference differs from VPC project reference (no CMP resource yet)", func() {
			m := newCSReconcilerWithMocks(GinkgoT())

			kubeVpc := createTestVpc(ctx, csVpcName+"-ivs-proj-vpc", v1alpha1.VPCSpec{
				Tenant:           "test-tenant",
				Region:           "ITBG-Bergamo",
				ProjectReference: v1alpha1.ResourceReference{Name: "other-project", Namespace: "default"},
			})
			defer func() { _ = k8sClient.Delete(ctx, kubeVpc) }()

			spec := defaultCSSpec(csIVSProjectName, csVpcName+"-ivs-proj-vpc", csBootVolName, csSubnetName, csSGName)
			cs = createTestCS(ctx, "test-cs-pv-proj-vpc", spec)
			setCSStatus(ctx, cs, v1alpha1.ResourcePhasePending, v1alpha1.ConditionReasonSynchronized, "", "", "", "", "", nil, nil, 0, time.Now())

			result, err := m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.ShortRequeueAfter))

			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), cs)).To(Succeed())

			// ivs fires at Stage 4 (before CMP calls) → validation fails, no CMP expectations needed.
			result, err = m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(BeZero())

			updated := &v1alpha1.CloudServer{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseFailed))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonIntentionValidationFailed))
			Expect(cond.Message).To(ContainSubstring("project reference"))
		})

		It("sets Failed+ValidationFailed at Pending phase when CloudServer project reference differs from BootVolume project reference (no CMP resource yet)", func() {
			m := newCSReconcilerWithMocks(GinkgoT())

			kubeBootVol := createTestBlockStorage(ctx, csBootVolName+"-ivs-proj-bv", v1alpha1.BlockStorageSpec{
				Tenant:           "test-tenant",
				Region:           "ITBG-Bergamo",
				Zone:             "ITBG",
				SizeGB:           20,
				BillingPeriod:    "Hour",
				ProjectReference: v1alpha1.ResourceReference{Name: "other-project", Namespace: "default"},
			})
			defer func() { _ = k8sClient.Delete(ctx, kubeBootVol) }()

			spec := defaultCSSpec(csIVSProjectName, csVpcName, csBootVolName+"-ivs-proj-bv", csSubnetName, csSGName)
			cs = createTestCS(ctx, "test-cs-pv-proj-bv", spec)
			setCSStatus(ctx, cs, v1alpha1.ResourcePhasePending, v1alpha1.ConditionReasonSynchronized, "", "", "", "", "", nil, nil, 0, time.Now())

			result, err := m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.ShortRequeueAfter))

			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), cs)).To(Succeed())

			// ivs fires at Stage 4 (before CMP calls) → validation fails, no CMP expectations needed.
			result, err = m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(BeZero())

			updated := &v1alpha1.CloudServer{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseFailed))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonIntentionValidationFailed))
			Expect(cond.Message).To(ContainSubstring("project reference"))
		})

		It("sets Failed+ValidationFailed at Pending phase when CloudServer project reference differs from Subnet project reference (no CMP resource yet)", func() {
			m := newCSReconcilerWithMocks(GinkgoT())

			kubeSubnet := createTestSubnet(ctx, csSubnetName+"-ivs-proj-sn", v1alpha1.SubnetSpec{
				Tenant:           "test-tenant",
				Region:           "ITBG-Bergamo",
				Type:             "Advanced",
				CIDR:             "10.2.0.0/24",
				VPCReference:     v1alpha1.ResourceReference{Name: csVpcName, Namespace: "default"},
				ProjectReference: v1alpha1.ResourceReference{Name: "other-project", Namespace: "default"},
			})
			defer func() { _ = k8sClient.Delete(ctx, kubeSubnet) }()

			spec := defaultCSSpec(csIVSProjectName, csVpcName, csBootVolName, csSubnetName+"-ivs-proj-sn", csSGName)
			cs = createTestCS(ctx, "test-cs-pv-proj-sn", spec)
			setCSStatus(ctx, cs, v1alpha1.ResourcePhasePending, v1alpha1.ConditionReasonSynchronized, "", "", "", "", "", nil, nil, 0, time.Now())

			result, err := m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.ShortRequeueAfter))

			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), cs)).To(Succeed())

			// ivs fires at Stage 4 (before CMP calls) → validation fails, no CMP expectations needed.
			result, err = m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(BeZero())

			updated := &v1alpha1.CloudServer{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseFailed))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonIntentionValidationFailed))
			Expect(cond.Message).To(ContainSubstring("project reference mismatch"))
		})

		It("sets Failed+ValidationFailed at Pending phase when CloudServer tenant differs from KeyPair tenant (no CMP resource yet)", func() {
			m := newCSReconcilerWithMocks(GinkgoT())

			kubeKP := createTestKeyPair(ctx, csKPName+"-ivs-tenant-kp", v1alpha1.KeyPairSpec{
				Tenant:           "other-tenant",
				Region:           "ITBG-Bergamo",
				Tags:             []string{},
				Value:            "ssh-rsa AAAAB3NzaC1 test-key",
				ProjectReference: v1alpha1.ResourceReference{Name: csIVSProjectName, Namespace: "default"},
			})
			defer func() { _ = k8sClient.Delete(ctx, kubeKP) }()

			spec := defaultCSSpec(csIVSProjectName, csVpcName, csBootVolName, csSubnetName, csSGName)
			spec.KeyPairReference = v1alpha1.ResourceReference{Name: csKPName + "-ivs-tenant-kp", Namespace: "default"}
			cs = createTestCS(ctx, "test-cs-pv-tenant-kp", spec)
			setCSStatus(ctx, cs, v1alpha1.ResourcePhasePending, v1alpha1.ConditionReasonSynchronized, "", "", "", "", "", nil, nil, 0, time.Now())

			result, err := m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.ShortRequeueAfter))

			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), cs)).To(Succeed())

			// ivs fires at Stage 4 (before CMP calls) → validation fails, no CMP expectations needed.
			result, err = m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(BeZero())

			updated := &v1alpha1.CloudServer{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseFailed))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonIntentionValidationFailed))
			Expect(cond.Message).To(ContainSubstring("tenant"))
		})

		It("sets Failed+ValidationFailed at Pending phase when CloudServer tenant differs from ElasticIP tenant (no CMP resource yet)", func() {
			m := newCSReconcilerWithMocks(GinkgoT())

			eipName := "cs-test-eip-ivs-tenant"
			kubeEIP := createTestElasticIP(ctx, eipName, v1alpha1.ElasticIPSpec{
				Tenant:           "other-tenant",
				Region:           "ITBG-Bergamo",
				Tags:             []string{},
				BillingPeriod:    "Hour",
				ProjectReference: v1alpha1.ResourceReference{Name: csIVSProjectName, Namespace: "default"},
			})
			defer func() { _ = k8sClient.Delete(ctx, kubeEIP) }()

			spec := defaultCSSpec(csIVSProjectName, csVpcName, csBootVolName, csSubnetName, csSGName)
			spec.ElasticIPReference = &v1alpha1.ResourceReference{Name: eipName, Namespace: "default"}
			cs = createTestCS(ctx, "test-cs-pv-tenant-eip", spec)
			setCSStatus(ctx, cs, v1alpha1.ResourcePhasePending, v1alpha1.ConditionReasonSynchronized, "", "", "", "", "", nil, nil, 0, time.Now())

			result, err := m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.ShortRequeueAfter))

			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), cs)).To(Succeed())

			// ivs fires at Stage 4 (before CMP calls) → validation fails, no CMP expectations needed.
			result, err = m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(BeZero())

			updated := &v1alpha1.CloudServer{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseFailed))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonIntentionValidationFailed))
			Expect(cond.Message).To(ContainSubstring("tenant"))
		})

		It("sets Failed+ValidationFailed at Pending phase when CloudServer tenant differs from SecurityGroup tenant (no CMP resource yet)", func() {
			m := newCSReconcilerWithMocks(GinkgoT())

			kubeSG := createTestSecurityGroup(ctx, csSGName+"-ivs-tenant-sg", v1alpha1.SecurityGroupSpec{
				Tenant:           "other-tenant",
				Region:           "ITBG-Bergamo",
				VPCReference:     v1alpha1.ResourceReference{Name: csVpcName, Namespace: "default"},
				ProjectReference: v1alpha1.ResourceReference{Name: csIVSProjectName, Namespace: "default"},
			})
			defer func() { _ = k8sClient.Delete(ctx, kubeSG) }()

			spec := defaultCSSpec(csIVSProjectName, csVpcName, csBootVolName, csSubnetName, csSGName+"-ivs-tenant-sg")
			cs = createTestCS(ctx, "test-cs-pv-tenant-sg", spec)
			setCSStatus(ctx, cs, v1alpha1.ResourcePhasePending, v1alpha1.ConditionReasonSynchronized, "", "", "", "", "", nil, nil, 0, time.Now())

			result, err := m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.ShortRequeueAfter))

			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), cs)).To(Succeed())

			// ivs fires at Stage 4 (before CMP calls) → validation fails, no CMP expectations needed.
			result, err = m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(BeZero())

			updated := &v1alpha1.CloudServer{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseFailed))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonIntentionValidationFailed))
			Expect(cond.Message).To(ContainSubstring("tenant mismatch"))
		})

		It("sets Failed+ValidationFailed at Pending phase when CloudServer project reference differs from KeyPair project reference (no CMP resource yet)", func() {
			m := newCSReconcilerWithMocks(GinkgoT())

			kubeKP := createTestKeyPair(ctx, csKPName+"-ivs-proj-kp", v1alpha1.KeyPairSpec{
				Tenant:           "test-tenant",
				Region:           "ITBG-Bergamo",
				Tags:             []string{},
				Value:            "ssh-rsa AAAAB3NzaC1 test-key",
				ProjectReference: v1alpha1.ResourceReference{Name: "other-project", Namespace: "default"},
			})
			defer func() { _ = k8sClient.Delete(ctx, kubeKP) }()

			spec := defaultCSSpec(csIVSProjectName, csVpcName, csBootVolName, csSubnetName, csSGName)
			spec.KeyPairReference = v1alpha1.ResourceReference{Name: csKPName + "-ivs-proj-kp", Namespace: "default"}
			cs = createTestCS(ctx, "test-cs-pv-proj-kp", spec)
			setCSStatus(ctx, cs, v1alpha1.ResourcePhasePending, v1alpha1.ConditionReasonSynchronized, "", "", "", "", "", nil, nil, 0, time.Now())

			result, err := m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.ShortRequeueAfter))

			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), cs)).To(Succeed())

			// ivs fires at Stage 4 (before CMP calls) → validation fails, no CMP expectations needed.
			result, err = m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(BeZero())

			updated := &v1alpha1.CloudServer{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseFailed))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonIntentionValidationFailed))
			Expect(cond.Message).To(ContainSubstring("project reference"))
		})

		It("sets Failed+ValidationFailed at Pending phase when CloudServer project reference differs from ElasticIP project reference (no CMP resource yet)", func() {
			m := newCSReconcilerWithMocks(GinkgoT())

			eipName := "cs-test-eip-ivs-proj"
			kubeEIP := createTestElasticIP(ctx, eipName, v1alpha1.ElasticIPSpec{
				Tenant:           "test-tenant",
				Region:           "ITBG-Bergamo",
				Tags:             []string{},
				BillingPeriod:    "Hour",
				ProjectReference: v1alpha1.ResourceReference{Name: "other-project", Namespace: "default"},
			})
			defer func() { _ = k8sClient.Delete(ctx, kubeEIP) }()

			spec := defaultCSSpec(csIVSProjectName, csVpcName, csBootVolName, csSubnetName, csSGName)
			spec.ElasticIPReference = &v1alpha1.ResourceReference{Name: eipName, Namespace: "default"}
			cs = createTestCS(ctx, "test-cs-pv-proj-eip", spec)
			setCSStatus(ctx, cs, v1alpha1.ResourcePhasePending, v1alpha1.ConditionReasonSynchronized, "", "", "", "", "", nil, nil, 0, time.Now())

			result, err := m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.ShortRequeueAfter))

			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), cs)).To(Succeed())

			// ivs fires at Stage 4 (before CMP calls) → validation fails, no CMP expectations needed.
			result, err = m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(BeZero())

			updated := &v1alpha1.CloudServer{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseFailed))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonIntentionValidationFailed))
			Expect(cond.Message).To(ContainSubstring("project reference"))
		})

		It("sets Failed+ValidationFailed at Pending phase when CloudServer project reference differs from SecurityGroup project reference (no CMP resource yet)", func() {
			m := newCSReconcilerWithMocks(GinkgoT())

			kubeSG := createTestSecurityGroup(ctx, csSGName+"-ivs-proj-sg", v1alpha1.SecurityGroupSpec{
				Tenant:           "test-tenant",
				Region:           "ITBG-Bergamo",
				VPCReference:     v1alpha1.ResourceReference{Name: csVpcName, Namespace: "default"},
				ProjectReference: v1alpha1.ResourceReference{Name: "other-project", Namespace: "default"},
			})
			defer func() { _ = k8sClient.Delete(ctx, kubeSG) }()

			spec := defaultCSSpec(csIVSProjectName, csVpcName, csBootVolName, csSubnetName, csSGName+"-ivs-proj-sg")
			cs = createTestCS(ctx, "test-cs-pv-proj-sg", spec)
			setCSStatus(ctx, cs, v1alpha1.ResourcePhasePending, v1alpha1.ConditionReasonSynchronized, "", "", "", "", "", nil, nil, 0, time.Now())

			result, err := m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.ShortRequeueAfter))

			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), cs)).To(Succeed())

			// ivs fires at Stage 4 (before CMP calls) → validation fails, no CMP expectations needed.
			result, err = m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(BeZero())

			updated := &v1alpha1.CloudServer{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseFailed))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonIntentionValidationFailed))
			Expect(cond.Message).To(ContainSubstring("project reference mismatch"))
		})
	})

	Describe("Validation", func() {
		It("sets Failed+ValidationFailed when CloudServer tenant does not match Project tenant", func() {
			// Create Project with a different tenant than the CloudServer.
			csValidationProjectName := "cs-validation-project"
			kubeProj := createTestProject(ctx, csValidationProjectName, v1alpha1.ProjectSpec{
				Tenant:      "other-tenant",
				Description: "validation test project",
				Tags:        []string{},
			})
			setProjectStatus(ctx, kubeProj, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "cs-val-proj-id", 0, time.Now())
			defer func() {
				_ = k8sClient.Delete(ctx, kubeProj)
			}()

			m := newCSReconcilerWithMocks(GinkgoT())
			cs = createTestCS(ctx, "test-cs-validation", defaultCSSpec(csValidationProjectName, csVpcName, csBootVolName, csSubnetName, csSGName))

			// First reconcile: ensureOwnerReference patches the CS and requeues.
			result, err := m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.ShortRequeueAfter))

			// Re-fetch so the owner-ref label is visible.
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), cs)).To(Succeed())

			// ivs fires at Stage 4 (before CMP calls) → validation fails, no CMP expectations needed.
			_, err = m.r.HandleReconcile(ctx, cs)
			Expect(err).To(Succeed())

			updated := &v1alpha1.CloudServer{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseFailed))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonIntentionValidationFailed))
			Expect(cond.Message).To(ContainSubstring("tenant mismatch with Project"))
		})
	})
})
