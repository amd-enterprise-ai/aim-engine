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
	"fmt"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
)

// Adapter staging mechanics (validation, staging Jobs, read-only subtree mount,
// status mirroring, component health) live in the pipeline-agnostic
// internal/aimadapter package and are shared with the v1alpha2 profile
// pipeline. The template pipeline only contributes the base-model resolution:
// it resolves the parent model artifact through the AIMTemplateCache's resolved
// artifacts, keyed on the resolved template's first model id.

// resolveAdapterParentName resolves the name of the base-model AIMArtifact this
// service's adapters attach to, via the template cache's resolved artifacts.
// Adapters bind to a single base model, so a resolved template carrying more
// than one model source is ambiguous and returns a terminal error. Returns
// ("", nil) when the parent cannot yet be resolved (cache not ready, template
// not resolved).
func resolveAdapterParentName(result ServiceFetchResult) (string, error) {
	if !result.templateCache.OK() || result.templateCache.Value == nil {
		return "", nil
	}

	var modelSources []aimv1alpha1.AIMModelSource
	switch {
	case result.template.Value != nil:
		modelSources = result.template.Value.Status.ModelSources
	case result.clusterTemplate.Value != nil:
		modelSources = result.clusterTemplate.Value.Status.ModelSources
	}
	if len(modelSources) == 0 {
		return "", nil
	}
	if len(modelSources) > 1 {
		return "", fmt.Errorf(
			"adapters require a single base model, but the resolved template has %d model sources",
			len(modelSources))
	}

	modelID := modelSources[0].ModelID
	for _, resolved := range result.templateCache.Value.Status.Artifacts {
		if resolved.Model == modelID {
			return resolved.Name, nil
		}
	}
	return "", nil
}
