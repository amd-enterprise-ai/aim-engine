/*
MIT License

Copyright (c) 2025 Advanced Micro Devices, Inc.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package aimservice

import (
	"fmt"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

// selectTemplateForService selects the best template from the model's templates
func selectTemplateForService(
	service *aimv1alpha1.AIMService,
	modelObs serviceModelObservation,
	modelFetchResult serviceModelFetchResult,
) (templateName string, scope aimv1alpha1.AIMResolutionScope, err error) {
	// Build candidate list from fetched templates
	candidates := make([]templateCandidate, 0)

	for _, template := range modelFetchResult.namespaceTemplatesForModel {
		candidates = append(candidates, templateCandidate{
			Name:      template.Name,
			Namespace: template.Namespace,
			Scope:     aimv1alpha1.AIMResolutionScopeNamespace,
			Spec:      template.Spec.AIMServiceTemplateSpecCommon,
			Status:    template.Status,
		})
	}

	for _, template := range modelFetchResult.clusterTemplatesForModel {
		candidates = append(candidates, templateCandidate{
			Name:   template.Name,
			Scope:  aimv1alpha1.AIMResolutionScopeCluster,
			Spec:   template.Spec.AIMServiceTemplateSpecCommon,
			Status: template.Status,
		})
	}

	if len(candidates) == 0 {
		return "", aimv1alpha1.AIMResolutionScopeUnknown, fmt.Errorf("no templates found for model %q", modelObs.modelName)
	}

	// Select best template
	selected := selectBestTemplate(candidates, service.Spec.Overrides)
	if selected == nil {
		return "", aimv1alpha1.AIMResolutionScopeUnknown, fmt.Errorf("no suitable templates found for model %q", modelObs.modelName)
	}

	return selected.Name, selected.Scope, nil
}

type templateCandidate struct {
	Name      string
	Namespace string
	Scope     aimv1alpha1.AIMResolutionScope
	Spec      aimv1alpha1.AIMServiceTemplateSpecCommon
	Status    aimv1alpha1.AIMServiceTemplateStatus
}

// selectBestTemplate selects the best template from candidates
// 1. Filter to only Ready templates
// 2. Filter by service overrides if specified
// 3. Prefer namespace templates over cluster templates
// 4. Prefer latency to throughput, lower precision
func selectBestTemplate(candidates []templateCandidate, overrides *aimv1alpha1.AIMServiceOverrides) *templateCandidate {
	// Filter to Ready templates only
	filtered := make([]templateCandidate, 0)
	for _, c := range candidates {
		if c.Status.Status == constants.AIMStatusReady {
			filtered = append(filtered, c)
		}
	}

	if len(filtered) == 0 {
		return nil
	}

	// Filter by overrides if specified
	if overrides != nil {
		filtered = filterByOverrides(filtered, overrides)
		if len(filtered) == 0 {
			return nil
		}
	}

	// Prefer namespace templates
	hasNamespace := false
	for _, c := range filtered {
		if c.Scope == aimv1alpha1.AIMResolutionScopeNamespace {
			hasNamespace = true
			break
		}
	}
	if hasNamespace {
		namespaceOnly := make([]templateCandidate, 0)
		for _, c := range filtered {
			if c.Scope == aimv1alpha1.AIMResolutionScopeNamespace {
				namespaceOnly = append(namespaceOnly, c)
			}
		}
		filtered = namespaceOnly
	}

	if len(filtered) == 1 {
		return &filtered[0]
	}

	// Pick best based on metric and precision preferences
	bestIdx := 0
	for i := 1; i < len(filtered); i++ {
		if isBetterTemplate(filtered[i], filtered[bestIdx]) {
			bestIdx = i
		}
	}

	return &filtered[bestIdx]
}

func filterByOverrides(candidates []templateCandidate, overrides *aimv1alpha1.AIMServiceOverrides) []templateCandidate {
	result := make([]templateCandidate, 0)
	for _, c := range candidates {
		if templateMatchesOverrides(&c.Spec, overrides) {
			result = append(result, c)
		}
	}
	return result
}

// isBetterTemplate returns true if a is better than b
// Prefers latency over throughput, lower precision (fp8 > fp16 > bf16 > fp32)
func isBetterTemplate(a, b templateCandidate) bool {
	// Prefer latency over throughput
	aMetric := getMetric(a.Spec)
	bMetric := getMetric(b.Spec)
	if aMetric == aimv1alpha1.AIMMetricLatency && bMetric != aimv1alpha1.AIMMetricLatency {
		return true
	}
	if bMetric == aimv1alpha1.AIMMetricLatency && aMetric != aimv1alpha1.AIMMetricLatency {
		return false
	}

	// Prefer lower precision (more optimized)
	aPrecision := getPrecision(a.Spec)
	bPrecision := getPrecision(b.Spec)
	return precisionRank(aPrecision) < precisionRank(bPrecision)
}

func getMetric(spec aimv1alpha1.AIMServiceTemplateSpecCommon) aimv1alpha1.AIMMetric {
	if spec.Metric != nil {
		return *spec.Metric
	}
	return ""
}

func getPrecision(spec aimv1alpha1.AIMServiceTemplateSpecCommon) aimv1alpha1.AIMPrecision {
	if spec.Precision != nil {
		return *spec.Precision
	}
	return ""
}

func precisionRank(p aimv1alpha1.AIMPrecision) int {
	switch p {
	case aimv1alpha1.AIMPrecisionFP8:
		return 0
	case aimv1alpha1.AIMPrecisionFP16:
		return 1
	case aimv1alpha1.AIMPrecisionBF16:
		return 2
	case aimv1alpha1.AIMPrecisionFP32:
		return 3
	default:
		return 999
	}
}

// findTemplateMatchingOverrides searches for an existing template that matches the service's overrides
// Returns the template name if found, empty string if not
func findTemplateMatchingOverrides(service *aimv1alpha1.AIMService, modelFetchResult serviceModelFetchResult) string {
	if service.Spec.Overrides == nil {
		return ""
	}

	// Check namespace templates first
	for _, template := range modelFetchResult.namespaceTemplatesForModel {
		if templateMatchesOverrides(&template.Spec.AIMServiceTemplateSpecCommon, service.Spec.Overrides) {
			return template.Name
		}
	}

	// Check cluster templates
	for _, template := range modelFetchResult.clusterTemplatesForModel {
		if templateMatchesOverrides(&template.Spec.AIMServiceTemplateSpecCommon, service.Spec.Overrides) {
			return template.Name
		}
	}

	return ""
}

// templateMatchesOverrides checks if a template's spec matches the service's overrides
// Only checks fields that are set in overrides (ignores unset fields)
func templateMatchesOverrides(templateSpec *aimv1alpha1.AIMServiceTemplateSpecCommon, overrides *aimv1alpha1.AIMServiceOverrides) bool {
	// Check Metric
	if overrides.Metric != nil && (templateSpec.Metric == nil || *templateSpec.Metric != *overrides.Metric) {
		return false
	}

	// Check Precision
	if overrides.Precision != nil && (templateSpec.Precision == nil || *templateSpec.Precision != *overrides.Precision) {
		return false
	}

	// Check GpuSelector
	if overrides.GpuSelector != nil {
		if templateSpec.GpuSelector == nil {
			return false
		}
		// Compare GPU selector fields
		if templateSpec.GpuSelector.Count != overrides.GpuSelector.Count {
			return false
		}
		if templateSpec.GpuSelector.Model != overrides.GpuSelector.Model {
			return false
		}
	}

	return true
}
