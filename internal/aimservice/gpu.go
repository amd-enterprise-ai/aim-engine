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

package aimservice

import (
	"strings"

	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	corev1 "k8s.io/api/core/v1"
)

const (
	// LabelNodeGPUProduct is the standard label for GPU product name
	LabelNodeGPUProduct = "amd.com/gpu.product"
	// LabelNodeGPUDeviceID is the label for GPU device ID
	LabelNodeGPUDeviceID = "amd.com/gpu.device-id"
	// LabelNodeGPUFamily is the label for GPU family
	LabelNodeGPUFamily = "amd.com/gpu.family"
)

// GPU model name mappings for normalization
var gpuModelAliases = map[string]string{
	"MI300X":              "MI300X",
	"MI-300X":             "MI300X",
	"AMD INSTINCT MI300X": "MI300X",
	"INSTINCT MI300X":     "MI300X",
	"MI325X":              "MI325X",
	"MI-325X":             "MI325X",
	"AMD INSTINCT MI325X": "MI325X",
	"INSTINCT MI325X":     "MI325X",
	"MI250X":              "MI250X",
	"MI-250X":             "MI250X",
	"AMD INSTINCT MI250X": "MI250X",
	"INSTINCT MI250X":     "MI250X",
	"MI210":               "MI210",
	"MI-210":              "MI210",
	"AMD INSTINCT MI210":  "MI210",
	"INSTINCT MI210":      "MI210",
}

// addGPUNodeAffinity adds node affinity rules for GPU selection to the InferenceService.
func addGPUNodeAffinity(isvc *servingv1beta1.InferenceService, gpuModel string) {
	if gpuModel == "" {
		return
	}

	// Normalize the GPU model for matching
	normalized := normalizeGPUModel(gpuModel)
	if normalized == "" {
		return
	}

	// Create the node selector requirement
	requirement := corev1.NodeSelectorRequirement{
		Key:      LabelNodeGPUProduct,
		Operator: corev1.NodeSelectorOpIn,
		Values:   gpuModelLabelValues(normalized),
	}

	// Ensure Affinity exists
	if isvc.Spec.Predictor.Affinity == nil {
		isvc.Spec.Predictor.Affinity = &corev1.Affinity{}
	}
	if isvc.Spec.Predictor.Affinity.NodeAffinity == nil {
		isvc.Spec.Predictor.Affinity.NodeAffinity = &corev1.NodeAffinity{}
	}

	// Add required node selector terms
	nodeAffinity := isvc.Spec.Predictor.Affinity.NodeAffinity
	if nodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		nodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution = &corev1.NodeSelector{
			NodeSelectorTerms: []corev1.NodeSelectorTerm{},
		}
	}

	// Add or update the node selector term
	terms := nodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
	if len(terms) == 0 {
		terms = append(terms, corev1.NodeSelectorTerm{
			MatchExpressions: []corev1.NodeSelectorRequirement{requirement},
		})
	} else {
		// Add to existing term
		terms[0].MatchExpressions = append(terms[0].MatchExpressions, requirement)
	}
	nodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms = terms
}

// extractGPUModelFromNodeLabels extracts the GPU model from node labels.
func extractGPUModelFromNodeLabels(labels map[string]string) string {
	if labels == nil {
		return ""
	}

	// Try primary label first
	if product, ok := labels[LabelNodeGPUProduct]; ok && product != "" {
		return product
	}

	// Try alternative labels
	if family, ok := labels[LabelNodeGPUFamily]; ok && family != "" {
		return family
	}

	return ""
}

// normalizeGPUModel normalizes a GPU model name to a canonical form.
func normalizeGPUModel(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}

	// Check aliases
	upper := strings.ToUpper(model)
	if canonical, ok := gpuModelAliases[upper]; ok {
		return canonical
	}

	// Try to find a match by checking if the input contains a known model
	for alias, canonical := range gpuModelAliases {
		if strings.Contains(upper, alias) {
			return canonical
		}
	}

	// Return original (uppercase for consistency)
	return upper
}

// gpuModelLabelValues returns possible label values for a normalized GPU model.
func gpuModelLabelValues(normalized string) []string {
	values := []string{normalized}

	// Add common variations
	switch normalized {
	case "MI300X":
		values = append(values,
			"mi300x",
			"MI-300X",
			"mi-300x",
			"AMD Instinct MI300X",
			"Instinct MI300X",
		)
	case "MI325X":
		values = append(values,
			"mi325x",
			"MI-325X",
			"mi-325x",
			"AMD Instinct MI325X",
			"Instinct MI325X",
		)
	case "MI250X":
		values = append(values,
			"mi250x",
			"MI-250X",
			"mi-250x",
			"AMD Instinct MI250X",
			"Instinct MI250X",
		)
	case "MI210":
		values = append(values,
			"mi210",
			"MI-210",
			"mi-210",
			"AMD Instinct MI210",
			"Instinct MI210",
		)
	}

	return values
}
