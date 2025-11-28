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
	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
)

// TODO remove? or merge with template_selection.go?

// findTemplateMatchingOverrides searches for an existing template that matches the service's overrides
// Returns the template name if found, empty string if not
func findTemplateMatchingOverrides(service *aimv1alpha1.AIMService, modelFetchResult ServiceModelFetchResult) string {
	if service.Spec.Overrides == nil {
		return ""
	}

	// Check namespace templates first
	for _, template := range modelFetchResult.NamespaceTemplatesForModel {
		if templateMatchesOverrides(&template.Spec.AIMServiceTemplateSpecCommon, service.Spec.Overrides) {
			return template.Name
		}
	}

	// Check cluster templates
	for _, template := range modelFetchResult.ClusterTemplatesForModel {
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
		if templateSpec.GpuSelector.ResourceName != overrides.GpuSelector.ResourceName {
			return false
		}
	}

	return true
}
