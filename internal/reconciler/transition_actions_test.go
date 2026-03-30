package reconciler

import (
	"context"
	"errors"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
	arubatypes "github.com/Arubacloud/sdk-go/pkg/types"
)

var _ = Describe("SetPhaseAndCondition", func() {
	var (
		ctx  context.Context
		proj *v1alpha1.Project
	)

	BeforeEach(func() {
		ctx = context.Background()
		proj = &v1alpha1.Project{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-set-phase",
				Namespace: "default",
			},
			Spec: v1alpha1.ProjectSpec{
				Tenant: "test-tenant",
			},
		}
		Expect(k8sClient.Create(ctx, proj)).To(Succeed())
	})

	AfterEach(func() {
		p := &v1alpha1.Project{}
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), p); err == nil {
			_ = k8sClient.Delete(ctx, p)
		}
	})

	It("sets the correct phase and condition", func() {
		err := SetPhaseAndCondition(k8sClient, ctx, proj, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, nil)
		Expect(err).To(Succeed())

		updated := &v1alpha1.Project{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())

		Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
		cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
	})

	It("sets previous conditions to Status=False", func() {
		// First set one condition
		Expect(SetPhaseAndCondition(k8sClient, ctx, proj, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, nil)).To(Succeed())

		// Re-fetch proj for the next call
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), proj)).To(Succeed())

		// Now transition to a different phase
		Expect(SetPhaseAndCondition(k8sClient, ctx, proj, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize, nil)).To(Succeed())

		updated := &v1alpha1.Project{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())

		// Old condition should be False
		oldCond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
		if oldCond != nil {
			Expect(oldCond.Status).To(Equal(metav1.ConditionFalse))
		}
		// New condition should be True
		newCond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseUpdating))
		Expect(newCond).NotTo(BeNil())
		Expect(newCond.Status).To(Equal(metav1.ConditionTrue))
	})

	It("includes ' - OK' in message when actionErr is nil", func() {
		Expect(SetPhaseAndCondition(k8sClient, ctx, proj, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, nil)).To(Succeed())

		updated := &v1alpha1.Project{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
		cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
		Expect(cond.Message).To(ContainSubstring("OK"))
	})

	It("includes ' - ERROR' in message when actionErr is non-nil", func() {
		testErr := errors.New("cmp failed")
		Expect(SetPhaseAndCondition(k8sClient, ctx, proj, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, testErr)).To(Succeed())

		updated := &v1alpha1.Project{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
		cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
		Expect(cond.Message).To(ContainSubstring("ERROR"))
		Expect(cond.Message).To(ContainSubstring("cmp failed"))
	})

	It("applies prePatch callbacks before writing", func() {
		prePatchCalled := false
		err := SetPhaseAndCondition(k8sClient, ctx, proj, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, nil,
			func(p *v1alpha1.Project) {
				prePatchCalled = true
				p.Spec.Description = "patched"
			},
		)
		Expect(err).To(Succeed())
		Expect(prePatchCalled).To(BeTrue())
	})
})

var _ = Describe("SetActiveAndSetID", func() {
	var (
		ctx  context.Context
		proj *v1alpha1.Project
	)

	BeforeEach(func() {
		ctx = context.Background()
		proj = &v1alpha1.Project{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-set-active",
				Namespace: "default",
			},
			Spec: v1alpha1.ProjectSpec{
				Tenant: "test-tenant",
			},
		}
		Expect(k8sClient.Create(ctx, proj)).To(Succeed())
	})

	AfterEach(func() {
		p := &v1alpha1.Project{}
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), p); err == nil {
			_ = k8sClient.Delete(ctx, p)
		}
	})

	It("sets phase to Active and condition to Active+Synchronized", func() {
		Expect(SetActiveAndSetID(k8sClient, ctx, proj, "cmp-id-1", nil)).To(Succeed())

		updated := &v1alpha1.Project{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseActive))

		cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseActive))
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronized))
	})

	It("sets ResourceID from cmpResourceID when empty", func() {
		Expect(SetActiveAndSetID(k8sClient, ctx, proj, "cmp-id-1", nil)).To(Succeed())

		updated := &v1alpha1.Project{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
		Expect(updated.Status.ResourceID).To(Equal("cmp-id-1"))
	})

	It("does NOT overwrite ResourceID when already set", func() {
		// Manually set an existing ResourceID
		p := proj.DeepCopy()
		p.Status.ResourceID = "existing-id"
		Expect(k8sClient.Status().Update(ctx, p)).To(Succeed())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), proj)).To(Succeed())

		Expect(SetActiveAndSetID(k8sClient, ctx, proj, "new-id", nil)).To(Succeed())

		updated := &v1alpha1.Project{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
		Expect(updated.Status.ResourceID).To(Equal("existing-id"))
	})

	It("stamps ObservedGeneration to Generation", func() {
		Expect(SetActiveAndSetID(k8sClient, ctx, proj, "cmp-id-1", nil)).To(Succeed())

		updated := &v1alpha1.Project{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
		Expect(updated.Status.ObservedGeneration).To(Equal(updated.Generation))
	})
})

var _ = Describe("SetFailedOnTimeout", func() {
	var (
		ctx  context.Context
		proj *v1alpha1.Project
	)

	BeforeEach(func() {
		ctx = context.Background()
		proj = &v1alpha1.Project{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-set-failed",
				Namespace: "default",
			},
			Spec: v1alpha1.ProjectSpec{
				Tenant: "test-tenant",
			},
		}
		Expect(k8sClient.Create(ctx, proj)).To(Succeed())

		// Put it in Creating+ShallSynchronize first
		Expect(SetPhaseAndCondition(k8sClient, ctx, proj, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, nil)).To(Succeed())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), proj)).To(Succeed())
	})

	AfterEach(func() {
		p := &v1alpha1.Project{}
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), p); err == nil {
			_ = k8sClient.Delete(ctx, p)
		}
	})

	It("sets phase to Failed", func() {
		Expect(SetFailedOnTimeout(k8sClient, ctx, proj)).To(Succeed())

		updated := &v1alpha1.Project{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
	})

	It("sets previous phase condition to Status=False, Reason=Failed", func() {
		Expect(SetFailedOnTimeout(k8sClient, ctx, proj)).To(Succeed())

		updated := &v1alpha1.Project{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())

		prevCond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
		Expect(prevCond).NotTo(BeNil())
		Expect(prevCond.Status).To(Equal(metav1.ConditionFalse))
		Expect(prevCond.Reason).To(Equal(v1alpha1.ConditionReasonFailed))
	})

	It("sets Failed condition to Status=True, Reason=Failed", func() {
		Expect(SetFailedOnTimeout(k8sClient, ctx, proj)).To(Succeed())

		updated := &v1alpha1.Project{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())

		failedCond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseFailed))
		Expect(failedCond).NotTo(BeNil())
		Expect(failedCond.Status).To(Equal(metav1.ConditionTrue))
		Expect(failedCond.Reason).To(Equal(v1alpha1.ConditionReasonFailed))
	})

	It("message references timeout", func() {
		Expect(SetFailedOnTimeout(k8sClient, ctx, proj)).To(Succeed())

		updated := &v1alpha1.Project{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())

		failedCond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseFailed))
		Expect(strings.ToLower(failedCond.Message)).To(ContainSubstring("timeout"))
	})
})

var _ = Describe("TagsAreEqual", func() {
	It("returns true for same order", func() {
		Expect(TagsAreEqual([]string{"a", "b", "c"}, []string{"a", "b", "c"})).To(BeTrue())
	})

	It("returns true for different order", func() {
		Expect(TagsAreEqual([]string{"c", "a", "b"}, []string{"a", "b", "c"})).To(BeTrue())
	})

	It("returns false for different tags", func() {
		Expect(TagsAreEqual([]string{"a", "b"}, []string{"a", "c"})).To(BeFalse())
	})

	It("returns true for both empty", func() {
		Expect(TagsAreEqual([]string{}, []string{})).To(BeTrue())
	})

	It("returns true for both nil", func() {
		Expect(TagsAreEqual(nil, nil)).To(BeTrue())
	})

	It("returns false for different lengths", func() {
		Expect(TagsAreEqual([]string{"a", "b"}, []string{"a"})).To(BeFalse())
	})
})

var _ = Describe("KubeDeleteFromPending", func() {
	var (
		ctx  context.Context
		proj *v1alpha1.Project
	)

	BeforeEach(func() {
		ctx = context.Background()
		proj = &v1alpha1.Project{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-delete-from-pending",
				Namespace: "default",
			},
			Spec: v1alpha1.ProjectSpec{
				Tenant: "test-tenant",
			},
		}
		Expect(k8sClient.Create(ctx, proj)).To(Succeed())

		// Set Pending status
		Expect(SetPhaseAndCondition(k8sClient, ctx, proj, v1alpha1.ResourcePhasePending, v1alpha1.ConditionReasonSynchronized, nil)).To(Succeed())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), proj)).To(Succeed())
	})

	AfterEach(func() {
		p := &v1alpha1.Project{}
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), p); err == nil {
			_ = k8sClient.Delete(ctx, p)
		}
	})

	It("sets phase to Deleted and flips Pending condition to Synchronized/False", func() {
		action := KubeDeleteFromPending[*v1alpha1.Project, *arubatypes.ProjectResponse](k8sClient)
		Expect(action(ctx, proj, nil)).To(Succeed())

		updated := &v1alpha1.Project{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleted))

		deletedCond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleted))
		Expect(deletedCond).NotTo(BeNil())
		Expect(deletedCond.Status).To(Equal(metav1.ConditionTrue))
		Expect(deletedCond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronized))

		pendingCond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhasePending))
		Expect(pendingCond).NotTo(BeNil())
		Expect(pendingCond.Status).To(Equal(metav1.ConditionFalse))
		Expect(pendingCond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronized))
	})
})

// findCondition is a test helper.
func findCondition(conditions []metav1.Condition, condType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}
