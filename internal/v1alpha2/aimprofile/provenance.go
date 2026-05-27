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

package aimprofile

import (
	"context"
	"fmt"
	"strconv"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

const (
	AnnotationProfileSource   = constants.AimLabelDomain + "/profile-source"
	AnnotationProfileCopyable = constants.AimLabelDomain + "/profile-copyable"
	annotationProfileSetUID   = constants.AimLabelDomain + "/profile-set-uid"

	// AnnotationOverlayService marks an AIMProfile as a private overlay
	// owned by a specific AIMService. Mirrors aimservice.AnnotationOverlayService
	// (defined here as the source of truth to keep the aimprofile package
	// importable from aimservice without creating an import cycle).
	AnnotationOverlayService = constants.AimLabelDomain + "/overlay-service"

	// AnnotationBaseImage is the internal wire format the AIMModel reconciler
	// uses to communicate the AIM_BASE_IMAGE_REF extracted from the source
	// image to the AIMProfile reconciler at materialisation time. The
	// AIMProfile reconciler launders the value into AIMProfileStatus.BaseImage
	// during DecorateStatus; downstream readers (AIMService overlay,
	// AIMProfileSet candidate listing) prefer status and only fall back to
	// the annotation for objects whose status hasn't yet been decorated
	// (e.g., immediately after creation).
	AnnotationBaseImage = constants.AimLabelDomain + "/base-image"

	ProfileSourceImage = "image"
	ProfileSourceCopy  = "copy"
)

// BaseImageFromProfile returns the base image to record on the profile's
// status. It prefers an existing status value (rare, but covers the case
// where a previous reconcile already populated it) and falls back to the
// AIMModel-stamped wire annotation.
func BaseImageFromProfile(profile client.Object, currentStatusBaseImage string) string {
	if currentStatusBaseImage != "" {
		return currentStatusBaseImage
	}
	if profile == nil {
		return ""
	}
	if anns := profile.GetAnnotations(); anns != nil {
		return anns[AnnotationBaseImage]
	}
	return ""
}

func MarkProfileSource(annotations map[string]string, source string) map[string]string {
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[AnnotationProfileSource] = source
	return annotations
}

func MarkProfileCopyable(annotations map[string]string, copyable bool) map[string]string {
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[AnnotationProfileCopyable] = strconv.FormatBool(copyable)
	return annotations
}

func IsProfileCopyable(annotations map[string]string) bool {
	value, err := strconv.ParseBool(annotations[AnnotationProfileCopyable])
	return err == nil && value
}

func IsCopiedProfile(annotations map[string]string) bool {
	return annotations[AnnotationProfileSource] == ProfileSourceCopy || annotations[annotationProfileSetUID] != ""
}

// IsProfileDeployable reports whether the spec is materialised enough to back
// an AIMService. Iteration 1 producers always emit deployable profiles; the
// base-profile case (false) is only reachable through hand-authored base
// profiles or once iteration 2 lands.
func IsProfileDeployable(spec aimv1alpha2.AIMProfileSpecCommon) bool {
	return spec.AimId != "" && len(spec.ModelSources) > 0
}

// ProfileRoleLabelValue returns the canonical `aim.eai.amd.com/profile-role`
// label value for a profile spec.
func ProfileRoleLabelValue(spec aimv1alpha2.AIMProfileSpecCommon) string {
	if IsProfileDeployable(spec) {
		return constants.LabelValueProfileRoleDeployable
	}
	return constants.LabelValueProfileRoleBase
}

// StampProfileProvenance writes the iteration-1 provenance labels onto a
// profile object. This is the single point where producer reconcilers
// (AIMModel image discovery, AIMModel/AIMProfileSet derivation, AIMProfile
// backfill) agree on how role / origin / source-model labels are stamped.
//
// User-set labels are preserved: this helper only writes our well-known keys.
// Passing source == nil clears the source-model labels so user-authored
// profiles that lose their owning model don't keep stale labels.
func StampProfileProvenance(profile client.Object, role string, origin aimv1alpha1.ProfileOrigin, source *aimv1alpha2.ProfileSourceModel) {
	if profile == nil {
		return
	}
	labels := profile.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	if role != "" {
		labels[constants.LabelKeyProfileRole] = role
	}
	if origin != "" {
		labels[constants.LabelKeyProfileOrigin] = string(origin)
	}
	if source != nil {
		labels[constants.LabelKeySourceModel] = source.Name
		labels[constants.LabelKeySourceModelScope] = sourceModelScopeLabel(source.Kind)
	} else {
		delete(labels, constants.LabelKeySourceModel)
		delete(labels, constants.LabelKeySourceModelScope)
	}
	profile.SetLabels(labels)
}

func sourceModelScopeLabel(kind aimv1alpha2.ProfileSourceModelKind) string {
	switch kind {
	case aimv1alpha2.ProfileSourceModelKindAIMClusterModel:
		return constants.LabelValueSourceModelScopeCluster
	case aimv1alpha2.ProfileSourceModelKindAIMModel:
		return constants.LabelValueSourceModelScopeNamespace
	default:
		return constants.LabelValueSourceModelScopeNamespace
	}
}

// SourceModelFromOwnerRefs returns the producing AIM(Cluster)Model identified
// by the controller owner reference, or nil when no such owner exists. The
// namespace argument is used to populate ProfileSourceModel.Namespace for
// AIMModel-owned profiles (cluster owners record an empty namespace).
//
// Direct AIM(Cluster)Model ownership is the common case for image-discovery
// profiles. Profiles owned by an AIM(Cluster)ProfileSet — produced by the
// derivation pipeline — instead inherit their `aim.eai.amd.com/source-model`
// label from the parent set via the shared apply-time propagation path
// (controller/utils/apply.PropagateLabels). The AIMProfile reconciler must
// honour those propagated labels as the authoritative source-of-truth;
// otherwise EnsureProfileProvenanceLabels would race with the propagation
// step, deleting the labels every reconcile while the parent set re-stamps
// them on every apply, producing a hot loop and breaking the AIMService
// resolver's source-model label query.
func SourceModelFromOwnerRefs(profile client.Object, namespace string) *aimv1alpha2.ProfileSourceModel {
	if profile == nil {
		return nil
	}
	for _, ref := range profile.GetOwnerReferences() {
		if ref.Controller == nil || !*ref.Controller {
			continue
		}
		if ref.APIVersion != aimv1alpha2.GroupVersion.String() &&
			ref.APIVersion != aimv1alpha1.GroupVersion.String() {
			continue
		}
		switch ref.Kind {
		case "AIMModel":
			return &aimv1alpha2.ProfileSourceModel{
				Name:      ref.Name,
				Kind:      aimv1alpha2.ProfileSourceModelKindAIMModel,
				Namespace: namespace,
			}
		case "AIMClusterModel":
			return &aimv1alpha2.ProfileSourceModel{
				Name: ref.Name,
				Kind: aimv1alpha2.ProfileSourceModelKindAIMClusterModel,
			}
		case "AIMProfileSet", "AIMClusterProfileSet":
			if source := sourceModelFromPropagatedLabels(profile, namespace); source != nil {
				return source
			}
		}
	}
	return nil
}

// sourceModelFromPropagatedLabels reconstructs a ProfileSourceModel from the
// `aim.eai.amd.com/source-model` / `…/source-model-scope` labels already
// stamped on the profile. Used by the AIM(Cluster)ProfileSet ownership path
// where the source-of-truth lives on the parent set and is propagated to
// the child via PropagateLabels.
func sourceModelFromPropagatedLabels(profile client.Object, namespace string) *aimv1alpha2.ProfileSourceModel {
	labels := profile.GetLabels()
	name := labels[constants.LabelKeySourceModel]
	if name == "" {
		return nil
	}
	source := &aimv1alpha2.ProfileSourceModel{Name: name}
	if labels[constants.LabelKeySourceModelScope] == constants.LabelValueSourceModelScopeCluster {
		source.Kind = aimv1alpha2.ProfileSourceModelKindAIMClusterModel
		return source
	}
	source.Kind = aimv1alpha2.ProfileSourceModelKindAIMModel
	source.Namespace = namespace
	return source
}

// DeriveProfileOrigin classifies how a profile was produced based on a
// previously-stamped label, owner references, and copy annotations.
//
// Resolution order:
//  1. An existing `aim.eai.amd.com/profile-origin` label (already stamped by a
//     producer) wins, so the AIMProfile reconciler never overrides an explicit
//     classification.
//  2. Profiles owned by an AIM(Cluster)Model and marked as image-sourced map
//     to Discovered.
//  3. Profiles owned by an AIM(Cluster)Model or carrying the legacy copy
//     annotations map to Derived.
//  4. Everything else falls back to UserAuthored (hand-authored profiles).
func DeriveProfileOrigin(profile client.Object) aimv1alpha1.ProfileOrigin {
	if profile == nil {
		return aimv1alpha1.ProfileOriginUserAuthored
	}
	if labels := profile.GetLabels(); labels != nil {
		switch aimv1alpha1.ProfileOrigin(labels[constants.LabelKeyProfileOrigin]) {
		case aimv1alpha1.ProfileOriginDiscovered:
			return aimv1alpha1.ProfileOriginDiscovered
		case aimv1alpha1.ProfileOriginDerived:
			return aimv1alpha1.ProfileOriginDerived
		case aimv1alpha1.ProfileOriginUserAuthored:
			return aimv1alpha1.ProfileOriginUserAuthored
		}
	}
	annotations := profile.GetAnnotations()
	hasAIMOwner := false
	for _, ref := range profile.GetOwnerReferences() {
		if ref.APIVersion != aimv1alpha2.GroupVersion.String() &&
			ref.APIVersion != aimv1alpha1.GroupVersion.String() {
			continue
		}
		if ref.Kind == "AIMModel" || ref.Kind == "AIMClusterModel" || ref.Kind == "AIMProfileSet" || ref.Kind == "AIMClusterProfileSet" {
			hasAIMOwner = true
			break
		}
	}
	if annotations[AnnotationProfileSource] == ProfileSourceImage && hasAIMOwner {
		return aimv1alpha1.ProfileOriginDiscovered
	}
	if hasAIMOwner || IsCopiedProfile(annotations) {
		return aimv1alpha1.ProfileOriginDerived
	}
	return aimv1alpha1.ProfileOriginUserAuthored
}

// IsServiceOverlay reports whether the profile is a per-service overlay
// (carries the AnnotationOverlayService annotation). Overlays are private
// to their owning AIMService and the AIMProfile reconciler skips
// source-model label management for them — overlays intentionally carry no
// source-model labels so they are not selector-discoverable, which would
// otherwise let selector-driven services recursively pick their own
// overlay as a seed.
func IsServiceOverlay(profile client.Object) bool {
	if profile == nil {
		return false
	}
	_, ok := profile.GetAnnotations()[AnnotationOverlayService]
	return ok
}

// EnsureProfileProvenanceLabels patches the profile's role / origin / source-model
// labels in-place via a merge patch when they diverge from the desired values.
// Returns true when a patch was sent. Safe to call on every reconcile loop:
// it is a no-op when labels are already up to date.
//
// For service overlays (see IsServiceOverlay), source-model labels are
// intentionally NOT managed here: the producer (aimservice.buildServiceOverlayProfile)
// stamps role/origin only, and we preserve any existing labels untouched
// to avoid racing the overlay-producer reconcile.
func EnsureProfileProvenanceLabels(
	ctx context.Context,
	c client.Client,
	profile client.Object,
	role string,
	origin aimv1alpha1.ProfileOrigin,
	source *aimv1alpha2.ProfileSourceModel,
) (bool, error) {
	if profile == nil {
		return false, nil
	}
	current := profile.GetLabels()
	var desired map[string]string
	if IsServiceOverlay(profile) {
		// Manage role/origin but leave source-model labels alone (they
		// should be absent on a freshly-stamped overlay; if a user
		// manually added them we won't touch them either).
		desired = mergeProvenanceLabelsPreservingSource(current, role, origin)
	} else {
		desired = mergeProvenanceLabels(current, role, origin, source)
	}
	if labelsEqual(current, desired) {
		return false, nil
	}
	patchBase := profile.DeepCopyObject().(client.Object)
	profile.SetLabels(desired)
	patch := client.MergeFrom(patchBase)
	if err := c.Patch(ctx, profile, patch); err != nil {
		return false, err
	}
	return true, nil
}

// mergeProvenanceLabelsPreservingSource is the overlay-safe variant of
// mergeProvenanceLabels: it manages role/origin but never touches the
// source-model labels (whether to add, remove, or update them).
func mergeProvenanceLabelsPreservingSource(current map[string]string, role string, origin aimv1alpha1.ProfileOrigin) map[string]string {
	out := make(map[string]string, len(current)+2)
	for k, v := range current {
		out[k] = v
	}
	if role != "" {
		out[constants.LabelKeyProfileRole] = role
	}
	if origin != "" {
		out[constants.LabelKeyProfileOrigin] = string(origin)
	}
	return out
}

func mergeProvenanceLabels(current map[string]string, role string, origin aimv1alpha1.ProfileOrigin, source *aimv1alpha2.ProfileSourceModel) map[string]string {
	out := make(map[string]string, len(current)+4)
	for k, v := range current {
		out[k] = v
	}
	if role != "" {
		out[constants.LabelKeyProfileRole] = role
	}
	if origin != "" {
		out[constants.LabelKeyProfileOrigin] = string(origin)
	}
	if source != nil {
		out[constants.LabelKeySourceModel] = source.Name
		out[constants.LabelKeySourceModelScope] = sourceModelScopeLabel(source.Kind)
	} else {
		delete(out, constants.LabelKeySourceModel)
		delete(out, constants.LabelKeySourceModelScope)
	}
	return out
}

func labelsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// EnsureProfileMetaProvenance is a convenience used by tests / non-controller
// callers that just want to mutate a metav1.Object directly without a client.
func EnsureProfileMetaProvenance(meta metav1.Object, role string, origin aimv1alpha1.ProfileOrigin, source *aimv1alpha2.ProfileSourceModel) {
	currentLabels := meta.GetLabels()
	merged := mergeProvenanceLabels(currentLabels, role, origin, source)
	meta.SetLabels(merged)
}

// SelectorScope categorises which scope a selector.modelRef should match.
// Returned by ProvenanceLabelSelector helpers so callers can decide between
// namespace AIMProfile and cluster AIMClusterProfile listings.
type SelectorScope int

const (
	// SelectorScopeAny is the default — try same-namespace AIMProfile first
	// and fall back to AIMClusterProfile.
	SelectorScopeAny SelectorScope = iota
	// SelectorScopeNamespace constrains the lookup to namespace AIMProfiles.
	SelectorScopeNamespace
	// SelectorScopeCluster constrains the lookup to cluster AIMClusterProfiles.
	SelectorScopeCluster
)

// ProvenanceLabelSelector turns the iteration-1 provenance fields on a
// ProfileSelector into a labels.Selector that filters AIMProfile /
// AIMClusterProfile lists by role / source-model[-scope] / origin.
//
// The returned selector applies these rules in order (anything unset is a
// pass-through):
//   - selector.role defaults to Deployable; iteration-1 producers always
//     stamp `aim.eai.amd.com/profile-role=deployable`. Selecting `Base`
//     filters down to base profiles, which iteration 1 never produces.
//   - selector.modelRef.name (when set) requires the source-model label.
//     selector.modelRef.scope chooses which scope label the source must have:
//     Namespace, Cluster, or Auto (no scope constraint, the resulting
//     SelectorScope is SelectorScopeAny and the caller should attempt
//     namespace AIMProfiles first then cluster AIMClusterProfiles).
//   - selector.origin (when set) requires the matching profile-origin label.
//
// Returns the selector to use for filtering plus the SelectorScope hint.
func ProvenanceLabelSelector(selector aimv1alpha1.ProfileSelector) (labels.Selector, SelectorScope, error) {
	sel := labels.NewSelector()
	// Role semantics:
	//   Base       → require role=base (matches only the iteration-2 base-image
	//                surface; in iteration 1 nothing is labelled `base`).
	//   Deployable → exclude anything actively labelled `base`. Profiles that
	//                are not yet labelled (transitional state before the
	//                AIMProfile reconciler backfills the role label) still
	//                match — they'll either become `deployable` or be filtered
	//                out on the next reconcile. This mirrors the plan's wording
	//                "Default Deployable excludes anything labelled base".
	switch selector.Role {
	case aimv1alpha1.ProfileSelectorRoleBase:
		req, err := labels.NewRequirement(constants.LabelKeyProfileRole, selection.Equals, []string{constants.LabelValueProfileRoleBase})
		if err != nil {
			return nil, SelectorScopeAny, fmt.Errorf("build role requirement: %w", err)
		}
		sel = sel.Add(*req)
	default:
		req, err := labels.NewRequirement(constants.LabelKeyProfileRole, selection.NotEquals, []string{constants.LabelValueProfileRoleBase})
		if err != nil {
			return nil, SelectorScopeAny, fmt.Errorf("build role requirement: %w", err)
		}
		sel = sel.Add(*req)
	}
	scope := SelectorScopeAny
	if selector.ModelRef != nil && selector.ModelRef.Name != "" {
		req, err := labels.NewRequirement(constants.LabelKeySourceModel, selection.Equals, []string{selector.ModelRef.Name})
		if err != nil {
			return nil, SelectorScopeAny, fmt.Errorf("build source-model requirement: %w", err)
		}
		sel = sel.Add(*req)
		switch selector.ModelRef.Scope {
		case aimv1alpha1.ProfileSelectorScopeNamespace:
			scope = SelectorScopeNamespace
			scopeReq, scopeErr := labels.NewRequirement(constants.LabelKeySourceModelScope, selection.Equals, []string{constants.LabelValueSourceModelScopeNamespace})
			if scopeErr != nil {
				return nil, SelectorScopeAny, fmt.Errorf("build source-model-scope requirement: %w", scopeErr)
			}
			sel = sel.Add(*scopeReq)
		case aimv1alpha1.ProfileSelectorScopeCluster:
			scope = SelectorScopeCluster
			scopeReq, scopeErr := labels.NewRequirement(constants.LabelKeySourceModelScope, selection.Equals, []string{constants.LabelValueSourceModelScopeCluster})
			if scopeErr != nil {
				return nil, SelectorScopeAny, fmt.Errorf("build source-model-scope requirement: %w", scopeErr)
			}
			sel = sel.Add(*scopeReq)
		}
	}
	if selector.Origin != "" {
		req, err := labels.NewRequirement(constants.LabelKeyProfileOrigin, selection.Equals, []string{string(selector.Origin)})
		if err != nil {
			return nil, SelectorScopeAny, fmt.Errorf("build origin requirement: %w", err)
		}
		sel = sel.Add(*req)
	}
	return sel, scope, nil
}
