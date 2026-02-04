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

package utils

import (
	"context"
	"regexp"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GPU node label keys for AMD GPUs.
const (
	// LabelAMDGPUDeviceID is the primary label for AMD GPU device identification.
	// Values are PCI device IDs (e.g., "74a1" for MI300X).
	LabelAMDGPUDeviceID = "amd.com/gpu.device-id"

	// LabelAMDGPUDeviceIDBeta is the beta version of the device ID label.
	LabelAMDGPUDeviceIDBeta = "beta.amd.com/gpu.device-id"

	// LabelAMDGPUFamily is the label for AMD GPU family (e.g., "AI", "NV").
	LabelAMDGPUFamily = "amd.com/gpu.family"

	// LabelAMDGPUFamilyBeta is the beta version of the family label.
	LabelAMDGPUFamilyBeta = "beta.amd.com/gpu.family"

	// LabelAMDGPUVRAM is the label for AMD GPU VRAM capacity (e.g., "192G").
	LabelAMDGPUVRAM = "amd.com/gpu.vram"

	// LabelAMDGPUVRAMBeta is the beta version of the VRAM label.
	LabelAMDGPUVRAMBeta = "beta.amd.com/gpu.vram"
)

// GPU node label keys for NVIDIA GPUs.
const (
	// LabelNVIDIAGPUProduct is the label for NVIDIA GPU product name.
	LabelNVIDIAGPUProduct = "nvidia.com/gpu.product"

	// LabelNVIDIAMIGProduct is the label for NVIDIA MIG product name.
	LabelNVIDIAMIGProduct = "nvidia.com/mig.product"

	// LabelNVIDIAGPUFamily is the label for NVIDIA GPU family.
	LabelNVIDIAGPUFamily = "nvidia.com/gpu.family"

	// LabelNFDNVIDIAGPUModel is the Node Feature Discovery label for NVIDIA GPU model.
	LabelNFDNVIDIAGPUModel = "feature.node.kubernetes.io/nvidia-gpu-model"
)

// GPU resource name prefixes.
const (
	// ResourcePrefixAMD is the resource name prefix for AMD GPUs.
	ResourcePrefixAMD = "amd.com/"

	// ResourcePrefixNVIDIA is the resource name prefix for NVIDIA GPUs.
	ResourcePrefixNVIDIA = "nvidia.com/"
)

// VRAM source values for GetGPUVRAM function.
const (
	VRAMSourceLabel   = "label"
	VRAMSourceStatic  = "static"
	VRAMSourceUnknown = "unknown"
)

// KnownAmdDevices maps AMD GPU device IDs (PCI device IDs) to their commercial model names.
// This mapping is used to identify GPU models from node labels when the device ID is available.
// Device IDs are typically exposed by AMD GPU labelers (e.g., amd.com/gpu.device-id).
//
// The mapping includes:
//   - AMD Instinct accelerators (MI series): MI100, MI210, MI250X, MI300X, MI308X, MI325X, MI350X, MI355X
//   - AMD Radeon Pro workstation GPUs: W6800, W6900X, W7800, W7900, V620, V710
//   - AMD Radeon gaming GPUs: RX6800, RX6900, RX7900, RX9070
//
// Note: Some device IDs map to the same model (e.g., multiple MI300X variants).
// Device IDs may represent different variants, revisions, or virtualization flavors (MxGPU, VF, HF).
var KnownAmdDevices = map[string]string{
	// AMD Instinct
	"738c": "MI100",
	"738e": "MI100",
	"7408": "MI250X",
	"740c": "MI250X", // MI250/MI250X
	"740f": "MI210",
	"7410": "MI210", // MI210 VF
	"74a0": "MI300A",
	"74a1": "MI300X",
	"74a2": "MI308X",
	"74a5": "MI325X",
	"74a8": "MI308X", // MI308X HF
	"74a9": "MI300X", // MI300X HF
	"74b5": "MI300X", // MI300X VF
	"74b6": "MI308X",
	"74b9": "MI325X", // MI325X VF
	"74bd": "MI300X", // MI300X HF
	"75a0": "MI350X",
	"75a3": "MI355X",
	"75b0": "MI350X", // MI350X VF
	"75b3": "MI355X", // MI355X VF
	// AMD Radeon Pro
	"7460": "V710",
	"7461": "V710", // Radeon Pro V710 MxGPU
	"7448": "W7900",
	"744a": "W7900", // W7900 Dual Slot
	"7449": "W7800", // W7800 48GB
	"745e": "W7800",
	"73a2": "W6900X",
	"73a3": "W6800",  // W6800 GL-XL
	"73ab": "W6800X", // W6800X / W6800X Duo
	"73a1": "V620",
	"73ae": "V620", // Radeon Pro V620 MxGPU
	// AMD Radeon
	"7550": "RX9070", // RX 9070 / 9070 XT
	"744c": "RX7900", // RX 7900 XT / 7900 XTX / 7900 GRE / 7900M
	"73af": "RX6900",
	"73bf": "RX6800", // RX 6800 / 6800 XT / 6900 XT
}

// KnownGPUVRAM provides fallback VRAM values when node labels are unavailable.
// Values are per-GPU VRAM capacity in the format used by AMD device plugin labels (e.g., "192G").
// This mapping is used when amd.com/gpu.vram or beta.amd.com/gpu.vram labels are not present.
var KnownGPUVRAM = map[string]string{
	// AMD Instinct (AI/HPC accelerators)
	"MI355X": "288G",
	"MI350X": "288G",
	"MI325X": "256G",
	"MI308X": "128G",
	"MI300X": "192G",
	"MI300A": "128G",
	"MI250X": "128G", // 64G per die × 2
	"MI210":  "64G",
	"MI100":  "32G",
	// AMD Radeon Pro (workstation)
	"V710":   "32G",
	"W7900":  "48G",
	"W7800":  "32G",
	"W6900X": "32G",
	"W6800":  "32G",
	"W6800X": "32G",
	"V620":   "32G",
	// AMD Radeon (consumer)
	"RX9070": "16G",
	"RX7900": "24G",
	"RX6900": "16G",
	"RX6800": "16G",
	// NVIDIA (for future support)
	"H200":      "141G",
	"H100":      "80G",
	"A100":      "80G",
	"A100-40GB": "40G",
	"L40S":      "48G",
	"L4":        "24G",
}

// KnownVRAMTiers is a sorted list of all known VRAM capacity values.
// Used for filtering GPUs by minimum VRAM requirement.
var KnownVRAMTiers = []string{
	"16G", "24G", "32G", "40G", "48G", "64G", "80G", "128G", "141G", "192G", "256G", "288G",
}

// GPUResourceInfo contains GPU resource information for a specific GPU model.
type GPUResourceInfo struct {
	// ResourceName is the full Kubernetes resource name (e.g., "amd.com/gpu").
	ResourceName string

	// VRAM is the GPU VRAM capacity in the format used by device plugin labels (e.g., "192G").
	// Empty string if VRAM information is not available.
	VRAM string

	// VRAMSource indicates how the VRAM value was determined:
	// "label" = from node label, "static" = from KnownGPUVRAM, "unknown" = not available.
	VRAMSource string
}

// GetClusterGPUResources returns an aggregated view of all GPU resources in the cluster.
// It scans all nodes and aggregates resources that start with "amd.com/" or "nvidia.com/".
// Returns a map where keys are GPU models (e.g., "MI300X", "A100") extracted from node labels,
// and values contain the resource name.
func GetClusterGPUResources(ctx context.Context, k8sClient client.Client) (map[string]GPUResourceInfo, error) {
	// List all nodes in the cluster
	var nodes corev1.NodeList
	if err := k8sClient.List(ctx, &nodes); err != nil {
		return nil, err
	}

	// Aggregate GPU resources by model
	gpuResources := make(map[string]GPUResourceInfo)

	for _, node := range nodes.Items {
		// Process GPU resources on this node
		filterGPULabelResources(&node, gpuResources)
	}

	return gpuResources, nil
}

// ExtractGPUModelFromNodeLabels extracts the GPU model from node labels.
// Supports multiple label formats from AMD and NVIDIA GPU labellers:
//   - AMD: amd.com/gpu.device-id (primary), beta.amd.com/gpu.device-id, amd.com/gpu.family,
//     and count-encoded variants (e.g., amd.com/gpu.device-id.74a1=4)
//   - NVIDIA: nvidia.com/gpu.product, nvidia.com/mig.product, nvidia.com/gpu.family,
//     and Node Feature Discovery labels (feature.node.kubernetes.io/nvidia-gpu-model)
//
// Returns a normalized GPU model name (e.g., "MI300X", "A100") or empty string if model cannot be determined.
// Nodes with GPU resources but insufficient labels will be excluded from template matching.
func ExtractGPUModelFromNodeLabels(labels map[string]string, resourceName string) string {
	if strings.HasPrefix(resourceName, ResourcePrefixAMD) {
		return ExtractAMDModel(labels)
	}

	if strings.HasPrefix(resourceName, ResourcePrefixNVIDIA) {
		return extractNvidiaModel(labels)
	}

	return ""
}

// NormalizeGPUModel normalizes GPU model names for consistency.
// Examples:
//   - "A100-SXM4-40GB" -> "A100"
//   - "MI300X (rev 2)" -> "MI300X"
//   - "Tesla-T4-SHARED" -> "T4"
func NormalizeGPUModel(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}

	model = strings.ToUpper(strings.ReplaceAll(model, "_", "-"))

	tokens := strings.FieldsFunc(model, func(r rune) bool {
		switch r {
		case '-', ' ', '/', ':':
			return true
		default:
			return false
		}
	})

	if token := pickGPUModelToken(tokens); token != "" {
		return token
	}

	for _, token := range tokens {
		if token != "" {
			return token
		}
	}

	return model
}

// MapAMDDeviceIDToModel maps AMD device IDs to model names.
// Comprehensive mapping covering AMD Instinct, Radeon Pro, and Radeon GPUs.
func MapAMDDeviceIDToModel(deviceID string) string {
	// Remove "0x" prefix if present
	deviceID = strings.TrimPrefix(strings.ToLower(deviceID), "0x")

	if model, ok := KnownAmdDevices[deviceID]; ok {
		return model
	}

	return "AMD-" + strings.ToUpper(deviceID)
}

// ExtractAMDModel extracts the AMD GPU model name from node labels.
// It tries multiple label sources in order of preference:
//  1. Device ID labels (amd.com/gpu.device-id or beta.amd.com/gpu.device-id) - most accurate
//  2. Count-encoded device ID labels (e.g., amd.com/gpu.device-id.74a1=4)
//  3. GPU family labels (amd.com/gpu.family or beta.amd.com/gpu.family)
//  4. Count-encoded GPU family labels
//
// Returns a normalized GPU model name (e.g., "MI300X") or empty string if not found.
// Device IDs are mapped using KnownAmdDevices for precise identification.
func ExtractAMDModel(labels map[string]string) string {
	// Primary method: Use device ID for accurate model identification
	if deviceID := labelValue(labels, LabelAMDGPUDeviceID, LabelAMDGPUDeviceIDBeta); deviceID != "" {
		return MapAMDDeviceIDToModel(deviceID)
	}
	if deviceKey := extractLabelSuffix(labels, LabelAMDGPUDeviceID+".", LabelAMDGPUDeviceIDBeta+"."); deviceKey != "" {
		return MapAMDDeviceIDToModel(deviceKey)
	}

	// GPU family (AI, NV, etc.) as a last resort
	if family := labelValue(labels, LabelAMDGPUFamily, LabelAMDGPUFamilyBeta); family != "" {
		return NormalizeGPUModel(family)
	}
	if familyKey := extractLabelSuffix(labels, LabelAMDGPUFamily+".", LabelAMDGPUFamilyBeta+"."); familyKey != "" {
		return NormalizeGPUModel(familyKey)
	}

	return ""
}

func extractNvidiaModel(labels map[string]string) string {
	if product := labelValue(labels, LabelNVIDIAGPUProduct, LabelNVIDIAMIGProduct); product != "" {
		return NormalizeGPUModel(product)
	}
	if productKey := extractLabelSuffix(labels, LabelNVIDIAGPUProduct+".", LabelNVIDIAMIGProduct+"."); productKey != "" {
		return NormalizeGPUModel(productKey)
	}
	if family := labelValue(labels, LabelNVIDIAGPUFamily); family != "" {
		return NormalizeGPUModel(family)
	}
	if familyKey := extractLabelSuffix(labels, LabelNVIDIAGPUFamily+"."); familyKey != "" {
		return NormalizeGPUModel(familyKey)
	}
	// Node Feature Discovery publishes a descriptive label as well
	if feature := labelValue(labels, LabelNFDNVIDIAGPUModel); feature != "" {
		return NormalizeGPUModel(feature)
	}
	return ""
}

func labelValue(labels map[string]string, keys ...string) string {
	for _, key := range keys {
		if value, ok := labels[key]; ok {
			value = strings.TrimSpace(value)
			if value != "" {
				return value
			}
		}
	}
	return ""
}

func extractLabelSuffix(labels map[string]string, prefixes ...string) string {
	for _, prefix := range prefixes {
		for key, value := range labels {
			if !strings.HasPrefix(key, prefix) {
				continue
			}
			value = strings.TrimSpace(value)
			if value == "" || value == "0" {
				continue
			}
			suffix := strings.TrimPrefix(key, prefix)
			if suffix != "" {
				return suffix
			}
		}
	}
	return ""
}

var gpuModelTokenRegex = regexp.MustCompile(`^(MI|ME|RX|RTX|GTX|A|H|L|T|V|K|P|QUADRO|TESLA|GRID)?[A-Z]*[0-9]+[A-Z0-9]*$`)

func pickGPUModelToken(tokens []string) string {
	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}

		switch token {
		case "AMD", "NVIDIA", "TESLA", "RTX", "INSTINCT":
			continue
		}

		if gpuModelTokenRegex.MatchString(token) {
			token = strings.TrimPrefix(token, "INSTINCT")
			return token
		}
	}
	return ""
}

// filterGPULabelResources performs GPU discovery based on node labels only.
// Skips GPUs where the model cannot be determined from node labels (strict matching requirement).
func filterGPULabelResources(node *corev1.Node, aggregate map[string]GPUResourceInfo) {
	for _, resourcePrefix := range []string{ResourcePrefixAMD, ResourcePrefixNVIDIA} {

		// Extract GPU model from node labels
		gpuModel := ExtractGPUModelFromNodeLabels(node.Labels, resourcePrefix)

		// Skip GPUs where model cannot be determined (insufficient node labels)
		if gpuModel == "" {
			continue
		}

		// Add to the aggregate if not already present
		if _, exists := aggregate[gpuModel]; !exists {
			// Extract VRAM from node labels or fall back to static mapping
			vram, vramSource := GetGPUVRAM(gpuModel, node.Labels)

			aggregate[gpuModel] = GPUResourceInfo{
				ResourceName: resourcePrefix + "gpu",
				VRAM:         vram,
				VRAMSource:   vramSource,
			}
		}
	}
}

// IsGPUResource checks if a resource name represents a GPU resource.
// Returns true if the resource name starts with "amd.com/" or "nvidia.com/".
func IsGPUResource(resourceName string) bool {
	return strings.HasPrefix(resourceName, ResourcePrefixAMD) ||
		strings.HasPrefix(resourceName, ResourcePrefixNVIDIA)
}

// GetAMDDeviceIDsForModel returns all AMD device IDs that map to a given GPU model name.
// This is the inverse of MapAMDDeviceIDToModel, allowing lookup of all device IDs for a model.
// Example: GetAMDDeviceIDsForModel("MI300X") returns ["74a1", "74a9", "74b5", "74bd"]
// Returns empty slice if the model is not found or is not an AMD GPU.
func GetAMDDeviceIDsForModel(modelName string) []string {
	// Normalize the model name for comparison
	normalized := NormalizeGPUModel(modelName)

	var deviceIDs []string
	for deviceID, model := range KnownAmdDevices {
		if model == normalized {
			deviceIDs = append(deviceIDs, deviceID)
		}
	}

	return deviceIDs
}

// IsGPUAvailable checks if a specific GPU model is available in the cluster.
// The gpuModel parameter should be the GPU model name (e.g., "MI300X", "A100"), not the resource name.
// The input is normalized to handle variants like "MI300X (rev 2)" or "Instinct MI300X".
func IsGPUAvailable(ctx context.Context, k8sClient client.Client, gpuModel string) (bool, error) {
	resources, err := GetClusterGPUResources(ctx, k8sClient)
	if err != nil {
		return false, err
	}

	// Normalize the input for comparison (handles variants and extra tokens)
	normalizedModel := NormalizeGPUModel(gpuModel)

	_, exists := resources[normalizedModel]
	if !exists {
		return false, nil
	}

	// GPU is available even if there's no capacity
	return true, nil
}

// ListAvailableGPUs returns a list of all GPU resource types available in the cluster.
func ListAvailableGPUs(ctx context.Context, k8sClient client.Client) ([]string, error) {
	resources, err := GetClusterGPUResources(ctx, k8sClient)
	if err != nil {
		return nil, err
	}

	gpuTypes := make([]string, 0, len(resources))
	for gpuModel := range resources {
		gpuTypes = append(gpuTypes, gpuModel)
	}

	// Sort for consistent ordering across reconciliations
	sort.Strings(gpuTypes)
	return gpuTypes, nil
}

// GetGPUVRAM returns the VRAM capacity for a GPU, checking node labels first, then static mapping.
// Returns the VRAM value (e.g., "192G") and the source (VRAMSourceLabel, VRAMSourceStatic, or VRAMSourceUnknown).
func GetGPUVRAM(gpuModel string, nodeLabels map[string]string) (vram string, source string) {
	// 1. Try node labels first (most accurate, runtime-detected)
	if v, ok := nodeLabels[LabelAMDGPUVRAM]; ok && v != "" {
		return v, VRAMSourceLabel
	}
	if v, ok := nodeLabels[LabelAMDGPUVRAMBeta]; ok && v != "" {
		return v, VRAMSourceLabel
	}

	// 2. Fall back to static mapping based on GPU model
	normalized := NormalizeGPUModel(gpuModel)
	if v, ok := KnownGPUVRAM[normalized]; ok {
		return v, VRAMSourceStatic
	}

	// 3. Unknown - return empty (caller decides behavior)
	return "", VRAMSourceUnknown
}

// ParseVRAMToBytes parses a VRAM string (e.g., "192G") to bytes.
// Supports G (gigabytes) and T (terabytes) suffixes.
// Returns 0 if the format is not recognized.
func ParseVRAMToBytes(vram string) int64 {
	if vram == "" {
		return 0
	}

	vram = strings.TrimSpace(strings.ToUpper(vram))

	var multiplier int64
	var numStr string

	if strings.HasSuffix(vram, "G") {
		multiplier = 1024 * 1024 * 1024 // 1 GB in bytes
		numStr = strings.TrimSuffix(vram, "G")
	} else if strings.HasSuffix(vram, "T") {
		multiplier = 1024 * 1024 * 1024 * 1024 // 1 TB in bytes
		numStr = strings.TrimSuffix(vram, "T")
	} else {
		// Assume bytes if no suffix
		multiplier = 1
		numStr = vram
	}

	// Parse the numeric part
	var value int64
	for _, c := range numStr {
		if c >= '0' && c <= '9' {
			value = value*10 + int64(c-'0')
		} else {
			return 0 // Invalid character
		}
	}

	return value * multiplier
}

// GetVRAMTiersAboveThreshold returns all known VRAM tier values that meet or exceed the threshold.
// The threshold should be a VRAM string (e.g., "64G") or a resource.Quantity string.
// Returns values in the format used by device plugin labels (e.g., ["64G", "80G", "128G", "192G"]).
func GetVRAMTiersAboveThreshold(minVRAMBytes int64) []string {
	if minVRAMBytes <= 0 {
		return KnownVRAMTiers
	}

	var result []string
	for _, tier := range KnownVRAMTiers {
		tierBytes := ParseVRAMToBytes(tier)
		if tierBytes >= minVRAMBytes {
			result = append(result, tier)
		}
	}
	return result
}

// GetGPUModelsWithMinVRAM returns all GPU model names that have VRAM >= minVRAMBytes.
// Uses the static KnownGPUVRAM mapping to determine which models meet the requirement.
// Returns a list of normalized model names (e.g., ["MI300X", "MI325X", "MI355X"]).
func GetGPUModelsWithMinVRAM(minVRAMBytes int64) []string {
	if minVRAMBytes <= 0 {
		// Return all known models
		models := make([]string, 0, len(KnownGPUVRAM))
		for model := range KnownGPUVRAM {
			models = append(models, model)
		}
		sort.Strings(models)
		return models
	}

	var models []string
	for model, vram := range KnownGPUVRAM {
		vramBytes := ParseVRAMToBytes(vram)
		if vramBytes >= minVRAMBytes {
			models = append(models, model)
		}
	}
	sort.Strings(models)
	return models
}

// GetAMDDeviceIDsForMinVRAM returns all AMD device IDs for GPUs that have VRAM >= minVRAMBytes.
//
// If gpuModel is specified (non-empty), only returns device IDs for that specific model
// if it meets the VRAM requirement. Returns empty slice if the model doesn't meet the requirement.
//
// If gpuModel is empty, returns device IDs for ALL GPU models meeting the VRAM requirement.
func GetAMDDeviceIDsForMinVRAM(minVRAMBytes int64, gpuModel string) []string {
	// If a specific GPU model is requested, check if it meets VRAM requirement
	if gpuModel != "" {
		normalized := NormalizeGPUModel(gpuModel)
		vram, ok := KnownGPUVRAM[normalized]
		if !ok {
			// Unknown model - return its device IDs anyway (permissive for unknown models)
			return GetAMDDeviceIDsForModel(normalized)
		}
		vramBytes := ParseVRAMToBytes(vram)
		if vramBytes < minVRAMBytes {
			// Model doesn't meet VRAM requirement - return empty
			return nil
		}
		// Model meets VRAM requirement - return its device IDs
		return GetAMDDeviceIDsForModel(normalized)
	}

	// No specific model - get device IDs for ALL models meeting VRAM requirement
	models := GetGPUModelsWithMinVRAM(minVRAMBytes)

	var allDeviceIDs []string
	for _, model := range models {
		deviceIDs := GetAMDDeviceIDsForModel(model)
		allDeviceIDs = append(allDeviceIDs, deviceIDs...)
	}

	// Sort for consistent ordering
	sort.Strings(allDeviceIDs)
	return allDeviceIDs
}
