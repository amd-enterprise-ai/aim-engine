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

package aimmodel

import (
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
)

func TestIsCustomModel(t *testing.T) {
	tests := []struct {
		name     string
		spec     *aimv1alpha1.AIMModelSpec
		expected bool
	}{
		{
			name: "custom model with model sources",
			spec: &aimv1alpha1.AIMModelSpec{
				ModelSources: []aimv1alpha1.AIMModelSource{
					{ModelID: "org/model", SourceURI: "s3://bucket/model"},
				},
			},
			expected: true,
		},
		{
			name:     "image-based model without model sources",
			spec:     &aimv1alpha1.AIMModelSpec{Image: "ghcr.io/org/model:latest"},
			expected: false,
		},
		{
			name:     "empty spec",
			spec:     &aimv1alpha1.AIMModelSpec{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsCustomModel(tt.spec)
			if result != tt.expected {
				t.Errorf("IsCustomModel() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestMergeHardware(t *testing.T) {
	tests := []struct {
		name             string
		specDefault      *aimv1alpha1.AIMHardwareRequirements
		templateOverride *aimv1alpha1.AIMHardwareRequirements
		expectedGPU      *aimv1alpha1.AIMGpuRequirements
		expectedCPU      *aimv1alpha1.AIMCpuRequirements
	}{
		{
			name:             "nil spec default uses template override",
			specDefault:      nil,
			templateOverride: &aimv1alpha1.AIMHardwareRequirements{GPU: &aimv1alpha1.AIMGpuRequirements{Requests: 2}},
			expectedGPU:      &aimv1alpha1.AIMGpuRequirements{Requests: 2},
			expectedCPU:      nil,
		},
		{
			name:             "nil template override uses spec default",
			specDefault:      &aimv1alpha1.AIMHardwareRequirements{GPU: &aimv1alpha1.AIMGpuRequirements{Requests: 1}},
			templateOverride: nil,
			expectedGPU:      &aimv1alpha1.AIMGpuRequirements{Requests: 1},
			expectedCPU:      nil,
		},
		{
			name:             "both nil returns nil",
			specDefault:      nil,
			templateOverride: nil,
			expectedGPU:      nil,
			expectedCPU:      nil,
		},
		{
			name:        "template GPU requests override spec",
			specDefault: &aimv1alpha1.AIMHardwareRequirements{GPU: &aimv1alpha1.AIMGpuRequirements{Requests: 1}},
			templateOverride: &aimv1alpha1.AIMHardwareRequirements{GPU: &aimv1alpha1.AIMGpuRequirements{
				Requests: 4,
			}},
			expectedGPU: &aimv1alpha1.AIMGpuRequirements{Requests: 4},
			expectedCPU: nil,
		},
		{
			name:        "template GPU model replaces spec model",
			specDefault: &aimv1alpha1.AIMHardwareRequirements{GPU: &aimv1alpha1.AIMGpuRequirements{Requests: 1, Model: "mi300x"}},
			templateOverride: &aimv1alpha1.AIMHardwareRequirements{GPU: &aimv1alpha1.AIMGpuRequirements{
				Model: "a100",
			}},
			expectedGPU: &aimv1alpha1.AIMGpuRequirements{Requests: 1, Model: "a100"},
			expectedCPU: nil,
		},
		{
			name:        "spec GPU preserved when template only has CPU",
			specDefault: &aimv1alpha1.AIMHardwareRequirements{GPU: &aimv1alpha1.AIMGpuRequirements{Requests: 2}},
			templateOverride: &aimv1alpha1.AIMHardwareRequirements{CPU: &aimv1alpha1.AIMCpuRequirements{
				Requests: *resource.NewQuantity(4, resource.DecimalSI),
			}},
			expectedGPU: &aimv1alpha1.AIMGpuRequirements{Requests: 2},
			expectedCPU: &aimv1alpha1.AIMCpuRequirements{Requests: *resource.NewQuantity(4, resource.DecimalSI)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MergeHardware(tt.specDefault, tt.templateOverride)

			// Check nil result
			if tt.expectedGPU == nil && tt.expectedCPU == nil {
				if result != nil && (result.GPU != nil || result.CPU != nil) {
					t.Errorf("MergeHardware() = %+v, expected nil or empty", result)
				}
				return
			}

			if result == nil {
				t.Fatal("MergeHardware() = nil, expected non-nil result")
			}

			// Check GPU
			if tt.expectedGPU != nil {
				if result.GPU == nil {
					t.Fatal("MergeHardware().GPU = nil, expected non-nil")
				}
				if result.GPU.Requests != tt.expectedGPU.Requests {
					t.Errorf("MergeHardware().GPU.Requests = %d, expected %d", result.GPU.Requests, tt.expectedGPU.Requests)
				}
				if tt.expectedGPU.Model != "" {
					if result.GPU.Model != tt.expectedGPU.Model {
						t.Errorf("MergeHardware().GPU.Model = %v, expected %v", result.GPU.Model, tt.expectedGPU.Model)
					}
				}
			}

			// Check CPU
			if tt.expectedCPU != nil {
				if result.CPU == nil {
					t.Fatal("MergeHardware().CPU = nil, expected non-nil")
				}
				if !result.CPU.Requests.Equal(tt.expectedCPU.Requests) {
					t.Errorf("MergeHardware().CPU.Requests = %v, expected %v", result.CPU.Requests, tt.expectedCPU.Requests)
				}
			}
		})
	}
}

func TestGetEffectiveType(t *testing.T) {
	optimized := aimv1alpha1.AIMProfileTypeOptimized
	preview := aimv1alpha1.AIMProfileTypePreview

	tests := []struct {
		name         string
		specDefault  *aimv1alpha1.AIMProfileType
		templateType aimv1alpha1.AIMProfileType
		expected     aimv1alpha1.AIMProfileType
	}{
		{
			name:         "template type takes precedence",
			specDefault:  &optimized,
			templateType: preview,
			expected:     preview,
		},
		{
			name:         "uses spec default when template is empty",
			specDefault:  &optimized,
			templateType: "",
			expected:     optimized,
		},
		{
			name:         "defaults to unoptimized when both empty",
			specDefault:  nil,
			templateType: "",
			expected:     aimv1alpha1.AIMProfileTypeUnoptimized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetEffectiveType(tt.specDefault, tt.templateType)
			if result != tt.expected {
				t.Errorf("GetEffectiveType() = %v, expected %v", result, tt.expected)
			}
		})
	}
}
