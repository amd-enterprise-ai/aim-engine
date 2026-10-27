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

package aimruntimeconfig

import (
	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/aimimage"
)

var compatibilityImageRegistryMappings = []aimimage.RegistryMapping{
	{
		From:     "docker.io/silogenai",
		To:       "docker.io/amdenterpriseai",
		Fallback: true,
	},
	{
		From:     "ghcr.io/silogen",
		To:       "docker.io/amdenterpriseai",
		Fallback: true,
	},
}

// ImageRegistryMappings adapts RuntimeConfig registry policy to the
// version-neutral image-reference resolver. Operator-owned compatibility
// mappings are fallbacks, so any matching mapping from the selected
// RuntimeConfig wins, including a broader private-registry mapping.
func ImageRegistryMappings(config *aimv1alpha1.AIMRuntimeConfigCommon) []aimimage.RegistryMapping {
	configured := 0
	if config != nil && config.ImageRegistry != nil {
		configured = len(config.ImageRegistry.Mappings)
	}
	mappings := make([]aimimage.RegistryMapping, 0, configured+len(compatibilityImageRegistryMappings))
	if configured > 0 {
		for _, mapping := range config.ImageRegistry.Mappings {
			mappings = append(mappings, aimimage.RegistryMapping{
				From: mapping.From,
				To:   mapping.To,
			})
		}
	}
	mappings = append(mappings, compatibilityImageRegistryMappings...)
	return mappings
}
