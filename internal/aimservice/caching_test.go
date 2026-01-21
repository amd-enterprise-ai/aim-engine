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

	corev1 "k8s.io/api/core/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

const (
	testStorageClassName = "fast-storage"
)

// ============================================================================
// NAME GENERATION TESTS
// ============================================================================

func TestGenerateServicePVCName(t *testing.T) {
	tests := []struct {
		name         string
		serviceName  string
		namespace    string
		wantContains []string
	}{
		{
			name:         "simple service",
			serviceName:  "my-service",
			namespace:    "my-namespace",
			wantContains: []string{"my-service", "temp-cache"},
		},
		{
			name:         "long service name",
			serviceName:  "very-long-service-name-that-might-exceed-limits",
			namespace:    "default",
			wantContains: []string{"temp-cache"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := GenerateServicePVCName(tt.serviceName, tt.namespace)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			for _, want := range tt.wantContains {
				if !strings.Contains(result, want) {
					t.Errorf("expected result to contain %q, got %q", want, result)
				}
			}

			// Verify k8s name constraints (max 63 chars, lowercase)
			if len(result) > 63 {
				t.Errorf("name too long: %d chars", len(result))
			}
		})
	}
}

func TestGenerateServicePVCName_Deterministic(t *testing.T) {
	result1, _ := GenerateServicePVCName("svc", "ns")
	result2, _ := GenerateServicePVCName("svc", "ns")
	result3, _ := GenerateServicePVCName("svc", "ns")

	if result1 != result2 || result2 != result3 {
		t.Errorf("expected deterministic output, got %q, %q, %q", result1, result2, result3)
	}
}

func TestGenerateTemplateCacheName(t *testing.T) {
	tests := []struct {
		name         string
		templateName string
		namespace    string
		wantContains []string
	}{
		{
			name:         "simple template",
			templateName: "llama-template",
			namespace:    "my-namespace",
			wantContains: []string{"llama-template"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := GenerateTemplateCacheName(tt.templateName, tt.namespace)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			for _, want := range tt.wantContains {
				if !strings.Contains(result, want) {
					t.Errorf("expected result to contain %q, got %q", want, result)
				}
			}
		})
	}
}

// ============================================================================
// CALCULATE REQUIRED STORAGE SIZE TESTS
// ============================================================================

func TestCalculateRequiredStorageSize(t *testing.T) {
	tests := []struct {
		name            string
		modelSources    []aimv1alpha1.AIMModelSource
		headroomPercent int32
		wantMinGi       int64
		wantErr         bool
	}{
		{
			name:            "empty sources",
			modelSources:    []aimv1alpha1.AIMModelSource{},
			headroomPercent: 10,
			wantErr:         true,
		},
		{
			name: "source without size",
			modelSources: []aimv1alpha1.AIMModelSource{
				NewModelSourceWithoutSize("hf://model/file.safetensors"),
			},
			headroomPercent: 10,
			wantErr:         true,
		},
		{
			name: "single source with size",
			modelSources: []aimv1alpha1.AIMModelSource{
				NewModelSource("hf://model/file.safetensors", 10*1024*1024*1024), // 10 Gi
			},
			headroomPercent: 10,
			wantMinGi:       11, // 10 + 10% = 11 Gi
			wantErr:         false,
		},
		{
			name: "multiple sources",
			modelSources: []aimv1alpha1.AIMModelSource{
				NewModelSource("hf://model/file1.safetensors", 5*1024*1024*1024), // 5 Gi
				NewModelSource("hf://model/file2.safetensors", 5*1024*1024*1024), // 5 Gi
			},
			headroomPercent: 10,
			wantMinGi:       11, // 10 + 10% = 11 Gi
			wantErr:         false,
		},
		{
			name: "higher headroom",
			modelSources: []aimv1alpha1.AIMModelSource{
				NewModelSource("hf://model/file.safetensors", 10*1024*1024*1024), // 10 Gi
			},
			headroomPercent: 50,
			wantMinGi:       15, // 10 + 50% = 15 Gi
			wantErr:         false,
		},
		{
			name: "small size rounds up to 1Gi",
			modelSources: []aimv1alpha1.AIMModelSource{
				NewModelSource("hf://model/small.safetensors", 100*1024*1024), // 100 Mi
			},
			headroomPercent: 10,
			wantMinGi:       1, // Minimum 1 Gi
			wantErr:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := calculateRequiredStorageSize(tt.modelSources, tt.headroomPercent)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			// Check result is at least the expected size
			resultGi := result.Value() / (1024 * 1024 * 1024)
			if resultGi < tt.wantMinGi {
				t.Errorf("expected at least %dGi, got %dGi", tt.wantMinGi, resultGi)
			}
		})
	}
}

// ============================================================================
// QUANTITY WITH HEADROOM TESTS
// ============================================================================

func TestQuantityWithHeadroom(t *testing.T) {
	tests := []struct {
		name            string
		bytes           int64
		headroomPercent int32
		expectedGi      int64
	}{
		{
			name:            "10Gi with 10% headroom",
			bytes:           10 * 1024 * 1024 * 1024,
			headroomPercent: 10,
			expectedGi:      11,
		},
		{
			name:            "10Gi with 0% headroom",
			bytes:           10 * 1024 * 1024 * 1024,
			headroomPercent: 0,
			expectedGi:      10,
		},
		{
			name:            "rounds up fractional Gi",
			bytes:           10*1024*1024*1024 + 500*1024*1024, // 10.5Gi
			headroomPercent: 0,
			expectedGi:      11, // Rounds up
		},
		{
			name:            "minimum 1Gi",
			bytes:           1, // 1 byte
			headroomPercent: 0,
			expectedGi:      1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := quantityWithHeadroom(tt.bytes, tt.headroomPercent)

			resultGi := result.Value() / (1024 * 1024 * 1024)
			if resultGi != tt.expectedGi {
				t.Errorf("expected %dGi, got %dGi", tt.expectedGi, resultGi)
			}
		})
	}
}

// ============================================================================
// RESOLVE STORAGE CLASS NAME TESTS
// ============================================================================

func TestResolveStorageClassName(t *testing.T) {
	tests := []struct {
		name         string
		service      *aimv1alpha1.AIMService
		obs          ServiceObservation
		expectedName string
	}{
		{
			name:         "no storage config",
			service:      NewService("svc").Build(),
			obs:          ServiceObservation{},
			expectedName: "",
		},
		{
			name: "service-level storage class",
			service: func() *aimv1alpha1.AIMService {
				svc := NewService("svc").Build()
				storageClass := testStorageClassName
				svc.Spec.Storage = &aimv1alpha1.AIMStorageConfig{
					DefaultStorageClassName: &storageClass,
				}
				return svc
			}(),
			obs:          ServiceObservation{},
			expectedName: testStorageClassName,
		},
		{
			name:    "runtime config storage class",
			service: NewService("svc").Build(),
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					mergedRuntimeConfig: controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]{
						Value: &aimv1alpha1.AIMRuntimeConfigCommon{
							AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
								Storage: &aimv1alpha1.AIMStorageConfig{
									DefaultStorageClassName: stringPtr("runtime-storage"),
								},
							},
						},
					},
				},
			},
			expectedName: "runtime-storage",
		},
		{
			name: "service overrides runtime config",
			service: func() *aimv1alpha1.AIMService {
				svc := NewService("svc").Build()
				storageClass := "service-storage"
				svc.Spec.Storage = &aimv1alpha1.AIMStorageConfig{
					DefaultStorageClassName: &storageClass,
				}
				return svc
			}(),
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					mergedRuntimeConfig: controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]{
						Value: &aimv1alpha1.AIMRuntimeConfigCommon{
							AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
								Storage: &aimv1alpha1.AIMStorageConfig{
									DefaultStorageClassName: stringPtr("runtime-storage"),
								},
							},
						},
					},
				},
			},
			expectedName: "service-storage",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := resolveStorageClassName(tt.service, tt.obs)

			if result != tt.expectedName {
				t.Errorf("expected %q, got %q", tt.expectedName, result)
			}
		})
	}
}

// ============================================================================
// RESOLVE PVC HEADROOM PERCENT TESTS
// ============================================================================

func TestResolvePVCHeadroomPercent(t *testing.T) {
	tests := []struct {
		name     string
		service  *aimv1alpha1.AIMService
		obs      ServiceObservation
		expected int32
	}{
		{
			name:     "default headroom",
			service:  NewService("svc").Build(),
			obs:      ServiceObservation{},
			expected: DefaultPVCHeadroomPercent,
		},
		{
			name: "service-level headroom",
			service: func() *aimv1alpha1.AIMService {
				svc := NewService("svc").Build()
				headroom := int32(20)
				svc.Spec.Storage = &aimv1alpha1.AIMStorageConfig{
					PVCHeadroomPercent: &headroom,
				}
				return svc
			}(),
			obs:      ServiceObservation{},
			expected: 20,
		},
		{
			name:    "runtime config headroom",
			service: NewService("svc").Build(),
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					mergedRuntimeConfig: controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]{
						Value: &aimv1alpha1.AIMRuntimeConfigCommon{
							AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
								Storage: &aimv1alpha1.AIMStorageConfig{
									PVCHeadroomPercent: int32Ptr(15),
								},
							},
						},
					},
				},
			},
			expected: 15,
		},
		{
			name: "service overrides runtime config",
			service: func() *aimv1alpha1.AIMService {
				svc := NewService("svc").Build()
				headroom := int32(25)
				svc.Spec.Storage = &aimv1alpha1.AIMStorageConfig{
					PVCHeadroomPercent: &headroom,
				}
				return svc
			}(),
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					mergedRuntimeConfig: controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]{
						Value: &aimv1alpha1.AIMRuntimeConfigCommon{
							AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
								Storage: &aimv1alpha1.AIMStorageConfig{
									PVCHeadroomPercent: int32Ptr(15),
								},
							},
						},
					},
				},
			},
			expected: 25,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := resolvePVCHeadroomPercent(tt.service, tt.obs)

			if result != tt.expected {
				t.Errorf("expected %d, got %d", tt.expected, result)
			}
		})
	}
}

// ============================================================================
// PLAN SERVICE PVC TESTS
// ============================================================================

func TestPlanServicePVC(t *testing.T) {
	tests := []struct {
		name           string
		service        *aimv1alpha1.AIMService
		templateName   string
		templateStatus *aimv1alpha1.AIMServiceTemplateStatus
		obs            ServiceObservation
		expectPVC      bool
	}{
		{
			name:           "no template status",
			service:        NewService("svc").Build(),
			templateName:   "template",
			templateStatus: nil,
			obs:            ServiceObservation{},
			expectPVC:      false,
		},
		{
			name:         "caching mode always - no PVC",
			service:      NewService("svc").WithCachingMode(aimv1alpha1.CachingModeAlways).Build(),
			templateName: "template",
			templateStatus: &aimv1alpha1.AIMServiceTemplateStatus{
				ModelSources: []aimv1alpha1.AIMModelSource{
					NewModelSource("hf://model/file.safetensors", 10*1024*1024*1024),
				},
			},
			obs:       ServiceObservation{},
			expectPVC: false,
		},
		{
			name:         "template cache ready - no PVC",
			service:      NewService("svc").Build(),
			templateName: "template",
			templateStatus: &aimv1alpha1.AIMServiceTemplateStatus{
				ModelSources: []aimv1alpha1.AIMModelSource{
					NewModelSource("hf://model/file.safetensors", 10*1024*1024*1024),
				},
			},
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					templateCache: controllerutils.FetchResult[*aimv1alpha1.AIMTemplateCache]{
						Value: &aimv1alpha1.AIMTemplateCache{
							Status: aimv1alpha1.AIMTemplateCacheStatus{
								Status: constants.AIMStatusReady,
							},
						},
					},
				},
			},
			expectPVC: false,
		},
		{
			name:         "PVC already exists - no new PVC",
			service:      NewService("svc").Build(),
			templateName: "template",
			templateStatus: &aimv1alpha1.AIMServiceTemplateStatus{
				ModelSources: []aimv1alpha1.AIMModelSource{
					NewModelSource("hf://model/file.safetensors", 10*1024*1024*1024),
				},
			},
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					pvc: controllerutils.FetchResult[*corev1.PersistentVolumeClaim]{
						Value: &corev1.PersistentVolumeClaim{},
					},
				},
			},
			expectPVC: false,
		},
		{
			name:         "no model sources - no PVC",
			service:      NewService("svc").Build(),
			templateName: "template",
			templateStatus: &aimv1alpha1.AIMServiceTemplateStatus{
				ModelSources: []aimv1alpha1.AIMModelSource{},
			},
			obs:       ServiceObservation{},
			expectPVC: false,
		},
		{
			name:         "creates PVC when conditions met",
			service:      NewService("svc").Build(),
			templateName: "template",
			templateStatus: &aimv1alpha1.AIMServiceTemplateStatus{
				ModelSources: []aimv1alpha1.AIMModelSource{
					NewModelSource("hf://model/file.safetensors", 10*1024*1024*1024),
				},
			},
			obs:       ServiceObservation{},
			expectPVC: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := planServicePVC(tt.service, tt.templateName, tt.templateStatus, tt.obs)

			if tt.expectPVC {
				if result == nil {
					t.Error("expected PVC to be created, got nil")
				}
			} else {
				if result != nil {
					t.Errorf("expected no PVC, got %T", result)
				}
			}
		})
	}
}

func TestPlanServicePVC_Labels(t *testing.T) {
	service := NewService("my-svc").Build()
	templateStatus := &aimv1alpha1.AIMServiceTemplateStatus{
		ModelSources: []aimv1alpha1.AIMModelSource{
			NewModelSource("hf://model/file.safetensors", 10*1024*1024*1024),
		},
	}

	result := planServicePVC(service, "my-template", templateStatus, ServiceObservation{})

	if result == nil {
		t.Fatal("expected PVC, got nil")
	}

	pvc, ok := result.(*corev1.PersistentVolumeClaim)
	if !ok {
		t.Fatalf("expected *PersistentVolumeClaim, got %T", result)
	}

	// Check labels
	if pvc.Labels[constants.LabelK8sManagedBy] != constants.LabelValueManagedBy {
		t.Errorf("expected managed-by label")
	}
	if pvc.Labels[constants.LabelCacheType] != constants.LabelValueCacheTypeTemp {
		t.Errorf("expected cache-type=temp label")
	}
}

// ============================================================================
// PLAN TEMPLATE CACHE TESTS
// ============================================================================

func TestPlanTemplateCache(t *testing.T) {
	tests := []struct {
		name           string
		service        *aimv1alpha1.AIMService
		templateName   string
		templateStatus *aimv1alpha1.AIMServiceTemplateStatus
		obs            ServiceObservation
		expectCache    bool
	}{
		{
			name:           "caching mode never - no cache",
			service:        NewService("svc").WithCachingMode(aimv1alpha1.CachingModeNever).Build(),
			templateName:   "template",
			templateStatus: &aimv1alpha1.AIMServiceTemplateStatus{},
			obs:            ServiceObservation{},
			expectCache:    false,
		},
		{
			name:         "cache already exists - no new cache",
			service:      NewService("svc").Build(),
			templateName: "template",
			templateStatus: &aimv1alpha1.AIMServiceTemplateStatus{
				ModelSources: []aimv1alpha1.AIMModelSource{
					NewModelSource("hf://model/file.safetensors", 10*1024*1024*1024),
				},
			},
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					templateCache: controllerutils.FetchResult[*aimv1alpha1.AIMTemplateCache]{
						Value: &aimv1alpha1.AIMTemplateCache{},
					},
				},
			},
			expectCache: false,
		},
		{
			name:           "no model sources - no cache",
			service:        NewService("svc").Build(),
			templateName:   "template",
			templateStatus: &aimv1alpha1.AIMServiceTemplateStatus{},
			obs:            ServiceObservation{},
			expectCache:    false,
		},
		{
			name:         "auto mode does not create cache (uses existing only)",
			service:      NewService("svc").Build(), // Default is Auto
			templateName: "template",
			templateStatus: &aimv1alpha1.AIMServiceTemplateStatus{
				ModelSources: []aimv1alpha1.AIMModelSource{
					NewModelSource("hf://model/file.safetensors", 10*1024*1024*1024),
				},
			},
			obs:         ServiceObservation{},
			expectCache: false, // Auto mode uses existing caches but doesn't create new ones
		},
		{
			name:         "creates cache when conditions met (always mode)",
			service:      NewService("svc").WithCachingMode(aimv1alpha1.CachingModeAlways).Build(),
			templateName: "template",
			templateStatus: &aimv1alpha1.AIMServiceTemplateStatus{
				ModelSources: []aimv1alpha1.AIMModelSource{
					NewModelSource("hf://model/file.safetensors", 10*1024*1024*1024),
				},
			},
			obs:         ServiceObservation{},
			expectCache: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := planTemplateCache(tt.service, tt.templateName, tt.templateStatus, tt.obs)

			if tt.expectCache {
				if result == nil {
					t.Error("expected template cache to be created, got nil")
				}
			} else {
				if result != nil {
					t.Errorf("expected no cache, got %T", result)
				}
			}
		})
	}
}

func TestPlanTemplateCache_Spec(t *testing.T) {
	storageClass := testStorageClassName
	service := NewService("my-svc").WithCachingMode(aimv1alpha1.CachingModeAlways).Build()
	service.Spec.Storage = &aimv1alpha1.AIMStorageConfig{
		DefaultStorageClassName: &storageClass,
	}

	templateStatus := &aimv1alpha1.AIMServiceTemplateStatus{
		ModelSources: []aimv1alpha1.AIMModelSource{
			NewModelSource("hf://model/file.safetensors", 10*1024*1024*1024),
		},
	}

	result := planTemplateCache(service, "my-template", templateStatus, ServiceObservation{})

	if result == nil {
		t.Fatal("expected cache, got nil")
	}

	cache, ok := result.(*aimv1alpha1.AIMTemplateCache)
	if !ok {
		t.Fatalf("expected *AIMTemplateCache, got %T", result)
	}

	// Check spec
	if cache.Spec.TemplateName != "my-template" {
		t.Errorf("expected templateName my-template, got %s", cache.Spec.TemplateName)
	}
	if cache.Spec.StorageClassName != testStorageClassName {
		t.Errorf("expected storageClassName %s, got %s", testStorageClassName, cache.Spec.StorageClassName)
	}
}

// ============================================================================
// HELPERS
// ============================================================================

func stringPtr(s string) *string {
	return &s
}

func int32Ptr(i int32) *int32 {
	return &i
}
