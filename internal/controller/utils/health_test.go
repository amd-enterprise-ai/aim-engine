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

	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

func TestDeriveStateFromErrors(t *testing.T) {
	tests := []struct {
		name     string
		errs     []error
		expected constants.AIMStatus
	}{
		{
			name:     "No errors returns Ready",
			errs:     []error{},
			expected: constants.AIMStatusReady,
		},
		{
			name:     "Nil errors returns Ready",
			errs:     nil,
			expected: constants.AIMStatusReady,
		},
		{
			name: "InvalidSpec error returns Degraded",
			errs: []error{
				NewInvalidSpecError("InvalidConfig", "Invalid configuration", nil),
			},
			expected: constants.AIMStatusDegraded,
		},
		{
			name: "Auth error returns Degraded",
			errs: []error{
				NewAuthError("Forbidden", "Insufficient permissions", nil),
			},
			expected: constants.AIMStatusDegraded,
		},
		{
			name: "Infrastructure error returns Degraded",
			errs: []error{
				NewInfrastructureError("NetworkTimeout", "Network timeout", nil),
			},
			expected: constants.AIMStatusDegraded,
		},
		{
			name: "MissingDependency error returns Progressing",
			errs: []error{
				NewMissingDownstreamDependencyError("NotFound", "Model not found", nil),
			},
			expected: constants.AIMStatusProgressing,
		},
		{
			name: "Multiple errors - worst status wins (Degraded > Progressing)",
			errs: []error{
				NewInfrastructureError("Timeout", "Timeout", nil),
				NewInvalidSpecError("Invalid", "Invalid", nil),
			},
			expected: constants.AIMStatusDegraded,
		},
		{
			name: "Multiple errors - auth and missing dep returns Degraded",
			errs: []error{
				NewMissingDownstreamDependencyError("NotFound", "Not found", nil),
				NewAuthError("Forbidden", "Forbidden", nil),
			},
			expected: constants.AIMStatusDegraded,
		},
		{
			name: "Multiple errors - worst status wins (Degraded > Progressing)",
			errs: []error{
				NewMissingDownstreamDependencyError("NotFound", "Not found", nil),
				NewInfrastructureError("Timeout", "Timeout", nil),
			},
			expected: constants.AIMStatusDegraded,
		},
		{
			name: "Unknown error category returns Progressing",
			errs: []error{
				errors.New("some random error"),
			},
			expected: constants.AIMStatusProgressing,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeriveStateFromErrors(tt.errs)
			if got != tt.expected {
				t.Errorf("DeriveStateFromErrors() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestComponentHealth_GetState(t *testing.T) {
	ready := constants.AIMStatusReady

	tests := []struct {
		name     string
		ch       ComponentHealth
		expected constants.AIMStatus
	}{
		{
			name: "Explicit state is returned",
			ch: ComponentHealth{
				Component: "Model",
				State:     ready,
			},
			expected: constants.AIMStatusReady,
		},
		{
			name: "Empty state with no errors derives Ready",
			ch: ComponentHealth{
				Component: "Model",
				Errors:    []error{},
			},
			expected: constants.AIMStatusReady,
		},
		{
			name: "Empty state with InvalidSpec error derives Degraded",
			ch: ComponentHealth{
				Component: "Model",
				Errors: []error{
					NewInvalidSpecError("Invalid", "Invalid", nil),
				},
			},
			expected: constants.AIMStatusDegraded,
		},
		{
			name: "Explicit state overrides errors",
			ch: ComponentHealth{
				Component: "Model",
				State:     ready,
				Errors: []error{
					NewInvalidSpecError("Invalid", "Invalid", nil),
				},
			},
			expected: constants.AIMStatusReady,
		},
		{
			name: "Empty state with Infrastructure error derives Degraded",
			ch: ComponentHealth{
				Component: "Model",
				Errors: []error{
					NewInfrastructureError("Timeout", "Timeout", nil),
				},
			},
			expected: constants.AIMStatusDegraded,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.ch.GetState()
			if got != tt.expected {
				t.Errorf("GetState() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestComponentHealth_GetReason(t *testing.T) {
	tests := []struct {
		name     string
		ch       ComponentHealth
		expected string
	}{
		{
			name: "Explicit reason is returned",
			ch: ComponentHealth{
				Component: "Model",
				Reason:    "CustomReason",
			},
			expected: "CustomReason",
		},
		{
			name: "Empty reason with no errors returns Ready",
			ch: ComponentHealth{
				Component: "Model",
				Reason:    "",
				Errors:    []error{},
			},
			expected: "Ready",
		},
		{
			name: "Empty reason with error derives from categorized error",
			ch: ComponentHealth{
				Component: "Model",
				Reason:    "",
				Errors: []error{
					NewInvalidSpecError("InvalidConfig", "Configuration is invalid", nil),
				},
			},
			expected: "InvalidConfig",
		},
		{
			name: "Explicit reason overrides errors",
			ch: ComponentHealth{
				Component: "Model",
				Reason:    "CustomReason",
				Errors: []error{
					NewInvalidSpecError("InvalidConfig", "Configuration is invalid", nil),
				},
			},
			expected: "CustomReason",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.ch.GetReason()
			if got != tt.expected {
				t.Errorf("GetReason() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestComponentHealth_GetMessage(t *testing.T) {
	tests := []struct {
		name     string
		ch       ComponentHealth
		expected string
	}{
		{
			name: "Explicit message is returned",
			ch: ComponentHealth{
				Component: "Model",
				Message:   "Custom message",
			},
			expected: "Custom message",
		},
		{
			name: "Empty message with no errors returns empty",
			ch: ComponentHealth{
				Component: "Model",
				Message:   "",
				Errors:    []error{},
			},
			expected: "",
		},
		{
			name: "Empty message with error derives from categorized error",
			ch: ComponentHealth{
				Component: "Model",
				Message:   "",
				Errors: []error{
					NewInvalidSpecError("InvalidConfig", "Configuration is invalid", nil),
				},
			},
			expected: "Configuration is invalid",
		},
		{
			name: "Explicit message overrides errors",
			ch: ComponentHealth{
				Component: "Model",
				Message:   "Custom message",
				Errors: []error{
					NewInvalidSpecError("InvalidConfig", "Configuration is invalid", nil),
				},
			},
			expected: "Custom message",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.ch.GetMessage()
			if got != tt.expected {
				t.Errorf("GetMessage() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestComponentHealth_FullyDerived(t *testing.T) {
	// Test case: ComponentHealth with only errors (no explicit state/reason/message)
	ch := ComponentHealth{
		Component: "ImageRegistry",
		Errors: []error{
			NewInvalidSpecError("InvalidCredentials", "Image pull secret is invalid", nil),
		},
	}

	// All fields should be derived from the error
	if got := ch.GetState(); got != constants.AIMStatusDegraded {
		t.Errorf("GetState() = %v, want %v", got, constants.AIMStatusDegraded)
	}

	if got := ch.GetReason(); got != "InvalidCredentials" {
		t.Errorf("GetReason() = %v, want InvalidCredentials", got)
	}

	if got := ch.GetMessage(); got != "Image pull secret is invalid" {
		t.Errorf("GetMessage() = %v, want 'Image pull secret is invalid'", got)
	}
}

func TestComponentHealth_ExplicitOverrides(t *testing.T) {
	// Test case: Explicit state/reason/message overrides error-derived values
	progressing := constants.AIMStatusProgressing
	ch := ComponentHealth{
		Component: "Model",
		State:     progressing,
		Reason:    "NotReady",
		Message:   "Waiting for model to become ready",
		Errors: []error{
			NewMissingDownstreamDependencyError("NotReady", "Model not ready", nil),
		},
	}

	if got := ch.GetState(); got != constants.AIMStatusProgressing {
		t.Errorf("GetState() = %v, want %v", got, constants.AIMStatusProgressing)
	}

	if got := ch.GetReason(); got != "NotReady" {
		t.Errorf("GetReason() = %v, want NotReady", got)
	}

	if got := ch.GetMessage(); got != "Waiting for model to become ready" {
		t.Errorf("GetMessage() = %v, want 'Waiting for model to become ready'", got)
	}
}

func TestComponentHealth_ChildRef(t *testing.T) {
	// Test that ChildRef can be used for fine-grained tracking
	ch := ComponentHealth{
		Component: "Workload",
		ChildRef: &ChildRef{
			Kind:      "Pod",
			Namespace: "default",
			Name:      "foo-123",
		},
		Errors: []error{
			NewInvalidSpecError("ImagePullBackOff", "Failed to pull container image", nil),
		},
	}

	if ch.ChildRef == nil {
		t.Fatal("ChildRef should not be nil")
	}

	if ch.ChildRef.Kind != "Pod" {
		t.Errorf("ChildRef.Kind = %v, want Pod", ch.ChildRef.Kind)
	}

	if ch.ChildRef.Name != "foo-123" {
		t.Errorf("ChildRef.Name = %v, want foo-123", ch.ChildRef.Name)
	}
}
