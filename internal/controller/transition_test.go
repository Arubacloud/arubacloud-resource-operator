package controller

import (
	"context"
	"errors"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	arubatypes "github.com/Arubacloud/sdk-go/pkg/types"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type testProject = *v1alpha1.Project
type testCMP = *arubatypes.ProjectResponse

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

	Describe("Condition()", func() {
		It("returns true when both kCondition and aCondition are true", func() {
			t := &AbstractTransition[testProject, testCMP]{
				name:           "both-true",
				kCondition:     func(_ testProject, _ testCMP) bool { return true },
				aCondition:     func(_ testProject, _ testCMP) bool { return true },
				requeue:        NoRequeue[testProject, testCMP],
				requeueOnError: NoRequeueAndPropagateError[testProject, testCMP],
			}
			Expect(t.Condition(proj, nilCMP)).To(BeTrue())
		})

		It("returns false when kCondition is false", func() {
			t := &AbstractTransition[testProject, testCMP]{
				name:           "k-false",
				kCondition:     func(_ testProject, _ testCMP) bool { return false },
				aCondition:     func(_ testProject, _ testCMP) bool { return true },
				requeue:        NoRequeue[testProject, testCMP],
				requeueOnError: NoRequeueAndPropagateError[testProject, testCMP],
			}
			Expect(t.Condition(proj, nilCMP)).To(BeFalse())
		})

		It("returns false when aCondition is false", func() {
			t := &AbstractTransition[testProject, testCMP]{
				name:           "a-false",
				kCondition:     func(_ testProject, _ testCMP) bool { return true },
				aCondition:     func(_ testProject, _ testCMP) bool { return false },
				requeue:        NoRequeue[testProject, testCMP],
				requeueOnError: NoRequeueAndPropagateError[testProject, testCMP],
			}
			Expect(t.Condition(proj, nilCMP)).To(BeFalse())
		})

		It("returns false when both conditions are false", func() {
			t := &AbstractTransition[testProject, testCMP]{
				name:           "both-false",
				kCondition:     func(_ testProject, _ testCMP) bool { return false },
				aCondition:     func(_ testProject, _ testCMP) bool { return false },
				requeue:        NoRequeue[testProject, testCMP],
				requeueOnError: NoRequeueAndPropagateError[testProject, testCMP],
			}
			Expect(t.Condition(proj, nilCMP)).To(BeFalse())
		})
	})

	Describe("Action() priority", func() {
		It("runs only kAction when kAction is set", func() {
			kRan, aRan := false, false
			t := &AbstractTransition[testProject, testCMP]{
				name:       "kaction-priority",
				kCondition: AlwaysTrue[testProject, testCMP],
				aCondition: AlwaysTrue[testProject, testCMP],
				kAction: func(_ context.Context, _ testProject, _ testCMP) error {
					kRan = true
					return nil
				},
				aAction: func(_ context.Context, _ testProject, _ testCMP) error {
					aRan = true
					return nil
				},
				kActionOnASuccess: func(_ context.Context, _ testProject, _ testCMP) error {
					return errors.New("should not be called")
				},
				requeue:        NoRequeue[testProject, testCMP],
				requeueOnError: NoRequeueAndPropagateError[testProject, testCMP],
			}
			Expect(t.Action(ctx, proj, nilCMP)).To(Succeed())
			Expect(kRan).To(BeTrue())
			Expect(aRan).To(BeFalse())
		})

		It("runs aAction and kActionOnASuccess when kAction is nil and aAction succeeds", func() {
			aRan, successRan := false, false
			t := &AbstractTransition[testProject, testCMP]{
				name:       "aaction-success",
				kCondition: AlwaysTrue[testProject, testCMP],
				aCondition: AlwaysTrue[testProject, testCMP],
				aAction: func(_ context.Context, _ testProject, _ testCMP) error {
					aRan = true
					return nil
				},
				kActionOnASuccess: func(_ context.Context, _ testProject, _ testCMP) error {
					successRan = true
					return nil
				},
				requeue:        NoRequeue[testProject, testCMP],
				requeueOnError: NoRequeueAndPropagateError[testProject, testCMP],
			}
			Expect(t.Action(ctx, proj, nilCMP)).To(Succeed())
			Expect(aRan).To(BeTrue())
			Expect(successRan).To(BeTrue())
		})

		It("runs aAction and kActionOnAError when kAction is nil and aAction fails", func() {
			aRan, errorRan := false, false
			testErr := errors.New("aAction failed")
			t := &AbstractTransition[testProject, testCMP]{
				name:       "aaction-error",
				kCondition: AlwaysTrue[testProject, testCMP],
				aCondition: AlwaysTrue[testProject, testCMP],
				aAction: func(_ context.Context, _ testProject, _ testCMP) error {
					aRan = true
					return testErr
				},
				kActionOnAError: func(_ context.Context, _ testProject, _ testCMP, err error) error {
					errorRan = true
					return err // propagate
				},
				requeue:        NoRequeue[testProject, testCMP],
				requeueOnError: NoRequeueAndPropagateError[testProject, testCMP],
			}
			err := t.Action(ctx, proj, nilCMP)
			Expect(err).To(MatchError(testErr))
			Expect(aRan).To(BeTrue())
			Expect(errorRan).To(BeTrue())
		})

		It("returns nil when both kAction and aAction are nil", func() {
			t := &AbstractTransition[testProject, testCMP]{
				name:           "no-actions",
				kCondition:     AlwaysTrue[testProject, testCMP],
				aCondition:     AlwaysTrue[testProject, testCMP],
				requeue:        NoRequeue[testProject, testCMP],
				requeueOnError: NoRequeueAndPropagateError[testProject, testCMP],
			}
			Expect(t.Action(ctx, proj, nilCMP)).To(Succeed())
		})
	})
})

var _ = Describe("TransitionSet.Run()", func() {
	var ctx = context.Background()

	buildTS := func(transitions ...*AbstractTransition[testProject, testCMP]) *TransitionSet[testProject, testCMP] {
		ts := &TransitionSet[testProject, testCMP]{
			defaultRequeue:        NoRequeue[testProject, testCMP],
			defaultRequeueOnError: NoRequeueAndPropagateError[testProject, testCMP],
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
				name:       "first",
				kCondition: AlwaysTrue[testProject, testCMP],
				aCondition: AlwaysTrue[testProject, testCMP],
				kAction: func(_ context.Context, _ testProject, _ testCMP) error {
					firstRan = true
					return nil
				},
				requeue:        NoRequeue[testProject, testCMP],
				requeueOnError: NoRequeueAndPropagateError[testProject, testCMP],
			},
			&AbstractTransition[testProject, testCMP]{
				name:       "second",
				kCondition: AlwaysTrue[testProject, testCMP],
				aCondition: AlwaysTrue[testProject, testCMP],
				kAction: func(_ context.Context, _ testProject, _ testCMP) error {
					secondRan = true
					return nil
				},
				requeue:        NoRequeue[testProject, testCMP],
				requeueOnError: NoRequeueAndPropagateError[testProject, testCMP],
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
				name:           "first-no-match",
				kCondition:     func(_ testProject, _ testCMP) bool { return false },
				aCondition:     AlwaysTrue[testProject, testCMP],
				requeue:        NoRequeue[testProject, testCMP],
				requeueOnError: NoRequeueAndPropagateError[testProject, testCMP],
			},
			&AbstractTransition[testProject, testCMP]{
				name:       "second-matches",
				kCondition: AlwaysTrue[testProject, testCMP],
				aCondition: AlwaysTrue[testProject, testCMP],
				kAction: func(_ context.Context, _ testProject, _ testCMP) error {
					secondRan = true
					return nil
				},
				requeue:        ShortRequeue[testProject, testCMP],
				requeueOnError: NoRequeueAndPropagateError[testProject, testCMP],
			},
		)
		proj := newMinimalProject()
		result, err := ts.Run(ctx, proj, nil)
		Expect(err).To(Succeed())
		Expect(result.RequeueAfter).To(Equal(reconciler.ShortRequeueAfter))
		Expect(secondRan).To(BeTrue())
	})

	It("executes DefaultAction when no transition matches", func() {
		defaultRan := false
		ts := buildTS(
			&AbstractTransition[testProject, testCMP]{
				name:           "no-match",
				kCondition:     func(_ testProject, _ testCMP) bool { return false },
				aCondition:     AlwaysTrue[testProject, testCMP],
				requeue:        NoRequeue[testProject, testCMP],
				requeueOnError: NoRequeueAndPropagateError[testProject, testCMP],
			},
		)
		ts.defaultKAction = func(_ context.Context, _ testProject, _ testCMP) error {
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
				name:       "error-transition",
				kCondition: AlwaysTrue[testProject, testCMP],
				aCondition: AlwaysTrue[testProject, testCMP],
				kAction: func(_ context.Context, _ testProject, _ testCMP) error {
					return testErr
				},
				requeue: NoRequeue[testProject, testCMP],
				requeueOnError: func(_ testProject, _ testCMP, err error) (ctrl.Result, error) {
					return ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
				},
			},
		)
		proj := newMinimalProject()
		result, err := ts.Run(ctx, proj, nil)
		Expect(err).To(Succeed()) // requeueOnError swallowed the error
		Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
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
		Expect(result.RequeueAfter).To(Equal(reconciler.ShortRequeueAfter))
	})

	It("LongRequeue returns LongRequeueAfter", func() {
		result := LongRequeue(proj, nilCMP)
		Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
	})

	It("NoRequeue returns empty Result", func() {
		result := NoRequeue(proj, nilCMP)
		Expect(result).To(Equal(ctrl.Result{}))
	})

	It("ShortRequeueAndIgnoreError returns ShortRequeueAfter and nil error", func() {
		result, err := ShortRequeueAndIgnoreError(proj, nilCMP, errors.New("some error"))
		Expect(err).To(Succeed())
		Expect(result.RequeueAfter).To(Equal(reconciler.ShortRequeueAfter))
	})

	It("LongRequeueAndIgnoreError returns LongRequeueAfter and nil error", func() {
		result, err := LongRequeueAndIgnoreError(proj, nilCMP, errors.New("some error"))
		Expect(err).To(Succeed())
		Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
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

	It("SmartRequeueOnError returns LongRequeueAfter for semantic CMPError", func() {
		semanticErr := &CMPError{Category: CMPErrorCategorySemantic, StatusCode: 400}
		result, err := SmartRequeueOnError(proj, nilCMP, semanticErr)
		Expect(err).To(Succeed())
		Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
	})

	It("SmartRequeueOnError returns ShortRequeueAfter for technical CMPError", func() {
		technicalErr := &CMPError{Category: CMPErrorCategoryTechnical, StatusCode: 500}
		result, err := SmartRequeueOnError(proj, nilCMP, technicalErr)
		Expect(err).To(Succeed())
		Expect(result.RequeueAfter).To(Equal(reconciler.ShortRequeueAfter))
	})

	It("SmartRequeueOnError returns ShortRequeueAfter for plain (non-CMP) errors", func() {
		result, err := SmartRequeueOnError(proj, nilCMP, errors.New("unknown error"))
		Expect(err).To(Succeed())
		Expect(result.RequeueAfter).To(Equal(reconciler.ShortRequeueAfter))
	})
})
