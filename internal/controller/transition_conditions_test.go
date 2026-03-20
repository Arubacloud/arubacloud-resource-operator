package controller

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
	arubatypes "github.com/Arubacloud/sdk-go/pkg/types"
)

// projectOpt is a functional option for building test Project objects.
type projectOpt func(*v1alpha1.Project)

func withPhase(p v1alpha1.ResourcePhase) projectOpt {
	return func(proj *v1alpha1.Project) {
		proj.Status.Phase = p
	}
}

func withResourceID(id string) projectOpt {
	return func(proj *v1alpha1.Project) {
		proj.Status.ResourceID = id
	}
}

func withDeleting() projectOpt {
	now := metav1.Now()
	return func(proj *v1alpha1.Project) {
		proj.DeletionTimestamp = &now
	}
}

func withGeneration(gen int64) projectOpt {
	return func(proj *v1alpha1.Project) {
		proj.Generation = gen
	}
}

func withObservedGeneration(gen int64) projectOpt {
	return func(proj *v1alpha1.Project) {
		proj.Status.ObservedGeneration = gen
	}
}

func withCondition(condType v1alpha1.ResourcePhase, status metav1.ConditionStatus, reason string, lastTransition time.Time) projectOpt {
	return func(proj *v1alpha1.Project) {
		proj.Status.Conditions = append(proj.Status.Conditions, metav1.Condition{
			Type:               string(condType),
			Status:             status,
			Reason:             reason,
			LastTransitionTime: metav1.NewTime(lastTransition),
		})
	}
}

func newTestProject(opts ...projectOpt) *v1alpha1.Project {
	proj := &v1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-project",
			Namespace: "default",
		},
	}
	for _, opt := range opts {
		opt(proj)
	}
	return proj
}

var _ = Describe("Transition Conditions", func() {
	// nil aruba resource used throughout (conditions only check kube state)
	var nilCMP *arubatypes.ProjectResponse

	Describe("failedPhase", func() {
		It("returns the phase whose condition has Status=False and Reason=Failed", func() {
			rs := &v1alpha1.ResourceStatus{
				Conditions: []metav1.Condition{
					{Type: "Creating", Status: metav1.ConditionFalse, Reason: v1alpha1.ConditionReasonFailed},
				},
			}
			Expect(failedPhase(rs)).To(Equal(v1alpha1.ResourcePhaseCreating))
		})

		It("returns empty string when no condition matches", func() {
			rs := &v1alpha1.ResourceStatus{
				Conditions: []metav1.Condition{
					{Type: "Creating", Status: metav1.ConditionTrue, Reason: v1alpha1.ConditionReasonFailed},
					{Type: "Updating", Status: metav1.ConditionFalse, Reason: v1alpha1.ConditionReasonSynchronized},
				},
			}
			Expect(failedPhase(rs)).To(Equal(v1alpha1.ResourcePhase("")))
		})

		It("returns empty string for empty conditions", func() {
			rs := &v1alpha1.ResourceStatus{}
			Expect(failedPhase(rs)).To(Equal(v1alpha1.ResourcePhase("")))
		})
	})

	Describe("kubeHasPhaseAndReason", func() {
		It("returns true when phase, reason, and deleting state all match (deleting=true)", func() {
			proj := newTestProject(
				withDeleting(),
				withPhase(v1alpha1.ResourcePhaseDeleting),
				withCondition(v1alpha1.ResourcePhaseDeleting, metav1.ConditionTrue, v1alpha1.ConditionReasonShallSynchronize, time.Now()),
			)
			Expect(kubeHasPhaseAndReason(proj, true, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize)).To(BeTrue())
		})

		It("returns true when phase, reason, and deleting state all match (deleting=false)", func() {
			proj := newTestProject(
				withPhase(v1alpha1.ResourcePhaseCreating),
				withCondition(v1alpha1.ResourcePhaseCreating, metav1.ConditionTrue, v1alpha1.ConditionReasonShallSynchronize, time.Now()),
			)
			Expect(kubeHasPhaseAndReason(proj, false, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize)).To(BeTrue())
		})

		It("returns false when phase does not match", func() {
			proj := newTestProject(
				withPhase(v1alpha1.ResourcePhaseCreating),
				withCondition(v1alpha1.ResourcePhaseCreating, metav1.ConditionTrue, v1alpha1.ConditionReasonShallSynchronize, time.Now()),
			)
			Expect(kubeHasPhaseAndReason(proj, false, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize)).To(BeFalse())
		})

		It("returns false when reason does not match", func() {
			proj := newTestProject(
				withPhase(v1alpha1.ResourcePhaseCreating),
				withCondition(v1alpha1.ResourcePhaseCreating, metav1.ConditionTrue, v1alpha1.ConditionReasonSynchronizing, time.Now()),
			)
			Expect(kubeHasPhaseAndReason(proj, false, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize)).To(BeFalse())
		})

		It("returns false when deleting state does not match (expected deleting but not deleting)", func() {
			proj := newTestProject(
				withPhase(v1alpha1.ResourcePhaseDeleting),
				withCondition(v1alpha1.ResourcePhaseDeleting, metav1.ConditionTrue, v1alpha1.ConditionReasonShallSynchronize, time.Now()),
			)
			Expect(kubeHasPhaseAndReason(proj, true, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize)).To(BeFalse())
		})

		It("returns false when deleting state does not match (expected not deleting but is deleting)", func() {
			proj := newTestProject(
				withDeleting(),
				withPhase(v1alpha1.ResourcePhaseCreating),
				withCondition(v1alpha1.ResourcePhaseCreating, metav1.ConditionTrue, v1alpha1.ConditionReasonShallSynchronize, time.Now()),
			)
			Expect(kubeHasPhaseAndReason(proj, false, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize)).To(BeFalse())
		})

		It("returns false when status nil", func() {
			proj := &v1alpha1.Project{}
			proj.Status.Phase = ""
			Expect(kubeHasPhaseAndReason(proj, false, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize)).To(BeFalse())
		})
	})

	Describe("kubePhaseTimedOut", func() {
		It("returns true when transitory phase, ShallSynchronize, and time exceeded", func() {
			proj := newTestProject(
				withPhase(v1alpha1.ResourcePhaseCreating),
				withCondition(v1alpha1.ResourcePhaseCreating, metav1.ConditionTrue, v1alpha1.ConditionReasonShallSynchronize,
					time.Now().Add(-(reconciler.MaxPhaseTimeout+time.Minute))),
			)
			Expect(kubePhaseTimedOut[*v1alpha1.Project, *arubatypes.ProjectResponse](proj, nilCMP)).To(BeTrue())
		})

		It("returns true when transitory phase, Synchronizing, and time exceeded", func() {
			proj := newTestProject(
				withPhase(v1alpha1.ResourcePhaseCreating),
				withCondition(v1alpha1.ResourcePhaseCreating, metav1.ConditionTrue, v1alpha1.ConditionReasonSynchronizing,
					time.Now().Add(-(reconciler.MaxPhaseTimeout+time.Minute))),
			)
			Expect(kubePhaseTimedOut[*v1alpha1.Project, *arubatypes.ProjectResponse](proj, nilCMP)).To(BeTrue())
		})

		It("returns false when condition is fresh (not timed out)", func() {
			proj := newTestProject(
				withPhase(v1alpha1.ResourcePhaseCreating),
				withCondition(v1alpha1.ResourcePhaseCreating, metav1.ConditionTrue, v1alpha1.ConditionReasonShallSynchronize, time.Now()),
			)
			Expect(kubePhaseTimedOut[*v1alpha1.Project, *arubatypes.ProjectResponse](proj, nilCMP)).To(BeFalse())
		})

		It("returns false when phase is final (Active)", func() {
			proj := newTestProject(
				withPhase(v1alpha1.ResourcePhaseActive),
				withCondition(v1alpha1.ResourcePhaseActive, metav1.ConditionTrue, v1alpha1.ConditionReasonSynchronized,
					time.Now().Add(-(reconciler.MaxPhaseTimeout+time.Minute))),
			)
			Expect(kubePhaseTimedOut[*v1alpha1.Project, *arubatypes.ProjectResponse](proj, nilCMP)).To(BeFalse())
		})

		It("returns false when condition reason is Synchronized (not a polling reason)", func() {
			proj := newTestProject(
				withPhase(v1alpha1.ResourcePhaseCreating),
				withCondition(v1alpha1.ResourcePhaseCreating, metav1.ConditionTrue, v1alpha1.ConditionReasonSynchronized,
					time.Now().Add(-(reconciler.MaxPhaseTimeout+time.Minute))),
			)
			Expect(kubePhaseTimedOut[*v1alpha1.Project, *arubatypes.ProjectResponse](proj, nilCMP)).To(BeFalse())
		})

		It("returns false when no status", func() {
			proj := &v1alpha1.Project{}
			Expect(kubePhaseTimedOut[*v1alpha1.Project, *arubatypes.ProjectResponse](proj, nilCMP)).To(BeFalse())
		})
	})

	Describe("kubeShouldDelete", func() {
		It("returns true when deleting, final phase (Active), and Synchronized", func() {
			proj := newTestProject(
				withDeleting(),
				withPhase(v1alpha1.ResourcePhaseActive),
				withCondition(v1alpha1.ResourcePhaseActive, metav1.ConditionTrue, v1alpha1.ConditionReasonSynchronized, time.Now()),
			)
			Expect(kubeShouldDelete[*v1alpha1.Project, *arubatypes.ProjectResponse](proj, nilCMP)).To(BeTrue())
		})

		It("returns false when not deleting", func() {
			proj := newTestProject(
				withPhase(v1alpha1.ResourcePhaseActive),
				withCondition(v1alpha1.ResourcePhaseActive, metav1.ConditionTrue, v1alpha1.ConditionReasonSynchronized, time.Now()),
			)
			Expect(kubeShouldDelete[*v1alpha1.Project, *arubatypes.ProjectResponse](proj, nilCMP)).To(BeFalse())
		})

		It("returns false when phase is Deleted", func() {
			proj := newTestProject(
				withDeleting(),
				withPhase(v1alpha1.ResourcePhaseDeleted),
				withCondition(v1alpha1.ResourcePhaseDeleted, metav1.ConditionTrue, v1alpha1.ConditionReasonSynchronized, time.Now()),
			)
			Expect(kubeShouldDelete[*v1alpha1.Project, *arubatypes.ProjectResponse](proj, nilCMP)).To(BeFalse())
		})

		It("returns false when phase is transitory (Creating)", func() {
			proj := newTestProject(
				withDeleting(),
				withPhase(v1alpha1.ResourcePhaseCreating),
				withCondition(v1alpha1.ResourcePhaseCreating, metav1.ConditionTrue, v1alpha1.ConditionReasonSynchronized, time.Now()),
			)
			Expect(kubeShouldDelete[*v1alpha1.Project, *arubatypes.ProjectResponse](proj, nilCMP)).To(BeFalse())
		})

		It("returns false when reason is not Synchronized", func() {
			proj := newTestProject(
				withDeleting(),
				withPhase(v1alpha1.ResourcePhaseActive),
				withCondition(v1alpha1.ResourcePhaseActive, metav1.ConditionTrue, v1alpha1.ConditionReasonShallSynchronize, time.Now()),
			)
			Expect(kubeShouldDelete[*v1alpha1.Project, *arubatypes.ProjectResponse](proj, nilCMP)).To(BeFalse())
		})
	})

	Describe("kubeShouldDeleteTimedOut", func() {
		It("returns true when deleting, Failed phase, Failed condition True+Failed, prev phase != Deleting", func() {
			proj := newTestProject(
				withDeleting(),
				withPhase(v1alpha1.ResourcePhaseFailed),
				withCondition(v1alpha1.ResourcePhaseCreating, metav1.ConditionFalse, v1alpha1.ConditionReasonFailed, time.Now()),
				withCondition(v1alpha1.ResourcePhaseFailed, metav1.ConditionTrue, v1alpha1.ConditionReasonFailed, time.Now()),
			)
			Expect(kubeShouldDeleteTimedOut[*v1alpha1.Project, *arubatypes.ProjectResponse](proj, nilCMP)).To(BeTrue())
		})

		It("returns false when not deleting", func() {
			proj := newTestProject(
				withPhase(v1alpha1.ResourcePhaseFailed),
				withCondition(v1alpha1.ResourcePhaseCreating, metav1.ConditionFalse, v1alpha1.ConditionReasonFailed, time.Now()),
				withCondition(v1alpha1.ResourcePhaseFailed, metav1.ConditionTrue, v1alpha1.ConditionReasonFailed, time.Now()),
			)
			Expect(kubeShouldDeleteTimedOut[*v1alpha1.Project, *arubatypes.ProjectResponse](proj, nilCMP)).To(BeFalse())
		})

		It("returns false when prev phase was Deleting", func() {
			proj := newTestProject(
				withDeleting(),
				withPhase(v1alpha1.ResourcePhaseFailed),
				withCondition(v1alpha1.ResourcePhaseDeleting, metav1.ConditionFalse, v1alpha1.ConditionReasonFailed, time.Now()),
				withCondition(v1alpha1.ResourcePhaseFailed, metav1.ConditionTrue, v1alpha1.ConditionReasonFailed, time.Now()),
			)
			Expect(kubeShouldDeleteTimedOut[*v1alpha1.Project, *arubatypes.ProjectResponse](proj, nilCMP)).To(BeFalse())
		})

		It("returns false when Failed condition has Reason=Synchronized (CMP failure, not timeout)", func() {
			proj := newTestProject(
				withDeleting(),
				withPhase(v1alpha1.ResourcePhaseFailed),
				withCondition(v1alpha1.ResourcePhaseFailed, metav1.ConditionTrue, v1alpha1.ConditionReasonSynchronized, time.Now()),
			)
			Expect(kubeShouldDeleteTimedOut[*v1alpha1.Project, *arubatypes.ProjectResponse](proj, nilCMP)).To(BeFalse())
		})
	})

	Describe("kubeShouldBeDeletedOnCMP", func() {
		It("returns true when deleting, Phase=Deleting, Reason=ShallSynchronize", func() {
			proj := newTestProject(
				withDeleting(),
				withPhase(v1alpha1.ResourcePhaseDeleting),
				withCondition(v1alpha1.ResourcePhaseDeleting, metav1.ConditionTrue, v1alpha1.ConditionReasonShallSynchronize, time.Now()),
			)
			Expect(kubeShouldBeDeletedOnCMP[*v1alpha1.Project, *arubatypes.ProjectResponse](proj, nilCMP)).To(BeTrue())
		})

		It("returns false when not deleting", func() {
			proj := newTestProject(
				withPhase(v1alpha1.ResourcePhaseDeleting),
				withCondition(v1alpha1.ResourcePhaseDeleting, metav1.ConditionTrue, v1alpha1.ConditionReasonShallSynchronize, time.Now()),
			)
			Expect(kubeShouldBeDeletedOnCMP[*v1alpha1.Project, *arubatypes.ProjectResponse](proj, nilCMP)).To(BeFalse())
		})

		It("returns false when wrong reason", func() {
			proj := newTestProject(
				withDeleting(),
				withPhase(v1alpha1.ResourcePhaseDeleting),
				withCondition(v1alpha1.ResourcePhaseDeleting, metav1.ConditionTrue, v1alpha1.ConditionReasonSynchronizing, time.Now()),
			)
			Expect(kubeShouldBeDeletedOnCMP[*v1alpha1.Project, *arubatypes.ProjectResponse](proj, nilCMP)).To(BeFalse())
		})
	})

	Describe("kubeWaitingDeletionOnCMP", func() {
		It("returns true when deleting, Phase=Deleting, Reason=Synchronizing", func() {
			proj := newTestProject(
				withDeleting(),
				withPhase(v1alpha1.ResourcePhaseDeleting),
				withCondition(v1alpha1.ResourcePhaseDeleting, metav1.ConditionTrue, v1alpha1.ConditionReasonSynchronizing, time.Now()),
			)
			Expect(kubeWaitingDeletionOnCMP[*v1alpha1.Project, *arubatypes.ProjectResponse](proj, nilCMP)).To(BeTrue())
		})
	})

	Describe("kubeDeletionAccomplished", func() {
		It("returns true when deleting, Phase=Deleting, Reason=Synchronized", func() {
			proj := newTestProject(
				withDeleting(),
				withPhase(v1alpha1.ResourcePhaseDeleting),
				withCondition(v1alpha1.ResourcePhaseDeleting, metav1.ConditionTrue, v1alpha1.ConditionReasonSynchronized, time.Now()),
			)
			Expect(kubeDeletionAccomplished[*v1alpha1.Project, *arubatypes.ProjectResponse](proj, nilCMP)).To(BeTrue())
		})
	})

	Describe("kubeIsFirstReconciliation", func() {
		It("returns true when not deleting, no ResourceID, no Phase, no Conditions", func() {
			proj := newTestProject()
			Expect(kubeIsFirstReconciliation[*v1alpha1.Project, *arubatypes.ProjectResponse](proj, nilCMP)).To(BeTrue())
		})

		It("returns false when has ResourceID", func() {
			proj := newTestProject(withResourceID("some-id"))
			Expect(kubeIsFirstReconciliation[*v1alpha1.Project, *arubatypes.ProjectResponse](proj, nilCMP)).To(BeFalse())
		})

		It("returns false when has conditions", func() {
			proj := newTestProject(
				withCondition(v1alpha1.ResourcePhaseCreating, metav1.ConditionTrue, v1alpha1.ConditionReasonShallSynchronize, time.Now()),
			)
			Expect(kubeIsFirstReconciliation[*v1alpha1.Project, *arubatypes.ProjectResponse](proj, nilCMP)).To(BeFalse())
		})

		It("returns false when is deleting", func() {
			proj := newTestProject(withDeleting())
			Expect(kubeIsFirstReconciliation[*v1alpha1.Project, *arubatypes.ProjectResponse](proj, nilCMP)).To(BeFalse())
		})

		It("returns false when has Phase", func() {
			proj := newTestProject(withPhase(v1alpha1.ResourcePhaseCreating))
			Expect(kubeIsFirstReconciliation[*v1alpha1.Project, *arubatypes.ProjectResponse](proj, nilCMP)).To(BeFalse())
		})
	})

	Describe("kubeShouldBeCreatedOnCMP", func() {
		It("returns true when not deleting, no ResourceID, Phase=Creating, Reason=ShallSynchronize", func() {
			proj := newTestProject(
				withPhase(v1alpha1.ResourcePhaseCreating),
				withCondition(v1alpha1.ResourcePhaseCreating, metav1.ConditionTrue, v1alpha1.ConditionReasonShallSynchronize, time.Now()),
			)
			Expect(kubeShouldBeCreatedOnCMP[*v1alpha1.Project, *arubatypes.ProjectResponse](proj, nilCMP)).To(BeTrue())
		})

		It("returns false when ResourceID is set", func() {
			proj := newTestProject(
				withPhase(v1alpha1.ResourcePhaseCreating),
				withResourceID("some-id"),
				withCondition(v1alpha1.ResourcePhaseCreating, metav1.ConditionTrue, v1alpha1.ConditionReasonShallSynchronize, time.Now()),
			)
			Expect(kubeShouldBeCreatedOnCMP[*v1alpha1.Project, *arubatypes.ProjectResponse](proj, nilCMP)).To(BeFalse())
		})
	})

	Describe("kubeWaitingCreationInCMP", func() {
		It("returns true when not deleting, no ResourceID, Phase=Creating, Reason=Synchronizing", func() {
			proj := newTestProject(
				withPhase(v1alpha1.ResourcePhaseCreating),
				withCondition(v1alpha1.ResourcePhaseCreating, metav1.ConditionTrue, v1alpha1.ConditionReasonSynchronizing, time.Now()),
			)
			Expect(kubeWaitingCreationInCMP[*v1alpha1.Project, *arubatypes.ProjectResponse](proj, nilCMP)).To(BeTrue())
		})
	})

	Describe("kubeIsCreatedOnCMP", func() {
		It("returns true when not deleting, no ResourceID, Phase=Creating, Reason=Synchronized", func() {
			proj := newTestProject(
				withPhase(v1alpha1.ResourcePhaseCreating),
				withCondition(v1alpha1.ResourcePhaseCreating, metav1.ConditionTrue, v1alpha1.ConditionReasonSynchronized, time.Now()),
			)
			Expect(kubeIsCreatedOnCMP[*v1alpha1.Project, *arubatypes.ProjectResponse](proj, nilCMP)).To(BeTrue())
		})
	})

	Describe("kubeShouldBeUpdatedOnCMP", func() {
		It("returns true when not deleting, Phase=Updating, Reason=ShallSynchronize", func() {
			proj := newTestProject(
				withPhase(v1alpha1.ResourcePhaseUpdating),
				withCondition(v1alpha1.ResourcePhaseUpdating, metav1.ConditionTrue, v1alpha1.ConditionReasonShallSynchronize, time.Now()),
			)
			Expect(kubeShouldBeUpdatedOnCMP[*v1alpha1.Project, *arubatypes.ProjectResponse](proj, nilCMP)).To(BeTrue())
		})
	})

	Describe("kubeWaitingUpdateOnCMP", func() {
		It("returns true when not deleting, Phase=Updating, Reason=Synchronizing", func() {
			proj := newTestProject(
				withPhase(v1alpha1.ResourcePhaseUpdating),
				withCondition(v1alpha1.ResourcePhaseUpdating, metav1.ConditionTrue, v1alpha1.ConditionReasonSynchronizing, time.Now()),
			)
			Expect(kubeWaitingUpdateOnCMP[*v1alpha1.Project, *arubatypes.ProjectResponse](proj, nilCMP)).To(BeTrue())
		})
	})

	Describe("kubeUpdateAccomplished", func() {
		It("returns true when not deleting, Phase=Updating, Reason=Synchronized", func() {
			proj := newTestProject(
				withPhase(v1alpha1.ResourcePhaseUpdating),
				withCondition(v1alpha1.ResourcePhaseUpdating, metav1.ConditionTrue, v1alpha1.ConditionReasonSynchronized, time.Now()),
			)
			Expect(kubeUpdateAccomplished[*v1alpha1.Project, *arubatypes.ProjectResponse](proj, nilCMP)).To(BeTrue())
		})
	})

	Describe("kubeActiveAndGenerationChanged", func() {
		It("returns true when Active, ResourceID set, gen != observedGen, condition True+Synchronized", func() {
			proj := newTestProject(
				withPhase(v1alpha1.ResourcePhaseActive),
				withResourceID("some-id"),
				withGeneration(2),
				withObservedGeneration(1),
				withCondition(v1alpha1.ResourcePhaseActive, metav1.ConditionTrue, v1alpha1.ConditionReasonSynchronized, time.Now()),
			)
			Expect(kubeActiveAndGenerationChanged[*v1alpha1.Project, *arubatypes.ProjectResponse](proj, nilCMP)).To(BeTrue())
		})

		It("returns false when same generation", func() {
			proj := newTestProject(
				withPhase(v1alpha1.ResourcePhaseActive),
				withResourceID("some-id"),
				withGeneration(1),
				withObservedGeneration(1),
				withCondition(v1alpha1.ResourcePhaseActive, metav1.ConditionTrue, v1alpha1.ConditionReasonSynchronized, time.Now()),
			)
			Expect(kubeActiveAndGenerationChanged[*v1alpha1.Project, *arubatypes.ProjectResponse](proj, nilCMP)).To(BeFalse())
		})

		It("returns false when no ResourceID", func() {
			proj := newTestProject(
				withPhase(v1alpha1.ResourcePhaseActive),
				withGeneration(2),
				withObservedGeneration(1),
				withCondition(v1alpha1.ResourcePhaseActive, metav1.ConditionTrue, v1alpha1.ConditionReasonSynchronized, time.Now()),
			)
			Expect(kubeActiveAndGenerationChanged[*v1alpha1.Project, *arubatypes.ProjectResponse](proj, nilCMP)).To(BeFalse())
		})

		It("returns false when phase is not Active", func() {
			proj := newTestProject(
				withPhase(v1alpha1.ResourcePhaseUpdating),
				withResourceID("some-id"),
				withGeneration(2),
				withObservedGeneration(1),
				withCondition(v1alpha1.ResourcePhaseActive, metav1.ConditionTrue, v1alpha1.ConditionReasonSynchronized, time.Now()),
			)
			Expect(kubeActiveAndGenerationChanged[*v1alpha1.Project, *arubatypes.ProjectResponse](proj, nilCMP)).To(BeFalse())
		})

		It("returns false when is deleting", func() {
			proj := newTestProject(
				withDeleting(),
				withPhase(v1alpha1.ResourcePhaseActive),
				withResourceID("some-id"),
				withGeneration(2),
				withObservedGeneration(1),
				withCondition(v1alpha1.ResourcePhaseActive, metav1.ConditionTrue, v1alpha1.ConditionReasonSynchronized, time.Now()),
			)
			Expect(kubeActiveAndGenerationChanged[*v1alpha1.Project, *arubatypes.ProjectResponse](proj, nilCMP)).To(BeFalse())
		})
	})
})
