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
	"k8s.io/apimachinery/pkg/api/resource"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

// ============================================================================
// GENERATE INFERENCE SERVICE NAME TESTS
// ============================================================================

func TestGenerateInferenceServiceName(t *testing.T) {
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
			wantContains: []string{"my-service"},
		},
		{
			name:         "long service name is truncated",
			serviceName:  "very-long-service-name-that-might-exceed-kubernetes-limits",
			namespace:    "default",
			wantContains: []string{}, // Just verify it doesn't error
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := GenerateInferenceServiceName(tt.serviceName, tt.namespace)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			// Verify k8s name constraints
			if len(result) > 63 {
				t.Errorf("name too long: %d chars", len(result))
			}

			for _, want := range tt.wantContains {
				if !strings.Contains(result, want) {
					t.Errorf("expected result to contain %q, got %q", want, result)
				}
			}
		})
	}
}

func TestGenerateInferenceServiceName_Deterministic(t *testing.T) {
	result1, _ := GenerateInferenceServiceName("svc", "ns")
	result2, _ := GenerateInferenceServiceName("svc", "ns")

	if result1 != result2 {
		t.Errorf("expected deterministic output, got %q and %q", result1, result2)
	}
}

// ============================================================================
// IS READY FOR INFERENCE SERVICE TESTS
// ============================================================================

func TestIsReadyForInferenceService(t *testing.T) {
	tests := []struct {
		name     string
		service  *aimv1alpha1.AIMService
		obs      ServiceObservation
		expected bool
	}{
		{
			name:     "not ready - no model",
			service:  NewService("svc").Build(),
			obs:      ServiceObservation{},
			expected: false,
		},
		{
			name:    "not ready - model not ready",
			service: NewService("svc").Build(),
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					modelResult: ModelFetchResult{
						Model: controllerutils.FetchResult[*aimv1alpha1.AIMModel]{
							Value: NewModel("m").WithStatus(constants.AIMStatusPending).Build(),
						},
					},
				},
			},
			expected: false,
		},
		{
			name:    "caching mode Always - not ready without cache",
			service: NewService("svc").WithCachingMode(aimv1alpha1.CachingModeAlways).Build(),
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					modelResult: ModelFetchResult{
						Model: controllerutils.FetchResult[*aimv1alpha1.AIMModel]{
							Value: NewModel("m").WithStatus(constants.AIMStatusReady).Build(),
						},
					},
				},
			},
			expected: false,
		},
		{
			name:    "caching mode Always - ready with cache",
			service: NewService("svc").WithCachingMode(aimv1alpha1.CachingModeAlways).Build(),
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					modelResult: ModelFetchResult{
						Model: controllerutils.FetchResult[*aimv1alpha1.AIMModel]{
							Value: NewModel("m").WithStatus(constants.AIMStatusReady).Build(),
						},
					},
					templateCache: controllerutils.FetchResult[*aimv1alpha1.AIMTemplateCache]{
						Value: &aimv1alpha1.AIMTemplateCache{
							Status: aimv1alpha1.AIMTemplateCacheStatus{
								Status: constants.AIMStatusReady,
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			name:    "caching mode Auto - ready with template cache",
			service: NewService("svc").Build(), // Auto is default
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					modelResult: ModelFetchResult{
						Model: controllerutils.FetchResult[*aimv1alpha1.AIMModel]{
							Value: NewModel("m").WithStatus(constants.AIMStatusReady).Build(),
						},
					},
					templateCache: controllerutils.FetchResult[*aimv1alpha1.AIMTemplateCache]{
						Value: &aimv1alpha1.AIMTemplateCache{
							Status: aimv1alpha1.AIMTemplateCacheStatus{
								Status: constants.AIMStatusReady,
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			name:    "caching mode Never - ready with template cache",
			service: NewService("svc").WithCachingMode(aimv1alpha1.CachingModeNever).Build(),
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					modelResult: ModelFetchResult{
						Model: controllerutils.FetchResult[*aimv1alpha1.AIMModel]{
							Value: NewModel("m").WithStatus(constants.AIMStatusReady).Build(),
						},
					},
					templateCache: controllerutils.FetchResult[*aimv1alpha1.AIMTemplateCache]{
						Value: &aimv1alpha1.AIMTemplateCache{
							Spec: aimv1alpha1.AIMTemplateCacheSpec{
								Mode: aimv1alpha1.TemplateCacheModeDedicated,
							},
							Status: aimv1alpha1.AIMTemplateCacheStatus{
								Status: constants.AIMStatusReady,
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			name:    "cluster model ready with template cache",
			service: NewService("svc").Build(),
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					modelResult: ModelFetchResult{
						ClusterModel: controllerutils.FetchResult[*aimv1alpha1.AIMClusterModel]{
							Value: NewClusterModel("cm").WithStatus(constants.AIMStatusReady).Build(),
						},
					},
					templateCache: controllerutils.FetchResult[*aimv1alpha1.AIMTemplateCache]{
						Value: &aimv1alpha1.AIMTemplateCache{
							Status: aimv1alpha1.AIMTemplateCacheStatus{
								Status: constants.AIMStatusReady,
							},
						},
					},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isReadyForInferenceService(tt.service, tt.obs)

			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

// ============================================================================
// RESOLVE RESOURCES TESTS
// ============================================================================

func TestResolveResources(t *testing.T) {
	tests := []struct {
		name           string
		service        *aimv1alpha1.AIMService
		templateSpec   *aimv1alpha1.AIMServiceTemplateSpecCommon
		gpuCount       int64 // Template profile GPU count
		expectGPU      bool
		expectGPUCount int64 // Expected final GPU count (defaults to gpuCount if 0)
		expectMemory   string
	}{
		{
			name:         "no GPU",
			service:      NewService("svc").Build(),
			templateSpec: nil,
			gpuCount:     0,
			expectGPU:    false,
		},
		{
			name:         "4 GPUs - default resources",
			service:      NewService("svc").Build(),
			templateSpec: nil,
			gpuCount:     4,
			expectGPU:    true,
			expectMemory: "128Gi", // 4 * 32Gi
		},
		{
			name: "service overrides memory resources",
			service: func() *aimv1alpha1.AIMService {
				svc := NewService("svc").Build()
				svc.Spec.Resources = &corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceMemory: resource.MustParse("64Gi"),
					},
				}
				return svc
			}(),
			templateSpec: nil,
			gpuCount:     4,
			expectGPU:    true,
			expectMemory: "64Gi", // Override
		},
		{
			name: "service overrides GPU count",
			service: func() *aimv1alpha1.AIMService {
				svc := NewService("svc").Build()
				svc.Spec.Resources = &corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceName(constants.DefaultGPUResourceName): resource.MustParse("8"),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceName(constants.DefaultGPUResourceName): resource.MustParse("8"),
					},
				}
				return svc
			}(),
			templateSpec:   nil,
			gpuCount:       1, // Template says 1 GPU
			expectGPU:      true,
			expectGPUCount: 8, // Service overrides to 8 GPUs
		},
		{
			name:    "template spec resources",
			service: NewService("svc").Build(),
			templateSpec: &aimv1alpha1.AIMServiceTemplateSpecCommon{
				Resources: &corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceMemory: resource.MustParse("256Gi"),
					},
				},
			},
			gpuCount:     4,
			expectGPU:    true,
			expectMemory: "256Gi",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := resolveResources(tt.service, tt.templateSpec, tt.gpuCount, corev1.ResourceName(constants.DefaultGPUResourceName))

			if tt.expectGPU {
				gpuQty := result.Requests[corev1.ResourceName(constants.DefaultGPUResourceName)]
				expectedGPU := tt.expectGPUCount
				if expectedGPU == 0 {
					expectedGPU = tt.gpuCount // Default to template GPU count
				}
				if gpuQty.Value() != expectedGPU {
					t.Errorf("expected GPU count %d, got %d", expectedGPU, gpuQty.Value())
				}
			}

			if tt.expectMemory != "" {
				memQty := result.Requests[corev1.ResourceMemory]
				expected := resource.MustParse(tt.expectMemory)
				if memQty.Cmp(expected) != 0 {
					t.Errorf("expected memory %s, got %s", tt.expectMemory, memQty.String())
				}
			}
		})
	}
}

// ============================================================================
// DEFAULT RESOURCE REQUIREMENTS TESTS
// ============================================================================

func TestDefaultResourceRequirementsForGPU(t *testing.T) {
	tests := []struct {
		name       string
		gpuCount   int64
		expectCPU  int64
		expectMem  string
		expectZero bool
	}{
		{
			name:       "0 GPUs",
			gpuCount:   0,
			expectZero: true,
		},
		{
			name:      "1 GPU",
			gpuCount:  1,
			expectCPU: 4,
			expectMem: "32Gi",
		},
		{
			name:      "4 GPUs",
			gpuCount:  4,
			expectCPU: 16,
			expectMem: "128Gi",
		},
		{
			name:      "8 GPUs",
			gpuCount:  8,
			expectCPU: 32,
			expectMem: "256Gi",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := defaultResourceRequirementsForGPU(tt.gpuCount)

			if tt.expectZero {
				if len(result.Requests) != 0 || len(result.Limits) != 0 {
					t.Errorf("expected zero resources, got %+v", result)
				}
				return
			}

			cpuQty := result.Requests[corev1.ResourceCPU]
			if cpuQty.Value() != tt.expectCPU {
				t.Errorf("expected CPU %d, got %d", tt.expectCPU, cpuQty.Value())
			}

			memQty := result.Requests[corev1.ResourceMemory]
			expectedMem := resource.MustParse(tt.expectMem)
			if memQty.Cmp(expectedMem) != 0 {
				t.Errorf("expected memory %s, got %s", tt.expectMem, memQty.String())
			}
		})
	}
}

// ============================================================================
// MERGE RESOURCE REQUIREMENTS TESTS
// ============================================================================

func TestMergeResourceRequirements(t *testing.T) {
	tests := []struct {
		name     string
		base     corev1.ResourceRequirements
		override *corev1.ResourceRequirements
		expected corev1.ResourceRequirements
	}{
		{
			name: "nil override returns base",
			base: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("16Gi"),
				},
			},
			override: nil,
			expected: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("16Gi"),
				},
			},
		},
		{
			name: "override replaces matching keys",
			base: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("16Gi"),
					corev1.ResourceCPU:    resource.MustParse("4"),
				},
			},
			override: &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("32Gi"),
				},
			},
			expected: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("32Gi"),
					corev1.ResourceCPU:    resource.MustParse("4"),
				},
			},
		},
		{
			name: "override adds new keys",
			base: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("16Gi"),
				},
			},
			override: &corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("32Gi"),
				},
			},
			expected: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("16Gi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("32Gi"),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mergeResourceRequirements(tt.base, tt.override)

			// Compare requests
			for key, expectedQty := range tt.expected.Requests {
				resultQty := result.Requests[key]
				if resultQty.Cmp(expectedQty) != 0 {
					t.Errorf("Requests[%s]: expected %s, got %s", key, expectedQty.String(), resultQty.String())
				}
			}

			// Compare limits
			for key, expectedQty := range tt.expected.Limits {
				resultQty := result.Limits[key]
				if resultQty.Cmp(expectedQty) != 0 {
					t.Errorf("Limits[%s]: expected %s, got %s", key, expectedQty.String(), resultQty.String())
				}
			}
		})
	}
}

// ============================================================================
// BUILD MERGED ENV VARS TESTS
// ============================================================================

func TestBuildMergedEnvVars(t *testing.T) {
	tests := []struct {
		name             string
		service          *aimv1alpha1.AIMService
		templateSpec     *aimv1alpha1.AIMServiceTemplateSpecCommon
		templateStatus   *aimv1alpha1.AIMServiceTemplateStatus
		obs              ServiceObservation
		expectContains   []string
		expectNotContain []string
	}{
		{
			name:           "system defaults always present",
			service:        &aimv1alpha1.AIMService{},
			templateSpec:   nil,
			templateStatus: nil,
			obs:            ServiceObservation{},
			expectContains: []string{constants.EnvAIMCachePath, constants.EnvVLLMEnableMetrics},
		},
		{
			name:    "template spec env vars",
			service: &aimv1alpha1.AIMService{},
			templateSpec: &aimv1alpha1.AIMServiceTemplateSpecCommon{
				Env: []corev1.EnvVar{
					{Name: "CUSTOM_VAR", Value: "custom-value"},
				},
			},
			templateStatus: nil,
			obs:            ServiceObservation{},
			expectContains: []string{"CUSTOM_VAR"},
		},
		{
			name:    "template spec metric and precision",
			service: &aimv1alpha1.AIMService{},
			templateSpec: func() *aimv1alpha1.AIMServiceTemplateSpecCommon {
				latency := aimv1alpha1.AIMMetricLatency
				fp16 := aimv1alpha1.AIMPrecisionFP16
				return &aimv1alpha1.AIMServiceTemplateSpecCommon{
					AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
						Metric:    &latency,
						Precision: &fp16,
					},
				}
			}(),
			templateStatus: nil,
			obs:            ServiceObservation{},
			expectContains: []string{constants.EnvAIMMetric, constants.EnvAIMPrecision},
		},
		{
			name:    "template spec profile id",
			service: &aimv1alpha1.AIMService{},
			templateSpec: &aimv1alpha1.AIMServiceTemplateSpecCommon{
				ProfileId: "my-profile-123",
			},
			templateStatus: nil,
			obs:            ServiceObservation{},
			expectContains: []string{constants.EnvAIMProfileID},
		},
		{
			name:         "profile env vars",
			service:      &aimv1alpha1.AIMService{},
			templateSpec: nil,
			templateStatus: &aimv1alpha1.AIMServiceTemplateStatus{
				Profile: &aimv1alpha1.AIMProfile{
					EnvVars: map[string]string{
						"PROFILE_VAR": "profile-value",
					},
				},
			},
			obs:            ServiceObservation{},
			expectContains: []string{"PROFILE_VAR"},
		},
		{
			name: "service env vars have highest precedence",
			service: &aimv1alpha1.AIMService{
				Spec: aimv1alpha1.AIMServiceSpec{
					AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
						Env: []corev1.EnvVar{
							{Name: "SERVICE_VAR", Value: "service-value"},
							{Name: "SHARED_VAR", Value: "from-service"},
						},
					},
				},
			},
			templateSpec: &aimv1alpha1.AIMServiceTemplateSpecCommon{
				Env: []corev1.EnvVar{
					{Name: "SHARED_VAR", Value: "from-template"},
				},
			},
			templateStatus: nil,
			obs:            ServiceObservation{},
			expectContains: []string{"SERVICE_VAR", "SHARED_VAR"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildMergedEnvVars(tt.service, tt.templateSpec, tt.templateStatus, tt.obs)

			envMap := make(map[string]string)
			for _, env := range result {
				envMap[env.Name] = env.Value
			}

			for _, expected := range tt.expectContains {
				if _, ok := envMap[expected]; !ok {
					t.Errorf("expected env var %s not found", expected)
				}
			}

			for _, notExpected := range tt.expectNotContain {
				if _, ok := envMap[notExpected]; ok {
					t.Errorf("unexpected env var %s found", notExpected)
				}
			}
		})
	}
}

func TestBuildMergedEnvVars_IsSorted(t *testing.T) {
	service := &aimv1alpha1.AIMService{}
	templateSpec := &aimv1alpha1.AIMServiceTemplateSpecCommon{
		Env: []corev1.EnvVar{
			{Name: "ZEBRA", Value: "z"},
			{Name: "APPLE", Value: "a"},
			{Name: "MANGO", Value: "m"},
		},
	}

	result := buildMergedEnvVars(service, templateSpec, nil, ServiceObservation{})

	for i := 1; i < len(result); i++ {
		if result[i-1].Name > result[i].Name {
			t.Errorf("env vars not sorted: %s > %s", result[i-1].Name, result[i].Name)
		}
	}
}

func TestBuildMergedEnvVars_ServiceOverridesAll(t *testing.T) {
	// Test that service env vars override profile, template, and runtime config
	service := &aimv1alpha1.AIMService{
		Spec: aimv1alpha1.AIMServiceSpec{
			AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
				Env: []corev1.EnvVar{
					{Name: "SHARED_VAR", Value: "from-service"},
				},
			},
		},
	}
	templateSpec := &aimv1alpha1.AIMServiceTemplateSpecCommon{
		Env: []corev1.EnvVar{
			{Name: "SHARED_VAR", Value: "from-template"},
		},
	}
	templateStatus := &aimv1alpha1.AIMServiceTemplateStatus{
		Profile: &aimv1alpha1.AIMProfile{
			EnvVars: map[string]string{
				"SHARED_VAR": "from-profile",
			},
		},
	}
	obs := ServiceObservation{
		ServiceFetchResult: ServiceFetchResult{
			mergedRuntimeConfig: controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]{
				Value: &aimv1alpha1.AIMRuntimeConfigCommon{
					AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
						Env: []corev1.EnvVar{
							{Name: "SHARED_VAR", Value: "from-runtime-config"},
						},
					},
				},
			},
		},
	}

	result := buildMergedEnvVars(service, templateSpec, templateStatus, obs)

	envMap := make(map[string]string)
	for _, env := range result {
		envMap[env.Name] = env.Value
	}

	// Service should win
	if envMap["SHARED_VAR"] != "from-service" {
		t.Errorf("expected SHARED_VAR='from-service', got '%s'", envMap["SHARED_VAR"])
	}
}

func TestBuildMergedEnvVars_ClusterTemplateEnv(t *testing.T) {
	// Test that env vars from cluster template spec (via common spec) propagate to inference service
	service := &aimv1alpha1.AIMService{}

	// Simulate a cluster template spec (same as namespace, just common spec)
	templateSpec := &aimv1alpha1.AIMServiceTemplateSpecCommon{
		Env: []corev1.EnvVar{
			{Name: "CLUSTER_TOKEN", Value: "my-cluster-token"},
			{Name: "SHARED_VAR", Value: "from-cluster-template"},
		},
	}

	result := buildMergedEnvVars(service, templateSpec, nil, ServiceObservation{})

	envMap := make(map[string]string)
	for _, env := range result {
		envMap[env.Name] = env.Value
	}

	// Check cluster template env vars are present
	if val, ok := envMap["CLUSTER_TOKEN"]; !ok {
		t.Error("missing env var CLUSTER_TOKEN")
	} else if val != "my-cluster-token" {
		t.Errorf("expected CLUSTER_TOKEN='my-cluster-token', got '%s'", val)
	}

	if val, ok := envMap["SHARED_VAR"]; !ok {
		t.Error("missing env var SHARED_VAR")
	} else if val != "from-cluster-template" {
		t.Errorf("expected SHARED_VAR='from-cluster-template', got '%s'", val)
	}
}
