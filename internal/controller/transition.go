package controller

import (
	"context"
	"fmt"
	"log"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
)

type ConditionFunc[K, A any] func(k K, a A) bool

func AlwaysTrue[K, A any](k K, a A) bool { return true }

type ActionFunc[K, A any] func(ctx context.Context, k K, a A) error

func NoAction[K, A any](ctx context.Context, _ K, _ A) error { return nil }

type ActionOnErrorFunc[K, A any] func(ctx context.Context, k K, a A, err error) error

func NoActionOnError[K, A any](ctx context.Context, _ K, _ A, _ error) error { return nil }

type RequeueFunc[K, A any] func(k K, a A) ctrl.Result

func DefaultRequeue[K, A any](_ K, _ A) ctrl.Result {
	return ctrl.Result{RequeueAfter: reconciler.RequeueAfter}
}

func NoRequeue[K, A any](_ K, _ A) ctrl.Result { return ctrl.Result{} }

type RequeueOnErrorFunc[K, A any] func(k K, a A, err error) ctrl.Result

func DefaultRequeueOnError[K, A any](_ K, _ A, _ error) ctrl.Result {
	return ctrl.Result{RequeueAfter: reconciler.RequeueAfter}
}

func NoRequeueOnError[K, A any](_ K, _ A, _ error) ctrl.Result { return ctrl.Result{} }

type Transition[K, A any] interface {
	Name() string
	KCondition(k K, a A) bool
	ACondition(k K, a A) bool
	KAction(ctx context.Context, k K, a A) error
	AAction(ctx context.Context, k K, a A) error
	KActionOnAError(ctx context.Context, k K, a A, err error) error
	Requeue(k K, a A) ctrl.Result
	RequeueOnError(k K, a A, err error) ctrl.Result
}

type AbstractTransition[K, A any] struct {
	name string

	kCondition ConditionFunc[K, A]
	aCondition ConditionFunc[K, A]

	kAction         ActionFunc[K, A]
	aAction         ActionFunc[K, A]
	kActionOnAError ActionOnErrorFunc[K, A]

	requeue        RequeueFunc[K, A]
	requeueOnError RequeueOnErrorFunc[K, A]
}

var _ Transition[any, any] = (*AbstractTransition[any, any])(nil)

func (t *AbstractTransition[K, A]) Name() string {
	return t.name
}

func (t *AbstractTransition[K, A]) KCondition(k K, a A) bool {
	return t.kCondition(k, a)
}

func (t *AbstractTransition[K, A]) ACondition(k K, a A) bool {
	return t.aCondition(k, a)
}

func (t *AbstractTransition[K, A]) Condition(k K, a A) bool {
	return t.ACondition(k, a) && t.KCondition(k, a)
}

func (t *AbstractTransition[K, A]) KAction(ctx context.Context, k K, a A) error {
	return t.kAction(ctx, k, a)
}

func (t *AbstractTransition[K, A]) AAction(ctx context.Context, k K, a A) error {
	return t.aAction(ctx, k, a)
}

func (t *AbstractTransition[K, A]) KActionOnAError(ctx context.Context, k K, a A, err error) error {
	return t.kAction(ctx, k, a)
}

func (t *AbstractTransition[K, A]) Action(ctx context.Context, k K, a A) error {
	if err := t.KAction(ctx, k, a); err != nil {
		return err
	}

	if err := t.AAction(ctx, k, a); err != nil {
		if nestedErr := t.kActionOnAError(ctx, k, a, err); nestedErr != nil {
			return fmt.Errorf("%w when reacting to error: %w", nestedErr, err)
		}

		return err
	}

	return nil
}

func (t *AbstractTransition[K, A]) Requeue(k K, a A) ctrl.Result {
	return t.requeue(k, a)
}

func (t *AbstractTransition[K, A]) RequeueOnError(k K, a A, err error) ctrl.Result {
	return t.requeueOnError(k, a, err)
}

type TransitionSet[K, A any] struct {
	transitions []*AbstractTransition[K, A]

	defaultKAction         ActionFunc[K, A]
	defaultAAction         ActionFunc[K, A]
	defaultKActionOnAError ActionOnErrorFunc[K, A]

	defaultRequeue        RequeueFunc[K, A]
	defaultRequeueOnError RequeueOnErrorFunc[K, A]
}

func (s *TransitionSet[K, A]) Add(t *AbstractTransition[K, A]) {
	s.transitions = append(s.transitions, t)
}

func (s *TransitionSet[K, A]) DefaultAction(ctx context.Context, k K, a A) error {
	if err := s.defaultKAction(ctx, k, a); err != nil {
		return err
	}

	if err := s.defaultAAction(ctx, k, a); err != nil {
		if nestedErr := s.defaultKActionOnAError(ctx, k, a, err); nestedErr != nil {
			return fmt.Errorf("%w when reacting to error: %w", nestedErr, err)
		}

		return err
	}

	return nil
}

func (s *TransitionSet[K, A]) Run(ctx context.Context, k K, a A) (ctrl.Result, error) {
	for _, t := range s.transitions {
		if t.Condition(k, a) {
			if err := t.Action(ctx, k, a); err != nil {
				log.Printf("transition error: name: '%s', err: '%v'", t.Name(), err) // TODO: better logging
				return t.RequeueOnError(k, a, err), err
			}

			log.Printf("transition succeed: name: '%s'", t.Name()) // TODO: better logging
			return t.Requeue(k, a), nil
		}
	}

	// For the case which no transition gives condition we run the default actions
	if err := s.DefaultAction(ctx, k, a); err != nil {
		return s.defaultRequeueOnError(k, a, err), err
	}

	return s.defaultRequeue(k, a), nil
}
