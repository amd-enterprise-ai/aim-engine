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
	"testing"
)

// State engine decision tests - simplified to test the logic without Pipeline type constraints
// The processStateEngine logic categorizes errors and returns decisions.
// These tests verify the error categorization → decision behavior.

func TestStateEngineDecision_ErrorCategorization(t *testing.T) {
	tests := []struct {
		name          string
		errors        []error
		shouldApply   bool
		shouldRequeue bool
		requeueNonNil bool
		description   string
	}{
		{
			name:          "No errors - should apply",
			errors:        []error{},
			shouldApply:   true,
			shouldRequeue: false,
			requeueNonNil: false,
			description:   "Healthy state should allow apply without requeue",
		},
		{
			name: "Infrastructure error - requeue but don't apply",
			errors: []error{
				NewInfrastructureError("NetworkTimeout", "Network timeout", errors.New("timeout")),
			},
			shouldApply:   false,
			shouldRequeue: true,
			requeueNonNil: true,
			description:   "Infrastructure errors trigger requeue for retry",
		},
		{
			name: "Auth error - don't apply, don't requeue",
			errors: []error{
				NewAuthError("Forbidden", "Insufficient permissions", nil),
			},
			shouldApply:   false,
			shouldRequeue: false,
			requeueNonNil: false,
			description:   "Auth errors require user intervention, not retry",
		},
		{
			name: "Invalid spec error - don't apply, don't requeue",
			errors: []error{
				NewInvalidSpecError("InvalidConfig", "Invalid configuration", nil),
			},
			shouldApply:   false,
			shouldRequeue: false,
			requeueNonNil: false,
			description:   "Invalid spec errors require user fix, not retry",
		},
		{
			name: "Missing dependency - should apply (waiting state)",
			errors: []error{
				NewMissingDownstreamDependencyError("NotFound", "Model not found", nil),
			},
			shouldApply:   true,
			shouldRequeue: false,
			requeueNonNil: false,
			description:   "Missing dependencies are waiting states, not blocking",
		},
		{
			name: "Multiple errors - infrastructure takes precedence",
			errors: []error{
				NewAuthError("Forbidden", "Forbidden", nil),
				NewInfrastructureError("Timeout", "Timeout", nil),
			},
			shouldApply:   false,
			shouldRequeue: true,
			requeueNonNil: true,
			description:   "Infrastructure errors override auth/spec errors for requeue",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Categorize errors and count by category
			var hasInfraError, hasAuthError, hasInvalidSpec bool

			for _, err := range tt.errors {
				if err == nil {
					continue
				}
				categorized := CategorizeError(err)
				switch categorized.Category() {
				case ErrorCategoryInfrastructure:
					hasInfraError = true
				case ErrorCategoryAuth:
					hasAuthError = true
				case ErrorCategoryInvalidSpec:
					hasInvalidSpec = true
				}
			}

			// Simulate state engine decision logic (from processStateEngine)
			var shouldApply, shouldRequeue bool
			var requeueError error

			if hasInfraError {
				// Infrastructure errors → requeue, don't apply
				shouldApply = false
				shouldRequeue = true
				requeueError = tt.errors[len(tt.errors)-1] // Simplified - real code joins all infra errors
			} else {
				// No infrastructure errors
				shouldApply = !hasAuthError && !hasInvalidSpec
				shouldRequeue = false
			}

			// Verify expectations
			if shouldApply != tt.shouldApply {
				t.Errorf("%s: shouldApply = %v, want %v", tt.description, shouldApply, tt.shouldApply)
			}

			if shouldRequeue != tt.shouldRequeue {
				t.Errorf("%s: shouldRequeue = %v, want %v", tt.description, shouldRequeue, tt.shouldRequeue)
			}

			requeueNonNil := requeueError != nil
			if requeueNonNil != tt.requeueNonNil {
				t.Errorf("%s: requeueError non-nil = %v, want %v", tt.description, requeueNonNil, tt.requeueNonNil)
			}
		})
	}
}
