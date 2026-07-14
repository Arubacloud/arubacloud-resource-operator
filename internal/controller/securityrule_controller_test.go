package controller

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
)

func srItem(id, name, state string) map[string]any {
	meta := cmpMeta(id, name)
	meta["tags"] = []string{"tag1"}
	return map[string]any{
		"metadata": meta,
		"status":   map[string]any{"state": state},
		"properties": map[string]any{
			"direction": "Ingress",
			"protocol":  "TCP",
			"port":      "80",
			"target":    map[string]any{"kind": "Ip", "value": "0.0.0.0/0"},
		},
	}
}

func defaultSecurityRuleSpec(projectName, vpcName, sgName string) v1alpha1.SecurityRuleSpec {
	return v1alpha1.SecurityRuleSpec{
		Tenant:                 "test-tenant",
		Tags:                   []string{"tag1"},
		Region:                 "ITBG-Bergamo",
		Protocol:               "TCP",
		Port:                   "80",
		Direction:              "Ingress",
		Target:                 v1alpha1.SecurityRuleTarget{Type: "Ip", Value: "0.0.0.0/0"},
		ProjectReference:       v1alpha1.ResourceReference{Name: projectName, Namespace: "default"},
		VPCReference:           v1alpha1.ResourceReference{Name: vpcName, Namespace: "default"},
		SecurityGroupReference: v1alpha1.ResourceReference{Name: sgName, Namespace: "default"},
	}
}

func createTestSecurityRule(ctx context.Context, name string, spec v1alpha1.SecurityRuleSpec) *v1alpha1.SecurityRule {
	sr := &v1alpha1.SecurityRule{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       spec,
	}
	ExpectWithOffset(1, k8sClient.Create(ctx, sr)).To(Succeed())
	return sr
}

func setSecurityRuleStatus(ctx context.Context, sr *v1alpha1.SecurityRule, phase v1alpha1.ResourcePhase, reason string, resourceID, projectID, vpcID, sgID string, observedGen int64, conditionTime time.Time) {
	s := sr.DeepCopy()
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), s)).To(Succeed())
	s.Status.Phase = phase
	s.Status.ResourceID = resourceID
	s.Status.ProjectID = projectID
	s.Status.VPCID = vpcID
	s.Status.SecurityGroupID = sgID
	s.Status.ObservedGeneration = observedGen
	if phase != "" {
		s.Status.Conditions = []metav1.Condition{{
			Type: string(phase), Status: metav1.ConditionTrue, Reason: reason,
			LastTransitionTime: metav1.NewTime(conditionTime), Message: string(phase) + " " + reason,
		}}
	}
	ExpectWithOffset(1, k8sClient.Status().Update(ctx, s)).To(Succeed())
	ExpectWithOffset(1, k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), sr)).To(Succeed())
}

type srFake struct {
	r *SecurityRuleReconciler
	f *fakeCMP
}

func newSRReconcilerWithFake() *srFake {
	f := newFakeCMP()
	DeferCleanup(f.close)
	return &srFake{r: NewSecurityRuleReconciler(newTestReconciler(GinkgoT(), f)), f: f}
}

func (m *srFake) stageParents(prjID, prjName, vpcID, vpcName, sgID, sgName string) {
	m.f.stage("projects", projectItem(prjID, prjName, nil, "", false))
	m.f.stage("vpcs", cmpItem(vpcID, vpcName, "Active"))
	m.f.stage("securityGroups", cmpItem(sgID, sgName, "Active"))
}
func (m *srFake) stageRules(items ...map[string]any) { m.f.stage("securityRules", items...) }

var _ = Describe("SecurityRuleReconciler", func() {
	const (
		srPrjName = "test-sr-project-ref"
		srPrjID   = "sr-proj-id-1"
		srVpcName = "test-sr-vpc-ref"
		srVpcID   = "sr-vpc-id-1"
		srSGName  = "test-sr-sg-ref"
		srSGID    = "sr-sg-id-1"
	)

	var (
		ctx context.Context
		sr  *v1alpha1.SecurityRule
	)

	BeforeEach(func() { ctx = context.Background() })

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

	It("transitions to Creating+ShallSynchronize when CMP has no security rule", func() {
		m := newSRReconcilerWithFake()
		sr = createTestSecurityRule(ctx, "test-sr-first", defaultSecurityRuleSpec(srPrjName, srVpcName, srSGName))
		setSecurityRuleStatus(ctx, sr, v1alpha1.ResourcePhasePending, v1alpha1.ConditionReasonSynchronized, "", "", "", "", 0, time.Now())
		m.stageParents(srPrjID, srPrjName, srVpcID, srVpcName, srSGID, srSGName)

		_, err := m.r.HandleReconcile(ctx, sr)
		Expect(err).To(Succeed())

		updated := &v1alpha1.SecurityRule{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
	})

	It("transitions to Active+Synchronized when CMP security rule is active", func() {
		m := newSRReconcilerWithFake()
		sr = createTestSecurityRule(ctx, "test-sr-active", defaultSecurityRuleSpec(srPrjName, srVpcName, srSGName))
		setSecurityRuleStatus(ctx, sr, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized, "", "", "", "", 0, time.Now())
		m.stageParents(srPrjID, srPrjName, srVpcID, srVpcName, srSGID, srSGName)
		m.stageRules(srItem("sr-id-1", "test-sr-active", "Active"))

		_, err := m.r.HandleReconcile(ctx, sr)
		Expect(err).To(Succeed())

		updated := &v1alpha1.SecurityRule{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseActive))
		Expect(updated.Status.ResourceID).To(Equal("sr-id-1"))
	})

	It("transitions to Deleting+Synchronizing after successful CMP delete", func() {
		m := newSRReconcilerWithFake()
		sr = createTestSecurityRule(ctx, "test-sr-delete", defaultSecurityRuleSpec(srPrjName, srVpcName, srSGName))
		sFetch := &v1alpha1.SecurityRule{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), sFetch)).To(Succeed())
		sFetch.Finalizers = []string{securityRuleFinalizerName}
		Expect(k8sClient.Update(ctx, sFetch)).To(Succeed())
		setSecurityRuleStatus(ctx, sr, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, "sr-id-1", srPrjID, srVpcID, srSGID, 1, time.Now())
		Expect(k8sClient.Delete(ctx, sr)).To(Succeed())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), sr)).To(Succeed())
		m.stageParents(srPrjID, srPrjName, srVpcID, srVpcName, srSGID, srSGName)
		m.stageRules(srItem("sr-id-1", "test-sr-delete", "Active"))

		_, err := m.r.HandleReconcile(ctx, sr)
		Expect(err).To(Succeed())

		updated := &v1alpha1.SecurityRule{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
		Expect(findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting)).Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
	})

	It("rolls the spec back and returns Active when an unsupported update is attempted", func() {
		m := newSRReconcilerWithFake()
		sr = createTestSecurityRule(ctx, "test-sr-rollback", defaultSecurityRuleSpec(srPrjName, srVpcName, srSGName))
		setSecurityRuleStatus(ctx, sr, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "sr-id-1", srPrjID, srVpcID, srSGID, 1, time.Now())

		srFetch := &v1alpha1.SecurityRule{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), srFetch)).To(Succeed())
		srFetch.Spec.Port = "8080" // bump generation
		Expect(k8sClient.Update(ctx, srFetch)).To(Succeed())

		m.stageParents(srPrjID, srPrjName, srVpcID, srVpcName, srSGID, srSGName)
		m.stageRules(srItem("sr-id-1", "test-sr-rollback", "Active"))

		Eventually(func() v1alpha1.ResourcePhase {
			fresh := &v1alpha1.SecurityRule{}
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), fresh)
			_, _ = m.r.HandleReconcile(ctx, fresh)
			updated := &v1alpha1.SecurityRule{}
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), updated)
			return updated.Status.Phase
		}, 3*time.Second, 50*time.Millisecond).Should(Equal(v1alpha1.ResourcePhaseActive))

		updated := &v1alpha1.SecurityRule{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), updated)).To(Succeed())
		Expect(updated.Spec.Port).To(Equal("80")) // rolled back
	})

	It("sets Failed+ValidationFailed when SR tenant differs from parent SecurityGroup tenant", func() {
		m := newSRReconcilerWithFake()
		kubeVpc := createTestVpc(ctx, srVpcName, v1alpha1.VPCSpec{
			Tenant: "test-tenant", Region: "ITBG-Bergamo",
			ProjectReference: v1alpha1.ResourceReference{Name: srPrjName, Namespace: "default"},
		})
		defer func() { _ = k8sClient.Delete(ctx, kubeVpc) }()
		kubeSG := createTestSecurityGroup(ctx, srSGName, v1alpha1.SecurityGroupSpec{
			Tenant: "other-tenant", Region: "ITBG-Bergamo",
			ProjectReference: v1alpha1.ResourceReference{Name: srPrjName, Namespace: "default"},
			VPCReference:     v1alpha1.ResourceReference{Name: srVpcName, Namespace: "default"},
		})
		defer func() { _ = k8sClient.Delete(ctx, kubeSG) }()

		sr = createTestSecurityRule(ctx, "test-sr-validation", defaultSecurityRuleSpec(srPrjName, srVpcName, srSGName))
		setSecurityRuleStatus(ctx, sr, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "sr-id-val", srPrjID, srVpcID, srSGID, 0, time.Now())

		result, err := m.r.HandleReconcile(ctx, sr)
		Expect(err).To(Succeed())
		Expect(result.RequeueAfter).To(Equal(reconciler.ShortRequeueAfter))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), sr)).To(Succeed())

		_, err = m.r.HandleReconcile(ctx, sr)
		Expect(err).To(Succeed())

		updated := &v1alpha1.SecurityRule{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sr), updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
		Expect(findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseFailed)).Message).To(ContainSubstring("tenant mismatch with SecurityGroup"))
	})
})
