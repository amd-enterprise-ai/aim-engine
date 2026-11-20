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

package v1alpha1

import (
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

// AIMCustomProfile defines inline custom profile data for user-provided inference engine configuration.
// When set on a template, the controller assembles a profile YAML, creates a ConfigMap,
// and mounts it into both the discovery job and the inference service container.
type AIMCustomProfile struct {
	// EngineArgs contains inference engine arguments as a free-form JSON object.
	// These are passed as CLI arguments to the inference engine (e.g., vLLM).
	// Do not include "model" — it is injected separately by the runtime.
	// +kubebuilder:pruning:PreserveUnknownFields
	// +kubebuilder:validation:Schemaless
	// +optional
	EngineArgs *apiextensionsv1.JSON `json:"engineArgs,omitempty"`

	// EnvVars contains environment variables applied to the inference engine process.
	// These are written into the profile YAML and applied by the AIM runtime via os.execv,
	// distinct from container-level Env which targets the AIM runtime container itself.
	// Keys must match ^[A-Z0-9_]+$ (uppercase with underscores).
	// +optional
	EnvVars map[string]string `json:"envVars,omitempty"`
}

// AIMProfileType indicates the optimization level of a deployment profile.
// Hierarchy: optimized > general > preview > unoptimized.
// +kubebuilder:validation:Enum=optimized;general;preview;unoptimized
type AIMProfileType string

const (
	// AIMProfileTypeOptimized indicates the profile has been fully optimized.
	AIMProfileTypeOptimized AIMProfileType = "optimized"
	// AIMProfileTypeGeneral indicates a general-purpose profile (between optimized and preview).
	AIMProfileTypeGeneral AIMProfileType = "general"
	// AIMProfileTypePreview indicates the profile is in preview/beta state.
	AIMProfileTypePreview AIMProfileType = "preview"
	// AIMProfileTypeUnoptimized indicates the profile has not been optimized.
	AIMProfileTypeUnoptimized AIMProfileType = "unoptimized"
	// AIMProfileTypeAny is a selector-only sentinel meaning "no optimization
	// floor — accept every tier". It is never stamped on a profile's own
	// spec.type; it is only valid as a ProfileSelector.minimumType value, where
	// it disables the floor (equivalent to flooring at unoptimized, the lowest
	// tier, but reads as intent rather than asking specifically for unoptimized).
	AIMProfileTypeAny AIMProfileType = "any"
)

// AIMProfileAutoSelectionPolicy controls which optimization tiers may
// participate in automatic selection when the AIMService does not explicitly
// set selector.minimumType.
// +kubebuilder:validation:Enum=optimized;any
type AIMProfileAutoSelectionPolicy string

const (
	// AIMProfileAutoSelectionPolicyOptimized applies the implicit optimized
	// minimumType floor.
	AIMProfileAutoSelectionPolicyOptimized AIMProfileAutoSelectionPolicy = "optimized"
	// AIMProfileAutoSelectionPolicyAny permits a profile of any optimization
	// tier to participate in a second selection pass when no optimized candidate
	// matched and the AIMService did not explicitly set selector.minimumType.
	AIMProfileAutoSelectionPolicyAny AIMProfileAutoSelectionPolicy = "any"
)

// AIMProfileTypeFloor enumerates the values accepted by a profile selector's
// minimumType floor: the real optimization tiers plus the selector-only "any"
// sentinel that disables the floor.
//
// It is a distinct type from AIMProfileType on purpose. MinimumType cannot just
// be an AIMProfileType: that type's own enum constrains a profile's spec.type to
// the four real tiers (a profile is never "any"), and a field whose type already
// declares an enum cannot widen it — controller-gen emits an allOf of the two
// enums, whose intersection silently drops "any" and makes the documented
// sentinel un-settable. A separate type carries the correct 5-value enum for the
// floor while leaving spec.type constrained to real tiers.
// +kubebuilder:validation:Enum=optimized;general;preview;unoptimized;any
type AIMProfileTypeFloor string

const (
	// AIMProfileTypeFloorOptimized floors selection at optimized (the default
	// for AIMService auto-selection).
	AIMProfileTypeFloorOptimized AIMProfileTypeFloor = "optimized"
	// AIMProfileTypeFloorGeneral floors selection at general.
	AIMProfileTypeFloorGeneral AIMProfileTypeFloor = "general"
	// AIMProfileTypeFloorPreview floors selection at preview.
	AIMProfileTypeFloorPreview AIMProfileTypeFloor = "preview"
	// AIMProfileTypeFloorUnoptimized floors selection at unoptimized (the
	// lowest real tier — admits every typed profile).
	AIMProfileTypeFloorUnoptimized AIMProfileTypeFloor = "unoptimized"
	// AIMProfileTypeFloorAny disables the floor entirely (accept every tier,
	// including untyped/unknown).
	AIMProfileTypeFloorAny AIMProfileTypeFloor = "any"
)
