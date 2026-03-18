package controller

import (
	"context"
	"fmt"
	"log"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
)

// ConditionFunc defines a function signature for checking conditions
// based on a Kubernetes resource (K) and an Aruba CMP resource (A).
type ConditionFunc[K, A any] func(k K, a A) bool

// AlwaysTrue is a ConditionFunc that always evaluates to true,
// regardless of the provided Kubernetes or Aruba resources.
func AlwaysTrue[K, A any](k K, a A) bool { return true }

// ActionFunc defines a function signature for performing an action
// against a Kubernetes resource (K) or an Aruba CMP resource (A).
type ActionFunc[K, A any] func(ctx context.Context, k K, a A) error

// NoAction is an ActionFunc that performs no operation and returns nil.
func NoAction[K, A any](ctx context.Context, _ K, _ A) error { return nil }

// ActionOnErrorFunc defines a function signature for performing an action
// when another action encounters an error. It receives the original error.
type ActionOnErrorFunc[K, A any] func(ctx context.Context, k K, a A, err error) error

// NoActionOnError is an ActionOnErrorFunc that performs no operation
// when an error occurs and returns the original error.
func NoActionOnError[K, A any](ctx context.Context, _ K, _ A, err error) error { return err }

// RequeueFunc defines a function signature to determine the controller
// requeue behavior after a successful transition.
type RequeueFunc[K, A any] func(k K, a A) ctrl.Result

// ShortRequeue is a RequeueFunc that returns a ctrl.Result
// configured with the default requeue delay defined in the reconciler package.
func ShortRequeue[K, A any](_ K, _ A) ctrl.Result {
	return ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}
}

// LongRequeue is a RequeueFunc that returns a ctrl.Result
// configured with the default requeue delay defined in the reconciler package.
func LongRequeue[K, A any](_ K, _ A) ctrl.Result {
	return ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}
}

// NoRequeue is a RequeueFunc that returns an empty ctrl.Result,
// indicating that the request should not be requeued.
func NoRequeue[K, A any](_ K, _ A) ctrl.Result { return ctrl.Result{} }

// RequeueOnErrorFunc defines a function signature to determine the controller
// requeue behavior after an error occurs during a transition.
type RequeueOnErrorFunc[K, A any] func(k K, a A, err error) (ctrl.Result, error)

// ShortRequeueAndIgnoreError is a RequeueOnErrorFunc that returns a ctrl.Result
// configured with the default requeue delay defined in the reconciler package.
func ShortRequeueAndIgnoreError[K, A any](_ K, _ A, _ error) (ctrl.Result, error) {
	return ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
}

// LongRequeueAndIgnoreError is a RequeueOnErrorFunc that returns a ctrl.Result
// configured with the default requeue delay defined in the reconciler package.
func LongRequeueAndIgnoreError[K, A any](_ K, _ A, _ error) (ctrl.Result, error) {
	return ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
}

// NoRequeueButIgnoreError is a RequeueOnErrorFunc that returns an empty ctrl.Result,
// indicating that the request should not be requeued despite the error.
func NoRequeueButIgnoreError[K, A any](_ K, _ A, _ error) (ctrl.Result, error) {
	return ctrl.Result{}, nil
}

func NoRequeueAndPropagateError[K, A any](_ K, _ A, err error) (ctrl.Result, error) {
	return ctrl.Result{}, err
}

// Transition defines the interface for a state transition step in the reconciliation loop.
// It dictates when the transition should occur and what actions should be performed.
type Transition[K, A any] interface {
	// Name returns the descriptive name of the transition.
	Name() string
	// KCondition evaluates a condition against the Kubernetes resource.
	KCondition(k K, a A) bool
	// ACondition evaluates a condition against the Aruba CMP resource.
	ACondition(k K, a A) bool
	// KAction executes the action intended for the Kubernetes resource.
	KAction(ctx context.Context, k K, a A) error
	// AAction executes the action intended for the Aruba CMP resource.
	AAction(ctx context.Context, k K, a A) error
	// KActionOnASuccess is executed when AAction is succeed,
	// typically to update status on the Kubernetes resource.
	KActionOnASuccess(ctx context.Context, k K, a A) error
	// KActionOnAError is executed when AAction returns an error,
	// typically to perform rollback or update status on the Kubernetes resource.
	KActionOnAError(ctx context.Context, k K, a A, err error) error
	// Requeue determines the requeue result upon successful completion of the transition.
	Requeue(k K, a A) ctrl.Result
	// RequeueOnError determines the requeue result when an error occurs during the transition.
	RequeueOnError(k K, a A, err error) (ctrl.Result, error)
}

// AbstractTransition is a concrete implementation of the Transition interface.
// It allows constructing a transition using function pointers for its logic.
type AbstractTransition[K, A any] struct {
	name string

	kCondition ConditionFunc[K, A]
	aCondition ConditionFunc[K, A]

	kAction           ActionFunc[K, A]
	aAction           ActionFunc[K, A]
	kActionOnASuccess ActionFunc[K, A]
	kActionOnAError   ActionOnErrorFunc[K, A]

	requeue        RequeueFunc[K, A]
	requeueOnError RequeueOnErrorFunc[K, A]
}

var _ Transition[any, any] = (*AbstractTransition[any, any])(nil)

// Name returns the descriptive name of the transition.
func (t *AbstractTransition[K, A]) Name() string {
	return t.name
}

// KCondition delegates the evaluation to the provided kCondition func.
func (t *AbstractTransition[K, A]) KCondition(k K, a A) bool {
	return t.kCondition(k, a)
}

// ACondition delegates the evaluation to the provided aCondition func.
func (t *AbstractTransition[K, A]) ACondition(k K, a A) bool {
	return t.aCondition(k, a)
}

// Condition evaluates both Kubernetes and Aruba CMP conditions.
// It returns true only if both conditions are satisfied.
func (t *AbstractTransition[K, A]) Condition(k K, a A) bool {
	return t.ACondition(k, a) && t.KCondition(k, a)
}

// KAction executes the kubernetes-specific action of the transition.
func (t *AbstractTransition[K, A]) KAction(ctx context.Context, k K, a A) error {
	return t.kAction(ctx, k, a)
}

// AAction executes the Aruba CMP-specific action of the transition.
func (t *AbstractTransition[K, A]) AAction(ctx context.Context, k K, a A) error {
	return t.aAction(ctx, k, a)
}

// KActionOnASuccess executes an action when the AAction is succeed.
func (t *AbstractTransition[K, A]) KActionOnASuccess(ctx context.Context, k K, a A) error {
	return t.kActionOnASuccess(ctx, k, a)
}

// KActionOnAError executes a fallback action when the AAction fails.
func (t *AbstractTransition[K, A]) KActionOnAError(ctx context.Context, k K, a A, err error) error {
	return t.kActionOnAError(ctx, k, a, err)
}

// Action sequentially performs KAction and AAction.
// If AAction fails, it executes KActionOnAError to handle the failure.
func (t *AbstractTransition[K, A]) Action(ctx context.Context, k K, a A) error {
	if t.kAction != nil {
		// 1 - In case the transition has a KAction, only the KAction will be
		// executed in order to avoid the "double-KAction" problem
		if err := t.kAction(ctx, k, a); err != nil {
			return err
		}

	} else if t.aAction != nil {
		// 2 - In case the transition does not have a KAction but has an
		// AAction, that last one will be performed
		if err := t.aAction(ctx, k, a); err != nil {
			// 2.1 If some error occours and the transition has e
			// KActionOnAError then that onne will be executed
			if t.kActionOnAError != nil {
				if nestedErr := t.kActionOnAError(ctx, k, a, err); nestedErr != nil {
					return fmt.Errorf("%w when reacting to error: %w", nestedErr, err)
				}
			}

			return err // TODO: review the logic about return this error here - maybe better to let the kActionOnAError manage that
		}

		// 2.2 - In case no error occours, if the transition has a
		// KActionOnASuccess, than that one will be executed
		if t.kActionOnASuccess != nil {
			return t.kActionOnASuccess(ctx, k, a)
		}
	}

	return nil
}

// Requeue calls the configured requeue func to determine the ctrl.Result.
func (t *AbstractTransition[K, A]) Requeue(k K, a A) ctrl.Result {
	return t.requeue(k, a)
}

// RequeueOnError calls the configured requeueOnError func to determine the ctrl.Result upon error.
func (t *AbstractTransition[K, A]) RequeueOnError(k K, a A, err error) (ctrl.Result, error) {
	return t.requeueOnError(k, a, err)
}

// TransitionSet manages a collection of transitions and executes the first one
// whose conditions are met. It also provides default actions and requeue logic
// if no specific transition applies.
type TransitionSet[K, A any] struct {
	transitions []*AbstractTransition[K, A]

	defaultKAction           ActionFunc[K, A]
	defaultAAction           ActionFunc[K, A]
	defaultKActionOnASuccess ActionFunc[K, A]
	defaultKActionOnAError   ActionOnErrorFunc[K, A]

	defaultRequeue        RequeueFunc[K, A]
	defaultRequeueOnError RequeueOnErrorFunc[K, A]
}

// Add appends a new transition to the set.
// Transitions are evaluated in the order they are added.
func (s *TransitionSet[K, A]) Add(t *AbstractTransition[K, A]) {
	s.transitions = append(s.transitions, t)
}

// DefaultAction performs the default actions defined for the TransitionSet.
// It follows the same logic as AbstractTransition.Action, executing KAction, then AAction,
// and handling AAction errors with KActionOnAError.
func (s *TransitionSet[K, A]) DefaultAction(ctx context.Context, k K, a A) error {
	if s.defaultKAction != nil {
		// 1 - In case the transition has a KAction, only the KAction will be
		// executed in order to avoid the "double-KAction" problem
		if err := s.defaultKAction(ctx, k, a); err != nil {
			return err
		}

	} else if s.defaultAAction != nil {
		// 2 - In case the transition does not have a KAction but has an
		// AAction, that last one will be performed
		if err := s.defaultAAction(ctx, k, a); err != nil {
			// 2.1 If some error occours and the transition has e
			// KActionOnAError then that onne will be executed
			if s.defaultKActionOnAError != nil {
				if nestedErr := s.defaultKActionOnAError(ctx, k, a, err); nestedErr != nil {
					return fmt.Errorf("%w when reacting to error: %w", nestedErr, err)
				}
			}

			return err // TODO: review the logic about return this error here - maybe better to let the kActionOnAError manage that
		}

		// 2.2 - In case no error occours, if the transition has a
		// KActionOnASuccess, than that one will be executed
		if s.defaultKActionOnASuccess != nil {
			return s.defaultKActionOnASuccess(ctx, k, a)
		}
	}

	return nil
}

// Run evaluates all transitions in order. It executes the Action of the first transition
// whose Condition returns true. If no transition's conditions are met, it executes the DefaultAction.
// It returns the appropriate ctrl.Result and any error encountered.
func (s *TransitionSet[K, A]) Run(ctx context.Context, k K, a A) (ctrl.Result, error) {
	for _, t := range s.transitions {
		if t.Condition(k, a) {
			log.Printf("transition met condition: name: '%s'", t.Name()) // TODO: better logging
			if err := t.Action(ctx, k, a); err != nil {
				log.Printf("transition error: name: '%s', err: '%v'", t.Name(), err) // TODO: better logging
				return t.RequeueOnError(k, a, err)
			}

			log.Printf("transition succeed: name: '%s'", t.Name()) // TODO: better logging
			return t.Requeue(k, a), nil
		}
	}

	// For the case which no transition gives condition we run the default actions
	if err := s.DefaultAction(ctx, k, a); err != nil {
		return s.defaultRequeueOnError(k, a, err)
	}

	return s.defaultRequeue(k, a), nil
}
