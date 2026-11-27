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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	fieldsTestSuffix  = "adapter-disk"
	fieldsTestManager = ssaAimFieldManager + "-" + fieldsTestSuffix
	fieldsTestKey     = "flag"
	fieldsTestReason  = "FlagTurnedOn"
)

// fieldsTestPatch is a partial ConfigMap carrying only its identity and one key.
func fieldsTestPatch() *unstructured.Unstructured {
	patch := &unstructured.Unstructured{Object: map[string]any{
		"data": map[string]any{fieldsTestKey: "true"},
	}}
	patch.SetAPIVersion("v1")
	patch.SetKind(coreConfigMapKind)
	patch.SetNamespace(ssaTestNamespace)
	patch.SetName(ssaTestConfigMap)
	return patch
}

// TestApplyFieldsState_FieldSurvivesMainManagerReapply locks the ApplyFields
// contract: the field is owned by "<manager>-<suffix>", so a later apply by the
// main manager that omits it leaves it in place and one that changes it
// conflicts; nothing but the field is added (no owner reference, labels or other
// keys); and the event is recorded on the observed target.
func TestApplyFieldsState_FieldSurvivesMainManagerReapply(t *testing.T) {
	ctx := context.Background()
	scheme := ssaTestScheme(t)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	if err := ApplyDesiredState(ctx, cl, ssaAimFieldManager, scheme, aimDesiredConfigMap(), nil); err != nil {
		t.Fatalf("main manager apply failed: %v", err)
	}
	target := &corev1.ConfigMap{}
	if err := cl.Get(ctx, client.ObjectKey{Namespace: ssaTestNamespace, Name: ssaTestConfigMap}, target); err != nil {
		t.Fatalf("get configmap: %v", err)
	}

	recorder := record.NewFakeRecorder(4)
	fields := []FieldApply{{
		Object:             fieldsTestPatch(),
		FieldManagerSuffix: fieldsTestSuffix,
		Event:              &FieldApplyEvent{Target: target, Reason: fieldsTestReason, Message: "turned on"},
	}}
	if err := ApplyFieldsState(ctx, cl, recorder, ssaAimFieldManager, scheme, fields); err != nil {
		t.Fatalf("ApplyFieldsState failed: %v", err)
	}

	// The main manager re-applies its full desired object, which omits the field.
	if err := ApplyDesiredState(ctx, cl, ssaAimFieldManager, scheme, aimDesiredConfigMap(), nil); err != nil {
		t.Fatalf("main manager re-apply failed: %v", err)
	}

	got := &corev1.ConfigMap{}
	if err := cl.Get(ctx, client.ObjectKeyFromObject(target), got); err != nil {
		t.Fatalf("get configmap: %v", err)
	}
	if got.Data[fieldsTestKey] != "true" {
		t.Fatalf("data = %v, want %s to survive the main manager's re-apply", got.Data, fieldsTestKey)
	}
	if got.Data[ssaContendedKey] != ssaAimFieldData || len(got.Data) != 2 {
		t.Errorf("data = %v, want only the main manager's key plus %s", got.Data, fieldsTestKey)
	}
	if len(got.OwnerReferences) != 0 || len(got.Labels) != 0 {
		t.Errorf("field apply added owner references %v or labels %v", got.OwnerReferences, got.Labels)
	}

	// The field belongs to the dedicated manager, so the main manager cannot
	// change it without force.
	takeover := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: ssaTestNamespace, Name: ssaTestConfigMap},
		Data:       map[string]string{ssaContendedKey: ssaAimFieldData, fieldsTestKey: "false"},
	}
	err := ApplyDesiredState(ctx, cl, ssaAimFieldManager, scheme, []client.Object{takeover}, nil)
	if !apierrors.IsConflict(err) {
		t.Fatalf("main manager changing %s: error = %v, want a conflict with %s", fieldsTestKey, err, fieldsTestManager)
	}

	select {
	case ev := <-recorder.Events:
		if !strings.Contains(ev, corev1.EventTypeNormal+" "+fieldsTestReason) {
			t.Errorf("event = %q, want Normal %s", ev, fieldsTestReason)
		}
	default:
		t.Error("no event recorded for the successful field apply")
	}
}

// TestApplyFieldsState_FailedApplyRecordsNoEvent verifies a failed field apply is
// returned and does not record its event, while the other field applies still run.
func TestApplyFieldsState_FailedApplyRecordsNoEvent(t *testing.T) {
	ctx := context.Background()
	scheme := ssaTestScheme(t)
	applyErr := errors.New("apply failed")
	cl := &failConfigMapPatchClient{
		Client:   fake.NewClientBuilder().WithScheme(scheme).Build(),
		err:      applyErr,
		failName: ssaTestConfigMap,
	}

	other := fieldsTestPatch()
	other.SetName("other")
	recorder := record.NewFakeRecorder(4)
	fields := []FieldApply{
		{
			Object:             fieldsTestPatch(),
			FieldManagerSuffix: fieldsTestSuffix,
			Event:              &FieldApplyEvent{Target: &corev1.ConfigMap{}, Reason: fieldsTestReason},
		},
		{Object: other, FieldManagerSuffix: fieldsTestSuffix},
	}

	if err := ApplyFieldsState(ctx, cl, recorder, ssaAimFieldManager, scheme, fields); !errors.Is(err, applyErr) {
		t.Fatalf("ApplyFieldsState error = %v, want %v", err, applyErr)
	}
	if len(cl.patchedKinds) != len(fields) {
		t.Errorf("Patch() calls = %d, want %d", len(cl.patchedKinds), len(fields))
	}
	select {
	case ev := <-recorder.Events:
		t.Errorf("event %q recorded for a failed field apply", ev)
	default:
	}
}

type fixedPlanReconciler struct {
	testReconciler
	plan PlanResult
}

func (r *fixedPlanReconciler) PlanResources(context.Context, ReconcileContext[*testObject], testObservation) PlanResult {
	return r.plan
}

// TestPipelineRun_SkipsFieldAppliesAfterApplyError verifies field applies run
// only once every earlier apply succeeded, so a field is never set on another
// controller's object while this controller's own resources failed to apply.
func TestPipelineRun_SkipsFieldAppliesAfterApplyError(t *testing.T) {
	scheme := ssaTestScheme(t)
	scheme.AddKnownTypes(metav1.SchemeGroupVersion, &testObject{})
	obj := &testObject{
		TypeMeta:   metav1.TypeMeta{APIVersion: "meta.k8s.io/v1", Kind: "testObject"},
		ObjectMeta: metav1.ObjectMeta{Name: "test-obj", Namespace: ssaTestNamespace},
	}
	cl := &failConfigMapPatchClient{
		Client:   fake.NewClientBuilder().WithScheme(scheme).WithObjects(obj).Build(),
		err:      errors.New("apply failed"),
		failName: ssaTestConfigMap,
	}

	var plan PlanResult
	plan.ApplyWithoutOwnerRef(aimDesiredConfigMap()[0])
	field := fieldsTestPatch()
	field.SetName("other")
	plan.ApplyFields(field, fieldsTestSuffix, nil)

	pipeline := &Pipeline[*testObject, *testStatus, testFetch, testObservation]{
		Client:         cl,
		StatusClient:   cl.Status(),
		Recorder:       record.NewFakeRecorder(16),
		ControllerName: "test",
		Reconciler:     &fixedPlanReconciler{plan: plan},
		Scheme:         scheme,
	}
	// The status write fails on the fake client; only the applies matter here.
	_, _ = pipeline.Run(context.Background(), obj)

	if len(cl.patchedKinds) != 1 {
		t.Errorf("Patch() calls = %v, want only the failing unowned apply", cl.patchedKinds)
	}
	err := cl.Get(context.Background(), client.ObjectKey{Namespace: ssaTestNamespace, Name: "other"}, &corev1.ConfigMap{})
	if !apierrors.IsNotFound(err) {
		t.Errorf("field apply target: get error = %v, want NotFound", err)
	}
}

// TestPlanResultMerge_CarriesFieldApplies verifies Merge keeps field applies, so
// composing sub-plans cannot silently drop them.
func TestPlanResultMerge_CarriesFieldApplies(t *testing.T) {
	var base, sub PlanResult
	sub.ApplyFields(fieldsTestPatch(), fieldsTestSuffix, nil)
	base.Merge(sub)
	if got := len(base.GetToApplyFields()); got != 1 {
		t.Fatalf("merged field applies = %d, want 1", got)
	}
}
