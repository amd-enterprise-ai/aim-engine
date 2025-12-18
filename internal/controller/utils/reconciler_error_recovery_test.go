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
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

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
