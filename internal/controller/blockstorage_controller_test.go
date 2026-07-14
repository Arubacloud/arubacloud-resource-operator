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

// --- CMP item builders ---

func bsItem(id, name, state string) map[string]any {
	return map[string]any{
		"metadata": cmpMeta(id, name),
		"properties": map[string]any{
			"sizeGb": 10, "billingPeriod": "Hour", "dataCenter": "zone1", "type": "Standard",
		},
		"status": map[string]any{"state": state},
	}
}

// --- Test fixture helpers ---

func defaultBSSpec(projectName string) v1alpha1.BlockStorageSpec {
	return v1alpha1.BlockStorageSpec{
		Tenant:        "test-tenant",
		Region:        "ITBG-Bergamo",
		SizeGB:        10,
		BillingPeriod: "Hour",
		Zone:          "zone1",
		Type:          "Standard",
		Tags:          []string{"tag1"},
		ProjectReference: v1alpha1.ResourceReference{
			Name:      projectName,
			Namespace: "default",
		},
	}
}

func createTestBlockStorage(ctx context.Context, name string, spec v1alpha1.BlockStorageSpec) *v1alpha1.BlockStorage {
	bs := &v1alpha1.BlockStorage{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       spec,
	}
	ExpectWithOffset(1, k8sClient.Create(ctx, bs)).To(Succeed())
	return bs
}

func setBSStatus(ctx context.Context, bs *v1alpha1.BlockStorage, phase v1alpha1.ResourcePhase, reason string, resourceID string, projectID string, observedGen int64, conditionTime time.Time) {
	b := bs.DeepCopy()
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), b)).To(Succeed())
	b.Status.Phase = phase
	b.Status.ResourceID = resourceID
	b.Status.ProjectID = projectID
	b.Status.ObservedGeneration = observedGen
	if phase != "" {
		b.Status.Conditions = []metav1.Condition{{
			Type:               string(phase),
			Status:             metav1.ConditionTrue,
			Reason:             reason,
			LastTransitionTime: metav1.NewTime(conditionTime),
			Message:            string(phase) + " " + reason + " - OK",
		}}
	}
	ExpectWithOffset(1, k8sClient.Status().Update(ctx, b)).To(Succeed())
	ExpectWithOffset(1, k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), bs)).To(Succeed())
}

type bsFake struct {
	r *BlockStorageReconciler
	f *fakeCMP
}

func newBSReconcilerWithFake() *bsFake {
	f := newFakeCMP()
	DeferCleanup(f.close)
	return &bsFake{r: NewBlockStorageReconciler(newTestReconciler(GinkgoT(), f)), f: f}
}

func (m *bsFake) stageProject(id, name string) {
	m.f.stage("projects", projectItem(id, name, nil, "", false))
}

func (m *bsFake) stageVolumes(items ...map[string]any) {
	m.f.stage("blockStorages", items...)
}

// --- Tests ---

var _ = Describe("BlockStorageReconciler", func() {
	const (
		bsProjectName = "test-project-ref"
		bsProjectID   = "proj-id-1"
	)

	var (
		ctx context.Context
		bs  *v1alpha1.BlockStorage
	)

	BeforeEach(func() { ctx = context.Background() })

	AfterEach(func() {
		if bs != nil {
			b := &v1alpha1.BlockStorage{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), b); err == nil {
				b.Finalizers = nil
				_ = k8sClient.Update(ctx, b)
				_ = k8sClient.Delete(ctx, b)
			}
			bs = nil
		}
	})

	Describe("First reconciliation", func() {
		It("transitions to Creating+ShallSynchronize when CMP has no BS", func() {
			m := newBSReconcilerWithFake()
			bs = createTestBlockStorage(ctx, "test-bs-first", defaultBSSpec(bsProjectName))
			setBSStatus(ctx, bs, v1alpha1.ResourcePhasePending, v1alpha1.ConditionReasonSynchronized, "", "", 0, time.Now())
			m.stageProject(bsProjectID, bsProjectName)

			_, err := m.r.HandleReconcile(ctx, bs)
			Expect(err).To(Succeed())

			updated := &v1alpha1.BlockStorage{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
			Expect(updated.Status.ProjectID).To(Equal(bsProjectID))
		})
	})

	Describe("Create on CMP", func() {
		It("transitions to Creating+Synchronizing after successful CMP create", func() {
			m := newBSReconcilerWithFake()
			bs = createTestBlockStorage(ctx, "test-bs-create-cmp", defaultBSSpec(bsProjectName))
			setBSStatus(ctx, bs, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, "", "", 0, time.Now())
			m.stageProject(bsProjectID, bsProjectName)

			_, err := m.r.HandleReconcile(ctx, bs)
			Expect(err).To(Succeed())

			updated := &v1alpha1.BlockStorage{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
		})
	})

	Describe("Waiting creation (BS in transitory CMP state)", func() {
		It("returns LongRequeue when CMP state is Creating", func() {
			m := newBSReconcilerWithFake()
			bs = createTestBlockStorage(ctx, "test-bs-wait-transitory", defaultBSSpec(bsProjectName))
			setBSStatus(ctx, bs, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "", "", 0, time.Now())
			m.stageProject(bsProjectID, bsProjectName)
			m.stageVolumes(bsItem("bs-id-1", "test-bs-wait-transitory", "Creating"))

			result, err := m.r.HandleReconcile(ctx, bs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("Creation accomplished", func() {
		It("transitions to Active+Synchronized and sets ResourceID", func() {
			m := newBSReconcilerWithFake()
			bs = createTestBlockStorage(ctx, "test-bs-accomplished", defaultBSSpec(bsProjectName))
			setBSStatus(ctx, bs, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized, "", "", 0, time.Now())
			m.stageProject(bsProjectID, bsProjectName)
			m.stageVolumes(bsItem("bs-id-1", "test-bs-accomplished", "Active"))

			_, err := m.r.HandleReconcile(ctx, bs)
			Expect(err).To(Succeed())

			updated := &v1alpha1.BlockStorage{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseActive))
			Expect(updated.Status.ResourceID).To(Equal("bs-id-1"))
		})
	})

	Describe("HasDeniedChanges", func() {
		It("returns LongRequeue when immutable field (size decrease) is changed", func() {
			m := newBSReconcilerWithFake()
			bs = createTestBlockStorage(ctx, "test-bs-denied", defaultBSSpec(bsProjectName))
			setBSStatus(ctx, bs, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "bs-id-1", bsProjectID, 1, time.Now())

			bFetch := &v1alpha1.BlockStorage{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), bFetch)).To(Succeed())
			bFetch.Spec.SizeGB = 1 // decrease from 10
			Expect(k8sClient.Update(ctx, bFetch)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), bs)).To(Succeed())

			m.stageProject(bsProjectID, bsProjectName)
			m.stageVolumes(bsItem("bs-id-1", "test-bs-denied", "Active"))

			result, err := m.r.HandleReconcile(ctx, bs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("IsInError", func() {
		It("transitions to Failed+Synchronized when CMP state is Failed", func() {
			m := newBSReconcilerWithFake()
			bs = createTestBlockStorage(ctx, "test-bs-in-error", defaultBSSpec(bsProjectName))
			setBSStatus(ctx, bs, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "bs-id-1", bsProjectID, 1, time.Now())
			m.stageProject(bsProjectID, bsProjectName)
			m.stageVolumes(bsItem("bs-id-1", "test-bs-in-error", "Failed"))

			_, err := m.r.HandleReconcile(ctx, bs)
			Expect(err).To(Succeed())

			updated := &v1alpha1.BlockStorage{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
		})
	})

	Describe("Project not found yet", func() {
		It("returns LongRequeue when project doesn't exist in CMP yet", func() {
			m := newBSReconcilerWithFake()
			bs = createTestBlockStorage(ctx, "test-bs-no-project", defaultBSSpec(bsProjectName))
			// no project staged

			result, err := m.r.HandleReconcile(ctx, bs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("Should delete", func() {
		It("transitions to Deleting+ShallSynchronize when deletion is requested on Active BS", func() {
			m := newBSReconcilerWithFake()
			bs = createTestBlockStorage(ctx, "test-bs-should-delete", defaultBSSpec(bsProjectName))
			bFetch := &v1alpha1.BlockStorage{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), bFetch)).To(Succeed())
			bFetch.Finalizers = []string{blockStorageFinalizerName}
			Expect(k8sClient.Update(ctx, bFetch)).To(Succeed())
			setBSStatus(ctx, bs, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "bs-id-1", bsProjectID, 1, time.Now())
			Expect(k8sClient.Delete(ctx, bs)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), bs)).To(Succeed())
			m.stageVolumes(bsItem("bs-id-1", "test-bs-should-delete", "Active"))

			_, err := m.r.HandleReconcile(ctx, bs)
			Expect(err).To(Succeed())

			updated := &v1alpha1.BlockStorage{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
		})
	})

	Describe("Delete on CMP", func() {
		It("transitions to Deleting+Synchronizing after successful CMP delete", func() {
			m := newBSReconcilerWithFake()
			bs = createTestBlockStorage(ctx, "test-bs-delete-cmp", defaultBSSpec(bsProjectName))
			bFetch := &v1alpha1.BlockStorage{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), bFetch)).To(Succeed())
			bFetch.Finalizers = []string{blockStorageFinalizerName}
			Expect(k8sClient.Update(ctx, bFetch)).To(Succeed())
			setBSStatus(ctx, bs, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, "bs-id-1", bsProjectID, 1, time.Now())
			Expect(k8sClient.Delete(ctx, bs)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), bs)).To(Succeed())
			m.stageVolumes(bsItem("bs-id-1", "test-bs-delete-cmp", "Active"))

			_, err := m.r.HandleReconcile(ctx, bs)
			Expect(err).To(Succeed())

			updated := &v1alpha1.BlockStorage{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
		})
	})

	Describe("Deletion accomplished", func() {
		It("transitions to Deleted phase when CMP BS is gone", func() {
			m := newBSReconcilerWithFake()
			bs = createTestBlockStorage(ctx, "test-bs-deletion-done", defaultBSSpec(bsProjectName))
			bFetch := &v1alpha1.BlockStorage{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), bFetch)).To(Succeed())
			bFetch.Finalizers = []string{blockStorageFinalizerName}
			Expect(k8sClient.Update(ctx, bFetch)).To(Succeed())
			setBSStatus(ctx, bs, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronized, "bs-id-1", bsProjectID, 1, time.Now())
			Expect(k8sClient.Delete(ctx, bs)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), bs)).To(Succeed())
			// no volumes staged → CMP gone

			_, err := m.r.HandleReconcile(ctx, bs)
			Expect(err).To(Succeed())

			updated := &v1alpha1.BlockStorage{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleted))
		})
	})

	Describe("Update on CMP", func() {
		It("transitions to Updating+Synchronizing after successful CMP update", func() {
			m := newBSReconcilerWithFake()
			bs = createTestBlockStorage(ctx, "test-bs-update-cmp", defaultBSSpec(bsProjectName))
			setBSStatus(ctx, bs, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize, "bs-id-1", bsProjectID, 1, time.Now())
			m.stageProject(bsProjectID, bsProjectName)
			m.stageVolumes(bsItem("bs-id-1", "test-bs-update-cmp", "Active"))

			_, err := m.r.HandleReconcile(ctx, bs)
			Expect(err).To(Succeed())

			updated := &v1alpha1.BlockStorage{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseUpdating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseUpdating))
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
		})
	})

	Describe("Phase timeout", func() {
		It("transitions to Failed when stuck in transitory phase too long", func() {
			m := newBSReconcilerWithFake()
			bs = createTestBlockStorage(ctx, "test-bs-timeout", defaultBSSpec(bsProjectName))
			setBSStatus(ctx, bs, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, "", bsProjectID,
				0, time.Now().Add(-(reconciler.MaxPhaseTimeout + time.Minute)))
			m.stageProject(bsProjectID, bsProjectName)
			m.stageVolumes(bsItem("bs-id-1", "test-bs-timeout", "Active"))

			_, err := m.r.HandleReconcile(ctx, bs)
			Expect(err).To(Succeed())

			updated := &v1alpha1.BlockStorage{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
		})
	})

	Describe("CMP error handling", func() {
		DescribeTable("CMP create fails — preserves Creating+ShallSynchronize, surfaces error",
			func(name string, statusCode int, expectedRequeue time.Duration) {
				m := newBSReconcilerWithFake()
				m.f.postStatus = statusCode
				bs = createTestBlockStorage(ctx, name, defaultBSSpec(bsProjectName))
				setBSStatus(ctx, bs, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, "", bsProjectID, 0, time.Now())
				m.stageProject(bsProjectID, bsProjectName)

				result, err := m.r.HandleReconcile(ctx, bs)
				Expect(err).To(Succeed())
				Expect(result.RequeueAfter).To(Equal(expectedRequeue))

				updated := &v1alpha1.BlockStorage{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), updated)).To(Succeed())
				Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
				cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
				Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
				Expect(cond.Message).To(ContainSubstring("ERROR"))
			},
			Entry("4xx → transient → LongRequeueAfter", "bs-cmp-err-create-400", http.StatusBadRequest, reconciler.LongRequeueAfter),
			Entry("5xx → technical → ShortRequeueAfter", "bs-cmp-err-create-500", http.StatusInternalServerError, reconciler.ShortRequeueAfter),
		)
	})

	Describe("Validation", func() {
		It("sets Failed+ValidationFailed when BlockStorage tenant differs from parent project tenant", func() {
			m := newBSReconcilerWithFake()
			proj := createTestProject(ctx, bsProjectName, v1alpha1.ProjectSpec{Tenant: "other-tenant"})
			defer func() { _ = k8sClient.Delete(ctx, proj) }()

			bs = createTestBlockStorage(ctx, "test-bs-validation-tenant", defaultBSSpec(bsProjectName))
			setBSStatus(ctx, bs, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "bs-id-val", bsProjectID, 0, time.Now())

			// First: owner ref setup → requeue.
			result, err := m.r.HandleReconcile(ctx, bs)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.ShortRequeueAfter))
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), bs)).To(Succeed())

			// Second: ivs fires at Stage 4 (before CMP) → validation fails.
			_, err = m.r.HandleReconcile(ctx, bs)
			Expect(err).To(Succeed())

			updated := &v1alpha1.BlockStorage{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bs), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseFailed))
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonIntentionValidationFailed))
			Expect(cond.Message).To(ContainSubstring("tenant mismatch with Project"))
		})
	})
})
