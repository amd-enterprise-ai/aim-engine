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

package aimservicetemplate

import (
	"strings"
	"testing"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
)

// ============================================================================
// TEMPLATE NAME GENERATION TESTS
// ============================================================================

func TestGenerateTemplateName(t *testing.T) {
	tests := []struct {
		name             string
		imageName        string
		deployment       aimv1alpha1.RecommendedDeployment
		expectContains   []string
		expectNotContain []string
	}{
		{
			name:      "full deployment specification",
			imageName: "llama-3-1-70b-instruct",
			deployment: aimv1alpha1.RecommendedDeployment{
				GPUModel:  "mi300x",
				GPUCount:  2,
				Metric:    "latency",
				Precision: "fp8",
			},
			expectContains: []string{"2x-mi300x", "lat", "fp8"},
		},
		{
			name:      "throughput metric gets shortened",
			imageName: "test-model",
			deployment: aimv1alpha1.RecommendedDeployment{
				Metric: "throughput",
			},
			expectContains: []string{"thr"},
		},
		{
			name:      "GPU model only",
			imageName: "test-model",
			deployment: aimv1alpha1.RecommendedDeployment{
				GPUModel: "mi300x",
			},
			expectContains: []string{"mi300x"},
		},
		{
			name:      "GPU count only",
			imageName: "test-model",
			deployment: aimv1alpha1.RecommendedDeployment{
				GPUCount: 4,
			},
			expectContains: []string{"x4"},
		},
		{
			name:      "precision only",
			imageName: "test-model",
			deployment: aimv1alpha1.RecommendedDeployment{
				Precision: "fp16",
			},
			expectContains: []string{"fp16"},
		},
		{
			name:           "empty deployment",
			imageName:      "test-model",
			deployment:     aimv1alpha1.RecommendedDeployment{},
			expectContains: []string{"test-model"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GenerateTemplateName(tt.imageName, tt.deployment)

			// Check length constraint (Kubernetes max = 63)
			if len(result) > 63 {
				t.Errorf("generated name exceeds 63 characters: %d", len(result))
			}

			// Check expected substrings
			for _, expected := range tt.expectContains {
				if !strings.Contains(result, expected) {
					t.Errorf("expected name to contain %q, got: %s", expected, result)
				}
			}

			// Check unexpected substrings
			for _, unexpected := range tt.expectNotContain {
				if strings.Contains(result, unexpected) {
					t.Errorf("expected name NOT to contain %q, got: %s", unexpected, result)
				}
			}
		})
	}
}

func TestGenerateTemplateName_Deterministic(t *testing.T) {
	imageName := "llama-3-1-70b"
	deployment := aimv1alpha1.RecommendedDeployment{
		GPUModel:  "mi300x",
		GPUCount:  2,
		Metric:    "latency",
		Precision: "fp8",
	}

	// Generate twice
	name1 := GenerateTemplateName(imageName, deployment)
	name2 := GenerateTemplateName(imageName, deployment)

	if name1 != name2 {
		t.Errorf("GenerateTemplateName not deterministic: %s != %s", name1, name2)
	}
}

func TestGenerateTemplateName_UniqueForDifferentInputs(t *testing.T) {
	imageName := "test-model"

	// Two deployments that differ only in precision
	deployment1 := aimv1alpha1.RecommendedDeployment{
		GPUModel:  "mi300x",
		GPUCount:  1,
		Precision: "fp16",
	}
	deployment2 := aimv1alpha1.RecommendedDeployment{
		GPUModel:  "mi300x",
		GPUCount:  1,
		Precision: "fp8",
	}

	name1 := GenerateTemplateName(imageName, deployment1)
	name2 := GenerateTemplateName(imageName, deployment2)

	if name1 == name2 {
		t.Errorf("expected different names for different deployments, got: %s", name1)
	}
}

func TestGenerateTemplateName_LongImageName(t *testing.T) {
	// Very long image name that will require truncation
	longImageName := "very-long-model-name-that-exceeds-kubernetes-limits-by-quite-a-lot"
	deployment := aimv1alpha1.RecommendedDeployment{
		GPUModel:  "mi300x",
		GPUCount:  2,
		Metric:    "latency",
		Precision: "fp8",
	}

	result := GenerateTemplateName(longImageName, deployment)

	// Should still be within limits
	if len(result) > 63 {
		t.Errorf("long image name not properly truncated: %d chars", len(result))
	}

	// Should still contain deployment params
	if !strings.Contains(result, "2x-mi300x") {
		t.Error("expected deployment params to be preserved even with long image name")
	}
}

// ============================================================================
// METRIC SHORTHAND TESTS
// ============================================================================

func TestGetMetricShorthand(t *testing.T) {
	tests := []struct {
		metric   string
		expected string
	}{
		{
			metric:   "latency",
			expected: "lat",
		},
		{
			metric:   "throughput",
			expected: "thr",
		},
		{
			metric:   "unknown-metric",
			expected: "unknown-metric", // Returns original if no mapping
		},
		{
			metric:   "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.metric, func(t *testing.T) {
			result := getMetricShorthand(tt.metric)
			if result != tt.expected {
				t.Errorf("getMetricShorthand(%q) = %q, want %q", tt.metric, result, tt.expected)
			}
		})
	}
}

// ============================================================================
// TEMPLATE REQUIRES GPU TESTS
// ============================================================================

func TestTemplateRequiresGPU(t *testing.T) {
	tests := []struct {
		name     string
		spec     aimv1alpha1.AIMServiceTemplateSpecCommon
		expected bool
	}{
		{
			name: "GPU selector with model",
			spec: aimv1alpha1.AIMServiceTemplateSpecCommon{
				ModelName: "test-model",
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					GpuSelector: &aimv1alpha1.AIMGpuSelector{
						Model: "mi300x",
						Count: 2,
					},
				},
			},
			expected: true,
		},
		{
			name: "GPU selector with model and whitespace",
			spec: aimv1alpha1.AIMServiceTemplateSpecCommon{
				ModelName: "test-model",
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					GpuSelector: &aimv1alpha1.AIMGpuSelector{
						Model: "  mi300x  ",
						Count: 1,
					},
				},
			},
			expected: true,
		},
		{
			name: "GPU selector with empty model",
			spec: aimv1alpha1.AIMServiceTemplateSpecCommon{
				ModelName: "test-model",
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					GpuSelector: &aimv1alpha1.AIMGpuSelector{
						Model: "",
						Count: 2,
					},
				},
			},
			expected: false,
		},
		{
			name: "GPU selector with whitespace-only model",
			spec: aimv1alpha1.AIMServiceTemplateSpecCommon{
				ModelName: "test-model",
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					GpuSelector: &aimv1alpha1.AIMGpuSelector{
						Model: "   ",
						Count: 2,
					},
				},
			},
			expected: false,
		},
		{
			name: "no GPU selector",
			spec: aimv1alpha1.AIMServiceTemplateSpecCommon{
				ModelName: "test-model",
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					GpuSelector: nil,
				},
			},
			expected: false,
		},
		{
			name: "empty spec",
			spec: aimv1alpha1.AIMServiceTemplateSpecCommon{
				ModelName: "test-model",
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := TemplateRequiresGPU(tt.spec)
			if result != tt.expected {
				t.Errorf("TemplateRequiresGPU() = %v, want %v", result, tt.expected)
			}
		})
	}
}
