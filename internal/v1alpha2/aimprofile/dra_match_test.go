// MIT License
//
// Copyright (c) 2026 Advanced Micro Devices, Inc.
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
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

func repeatDevices(n int, deviceID, deviceType, partitionProfile string) []resourcev1.Device {
	devices := make([]resourcev1.Device, n)
	for i := range devices {
		devices[i] = amdDevice(fmt.Sprintf("gpu-%s-%d", deviceID, i), deviceID, deviceType, partitionProfile)
	}
	return devices
}

func hostCapacity(cpu string) corev1.ResourceList {
	return corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse(cpu),
		corev1.ResourceMemory: resource.MustParse("1536Gi"),
	}
}

// draTestFleet has one device-plugin node and DRA nodes labelled the way the
// AcceleratorDetector labels them.
func draTestFleet() NodeInventory {
	amdGPU := map[string]string{
		AcceleratorVendorLabelPrefix + "GPU.AMD":                  "8",
		PartitioningSchemeLabelPrefix + PartitioningSchemeDefault: "8",
	}
	withLabels := func(extra map[string]string) map[string]string {
		labels := map[string]string{}
		for k, v := range amdGPU {
			labels[k] = v
		}
		for k, v := range extra {
			labels[k] = v
		}
		return labels
	}
	dpCapacity := hostCapacity("192")
	dpCapacity["amd.com/gpu"] = resource.MustParse("8")

	nodes := []corev1.Node{
		makeNode("dp-mi300x", withLabels(map[string]string{AcceleratorLabelPrefix + "MI300X": "8"}), dpCapacity),
		makeNode("dra-mi355x", withLabels(map[string]string{AcceleratorLabelPrefix + "MI355X": "8"}), hostCapacity("192")),
		makeNode("dra-mixed", withLabels(map[string]string{
			AcceleratorLabelPrefix + "MI350P": "4",
			AcceleratorLabelPrefix + "R9700":  "2",
			MixedModelsLabelPrefix + "GPU":    "2",
		}), hostCapacity("192")),
		makeNode("dra-cpx", map[string]string{
			AcceleratorVendorLabelPrefix + "GPU.AMD":   "8",
			AcceleratorLabelPrefix + "MI300X":          "8",
			PartitioningSchemeLabelPrefix + "CPX-NPS4": "64",
		}, hostCapacity("192")),
		makeNode("dra-mi355x-small", withLabels(map[string]string{AcceleratorLabelPrefix + "MI355X": "8"}), hostCapacity("2")),
	}
	return BuildNodeInventory(nodes, []resourcev1.ResourceSlice{
		amdSlice("dra-mi355x", "dra-mi355x", "dra-mi355x", 1, repeatDevices(8, "0x75a3", DRADeviceTypeGPU, "spx_nps1")...),
		amdSlice("dra-mixed", "dra-mixed", "dra-mixed", 1, append(
			repeatDevices(4, "0x75a8", DRADeviceTypeGPU, ""),
			repeatDevices(2, "0x7551", DRADeviceTypeGPU, "")...)...),
		amdSlice("dra-cpx", "dra-cpx", "dra-cpx", 1, repeatDevices(64, "0x74a1", DRADeviceTypePartition, "cpx_nps4")...),
		amdSlice("dra-mi355x-small", "dra-mi355x-small", "dra-mi355x-small", 1, repeatDevices(8, "0x75a3", DRADeviceTypeGPU, "spx_nps1")...),
	})
}

func TestMatchProfileNodesSeparatesDevicePluginAndDRANodes(t *testing.T) {
	gpu := func(model string, count int32, partitioning string) aimv1alpha2.AIMProfileSpecCommon {
		return aimv1alpha2.AIMProfileSpecCommon{
			AcceleratorType:             aimv1alpha1.AcceleratorTypeGPU,
			AcceleratorModel:            model,
			AcceleratorCount:            count,
			AcceleratorPartitioningMode: partitioning,
		}
	}
	vendorOnly := func(count int32) aimv1alpha2.AIMProfileSpecCommon {
		spec := gpu("", count, "")
		spec.AcceleratorVendor = aimv1alpha1.AcceleratorVendorAMD
		return spec
	}
	nvidia := gpu("H100", 1, "")
	nvidia.AcceleratorVendor = aimv1alpha1.AcceleratorVendorNVIDIA

	tests := []struct {
		name    string
		spec    aimv1alpha2.AIMProfileSpecCommon
		wantDP  int32
		wantDRA int32
	}{
		// The DRA MI355X nodes carry the detector's MI355X label but no
		// amd.com/gpu; they used to count as device-plugin matches.
		{name: "whole MI355X GPUs only on DRA nodes", spec: gpu("MI355X", 8, ""), wantDP: 0, wantDRA: 1},
		{name: "MI300X whole GPU on the device-plugin node, not the CPX DRA node", spec: gpu("MI300X", 1, ""), wantDP: 1, wantDRA: 0},
		{name: "MI300X CPX-NPS4 partitions on the DRA node", spec: gpu("MI300X", 4, "cpx-nps4"), wantDP: 0, wantDRA: 1},
		{name: "any partition mode", spec: gpu("MI300X", 64, "partitioned"), wantDP: 0, wantDRA: 1},
		{name: "one model of the mixed DRA node", spec: gpu("R9700", 2, ""), wantDP: 0, wantDRA: 1},
		{name: "more of a model than the mixed DRA node has", spec: gpu("R9700", 3, ""), wantDP: 0, wantDRA: 0},
		{name: "vendor-only count within one model", spec: vendorOnly(4), wantDP: 1, wantDRA: 2},
		{name: "vendor-only count spanning two models", spec: vendorOnly(5), wantDP: 1, wantDRA: 1},
		{name: "family-level GPU model", spec: gpu("GPU", 8, ""), wantDP: 0, wantDRA: 1},
		{name: "NVIDIA profiles ignore AMD DRA devices", spec: nvidia, wantDP: 0, wantDRA: 0},
	}
	inventory := draTestFleet()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MatchProfileNodes(inventory, tt.spec, ResolveProfileResources(tt.spec))
			if result.MatchingNodes != tt.wantDP || result.DRAMatchingNodes != tt.wantDRA {
				t.Errorf("device plugin %d, DRA %d; want %d and %d\nmismatches: %+v",
					result.MatchingNodes, result.DRAMatchingNodes, tt.wantDP, tt.wantDRA, result.NodeMismatches)
			}
		})
	}
}

func TestUnpartitionedDRAMatchesOnlyDefaultWholeGPUs(t *testing.T) {
	req := &draRequirement{partitioning: PartitioningModeUnpartitioned}
	for _, tt := range []struct {
		device DRADevice
		want   bool
	}{
		{DRADevice{Type: DRADeviceTypeGPU, PartitionProfile: "spx_nps1"}, true},
		{DRADevice{Type: DRADeviceTypeGPU}, true},
		{DRADevice{Type: DRADeviceTypeGPU, PartitionProfile: "spx_nps2"}, false},
		{DRADevice{Type: DRADeviceTypePartition, PartitionProfile: "cpx_nps1"}, false},
	} {
		if got := req.matchesDevice(tt.device); got != tt.want {
			t.Errorf("matchesDevice(%+v) = %v, want %v", tt.device, got, tt.want)
		}
	}
}

func TestMatchProfileNodesCountsDRANodeWithDevicePluginGPUsAsBoth(t *testing.T) {
	capacity := hostCapacity("192")
	capacity["amd.com/gpu"] = resource.MustParse("8")
	node := makeNode("dra-and-dp", map[string]string{
		AcceleratorVendorLabelPrefix + "GPU.AMD":                  "8",
		AcceleratorLabelPrefix + "MI355X":                         "8",
		PartitioningSchemeLabelPrefix + PartitioningSchemeDefault: "8",
	}, capacity)
	inventory := BuildNodeInventory([]corev1.Node{node}, []resourcev1.ResourceSlice{
		amdSlice("dra-and-dp", "dra-and-dp", "dra-and-dp", 1, repeatDevices(8, "0x75a3", DRADeviceTypeGPU, "spx_nps1")...),
	})
	spec := aimv1alpha2.AIMProfileSpecCommon{
		AcceleratorType:  aimv1alpha1.AcceleratorTypeGPU,
		AcceleratorModel: "MI355X",
		AcceleratorCount: 8,
	}
	result := MatchProfileNodes(inventory, spec, ResolveProfileResources(spec))
	if result.MatchingNodes != 1 || result.DRAMatchingNodes != 1 || result.BothMatchingNodes != 1 {
		t.Errorf("device plugin %d, DRA %d, both %d; want 1, 1 and 1",
			result.MatchingNodes, result.DRAMatchingNodes, result.BothMatchingNodes)
	}
}

func TestMatchProfileNodesExplainsDRAMismatches(t *testing.T) {
	spec := aimv1alpha2.AIMProfileSpecCommon{
		AcceleratorType:  aimv1alpha1.AcceleratorTypeGPU,
		AcceleratorModel: "MI355X",
		AcceleratorCount: 8,
	}
	result := MatchProfileNodes(draTestFleet(), spec, ResolveProfileResources(spec))
	reasons := map[string]string{}
	for _, m := range result.NodeMismatches {
		reasons[m.NodeName] = strings.Join(m.Reasons, "; ")
	}
	if got := reasons["dra-mi355x-small"]; !strings.Contains(got, "resource cpu requested") {
		t.Errorf("small DRA node reasons = %q, want a CPU capacity reason", got)
	}
	if got := reasons["dra-mixed"]; !strings.Contains(got, "0 of one model matching model MI355X as whole GPUs, 8 needed") {
		t.Errorf("mixed DRA node reasons = %q, want a device count reason", got)
	}
	if got := reasons["dra-mixed"]; strings.Contains(got, "more than one GPU model") {
		t.Errorf("mixed DRA node reasons = %q; the mixed-model exclusion applies only to device-plugin nodes", got)
	}

	unknown := spec
	unknown.AcceleratorModel = "MI999"
	result = MatchProfileNodes(draTestFleet(), unknown, ResolveProfileResources(unknown))
	for _, m := range result.NodeMismatches {
		if m.NodeName == "dra-mi355x" && !strings.Contains(strings.Join(m.Reasons, "; "), `no AMD GPU device IDs are known for accelerator model "MI999"`) {
			t.Errorf("unknown model reasons = %v", m.Reasons)
		}
	}
}

func TestDecorateProfileStatusReportsDRAOnlyHardware(t *testing.T) {
	spec := aimv1alpha2.AIMProfileSpecCommon{
		AcceleratorType:  aimv1alpha1.AcceleratorTypeGPU,
		AcceleratorModel: "MI355X",
		AcceleratorCount: 8,
	}
	resources := ResolveProfileResources(spec)
	result := MatchProfileNodes(draTestFleet(), spec, resources)

	status := &aimv1alpha2.AIMProfileStatus{}
	cm := controllerutils.NewConditionManager(nil)
	decorateProfileStatus(status, cm, spec, resources, nil, result, true, nil, "", "")

	if status.MatchingNodes != 0 {
		t.Errorf("MatchingNodes = %d, want 0: DRA nodes cannot serve the profile yet", status.MatchingNodes)
	}
	want := aimv1alpha2.AIMProfileMatchingNodes{DevicePlugin: 0, DRA: 1}
	if status.MatchingNodesByAllocation == nil || *status.MatchingNodesByAllocation != want {
		t.Errorf("MatchingNodesByAllocation = %v, want %+v", status.MatchingNodesByAllocation, want)
	}
	cond := cm.Get(aimv1alpha2.AIMProfileConditionHardwareAvailable)
	if cond == nil || cond.Status != "False" || cond.Reason != aimv1alpha2.AIMProfileReasonHardwareOnlyOnDRANodes {
		t.Errorf("HardwareAvailable = %+v, want False with reason %s", cond, aimv1alpha2.AIMProfileReasonHardwareOnlyOnDRANodes)
	}

	cpu := aimv1alpha2.AIMProfileSpecCommon{AcceleratorType: aimv1alpha1.AcceleratorTypeCPU, AcceleratorModel: "EPYC_9965"}
	decorateProfileStatus(status, controllerutils.NewConditionManager(nil), cpu, nil, nil, NodeMatchResult{}, true, nil, "", "")
	if status.MatchingNodesByAllocation != nil {
		t.Errorf("MatchingNodesByAllocation = %+v for a CPU profile, want unset", status.MatchingNodesByAllocation)
	}
}
