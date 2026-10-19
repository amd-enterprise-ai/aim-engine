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

package aimservice

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

func TestLegacyViewIsDeepCopied(t *testing.T) {
	canonical := &aimv1alpha2.AIMService{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "service",
			Labels: map[string]string{"example.com/source": "canonical"},
		},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: "profile"},
			ProfileOverrides: &aimv1alpha1.AIMServiceProfileOverrides{
				EngineEnv: map[string]string{"MODE": "canonical"},
			},
		},
		Status: aimv1alpha1.AIMServiceStatus{
			Conditions: []metav1.Condition{{Type: "Ready", Reason: "Canonical"}},
		},
	}

	legacy := LegacyView(canonical)
	legacy.Labels["example.com/source"] = "legacy"
	legacy.Spec.Profile.Name = "legacy-profile"
	legacy.Spec.ProfileOverrides.EngineEnv["MODE"] = "legacy"
	legacy.Status.Conditions[0].Reason = "Legacy"

	if canonical.Labels["example.com/source"] != "canonical" ||
		canonical.Spec.Profile.Name != "profile" ||
		canonical.Spec.ProfileOverrides.EngineEnv["MODE"] != "canonical" ||
		canonical.Status.Conditions[0].Reason != "Canonical" {
		t.Fatal("legacy mutation changed the canonical v1alpha2 service")
	}
}

func TestSelectPipeline(t *testing.T) {
	modelName := "model"
	tests := []struct {
		name        string
		annotations map[string]string
		spec        aimv1alpha1.AIMServiceSpec
		want        Pipeline
	}{
		{
			name: "profile shape",
			spec: aimv1alpha1.AIMServiceSpec{
				Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: "profile"},
			},
			want: PipelineProfile,
		},
		{
			name: "model shape",
			spec: aimv1alpha1.AIMServiceSpec{
				Model: &aimv1alpha1.AIMServiceModel{Name: &modelName},
			},
			want: PipelineTemplate,
		},
		{
			name:        "explicit profile overrides model shape",
			annotations: map[string]string{constants.AnnotationReconcilerPipeline: constants.ReconcilerPipelineProfile},
			spec: aimv1alpha1.AIMServiceSpec{
				Model: &aimv1alpha1.AIMServiceModel{Name: &modelName},
			},
			want: PipelineProfile,
		},
		{
			name:        "explicit template overrides profile in supported mixed shape",
			annotations: map[string]string{constants.AnnotationReconcilerPipeline: constants.ReconcilerPipelineTemplate},
			spec: aimv1alpha1.AIMServiceSpec{
				Model:   &aimv1alpha1.AIMServiceModel{Name: &modelName},
				Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: "profile"},
			},
			want: PipelineTemplate,
		},
		{
			name:        "grandfathered unknown marker uses profile shape",
			annotations: map[string]string{constants.AnnotationReconcilerPipeline: "historical"},
			spec: aimv1alpha1.AIMServiceSpec{
				Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: "profile"},
			},
			want: PipelineProfile,
		},
		{
			name:        "grandfathered empty marker uses model shape",
			annotations: map[string]string{constants.AnnotationReconcilerPipeline: ""},
			spec: aimv1alpha1.AIMServiceSpec{
				Model: &aimv1alpha1.AIMServiceModel{Name: &modelName},
			},
			want: PipelineTemplate,
		},
		{
			name:        "grandfathered template marker without model uses profile shape",
			annotations: map[string]string{constants.AnnotationReconcilerPipeline: constants.ReconcilerPipelineTemplate},
			spec: aimv1alpha1.AIMServiceSpec{
				Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: "profile"},
			},
			want: PipelineProfile,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &aimv1alpha1.AIMService{
				ObjectMeta: metav1.ObjectMeta{Annotations: test.annotations},
				Spec:       test.spec,
			}
			if got := SelectPipeline(service); got != test.want {
				t.Fatalf("SelectPipeline() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestPipelineMarkerWarning(t *testing.T) {
	modelName := "model"
	tests := []struct {
		name         string
		annotations  map[string]string
		spec         aimv1alpha1.AIMServiceSpec
		wantFragment string
	}{
		{name: "absent"},
		{
			name:        "valid profile",
			annotations: map[string]string{constants.AnnotationReconcilerPipeline: constants.ReconcilerPipelineProfile},
		},
		{
			name:        "valid template",
			annotations: map[string]string{constants.AnnotationReconcilerPipeline: constants.ReconcilerPipelineTemplate},
			spec:        aimv1alpha1.AIMServiceSpec{Model: &aimv1alpha1.AIMServiceModel{Name: &modelName}},
		},
		{
			name:         "empty",
			annotations:  map[string]string{constants.AnnotationReconcilerPipeline: ""},
			wantFragment: "is empty",
		},
		{
			name:         "unknown",
			annotations:  map[string]string{constants.AnnotationReconcilerPipeline: "historical"},
			wantFragment: "unsupported value",
		},
		{
			name:         "template without model",
			annotations:  map[string]string{constants.AnnotationReconcilerPipeline: constants.ReconcilerPipelineTemplate},
			wantFragment: "requires spec.model",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &aimv1alpha1.AIMService{
				ObjectMeta: metav1.ObjectMeta{Annotations: test.annotations},
				Spec:       test.spec,
			}
			got := PipelineMarkerWarning(service)
			if test.wantFragment == "" && got != "" {
				t.Fatalf("PipelineMarkerWarning() = %q, want no warning", got)
			}
			if test.wantFragment != "" && !strings.Contains(got, test.wantFragment) {
				t.Fatalf("PipelineMarkerWarning() = %q, want fragment %q", got, test.wantFragment)
			}
		})
	}
}

func TestPrepareStatusForPipeline_templateToProfile(t *testing.T) {
	status := &aimv1alpha1.AIMServiceStatus{
		ReconcilerPipeline: string(PipelineTemplate),
		Status:             constants.AIMStatusRunning,
		Conditions: []metav1.Condition{
			{Type: "ModelReady", Status: metav1.ConditionTrue},
			{Type: "TemplateReady", Status: metav1.ConditionTrue},
			{Type: "CacheReady", Status: metav1.ConditionTrue},
			{Type: "MigrationRequired", Status: metav1.ConditionTrue},
			{Type: "ProfileReady", Status: metav1.ConditionTrue},
			{Type: "InferenceServiceReady", Status: metav1.ConditionTrue},
			{Type: "Ready", Status: metav1.ConditionTrue},
			{Type: "ConfigValid", Status: metav1.ConditionTrue},
			{Type: "AuthValid", Status: metav1.ConditionTrue},
			{Type: "DependenciesReachable", Status: metav1.ConditionFalse},
		},
		ResolvedModel:    &aimv1alpha1.AIMResolvedReference{Name: "model"},
		ResolvedTemplate: &aimv1alpha1.AIMResolvedReference{Name: "template"},
		ResolvedProfile:  &aimv1alpha1.AIMResolvedReference{Name: "profile"},
		Cache: &aimv1alpha1.AIMServiceCacheStatus{
			TemplateCacheRef: &aimv1alpha1.AIMResolvedReference{Name: "template-cache"},
			RetryAttempts:    1,
		},
		ResolvedRuntimeConfig:               &aimv1alpha1.AIMResolvedReference{Name: "runtime"},
		Runtime:                             &aimv1alpha1.AIMServiceRuntimeStatus{CurrentReplicas: 2},
		AdapterSubtreeSyncKey:               "sync",
		AdapterDiskPersistentVolumeClaim:    "adapter-pvc",
		AdapterDiskPersistentVolumeClaimUID: "adapter-pvc-uid",
	}

	if !PrepareStatusForPipeline(status, PipelineProfile) {
		t.Fatal("PrepareStatusForPipeline() = false, want transition")
	}
	if status.ReconcilerPipeline != string(PipelineProfile) {
		t.Fatalf("ReconcilerPipeline = %q, want %q", status.ReconcilerPipeline, PipelineProfile)
	}
	assertConditionsAbsent(t, status.Conditions, "ModelReady", "TemplateReady", "CacheReady", "MigrationRequired")
	assertConditionsAbsent(t, status.Conditions, "Ready", "ConfigValid", "AuthValid", "DependenciesReachable")
	assertConditionsPresent(t, status.Conditions, "ProfileReady", "InferenceServiceReady")
	if status.Status != constants.AIMStatusPending {
		t.Fatalf("status = %q, want Pending during pipeline transition", status.Status)
	}
	if status.ResolvedModel != nil || status.ResolvedTemplate != nil || status.Cache != nil {
		t.Fatal("template-owned references or cache lineage survived transition")
	}
	if status.ResolvedProfile == nil || status.ResolvedRuntimeConfig == nil || status.Runtime == nil {
		t.Fatal("active or pipeline-neutral status was removed")
	}
	assertStorageLineageCleared(t, status)
	if PrepareStatusForPipeline(status, PipelineProfile) {
		t.Fatal("same-pipeline reconcile requested redundant status persistence")
	}
}

func TestPrepareStatusForPipeline_profileToTemplate(t *testing.T) {
	status := &aimv1alpha1.AIMServiceStatus{
		ReconcilerPipeline: string(PipelineProfile),
		Status:             constants.AIMStatusFailed,
		Conditions: []metav1.Condition{
			{Type: "ProfileReady", Status: metav1.ConditionTrue},
			{Type: "ProfileConfigReady", Status: metav1.ConditionTrue},
			{Type: "ProfileCacheReady", Status: metav1.ConditionTrue},
			{Type: "ProfileRuntimeReady", Status: metav1.ConditionTrue},
			{Type: "TemplateReady", Status: metav1.ConditionTrue},
			{Type: "HTTPRouteReady", Status: metav1.ConditionTrue},
			{Type: "Ready", Status: metav1.ConditionFalse},
			{Type: "ConfigValid", Status: metav1.ConditionFalse},
			{Type: "AuthValid", Status: metav1.ConditionTrue},
			{Type: "DependenciesReachable", Status: metav1.ConditionTrue},
		},
		ResolvedModel:    &aimv1alpha1.AIMResolvedReference{Name: "model-id"},
		ResolvedTemplate: &aimv1alpha1.AIMResolvedReference{Name: "template"},
		ResolvedProfile:  &aimv1alpha1.AIMResolvedReference{Name: "profile"},
		Cache: &aimv1alpha1.AIMServiceCacheStatus{
			ProfileCacheRef: &aimv1alpha1.AIMResolvedReference{Name: "profile-cache"},
			RetryAttempts:   1,
		},
		Routing: &aimv1alpha1.AIMServiceRoutingStatus{Path: "/service"},
		Runtime: &aimv1alpha1.AIMServiceRuntimeStatus{CurrentReplicas: 2},
	}

	if !PrepareStatusForPipeline(status, PipelineTemplate) {
		t.Fatal("PrepareStatusForPipeline() = false, want transition")
	}
	assertConditionsAbsent(
		t,
		status.Conditions,
		"ProfileReady",
		"ProfileConfigReady",
		"ProfileCacheReady",
		"ProfileRuntimeReady",
	)
	assertConditionsAbsent(t, status.Conditions, "Ready", "ConfigValid", "AuthValid", "DependenciesReachable")
	assertConditionsPresent(t, status.Conditions, "TemplateReady", "HTTPRouteReady")
	if status.Status != constants.AIMStatusPending {
		t.Fatalf("status = %q, want Pending during pipeline transition", status.Status)
	}
	if status.ResolvedModel != nil || status.ResolvedProfile != nil || status.Cache != nil {
		t.Fatal("profile-owned references or cache lineage survived transition")
	}
	if status.ResolvedTemplate == nil || status.Routing == nil || status.Runtime == nil {
		t.Fatal("active or pipeline-neutral status was removed")
	}
}

func TestPrepareStatusForPipeline_bootstrapPreservesSelectedCache(t *testing.T) {
	tests := []struct {
		name     string
		pipeline Pipeline
		cache    *aimv1alpha1.AIMServiceCacheStatus
	}{
		{
			name:     "template",
			pipeline: PipelineTemplate,
			cache: &aimv1alpha1.AIMServiceCacheStatus{
				TemplateCacheRef: &aimv1alpha1.AIMResolvedReference{Name: "template-cache"},
				RetryAttempts:    1,
			},
		},
		{
			name:     "profile",
			pipeline: PipelineProfile,
			cache: &aimv1alpha1.AIMServiceCacheStatus{
				ProfileCacheRef: &aimv1alpha1.AIMResolvedReference{Name: "profile-cache"},
				RetryAttempts:   1,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status := &aimv1alpha1.AIMServiceStatus{Cache: test.cache}
			if !PrepareStatusForPipeline(status, test.pipeline) {
				t.Fatal("bootstrap did not persist pipeline provenance")
			}
			if status.Cache == nil || status.Cache.RetryAttempts != 1 {
				t.Fatal("cache lineage owned by selected pipeline was removed")
			}
		})
	}
}

func assertConditionsAbsent(t *testing.T, conditions []metav1.Condition, conditionTypes ...string) {
	t.Helper()
	for _, conditionType := range conditionTypes {
		if conditionByType(conditions, conditionType) != nil {
			t.Errorf("%s condition survived cleanup", conditionType)
		}
	}
}

func assertConditionsPresent(t *testing.T, conditions []metav1.Condition, conditionTypes ...string) {
	t.Helper()
	for _, conditionType := range conditionTypes {
		if conditionByType(conditions, conditionType) == nil {
			t.Errorf("%s condition was removed", conditionType)
		}
	}
}

func assertStorageLineageCleared(t *testing.T, status *aimv1alpha1.AIMServiceStatus) {
	t.Helper()
	if status.AdapterSubtreeSyncKey != "" ||
		status.AdapterDiskPersistentVolumeClaim != "" ||
		status.AdapterDiskPersistentVolumeClaimUID != "" {
		t.Fatal("pipeline-derived adapter storage lineage survived transition")
	}
}

func conditionByType(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}
