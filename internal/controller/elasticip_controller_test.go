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

func defaultElasticIPSpec(projectName string) v1alpha1.ElasticIPSpec {
	return v1alpha1.ElasticIPSpec{
		Tenant:           "test-tenant",
		Region:           "ITBG-Bergamo",
		Tags:             []string{"tag1"},
		BillingPeriod:    "Hour",
		ProjectReference: v1alpha1.ResourceReference{Name: projectName, Namespace: "default"},
	}
}

func createTestElasticIP(ctx context.Context, name string, spec v1alpha1.ElasticIPSpec) *v1alpha1.ElasticIP {
	eip := &v1alpha1.ElasticIP{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       spec,
	}
	ExpectWithOffset(1, k8sClient.Create(ctx, eip)).To(Succeed())
	return eip
}

func setElasticIPStatus(ctx context.Context, eip *v1alpha1.ElasticIP, phase v1alpha1.ResourcePhase, reason string, resourceID string, projectID string, observedGen int64, conditionTime time.Time) {
	e := eip.DeepCopy()
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), e)).To(Succeed())
	e.Status.Phase = phase
	e.Status.ResourceID = resourceID
	e.Status.ProjectID = projectID
	e.Status.ObservedGeneration = observedGen
	if phase != "" {
		e.Status.Conditions = []metav1.Condition{{
			Type: string(phase), Status: metav1.ConditionTrue, Reason: reason,
			LastTransitionTime: metav1.NewTime(conditionTime), Message: string(phase) + " " + reason,
		}}
	}
	ExpectWithOffset(1, k8sClient.Status().Update(ctx, e)).To(Succeed())
	ExpectWithOffset(1, k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), eip)).To(Succeed())
}

type eipFake struct {
	r *ElasticIPReconciler
	f *fakeCMP
}

func newEipReconcilerWithFake() *eipFake {
	f := newFakeCMP()
	DeferCleanup(f.close)
	return &eipFake{r: NewElasticIPReconciler(newTestReconciler(GinkgoT(), f)), f: f}
}

func (m *eipFake) stageProject(id, name string) {
	m.f.stage("projects", projectItem(id, name, nil, "", false))
}
func (m *eipFake) stageEIPs(items ...map[string]any) { m.f.stage("elasticIps", items...) }

var _ = Describe("ElasticIPReconciler", func() {
	const (
		eipProjectName = "test-eip-project-ref"
		eipProjectID   = "eip-proj-id-1"
	)

	var (
		ctx context.Context
		eip *v1alpha1.ElasticIP
	)

	BeforeEach(func() { ctx = context.Background() })

	AfterEach(func() {
		if eip != nil {
			e := &v1alpha1.ElasticIP{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), e); err == nil {
				e.Finalizers = nil
				_ = k8sClient.Update(ctx, e)
				_ = k8sClient.Delete(ctx, e)
			}
			eip = nil
		}
	})

	It("transitions to Creating+ShallSynchronize when CMP has no ElasticIP", func() {
		m := newEipReconcilerWithFake()
		eip = createTestElasticIP(ctx, "test-eip-first", defaultElasticIPSpec(eipProjectName))
		setElasticIPStatus(ctx, eip, v1alpha1.ResourcePhasePending, v1alpha1.ConditionReasonSynchronized, "", "", 0, time.Now())
		m.stageProject(eipProjectID, eipProjectName)

		_, err := m.r.HandleReconcile(ctx, eip)
		Expect(err).To(Succeed())

		updated := &v1alpha1.ElasticIP{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
	})

	It("transitions to Active+Synchronized when CMP EIP is in a usable state", func() {
		m := newEipReconcilerWithFake()
		eip = createTestElasticIP(ctx, "test-eip-active", defaultElasticIPSpec(eipProjectName))
		setElasticIPStatus(ctx, eip, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized, "", "", 0, time.Now())
		m.stageProject(eipProjectID, eipProjectName)
		m.stageEIPs(cmpItem("eip-id-1", "test-eip-active", "NotUsed"))

		_, err := m.r.HandleReconcile(ctx, eip)
		Expect(err).To(Succeed())

		updated := &v1alpha1.ElasticIP{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseActive))
		Expect(updated.Status.ResourceID).To(Equal("eip-id-1"))
	})

	It("transitions to Failed when CMP EIP is in a failure state", func() {
		m := newEipReconcilerWithFake()
		eip = createTestElasticIP(ctx, "test-eip-failed", defaultElasticIPSpec(eipProjectName))
		setElasticIPStatus(ctx, eip, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "eip-id-1", eipProjectID, 1, time.Now())
		m.stageProject(eipProjectID, eipProjectName)
		m.stageEIPs(cmpItem("eip-id-1", "test-eip-failed", "Failed"))

		_, err := m.r.HandleReconcile(ctx, eip)
		Expect(err).To(Succeed())

		updated := &v1alpha1.ElasticIP{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
	})

	It("transitions to Deleting+Synchronizing after successful CMP delete", func() {
		m := newEipReconcilerWithFake()
		eip = createTestElasticIP(ctx, "test-eip-delete", defaultElasticIPSpec(eipProjectName))
		eFetch := &v1alpha1.ElasticIP{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), eFetch)).To(Succeed())
		eFetch.Finalizers = []string{elasticIpFinalizerName}
		Expect(k8sClient.Update(ctx, eFetch)).To(Succeed())
		setElasticIPStatus(ctx, eip, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, "eip-id-1", eipProjectID, 1, time.Now())
		Expect(k8sClient.Delete(ctx, eip)).To(Succeed())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), eip)).To(Succeed())
		m.stageEIPs(cmpItem("eip-id-1", "test-eip-delete", "NotUsed"))

		_, err := m.r.HandleReconcile(ctx, eip)
		Expect(err).To(Succeed())

		updated := &v1alpha1.ElasticIP{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
		Expect(findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting)).Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
	})

	DescribeTable("CMP create fails — preserves Creating+ShallSynchronize",
		func(name string, statusCode int, expectedRequeue time.Duration) {
			m := newEipReconcilerWithFake()
			m.f.postStatus = statusCode
			eip = createTestElasticIP(ctx, name, defaultElasticIPSpec(eipProjectName))
			setElasticIPStatus(ctx, eip, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, "", eipProjectID, 0, time.Now())
			m.stageProject(eipProjectID, eipProjectName)

			result, err := m.r.HandleReconcile(ctx, eip)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(expectedRequeue))

			updated := &v1alpha1.ElasticIP{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
		},
		Entry("4xx → transient → LongRequeueAfter", "eip-err-400", http.StatusBadRequest, reconciler.LongRequeueAfter),
		Entry("5xx → technical → ShortRequeueAfter", "eip-err-500", http.StatusInternalServerError, reconciler.ShortRequeueAfter),
	)

	It("sets Failed+ValidationFailed when EIP tenant differs from parent project tenant", func() {
		m := newEipReconcilerWithFake()
		proj := createTestProject(ctx, eipProjectName, v1alpha1.ProjectSpec{Tenant: "other-tenant"})
		defer func() { _ = k8sClient.Delete(ctx, proj) }()

		eip = createTestElasticIP(ctx, "test-eip-validation", defaultElasticIPSpec(eipProjectName))
		setElasticIPStatus(ctx, eip, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "eip-id-val", eipProjectID, 0, time.Now())

		result, err := m.r.HandleReconcile(ctx, eip)
		Expect(err).To(Succeed())
		Expect(result.RequeueAfter).To(Equal(reconciler.ShortRequeueAfter))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), eip)).To(Succeed())

		_, err = m.r.HandleReconcile(ctx, eip)
		Expect(err).To(Succeed())

		updated := &v1alpha1.ElasticIP{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
		Expect(findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseFailed)).Message).To(ContainSubstring("tenant mismatch with Project"))
	})
})
