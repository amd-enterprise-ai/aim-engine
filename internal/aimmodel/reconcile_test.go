/*
MIT License

Copyright (c) 2025 Advanced Micro Devices, Inc.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package aimmodel

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/aimruntimeconfig"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

// ============================================================================
// INTEGRATION TESTS - Observe
// ============================================================================

func TestClusterModelReconciler_Observe(t *testing.T) {
	clusterModel := &aimv1alpha1.AIMClusterModel{
		Spec: aimv1alpha1.AIMModelSpec{
			Image:               "test:latest",
			RuntimeConfigName:   "default",
		},
	}

	fetchResult := ClusterModelFetchResult{
		runtimeConfig: aimruntimeconfig.RuntimeConfigFetchResult{
			ConfigName:      "default",
			ClusterConfig:   nil,
			NamespaceConfig: nil,
		},
		clusterServiceTemplates: clusterModelServiceTemplateFetchResult{
			clusterServiceTemplates: []aimv1alpha1.AIMClusterServiceTemplate{},
		},
		imageMetadata: nil, // Not attempted
	}

	reconciler := &ClusterModelReconciler{
		Scheme: runtime.NewScheme(),
	}

	obs, err := reconciler.Observe(context.Background(), clusterModel, fetchResult)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if obs.runtimeConfig.ConfigNotFound {
		t.Error("expected default config not to be marked as not found")
	}
	if obs.metadata.Extracted {
		t.Error("expected metadata not to be extracted when imageMetadata is nil")
	}
}

func TestModelReconciler_Observe(t *testing.T) {
	model := &aimv1alpha1.AIMModel{
		Spec: aimv1alpha1.AIMModelSpec{
			Image:             "test:latest",
			RuntimeConfigName: "default",
		},
	}

	fetchResult := ModelFetchResult{
		runtimeConfig: aimruntimeconfig.RuntimeConfigFetchResult{
			ConfigName:      "default",
			ClusterConfig:   nil,
			NamespaceConfig: nil,
		},
		serviceTemplates: modelServiceTemplateFetchResult{
			serviceTemplates: []aimv1alpha1.AIMServiceTemplate{},
		},
		imageMetadata: nil,
	}

	reconciler := &ModelReconciler{
		Scheme: runtime.NewScheme(),
	}

	obs, err := reconciler.Observe(context.Background(), model, fetchResult)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if obs.runtimeConfig.ConfigNotFound {
		t.Error("expected default config not to be marked as not found")
	}
}

func TestClusterModelReconciler_Observe_WithMetadata(t *testing.T) {
	clusterModel := &aimv1alpha1.AIMClusterModel{
		Spec: aimv1alpha1.AIMModelSpec{
			Image:             "test:latest",
			RuntimeConfigName: "default",
		},
	}

	metadata := &aimv1alpha1.ImageMetadata{
		Model: &aimv1alpha1.ModelMetadata{
			CanonicalName: "test-model",
			RecommendedDeployments: []aimv1alpha1.RecommendedDeployment{
				{
					GPUModel: "MI300X",
					GPUCount: 1,
				},
			},
		},
	}

	fetchResult := ClusterModelFetchResult{
		runtimeConfig: aimruntimeconfig.RuntimeConfigFetchResult{
			ConfigName:      "default",
			ClusterConfig:   nil,
			NamespaceConfig: nil,
		},
		clusterServiceTemplates: clusterModelServiceTemplateFetchResult{},
		imageMetadata: &modelMetadataFetchResult{
			ImageMetadata: metadata,
		},
	}

	reconciler := &ClusterModelReconciler{
		Scheme: runtime.NewScheme(),
	}

	obs, err := reconciler.Observe(context.Background(), clusterModel, fetchResult)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !obs.metadata.Extracted {
		t.Error("expected metadata to be extracted")
	}
	if obs.metadata.ExtractedMetadata == nil {
		t.Error("expected ExtractedMetadata to be set")
	}
}

// ============================================================================
// INTEGRATION TESTS - Plan
// ============================================================================

func TestClusterModelReconciler_Plan_NoTemplates(t *testing.T) {
	clusterModel := &aimv1alpha1.AIMClusterModel{
		Spec: aimv1alpha1.AIMModelSpec{
			Image: "test:latest",
		},
	}

	obs := ClusterModelObservation{
		templates: clusterModelServiceTemplateObservation{
			shouldCreateTemplates: false,
		},
	}

	reconciler := &ClusterModelReconciler{
		Scheme: runtime.NewScheme(),
	}

	planResult, err := reconciler.Plan(context.Background(), clusterModel, obs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(planResult.Apply) != 0 {
		t.Errorf("expected no templates to be planned, got %d", len(planResult.Apply))
	}
}

func TestClusterModelReconciler_Plan_CreateTemplates(t *testing.T) {
	clusterModel := &aimv1alpha1.AIMClusterModel{
		Spec: aimv1alpha1.AIMModelSpec{
			Image: "test:latest",
		},
	}

	obs := ClusterModelObservation{
		templates: clusterModelServiceTemplateObservation{
			shouldCreateTemplates: true,
		},
		metadata: modelMetadataObservation{
			ExtractedMetadata: &aimv1alpha1.ImageMetadata{
				Model: &aimv1alpha1.ModelMetadata{
					RecommendedDeployments: []aimv1alpha1.RecommendedDeployment{
						{
							GPUModel: "MI300X",
							GPUCount: 1,
						},
					},
				},
			},
		},
	}

	scheme := runtime.NewScheme()
	_ = aimv1alpha1.AddToScheme(scheme)

	reconciler := &ClusterModelReconciler{
		Scheme: scheme,
	}

	planResult, err := reconciler.Plan(context.Background(), clusterModel, obs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(planResult.Apply) != 1 {
		t.Errorf("expected 1 template to be planned, got %d", len(planResult.Apply))
	}
}

func TestModelReconciler_Plan_CreateTemplates(t *testing.T) {
	model := &aimv1alpha1.AIMModel{
		Spec: aimv1alpha1.AIMModelSpec{
			Image: "test:latest",
		},
	}

	obs := ModelObservation{
		templates: modelServiceTemplateObservation{
			shouldCreateTemplates: true,
		},
		metadata: modelMetadataObservation{
			ExtractedMetadata: &aimv1alpha1.ImageMetadata{
				Model: &aimv1alpha1.ModelMetadata{
					RecommendedDeployments: []aimv1alpha1.RecommendedDeployment{
						{
							GPUModel: "MI300X",
							GPUCount: 2,
						},
					},
				},
			},
		},
	}

	scheme := runtime.NewScheme()
	_ = aimv1alpha1.AddToScheme(scheme)

	reconciler := &ModelReconciler{
		Scheme: scheme,
	}

	planResult, err := reconciler.Plan(context.Background(), model, obs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(planResult.Apply) != 1 {
		t.Errorf("expected 1 template to be planned, got %d", len(planResult.Apply))
	}
}

// ============================================================================
// INTEGRATION TESTS - Project
// ============================================================================

func TestClusterModelReconciler_Project_RuntimeConfigError(t *testing.T) {
	obs := ClusterModelObservation{
		runtimeConfig: aimruntimeconfig.RuntimeConfigObservation{
			ConfigNotFound: true,
			Error:          &mockError{},
		},
	}

	status := &aimv1alpha1.AIMModelStatus{}
	cm := controllerutils.NewConditionManager(nil)

	reconciler := &ClusterModelReconciler{
		Scheme: runtime.NewScheme(),
	}

	reconciler.Project(status, cm, obs)

	// Should stop at runtime config error
	if status.Status != constants.AIMStatusFailed {
		t.Errorf("expected status Failed when runtime config not found, got %s", status.Status)
	}
}

func TestClusterModelReconciler_Project_MetadataError(t *testing.T) {
	obs := ClusterModelObservation{
		runtimeConfig: aimruntimeconfig.RuntimeConfigObservation{
			ConfigNotFound: false,
			MergedConfig:   &aimv1alpha1.AIMRuntimeConfigCommon{},
		},
		metadata: modelMetadataObservation{
			Error: &metadataFormatError{
				Reason:  "MetadataFormatInvalid",
				Message: "invalid format",
			},
			FormatError: &metadataFormatError{
				Reason:  "MetadataFormatInvalid",
				Message: "invalid format",
			},
		},
	}

	status := &aimv1alpha1.AIMModelStatus{}
	cm := controllerutils.NewConditionManager(nil)

	reconciler := &ClusterModelReconciler{
		Scheme: runtime.NewScheme(),
	}

	reconciler.Project(status, cm, obs)

	// Should stop at metadata error
	if status.Status != constants.AIMStatusFailed {
		t.Errorf("expected status Failed when metadata format error, got %s", status.Status)
	}
}

func TestClusterModelReconciler_Project_Success(t *testing.T) {
	obs := ClusterModelObservation{
		runtimeConfig: aimruntimeconfig.RuntimeConfigObservation{
			ConfigNotFound: false,
			MergedConfig:   &aimv1alpha1.AIMRuntimeConfigCommon{},
		},
		metadata: modelMetadataObservation{
			ExtractedMetadata: &aimv1alpha1.ImageMetadata{
				Model: &aimv1alpha1.ModelMetadata{
					CanonicalName: "test-model",
				},
			},
		},
		templates: clusterModelServiceTemplateObservation{
			existingTemplates: []aimv1alpha1.AIMClusterServiceTemplate{
				{
					Status: aimv1alpha1.AIMServiceTemplateStatus{
						Status: constants.AIMStatusReady,
					},
				},
			},
		},
	}

	status := &aimv1alpha1.AIMModelStatus{}
	cm := controllerutils.NewConditionManager(nil)

	reconciler := &ClusterModelReconciler{
		Scheme: runtime.NewScheme(),
	}

	reconciler.Project(status, cm, obs)

	if status.Status != constants.AIMStatusReady {
		t.Errorf("expected status Ready when all templates ready, got %s", status.Status)
	}
}

func TestModelReconciler_Project_Success(t *testing.T) {
	obs := ModelObservation{
		runtimeConfig: aimruntimeconfig.RuntimeConfigObservation{
			ConfigNotFound: false,
			MergedConfig:   &aimv1alpha1.AIMRuntimeConfigCommon{},
		},
		metadata: modelMetadataObservation{
			ExtractedMetadata: &aimv1alpha1.ImageMetadata{
				Model: &aimv1alpha1.ModelMetadata{
					CanonicalName: "test-model",
				},
			},
		},
		templates: modelServiceTemplateObservation{
			existingTemplates: []aimv1alpha1.AIMServiceTemplate{
				{
					Status: aimv1alpha1.AIMServiceTemplateStatus{
						Status: constants.AIMStatusProgressing,
					},
				},
			},
		},
	}

	status := &aimv1alpha1.AIMModelStatus{}
	cm := controllerutils.NewConditionManager(nil)

	reconciler := &ModelReconciler{
		Scheme: runtime.NewScheme(),
	}

	reconciler.Project(status, cm, obs)

	if status.Status != constants.AIMStatusProgressing {
		t.Errorf("expected status Progressing when template progressing, got %s", status.Status)
	}
}

func TestClusterModelReconciler_Project_MixedTemplateStatus(t *testing.T) {
	obs := ClusterModelObservation{
		runtimeConfig: aimruntimeconfig.RuntimeConfigObservation{
			ConfigNotFound: false,
			MergedConfig:   &aimv1alpha1.AIMRuntimeConfigCommon{},
		},
		metadata: modelMetadataObservation{
			ExtractedMetadata: &aimv1alpha1.ImageMetadata{
				Model: &aimv1alpha1.ModelMetadata{
					CanonicalName: "test-model",
				},
			},
		},
		templates: clusterModelServiceTemplateObservation{
			existingTemplates: []aimv1alpha1.AIMClusterServiceTemplate{
				{
					Status: aimv1alpha1.AIMServiceTemplateStatus{
						Status: constants.AIMStatusReady,
					},
				},
				{
					Status: aimv1alpha1.AIMServiceTemplateStatus{
						Status: constants.AIMStatusDegraded,
					},
				},
			},
		},
	}

	status := &aimv1alpha1.AIMModelStatus{}
	cm := controllerutils.NewConditionManager(nil)

	reconciler := &ClusterModelReconciler{
		Scheme: runtime.NewScheme(),
	}

	reconciler.Project(status, cm, obs)

	if status.Status != constants.AIMStatusDegraded {
		t.Errorf("expected status Degraded when some templates degraded, got %s", status.Status)
	}
}

func TestModelReconciler_Project_DisabledAutoDiscovery(t *testing.T) {
	obs := ModelObservation{
		runtimeConfig: aimruntimeconfig.RuntimeConfigObservation{
			ConfigNotFound: false,
			MergedConfig: &aimv1alpha1.AIMRuntimeConfigCommon{
				Model: &aimv1alpha1.AIMModelConfig{
					AutoDiscovery: ptr.To(false),
				},
			},
		},
		metadata: modelMetadataObservation{
			ExtractedMetadata: &aimv1alpha1.ImageMetadata{
				Model: &aimv1alpha1.ModelMetadata{
					CanonicalName: "test-model",
				},
			},
		},
		templates: modelServiceTemplateObservation{
			shouldCreateTemplates: false,
			existingTemplates:     []aimv1alpha1.AIMServiceTemplate{},
		},
	}

	status := &aimv1alpha1.AIMModelStatus{}
	cm := controllerutils.NewConditionManager(nil)

	reconciler := &ModelReconciler{
		Scheme: runtime.NewScheme(),
	}

	reconciler.Project(status, cm, obs)

	// With no templates and auto-discovery disabled, status should remain empty/pending
	// The metadata extraction should still succeed
	conditions := cm.Conditions()
	metadataExtracted := false
	for _, cond := range conditions {
		if cond.Type == aimv1alpha1.AIMModelConditionMetadataExtracted {
			metadataExtracted = true
		}
	}
	if !metadataExtracted {
		t.Error("expected MetadataExtracted condition to be set")
	}
}

// ============================================================================
// TEST HELPERS
// ============================================================================

// mockError implements error for testing
type mockError struct{}

func (e *mockError) Error() string {
	return "mock error"
}
