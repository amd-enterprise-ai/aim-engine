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

package aimservicetemplate

import (
	"errors"
	"sort"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

// ============================================================================
// IS GPU AVAILABLE FOR SPEC TESTS
// ============================================================================

func TestIsGPUAvailableForSpec(t *testing.T) {
	gpuResources := map[string]utils.GPUResourceInfo{
		"MI300X": {ResourceName: "amd.com/gpu"},
		"MI250X": {ResourceName: "amd.com/gpu"},
	}

	tests := []struct {
		name         string
		spec         aimv1alpha1.AIMServiceTemplateSpecCommon
		gpuResources map[string]utils.GPUResourceInfo
		gpuFetchErr  error
		expected     bool
	}{
		{
			name: "specific GPU model available",
			spec: aimv1alpha1.AIMServiceTemplateSpecCommon{
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					Hardware: &aimv1alpha1.AIMHardwareRequirements{GPU: &aimv1alpha1.AIMGpuRequirements{
						Model:    "MI300X",
						Requests: 1,
					}},
				},
			},
			gpuResources: gpuResources,
			expected:     true,
		},
		{
			name: "specific GPU model not available",
			spec: aimv1alpha1.AIMServiceTemplateSpecCommon{
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					Hardware: &aimv1alpha1.AIMHardwareRequirements{GPU: &aimv1alpha1.AIMGpuRequirements{
						Model:    "MI355X",
						Requests: 1,
					}},
				},
			},
			gpuResources: gpuResources,
			expected:     false,
		},
		{
			name: "any GPU required (empty model) - GPUs available",
			spec: aimv1alpha1.AIMServiceTemplateSpecCommon{
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					Hardware: &aimv1alpha1.AIMHardwareRequirements{GPU: &aimv1alpha1.AIMGpuRequirements{
						Model:    "",
						Requests: 1,
					}},
				},
			},
			gpuResources: gpuResources,
			expected:     true,
		},
		{
			name: "any GPU required (empty model) - no GPUs available",
			spec: aimv1alpha1.AIMServiceTemplateSpecCommon{
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					Hardware: &aimv1alpha1.AIMHardwareRequirements{GPU: &aimv1alpha1.AIMGpuRequirements{
						Model:    "",
						Requests: 1,
					}},
				},
			},
			gpuResources: map[string]utils.GPUResourceInfo{},
			expected:     false,
		},
		{
			name: "no GPU required",
			spec: aimv1alpha1.AIMServiceTemplateSpecCommon{
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					Hardware: nil,
				},
			},
			gpuResources: gpuResources,
			expected:     true,
		},
		{
			name: "GPU fetch error",
			spec: aimv1alpha1.AIMServiceTemplateSpecCommon{
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					Hardware: &aimv1alpha1.AIMHardwareRequirements{GPU: &aimv1alpha1.AIMGpuRequirements{
						Model:    "MI300X",
						Requests: 1,
					}},
				},
			},
			gpuResources: gpuResources,
			gpuFetchErr:  errors.New("failed to fetch GPU resources"),
			expected:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsGPUAvailableForSpec(tt.spec, tt.gpuResources, tt.gpuFetchErr)
			if result != tt.expected {
				t.Errorf("IsGPUAvailableForSpec() = %v, want %v", result, tt.expected)
			}
		})
	}
}

// ============================================================================
// GET GPU HEALTH FROM RESOURCES TESTS
// ============================================================================

func TestGetGPUHealthFromResources(t *testing.T) {
	gpuResources := map[string]utils.GPUResourceInfo{
		"MI300X": {ResourceName: "amd.com/gpu"},
		"MI250X": {ResourceName: "amd.com/gpu"},
	}

	tests := []struct {
		name          string
		spec          aimv1alpha1.AIMServiceTemplateSpecCommon
		gpuResources  map[string]utils.GPUResourceInfo
		gpuFetchErr   error
		expectedState constants.AIMStatus
	}{
		{
			name: "specific GPU model available",
			spec: aimv1alpha1.AIMServiceTemplateSpecCommon{
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					Hardware: &aimv1alpha1.AIMHardwareRequirements{GPU: &aimv1alpha1.AIMGpuRequirements{
						Model:    "MI300X",
						Requests: 1,
					}},
				},
			},
			gpuResources:  gpuResources,
			expectedState: constants.AIMStatusReady,
		},
		{
			name: "specific GPU model not available",
			spec: aimv1alpha1.AIMServiceTemplateSpecCommon{
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					Hardware: &aimv1alpha1.AIMHardwareRequirements{GPU: &aimv1alpha1.AIMGpuRequirements{
						Model:    "MI355X",
						Requests: 1,
					}},
				},
			},
			gpuResources:  gpuResources,
			expectedState: constants.AIMStatusNotAvailable,
		},
		{
			name: "any GPU required (empty model) - GPUs available",
			spec: aimv1alpha1.AIMServiceTemplateSpecCommon{
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					Hardware: &aimv1alpha1.AIMHardwareRequirements{GPU: &aimv1alpha1.AIMGpuRequirements{
						Model:    "",
						Requests: 1,
					}},
				},
			},
			gpuResources:  gpuResources,
			expectedState: constants.AIMStatusReady,
		},
		{
			name: "any GPU required (empty model) - no GPUs available",
			spec: aimv1alpha1.AIMServiceTemplateSpecCommon{
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					Hardware: &aimv1alpha1.AIMHardwareRequirements{GPU: &aimv1alpha1.AIMGpuRequirements{
						Model:    "",
						Requests: 1,
					}},
				},
			},
			gpuResources:  map[string]utils.GPUResourceInfo{},
			expectedState: constants.AIMStatusNotAvailable,
		},
		{
			name: "no GPU required - returns empty health",
			spec: aimv1alpha1.AIMServiceTemplateSpecCommon{
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					Hardware: nil,
				},
			},
			gpuResources:  gpuResources,
			expectedState: "", // Empty health
		},
		{
			name: "GPU fetch error",
			spec: aimv1alpha1.AIMServiceTemplateSpecCommon{
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					Hardware: &aimv1alpha1.AIMHardwareRequirements{GPU: &aimv1alpha1.AIMGpuRequirements{
						Model:    "MI300X",
						Requests: 1,
					}},
				},
			},
			gpuResources:  gpuResources,
			gpuFetchErr:   errors.New("failed to fetch GPU resources"),
			expectedState: constants.AIMStatusDegraded,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetGPUHealthFromResources(tt.spec, tt.gpuResources, tt.gpuFetchErr)
			if result.State != tt.expectedState {
				t.Errorf("GetGPUHealthFromResources() state = %v, want %v", result.State, tt.expectedState)
			}
		})
	}
}

// ============================================================================
// TEMPLATE REQUIRES GPU WITH REQUESTS BUT NO MODEL
// ============================================================================

func TestTemplateRequiresGPU_RequestsWithoutModel(t *testing.T) {
	// This is the specific bug scenario: gpu.requests > 0 but gpu.model is empty
	spec := aimv1alpha1.AIMServiceTemplateSpecCommon{
		ModelName: "test-model",
		AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
			Hardware: &aimv1alpha1.AIMHardwareRequirements{GPU: &aimv1alpha1.AIMGpuRequirements{
				Model:    "",
				Requests: 1,
			}},
		},
	}

	result := TemplateRequiresGPU(spec)
	if !result {
		t.Errorf("TemplateRequiresGPU() = false, want true for requests > 0 with empty model")
	}
}

// ============================================================================
// VRAM AVAILABILITY CHECKS
// ============================================================================

func TestCheckVRAMAvailability(t *testing.T) {
	tests := []struct {
		name           string
		spec           aimv1alpha1.AIMServiceTemplateSpecCommon
		gpuResources   map[string]utils.GPUResourceInfo
		expectedState  constants.AIMStatus
		expectedReason string
	}{
		{
			name: "minVram satisfied - GPU has sufficient VRAM",
			spec: aimv1alpha1.AIMServiceTemplateSpecCommon{
				ModelName: "test-model",
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					Hardware: &aimv1alpha1.AIMHardwareRequirements{GPU: &aimv1alpha1.AIMGpuRequirements{
						Requests: 1,
						MinVRAM:  resource.NewQuantity(64*1024*1024*1024, resource.BinarySI), // 64Gi
					}},
				},
			},
			gpuResources: map[string]utils.GPUResourceInfo{
				"MI300X": {ResourceName: "amd.com/gpu", VRAM: "192G", VRAMSource: "label"},
			},
			expectedState:  constants.AIMStatusReady,
			expectedReason: "VRAMAvailable",
		},
		{
			name: "minVram NOT satisfied - all GPUs have insufficient VRAM",
			spec: aimv1alpha1.AIMServiceTemplateSpecCommon{
				ModelName: "test-model",
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					Hardware: &aimv1alpha1.AIMHardwareRequirements{GPU: &aimv1alpha1.AIMGpuRequirements{
						Requests: 1,
						MinVRAM:  resource.NewQuantity(256*1024*1024*1024, resource.BinarySI), // 256Gi
					}},
				},
			},
			gpuResources: map[string]utils.GPUResourceInfo{
				"MI300X": {ResourceName: "amd.com/gpu", VRAM: "192G", VRAMSource: "label"},
				"MI210":  {ResourceName: "amd.com/gpu", VRAM: "64G", VRAMSource: "static"},
			},
			expectedState:  constants.AIMStatusNotAvailable,
			expectedReason: "VRAMNotAvailable",
		},
		{
			name: "minVram exceeds all known GPUs",
			spec: aimv1alpha1.AIMServiceTemplateSpecCommon{
				ModelName: "test-model",
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					Hardware: &aimv1alpha1.AIMHardwareRequirements{GPU: &aimv1alpha1.AIMGpuRequirements{
						Requests: 1,
						MinVRAM:  resource.NewQuantity(1024*1024*1024*1024, resource.BinarySI), // 1Ti
					}},
				},
			},
			gpuResources: map[string]utils.GPUResourceInfo{
				"MI300X": {ResourceName: "amd.com/gpu", VRAM: "192G", VRAMSource: "label"},
			},
			expectedState:  constants.AIMStatusNotAvailable,
			expectedReason: "VRAMNotAvailable",
		},
		{
			name: "no minVram specified - should be Ready",
			spec: aimv1alpha1.AIMServiceTemplateSpecCommon{
				ModelName: "test-model",
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					Hardware: &aimv1alpha1.AIMHardwareRequirements{GPU: &aimv1alpha1.AIMGpuRequirements{
						Requests: 1,
						// No MinVRAM
					}},
				},
			},
			gpuResources: map[string]utils.GPUResourceInfo{
				"MI300X": {ResourceName: "amd.com/gpu", VRAM: "192G", VRAMSource: "label"},
			},
			expectedState:  constants.AIMStatusReady,
			expectedReason: "",
		},
		{
			name: "minVram with multiple GPUs - some satisfy, some don't",
			spec: aimv1alpha1.AIMServiceTemplateSpecCommon{
				ModelName: "test-model",
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					Hardware: &aimv1alpha1.AIMHardwareRequirements{GPU: &aimv1alpha1.AIMGpuRequirements{
						Requests: 1,
						MinVRAM:  resource.NewQuantity(128*1024*1024*1024, resource.BinarySI), // 128Gi
					}},
				},
			},
			gpuResources: map[string]utils.GPUResourceInfo{
				"MI300X": {ResourceName: "amd.com/gpu", VRAM: "192G", VRAMSource: "label"},
				"MI210":  {ResourceName: "amd.com/gpu", VRAM: "64G", VRAMSource: "static"},
				"MI100":  {ResourceName: "amd.com/gpu", VRAM: "32G", VRAMSource: "static"},
			},
			expectedState:  constants.AIMStatusReady,
			expectedReason: "VRAMAvailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := checkVRAMAvailability(tt.spec, tt.gpuResources)
			if result.State != tt.expectedState {
				t.Errorf("checkVRAMAvailability() state = %v, want %v", result.State, tt.expectedState)
			}
			if tt.expectedReason != "" && result.Reason != tt.expectedReason {
				t.Errorf("checkVRAMAvailability() reason = %v, want %v", result.Reason, tt.expectedReason)
			}
		})
	}
}

func TestGetGPUHealthFromResources_WithMinVRAM(t *testing.T) {
	tests := []struct {
		name           string
		spec           aimv1alpha1.AIMServiceTemplateSpecCommon
		gpuResources   map[string]utils.GPUResourceInfo
		expectedState  constants.AIMStatus
		expectedReason string
	}{
		{
			name: "GPU model available but VRAM insufficient - should fail",
			spec: aimv1alpha1.AIMServiceTemplateSpecCommon{
				ModelName: "test-model",
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					Hardware: &aimv1alpha1.AIMHardwareRequirements{GPU: &aimv1alpha1.AIMGpuRequirements{
						Requests: 1,
						Model:    "MI300X",
						MinVRAM:  resource.NewQuantity(512*1024*1024*1024, resource.BinarySI), // 512Gi (too high)
					}},
				},
			},
			gpuResources: map[string]utils.GPUResourceInfo{
				"MI300X": {ResourceName: "amd.com/gpu", VRAM: "192G", VRAMSource: "label"},
			},
			expectedState:  constants.AIMStatusNotAvailable,
			expectedReason: "VRAMNotAvailable",
		},
		{
			name: "GPU model available and VRAM sufficient - should succeed",
			spec: aimv1alpha1.AIMServiceTemplateSpecCommon{
				ModelName: "test-model",
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					Hardware: &aimv1alpha1.AIMHardwareRequirements{GPU: &aimv1alpha1.AIMGpuRequirements{
						Requests: 1,
						Model:    "MI300X",
						MinVRAM:  resource.NewQuantity(128*1024*1024*1024, resource.BinarySI), // 128Gi
					}},
				},
			},
			gpuResources: map[string]utils.GPUResourceInfo{
				"MI300X": {ResourceName: "amd.com/gpu", VRAM: "192G", VRAMSource: "label"},
			},
			expectedState:  constants.AIMStatusReady,
			expectedReason: "GPUAvailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetGPUHealthFromResources(tt.spec, tt.gpuResources, nil)
			if result.State != tt.expectedState {
				t.Errorf("GetGPUHealthFromResources() state = %v, want %v", result.State, tt.expectedState)
			}
			if tt.expectedReason != "" && result.Reason != tt.expectedReason {
				t.Errorf("GetGPUHealthFromResources() reason = %v, want %v", result.Reason, tt.expectedReason)
			}
		})
	}
}

// ============================================================================
// BUILD NODE AFFINITY FROM GPU REQUIREMENTS TESTS
// ============================================================================

// TestBuildNodeAffinityFromGPURequirements_AMDModels verifies the device IDs
// emitted for each supported AMD GPU model. A regression here would let pods
// schedule onto the wrong accelerators (or none at all).
func TestBuildNodeAffinityFromGPURequirements_AMDModels(t *testing.T) {
	tests := []struct {
		name              string
		model             string
		expectedDeviceIDs []string
	}{
		{
			name:              "Radeon Pro W7900",
			model:             "W7900",
			expectedDeviceIDs: []string{"7448", "744a", "744b"},
		},
		{
			name:              "Radeon AI Pro R9700",
			model:             "R9700",
			expectedDeviceIDs: []string{"7551"},
		},
		{
			name:              "Instinct MI300X",
			model:             "MI300X",
			expectedDeviceIDs: []string{"74a1", "74a9", "74b5", "74bd"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := aimv1alpha1.AIMServiceTemplateSpecCommon{
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					Hardware: &aimv1alpha1.AIMHardwareRequirements{
						GPU: &aimv1alpha1.AIMGpuRequirements{
							Model:    tt.model,
							Requests: 1,
						},
					},
				},
			}

			got := BuildNodeAffinityFromGPURequirements(spec, nil)
			if got == nil {
				t.Fatalf("expected non-nil NodeAffinity for model %q", tt.model)
			}
			req := got.RequiredDuringSchedulingIgnoredDuringExecution
			if req == nil || len(req.NodeSelectorTerms) != 1 {
				t.Fatalf("expected exactly one NodeSelectorTerm, got %+v", req)
			}
			term := req.NodeSelectorTerms[0]
			if len(term.MatchExpressions) != 1 {
				t.Fatalf("expected exactly one MatchExpression, got %d", len(term.MatchExpressions))
			}
			expr := term.MatchExpressions[0]
			if expr.Key != utils.LabelAMDGPUDeviceID {
				t.Errorf("expected key %q, got %q", utils.LabelAMDGPUDeviceID, expr.Key)
			}
			if expr.Operator != corev1.NodeSelectorOpIn {
				t.Errorf("expected operator In, got %v", expr.Operator)
			}
			// KnownAmdDevices map iteration is non-deterministic; sort before compare.
			gotIDs := append([]string(nil), expr.Values...)
			sort.Strings(gotIDs)
			wantIDs := append([]string(nil), tt.expectedDeviceIDs...)
			sort.Strings(wantIDs)
			if len(gotIDs) != len(wantIDs) {
				t.Fatalf("expected %d device IDs %v, got %d %v", len(wantIDs), wantIDs, len(gotIDs), gotIDs)
			}
			for i := range gotIDs {
				if gotIDs[i] != wantIDs[i] {
					t.Errorf("device IDs mismatch: got %v, want %v", gotIDs, wantIDs)
					break
				}
			}
		})
	}
}

// TestBuildNodeAffinityFromGPURequirements_NoAffinity asserts that specs
// without a GPU constraint or with an unknown model produce no node affinity.
func TestBuildNodeAffinityFromGPURequirements_NoAffinity(t *testing.T) {
	tests := []struct {
		name string
		spec aimv1alpha1.AIMServiceTemplateSpecCommon
	}{
		{
			name: "no hardware requirements",
			spec: aimv1alpha1.AIMServiceTemplateSpecCommon{},
		},
		{
			name: "GPU model unknown to KnownAmdDevices",
			spec: aimv1alpha1.AIMServiceTemplateSpecCommon{
				AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
					Hardware: &aimv1alpha1.AIMHardwareRequirements{GPU: &aimv1alpha1.AIMGpuRequirements{
						Model:    "NotAGPU9000",
						Requests: 1,
					}},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BuildNodeAffinityFromGPURequirements(tt.spec, nil); got != nil {
				t.Errorf("expected nil NodeAffinity, got %+v", got)
			}
		})
	}
}

func TestBuildNodeAffinityFromGPURequirements_SortsDeviceIDs(t *testing.T) {
	spec := aimv1alpha1.AIMServiceTemplateSpecCommon{
		ModelName: "test-model",
		AIMRuntimeParameters: aimv1alpha1.AIMRuntimeParameters{
			Hardware: &aimv1alpha1.AIMHardwareRequirements{GPU: &aimv1alpha1.AIMGpuRequirements{
				Model:    "MI300X",
				Requests: 1,
			}},
		},
	}

	affinity := BuildNodeAffinityFromGPURequirements(spec, nil)
	if affinity == nil || affinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		t.Fatal("BuildNodeAffinityFromGPURequirements() returned nil affinity")
	}

	terms := affinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
	if len(terms) != 1 || len(terms[0].MatchExpressions) != 1 {
		t.Fatalf("unexpected node affinity shape: %#v", affinity)
	}

	expr := terms[0].MatchExpressions[0]
	if expr.Key != utils.LabelAMDGPUDeviceID {
		t.Fatalf("match expression key = %q, want %q", expr.Key, utils.LabelAMDGPUDeviceID)
	}
	if expr.Operator != corev1.NodeSelectorOpIn {
		t.Fatalf("match expression operator = %q, want %q", expr.Operator, corev1.NodeSelectorOpIn)
	}

	want := []string{"74a1", "74a9", "74b5", "74bd"}
	if len(expr.Values) != len(want) {
		t.Fatalf("match expression values len = %d, want %d (%v)", len(expr.Values), len(want), expr.Values)
	}
	for i := range want {
		if expr.Values[i] != want[i] {
			t.Fatalf("match expression values = %v, want %v", expr.Values, want)
		}
	}
}
