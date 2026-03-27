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

func buildSecurityGroupResponse(id, name, state string) *arubatypes.SecurityGroupResponse {
	defaultVal := false
	return &arubatypes.SecurityGroupResponse{
		Metadata: arubatypes.ResourceMetadataResponse{
			ID:   &id,
			Name: &name,
		},
		Properties: arubatypes.SecurityGroupPropertiesResponse{
			Default: defaultVal,
		},
		Status: arubatypes.ResourceStatus{
			State: &state,
		},
	}
}

func buildSecurityGroupList(responses ...*arubatypes.SecurityGroupResponse) *arubatypes.Response[arubatypes.SecurityGroupList] {
	list := &arubatypes.SecurityGroupList{}
	for _, r := range responses {
		list.Values = append(list.Values, *r)
		list.Total++
	}
	return &arubatypes.Response[arubatypes.SecurityGroupList]{
		Data:       list,
		StatusCode: http.StatusOK,
	}
}

func buildSGCRUDResponse(statusCode int) *arubatypes.Response[arubatypes.SecurityGroupResponse] {
	return &arubatypes.Response[arubatypes.SecurityGroupResponse]{
		StatusCode: statusCode,
	}
}

func buildVpcListForSG(vpcID, vpcName string) *arubatypes.Response[arubatypes.VPCList] {
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

func buildProjectListForSG(projectID, projectName string) *arubatypes.Response[arubatypes.ProjectList] {
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

func defaultSecurityGroupSpec(projectName, vpcName string) v1alpha1.SecurityGroupSpec {
	return v1alpha1.SecurityGroupSpec{
		Tenant: "test-tenant",
		Tags:   []string{"tag1"},
		Region: "ITBG-Bergamo",
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

func createTestSecurityGroup(ctx context.Context, name string, spec v1alpha1.SecurityGroupSpec) *v1alpha1.SecurityGroup {
	sg := &v1alpha1.SecurityGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: spec,
	}
	ExpectWithOffset(1, k8sClient.Create(ctx, sg)).To(Succeed())
	return sg
}

func setSecurityGroupStatus(ctx context.Context, sg *v1alpha1.SecurityGroup, phase v1alpha1.ResourcePhase, reason string, resourceID string, projectID string, vpcID string, observedGen int64, conditionTime time.Time) {
	s := sg.DeepCopy()
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), s)).To(Succeed())
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
	ExpectWithOffset(1, k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), sg)).To(Succeed())
}

// --- Mock setup ---

type sgMocks struct {
	r              *SecurityGroupReconciler
	mockAruba      *arubamocks.MockClient
	mockProject    *arubamocks.MockProjectClient
	mockNetwork    *arubamocks.MockNetworkClient
	mockVPCsClient *arubamocks.MockVPCsClient
	mockSecGroups  *arubamocks.MockSecurityGroupsClient
}

func newSGReconcilerWithMocks(t GinkgoTInterface) *sgMocks {
	mockAruba := arubamocks.NewMockClient(t)
	mockProject := arubamocks.NewMockProjectClient(t)
	mockNetwork := arubamocks.NewMockNetworkClient(t)
	mockVPCsClient := arubamocks.NewMockVPCsClient(t)
	mockSecGroups := arubamocks.NewMockSecurityGroupsClient(t)

	r := NewSecurityGroupReconciler(newTestReconciler(t, mockAruba))

	return &sgMocks{
		r:              r,
		mockAruba:      mockAruba,
		mockProject:    mockProject,
		mockNetwork:    mockNetwork,
		mockVPCsClient: mockVPCsClient,
		mockSecGroups:  mockSecGroups,
	}
}

func (m *sgMocks) expectProjectList(projectID, projectName string) {
	m.mockAruba.EXPECT().FromProject().Return(m.mockProject)
	m.mockProject.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectListForSG(projectID, projectName), nil)
}

func (m *sgMocks) expectVpcList(projectID, vpcID, vpcName string) {
	m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
	m.mockNetwork.EXPECT().VPCs().Return(m.mockVPCsClient)
	m.mockVPCsClient.EXPECT().List(mock.Anything, projectID, mock.Anything).Return(buildVpcListForSG(vpcID, vpcName), nil)
}

func (m *sgMocks) expectSGList(projectID, vpcID string, responses ...*arubatypes.SecurityGroupResponse) {
	m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
	m.mockNetwork.EXPECT().SecurityGroups().Return(m.mockSecGroups)
	m.mockSecGroups.EXPECT().List(mock.Anything, projectID, vpcID, mock.Anything).Return(buildSecurityGroupList(responses...), nil)
}

// --- Tests ---

var _ = Describe("SecurityGroupReconciler", func() {
	const (
		sgProjectName = "test-sg-project-ref"
		sgProjectID   = "sg-proj-id-1"
		sgVpcName     = "test-sg-vpc-ref"
		sgVpcID       = "sg-vpc-id-1"
	)

	var (
		ctx context.Context
		sg  *v1alpha1.SecurityGroup
	)

	BeforeEach(func() {
		ctx = context.Background()
	})

	AfterEach(func() {
		if sg != nil {
			s := &v1alpha1.SecurityGroup{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), s); err == nil {
				s.Finalizers = nil
				_ = k8sClient.Update(ctx, s)
				_ = k8sClient.Delete(ctx, s)
			}
			sg = nil
		}
	})

	Describe("First reconciliation", func() {
		It("transitions to Creating+ShallSynchronize when CMP has no SecurityGroup", func() {
			m := newSGReconcilerWithMocks(GinkgoT())
			sg = createTestSecurityGroup(ctx, "test-sg-first", defaultSecurityGroupSpec(sgProjectName, sgVpcName))

			m.expectProjectList(sgProjectID, sgProjectName)
			m.expectVpcList(sgProjectID, sgVpcID, sgVpcName)
			m.expectSGList(sgProjectID, sgVpcID)

			_, err := m.r.HandleReconcile(ctx, sg)
			Expect(err).To(Succeed())

			updated := &v1alpha1.SecurityGroup{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
		})
	})

	Describe("Create on CMP", func() {
		It("transitions to Creating+Synchronizing after successful CMP create", func() {
			m := newSGReconcilerWithMocks(GinkgoT())
			sg = createTestSecurityGroup(ctx, "test-sg-create-cmp", defaultSecurityGroupSpec(sgProjectName, sgVpcName))
			setSecurityGroupStatus(ctx, sg, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, "", "", "", 0, time.Now())

			m.expectProjectList(sgProjectID, sgProjectName)
			m.expectVpcList(sgProjectID, sgVpcID, sgVpcName)
			m.expectSGList(sgProjectID, sgVpcID)
			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
			m.mockNetwork.EXPECT().SecurityGroups().Return(m.mockSecGroups)
			m.mockSecGroups.EXPECT().Create(mock.Anything, sgProjectID, sgVpcID, mock.Anything, mock.Anything).Return(buildSGCRUDResponse(http.StatusCreated), nil)

			_, err := m.r.HandleReconcile(ctx, sg)
			Expect(err).To(Succeed())

			updated := &v1alpha1.SecurityGroup{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
		})
	})

	Describe("Waiting creation (SecurityGroup not yet in CMP)", func() {
		It("returns LongRequeue", func() {
			m := newSGReconcilerWithMocks(GinkgoT())
			sg = createTestSecurityGroup(ctx, "test-sg-wait-create", defaultSecurityGroupSpec(sgProjectName, sgVpcName))
			setSecurityGroupStatus(ctx, sg, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "", "", "", 0, time.Now())

			m.expectProjectList(sgProjectID, sgProjectName)
			m.expectVpcList(sgProjectID, sgVpcID, sgVpcName)
			m.expectSGList(sgProjectID, sgVpcID)

			result, err := m.r.HandleReconcile(ctx, sg)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("Waiting creation (SecurityGroup in transitory CMP state)", func() {
		It("returns LongRequeue when CMP state is Creating", func() {
			m := newSGReconcilerWithMocks(GinkgoT())
			sg = createTestSecurityGroup(ctx, "test-sg-wait-create-transitory", defaultSecurityGroupSpec(sgProjectName, sgVpcName))
			setSecurityGroupStatus(ctx, sg, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "", "", "", 0, time.Now())

			cmpSG := buildSecurityGroupResponse("sg-id-1", "test-sg-wait-create-transitory", CSPResourceStateCreating)
			m.expectProjectList(sgProjectID, sgProjectName)
			m.expectVpcList(sgProjectID, sgVpcID, sgVpcName)
			m.expectSGList(sgProjectID, sgVpcID, cmpSG)

			result, err := m.r.HandleReconcile(ctx, sg)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("Creation confirmed on CMP", func() {
		It("transitions to Creating+Synchronized when CMP SecurityGroup is active", func() {
			m := newSGReconcilerWithMocks(GinkgoT())
			sg = createTestSecurityGroup(ctx, "test-sg-creation-confirmed", defaultSecurityGroupSpec(sgProjectName, sgVpcName))
			setSecurityGroupStatus(ctx, sg, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "", "", "", 0, time.Now())

			cmpSG := buildSecurityGroupResponse("sg-id-1", "test-sg-creation-confirmed", CSPResourceStateActive)
			m.expectProjectList(sgProjectID, sgProjectName)
			m.expectVpcList(sgProjectID, sgVpcID, sgVpcName)
			m.expectSGList(sgProjectID, sgVpcID, cmpSG)

			_, err := m.r.HandleReconcile(ctx, sg)
			Expect(err).To(Succeed())

			updated := &v1alpha1.SecurityGroup{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronized))
		})
	})

	Describe("Creation accomplished", func() {
		It("transitions to Active+Synchronized and sets ResourceID", func() {
			m := newSGReconcilerWithMocks(GinkgoT())
			sg = createTestSecurityGroup(ctx, "test-sg-creation-accomplished", defaultSecurityGroupSpec(sgProjectName, sgVpcName))
			setSecurityGroupStatus(ctx, sg, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized, "", "", "", 0, time.Now())

			cmpSG := buildSecurityGroupResponse("sg-id-1", "test-sg-creation-accomplished", CSPResourceStateActive)
			m.expectProjectList(sgProjectID, sgProjectName)
			m.expectVpcList(sgProjectID, sgVpcID, sgVpcName)
			m.expectSGList(sgProjectID, sgVpcID, cmpSG)

			_, err := m.r.HandleReconcile(ctx, sg)
			Expect(err).To(Succeed())

			updated := &v1alpha1.SecurityGroup{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseActive))
			Expect(updated.Status.ResourceID).To(Equal("sg-id-1"))
		})
	})

	Describe("HasDeniedChanges", func() {
		It("returns LongRequeue when immutable field (location) is changed", func() {
			m := newSGReconcilerWithMocks(GinkgoT())
			sg = createTestSecurityGroup(ctx, "test-sg-denied-changes", defaultSecurityGroupSpec(sgProjectName, sgVpcName))
			setSecurityGroupStatus(ctx, sg, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "sg-id-1", sgProjectID, sgVpcID, 1, time.Now())

			// Force generation change with different location
			sgFetch := &v1alpha1.SecurityGroup{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), sgFetch)).To(Succeed())
			sgFetch.Spec.Region = "IT-MILAN"
			Expect(k8sClient.Update(ctx, sgFetch)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), sg)).To(Succeed())

			// CMP still has original location
			cmpSG := buildSecurityGroupResponse("sg-id-1", "test-sg-denied-changes", CSPResourceStateActive)
			cmpSG.Metadata.LocationResponse = &arubatypes.LocationResponse{Value: "ITBG-Bergamo"}
			m.expectProjectList(sgProjectID, sgProjectName)
			m.expectVpcList(sgProjectID, sgVpcID, sgVpcName)
			m.expectSGList(sgProjectID, sgVpcID, cmpSG)

			result, err := m.r.HandleReconcile(ctx, sg)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("SpecAlreadyInSyncWithCMP", func() {
		It("re-stamps ObservedGeneration when spec hasn't actually changed", func() {
			m := newSGReconcilerWithMocks(GinkgoT())
			sg = createTestSecurityGroup(ctx, "test-sg-spec-in-sync", defaultSecurityGroupSpec(sgProjectName, sgVpcName))
			setSecurityGroupStatus(ctx, sg, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "sg-id-1", sgProjectID, sgVpcID, 1, time.Now())

			// Trigger generation bump with same tags
			sgFetch := &v1alpha1.SecurityGroup{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), sgFetch)).To(Succeed())
			sgFetch.Spec.Tags = []string{"tag1"}
			Expect(k8sClient.Update(ctx, sgFetch)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), sg)).To(Succeed())

			// CMP matches: same tags, same Default
			cmpSG := buildSecurityGroupResponse("sg-id-1", "test-sg-spec-in-sync", CSPResourceStateActive)
			cmpSG.Metadata.Tags = []string{"tag1"}
			m.expectProjectList(sgProjectID, sgProjectName)
			m.expectVpcList(sgProjectID, sgVpcID, sgVpcName)
			m.expectSGList(sgProjectID, sgVpcID, cmpSG)

			_, err := m.r.HandleReconcile(ctx, sg)
			Expect(err).To(Succeed())

			updated := &v1alpha1.SecurityGroup{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseActive))
			Expect(updated.Status.ObservedGeneration).To(Equal(sg.Generation))
		})
	})

	Describe("ShouldBeUpdated", func() {
		It("transitions to Updating+ShallSynchronize when tags differ", func() {
			m := newSGReconcilerWithMocks(GinkgoT())
			sg = createTestSecurityGroup(ctx, "test-sg-should-update", defaultSecurityGroupSpec(sgProjectName, sgVpcName))
			setSecurityGroupStatus(ctx, sg, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "sg-id-1", sgProjectID, sgVpcID, 1, time.Now())

			// Change tags to trigger update
			sgFetch := &v1alpha1.SecurityGroup{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), sgFetch)).To(Succeed())
			sgFetch.Spec.Tags = []string{"tag1", "tag2"}
			Expect(k8sClient.Update(ctx, sgFetch)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), sg)).To(Succeed())

			// CMP has old tags
			cmpSG := buildSecurityGroupResponse("sg-id-1", "test-sg-should-update", CSPResourceStateActive)
			cmpSG.Metadata.Tags = []string{"tag1"}
			m.expectProjectList(sgProjectID, sgProjectName)
			m.expectVpcList(sgProjectID, sgVpcID, sgVpcName)
			m.expectSGList(sgProjectID, sgVpcID, cmpSG)

			_, err := m.r.HandleReconcile(ctx, sg)
			Expect(err).To(Succeed())

			updated := &v1alpha1.SecurityGroup{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseUpdating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseUpdating))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
		})
	})

	Describe("Update on CMP", func() {
		It("transitions to Updating+Synchronizing after successful CMP update", func() {
			m := newSGReconcilerWithMocks(GinkgoT())
			sg = createTestSecurityGroup(ctx, "test-sg-update-cmp", defaultSecurityGroupSpec(sgProjectName, sgVpcName))
			setSecurityGroupStatus(ctx, sg, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize, "sg-id-1", sgProjectID, sgVpcID, 1, time.Now())

			cmpSG := buildSecurityGroupResponse("sg-id-1", "test-sg-update-cmp", CSPResourceStateActive)
			m.mockAruba.EXPECT().FromProject().Return(m.mockProject)
			m.mockProject.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectListForSG(sgProjectID, sgProjectName), nil)
			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
			m.mockNetwork.EXPECT().VPCs().Return(m.mockVPCsClient)
			m.mockVPCsClient.EXPECT().List(mock.Anything, sgProjectID, mock.Anything).Return(buildVpcListForSG(sgVpcID, sgVpcName), nil)
			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
			m.mockNetwork.EXPECT().SecurityGroups().Return(m.mockSecGroups)
			m.mockSecGroups.EXPECT().List(mock.Anything, sgProjectID, sgVpcID, mock.Anything).Return(buildSecurityGroupList(cmpSG), nil)
			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
			m.mockNetwork.EXPECT().SecurityGroups().Return(m.mockSecGroups)
			m.mockSecGroups.EXPECT().Update(mock.Anything, sgProjectID, sgVpcID, "sg-id-1", mock.Anything, mock.Anything).Return(buildSGCRUDResponse(http.StatusOK), nil)

			_, err := m.r.HandleReconcile(ctx, sg)
			Expect(err).To(Succeed())

			updated := &v1alpha1.SecurityGroup{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseUpdating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseUpdating))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
		})
	})

	Describe("Should delete", func() {
		It("transitions to Deleting+ShallSynchronize when deletion is requested on Active SecurityGroup", func() {
			m := newSGReconcilerWithMocks(GinkgoT())
			sg = createTestSecurityGroup(ctx, "test-sg-should-delete", defaultSecurityGroupSpec(sgProjectName, sgVpcName))
			sgFetch := &v1alpha1.SecurityGroup{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), sgFetch)).To(Succeed())
			sgFetch.Finalizers = []string{securityGroupFinalizerName}
			Expect(k8sClient.Update(ctx, sgFetch)).To(Succeed())
			setSecurityGroupStatus(ctx, sg, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "sg-id-1", sgProjectID, sgVpcID, 1, time.Now())
			Expect(k8sClient.Delete(ctx, sg)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), sg)).To(Succeed())

			cmpSG := buildSecurityGroupResponse("sg-id-1", "test-sg-should-delete", CSPResourceStateActive)
			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
			m.mockNetwork.EXPECT().SecurityGroups().Return(m.mockSecGroups)
			m.mockSecGroups.EXPECT().List(mock.Anything, sgProjectID, sgVpcID, mock.Anything).Return(buildSecurityGroupList(cmpSG), nil)

			_, err := m.r.HandleReconcile(ctx, sg)
			Expect(err).To(Succeed())

			updated := &v1alpha1.SecurityGroup{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
		})
	})

	Describe("Delete on CMP", func() {
		It("transitions to Deleting+Synchronizing after successful CMP delete", func() {
			m := newSGReconcilerWithMocks(GinkgoT())
			sg = createTestSecurityGroup(ctx, "test-sg-delete-cmp", defaultSecurityGroupSpec(sgProjectName, sgVpcName))
			sgFetch := &v1alpha1.SecurityGroup{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), sgFetch)).To(Succeed())
			sgFetch.Finalizers = []string{securityGroupFinalizerName}
			Expect(k8sClient.Update(ctx, sgFetch)).To(Succeed())
			setSecurityGroupStatus(ctx, sg, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, "sg-id-1", sgProjectID, sgVpcID, 1, time.Now())
			Expect(k8sClient.Delete(ctx, sg)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), sg)).To(Succeed())

			cmpSG := buildSecurityGroupResponse("sg-id-1", "test-sg-delete-cmp", CSPResourceStateActive)
			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
			m.mockNetwork.EXPECT().SecurityGroups().Return(m.mockSecGroups)
			m.mockSecGroups.EXPECT().List(mock.Anything, sgProjectID, sgVpcID, mock.Anything).Return(buildSecurityGroupList(cmpSG), nil)
			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
			m.mockNetwork.EXPECT().SecurityGroups().Return(m.mockSecGroups)
			m.mockSecGroups.EXPECT().Delete(mock.Anything, sgProjectID, sgVpcID, "sg-id-1", mock.Anything).Return(buildDeleteResponse(http.StatusOK), nil)

			_, err := m.r.HandleReconcile(ctx, sg)
			Expect(err).To(Succeed())

			updated := &v1alpha1.SecurityGroup{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
		})
	})

	Describe("DeletionOnCMPNotNeeded", func() {
		It("advances to Deleting+Synchronized when CMP SecurityGroup is already gone", func() {
			m := newSGReconcilerWithMocks(GinkgoT())
			sg = createTestSecurityGroup(ctx, "test-sg-delete-not-needed", defaultSecurityGroupSpec(sgProjectName, sgVpcName))
			sgFetch := &v1alpha1.SecurityGroup{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), sgFetch)).To(Succeed())
			sgFetch.Finalizers = []string{securityGroupFinalizerName}
			Expect(k8sClient.Update(ctx, sgFetch)).To(Succeed())
			setSecurityGroupStatus(ctx, sg, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, "sg-id-1", sgProjectID, sgVpcID, 1, time.Now())
			Expect(k8sClient.Delete(ctx, sg)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), sg)).To(Succeed())

			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
			m.mockNetwork.EXPECT().SecurityGroups().Return(m.mockSecGroups)
			m.mockSecGroups.EXPECT().List(mock.Anything, sgProjectID, sgVpcID, mock.Anything).Return(buildSecurityGroupList(), nil)

			_, err := m.r.HandleReconcile(ctx, sg)
			Expect(err).To(Succeed())

			updated := &v1alpha1.SecurityGroup{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronized))
		})
	})

	Describe("CMP transitory during deletion", func() {
		It("returns LongRequeue when CMP state is Deleting", func() {
			m := newSGReconcilerWithMocks(GinkgoT())
			sg = createTestSecurityGroup(ctx, "test-sg-deleting-transitory", defaultSecurityGroupSpec(sgProjectName, sgVpcName))
			sgFetch := &v1alpha1.SecurityGroup{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), sgFetch)).To(Succeed())
			sgFetch.Finalizers = []string{securityGroupFinalizerName}
			Expect(k8sClient.Update(ctx, sgFetch)).To(Succeed())
			setSecurityGroupStatus(ctx, sg, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronizing, "sg-id-1", sgProjectID, sgVpcID, 1, time.Now())
			Expect(k8sClient.Delete(ctx, sg)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), sg)).To(Succeed())

			cmpSG := buildSecurityGroupResponse("sg-id-1", "test-sg-deleting-transitory", CSPResourceStateDeleting)
			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
			m.mockNetwork.EXPECT().SecurityGroups().Return(m.mockSecGroups)
			m.mockSecGroups.EXPECT().List(mock.Anything, sgProjectID, sgVpcID, mock.Anything).Return(buildSecurityGroupList(cmpSG), nil)

			result, err := m.r.HandleReconcile(ctx, sg)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("Deletion accomplished", func() {
		It("transitions to Deleted phase when CMP SecurityGroup is gone", func() {
			m := newSGReconcilerWithMocks(GinkgoT())
			sg = createTestSecurityGroup(ctx, "test-sg-deletion-accomplished", defaultSecurityGroupSpec(sgProjectName, sgVpcName))
			sgFetch := &v1alpha1.SecurityGroup{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), sgFetch)).To(Succeed())
			sgFetch.Finalizers = []string{securityGroupFinalizerName}
			Expect(k8sClient.Update(ctx, sgFetch)).To(Succeed())
			setSecurityGroupStatus(ctx, sg, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronized, "sg-id-1", sgProjectID, sgVpcID, 1, time.Now())
			Expect(k8sClient.Delete(ctx, sg)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), sg)).To(Succeed())

			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
			m.mockNetwork.EXPECT().SecurityGroups().Return(m.mockSecGroups)
			m.mockSecGroups.EXPECT().List(mock.Anything, sgProjectID, sgVpcID, mock.Anything).Return(buildSecurityGroupList(), nil)

			_, err := m.r.HandleReconcile(ctx, sg)
			Expect(err).To(Succeed())

			updated := &v1alpha1.SecurityGroup{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleted))
		})
	})

	Describe("IsInError", func() {
		It("transitions to Failed+Synchronized when CMP state is Failed", func() {
			m := newSGReconcilerWithMocks(GinkgoT())
			sg = createTestSecurityGroup(ctx, "test-sg-in-error", defaultSecurityGroupSpec(sgProjectName, sgVpcName))
			setSecurityGroupStatus(ctx, sg, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "sg-id-1", sgProjectID, sgVpcID, 1, time.Now())

			cmpSG := buildSecurityGroupResponse("sg-id-1", "test-sg-in-error", CSPResourceStateFailed)
			m.expectProjectList(sgProjectID, sgProjectName)
			m.expectVpcList(sgProjectID, sgVpcID, sgVpcName)
			m.expectSGList(sgProjectID, sgVpcID, cmpSG)

			_, err := m.r.HandleReconcile(ctx, sg)
			Expect(err).To(Succeed())

			updated := &v1alpha1.SecurityGroup{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseFailed))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronized))
		})
	})

	Describe("Phase timeout", func() {
		It("transitions to Failed when stuck in transitory phase too long", func() {
			m := newSGReconcilerWithMocks(GinkgoT())
			sg = createTestSecurityGroup(ctx, "test-sg-timeout", defaultSecurityGroupSpec(sgProjectName, sgVpcName))
			setSecurityGroupStatus(ctx, sg, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, "", sgProjectID, sgVpcID,
				0, time.Now().Add(-(reconciler.MaxPhaseTimeout + time.Minute)))

			cmpSG := buildSecurityGroupResponse("sg-id-1", "test-sg-timeout", CSPResourceStateActive)
			m.expectProjectList(sgProjectID, sgProjectName)
			m.expectVpcList(sgProjectID, sgVpcID, sgVpcName)
			m.expectSGList(sgProjectID, sgVpcID, cmpSG)

			_, err := m.r.HandleReconcile(ctx, sg)
			Expect(err).To(Succeed())

			updated := &v1alpha1.SecurityGroup{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
		})
	})

	Describe("Project not found yet", func() {
		It("returns LongRequeue when project doesn't exist in CMP yet", func() {
			m := newSGReconcilerWithMocks(GinkgoT())
			sg = createTestSecurityGroup(ctx, "test-sg-no-project", defaultSecurityGroupSpec(sgProjectName, sgVpcName))

			m.mockAruba.EXPECT().FromProject().Return(m.mockProject)
			m.mockProject.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectList(), nil)

			result, err := m.r.HandleReconcile(ctx, sg)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("VPC not found yet", func() {
		It("returns LongRequeue when VPC doesn't exist in CMP yet", func() {
			m := newSGReconcilerWithMocks(GinkgoT())
			sg = createTestSecurityGroup(ctx, "test-sg-no-vpc", defaultSecurityGroupSpec(sgProjectName, sgVpcName))

			m.expectProjectList(sgProjectID, sgProjectName)

			emptyVpcList := &arubatypes.Response[arubatypes.VPCList]{
				Data:       &arubatypes.VPCList{},
				StatusCode: http.StatusOK,
			}
			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
			m.mockNetwork.EXPECT().VPCs().Return(m.mockVPCsClient)
			m.mockVPCsClient.EXPECT().List(mock.Anything, sgProjectID, mock.Anything).Return(emptyVpcList, nil)

			result, err := m.r.HandleReconcile(ctx, sg)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("ProjectID and VpcID stamped in status", func() {
		It("stamps ProjectID and VpcID on status when first transitioning", func() {
			m := newSGReconcilerWithMocks(GinkgoT())
			sg = createTestSecurityGroup(ctx, "test-sg-ids-stamped", defaultSecurityGroupSpec(sgProjectName, sgVpcName))

			m.expectProjectList(sgProjectID, sgProjectName)
			m.expectVpcList(sgProjectID, sgVpcID, sgVpcName)
			m.expectSGList(sgProjectID, sgVpcID)

			_, err := m.r.HandleReconcile(ctx, sg)
			Expect(err).To(Succeed())

			updated := &v1alpha1.SecurityGroup{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), updated)).To(Succeed())
			Expect(updated.Status.ProjectID).To(Equal(sgProjectID))
			Expect(updated.Status.VPCID).To(Equal(sgVpcID))
		})
	})

	Describe("CMP error handling", func() {
		DescribeTable("CMP create fails — preserves Creating+ShallSynchronize, surfaces error in condition",
			func(name string, statusCode int, expectedRequeue time.Duration) {
				m := newSGReconcilerWithMocks(GinkgoT())
				sg = createTestSecurityGroup(ctx, name, defaultSecurityGroupSpec(sgProjectName, sgVpcName))
				setSecurityGroupStatus(ctx, sg, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, "", "", "", 0, time.Now())

				m.expectProjectList(sgProjectID, sgProjectName)
				m.expectVpcList(sgProjectID, sgVpcID, sgVpcName)
				m.expectSGList(sgProjectID, sgVpcID)
				m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
				m.mockNetwork.EXPECT().SecurityGroups().Return(m.mockSecGroups)
				m.mockSecGroups.EXPECT().Create(mock.Anything, sgProjectID, sgVpcID, mock.Anything, mock.Anything).Return(buildSGCRUDResponse(statusCode), nil)

				result, err := m.r.HandleReconcile(ctx, sg)
				Expect(err).To(Succeed())
				Expect(result.RequeueAfter).To(Equal(expectedRequeue))

				updated := &v1alpha1.SecurityGroup{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), updated)).To(Succeed())
				Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
				cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
				Expect(cond).NotTo(BeNil())
				Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
				Expect(cond.Message).To(ContainSubstring("ERROR"))
			},
			Entry("4xx → LongRequeueAfter, no phase change", "sg-cmp-err-create-400", http.StatusBadRequest, reconciler.LongRequeueAfter),
			Entry("5xx → ShortRequeueAfter, no phase change", "sg-cmp-err-create-500", http.StatusInternalServerError, reconciler.ShortRequeueAfter),
		)
	})
})
