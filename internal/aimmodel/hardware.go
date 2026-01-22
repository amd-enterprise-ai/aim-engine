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

package aimmodel

import (
	"dario.cat/mergo"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
)

// IsCustomModel returns true if the model spec defines inline model sources,
// indicating it's a custom model rather than an image-based model.
func IsCustomModel(spec *aimv1alpha1.AIMModelSpec) bool {
	return len(spec.ModelSources) > 0
}

// MergeHardware merges spec-level default hardware with template-level overrides.
// Template values take precedence over spec defaults.
// Uses mergo for field-by-field merging - non-zero template values override spec defaults.
func MergeHardware(specDefault, templateOverride *aimv1alpha1.AIMHardwareRequirements) *aimv1alpha1.AIMHardwareRequirements {
	// If no spec default, use template override directly
	if specDefault == nil {
		return templateOverride
	}

	// If no template override, use spec default directly
	if templateOverride == nil {
		return specDefault
	}

	// Deep copy spec default as base, then merge template override on top
	result := specDefault.DeepCopy()
	_ = mergo.Merge(result, templateOverride, mergo.WithOverride)

	return result
}

// GetEffectiveType returns the effective profile type for a custom template.
// Uses template-level type if set, otherwise falls back to spec-level default,
// otherwise defaults to "unoptimized".
func GetEffectiveType(specDefault *aimv1alpha1.AIMProfileType, templateType aimv1alpha1.AIMProfileType) aimv1alpha1.AIMProfileType {
	// Template-level type takes precedence
	if templateType != "" {
		return templateType
	}

	// Spec-level default
	if specDefault != nil {
		return *specDefault
	}

	// Default to unoptimized
	return aimv1alpha1.AIMProfileTypeUnoptimized
}
