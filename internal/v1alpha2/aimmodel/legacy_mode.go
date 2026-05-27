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
// reliable signal of which apiVersion the user originally submitted — the
// stored payload is identical, and the apiVersion observed by a controller
// is just whichever type its informer registered for.
//
// Therefore we do not try to infer "which API the user meant". Instead, both
// behaviours run side by side and each phase is gated only on whether its
// inputs are populated:
//
//   - The legacy v1alpha1 pipeline (OCI image inspection +
//     RecommendedDeployments → AIMServiceTemplate, plus customTemplates /
//     fine-tuned matching) runs whenever the spec carries any v1alpha1
//     inputs (image, customTemplates, modelSources, aimId, etc.). It is
//     skipped entirely for pure v1alpha2 specs (profileCopy-only) so it
//     does not surface conditions like ImageMetadataReady=False on a model
//     that legitimately has no image.
//   - The v1alpha2 native pipeline (in-cluster discovery Job → catalog →
//     AIMProfile, plus profileCopy → AIMProfileSet) always runs. It is a
//     no-op when neither spec.image nor spec.profileCopy is set.
//
// The two pipelines write disjoint plan items (templates vs profiles) and
// disjoint status fields, so they compose without conflict. Earlier
// revisions used an aim.eai.amd.com/mode annotation to pick one pipeline;
// that annotation has been removed and is no longer consulted.

package aimmodel

import (
	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
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
// Precedence: spec.image wins over spec.profiles when both are somehow
// set (CEL should prevent this, but if it leaks through we treat the
// image as authoritative since image discovery would dominate the
// resulting profile set).
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

// hasLegacyInputs reports whether the spec carries any v1alpha1-shaped
// configuration that would give the legacy reconciler real work to do. A
// model that only sets spec.profileCopy is purely v1alpha2 and must not
// be touched by the legacy pipeline — otherwise the legacy ImageMetadata
// fetch tries to parse an empty image reference and surfaces spurious
// failures (e.g. ImageMetadataReady=False, DependenciesReachable=False).
//
// Note: fine-tuned models (aimId + modelSources, possibly without spec.image)
// intentionally fall on the legacy side of this gate. The empty-image
// semantics for fine-tunes — skipping the OCI fetch and resolving the base
// image at AIMService time from the matched template — are owned by the
// legacy reconciler itself, not by this gate. Today on this branch that
// path still calls inspectImage(""), but the upcoming
// fix/set_finetuned_spec_image PR adds a fine-tuned skip in
// fetchImageMetadata and a base-image resolver in finetune_image.go. Once
// that lands, fine-tuned-no-image v1alpha2 models start working "for free"
// via the dual-pipeline; no change is needed here.
func hasLegacyInputs(spec aimv1alpha1.AIMModelSpec) bool {
	if spec.Image != "" {
		return true
	}
	if len(spec.CustomTemplates) > 0 {
		return true
	}
	if len(spec.ModelSources) > 0 {
		return true
	}
	if spec.AimId != "" {
		return true
	}
	if spec.ImageMetadata != nil {
		return true
	}
	if spec.Discovery != nil {
		return true
	}
	if spec.DefaultServiceTemplate != "" {
		return true
	}
	if spec.Custom != nil {
		return true
	}
	// NOTE: spec.Name is intentionally NOT checked here. It is the embedded
	// RuntimeConfigRef.Name (i.e. spec.runtimeConfigName) — a field that
	// both v1alpha1 and v1alpha2 specs may set and that on its own says
	// nothing about whether the legacy pipeline has real work to do.
	// Checking it caused pure v1alpha2 specs that referenced a custom
	// runtime config to be misclassified as legacy and routed through the
	// OCI inspection path with an empty image.
	return false
}

// shouldRunNativeDiscovery reports whether the v1alpha2 native discovery
// pipeline (in-cluster Job → DiscoveryCatalog → AIMProfile) should run for
// this spec. It honours the legacy spec.discovery.extractMetadata=false
// opt-out: that flag is the user's explicit "do not inspect my image", and
// the native pipeline is a stronger form of inspection than the legacy
// OCI-label fetch (it actually pulls and exec's the image inside the cluster).
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

// asLegacyModel rewraps a v1alpha2 AIMModel as a v1alpha1 AIMModel by sharing
// the same metadata/spec/status. Mutations through the legacy view are visible
// to the original v1alpha2 object because the embedded Spec/Status types are
// identical.
func asLegacyModel(model *aimv1alpha2.AIMModel) *aimv1alpha1.AIMModel {
	if model == nil {
		return nil
	}
	return &aimv1alpha1.AIMModel{
		TypeMeta:   model.TypeMeta,
		ObjectMeta: model.ObjectMeta,
		Spec:       model.Spec,
		Status:     model.Status,
	}
}

func asLegacyClusterModel(model *aimv1alpha2.AIMClusterModel) *aimv1alpha1.AIMClusterModel {
	if model == nil {
		return nil
	}
	return &aimv1alpha1.AIMClusterModel{
		TypeMeta:   model.TypeMeta,
		ObjectMeta: model.ObjectMeta,
		Spec:       model.Spec,
		Status:     model.Status,
	}
}

// EnsureBaseImageBridge keeps the v1alpha2-style status.baseImage in sync with
// the v1alpha1-style status.imageMetadata.baseImageRef so consumers that read
// either field see a consistent value. Either field may be authoritative
// depending on which reconcile path populated it.
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
	if status.BaseImage != "" {
		if status.ImageMetadata == nil {
			status.ImageMetadata = &aimv1alpha1.ImageMetadata{}
		}
		if status.ImageMetadata.BaseImageRef == "" {
			status.ImageMetadata.BaseImageRef = status.BaseImage
		}
	}
}
