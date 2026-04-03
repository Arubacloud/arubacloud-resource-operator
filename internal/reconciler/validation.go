package reconciler

import "fmt"

// ValidationFunc checks a single consistency constraint between the K8s resource (k),
// its primary CMP resource (a), and a bundle of resolved dependency data (b).
// It returns nil if the constraint is satisfied, or an error describing the violation.
// Validation functions must be pure: no I/O, no side effects, no context.Context.
type ValidationFunc[K ResourceObject, A any, B any] func(k K, a A, b B) error

type namedValidation[K ResourceObject, A any, B any] struct {
	name string
	fn   ValidationFunc[K, A, B]
}

// ValidationSet holds an ordered collection of named validation functions for a
// specific resource type. All functions are always executed (no short-circuit) so
// the caller receives every violation at once.
type ValidationSet[K ResourceObject, A any, B any] struct {
	validations []namedValidation[K, A, B]
}

// Add registers a named validation function. Functions are evaluated in insertion order.
func (vs *ValidationSet[K, A, B]) Add(name string, fn ValidationFunc[K, A, B]) {
	vs.validations = append(vs.validations, namedValidation[K, A, B]{name: name, fn: fn})
}

// Run executes all registered validation functions against (k, a, b) and returns
// nil if every function passes, or an *ErrInvalid aggregating every violation found.
func (vs *ValidationSet[K, A, B]) Run(k K, a A, b B) error {
	var violations []ValidationViolation
	for _, v := range vs.validations {
		if err := v.fn(k, a, b); err != nil {
			violations = append(violations, ValidationViolation{
				Rule:    v.name,
				Message: err.Error(),
			})
		}
	}
	if len(violations) == 0 {
		return nil
	}
	return &ErrInvalid{Violations: violations}
}

// FieldMustMatch returns a ValidationFunc that compares an arbitrary string field
// between the K8s resource or its CMP response and a value from the bundle.
// fieldName, selfValue, and otherValue are used to build the error message.
func FieldMustMatch[K ResourceObject, A any, B any](
	fieldName string,
	selfValue func(k K) string,
	otherValue func(b B) string,
	otherResourceName string,
) ValidationFunc[K, A, B] {
	return func(k K, _ A, b B) error {
		self := selfValue(k)
		other := otherValue(b)
		if self == other {
			return nil
		}
		return fmt.Errorf("%s mismatch with %s: %q != %q", fieldName, otherResourceName, self, other)
	}
}
