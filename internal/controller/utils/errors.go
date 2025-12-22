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

import (
	"errors"
	"net"
	"syscall"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// ErrorCategory classifies high-level error semantics for the state engine.
type ErrorCategory int

const (
	ErrorCategoryUnknown ErrorCategory = iota
	ErrorCategoryInfrastructure
	ErrorCategoryAuth
	ErrorCategoryMissingDownstreamDependency
	ErrorCategoryMissingUpstreamDependency
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
	case ErrorCategoryMissingDownstreamDependency:
		return "MissingDependency"
	case ErrorCategoryMissingUpstreamDependency:
		return "MissingReference"
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
func NewInfrastructureError(reason, message string, cause error) StateEngineError {
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
func NewAuthError(reason, message string, cause error) StateEngineError {
	return &stateEngineError{
		err:     cause,
		cat:     ErrorCategoryAuth,
		reason:  reason,
		message: message,
	}
}

// NewMissingDownstreamDependencyError creates an error for missing internal dependencies
// that the controller is waiting for (e.g., a template being created, a pod starting).
// These are transient states that should self-heal - the controller will keep progressing.
//
// Parameters:
//   - reason: Machine-readable reason code (e.g., "TemplateNotReady")
//   - message: Human-readable description for users
//   - cause: Underlying error that caused this issue (may be nil)
func NewMissingDownstreamDependencyError(reason, message string, cause error) StateEngineError {
	return &stateEngineError{
		err:     cause,
		cat:     ErrorCategoryMissingDownstreamDependency,
		reason:  reason,
		message: message,
	}
}

// NewMissingUpstreamDependencyError creates an error for user-referenced resources that don't exist
// (e.g., a runtimeConfigName that points to a non-existent config, a secret reference).
// These are configuration errors that require user intervention - the spec is valid but
// the referenced resource is missing. Sets ConfigValid=False.
//
// Parameters:
//   - reason: Machine-readable reason code (e.g., "ConfigNotFound")
//   - message: Human-readable description for users
//   - cause: Underlying error that caused this issue (may be nil)
func NewMissingUpstreamDependencyError(reason, message string, cause error) StateEngineError {
	return &stateEngineError{
		err:     cause,
		cat:     ErrorCategoryMissingUpstreamDependency,
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
func NewInvalidSpecError(reason, message string, cause error) StateEngineError {
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

// CategorizeError inspects a raw error and categorizes it as a StateEngineError.
// This function performs deep inspection to determine the error category:
// - Kubernetes API errors (NotFound, Forbidden, Unauthorized, etc.)
// - Network errors (connection refused, timeout, DNS failures)
// - HTTP status codes (401, 403, 5xx)
//
// This is the SINGLE place where error categorization happens.
func CategorizeError(err error) StateEngineError {
	if err == nil {
		return nil
	}

	// If already a StateEngineError, return as-is
	var se StateEngineError
	if errors.As(err, &se) {
		return se
	}

	// Kubernetes API errors
	if statusErr := apierrors.APIStatus(nil); errors.As(err, &statusErr) {
		status := statusErr.Status()

		switch {
		case apierrors.IsNotFound(err):
			return NewMissingDownstreamDependencyError(
				"NotFound",
				"Resource not found",
				err,
			)

		case apierrors.IsUnauthorized(err):
			return NewAuthError(
				"Unauthorized",
				"Authentication required or invalid credentials",
				err,
			)

		case apierrors.IsForbidden(err):
			return NewAuthError(
				"Forbidden",
				"Insufficient permissions to access resource",
				err,
			)

		case apierrors.IsInvalid(err):
			return NewInvalidSpecError(
				"InvalidSpec",
				"Resource specification is invalid",
				err,
			)

		case apierrors.IsAlreadyExists(err):
			return NewInvalidSpecError(
				"AlreadyExists",
				"Resource already exists",
				err,
			)

		case apierrors.IsConflict(err):
			return NewInvalidSpecError(
				"Conflict",
				"Resource conflict - version mismatch or concurrent modification",
				err,
			)

		case apierrors.IsServerTimeout(err), apierrors.IsTimeout(err):
			return NewInfrastructureError(
				"Timeout",
				"Request timed out",
				err,
			)

		case apierrors.IsServiceUnavailable(err), apierrors.IsInternalError(err):
			return NewInfrastructureError(
				"ServiceUnavailable",
				"Kubernetes API server unavailable or internal error",
				err,
			)

		case apierrors.IsTooManyRequests(err):
			return NewInfrastructureError(
				"RateLimited",
				"Too many requests - rate limited",
				err,
			)

		default:
			// Check HTTP status code
			if status.Code >= 500 {
				return NewInfrastructureError(
					"ServerError",
					"Server error (5xx)",
					err,
				)
			}
			if status.Code >= 400 && status.Code < 500 {
				return NewInvalidSpecError(
					"ClientError",
					"Client error (4xx)",
					err,
				)
			}
		}
	}

	// Network-level errors
	if errors.Is(err, syscall.ECONNREFUSED) {
		return NewInfrastructureError(
			"ConnectionRefused",
			"Connection refused - service may be down",
			err,
		)
	}

	if errors.Is(err, syscall.ETIMEDOUT) {
		return NewInfrastructureError(
			"NetworkTimeout",
			"Network timeout",
			err,
		)
	}

	if errors.Is(err, syscall.ECONNRESET) {
		return NewInfrastructureError(
			"ConnectionReset",
			"Connection reset by peer",
			err,
		)
	}

	// DNS errors - use Go's structured net.DNSError type
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return NewInfrastructureError(
			"DNSFailure",
			"DNS resolution failed for "+dnsErr.Name,
			err,
		)
	}

	// Generic network operation errors (dial, read, write failures)
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return NewInfrastructureError(
			"NetworkError",
			"Network operation failed: "+opErr.Op,
			err,
		)
	}

	// Default: unclassified error - categorize as unknown infrastructure
	// This ensures we never return nil, but indicates investigation needed
	return &stateEngineError{
		err:     err,
		cat:     ErrorCategoryUnknown,
		reason:  "UnknownError",
		message: err.Error(),
	}
}
