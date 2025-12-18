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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

const (
	componentName = "Model"
)

func TestFetchResult_IsNotFound(t *testing.T) {
	tests := []struct {
		name     string
		fr       FetchResult[any]
		expected bool
	}{
		{
			name: "NotFound error returns true",
			fr: FetchResult[any]{
				Error: apierrors.NewNotFound(schema.GroupResource{}, "test"),
			},
			expected: true,
		},
		{
			name: "Other API error returns false",
			fr: FetchResult[any]{
				Error: apierrors.NewForbidden(schema.GroupResource{}, "test", errors.New("forbidden")),
			},
			expected: false,
		},
		{
			name: "Generic error returns false",
			fr: FetchResult[any]{
				Error: errors.New("generic error"),
			},
			expected: false,
		},
		{
			name: "No error returns false",
			fr: FetchResult[any]{
				Error: nil,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.fr.IsNotFound()
			if got != tt.expected {
				t.Errorf("IsNotFound() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestFetchResult_OK(t *testing.T) {
	tests := []struct {
		name     string
		fr       FetchResult[any]
		expected bool
	}{
		{
			name: "No error returns true",
			fr: FetchResult[any]{
				Error: nil,
			},
			expected: true,
		},
		{
			name: "With error returns false",
			fr: FetchResult[any]{
				Error: errors.New("test error"),
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.fr.OK()
			if got != tt.expected {
				t.Errorf("OK() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestFetchResult_HasError(t *testing.T) {
	tests := []struct {
		name     string
		fr       FetchResult[any]
		expected bool
	}{
		{
			name: "No error returns false",
			fr: FetchResult[any]{
				Error: nil,
			},
			expected: false,
		},
		{
			name: "With error returns true",
			fr: FetchResult[any]{
				Error: errors.New("test error"),
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.fr.HasError()
			if got != tt.expected {
				t.Errorf("HasError() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestFetchResult_ToComponentHealth_Success(t *testing.T) {
	// Test case: successful fetch with inspector function
	type testModel struct {
		Ready bool
	}

	fr := FetchResult[*testModel]{
		Value: &testModel{Ready: true},
		Error: nil,
	}

	ch := fr.ToComponentHealth(componentName, func(m *testModel) ComponentHealth {
		if m.Ready {
			return ComponentHealth{
				State:   constants.AIMStatusReady,
				Reason:  "Ready",
				Message: "Model is ready",
			}
		}
		return ComponentHealth{
			State:   constants.AIMStatusProgressing,
			Reason:  "NotReady",
			Message: "Waiting for model",
		}
	})

	if ch.Component != componentName {
		t.Errorf("Component = %v, want Model", ch.Component)
	}

	if ch.State != constants.AIMStatusReady {
		t.Errorf("State = %v, want Ready", ch.State)
	}

	if ch.Reason != "Ready" {
		t.Errorf("Reason = %v, want Ready", ch.Reason)
	}

	if ch.Message != "Model is ready" {
		t.Errorf("Message = %v, want 'Model is ready'", ch.Message)
	}

	if len(ch.Errors) != 0 {
		t.Errorf("Errors should be empty, got %v", ch.Errors)
	}
}

func TestFetchResult_ToComponentHealth_NotReady(t *testing.T) {
	// Test case: successful fetch but component not ready
	type testModel struct {
		Ready bool
	}

	fr := FetchResult[*testModel]{
		Value: &testModel{Ready: false},
		Error: nil,
	}

	ch := fr.ToComponentHealth(componentName, func(m *testModel) ComponentHealth {
		if m.Ready {
			return ComponentHealth{
				State:   constants.AIMStatusReady,
				Reason:  "Ready",
				Message: "Model is ready",
			}
		}
		return ComponentHealth{
			State:   constants.AIMStatusProgressing,
			Reason:  "NotReady",
			Message: "Waiting for model",
		}
	})

	if ch.Component != componentName {
		t.Errorf("Component = %v, want Model", ch.Component)
	}

	if ch.State != constants.AIMStatusProgressing {
		t.Errorf("State = %v, want Progressing", ch.State)
	}

	if ch.Reason != "NotReady" {
		t.Errorf("Reason = %v, want NotReady", ch.Reason)
	}

	if ch.Message != "Waiting for model" {
		t.Errorf("Message = %v, want 'Waiting for model'", ch.Message)
	}

	if len(ch.Errors) != 0 {
		t.Errorf("Errors should be empty, got %v", ch.Errors)
	}
}

func TestFetchResult_ToComponentHealth_FetchError(t *testing.T) {
	// Test case: fetch error - inspector should not be called
	type testModel struct {
		Ready bool
	}

	fetchErr := apierrors.NewNotFound(schema.GroupResource{}, "model-123")
	fr := FetchResult[*testModel]{
		Value: nil,
		Error: fetchErr,
	}

	inspectorCalled := false
	ch := fr.ToComponentHealth("Model", func(m *testModel) ComponentHealth {
		inspectorCalled = true
		return ComponentHealth{
			State:   constants.AIMStatusReady,
			Reason:  "Ready",
			Message: "Should not be called",
		}
	})

	if inspectorCalled {
		t.Error("Inspector should not be called when fetch has error")
	}

	if ch.Component != componentName {
		t.Errorf("Component = %v, want Model", ch.Component)
	}

	if ch.State != "" {
		t.Errorf("State should be empty for error case, got %v", ch.State)
	}

	if ch.Reason != "" {
		t.Errorf("Reason should be empty for error case, got %v", ch.Reason)
	}

	if ch.Message != "" {
		t.Errorf("Message should be empty for error case, got %v", ch.Message)
	}

	if len(ch.Errors) != 1 {
		t.Fatalf("Expected 1 error, got %v", len(ch.Errors))
	}

	if !errors.Is(fetchErr, ch.Errors[0]) {
		t.Errorf("Error = %v, want %v", ch.Errors[0], fetchErr)
	}
}

func TestFetchResult_ToComponentHealth_InfrastructureError(t *testing.T) {
	// Test case: infrastructure error during fetch
	type testModel struct {
		Ready bool
	}

	fetchErr := errors.New("network timeout")
	fr := FetchResult[*testModel]{
		Value: nil,
		Error: fetchErr,
	}

	ch := fr.ToComponentHealth(componentName, func(m *testModel) ComponentHealth {
		return ComponentHealth{
			State:   constants.AIMStatusReady,
			Reason:  "Ready",
			Message: "Should not be called",
		}
	})

	// State/Reason/Message should be derived from error by GetState/GetReason/GetMessage
	if ch.Component != "Model" {
		t.Errorf("Component = %v, want Model", ch.Component)
	}

	if len(ch.Errors) != 1 {
		t.Fatalf("Expected 1 error, got %v", len(ch.Errors))
	}

	if !errors.Is(fetchErr, ch.Errors[0]) {
		t.Errorf("Error = %v, want %v", ch.Errors[0], fetchErr)
	}

	// Verify that GetState/GetReason/GetMessage work with the error
	state := ch.GetState()
	if state != constants.AIMStatusProgressing {
		t.Errorf("GetState() = %v, want Progressing (derived from unknown error)", state)
	}

	reason := ch.GetReason()
	if reason != "UnknownError" {
		t.Errorf("GetReason() = %v, want UnknownError (derived from categorized error)", reason)
	}

	message := ch.GetMessage()
	if message != "network timeout" {
		t.Errorf("GetMessage() = %v, want 'network timeout' (derived from error)", message)
	}
}
