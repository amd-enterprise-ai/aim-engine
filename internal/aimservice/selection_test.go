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
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

// ============================================================================
// STAGE 1: AVAILABILITY FILTER TESTS
// ============================================================================

func TestFilterByAvailability(t *testing.T) {
	tests := []struct {
		name             string
		candidates       []TemplateCandidate
		expectedCount    int
		expectedRejected int
	}{
		{
			name:             "empty candidates",
			candidates:       []TemplateCandidate{},
			expectedCount:    0,
			expectedRejected: 0,
		},
		{
			name: "all ready",
			candidates: []TemplateCandidate{
				NewCandidate("t1").WithStatus(constants.AIMStatusReady).Build(),
				NewCandidate("t2").WithStatus(constants.AIMStatusReady).Build(),
			},
			expectedCount:    2,
			expectedRejected: 0,
		},
		{
			name: "all not ready",
			candidates: []TemplateCandidate{
				NewCandidate("t1").WithStatus(constants.AIMStatusPending).Build(),
				NewCandidate("t2").WithStatus(constants.AIMStatusProgressing).Build(),
				NewCandidate("t3").WithStatus(constants.AIMStatusFailed).Build(),
			},
			expectedCount:    0,
			expectedRejected: 3,
		},
		{
			name: "mixed statuses",
			candidates: []TemplateCandidate{
				NewCandidate("ready1").WithStatus(constants.AIMStatusReady).Build(),
				NewCandidate("pending").WithStatus(constants.AIMStatusPending).Build(),
				NewCandidate("ready2").WithStatus(constants.AIMStatusReady).Build(),
				NewCandidate("failed").WithStatus(constants.AIMStatusFailed).Build(),
			},
			expectedCount:    2,
			expectedRejected: 2,
		},
		{
			name: "NotAvailable status rejected",
			candidates: []TemplateCandidate{
				NewCandidate("ready").WithStatus(constants.AIMStatusReady).Build(),
				NewCandidate("notavail").WithStatus(constants.AIMStatusNotAvailable).Build(),
			},
			expectedCount:    1,
			expectedRejected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rejected := make(map[string][]TemplateCandidate)
			result := filterByAvailability(tt.candidates, rejected)

			if len(result) != tt.expectedCount {
				t.Errorf("expected %d candidates, got %d", tt.expectedCount, len(result))
			}
			if len(rejected[stageAvailability]) != tt.expectedRejected {
				t.Errorf("expected %d rejected, got %d", tt.expectedRejected, len(rejected[stageAvailability]))
			}
		})
	}
}

// ============================================================================
// STAGE 2: OPTIMIZATION STATUS FILTER TESTS
// ============================================================================

func TestFilterByOptimizationStatus(t *testing.T) {
	tests := []struct {
		name             string
		candidates       []TemplateCandidate
		allowUnoptimized bool
		expectedCount    int
		expectedRejected int
	}{
		{
			name: "all optimized - no unoptimized allowed",
			candidates: []TemplateCandidate{
				NewCandidate("t1").WithProfileType(aimv1alpha1.AIMProfileTypeOptimized).Build(),
				NewCandidate("t2").WithProfileType(aimv1alpha1.AIMProfileTypeOptimized).Build(),
			},
			allowUnoptimized: false,
			expectedCount:    2,
			expectedRejected: 0,
		},
		{
			name: "mixed - unoptimized rejected when not allowed",
			candidates: []TemplateCandidate{
				NewCandidate("optimized").WithProfileType(aimv1alpha1.AIMProfileTypeOptimized).Build(),
				NewCandidate("unoptimized").WithProfileType(aimv1alpha1.AIMProfileTypeUnoptimized).Build(),
				NewCandidate("preview").WithProfileType(aimv1alpha1.AIMProfileTypePreview).Build(),
			},
			allowUnoptimized: false,
			expectedCount:    1,
			expectedRejected: 2,
		},
		{
			name: "mixed - all allowed when allowUnoptimized=true",
			candidates: []TemplateCandidate{
				NewCandidate("optimized").WithProfileType(aimv1alpha1.AIMProfileTypeOptimized).Build(),
				NewCandidate("unoptimized").WithProfileType(aimv1alpha1.AIMProfileTypeUnoptimized).Build(),
				NewCandidate("preview").WithProfileType(aimv1alpha1.AIMProfileTypePreview).Build(),
			},
			allowUnoptimized: true,
			expectedCount:    3,
			expectedRejected: 0,
		},
		{
			name: "only unoptimized - all rejected when not allowed",
			candidates: []TemplateCandidate{
				NewCandidate("t1").WithProfileType(aimv1alpha1.AIMProfileTypeUnoptimized).Build(),
				NewCandidate("t2").WithProfileType(aimv1alpha1.AIMProfileTypePreview).Build(),
			},
			allowUnoptimized: false,
			expectedCount:    0,
			expectedRejected: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rejected := make(map[string][]TemplateCandidate)
			result := filterByOptimizationStatus(tt.candidates, tt.allowUnoptimized, rejected)

			if len(result) != tt.expectedCount {
				t.Errorf("expected %d candidates, got %d", tt.expectedCount, len(result))
			}
			if len(rejected[stageUnoptimized]) != tt.expectedRejected {
				t.Errorf("expected %d rejected, got %d", tt.expectedRejected, len(rejected[stageUnoptimized]))
			}
		})
	}
}

// ============================================================================
// STAGE 3: OVERRIDES FILTER TESTS
// ============================================================================

func TestFilterTemplatesByOverrides(t *testing.T) {
	latency := aimv1alpha1.AIMMetricLatency
	throughput := aimv1alpha1.AIMMetricThroughput
	fp16 := aimv1alpha1.AIMPrecisionFP16
	fp8 := aimv1alpha1.AIMPrecisionFP8

	tests := []struct {
		name          string
		candidates    []TemplateCandidate
		overrides     *aimv1alpha1.AIMServiceOverrides
		expectedNames []string
	}{
		{
			name: "nil overrides - all pass",
			candidates: []TemplateCandidate{
				NewCandidate("t1").WithMetric(latency).Build(),
				NewCandidate("t2").WithMetric(throughput).Build(),
			},
			overrides:     nil,
			expectedNames: []string{"t1", "t2"},
		},
		{
			name: "filter by metric",
			candidates: []TemplateCandidate{
				NewCandidate("latency").WithMetric(latency).Build(),
				NewCandidate("throughput").WithMetric(throughput).Build(),
			},
			overrides:     &aimv1alpha1.AIMServiceOverrides{AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{Metric: &latency}},
			expectedNames: []string{"latency"},
		},
		{
			name: "filter by precision",
			candidates: []TemplateCandidate{
				NewCandidate("fp16").WithPrecision(fp16).Build(),
				NewCandidate("fp8").WithPrecision(fp8).Build(),
			},
			overrides:     &aimv1alpha1.AIMServiceOverrides{AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{Precision: &fp16}},
			expectedNames: []string{"fp16"},
		},
		{
			name: "filter by GPU model",
			candidates: []TemplateCandidate{
				NewCandidate("mi300x").WithGPU("MI300X", 4).Build(),
				NewCandidate("mi325x").WithGPU("MI325X", 8).Build(),
			},
			overrides: &aimv1alpha1.AIMServiceOverrides{
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					GpuSelector: &aimv1alpha1.AIMGpuSelector{Model: "MI300X"}, // Only model, no count
				},
			},
			expectedNames: []string{"mi300x"},
		},
		{
			name: "filter by GPU count",
			candidates: []TemplateCandidate{
				NewCandidate("4gpu").WithGPU("MI300X", 4).Build(),
				NewCandidate("8gpu").WithGPU("MI300X", 8).Build(),
			},
			overrides: &aimv1alpha1.AIMServiceOverrides{
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					GpuSelector: &aimv1alpha1.AIMGpuSelector{Count: 4},
				},
			},
			expectedNames: []string{"4gpu"},
		},
		{
			name: "filter by multiple overrides",
			candidates: []TemplateCandidate{
				NewCandidate("match").WithGPU("MI300X", 4).WithPrecision(fp16).WithMetric(latency).Build(),
				NewCandidate("wrong-gpu").WithGPU("MI325X", 4).WithPrecision(fp16).WithMetric(latency).Build(),
				NewCandidate("wrong-precision").WithGPU("MI300X", 4).WithPrecision(fp8).WithMetric(latency).Build(),
			},
			overrides: &aimv1alpha1.AIMServiceOverrides{
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					GpuSelector: &aimv1alpha1.AIMGpuSelector{Model: "MI300X", Count: 4},
					Precision:   &fp16,
					Metric:      &latency,
				},
			},
			expectedNames: []string{"match"},
		},
		{
			name: "no matches",
			candidates: []TemplateCandidate{
				NewCandidate("t1").WithMetric(throughput).Build(),
				NewCandidate("t2").WithMetric(throughput).Build(),
			},
			overrides:     &aimv1alpha1.AIMServiceOverrides{AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{Metric: &latency}},
			expectedNames: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterTemplatesByOverrides(tt.candidates, tt.overrides)

			if len(result) != len(tt.expectedNames) {
				t.Errorf("expected %d candidates, got %d", len(tt.expectedNames), len(result))
				return
			}

			for i, expected := range tt.expectedNames {
				if result[i].Name != expected {
					t.Errorf("expected candidate[%d].Name=%s, got %s", i, expected, result[i].Name)
				}
			}
		})
	}
}

// ============================================================================
// STAGE 4: GPU AVAILABILITY FILTER TESTS
// ============================================================================

func TestFilterTemplatesByGPUAvailability(t *testing.T) {
	tests := []struct {
		name          string
		candidates    []TemplateCandidate
		availableGPUs []string
		expectedNames []string
	}{
		{
			name: "no GPUs available - all rejected",
			candidates: []TemplateCandidate{
				NewCandidate("mi300x").WithGPU("MI300X", 4).Build(),
				NewCandidate("mi325x").WithGPU("MI325X", 8).Build(),
			},
			availableGPUs: []string{},
			expectedNames: []string{},
		},
		{
			name: "all GPUs available",
			candidates: []TemplateCandidate{
				NewCandidate("mi300x").WithGPU("MI300X", 4).Build(),
				NewCandidate("mi325x").WithGPU("MI325X", 8).Build(),
			},
			availableGPUs: []string{"MI300X", "MI325X"},
			expectedNames: []string{"mi300x", "mi325x"},
		},
		{
			name: "partial GPU availability",
			candidates: []TemplateCandidate{
				NewCandidate("mi300x").WithGPU("MI300X", 4).Build(),
				NewCandidate("mi325x").WithGPU("MI325X", 8).Build(),
				NewCandidate("a100").WithGPU("A100", 4).Build(),
			},
			availableGPUs: []string{"MI300X", "A100"},
			expectedNames: []string{"mi300x", "a100"},
		},
		{
			name: "candidate without GPU spec - passes",
			candidates: []TemplateCandidate{
				NewCandidate("no-gpu").Build(), // No GPU specified
				NewCandidate("mi300x").WithGPU("MI300X", 4).Build(),
			},
			availableGPUs: []string{},
			expectedNames: []string{"no-gpu"},
		},
		{
			name: "case-insensitive GPU matching",
			candidates: []TemplateCandidate{
				NewCandidate("mi300x-lower").WithGPU("mi300x", 4).Build(),
				NewCandidate("MI300X-upper").WithGPU("MI300X", 4).Build(),
			},
			availableGPUs: []string{"MI300X"},
			expectedNames: []string{"mi300x-lower", "MI300X-upper"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterTemplatesByGPUAvailability(tt.candidates, tt.availableGPUs)

			if len(result) != len(tt.expectedNames) {
				t.Errorf("expected %d candidates, got %d", len(tt.expectedNames), len(result))
				return
			}

			for i, expected := range tt.expectedNames {
				if result[i].Name != expected {
					t.Errorf("expected candidate[%d].Name=%s, got %s", i, expected, result[i].Name)
				}
			}
		})
	}
}

// ============================================================================
// STAGE 5: SCOPE PREFERENCE TESTS
// ============================================================================

func TestPreferNamespaceTemplates(t *testing.T) {
	tests := []struct {
		name          string
		candidates    []TemplateCandidate
		expectedNames []string
	}{
		{
			name: "all namespace-scoped",
			candidates: []TemplateCandidate{
				NewCandidate("ns1").WithScope(aimv1alpha1.AIMResolutionScopeNamespace).Build(),
				NewCandidate("ns2").WithScope(aimv1alpha1.AIMResolutionScopeNamespace).Build(),
			},
			expectedNames: []string{"ns1", "ns2"},
		},
		{
			name: "all cluster-scoped",
			candidates: []TemplateCandidate{
				NewCandidate("cl1").WithScope(aimv1alpha1.AIMResolutionScopeCluster).Build(),
				NewCandidate("cl2").WithScope(aimv1alpha1.AIMResolutionScopeCluster).Build(),
			},
			expectedNames: []string{"cl1", "cl2"}, // No namespace templates, so cluster ones pass
		},
		{
			name: "mixed - namespace preferred",
			candidates: []TemplateCandidate{
				NewCandidate("cluster").WithScope(aimv1alpha1.AIMResolutionScopeCluster).Build(),
				NewCandidate("namespace").WithScope(aimv1alpha1.AIMResolutionScopeNamespace).Build(),
			},
			expectedNames: []string{"namespace"}, // Only namespace template returned
		},
		{
			name: "multiple mixed - all namespace returned",
			candidates: []TemplateCandidate{
				NewCandidate("cl1").WithScope(aimv1alpha1.AIMResolutionScopeCluster).Build(),
				NewCandidate("ns1").WithScope(aimv1alpha1.AIMResolutionScopeNamespace).Build(),
				NewCandidate("cl2").WithScope(aimv1alpha1.AIMResolutionScopeCluster).Build(),
				NewCandidate("ns2").WithScope(aimv1alpha1.AIMResolutionScopeNamespace).Build(),
			},
			expectedNames: []string{"ns1", "ns2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := preferNamespaceTemplates(tt.candidates)

			if len(result) != len(tt.expectedNames) {
				t.Errorf("expected %d candidates, got %d", len(tt.expectedNames), len(result))
				return
			}

			// Check names (order may vary for namespace templates)
			resultNames := make(map[string]bool)
			for _, c := range result {
				resultNames[c.Name] = true
			}
			for _, expected := range tt.expectedNames {
				if !resultNames[expected] {
					t.Errorf("expected candidate %s not found in result", expected)
				}
			}
		})
	}
}

// ============================================================================
// STAGE 6: PREFERENCE SCORING TESTS
// ============================================================================

func TestChoosePreferredTemplate(t *testing.T) {
	latency := aimv1alpha1.AIMMetricLatency
	throughput := aimv1alpha1.AIMMetricThroughput
	fp16 := aimv1alpha1.AIMPrecisionFP16
	fp8 := aimv1alpha1.AIMPrecisionFP8
	bf16 := aimv1alpha1.AIMPrecisionBF16

	tests := []struct {
		name          string
		candidates    []TemplateCandidate
		expectedName  string
		expectedCount int // Number of candidates with identical best scores
	}{
		{
			name:          "empty candidates",
			candidates:    []TemplateCandidate{},
			expectedName:  "",
			expectedCount: 0,
		},
		{
			name: "single candidate",
			candidates: []TemplateCandidate{
				NewCandidate("only").Build(),
			},
			expectedName:  "only",
			expectedCount: 1,
		},
		{
			name: "prefer optimized over unoptimized",
			candidates: []TemplateCandidate{
				NewCandidate("unoptimized").WithProfileType(aimv1alpha1.AIMProfileTypeUnoptimized).Build(),
				NewCandidate("optimized").WithProfileType(aimv1alpha1.AIMProfileTypeOptimized).Build(),
			},
			expectedName:  "optimized",
			expectedCount: 1,
		},
		{
			name: "prefer MI325X over MI300X (GPU tier)",
			candidates: []TemplateCandidate{
				NewCandidate("mi300x").WithProfileType(aimv1alpha1.AIMProfileTypeOptimized).WithGPU("MI300X", 4).Build(),
				NewCandidate("mi325x").WithProfileType(aimv1alpha1.AIMProfileTypeOptimized).WithGPU("MI325X", 4).Build(),
			},
			expectedName:  "mi325x",
			expectedCount: 1,
		},
		{
			name: "prefer latency over throughput (metric)",
			candidates: []TemplateCandidate{
				NewCandidate("throughput").WithProfileType(aimv1alpha1.AIMProfileTypeOptimized).WithGPU("MI300X", 4).WithMetric(throughput).Build(),
				NewCandidate("latency").WithProfileType(aimv1alpha1.AIMProfileTypeOptimized).WithGPU("MI300X", 4).WithMetric(latency).Build(),
			},
			expectedName:  "latency",
			expectedCount: 1,
		},
		{
			name: "prefer fp8 over fp16 (precision)",
			candidates: []TemplateCandidate{
				NewCandidate("fp16").WithProfileType(aimv1alpha1.AIMProfileTypeOptimized).WithGPU("MI300X", 4).WithMetric(latency).WithPrecision(fp16).Build(),
				NewCandidate("fp8").WithProfileType(aimv1alpha1.AIMProfileTypeOptimized).WithGPU("MI300X", 4).WithMetric(latency).WithPrecision(fp8).Build(),
			},
			expectedName:  "fp8",
			expectedCount: 1,
		},
		{
			name: "profile type beats GPU tier",
			candidates: []TemplateCandidate{
				NewCandidate("unoptimized-mi325x").WithProfileType(aimv1alpha1.AIMProfileTypeUnoptimized).WithGPU("MI325X", 8).Build(),
				NewCandidate("optimized-mi300x").WithProfileType(aimv1alpha1.AIMProfileTypeOptimized).WithGPU("MI300X", 4).Build(),
			},
			expectedName:  "optimized-mi300x",
			expectedCount: 1,
		},
		{
			name: "GPU tier beats metric",
			candidates: []TemplateCandidate{
				NewCandidate("mi300x-latency").WithProfileType(aimv1alpha1.AIMProfileTypeOptimized).WithGPU("MI300X", 4).WithMetric(latency).Build(),
				NewCandidate("mi325x-throughput").WithProfileType(aimv1alpha1.AIMProfileTypeOptimized).WithGPU("MI325X", 4).WithMetric(throughput).Build(),
			},
			expectedName:  "mi325x-throughput",
			expectedCount: 1,
		},
		{
			name: "identical scores - count > 1",
			candidates: []TemplateCandidate{
				NewCandidate("t1").WithProfileType(aimv1alpha1.AIMProfileTypeOptimized).WithGPU("MI300X", 4).WithMetric(latency).WithPrecision(fp16).Build(),
				NewCandidate("t2").WithProfileType(aimv1alpha1.AIMProfileTypeOptimized).WithGPU("MI300X", 4).WithMetric(latency).WithPrecision(fp16).Build(),
			},
			expectedName:  "t1", // First one wins when identical
			expectedCount: 2,
		},
		{
			name: "complex scenario with multiple factors",
			candidates: []TemplateCandidate{
				NewCandidate("worst").WithProfileType(aimv1alpha1.AIMProfileTypeUnoptimized).WithGPU("A100", 4).WithMetric(throughput).WithPrecision(bf16).Build(),
				NewCandidate("mid").WithProfileType(aimv1alpha1.AIMProfileTypeOptimized).WithGPU("MI300X", 4).WithMetric(throughput).WithPrecision(fp16).Build(),
				NewCandidate("best").WithProfileType(aimv1alpha1.AIMProfileTypeOptimized).WithGPU("MI325X", 8).WithMetric(latency).WithPrecision(fp8).Build(),
			},
			expectedName:  "best",
			expectedCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selected, count := choosePreferredTemplate(tt.candidates)

			if tt.expectedName == "" {
				if selected != nil {
					t.Errorf("expected nil selection, got %s", selected.Name)
				}
			} else {
				if selected == nil {
					t.Errorf("expected selection %s, got nil", tt.expectedName)
					return
				}
				if selected.Name != tt.expectedName {
					t.Errorf("expected selection %s, got %s", tt.expectedName, selected.Name)
				}
			}

			if count != tt.expectedCount {
				t.Errorf("expected count %d, got %d", tt.expectedCount, count)
			}
		})
	}
}

// ============================================================================
// FULL SELECTION ALGORITHM TESTS
// ============================================================================

func TestSelectBestTemplate(t *testing.T) {
	latency := aimv1alpha1.AIMMetricLatency
	fp16 := aimv1alpha1.AIMPrecisionFP16

	tests := []struct {
		name             string
		candidates       []TemplateCandidate
		overrides        *aimv1alpha1.AIMServiceOverrides
		availableGPUs    []string
		allowUnoptimized bool
		expectedName     string
		expectedCount    int
	}{
		{
			name:          "empty candidates",
			candidates:    []TemplateCandidate{},
			availableGPUs: []string{"MI300X"},
			expectedName:  "",
			expectedCount: 0,
		},
		{
			name: "single matching candidate",
			candidates: []TemplateCandidate{
				NewCandidate("only").WithGPU("MI300X", 4).Build(),
			},
			availableGPUs: []string{"MI300X"},
			expectedName:  "only",
			expectedCount: 1,
		},
		{
			name: "filter by availability first",
			candidates: []TemplateCandidate{
				NewCandidate("not-ready").WithStatus(constants.AIMStatusPending).WithGPU("MI325X", 8).Build(),
				NewCandidate("ready").WithStatus(constants.AIMStatusReady).WithGPU("MI300X", 4).Build(),
			},
			availableGPUs: []string{"MI300X", "MI325X"},
			expectedName:  "ready",
			expectedCount: 1,
		},
		{
			name: "filter by optimization status",
			candidates: []TemplateCandidate{
				NewCandidate("unoptimized").WithProfileType(aimv1alpha1.AIMProfileTypeUnoptimized).WithGPU("MI325X", 8).Build(),
				NewCandidate("optimized").WithProfileType(aimv1alpha1.AIMProfileTypeOptimized).WithGPU("MI300X", 4).Build(),
			},
			availableGPUs:    []string{"MI300X", "MI325X"},
			allowUnoptimized: false,
			expectedName:     "optimized",
			expectedCount:    1,
		},
		{
			name: "filter by overrides",
			candidates: []TemplateCandidate{
				NewCandidate("wrong-metric").WithGPU("MI300X", 4).WithMetric(aimv1alpha1.AIMMetricThroughput).Build(),
				NewCandidate("right-metric").WithGPU("MI300X", 4).WithMetric(latency).Build(),
			},
			overrides:     &aimv1alpha1.AIMServiceOverrides{AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{Metric: &latency}},
			availableGPUs: []string{"MI300X"},
			expectedName:  "right-metric",
			expectedCount: 1,
		},
		{
			name: "filter by GPU availability",
			candidates: []TemplateCandidate{
				NewCandidate("unavailable-gpu").WithGPU("MI325X", 8).Build(),
				NewCandidate("available-gpu").WithGPU("MI300X", 4).Build(),
			},
			availableGPUs: []string{"MI300X"}, // MI325X not available
			expectedName:  "available-gpu",
			expectedCount: 1,
		},
		{
			name: "prefer namespace over cluster",
			candidates: []TemplateCandidate{
				NewCandidate("cluster").WithScope(aimv1alpha1.AIMResolutionScopeCluster).WithGPU("MI325X", 8).Build(),
				NewCandidate("namespace").WithScope(aimv1alpha1.AIMResolutionScopeNamespace).WithGPU("MI300X", 4).Build(),
			},
			availableGPUs: []string{"MI300X", "MI325X"},
			expectedName:  "namespace",
			expectedCount: 1,
		},
		{
			name: "all filters eliminate candidates",
			candidates: []TemplateCandidate{
				NewCandidate("not-ready").WithStatus(constants.AIMStatusFailed).Build(),
				NewCandidate("wrong-gpu").WithGPU("MI325X", 8).Build(),
			},
			availableGPUs: []string{"MI300X"}, // MI325X not available
			expectedName:  "",
			expectedCount: 0,
		},
		{
			name: "full pipeline with scoring",
			candidates: []TemplateCandidate{
				NewCandidate("good").WithGPU("MI300X", 4).WithMetric(latency).WithPrecision(fp16).Build(),
				NewCandidate("better").WithGPU("MI325X", 4).WithMetric(latency).WithPrecision(fp16).Build(),
			},
			availableGPUs: []string{"MI300X", "MI325X"},
			expectedName:  "better", // MI325X preferred
			expectedCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selected, count, _, _ := selectBestTemplate(
				tt.candidates,
				tt.overrides,
				tt.availableGPUs,
				tt.allowUnoptimized,
			)

			if tt.expectedName == "" {
				if selected != nil {
					t.Errorf("expected nil selection, got %s", selected.Name)
				}
			} else {
				if selected == nil {
					t.Errorf("expected selection %s, got nil", tt.expectedName)
					return
				}
				if selected.Name != tt.expectedName {
					t.Errorf("expected selection %s, got %s", tt.expectedName, selected.Name)
				}
			}

			if count != tt.expectedCount {
				t.Errorf("expected count %d, got %d", tt.expectedCount, count)
			}
		})
	}
}

// ============================================================================
// INTEGRATION TESTS WITH FAKE CLIENT
// ============================================================================

func TestSelectTemplateForModel_Integration(t *testing.T) {
	ctx := testContext()

	tests := []struct {
		name                    string
		service                 *aimv1alpha1.AIMService
		templates               []aimv1alpha1.AIMServiceTemplate
		nodes                   []corev1.Node
		expectedName            string
		expectedReason          string
		expectTemplatesNotReady bool
		expectError             bool
	}{
		{
			name:      "no templates exist",
			service:   NewService("svc").WithModelName(testModelName).Build(),
			templates: []aimv1alpha1.AIMServiceTemplate{
				// Empty
			},
			expectedReason: aimv1alpha1.AIMServiceReasonTemplateNotFound,
		},
		{
			name:    "single matching template",
			service: NewService("svc").WithModelName(testModelName).Build(),
			templates: []aimv1alpha1.AIMServiceTemplate{
				*NewTemplate("t1").WithModelName(testModelName).WithGPU("MI300X", 4).Build(),
			},
			nodes: []corev1.Node{
				*NewNode("gpu-node").WithGPUProductID("0x74a1").Build(), // MI300X
			},
			expectedName: "t1",
		},
		{
			name:    "templates exist but not ready",
			service: NewService("svc").WithModelName(testModelName).Build(),
			templates: []aimv1alpha1.AIMServiceTemplate{
				*NewTemplate("t1").WithModelName(testModelName).WithStatus(constants.AIMStatusPending).Build(),
			},
			expectTemplatesNotReady: true, // No reason set when templates exist but aren't ready
		},
		{
			name:    "templates for different model",
			service: NewService("svc").WithModelName(testModelName).Build(),
			templates: []aimv1alpha1.AIMServiceTemplate{
				*NewTemplate("t1").WithModelName("other-model").Build(),
			},
			expectedReason: aimv1alpha1.AIMServiceReasonTemplateNotFound,
		},
		{
			name:    "unoptimized templates filtered by default",
			service: NewService("svc").WithModelName(testModelName).Build(),
			templates: []aimv1alpha1.AIMServiceTemplate{
				*NewTemplate("t1").WithModelName(testModelName).WithProfileType(aimv1alpha1.AIMProfileTypeUnoptimized).Build(),
			},
			expectedReason: aimv1alpha1.AIMServiceReasonTemplateNotFound,
		},
		{
			name:    "unoptimized allowed when specified",
			service: NewService("svc").WithModelName(testModelName).WithAllowUnoptimized(true).Build(),
			templates: []aimv1alpha1.AIMServiceTemplate{
				*NewTemplate("t1").WithModelName(testModelName).WithProfileType(aimv1alpha1.AIMProfileTypeUnoptimized).WithGPU("MI300X", 4).Build(),
			},
			nodes: []corev1.Node{
				*NewNode("gpu-node").WithGPUProductID("0x74a1").Build(),
			},
			expectedName: "t1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Build objects for fake client
			var objs []client.Object
			for i := range tt.templates {
				objs = append(objs, &tt.templates[i])
			}
			for i := range tt.nodes {
				objs = append(objs, &tt.nodes[i])
			}

			c := newFakeClient(objs...)
			result := selectTemplateForModel(ctx, c, tt.service, testModelName)

			if tt.expectError {
				if result.Error == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if tt.expectedName != "" {
				if result.SelectedTemplate == nil {
					t.Errorf("expected template %s, got nil", tt.expectedName)
					return
				}
				if result.SelectedTemplate.Name != tt.expectedName {
					t.Errorf("expected template %s, got %s", tt.expectedName, result.SelectedTemplate.Name)
				}
			}

			if tt.expectedReason != "" {
				if result.SelectionReason != tt.expectedReason {
					t.Errorf("expected reason %s, got %s", tt.expectedReason, result.SelectionReason)
				}
			}

			if tt.expectTemplatesNotReady {
				if !result.TemplatesExistButNotReady {
					t.Error("expected TemplatesExistButNotReady=true, got false")
				}
			}
		})
	}
}
