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
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

// amdDevice is a device as the AMD GPU DRA driver publishes it. An empty
// partitionProfile leaves the attribute out, as on GPUs that cannot be
// partitioned.
func amdDevice(name, deviceID, deviceType, partitionProfile string) resourcev1.Device {
	attrs := map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{
		"deviceID": {StringValue: ptr.To(deviceID)},
		"type":     {StringValue: ptr.To(deviceType)},
	}
	if partitionProfile != "" {
		attrs["partitionProfile"] = resourcev1.DeviceAttribute{StringValue: ptr.To(partitionProfile)}
	}
	return resourcev1.Device{Name: name, Attributes: attrs}
}

func amdSlice(name, node, pool string, generation int64, devices ...resourcev1.Device) resourcev1.ResourceSlice {
	return resourcev1.ResourceSlice{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: resourcev1.ResourceSliceSpec{
			Driver:   constants.AMDGPUDRADriverName,
			NodeName: ptr.To(node),
			Pool:     resourcev1.ResourcePool{Name: pool, Generation: generation, ResourceSliceCount: 1},
			Devices:  devices,
		},
	}
}

func TestBuildNodeInventory(t *testing.T) {
	nodes := []corev1.Node{{ObjectMeta: metav1.ObjectMeta{Name: "dra"}}, {ObjectMeta: metav1.ObjectMeta{Name: "plain"}}}
	qualified := amdDevice("gpu-2", "", DRADeviceTypeGPU, "")
	qualified.Attributes = map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{
		constants.AMDGPUDRADriverName + "/deviceID": {StringValue: ptr.To("0x74A1")},
		constants.AMDGPUDRADriverName + "/type":     {StringValue: ptr.To(DRADeviceTypeGPU)},
	}
	otherDriver := amdSlice("other", "plain", "plain", 1, amdDevice("gpu-0", "0x2330", "gpu", ""))
	otherDriver.Spec.Driver = "gpu.nvidia.com"
	networkAttached := amdSlice("network", "", "fabric", 1, amdDevice("gpu-0", "0x74a1", DRADeviceTypeGPU, ""))
	networkAttached.Spec.NodeName = nil
	networkAttached.Spec.AllNodes = ptr.To(true)

	inventory := BuildNodeInventory(nodes, []resourcev1.ResourceSlice{
		amdSlice("stale", "dra", "dra", 1, amdDevice("gpu-9", "0x74a1", DRADeviceTypeGPU, "spx_nps1")),
		amdSlice("current", "dra", "dra", 2,
			amdDevice("gpu-0", "0x74a1", DRADeviceTypeGPU, "spx_nps1"),
			amdDevice("gpu-1", "0x7551", DRADeviceTypeGPU, ""),
		),
		amdSlice("current-2", "dra", "dra", 2, qualified),
		otherDriver,
		networkAttached,
	})

	want := map[string][]DRADevice{"dra": {
		{DeviceID: "74a1", Type: DRADeviceTypeGPU, PartitionProfile: "spx_nps1"},
		{DeviceID: "7551", Type: DRADeviceTypeGPU},
		{DeviceID: "74a1", Type: DRADeviceTypeGPU},
	}}
	if !reflect.DeepEqual(inventory.DRADevices, want) {
		t.Errorf("DRADevices = %+v, want %+v", inventory.DRADevices, want)
	}
	if len(inventory.Nodes) != 2 {
		t.Errorf("Nodes = %d, want 2", len(inventory.Nodes))
	}
}

func TestBuildNodeInventoryNodeWithoutDevicesIsADRANode(t *testing.T) {
	inventory := BuildNodeInventory(nil, []resourcev1.ResourceSlice{amdSlice("empty", "dra", "dra", 1)})
	devices, ok := inventory.DRADevices["dra"]
	if !ok || len(devices) != 0 {
		t.Errorf("DRADevices[dra] = %v (present %v), want an empty entry", devices, ok)
	}
}

func TestListNodeInventoryWithoutResourceAPI(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node"}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(node).Build()

	inventory, err := ListNodeInventory(context.Background(), c)
	if err != nil {
		t.Fatalf("ListNodeInventory() error = %v, want none when resource.k8s.io is unavailable", err)
	}
	if len(inventory.Nodes) != 1 || inventory.DRADevices != nil {
		t.Errorf("inventory = %+v, want one node and no DRA devices", inventory)
	}
}
