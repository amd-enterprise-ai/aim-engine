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

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
)

// partitionNode builds a node carrying the model label plus the given
// partitioning-scheme.* keys (values are informational, ignored by matching).
func partitionNode(name, model string, schemes ...string) corev1.Node {
	labels := map[string]string{AcceleratorLabelPrefix + model: "8"}
	for _, s := range schemes {
		labels[PartitioningSchemeLabelPrefix+s] = "8"
	}
	return makeNode(name, labels, nil)
}

func TestMatchNodes_Partitioning(t *testing.T) {
	// Node states matching the partitioning-scheme label schema.
	unpartitioned := partitionNode("spx", "MI300X", PartitioningSchemeDefault, "SPX-NPS1")
	cpxNps4 := partitionNode("cpx4", "MI300X", "CPX-NPS4")
	cpxNps1 := partitionNode("cpx1", "MI300X", "CPX-NPS1")
	radeon := partitionNode("radeon", "RadeonW7900", PartitioningSchemeDefault)

	all := []corev1.Node{unpartitioned, cpxNps4, cpxNps1, radeon}

	tests := []struct {
		name         string
		nodes        []corev1.Node
		model        string
		mode         string
		wantMatching int32
	}{
		{"unpartitioned matches unpartitioned MI300X", all, "MI300X", "unpartitioned", 1},
		{"unpartitioned matches Radeon via default", all, "RadeonW7900", "unpartitioned", 1},
		{"empty mode behaves like unpartitioned", all, "MI300X", "", 1},
		{"partitioned excludes unpartitioned and Radeon", all, "MI300X", "partitioned", 2},
		{"partitioned does not match Radeon", all, "RadeonW7900", "partitioned", 0},
		{"specific scheme CPX-NPS4 matches only that node", all, "MI300X", "CPX-NPS4", 1},
		{"specific scheme CPX-NPS1 matches only that node", all, "MI300X", "CPX-NPS1", 1},
		{"unknown scheme matches nothing (fail-safe)", all, "MI300X", "DPX-NPS2", 0},
		{"compute-only spec value fails safe to zero", all, "MI300X", "CPX", 0},
		// Casing: detector publishes upper-case scheme keys; a lower-case or
		// mixed-case spec value must still match (canonicalizePartitioningMode).
		{"lower-case scheme matches upper-case node label", all, "MI300X", "cpx-nps4", 1},
		{"mixed-case scheme matches upper-case node label", all, "MI300X", "Cpx-Nps4", 1},
		{"upper-case UNPARTITIONED reserved word behaves like unpartitioned", all, "MI300X", "UNPARTITIONED", 1},
		{"upper-case PARTITIONED reserved word behaves like partitioned", all, "MI300X", "PARTITIONED", 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MatchNodes(tt.nodes, aimv1alpha1.AcceleratorTypeGPU, tt.model, tt.mode, nil).MatchingNodes
			if got != tt.wantMatching {
				t.Errorf("MatchingNodes = %d, want %d", got, tt.wantMatching)
			}
		})
	}
}

func TestMatchNodes_CPUIgnoresPartitioning(t *testing.T) {
	// A CPU node carries the model label but no partitioning-scheme.* labels;
	// a CPU profile must still match it (partition term is GPU-only).
	cpuNode := makeNode("cpu", map[string]string{AcceleratorLabelPrefix + "EPYC_9965": "128"}, nil)
	got := MatchNodes([]corev1.Node{cpuNode}, aimv1alpha1.AcceleratorTypeCPU, "EPYC_9965", "unpartitioned", nil).MatchingNodes
	if got != 1 {
		t.Errorf("CPU profile MatchingNodes = %d, want 1 (partitioning must not apply to CPU)", got)
	}
}

func TestMatchesPartitioningSelector(t *testing.T) {
	tests := []struct {
		name      string
		selector  string
		candidate string
		want      bool
	}{
		{"empty selector matches anything", "", "CPX-NPS4", true},
		{"empty selector matches empty", "", "", true},
		{"unpartitioned matches empty", "unpartitioned", "", true},
		{"unpartitioned matches unpartitioned", "unpartitioned", "unpartitioned", true},
		{"unpartitioned does not match partitioned", "unpartitioned", "CPX-NPS4", false},
		{"partitioned matches a scheme", "partitioned", "CPX-NPS4", true},
		{"partitioned does not match unpartitioned", "partitioned", "unpartitioned", false},
		{"partitioned does not match empty", "partitioned", "", false},
		{"prefix CPX matches CPX-NPS1", "CPX", "CPX-NPS1", true},
		{"prefix CPX matches CPX-NPS4", "CPX", "CPX-NPS4", true},
		{"prefix CPX does not match SPX-NPS1", "CPX", "SPX-NPS1", false},
		{"prefix CPX does not match bare unpartitioned", "CPX", "unpartitioned", false},
		{"exact scheme matches", "CPX-NPS4", "CPX-NPS4", true},
		{"exact scheme rejects different memory", "CPX-NPS4", "CPX-NPS1", false},
		// Casing is normalized on both sides before comparison.
		{"lower-case selector matches upper-case candidate", "cpx-nps4", "CPX-NPS4", true},
		{"upper-case selector matches lower-case candidate", "CPX-NPS4", "cpx-nps4", true},
		{"lower-case prefix matches mixed-case candidate", "cpx", "Cpx-Nps1", true},
		{"upper-case PARTITIONED selector matches partitioned candidate", "PARTITIONED", "cpx-nps4", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchesPartitioningSelector(tt.selector, tt.candidate); got != tt.want {
				t.Errorf("matchesPartitioningSelector(%q, %q) = %v, want %v", tt.selector, tt.candidate, got, tt.want)
			}
		})
	}
}

func TestApplyProfileCopyOverrides_PartitioningMode(t *testing.T) {
	source := aimv1alpha2.AIMProfileSpecCommon{
		Image:                       "registry.io/model:1.0.0",
		AcceleratorModel:            "MI300X",
		AcceleratorType:             aimv1alpha2.AcceleratorTypeGPU,
		AcceleratorPartitioningMode: "unpartitioned",
	}

	t.Run("override replaces mode", func(t *testing.T) {
		out, err := ApplyProfileCopyOverrides(source, &aimv1alpha1.ProfileOverrides{
			AcceleratorPartitioningMode: "CPX-NPS4",
		}, "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.AcceleratorPartitioningMode != "CPX-NPS4" {
			t.Errorf("mode = %q, want CPX-NPS4", out.AcceleratorPartitioningMode)
		}
	})

	t.Run("empty override leaves source mode untouched", func(t *testing.T) {
		out, err := ApplyProfileCopyOverrides(source, &aimv1alpha1.ProfileOverrides{}, "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.AcceleratorPartitioningMode != "unpartitioned" {
			t.Errorf("mode = %q, want unpartitioned (unchanged)", out.AcceleratorPartitioningMode)
		}
	})
}
