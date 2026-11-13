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

import "fmt"

// Adapter staging mechanics (validation, staging Jobs, read-only subtree mount,
// status mirroring, component health) live in the pipeline-agnostic
// internal/aimadapter package and are shared with the v1alpha1 template
// pipeline. The profile pipeline only contributes the base-model resolution:
// it resolves the parent model artifact through the AIMProfileCache's resolved
// artifacts, keyed on the resolved profile's model id.

// resolveAdapterParentName resolves the name of the base-model AIMArtifact this
// service's adapters attach to, via the profile cache's resolved artifacts.
// Adapters bind to a single base model, so a profile resolving to more than one
// model source is ambiguous and returns a terminal error. Returns ("", nil)
// when the parent cannot yet be resolved (profile or cache not ready).
func resolveAdapterParentName(seed *ServiceObservation, result *ServiceFetchResult) (string, error) {
	if seed.resolvedProfileSpec == nil || len(seed.resolvedProfileSpec.ModelSources) == 0 {
		return "", nil
	}
	if len(seed.resolvedProfileSpec.ModelSources) > 1 {
		return "", fmt.Errorf(
			"adapters require a single base model, but the resolved profile has %d model sources",
			len(seed.resolvedProfileSpec.ModelSources))
	}
	if !result.profileCache.OK() || result.profileCache.Value == nil {
		return "", nil
	}
	modelID := seed.resolvedProfileSpec.ModelSources[0].ModelID
	for _, resolved := range result.profileCache.Value.Status.Artifacts {
		if resolved.Model == modelID {
			return resolved.Name, nil
		}
	}
	return "", nil
}
