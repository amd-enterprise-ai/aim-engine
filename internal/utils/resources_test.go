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

package utils

import (
	"testing"
)

func TestGetGPUVRAM(t *testing.T) {
	tests := []struct {
		name           string
		gpuModel       string
		nodeLabels     map[string]string
		expectedVRAM   string
		expectedSource string
	}{
		{
			name:     "VRAM from primary label",
			gpuModel: "MI300X",
			nodeLabels: map[string]string{
				LabelAMDGPUVRAM: "192G",
			},
			expectedVRAM:   "192G",
			expectedSource: "label",
		},
		{
			name:     "VRAM from beta label",
			gpuModel: "MI300X",
			nodeLabels: map[string]string{
				LabelAMDGPUVRAMBeta: "192G",
			},
			expectedVRAM:   "192G",
			expectedSource: "label",
		},
		{
			name:     "primary label takes precedence over beta",
			gpuModel: "MI300X",
			nodeLabels: map[string]string{
				LabelAMDGPUVRAM:     "192G",
				LabelAMDGPUVRAMBeta: "180G",
			},
			expectedVRAM:   "192G",
			expectedSource: "label",
		},
		{
			name:           "fallback to static mapping - MI300X",
			gpuModel:       "MI300X",
			nodeLabels:     map[string]string{},
			expectedVRAM:   "192G",
			expectedSource: "static",
		},
		{
			name:           "fallback to static mapping - MI325X",
			gpuModel:       "MI325X",
			nodeLabels:     map[string]string{},
			expectedVRAM:   "256G",
			expectedSource: "static",
		},
		{
			name:           "fallback to static mapping - MI210",
			gpuModel:       "MI210",
			nodeLabels:     map[string]string{},
			expectedVRAM:   "64G",
			expectedSource: "static",
		},
		{
			name:           "unknown GPU model - no label, no static mapping",
			gpuModel:       "UNKNOWN-GPU",
			nodeLabels:     map[string]string{},
			expectedVRAM:   "",
			expectedSource: "unknown",
		},
		{
			name:     "label overrides static mapping",
			gpuModel: "MI300X",
			nodeLabels: map[string]string{
				LabelAMDGPUVRAM: "256G", // Different from static 192G
			},
			expectedVRAM:   "256G",
			expectedSource: "label",
		},
		{
			name:           "model normalization - lowercase",
			gpuModel:       "mi300x",
			nodeLabels:     map[string]string{},
			expectedVRAM:   "192G",
			expectedSource: "static",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vram, source := GetGPUVRAM(tt.gpuModel, tt.nodeLabels)
			if vram != tt.expectedVRAM {
				t.Errorf("GetGPUVRAM() vram = %q, want %q", vram, tt.expectedVRAM)
			}
			if source != tt.expectedSource {
				t.Errorf("GetGPUVRAM() source = %q, want %q", source, tt.expectedSource)
			}
		})
	}
}

func TestParseVRAMToBytes(t *testing.T) {
	tests := []struct {
		name     string
		vram     string
		expected int64
	}{
		{
			name:     "192G",
			vram:     "192G",
			expected: 192 * 1024 * 1024 * 1024,
		},
		{
			name:     "64G",
			vram:     "64G",
			expected: 64 * 1024 * 1024 * 1024,
		},
		{
			name:     "1T",
			vram:     "1T",
			expected: 1024 * 1024 * 1024 * 1024,
		},
		{
			name:     "lowercase 192g",
			vram:     "192g",
			expected: 192 * 1024 * 1024 * 1024,
		},
		{
			name:     "empty string",
			vram:     "",
			expected: 0,
		},
		{
			name:     "invalid format",
			vram:     "abc",
			expected: 0,
		},
		{
			name:     "whitespace handling",
			vram:     " 64G ",
			expected: 64 * 1024 * 1024 * 1024,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseVRAMToBytes(tt.vram)
			if result != tt.expected {
				t.Errorf("ParseVRAMToBytes(%q) = %d, want %d", tt.vram, result, tt.expected)
			}
		})
	}
}

func TestGetVRAMTiersAboveThreshold(t *testing.T) {
	tests := []struct {
		name         string
		minVRAMBytes int64
		wantContains []string
		wantExcludes []string
	}{
		{
			name:         "zero threshold returns all tiers",
			minVRAMBytes: 0,
			wantContains: []string{"16G", "192G", "288G"},
			wantExcludes: []string{},
		},
		{
			name:         "64G threshold",
			minVRAMBytes: 64 * 1024 * 1024 * 1024,
			wantContains: []string{"64G", "128G", "192G", "256G", "288G"},
			wantExcludes: []string{"16G", "24G", "32G", "48G"},
		},
		{
			name:         "192G threshold",
			minVRAMBytes: 192 * 1024 * 1024 * 1024,
			wantContains: []string{"192G", "256G", "288G"},
			wantExcludes: []string{"16G", "64G", "128G"},
		},
		{
			name:         "256G threshold",
			minVRAMBytes: 256 * 1024 * 1024 * 1024,
			wantContains: []string{"256G", "288G"},
			wantExcludes: []string{"192G"},
		},
		{
			name:         "very high threshold returns only highest tiers",
			minVRAMBytes: 288 * 1024 * 1024 * 1024,
			wantContains: []string{"288G"},
			wantExcludes: []string{"256G"},
		},
		{
			name:         "threshold higher than all known GPUs",
			minVRAMBytes: 1024 * 1024 * 1024 * 1024, // 1TB
			wantContains: []string{},
			wantExcludes: []string{"288G"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetVRAMTiersAboveThreshold(tt.minVRAMBytes)

			// Check that expected tiers are present
			resultMap := make(map[string]bool)
			for _, tier := range result {
				resultMap[tier] = true
			}

			for _, want := range tt.wantContains {
				if !resultMap[want] {
					t.Errorf("GetVRAMTiersAboveThreshold(%d) missing expected tier %q, got %v", tt.minVRAMBytes, want, result)
				}
			}

			for _, exclude := range tt.wantExcludes {
				if resultMap[exclude] {
					t.Errorf("GetVRAMTiersAboveThreshold(%d) should not contain %q, got %v", tt.minVRAMBytes, exclude, result)
				}
			}
		})
	}
}

func TestKnownGPUVRAMConsistency(t *testing.T) {
	// Verify that all GPUs in KnownAmdDevices that have a static VRAM mapping are consistent
	for deviceID, modelName := range KnownAmdDevices {
		if vram, ok := KnownGPUVRAM[modelName]; ok {
			// Verify the VRAM value is parseable
			bytes := ParseVRAMToBytes(vram)
			if bytes == 0 {
				t.Errorf("KnownGPUVRAM[%q] = %q (from device %s) is not parseable", modelName, vram, deviceID)
			}
		}
	}
}

func TestGPUResourceInfoWithVRAM(t *testing.T) {
	// Test that GPUResourceInfo properly stores VRAM information
	info := GPUResourceInfo{
		ResourceName: "amd.com/gpu",
		VRAM:         "192G",
		VRAMSource:   "label",
	}

	if info.VRAM != "192G" {
		t.Errorf("GPUResourceInfo.VRAM = %q, want %q", info.VRAM, "192G")
	}
	if info.VRAMSource != "label" {
		t.Errorf("GPUResourceInfo.VRAMSource = %q, want %q", info.VRAMSource, "label")
	}
}

func TestGetGPUModelsWithMinVRAM(t *testing.T) {
	tests := []struct {
		name         string
		minVRAMBytes int64
		wantContains []string
		wantExcludes []string
	}{
		{
			name:         "zero threshold returns all models",
			minVRAMBytes: 0,
			wantContains: []string{"MI300X", "MI100", "RX6800"},
			wantExcludes: []string{},
		},
		{
			name:         "64G threshold",
			minVRAMBytes: 64 * 1024 * 1024 * 1024,
			wantContains: []string{"MI300X", "MI325X", "MI210", "MI250X"},
			wantExcludes: []string{"MI100", "RX6800"}, // 32G and 16G
		},
		{
			name:         "192G threshold",
			minVRAMBytes: 192 * 1024 * 1024 * 1024,
			wantContains: []string{"MI300X", "MI325X", "MI355X"},
			wantExcludes: []string{"MI210", "MI100"},
		},
		{
			name:         "very high threshold",
			minVRAMBytes: 512 * 1024 * 1024 * 1024, // 512G
			wantContains: []string{},
			wantExcludes: []string{"MI300X", "MI355X"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetGPUModelsWithMinVRAM(tt.minVRAMBytes)
			resultMap := make(map[string]bool)
			for _, model := range result {
				resultMap[model] = true
			}

			for _, want := range tt.wantContains {
				if !resultMap[want] {
					t.Errorf("GetGPUModelsWithMinVRAM(%d) missing expected model %q", tt.minVRAMBytes, want)
				}
			}

			for _, exclude := range tt.wantExcludes {
				if resultMap[exclude] {
					t.Errorf("GetGPUModelsWithMinVRAM(%d) should not contain %q", tt.minVRAMBytes, exclude)
				}
			}
		})
	}
}

func TestGetAMDDeviceIDsForMinVRAM(t *testing.T) {
	tests := []struct {
		name         string
		minVRAMBytes int64
		gpuModel     string
		wantEmpty    bool
		wantContains []string
	}{
		{
			name:         "minVRAM only - returns device IDs for all models meeting requirement",
			minVRAMBytes: 128 * 1024 * 1024 * 1024, // 128G
			gpuModel:     "",
			wantEmpty:    false,
			wantContains: []string{"74a1"}, // MI300X device ID
		},
		{
			name:         "model + minVRAM - model meets requirement",
			minVRAMBytes: 128 * 1024 * 1024 * 1024, // 128G
			gpuModel:     "MI300X",                 // 192G
			wantEmpty:    false,
			wantContains: []string{"74a1"},
		},
		{
			name:         "model + minVRAM - model does NOT meet requirement",
			minVRAMBytes: 256 * 1024 * 1024 * 1024, // 256G
			gpuModel:     "MI300X",                 // 192G - not enough
			wantEmpty:    true,
		},
		{
			name:         "model only - no minVRAM",
			minVRAMBytes: 0,
			gpuModel:     "MI300X",
			wantEmpty:    false,
			wantContains: []string{"74a1"},
		},
		{
			name:         "unknown model with minVRAM - permissive for unknown models",
			minVRAMBytes: 128 * 1024 * 1024 * 1024,
			gpuModel:     "UNKNOWN-GPU",
			wantEmpty:    true, // Unknown model has no device IDs
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetAMDDeviceIDsForMinVRAM(tt.minVRAMBytes, tt.gpuModel)

			if tt.wantEmpty && len(result) > 0 {
				t.Errorf("GetAMDDeviceIDsForMinVRAM(%d, %q) = %v, want empty", tt.minVRAMBytes, tt.gpuModel, result)
			}

			if !tt.wantEmpty && len(result) == 0 {
				t.Errorf("GetAMDDeviceIDsForMinVRAM(%d, %q) returned empty, want non-empty", tt.minVRAMBytes, tt.gpuModel)
			}

			resultMap := make(map[string]bool)
			for _, id := range result {
				resultMap[id] = true
			}

			for _, want := range tt.wantContains {
				if !resultMap[want] {
					t.Errorf("GetAMDDeviceIDsForMinVRAM(%d, %q) missing expected device ID %q", tt.minVRAMBytes, tt.gpuModel, want)
				}
			}
		})
	}
}
