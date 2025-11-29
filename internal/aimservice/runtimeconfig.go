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
	"dario.cat/mergo"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/aimruntimeconfig"
)

// MergeServiceRuntimeConfig merges runtime configuration from multiple sources with proper precedence.
// The merge order is: RuntimeConfigObservation (cluster + namespace merged) → Service inline config
// Service inline config (spec.storage, spec.routing) takes highest precedence.
//
// This function is specifically designed for AIMService reconciliation, where services can override
// certain runtime config fields (storage class, routing, PVC headroom) but not others (model discovery).
//
// Parameters:
//   - runtimeConfigObs: Observation from aimruntimeconfig package (cluster + namespace already merged)
//   - serviceConfig: Service-level config overrides (from inlined AIMServiceRuntimeConfig fields, highest precedence)
//
// Returns the merged configuration with all service-applicable fields resolved.
func MergeServiceRuntimeConfig(
	runtimeConfigObs aimruntimeconfig.RuntimeConfigObservation,
	serviceConfig *aimv1alpha1.AIMServiceRuntimeConfig,
) aimv1alpha1.AIMRuntimeConfigCommon {
	merged := aimv1alpha1.AIMRuntimeConfigCommon{}

	// Start with the merged runtime config (cluster + namespace)
	if runtimeConfigObs.MergedConfig != nil {
		_ = mergo.Merge(&merged, runtimeConfigObs.MergedConfig)
	}

	// Override with service-level config
	// Note: We merge into the embedded AIMServiceRuntimeConfig field to only affect
	// service-applicable fields (not Model.AutoDiscovery which doesn't apply to services)
	if serviceConfig != nil {
		_ = mergo.Merge(&merged.AIMServiceRuntimeConfig, serviceConfig, mergo.WithOverride)
	}

	return merged
}
