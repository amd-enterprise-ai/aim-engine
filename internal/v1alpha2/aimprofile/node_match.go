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
	"strings"

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

	// PartitioningSchemeLabelPrefix is the single partition axis published by
	// the AcceleratorDetector. Keys are either the `default` sentinel or a
	// literal `<Compute>-<Memory>` scheme:
	//   feature.node.kubernetes.io/aim-accelerator.partitioning-scheme.default=8
	//   feature.node.kubernetes.io/aim-accelerator.partitioning-scheme.CPX-NPS4=64
	// Like the model labels, values are informational; the resolver matches on
	// key existence via Exists / DoesNotExist.
	PartitioningSchemeLabelPrefix = AcceleratorLabelPrefix + "partitioning-scheme."

	// PartitioningSchemeDefault is the hardware-agnostic sentinel key suffix
	// stamped on canonical-unpartitioned and non-partitionable nodes.
	PartitioningSchemeDefault = "default"

	// PartitioningModeUnpartitioned selects unpartitioned hardware (the CRD default).
	PartitioningModeUnpartitioned = "unpartitioned"
	// PartitioningModePartitioned selects any actively-partitioned mode.
	PartitioningModePartitioned = "partitioned"
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
//
// MIXED-MODE SEAM: under resource_naming_strategy: single (the only supported
// strategy today) every GPU/partition is advertised as amd.com/gpu, so partition
// mode does NOT affect the resource name — it only scopes node affinity (see
// partitionNodeSelectorRequirement). The future mixed/multiple follow-up would
// branch here on the resolved partition mode to request a mode-specific resource
// (e.g. amd.com/cpx_nps4). Do not add that branch yet.
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

// MatchNodes checks how many nodes in the list satisfy the accelerator label
// requirements (model +, for GPU profiles, partition), and the resolved
// resource capacity requirements of a profile.
func MatchNodes(nodes []corev1.Node, accelType aimv1alpha1.AcceleratorType, accelModel, partitioningMode string, resolvedResources *corev1.ResourceRequirements) NodeMatchResult {
	affinity := BuildNodeAffinity(accelType, accelModel, partitioningMode)
	partitionReq := partitionNodeSelectorRequirement(accelType, partitioningMode)

	var count int32
	for i := range nodes {
		if nodeMatchesAccelerator(&nodes[i], accelModel) &&
			nodeMatchesPartitioning(&nodes[i], partitionReq) &&
			nodeHasResourceCapacity(&nodes[i], resolvedResources) {
			count++
		}
	}

	return NodeMatchResult{
		MatchingNodes: count,
		NodeAffinity:  affinity,
	}
}

// BuildNodeAffinity constructs a corev1.NodeAffinity from the accelerator model
// and (for GPU profiles) the partitioning mode. The terms are AND-ed into a
// single NodeSelectorTerm so the resulting affinity is "model AND partition".
// Returns nil when no accelerator model is specified.
func BuildNodeAffinity(accelType aimv1alpha1.AcceleratorType, accelModel, partitioningMode string) *corev1.NodeAffinity {
	if accelModel == "" {
		return nil
	}

	exprs := []corev1.NodeSelectorRequirement{
		{
			Key:      AcceleratorLabelPrefix + accelModel,
			Operator: corev1.NodeSelectorOpExists,
		},
	}
	if req := partitionNodeSelectorRequirement(accelType, partitioningMode); req != nil {
		exprs = append(exprs, *req)
	}

	return &corev1.NodeAffinity{
		RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
			NodeSelectorTerms: []corev1.NodeSelectorTerm{
				{MatchExpressions: exprs},
			},
		},
	}
}

// partitionNodeSelectorRequirement is the single seam that maps an
// acceleratorPartitioningMode to a node-affinity term over the
// partitioning-scheme label axis. Partitioning is a GPU-only concept, so the
// term is emitted only for GPU profiles; CPU and untyped profiles get no
// partition constraint (returns nil). The mapping is uniform and string-blind:
//
//	"" / "unpartitioned" -> partitioning-scheme.default Exists
//	"partitioned"        -> partitioning-scheme.default DoesNotExist
//	"<value>"            -> partitioning-scheme.<value> Exists
//
// MIXED-MODE SEAM: this is the one place partition mode becomes a *label*
// affinity term. The future resource_naming_strategy: mixed/multiple follow-up
// (a partition requested via a mode-specific RESOURCE name such as
// amd.com/cpx_nps4 instead of, or in addition to, this label) replaces or
// augments exactly this function plus acceleratorDeviceRequest below. No other
// caller needs to change, and the partitioning-scheme.<C>-<M> value shape is
// already 1:1 with the device plugin's mode-specific resource names. Do NOT add
// a strategy switch here yet — single is the only supported strategy.
func partitionNodeSelectorRequirement(accelType aimv1alpha1.AcceleratorType, partitioningMode string) *corev1.NodeSelectorRequirement {
	if accelType != aimv1alpha1.AcceleratorTypeGPU {
		return nil
	}
	switch mode := canonicalizePartitioningMode(partitioningMode); mode {
	case "", PartitioningModeUnpartitioned:
		return &corev1.NodeSelectorRequirement{
			Key:      PartitioningSchemeLabelPrefix + PartitioningSchemeDefault,
			Operator: corev1.NodeSelectorOpExists,
		}
	case PartitioningModePartitioned:
		return &corev1.NodeSelectorRequirement{
			Key:      PartitioningSchemeLabelPrefix + PartitioningSchemeDefault,
			Operator: corev1.NodeSelectorOpDoesNotExist,
		}
	default:
		return &corev1.NodeSelectorRequirement{
			Key:      PartitioningSchemeLabelPrefix + mode,
			Operator: corev1.NodeSelectorOpExists,
		}
	}
}

// canonicalizePartitioningMode normalizes a user-entered partitioning mode so
// matching is case-insensitive. The reserved sentinels collapse to their
// canonical lowercase form ("unpartitioned"/"partitioned"); every other value
// is a scheme and is upper-cased to line up with the partition labels the
// AcceleratorDetector publishes — amd-smi axis values are always emitted
// upper-case (see config/accelerator-detector/scripts/detect-and-label.py
// `_clean_axis`). Without this, a profile written as "cpx-nps4" would build
// affinity on partitioning-scheme.cpx-nps4 and silently match zero nodes.
func canonicalizePartitioningMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "":
		return ""
	case PartitioningModeUnpartitioned:
		return PartitioningModeUnpartitioned
	case PartitioningModePartitioned:
		return PartitioningModePartitioned
	default:
		return strings.ToUpper(strings.TrimSpace(mode))
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

// nodeMatchesPartitioning evaluates the partition term (already resolved by
// partitionNodeSelectorRequirement) against a node's labels, mirroring its
// Exists / DoesNotExist semantics. A nil requirement (non-GPU profile) matches
// every node.
func nodeMatchesPartitioning(node *corev1.Node, req *corev1.NodeSelectorRequirement) bool {
	if req == nil {
		return true
	}
	_, exists := node.Labels[req.Key]
	if req.Operator == corev1.NodeSelectorOpDoesNotExist {
		return !exists
	}
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
