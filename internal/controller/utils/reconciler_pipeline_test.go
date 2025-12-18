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
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// Core Pipeline.Run() integration tests

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
