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
	"fmt"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

// ======================================================
// TEST HELPERS
// ======================================================

// Test condition type constants - derived from test component names
const (
	testComponentModel          = "Model"
	testConditionTypeModelReady = testComponentModel + ComponentConditionSuffix // "ModelReady"
)

type testObject struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Status            testStatus `json:"status,omitempty"`
}

func (t *testObject) DeepCopyObject() runtime.Object {
	out := &testObject{}
	*out = *t
	out.Status.Conditions = append([]metav1.Condition(nil), t.Status.Conditions...)
	return out
}

func (t *testObject) GetStatus() *testStatus {
	return &t.Status
}

type testStatus struct {
	Status     string             `json:"status"`
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

func (t *testStatus) GetConditions() []metav1.Condition {
	return t.Conditions
}

func (t *testStatus) SetConditions(conds []metav1.Condition) {
	t.Conditions = conds
}

func (t *testStatus) SetStatus(status string) {
	t.Status = status
}

type testFetch struct {
	ModelReady bool
}

type testObservation struct {
	modelReady bool
}

func (o testObservation) GetComponentHealth() []ComponentHealth {
	if o.modelReady {
		return []ComponentHealth{
			{
				Component: "Model",
				State:     constants.AIMStatusReady,
				Reason:    "Ready",
				Message:   "Model is ready",
			},
		}
	}
	return []ComponentHealth{
		{
			Component: "Model",
			State:     constants.AIMStatusProgressing,
			Reason:    "NotReady",
			Message:   "Waiting for model",
		},
	}
}

type testReconciler struct {
	fetchResult testFetch
}

func (r *testReconciler) FetchRemoteState(ctx context.Context, c client.Client, obj ReconcileContext[*testObject]) testFetch {
	return r.fetchResult
}

func (r *testReconciler) ComposeState(ctx context.Context, obj ReconcileContext[*testObject], fetched testFetch) testObservation {
	return testObservation{modelReady: fetched.ModelReady}
}

func (r *testReconciler) PlanResources(ctx context.Context, obj ReconcileContext[*testObject], obs testObservation) PlanResult {
	// Simple test: return empty plan
	return PlanResult{}
}

// Test reconciler with errors
type testObservationWithError struct {
	infraError error
}

func (o testObservationWithError) GetComponentHealth() []ComponentHealth {
	if o.infraError != nil {
		return []ComponentHealth{
			{
				Component: "Model",
				Errors:    []error{o.infraError},
			},
		}
	}
	return []ComponentHealth{
		{
			Component: "Model",
			State:     constants.AIMStatusReady,
			Reason:    "Ready",
			Message:   "Model is ready",
		},
	}
}

type testReconcilerWithError struct {
	infraError error
}

func (r *testReconcilerWithError) FetchRemoteState(ctx context.Context, c client.Client, obj ReconcileContext[*testObject]) testFetch {
	return testFetch{}
}

func (r *testReconcilerWithError) ComposeState(ctx context.Context, obj ReconcileContext[*testObject], fetched testFetch) testObservationWithError {
	return testObservationWithError{infraError: r.infraError}
}

func (r *testReconcilerWithError) PlanResources(ctx context.Context, obj ReconcileContext[*testObject], obs testObservationWithError) PlanResult {
	return PlanResult{}
}

// Test observation with custom health (for observability tests)
type testObservationCustomHealth struct {
	health []ComponentHealth
}

func (o testObservationCustomHealth) GetComponentHealth() []ComponentHealth {
	return o.health
}

type testReconcilerCustomHealth struct{}

func (r *testReconcilerCustomHealth) FetchRemoteState(ctx context.Context, c client.Client, obj ReconcileContext[*testObject]) testFetch {
	return testFetch{}
}

func (r *testReconcilerCustomHealth) ComposeState(ctx context.Context, obj ReconcileContext[*testObject], fetched testFetch) testObservationCustomHealth {
	// This won't actually be called in our tests since we pass obs directly to processStateEngine
	return testObservationCustomHealth{}
}

func (r *testReconcilerCustomHealth) PlanResources(ctx context.Context, obj ReconcileContext[*testObject], obs testObservationCustomHealth) PlanResult {
	return PlanResult{}
}

// ======================================================
// DECISION TESTS
// ======================================================

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

// ======================================================
// STATE ENGINE TESTS
// ======================================================

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

// ======================================================
// PIPELINE TESTS
// ======================================================

func TestPipeline_Run_Success(t *testing.T) {
	// Test successful reconciliation with all components healthy
	scheme := runtime.NewScheme()
	_ = metav1.AddMetaToScheme(scheme)

	// Register testObject in scheme
	scheme.AddKnownTypes(metav1.SchemeGroupVersion, &testObject{})

	obj := &testObject{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "meta.k8s.io/v1",
			Kind:       "testObject",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-obj",
			Namespace: "default",
		},
	}

	// Create fake client with the object already in it
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(obj).Build()
	recorder := record.NewFakeRecorder(100)

	reconciler := &testReconciler{
		fetchResult: testFetch{ModelReady: true},
	}

	pipeline := &Pipeline[*testObject, *testStatus, testFetch, testObservation]{
		Client:         cl,
		StatusClient:   cl.Status(),
		Recorder:       recorder,
		ControllerName: "test",
		Reconciler:     reconciler,
		Scheme:         scheme,
	}

	err := pipeline.Run(context.Background(), obj)

	// Note: We expect a status update error because fake client doesn't fully support SubResource updates
	// The important thing is that our state engine logic ran and set the status fields in memory
	if err != nil && err.Error() != "status update failed: testobjects.meta.k8s.io \"test-obj\" not found" {
		t.Fatalf("Run() returned unexpected error: %v", err)
	}

	// Verify status was set in memory (even though update to fake client failed)
	if obj.Status.Status == "" {
		t.Error("Status.Status should be set")
	}

	// Verify conditions were set in memory
	if len(obj.Status.Conditions) == 0 {
		t.Error("Conditions should be set")
	}

	// Verify specific conditions
	var modelReady, ready *metav1.Condition
	for i := range obj.Status.Conditions {
		cond := &obj.Status.Conditions[i]
		if cond.Type == testConditionTypeModelReady {
			modelReady = cond
		}
		if cond.Type == ConditionTypeReady {
			ready = cond
		}
	}

	if modelReady == nil {
		t.Error("ModelReady condition should be set")
	} else if modelReady.Status != metav1.ConditionTrue {
		t.Errorf("ModelReady should be True, got %v", modelReady.Status)
	}

	if ready == nil {
		t.Error("Ready condition should be set")
	} else if ready.Status != metav1.ConditionTrue {
		t.Errorf("Ready should be True, got %v", ready.Status)
	}

	// Verify events were emitted
	// Note: Events are only emitted on transitions, and this is the first time we're setting these conditions
	// So we expect 2 events: ModelReady and Ready
	select {
	case event := <-recorder.Events:
		// Should have at least one event
		if event == "" {
			t.Error("Expected event to be emitted")
		}
		t.Logf("Event emitted: %s", event)
	default:
		t.Error("Expected events to be emitted but recorder is empty")
	}
}

func TestPipeline_Run_WithInfrastructureError(t *testing.T) {
	// Test that infrastructure errors trigger requeue
	scheme := runtime.NewScheme()
	_ = metav1.AddMetaToScheme(scheme)

	// Register testObject in scheme
	scheme.AddKnownTypes(metav1.SchemeGroupVersion, &testObject{})

	obj := &testObject{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "meta.k8s.io/v1",
			Kind:       "testObject",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-obj",
			Namespace: "default",
		},
	}

	// Create fake client with the object already in it
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(obj).Build()
	recorder := record.NewFakeRecorder(100)

	// Create a reconciler that returns infrastructure error in observation
	reconciler := &testReconcilerWithError{
		infraError: NewInfrastructureError("NetworkTimeout", "Network timeout", errors.New("timeout")),
	}

	pipeline := &Pipeline[*testObject, *testStatus, testFetch, testObservationWithError]{
		Client:         cl,
		StatusClient:   cl.Status(),
		Recorder:       recorder,
		ControllerName: "test",
		Reconciler:     reconciler,
		Scheme:         scheme,
	}

	err := pipeline.Run(context.Background(), obj)

	// Should return error for requeue
	if err == nil {
		t.Fatal("Run() should return error for infrastructure issues")
	}

	// Status should still be updated (even on error)
	if len(obj.Status.Conditions) == 0 {
		t.Error("Conditions should be set even when returning error")
	}

	// Verify that ModelReady condition uses AsError (recurring) since it has an infrastructure error
	// This means it should emit warning events even though it's the first transition
	var modelReady *metav1.Condition
	for i := range obj.Status.Conditions {
		if obj.Status.Conditions[i].Type == testConditionTypeModelReady {
			modelReady = &obj.Status.Conditions[i]
			break
		}
	}

	if modelReady != nil {
		// ModelReady should be False (infrastructure error derives Degraded state, which maps to False)
		if modelReady.Status != metav1.ConditionFalse {
			t.Errorf("ModelReady should be False for infrastructure error (Degraded state), got %v", modelReady.Status)
		}
	}

	// Verify events were emitted (should have warning events due to AsError)
	eventCount := 0
	for {
		select {
		case event := <-recorder.Events:
			eventCount++
			t.Logf("Event emitted: %s", event)
			// Infrastructure errors should produce Warning events
			if !strings.Contains(event, "Warning") {
				t.Errorf("Expected Warning event for infrastructure error, got: %s", event)
			}
		default:
			goto doneCountingEvents
		}
	}
doneCountingEvents:

	if eventCount == 0 {
		t.Error("Expected warning events to be emitted for infrastructure error")
	}
}

// conflictStatusWriter is a status writer that always returns a conflict error
type conflictStatusWriter struct {
	client.StatusWriter
}

func (c *conflictStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	return apierrors.NewConflict(schema.GroupResource{Group: "test", Resource: "testobjects"}, "test-obj", errors.New("the object has been modified"))
}

func TestPipeline_Run_StatusConflict(t *testing.T) {
	// Test that status update conflicts are handled gracefully without returning an error
	scheme := runtime.NewScheme()
	_ = metav1.AddMetaToScheme(scheme)
	scheme.AddKnownTypes(metav1.SchemeGroupVersion, &testObject{})

	obj := &testObject{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "meta.k8s.io/v1",
			Kind:       "testObject",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-obj",
			Namespace: "default",
		},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(obj).Build()
	recorder := record.NewFakeRecorder(100)

	reconciler := &testReconciler{
		fetchResult: testFetch{ModelReady: true},
	}

	pipeline := &Pipeline[*testObject, *testStatus, testFetch, testObservation]{
		Client:         cl,
		StatusClient:   &conflictStatusWriter{}, // Use our conflict-returning status writer
		Recorder:       recorder,
		ControllerName: "test",
		Reconciler:     reconciler,
		Scheme:         scheme,
	}

	err := pipeline.Run(context.Background(), obj)
	// Conflict errors should be swallowed - no error returned
	if err != nil {
		t.Errorf("Expected nil error for status conflict, got: %v", err)
	}

	// Status should still be set in memory (even though update failed)
	if obj.Status.Status == "" {
		t.Error("Status.Status should be set in memory")
	}
}

// ======================================================
// GRACE PERIOD TESTS
// ======================================================

func TestPipeline_GracePeriod_WithinThreshold(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = metav1.AddMetaToScheme(scheme)
	scheme.AddKnownTypes(metav1.SchemeGroupVersion, &testObject{})

	// Create object with existing Ready condition
	now := metav1.Now()
	obj := &testObject{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "meta.k8s.io/v1",
			Kind:       "testObject",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-obj",
			Namespace: "default",
		},
		Status: testStatus{
			Status: string(constants.AIMStatusReady),
			Conditions: []metav1.Condition{
				{
					Type:               testConditionTypeModelReady,
					Status:             metav1.ConditionTrue,
					Reason:             "Ready",
					Message:            "Model is ready",
					LastTransitionTime: now,
				},
				{
					Type:               ConditionTypeReady,
					Status:             metav1.ConditionTrue,
					Reason:             "AllComponentsReady",
					Message:            "All components are ready",
					LastTransitionTime: now,
				},
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(obj).Build()
	recorder := record.NewFakeRecorder(100)

	// Reconciler returns infrastructure error (simulating temporary network issue)
	reconciler := &testReconcilerWithError{
		infraError: NewInfrastructureError("NetworkTimeout", "Network timeout", errors.New("timeout")),
	}

	pipeline := &Pipeline[*testObject, *testStatus, testFetch, testObservationWithError]{
		Client:         cl,
		StatusClient:   cl.Status(),
		Recorder:       recorder,
		ControllerName: "test",
		Reconciler:     reconciler,
		Scheme:         scheme,
	}

	err := pipeline.Run(context.Background(), obj)

	// Should return error for requeue
	if err == nil {
		t.Fatal("Run() should return error for infrastructure issues")
	}

	// Verify DependenciesReachable condition was set to False
	var depReachable *metav1.Condition
	for i := range obj.Status.Conditions {
		if obj.Status.Conditions[i].Type == "DependenciesReachable" {
			depReachable = &obj.Status.Conditions[i]
			break
		}
	}

	if depReachable == nil {
		t.Fatal("DependenciesReachable condition should be set")
	}

	if depReachable.Status != metav1.ConditionFalse {
		t.Errorf("DependenciesReachable should be False, got %v", depReachable.Status)
	}

	// CRITICAL: ModelReady should STAY True (grace period - just set the condition)
	// Because this is the first time DependenciesReachable went False, we're within grace period
	var modelReady *metav1.Condition
	for i := range obj.Status.Conditions {
		if obj.Status.Conditions[i].Type == testConditionTypeModelReady {
			modelReady = &obj.Status.Conditions[i]
			break
		}
	}

	if modelReady == nil {
		t.Fatal("ModelReady condition should be set")
	}

	if modelReady.Status != metav1.ConditionTrue {
		t.Errorf("ModelReady should STAY True during grace period, got %v", modelReady.Status)
	}

	// Status should stay Ready (grace period)
	if obj.Status.Status != string(constants.AIMStatusReady) {
		t.Errorf("Status should stay Ready during grace period, got %v", obj.Status.Status)
	}

	t.Logf("Within grace period: ModelReady=%v, Status=%v", modelReady.Status, obj.Status.Status)
}

func TestPipeline_GracePeriod_AfterThreshold(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = metav1.AddMetaToScheme(scheme)
	scheme.AddKnownTypes(metav1.SchemeGroupVersion, &testObject{})

	// Create object with DependenciesReachable=False that has been false for > 10 seconds
	elevenSecondsAgo := metav1.NewTime(metav1.Now().Add(-11 * time.Second))
	obj := &testObject{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "meta.k8s.io/v1",
			Kind:       "testObject",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-obj",
			Namespace: "default",
		},
		Status: testStatus{
			Status: string(constants.AIMStatusReady),
			Conditions: []metav1.Condition{
				{
					Type:               testConditionTypeModelReady,
					Status:             metav1.ConditionTrue,
					Reason:             "Ready",
					Message:            "Model is ready",
					LastTransitionTime: elevenSecondsAgo,
				},
				{
					Type:               ConditionTypeDependenciesReachable,
					Status:             metav1.ConditionFalse,
					Reason:             "InfrastructureError",
					Message:            "Cannot reach dependencies",
					LastTransitionTime: elevenSecondsAgo, // Been false for 11 seconds
				},
				{
					Type:               ConditionTypeReady,
					Status:             metav1.ConditionTrue,
					Reason:             "AllComponentsReady",
					Message:            "All components are ready",
					LastTransitionTime: elevenSecondsAgo,
				},
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(obj).Build()
	recorder := record.NewFakeRecorder(100)

	// Reconciler still returns infrastructure error
	reconciler := &testReconcilerWithError{
		infraError: NewInfrastructureError("NetworkTimeout", "Network timeout", errors.New("timeout")),
	}

	pipeline := &Pipeline[*testObject, *testStatus, testFetch, testObservationWithError]{
		Client:         cl,
		StatusClient:   cl.Status(),
		Recorder:       recorder,
		ControllerName: "test",
		Reconciler:     reconciler,
		Scheme:         scheme,
	}

	err := pipeline.Run(context.Background(), obj)

	// Should return error for requeue
	if err == nil {
		t.Fatal("Run() should return error for infrastructure issues")
	}

	// CRITICAL: After threshold, ModelReady should degrade to False
	var modelReady *metav1.Condition
	for i := range obj.Status.Conditions {
		if obj.Status.Conditions[i].Type == testConditionTypeModelReady {
			modelReady = &obj.Status.Conditions[i]
			break
		}
	}

	if modelReady == nil {
		t.Fatal("ModelReady condition should be set")
	}

	if modelReady.Status != metav1.ConditionFalse {
		t.Errorf("ModelReady should degrade to False after threshold, got %v", modelReady.Status)
	}

	// Status should degrade to Degraded
	if obj.Status.Status != string(constants.AIMStatusDegraded) {
		t.Errorf("Status should degrade to Degraded after threshold, got %v", obj.Status.Status)
	}

	t.Logf("After grace period: ModelReady=%v, Status=%v", modelReady.Status, obj.Status.Status)
}

func TestPipeline_GracePeriod_Recovery(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = metav1.AddMetaToScheme(scheme)
	scheme.AddKnownTypes(metav1.SchemeGroupVersion, &testObject{})

	// Create object with DependenciesReachable=False that has been false for 5 seconds
	fiveSecondsAgo := metav1.NewTime(metav1.Now().Add(-5 * time.Second))
	obj := &testObject{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "meta.k8s.io/v1",
			Kind:       "testObject",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-obj",
			Namespace: "default",
		},
		Status: testStatus{
			Status: string(constants.AIMStatusReady),
			Conditions: []metav1.Condition{
				{
					Type:               testConditionTypeModelReady,
					Status:             metav1.ConditionTrue,
					Reason:             "Ready",
					Message:            "Model is ready",
					LastTransitionTime: fiveSecondsAgo,
				},
				{
					Type:               ConditionTypeDependenciesReachable,
					Status:             metav1.ConditionFalse,
					Reason:             "InfrastructureError",
					Message:            "Cannot reach dependencies",
					LastTransitionTime: fiveSecondsAgo, // Been false for 5 seconds (within threshold)
				},
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(obj).Build()
	recorder := record.NewFakeRecorder(100)

	// Reconciler now returns NO error (recovery!)
	reconciler := &testReconciler{
		fetchResult: testFetch{ModelReady: true},
	}

	pipeline := &Pipeline[*testObject, *testStatus, testFetch, testObservation]{
		Client:         cl,
		StatusClient:   cl.Status(),
		Recorder:       recorder,
		ControllerName: "test",
		Reconciler:     reconciler,
		Scheme:         scheme,
	}

	err := pipeline.Run(context.Background(), obj)

	// May have status update error from fake client, but that's OK
	if err != nil && !strings.Contains(err.Error(), "status update failed") {
		t.Fatalf("Unexpected error: %v", err)
	}

	// DependenciesReachable should now be True (recovered)
	var depReachable *metav1.Condition
	for i := range obj.Status.Conditions {
		if obj.Status.Conditions[i].Type == ConditionTypeDependenciesReachable {
			depReachable = &obj.Status.Conditions[i]
			break
		}
	}

	if depReachable == nil {
		t.Fatal("DependenciesReachable condition should be set")
	}

	if depReachable.Status != metav1.ConditionTrue {
		t.Errorf("DependenciesReachable should be True after recovery, got %v", depReachable.Status)
	}

	// ModelReady should be True (still ready, never degraded)
	var modelReady *metav1.Condition
	for i := range obj.Status.Conditions {
		if obj.Status.Conditions[i].Type == testConditionTypeModelReady {
			modelReady = &obj.Status.Conditions[i]
			break
		}
	}

	if modelReady == nil {
		t.Fatal("ModelReady condition should be set")
	}

	if modelReady.Status != metav1.ConditionTrue {
		t.Errorf("ModelReady should stay True after recovery, got %v", modelReady.Status)
	}

	// Status should stay Ready (never degraded)
	if obj.Status.Status != string(constants.AIMStatusReady) {
		t.Errorf("Status should stay Ready after recovery, got %v", obj.Status.Status)
	}

	t.Logf("After recovery: ModelReady=%v, Status=%v, DependenciesReachable=%v",
		modelReady.Status, obj.Status.Status, depReachable.Status)
}

// ======================================================
// ERROR RECOVERY TESTS
// ======================================================

func TestPipeline_ErrorRecovery_AuthValid(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = metav1.AddMetaToScheme(scheme)
	scheme.AddKnownTypes(metav1.SchemeGroupVersion, &testObject{})

	now := metav1.Now()
	obj := &testObject{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "meta.k8s.io/v1",
			Kind:       "testObject",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-obj",
			Namespace: "default",
		},
		Status: testStatus{
			Status: string(constants.AIMStatusFailed),
			Conditions: []metav1.Condition{
				{
					Type:               "AuthValid",
					Status:             metav1.ConditionFalse,
					Reason:             "AuthError",
					Message:            "Authentication or authorization failure",
					LastTransitionTime: now,
				},
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(obj).Build()
	recorder := record.NewFakeRecorder(100)

	// First reconcile: auth error cleared, reconciler returns success
	reconciler := &testReconciler{
		fetchResult: testFetch{ModelReady: true},
	}

	pipeline := &Pipeline[*testObject, *testStatus, testFetch, testObservation]{
		Client:         cl,
		StatusClient:   cl.Status(),
		Recorder:       recorder,
		ControllerName: "test",
		Reconciler:     reconciler,
		Scheme:         scheme,
	}

	err := pipeline.Run(context.Background(), obj)

	// May have status update error from fake client
	if err != nil && !strings.Contains(err.Error(), "status update failed") {
		t.Fatalf("Unexpected error: %v", err)
	}

	// AuthValid should transition to True
	var authValid *metav1.Condition
	for i := range obj.Status.Conditions {
		if obj.Status.Conditions[i].Type == "AuthValid" {
			authValid = &obj.Status.Conditions[i]
			break
		}
	}

	if authValid == nil {
		t.Fatal("AuthValid condition should be set")
	}

	if authValid.Status != metav1.ConditionTrue {
		t.Errorf("AuthValid should be True after error clears, got %v", authValid.Status)
	}

	if authValid.Reason != "AuthenticationValid" {
		t.Errorf("AuthValid reason should be AuthenticationValid, got %v", authValid.Reason)
	}

	t.Logf("AuthValid recovered: Status=%v, Reason=%v", authValid.Status, authValid.Reason)
}

func TestPipeline_ErrorRecovery_ConfigValid(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = metav1.AddMetaToScheme(scheme)
	scheme.AddKnownTypes(metav1.SchemeGroupVersion, &testObject{})

	now := metav1.Now()
	obj := &testObject{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "meta.k8s.io/v1",
			Kind:       "testObject",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-obj",
			Namespace: "default",
		},
		Status: testStatus{
			Status: string(constants.AIMStatusFailed),
			Conditions: []metav1.Condition{
				{
					Type:               "ConfigValid",
					Status:             metav1.ConditionFalse,
					Reason:             "InvalidSpec",
					Message:            "Configuration validation failed",
					LastTransitionTime: now,
				},
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(obj).Build()
	recorder := record.NewFakeRecorder(100)

	// Config error cleared
	reconciler := &testReconciler{
		fetchResult: testFetch{ModelReady: true},
	}

	pipeline := &Pipeline[*testObject, *testStatus, testFetch, testObservation]{
		Client:         cl,
		StatusClient:   cl.Status(),
		Recorder:       recorder,
		ControllerName: "test",
		Reconciler:     reconciler,
		Scheme:         scheme,
	}

	err := pipeline.Run(context.Background(), obj)

	if err != nil && !strings.Contains(err.Error(), "status update failed") {
		t.Fatalf("Unexpected error: %v", err)
	}

	// ConfigValid should transition to True
	var configValid *metav1.Condition
	for i := range obj.Status.Conditions {
		if obj.Status.Conditions[i].Type == "ConfigValid" {
			configValid = &obj.Status.Conditions[i]
			break
		}
	}

	if configValid == nil {
		t.Fatal("ConfigValid condition should be set")
	}

	if configValid.Status != metav1.ConditionTrue {
		t.Errorf("ConfigValid should be True after error clears, got %v", configValid.Status)
	}

	if configValid.Reason != "ConfigurationValid" {
		t.Errorf("ConfigValid reason should be ConfigurationValid, got %v", configValid.Reason)
	}

	t.Logf("ConfigValid recovered: Status=%v, Reason=%v", configValid.Status, configValid.Reason)
}

func TestPipeline_ErrorRecovery_ComponentAuthError(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = metav1.AddMetaToScheme(scheme)
	scheme.AddKnownTypes(metav1.SchemeGroupVersion, &testObject{})

	now := metav1.Now()
	obj := &testObject{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "meta.k8s.io/v1",
			Kind:       "testObject",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-obj",
			Namespace: "default",
		},
		Status: testStatus{
			Status: string(constants.AIMStatusFailed),
			Conditions: []metav1.Condition{
				{
					Type:               testConditionTypeModelReady,
					Status:             metav1.ConditionFalse,
					Reason:             "AuthError",
					Message:            "Forbidden",
					LastTransitionTime: now,
				},
				{
					Type:               "AuthValid",
					Status:             metav1.ConditionFalse,
					Reason:             "AuthError",
					Message:            "Authentication or authorization failure",
					LastTransitionTime: now,
				},
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(obj).Build()
	recorder := record.NewFakeRecorder(100)

	// Auth error cleared - component now ready
	reconciler := &testReconciler{
		fetchResult: testFetch{ModelReady: true},
	}

	pipeline := &Pipeline[*testObject, *testStatus, testFetch, testObservation]{
		Client:         cl,
		StatusClient:   cl.Status(),
		Recorder:       recorder,
		ControllerName: "test",
		Reconciler:     reconciler,
		Scheme:         scheme,
	}

	err := pipeline.Run(context.Background(), obj)

	if err != nil && !strings.Contains(err.Error(), "status update failed") {
		t.Fatalf("Unexpected error: %v", err)
	}

	// ModelReady should transition to True
	var modelReady *metav1.Condition
	for i := range obj.Status.Conditions {
		if obj.Status.Conditions[i].Type == testConditionTypeModelReady {
			modelReady = &obj.Status.Conditions[i]
			break
		}
	}

	if modelReady == nil {
		t.Fatal("ModelReady condition should be set")
	}

	if modelReady.Status != metav1.ConditionTrue {
		t.Errorf("ModelReady should be True after auth error clears, got %v", modelReady.Status)
	}

	// AuthValid should also be True
	var authValid *metav1.Condition
	for i := range obj.Status.Conditions {
		if obj.Status.Conditions[i].Type == "AuthValid" {
			authValid = &obj.Status.Conditions[i]
			break
		}
	}

	if authValid == nil {
		t.Fatal("AuthValid condition should be set")
	}

	if authValid.Status != metav1.ConditionTrue {
		t.Errorf("AuthValid should be True after error clears, got %v", authValid.Status)
	}

	// Status should transition to Ready
	if obj.Status.Status != string(constants.AIMStatusReady) {
		t.Errorf("Status should be Ready after recovery, got %v", obj.Status.Status)
	}

	t.Logf("Component recovered from auth error: ModelReady=%v, AuthValid=%v, Status=%v",
		modelReady.Status, authValid.Status, obj.Status.Status)
}

func TestPipeline_processStateEngine_AuthValidRecovery(t *testing.T) {
	// Start with AuthValid=False
	oldConditions := []metav1.Condition{
		{
			Type:               "AuthValid",
			Status:             metav1.ConditionFalse,
			Reason:             "AuthError",
			Message:            "Authentication or authorization failure",
			LastTransitionTime: metav1.Now(),
		},
	}

	// No auth errors in observation
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

	cm := NewConditionManager(oldConditions)
	status := &testStatus{}

	p := &Pipeline[*testObject, *testStatus, testFetch, testObservationCustomHealth]{
		Reconciler: &testReconcilerCustomHealth{},
	}

	_, err := p.processStateEngine(context.Background(), obs, cm, status)
	if err != nil {
		t.Fatalf("processStateEngine returned error: %v", err)
	}

	// AuthValid should now be True
	authValid := cm.Get("AuthValid")
	if authValid == nil {
		t.Fatal("AuthValid condition should exist")
	}

	if authValid.Status != metav1.ConditionTrue {
		t.Errorf("AuthValid should be True, got %v", authValid.Status)
	}

	if authValid.Reason != "AuthenticationValid" {
		t.Errorf("AuthValid reason should be AuthenticationValid, got %v", authValid.Reason)
	}

	// Verify observability config is AsInfo (not AsError)
	cfg := cm.ConfigFor("AuthValid")
	if cfg.eventMode != EventOnTransition {
		t.Errorf("AuthValid should use EventOnTransition when True, got %v", cfg.eventMode)
	}
	if cfg.logLevel != 0 {
		t.Errorf("AuthValid should use info log (V(0)) when True, got level %v", cfg.logLevel)
	}
}

func TestPipeline_processStateEngine_ConfigValidRecovery(t *testing.T) {
	// Start with ConfigValid=False
	oldConditions := []metav1.Condition{
		{
			Type:               "ConfigValid",
			Status:             metav1.ConditionFalse,
			Reason:             "InvalidSpec",
			Message:            "Configuration validation failed",
			LastTransitionTime: metav1.Now(),
		},
	}

	// No config errors in observation
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

	cm := NewConditionManager(oldConditions)
	status := &testStatus{}

	p := &Pipeline[*testObject, *testStatus, testFetch, testObservationCustomHealth]{
		Reconciler: &testReconcilerCustomHealth{},
	}

	_, err := p.processStateEngine(context.Background(), obs, cm, status)
	if err != nil {
		t.Fatalf("processStateEngine returned error: %v", err)
	}

	// ConfigValid should now be True
	configValid := cm.Get("ConfigValid")
	if configValid == nil {
		t.Fatal("ConfigValid condition should exist")
	}

	if configValid.Status != metav1.ConditionTrue {
		t.Errorf("ConfigValid should be True, got %v", configValid.Status)
	}

	if configValid.Reason != "ConfigurationValid" {
		t.Errorf("ConfigValid reason should be ConfigurationValid, got %v", configValid.Reason)
	}

	// Verify observability config is AsInfo (not AsError)
	cfg := cm.ConfigFor("ConfigValid")
	if cfg.eventMode != EventOnTransition {
		t.Errorf("ConfigValid should use EventOnTransition when True, got %v", cfg.eventMode)
	}
	if cfg.logLevel != 0 {
		t.Errorf("ConfigValid should use info log (V(0)) when True, got level %v", cfg.logLevel)
	}
}

func TestPipeline_processStateEngine_DependenciesReachableRecovery(t *testing.T) {
	// Start with DependenciesReachable=False
	oldConditions := []metav1.Condition{
		{
			Type:               "DependenciesReachable",
			Status:             metav1.ConditionFalse,
			Reason:             "InfrastructureError",
			Message:            "Cannot reach dependencies",
			LastTransitionTime: metav1.Now(),
		},
	}

	// No infrastructure errors in observation
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

	cm := NewConditionManager(oldConditions)
	status := &testStatus{}

	p := &Pipeline[*testObject, *testStatus, testFetch, testObservationCustomHealth]{
		Reconciler: &testReconcilerCustomHealth{},
	}

	_, err := p.processStateEngine(context.Background(), obs, cm, status)
	if err != nil {
		t.Fatalf("processStateEngine returned error: %v", err)
	}

	// DependenciesReachable should now be True
	depReachable := cm.Get("DependenciesReachable")
	if depReachable == nil {
		t.Fatal("DependenciesReachable condition should exist")
	}

	if depReachable.Status != metav1.ConditionTrue {
		t.Errorf("DependenciesReachable should be True, got %v", depReachable.Status)
	}

	if depReachable.Reason != "Reachable" {
		t.Errorf("DependenciesReachable reason should be Reachable, got %v", depReachable.Reason)
	}

	// Verify observability config is AsInfo (not AsError)
	cfg := cm.ConfigFor("DependenciesReachable")
	if cfg.eventMode != EventOnTransition {
		t.Errorf("DependenciesReachable should use EventOnTransition when True, got %v", cfg.eventMode)
	}
	if cfg.logLevel != 0 {
		t.Errorf("DependenciesReachable should use info log (V(0)) when True, got level %v", cfg.logLevel)
	}
}

// ======================================================
// CRITICAL BUG FIX TESTS
// ======================================================

func TestPipeline_MissingUpstreamDep_BlocksApply(t *testing.T) {
	// Test that missing upstream dependencies block apply (ShouldApply=false)
	upstreamErr := NewMissingUpstreamDependencyError("SecretNotFound", "Secret 'my-secret' not found in namespace 'default'", errors.New("secret not found"))
	obs := testObservationWithError{infraError: upstreamErr}
	cm := NewConditionManager([]metav1.Condition{})
	status := &testStatus{}

	p := &Pipeline[*testObject, *testStatus, testFetch, testObservationWithError]{
		Reconciler: &testReconcilerWithError{infraError: upstreamErr},
	}

	decision, err := p.processStateEngine(context.Background(), obs, cm, status)
	if err != nil {
		t.Fatalf("processStateEngine returned error: %v", err)
	}

	if decision.ShouldApply {
		t.Error("ShouldApply should be false for missing upstream dependency")
	}

	if decision.ShouldRequeue {
		t.Error("ShouldRequeue should be false for missing upstream dependency (not retriable)")
	}

	// Verify ConfigValid condition is set to False
	configValid := cm.Get(ConditionTypeConfigValid)
	if configValid == nil {
		t.Fatal("ConfigValid condition should be set")
	}
	if configValid.Status != metav1.ConditionFalse {
		t.Errorf("ConfigValid should be False for missing upstream dep, got %v", configValid.Status)
	}
	if configValid.Reason != ReasonMissingRef {
		t.Errorf("ConfigValid reason should be %s, got %s", ReasonMissingRef, configValid.Reason)
	}

	// Verify Ready condition is set to False
	ready := cm.Get(ConditionTypeReady)
	if ready == nil {
		t.Fatal("Ready condition should be set")
	}
	if ready.Status != metav1.ConditionFalse {
		t.Errorf("Ready should be False for missing upstream dep, got %v", ready.Status)
	}
}

func TestPipeline_Run_ApplyError_SetsDependenciesReachable(t *testing.T) {
	// Test that apply errors set DependenciesReachable=False and return InfrastructureError
	scheme := runtime.NewScheme()
	_ = metav1.AddMetaToScheme(scheme)
	scheme.AddKnownTypes(schema.GroupVersion{Group: "test.k8s.io", Version: "v1"}, &testObject{})

	now := metav1.Now()
	obj := &testObject{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "test.k8s.io/v1",
			Kind:       "TestObject",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-obj",
			Namespace:         "default",
			CreationTimestamp: now,
		},
	}

	// Create a fake client that will fail on Apply
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(obj).WithStatusSubresource(obj).Build()

	// Create a test reconciler that returns resources to apply
	reconciler := &testReconcilerWithPlan{
		fetchResult: testFetch{ModelReady: true},
		planResult: PlanResult{
			toApply: []client.Object{
				&testObject{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "test.k8s.io/v1",
						Kind:       "TestObject",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "child-resource",
						Namespace: "default",
					},
				},
			},
		},
	}

	recorder := record.NewFakeRecorder(10)
	p := &Pipeline[*testObject, *testStatus, testFetch, testObservation]{
		Client:         &failingApplyClient{Client: fakeClient},
		StatusClient:   fakeClient.Status(),
		Recorder:       recorder,
		Reconciler:     reconciler,
		Scheme:         scheme,
		ControllerName: "test",
	}

	err := p.Run(context.Background(), obj)

	// Should return InfrastructureError
	if err == nil {
		t.Fatal("Expected error from apply failure, got nil")
	}

	var infraErr InfrastructureError
	if !errors.As(err, &infraErr) {
		t.Errorf("Expected InfrastructureError, got %T: %v", err, err)
	}

	if infraErr.Count != 1 {
		t.Errorf("Expected 1 infrastructure error, got %d", infraErr.Count)
	}

	// Verify DependenciesReachable is False
	depReachable := findCondition(obj.Status.Conditions, ConditionTypeDependenciesReachable)
	if depReachable == nil {
		t.Fatal("DependenciesReachable condition should be set")
	}
	if depReachable.Status != metav1.ConditionFalse {
		t.Errorf("DependenciesReachable should be False after apply error, got %v", depReachable.Status)
	}
	if !strings.Contains(depReachable.Message, "Failed to apply") {
		t.Errorf("DependenciesReachable message should mention apply failure, got: %s", depReachable.Message)
	}
}

func TestPipeline_Run_DeleteError_SetsDependenciesReachable(t *testing.T) {
	// Test that delete errors set DependenciesReachable=False and return InfrastructureError
	scheme := runtime.NewScheme()
	_ = metav1.AddMetaToScheme(scheme)
	scheme.AddKnownTypes(schema.GroupVersion{Group: "test.k8s.io", Version: "v1"}, &testObject{})

	now := metav1.Now()
	obj := &testObject{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "test.k8s.io/v1",
			Kind:       "TestObject",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-obj",
			Namespace:         "default",
			CreationTimestamp: now,
		},
	}

	// Create a fake client that will fail on Delete
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(obj).WithStatusSubresource(obj).Build()

	// Create a test reconciler that returns resources to delete
	reconciler := &testReconcilerWithPlan{
		fetchResult: testFetch{ModelReady: true},
		planResult: PlanResult{
			toDelete: []client.Object{
				&testObject{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "test.k8s.io/v1",
						Kind:       "TestObject",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "child-to-delete",
						Namespace: "default",
					},
				},
			},
		},
	}

	recorder := record.NewFakeRecorder(10)
	p := &Pipeline[*testObject, *testStatus, testFetch, testObservation]{
		Client:         &failingDeleteClient{Client: fakeClient},
		StatusClient:   fakeClient.Status(),
		Recorder:       recorder,
		Reconciler:     reconciler,
		Scheme:         scheme,
		ControllerName: "test",
	}

	err := p.Run(context.Background(), obj)

	// Should return InfrastructureError
	if err == nil {
		t.Fatal("Expected error from delete failure, got nil")
	}

	var infraErr InfrastructureError
	if !errors.As(err, &infraErr) {
		t.Errorf("Expected InfrastructureError, got %T: %v", err, err)
	}

	if infraErr.Count != 1 {
		t.Errorf("Expected 1 infrastructure error, got %d", infraErr.Count)
	}

	// Verify DependenciesReachable is False
	depReachable := findCondition(obj.Status.Conditions, ConditionTypeDependenciesReachable)
	if depReachable == nil {
		t.Fatal("DependenciesReachable condition should be set")
	}
	if depReachable.Status != metav1.ConditionFalse {
		t.Errorf("DependenciesReachable should be False after delete error, got %v", depReachable.Status)
	}
	if !strings.Contains(depReachable.Message, "Failed to delete") {
		t.Errorf("DependenciesReachable message should mention delete failure, got: %s", depReachable.Message)
	}
}

func TestInfrastructureError_StableMessage(t *testing.T) {
	tests := []struct {
		name          string
		count         int
		errors        []error
		wantMessage   string
		wantUnwrapLen int
	}{
		{
			name:          "single error",
			count:         1,
			errors:        []error{errors.New("network timeout")},
			wantMessage:   "infrastructure error (1 failure)",
			wantUnwrapLen: 1,
		},
		{
			name:          "multiple errors",
			count:         3,
			errors:        []error{errors.New("timeout 1"), errors.New("timeout 2"), errors.New("timeout 3")},
			wantMessage:   "infrastructure errors (3 failures)",
			wantUnwrapLen: 3,
		},
		{
			name:          "different error details but same count",
			count:         2,
			errors:        []error{errors.New("completely different error"), errors.New("another different error")},
			wantMessage:   "infrastructure errors (2 failures)",
			wantUnwrapLen: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			infraErr := InfrastructureError{
				Count:  tt.count,
				Errors: tt.errors,
			}

			// Test Error() returns stable message
			if infraErr.Error() != tt.wantMessage {
				t.Errorf("Error() = %q, want %q", infraErr.Error(), tt.wantMessage)
			}

			// Test Unwrap() returns underlying errors
			unwrapped := infraErr.Unwrap()
			if len(unwrapped) != tt.wantUnwrapLen {
				t.Errorf("Unwrap() returned %d errors, want %d", len(unwrapped), tt.wantUnwrapLen)
			}

			// Verify errors.As works with the error wrapped
			wrappedErr := fmt.Errorf("wrapped: %w", infraErr)
			var asInfraErr InfrastructureError
			if !errors.As(wrappedErr, &asInfraErr) {
				t.Error("errors.As should work with InfrastructureError")
			}
			if asInfraErr.Count != tt.count {
				t.Errorf("errors.As preserved Count: got %d, want %d", asInfraErr.Count, tt.count)
			}
		})
	}
}

// Helper type for testing apply failures
type failingApplyClient struct {
	client.Client
}

func (c *failingApplyClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	// Simulate apply failure
	return errors.New("simulated apply failure: insufficient permissions")
}

// Helper type for testing delete failures
type failingDeleteClient struct {
	client.Client
}

func (c *failingDeleteClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	// Simulate delete failure
	return errors.New("simulated delete failure: resource locked")
}

// Helper reconciler that returns a plan with resources
type testReconcilerWithPlan struct {
	fetchResult testFetch
	planResult  PlanResult
}

func (r *testReconcilerWithPlan) FetchRemoteState(ctx context.Context, c client.Client, obj ReconcileContext[*testObject]) testFetch {
	return r.fetchResult
}

func (r *testReconcilerWithPlan) ComposeState(ctx context.Context, obj ReconcileContext[*testObject], fetched testFetch) testObservation {
	return testObservation{modelReady: fetched.ModelReady}
}

func (r *testReconcilerWithPlan) PlanResources(ctx context.Context, obj ReconcileContext[*testObject], obs testObservation) PlanResult {
	return r.planResult
}

// Helper to find a condition by type
func findCondition(conditions []metav1.Condition, condType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}
