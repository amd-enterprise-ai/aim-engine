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

	corev1 "k8s.io/api/core/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

// gpuFamilyModel is the accelerator model token of family-level GPU
// profiles, which accept any GPU model.
const gpuFamilyModel = "GPU"

// draRequirement is the GPU requirement of an AMD GPU profile, checked against
// the devices the AMD GPU DRA driver publishes for a node. A node matches
// when it has count devices of a single GPU model that match the model and
// partitioning mode, and room for the profile's other resource requests.
type draRequirement struct {
	model string
	// deviceIDs are the PCI device IDs of the profile's model; nil accepts
	// every model.
	deviceIDs map[string]bool
	// partitioning is the canonical partitioning mode.
	partitioning string
	count        int64
	// hostResources are the resolved requests other than the GPU resource.
	hostResources *corev1.ResourceRequirements
}

// newDRARequirement returns the DRA requirement of a profile, or nil when the
// profile cannot be placed on AMD GPU DRA devices: profiles without GPUs and
// profiles for other vendors.
func newDRARequirement(
	accelType aimv1alpha1.AcceleratorType,
	accelVendor aimv1alpha1.AcceleratorVendor,
	accelModel, partitioningMode string,
	resolvedResources *corev1.ResourceRequirements,
) *draRequirement {
	if accelType != aimv1alpha1.AcceleratorTypeGPU {
		return nil
	}
	if accelVendor != "" && accelVendor != aimv1alpha1.AcceleratorVendorAMD {
		return nil
	}

	req := &draRequirement{
		model:        accelModel,
		partitioning: canonicalizePartitioningMode(partitioningMode),
		count:        1,
	}
	if accelModel != "" && !strings.EqualFold(accelModel, gpuFamilyModel) {
		req.deviceIDs = map[string]bool{}
		for _, id := range utils.GetAMDDeviceIDsForModel(accelModel) {
			req.deviceIDs[id] = true
		}
	}

	gpuResource := gpuResourceName(accelVendor, partitioningMode)
	if resolvedResources != nil {
		if qty, ok := resolvedResources.Requests[gpuResource]; ok && qty.Value() > 0 {
			req.count = qty.Value()
		}
		req.hostResources = &corev1.ResourceRequirements{Requests: corev1.ResourceList{}}
		for name, qty := range resolvedResources.Requests {
			if name != gpuResource {
				req.hostResources.Requests[name] = qty
			}
		}
	}
	return req
}

func (r *draRequirement) matchesDevice(d DRADevice) bool {
	if r.deviceIDs != nil && !r.deviceIDs[d.DeviceID] {
		return false
	}
	switch r.partitioning {
	case "", PartitioningModeUnpartitioned:
		return d.Type == DRADeviceTypeGPU
	case PartitioningModePartitioned:
		return d.Type == DRADeviceTypePartition
	default:
		return d.PartitionProfile == draPartitionProfile(r.partitioning)
	}
}

// draPartitionProfile is the DRA driver's spelling of a partition scheme:
// CPX-NPS4 is published as cpx_nps4.
func draPartitionProfile(scheme string) string {
	return strings.ToLower(strings.ReplaceAll(scheme, "-", "_"))
}

// mismatchReasons explains why a DRA node does not match; nil when it does.
func (r *draRequirement) mismatchReasons(node *corev1.Node, devices []DRADevice) []string {
	var reasons []string
	if r.deviceIDs != nil && len(r.deviceIDs) == 0 {
		reasons = append(reasons, fmt.Sprintf("no AMD GPU device IDs are known for accelerator model %q", r.model))
	} else {
		perModel := map[string]int64{}
		var best int64
		for _, d := range devices {
			if r.matchesDevice(d) {
				perModel[d.DeviceID]++
				best = max(best, perModel[d.DeviceID])
			}
		}
		if best < r.count {
			reasons = append(reasons, fmt.Sprintf(
				"the node's AMD GPU DRA devices have %d of one model matching %s, %d needed",
				best, r.describe(), r.count,
			))
		}
	}
	return append(reasons, resourceMismatchReasons(node, r.hostResources)...)
}

func (r *draRequirement) describe() string {
	model := "any model"
	if r.deviceIDs != nil {
		model = "model " + r.model
	}
	switch r.partitioning {
	case "", PartitioningModeUnpartitioned:
		return model + " as whole GPUs"
	case PartitioningModePartitioned:
		return model + " as partitions"
	default:
		return model + " in partition profile " + draPartitionProfile(r.partitioning)
	}
}
