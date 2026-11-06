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
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/yaml"
)

const (
	// LabelFakeGPU marks every object the generator emits, so a fleet can be
	// removed with one label selector.
	LabelFakeGPU = "aim.eai.amd.com/fake-gpu"
	// LabelFakeGPUGroup is the fleet group a node belongs to.
	LabelFakeGPUGroup = "aim.eai.amd.com/fake-gpu-group"

	// DriverName is the AMD GPU DRA driver's name, used for the DeviceClass and
	// the ResourceSlices.
	DriverName = "gpu.amd.com"
	// ResourceGPU is the AMD device plugin's resource.
	ResourceGPU corev1.ResourceName = "amd.com/gpu"

	// maxPCIGPUs keeps every GPU on its own PCIe root bus (0x00, 0x10, ... 0xf0).
	maxPCIGPUs = 16
	cardBase   = 1
	renderBase = 128

	labelAMDGPU         = "feature.node.kubernetes.io/amd-gpu"
	aimLabelPrefix      = "feature.node.kubernetes.io/aim-accelerator"
	aimPartitionPrefix  = aimLabelPrefix + ".partitioning-scheme"
	canonicalSPXScheme  = "SPX-NPS1"
	productNameMaxLen   = validation.LabelValueMaxLength - len("gpu.product-name.")
	defaultNodeCPU      = "192"
	defaultNodeMemory   = "1536Gi"
	defaultNodeMaxPods  = "110"
	kwokNodeAnnotation  = "kwok.x-k8s.io/node"
	kwokNodeTaintKey    = "kwok.x-k8s.io/node"
	kwokNodeTaintValue  = "fake"
	nodeTTLAnnotation   = "node.alpha.kubernetes.io/ttl"
	partitionDeviceType = "amdgpu-partition"
	wholeGPUDeviceType  = "amdgpu"
)

// Mode is how a group's nodes present their GPUs.
type Mode string

const (
	// ModeDevicePlugin advertises allocatable amd.com/gpu, one per GPU or per
	// partition, as the AMD device plugin does with resource_naming_strategy
	// single.
	ModeDevicePlugin Mode = "device-plugin"
	// ModeDRA publishes a ResourceSlice in the AMD GPU DRA driver's format.
	ModeDRA Mode = "dra"
)

// Fleet is the generator's input: groups of identical fake nodes.
type Fleet struct {
	// DriverVersion is published as the amdgpu driver version, in the node
	// labeller's driver-version label and the slices' driverVersion attribute.
	// Empty leaves both out.
	DriverVersion string  `json:"driverVersion,omitempty"`
	Groups        []Group `json:"groups"`
}

// Group is a set of identical nodes, named fake-gpu-<name>-<index>.
type Group struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
	// Mode defaults to device-plugin.
	Mode Mode `json:"mode,omitempty"`
	// NumaNodes spreads the GPUs evenly over this many NUMA nodes (default 1).
	NumaNodes int   `json:"numaNodes,omitempty"`
	GPUs      []GPU `json:"gpus,omitempty"`
	// CPU labels a node without GPUs with the CPU detector's labels.
	CPU *CPU `json:"cpu,omitempty"`
	// Labels are added after the generated ones.
	Labels map[string]string `json:"labels,omitempty"`
	// Capacity overrides the default cpu, memory and pods.
	Capacity corev1.ResourceList `json:"capacity,omitempty"`
	// Taints replace the default: amd.com/gpu:NoSchedule on GPU nodes and
	// kwok.x-k8s.io/node=fake:NoSchedule on nodes without GPUs.
	Taints *[]corev1.Taint `json:"taints,omitempty"`
}

// GPU is a number of GPUs of one model in each node of a group.
type GPU struct {
	Model string `json:"model"`
	Count int    `json:"count"`
	// Partition is a compute-memory partition mode such as cpx-nps4. Empty
	// means spx-nps1 on GPUs that can be partitioned.
	Partition string `json:"partition,omitempty"`
}

// CPU is the CPU model and core count of a node without GPUs.
type CPU struct {
	Model string `json:"model"`
	Cores int    `json:"cores"`
}

// device is one PCI GPU of a node.
type device struct {
	model gpuModel
	// compute and memory are the partition modes; empty when the model cannot
	// be partitioned.
	compute, memory string
	bdf, pcieRoot   string
	numa            int
	partitions      []partition
}

// partition is one schedulable device of a GPU: the whole GPU, or one of its
// compute partitions.
type partition struct {
	card, render int
	computeUnits int
	simdUnits    int
	memoryBytes  int64
}

func (d device) partitioned() bool {
	return d.compute != "" && d.compute != "spx"
}

var (
	computePartitionCount = map[string]int{"spx": 1, "dpx": 2, "qpx": 4}
	memoryPartitionCount  = map[string]int64{"nps1": 1, "nps2": 2, "nps4": 4}
)

// Combinations amdgpu accepts.
var validPartitions = map[string]bool{
	"spx-nps1": true, "dpx-nps1": true, "dpx-nps2": true,
	"qpx-nps1": true, "cpx-nps1": true, "cpx-nps4": true,
}

// ParseFleet reads a fleet file, rejecting unknown fields.
func ParseFleet(data []byte) (*Fleet, error) {
	var fleet Fleet
	if err := yaml.UnmarshalStrict(data, &fleet); err != nil {
		return nil, err
	}
	if err := fleet.validate(); err != nil {
		return nil, err
	}
	return &fleet, nil
}

func (f *Fleet) validate() error {
	seen := map[string]bool{}
	for _, g := range f.Groups {
		if errs := validation.IsDNS1123Label(g.Name); len(errs) > 0 {
			return fmt.Errorf("group name %q: %s", g.Name, strings.Join(errs, "; "))
		}
		if seen[g.Name] {
			return fmt.Errorf("group %q is defined twice", g.Name)
		}
		seen[g.Name] = true
		if g.Count < 1 {
			return fmt.Errorf("group %s: count must be at least 1", g.Name)
		}
		if g.Mode != "" && g.Mode != ModeDevicePlugin && g.Mode != ModeDRA {
			return fmt.Errorf("group %s: mode %q, want %q or %q", g.Name, g.Mode, ModeDevicePlugin, ModeDRA)
		}
		if len(g.GPUs) > 0 && g.CPU != nil {
			return fmt.Errorf("group %s: cpu labels only apply to nodes without GPUs", g.Name)
		}
		if _, err := expandDevices(g); err != nil {
			return fmt.Errorf("group %s: %w", g.Name, err)
		}
	}
	return nil
}

func parsePartition(gpu GPU, m gpuModel) (compute, memory string, err error) {
	if len(m.computeModes) == 0 {
		if gpu.Partition != "" {
			return "", "", fmt.Errorf("%s cannot be partitioned", gpu.Model)
		}
		return "", "", nil
	}
	mode := strings.ToLower(gpu.Partition)
	if mode == "" {
		mode = "spx-nps1"
	}
	compute, memory, ok := strings.Cut(mode, "-")
	if !ok || !slices.Contains(m.computeModes, compute) || !slices.Contains(m.memoryModes, memory) {
		return "", "", fmt.Errorf("%s does not offer partition %q", gpu.Model, gpu.Partition)
	}
	if !validPartitions[mode] {
		return "", "", fmt.Errorf("amdgpu does not accept partition %q", gpu.Partition)
	}
	return compute, memory, nil
}

// expandDevices lays out the PCI GPUs of one node of the group: one PCIe root
// bus per GPU, GPUs spread evenly over the NUMA nodes, and DRM card and
// render minors numbered across all partitions.
func expandDevices(g Group) ([]device, error) {
	var devices []device
	for _, gpu := range g.GPUs {
		m, ok := gpuModels[gpu.Model]
		if !ok {
			return nil, fmt.Errorf("unknown GPU model %q", gpu.Model)
		}
		if gpu.Count < 1 {
			return nil, fmt.Errorf("%s: count must be at least 1", gpu.Model)
		}
		compute, memory, err := parsePartition(gpu, m)
		if err != nil {
			return nil, err
		}
		for range gpu.Count {
			devices = append(devices, device{model: m, compute: compute, memory: memory})
		}
	}
	if len(devices) > maxPCIGPUs {
		return nil, fmt.Errorf("%d GPUs in one node, at most %d", len(devices), maxPCIGPUs)
	}

	numaNodes := max(g.NumaNodes, 1)
	card, render := cardBase, renderBase
	for i := range devices {
		d := &devices[i]
		rootBus := i * 0x10
		d.bdf = fmt.Sprintf("0000:%02x:00.0", rootBus+5)
		d.pcieRoot = fmt.Sprintf("pci0000:%02x", rootBus)
		d.numa = i * numaNodes / len(devices)

		parts := 1
		switch {
		case d.compute == "cpx":
			parts = d.model.xcc
		case d.compute != "":
			parts = computePartitionCount[d.compute]
		}
		memoryBytes := d.model.vramMiB << 20
		if d.memory != "" {
			memoryBytes /= memoryPartitionCount[d.memory]
		}
		computeUnits := d.model.computeUnits / parts
		for range parts {
			d.partitions = append(d.partitions, partition{
				card:         card,
				render:       render,
				computeUnits: computeUnits,
				simdUnits:    computeUnits * d.model.simdPerCU,
				memoryBytes:  memoryBytes,
			})
			card++
			render++
		}
	}
	return devices, nil
}

// Objects returns the nodes and ResourceSlices of the fleet, in group order.
func (f *Fleet) Objects() ([]runtime.Object, error) {
	var objects []runtime.Object
	for _, g := range f.Groups {
		devices, err := expandDevices(g)
		if err != nil {
			return nil, fmt.Errorf("group %s: %w", g.Name, err)
		}
		for i := range g.Count {
			node := buildNode(f, g, i, devices)
			objects = append(objects, node)
			if g.Mode == ModeDRA && len(devices) > 0 {
				objects = append(objects, buildResourceSlice(node.Name, devices, f.DriverVersion))
			}
		}
	}
	return objects, nil
}

// NodeName is the name of the i-th node of a group.
func NodeName(group string, i int) string {
	return fmt.Sprintf("fake-gpu-%s-%d", group, i)
}

func buildNode(f *Fleet, g Group, i int, devices []device) *corev1.Node {
	name := NodeName(g.Name, i)
	labels := map[string]string{}
	for k, v := range gpuOperatorLabels(devices, f.DriverVersion) {
		labels[k] = v
	}
	for k, v := range aimLabels(devices, g.CPU) {
		labels[k] = v
	}
	for k, v := range g.Labels {
		labels[k] = v
	}
	for k, v := range map[string]string{
		"kubernetes.io/hostname": name,
		"kubernetes.io/os":       "linux",
		"kubernetes.io/arch":     "amd64",
		"type":                   "kwok",
		LabelFakeGPU:             "true",
		LabelFakeGPUGroup:        g.Name,
	} {
		labels[k] = v
	}

	capacity := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse(defaultNodeCPU),
		corev1.ResourceMemory: resource.MustParse(defaultNodeMemory),
		corev1.ResourcePods:   resource.MustParse(defaultNodeMaxPods),
	}
	for k, v := range g.Capacity {
		capacity[k] = v
	}
	if g.Mode != ModeDRA && len(devices) > 0 {
		capacity[ResourceGPU] = *resource.NewQuantity(int64(devicePluginGPUs(devices)), resource.DecimalSI)
	}

	taints := []corev1.Taint{{Key: string(ResourceGPU), Effect: corev1.TaintEffectNoSchedule}}
	if len(devices) == 0 {
		taints = []corev1.Taint{{Key: kwokNodeTaintKey, Value: kwokNodeTaintValue, Effect: corev1.TaintEffectNoSchedule}}
	}
	if g.Taints != nil {
		taints = *g.Taints
	}

	return &corev1.Node{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Node"},
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
			Annotations: map[string]string{
				kwokNodeAnnotation: "fake",
				nodeTTLAnnotation:  "0",
			},
		},
		Spec: corev1.NodeSpec{Taints: taints},
		Status: corev1.NodeStatus{
			Capacity:    capacity,
			Allocatable: capacity.DeepCopy(),
		},
	}
}

// devicePluginGPUs is the amd.com/gpu count the AMD device plugin advertises
// with resource_naming_strategy single: one per GPU, or per partition of a
// partitioned GPU.
func devicePluginGPUs(devices []device) int {
	n := 0
	for _, d := range devices {
		n += len(d.partitions)
	}
	return n
}

func buildResourceSlice(nodeName string, devices []device, driverVersion string) *resourcev1.ResourceSlice {
	return &resourcev1.ResourceSlice{
		TypeMeta: metav1.TypeMeta{APIVersion: resourcev1.SchemeGroupVersion.String(), Kind: "ResourceSlice"},
		ObjectMeta: metav1.ObjectMeta{
			Name:   nodeName + "-" + DriverName,
			Labels: map[string]string{LabelFakeGPU: "true"},
		},
		Spec: resourcev1.ResourceSliceSpec{
			Driver:   DriverName,
			NodeName: ptr.To(nodeName),
			Pool:     resourcev1.ResourcePool{Name: nodeName, Generation: 1, ResourceSliceCount: 1},
			Devices:  sliceDevices(devices, driverVersion),
		},
	}
}

// sliceDevices returns the devices the AMD GPU DRA driver publishes for these
// GPUs: one per whole GPU, or one per partition of a partitioned GPU. See the
// driver's docs/driver-attributes.md.
func sliceDevices(devices []device, driverVersion string) []resourcev1.Device {
	var out []resourcev1.Device
	for _, d := range devices {
		deviceType := wholeGPUDeviceType
		if d.partitioned() {
			deviceType = partitionDeviceType
		}
		for _, p := range d.partitions {
			// productName is published even when empty, as on Radeon boards
			// without a product_name, so selectors must not rely on it there.
			attrs := map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{
				"type":                            {StringValue: ptr.To(deviceType)},
				"deviceID":                        {StringValue: ptr.To(d.model.deviceID)},
				"productName":                     {StringValue: ptr.To(driverProductName(d.model.productName))},
				"numaNode":                        {IntValue: ptr.To(int64(d.numa))},
				"resource.kubernetes.io/pciBusID": {StringValue: ptr.To(d.bdf)},
				"resource.kubernetes.io/pcieRoot": {StringValue: ptr.To(d.pcieRoot)},
			}
			if d.compute != "" {
				attrs["partitionProfile"] = resourcev1.DeviceAttribute{StringValue: ptr.To(d.compute + "_" + d.memory)}
			}
			if driverVersion != "" {
				attrs["driverVersion"] = resourcev1.DeviceAttribute{VersionValue: ptr.To(driverVersion)}
			}
			out = append(out, resourcev1.Device{
				Name:       fmt.Sprintf("gpu-%d-%d", p.card, p.render),
				Attributes: attrs,
				Capacity: map[resourcev1.QualifiedName]resourcev1.DeviceCapacity{
					"computeUnits": {Value: *resource.NewQuantity(int64(p.computeUnits), resource.BinarySI)},
					"simdUnits":    {Value: *resource.NewQuantity(int64(p.simdUnits), resource.BinarySI)},
					"memory":       {Value: *resource.NewQuantity(p.memoryBytes, resource.BinarySI)},
				},
			})
		}
	}
	return out
}

// driverProductName is the DRA driver's normalisation of sysfs product_name.
func driverProductName(name string) string {
	return strings.NewReplacer(" ", "_", "(", "", ")", "").Replace(strings.TrimSpace(name))
}

// DeviceClass returns the gpu.amd.com DeviceClass the AMD GPU DRA driver's
// chart installs, for clusters without the driver.
func DeviceClass() *resourcev1.DeviceClass {
	return &resourcev1.DeviceClass{
		TypeMeta: metav1.TypeMeta{APIVersion: resourcev1.SchemeGroupVersion.String(), Kind: "DeviceClass"},
		ObjectMeta: metav1.ObjectMeta{
			Name:   DriverName,
			Labels: map[string]string{LabelFakeGPU: "true"},
		},
		Spec: resourcev1.DeviceClassSpec{
			Selectors: []resourcev1.DeviceSelector{{
				CEL: &resourcev1.CELDeviceSelector{Expression: fmt.Sprintf("device.driver == '%s'", DriverName)},
			}},
		},
	}
}

// gpuOperatorLabels returns the AMD GPU Operator's NFD label and its node
// labeller's labels for the flags the operator enables by default: vram,
// cu-count, simd-count, device-id, family, product-name and driver-version.
// The labeller counts every partition as a GPU; partitions other than the
// first of each GPU have no PCI device or product_name, so they contribute
// no device ID and fall back to libdrm's market name.
func gpuOperatorLabels(devices []device, driverVersion string) map[string]string {
	if len(devices) == 0 {
		return nil
	}
	counts := map[string]map[string]int{}
	count := func(kind, value string) {
		if value == "" {
			return
		}
		if counts[kind] == nil {
			counts[kind] = map[string]int{}
		}
		counts[kind][value]++
	}
	for _, d := range devices {
		for i, p := range d.partitions {
			pciDevice := i == 0
			if pciDevice {
				count("device-id", strings.TrimPrefix(strings.ToLower(d.model.deviceID), "0x"))
			}
			name := d.model.marketName
			if pciDevice && d.model.productName != "" {
				name = d.model.productName
			}
			count("product-name", labellerLabelValue(name, productNameMaxLen))
			count("family", gpuFamily(d.model.gfxTargetVersion))
			count("vram", fmt.Sprintf("%dG", int(math.Round(float64(p.memoryBytes)/(1<<30)))))
			count("simd-count", strconv.Itoa(p.simdUnits))
			count("cu-count", strconv.Itoa(p.computeUnits))
		}
	}

	labels := map[string]string{labelAMDGPU: "true"}
	for kind, values := range counts {
		for value, n := range values {
			labels[fmt.Sprintf("beta.amd.com/gpu.%s.%s", kind, value)] = strconv.Itoa(n)
			if len(values) == 1 {
				labels["beta.amd.com/gpu."+kind] = value
				labels["amd.com/gpu."+kind] = value
			} else {
				labels[fmt.Sprintf("amd.com/gpu.%s.%s", kind, value)] = strconv.Itoa(n)
			}
		}
	}
	if driverVersion != "" {
		labels["amd.com/gpu.driver-version"] = driverVersion
	}
	return labels
}

// labellerLabelValue is the node labeller's sanitising of a label value:
// [A-Za-z0-9._-], at most maxLen characters, alphanumeric at both ends.
func labellerLabelValue(s string, maxLen int) string {
	s = strings.NewReplacer("(", "", ")", "").Replace(strings.TrimSpace(s))
	s = strings.Map(func(r rune) rune {
		if r <= unicode.MaxASCII && (r == '.' || r == '_' || r == '-' || unicode.IsLetter(r) || unicode.IsDigit(r)) {
			return r
		}
		return '_'
	}, s)
	if len(s) > maxLen {
		s = s[:maxLen]
	}
	return strings.TrimFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// gpuFamily is the node labeller's name for the AMDGPU family of a gfx target
// version; "" if unknown.
func gpuFamily(gfx int) string {
	major, minor, step := gfx/10000, gfx/100%100, gfx%100
	switch {
	case major == 9:
		return "AI"
	case major == 10:
		return "NV"
	case major == 11 && minor == 0 && step == 3:
		return "GC_11_0_1"
	case major == 11 && minor == 0:
		return "GC_11_0_0"
	case major == 11 && minor == 5:
		return "GC_11_5_0"
	case major == 12:
		return "GC_12_0_0"
	}
	return ""
}

// aimLabels returns the labels AIM Engine's AcceleratorDetector publishes. A
// GPU node gets each model, the GPU family and the AMD vendor with their GPU
// counts, and the partition schemes with their schedulable units; a node
// without GPUs gets the CPU detector's labels, if a CPU is given.
func aimLabels(devices []device, cpu *CPU) map[string]string {
	labels := map[string]string{}
	if len(devices) == 0 {
		if cpu != nil {
			cores := strconv.Itoa(cpu.Cores)
			labels[aimLabelPrefix+"."+cpu.Model] = cores
			labels[aimLabelPrefix+".CPU"] = cores
			labels[aimLabelPrefix+".vendor.CPU.AMD"] = cores
		}
		return labels
	}

	models := map[string]int{}
	for _, d := range devices {
		models[d.model.aimModel]++
	}
	for model, n := range models {
		labels[aimLabelPrefix+"."+model] = strconv.Itoa(n)
	}
	total := strconv.Itoa(len(devices))
	labels[aimLabelPrefix+".GPU"] = total
	labels[aimLabelPrefix+".vendor.GPU.AMD"] = total

	// amd-smi reports one row per partition; GPUs that cannot be partitioned
	// report none, leaving only the default sentinel.
	schemes := map[string]int{}
	for _, d := range devices {
		if d.compute != "" {
			schemes[strings.ToUpper(d.compute+"-"+d.memory)] += len(d.partitions)
		}
	}
	for scheme, n := range schemes {
		labels[aimPartitionPrefix+"."+scheme] = strconv.Itoa(n)
	}
	switch {
	case len(schemes) == 0:
		labels[aimPartitionPrefix+".default"] = total
	case len(schemes) == 1 && schemes[canonicalSPXScheme] > 0:
		labels[aimPartitionPrefix+".default"] = strconv.Itoa(schemes[canonicalSPXScheme])
	}
	return labels
}
