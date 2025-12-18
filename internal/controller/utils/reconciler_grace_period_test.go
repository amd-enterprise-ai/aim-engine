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
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

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
