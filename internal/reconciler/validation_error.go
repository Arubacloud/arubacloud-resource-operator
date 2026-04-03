package reconciler

import (
	"errors"
	"fmt"
	"strings"
)

// ValidationViolation describes a single cross-resource consistency failure,
// identified by the rule name that detected it.
type ValidationViolation struct {
	// Rule is the name of the validation rule that produced this violation.
	Rule string
	// Message is a human-readable description of the inconsistency.
	Message string
}

// ErrInvalid aggregates one or more ValidationViolations detected by a ValidationSet.
// All violations are collected before returning so the user sees every issue at once.
type ErrInvalid struct {
	Violations []ValidationViolation
}

// Error implements the error interface. The message lists every violation separated
// by "; ".
func (e *ErrInvalid) Error() string {
	parts := make([]string, len(e.Violations))
	for i, v := range e.Violations {
		parts[i] = fmt.Sprintf("[%s] %s", v.Rule, v.Message)
	}
	return "validation failed: " + strings.Join(parts, "; ")
}

// Unwrap returns the individual violation errors as a slice, enabling errors.Is /
// errors.As to traverse the chain (Go 1.20+ multi-unwrap).
func (e *ErrInvalid) Unwrap() []error {
	errs := make([]error, len(e.Violations))
	for i, v := range e.Violations {
		errs[i] = fmt.Errorf("[%s] %s", v.Rule, v.Message)
	}
	return errs
}

// IsErrInvalid reports whether err (or any error in its chain) is an *ErrInvalid.
func IsErrInvalid(err error) bool {
	var invalid *ErrInvalid
	return errors.As(err, &invalid)
}
