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

// projectItem stages a CMP project. Project is a Family-B resource (no lifecycle
// state), so the operator drives it off id/name plus the mutable description/tags.
func projectItem(id, name string, tags []string, description string, def bool) map[string]any {
	meta := map[string]any{"id": id, "name": name}
	if tags != nil {
		meta["tags"] = tags
	}
	return map[string]any{
		"metadata":   meta,
		"properties": map[string]any{"description": description, "default": def},
	}
}

// --- Shared K8s fixture helpers (used across controller test files) ---

func createTestProject(ctx context.Context, name string, spec v1alpha1.ProjectSpec) *v1alpha1.Project {
	proj := &v1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       spec,
	}
	ExpectWithOffset(1, k8sClient.Create(ctx, proj)).To(Succeed())
	return proj
}

func setProjectStatus(ctx context.Context, proj *v1alpha1.Project, phase v1alpha1.ResourcePhase, reason string, resourceID string, observedGen int64, conditionTime time.Time) {
	p := proj.DeepCopy()
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), p)).To(Succeed())
	p.Status.Phase = phase
	p.Status.ResourceID = resourceID
	p.Status.ObservedGeneration = observedGen
	if phase != "" {
		p.Status.Conditions = []metav1.Condition{{
			Type:               string(phase),
			Status:             metav1.ConditionTrue,
			Reason:             reason,
			LastTransitionTime: metav1.NewTime(conditionTime),
			Message:            string(phase) + " " + reason + " - OK",
		}}
	}
	ExpectWithOffset(1, k8sClient.Status().Update(ctx, p)).To(Succeed())
	ExpectWithOffset(1, k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), proj)).To(Succeed())
}

func defaultProjectSpec() v1alpha1.ProjectSpec {
	return v1alpha1.ProjectSpec{
		Tenant:      "test-tenant",
		Description: "test description",
		Tags:        []string{"tag1"},
	}
}

func newProjectReconcilerWithFake() (*ProjectReconciler, *fakeCMP) {
	f := newFakeCMP()
	DeferCleanup(f.close)
	return NewProjectReconciler(newTestReconciler(GinkgoT(), f)), f
}

// --- Tests ---

var _ = Describe("ProjectReconciler", func() {
	var (
		ctx  context.Context
		proj *v1alpha1.Project
	)

	BeforeEach(func() { ctx = context.Background() })

	AfterEach(func() {
		if proj != nil {
			p := &v1alpha1.Project{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), p); err == nil {
				p.Finalizers = nil
				_ = k8sClient.Update(ctx, p)
				_ = k8sClient.Delete(ctx, p)
			}
			proj = nil
		}
	})

	Describe("First reconciliation (no status)", func() {
		It("transitions to Creating+ShallSynchronize when CMP has no project", func() {
			r, _ := newProjectReconcilerWithFake()
			proj = createTestProject(ctx, "test-first-reconcile", defaultProjectSpec())
			setProjectStatus(ctx, proj, v1alpha1.ResourcePhasePending, v1alpha1.ConditionReasonSynchronized, "", 0, time.Now())

			_, err := r.HandleReconcile(ctx, proj)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))

			pendingCond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhasePending))
			Expect(pendingCond.Status).To(Equal(metav1.ConditionFalse))
		})
	})

	Describe("PendingAndDeleting", func() {
		It("transitions directly to Deleted when in Pending and being deleted", func() {
			r, _ := newProjectReconcilerWithFake()
			proj = createTestProject(ctx, "test-proj-pending-deleting", defaultProjectSpec())
			setProjectStatus(ctx, proj, v1alpha1.ResourcePhasePending, v1alpha1.ConditionReasonSynchronized, "", 0, time.Now())

			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), proj)).To(Succeed())
			proj.Finalizers = []string{projectFinalizerName}
			Expect(k8sClient.Update(ctx, proj)).To(Succeed())
			Expect(k8sClient.Delete(ctx, proj)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), proj)).To(Succeed())

			_, err := r.HandleReconcile(ctx, proj)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleted))
		})
	})

	Describe("Create on CMP", func() {
		It("transitions to Creating+Synchronizing after successful CMP create", func() {
			r, _ := newProjectReconcilerWithFake()
			proj = createTestProject(ctx, "test-create-cmp", defaultProjectSpec())
			setProjectStatus(ctx, proj, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, "", 0, time.Now())

			_, err := r.HandleReconcile(ctx, proj)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
		})
	})

	Describe("Waiting creation (Synchronizing, no CMP project yet)", func() {
		It("returns LongRequeue without updating status", func() {
			r, _ := newProjectReconcilerWithFake()
			proj = createTestProject(ctx, "test-wait-create", defaultProjectSpec())
			setProjectStatus(ctx, proj, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "", 0, time.Now())

			result, err := r.HandleReconcile(ctx, proj)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("Creation confirmed on CMP", func() {
		It("transitions to Creating+Synchronized when CMP project exists", func() {
			r, f := newProjectReconcilerWithFake()
			proj = createTestProject(ctx, "test-creation-confirmed", defaultProjectSpec())
			setProjectStatus(ctx, proj, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "", 0, time.Now())
			f.stage("projects", projectItem("cmp-id-1", "test-creation-confirmed", nil, "test description", false))

			_, err := r.HandleReconcile(ctx, proj)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronized))
		})
	})

	Describe("Creation accomplished", func() {
		It("transitions to Active+Synchronized and sets ResourceID", func() {
			r, f := newProjectReconcilerWithFake()
			proj = createTestProject(ctx, "test-creation-accomplished", defaultProjectSpec())
			setProjectStatus(ctx, proj, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized, "", 0, time.Now())
			f.stage("projects", projectItem("cmp-id-1", "test-creation-accomplished", nil, "test description", false))

			_, err := r.HandleReconcile(ctx, proj)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseActive))
			Expect(updated.Status.ResourceID).To(Equal("cmp-id-1"))
		})
	})

	Describe("Spec change needing update", func() {
		It("transitions to Updating+ShallSynchronize when spec differs from CMP", func() {
			r, f := newProjectReconcilerWithFake()
			proj = createTestProject(ctx, "test-needs-update", defaultProjectSpec())
			setProjectStatus(ctx, proj, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "cmp-id-1", 1, time.Now())

			pFetch := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), pFetch)).To(Succeed())
			pFetch.Spec.Description = "changed description"
			Expect(k8sClient.Update(ctx, pFetch)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), proj)).To(Succeed())

			f.stage("projects", projectItem("cmp-id-1", "test-needs-update", nil, "test description", false))

			_, err := r.HandleReconcile(ctx, proj)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseUpdating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseUpdating))
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
		})
	})

	Describe("Update on CMP", func() {
		It("transitions to Updating+Synchronizing after successful CMP update", func() {
			r, f := newProjectReconcilerWithFake()
			proj = createTestProject(ctx, "test-update-cmp", defaultProjectSpec())
			setProjectStatus(ctx, proj, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize, "cmp-id-1", 1, time.Now())
			f.stage("projects", projectItem("cmp-id-1", "test-update-cmp", nil, "old description", false))

			_, err := r.HandleReconcile(ctx, proj)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseUpdating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseUpdating))
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
		})
	})

	Describe("Update accomplished", func() {
		It("transitions back to Active+Synchronized", func() {
			r, f := newProjectReconcilerWithFake()
			proj = createTestProject(ctx, "test-update-accomplished", defaultProjectSpec())
			setProjectStatus(ctx, proj, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronized, "cmp-id-1", 1, time.Now())
			f.stage("projects", projectItem("cmp-id-1", "test-update-accomplished", nil, "test description", false))

			_, err := r.HandleReconcile(ctx, proj)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseActive))
		})
	})

	Describe("Should delete", func() {
		It("transitions to Deleting+ShallSynchronize when deletion is requested on Active resource", func() {
			r, f := newProjectReconcilerWithFake()
			proj = createTestProject(ctx, "test-should-delete", defaultProjectSpec())
			pFetch := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), pFetch)).To(Succeed())
			pFetch.Finalizers = []string{projectFinalizerName}
			Expect(k8sClient.Update(ctx, pFetch)).To(Succeed())
			setProjectStatus(ctx, proj, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "cmp-id-1", 1, time.Now())
			Expect(k8sClient.Delete(ctx, proj)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), proj)).To(Succeed())
			f.stage("projects", projectItem("cmp-id-1", "test-should-delete", nil, "test description", false))

			_, err := r.HandleReconcile(ctx, proj)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
		})
	})

	Describe("Delete on CMP", func() {
		It("transitions to Deleting+Synchronizing after successful CMP delete", func() {
			r, f := newProjectReconcilerWithFake()
			proj = createTestProject(ctx, "test-delete-cmp", defaultProjectSpec())
			pFetch := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), pFetch)).To(Succeed())
			pFetch.Finalizers = []string{projectFinalizerName}
			Expect(k8sClient.Update(ctx, pFetch)).To(Succeed())
			setProjectStatus(ctx, proj, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, "cmp-id-1", 1, time.Now())
			Expect(k8sClient.Delete(ctx, proj)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), proj)).To(Succeed())
			f.stage("projects", projectItem("cmp-id-1", "test-delete-cmp", nil, "test description", false))

			_, err := r.HandleReconcile(ctx, proj)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
		})
	})

	Describe("Deletion accomplished", func() {
		It("transitions to Deleted phase when CMP project is gone", func() {
			r, _ := newProjectReconcilerWithFake()
			proj = createTestProject(ctx, "test-deletion-accomplished", defaultProjectSpec())
			pFetch := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), pFetch)).To(Succeed())
			pFetch.Finalizers = []string{projectFinalizerName}
			Expect(k8sClient.Update(ctx, pFetch)).To(Succeed())
			setProjectStatus(ctx, proj, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronized, "cmp-id-1", 1, time.Now())
			Expect(k8sClient.Delete(ctx, proj)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), proj)).To(Succeed())

			_, err := r.HandleReconcile(ctx, proj)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleted))
		})
	})

	Describe("Phase timeout", func() {
		It("transitions to Failed when stuck in transitory phase too long", func() {
			r, f := newProjectReconcilerWithFake()
			proj = createTestProject(ctx, "test-timeout", defaultProjectSpec())
			setProjectStatus(ctx, proj, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, "",
				0, time.Now().Add(-(reconciler.MaxPhaseTimeout + time.Minute)))
			f.stage("projects", projectItem("cmp-id-1", "test-timeout", nil, "test description", false))

			_, err := r.HandleReconcile(ctx, proj)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
		})
	})

	Describe("CMP List error", func() {
		It("returns error from HandleReconcile when CMP responds with error status", func() {
			r, f := newProjectReconcilerWithFake()
			f.getStatus = http.StatusInternalServerError
			proj = createTestProject(ctx, "test-cmp-error", defaultProjectSpec())
			setProjectStatus(ctx, proj, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, "", 0, time.Now())

			_, err := r.HandleReconcile(ctx, proj)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("CMP error handling", func() {
		DescribeTable("CMP create fails — preserves Creating+ShallSynchronize, surfaces error in condition",
			func(name string, statusCode int, expectedRequeue time.Duration) {
				r, f := newProjectReconcilerWithFake()
				f.postStatus = statusCode
				proj = createTestProject(ctx, name, defaultProjectSpec())
				setProjectStatus(ctx, proj, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, "", 0, time.Now())

				result, err := r.HandleReconcile(ctx, proj)
				Expect(err).To(Succeed())
				Expect(result.RequeueAfter).To(Equal(expectedRequeue))

				updated := &v1alpha1.Project{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
				Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
				cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
				Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
				Expect(cond.Message).To(ContainSubstring("ERROR"))
			},
			Entry("4xx (no field errors) → transient → LongRequeueAfter", "proj-cmp-err-create-400", http.StatusBadRequest, reconciler.LongRequeueAfter),
			Entry("5xx → technical → ShortRequeueAfter", "proj-cmp-err-create-500", http.StatusInternalServerError, reconciler.ShortRequeueAfter),
		)

		It("CMP create semantic error (4xx with field errors) moves to Failed+ValidationFailed, then recovers after spec change", func() {
			r, f := newProjectReconcilerWithFake()
			f.postStatus = http.StatusBadRequest
			f.errKind = "validation"
			proj = createTestProject(ctx, "proj-semantic-recovery", defaultProjectSpec())
			setProjectStatus(ctx, proj, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, "", 0, time.Now())

			result, err := r.HandleReconcile(ctx, proj)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(BeZero()) // semantic error → no requeue

			updated := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
			failedCond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseFailed))
			Expect(failedCond.Reason).To(Equal(v1alpha1.ConditionReasonValidationFailed))

			// Spec change bumps generation → generation-gated recovery fires.
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), proj)).To(Succeed())
			proj.Spec.Description = "updated description after validation fix"
			Expect(k8sClient.Update(ctx, proj)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), proj)).To(Succeed())

			result, err = r.HandleReconcile(ctx, proj)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.ShortRequeueAfter))

			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhasePending))
		})
	})
})
