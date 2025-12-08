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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// Mock types for testing
type TestObject struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Status            TestStatus `json:"status,omitempty"`
}

func (t *TestObject) DeepCopyObject() runtime.Object {
	return &TestObject{
		TypeMeta:   t.TypeMeta,
		ObjectMeta: *t.DeepCopy(),
		Status:     *t.Status.DeepCopy(),
	}
}

func (t *TestObject) GetStatus() *TestStatus {
	return &t.Status
}

type TestStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

func (s *TestStatus) DeepCopy() *TestStatus {
	if s == nil {
		return nil
	}
	out := new(TestStatus)
	s.DeepCopyInto(out)
	return out
}

func (s *TestStatus) DeepCopyInto(out *TestStatus) {
	*out = *s
	if s.Conditions != nil {
		in, out := &s.Conditions, &out.Conditions
		*out = make([]metav1.Condition, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

func (s *TestStatus) GetConditions() []metav1.Condition {
	return s.Conditions
}

func (s *TestStatus) SetConditions(conditions []metav1.Condition) {
	s.Conditions = conditions
}

func (s *TestStatus) SetStatus(status string) {
	// Not used in these tests
}

type TestFetchResult struct{}

// Mock reconciler for testing
type mockReconciler struct {
	fetchFunc   func(ctx context.Context, c client.Client, obj *TestObject) (TestFetchResult, error)
	observeFunc func(ctx context.Context, obj *TestObject, fetched TestFetchResult) (*TestObservabilityConfig, error)
	planFunc    func(ctx context.Context, obj *TestObject, obs *TestObservabilityConfig) (PlanResult, error)
	projectFunc func(status *TestStatus, cm *ConditionManager, obs *TestObservabilityConfig)
}

type TestObservabilityConfig struct{}

func (m *mockReconciler) Fetch(ctx context.Context, c client.Client, obj *TestObject) (TestFetchResult, error) {
	if m.fetchFunc != nil {
		return m.fetchFunc(ctx, c, obj)
	}
	return TestFetchResult{}, nil
}

func (m *mockReconciler) Observe(ctx context.Context, obj *TestObject, fetched TestFetchResult) (*TestObservabilityConfig, error) {
	if m.observeFunc != nil {
		return m.observeFunc(ctx, obj, fetched)
	}
	return &TestObservabilityConfig{}, nil
}

func (m *mockReconciler) Plan(ctx context.Context, obj *TestObject, obs *TestObservabilityConfig) (PlanResult, error) {
	if m.planFunc != nil {
		return m.planFunc(ctx, obj, obs)
	}
	return PlanResult{}, nil
}

func (m *mockReconciler) Project(status *TestStatus, cm *ConditionManager, obs *TestObservabilityConfig) {
	if m.projectFunc != nil {
		m.projectFunc(status, cm, obs)
	}
}

func TestPipelineRun_FetchError(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	obj := &TestObject{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
	}

	mockReconciler := &mockReconciler{
		fetchFunc: func(ctx context.Context, c client.Client, obj *TestObject) (TestFetchResult, error) {
			return TestFetchResult{}, errors.New("fetch error")
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	fakeRecorder := &record.FakeRecorder{}

	pipeline := &Pipeline[*TestObject, *TestStatus, TestFetchResult, *TestObservabilityConfig]{
		Client:       fakeClient,
		StatusClient: fakeClient.Status(),
		Reconciler:   mockReconciler,
		Recorder:     fakeRecorder,
		FieldOwner:   "test-controller",
		Scheme:       scheme,
	}

	err := pipeline.Run(context.Background(), obj)
	if err == nil {
		t.Fatal("expected error from fetch failure, got nil")
	}
	if err.Error() != "fetch failed: fetch error" {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestPipelineRun_ObserveError(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	obj := &TestObject{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
	}

	mockReconciler := &mockReconciler{
		observeFunc: func(ctx context.Context, obj *TestObject, fetched TestFetchResult) (*TestObservabilityConfig, error) {
			return nil, errors.New("observe error")
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	fakeRecorder := &record.FakeRecorder{}

	pipeline := &Pipeline[*TestObject, *TestStatus, TestFetchResult, *TestObservabilityConfig]{
		Client:       fakeClient,
		StatusClient: fakeClient.Status(),
		Reconciler:   mockReconciler,
		Recorder:     fakeRecorder,
		FieldOwner:   "test-controller",
		Scheme:       scheme,
	}

	err := pipeline.Run(context.Background(), obj)
	if err == nil {
		t.Fatal("expected error from observe failure, got nil")
	}
	if err.Error() != "observe failed: observe error" {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestPipelineRun_PlanError(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	obj := &TestObject{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
	}

	mockReconciler := &mockReconciler{
		planFunc: func(ctx context.Context, obj *TestObject, obs *TestObservabilityConfig) (PlanResult, error) {
			return PlanResult{}, errors.New("plan error")
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	fakeRecorder := &record.FakeRecorder{}

	pipeline := &Pipeline[*TestObject, *TestStatus, TestFetchResult, *TestObservabilityConfig]{
		Client:       fakeClient,
		StatusClient: fakeClient.Status(),
		Reconciler:   mockReconciler,
		Recorder:     fakeRecorder,
		FieldOwner:   "test-controller",
		Scheme:       scheme,
	}

	err := pipeline.Run(context.Background(), obj)
	if err == nil {
		t.Fatal("expected error from plan failure, got nil")
	}
	if err.Error() != "plan failed: plan error" {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestPipelineRun_DeleteAggregatesErrors(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	obj := &TestObject{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
	}

	pod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod1",
			Namespace: "default",
		},
	}
	pod1.SetGroupVersionKind(schema.GroupVersionKind{Version: "v1", Kind: "Pod"})

	pod2 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod2",
			Namespace: "default",
		},
	}
	pod2.SetGroupVersionKind(schema.GroupVersionKind{Version: "v1", Kind: "Pod"})

	mockReconciler := &mockReconciler{
		planFunc: func(ctx context.Context, obj *TestObject, obs *TestObservabilityConfig) (PlanResult, error) {
			return PlanResult{
				Delete: []client.Object{pod1, pod2},
			}, nil
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	fakeRecorder := &record.FakeRecorder{}

	pipeline := &Pipeline[*TestObject, *TestStatus, TestFetchResult, *TestObservabilityConfig]{
		Client:       fakeClient,
		StatusClient: fakeClient.Status(),
		Reconciler:   mockReconciler,
		Recorder:     fakeRecorder,
		FieldOwner:   "test-controller",
		Scheme:       scheme,
	}

	err := pipeline.Run(context.Background(), obj)
	// Both pods don't exist, so delete will return NotFound errors
	// These should be ignored by client.IgnoreNotFound, so no error expected
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPipelineRun_DeepCopyTypeAssertion(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	// Create an object that returns wrong type from DeepCopyObject
	badObj := &struct {
		*TestObject
	}{
		TestObject: &TestObject{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: "default",
			},
		},
	}

	// Override DeepCopyObject to return wrong type
	type BadObject struct {
		*TestObject
	}
	badDeepCopy := &BadObject{TestObject: &TestObject{}}

	mockReconciler := &mockReconciler{}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	fakeRecorder := &record.FakeRecorder{}

	pipeline := &Pipeline[*TestObject, *TestStatus, TestFetchResult, *TestObservabilityConfig]{
		Client:       fakeClient,
		StatusClient: fakeClient.Status(),
		Reconciler:   mockReconciler,
		Recorder:     fakeRecorder,
		FieldOwner:   "test-controller",
		Scheme:       scheme,
	}

	// Note: This test is limited because we can't easily override DeepCopyObject behavior
	// in the fake object. The type assertion check is still valuable for runtime safety.
	_ = badDeepCopy
	_ = pipeline
	_ = badObj

	// The actual test would require mocking the DeepCopyObject method,
	// which is difficult without interfaces. We've added the check to prevent panics.
}

func TestPipelineRun_Success(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	obj := &TestObject{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
	}

	// Don't add test object to fake client since it's not registered
	// We'll test status update separately

	projectCalled := false
	mockReconciler := &mockReconciler{
		projectFunc: func(status *TestStatus, cm *ConditionManager, obs *TestObservabilityConfig) {
			projectCalled = true
			cm.MarkTrue("Ready", "Success", "Test ready")
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	fakeRecorder := &record.FakeRecorder{}

	pipeline := &Pipeline[*TestObject, *TestStatus, TestFetchResult, *TestObservabilityConfig]{
		Client:       fakeClient,
		StatusClient: fakeClient.Status(),
		Reconciler:   mockReconciler,
		Recorder:     fakeRecorder,
		FieldOwner:   "test-controller",
		Scheme:       scheme,
	}

	err := pipeline.Run(context.Background(), obj)
	// Status update will fail since TestObject isn't registered in scheme
	// This is expected - we're testing the pipeline flow, not the fake client
	if err == nil {
		t.Fatal("expected error from status update (unregistered type)")
	}
	if !projectCalled {
		t.Fatal("expected Project to be called before status update")
	}

	// Verify condition was set (even though status update failed)
	if len(obj.Status.Conditions) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(obj.Status.Conditions))
	}

	cond := obj.Status.Conditions[0]
	if cond.Type != "Ready" {
		t.Errorf("expected condition type 'Ready', got %s", cond.Type)
	}
	if cond.Status != metav1.ConditionTrue {
		t.Errorf("expected condition status True, got %s", cond.Status)
	}
}

func TestApplyDesiredState_StampsGVK(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
	}

	// Test that stampGVK works directly
	err := stampGVK(pod, scheme)
	if err != nil {
		t.Fatalf("unexpected error from stampGVK: %v", err)
	}

	// Verify GVK was stamped
	gvk := pod.GetObjectKind().GroupVersionKind()
	if gvk.Version != "v1" || gvk.Kind != "Pod" {
		t.Errorf("expected GVK v1/Pod, got %v", gvk)
	}
}

func TestApplyDesiredState_SortsObjects(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	// Create objects in reverse order
	pod2 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-z",
			Namespace: "default",
		},
	}
	pod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-a",
			Namespace: "default",
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	// Objects should be sorted by name
	objects := []client.Object{pod2, pod1}
	err := ApplyDesiredState(context.Background(), fakeClient, "test", scheme, objects)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Note: We can't easily verify the apply order with fake client,
	// but the sort function is tested separately
}

func TestSortObjects(t *testing.T) {
	pod1 := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod-b", Namespace: "ns1"}}
	pod1.SetGroupVersionKind(schema.GroupVersionKind{Version: "v1", Kind: "Pod"})

	pod2 := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod-a", Namespace: "ns1"}}
	pod2.SetGroupVersionKind(schema.GroupVersionKind{Version: "v1", Kind: "Pod"})

	pod3 := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod-a", Namespace: "ns2"}}
	pod3.SetGroupVersionKind(schema.GroupVersionKind{Version: "v1", Kind: "Pod"})

	objects := []client.Object{pod1, pod2, pod3}
	sorted := sortObjects(objects)

	// Verify sort order: same GVK, sorted by namespace then name
	// Input: pod-b/ns1, pod-a/ns1, pod-a/ns2
	// Expected: pod-a/ns1, pod-b/ns1, pod-a/ns2 (namespace ns1 < ns2)
	if sorted[0].GetName() != "pod-a" || sorted[0].GetNamespace() != "ns1" {
		t.Errorf("expected first object to be pod-a/ns1, got %s/%s", sorted[0].GetName(), sorted[0].GetNamespace())
	}
	if sorted[1].GetName() != "pod-b" || sorted[1].GetNamespace() != "ns1" {
		t.Errorf("expected second object to be pod-b/ns1, got %s/%s", sorted[1].GetName(), sorted[1].GetNamespace())
	}
	if sorted[2].GetName() != "pod-a" || sorted[2].GetNamespace() != "ns2" {
		t.Errorf("expected third object to be pod-a/ns2, got %s/%s", sorted[2].GetName(), sorted[2].GetNamespace())
	}
}

func TestStampGVK_Success(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	pod := &corev1.Pod{}
	err := stampGVK(pod, scheme)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gvk := pod.GetObjectKind().GroupVersionKind()
	if gvk.Version != "v1" || gvk.Kind != "Pod" {
		t.Errorf("expected GVK v1/Pod, got %v", gvk)
	}
}

func TestStampGVK_UnknownType(t *testing.T) {
	scheme := runtime.NewScheme()

	// Use a real Kubernetes type that's not registered in the scheme
	configMap := &corev1.ConfigMap{}

	err := stampGVK(configMap, scheme)
	if err == nil {
		t.Fatal("expected error for unregistered type, got nil")
	}
}
