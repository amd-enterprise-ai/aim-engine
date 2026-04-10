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

package aimprofile

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
)

func makeNode(name string, labels map[string]string, allocatable corev1.ResourceList) corev1.Node {
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Status:     corev1.NodeStatus{Allocatable: allocatable},
	}
}

func TestMatchNodes(t *testing.T) {
	mi300xNode := makeNode("gpu-node-1",
		map[string]string{
			AcceleratorLabelPrefix + "MI300X": "",
		},
		corev1.ResourceList{
			"amd.com/gpu":         resource.MustParse("4"),
			corev1.ResourceCPU:    resource.MustParse("64"),
			corev1.ResourceMemory: resource.MustParse("512Gi"),
		},
	)
	mi325xNode := makeNode("gpu-node-2",
		map[string]string{
			AcceleratorLabelPrefix + "MI325X": "",
		},
		corev1.ResourceList{
			"amd.com/gpu":         resource.MustParse("8"),
			corev1.ResourceCPU:    resource.MustParse("128"),
			corev1.ResourceMemory: resource.MustParse("1024Gi"),
		},
	)
	cpuNode := makeNode("cpu-node",
		map[string]string{},
		corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("32"),
			corev1.ResourceMemory: resource.MustParse("256Gi"),
		},
	)

	tests := []struct {
		name              string
		nodes             []corev1.Node
		accelModel        string
		resolvedResources *corev1.ResourceRequirements
		wantMatching      int32
		wantAffinity      bool
	}{
		{
			name:              "MI300X with count=1 matches one node",
			nodes:             []corev1.Node{mi300xNode, mi325xNode, cpuNode},
			accelModel:        "MI300X",
			resolvedResources: ResolveResources(aimv1alpha2.AcceleratorTypeGPU, 1, nil),
			wantMatching:      1,
			wantAffinity:      true,
		},
		{
			name:              "MI325X with count=8 matches one node",
			nodes:             []corev1.Node{mi300xNode, mi325xNode},
			accelModel:        "MI325X",
			resolvedResources: ResolveResources(aimv1alpha2.AcceleratorTypeGPU, 8, nil),
			wantMatching:      1,
			wantAffinity:      true,
		},
		{
			name:              "count exceeds node capacity matches zero",
			nodes:             []corev1.Node{mi300xNode},
			accelModel:        "MI300X",
			resolvedResources: ResolveResources(aimv1alpha2.AcceleratorTypeGPU, 8, nil),
			wantMatching:      0,
			wantAffinity:      true,
		},
		{
			name:       "explicit resource override takes precedence",
			nodes:      []corev1.Node{mi300xNode, mi325xNode, cpuNode},
			accelModel: "MI300X",
			resolvedResources: ResolveResources(aimv1alpha2.AcceleratorTypeGPU, 2, &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{"amd.com/gpu": resource.MustParse("1")},
			}),
			wantMatching: 1,
			wantAffinity: true,
		},
		{
			name:              "empty model matches all nodes",
			nodes:             []corev1.Node{mi300xNode, cpuNode},
			accelModel:        "",
			resolvedResources: nil,
			wantMatching:      2,
			wantAffinity:      false,
		},
		{
			name:              "unknown GPU model matches zero nodes",
			nodes:             []corev1.Node{mi300xNode, mi325xNode},
			accelModel:        "H100",
			resolvedResources: nil,
			wantMatching:      0,
			wantAffinity:      true,
		},
		{
			name:              "empty node list matches zero",
			nodes:             []corev1.Node{},
			accelModel:        "MI300X",
			resolvedResources: nil,
			wantMatching:      0,
			wantAffinity:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MatchNodes(tt.nodes, tt.accelModel, tt.resolvedResources)
			if result.MatchingNodes != tt.wantMatching {
				t.Errorf("MatchingNodes = %d, want %d", result.MatchingNodes, tt.wantMatching)
			}
			if tt.wantAffinity && result.NodeAffinity == nil {
				t.Error("expected NodeAffinity to be non-nil")
			}
			if !tt.wantAffinity && result.NodeAffinity != nil {
				t.Error("expected NodeAffinity to be nil")
			}
		})
	}
}

func TestBuildNodeAffinity(t *testing.T) {
	tests := []struct {
		name       string
		accelModel string
		wantNil    bool
		wantExprs  int
	}{
		{
			name:       "empty model returns nil",
			accelModel: "",
			wantNil:    true,
		},
		{
			name:       "model produces one expression",
			accelModel: "MI300X",
			wantExprs:  1,
		},
		{
			name:       "architecture-level model works the same",
			accelModel: "CDNA3",
			wantExprs:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildNodeAffinity(tt.accelModel)
			if tt.wantNil {
				if result != nil {
					t.Fatal("expected nil NodeAffinity")
				}
				return
			}
			if result == nil {
				t.Fatal("expected non-nil NodeAffinity")
			}
			req := result.RequiredDuringSchedulingIgnoredDuringExecution
			if req == nil {
				t.Fatal("expected RequiredDuringSchedulingIgnoredDuringExecution")
			}
			if len(req.NodeSelectorTerms) != 1 {
				t.Errorf("terms = %d, want 1", len(req.NodeSelectorTerms))
			}
			if len(req.NodeSelectorTerms[0].MatchExpressions) != tt.wantExprs {
				t.Errorf("expressions = %d, want %d", len(req.NodeSelectorTerms[0].MatchExpressions), tt.wantExprs)
			}
			expr := req.NodeSelectorTerms[0].MatchExpressions[0]
			if expr.Operator != corev1.NodeSelectorOpExists {
				t.Errorf("operator = %q, want Exists", expr.Operator)
			}
			wantKey := AcceleratorLabelPrefix + tt.accelModel
			if expr.Key != wantKey {
				t.Errorf("key = %q, want %q", expr.Key, wantKey)
			}
		})
	}
}

func TestBuildNodeAffinity_DeterministicOutput(t *testing.T) {
	first := BuildNodeAffinity("MI300X")

	for i := 0; i < 20; i++ {
		result := BuildNodeAffinity("MI300X")
		exprs := result.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchExpressions
		firstExprs := first.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchExpressions
		if exprs[0].Key != firstExprs[0].Key {
			t.Fatalf("iteration %d: key = %q, want %q (non-deterministic)", i, exprs[0].Key, firstExprs[0].Key)
		}
		if exprs[0].Operator != firstExprs[0].Operator {
			t.Fatalf("iteration %d: operator = %q, want %q (non-deterministic)", i, exprs[0].Operator, firstExprs[0].Operator)
		}
	}
}

func TestFormatHardwareSummary(t *testing.T) {
	tests := []struct {
		name       string
		accelModel string
		accelCount int32
		want       string
	}{
		{"single GPU", "MI300X", 1, "1 x MI300X"},
		{"multiple GPUs", "MI300X", 4, "4 x MI300X"},
		{"no model no count", "", 0, "CPU"},
		{"model without count", "MI300X", 0, "MI300X"},
		{"CPU accelerator with model and count", "EPYC_9965", 96, "96 x EPYC_9965"},
		{"CPU accelerator model only", "EPYC_9965", 0, "EPYC_9965"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatHardwareSummary(tt.accelModel, tt.accelCount)
			if got != tt.want {
				t.Errorf("FormatHardwareSummary() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractVersionFromImage(t *testing.T) {
	tests := []struct {
		image string
		want  string
	}{
		{"registry.io/aim-qwen:0.8.5", "0.8.5"},
		{"registry.io/aim-qwen:latest", "latest"},
		{"registry.io/aim-qwen", ""},
		{"registry.io:5000/aim-qwen:1.2.3", "1.2.3"},
		{"registry.io/aim-qwen@sha256:abc123", ""},
	}

	for _, tt := range tests {
		t.Run(tt.image, func(t *testing.T) {
			got := ExtractVersionFromImage(tt.image)
			if got != tt.want {
				t.Errorf("ExtractVersionFromImage(%q) = %q, want %q", tt.image, got, tt.want)
			}
		})
	}
}

func TestHasAcceleratorRequirement(t *testing.T) {
	tests := []struct {
		name       string
		accelModel string
		accelCount int32
		resources  *corev1.ResourceRequirements
		want       bool
	}{
		{
			name:       "has accelerator model",
			accelModel: "MI300X",
			want:       true,
		},
		{
			name: "has GPU resource requests",
			resources: &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{"amd.com/gpu": resource.MustParse("1")},
			},
			want: true,
		},
		{
			name: "CPU-only resources",
			resources: &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("4"),
					corev1.ResourceMemory: resource.MustParse("32Gi"),
				},
			},
			want: false,
		},
		{
			name: "no requirements",
			want: false,
		},
		{
			name:       "count only (no model)",
			accelCount: 4,
			want:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HasAcceleratorRequirement(tt.accelModel, tt.accelCount, tt.resources)
			if got != tt.want {
				t.Errorf("HasAcceleratorRequirement() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMatchNodes_NilLabelsNode(t *testing.T) {
	node := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "bare-node"},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("32"),
				corev1.ResourceMemory: resource.MustParse("256Gi"),
			},
		},
	}

	result := MatchNodes([]corev1.Node{node}, "MI300X", nil)
	if result.MatchingNodes != 0 {
		t.Errorf("expected 0 matching nodes for node with nil Labels, got %d", result.MatchingNodes)
	}
}

func TestResolveResources(t *testing.T) {
	tests := []struct {
		name       string
		accelType  aimv1alpha2.AcceleratorType
		accelCount int32
		resources  *corev1.ResourceRequirements
		wantNil    bool
		wantGPU    string
		wantCPU    string
		wantMemory string
	}{
		{
			name:       "GPU count injected",
			accelType:  aimv1alpha2.AcceleratorTypeGPU,
			accelCount: 4,
			wantGPU:    "4",
		},
		{
			name:       "CPU count injected",
			accelType:  aimv1alpha2.AcceleratorTypeCPU,
			accelCount: 96,
			wantCPU:    "96",
		},
		{
			name:       "explicit resources override derived count",
			accelType:  aimv1alpha2.AcceleratorTypeGPU,
			accelCount: 4,
			resources: &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{"amd.com/gpu": resource.MustParse("2")},
			},
			wantGPU: "2",
		},
		{
			name:       "count merged with cpu/memory resources",
			accelType:  aimv1alpha2.AcceleratorTypeGPU,
			accelCount: 1,
			resources: &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("8"),
					corev1.ResourceMemory: resource.MustParse("64Gi"),
				},
			},
			wantGPU:    "1",
			wantCPU:    "8",
			wantMemory: "64Gi",
		},
		{
			name: "nil type nil count with resources passes through",
			resources: &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4")},
			},
			wantCPU: "4",
		},
		{
			name:    "no type no count no resources returns nil",
			wantNil: true,
		},
		{
			name:       "zero count with no resources returns nil",
			accelType:  aimv1alpha2.AcceleratorTypeGPU,
			accelCount: 0,
			wantNil:    true,
		},
		{
			name:       "count without type returns nil (unknown type cannot derive resource)",
			accelCount: 4,
			wantNil:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved := ResolveResources(tt.accelType, tt.accelCount, tt.resources)
			if tt.wantNil {
				if resolved != nil {
					t.Fatalf("expected nil, got %v", resolved)
				}
				return
			}
			if resolved == nil {
				if tt.wantGPU != "" || tt.wantCPU != "" || tt.wantMemory != "" {
					t.Fatal("expected non-nil resolved resources")
				}
				return
			}
			if tt.wantGPU != "" {
				qty, ok := resolved.Requests[corev1.ResourceName("amd.com/gpu")]
				if !ok {
					t.Errorf("expected amd.com/gpu in resolved requests")
				} else if qty.String() != tt.wantGPU {
					t.Errorf("amd.com/gpu = %q, want %q", qty.String(), tt.wantGPU)
				}
			}
			if tt.wantCPU != "" {
				qty, ok := resolved.Requests[corev1.ResourceCPU]
				if !ok {
					t.Errorf("expected cpu in resolved requests")
				} else if qty.String() != tt.wantCPU {
					t.Errorf("cpu = %q, want %q", qty.String(), tt.wantCPU)
				}
			}
			if tt.wantMemory != "" {
				qty, ok := resolved.Requests[corev1.ResourceMemory]
				if !ok {
					t.Errorf("expected memory in resolved requests")
				} else if qty.String() != tt.wantMemory {
					t.Errorf("memory = %q, want %q", qty.String(), tt.wantMemory)
				}
			}
		})
	}
}
