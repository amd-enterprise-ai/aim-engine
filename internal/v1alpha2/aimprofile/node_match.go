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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/aimimage"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

const (
	// AcceleratorLabelPrefix is the node label prefix for accelerator matching.
	// The AcceleratorDetector writes one label per detected identifier:
	//   feature.node.kubernetes.io/aim-accelerator.MI300X=8
	//   feature.node.kubernetes.io/aim-accelerator.EPYC_ZEN5=128
	// The value carries the accelerator count but is ignored by the matcher;
	// the operator uses an Exists selector on the key for node affinity. A
	// single node may have multiple labels (model, architecture, family).
	AcceleratorLabelPrefix = "feature.node.kubernetes.io/aim-accelerator."
)

// NodeMatchResult holds the result of matching a profile against cluster nodes.
type NodeMatchResult struct {
	MatchingNodes int32
	NodeAffinity  *corev1.NodeAffinity
}

// ResolveResources merges the accelerator-derived device request with explicit resources.
// The accelerator count is translated to a Kubernetes resource name based on type:
//
//	gpu → constants.DefaultGPUResourceName (amd.com/gpu)
//	cpu → corev1.ResourceCPU
//
// If spec.resources already contains the derived resource name, the explicit value wins.
// Returns nil only when both accelerator count is zero and resources is nil.
func ResolveResources(accelType aimv1alpha1.AcceleratorType, accelCount int32, resources *corev1.ResourceRequirements) *corev1.ResourceRequirements {
	derivedName, derivedQty := acceleratorDeviceRequest(accelType, accelCount)

	if derivedName == "" && resources == nil {
		return nil
	}

	resolved := &corev1.ResourceRequirements{}
	if resources != nil {
		resolved.Requests = make(corev1.ResourceList, len(resources.Requests))
		for k, v := range resources.Requests {
			resolved.Requests[k] = v.DeepCopy()
		}
		if resources.Limits != nil {
			resolved.Limits = make(corev1.ResourceList, len(resources.Limits))
			for k, v := range resources.Limits {
				resolved.Limits[k] = v.DeepCopy()
			}
		}
	} else {
		resolved.Requests = make(corev1.ResourceList)
	}

	if derivedName != "" {
		if _, exists := resolved.Requests[derivedName]; !exists {
			resolved.Requests[derivedName] = derivedQty
		}
		// GPU (and other extended/device resources) are non-overcommitable, so
		// Kubernetes requires Limits to be set for them on the Pod spec and
		// enforces requests==limits. Mirror the derived request into Limits when
		// the accelerator is a device type; CPU is overcommitable and can be
		// left unlimited.
		if accelType == aimv1alpha1.AcceleratorTypeGPU {
			if resolved.Limits == nil {
				resolved.Limits = make(corev1.ResourceList)
			}
			if _, exists := resolved.Limits[derivedName]; !exists {
				resolved.Limits[derivedName] = derivedQty
			}
		}
	}

	return resolved
}

// acceleratorDeviceRequest returns the K8s resource name and quantity derived from the
// accelerator type and count. Returns empty name when no device request can be derived.
func acceleratorDeviceRequest(accelType aimv1alpha1.AcceleratorType, accelCount int32) (corev1.ResourceName, resource.Quantity) {
	if accelCount <= 0 {
		return "", resource.Quantity{}
	}

	switch accelType {
	case aimv1alpha1.AcceleratorTypeGPU:
		return corev1.ResourceName(constants.DefaultGPUResourceName), *resource.NewQuantity(int64(accelCount), resource.DecimalSI)
	case aimv1alpha1.AcceleratorTypeCPU:
		return corev1.ResourceCPU, *resource.NewQuantity(int64(accelCount), resource.DecimalSI)
	default:
		return "", resource.Quantity{}
	}
}

// MatchNodes checks how many nodes in the list satisfy both the accelerator label
// requirements and the resolved resource capacity requirements of a profile.
func MatchNodes(nodes []corev1.Node, accelModel string, resolvedResources *corev1.ResourceRequirements) NodeMatchResult {
	affinity := BuildNodeAffinity(accelModel)

	var count int32
	for i := range nodes {
		if nodeMatchesAccelerator(&nodes[i], accelModel) && nodeHasResourceCapacity(&nodes[i], resolvedResources) {
			count++
		}
	}

	return NodeMatchResult{
		MatchingNodes: count,
		NodeAffinity:  affinity,
	}
}

// BuildNodeAffinity constructs a corev1.NodeAffinity from the accelerator model.
// Returns nil if no accelerator model is specified.
func BuildNodeAffinity(accelModel string) *corev1.NodeAffinity {
	if accelModel == "" {
		return nil
	}

	return &corev1.NodeAffinity{
		RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
			NodeSelectorTerms: []corev1.NodeSelectorTerm{
				{MatchExpressions: []corev1.NodeSelectorRequirement{
					{
						Key:      AcceleratorLabelPrefix + accelModel,
						Operator: corev1.NodeSelectorOpExists,
					},
				}},
			},
		},
	}
}

// nodeMatchesAccelerator checks if a node has the accelerator label key present.
// Returns true if no accelerator model is specified (no label constraints).
func nodeMatchesAccelerator(node *corev1.Node, accelModel string) bool {
	if accelModel == "" {
		return true
	}
	_, exists := node.Labels[AcceleratorLabelPrefix+accelModel]
	return exists
}

// nodeHasResourceCapacity checks if a node's allocatable resources can satisfy the
// profile's resource requests. Returns true if resources is nil (no resource constraints).
//
// Capacity is enforced only when the requested resource is actually reported in
// node.Status.Allocatable. If the resource is absent (e.g. on a kind cluster
// where no device plugin advertises amd.com/gpu, or during an NFD-vs-device-plugin
// race window at cluster startup), the accelerator label is treated as the
// authoritative signal that the hardware exists and the node is counted as a
// match. The kubelet still enforces real capacity at pod admission time, so a
// lying label can never produce a successfully-running pod — it just shifts the
// failure surface from profile-Ready to pod-Pending. This mirrors v1alpha1's
// label-only availability check (see aimservicetemplate.GPUHealthFromResources)
// and lets v1alpha2 profiles with realistic acceleratorModel values stay Ready
// on kind clusters that only carry labels, not device-plugin capacity.
func nodeHasResourceCapacity(node *corev1.Node, resources *corev1.ResourceRequirements) bool {
	if resources == nil || len(resources.Requests) == 0 {
		return true
	}
	for resourceName, requested := range resources.Requests {
		allocatable, exists := node.Status.Allocatable[resourceName]
		if !exists {
			continue
		}
		if allocatable.Cmp(requested) < 0 {
			return false
		}
	}
	return true
}

// FormatHardwareSummary builds a human-readable string from the accelerator spec.
// Examples: "4 x MI300X", "1 x MI300X", "EPYC_9965", "CPU".
func FormatHardwareSummary(accelModel string, accelCount int32) string {
	if accelModel == "" {
		return "CPU"
	}

	if accelCount > 0 {
		return fmt.Sprintf("%d x %s", accelCount, accelModel)
	}

	return accelModel
}

// ExtractVersionFromImage extracts a version tag from a container image
// reference. Returns the part after the last colon, or empty string if
// no tag is present.
//
// Thin wrapper around aimimage.ExtractTag so the v1alpha2 profile
// pipeline and the v1alpha1/v1alpha2 model pipelines share one
// implementation that's correct on tricky inputs (registry ports,
// digest references). Examples:
//
//	"registry/image:0.8.5"        → "0.8.5"
//	"registry/image"              → ""
//	"registry/image@sha256:abcd"  → ""
//	"registry.local:5000/image"   → ""
func ExtractVersionFromImage(image string) string {
	return aimimage.ExtractTag(image)
}

// HasAcceleratorRequirement returns true if the profile requires specific accelerator hardware.
// Checks accelerator model/count and any extended device resource in spec.resources.
func HasAcceleratorRequirement(accelModel string, accelCount int32, resources *corev1.ResourceRequirements) bool {
	if accelModel != "" || accelCount > 0 {
		return true
	}
	if resources == nil {
		return false
	}
	for name := range resources.Requests {
		if name != corev1.ResourceCPU && name != corev1.ResourceMemory && name != corev1.ResourceEphemeralStorage {
			return true
		}
	}
	return false
}
