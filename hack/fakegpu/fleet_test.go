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

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"sigs.k8s.io/yaml"
)

// TestSliceDevicesMatchDriverOutput compares the generated devices with the
// ResourceSlices the AMD GPU DRA driver's own discovery code published for
// the same node shapes. Device names are left out: they follow the DRM minors,
// which the fixtures number from card10/renderD200.
func TestSliceDevicesMatchDriverOutput(t *testing.T) {
	tests := []struct {
		fixture string
		group   Group
	}{
		{
			fixture: "mi300x-v1.0.1.yaml",
			group:   Group{NumaNodes: 2, GPUs: []GPU{{Model: "mi300x", Count: 8}}},
		},
		{
			fixture: "mi300x-cpx-v1.0.1.yaml",
			group:   Group{GPUs: []GPU{{Model: "mi300x", Count: 2, Partition: "cpx-nps4"}}},
		},
		{
			fixture: "mixed-v1.0.1.yaml",
			group:   Group{GPUs: []GPU{{Model: "mi210", Count: 4}, {Model: "w7900", Count: 2}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", tt.fixture))
			if err != nil {
				t.Fatal(err)
			}
			var want resourcev1.ResourceSlice
			if err := yaml.UnmarshalStrict(data, &want); err != nil {
				t.Fatal(err)
			}
			devices, err := expandDevices(tt.group)
			if err != nil {
				t.Fatal(err)
			}
			got := sliceDevices(devices, "")
			if len(got) != len(want.Spec.Devices) {
				t.Fatalf("got %d devices, want %d", len(got), len(want.Spec.Devices))
			}
			for i := range got {
				g, w := got[i], want.Spec.Devices[i]
				g.Name, w.Name = "", ""
				if !equality.Semantic.DeepEqual(g, w) {
					gotYAML, _ := yaml.Marshal(g)
					wantYAML, _ := yaml.Marshal(w)
					t.Errorf("device %d differs from the driver's\ngot:\n%s\nwant:\n%s", i, gotYAML, wantYAML)
				}
			}
		})
	}
}

func nodesByName(t *testing.T, fleet *Fleet) (map[string]*corev1.Node, map[string]*resourcev1.ResourceSlice) {
	t.Helper()
	objects, err := fleet.Objects()
	if err != nil {
		t.Fatal(err)
	}
	nodes := map[string]*corev1.Node{}
	slices := map[string]*resourcev1.ResourceSlice{}
	for _, obj := range objects {
		switch o := obj.(type) {
		case *corev1.Node:
			nodes[o.Name] = o
		case *resourcev1.ResourceSlice:
			slices[*o.Spec.NodeName] = o
		default:
			t.Fatalf("unexpected object %T", obj)
		}
	}
	return nodes, slices
}

func assertLabels(t *testing.T, node *corev1.Node, want map[string]string, absent ...string) {
	t.Helper()
	for k, v := range want {
		if got, ok := node.Labels[k]; !ok || got != v {
			t.Errorf("%s: label %s = %q (present %v), want %q", node.Name, k, got, ok, v)
		}
	}
	for _, k := range absent {
		if _, ok := node.Labels[k]; ok {
			t.Errorf("%s: label %s should be absent", node.Name, k)
		}
	}
}

func TestDevicePluginNodes(t *testing.T) {
	fleet := &Fleet{DriverVersion: "6.16.6", Groups: []Group{
		{Name: "mi300x", Count: 2, NumaNodes: 2, GPUs: []GPU{{Model: "mi300x", Count: 8}}},
		{Name: "cpx", Count: 1, GPUs: []GPU{{Model: "mi300x", Count: 8, Partition: "cpx-nps4"}}},
		{Name: "radeon", Count: 1, GPUs: []GPU{{Model: "r9700", Count: 4}}},
	}}
	nodes, slices := nodesByName(t, fleet)
	if len(nodes) != 4 || len(slices) != 0 {
		t.Fatalf("got %d nodes and %d slices, want 4 nodes and no slices", len(nodes), len(slices))
	}

	tests := []struct {
		node   string
		gpus   int64
		labels map[string]string
		absent []string
	}{
		{
			node: "fake-gpu-mi300x-1",
			gpus: 8,
			labels: map[string]string{
				aimLabelPrefix + ".MI300X":                              "8",
				aimLabelPrefix + ".GPU":                                 "8",
				aimLabelPrefix + ".vendor.GPU.AMD":                      "8",
				aimPartitionPrefix + ".SPX-NPS1":                        "8",
				aimPartitionPrefix + ".default":                         "8",
				labelAMDGPU:                                             "true",
				"amd.com/gpu.device-id":                                 "74a1",
				"amd.com/gpu.vram":                                      "192G",
				"amd.com/gpu.family":                                    "AI",
				"amd.com/gpu.cu-count":                                  "304",
				"amd.com/gpu.product-name":                              "AMD_Instinct_MI300X_OAM",
				"amd.com/gpu.driver-version":                            "6.16.6",
				LabelFakeGPUGroup:                                       "mi300x",
				LabelFakeGPU:                                            "true",
				"kubernetes.io/hostname":                                "fake-gpu-mi300x-1",
				"beta.amd.com/gpu.device-id.74a1":                       "8",
				"beta.amd.com/gpu.product-name.AMD_Instinct_MI300X_OAM": "8",
			},
		},
		{
			// Each GPU is eight partitions; only the first of each has a PCI
			// device and product_name, so the labeller sees one device ID and
			// two product names.
			node: "fake-gpu-cpx-0",
			gpus: 64,
			labels: map[string]string{
				aimLabelPrefix + ".MI300X":                         "8",
				aimPartitionPrefix + ".CPX-NPS4":                   "64",
				"amd.com/gpu.device-id":                            "74a1",
				"amd.com/gpu.vram":                                 "48G",
				"amd.com/gpu.cu-count":                             "38",
				"amd.com/gpu.product-name.AMD_Instinct_MI300X_OAM": "8",
				"amd.com/gpu.product-name.AMD_Instinct_MI300X":     "56",
			},
			absent: []string{aimPartitionPrefix + ".default", aimPartitionPrefix + ".SPX-NPS1", "amd.com/gpu.product-name"},
		},
		{
			node: "fake-gpu-radeon-0",
			gpus: 4,
			labels: map[string]string{
				aimLabelPrefix + ".R9700":       "4",
				aimPartitionPrefix + ".default": "4",
				"amd.com/gpu.family":            "GC_12_0_0",
				"amd.com/gpu.product-name":      "AMD_Radeon_AI_PRO_R9700",
			},
			absent: []string{aimPartitionPrefix + ".SPX-NPS1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.node, func(t *testing.T) {
			node := nodes[tt.node]
			if node == nil {
				t.Fatalf("node %s not generated", tt.node)
			}
			assertLabels(t, node, tt.labels, tt.absent...)
			if got := node.Status.Allocatable[ResourceGPU]; got.Value() != tt.gpus {
				t.Errorf("allocatable %s = %s, want %d", ResourceGPU, got.String(), tt.gpus)
			}
			if node.Annotations[kwokNodeAnnotation] != "fake" {
				t.Errorf("missing KWOK annotation")
			}
			wantTaint := corev1.Taint{Key: string(ResourceGPU), Effect: corev1.TaintEffectNoSchedule}
			if len(node.Spec.Taints) != 1 || node.Spec.Taints[0] != wantTaint {
				t.Errorf("taints = %v, want [%v]", node.Spec.Taints, wantTaint)
			}
		})
	}
}

func TestDRANodes(t *testing.T) {
	fleet := &Fleet{DriverVersion: "6.16.6", Groups: []Group{
		{Name: "mi355x", Count: 2, Mode: ModeDRA, NumaNodes: 2, GPUs: []GPU{{Model: "mi355x", Count: 8}}},
	}}
	nodes, slices := nodesByName(t, fleet)
	if len(nodes) != 2 || len(slices) != 2 {
		t.Fatalf("got %d nodes and %d slices, want 2 of each", len(nodes), len(slices))
	}
	node := nodes["fake-gpu-mi355x-0"]
	if _, ok := node.Status.Allocatable[ResourceGPU]; ok {
		t.Errorf("DRA node advertises %s", ResourceGPU)
	}
	assertLabels(t, node, map[string]string{
		aimLabelPrefix + ".MI355X":      "8",
		aimPartitionPrefix + ".default": "8",
	})

	slice := slices["fake-gpu-mi355x-0"]
	if slice.Name != "fake-gpu-mi355x-0-gpu.amd.com" || slice.Spec.Driver != DriverName || slice.Spec.Pool.Name != "fake-gpu-mi355x-0" {
		t.Errorf("slice identity = %s, driver %s, pool %s", slice.Name, slice.Spec.Driver, slice.Spec.Pool.Name)
	}
	if len(slice.Spec.Devices) != 8 {
		t.Fatalf("slice has %d devices, want 8", len(slice.Spec.Devices))
	}
	first, last := slice.Spec.Devices[0], slice.Spec.Devices[7]
	if first.Name != "gpu-1-128" {
		t.Errorf("first device name = %s, want gpu-1-128", first.Name)
	}
	if v := first.Attributes["driverVersion"].VersionValue; v == nil || *v != "6.16.6" {
		t.Errorf("driverVersion = %v, want 6.16.6", v)
	}
	if *first.Attributes["numaNode"].IntValue != 0 || *last.Attributes["numaNode"].IntValue != 1 {
		t.Errorf("GPUs are not spread over two NUMA nodes")
	}
}

func TestMixedModelNode(t *testing.T) {
	fleet := &Fleet{Groups: []Group{
		{Name: "mixed", Count: 1, GPUs: []GPU{{Model: "mi350p", Count: 4}, {Model: "r9700", Count: 2}}},
		{Name: "mixed-dra", Count: 1, Mode: ModeDRA, GPUs: []GPU{{Model: "mi350p", Count: 4}, {Model: "r9700", Count: 2}}},
	}}
	nodes, slices := nodesByName(t, fleet)
	for _, name := range []string{"fake-gpu-mixed-0", "fake-gpu-mixed-dra-0"} {
		// The labeller reports each value with its count when a node has
		// several, and leaves out the single-value label.
		assertLabels(t, nodes[name], map[string]string{
			aimLabelPrefix + ".MI350P":                     "4",
			aimLabelPrefix + ".R9700":                      "2",
			aimLabelPrefix + ".GPU":                        "6",
			aimMixedModelsLabel:                            "2",
			aimPartitionPrefix + ".default":                "6",
			"amd.com/gpu.device-id.75a8":                   "4",
			"amd.com/gpu.device-id.7551":                   "2",
			"amd.com/gpu.family.AI":                        "4",
			"amd.com/gpu.family.GC_12_0_0":                 "2",
			"amd.com/gpu.product-name.AMD_Instinct_MI350P": "4",
		}, "amd.com/gpu.device-id", "amd.com/gpu.family")
	}
	if got := nodes["fake-gpu-mixed-0"].Status.Allocatable[ResourceGPU]; got.Value() != 6 {
		t.Errorf("device-plugin node allocatable %s = %s, want one pool of 6", ResourceGPU, got.String())
	}

	devices := slices["fake-gpu-mixed-dra-0"].Spec.Devices
	if len(devices) != 6 {
		t.Fatalf("slice has %d devices, want 6", len(devices))
	}
	for i, d := range devices {
		wantID := "0x75a8"
		if i >= 4 {
			wantID = "0x7551"
		}
		if got := *d.Attributes["deviceID"].StringValue; got != wantID {
			t.Errorf("device %d deviceID = %s, want %s", i, got, wantID)
		}
		if _, ok := d.Attributes["partitionProfile"]; ok {
			t.Errorf("device %d of models that cannot be partitioned has a partitionProfile", i)
		}
	}
}

func TestMixedPartitionDRANode(t *testing.T) {
	fleet := &Fleet{Groups: []Group{{
		Name: "parts", Count: 1, Mode: ModeDRA,
		GPUs: []GPU{{Model: "mi300x", Count: 4}, {Model: "mi300x", Count: 4, Partition: "cpx-nps1"}},
	}}}
	nodes, slices := nodesByName(t, fleet)
	assertLabels(t, nodes["fake-gpu-parts-0"], map[string]string{
		aimLabelPrefix + ".MI300X":       "8",
		aimPartitionPrefix + ".SPX-NPS1": "4",
		aimPartitionPrefix + ".CPX-NPS1": "32",
	}, aimPartitionPrefix+".default", aimMixedModelsLabel)

	counts := map[string]int{}
	for _, d := range slices["fake-gpu-parts-0"].Spec.Devices {
		counts[*d.Attributes["type"].StringValue+"/"+*d.Attributes["partitionProfile"].StringValue]++
	}
	want := map[string]int{"amdgpu/spx_nps1": 4, "amdgpu-partition/cpx_nps1": 32}
	if !equality.Semantic.DeepEqual(counts, want) {
		t.Errorf("devices by type and partition = %v, want %v", counts, want)
	}
}

func TestMI350PDevice(t *testing.T) {
	devices, err := expandDevices(Group{GPUs: []GPU{{Model: "mi350p", Count: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	d := sliceDevices(devices, "")[0]
	if got := *d.Attributes["productName"].StringValue; got != "AMD_Instinct_MI350P" {
		t.Errorf("productName = %s", got)
	}
	if got := d.Capacity["computeUnits"].Value; got.Value() != 128 {
		t.Errorf("computeUnits = %s, want 128", got.String())
	}
	if got := d.Capacity["memory"].Value; got.String() != "147440Mi" {
		t.Errorf("memory = %s, want 147440Mi", got.String())
	}
}

func TestParseFleetRejectsInvalidGroups(t *testing.T) {
	tests := map[string]string{
		"unknown field":      "groups: [{name: a, count: 1, gpu: []}]",
		"unknown model":      "groups: [{name: a, count: 1, gpus: [{model: mi999, count: 1}]}]",
		"unpartitionable":    "groups: [{name: a, count: 1, gpus: [{model: r9700, count: 1, partition: cpx-nps1}]}]",
		"invalid partition":  "groups: [{name: a, count: 1, gpus: [{model: mi300x, count: 1, partition: cpx-nps2}]}]",
		"too many GPUs":      "groups: [{name: a, count: 1, gpus: [{model: mi300x, count: 17}]}]",
		"duplicate group":    "groups: [{name: a, count: 1}, {name: a, count: 1}]",
		"unknown mode":       "groups: [{name: a, count: 1, mode: vfio}]",
		"invalid group name": "groups: [{name: A_B, count: 1}]",
		"no nodes":           "groups: [{name: a, count: 0}]",
		"cpu on a GPU node":  "groups: [{name: a, count: 1, cpu: {model: EPYC_9965, cores: 192}, gpus: [{model: mi300x, count: 1}]}]",
		"mixed partition modes on a device-plugin node": "groups: [{name: a, count: 1, gpus: [" +
			"{model: mi300x, count: 1}, {model: mi300x, count: 1, partition: cpx-nps1}]}]",
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseFleet([]byte(input)); err == nil {
				t.Errorf("ParseFleet(%q) succeeded, want an error", input)
			}
		})
	}
}

func TestCPUOnlyNode(t *testing.T) {
	fleet, err := ParseFleet([]byte("groups: [{name: epyc, count: 1, cpu: {model: EPYC_9965, cores: 192}}]"))
	if err != nil {
		t.Fatal(err)
	}
	nodes, _ := nodesByName(t, fleet)
	node := nodes["fake-gpu-epyc-0"]
	assertLabels(t, node, map[string]string{
		aimLabelPrefix + ".EPYC_9965":      "192",
		aimLabelPrefix + ".vendor.CPU.AMD": "192",
	}, labelAMDGPU, aimLabelPrefix+".GPU")
	if node.Spec.Taints[0].Key != kwokNodeTaintKey {
		t.Errorf("taints = %v, want the KWOK node taint", node.Spec.Taints)
	}
}

func TestDeviceClassFlag(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out, "", true); err != nil {
		t.Fatal(err)
	}
	var class resourcev1.DeviceClass
	if err := yaml.UnmarshalStrict(bytes.TrimPrefix(out.Bytes(), []byte("---\n")), &class); err != nil {
		t.Fatal(err)
	}
	if class.Name != DriverName || class.Spec.ExtendedResourceName != nil {
		t.Errorf("DeviceClass %s, extendedResourceName %v; want %s without one", class.Name, class.Spec.ExtendedResourceName, DriverName)
	}
}

func TestDefaultFleet(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out, "fleet.yaml", false); err != nil {
		t.Fatal(err)
	}
	for _, doc := range strings.Split(out.String(), "---\n")[1:] {
		var obj map[string]any
		if err := yaml.Unmarshal([]byte(doc), &obj); err != nil || obj["kind"] == nil {
			t.Fatalf("invalid document %q: %v", doc, err)
		}
	}
	for _, field := range []string{"creationTimestamp", "nodeInfo", "daemonEndpoints"} {
		if strings.Contains(out.String(), field+":") {
			t.Errorf("output contains the zero-valued field %s", field)
		}
	}
}
