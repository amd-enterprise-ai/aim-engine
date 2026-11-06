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
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

// AMD GPU DRA driver device types.
const (
	DRADeviceTypeGPU       = "amdgpu"
	DRADeviceTypePartition = "amdgpu-partition"
)

// DRADevice is one AMD GPU, or GPU partition, that the AMD GPU DRA driver
// publishes for a node.
type DRADevice struct {
	// DeviceID is the PCI device ID, lower-case and without the 0x prefix
	// (for example 74a1), as in utils.KnownAmdDevices.
	DeviceID string
	// Type is DRADeviceTypeGPU for a whole GPU and DRADeviceTypePartition for
	// one compute partition of a GPU.
	Type string
	// PartitionProfile is the compute-memory partition profile, such as
	// cpx_nps4. Empty on GPUs that cannot be partitioned.
	PartitionProfile string
}

// NodeInventory is the cluster state profile matching reads.
type NodeInventory struct {
	Nodes []corev1.Node
	// DRADevices maps the name of each node that has AMD GPU DRA
	// ResourceSlices to the devices they publish. Such a node presents its
	// GPUs through DRA, not as device-plugin resources.
	DRADevices map[string][]DRADevice
}

// NodesOnly is an inventory of nodes without DRA devices.
func NodesOnly(nodes []corev1.Node) NodeInventory {
	return NodeInventory{Nodes: nodes}
}

// ListNodeInventory lists the cluster's nodes and the AMD GPU DRA devices
// published for them. A cluster that does not serve resource.k8s.io/v1, or a
// client whose scheme lacks it, has no DRA nodes.
func ListNodeInventory(ctx context.Context, c client.Reader) (NodeInventory, error) {
	var nodes corev1.NodeList
	if err := c.List(ctx, &nodes); err != nil {
		return NodeInventory{}, fmt.Errorf("list nodes: %w", err)
	}
	var slices resourcev1.ResourceSliceList
	if err := c.List(ctx, &slices); err != nil {
		if meta.IsNoMatchError(err) || runtime.IsNotRegisteredError(err) {
			return NodesOnly(nodes.Items), nil
		}
		return NodeInventory{}, fmt.Errorf("list ResourceSlices: %w", err)
	}
	return BuildNodeInventory(nodes.Items, slices.Items), nil
}

// BuildNodeInventory combines nodes with the devices of the AMD GPU DRA
// driver's node-local ResourceSlices. Only the newest generation of each
// resource pool counts: the driver replaces slices of older generations.
func BuildNodeInventory(nodes []corev1.Node, slices []resourcev1.ResourceSlice) NodeInventory {
	type poolKey struct{ node, pool string }
	newest := map[poolKey]int64{}
	for i := range slices {
		s := &slices[i]
		if !isAMDGPUNodeSlice(s) {
			continue
		}
		key := poolKey{*s.Spec.NodeName, s.Spec.Pool.Name}
		if gen, seen := newest[key]; !seen || s.Spec.Pool.Generation > gen {
			newest[key] = s.Spec.Pool.Generation
		}
	}

	inventory := NodeInventory{Nodes: nodes}
	for i := range slices {
		s := &slices[i]
		if !isAMDGPUNodeSlice(s) {
			continue
		}
		node := *s.Spec.NodeName
		if s.Spec.Pool.Generation != newest[poolKey{node, s.Spec.Pool.Name}] {
			continue
		}
		if inventory.DRADevices == nil {
			inventory.DRADevices = map[string][]DRADevice{}
		}
		devices := inventory.DRADevices[node]
		if devices == nil {
			devices = []DRADevice{}
		}
		for _, d := range s.Spec.Devices {
			devices = append(devices, DRADevice{
				DeviceID:         strings.TrimPrefix(strings.ToLower(draStringAttribute(d, "deviceID")), "0x"),
				Type:             draStringAttribute(d, "type"),
				PartitionProfile: draStringAttribute(d, "partitionProfile"),
			})
		}
		inventory.DRADevices[node] = devices
	}
	return inventory
}

func isAMDGPUNodeSlice(s *resourcev1.ResourceSlice) bool {
	return s.Spec.Driver == constants.AMDGPUDRADriverName && s.Spec.NodeName != nil && *s.Spec.NodeName != ""
}

// draStringAttribute reads a driver attribute, which may be published with
// or without the driver's domain prefix.
func draStringAttribute(d resourcev1.Device, name string) string {
	for _, key := range []resourcev1.QualifiedName{
		resourcev1.QualifiedName(name),
		resourcev1.QualifiedName(constants.AMDGPUDRADriverName + "/" + name),
	} {
		if attr, ok := d.Attributes[key]; ok && attr.StringValue != nil {
			return *attr.StringValue
		}
	}
	return ""
}
