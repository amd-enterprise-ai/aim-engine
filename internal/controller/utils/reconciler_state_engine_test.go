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
	"context"
	"errors"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

func TestPipeline_processStateEngine_Success(t *testing.T) {
	// Test processStateEngine with healthy components
	obs := testObservation{modelReady: true}
	cm := NewConditionManager([]metav1.Condition{})
	status := &testStatus{}

	p := &Pipeline[*testObject, *testStatus, testFetch, testObservation]{
		Reconciler: &testReconciler{},
	}

	decision, err := p.processStateEngine(context.Background(), obs, cm, status)
	if err != nil {
		t.Fatalf("processStateEngine returned error: %v", err)
	}

	if !decision.ShouldApply {
		t.Error("ShouldApply should be true for healthy state")
	}

	if decision.ShouldRequeue {
		t.Error("ShouldRequeue should be false for healthy state")
	}

	if decision.RequeueError != nil {
		t.Errorf("RequeueError should be nil, got %v", decision.RequeueError)
	}
}

func TestPipeline_processStateEngine_InfrastructureError(t *testing.T) {
	// Test processStateEngine with infrastructure error
	infraErr := NewInfrastructureError("NetworkTimeout", "Network timeout", errors.New("timeout"))
	obs := testObservationWithError{infraError: infraErr}
	cm := NewConditionManager([]metav1.Condition{})
	status := &testStatus{}

	p := &Pipeline[*testObject, *testStatus, testFetch, testObservationWithError]{
		Reconciler: &testReconcilerWithError{infraError: infraErr},
	}

	decision, err := p.processStateEngine(context.Background(), obs, cm, status)
	if err != nil {
		t.Fatalf("processStateEngine returned error: %v", err)
	}

	if decision.ShouldApply {
		t.Error("ShouldApply should be false for infrastructure error")
	}

	if !decision.ShouldRequeue {
		t.Error("ShouldRequeue should be true for infrastructure error")
	}

	if decision.RequeueError == nil {
		t.Error("RequeueError should not be nil for infrastructure error")
	}
}

func TestPipeline_processStateEngine_ObservabilityLevels(t *testing.T) {
	tests := []struct {
		name            string
		componentHealth []ComponentHealth
		conditionType   string
		wantEventMode   EventMode
		wantEventLevel  EventLevel
		wantLogMode     LogMode
		wantLogLevel    int
		description     string
	}{
		{
			name: "Component Ready state uses AsInfo",
			componentHealth: []ComponentHealth{
				{
					Component: testComponentModel,
					State:     constants.AIMStatusReady,
					Reason:    "Ready",
					Message:   "Model is ready",
				},
			},
			conditionType:  testConditionTypeModelReady,
			wantEventMode:  EventOnTransition,
			wantEventLevel: LevelNormal,
			wantLogMode:    LogOnTransition,
			wantLogLevel:   0, // V(0) = visible at default info level
			description:    "Ready components should use AsInfo (normal event + info log on transition)",
		},
		{
			name: "Component Progressing state uses AsInfo",
			componentHealth: []ComponentHealth{
				{
					Component: testComponentModel,
					State:     constants.AIMStatusProgressing,
					Reason:    "NotReady",
					Message:   "Waiting for model",
				},
			},
			conditionType:  testConditionTypeModelReady,
			wantEventMode:  EventOnTransition,
			wantEventLevel: LevelNormal,
			wantLogMode:    LogOnTransition,
			wantLogLevel:   0, // V(0) = visible at default info level
			description:    "Progressing components should use AsInfo",
		},
		{
			name: "Component Failed state uses AsError",
			componentHealth: []ComponentHealth{
				{
					Component: testComponentModel,
					State:     constants.AIMStatusFailed,
					Reason:    "Failed",
					Message:   "Model failed",
				},
			},
			conditionType:  testConditionTypeModelReady,
			wantEventMode:  EventAlways,
			wantEventLevel: LevelWarning,
			wantLogMode:    LogAlways,
			wantLogLevel:   0,
			description:    "Failed components should use AsError (warning event + error log every reconcile)",
		},
		{
			name: "Component Degraded state uses AsError",
			componentHealth: []ComponentHealth{
				{
					Component: testComponentModel,
					State:     constants.AIMStatusDegraded,
					Reason:    "Degraded",
					Message:   "Model degraded",
				},
			},
			conditionType:  testConditionTypeModelReady,
			wantEventMode:  EventAlways,
			wantEventLevel: LevelWarning,
			wantLogMode:    LogAlways,
			wantLogLevel:   0,
			description:    "Degraded components should use AsError",
		},
		{
			name: "Component NotAvailable state uses AsError",
			componentHealth: []ComponentHealth{
				{
					Component: testComponentModel,
					State:     constants.AIMStatusNotAvailable,
					Reason:    "NotAvailable",
					Message:   "Model not available",
				},
			},
			conditionType:  testConditionTypeModelReady,
			wantEventMode:  EventAlways,
			wantEventLevel: LevelWarning,
			wantLogMode:    LogAlways,
			wantLogLevel:   0,
			description:    "NotAvailable components should use AsError",
		},
		{
			name: "Ready=True uses AsInfo",
			componentHealth: []ComponentHealth{
				{
					Component: testComponentModel,
					State:     constants.AIMStatusReady,
					Reason:    "Ready",
					Message:   "Model is ready",
				},
			},
			conditionType:  ConditionTypeReady,
			wantEventMode:  EventOnTransition,
			wantEventLevel: LevelNormal,
			wantLogMode:    LogOnTransition,
			wantLogLevel:   0, // V(0) = visible at default info level
			description:    "Ready=True should use AsInfo",
		},
		{
			name: "Ready=False (progressing) uses AsInfo",
			componentHealth: []ComponentHealth{
				{
					Component: "Model",
					State:     constants.AIMStatusProgressing,
					Reason:    "NotReady",
					Message:   "Waiting",
				},
			},
			conditionType:  "Ready",
			wantEventMode:  EventOnTransition,
			wantEventLevel: LevelNormal,
			wantLogMode:    LogOnTransition,
			wantLogLevel:   0, // V(0) = visible at default info level
			description:    "Ready=False due to normal progression should use AsInfo",
		},
		{
			name: "Ready=False (component failure) uses AsError",
			componentHealth: []ComponentHealth{
				{
					Component: "Model",
					State:     constants.AIMStatusFailed,
					Reason:    "Failed",
					Message:   "Model failed",
				},
			},
			conditionType:  "Ready",
			wantEventMode:  EventAlways,
			wantEventLevel: LevelWarning,
			wantLogMode:    LogAlways,
			wantLogLevel:   0,
			description:    "Ready=False due to component failure should use AsError",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test observation that returns our test component health
			obs := testObservationCustomHealth{health: tt.componentHealth}
			cm := NewConditionManager([]metav1.Condition{})
			status := &testStatus{}

			p := &Pipeline[*testObject, *testStatus, testFetch, testObservationCustomHealth]{
				Reconciler: &testReconcilerCustomHealth{},
			}

			_, err := p.processStateEngine(context.Background(), obs, cm, status)
			if err != nil {
				t.Fatalf("processStateEngine returned error: %v", err)
			}

			// Verify observability config
			cfg := cm.ConfigFor(tt.conditionType)

			if cfg.eventMode != tt.wantEventMode {
				t.Errorf("%s: eventMode = %v, want %v", tt.description, cfg.eventMode, tt.wantEventMode)
			}

			if cfg.eventLevel != tt.wantEventLevel {
				t.Errorf("%s: eventLevel = %v, want %v", tt.description, cfg.eventLevel, tt.wantEventLevel)
			}

			if cfg.logMode != tt.wantLogMode {
				t.Errorf("%s: logMode = %v, want %v", tt.description, cfg.logMode, tt.wantLogMode)
			}

			if cfg.logLevel != tt.wantLogLevel {
				t.Errorf("%s: logLevel = %v, want %v", tt.description, cfg.logLevel, tt.wantLogLevel)
			}
		})
	}
}

func TestPipeline_processStateEngine_ParentConditionObservability(t *testing.T) {
	tests := []struct {
		name           string
		errors         []error
		conditionType  string
		wantEventMode  EventMode
		wantEventLevel EventLevel
		wantLogMode    LogMode
		wantLogLevel   int
	}{
		{
			name: "AuthValid=False uses AsError",
			errors: []error{
				NewAuthError("Forbidden", "Access denied", nil),
			},
			conditionType:  "AuthValid",
			wantEventMode:  EventAlways,
			wantEventLevel: LevelWarning,
			wantLogMode:    LogAlways,
			wantLogLevel:   0,
		},
		{
			name: "ConfigValid=False uses AsError",
			errors: []error{
				NewInvalidSpecError("InvalidSpec", "Bad config", nil),
			},
			conditionType:  "ConfigValid",
			wantEventMode:  EventAlways,
			wantEventLevel: LevelWarning,
			wantLogMode:    LogAlways,
			wantLogLevel:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obs := testObservationCustomHealth{
				health: []ComponentHealth{
					{
						Component: "Model",
						Errors:    tt.errors,
					},
				},
			}
			cm := NewConditionManager([]metav1.Condition{})
			status := &testStatus{}

			p := &Pipeline[*testObject, *testStatus, testFetch, testObservationCustomHealth]{
				Reconciler: &testReconcilerCustomHealth{},
			}

			_, err := p.processStateEngine(context.Background(), obs, cm, status)
			if err != nil {
				t.Fatalf("processStateEngine returned error: %v", err)
			}

			cfg := cm.ConfigFor(tt.conditionType)

			if cfg.eventMode != tt.wantEventMode {
				t.Errorf("eventMode = %v, want %v", cfg.eventMode, tt.wantEventMode)
			}

			if cfg.eventLevel != tt.wantEventLevel {
				t.Errorf("eventLevel = %v, want %v", cfg.eventLevel, tt.wantEventLevel)
			}

			if cfg.logMode != tt.wantLogMode {
				t.Errorf("logMode = %v, want %v", cfg.logMode, tt.wantLogMode)
			}

			if cfg.logLevel != tt.wantLogLevel {
				t.Errorf("logLevel = %v, want %v", cfg.logLevel, tt.wantLogLevel)
			}
		})
	}
}

func TestPipeline_processStateEngine_NoConditionPollution(t *testing.T) {
	// Start with no conditions
	obs := testObservationCustomHealth{
		health: []ComponentHealth{
			{
				Component: "Model",
				State:     constants.AIMStatusReady,
				Reason:    "Ready",
				Message:   "Model is ready",
			},
		},
	}
	cm := NewConditionManager([]metav1.Condition{})
	status := &testStatus{}

	p := &Pipeline[*testObject, *testStatus, testFetch, testObservationCustomHealth]{
		Reconciler: &testReconcilerCustomHealth{},
	}

	_, err := p.processStateEngine(context.Background(), obs, cm, status)
	if err != nil {
		t.Fatalf("processStateEngine returned error: %v", err)
	}

	// AuthValid, ConfigValid, and DependenciesReachable should NOT be set
	// (since there are no errors and they weren't previously present)
	if cm.Get(ConditionTypeAuthValid) != nil {
		t.Error("AuthValid should not be set when there are no auth errors and it wasn't previously present")
	}

	if cm.Get(ConditionTypeConfigValid) != nil {
		t.Error("ConfigValid should not be set when there are no config errors and it wasn't previously present")
	}

	if cm.Get(ConditionTypeDependenciesReachable) != nil {
		t.Error("DependenciesReachable should not be set when there are no infra errors and it wasn't previously present")
	}

	// However, ModelReady and Ready SHOULD be set (component condition + overall ready)
	if cm.Get(testConditionTypeModelReady) == nil {
		t.Error("ModelReady should be set for component health")
	}

	if cm.Get(ConditionTypeReady) == nil {
		t.Error("Ready should always be set")
	}
}
