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
)

// ============================================================================
// DERIVED TEMPLATE NAME GENERATION TESTS
// ============================================================================

func TestGenerateDerivedTemplateName(t *testing.T) {
	latency := aimv1alpha1.AIMMetricLatency
	throughput := aimv1alpha1.AIMMetricThroughput
	fp16 := aimv1alpha1.AIMPrecisionFP16
	fp8 := aimv1alpha1.AIMPrecisionFP8

	tests := []struct {
		name           string
		baseName       string
		overrides      *aimv1alpha1.AIMServiceOverrides
		expectContains []string // Substrings expected in the result
		expectPrefix   string   // Expected prefix
	}{
		{
			name:         "nil overrides returns base name",
			baseName:     "my-template",
			overrides:    nil,
			expectPrefix: "my-template",
		},
		{
			name:     "empty overrides returns base name",
			baseName: "my-template",
			overrides: &aimv1alpha1.AIMServiceOverrides{
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{},
			},
			expectPrefix: "my-template",
		},
		{
			name:     "GPU model only",
			baseName: "base",
			overrides: &aimv1alpha1.AIMServiceOverrides{
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					Hardware: &aimv1alpha1.AIMHardwareRequirements{GPU: &aimv1alpha1.AIMGpuRequirements{Model: "MI300X"}},
				},
			},
			expectPrefix:   "base-ovr-mi300x-",
			expectContains: []string{"base", "ovr", "mi300x"},
		},
		{
			name:     "GPU count only",
			baseName: "base",
			overrides: &aimv1alpha1.AIMServiceOverrides{
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					Hardware: &aimv1alpha1.AIMHardwareRequirements{GPU: &aimv1alpha1.AIMGpuRequirements{Requests: 4}},
				},
			},
			expectPrefix:   "base-ovr-4gpu-",
			expectContains: []string{"base", "ovr", "4gpu"},
		},
		{
			name:     "GPU model and count",
			baseName: "base",
			overrides: &aimv1alpha1.AIMServiceOverrides{
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					Hardware: &aimv1alpha1.AIMHardwareRequirements{GPU: &aimv1alpha1.AIMGpuRequirements{Model: "MI325X", Requests: 8}},
				},
			},
			expectPrefix:   "base-ovr-mi325x-8gpu-",
			expectContains: []string{"base", "ovr", "mi325x", "8gpu"},
		},
		{
			name:     "precision only",
			baseName: "base",
			overrides: &aimv1alpha1.AIMServiceOverrides{
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					Precision: &fp16,
				},
			},
			expectPrefix:   "base-ovr-fp16-",
			expectContains: []string{"base", "ovr", "fp16"},
		},
		{
			name:     "metric only",
			baseName: "base",
			overrides: &aimv1alpha1.AIMServiceOverrides{
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					Metric: &latency,
				},
			},
			expectPrefix:   "base-ovr-latency-",
			expectContains: []string{"base", "ovr", "latency"},
		},
		{
			name:     "all overrides",
			baseName: "my-template",
			overrides: &aimv1alpha1.AIMServiceOverrides{
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					Hardware:  &aimv1alpha1.AIMHardwareRequirements{GPU: &aimv1alpha1.AIMGpuRequirements{Model: "MI300X", Requests: 4}},
					Precision: &fp8,
					Metric:    &throughput,
				},
			},
			expectPrefix:   "my-template-ovr-mi300x-4gpu-fp8-throughput-",
			expectContains: []string{"my-template", "ovr", "mi300x", "4gpu", "fp8", "throughput"},
		},
		{
			name:     "deterministic - same inputs same output",
			baseName: "template",
			overrides: &aimv1alpha1.AIMServiceOverrides{
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					Hardware:  &aimv1alpha1.AIMHardwareRequirements{GPU: &aimv1alpha1.AIMGpuRequirements{Model: "MI300X"}},
					Precision: &fp16,
				},
			},
			expectContains: []string{"template", "ovr", "mi300x", "fp16"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := generateDerivedTemplateName(tt.baseName, tt.overrides)

			// Check prefix if specified
			if tt.expectPrefix != "" && !strings.HasPrefix(result, tt.expectPrefix) {
				t.Errorf("expected prefix %q, got %q", tt.expectPrefix, result)
			}

			// Check expected substrings
			for _, substr := range tt.expectContains {
				if !strings.Contains(result, substr) {
					t.Errorf("expected result to contain %q, got %q", substr, result)
				}
			}
		})
	}
}

func TestGenerateDerivedTemplateName_Deterministic(t *testing.T) {
	fp16 := aimv1alpha1.AIMPrecisionFP16
	overrides := &aimv1alpha1.AIMServiceOverrides{
		AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
			Hardware:  &aimv1alpha1.AIMHardwareRequirements{GPU: &aimv1alpha1.AIMGpuRequirements{Model: "MI300X", Requests: 4}},
			Precision: &fp16,
		},
	}

	// Call multiple times - should always return the same result
	result1 := generateDerivedTemplateName("base", overrides)
	result2 := generateDerivedTemplateName("base", overrides)
	result3 := generateDerivedTemplateName("base", overrides)

	if result1 != result2 || result2 != result3 {
		t.Errorf("expected deterministic output, got %q, %q, %q", result1, result2, result3)
	}
}

func TestGenerateDerivedTemplateName_DifferentInputsDifferentOutputs(t *testing.T) {
	fp16 := aimv1alpha1.AIMPrecisionFP16
	fp8 := aimv1alpha1.AIMPrecisionFP8

	overrides1 := &aimv1alpha1.AIMServiceOverrides{
		AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
			Precision: &fp16,
		},
	}
	overrides2 := &aimv1alpha1.AIMServiceOverrides{
		AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
			Precision: &fp8,
		},
	}

	result1 := generateDerivedTemplateName("base", overrides1)
	result2 := generateDerivedTemplateName("base", overrides2)

	if result1 == result2 {
		t.Errorf("expected different outputs for different inputs, got same: %q", result1)
	}
}

// ============================================================================
// BUILD OVERRIDE NAME PARTS TESTS
// ============================================================================

func TestBuildOverrideNameParts(t *testing.T) {
	latency := aimv1alpha1.AIMMetricLatency
	fp16 := aimv1alpha1.AIMPrecisionFP16

	tests := []struct {
		name              string
		overrides         *aimv1alpha1.AIMServiceOverrides
		expectedParts     []string
		expectedHashCount int // Number of hash input pairs
	}{
		{
			name:              "nil overrides",
			overrides:         nil,
			expectedParts:     nil,
			expectedHashCount: 0,
		},
		{
			name: "empty overrides",
			overrides: &aimv1alpha1.AIMServiceOverrides{
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{},
			},
			expectedParts:     nil,
			expectedHashCount: 0,
		},
		{
			name: "GPU model",
			overrides: &aimv1alpha1.AIMServiceOverrides{
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					Hardware: &aimv1alpha1.AIMHardwareRequirements{GPU: &aimv1alpha1.AIMGpuRequirements{Model: "MI300X"}},
				},
			},
			expectedParts:     []string{"mi300x"},
			expectedHashCount: 2, // "gpu", "MI300X"
		},
		{
			name: "GPU count",
			overrides: &aimv1alpha1.AIMServiceOverrides{
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					Hardware: &aimv1alpha1.AIMHardwareRequirements{GPU: &aimv1alpha1.AIMGpuRequirements{Requests: 4}},
				},
			},
			expectedParts:     []string{"4gpu"},
			expectedHashCount: 2, // "count", 4
		},
		{
			name: "precision",
			overrides: &aimv1alpha1.AIMServiceOverrides{
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					Precision: &fp16,
				},
			},
			expectedParts:     []string{"fp16"},
			expectedHashCount: 2, // "precision", "fp16"
		},
		{
			name: "metric",
			overrides: &aimv1alpha1.AIMServiceOverrides{
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					Metric: &latency,
				},
			},
			expectedParts:     []string{"latency"},
			expectedHashCount: 2, // "metric", "latency"
		},
		{
			name: "all fields - order is gpu, count, precision, metric",
			overrides: &aimv1alpha1.AIMServiceOverrides{
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					Hardware:  &aimv1alpha1.AIMHardwareRequirements{GPU: &aimv1alpha1.AIMGpuRequirements{Model: "MI325X", Requests: 8}},
					Precision: &fp16,
					Metric:    &latency,
				},
			},
			expectedParts:     []string{"mi325x", "8gpu", "fp16", "latency"},
			expectedHashCount: 8, // 4 pairs
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parts, hashInputs := buildOverrideNameParts(tt.overrides)

			// Check parts
			if len(parts) != len(tt.expectedParts) {
				t.Errorf("expected %d parts, got %d: %v", len(tt.expectedParts), len(parts), parts)
				return
			}
			for i, expected := range tt.expectedParts {
				if parts[i] != expected {
					t.Errorf("part[%d]: expected %q, got %q", i, expected, parts[i])
				}
			}

			// Check hash inputs count
			if len(hashInputs) != tt.expectedHashCount {
				t.Errorf("expected %d hash inputs, got %d", tt.expectedHashCount, len(hashInputs))
			}
		})
	}
}

// ============================================================================
// BUILD DERIVED TEMPLATE TESTS
// ============================================================================

func TestBuildDerivedTemplate(t *testing.T) {
	latency := aimv1alpha1.AIMMetricLatency
	fp16 := aimv1alpha1.AIMPrecisionFP16

	tests := []struct {
		name              string
		service           *aimv1alpha1.AIMService
		templateName      string
		modelName         string
		baseSpec          *aimv1alpha1.AIMServiceTemplateSpec
		expectedModelName string
		expectedMetric    *aimv1alpha1.AIMMetric
		expectedPrecision *aimv1alpha1.AIMPrecision
		expectedGPU       *aimv1alpha1.AIMGpuRequirements
	}{
		{
			name: "basic derived template",
			service: NewService("svc").
				WithOverrideMetric(latency).
				WithOverridePrecision(fp16).
				Build(),
			templateName:      "derived-template",
			modelName:         "test-model",
			baseSpec:          nil,
			expectedModelName: "test-model",
			expectedMetric:    &latency,
			expectedPrecision: &fp16,
		},
		{
			name: "inherits base spec model name if set",
			service: NewService("svc").
				WithOverrideMetric(latency).
				Build(),
			templateName: "derived",
			modelName:    "resolved-model",
			baseSpec: &aimv1alpha1.AIMServiceTemplateSpec{
				AIMServiceTemplateSpecCommon: aimv1alpha1.AIMServiceTemplateSpecCommon{
					ModelName: "base-model",
				},
			},
			expectedModelName: "base-model", // Base spec takes precedence if already set
			expectedMetric:    &latency,
		},
		{
			name: "GPU selector override",
			service: NewService("svc").
				WithOverrideGPU("MI300X", 4).
				Build(),
			templateName:      "derived",
			modelName:         "model",
			baseSpec:          nil,
			expectedModelName: "model",
			expectedGPU:       &aimv1alpha1.AIMGpuRequirements{Model: "MI300X", Requests: 4},
		},
		{
			name: "caching mode always enables caching",
			service: NewService("svc").
				WithCachingMode(aimv1alpha1.CachingModeAlways).
				WithOverrideMetric(latency).
				Build(),
			templateName:      "derived",
			modelName:         "model",
			baseSpec:          nil,
			expectedModelName: "model",
			expectedMetric:    &latency,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildDerivedTemplate(tt.service, tt.templateName, tt.modelName, tt.baseSpec)

			// Check basic metadata
			if result.Name != tt.templateName {
				t.Errorf("expected name %s, got %s", tt.templateName, result.Name)
			}
			if result.Namespace != tt.service.Namespace {
				t.Errorf("expected namespace %s, got %s", tt.service.Namespace, result.Namespace)
			}

			// Check labels
			if result.Labels[constants.LabelKeyOrigin] != constants.LabelValueOriginDerived {
				t.Errorf("expected origin label %s, got %s", constants.LabelValueOriginDerived, result.Labels[constants.LabelKeyOrigin])
			}
			if result.Labels[constants.LabelK8sManagedBy] != constants.LabelValueManagedBy {
				t.Errorf("expected managed-by label %s, got %s", constants.LabelValueManagedBy, result.Labels[constants.LabelK8sManagedBy])
			}

			// Check no owner references (orphaned for reuse)
			if len(result.OwnerReferences) != 0 {
				t.Errorf("expected no owner references, got %d", len(result.OwnerReferences))
			}

			// Check spec values
			if result.Spec.ModelName != tt.expectedModelName {
				t.Errorf("expected model name %s, got %s", tt.expectedModelName, result.Spec.ModelName)
			}

			if tt.expectedMetric != nil {
				if result.Spec.Metric == nil || *result.Spec.Metric != *tt.expectedMetric {
					t.Errorf("expected metric %v, got %v", tt.expectedMetric, result.Spec.Metric)
				}
			}

			if tt.expectedPrecision != nil {
				if result.Spec.Precision == nil || *result.Spec.Precision != *tt.expectedPrecision {
					t.Errorf("expected precision %v, got %v", tt.expectedPrecision, result.Spec.Precision)
				}
			}

			if tt.expectedGPU != nil {
				if result.Spec.Hardware == nil || result.Spec.Hardware.GPU == nil {
					t.Error("expected GPU requirements, got nil")
				} else if result.Spec.Hardware.GPU.Requests != tt.expectedGPU.Requests {
					t.Errorf("expected GPU Requests %d, got %d", tt.expectedGPU.Requests, result.Spec.Hardware.GPU.Requests)
				} else if result.Spec.Hardware.GPU.Model != tt.expectedGPU.Model {
					t.Errorf("expected GPU Model %v, got %v", tt.expectedGPU.Model, result.Spec.Hardware.GPU.Model)
				}
			}
		})
	}
}

func TestBuildDerivedTemplate_InheritsBaseSpec(t *testing.T) {
	latency := aimv1alpha1.AIMMetricLatency

	service := NewService("svc").
		WithOverrideMetric(latency).
		Build()

	baseSpec := &aimv1alpha1.AIMServiceTemplateSpec{
		AIMServiceTemplateSpecCommon: aimv1alpha1.AIMServiceTemplateSpecCommon{
			ModelName: "",
			ImagePullSecrets: []corev1.LocalObjectReference{
				{Name: "base-secret"},
			},
			Resources: &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("16Gi"),
				},
			},
		},
		Env: []corev1.EnvVar{
			{Name: "BASE_VAR", Value: "base-value"},
		},
	}

	result := buildDerivedTemplate(service, "derived", "model", baseSpec)

	// Should inherit env vars
	if len(result.Spec.Env) != 1 || result.Spec.Env[0].Name != "BASE_VAR" {
		t.Errorf("expected inherited env vars, got %v", result.Spec.Env)
	}

	// Should inherit image pull secrets
	if len(result.Spec.ImagePullSecrets) != 1 || result.Spec.ImagePullSecrets[0].Name != "base-secret" {
		t.Errorf("expected inherited image pull secrets, got %v", result.Spec.ImagePullSecrets)
	}

	// Should inherit resources
	if result.Spec.Resources == nil {
		t.Error("expected inherited resources, got nil")
	}
}

func TestBuildDerivedTemplate_ServiceOverridesBaseSpec(t *testing.T) {
	latency := aimv1alpha1.AIMMetricLatency

	service := NewService("svc").
		WithOverrideMetric(latency).
		Build()
	service.Spec.ImagePullSecrets = []corev1.LocalObjectReference{
		{Name: "service-secret"},
	}
	service.Spec.Resources = &corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse("32Gi"),
		},
	}

	baseSpec := &aimv1alpha1.AIMServiceTemplateSpec{
		AIMServiceTemplateSpecCommon: aimv1alpha1.AIMServiceTemplateSpecCommon{
			ImagePullSecrets: []corev1.LocalObjectReference{
				{Name: "base-secret"},
			},
			Resources: &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("16Gi"),
				},
			},
		},
	}

	result := buildDerivedTemplate(service, "derived", "model", baseSpec)

	// Service values should override base
	if len(result.Spec.ImagePullSecrets) != 1 || result.Spec.ImagePullSecrets[0].Name != "service-secret" {
		t.Errorf("expected service image pull secrets to override, got %v", result.Spec.ImagePullSecrets)
	}

	if result.Spec.Resources == nil {
		t.Fatal("expected resources, got nil")
	}
	memory := result.Spec.Resources.Requests[corev1.ResourceMemory]
	if memory.String() != "32Gi" {
		t.Errorf("expected service resources to override, got %s", memory.String())
	}
}

// ============================================================================
// NORMALIZE RUNTIME CONFIG NAME TESTS
// ============================================================================

func TestNormalizeRuntimeConfigName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty string returns default",
			input:    "",
			expected: constants.DefaultRuntimeConfigName,
		},
		{
			name:     "whitespace only returns default",
			input:    "   ",
			expected: constants.DefaultRuntimeConfigName,
		},
		{
			name:     "valid name returned as-is",
			input:    "my-config",
			expected: "my-config",
		},
		{
			name:     "name with whitespace trimmed",
			input:    "  my-config  ",
			expected: "my-config",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeRuntimeConfigName(tt.input)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}
