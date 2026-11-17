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

// Package aimmodel reconciles AIMModel/AIMClusterModel.
//
// The v1alpha1 and v1alpha2 schemas are unified (same Go type, same storage
// version, None conversion). Once an object lands in etcd there is no
// reliable signal of which apiVersion the user originally submitted, so a
// model is reconciled from its spec shape alone. Each phase is gated only on
// whether its inputs are populated:
//
//   - spec.image: OCI label inspection, then an in-cluster discovery Job →
//     catalog → AIMProfiles.
//   - spec.modelId: AIMProfiles generated from the runtime config's
//     profile-generation fallbacks.
//   - spec.profiles (or the deprecated spec.derivedFrom / spec.profileCopy):
//     a child AIMProfileSet.
//
// Spec fields that only configured AIMServiceTemplates are not read; a
// model that sets them reports the TemplateFieldsIgnored warning.

package aimmodel

import (
	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
)

// derivationSpec returns the active AIMProfileSet derivation source for the
// model. Resolution order:
//
//  1. spec.profiles (v1alpha2 grouped shape) — translated to AIMProfileSetSpec.
//  2. spec.derivedFrom (deprecated v1alpha2 flat shape, retained for objects
//     authored before the rename).
//  3. spec.profileCopy (v1alpha1 transition alias).
//
// Returns nil when none of the three is set.
func derivationSpec(spec *aimv1alpha1.AIMModelSpec) *aimv1alpha1.AIMProfileSetSpec {
	if spec == nil {
		return nil
	}
	if translated := profilesSpecToProfileSet(spec.Profiles); translated != nil {
		return translated
	}
	if spec.DerivedFrom != nil {
		return spec.DerivedFrom
	}
	return spec.ProfileCopy
}

// classifyModelKind discriminates the v1alpha2 onboarding flow from the
// spec shape. Returns one of AIMModelKindImage, AIMModelKindDerived,
// AIMModelKindCustom, or empty when the spec doesn't match any known flow.
//
// Image and profiles are mutually exclusive on new v1alpha2 objects.
//
// Within the derivation flows, the discriminator is the source profile's
// role: a `base`-role selector means "overlay onto a generic base
// profile" (Custom), anything else (deployable role or unset) means
// "re-derive from an already-deployable profile" (Derived).
func classifyModelKind(spec *aimv1alpha1.AIMModelSpec) aimv1alpha1.AIMModelKind {
	if spec == nil {
		return ""
	}
	if spec.Image != "" {
		return aimv1alpha1.AIMModelKindImage
	}
	if spec.ModelID != "" {
		return aimv1alpha1.AIMModelKindGenerated
	}
	derivation := derivationSpec(spec)
	if derivation == nil {
		return ""
	}
	if derivation.Selector.Role == aimv1alpha1.ProfileSelectorRoleBase {
		return aimv1alpha1.AIMModelKindCustom
	}
	return aimv1alpha1.AIMModelKindDerived
}

// profilesSpecToProfileSet translates the grouped spec.profiles shape into
// the canonical AIMProfileSetSpec the rest of the reconciler operates on.
// The translation is purely structural — no defaulting, no validation —
// because the CRD CEL rules already constrain the inputs.
func profilesSpecToProfileSet(profiles *aimv1alpha1.AIMModelProfilesSpec) *aimv1alpha1.AIMProfileSetSpec {
	if profiles == nil || profiles.DerivedFrom == nil {
		return nil
	}
	out := &aimv1alpha1.AIMProfileSetSpec{
		Selector:      profiles.DerivedFrom.Selector,
		SourceRef:     profiles.DerivedFrom.SourceRef,
		VersionPolicy: profiles.VersionPolicy,
		Version:       profiles.Version,
		Overrides:     profiles.Overrides,
		OutputPolicy:  profiles.OutputPolicy,
	}
	// The deployment-image override migrated from
	// spec.derivedFrom.image to spec.profiles.overrides.image. Keep the
	// child AIMProfileSet shape unchanged by hoisting it back onto the
	// top-level Image field that AIMProfileSet's reconciler reads.
	if profiles.Overrides != nil && profiles.Overrides.Image != "" {
		out.Image = profiles.Overrides.Image
	}
	return out
}

// shouldRunNativeDiscovery reports whether the discovery pipeline
// (in-cluster Job → DiscoveryCatalog → AIMProfile) should run for this spec.
// It honours the spec.discovery.extractMetadata=false opt-out: that flag is
// the user's explicit "do not inspect my image", and the discovery Job is a
// stronger form of inspection than the OCI-label fetch (it actually pulls
// and exec's the image inside the cluster).
//
// When extractMetadata is false, the model is expected to reach Ready
// without producing AIMProfiles. Callers that gate Discovery / NodeInventory
// / Profiles component health on this helper get a clean "no native work
// required" outcome rather than a stuck "DiscoveryPending".
func shouldRunNativeDiscovery(spec *aimv1alpha1.AIMModelSpec) bool {
	if spec == nil {
		return false
	}
	return spec.Discovery.IsExtractMetadataEnabled()
}

// EnsureBaseImageBridge keeps the v1alpha2-style status.baseImage in sync with
// the v1alpha1-style status.imageMetadata.baseImageRef so consumers that read
// either field see a consistent value. Either field may be authoritative
// depending on which reconcile path populated it.
//
// It never creates status.imageMetadata: the image metadata fetch treats any
// value there as already inspected and would stop reading the image labels.
func EnsureBaseImageBridge(spec *aimv1alpha1.AIMModelSpec, status *aimv1alpha1.AIMModelStatus) {
	if status == nil {
		return
	}
	// Prefer spec-provided metadata when present (user intent). When the
	// extracted base image ref differs from the cached status, the metadata
	// extractor is the source of truth and will overwrite this on next pass.
	if ref := spec.GetBaseImageRef(status); ref != "" && status.BaseImage == "" {
		status.BaseImage = ref
	}
	if status.BaseImage != "" && status.ImageMetadata != nil && status.ImageMetadata.BaseImageRef == "" {
		status.ImageMetadata.BaseImageRef = status.BaseImage
	}
}
