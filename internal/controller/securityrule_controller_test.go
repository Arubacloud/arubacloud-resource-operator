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

func buildSecurityRuleResponse(id, name, state string) *arubatypes.SecurityRuleResponse {
	return &arubatypes.SecurityRuleResponse{
		Metadata: arubatypes.ResourceMetadataResponse{
			ID:   &id,
			Name: &name,
		},
		Properties: arubatypes.SecurityRulePropertiesResponse{
			Protocol:  "TCP",
			Port:      "80",
			Direction: arubatypes.RuleDirection("Ingress"),
			Target: &arubatypes.RuleTarget{
				Kind:  arubatypes.EndpointTypeDto("Ip"),
				Value: "0.0.0.0/0",
			},
		},
		Status: arubatypes.ResourceStatus{
			State: &state,
		},
	}
}

func buildSecurityRuleList(responses ...*arubatypes.SecurityRuleResponse) *arubatypes.Response[arubatypes.SecurityRuleList] {
	list := &arubatypes.SecurityRuleList{}
	for _, r := range responses {
		list.Values = append(list.Values, *r)
		list.Total++
	}
	return &arubatypes.Response[arubatypes.SecurityRuleList]{
		Data:       list,
		StatusCode: http.StatusOK,
	}
}

func buildSRCRUDResponse(statusCode int) *arubatypes.Response[arubatypes.SecurityRuleResponse] {
	return &arubatypes.Response[arubatypes.SecurityRuleResponse]{
		StatusCode: statusCode,
	}
}

func buildSGListForSR(sgID, sgName string) *arubatypes.Response[arubatypes.SecurityGroupList] {
	defaultVal := false
	state := CSPResourceStateActive
	sg := arubatypes.SecurityGroupResponse{
		Metadata: arubatypes.ResourceMetadataResponse{
			ID:   &sgID,
			Name: &sgName,
		},
		Properties: arubatypes.SecurityGroupPropertiesResponse{
			Default: defaultVal,
		},
		Status: arubatypes.ResourceStatus{
			State: &state,
		},
	}
	list := &arubatypes.SecurityGroupList{}
	list.Values = append(list.Values, sg)
	list.Total = 1
	return &arubatypes.Response[arubatypes.SecurityGroupList]{
		Data:       list,
		StatusCode: http.StatusOK,
	}
}

// --- Test fixture helpers ---

func defaultSecurityRuleSpec(projectName, vpcName, sgName string) v1alpha1.SecurityRuleSpec {
	return v1alpha1.SecurityRuleSpec{
		Tenant:    "test-tenant",
		Tags:      []string{"tag1"},
		Region:    "ITBG-Bergamo",
		Protocol:  "TCP",
		Port:      "80",
		Direction: "Ingress",
		Target: v1alpha1.SecurityRuleTarget{
			Type:  "Ip",
			Value: "0.0.0.0/0",
		},
		ProjectReference: v1alpha1.ResourceReference{
			Name:      projectName,
			Namespace: "default",
		},
		VPCReference: v1alpha1.ResourceReference{
			Name:      vpcName,
			Namespace: "default",
		},
		SecurityGroupReference: v1alpha1.ResourceReference{
			Name:      sgName,
			Namespace: "default",
		},
	}
}

func createTestSecurityRule(ctx context.Context, name string, spec v1alpha1.SecurityRuleSpec) *v1alpha1.SecurityRule {
	sr := &v1alpha1.SecurityRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: spec,
	}
	ExpectWithOffset(1, k8sClient.Create(ctx, sr)).To(Succeed())
	return sr
}

func setSecurityRuleStatus(ctx context.Context, sr *v1alpha1.SecurityRule, phase v1alpha1.ResourcePhase, reason string, resourceID string, projectID string, vpcID string, sgID string, observedGen int64, conditionTime time.Time) {
	s := sr.DeepCopy()
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), s)).To(Succeed())
	s.Status.Phase = phase
	s.Status.ResourceID = resourceID
	s.Status.ProjectID = projectID
	s.Status.VPCID = vpcID
	s.Status.SecurityGroupID = sgID
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
	ExpectWithOffset(1, k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), sr)).To(Succeed())
}

// --- Mock setup ---

type srMocks struct {
	r           *SecurityRuleReconciler
	mockAruba   *arubamocks.MockClient
	mockProject *arubamocks.MockProjectClient
	mockNetwork *arubamocks.MockNetworkClient
	mockVPCs    *arubamocks.MockVPCsClient
	mockSecGrps *arubamocks.MockSecurityGroupsClient
	mockSRules  *arubamocks.MockSecurityGroupRulesClient
}

func newSRReconcilerWithMocks(t GinkgoTInterface) *srMocks {
	mockAruba := arubamocks.NewMockClient(t)
	mockProject := arubamocks.NewMockProjectClient(t)
	mockNetwork := arubamocks.NewMockNetworkClient(t)
	mockVPCs := arubamocks.NewMockVPCsClient(t)
	mockSecGrps := arubamocks.NewMockSecurityGroupsClient(t)
	mockSRules := arubamocks.NewMockSecurityGroupRulesClient(t)

	r := NewSecurityRuleReconciler(newTestReconciler(t, mockAruba))

	return &srMocks{
		r:           r,
		mockAruba:   mockAruba,
		mockProject: mockProject,
		mockNetwork: mockNetwork,
		mockVPCs:    mockVPCs,
		mockSecGrps: mockSecGrps,
		mockSRules:  mockSRules,
	}
}

func (m *srMocks) expectProjectList(projectID, projectName string) {
	m.mockAruba.EXPECT().FromProject().Return(m.mockProject)
	m.mockProject.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectListForSG(projectID, projectName), nil)
}

func (m *srMocks) expectVpcList(projectID, vpcID, vpcName string) {
	m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
	m.mockNetwork.EXPECT().VPCs().Return(m.mockVPCs)
	m.mockVPCs.EXPECT().List(mock.Anything, projectID, mock.Anything).Return(buildVpcListForSG(vpcID, vpcName), nil)
}

func (m *srMocks) expectSGList(projectID, vpcID, sgID, sgName string) {
	m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
	m.mockNetwork.EXPECT().SecurityGroups().Return(m.mockSecGrps)
	m.mockSecGrps.EXPECT().List(mock.Anything, projectID, vpcID, mock.Anything).Return(buildSGListForSR(sgID, sgName), nil)
}

func (m *srMocks) expectSRList(projectID, vpcID, sgID string, responses ...*arubatypes.SecurityRuleResponse) {
	m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
	m.mockNetwork.EXPECT().SecurityGroupRules().Return(m.mockSRules)
	m.mockSRules.EXPECT().List(mock.Anything, projectID, vpcID, sgID, mock.Anything).Return(buildSecurityRuleList(responses...), nil)
}

// --- Tests ---

var _ = Describe("SecurityRuleReconciler", func() {
	const (
		srProjectName = "test-sr-project-ref"
		srProjectID   = "sr-proj-id-1"
		srVpcName     = "test-sr-vpc-ref"
		srVpcID       = "sr-vpc-id-1"
		srSGName      = "test-sr-sg-ref"
		srSGID        = "sr-sg-id-1"
	)

	var (
		ctx context.Context
		sr  *v1alpha1.SecurityRule
	)

	BeforeEach(func() {
		ctx = context.Background()
	})

	AfterEach(func() {
		if sr != nil {
			s := &v1alpha1.SecurityRule{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), s); err == nil {
				s.Finalizers = nil
				_ = k8sClient.Update(ctx, s)
				_ = k8sClient.Delete(ctx, s)
			}
			sr = nil
		}
	})

	Describe("First reconciliation", func() {
		It("transitions to Creating+ShallSynchronize when CMP has no SecurityRule", func() {
			m := newSRReconcilerWithMocks(GinkgoT())
			sr = createTestSecurityRule(ctx, "test-sr-first", defaultSecurityRuleSpec(srProjectName, srVpcName, srSGName))

			m.expectProjectList(srProjectID, srProjectName)
			m.expectVpcList(srProjectID, srVpcID, srVpcName)
			m.expectSGList(srProjectID, srVpcID, srSGID, srSGName)
			m.expectSRList(srProjectID, srVpcID, srSGID)

			_, err := m.r.HandleReconcile(ctx, sr)
			Expect(err).To(Succeed())

			updated := &v1alpha1.SecurityRule{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
		})
	})

	Describe("Create on CMP", func() {
		It("transitions to Creating+Synchronizing after successful CMP create", func() {
			m := newSRReconcilerWithMocks(GinkgoT())
			sr = createTestSecurityRule(ctx, "test-sr-create-cmp", defaultSecurityRuleSpec(srProjectName, srVpcName, srSGName))
			setSecurityRuleStatus(ctx, sr, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, "", "", "", "", 0, time.Now())

			m.expectProjectList(srProjectID, srProjectName)
			m.expectVpcList(srProjectID, srVpcID, srVpcName)
			m.expectSGList(srProjectID, srVpcID, srSGID, srSGName)
			m.expectSRList(srProjectID, srVpcID, srSGID)
			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
			m.mockNetwork.EXPECT().SecurityGroupRules().Return(m.mockSRules)
			m.mockSRules.EXPECT().Create(mock.Anything, srProjectID, srVpcID, srSGID, mock.Anything, mock.Anything).Return(buildSRCRUDResponse(http.StatusCreated), nil)

			_, err := m.r.HandleReconcile(ctx, sr)
			Expect(err).To(Succeed())

			updated := &v1alpha1.SecurityRule{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
		})
	})

	Describe("Waiting creation (SecurityRule not yet in CMP)", func() {
		It("returns LongRequeue when CMP has no SecurityRule", func() {
			m := newSRReconcilerWithMocks(GinkgoT())
			sr = createTestSecurityRule(ctx, "test-sr-wait-create", defaultSecurityRuleSpec(srProjectName, srVpcName, srSGName))
			setSecurityRuleStatus(ctx, sr, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "", "", "", "", 0, time.Now())

			m.expectProjectList(srProjectID, srProjectName)
			m.expectVpcList(srProjectID, srVpcID, srVpcName)
			m.expectSGList(srProjectID, srVpcID, srSGID, srSGName)
			m.expectSRList(srProjectID, srVpcID, srSGID)

			result, err := m.r.HandleReconcile(ctx, sr)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("Waiting creation (SecurityRule in transitory CMP state)", func() {
		It("returns LongRequeue when CMP state is Creating", func() {
			m := newSRReconcilerWithMocks(GinkgoT())
			sr = createTestSecurityRule(ctx, "test-sr-wait-create-transitory", defaultSecurityRuleSpec(srProjectName, srVpcName, srSGName))
			setSecurityRuleStatus(ctx, sr, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "", "", "", "", 0, time.Now())

			cmpSR := buildSecurityRuleResponse("sr-id-1", "test-sr-wait-create-transitory", CSPResourceStateCreating)
			m.expectProjectList(srProjectID, srProjectName)
			m.expectVpcList(srProjectID, srVpcID, srVpcName)
			m.expectSGList(srProjectID, srVpcID, srSGID, srSGName)
			m.expectSRList(srProjectID, srVpcID, srSGID, cmpSR)

			result, err := m.r.HandleReconcile(ctx, sr)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("Creation confirmed on CMP", func() {
		It("transitions to Creating+Synchronized when CMP SecurityRule is active", func() {
			m := newSRReconcilerWithMocks(GinkgoT())
			sr = createTestSecurityRule(ctx, "test-sr-creation-confirmed", defaultSecurityRuleSpec(srProjectName, srVpcName, srSGName))
			setSecurityRuleStatus(ctx, sr, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "", "", "", "", 0, time.Now())

			cmpSR := buildSecurityRuleResponse("sr-id-1", "test-sr-creation-confirmed", CSPResourceStateActive)
			m.expectProjectList(srProjectID, srProjectName)
			m.expectVpcList(srProjectID, srVpcID, srVpcName)
			m.expectSGList(srProjectID, srVpcID, srSGID, srSGName)
			m.expectSRList(srProjectID, srVpcID, srSGID, cmpSR)

			_, err := m.r.HandleReconcile(ctx, sr)
			Expect(err).To(Succeed())

			updated := &v1alpha1.SecurityRule{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronized))
		})
	})

	Describe("Creation accomplished", func() {
		It("transitions to Active+Synchronized and sets ResourceID", func() {
			m := newSRReconcilerWithMocks(GinkgoT())
			sr = createTestSecurityRule(ctx, "test-sr-creation-accomplished", defaultSecurityRuleSpec(srProjectName, srVpcName, srSGName))
			setSecurityRuleStatus(ctx, sr, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized, "", "", "", "", 0, time.Now())

			cmpSR := buildSecurityRuleResponse("sr-id-1", "test-sr-creation-accomplished", CSPResourceStateActive)
			m.expectProjectList(srProjectID, srProjectName)
			m.expectVpcList(srProjectID, srVpcID, srVpcName)
			m.expectSGList(srProjectID, srVpcID, srSGID, srSGName)
			m.expectSRList(srProjectID, srVpcID, srSGID, cmpSR)

			_, err := m.r.HandleReconcile(ctx, sr)
			Expect(err).To(Succeed())

			updated := &v1alpha1.SecurityRule{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseActive))
			Expect(updated.Status.ResourceID).To(Equal("sr-id-1"))
		})
	})

	Describe("ShouldBeUpdated", func() {
		It("transitions to Updating+ShallSynchronize when spec changes", func() {
			m := newSRReconcilerWithMocks(GinkgoT())
			sr = createTestSecurityRule(ctx, "test-sr-should-update", defaultSecurityRuleSpec(srProjectName, srVpcName, srSGName))
			setSecurityRuleStatus(ctx, sr, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "sr-id-1", srProjectID, srVpcID, srSGID, 1, time.Now())

			// Change tags to trigger generation bump
			srFetch := &v1alpha1.SecurityRule{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), srFetch)).To(Succeed())
			srFetch.Spec.Tags = []string{"tag1", "tag2"}
			Expect(k8sClient.Update(ctx, srFetch)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), sr)).To(Succeed())

			cmpSR := buildSecurityRuleResponse("sr-id-1", "test-sr-should-update", CSPResourceStateActive)
			m.expectProjectList(srProjectID, srProjectName)
			m.expectVpcList(srProjectID, srVpcID, srVpcName)
			m.expectSGList(srProjectID, srVpcID, srSGID, srSGName)
			m.expectSRList(srProjectID, srVpcID, srSGID, cmpSR)

			_, err := m.r.HandleReconcile(ctx, sr)
			Expect(err).To(Succeed())

			updated := &v1alpha1.SecurityRule{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseUpdating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseUpdating))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
		})
	})

	Describe("UpdateNotSupported", func() {
		It("transitions to Updating+Failed with error message when update is attempted", func() {
			m := newSRReconcilerWithMocks(GinkgoT())
			sr = createTestSecurityRule(ctx, "test-sr-update-not-supported", defaultSecurityRuleSpec(srProjectName, srVpcName, srSGName))
			setSecurityRuleStatus(ctx, sr, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize, "sr-id-1", srProjectID, srVpcID, srSGID, 1, time.Now())

			cmpSR := buildSecurityRuleResponse("sr-id-1", "test-sr-update-not-supported", CSPResourceStateActive)
			m.expectProjectList(srProjectID, srProjectName)
			m.expectVpcList(srProjectID, srVpcID, srVpcName)
			m.expectSGList(srProjectID, srVpcID, srSGID, srSGName)
			m.expectSRList(srProjectID, srVpcID, srSGID, cmpSR)

			_, err := m.r.HandleReconcile(ctx, sr)
			Expect(err).To(Succeed())

			updated := &v1alpha1.SecurityRule{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseUpdating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseUpdating))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonFailed))
			Expect(cond.Message).To(ContainSubstring("updating SecurityRule resources is not supported"))
		})
	})

	Describe("UpdateRollback", func() {
		It("rolls back spec from CMP values and transitions to Active+Synchronized", func() {
			m := newSRReconcilerWithMocks(GinkgoT())
			sr = createTestSecurityRule(ctx, "test-sr-update-rollback", defaultSecurityRuleSpec(srProjectName, srVpcName, srSGName))
			setSecurityRuleStatus(ctx, sr, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonFailed, "sr-id-1", srProjectID, srVpcID, srSGID, 1, time.Now())

			// Manually set the Updating condition with Failed reason (as kubeMarkUpdatingFailed would)
			srFetch := &v1alpha1.SecurityRule{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), srFetch)).To(Succeed())
			srFetch.Status.Conditions = []metav1.Condition{
				{
					Type:               string(v1alpha1.ResourcePhaseUpdating),
					Status:             metav1.ConditionTrue,
					Reason:             v1alpha1.ConditionReasonFailed,
					LastTransitionTime: metav1.Now(),
					Message:            "updating SecurityRule resources is not supported",
				},
			}
			Expect(k8sClient.Status().Update(ctx, srFetch)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), sr)).To(Succeed())

			cmpSR := buildSecurityRuleResponse("sr-id-1", "test-sr-update-rollback", CSPResourceStateActive)
			cmpSR.Metadata.Tags = []string{"cmp-tag1"}
			cmpSR.Metadata.LocationResponse = &arubatypes.LocationResponse{Value: "ITBG-Bergamo"}
			cmpSR.Properties.Protocol = "UDP"
			cmpSR.Properties.Port = "53"
			cmpSR.Properties.Direction = arubatypes.RuleDirection("Egress")
			cmpSR.Properties.Target = &arubatypes.RuleTarget{
				Kind:  arubatypes.EndpointTypeDto("Ip"),
				Value: "10.0.0.0/8",
			}
			m.expectProjectList(srProjectID, srProjectName)
			m.expectVpcList(srProjectID, srVpcID, srVpcName)
			m.expectSGList(srProjectID, srVpcID, srSGID, srSGName)
			m.expectSRList(srProjectID, srVpcID, srSGID, cmpSR)

			_, err := m.r.HandleReconcile(ctx, sr)
			Expect(err).To(Succeed())

			updated := &v1alpha1.SecurityRule{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseActive))
			// Spec should be rolled back to CMP values
			Expect(updated.Spec.Tags).To(Equal(cmpSR.Metadata.Tags))
			Expect(updated.Spec.Region).To(Equal(cmpSR.Metadata.LocationResponse.Value))
			Expect(updated.Spec.Protocol).To(Equal(cmpSR.Properties.Protocol))
			Expect(updated.Spec.Port).To(Equal(cmpSR.Properties.Port))
			Expect(updated.Spec.Direction).To(Equal(string(cmpSR.Properties.Direction)))
			Expect(updated.Spec.Target.Type).To(Equal(string(cmpSR.Properties.Target.Kind)))
			Expect(updated.Spec.Target.Value).To(Equal(cmpSR.Properties.Target.Value))
		})
	})

	Describe("Should delete", func() {
		It("transitions to Deleting+ShallSynchronize when deletion is requested on Active SecurityRule", func() {
			m := newSRReconcilerWithMocks(GinkgoT())
			sr = createTestSecurityRule(ctx, "test-sr-should-delete", defaultSecurityRuleSpec(srProjectName, srVpcName, srSGName))
			srFetch := &v1alpha1.SecurityRule{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), srFetch)).To(Succeed())
			srFetch.Finalizers = []string{securityRuleFinalizerName}
			Expect(k8sClient.Update(ctx, srFetch)).To(Succeed())
			setSecurityRuleStatus(ctx, sr, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "sr-id-1", srProjectID, srVpcID, srSGID, 1, time.Now())
			Expect(k8sClient.Delete(ctx, sr)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), sr)).To(Succeed())

			// All 3 parent IDs cached → only SR list called
			cmpSR := buildSecurityRuleResponse("sr-id-1", "test-sr-should-delete", CSPResourceStateActive)
			m.expectSRList(srProjectID, srVpcID, srSGID, cmpSR)

			_, err := m.r.HandleReconcile(ctx, sr)
			Expect(err).To(Succeed())

			updated := &v1alpha1.SecurityRule{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
		})
	})

	Describe("Delete on CMP", func() {
		It("transitions to Deleting+Synchronizing after successful CMP delete", func() {
			m := newSRReconcilerWithMocks(GinkgoT())
			sr = createTestSecurityRule(ctx, "test-sr-delete-cmp", defaultSecurityRuleSpec(srProjectName, srVpcName, srSGName))
			srFetch := &v1alpha1.SecurityRule{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), srFetch)).To(Succeed())
			srFetch.Finalizers = []string{securityRuleFinalizerName}
			Expect(k8sClient.Update(ctx, srFetch)).To(Succeed())
			setSecurityRuleStatus(ctx, sr, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, "sr-id-1", srProjectID, srVpcID, srSGID, 1, time.Now())
			Expect(k8sClient.Delete(ctx, sr)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), sr)).To(Succeed())

			// All 3 parent IDs cached → only SR list called, then delete
			cmpSR := buildSecurityRuleResponse("sr-id-1", "test-sr-delete-cmp", CSPResourceStateActive)
			m.expectSRList(srProjectID, srVpcID, srSGID, cmpSR)
			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
			m.mockNetwork.EXPECT().SecurityGroupRules().Return(m.mockSRules)
			m.mockSRules.EXPECT().Delete(mock.Anything, srProjectID, srVpcID, srSGID, "sr-id-1", mock.Anything).Return(buildDeleteResponse(http.StatusOK), nil)

			_, err := m.r.HandleReconcile(ctx, sr)
			Expect(err).To(Succeed())

			updated := &v1alpha1.SecurityRule{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
		})
	})

	Describe("DeletionOnCMPNotNeeded", func() {
		It("advances to Deleting+Synchronized when CMP SecurityRule is already gone", func() {
			m := newSRReconcilerWithMocks(GinkgoT())
			sr = createTestSecurityRule(ctx, "test-sr-delete-not-needed", defaultSecurityRuleSpec(srProjectName, srVpcName, srSGName))
			srFetch := &v1alpha1.SecurityRule{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), srFetch)).To(Succeed())
			srFetch.Finalizers = []string{securityRuleFinalizerName}
			Expect(k8sClient.Update(ctx, srFetch)).To(Succeed())
			setSecurityRuleStatus(ctx, sr, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, "sr-id-1", srProjectID, srVpcID, srSGID, 1, time.Now())
			Expect(k8sClient.Delete(ctx, sr)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), sr)).To(Succeed())

			m.expectSRList(srProjectID, srVpcID, srSGID)

			_, err := m.r.HandleReconcile(ctx, sr)
			Expect(err).To(Succeed())

			updated := &v1alpha1.SecurityRule{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronized))
		})
	})

	Describe("CMP transitory during deletion", func() {
		It("returns LongRequeue when CMP state is Deleting", func() {
			m := newSRReconcilerWithMocks(GinkgoT())
			sr = createTestSecurityRule(ctx, "test-sr-deleting-transitory", defaultSecurityRuleSpec(srProjectName, srVpcName, srSGName))
			srFetch := &v1alpha1.SecurityRule{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), srFetch)).To(Succeed())
			srFetch.Finalizers = []string{securityRuleFinalizerName}
			Expect(k8sClient.Update(ctx, srFetch)).To(Succeed())
			setSecurityRuleStatus(ctx, sr, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronizing, "sr-id-1", srProjectID, srVpcID, srSGID, 1, time.Now())
			Expect(k8sClient.Delete(ctx, sr)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), sr)).To(Succeed())

			cmpSR := buildSecurityRuleResponse("sr-id-1", "test-sr-deleting-transitory", CSPResourceStateDeleting)
			m.expectSRList(srProjectID, srVpcID, srSGID, cmpSR)

			result, err := m.r.HandleReconcile(ctx, sr)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("Deletion accomplished", func() {
		It("transitions to Deleted phase when CMP SecurityRule is gone", func() {
			m := newSRReconcilerWithMocks(GinkgoT())
			sr = createTestSecurityRule(ctx, "test-sr-deletion-accomplished", defaultSecurityRuleSpec(srProjectName, srVpcName, srSGName))
			srFetch := &v1alpha1.SecurityRule{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), srFetch)).To(Succeed())
			srFetch.Finalizers = []string{securityRuleFinalizerName}
			Expect(k8sClient.Update(ctx, srFetch)).To(Succeed())
			setSecurityRuleStatus(ctx, sr, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronized, "sr-id-1", srProjectID, srVpcID, srSGID, 1, time.Now())
			Expect(k8sClient.Delete(ctx, sr)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), sr)).To(Succeed())

			m.expectSRList(srProjectID, srVpcID, srSGID)

			_, err := m.r.HandleReconcile(ctx, sr)
			Expect(err).To(Succeed())

			updated := &v1alpha1.SecurityRule{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleted))
		})
	})

	Describe("IsInError", func() {
		It("transitions to Failed+Synchronized when CMP state is Failed", func() {
			m := newSRReconcilerWithMocks(GinkgoT())
			sr = createTestSecurityRule(ctx, "test-sr-in-error", defaultSecurityRuleSpec(srProjectName, srVpcName, srSGName))
			setSecurityRuleStatus(ctx, sr, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "sr-id-1", srProjectID, srVpcID, srSGID, 1, time.Now())

			cmpSR := buildSecurityRuleResponse("sr-id-1", "test-sr-in-error", CSPResourceStateFailed)
			m.expectProjectList(srProjectID, srProjectName)
			m.expectVpcList(srProjectID, srVpcID, srVpcName)
			m.expectSGList(srProjectID, srVpcID, srSGID, srSGName)
			m.expectSRList(srProjectID, srVpcID, srSGID, cmpSR)

			_, err := m.r.HandleReconcile(ctx, sr)
			Expect(err).To(Succeed())

			updated := &v1alpha1.SecurityRule{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseFailed))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronized))
		})
	})

	Describe("Phase timeout", func() {
		It("transitions to Failed when stuck in transitory phase too long", func() {
			m := newSRReconcilerWithMocks(GinkgoT())
			sr = createTestSecurityRule(ctx, "test-sr-timeout", defaultSecurityRuleSpec(srProjectName, srVpcName, srSGName))
			setSecurityRuleStatus(ctx, sr, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, "", srProjectID, srVpcID, srSGID,
				0, time.Now().Add(-(reconciler.MaxPhaseTimeout + time.Minute)))

			cmpSR := buildSecurityRuleResponse("sr-id-1", "test-sr-timeout", CSPResourceStateActive)
			m.expectProjectList(srProjectID, srProjectName)
			m.expectVpcList(srProjectID, srVpcID, srVpcName)
			m.expectSGList(srProjectID, srVpcID, srSGID, srSGName)
			m.expectSRList(srProjectID, srVpcID, srSGID, cmpSR)

			_, err := m.r.HandleReconcile(ctx, sr)
			Expect(err).To(Succeed())

			updated := &v1alpha1.SecurityRule{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
		})
	})

	Describe("Project not found yet", func() {
		It("returns LongRequeue when project doesn't exist in CMP yet", func() {
			m := newSRReconcilerWithMocks(GinkgoT())
			sr = createTestSecurityRule(ctx, "test-sr-no-project", defaultSecurityRuleSpec(srProjectName, srVpcName, srSGName))

			m.mockAruba.EXPECT().FromProject().Return(m.mockProject)
			m.mockProject.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectList(), nil)

			result, err := m.r.HandleReconcile(ctx, sr)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("VPC not found yet", func() {
		It("returns LongRequeue when VPC doesn't exist in CMP yet", func() {
			m := newSRReconcilerWithMocks(GinkgoT())
			sr = createTestSecurityRule(ctx, "test-sr-no-vpc", defaultSecurityRuleSpec(srProjectName, srVpcName, srSGName))

			m.expectProjectList(srProjectID, srProjectName)

			emptyVpcList := &arubatypes.Response[arubatypes.VPCList]{
				Data:       &arubatypes.VPCList{},
				StatusCode: http.StatusOK,
			}
			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
			m.mockNetwork.EXPECT().VPCs().Return(m.mockVPCs)
			m.mockVPCs.EXPECT().List(mock.Anything, srProjectID, mock.Anything).Return(emptyVpcList, nil)

			result, err := m.r.HandleReconcile(ctx, sr)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("SecurityGroup not found yet", func() {
		It("returns LongRequeue when SecurityGroup doesn't exist in CMP yet", func() {
			m := newSRReconcilerWithMocks(GinkgoT())
			sr = createTestSecurityRule(ctx, "test-sr-no-sg", defaultSecurityRuleSpec(srProjectName, srVpcName, srSGName))

			m.expectProjectList(srProjectID, srProjectName)
			m.expectVpcList(srProjectID, srVpcID, srVpcName)

			emptySGList := &arubatypes.Response[arubatypes.SecurityGroupList]{
				Data:       &arubatypes.SecurityGroupList{},
				StatusCode: http.StatusOK,
			}
			m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
			m.mockNetwork.EXPECT().SecurityGroups().Return(m.mockSecGrps)
			m.mockSecGrps.EXPECT().List(mock.Anything, srProjectID, srVpcID, mock.Anything).Return(emptySGList, nil)

			result, err := m.r.HandleReconcile(ctx, sr)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("ProjectID, VpcID, and SecurityGroupID stamped in status", func() {
		It("stamps all three parent IDs on status when first transitioning", func() {
			m := newSRReconcilerWithMocks(GinkgoT())
			sr = createTestSecurityRule(ctx, "test-sr-ids-stamped", defaultSecurityRuleSpec(srProjectName, srVpcName, srSGName))

			m.expectProjectList(srProjectID, srProjectName)
			m.expectVpcList(srProjectID, srVpcID, srVpcName)
			m.expectSGList(srProjectID, srVpcID, srSGID, srSGName)
			m.expectSRList(srProjectID, srVpcID, srSGID)

			_, err := m.r.HandleReconcile(ctx, sr)
			Expect(err).To(Succeed())

			updated := &v1alpha1.SecurityRule{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), updated)).To(Succeed())
			Expect(updated.Status.ProjectID).To(Equal(srProjectID))
			Expect(updated.Status.VPCID).To(Equal(srVpcID))
			Expect(updated.Status.SecurityGroupID).To(Equal(srSGID))
		})
	})

	Describe("CMP error handling", func() {
		DescribeTable("CMP create fails — preserves Creating+ShallSynchronize, surfaces error in condition",
			func(name string, statusCode int, expectedRequeue time.Duration) {
				m := newSRReconcilerWithMocks(GinkgoT())
				sr = createTestSecurityRule(ctx, name, defaultSecurityRuleSpec(srProjectName, srVpcName, srSGName))
				setSecurityRuleStatus(ctx, sr, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, "", "", "", "", 0, time.Now())

				m.expectProjectList(srProjectID, srProjectName)
				m.expectVpcList(srProjectID, srVpcID, srVpcName)
				m.expectSGList(srProjectID, srVpcID, srSGID, srSGName)
				m.expectSRList(srProjectID, srVpcID, srSGID)
				m.mockAruba.EXPECT().FromNetwork().Return(m.mockNetwork)
				m.mockNetwork.EXPECT().SecurityGroupRules().Return(m.mockSRules)
				m.mockSRules.EXPECT().Create(mock.Anything, srProjectID, srVpcID, srSGID, mock.Anything, mock.Anything).Return(buildSRCRUDResponse(statusCode), nil)

				result, err := m.r.HandleReconcile(ctx, sr)
				Expect(err).To(Succeed())
				Expect(result.RequeueAfter).To(Equal(expectedRequeue))

				updated := &v1alpha1.SecurityRule{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), updated)).To(Succeed())
				Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
				cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
				Expect(cond).NotTo(BeNil())
				Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
				Expect(cond.Message).To(ContainSubstring("ERROR"))
			},
			Entry("4xx → LongRequeueAfter, no phase change", "sr-cmp-err-create-400", http.StatusBadRequest, reconciler.LongRequeueAfter),
			Entry("5xx → ShortRequeueAfter, no phase change", "sr-cmp-err-create-500", http.StatusInternalServerError, reconciler.ShortRequeueAfter),
		)
	})
})
