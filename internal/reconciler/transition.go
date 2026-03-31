package reconciler

import (
	"context"
	"fmt"
	"reflect"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
)

// ConditionFunc defines a function signature for checking conditions
// based on a Kubernetes resource (K) and an Aruba CMP resource (A).
type ConditionFunc[K ResourceObject, A any] func(k K, a A) bool

// AlwaysTrue is a ConditionFunc that always evaluates to true,
// regardless of the provided Kubernetes or Aruba resources.
func AlwaysTrue[K ResourceObject, A any](k K, a A) bool { return true }

// ActionFunc defines a function signature for performing an action
// against a Kubernetes resource (K) or an Aruba CMP resource (A).
type ActionFunc[K ResourceObject, A any] func(ctx context.Context, k K, a A) error

// NoAction is an ActionFunc that performs no operation and returns nil.
func NoAction[K ResourceObject, A any](ctx context.Context, _ K, _ A) error { return nil }

// ActionOnErrorFunc defines a function signature for performing an action
// when another action encounters an error. It receives the original error.
type ActionOnErrorFunc[K ResourceObject, A any] func(ctx context.Context, k K, a A, err error) error

// NoActionOnError is an ActionOnErrorFunc that performs no operation
// when an error occurs and returns the original error.
func NoActionOnError[K ResourceObject, A any](ctx context.Context, _ K, _ A, err error) error {
	return err
}

// RequeueFunc defines a function signature to determine the controller
// requeue behavior after a successful transition.
type RequeueFunc[K ResourceObject, A any] func(k K, a A) ctrl.Result

// ShortRequeue is a RequeueFunc that returns a ctrl.Result
// configured with the default requeue delay defined in the reconciler package.
func ShortRequeue[K ResourceObject, A any](_ K, _ A) ctrl.Result {
	return ctrl.Result{RequeueAfter: ShortRequeueAfter}
}

// LongRequeue is a RequeueFunc that returns a ctrl.Result
// configured with the default requeue delay defined in the reconciler package.
func LongRequeue[K ResourceObject, A any](_ K, _ A) ctrl.Result {
	return ctrl.Result{RequeueAfter: LongRequeueAfter}
}

// NoRequeue is a RequeueFunc that returns an empty ctrl.Result,
// indicating that the request should not be requeued.
func NoRequeue[K ResourceObject, A any](_ K, _ A) ctrl.Result { return ctrl.Result{} }

// RequeueOnErrorFunc defines a function signature to determine the controller
// requeue behavior after an error occurs during a transition.
type RequeueOnErrorFunc[K ResourceObject, A any] func(k K, a A, err error) (ctrl.Result, error)

// ShortRequeueAndIgnoreError is a RequeueOnErrorFunc that returns a ctrl.Result
// configured with the default requeue delay defined in the reconciler package.
func ShortRequeueAndIgnoreError[K ResourceObject, A any](_ K, _ A, _ error) (ctrl.Result, error) {
	return ctrl.Result{RequeueAfter: ShortRequeueAfter}, nil
}

// LongRequeueAndIgnoreError is a RequeueOnErrorFunc that returns a ctrl.Result
// configured with the default requeue delay defined in the reconciler package.
func LongRequeueAndIgnoreError[K ResourceObject, A any](_ K, _ A, _ error) (ctrl.Result, error) {
	return ctrl.Result{RequeueAfter: LongRequeueAfter}, nil
}

// NoRequeueButIgnoreError is a RequeueOnErrorFunc that returns an empty ctrl.Result,
// indicating that the request should not be requeued despite the error.
func NoRequeueButIgnoreError[K ResourceObject, A any](_ K, _ A, _ error) (ctrl.Result, error) {
	return ctrl.Result{}, nil
}

// NoRequeueAndPropagateError is a RequeueOnErrorFunc that returns an empty ctrl.Result
// but propagates the encountered error.
func NoRequeueAndPropagateError[K ResourceObject, A any](_ K, _ A, err error) (ctrl.Result, error) {
	return ctrl.Result{}, err
}

// SmartRequeueOnError is a RequeueOnErrorFunc that inspects the error category of a *CMPError:
//   - During Deleting phase: always requeue (there is no "wait for spec change" recovery
//     during deletion — semantic errors are temporary CMP-side conditions that resolve once
//     dependent resources are cleaned up).
//   - Semantic errors (4xx with validation errors): no requeue. KubeSetErrorMessageOnCMPError
//     has already moved the resource to Failed+ValidationFailed; the next reconcile only fires
//     when the user corrects the spec (generation-gated recovery via IsCMPValidationFailedAndSpecChanged).
//   - Transient errors (4xx without validation errors): long requeue — temporary condition.
//   - Technical errors (5xx, network failures): short requeue for faster recovery.
//   - Non-CMPError errors: long requeue.
func SmartRequeueOnError[K ResourceObject, A any](k K, _ A, err error) (ctrl.Result, error) {
	// During deletion, always requeue — the CMP-side condition is temporary
	// and will resolve once dependent resources are cleaned up.
	if rs := k.GetResourceStatus(); rs != nil && rs.Phase == v1alpha1.ResourcePhaseDeleting {
		if CMPErrorIsTechnical(err) {
			return ctrl.Result{RequeueAfter: ShortRequeueAfter}, nil
		}
		return ctrl.Result{RequeueAfter: LongRequeueAfter}, nil
	}
	if CMPErrorIsSemantic(err) {
		return ctrl.Result{}, nil
	}
	if CMPErrorIsTechnical(err) {
		return ctrl.Result{RequeueAfter: ShortRequeueAfter}, nil
	}
	return ctrl.Result{RequeueAfter: LongRequeueAfter}, nil
}

// AbstractTransition is a concrete state transition step in the reconciliation loop.
// It allows constructing a transition using function fields for its logic.
// All fields are exported so that controllers in other packages can initialise
// transition sets using composite-literal syntax.
type AbstractTransition[K ResourceObject, A any] struct {
	// Name is the descriptive identifier of the transition, used in log messages.
	Name string

	// KCondition evaluates a condition against the Kubernetes resource.
	KCondition ConditionFunc[K, A]
	// ACondition evaluates a condition against the Aruba CMP resource.
	ACondition ConditionFunc[K, A]

	// KAction executes an action intended for the Kubernetes resource.
	// KAction and AAction are mutually exclusive — only one runs per transition.
	KAction ActionFunc[K, A]
	// AAction executes an action against the Aruba CMP.
	AAction ActionFunc[K, A]
	// KActionOnASuccess is executed when AAction succeeds.
	KActionOnASuccess ActionFunc[K, A]
	// KActionOnAError is executed when AAction returns an error.
	KActionOnAError ActionOnErrorFunc[K, A]

	// Requeue determines the requeue result upon successful completion.
	Requeue RequeueFunc[K, A]
	// RequeueOnError determines the requeue result when an error occurs.
	RequeueOnError RequeueOnErrorFunc[K, A]
}

// condition evaluates both Kubernetes and Aruba CMP conditions.
// Returns true only if both are satisfied.
func (t *AbstractTransition[K, A]) condition(k K, a A) bool {
	return t.ACondition(k, a) && t.KCondition(k, a)
}

// action executes the defined actions for the transition.
// It prioritizes KAction. If no KAction is defined, it executes AAction.
// If AAction succeeds, it executes KActionOnASuccess.
// If AAction fails, it executes KActionOnAError to handle the failure.
func (t *AbstractTransition[K, A]) action(ctx context.Context, k K, a A) error {
	if t.KAction != nil {
		// 1 - In case the transition has a KAction, only the KAction will be
		// executed in order to avoid the "double-KAction" problem
		if err := t.KAction(ctx, k, a); err != nil {
			return err
		}

	} else if t.AAction != nil {
		// 2 - In case the transition does not have a KAction but has an
		// AAction, that last one will be performed
		if err := t.AAction(ctx, k, a); err != nil {
			// 2.1 If some error occurs and the transition has a
			// KActionOnAError then that one will be executed
			if t.KActionOnAError != nil {
				if nestedErr := t.KActionOnAError(ctx, k, a, err); nestedErr != nil {
					return fmt.Errorf("%w when reacting to error: %w", nestedErr, err)
				}
			}

			return err // TODO: review the logic about return this error here - maybe better to let the kActionOnAError manage that
		}

		// 2.2 - In case no error occurs, if the transition has a
		// KActionOnASuccess, then that one will be executed
		if t.KActionOnASuccess != nil {
			return t.KActionOnASuccess(ctx, k, a)
		}
	}

	return nil
}

// TransitionSet manages a collection of transitions and executes the first one
// whose conditions are met. It also provides default actions and requeue logic
// if no specific transition applies.
type TransitionSet[K ResourceObject, A any] struct {
	transitions []*AbstractTransition[K, A]

	DefaultKAction           ActionFunc[K, A]
	DefaultAAction           ActionFunc[K, A]
	DefaultKActionOnASuccess ActionFunc[K, A]
	DefaultKActionOnAError   ActionOnErrorFunc[K, A]

	DefaultRequeue        RequeueFunc[K, A]
	DefaultRequeueOnError RequeueOnErrorFunc[K, A]
}

// Add appends a new transition to the set.
// Transitions are evaluated in the order they are added.
func (s *TransitionSet[K, A]) Add(t *AbstractTransition[K, A]) {
	s.transitions = append(s.transitions, t)
}

// defaultAction performs the default actions defined for the TransitionSet.
// It follows the same logic as AbstractTransition.action, prioritizing KAction,
// falling back to AAction, and handling AAction successes and errors accordingly.
func (s *TransitionSet[K, A]) defaultAction(ctx context.Context, k K, a A) error {
	if s.DefaultKAction != nil {
		// 1 - In case the transition has a KAction, only the KAction will be
		// executed in order to avoid the "double-KAction" problem
		if err := s.DefaultKAction(ctx, k, a); err != nil {
			return err
		}

	} else if s.DefaultAAction != nil {
		// 2 - In case the transition does not have a KAction but has an
		// AAction, that last one will be performed
		if err := s.DefaultAAction(ctx, k, a); err != nil {
			// 2.1 If some error occurs and the transition has a
			// KActionOnAError then that one will be executed
			if s.DefaultKActionOnAError != nil {
				if nestedErr := s.DefaultKActionOnAError(ctx, k, a, err); nestedErr != nil {
					return fmt.Errorf("%w when reacting to error: %w", nestedErr, err)
				}
			}

			return err // TODO: review the logic about return this error here - maybe better to let the kActionOnAError manage that
		}

		// 2.2 - In case no error occurs, if the transition has a
		// KActionOnASuccess, then that one will be executed
		if s.DefaultKActionOnASuccess != nil {
			return s.DefaultKActionOnASuccess(ctx, k, a)
		}
	}

	return nil
}

// Run evaluates all transitions in order. It executes the action of the first transition
// whose condition returns true. If no transition's conditions are met, it executes the defaultAction.
// It returns the appropriate ctrl.Result and any error encountered.
func (s *TransitionSet[K, A]) Run(ctx context.Context, k K, a A) (ctrl.Result, error) {
	kind := reflect.TypeOf(k).Elem().Name()
	rs := k.GetResourceStatus()
	resID := ""
	if rs != nil {
		resID = rs.ResourceID
	}
	nsName := fmt.Sprintf("%s/%s", k.GetNamespace(), k.GetName())

	logger := log.FromContext(ctx).WithValues(
		"resourceKind", kind,
		"resource", nsName,
		"resourceID", resID,
	)

	for _, t := range s.transitions {
		if t.condition(k, a) {
			logger.V(2).Info("transition matched", "transition", t.Name)
			if err := t.action(ctx, k, a); err != nil {
				logger.Error(err, "transition action failed", "transition", t.Name)
				return t.RequeueOnError(k, a, err)
			}

			logger.V(1).Info("transition completed", "transition", t.Name)
			return t.Requeue(k, a), nil
		}
	}

	// For the case which no transition gives condition we run the default actions
	logger.V(2).Info("no transition matched, running default action")
	if err := s.defaultAction(ctx, k, a); err != nil {
		return s.DefaultRequeueOnError(k, a, err)
	}

	return s.DefaultRequeue(k, a), nil
}
