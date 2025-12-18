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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

// Shared test types for all reconciler tests

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
