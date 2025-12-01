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

package aimtemplatecache

import (
	"context"
	"errors"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

// ============================================================================
// HELPER FUNCTIONS TESTS
// ============================================================================

func TestBuildTemplateObservation(t *testing.T) {
	tests := []struct {
		name     string
		inputs   templateObservationInputs
		expected templateObservation
	}{
		{
			name: "template found with model sources",
			inputs: templateObservationInputs{
				modelSources: []aimv1alpha1.AIMModelSource{
					{Name: "model1", SourceURI: "hf://model1"},
				},
				error: nil,
			},
			expected: templateObservation{
				found: true,
				modelSources: []aimv1alpha1.AIMModelSource{
					{Name: "model1", SourceURI: "hf://model1"},
				},
			},
		},
		{
			name: "template not found",
			inputs: templateObservationInputs{
				modelSources: nil,
				error:        errors.New("template not found"),
			},
			expected: templateObservation{
				found: false,
				error: "template not found",
			},
		},
		{
			name: "template found with empty model sources",
			inputs: templateObservationInputs{
				modelSources: []aimv1alpha1.AIMModelSource{},
				error:        nil,
			},
			expected: templateObservation{
				found:        true,
				modelSources: []aimv1alpha1.AIMModelSource{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildTemplateObservation(tt.inputs)

			if result.found != tt.expected.found {
				t.Errorf("expected found=%v, got %v", tt.expected.found, result.found)
			}
			if result.error != tt.expected.error {
				t.Errorf("expected error=%q, got %q", tt.expected.error, result.error)
			}
			if len(result.modelSources) != len(tt.expected.modelSources) {
				t.Errorf("expected %d model sources, got %d", len(tt.expected.modelSources), len(result.modelSources))
			}
		})
	}
}

func TestBuildModelCachesObservation(t *testing.T) {
	tests := []struct {
		name     string
		inputs   modelCachesObservationInputs
		expected modelCachesObservation
	}{
		{
			name: "all caches available",
			inputs: modelCachesObservationInputs{
				modelSources: []aimv1alpha1.AIMModelSource{
					{Name: "model1", SourceURI: "hf://model1"},
				},
				availableCaches: []aimv1alpha1.AIMModelCache{
					{
						Spec: aimv1alpha1.AIMModelCacheSpec{
							SourceURI: "hf://model1",
						},
						Status: aimv1alpha1.AIMModelCacheStatus{
							Status: constants.AIMStatusReady,
						},
					},
				},
				storageClassName: "",
			},
			expected: modelCachesObservation{
				allCachesAvailable: true,
				missingCaches:      []aimv1alpha1.AIMModelSource{},
				cacheStatus: map[string]constants.AIMStatus{
					"model1": constants.AIMStatusReady,
				},
			},
		},
		{
			name: "missing cache",
			inputs: modelCachesObservationInputs{
				modelSources: []aimv1alpha1.AIMModelSource{
					{Name: "model1", SourceURI: "hf://model1"},
				},
				availableCaches:  []aimv1alpha1.AIMModelCache{},
				storageClassName: "",
			},
			expected: modelCachesObservation{
				allCachesAvailable: false,
				missingCaches: []aimv1alpha1.AIMModelSource{
					{Name: "model1", SourceURI: "hf://model1"},
				},
				cacheStatus: map[string]constants.AIMStatus{
					"model1": constants.AIMStatusPending,
				},
			},
		},
		{
			name: "cache with matching storage class",
			inputs: modelCachesObservationInputs{
				modelSources: []aimv1alpha1.AIMModelSource{
					{Name: "model1", SourceURI: "hf://model1"},
				},
				availableCaches: []aimv1alpha1.AIMModelCache{
					{
						Spec: aimv1alpha1.AIMModelCacheSpec{
							SourceURI:        "hf://model1",
							StorageClassName: "fast-storage",
						},
						Status: aimv1alpha1.AIMModelCacheStatus{
							Status: constants.AIMStatusReady,
						},
					},
				},
				storageClassName: "fast-storage",
			},
			expected: modelCachesObservation{
				allCachesAvailable: true,
				missingCaches:      []aimv1alpha1.AIMModelSource{},
				cacheStatus: map[string]constants.AIMStatus{
					"model1": constants.AIMStatusReady,
				},
			},
		},
		{
			name: "cache with non-matching storage class",
			inputs: modelCachesObservationInputs{
				modelSources: []aimv1alpha1.AIMModelSource{
					{Name: "model1", SourceURI: "hf://model1"},
				},
				availableCaches: []aimv1alpha1.AIMModelCache{
					{
						Spec: aimv1alpha1.AIMModelCacheSpec{
							SourceURI:        "hf://model1",
							StorageClassName: "slow-storage",
						},
						Status: aimv1alpha1.AIMModelCacheStatus{
							Status: constants.AIMStatusReady,
						},
					},
				},
				storageClassName: "fast-storage",
			},
			expected: modelCachesObservation{
				allCachesAvailable: false,
				missingCaches: []aimv1alpha1.AIMModelSource{
					{Name: "model1", SourceURI: "hf://model1"},
				},
				cacheStatus: map[string]constants.AIMStatus{
					"model1": constants.AIMStatusPending,
				},
			},
		},
		{
			name: "multiple caches - picks best status",
			inputs: modelCachesObservationInputs{
				modelSources: []aimv1alpha1.AIMModelSource{
					{Name: "model1", SourceURI: "hf://model1"},
				},
				availableCaches: []aimv1alpha1.AIMModelCache{
					{
						Spec: aimv1alpha1.AIMModelCacheSpec{
							SourceURI: "hf://model1",
						},
						Status: aimv1alpha1.AIMModelCacheStatus{
							Status: constants.AIMStatusProgressing,
						},
					},
					{
						Spec: aimv1alpha1.AIMModelCacheSpec{
							SourceURI: "hf://model1",
						},
						Status: aimv1alpha1.AIMModelCacheStatus{
							Status: constants.AIMStatusReady,
						},
					},
				},
				storageClassName: "",
			},
			expected: modelCachesObservation{
				allCachesAvailable: true,
				missingCaches:      []aimv1alpha1.AIMModelSource{},
				cacheStatus: map[string]constants.AIMStatus{
					"model1": constants.AIMStatusReady,
				},
			},
		},
		{
			name: "empty model sources",
			inputs: modelCachesObservationInputs{
				modelSources:     []aimv1alpha1.AIMModelSource{},
				availableCaches:  []aimv1alpha1.AIMModelCache{},
				storageClassName: "",
			},
			expected: modelCachesObservation{
				allCachesAvailable: false,
				missingCaches:      []aimv1alpha1.AIMModelSource{},
				cacheStatus:        map[string]constants.AIMStatus{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildModelCachesObservation(tt.inputs)

			if result.allCachesAvailable != tt.expected.allCachesAvailable {
				t.Errorf("expected allCachesAvailable=%v, got %v", tt.expected.allCachesAvailable, result.allCachesAvailable)
			}
			if len(result.missingCaches) != len(tt.expected.missingCaches) {
				t.Errorf("expected %d missing caches, got %d", len(tt.expected.missingCaches), len(result.missingCaches))
			}
			if len(result.cacheStatus) != len(tt.expected.cacheStatus) {
				t.Errorf("expected %d cache statuses, got %d", len(tt.expected.cacheStatus), len(result.cacheStatus))
			}
			for key, expectedStatus := range tt.expected.cacheStatus {
				if result.cacheStatus[key] != expectedStatus {
					t.Errorf("expected status %v for %s, got %v", expectedStatus, key, result.cacheStatus[key])
				}
			}
		})
	}
}

func TestBuildMissingModelCaches(t *testing.T) {
	tc := &aimv1alpha1.AIMTemplateCache{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cache",
			Namespace: "default",
		},
		Spec: aimv1alpha1.AIMTemplateCacheSpec{
			StorageClassName: "fast-storage",
		},
	}

	obs := Observation{
		modelCaches: modelCachesObservation{
			missingCaches: []aimv1alpha1.AIMModelSource{
				{
					Name:      "model-1",
					SourceURI: "hf://model1",
					Size:      resource.NewQuantity(10*1024*1024*1024, resource.BinarySI),
				},
			},
		},
	}

	caches := buildMissingModelCaches(tc, obs)

	if len(caches) != 1 {
		t.Fatalf("expected 1 cache, got %d", len(caches))
	}

	cache := caches[0]
	if cache.Name == "" {
		t.Error("expected cache to have a name")
	}
	if cache.Namespace != "default" {
		t.Errorf("expected namespace=default, got %s", cache.Namespace)
	}
	if cache.Spec.StorageClassName != "fast-storage" {
		t.Errorf("expected storageClassName=fast-storage, got %s", cache.Spec.StorageClassName)
	}
	if cache.Spec.SourceURI != "hf://model1" {
		t.Errorf("expected sourceURI=hf://model1, got %s", cache.Spec.SourceURI)
	}
	if cache.Labels[constants.LabelKeyTemplateCache] != "test-cache" {
		t.Error("expected template-cache label to be set")
	}
}

// ============================================================================
// OBSERVE INTEGRATION TESTS
// ============================================================================

func TestReconciler_Observe(t *testing.T) {
	tests := []struct {
		name        string
		cache       *aimv1alpha1.AIMTemplateCache
		fetchResult FetchResult
		check       func(*testing.T, Observation)
	}{
		{
			name: "namespace template found",
			cache: &aimv1alpha1.AIMTemplateCache{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cache",
					Namespace: "default",
				},
				Spec: aimv1alpha1.AIMTemplateCacheSpec{
					TemplateName: "test-template",
				},
			},
			fetchResult: FetchResult{
				NamespaceTemplate: controllerutils.FetchResult[*aimv1alpha1.AIMServiceTemplate]{
					Result: &aimv1alpha1.AIMServiceTemplate{
						Status: aimv1alpha1.AIMServiceTemplateStatus{
							ModelSources: []aimv1alpha1.AIMModelSource{
								{Name: "model1", SourceURI: "hf://model1"},
							},
						},
					},
					Error: nil,
				},
				ModelCaches: controllerutils.FetchResult[[]aimv1alpha1.AIMModelCache]{
					Result: []aimv1alpha1.AIMModelCache{},
				},
			},
			check: func(t *testing.T, obs Observation) {
				if !obs.template.found {
					t.Error("expected template to be found")
				}
				if len(obs.template.modelSources) != 1 {
					t.Errorf("expected 1 model source, got %d", len(obs.template.modelSources))
				}
			},
		},
		{
			name: "cluster template fallback",
			cache: &aimv1alpha1.AIMTemplateCache{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cache",
					Namespace: "default",
				},
				Spec: aimv1alpha1.AIMTemplateCacheSpec{
					TemplateName: "test-template",
				},
			},
			fetchResult: FetchResult{
				NamespaceTemplate: controllerutils.FetchResult[*aimv1alpha1.AIMServiceTemplate]{
					Error: apierrors.NewNotFound(schema.GroupResource{}, "test-template"),
				},
				ClusterTemplate: controllerutils.FetchResult[*aimv1alpha1.AIMClusterServiceTemplate]{
					Result: &aimv1alpha1.AIMClusterServiceTemplate{
						Status: aimv1alpha1.AIMServiceTemplateStatus{
							ModelSources: []aimv1alpha1.AIMModelSource{
								{Name: "cluster-model", SourceURI: "hf://cluster-model"},
							},
						},
					},
					Error: nil,
				},
				ModelCaches: controllerutils.FetchResult[[]aimv1alpha1.AIMModelCache]{
					Result: []aimv1alpha1.AIMModelCache{},
				},
			},
			check: func(t *testing.T, obs Observation) {
				if !obs.template.found {
					t.Error("expected cluster template to be found")
				}
				if len(obs.template.modelSources) != 1 {
					t.Errorf("expected 1 model source, got %d", len(obs.template.modelSources))
				}
			},
		},
		{
			name: "both templates not found",
			cache: &aimv1alpha1.AIMTemplateCache{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cache",
					Namespace: "default",
				},
				Spec: aimv1alpha1.AIMTemplateCacheSpec{
					TemplateName: "missing-template",
				},
			},
			fetchResult: FetchResult{
				NamespaceTemplate: controllerutils.FetchResult[*aimv1alpha1.AIMServiceTemplate]{
					Error: apierrors.NewNotFound(schema.GroupResource{}, "missing-template"),
				},
				ClusterTemplate: controllerutils.FetchResult[*aimv1alpha1.AIMClusterServiceTemplate]{
					Error: apierrors.NewNotFound(schema.GroupResource{}, "missing-template"),
				},
				ModelCaches: controllerutils.FetchResult[[]aimv1alpha1.AIMModelCache]{
					Result: []aimv1alpha1.AIMModelCache{},
				},
			},
			check: func(t *testing.T, obs Observation) {
				if obs.template.found {
					t.Error("expected template not to be found")
				}
				if obs.template.error == "" {
					t.Error("expected error message when template not found")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Reconciler{}
			obs, err := r.Observe(context.Background(), tt.cache, tt.fetchResult)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			tt.check(t, obs)
		})
	}
}

// ============================================================================
// PLAN INTEGRATION TESTS
// ============================================================================

func TestReconciler_Plan(t *testing.T) {
	tests := []struct {
		name          string
		cache         *aimv1alpha1.AIMTemplateCache
		obs           Observation
		expectedCount int
	}{
		{
			name: "create missing caches",
			cache: &aimv1alpha1.AIMTemplateCache{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cache",
					Namespace: "default",
				},
			},
			obs: Observation{
				template: templateObservation{
					found: true,
				},
				modelCaches: modelCachesObservation{
					missingCaches: []aimv1alpha1.AIMModelSource{
						{Name: "model1", SourceURI: "hf://model1", Size: resource.NewQuantity(10*1024*1024*1024, resource.BinarySI)},
						{Name: "model2", SourceURI: "hf://model2", Size: resource.NewQuantity(20*1024*1024*1024, resource.BinarySI)},
					},
				},
			},
			expectedCount: 2,
		},
		{
			name: "template not found - no caches created",
			cache: &aimv1alpha1.AIMTemplateCache{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cache",
					Namespace: "default",
				},
			},
			obs: Observation{
				template: templateObservation{
					found: false,
				},
			},
			expectedCount: 0,
		},
		{
			name: "all caches exist - nothing to create",
			cache: &aimv1alpha1.AIMTemplateCache{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cache",
					Namespace: "default",
				},
			},
			obs: Observation{
				template: templateObservation{
					found: true,
				},
				modelCaches: modelCachesObservation{
					missingCaches: []aimv1alpha1.AIMModelSource{},
				},
			},
			expectedCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = aimv1alpha1.AddToScheme(scheme)

			r := &Reconciler{Scheme: scheme}
			result, err := r.Plan(context.Background(), tt.cache, tt.obs)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(result.Apply) != tt.expectedCount {
				t.Errorf("expected %d objects to create, got %d", tt.expectedCount, len(result.Apply))
			}
		})
	}
}

// ============================================================================
// PROJECT INTEGRATION TESTS
// ============================================================================

func TestReconciler_Project(t *testing.T) {
	tests := []struct {
		name           string
		obs            Observation
		expectedStatus constants.AIMStatus
		checkCondition func(*testing.T, *controllerutils.ConditionManager)
	}{
		{
			name: "template not found",
			obs: Observation{
				template: templateObservation{
					found: false,
					error: "template not found",
				},
			},
			expectedStatus: constants.AIMStatusPending,
			checkCondition: func(t *testing.T, cm *controllerutils.ConditionManager) {
				conds := cm.Conditions()
				found := false
				for _, cond := range conds {
					if cond.Type == aimv1alpha1.AIMTemplateCacheConditionTemplateFound && cond.Status == metav1.ConditionFalse {
						found = true
					}
				}
				if !found {
					t.Error("expected TemplateFound condition to be False")
				}
			},
		},
		{
			name: "all caches ready",
			obs: Observation{
				template: templateObservation{
					found: true,
				},
				modelCaches: modelCachesObservation{
					allCachesAvailable: true,
					cacheStatus: map[string]constants.AIMStatus{
						"model1": constants.AIMStatusReady,
					},
				},
			},
			expectedStatus: constants.AIMStatusReady,
			checkCondition: func(t *testing.T, cm *controllerutils.ConditionManager) {
				conds := cm.Conditions()
				found := false
				for _, cond := range conds {
					if cond.Type == aimv1alpha1.AIMTemplateCacheConditionTemplateFound && cond.Status == metav1.ConditionTrue {
						found = true
					}
				}
				if !found {
					t.Error("expected TemplateFound condition to be True")
				}
			},
		},
		{
			name: "some caches progressing",
			obs: Observation{
				template: templateObservation{
					found: true,
				},
				modelCaches: modelCachesObservation{
					allCachesAvailable: false,
					cacheStatus: map[string]constants.AIMStatus{
						"model1": constants.AIMStatusReady,
						"model2": constants.AIMStatusProgressing,
					},
				},
			},
			expectedStatus: constants.AIMStatusProgressing,
		},
		{
			name: "cache failed",
			obs: Observation{
				template: templateObservation{
					found: true,
				},
				modelCaches: modelCachesObservation{
					allCachesAvailable: false,
					cacheStatus: map[string]constants.AIMStatus{
						"model1": constants.AIMStatusFailed,
					},
				},
			},
			expectedStatus: constants.AIMStatusFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := &aimv1alpha1.AIMTemplateCacheStatus{}
			cm := controllerutils.NewConditionManager(nil)

			r := &Reconciler{}
			r.Project(status, cm, tt.obs)

			if status.Status != tt.expectedStatus {
				t.Errorf("expected status=%v, got %v", tt.expectedStatus, status.Status)
			}

			if tt.checkCondition != nil {
				tt.checkCondition(t, cm)
			}
		})
	}
}
