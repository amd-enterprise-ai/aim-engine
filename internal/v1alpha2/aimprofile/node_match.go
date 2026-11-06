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
	"sort"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
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

	// AcceleratorVendorLabelPrefix is the per-accelerator-type vendor axis
	// published by the AcceleratorDetector:
	//   feature.node.kubernetes.io/aim-accelerator.vendor.GPU.AMD=8
	//   feature.node.kubernetes.io/aim-accelerator.vendor.GPU.NVIDIA=8
	AcceleratorVendorLabelPrefix = AcceleratorLabelPrefix + "vendor."

	// PartitioningSchemeLabelPrefix is the single partition axis published by
	// the AcceleratorDetector. Keys are either the `default` sentinel or a
	// literal `<Compute>-<Memory>` scheme:
	//   feature.node.kubernetes.io/aim-accelerator.partitioning-scheme.default=8
	//   feature.node.kubernetes.io/aim-accelerator.partitioning-scheme.CPX-NPS4=64
	// Like the model labels, values are informational; the resolver matches on
	// key existence via Exists / DoesNotExist.
	PartitioningSchemeLabelPrefix = AcceleratorLabelPrefix + "partitioning-scheme."

	// MixedModelsLabelPrefix is published by the AcceleratorDetector on nodes
	// carrying more than one model of an accelerator type; the value is the
	// number of distinct models:
	//   feature.node.kubernetes.io/aim-accelerator.mixed-models.GPU=2
	MixedModelsLabelPrefix = AcceleratorLabelPrefix + "mixed-models."

	// PartitioningSchemeDefault is the hardware-agnostic sentinel key suffix
	// stamped on canonical-unpartitioned and non-partitionable nodes.
	PartitioningSchemeDefault = "default"

	// PartitioningModeUnpartitioned selects unpartitioned hardware (the CRD default).
	PartitioningModeUnpartitioned = "unpartitioned"
	// PartitioningModePartitioned selects any actively-partitioned mode.
	PartitioningModePartitioned = "partitioned"
)

const maxRecordedNodeMismatches = 5

// NodeMismatch records why one node did not satisfy a profile's hardware
// requirements. Reasons follow the same checks used to calculate
// MatchingNodes, so diagnostics cannot disagree with scheduling affinity.
type NodeMismatch struct {
	NodeName string
	Reasons  []string
}

// NodeMatchResult holds the result of matching a profile against cluster nodes.
type NodeMatchResult struct {
	// MatchingNodes counts the matching nodes that present their GPUs as
	// device-plugin resources, and matching nodes of profiles without GPUs.
	MatchingNodes int32
	// DRAMatchingNodes counts the matching nodes whose GPUs are published
	// through the AMD GPU DRA driver.
	DRAMatchingNodes int32
	// BothMatchingNodes counts the nodes included in both counts above: DRA
	// nodes that also advertise amd.com/gpu.
	BothMatchingNodes int32
	NodeAffinity      *corev1.NodeAffinity
	NodeMismatches    []NodeMismatch
	OmittedMismatches int
}

// ResolveResources merges the accelerator-derived device request with explicit
// resources using the legacy accelerator contract. Empty vendor preserves the
// historical AMD whole-GPU resource name.
//
// New profile reconciliation should use ResolveProfileResources so the vendor
// and partitioning axes participate in resource-name resolution. GPU profiles
// also receive the host CPU and memory defaults used by the v1alpha1 service
// path, with explicit resources remaining authoritative per key.
func ResolveResources(
	accelType aimv1alpha1.AcceleratorType,
	accelCount int32,
	resources *corev1.ResourceRequirements,
	acceleratorModel string,
	engineEnv map[string]string,
) *corev1.ResourceRequirements {
	return ResolveResourcesForPartition(
		accelType,
		accelCount,
		resources,
		acceleratorModel,
		engineEnv,
		PartitioningModeUnpartitioned,
	)
}

// ResolveResourcesForPartition is ResolveResources with partition-aware GPU
// host defaults under the legacy empty-vendor contract. AcceleratorCount
// remains the number of schedulable device units requested. For a recognized
// AMD compute partition scheme, host CPU and memory defaults are divided by
// the number of slices per physical GPU. Generic "partitioned" and unknown
// schemes omit host defaults because their slice geometry cannot be inferred
// safely.
func ResolveResourcesForPartition(
	accelType aimv1alpha1.AcceleratorType,
	accelCount int32,
	resources *corev1.ResourceRequirements,
	acceleratorModel string,
	engineEnv map[string]string,
	partitioningMode string,
) *corev1.ResourceRequirements {
	return resolveResources(
		accelType,
		"",
		partitioningMode,
		accelCount,
		resources,
		acceleratorModel,
		engineEnv,
	)
}

// ResolveProfileResources merges the accelerator-derived device request with
// explicit resources using all profile hardware axes.
func ResolveProfileResources(spec aimv1alpha2.AIMProfileSpecCommon) *corev1.ResourceRequirements {
	return resolveResources(
		spec.AcceleratorType,
		spec.AcceleratorVendor,
		spec.AcceleratorPartitioningMode,
		spec.AcceleratorCount,
		spec.Resources,
		spec.AcceleratorModel,
		spec.EngineEnv,
	)
}

// resolveResources translates accelerator count to a Kubernetes resource name:
//
//	gpu → vendor/partition-specific extended resource
//	cpu → corev1.ResourceCPU
//
// For EPYC CPU profiles (acceleratorModel starts with "EPYC"), memory is derived from
// the engine env var VLLM_CPU_KVCACHE_SPACE (doubled) to enable Guaranteed QoS pods.
//
// If spec.resources already contains the derived resource name, the explicit value wins.
// Returns nil only when both accelerator count is zero and resources is nil.
func resolveResources(
	accelType aimv1alpha1.AcceleratorType,
	acceleratorVendor aimv1alpha1.AcceleratorVendor,
	partitioningMode string,
	accelCount int32,
	resources *corev1.ResourceRequirements,
	acceleratorModel string,
	engineEnv map[string]string,
) *corev1.ResourceRequirements {
	derivedName, derivedQty := acceleratorDeviceRequest(accelType, acceleratorVendor, partitioningMode, accelCount)

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
		requestQty, requestExists := resolved.Requests[derivedName]
		limitQty, limitExists := resolved.Limits[derivedName]
		if !requestExists {
			requestQty = derivedQty
			if limitExists {
				if accelType == aimv1alpha1.AcceleratorTypeGPU || limitQty.Cmp(requestQty) < 0 {
					requestQty = limitQty.DeepCopy()
				}
			}
			resolved.Requests[derivedName] = requestQty
		}
		// Mirror requests into limits for both GPU and CPU accelerator types.
		// GPU device resources are non-overcommitable (K8s enforces requests==limits).
		// CPU limits are set to enable Guaranteed QoS on EPYC pods (memory
		// limits are added separately below for EPYC profiles).
		if accelType == aimv1alpha1.AcceleratorTypeGPU || accelType == aimv1alpha1.AcceleratorTypeCPU {
			if resolved.Limits == nil {
				resolved.Limits = make(corev1.ResourceList)
			}
			if !limitExists {
				resolved.Limits[derivedName] = requestQty.DeepCopy()
			}
		}
	}

	if accelType == aimv1alpha1.AcceleratorTypeGPU && accelCount > 0 {
		applyDefaultGPUResources(resolved, int64(accelCount), partitioningMode)
	}

	// EPYC CPU profiles: derive memory from VLLM_CPU_KVCACHE_SPACE to enable
	// Guaranteed QoS (requests==limits for both CPU and memory). Explicit
	// spec.resources memory takes precedence.
	if accelType == aimv1alpha1.AcceleratorTypeCPU && strings.HasPrefix(acceleratorModel, "EPYC") {
		if _, exists := resolved.Requests[corev1.ResourceMemory]; !exists {
			memGi := deriveEPYCMemoryGi(engineEnv)
			memQty := resource.MustParse(fmt.Sprintf("%dGi", memGi))
			resolved.Requests[corev1.ResourceMemory] = memQty
			if resolved.Limits == nil {
				resolved.Limits = make(corev1.ResourceList)
			}
			resolved.Limits[corev1.ResourceMemory] = memQty
		}
	}

	return resolved
}

// applyDefaultGPUResources fills host CPU and memory defaults for GPU profiles.
// Existing entries are preserved so profile and service overrides remain
// authoritative per resource key. A generated request never exceeds an
// explicit limit, and a generated memory limit is never below an explicit
// request. CPU intentionally has no generated default limit.
func applyDefaultGPUResources(resources *corev1.ResourceRequirements, acceleratorCount int64, partitioningMode string) {
	slicesPerGPU := gpuSlicesPerPhysicalGPU(partitioningMode)
	if slicesPerGPU == 0 {
		return
	}

	if resources.Requests == nil {
		resources.Requests = make(corev1.ResourceList)
	}
	if _, exists := resources.Requests[corev1.ResourceCPU]; !exists {
		defaultCPU := resource.NewMilliQuantity(
			divideRoundUp(acceleratorCount*constants.DefaultCPURequestPerGPU*1000, slicesPerGPU),
			resource.DecimalSI,
		)
		resources.Requests[corev1.ResourceCPU] = quantityAtMostExplicitLimit(
			*defaultCPU,
			resources.Limits,
			corev1.ResourceCPU,
		)
	}
	if _, exists := resources.Requests[corev1.ResourceMemory]; !exists {
		defaultMemory := resource.MustParse(fmt.Sprintf(
			"%dGi",
			acceleratorCount*constants.DefaultMemoryRequestGiPerGPU/slicesPerGPU,
		))
		resources.Requests[corev1.ResourceMemory] = quantityAtMostExplicitLimit(
			defaultMemory,
			resources.Limits,
			corev1.ResourceMemory,
		)
	}

	if resources.Limits == nil {
		resources.Limits = make(corev1.ResourceList)
	}
	if _, exists := resources.Limits[corev1.ResourceMemory]; !exists {
		defaultMemoryLimit := resource.MustParse(fmt.Sprintf(
			"%dGi",
			acceleratorCount*constants.DefaultMemoryLimitGiPerGPU/slicesPerGPU,
		))
		if request, exists := resources.Requests[corev1.ResourceMemory]; exists && request.Cmp(defaultMemoryLimit) > 0 {
			defaultMemoryLimit = request.DeepCopy()
		}
		resources.Limits[corev1.ResourceMemory] = defaultMemoryLimit
	}
}

// gpuSlicesPerPhysicalGPU returns the number of logical accelerator units
// exposed by one physical GPU for a recognized AMD compute partition mode.
// The memory mode does not change the logical-device count. Zero means the
// geometry is not known well enough to derive host defaults safely.
func gpuSlicesPerPhysicalGPU(partitioningMode string) int64 {
	mode := strings.ToUpper(strings.TrimSpace(partitioningMode))
	switch mode {
	case "", strings.ToUpper(PartitioningModeUnpartitioned):
		return 1
	case strings.ToUpper(PartitioningModePartitioned):
		return 0
	}

	computeMode, _, hasMemoryMode := strings.Cut(mode, "-")
	if !hasMemoryMode {
		return 0
	}
	switch computeMode {
	case "SPX":
		return 1
	case "DPX":
		return 2
	case "QPX":
		return 4
	case "CPX":
		return 8
	default:
		return 0
	}
}

func quantityAtMostExplicitLimit(
	defaultQty resource.Quantity,
	limits corev1.ResourceList,
	name corev1.ResourceName,
) resource.Quantity {
	if limit, exists := limits[name]; exists && limit.Cmp(defaultQty) < 0 {
		return limit.DeepCopy()
	}
	return defaultQty
}

func divideRoundUp(value, divisor int64) int64 {
	return (value + divisor - 1) / divisor
}

const defaultEPYCMemoryGi int64 = 120

// deriveEPYCMemoryGi computes memory in GiB for EPYC CPU profiles by
// doubling VLLM_CPU_KVCACHE_SPACE (the KV-cache reservation already
// accounts for roughly half of useful runtime memory). Falls back to
// defaultEPYCMemoryGi when the env var is absent or unparseable.
func deriveEPYCMemoryGi(engineEnv map[string]string) int64 {
	if v, ok := engineEnv["VLLM_CPU_KVCACHE_SPACE"]; ok {
		if gi, err := strconv.ParseInt(v, 10, 64); err == nil && gi > 0 {
			return gi * 2
		}
	}
	return defaultEPYCMemoryGi
}

// acceleratorDeviceRequest returns the K8s resource name and quantity derived from the
// accelerator type and count. Returns empty name when no device request can be derived.
//
// RESOURCE-NAMING SEAM: gpuResourceName is the only mapping from the profile's
// vendor/partition contract to a device-plugin resource. Whole-device NVIDIA
// and AMD are implemented now. Future AMD partition resources such as
// amd.com/cpx_nps4 belong in that function without changing resource merge,
// node-capacity, or runtime projection code.
func acceleratorDeviceRequest(
	accelType aimv1alpha1.AcceleratorType,
	acceleratorVendor aimv1alpha1.AcceleratorVendor,
	partitioningMode string,
	accelCount int32,
) (corev1.ResourceName, resource.Quantity) {
	if accelCount <= 0 {
		return "", resource.Quantity{}
	}

	switch accelType {
	case aimv1alpha1.AcceleratorTypeGPU:
		name := gpuResourceName(acceleratorVendor, partitioningMode)
		if name == "" {
			return "", resource.Quantity{}
		}
		return name, *resource.NewQuantity(int64(accelCount), resource.DecimalSI)
	case aimv1alpha1.AcceleratorTypeCPU:
		return corev1.ResourceCPU, *resource.NewQuantity(int64(accelCount), resource.DecimalSI)
	default:
		return "", resource.Quantity{}
	}
}

// gpuResourceName resolves the device-plugin extended resource for one GPU
// unit. Empty vendor is the backwards-compatible AMD contract.
func gpuResourceName(vendor aimv1alpha1.AcceleratorVendor, partitioningMode string) corev1.ResourceName {
	switch vendor {
	case "", aimv1alpha1.AcceleratorVendorAMD:
		// Today all AMD partition modes use the single-resource strategy.
		// When mixed resource naming is enabled, canonicalizePartitioningMode
		// here and map concrete schemes (for example CPX-NPS4) to resources
		// such as amd.com/cpx_nps4.
		_ = partitioningMode
		return corev1.ResourceName(constants.AMDGPUResourceName)
	case aimv1alpha1.AcceleratorVendorNVIDIA:
		return corev1.ResourceName(constants.NVIDIAGPUResourceName)
	default:
		return ""
	}
}

// MatchNodes checks how many nodes in the list satisfy the accelerator label
// requirements (model +, for GPU profiles, partition), and the resolved
// resource capacity requirements of a profile.
func MatchNodes(nodes []corev1.Node, accelType aimv1alpha1.AcceleratorType, accelModel, partitioningMode string, resolvedResources *corev1.ResourceRequirements) NodeMatchResult {
	return matchNodes(NodesOnly(nodes), accelType, "", accelModel, partitioningMode, resolvedResources)
}

// MatchProfileNodes checks node compatibility using all profile hardware axes.
func MatchProfileNodes(inventory NodeInventory, spec aimv1alpha2.AIMProfileSpecCommon, resolvedResources *corev1.ResourceRequirements) NodeMatchResult {
	return matchNodes(
		inventory,
		spec.AcceleratorType,
		spec.AcceleratorVendor,
		spec.AcceleratorModel,
		spec.AcceleratorPartitioningMode,
		resolvedResources,
	)
}

func matchNodes(
	inventory NodeInventory,
	accelType aimv1alpha1.AcceleratorType,
	accelVendor aimv1alpha1.AcceleratorVendor,
	accelModel, partitioningMode string,
	resolvedResources *corev1.ResourceRequirements,
) NodeMatchResult {
	affinity := buildNodeAffinity(accelType, accelVendor, accelModel, partitioningMode)
	vendorReq := vendorNodeSelectorRequirement(accelType, accelVendor)
	partitionReq := partitionNodeSelectorRequirement(accelType, partitioningMode)
	mixedModelsReq := mixedModelsNodeSelectorRequirement(accelType)
	draReq := newDRARequirement(accelType, accelVendor, accelModel, partitioningMode, resolvedResources)

	var result NodeMatchResult
	result.NodeAffinity = affinity
	for i := range inventory.Nodes {
		node := &inventory.Nodes[i]
		var reasons []string
		draMatch, devicePluginMatch := false, false
		devices, draNode := inventory.DRADevices[node.Name]
		if draNode && draReq != nil {
			reasons = draReq.mismatchReasons(node, devices)
			draMatch = len(reasons) == 0
		}
		// A node can publish DRA devices while amd.com/gpu is still allocatable,
		// for example while it moves from the device plugin to the DRA driver.
		if !draNode || draReq == nil || advertisesAMDGPUs(node) {
			devicePluginReasons := nodeMismatchReasons(
				node,
				vendorReq,
				accelModel,
				partitionReq,
				mixedModelsReq,
				resolvedResources,
			)
			devicePluginMatch = len(devicePluginReasons) == 0
			reasons = append(reasons, devicePluginReasons...)
		}
		if draMatch {
			result.DRAMatchingNodes++
		}
		if devicePluginMatch {
			result.MatchingNodes++
		}
		if draMatch && devicePluginMatch {
			result.BothMatchingNodes++
		}
		if draMatch || devicePluginMatch {
			continue
		}
		if len(result.NodeMismatches) < maxRecordedNodeMismatches {
			result.NodeMismatches = append(result.NodeMismatches, NodeMismatch{
				NodeName: node.Name,
				Reasons:  reasons,
			})
		} else {
			result.OmittedMismatches++
		}
	}
	return result
}

func nodeMismatchReasons(
	node *corev1.Node,
	vendorReq *corev1.NodeSelectorRequirement,
	accelModel string,
	partitionReq *corev1.NodeSelectorRequirement,
	mixedModelsReq *corev1.NodeSelectorRequirement,
	resolvedResources *corev1.ResourceRequirements,
) []string {
	var reasons []string
	if !nodeMatchesVendor(node, vendorReq) {
		reasons = append(reasons, fmt.Sprintf("required node label %q is missing", vendorReq.Key))
	}
	if !nodeMatchesAccelerator(node, accelModel) {
		reasons = append(reasons, fmt.Sprintf(
			"required node label %q is missing",
			AcceleratorLabelPrefix+accelModel,
		))
	}
	if !nodeMatchesPartitioning(node, partitionReq) {
		if partitionReq.Operator == corev1.NodeSelectorOpDoesNotExist {
			reasons = append(reasons, fmt.Sprintf("node label %q must be absent", partitionReq.Key))
		} else {
			reasons = append(reasons, fmt.Sprintf("required node label %q is missing", partitionReq.Key))
		}
	}
	reasons = append(reasons, resourceMismatchReasons(node, resolvedResources)...)
	// Reported only when it is the node's only mismatch, so a node that could
	// never serve the profile isn't explained by its mix of models.
	if len(reasons) == 0 && mixedModelsReq != nil {
		if _, mixed := node.Labels[mixedModelsReq.Key]; mixed {
			reasons = append(reasons, fmt.Sprintf(
				"node carries more than one GPU model (label %q): device-plugin scheduling "+
					"cannot choose which model a pod receives, so GPU workloads are not "+
					"scheduled onto this node",
				mixedModelsReq.Key,
			))
		}
	}
	return reasons
}

func resourceMismatchReasons(node *corev1.Node, resources *corev1.ResourceRequirements) []string {
	if resources == nil || len(resources.Requests) == 0 {
		return nil
	}

	names := make([]string, 0, len(resources.Requests))
	for name := range resources.Requests {
		names = append(names, string(name))
	}
	sort.Strings(names)

	var reasons []string
	for _, rawName := range names {
		name := corev1.ResourceName(rawName)
		requested := resources.Requests[name]
		allocatable, exists := node.Status.Allocatable[name]
		// Capacity is enforced only when the requested resource is actually
		// reported in Allocatable. Preserve the existing labels-only behavior
		// during device-plugin startup races and in synthetic test clusters:
		// kubelet still prevents a real pod from consuming a missing resource.
		if !exists || allocatable.Cmp(requested) >= 0 {
			continue
		}
		reasons = append(reasons, fmt.Sprintf(
			"resource %s requested %s, allocatable %s",
			name,
			requested.String(),
			allocatable.String(),
		))
	}
	return reasons
}

// BuildNodeAffinity constructs a corev1.NodeAffinity from the accelerator model
// and, for GPU profiles, the partitioning mode and the mixed-models exclusion.
// The terms are AND-ed into a single NodeSelectorTerm so the resulting affinity
// is "model AND partition AND not mixed-models". Returns nil when the profile
// has neither an accelerator model nor the GPU accelerator type.
func BuildNodeAffinity(accelType aimv1alpha1.AcceleratorType, accelModel, partitioningMode string) *corev1.NodeAffinity {
	return buildNodeAffinity(accelType, "", accelModel, partitioningMode)
}

// BuildProfileNodeAffinity constructs node affinity using all profile hardware axes.
func BuildProfileNodeAffinity(spec aimv1alpha2.AIMProfileSpecCommon) *corev1.NodeAffinity {
	return buildNodeAffinity(spec.AcceleratorType, spec.AcceleratorVendor, spec.AcceleratorModel, spec.AcceleratorPartitioningMode)
}

func buildNodeAffinity(
	accelType aimv1alpha1.AcceleratorType,
	accelVendor aimv1alpha1.AcceleratorVendor,
	accelModel, partitioningMode string,
) *corev1.NodeAffinity {
	var exprs []corev1.NodeSelectorRequirement
	if req := vendorNodeSelectorRequirement(accelType, accelVendor); req != nil {
		exprs = append(exprs, *req)
	}
	if accelModel != "" {
		exprs = append(exprs, corev1.NodeSelectorRequirement{
			Key:      AcceleratorLabelPrefix + accelModel,
			Operator: corev1.NodeSelectorOpExists,
		})
	}
	if len(exprs) > 0 {
		if req := partitionNodeSelectorRequirement(accelType, partitioningMode); req != nil {
			exprs = append(exprs, *req)
		}
	}
	if req := mixedModelsNodeSelectorRequirement(accelType); req != nil {
		exprs = append(exprs, *req)
	}
	if len(exprs) == 0 {
		return nil
	}

	return &corev1.NodeAffinity{
		RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
			NodeSelectorTerms: []corev1.NodeSelectorTerm{
				{MatchExpressions: exprs},
			},
		},
	}
}

// mixedModelsNodeSelectorRequirement keeps GPU profiles off nodes that carry
// several GPU models. Device-plugin resources (amd.com/gpu, nvidia.com/gpu) are
// a single pool per node, so a pod on such a node may receive GPUs of a model
// other than the one its profile targets, or a mix of models. Only DRA claims
// can select devices by model within a node.
func mixedModelsNodeSelectorRequirement(accelType aimv1alpha1.AcceleratorType) *corev1.NodeSelectorRequirement {
	if accelType != aimv1alpha1.AcceleratorTypeGPU {
		return nil
	}
	return &corev1.NodeSelectorRequirement{
		Key:      MixedModelsLabelPrefix + strings.ToUpper(string(accelType)),
		Operator: corev1.NodeSelectorOpDoesNotExist,
	}
}

// vendorNodeSelectorRequirement maps acceleratorVendor onto the detector's
// per-type vendor label. Empty vendor preserves legacy model-only matching.
func vendorNodeSelectorRequirement(
	accelType aimv1alpha1.AcceleratorType,
	vendor aimv1alpha1.AcceleratorVendor,
) *corev1.NodeSelectorRequirement {
	if vendor == "" || accelType == "" {
		return nil
	}
	return &corev1.NodeSelectorRequirement{
		Key: AcceleratorVendorLabelPrefix +
			strings.ToUpper(string(accelType)) + "." +
			strings.ToUpper(string(vendor)),
		Operator: corev1.NodeSelectorOpExists,
	}
}

// advertisesAMDGPUs reports whether the node has allocatable amd.com/gpu.
func advertisesAMDGPUs(node *corev1.Node) bool {
	quantity, ok := node.Status.Allocatable[corev1.ResourceName(constants.AMDGPUResourceName)]
	return ok && !quantity.IsZero()
}

// nodeMatchesVendor evaluates a resolved vendor selector against node labels.
func nodeMatchesVendor(node *corev1.Node, req *corev1.NodeSelectorRequirement) bool {
	if req == nil {
		return true
	}
	_, exists := node.Labels[req.Key]
	return exists
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

// FormatProfileHardwareSummary includes the vendor when a profile intentionally
// targets a generic accelerator class without naming a concrete model.
func FormatProfileHardwareSummary(spec aimv1alpha2.AIMProfileSpecCommon) string {
	if spec.AcceleratorModel != "" {
		return FormatHardwareSummary(spec.AcceleratorModel, spec.AcceleratorCount)
	}
	if spec.AcceleratorType == aimv1alpha1.AcceleratorTypeGPU {
		name := "GPU"
		switch spec.AcceleratorVendor {
		case aimv1alpha1.AcceleratorVendorAMD:
			name = "AMD GPU"
		case aimv1alpha1.AcceleratorVendorNVIDIA:
			name = "NVIDIA GPU"
		}
		if spec.AcceleratorCount > 0 {
			return fmt.Sprintf("%d x %s", spec.AcceleratorCount, name)
		}
		return name
	}
	return FormatHardwareSummary(spec.AcceleratorModel, spec.AcceleratorCount)
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

// HasProfileAcceleratorRequirement reports whether any profile hardware axis
// requires accelerator-aware matching.
func HasProfileAcceleratorRequirement(spec aimv1alpha2.AIMProfileSpecCommon) bool {
	return spec.AcceleratorVendor != "" ||
		HasAcceleratorRequirement(spec.AcceleratorModel, spec.AcceleratorCount, spec.Resources)
}
