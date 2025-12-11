// MIT License
//
// Copyright (c) 2025 Advanced Micro Devices, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package controllerutils

import "errors"

// ErrorCategory classifies high-level error semantics for the state engine.
type ErrorCategory int

const (
	ErrorCategoryUnknown ErrorCategory = iota
	ErrorCategoryInfrastructure
	ErrorCategoryAuth
	ErrorCategoryMissingDependency
	ErrorCategoryInvalidSpec
)

// StateEngineError is a structured error for the state engine layer.
type StateEngineError interface {
	error
	Category() ErrorCategory
	Reason() string
	UserMessage() string
}

// stateEngineError is the concrete implementation of StateEngineError.
type stateEngineError struct {
	err     error
	cat     ErrorCategory
	reason  string
	message string
}

// Error returns a formatted error message combining the reason and user message.
// This satisfies the error interface and provides context for logging.
func (e *stateEngineError) Error() string {
	if e.reason != "" && e.message != "" {
		return e.reason + ": " + e.message
	}
	if e.reason != "" {
		return e.reason
	}
	if e.message != "" {
		return e.message
	}
	if e.err != nil {
		return e.err.Error()
	}
	return "unknown error"
}

func (e *stateEngineError) Unwrap() error {
	return e.err
}

func (e *stateEngineError) Category() ErrorCategory {
	return e.cat
}

func (e *stateEngineError) Reason() string {
	return e.reason
}

func (e *stateEngineError) UserMessage() string {
	return e.message
}

// String returns the human-readable name of the error category.
func (c ErrorCategory) String() string {
	switch c {
	case ErrorCategoryInfrastructure:
		return "Infrastructure"
	case ErrorCategoryAuth:
		return "Auth"
	case ErrorCategoryMissingDependency:
		return "MissingDependency"
	case ErrorCategoryInvalidSpec:
		return "InvalidSpec"
	default:
		return "Unknown"
	}
}

// Constructors for state engine errors.

// NewInfrastructureError creates an error for transient infrastructure issues
// (e.g., network failures, API server unavailability). These errors should
// typically cause reconciliation retry without affecting status conditions.
//
// Parameters:
//   - reason: Machine-readable reason code (e.g., "NetworkFailure")
//   - message: Human-readable description for users
//   - cause: Underlying error that caused this issue (may be nil)
func NewInfrastructureError(reason, message string, cause error) error {
	return &stateEngineError{
		err:     cause,
		cat:     ErrorCategoryInfrastructure,
		reason:  reason,
		message: message,
	}
}

// NewAuthError creates an error for authentication or authorization failures
// (e.g., missing credentials, insufficient permissions). These errors should
// be surfaced in status conditions as they indicate configuration issues.
//
// Parameters:
//   - reason: Machine-readable reason code (e.g., "InsufficientPermissions")
//   - message: Human-readable description for users
//   - cause: Underlying error that caused this issue (may be nil)
func NewAuthError(reason, message string, cause error) error {
	return &stateEngineError{
		err:     cause,
		cat:     ErrorCategoryAuth,
		reason:  reason,
		message: message,
	}
}

// NewMissingDependencyError creates an error for missing required resources
// (e.g., ConfigMaps, Secrets, CRDs). These errors should be surfaced in status
// conditions to inform users about missing dependencies.
//
// Parameters:
//   - reason: Machine-readable reason code (e.g., "SecretNotFound")
//   - message: Human-readable description for users
//   - cause: Underlying error that caused this issue (may be nil)
func NewMissingDependencyError(reason, message string, cause error) error {
	return &stateEngineError{
		err:     cause,
		cat:     ErrorCategoryMissingDependency,
		reason:  reason,
		message: message,
	}
}

// NewInvalidSpecError creates an error for invalid user-provided specifications
// (e.g., validation failures, malformed configuration). These errors should be
// surfaced in status conditions as they require user intervention to fix.
//
// Parameters:
//   - reason: Machine-readable reason code (e.g., "InvalidConfiguration")
//   - message: Human-readable description for users
//   - cause: Underlying error that caused this issue (may be nil)
func NewInvalidSpecError(reason, message string, cause error) error {
	return &stateEngineError{
		err:     cause,
		cat:     ErrorCategoryInvalidSpec,
		reason:  reason,
		message: message,
	}
}

// IsStateEngineError returns true if the error is a StateEngineError.
func IsStateEngineError(err error) bool {
	var se StateEngineError
	return errors.As(err, &se)
}

// ErrorSummary aggregates errors and workload issues for the state engine.
type ErrorSummary struct {
	InfrastructureErrors []StateEngineError
	AuthErrors           []StateEngineError
	MissingDeps          []StateEngineError
	InvalidSpecs         []StateEngineError

	// UnclassifiedErrors contains errors that are not StateEngineErrors or have
	// ErrorCategoryUnknown. These typically indicate bugs in error construction
	// or unexpected error types that need investigation.
	UnclassifiedErrors []error

	WorkloadIssues []WorkloadIssue
}

// HasInfrastructureError returns true if any infrastructure errors are present.
func (es ErrorSummary) HasInfrastructureError() bool {
	return len(es.InfrastructureErrors) > 0
}

// HasAuthError returns true if any authentication/authorization errors are present.
func (es ErrorSummary) HasAuthError() bool {
	return len(es.AuthErrors) > 0
}

// HasMissingDependency returns true if any missing dependency errors are present.
func (es ErrorSummary) HasMissingDependency() bool {
	return len(es.MissingDeps) > 0
}

// HasInvalidSpec returns true if any invalid specification errors are present.
func (es ErrorSummary) HasInvalidSpec() bool {
	return len(es.InvalidSpecs) > 0
}

// HasUnclassifiedErrors returns true if any unclassified errors are present.
func (es ErrorSummary) HasUnclassifiedErrors() bool {
	return len(es.UnclassifiedErrors) > 0
}

// HasAnyErrors returns true if any errors (categorized or unclassified) are present.
func (es ErrorSummary) HasAnyErrors() bool {
	return es.HasInfrastructureError() ||
		es.HasAuthError() ||
		es.HasMissingDependency() ||
		es.HasInvalidSpec() ||
		es.HasUnclassifiedErrors()
}

// BuildErrorSummary walks over a list of errors and classifies StateEngineErrors
// into their respective categories. This allows controllers to aggregate errors
// from multiple operations and make decisions about status conditions based on
// error categories.
//
// Non-StateEngineErrors and errors with ErrorCategoryUnknown are collected in
// the UnclassifiedErrors field. The caller can inspect this field to determine
// how to handle unexpected errors (e.g., fail reconciliation, log warnings, etc.).
//
// Parameters:
//   - errs: List of errors to analyze (may contain nil, non-StateEngineErrors, or wrapped errors)
//
// Returns:
//   - ErrorSummary with categorized state engine errors and unclassified errors
func BuildErrorSummary(errs []error) ErrorSummary {
	summary := ErrorSummary{
		InfrastructureErrors: []StateEngineError{},
		AuthErrors:           []StateEngineError{},
		MissingDeps:          []StateEngineError{},
		InvalidSpecs:         []StateEngineError{},
		UnclassifiedErrors:   []error{},
		WorkloadIssues:       []WorkloadIssue{},
	}

	// Track which concrete StateEngineError instances we've already seen
	// to avoid duplicates when the same error appears multiple times in a chain.
	seen := make(map[*stateEngineError]bool)

	for _, err := range errs {
		if err == nil {
			continue
		}

		hasStateEngineError := false

		// Walk the error chain to find all state engine errors
		walkErrors(err, func(e error) {
			// Check if this specific error is a StateEngineError
			if concreteErr, ok := e.(*stateEngineError); ok {
				// Skip if we've already processed this exact error instance
				if seen[concreteErr] {
					return
				}
				seen[concreteErr] = true
				hasStateEngineError = true

				switch concreteErr.Category() {
				case ErrorCategoryInfrastructure:
					summary.InfrastructureErrors = append(summary.InfrastructureErrors, concreteErr)
				case ErrorCategoryAuth:
					summary.AuthErrors = append(summary.AuthErrors, concreteErr)
				case ErrorCategoryMissingDependency:
					summary.MissingDeps = append(summary.MissingDeps, concreteErr)
				case ErrorCategoryInvalidSpec:
					summary.InvalidSpecs = append(summary.InvalidSpecs, concreteErr)
				case ErrorCategoryUnknown:
					// Unknown category indicates a bug in error construction.
					// Add to unclassified to surface the issue.
					summary.UnclassifiedErrors = append(summary.UnclassifiedErrors, e)
				}
			}
		})

		// If the error chain contains no state engine errors, it's a plain error
		// that should not be swallowed.
		if !hasStateEngineError {
			summary.UnclassifiedErrors = append(summary.UnclassifiedErrors, err)
		}
	}

	return summary
}

// walkErrors traverses an error chain, calling fn for each error encountered.
// This handles both simple wrapped errors (via Unwrap()) and joined errors
// (from errors.Join()). The function ensures all errors in a chain are visited,
// regardless of how they were combined.
//
// Parameters:
//   - err: The error to traverse (can be wrapped or joined)
//   - fn: Function to call for each error in the chain
func walkErrors(err error, fn func(error)) {
	if err == nil {
		return
	}

	// Call the function for the current error
	fn(err)

	// Check if this error wraps multiple errors (errors.Join pattern)
	type unwrapper interface {
		Unwrap() []error
	}
	if u, ok := err.(unwrapper); ok {
		for _, e := range u.Unwrap() {
			walkErrors(e, fn)
		}
		return
	}

	// Check if this error wraps a single error (fmt.Errorf %w pattern)
	if e := errors.Unwrap(err); e != nil {
		walkErrors(e, fn)
	}
}
