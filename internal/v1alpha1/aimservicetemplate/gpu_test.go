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
	"testing"

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
