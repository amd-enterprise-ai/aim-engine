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

// Package runtimeprojection holds the lazy KServe ServingRuntime projection.
// When a managed namespaced ServingRuntime key does not already resolve to a
// complete ServingRuntime, AIM Engine materializes that ServingRuntime plus its
// colocated profile ConfigMap, owned by the backing profile. This shadows a bare
// KServe ClusterServingRuntime with a complete namespaced ServingRuntime (the
// ClusterServingRuntime stays standalone-usable elsewhere).
//
// DesiredForRuntime is the ServingRuntime-keyed pure seam. DesiredFor remains
// as a compatibility wrapper for callers that already hold a KServe
// InferenceService.
package runtimeprojection

import (
	"fmt"
	"strings"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/profileyaml"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/serving"
)

// BackingProfile is the scope-agnostic view of the profile that backs a
// referenced KServe ServingRuntime or ClusterServingRuntime: either a cluster
// AIMClusterProfile or a namespace AIMProfile. Both embed AIMProfileSpecCommon
// and AIMProfileStatus and are client.Objects, which is everything the lazy
// projection needs to build and own a complete namespaced ServingRuntime — so
// resolution can settle on either scope and the shared builder handles the rest.
type BackingProfile interface {
	client.Object
	GetProfileSpecCommon() aimv1alpha2.AIMProfileSpecCommon
	GetStatus() *aimv1alpha2.AIMProfileStatus
}

// ProjectionState carries the resolved cluster state the pure projection seam
// needs to decide whether (and what) to materialize for a ServingRuntime key.
//
// The profile candidates set the backing-profile resolution order: an existing
// lazy sibling's ownerReference wins, then a managed ClusterServingRuntime's
// backing profile, then an indexed InferenceService annotation. There is no
// reverse name-lookup candidate because hashed per-profile names aren't
// reversible.
//
// The annotation candidate carries either scope (namespace AIMProfile or cluster
// AIMClusterProfile, resolved namespace-first), which lets a Reduced-mode
// namespace-profile-backed service self-complete with no eager per-profile
// ServingRuntime.
type ProjectionState struct {
	// NamespaceRuntimeComplete reports that the referenced KServe
	// ServingRuntime or ClusterServingRuntime name already resolves to a complete
	// namespaced ServingRuntime plus its colocated ConfigMap. The lazy projection
	// must defer to that object — e.g. a same-namespace profile's eager
	// projection or a hand-authored ServingRuntime. A prior lazy shadow does NOT
	// set this: it must be re-applied so drift, a late cache, or backing-profile
	// changes are reasserted. When true, no materialization is needed.
	NamespaceRuntimeComplete bool

	// ExistingShadowProfile is resolved from the ownerReference on either
	// existing lazy sibling. It keeps inactive and partially deleted shadows
	// anchored to their original profile.
	ExistingShadowProfile BackingProfile

	// ManagedRuntimeProfile is the profile resolved by following the referenced
	// managed KServe ClusterServingRuntime's ownerRef/label (the native
	// annotation-free flow). Always an AIMClusterProfile in practice (a
	// namespace profile projects a namespaced ServingRuntime, not a
	// ClusterServingRuntime). Nil when the referenced object is not a managed
	// ClusterServingRuntime.
	ManagedRuntimeProfile BackingProfile

	// AnnotatedProfile is the profile named by the AIMService-stamped
	// runtime-profile annotation on the InferenceService (bootstrap fast-path,
	// used when its referenced ServingRuntime or ClusterServingRuntime is not yet
	// created). A namespace AIMProfile in the InferenceService's namespace or a
	// cluster AIMClusterProfile. Nil when absent.
	AnnotatedProfile BackingProfile

	// Cache is the ready profile-owned AIMProfileCache colocated in the ISVC's
	// namespace, when the backing profile opts into caching. Nil otherwise; it
	// contributes the profile-owned cache mount on the materialized
	// ServingRuntime.
	Cache *aimv1alpha2.AIMProfileCache
}

// BackingProfile returns the profile the referenced KServe ServingRuntime or
// ClusterServingRuntime resolves to, applying this order: existing lazy
// ServingRuntime/ConfigMap owner, managed ClusterServingRuntime owner, then the
// AIMService-stamped InferenceService annotation fast-path. Returns nil when no
// candidate resolves.
// The candidates carry genuine nil interfaces (not typed-nil pointers), so the
// first-non-nil switch is safe. There is no name-lookup fallback because hashed
// per-profile ServingRuntime names are not reversible.
func (s ProjectionState) BackingProfile() BackingProfile {
	switch {
	case s.ExistingShadowProfile != nil:
		return s.ExistingShadowProfile
	case s.ManagedRuntimeProfile != nil:
		return s.ManagedRuntimeProfile
	default:
		return s.AnnotatedProfile
	}
}

// DesiredProjection is what DesiredFor decides to materialize. A zero value
// (nil Runtime) means "nothing to do".
type DesiredProjection struct {
	// Runtime is the complete namespaced KServe ServingRuntime to apply, or nil.
	Runtime *kservev1alpha1.ServingRuntime
	// ConfigMap is the colocated profile ConfigMap to apply, or nil.
	ConfigMap *corev1.ConfigMap
	// Owner is the backing profile (cluster or namespace scope) that owns the
	// materialized objects, so they are garbage collected when the profile is
	// deleted. Nil when nothing is materialized.
	Owner BackingProfile
}

// ReferencedRuntimeName returns the KServe ServingRuntime or
// ClusterServingRuntime name an InferenceService explicitly references via
// predictor.model.runtime, or "" when none is set.
func ReferencedRuntimeName(isvc *servingv1beta1.InferenceService) string {
	model := isvc.Spec.Predictor.Model
	if model == nil || model.Runtime == nil {
		return ""
	}
	return *model.Runtime
}

// DesiredFor is the compatibility wrapper for InferenceService-keyed callers.
func DesiredFor(isvc *servingv1beta1.InferenceService, state ProjectionState) (DesiredProjection, error) {
	return DesiredForRuntime(isvc.Namespace, ReferencedRuntimeName(isvc), state)
}

// DesiredForRuntime decides what to materialize for a
// namespace/ServingRuntime-name key.
// It returns nothing unless the key is under the reserved aim- prefix, no
// complete eager/hand-authored ServingRuntime owns the key, and a backing
// profile resolves. In that case it returns the ServingRuntime and colocated
// ConfigMap owned by that profile.
//
// Names outside the reserved aim- prefix are left alone: AIM Engine never reads,
// creates, or modifies ServingRuntime objects it does not own.
func DesiredForRuntime(namespace, runtimeName string, state ProjectionState) (DesiredProjection, error) {
	input, profile, err := resolveNamespaceInput(namespace, runtimeName, state)
	if err != nil || profile == nil {
		return DesiredProjection{}, err
	}

	runtime, configMap, err := serving.BuildNamespaceServingRuntime(input)
	if err != nil {
		return DesiredProjection{}, fmt.Errorf("build namespaced KServe ServingRuntime for profile %q: %w", profile.GetName(), err)
	}

	// Mark both objects as lazily materialized so the controller can tell this
	// shadow apart from an eager per-profile projection of the same name (they
	// otherwise share the AIMProfile ownerReference for a namespace profile) and
	// re-apply it on every reconcile to self-heal.
	markLazyProjection(runtime)
	markLazyProjection(configMap)

	return DesiredProjection{Runtime: runtime, ConfigMap: configMap, Owner: profile}, nil
}

// DesiredLLMInferenceServiceConfigForRuntime returns the namespaced KServe
// LLMInferenceServiceConfig the lazy projection materializes next to the
// ServingRuntime of the same name, under exactly the gates DesiredForRuntime
// applies. It mounts the ConfigMap DesiredForRuntime returns. Nil when
// DesiredForRuntime materializes nothing.
func DesiredLLMInferenceServiceConfigForRuntime(
	namespace, runtimeName string,
	state ProjectionState,
) (*kservev1alpha2.LLMInferenceServiceConfig, error) {
	input, profile, err := resolveNamespaceInput(namespace, runtimeName, state)
	if err != nil || profile == nil {
		return nil, err
	}
	config, err := serving.BuildNamespaceLLMInferenceServiceConfig(input)
	if err != nil {
		return nil, fmt.Errorf("build namespaced LLMInferenceServiceConfig for profile %q: %w", profile.GetName(), err)
	}
	markLazyProjection(config)
	return config, nil
}

// resolveNamespaceInput applies the lazy projection gates for a
// namespace/name key and returns the builder input for the resolved backing
// profile. A nil profile means nothing should be materialized: the name is
// outside the reserved aim- prefix, a complete eager or hand-authored
// projection already owns it, or no backing profile projects that name.
func resolveNamespaceInput(
	namespace, runtimeName string,
	state ProjectionState,
) (serving.NamespaceRuntimeInput, BackingProfile, error) {
	if runtimeName == "" {
		return serving.NamespaceRuntimeInput{}, nil, nil
	}

	// Only ever touch names under the reserved aim- prefix; any other name is a
	// hand-authored ServingRuntime AIM Engine must not shadow.
	if !strings.HasPrefix(runtimeName, serving.RuntimeNamePrefix) {
		return serving.NamespaceRuntimeInput{}, nil, nil
	}

	// The referenced KServe ServingRuntime or ClusterServingRuntime name already
	// resolves to a complete namespaced ServingRuntime here (same-namespace
	// profile or a prior shadow) — nothing to do.
	if state.NamespaceRuntimeComplete {
		return serving.NamespaceRuntimeInput{}, nil, nil
	}

	profile := state.BackingProfile()
	if profile == nil {
		return serving.NamespaceRuntimeInput{}, nil, nil
	}

	// Only shadow a ServingRuntime name the resolved profile actually projects.
	// A mismatch means the reference does not belong to this profile, so
	// materializing under runtimeName would not shadow it.
	if !runtimeNameMatchesProfile(profile, runtimeName) {
		return serving.NamespaceRuntimeInput{}, nil, nil
	}

	spec := profile.GetProfileSpecCommon()
	status := profile.GetStatus()
	origin := profileyaml.EffectiveOrigin(profile.GetLabels(), status.Origin)
	yamlContract, err := profileyaml.ForProfile(profile.GetAnnotations(), origin, &spec)
	if err != nil {
		return serving.NamespaceRuntimeInput{}, nil, fmt.Errorf("parse profile YAML contract for profile %q: %w", profile.GetName(), err)
	}
	input := serving.NamespaceRuntimeInput{
		ProfileName:  profile.GetName(),
		Namespace:    namespace,
		Spec:         &spec,
		YAMLContract: yamlContract,
		Resources:    status.Resources,
		NodeAffinity: status.ResolvedNodeAffinity,
		Cache:        state.Cache,
	}

	// A reference that matched via the model-slug name (not the per-profile
	// name) completes the Reduced/Both-mode model-slug primary: materialize the
	// shadow under the referenced slug name, mirroring the eager slug
	// ServingRuntime (aimprofile.planNamespaceModelSlugRuntime). autoSelect stays
	// off on every projected ServingRuntime (the shared model format would
	// otherwise collide across models in KServe auto-selection); native
	// consumers reference the ServingRuntime by name. The correlator label still
	// points to the backing primary profile (ProfileName is unchanged).
	if runtimeName != serving.RuntimeName(profile.GetName()) && matchesModelSlugRuntime(profile, runtimeName) {
		input.Name = runtimeName
	}
	return input, profile, nil
}

// markLazyProjection stamps the lazy-projection marker label on a materialized
// shadow object, so the controller re-applies it (self-heal) rather than
// deferring to it as if it were an eager projection or hand-authored
// ServingRuntime.
func markLazyProjection(obj metav1.Object) {
	labels := obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels[constants.LabelRuntimeProjection] = constants.LabelValueRuntimeProjectionLazy
	obj.SetLabels(labels)
}

// runtimeNameMatchesProfile reports whether runtimeName is a reserved KServe
// ServingRuntime or ClusterServingRuntime name the resolved profile actually
// projects. Two names qualify: the per-profile name (aim-<profile.Name>) and the
// Reduced/Both-mode model-slug primary name (aim-<model-slug>, derived from the
// profile's aimId). A native KServe InferenceService referencing either resolves
// to this profile, so both must be shadowed cross-namespace — the slug is the
// portable, vendor-independent handle the eager slug projection publishes
// (aimprofile.planClusterModelSlugRuntime).
func runtimeNameMatchesProfile(profile BackingProfile, runtimeName string) bool {
	if serving.RuntimeName(profile.GetName()) == runtimeName {
		return true
	}
	return matchesModelSlugRuntime(profile, runtimeName)
}

// RuntimeNameMatchesProfile reports whether the profile projects runtimeName as
// a KServe ServingRuntime or ClusterServingRuntime. Controllers use it while
// deterministically resolving competing bootstrap annotations.
func RuntimeNameMatchesProfile(profile BackingProfile, runtimeName string) bool {
	return runtimeNameMatchesProfile(profile, runtimeName)
}

// matchesModelSlugRuntime reports whether runtimeName is the model-slug primary
// ServingRuntime or ClusterServingRuntime name for the profile's aimId. It
// mirrors the eager slug projection's own guard
// (aimprofile.modelSlugProjectable), which publishes a slug object only for a
// profile carrying an aimId — so an empty aimId never matches (it would
// otherwise collapse to the bare "aim-" prefix and shadow an unrelated
// reference).
func matchesModelSlugRuntime(profile BackingProfile, runtimeName string) bool {
	aimID := profile.GetProfileSpecCommon().AimId
	if aimID == "" {
		return false
	}
	return serving.ModelSlugRuntimeName(aimID) == runtimeName
}
