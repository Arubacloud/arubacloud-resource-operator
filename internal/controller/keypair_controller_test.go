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

// kpItem stages a CMP key pair. KeyPair is a Family-B resource (no lifecycle
// state); the rollback flow reads back tags/region/public key.
func kpItem(id, name, publicKey string, tags []string) map[string]any {
	meta := cmpMeta(id, name)
	if tags != nil {
		meta["tags"] = tags
	}
	return map[string]any{
		"metadata":   meta,
		"properties": map[string]any{"value": publicKey},
	}
}

func defaultKeyPairSpec(projectName string) v1alpha1.KeyPairSpec {
	return v1alpha1.KeyPairSpec{
<<<<<<< HEAD
		Tenant:           "test-tenant",
		Region:           "ITBG-Bergamo",
		Tags:             []string{"tag1"},
		Value:            "ssh-rsa AAAAB3NzaC1 test-key",
=======
		Tenant: "test-tenant",
		Region: "ITBG-Bergamo",
		Tags:   []string{"tag1"},
		Value:  "ssh-rsa AAAAB3NzaC1 test-key",
>>>>>>> 28ce438 (test: blockstorage, vpc, elasticip, keypair controller tests on fake CMP)
		ProjectReference: v1alpha1.ResourceReference{Name: projectName, Namespace: "default"},
	}
}

func createTestKeyPair(ctx context.Context, name string, spec v1alpha1.KeyPairSpec) *v1alpha1.KeyPair {
	kp := &v1alpha1.KeyPair{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       spec,
	}
	ExpectWithOffset(1, k8sClient.Create(ctx, kp)).To(Succeed())
	return kp
}

func setKeyPairStatus(ctx context.Context, kp *v1alpha1.KeyPair, phase v1alpha1.ResourcePhase, reason string, resourceID string, projectID string, observedGen int64, conditionTime time.Time) {
	k := kp.DeepCopy()
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), k)).To(Succeed())
	k.Status.Phase = phase
	k.Status.ResourceID = resourceID
	k.Status.ProjectID = projectID
	k.Status.ObservedGeneration = observedGen
	if phase != "" {
		k.Status.Conditions = []metav1.Condition{{
			Type: string(phase), Status: metav1.ConditionTrue, Reason: reason,
			LastTransitionTime: metav1.NewTime(conditionTime), Message: string(phase) + " " + reason,
		}}
	}
	ExpectWithOffset(1, k8sClient.Status().Update(ctx, k)).To(Succeed())
	ExpectWithOffset(1, k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), kp)).To(Succeed())
}

type kpFake struct {
	r *KeyPairReconciler
	f *fakeCMP
}

func newKpReconcilerWithFake() *kpFake {
	f := newFakeCMP()
	DeferCleanup(f.close)
	return &kpFake{r: NewKeyPairReconciler(newTestReconciler(GinkgoT(), f)), f: f}
}

<<<<<<< HEAD
func (m *kpFake) stageProject(id, name string) {
	m.f.stage("projects", projectItem(id, name, nil, "", false))
}
=======
func (m *kpFake) stageProject(id, name string)     { m.f.stage("projects", projectItem(id, name, nil, "", false)) }
>>>>>>> 28ce438 (test: blockstorage, vpc, elasticip, keypair controller tests on fake CMP)
func (m *kpFake) stageKeyPairs(items ...map[string]any) { m.f.stage("keyPairs", items...) }

var _ = Describe("KeyPairReconciler", func() {
	const (
		kpProjectName = "test-kp-project-ref"
		kpProjectID   = "kp-proj-id-1"
	)

	var (
		ctx context.Context
		kp  *v1alpha1.KeyPair
	)

	BeforeEach(func() { ctx = context.Background() })

	AfterEach(func() {
		if kp != nil {
			k := &v1alpha1.KeyPair{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), k); err == nil {
				k.Finalizers = nil
				_ = k8sClient.Update(ctx, k)
				_ = k8sClient.Delete(ctx, k)
			}
			kp = nil
		}
	})

	It("transitions to Creating+ShallSynchronize when CMP has no KeyPair", func() {
		m := newKpReconcilerWithFake()
		kp = createTestKeyPair(ctx, "test-kp-first", defaultKeyPairSpec(kpProjectName))
		setKeyPairStatus(ctx, kp, v1alpha1.ResourcePhasePending, v1alpha1.ConditionReasonSynchronized, "", "", 0, time.Now())
		m.stageProject(kpProjectID, kpProjectName)

		_, err := m.r.HandleReconcile(ctx, kp)
		Expect(err).To(Succeed())

		updated := &v1alpha1.KeyPair{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
	})

	It("transitions to Active+Synchronized when the CMP KeyPair exists", func() {
		m := newKpReconcilerWithFake()
		kp = createTestKeyPair(ctx, "test-kp-active", defaultKeyPairSpec(kpProjectName))
		setKeyPairStatus(ctx, kp, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized, "", "", 0, time.Now())
		m.stageProject(kpProjectID, kpProjectName)
		m.stageKeyPairs(kpItem("kp-id-1", "test-kp-active", "ssh-rsa AAAAB3NzaC1 test-key", []string{"tag1"}))

		_, err := m.r.HandleReconcile(ctx, kp)
		Expect(err).To(Succeed())

		updated := &v1alpha1.KeyPair{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseActive))
		Expect(updated.Status.ResourceID).To(Equal("kp-id-1"))
	})

	It("rolls the spec back to CMP and returns Active when an unsupported update is attempted", func() {
		m := newKpReconcilerWithFake()
		kp = createTestKeyPair(ctx, "test-kp-rollback", defaultKeyPairSpec(kpProjectName))
		setKeyPairStatus(ctx, kp, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "kp-id-1", kpProjectID, 1, time.Now())

		// Change a mutable-looking field to bump generation → triggers the update flow.
		kFetch := &v1alpha1.KeyPair{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), kFetch)).To(Succeed())
		kFetch.Spec.Tags = []string{"changed"}
		Expect(k8sClient.Update(ctx, kFetch)).To(Succeed())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), kp)).To(Succeed())

		m.stageProject(kpProjectID, kpProjectName)
		m.stageKeyPairs(kpItem("kp-id-1", "test-kp-rollback", "ssh-rsa AAAAB3NzaC1 test-key", []string{"tag1"}))

		// First reconcile: marks Updating+Failed (update not supported).
		_, err := m.r.HandleReconcile(ctx, kp)
		Expect(err).To(Succeed())

		// Subsequent reconciles roll the spec back and return Active. Re-fetch the
		// object each pass (as the production reconcile loop does); the staged CMP
		// key pair persists across GETs, so no re-staging is needed.
		Eventually(func() v1alpha1.ResourcePhase {
			fresh := &v1alpha1.KeyPair{}
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), fresh)
			_, _ = m.r.HandleReconcile(ctx, fresh)
			updated := &v1alpha1.KeyPair{}
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), updated)
			return updated.Status.Phase
		}, 3*time.Second, 50*time.Millisecond).Should(Equal(v1alpha1.ResourcePhaseActive))

		updated := &v1alpha1.KeyPair{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), updated)).To(Succeed())
		Expect(updated.Spec.Tags).To(Equal([]string{"tag1"})) // rolled back
	})

	It("transitions to Deleting+Synchronizing after successful CMP delete", func() {
		m := newKpReconcilerWithFake()
		kp = createTestKeyPair(ctx, "test-kp-delete", defaultKeyPairSpec(kpProjectName))
		kFetch := &v1alpha1.KeyPair{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), kFetch)).To(Succeed())
		kFetch.Finalizers = []string{keyPairFinalizerName}
		Expect(k8sClient.Update(ctx, kFetch)).To(Succeed())
		setKeyPairStatus(ctx, kp, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, "kp-id-1", kpProjectID, 1, time.Now())
		Expect(k8sClient.Delete(ctx, kp)).To(Succeed())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), kp)).To(Succeed())
		m.stageKeyPairs(kpItem("kp-id-1", "test-kp-delete", "ssh-rsa AAAAB3NzaC1 test-key", nil))

		_, err := m.r.HandleReconcile(ctx, kp)
		Expect(err).To(Succeed())

		updated := &v1alpha1.KeyPair{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
		Expect(findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting)).Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
	})

	It("sets Failed+ValidationFailed when KeyPair tenant differs from parent project tenant", func() {
		m := newKpReconcilerWithFake()
		proj := createTestProject(ctx, kpProjectName, v1alpha1.ProjectSpec{Tenant: "other-tenant"})
		defer func() { _ = k8sClient.Delete(ctx, proj) }()

		kp = createTestKeyPair(ctx, "test-kp-validation", defaultKeyPairSpec(kpProjectName))
		setKeyPairStatus(ctx, kp, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "kp-id-val", kpProjectID, 0, time.Now())

		result, err := m.r.HandleReconcile(ctx, kp)
		Expect(err).To(Succeed())
		Expect(result.RequeueAfter).To(Equal(reconciler.ShortRequeueAfter))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), kp)).To(Succeed())

		_, err = m.r.HandleReconcile(ctx, kp)
		Expect(err).To(Succeed())

		updated := &v1alpha1.KeyPair{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(kp), updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
		Expect(findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseFailed)).Message).To(ContainSubstring("tenant mismatch with Project"))
	})
})
