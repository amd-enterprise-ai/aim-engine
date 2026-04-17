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
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

func TestBuildComponentHealth(t *testing.T) {
	tests := []struct {
		name              string
		accelModel        string
		accelCount        int32
		resolvedResources *corev1.ResourceRequirements
		nodeErr           error
		matchResult       NodeMatchResult
		wantLen           int
		wantState         constants.AIMStatus
		wantReason        string
	}{
		{
			name:        "no accelerator returns nil",
			matchResult: NodeMatchResult{},
			wantLen:     0,
		},
		{
			name: "cpu-only resources returns nil",
			resolvedResources: &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("4"),
					corev1.ResourceMemory: resource.MustParse("32Gi"),
				},
			},
			matchResult: NodeMatchResult{},
			wantLen:     0,
		},
		{
			name:        "node list failure returns degraded",
			accelModel:  "MI300X",
			nodeErr:     fmt.Errorf("connection refused"),
			matchResult: NodeMatchResult{},
			wantLen:     1,
			wantState:   constants.AIMStatusDegraded,
			wantReason:  "NodeListFailed",
		},
		{
			name:              "matching nodes returns ready",
			accelModel:        "MI300X",
			accelCount:        1,
			resolvedResources: ResolveResources(aimv1alpha2.AcceleratorTypeGPU, 1, nil),
			matchResult:       NodeMatchResult{MatchingNodes: 2},
			wantLen:           1,
			wantState:         constants.AIMStatusReady,
			wantReason:        aimv1alpha2.AIMProfileReasonHardwareAvailable,
		},
		{
			name:              "no matching nodes returns not available",
			accelModel:        "MI300X",
			accelCount:        1,
			resolvedResources: ResolveResources(aimv1alpha2.AcceleratorTypeGPU, 1, nil),
			matchResult:       NodeMatchResult{MatchingNodes: 0},
			wantLen:           1,
			wantState:         constants.AIMStatusNotAvailable,
			wantReason:        aimv1alpha2.AIMProfileReasonHardwareNotAvailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildComponentHealth(tt.accelModel, tt.accelCount, tt.resolvedResources, tt.nodeErr, tt.matchResult)
			if len(result) != tt.wantLen {
				t.Fatalf("len(result) = %d, want %d", len(result), tt.wantLen)
			}
			if tt.wantLen > 0 {
				if result[0].State != tt.wantState {
					t.Errorf("state = %q, want %q", result[0].State, tt.wantState)
				}
				if result[0].Reason != tt.wantReason {
					t.Errorf("reason = %q, want %q", result[0].Reason, tt.wantReason)
				}
			}
		})
	}
}

func TestDecorateProfileStatus(t *testing.T) {
	tests := []struct {
		name            string
		spec            aimv1alpha2.AIMProfileSpecCommon
		nodeErr         error
		matchResult     NodeMatchResult
		wantVersion     string
		wantHWSummary   string
		wantMatchNodes  int32
		wantCondType    string
		wantCondStatus  metav1.ConditionStatus
		wantCondReason  string
		wantNoCond      bool
		wantHasAffinity bool
	}{
		{
			name: "CPU-only profile (no accelerator)",
			spec: aimv1alpha2.AIMProfileSpecCommon{
				Image: "registry.io/model:2.0.0",
			},
			matchResult:    NodeMatchResult{},
			wantVersion:    "2.0.0",
			wantHWSummary:  "CPU",
			wantMatchNodes: 0,
			wantCondType:   aimv1alpha2.AIMProfileConditionHardwareAvailable,
			wantCondStatus: metav1.ConditionTrue,
			wantCondReason: aimv1alpha2.AIMProfileReasonNoAccelerator,
		},
		{
			name: "GPU profile with matching nodes",
			spec: aimv1alpha2.AIMProfileSpecCommon{
				Image:            "registry.io/model:1.0.0",
				AcceleratorModel: "MI300X",
				AcceleratorType:  aimv1alpha2.AcceleratorTypeGPU,
				AcceleratorCount: 1,
			},
			matchResult: NodeMatchResult{
				MatchingNodes: 3,
				NodeAffinity:  BuildNodeAffinity("MI300X"),
			},
			wantVersion:     "1.0.0",
			wantHWSummary:   "1 x MI300X",
			wantMatchNodes:  3,
			wantCondType:    aimv1alpha2.AIMProfileConditionHardwareAvailable,
			wantCondStatus:  metav1.ConditionTrue,
			wantCondReason:  aimv1alpha2.AIMProfileReasonHardwareAvailable,
			wantHasAffinity: true,
		},
		{
			name: "GPU profile with no matching nodes",
			spec: aimv1alpha2.AIMProfileSpecCommon{
				Image:            "registry.io/model:0.8.5",
				AcceleratorModel: "MI300X",
				AcceleratorType:  aimv1alpha2.AcceleratorTypeGPU,
				AcceleratorCount: 4,
			},
			matchResult:     NodeMatchResult{MatchingNodes: 0},
			wantVersion:     "0.8.5",
			wantHWSummary:   "4 x MI300X",
			wantMatchNodes:  0,
			wantCondType:    aimv1alpha2.AIMProfileConditionHardwareAvailable,
			wantCondStatus:  metav1.ConditionFalse,
			wantCondReason:  aimv1alpha2.AIMProfileReasonHardwareNotAvailable,
			wantHasAffinity: false,
		},
		{
			name: "image without tag produces empty version",
			spec: aimv1alpha2.AIMProfileSpecCommon{
				Image: "registry.io/model",
			},
			matchResult:    NodeMatchResult{},
			wantVersion:    "",
			wantHWSummary:  "CPU",
			wantCondType:   aimv1alpha2.AIMProfileConditionHardwareAvailable,
			wantCondStatus: metav1.ConditionTrue,
			wantCondReason: aimv1alpha2.AIMProfileReasonNoAccelerator,
		},
		{
			name: "node list error skips HardwareAvailable condition",
			spec: aimv1alpha2.AIMProfileSpecCommon{
				Image:            "registry.io/model:1.0.0",
				AcceleratorModel: "MI300X",
				AcceleratorType:  aimv1alpha2.AcceleratorTypeGPU,
				AcceleratorCount: 1,
			},
			nodeErr:        fmt.Errorf("connection refused"),
			matchResult:    NodeMatchResult{},
			wantVersion:    "1.0.0",
			wantHWSummary:  "1 x MI300X",
			wantMatchNodes: 0,
			wantNoCond:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := &aimv1alpha2.AIMProfileStatus{}
			cm := controllerutils.NewConditionManager(nil)
			resolvedResources := ResolveResources(tt.spec.AcceleratorType, tt.spec.AcceleratorCount, tt.spec.Resources)

			decorateProfileStatus(status, cm, tt.spec, resolvedResources, tt.nodeErr, tt.matchResult)

			if status.Version != tt.wantVersion {
				t.Errorf("Version = %q, want %q", status.Version, tt.wantVersion)
			}
			if status.HardwareSummary != tt.wantHWSummary {
				t.Errorf("HardwareSummary = %q, want %q", status.HardwareSummary, tt.wantHWSummary)
			}
			if status.MatchingNodes != tt.wantMatchNodes {
				t.Errorf("MatchingNodes = %d, want %d", status.MatchingNodes, tt.wantMatchNodes)
			}
			if tt.wantHasAffinity && status.ResolvedNodeAffinity == nil {
				t.Error("expected ResolvedNodeAffinity to be non-nil")
			}

			conds := cm.Conditions()
			if tt.wantNoCond {
				for _, c := range conds {
					if c.Type == aimv1alpha2.AIMProfileConditionHardwareAvailable {
						t.Errorf("expected no HardwareAvailable condition, but found one with status=%q reason=%q", c.Status, c.Reason)
					}
				}
				return
			}

			found := false
			for _, c := range conds {
				if c.Type == tt.wantCondType {
					found = true
					if c.Status != tt.wantCondStatus {
						t.Errorf("condition %q status = %q, want %q", c.Type, c.Status, tt.wantCondStatus)
					}
					if c.Reason != tt.wantCondReason {
						t.Errorf("condition %q reason = %q, want %q", c.Type, c.Reason, tt.wantCondReason)
					}
					break
				}
			}
			if !found {
				t.Errorf("expected condition %q not found in %v", tt.wantCondType, conds)
			}
		})
	}
}
