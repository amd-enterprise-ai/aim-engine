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
	"fmt"
	"net"
	"syscall"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestErrorCategoryString(t *testing.T) {
	tests := []struct {
		name     string
		category ErrorCategory
		want     string
	}{
		{
			name:     "Infrastructure",
			category: ErrorCategoryInfrastructure,
			want:     "Infrastructure",
		},
		{
			name:     "Auth",
			category: ErrorCategoryAuth,
			want:     "Auth",
		},
		{
			name:     "MissingDependency",
			category: ErrorCategoryMissingDownstreamDependency,
			want:     "MissingDependency",
		},
		{
			name:     "InvalidSpec",
			category: ErrorCategoryInvalidSpec,
			want:     "InvalidSpec",
		},
		{
			name:     "Unknown",
			category: ErrorCategoryUnknown,
			want:     "Unknown",
		},
		{
			name:     "Invalid value defaults to Unknown",
			category: ErrorCategory(999),
			want:     "Unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.category.String(); got != tt.want {
				t.Errorf("ErrorCategory.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewInfrastructureError(t *testing.T) {
	cause := errors.New("network timeout")
	err := NewInfrastructureError("NetworkFailure", "Failed to connect to API server", cause)

	var stateErr StateEngineError
	if !errors.As(err, &stateErr) {
		t.Fatal("NewInfrastructureError should return a StateEngineError")
	}

	if stateErr.Category() != ErrorCategoryInfrastructure {
		t.Errorf("Category() = %v, want %v", stateErr.Category(), ErrorCategoryInfrastructure)
	}

	if stateErr.Reason() != "NetworkFailure" {
		t.Errorf("Reason() = %v, want NetworkFailure", stateErr.Reason())
	}

	if stateErr.UserMessage() != "Failed to connect to API server" {
		t.Errorf("UserMessage() = %v, want 'Failed to connect to API server'", stateErr.UserMessage())
	}

	if !errors.Is(err, cause) {
		t.Error("Error chain should contain the cause")
	}
}

func TestNewAuthError(t *testing.T) {
	cause := errors.New("permission denied")
	err := NewAuthError("InsufficientPermissions", "Service account lacks required permissions", cause)

	var stateErr StateEngineError
	if !errors.As(err, &stateErr) {
		t.Fatal("NewAuthError should return a StateEngineError")
	}

	if stateErr.Category() != ErrorCategoryAuth {
		t.Errorf("Category() = %v, want %v", stateErr.Category(), ErrorCategoryAuth)
	}

	if stateErr.Reason() != "InsufficientPermissions" {
		t.Errorf("Reason() = %v, want InsufficientPermissions", stateErr.Reason())
	}
}

func TestNewMissingDependencyError(t *testing.T) {
	cause := errors.New("not found")
	err := NewMissingDownstreamDependencyError("SecretNotFound", "Required secret 'api-key' not found", cause)

	var stateErr StateEngineError
	if !errors.As(err, &stateErr) {
		t.Fatal("NewMissingDownstreamDependencyError should return a StateEngineError")
	}

	if stateErr.Category() != ErrorCategoryMissingDownstreamDependency {
		t.Errorf("Category() = %v, want %v", stateErr.Category(), ErrorCategoryMissingDownstreamDependency)
	}

	if stateErr.Reason() != "SecretNotFound" {
		t.Errorf("Reason() = %v, want SecretNotFound", stateErr.Reason())
	}
}

func TestNewInvalidSpecError(t *testing.T) {
	cause := errors.New("validation failed")
	err := NewInvalidSpecError("InvalidConfiguration", "Replicas must be positive", cause)

	var stateErr StateEngineError
	if !errors.As(err, &stateErr) {
		t.Fatal("NewInvalidSpecError should return a StateEngineError")
	}

	if stateErr.Category() != ErrorCategoryInvalidSpec {
		t.Errorf("Category() = %v, want %v", stateErr.Category(), ErrorCategoryInvalidSpec)
	}

	if stateErr.Reason() != "InvalidConfiguration" {
		t.Errorf("Reason() = %v, want InvalidConfiguration", stateErr.Reason())
	}
}

func TestStateEngineErrorError(t *testing.T) {
	tests := []struct {
		name string
		err  *stateEngineError
		want string
	}{
		{
			name: "Both reason and message",
			err: &stateEngineError{
				reason:  "NetworkFailure",
				message: "Connection timeout",
			},
			want: "NetworkFailure: Connection timeout",
		},
		{
			name: "Reason only",
			err: &stateEngineError{
				reason: "NetworkFailure",
			},
			want: "NetworkFailure",
		},
		{
			name: "Message only",
			err: &stateEngineError{
				message: "Connection timeout",
			},
			want: "Connection timeout",
		},
		{
			name: "Wrapped error only",
			err: &stateEngineError{
				err: errors.New("underlying error"),
			},
			want: "underlying error",
		},
		{
			name: "Empty error",
			err:  &stateEngineError{},
			want: "unknown error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStateEngineErrorUnwrap(t *testing.T) {
	cause := errors.New("root cause")
	err := NewInfrastructureError("Test", "test message", cause)

	unwrapped := errors.Unwrap(err)
	if !errors.Is(unwrapped, cause) {
		t.Errorf("Unwrap() = %v, want %v", unwrapped, cause)
	}
}

func TestIsStateEngineError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "StateEngineError",
			err:  NewInfrastructureError("Test", "test", nil),
			want: true,
		},
		{
			name: "Wrapped StateEngineError",
			err:  fmt.Errorf("wrapped: %w", NewAuthError("Test", "test", nil)),
			want: true,
		},
		{
			name: "Plain error",
			err:  errors.New("plain error"),
			want: false,
		},
		{
			name: "Nil error",
			err:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsStateEngineError(tt.err); got != tt.want {
				t.Errorf("IsStateEngineError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCategorizeError_KubernetesAPIErrors(t *testing.T) {
	tests := []struct {
		name             string
		err              error
		expectedCategory ErrorCategory
		expectedReason   string
	}{
		{
			name: "NotFound error",
			err: &apierrors.StatusError{
				ErrStatus: metav1.Status{
					Code:   404,
					Reason: metav1.StatusReasonNotFound,
				},
			},
			expectedCategory: ErrorCategoryMissingDownstreamDependency,
			expectedReason:   "NotFound",
		},
		{
			name: "Unauthorized error",
			err: &apierrors.StatusError{
				ErrStatus: metav1.Status{
					Code:   401,
					Reason: metav1.StatusReasonUnauthorized,
				},
			},
			expectedCategory: ErrorCategoryAuth,
			expectedReason:   "Unauthorized",
		},
		{
			name: "Forbidden error",
			err: &apierrors.StatusError{
				ErrStatus: metav1.Status{
					Code:   403,
					Reason: metav1.StatusReasonForbidden,
				},
			},
			expectedCategory: ErrorCategoryAuth,
			expectedReason:   "Forbidden",
		},
		{
			name: "Invalid error",
			err: &apierrors.StatusError{
				ErrStatus: metav1.Status{
					Code:   422,
					Reason: metav1.StatusReasonInvalid,
				},
			},
			expectedCategory: ErrorCategoryInvalidSpec,
			expectedReason:   "InvalidSpec",
		},
		{
			name: "AlreadyExists error",
			err: &apierrors.StatusError{
				ErrStatus: metav1.Status{
					Code:   409,
					Reason: metav1.StatusReasonAlreadyExists,
				},
			},
			expectedCategory: ErrorCategoryInvalidSpec,
			expectedReason:   "AlreadyExists",
		},
		{
			name: "Conflict error",
			err: &apierrors.StatusError{
				ErrStatus: metav1.Status{
					Code:   409,
					Reason: metav1.StatusReasonConflict,
				},
			},
			expectedCategory: ErrorCategoryInfrastructure,
			expectedReason:   "Conflict",
		},
		{
			name: "Timeout error",
			err: &apierrors.StatusError{
				ErrStatus: metav1.Status{
					Code:   504,
					Reason: metav1.StatusReasonTimeout,
				},
			},
			expectedCategory: ErrorCategoryInfrastructure,
			expectedReason:   "Timeout",
		},
		{
			name: "ServiceUnavailable error",
			err: &apierrors.StatusError{
				ErrStatus: metav1.Status{
					Code:   503,
					Reason: metav1.StatusReasonServiceUnavailable,
				},
			},
			expectedCategory: ErrorCategoryInfrastructure,
			expectedReason:   "ServiceUnavailable",
		},
		{
			name: "TooManyRequests error",
			err: &apierrors.StatusError{
				ErrStatus: metav1.Status{
					Code:   429,
					Reason: metav1.StatusReasonTooManyRequests,
				},
			},
			expectedCategory: ErrorCategoryInfrastructure,
			expectedReason:   "RateLimited",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			categorized := CategorizeError(tt.err)
			if categorized == nil {
				t.Fatal("CategorizeError returned nil")
			}

			if categorized.Category() != tt.expectedCategory {
				t.Errorf("expected category %v, got %v", tt.expectedCategory, categorized.Category())
			}

			if categorized.Reason() != tt.expectedReason {
				t.Errorf("expected reason %q, got %q", tt.expectedReason, categorized.Reason())
			}
		})
	}
}

func TestCategorizeError_HTTPStatusCodes(t *testing.T) {
	tests := []struct {
		name             string
		statusCode       int32
		statusReason     metav1.StatusReason
		expectedCategory ErrorCategory
		expectedReason   string
	}{
		{
			name:             "500 Internal Error",
			statusCode:       500,
			statusReason:     metav1.StatusReasonInternalError,
			expectedCategory: ErrorCategoryInfrastructure,
			expectedReason:   "ServiceUnavailable",
		},
		{
			name:             "502 Bad Gateway",
			statusCode:       502,
			statusReason:     metav1.StatusReasonUnknown,
			expectedCategory: ErrorCategoryInfrastructure,
			expectedReason:   "ServerError",
		},
		{
			name:             "400 Bad Request",
			statusCode:       400,
			statusReason:     metav1.StatusReasonBadRequest,
			expectedCategory: ErrorCategoryInvalidSpec,
			expectedReason:   "ClientError",
		},
		{
			name:             "422 Invalid",
			statusCode:       422,
			statusReason:     metav1.StatusReasonInvalid,
			expectedCategory: ErrorCategoryInvalidSpec,
			expectedReason:   "InvalidSpec",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &apierrors.StatusError{
				ErrStatus: metav1.Status{
					Code:   tt.statusCode,
					Reason: tt.statusReason,
				},
			}

			categorized := CategorizeError(err)
			if categorized == nil {
				t.Fatal("CategorizeError returned nil")
			}

			if categorized.Category() != tt.expectedCategory {
				t.Errorf("expected category %v, got %v", tt.expectedCategory, categorized.Category())
			}

			if categorized.Reason() != tt.expectedReason {
				t.Errorf("expected reason %q, got %q", tt.expectedReason, categorized.Reason())
			}
		})
	}
}

func TestCategorizeError_NetworkErrors(t *testing.T) {
	tests := []struct {
		name             string
		err              error
		expectedCategory ErrorCategory
		expectedReason   string
	}{
		{
			name:             "Connection refused",
			err:              syscall.ECONNREFUSED,
			expectedCategory: ErrorCategoryInfrastructure,
			expectedReason:   "ConnectionRefused",
		},
		{
			name:             "Network timeout",
			err:              syscall.ETIMEDOUT,
			expectedCategory: ErrorCategoryInfrastructure,
			expectedReason:   "NetworkTimeout",
		},
		{
			name:             "Connection reset",
			err:              syscall.ECONNRESET,
			expectedCategory: ErrorCategoryInfrastructure,
			expectedReason:   "ConnectionReset",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			categorized := CategorizeError(tt.err)
			if categorized == nil {
				t.Fatal("CategorizeError returned nil")
			}

			if categorized.Category() != tt.expectedCategory {
				t.Errorf("expected category %v, got %v", tt.expectedCategory, categorized.Category())
			}

			if categorized.Reason() != tt.expectedReason {
				t.Errorf("expected reason %q, got %q", tt.expectedReason, categorized.Reason())
			}
		})
	}
}

func TestCategorizeError_DNSErrors(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		expectedReason string
	}{
		{
			name: "net.DNSError not found",
			err: &net.DNSError{
				Err:        "no such host",
				Name:       "example.com",
				IsNotFound: true,
			},
			expectedReason: "DNSFailure",
		},
		{
			name: "net.DNSError timeout",
			err: &net.DNSError{
				Err:       "i/o timeout",
				Name:      "example.com",
				IsTimeout: true,
			},
			expectedReason: "DNSFailure",
		},
		{
			name: "wrapped net.DNSError",
			err: fmt.Errorf("lookup failed: %w", &net.DNSError{
				Err:  "no such host",
				Name: "api.example.com",
			}),
			expectedReason: "DNSFailure",
		},
		{
			name: "net.OpError dial failure",
			err: &net.OpError{
				Op:  "dial",
				Net: "tcp",
				Err: errors.New("connection failed"),
			},
			expectedReason: "NetworkError",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			categorized := CategorizeError(tt.err)
			if categorized == nil {
				t.Fatal("CategorizeError returned nil")
			}

			if categorized.Category() != ErrorCategoryInfrastructure {
				t.Errorf("expected Infrastructure category, got %v", categorized.Category())
			}

			if categorized.Reason() != tt.expectedReason {
				t.Errorf("expected reason %s, got %q", tt.expectedReason, categorized.Reason())
			}
		})
	}
}

func TestCategorizeError_AlreadyCategorized(t *testing.T) {
	original := NewAuthError("CustomAuth", "custom auth error", nil)

	categorized := CategorizeError(original)

	if categorized != original {
		t.Error("CategorizeError should return already-categorized errors unchanged")
	}

	if categorized.Category() != ErrorCategoryAuth {
		t.Errorf("expected Auth category, got %v", categorized.Category())
	}
}

func TestCategorizeError_WrappedErrors(t *testing.T) {
	baseErr := &apierrors.StatusError{
		ErrStatus: metav1.Status{
			Code:   404,
			Reason: metav1.StatusReasonNotFound,
		},
	}
	wrappedErr := fmt.Errorf("failed to get deployment: %w", baseErr)

	categorized := CategorizeError(wrappedErr)

	if categorized.Category() != ErrorCategoryMissingDownstreamDependency {
		t.Errorf("expected MissingDependency category for wrapped NotFound, got %v", categorized.Category())
	}
}

func TestCategorizeError_NilError(t *testing.T) {
	categorized := CategorizeError(nil)
	if categorized != nil {
		t.Error("CategorizeError should return nil for nil input")
	}
}

func TestCategorizeError_UnknownError(t *testing.T) {
	err := errors.New("some random error")

	categorized := CategorizeError(err)
	if categorized == nil {
		t.Fatal("CategorizeError returned nil")
	}

	if categorized.Category() != ErrorCategoryUnknown {
		t.Errorf("expected Unknown category, got %v", categorized.Category())
	}

	if categorized.Reason() != "UnknownError" {
		t.Errorf("expected reason UnknownError, got %q", categorized.Reason())
	}
}
