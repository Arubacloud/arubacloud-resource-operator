package reconciler

import (
	"context"
	"errors"

	"github.com/prometheus/client_golang/prometheus/testutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/Arubacloud/sdk-go/pkg/aruba"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type testProject = *v1alpha1.Project
type testCMP = *aruba.Project

func newMinimalProject() testProject {
	return &v1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: "t", Namespace: "default"},
	}
}

var _ = Describe("AbstractTransition", func() {
	var (
		ctx    = context.Background()
		proj   testProject
		nilCMP testCMP
	)

	BeforeEach(func() {
		proj = newMinimalProject()
		nilCMP = nil
	})

	Describe("condition()", func() {
		It("returns true when both KCondition and ACondition are true", func() {
			t := &AbstractTransition[testProject, testCMP]{
				Name:           "both-true",
				KCondition:     func(_ testProject, _ testCMP) bool { return true },
				ACondition:     func(_ testProject, _ testCMP) bool { return true },
				Requeue:        NoRequeue[testProject, testCMP],
				RequeueOnError: NoRequeueAndPropagateError[testProject, testCMP],
			}
			Expect(t.condition(proj, nilCMP)).To(BeTrue())
		})

		It("returns false when KCondition is false", func() {
			t := &AbstractTransition[testProject, testCMP]{
				Name:           "k-false",
				KCondition:     func(_ testProject, _ testCMP) bool { return false },
				ACondition:     func(_ testProject, _ testCMP) bool { return true },
				Requeue:        NoRequeue[testProject, testCMP],
				RequeueOnError: NoRequeueAndPropagateError[testProject, testCMP],
			}
			Expect(t.condition(proj, nilCMP)).To(BeFalse())
		})

		It("returns false when ACondition is false", func() {
			t := &AbstractTransition[testProject, testCMP]{
				Name:           "a-false",
				KCondition:     func(_ testProject, _ testCMP) bool { return true },
				ACondition:     func(_ testProject, _ testCMP) bool { return false },
				Requeue:        NoRequeue[testProject, testCMP],
				RequeueOnError: NoRequeueAndPropagateError[testProject, testCMP],
			}
			Expect(t.condition(proj, nilCMP)).To(BeFalse())
		})

		It("returns false when both conditions are false", func() {
			t := &AbstractTransition[testProject, testCMP]{
				Name:           "both-false",
				KCondition:     func(_ testProject, _ testCMP) bool { return false },
				ACondition:     func(_ testProject, _ testCMP) bool { return false },
				Requeue:        NoRequeue[testProject, testCMP],
				RequeueOnError: NoRequeueAndPropagateError[testProject, testCMP],
			}
			Expect(t.condition(proj, nilCMP)).To(BeFalse())
		})
	})

	Describe("action() priority", func() {
		It("runs only KAction when KAction is set", func() {
			kRan, aRan := false, false
			t := &AbstractTransition[testProject, testCMP]{
				Name:       "kaction-priority",
				KCondition: AlwaysTrue[testProject, testCMP],
				ACondition: AlwaysTrue[testProject, testCMP],
				KAction: func(_ context.Context, _ testProject, _ testCMP) error {
					kRan = true
					return nil
				},
				AAction: func(_ context.Context, _ testProject, _ testCMP) error {
					aRan = true
					return nil
				},
				KActionOnASuccess: func(_ context.Context, _ testProject, _ testCMP) error {
					return errors.New("should not be called")
				},
				Requeue:        NoRequeue[testProject, testCMP],
				RequeueOnError: NoRequeueAndPropagateError[testProject, testCMP],
			}
			Expect(t.action(ctx, proj, nilCMP)).To(Succeed())
			Expect(kRan).To(BeTrue())
			Expect(aRan).To(BeFalse())
		})

		It("runs AAction and KActionOnASuccess when KAction is nil and AAction succeeds", func() {
			aRan, successRan := false, false
			t := &AbstractTransition[testProject, testCMP]{
				Name:       "aaction-success",
				KCondition: AlwaysTrue[testProject, testCMP],
				ACondition: AlwaysTrue[testProject, testCMP],
				AAction: func(_ context.Context, _ testProject, _ testCMP) error {
					aRan = true
					return nil
				},
				KActionOnASuccess: func(_ context.Context, _ testProject, _ testCMP) error {
					successRan = true
					return nil
				},
				Requeue:        NoRequeue[testProject, testCMP],
				RequeueOnError: NoRequeueAndPropagateError[testProject, testCMP],
			}
			Expect(t.action(ctx, proj, nilCMP)).To(Succeed())
			Expect(aRan).To(BeTrue())
			Expect(successRan).To(BeTrue())
		})

		It("runs AAction and KActionOnAError when KAction is nil and AAction fails", func() {
			aRan, errorRan := false, false
			testErr := errors.New("aAction failed")
			t := &AbstractTransition[testProject, testCMP]{
				Name:       "aaction-error",
				KCondition: AlwaysTrue[testProject, testCMP],
				ACondition: AlwaysTrue[testProject, testCMP],
				AAction: func(_ context.Context, _ testProject, _ testCMP) error {
					aRan = true
					return testErr
				},
				KActionOnAError: func(_ context.Context, _ testProject, _ testCMP, err error) error {
					errorRan = true
					return err // propagate
				},
				Requeue:        NoRequeue[testProject, testCMP],
				RequeueOnError: NoRequeueAndPropagateError[testProject, testCMP],
			}
			err := t.action(ctx, proj, nilCMP)
			Expect(err).To(MatchError(testErr))
			Expect(aRan).To(BeTrue())
			Expect(errorRan).To(BeTrue())
		})

		It("returns nil when both KAction and AAction are nil", func() {
			t := &AbstractTransition[testProject, testCMP]{
				Name:           "no-actions",
				KCondition:     AlwaysTrue[testProject, testCMP],
				ACondition:     AlwaysTrue[testProject, testCMP],
				Requeue:        NoRequeue[testProject, testCMP],
				RequeueOnError: NoRequeueAndPropagateError[testProject, testCMP],
			}
			Expect(t.action(ctx, proj, nilCMP)).To(Succeed())
		})
	})
})

var _ = Describe("TransitionSet.Run()", func() {
	var ctx = context.Background()

	buildTS := func(transitions ...*AbstractTransition[testProject, testCMP]) *TransitionSet[testProject, testCMP] {
		ts := &TransitionSet[testProject, testCMP]{
			DefaultRequeue:        NoRequeue[testProject, testCMP],
			DefaultRequeueOnError: NoRequeueAndPropagateError[testProject, testCMP],
		}
		for _, t := range transitions {
			ts.Add(t)
		}
		return ts
	}

	It("executes the first matching transition and skips subsequent ones", func() {
		firstRan, secondRan := false, false
		ts := buildTS(
			&AbstractTransition[testProject, testCMP]{
				Name:       "first",
				KCondition: AlwaysTrue[testProject, testCMP],
				ACondition: AlwaysTrue[testProject, testCMP],
				KAction: func(_ context.Context, _ testProject, _ testCMP) error {
					firstRan = true
					return nil
				},
				Requeue:        NoRequeue[testProject, testCMP],
				RequeueOnError: NoRequeueAndPropagateError[testProject, testCMP],
			},
			&AbstractTransition[testProject, testCMP]{
				Name:       "second",
				KCondition: AlwaysTrue[testProject, testCMP],
				ACondition: AlwaysTrue[testProject, testCMP],
				KAction: func(_ context.Context, _ testProject, _ testCMP) error {
					secondRan = true
					return nil
				},
				Requeue:        NoRequeue[testProject, testCMP],
				RequeueOnError: NoRequeueAndPropagateError[testProject, testCMP],
			},
		)
		proj := newMinimalProject()
		result, err := ts.Run(ctx, proj, nil)
		Expect(err).To(Succeed())
		Expect(result).To(Equal(ctrl.Result{}))
		Expect(firstRan).To(BeTrue())
		Expect(secondRan).To(BeFalse())
	})

	It("executes the second transition when first doesn't match", func() {
		secondRan := false
		ts := buildTS(
			&AbstractTransition[testProject, testCMP]{
				Name:           "first-no-match",
				KCondition:     func(_ testProject, _ testCMP) bool { return false },
				ACondition:     AlwaysTrue[testProject, testCMP],
				Requeue:        NoRequeue[testProject, testCMP],
				RequeueOnError: NoRequeueAndPropagateError[testProject, testCMP],
			},
			&AbstractTransition[testProject, testCMP]{
				Name:       "second-matches",
				KCondition: AlwaysTrue[testProject, testCMP],
				ACondition: AlwaysTrue[testProject, testCMP],
				KAction: func(_ context.Context, _ testProject, _ testCMP) error {
					secondRan = true
					return nil
				},
				Requeue:        ShortRequeue[testProject, testCMP],
				RequeueOnError: NoRequeueAndPropagateError[testProject, testCMP],
			},
		)
		proj := newMinimalProject()
		result, err := ts.Run(ctx, proj, nil)
		Expect(err).To(Succeed())
		Expect(result.RequeueAfter).To(Equal(ShortRequeueAfter))
		Expect(secondRan).To(BeTrue())
	})

	It("executes DefaultKAction when no transition matches", func() {
		defaultRan := false
		ts := buildTS(
			&AbstractTransition[testProject, testCMP]{
				Name:           "no-match",
				KCondition:     func(_ testProject, _ testCMP) bool { return false },
				ACondition:     AlwaysTrue[testProject, testCMP],
				Requeue:        NoRequeue[testProject, testCMP],
				RequeueOnError: NoRequeueAndPropagateError[testProject, testCMP],
			},
		)
		ts.DefaultKAction = func(_ context.Context, _ testProject, _ testCMP) error {
			defaultRan = true
			return nil
		}
		proj := newMinimalProject()
		_, err := ts.Run(ctx, proj, nil)
		Expect(err).To(Succeed())
		Expect(defaultRan).To(BeTrue())
	})

	It("returns error from RequeueOnError when action errors", func() {
		testErr := errors.New("transition error")
		ts := buildTS(
			&AbstractTransition[testProject, testCMP]{
				Name:       "error-transition",
				KCondition: AlwaysTrue[testProject, testCMP],
				ACondition: AlwaysTrue[testProject, testCMP],
				KAction: func(_ context.Context, _ testProject, _ testCMP) error {
					return testErr
				},
				Requeue: NoRequeue[testProject, testCMP],
				RequeueOnError: func(_ testProject, _ testCMP, err error) (ctrl.Result, error) {
					return ctrl.Result{RequeueAfter: LongRequeueAfter}, nil
				},
			},
		)
		proj := newMinimalProject()
		result, err := ts.Run(ctx, proj, nil)
		Expect(err).To(Succeed()) // requeueOnError swallowed the error
		Expect(result.RequeueAfter).To(Equal(LongRequeueAfter))
	})
})

var _ = Describe("TransitionSet.Run() metrics", func() {
	var (
		ctx  = context.Background()
		proj testProject
	)

	buildMetricsTS := func(transitions ...*AbstractTransition[testProject, testCMP]) *TransitionSet[testProject, testCMP] {
		ts := &TransitionSet[testProject, testCMP]{
			DefaultRequeue:        NoRequeue[testProject, testCMP],
			DefaultRequeueOnError: NoRequeueAndPropagateError[testProject, testCMP],
		}
		for _, t := range transitions {
			ts.Add(t)
		}
		return ts
	}

	BeforeEach(func() {
		proj = &v1alpha1.Project{
			ObjectMeta: metav1.ObjectMeta{Name: "t", Namespace: "default"},
			Status: v1alpha1.ResourceStatus{
				Phase: v1alpha1.ResourcePhasePending,
				Conditions: []metav1.Condition{
					{
						Type:   string(v1alpha1.ResourcePhasePending),
						Status: metav1.ConditionTrue,
						Reason: v1alpha1.ConditionReasonSynchronized,
					},
				},
			},
		}
		TransitionActionDuration.Reset()
		TransitionUnmatchedTotal.Reset()
	})

	It("records one duration series with result=success on a successful transition", func() {
		ts := buildMetricsTS(&AbstractTransition[testProject, testCMP]{
			Name:           "test-transition",
			KCondition:     AlwaysTrue[testProject, testCMP],
			ACondition:     AlwaysTrue[testProject, testCMP],
			KAction:        NoAction[testProject, testCMP],
			Requeue:        NoRequeue[testProject, testCMP],
			RequeueOnError: NoRequeueAndPropagateError[testProject, testCMP],
		})

		_, err := ts.Run(ctx, proj, nil)
		Expect(err).To(Succeed())

		// One series should have been observed (result=success, transition_name=test-transition)
		Expect(testutil.CollectAndCount(TransitionActionDuration)).To(Equal(1))
	})

	It("records one duration series with result=error on a failed transition", func() {
		actionErr := errors.New("action failed")
		ts := buildMetricsTS(&AbstractTransition[testProject, testCMP]{
			Name:       "failing-transition",
			KCondition: AlwaysTrue[testProject, testCMP],
			ACondition: AlwaysTrue[testProject, testCMP],
			KAction: func(_ context.Context, _ testProject, _ testCMP) error {
				return actionErr
			},
			Requeue: NoRequeue[testProject, testCMP],
			RequeueOnError: func(_ testProject, _ testCMP, _ error) (ctrl.Result, error) {
				return ctrl.Result{}, nil // swallow so Run doesn't propagate the error
			},
		})

		_, err := ts.Run(ctx, proj, nil)
		Expect(err).To(Succeed())

		// One series should have been observed (result=error)
		Expect(testutil.CollectAndCount(TransitionActionDuration)).To(Equal(1))
	})

	It("increments the unmatched counter when no transition matches", func() {
		ts := buildMetricsTS(&AbstractTransition[testProject, testCMP]{
			Name:           "no-match",
			KCondition:     func(_ testProject, _ testCMP) bool { return false },
			ACondition:     AlwaysTrue[testProject, testCMP],
			Requeue:        NoRequeue[testProject, testCMP],
			RequeueOnError: NoRequeueAndPropagateError[testProject, testCMP],
		})

		_, err := ts.Run(ctx, proj, nil)
		Expect(err).To(Succeed())

		Expect(testutil.ToFloat64(TransitionUnmatchedTotal.WithLabelValues(
			"Project",
			string(v1alpha1.ResourcePhasePending),
			v1alpha1.ConditionReasonSynchronized,
		))).To(Equal(float64(1)))
		Expect(testutil.CollectAndCount(TransitionActionDuration)).To(Equal(0))
	})

	It("does not increment the unmatched counter when a transition matches", func() {
		ts := buildMetricsTS(&AbstractTransition[testProject, testCMP]{
			Name:           "matches",
			KCondition:     AlwaysTrue[testProject, testCMP],
			ACondition:     AlwaysTrue[testProject, testCMP],
			KAction:        NoAction[testProject, testCMP],
			Requeue:        NoRequeue[testProject, testCMP],
			RequeueOnError: NoRequeueAndPropagateError[testProject, testCMP],
		})

		_, err := ts.Run(ctx, proj, nil)
		Expect(err).To(Succeed())

		Expect(testutil.ToFloat64(TransitionUnmatchedTotal.WithLabelValues(
			"Project",
			string(v1alpha1.ResourcePhasePending),
			v1alpha1.ConditionReasonSynchronized,
		))).To(Equal(float64(0)))
	})
})

var _ = Describe("Requeue helpers", func() {
	var proj testProject
	var nilCMP testCMP

	BeforeEach(func() {
		proj = newMinimalProject()
		nilCMP = nil
	})

	It("ShortRequeue returns ShortRequeueAfter", func() {
		result := ShortRequeue(proj, nilCMP)
		Expect(result.RequeueAfter).To(Equal(ShortRequeueAfter))
	})

	It("LongRequeue returns LongRequeueAfter", func() {
		result := LongRequeue(proj, nilCMP)
		Expect(result.RequeueAfter).To(Equal(LongRequeueAfter))
	})

	It("NoRequeue returns empty Result", func() {
		result := NoRequeue(proj, nilCMP)
		Expect(result).To(Equal(ctrl.Result{}))
	})

	It("ShortRequeueAndIgnoreError returns ShortRequeueAfter and nil error", func() {
		result, err := ShortRequeueAndIgnoreError(proj, nilCMP, errors.New("some error"))
		Expect(err).To(Succeed())
		Expect(result.RequeueAfter).To(Equal(ShortRequeueAfter))
	})

	It("LongRequeueAndIgnoreError returns LongRequeueAfter and nil error", func() {
		result, err := LongRequeueAndIgnoreError(proj, nilCMP, errors.New("some error"))
		Expect(err).To(Succeed())
		Expect(result.RequeueAfter).To(Equal(LongRequeueAfter))
	})

	It("NoRequeueButIgnoreError returns empty Result and nil error", func() {
		result, err := NoRequeueButIgnoreError(proj, nilCMP, errors.New("some error"))
		Expect(err).To(Succeed())
		Expect(result).To(Equal(ctrl.Result{}))
	})

	It("NoRequeueAndPropagateError returns empty Result and propagates error", func() {
		testErr := errors.New("some error")
		result, err := NoRequeueAndPropagateError(proj, nilCMP, testErr)
		Expect(err).To(MatchError(testErr))
		Expect(result).To(Equal(ctrl.Result{}))
	})

	It("SmartRequeueOnError returns no requeue for semantic CMPError during non-Deleting phase", func() {
		semanticErr := &CMPError{Category: CMPErrorCategorySemantic, StatusCode: 400}
		result, err := SmartRequeueOnError(proj, nilCMP, semanticErr)
		Expect(err).To(Succeed())
		Expect(result).To(Equal(ctrl.Result{}))
	})

	It("SmartRequeueOnError returns LongRequeueAfter for semantic CMPError during Deleting phase", func() {
		proj.Status.Phase = v1alpha1.ResourcePhaseDeleting
		semanticErr := &CMPError{Category: CMPErrorCategorySemantic, StatusCode: 400}
		result, err := SmartRequeueOnError(proj, nilCMP, semanticErr)
		Expect(err).To(Succeed())
		Expect(result.RequeueAfter).To(Equal(LongRequeueAfter))
	})

	It("SmartRequeueOnError returns LongRequeueAfter for transient CMPError", func() {
		transientErr := &CMPError{Category: CMPErrorCategoryTransient, StatusCode: 409}
		result, err := SmartRequeueOnError(proj, nilCMP, transientErr)
		Expect(err).To(Succeed())
		Expect(result.RequeueAfter).To(Equal(LongRequeueAfter))
	})

	It("SmartRequeueOnError returns ShortRequeueAfter for technical CMPError", func() {
		technicalErr := &CMPError{Category: CMPErrorCategoryTechnical, StatusCode: 500}
		result, err := SmartRequeueOnError(proj, nilCMP, technicalErr)
		Expect(err).To(Succeed())
		Expect(result.RequeueAfter).To(Equal(ShortRequeueAfter))
	})

	It("SmartRequeueOnError returns LongRequeueAfter for plain (non-CMP) errors", func() {
		result, err := SmartRequeueOnError(proj, nilCMP, errors.New("unknown error"))
		Expect(err).To(Succeed())
		Expect(result.RequeueAfter).To(Equal(LongRequeueAfter))
	})
})
