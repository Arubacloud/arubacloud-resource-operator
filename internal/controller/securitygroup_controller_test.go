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

func defaultSecurityGroupSpec(projectName, vpcName string) v1alpha1.SecurityGroupSpec {
	return v1alpha1.SecurityGroupSpec{
		Tenant: "test-tenant",
		Tags:   []string{"tag1"},
		Region: "ITBG-Bergamo",
		ProjectReference: v1alpha1.ResourceReference{Name: projectName, Namespace: "default"},
		VPCReference:     v1alpha1.ResourceReference{Name: vpcName, Namespace: "default"},
	}
}

func createTestSecurityGroup(ctx context.Context, name string, spec v1alpha1.SecurityGroupSpec) *v1alpha1.SecurityGroup {
	sg := &v1alpha1.SecurityGroup{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       spec,
	}
	ExpectWithOffset(1, k8sClient.Create(ctx, sg)).To(Succeed())
	return sg
}

func setSecurityGroupStatus(ctx context.Context, sg *v1alpha1.SecurityGroup, phase v1alpha1.ResourcePhase, reason string, resourceID, projectID, vpcID string, observedGen int64, conditionTime time.Time) {
	s := sg.DeepCopy()
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), s)).To(Succeed())
	s.Status.Phase = phase
	s.Status.ResourceID = resourceID
	s.Status.ProjectID = projectID
	s.Status.VPCID = vpcID
	s.Status.ObservedGeneration = observedGen
	if phase != "" {
		s.Status.Conditions = []metav1.Condition{{
			Type: string(phase), Status: metav1.ConditionTrue, Reason: reason,
			LastTransitionTime: metav1.NewTime(conditionTime), Message: string(phase) + " " + reason,
		}}
	}
	ExpectWithOffset(1, k8sClient.Status().Update(ctx, s)).To(Succeed())
	ExpectWithOffset(1, k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), sg)).To(Succeed())
}

type sgFake struct {
	r *SecurityGroupReconciler
	f *fakeCMP
}

func newSGReconcilerWithFake() *sgFake {
	f := newFakeCMP()
	DeferCleanup(f.close)
	return &sgFake{r: NewSecurityGroupReconciler(newTestReconciler(GinkgoT(), f)), f: f}
}

func (m *sgFake) stageParents(prjID, prjName, vpcID, vpcName string) {
	m.f.stage("projects", projectItem(prjID, prjName, nil, "", false))
	m.f.stage("vpcs", cmpItem(vpcID, vpcName, "Active"))
}
func (m *sgFake) stageSGs(items ...map[string]any) { m.f.stage("securityGroups", items...) }

var _ = Describe("SecurityGroupReconciler", func() {
	const (
		sgPrjName = "test-sg-project-ref"
		sgPrjID   = "sg-proj-id-1"
		sgVpcName = "test-sg-vpc-ref"
		sgVpcID   = "sg-vpc-id-1"
	)

	var (
		ctx context.Context
		sg  *v1alpha1.SecurityGroup
	)

	BeforeEach(func() { ctx = context.Background() })

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

	It("transitions to Creating+ShallSynchronize when CMP has no security group", func() {
		m := newSGReconcilerWithFake()
		sg = createTestSecurityGroup(ctx, "test-sg-first", defaultSecurityGroupSpec(sgPrjName, sgVpcName))
		setSecurityGroupStatus(ctx, sg, v1alpha1.ResourcePhasePending, v1alpha1.ConditionReasonSynchronized, "", "", "", 0, time.Now())
		m.stageParents(sgPrjID, sgPrjName, sgVpcID, sgVpcName)

		_, err := m.r.HandleReconcile(ctx, sg)
		Expect(err).To(Succeed())

		updated := &v1alpha1.SecurityGroup{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
	})

	It("transitions to Active+Synchronized when CMP security group is active", func() {
		m := newSGReconcilerWithFake()
		sg = createTestSecurityGroup(ctx, "test-sg-active", defaultSecurityGroupSpec(sgPrjName, sgVpcName))
		setSecurityGroupStatus(ctx, sg, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized, "", "", "", 0, time.Now())
		m.stageParents(sgPrjID, sgPrjName, sgVpcID, sgVpcName)
		m.stageSGs(cmpItem("sg-id-1", "test-sg-active", "Active"))

		_, err := m.r.HandleReconcile(ctx, sg)
		Expect(err).To(Succeed())

		updated := &v1alpha1.SecurityGroup{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseActive))
		Expect(updated.Status.ResourceID).To(Equal("sg-id-1"))
	})

	It("transitions to Deleting+Synchronizing after successful CMP delete", func() {
		m := newSGReconcilerWithFake()
		sg = createTestSecurityGroup(ctx, "test-sg-delete", defaultSecurityGroupSpec(sgPrjName, sgVpcName))
		sFetch := &v1alpha1.SecurityGroup{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), sFetch)).To(Succeed())
		sFetch.Finalizers = []string{securityGroupFinalizerName}
		Expect(k8sClient.Update(ctx, sFetch)).To(Succeed())
		setSecurityGroupStatus(ctx, sg, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, "sg-id-1", sgPrjID, sgVpcID, 1, time.Now())
		Expect(k8sClient.Delete(ctx, sg)).To(Succeed())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), sg)).To(Succeed())
		m.stageParents(sgPrjID, sgPrjName, sgVpcID, sgVpcName)
		m.stageSGs(cmpItem("sg-id-1", "test-sg-delete", "Active"))

		_, err := m.r.HandleReconcile(ctx, sg)
		Expect(err).To(Succeed())

		updated := &v1alpha1.SecurityGroup{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
		Expect(findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting)).Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
	})

	It("sets Failed+ValidationFailed when SG tenant differs from parent VPC tenant", func() {
		m := newSGReconcilerWithFake()
		kubeVpc := createTestVpc(ctx, sgVpcName, v1alpha1.VPCSpec{
			Tenant: "other-tenant", Region: "ITBG-Bergamo",
			ProjectReference: v1alpha1.ResourceReference{Name: sgPrjName, Namespace: "default"},
		})
		defer func() { _ = k8sClient.Delete(ctx, kubeVpc) }()

		sg = createTestSecurityGroup(ctx, "test-sg-validation", defaultSecurityGroupSpec(sgPrjName, sgVpcName))
		setSecurityGroupStatus(ctx, sg, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "sg-id-val", sgPrjID, sgVpcID, 0, time.Now())

		result, err := m.r.HandleReconcile(ctx, sg)
		Expect(err).To(Succeed())
		Expect(result.RequeueAfter).To(Equal(reconciler.ShortRequeueAfter))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), sg)).To(Succeed())

		_, err = m.r.HandleReconcile(ctx, sg)
		Expect(err).To(Succeed())

		updated := &v1alpha1.SecurityGroup{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
		Expect(findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseFailed)).Message).To(ContainSubstring("tenant mismatch with VPC"))
	})
})
