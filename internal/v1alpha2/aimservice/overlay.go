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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimprofile"
)

// AnnotationOverlayService records the AIMService that owns a service overlay
// AIMProfile so the resource is self-describing in the cluster (kubectl get
// aimprofile -o yaml shows where the overlay came from). Owner references
// already encode the GC relationship; the annotation is for humans.
//
// The canonical constant lives in the aimprofile package (so the AIMProfile
// reconciler can detect overlays without an import cycle); this alias keeps
// callers in this package readable.
const AnnotationOverlayService = aimprofile.AnnotationOverlayService

// hasProfileOverrides reports whether spec.profileOverrides is non-empty in a
// way that would actually change the resolved profile spec. A non-nil but
// otherwise-empty override block is treated as "no overlay needed" so users
// who emit `profileOverrides: {}` from a templating layer don't accidentally
// fork a per-service overlay.
func hasProfileOverrides(o *aimv1alpha1.AIMServiceProfileOverrides) bool {
	if o == nil {
		return false
	}
	if len(o.ModelSources) > 0 {
		return true
	}
	if o.AcceleratorModel != "" {
		return true
	}
	if o.AcceleratorCount != nil {
		return true
	}
	if len(o.ContainerEnv) > 0 {
		return true
	}
	if len(o.EngineEnv) > 0 {
		return true
	}
	if o.EngineArgs != nil && len(o.EngineArgs.Raw) > 0 {
		return true
	}
	return false
}

// asProfileOverrides reshapes the service-level override block onto the shared
// aimv1alpha1.ProfileOverrides type that aimprofile.ApplyProfileCopyOverrides
// expects. The two types intentionally have the same field set; this is a
// straight projection so the same merge semantics that AIMProfileSet uses for
// model-driven derivation also drive service-driven derivation.
func asProfileOverrides(o *aimv1alpha1.AIMServiceProfileOverrides) *aimv1alpha1.ProfileOverrides {
	if o == nil {
		return nil
	}
	return &aimv1alpha1.ProfileOverrides{
		ModelSources:     o.ModelSources,
		AcceleratorModel: o.AcceleratorModel,
		AcceleratorCount: o.AcceleratorCount,
		ContainerEnv:     o.ContainerEnv,
		EngineEnv:        o.EngineEnv,
		EngineArgs:       o.EngineArgs,
	}
}

// buildServiceOverlayProfile materialises a per-service overlay AIMProfile
// from the resolved seed profile with spec.profileOverrides applied. The
// overlay is namespace-scoped and owner-referenced by the AIMService so it
// is garbage-collected on service deletion.
//
// Naming follows the same shape as v1alpha1's GenerateTemplateCacheName for
// dedicated caches: a readable prefix derived from the seed plus a hash that
// includes the service UID. This guarantees uniqueness across delete-and-
// recreate of the same service name (the new instance gets a fresh overlay
// and won't accidentally inherit a stale one from the previous incarnation),
// while keeping the visible name informative.
//
// The function returns the fully-formed overlay AIMProfile (ready to apply)
// and the derived AIMProfileSpecCommon that downstream code (cache, ConfigMap,
// KServe builder) should treat as authoritative.
func buildServiceOverlayProfile(
	service *aimv1alpha1.AIMService,
	obs ServiceObservation,
) (*aimv1alpha2.AIMProfile, aimv1alpha2.AIMProfileSpecCommon, error) {
	if obs.resolvedProfileSpec == nil {
		return nil, aimv1alpha2.AIMProfileSpecCommon{}, fmt.Errorf("no seed profile to derive an overlay from")
	}

	overrides := asProfileOverrides(service.Spec.ProfileOverrides)

	// Pull baseImage from the seed profile's annotations (set by
	// AIMModel discovery when it materialised the seed). aimprofile uses
	// it to rebase the resolved deployment image onto the seed's
	// registry+org for fine-tuned weights, so private mirrors don't reach
	// back to the upstream aim-base. Annotation absent on user-authored
	// AIMProfiles is fine: ApplyProfileCopyOverrides treats empty as
	// "leave the seed image alone" which is the right semantic.
	seedBaseImage := seedBaseImageFromObservation(obs)

	overlaySpec, err := aimprofile.ApplyProfileCopyOverrides(
		*obs.resolvedProfileSpec,
		overrides,
		"", // no image override at the service level today
		seedBaseImage,
	)
	if err != nil {
		return nil, aimv1alpha2.AIMProfileSpecCommon{}, err
	}

	overlayName, err := utils.GenerateDerivedName(
		[]string{obs.profileName, service.Name, "overlay"},
		utils.WithHashSource(string(service.UID)),
	)
	if err != nil {
		return nil, aimv1alpha2.AIMProfileSpecCommon{}, fmt.Errorf("generate overlay name: %w", err)
	}

	// Inherit the seed's pull secrets and service account so the cache
	// download Job and inference pod can reach private registries / model
	// stores without the user re-stating them on the AIMService.
	overlaySpec.ImagePullSecrets = inheritedOverlayPullSecrets(*obs.resolvedProfileSpec)
	overlaySpec.ServiceAccountName = inheritedOverlayServiceAccount(*obs.resolvedProfileSpec)

	overlay := &aimv1alpha2.AIMProfile{
		TypeMeta: metav1.TypeMeta{
			APIVersion: aimv1alpha2.GroupVersion.String(),
			Kind:       "AIMProfile",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      overlayName,
			Namespace: service.Namespace,
			Annotations: map[string]string{
				AnnotationOverlayService: service.Name,
			},
		},
		Spec: aimv1alpha2.AIMProfileSpec{
			AIMProfileSpecCommon: overlaySpec,
		},
	}

	// Overlays are PRIVATE to their owning service: stamp only the
	// minimum provenance (role=deployable, origin=derived) and never the
	// source-model labels.
	//
	// Two reasons:
	//   - Without source-model labels, a selector-driven AIMService that
	//     authored `spec.profile.selector` + `spec.profileOverrides`
	//     cannot recursively select its own overlay as a seed on the
	//     next reconcile (the source-model label is the primary
	//     candidate filter on the selector path).
	//   - The AIMProfile reconciler's EnsureProfileProvenanceLabels
	//     cannot derive a ProfileSourceModel from an AIMService owner
	//     and would strip the labels every reconcile, racing with this
	//     code path. Not stamping them in the first place removes the
	//     race by construction.
	//
	// Overlays are reached only through the dedicated overlay fetch
	// path (`AnnotationOverlayService`), so loss of selector
	// discoverability is intentional and harmless.
	aimprofile.StampProfileProvenance(
		overlay,
		constants.LabelValueProfileRoleDeployable,
		aimv1alpha1.ProfileOriginDerived,
		nil,
	)

	return overlay, overlaySpec, nil
}

func inheritedOverlayPullSecrets(seed aimv1alpha2.AIMProfileSpecCommon) []corev1.LocalObjectReference {
	if len(seed.ImagePullSecrets) == 0 {
		return nil
	}
	out := make([]corev1.LocalObjectReference, len(seed.ImagePullSecrets))
	copy(out, seed.ImagePullSecrets)
	return out
}

func inheritedOverlayServiceAccount(seed aimv1alpha2.AIMProfileSpecCommon) string {
	return seed.ServiceAccountName
}

// seedBaseImageFromObservation reads the AIM_BASE_IMAGE_REF the AIMModel
// inspector recorded on the seed profile. Status is the canonical surface
// (laundered there by the AIMProfile reconciler from the producer's
// internal annotation); the annotation fallback covers the brief window
// between profile creation and the AIMProfile reconciler running. Unset for
// user-authored AIMProfiles, which ApplyProfileCopyOverrides handles
// correctly.
func seedBaseImageFromObservation(obs ServiceObservation) string {
	if obs.profile.Value != nil {
		return aimprofile.BaseImageFromProfile(obs.profile.Value, obs.profile.Value.Status.BaseImage)
	}
	if obs.clusterProfile.Value != nil {
		return aimprofile.BaseImageFromProfile(obs.clusterProfile.Value, obs.clusterProfile.Value.Status.BaseImage)
	}
	return ""
}
