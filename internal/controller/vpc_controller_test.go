package controller

import (
	"context"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
)

// --- Test fixture helpers ---

func defaultVPCSpec(projectName string) v1alpha1.VPCSpec {
	return v1alpha1.VPCSpec{
		Tenant: "test-tenant",
		Region: "ITBG-Bergamo",
		Tags:   []string{"tag1"},
		ProjectReference: v1alpha1.ResourceReference{Name: projectName, Namespace: "default"},
	}
}

func createTestVpc(ctx context.Context, name string, spec v1alpha1.VPCSpec) *v1alpha1.VPC {
	vpc := &v1alpha1.VPC{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       spec,
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
		v.Status.Conditions = []metav1.Condition{{
			Type: string(phase), Status: metav1.ConditionTrue, Reason: reason,
			LastTransitionTime: metav1.NewTime(conditionTime), Message: string(phase) + " " + reason,
		}}
	}
	ExpectWithOffset(1, k8sClient.Status().Update(ctx, v)).To(Succeed())
	ExpectWithOffset(1, k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), vpc)).To(Succeed())
}

type vpcFake struct {
	r *VPCReconciler
	f *fakeCMP
}

func newVPCReconcilerWithFake() *vpcFake {
	f := newFakeCMP()
	DeferCleanup(f.close)
	return &vpcFake{r: NewVPCReconciler(newTestReconciler(GinkgoT(), f)), f: f}
}

func (m *vpcFake) stageProject(id, name string) { m.f.stage("projects", projectItem(id, name, nil, "", false)) }
func (m *vpcFake) stageVPCs(items ...map[string]any) { m.f.stage("vpcs", items...) }

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

	BeforeEach(func() { ctx = context.Background() })

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

	It("transitions to Creating+ShallSynchronize when CMP has no VPC", func() {
		m := newVPCReconcilerWithFake()
		vpc = createTestVpc(ctx, "test-vpc-first", defaultVPCSpec(vpcProjectName))
		setVPCStatus(ctx, vpc, v1alpha1.ResourcePhasePending, v1alpha1.ConditionReasonSynchronized, "", "", 0, time.Now())
		m.stageProject(vpcProjectID, vpcProjectName)

		_, err := m.r.HandleReconcile(ctx, vpc)
		Expect(err).To(Succeed())

		updated := &v1alpha1.VPC{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
		Expect(findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating)).Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
	})

	It("transitions to Active+Synchronized when CMP VPC is active", func() {
		m := newVPCReconcilerWithFake()
		vpc = createTestVpc(ctx, "test-vpc-active", defaultVPCSpec(vpcProjectName))
		setVPCStatus(ctx, vpc, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized, "", "", 0, time.Now())
		m.stageProject(vpcProjectID, vpcProjectName)
		m.stageVPCs(cmpItem("vpc-id-1", "test-vpc-active", "Active"))

		_, err := m.r.HandleReconcile(ctx, vpc)
		Expect(err).To(Succeed())

		updated := &v1alpha1.VPC{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseActive))
		Expect(updated.Status.ResourceID).To(Equal("vpc-id-1"))
	})

	It("waits (LongRequeue) when CMP VPC is transitory", func() {
		m := newVPCReconcilerWithFake()
		vpc = createTestVpc(ctx, "test-vpc-wait", defaultVPCSpec(vpcProjectName))
		setVPCStatus(ctx, vpc, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "", "", 0, time.Now())
		m.stageProject(vpcProjectID, vpcProjectName)
		m.stageVPCs(cmpItem("vpc-id-1", "test-vpc-wait", "Creating"))

		result, err := m.r.HandleReconcile(ctx, vpc)
		Expect(err).To(Succeed())
		Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
	})

	It("transitions to Failed when CMP VPC is in a failure state", func() {
		m := newVPCReconcilerWithFake()
		vpc = createTestVpc(ctx, "test-vpc-failed", defaultVPCSpec(vpcProjectName))
		setVPCStatus(ctx, vpc, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "vpc-id-1", vpcProjectID, 1, time.Now())
		m.stageProject(vpcProjectID, vpcProjectName)
		m.stageVPCs(cmpItem("vpc-id-1", "test-vpc-failed", "Failed"))

		_, err := m.r.HandleReconcile(ctx, vpc)
		Expect(err).To(Succeed())

		updated := &v1alpha1.VPC{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
	})

	It("transitions to Deleting+Synchronizing after successful CMP delete", func() {
		m := newVPCReconcilerWithFake()
		vpc = createTestVpc(ctx, "test-vpc-delete", defaultVPCSpec(vpcProjectName))
		vFetch := &v1alpha1.VPC{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), vFetch)).To(Succeed())
		vFetch.Finalizers = []string{vpcFinalizerName}
		Expect(k8sClient.Update(ctx, vFetch)).To(Succeed())
		setVPCStatus(ctx, vpc, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, "vpc-id-1", vpcProjectID, 1, time.Now())
		Expect(k8sClient.Delete(ctx, vpc)).To(Succeed())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), vpc)).To(Succeed())
		m.stageVPCs(cmpItem("vpc-id-1", "test-vpc-delete", "Active"))

		_, err := m.r.HandleReconcile(ctx, vpc)
		Expect(err).To(Succeed())

		updated := &v1alpha1.VPC{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
		Expect(findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting)).Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
	})

	DescribeTable("CMP create fails — preserves Creating+ShallSynchronize",
		func(name string, statusCode int, expectedRequeue time.Duration) {
			m := newVPCReconcilerWithFake()
			m.f.postStatus = statusCode
			vpc = createTestVpc(ctx, name, defaultVPCSpec(vpcProjectName))
			setVPCStatus(ctx, vpc, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, "", vpcProjectID, 0, time.Now())
			m.stageProject(vpcProjectID, vpcProjectName)

			result, err := m.r.HandleReconcile(ctx, vpc)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(expectedRequeue))

			updated := &v1alpha1.VPC{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
			Expect(findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating)).Message).To(ContainSubstring("ERROR"))
		},
		Entry("4xx → transient → LongRequeueAfter", "vpc-err-400", http.StatusBadRequest, reconciler.LongRequeueAfter),
		Entry("5xx → technical → ShortRequeueAfter", "vpc-err-500", http.StatusInternalServerError, reconciler.ShortRequeueAfter),
	)

	It("sets Failed+ValidationFailed when VPC tenant differs from parent project tenant", func() {
		m := newVPCReconcilerWithFake()
		proj := createTestProject(ctx, vpcProjectName, v1alpha1.ProjectSpec{Tenant: "other-tenant"})
		defer func() { _ = k8sClient.Delete(ctx, proj) }()

		vpc = createTestVpc(ctx, "test-vpc-validation", defaultVPCSpec(vpcProjectName))
		setVPCStatus(ctx, vpc, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "vpc-id-val", vpcProjectID, 0, time.Now())

		result, err := m.r.HandleReconcile(ctx, vpc)
		Expect(err).To(Succeed())
		Expect(result.RequeueAfter).To(Equal(reconciler.ShortRequeueAfter))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), vpc)).To(Succeed())

		_, err = m.r.HandleReconcile(ctx, vpc)
		Expect(err).To(Succeed())

		updated := &v1alpha1.VPC{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vpc), updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
		cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseFailed))
		Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonIntentionValidationFailed))
		Expect(cond.Message).To(ContainSubstring("tenant mismatch with Project"))
	})
})
