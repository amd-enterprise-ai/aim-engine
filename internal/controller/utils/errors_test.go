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
	"testing"
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
			category: ErrorCategoryMissingDependency,
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
	err := NewMissingDependencyError("SecretNotFound", "Required secret 'api-key' not found", cause)

	var stateErr StateEngineError
	if !errors.As(err, &stateErr) {
		t.Fatal("NewMissingDependencyError should return a StateEngineError")
	}

	if stateErr.Category() != ErrorCategoryMissingDependency {
		t.Errorf("Category() = %v, want %v", stateErr.Category(), ErrorCategoryMissingDependency)
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

func TestErrorSummaryHasMethods(t *testing.T) {
	summary := ErrorSummary{
		InfrastructureErrors: []StateEngineError{
			&stateEngineError{cat: ErrorCategoryInfrastructure},
		},
		AuthErrors: []StateEngineError{
			&stateEngineError{cat: ErrorCategoryAuth},
		},
		MissingDeps: []StateEngineError{
			&stateEngineError{cat: ErrorCategoryMissingDependency},
		},
		InvalidSpecs: []StateEngineError{
			&stateEngineError{cat: ErrorCategoryInvalidSpec},
		},
		UnclassifiedErrors: []error{
			errors.New("unclassified"),
		},
	}

	if !summary.HasInfrastructureError() {
		t.Error("HasInfrastructureError() = false, want true")
	}
	if !summary.HasAuthError() {
		t.Error("HasAuthError() = false, want true")
	}
	if !summary.HasMissingDependency() {
		t.Error("HasMissingDependency() = false, want true")
	}
	if !summary.HasInvalidSpec() {
		t.Error("HasInvalidSpec() = false, want true")
	}
	if !summary.HasUnclassifiedErrors() {
		t.Error("HasUnclassifiedErrors() = false, want true")
	}
	if !summary.HasAnyErrors() {
		t.Error("HasAnyErrors() = false, want true")
	}

	emptySummary := ErrorSummary{}
	if emptySummary.HasInfrastructureError() {
		t.Error("Empty summary HasInfrastructureError() = true, want false")
	}
	if emptySummary.HasAnyErrors() {
		t.Error("Empty summary HasAnyErrors() = true, want false")
	}
}

func TestBuildErrorSummary(t *testing.T) {
	t.Run("Empty list", func(t *testing.T) {
		summary := BuildErrorSummary([]error{})
		if summary.HasAnyErrors() {
			t.Error("Expected empty summary")
		}
	})

	t.Run("Nil errors ignored", func(t *testing.T) {
		summary := BuildErrorSummary([]error{nil, nil})
		if summary.HasAnyErrors() {
			t.Error("Expected empty summary when all errors are nil")
		}
	})

	t.Run("Single categorized error", func(t *testing.T) {
		infraErr := NewInfrastructureError("Test", "test", nil)
		summary := BuildErrorSummary([]error{infraErr})

		if !summary.HasInfrastructureError() {
			t.Error("Expected infrastructure error")
		}
		if len(summary.InfrastructureErrors) != 1 {
			t.Errorf("Expected 1 infrastructure error, got %d", len(summary.InfrastructureErrors))
		}
		if summary.HasAuthError() || summary.HasMissingDependency() || summary.HasInvalidSpec() {
			t.Error("Expected only infrastructure errors")
		}
	})

	t.Run("Multiple categorized errors", func(t *testing.T) {
		errs := []error{
			NewInfrastructureError("Infra1", "test1", nil),
			NewAuthError("Auth1", "test2", nil),
			NewMissingDependencyError("Dep1", "test3", nil),
			NewInvalidSpecError("Spec1", "test4", nil),
			NewInfrastructureError("Infra2", "test5", nil),
		}
		summary := BuildErrorSummary(errs)

		if len(summary.InfrastructureErrors) != 2 {
			t.Errorf("Expected 2 infrastructure errors, got %d", len(summary.InfrastructureErrors))
		}
		if len(summary.AuthErrors) != 1 {
			t.Errorf("Expected 1 auth error, got %d", len(summary.AuthErrors))
		}
		if len(summary.MissingDeps) != 1 {
			t.Errorf("Expected 1 missing dependency error, got %d", len(summary.MissingDeps))
		}
		if len(summary.InvalidSpecs) != 1 {
			t.Errorf("Expected 1 invalid spec error, got %d", len(summary.InvalidSpecs))
		}
		if summary.HasUnclassifiedErrors() {
			t.Error("Expected no unclassified errors")
		}
	})

	t.Run("Wrapped StateEngineError", func(t *testing.T) {
		wrappedErr := fmt.Errorf("wrapped: %w", NewAuthError("Auth", "auth failed", nil))
		summary := BuildErrorSummary([]error{wrappedErr})

		if !summary.HasAuthError() {
			t.Error("Expected auth error to be found in wrapped error")
		}
		if len(summary.AuthErrors) != 1 {
			t.Errorf("Expected 1 auth error, got %d", len(summary.AuthErrors))
		}
	})

	t.Run("Joined errors", func(t *testing.T) {
		err1 := NewInfrastructureError("Infra", "test1", nil)
		err2 := NewAuthError("Auth", "test2", nil)
		joined := errors.Join(err1, err2)

		summary := BuildErrorSummary([]error{joined})

		if len(summary.InfrastructureErrors) != 1 {
			t.Errorf("Expected 1 infrastructure error, got %d", len(summary.InfrastructureErrors))
		}
		if len(summary.AuthErrors) != 1 {
			t.Errorf("Expected 1 auth error, got %d", len(summary.AuthErrors))
		}
	})

	t.Run("Plain errors become unclassified", func(t *testing.T) {
		plainErr := errors.New("plain error")
		summary := BuildErrorSummary([]error{plainErr})

		if !summary.HasUnclassifiedErrors() {
			t.Error("Expected plain error to be unclassified")
		}
		if len(summary.UnclassifiedErrors) != 1 {
			t.Errorf("Expected 1 unclassified error, got %d", len(summary.UnclassifiedErrors))
		}
	})

	t.Run("ErrorCategoryUnknown becomes unclassified", func(t *testing.T) {
		unknownErr := &stateEngineError{
			cat:     ErrorCategoryUnknown,
			reason:  "Unknown",
			message: "unknown error",
		}
		summary := BuildErrorSummary([]error{unknownErr})

		if !summary.HasUnclassifiedErrors() {
			t.Error("Expected unknown category error to be unclassified")
		}
		if len(summary.UnclassifiedErrors) != 1 {
			t.Errorf("Expected 1 unclassified error, got %d", len(summary.UnclassifiedErrors))
		}
	})

	t.Run("Mixed error types", func(t *testing.T) {
		errs := []error{
			NewInfrastructureError("Infra", "test", nil),
			errors.New("plain error"),
			NewAuthError("Auth", "test", nil),
			nil,
			&stateEngineError{cat: ErrorCategoryUnknown, reason: "Unknown", message: "test"},
		}
		summary := BuildErrorSummary(errs)

		if len(summary.InfrastructureErrors) != 1 {
			t.Errorf("Expected 1 infrastructure error, got %d", len(summary.InfrastructureErrors))
		}
		if len(summary.AuthErrors) != 1 {
			t.Errorf("Expected 1 auth error, got %d", len(summary.AuthErrors))
		}
		if len(summary.UnclassifiedErrors) != 2 {
			t.Errorf("Expected 2 unclassified errors (plain + unknown), got %d", len(summary.UnclassifiedErrors))
		}
	})

	t.Run("Deeply nested wrapped errors", func(t *testing.T) {
		baseErr := NewMissingDependencyError("Dep", "missing", nil)
		wrapped1 := fmt.Errorf("level 1: %w", baseErr)
		wrapped2 := fmt.Errorf("level 2: %w", wrapped1)
		wrapped3 := fmt.Errorf("level 3: %w", wrapped2)

		summary := BuildErrorSummary([]error{wrapped3})

		if len(summary.MissingDeps) != 1 {
			t.Errorf("Expected 1 missing dependency error in nested chain, got %d", len(summary.MissingDeps))
		}
	})

	t.Run("StateEngineError with wrapped cause", func(t *testing.T) {
		cause := errors.New("underlying cause")
		stateErr := NewInfrastructureError("Infra", "failed", cause)

		summary := BuildErrorSummary([]error{stateErr})

		if len(summary.InfrastructureErrors) != 1 {
			t.Errorf("Expected 1 infrastructure error, got %d", len(summary.InfrastructureErrors))
		}
		// The cause should not be treated as a separate unclassified error
		// because it's wrapped inside a StateEngineError
		if summary.HasUnclassifiedErrors() {
			t.Error("Expected no unclassified errors when cause is wrapped in StateEngineError")
		}
	})
}

func TestWalkErrors(t *testing.T) {
	t.Run("Nil error", func(t *testing.T) {
		called := false
		walkErrors(nil, func(e error) {
			called = true
		})
		if called {
			t.Error("Function should not be called for nil error")
		}
	})

	t.Run("Simple error", func(t *testing.T) {
		err := errors.New("test")
		count := 0
		walkErrors(err, func(e error) {
			count++
		})
		if count != 1 {
			t.Errorf("Expected function called 1 time, got %d", count)
		}
	})

	t.Run("Wrapped error", func(t *testing.T) {
		base := errors.New("base")
		wrapped := fmt.Errorf("wrapped: %w", base)

		var collected []error
		walkErrors(wrapped, func(e error) {
			collected = append(collected, e)
		})

		if len(collected) != 2 {
			t.Errorf("Expected 2 errors in chain, got %d", len(collected))
		}
	})

	t.Run("Joined errors", func(t *testing.T) {
		err1 := errors.New("error 1")
		err2 := errors.New("error 2")
		err3 := errors.New("error 3")
		joined := errors.Join(err1, err2, err3)

		var collected []error
		walkErrors(joined, func(e error) {
			collected = append(collected, e)
		})

		// Should visit joined error itself + 3 individual errors
		if len(collected) != 4 {
			t.Errorf("Expected 4 errors (joined + 3 individuals), got %d", len(collected))
		}
	})

	t.Run("Complex nested structure", func(t *testing.T) {
		err1 := errors.New("error 1")
		err2 := errors.New("error 2")
		joined := errors.Join(err1, err2)
		wrapped := fmt.Errorf("wrapper: %w", joined)

		count := 0
		walkErrors(wrapped, func(e error) {
			count++
		})

		// Should visit all errors in the tree
		if count < 4 {
			t.Errorf("Expected at least 4 errors visited, got %d", count)
		}
	})
}
