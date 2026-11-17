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

package aimmodel

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

const templateFieldsIgnoredPrefix = "These fields have no effect on profile-based serving and can be removed: "

type countingStatusWriter struct {
	client.StatusWriter
	updates int
}

func (w *countingStatusWriter) Update(
	ctx context.Context,
	obj client.Object,
	opts ...client.SubResourceUpdateOption,
) error {
	w.updates++
	return w.StatusWriter.Update(ctx, obj, opts...)
}

// A model reconciled while it still produced AIMServiceTemplates arrives with
// that pipeline's conditions. The first reconcile must replace them in its
// single status write and warn once about the template-only field.
func TestPipelineRun_ReplacesTemplateEraStatusWithFieldWarning(t *testing.T) {
	ctx := context.Background()
	model := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "template-era", Namespace: "team-a", UID: "model-uid"},
		Spec: aimv1alpha1.AIMModelSpec{
			Image:     testImageModelImage,
			Discovery: &aimv1alpha1.AIMModelDiscoveryConfig{ExtractMetadata: ptr.To(false)},
			Env:       []corev1.EnvVar{{Name: "VLLM_LOGGING_LEVEL", Value: "DEBUG"}},
		},
		Status: aimv1alpha1.AIMModelStatus{Conditions: []metav1.Condition{
			{
				Type:   controllerutils.ConditionTypeMigrationRequired,
				Status: metav1.ConditionTrue,
				Reason: "TemplateAPIDeprecated",
			},
			{Type: "ServiceTemplatesReady", Status: metav1.ConditionFalse, Reason: "TemplatesNotReady"},
		}},
	}
	c := imageModelFakeClient(t, model)
	recorder := record.NewFakeRecorder(20)
	statusWriter := &countingStatusWriter{StatusWriter: c.Status()}
	pipeline := controllerutils.Pipeline[*aimv1alpha2.AIMModel, *aimv1alpha1.AIMModelStatus, ModelFetchResult, ModelObservation]{
		Client:         c,
		StatusClient:   statusWriter,
		Recorder:       recorder,
		Reconciler:     &ModelReconciler{},
		Scheme:         c.Scheme(),
		ControllerName: "model",
	}
	reconcileModel := func() aimv1alpha1.AIMModelStatus {
		t.Helper()
		current := &aimv1alpha2.AIMModel{}
		if err := c.Get(ctx, client.ObjectKeyFromObject(model), current); err != nil {
			t.Fatal(err)
		}
		if _, err := pipeline.Run(ctx, current); err != nil {
			t.Fatal(err)
		}
		if err := c.Get(ctx, client.ObjectKeyFromObject(model), current); err != nil {
			t.Fatal(err)
		}
		return current.Status
	}

	status := reconcileModel()

	if statusWriter.updates != 1 {
		t.Fatalf("status writes = %d, want 1", statusWriter.updates)
	}
	for _, conditionType := range templateEraModelConditions {
		requireConditionAbsent(t, status.Conditions, conditionType)
	}
	warning := meta.FindStatusCondition(status.Conditions, controllerutils.ConditionTypeTemplateFieldsIgnored)
	if warning == nil || warning.Status != metav1.ConditionTrue || warning.Message != templateFieldsIgnoredPrefix+"spec.env" {
		t.Fatalf("TemplateFieldsIgnored = %#v, want True naming spec.env", warning)
	}
	if status.Status != constants.AIMStatusReady {
		t.Fatalf("status = %s, want Ready with a field warning", status.Status)
	}
	if got := drainTemplateFieldsEvents(recorder); got != 1 {
		t.Fatalf("TemplateFieldsSet warning events after first reconcile = %d, want 1", got)
	}

	reconcileModel()

	if got := drainTemplateFieldsEvents(recorder); got != 0 {
		t.Fatalf("TemplateFieldsSet warning events after unchanged reconcile = %d, want 0", got)
	}
}

func TestDecorateStatus_TemplateFieldsIgnoredNamesSetFields(t *testing.T) {
	t.Parallel()

	memory := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("1Gi")},
	}
	tests := []struct {
		name       string
		spec       aimv1alpha1.AIMModelSpec
		wantFields string
	}{
		{
			name: "no template-only fields",
		},
		{
			name: "createServiceTemplates true is the default",
			spec: aimv1alpha1.AIMModelSpec{
				Discovery: &aimv1alpha1.AIMModelDiscoveryConfig{CreateServiceTemplates: ptr.To(true)},
			},
		},
		{
			name: "spec.discovery.createServiceTemplates",
			spec: aimv1alpha1.AIMModelSpec{
				Discovery: &aimv1alpha1.AIMModelDiscoveryConfig{CreateServiceTemplates: ptr.To(false)},
			},
			wantFields: "spec.discovery.createServiceTemplates",
		},
		{
			name:       "spec.defaultServiceTemplate",
			spec:       aimv1alpha1.AIMModelSpec{DefaultServiceTemplate: "default-template"},
			wantFields: "spec.defaultServiceTemplate",
		},
		{
			name:       "spec.custom",
			spec:       aimv1alpha1.AIMModelSpec{Custom: &aimv1alpha1.AIMCustomModelSpec{}},
			wantFields: "spec.custom",
		},
		{
			name:       "spec.customTemplates",
			spec:       aimv1alpha1.AIMModelSpec{CustomTemplates: []aimv1alpha1.AIMCustomTemplate{{}}},
			wantFields: "spec.customTemplates",
		},
		{
			name:       "spec.env",
			spec:       aimv1alpha1.AIMModelSpec{Env: []corev1.EnvVar{{Name: "A", Value: "1"}}},
			wantFields: "spec.env",
		},
		{
			name:       "spec.resources",
			spec:       aimv1alpha1.AIMModelSpec{Resources: memory},
			wantFields: "spec.resources",
		},
		{
			name: "all template-only fields",
			spec: aimv1alpha1.AIMModelSpec{
				Discovery:              &aimv1alpha1.AIMModelDiscoveryConfig{CreateServiceTemplates: ptr.To(false)},
				DefaultServiceTemplate: "default-template",
				Custom:                 &aimv1alpha1.AIMCustomModelSpec{},
				CustomTemplates:        []aimv1alpha1.AIMCustomTemplate{{}},
				Env:                    []corev1.EnvVar{{Name: "A", Value: "1"}},
				Resources:              memory,
			},
			wantFields: strings.Join([]string{
				"spec.discovery.createServiceTemplates",
				"spec.defaultServiceTemplate",
				"spec.custom",
				"spec.customTemplates",
				"spec.env",
				"spec.resources",
			}, ", "),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			spec := test.spec
			spec.Image = testImageModelImage

			// Start from a stale warning so the no-fields cases show it clears.
			cm := staleTemplateFieldsWarning()
			(&ModelReconciler{}).DecorateStatus(&aimv1alpha1.AIMModelStatus{}, cm, ModelObservation{
				ModelFetchResult: ModelFetchResult{model: &aimv1alpha2.AIMModel{Spec: spec}},
			})
			requireTemplateFieldsIgnored(t, "AIMModel", cm, test.wantFields)

			cm = staleTemplateFieldsWarning()
			(&ClusterModelReconciler{}).DecorateStatus(&aimv1alpha1.AIMModelStatus{}, cm, ClusterModelObservation{
				ClusterModelFetchResult: ClusterModelFetchResult{model: &aimv1alpha2.AIMClusterModel{Spec: spec}},
			})
			requireTemplateFieldsIgnored(t, "AIMClusterModel", cm, test.wantFields)
		})
	}
}

func staleTemplateFieldsWarning() *controllerutils.ConditionManager {
	return controllerutils.NewConditionManager([]metav1.Condition{{
		Type:    controllerutils.ConditionTypeTemplateFieldsIgnored,
		Status:  metav1.ConditionTrue,
		Reason:  controllerutils.ReasonTemplateFieldsSet,
		Message: "stale",
	}})
}

func requireTemplateFieldsIgnored(t *testing.T, kind string, cm *controllerutils.ConditionManager, wantFields string) {
	t.Helper()
	got := cm.Get(controllerutils.ConditionTypeTemplateFieldsIgnored)
	if wantFields == "" {
		if got != nil {
			t.Fatalf("%s TemplateFieldsIgnored = %#v, want no condition", kind, got)
		}
		return
	}
	if got == nil ||
		got.Status != metav1.ConditionTrue ||
		got.Reason != controllerutils.ReasonTemplateFieldsSet ||
		got.Message != templateFieldsIgnoredPrefix+wantFields {
		t.Fatalf("%s TemplateFieldsIgnored = %#v, want True/%s naming %q",
			kind, got, controllerutils.ReasonTemplateFieldsSet, wantFields)
	}
}

func drainTemplateFieldsEvents(recorder *record.FakeRecorder) int {
	count := 0
	for {
		select {
		case event := <-recorder.Events:
			if strings.HasPrefix(event, corev1.EventTypeWarning+" "+controllerutils.ReasonTemplateFieldsSet) {
				count++
			}
		default:
			return count
		}
	}
}
