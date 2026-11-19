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
	"errors"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/profilecache"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/serving"
)

const (
	runtimeProjectionComponent              = "RuntimeProjection"
	runtimeProjectionReadyCondition         = runtimeProjectionComponent + controllerutils.ComponentConditionSuffix
	runtimeProjectionElectionComponent      = "RuntimeProjectionElection"
	runtimeProjectionElectionReadyCondition = runtimeProjectionElectionComponent + controllerutils.ComponentConditionSuffix
)

// runtimeProjectable reports whether a profile should have a runtime projected.
// A profile is projectable when it is deployable, carries an image, and either
// needs no accelerator (no hardware constraint to satisfy) or has at least one
// matching device-plugin node — read from the freshly-computed match result,
// not last reconcile's status. DRA nodes do not count: a projected runtime is
// self-contained only with a device-plugin GPU request, and DRA services use
// serving.DRARuntimeName instead.
func runtimeProjectable(spec aimv1alpha2.AIMProfileSpecCommon, deployable bool, match NodeMatchResult) bool {
	if !deployable || spec.Image == "" {
		return false
	}
	if !HasProfileAcceleratorRequirement(spec) {
		return true
	}
	return match.MatchingNodes > 0
}

// modelSlugProjectable reports whether a profile should publish the
// Reduced/Both model-slug runtime. A profile must first win the deterministic
// same-scope election among all declared primaries for its aimId; the normal
// projectability gate is then applied to that one winner.
func modelSlugProjectable(spec aimv1alpha2.AIMProfileSpecCommon, projectable, elected bool) bool {
	return elected && projectable && spec.Primary && spec.AimId != ""
}

// electNamespaceModelSlugWinner elects exactly one namespace profile among all
// declared primaries for the same aimId. The shared AIMService profile ranker is
// used so native model-slug projection and AIMService auto-selection cannot
// disagree about which profile is preferred.
func electNamespaceModelSlugWinner(
	ctx context.Context,
	c client.Client,
	profile *aimv1alpha2.AIMProfile,
) (bool, error) {
	if profile == nil || !profile.Spec.Primary || profile.Spec.AimId == "" {
		return false, nil
	}

	var peers aimv1alpha2.AIMProfileList
	if err := c.List(
		ctx,
		&peers,
		client.InNamespace(profile.Namespace),
		client.MatchingFields{aimv1alpha2.ProfileAimIdIndexKey: profile.Spec.AimId},
	); err != nil {
		return false, fmt.Errorf("list namespace primary profiles for runtime projection: %w", err)
	}

	candidates := peers.Items[:0]
	for i := range peers.Items {
		if peers.Items[i].Spec.Primary {
			candidates = append(candidates, peers.Items[i])
		}
	}
	SortNamespaceProfiles(candidates)
	winner := SelectBestNamespaceProfile(candidates)
	return winner != nil && winner.Name == profile.Name, nil
}

// electClusterModelSlugWinner is the cluster-scoped counterpart of
// electNamespaceModelSlugWinner.
func electClusterModelSlugWinner(
	ctx context.Context,
	c client.Client,
	profile *aimv1alpha2.AIMClusterProfile,
) (bool, error) {
	if profile == nil || !profile.Spec.Primary || profile.Spec.AimId == "" {
		return false, nil
	}

	var peers aimv1alpha2.AIMClusterProfileList
	if err := c.List(
		ctx,
		&peers,
		client.MatchingFields{aimv1alpha2.ProfileAimIdIndexKey: profile.Spec.AimId},
	); err != nil {
		return false, fmt.Errorf("list cluster primary profiles for runtime projection: %w", err)
	}

	candidates := peers.Items[:0]
	for i := range peers.Items {
		if peers.Items[i].Spec.Primary {
			candidates = append(candidates, peers.Items[i])
		}
	}
	SortClusterProfiles(candidates)
	winner := SelectBestClusterProfile(candidates)
	return winner != nil && winner.Name == profile.Name, nil
}

func validateNamespaceRuntimeProjection(
	mode aimv1alpha2.RuntimeProjectionMode,
	profile *aimv1alpha2.AIMProfile,
	obs ProfileObservation,
) error {
	if profile == nil {
		return nil
	}
	spec := profile.Spec.AIMProfileSpecCommon
	input := serving.NamespaceRuntimeInput{
		ProfileName:  profile.Name,
		Namespace:    profile.Namespace,
		Spec:         &spec,
		YAMLContract: obs.yamlContract,
		Resources:    obs.resolvedResources,
		NodeAffinity: obs.matchResult.NodeAffinity,
		Cache:        obs.profileCache,
	}
	if shouldMaintainNamespaceRuntime(mode, profile, obs.projectable) {
		if _, _, err := serving.BuildNamespaceServingRuntime(input); err != nil {
			return fmt.Errorf("build per-profile ServingRuntime: %w", err)
		}
	}
	if shouldMaintainNamespaceModelSlugRuntime(mode, profile, obs.projectable, obs.modelSlugWinner) {
		input.Name = serving.ModelSlugRuntimeName(spec.AimId)
		if _, _, err := serving.BuildNamespaceServingRuntime(input); err != nil {
			return fmt.Errorf("build model-slug ServingRuntime: %w", err)
		}
	}
	return nil
}

func validateClusterRuntimeProjection(
	mode aimv1alpha2.RuntimeProjectionMode,
	profile *aimv1alpha2.AIMClusterProfile,
	obs ClusterProfileObservation,
) error {
	if profile == nil {
		return nil
	}
	spec := profile.Spec.AIMProfileSpecCommon
	input := serving.ClusterRuntimeInput{
		ProfileName:  profile.Name,
		Spec:         &spec,
		Resources:    obs.resolvedResources,
		NodeAffinity: obs.matchResult.NodeAffinity,
	}
	if shouldMaintainClusterRuntime(mode, profile, obs.projectable) {
		if _, err := serving.BuildClusterServingRuntime(input); err != nil {
			return fmt.Errorf("build per-profile ClusterServingRuntime: %w", err)
		}
	}
	if shouldMaintainClusterModelSlugRuntime(mode, profile, obs.projectable, obs.modelSlugWinner) {
		input.Name = serving.ModelSlugRuntimeName(spec.AimId)
		if _, err := serving.BuildClusterServingRuntime(input); err != nil {
			return fmt.Errorf("build model-slug ClusterServingRuntime: %w", err)
		}
	}
	return nil
}

// fetchMountableCache resolves the Ready, profile-owned (Shared) AIMProfileCache
// whose ready artifacts the projected namespace runtime mounts. It resolves the
// SAME cache the lazy InferenceService-watch shadow would (via
// profilecache.FindReadyShared), so an eager per-profile / model-slug runtime and
// a lazy shadow mount identically — serving correctness does not depend on the
// projection mode.
//
// Deliberately NOT gated on profile.spec.caching.enabled: a service-driven Shared
// cache (AIMService caching.mode=Shared, the default) is named
// <profile>-cache-<hash> and never flips the profile's caching flag, yet it must
// still be mounted here or a Shared-mode service resolving to a namespace profile
// would cold-pull weights on every pod start (an outright failure air-gapped).
// A list error must remain distinguishable from "no Ready cache": treating an
// unknown result as nil would force-apply a cacheless runtime and remove a
// working PVC mount. The caller reports the infrastructure failure and skips
// projection apply, preserving the last known-good runtime.
func fetchMountableCache(
	ctx context.Context,
	c client.Client,
	profile *aimv1alpha2.AIMProfile,
) (*aimv1alpha2.AIMProfileCache, error) {
	cache, err := profilecache.FindReadyShared(ctx, c, profile.Namespace, profile.Name, aimv1alpha1.AIMResolutionScopeNamespace)
	if err != nil {
		return nil, err
	}
	return cache, nil
}

// planNamespaceRuntime appends the eager ServingRuntime (+ colocated profile
// ConfigMap) projection for a namespace-scoped profile. Both objects share the
// per-profile runtime name (serving.RuntimeName — aim-<truncated-profile>-<hash>
// under the reserved prefix, always ≤63 chars and unguessable) and are routed
// through the force-apply bucket so AIM Engine authoritatively reconciles drift;
// the pipeline sets the owner reference for garbage collection. Force-apply is
// safe by the reserved aim- prefix policy (CONTEXT.md "Authoritative apply");
// the hashed name adds an EXTRA margin specific to the per-profile scheme
// — it cannot collide with a hand-authored object even by accident, so a
// force-apply only ever clobbers a runtime AIM Engine owns.
//
// Projection is additive: a runtime is first emitted only while the profile is
// projectable. Once status records that projection, later gate failures keep
// reasserting it instead of leaving stale content behind. This matters when an
// AIMService resource override can still use a profile whose default footprint
// no longer fits a node. Removal remains asymmetric — the runtime is torn out
// only by ownerRef GC on profile delete or an explicit disable.
func planNamespaceRuntime(
	ctx context.Context,
	plan *controllerutils.PlanResult,
	mode aimv1alpha2.RuntimeProjectionMode,
	profile *aimv1alpha2.AIMProfile,
	obs ProfileObservation,
) {
	if !shouldMaintainNamespaceRuntime(mode, profile, obs.projectable) {
		return
	}
	spec := profile.Spec.AIMProfileSpecCommon
	if spec.Image == "" {
		return
	}
	runtime, configMap, err := serving.BuildNamespaceServingRuntime(serving.NamespaceRuntimeInput{
		ProfileName:  profile.Name,
		Namespace:    profile.Namespace,
		Spec:         &spec,
		YAMLContract: obs.yamlContract,
		Resources:    obs.resolvedResources,
		NodeAffinity: obs.matchResult.NodeAffinity,
		Cache:        obs.profileCache,
	})
	if err != nil {
		log.FromContext(ctx).Error(err, "failed to build projected ServingRuntime", "profile", profile.Name)
		return
	}

	markEagerProjection(runtime)
	markEagerProjection(configMap)
	plan.ApplyWithForce(runtime)
	plan.ApplyWithForce(configMap)
}

func shouldMaintainNamespaceRuntime(
	mode aimv1alpha2.RuntimeProjectionMode,
	profile *aimv1alpha2.AIMProfile,
	projectable bool,
) bool {
	if profile == nil {
		return false
	}
	// Projection mode controls first publication, not ownership of an existing
	// eager projection. Once status records the per-profile runtime, keep
	// reasserting it across an Exhaustive/Both -> Reduced switch: the lazy
	// controller intentionally defers to its eager marker.
	return (mode.ProjectsPerProfile() && projectable) ||
		profile.Status.ProjectedRuntimeName == serving.RuntimeName(profile.Name)
}

// markEagerProjection stamps the eager-projection marker on a per-profile /
// model-slug namespace ServingRuntime (or its colocated ConfigMap) the profile
// reconciler force-applies. The marker settles managedFields ownership after a
// projection-mode flip: when the mode turns eager projection on for a name a
// lazy shadow already materialised (Reduced -> Exhaustive/Both, or Both
// toggled), both the profile reconciler and the lazy InferenceService-watch
// reconciler would otherwise force-apply the same namespace ServingRuntime under
// different field managers every reconcile, ping-ponging its managedFields /
// resourceVersion (the content is identical, so there is no spec churn — just
// noisy, dual-authority ownership).
//
// Because it stamps the SAME LabelRuntimeProjection key the lazy path uses, the
// eager force-apply (SSA + ForceOwnership, see plan.ApplyWithForce) reclaims the
// label key from the lazy field manager and overwrites the lazy marker with the
// eager value — an eager apply that merely omitted the label would leave the
// lazy-owned key in place. With the marker no longer "lazy",
// ownedByLazyProjection reports false, the lazy reconciler's
// namespaceRuntimeComplete returns true, and the lazy path defers, leaving the
// eager manager as the single owner that reasserts drift thereafter. On the
// reverse flip (eager off) the marked runtime simply survives via ownerRef GC
// (additive teardown); a subsequent consumer defers to it exactly as it would a
// hand-authored runtime, so the object is never stranded or re-shadowed.
func markEagerProjection(obj metav1.Object) {
	labels := obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels[constants.LabelRuntimeProjection] = constants.LabelValueRuntimeProjectionEager
	obj.SetLabels(labels)
}

// planClusterRuntime appends the eager ClusterServingRuntime projection for a
// cluster-scoped profile. The bare CSR (no colocated ConfigMap — a cluster
// runtime cannot guarantee one in an arbitrary consumer namespace) carries the
// per-profile runtime name (serving.RuntimeName — aim-<truncated-profile>-<hash>,
// length-safe, and collision-proof via the hash as an extra margin on top of the
// reserved-prefix authority) and is routed through the force-apply bucket; the
// pipeline sets the owner reference for garbage collection. Additive projection
// and asymmetric teardown apply exactly as in planNamespaceRuntime.
func planClusterRuntime(
	ctx context.Context,
	plan *controllerutils.PlanResult,
	mode aimv1alpha2.RuntimeProjectionMode,
	profile *aimv1alpha2.AIMClusterProfile,
	obs ClusterProfileObservation,
) {
	if !shouldMaintainClusterRuntime(mode, profile, obs.projectable) {
		return
	}
	spec := profile.Spec.AIMProfileSpecCommon
	if spec.Image == "" {
		return
	}

	runtime, err := serving.BuildClusterServingRuntime(serving.ClusterRuntimeInput{
		ProfileName:  profile.Name,
		Spec:         &spec,
		Resources:    obs.resolvedResources,
		NodeAffinity: obs.matchResult.NodeAffinity,
	})
	if err != nil {
		log.FromContext(ctx).Error(err, "failed to build projected ClusterServingRuntime", "profile", profile.Name)
		return
	}

	plan.ApplyWithForce(runtime)
}

func shouldMaintainClusterRuntime(
	mode aimv1alpha2.RuntimeProjectionMode,
	profile *aimv1alpha2.AIMClusterProfile,
	projectable bool,
) bool {
	if profile == nil {
		return false
	}
	// Match the namespace lifecycle: changing mode stops new per-profile
	// publication but does not abandon a runtime already recorded in status.
	return (mode.ProjectsPerProfile() && projectable) ||
		profile.Status.ProjectedRuntimeName == serving.RuntimeName(profile.Name)
}

// planNamespaceModelSlugRuntime appends the Reduced/Both model-slug primary
// ServingRuntime (+ colocated profile ConfigMap) for the elected namespace
// primary. The objects are named aim-<model-slug> (derived from spec.aimId,
// vendor/precision-independent), giving native KServe InferenceServices one
// stable runtime name per model to reference explicitly.
// autoSelect stays OFF (like every projected runtime): all runtimes share the
// single model format, so autoSelect would collide across models rather than
// resolve one. The correlator labels still point to the backing primary profile.
//
// Both objects route through the authoritative force-apply bucket (SSA +
// ForceOwnership); the pipeline sets the owner reference for GC. Force-apply here
// is safe by the reserved aim- prefix policy ALONE (CONTEXT.md "Reserved `aim-`
// prefix" / "Authoritative apply"): AIM Engine owns everything under
// aim- exclusively and is authoritative over it, so it force-applies
// unconditionally — no pre-apply Get, no per-name branching. The justification is
// the reserved PREFIX, not name unguessability: unlike the per-profile runtime,
// the model-slug name (aim-<model-slug>) is deliberately READABLE and therefore
// guessable, so the per-profile hashed-name "cannot collide by accident" argument
// (see serving.RuntimeName) does NOT cover this path — and does not need to.
//
// Projection mode controls first publication and active winner election. Once
// status records this profile as the model-slug owner, a mode that disables
// model-slug publication still reasserts the existing runtime so its eager marker
// cannot strand stale content. When model-slug publication is active, the live
// election remains authoritative and a losing profile stops applying the name.
func planNamespaceModelSlugRuntime(
	ctx context.Context,
	plan *controllerutils.PlanResult,
	mode aimv1alpha2.RuntimeProjectionMode,
	profile *aimv1alpha2.AIMProfile,
	obs ProfileObservation,
) {
	spec := profile.Spec.AIMProfileSpecCommon
	if !shouldMaintainNamespaceModelSlugRuntime(mode, profile, obs.projectable, obs.modelSlugWinner) {
		return
	}
	runtime, configMap, err := serving.BuildNamespaceServingRuntime(serving.NamespaceRuntimeInput{
		ProfileName:  profile.Name,
		Name:         serving.ModelSlugRuntimeName(spec.AimId),
		Namespace:    profile.Namespace,
		Spec:         &spec,
		YAMLContract: obs.yamlContract,
		Resources:    obs.resolvedResources,
		NodeAffinity: obs.matchResult.NodeAffinity,
		Cache:        obs.profileCache,
	})
	if err != nil {
		log.FromContext(ctx).Error(err, "failed to build model-slug primary ServingRuntime", "profile", profile.Name)
		return
	}

	markEagerProjection(runtime)
	markEagerProjection(configMap)
	plan.ApplyWithForce(runtime)
	plan.ApplyWithForce(configMap)
}

func shouldMaintainNamespaceModelSlugRuntime(
	mode aimv1alpha2.RuntimeProjectionMode,
	profile *aimv1alpha2.AIMProfile,
	projectable, modelSlugWinner bool,
) bool {
	if profile == nil {
		return false
	}
	if mode.ProjectsModelSlug() {
		return modelSlugProjectable(profile.Spec.AIMProfileSpecCommon, projectable, modelSlugWinner)
	}
	return profile.Status.ProjectedModelSlugRuntimeName ==
		serving.ModelSlugRuntimeName(profile.Spec.AimId)
}

// planClusterModelSlugRuntime appends the Reduced/Both model-slug primary
// ClusterServingRuntime for the elected cluster primary. The bare CSR (no
// colocated ConfigMap) is named aim-<model-slug>; autoSelect stays OFF (native
// consumers reference it by name). It is force-applied
// authoritatively (SSA + ForceOwnership), safe by the reserved aim- prefix policy
// — NOT by name unguessability: the model-slug name is readable and guessable, so
// the per-profile hashed-name collision argument does not apply here (see
// planNamespaceModelSlugRuntime and serving.ModelSlugRuntimeName; CONTEXT.md
// "Reserved `aim-` prefix" / "Authoritative apply"). See
// planNamespaceModelSlugRuntime for the lifecycle notes.
func planClusterModelSlugRuntime(
	ctx context.Context,
	plan *controllerutils.PlanResult,
	mode aimv1alpha2.RuntimeProjectionMode,
	profile *aimv1alpha2.AIMClusterProfile,
	obs ClusterProfileObservation,
) {
	spec := profile.Spec.AIMProfileSpecCommon
	if !shouldMaintainClusterModelSlugRuntime(mode, profile, obs.projectable, obs.modelSlugWinner) {
		return
	}

	runtime, err := serving.BuildClusterServingRuntime(serving.ClusterRuntimeInput{
		ProfileName:  profile.Name,
		Name:         serving.ModelSlugRuntimeName(spec.AimId),
		Spec:         &spec,
		Resources:    obs.resolvedResources,
		NodeAffinity: obs.matchResult.NodeAffinity,
	})
	if err != nil {
		log.FromContext(ctx).Error(err, "failed to build model-slug primary ClusterServingRuntime", "profile", profile.Name)
		return
	}

	plan.ApplyWithForce(runtime)
}

func shouldMaintainClusterModelSlugRuntime(
	mode aimv1alpha2.RuntimeProjectionMode,
	profile *aimv1alpha2.AIMClusterProfile,
	projectable, modelSlugWinner bool,
) bool {
	if profile == nil {
		return false
	}
	if mode.ProjectsModelSlug() {
		return modelSlugProjectable(profile.Spec.AIMProfileSpecCommon, projectable, modelSlugWinner)
	}
	return profile.Status.ProjectedModelSlugRuntimeName ==
		serving.ModelSlugRuntimeName(profile.Spec.AimId)
}

// recordProjectedRuntimeNames publishes the projected runtime name(s) on status
// so a profile maps to its runtime without reversing the opaque hashed name.
//
// It follows the projection's additive/degrade lifecycle: a name is written
// while the profile projects that runtime and retained when only the
// projectability gate later flips. The model-slug field is cleared when the
// profile loses the election because another profile now owns the shared
// runtime. The per-profile and model-slug fields are each set only under a mode
// that projects them, matching the plan guards.
func recordProjectedRuntimeNames(
	status *aimv1alpha2.AIMProfileStatus,
	mode aimv1alpha2.RuntimeProjectionMode,
	spec aimv1alpha2.AIMProfileSpecCommon,
	profileName string,
	projectable, modelSlugWinner bool,
) {
	if mode.ProjectsPerProfile() && projectable {
		status.ProjectedRuntimeName = serving.RuntimeName(profileName)
	}
	if mode.ProjectsModelSlug() {
		switch {
		case modelSlugProjectable(spec, projectable, modelSlugWinner):
			status.ProjectedModelSlugRuntimeName = serving.ModelSlugRuntimeName(spec.AimId)
		case !modelSlugWinner:
			// Losing an election is different from a projectability gate flip:
			// another profile now owns and publishes the shared runtime, so this
			// profile must stop claiming the model-slug name in status.
			status.ProjectedModelSlugRuntimeName = ""
		}
	}
}

// decorateProjectionCondition records the RuntimeProjected condition for the
// mode in effect. A builder error records False/RuntimeProjectionFailed, while
// an infrastructure error preserves the prior informational condition.
// Otherwise per-profile modes (Exhaustive/Both) reflect this profile's own
// projection. Reduced-only mode has no per-profile runtime, so it reflects the
// condition only for the elected primary profile (the one that publishes the
// model-slug runtime); non-winners project nothing and stay silent.
func decorateProjectionCondition(
	cm *controllerutils.ConditionManager,
	mode aimv1alpha2.RuntimeProjectionMode,
	modelSlugWinner, projectable bool,
	nodeErr error,
	projectionErr error,
) {
	if projectionErr != nil {
		var categorized controllerutils.StateEngineError
		if errors.As(projectionErr, &categorized) &&
			categorized.Category() == controllerutils.ErrorCategoryInfrastructure {
			return
		}
		cm.MarkFalse(
			aimv1alpha2.AIMProfileConditionRuntimeProjected,
			aimv1alpha2.AIMProfileReasonRuntimeProjectionFailed,
			fmt.Sprintf("Failed to build projected runtime: %v", projectionErr),
		)
		return
	}

	// RuntimeProjectionReady is emitted by component health only while runtime
	// construction is failing. Remove the old component condition explicitly
	// after recovery so it cannot continue gating aggregate readiness.
	cm.Delete(runtimeProjectionReadyCondition)

	switch {
	case mode.ProjectsPerProfile():
		// Exhaustive/Both project a per-profile runtime. Asymmetric teardown: when
		// the gate flips after a prior projection, the runtime survives (the plan
		// stops emitting it and nothing prunes it) and we degrade rather than go
		// silent. "Was projected before" is read from the last-reconcile condition
		// the ConditionManager was seeded with — no client call.
		decorateRuntimeProjection(cm, projectable, priorRuntimeProjected(cm), nodeErr)
	case mode.ProjectsModelSlug() && modelSlugWinner:
		// Reduced publishes only the model-slug primary. onPrimaryUnavailable
		// (degrade vs repoint of the shared slug runtime) is deferred, so this path
		// never degrades: it reports True while projectable and stays silent
		// otherwise. Passing wasProjected=false preserves that behaviour (the slug
		// runtime's survival was never tracked before this refactor either).
		decorateRuntimeProjection(cm, projectable, false, nodeErr)
	}
}

// priorRuntimeProjected reports whether the profile's RuntimeProjected condition
// was True on the previous reconcile. The pipeline seeds the ConditionManager
// from the object's existing status before any decoration runs, and nothing sets
// RuntimeProjected earlier in the reconcile, so this reads last-reconcile state
// without a client call. It is the "did a runtime exist before" signal that
// distinguishes a never-projected profile (stay silent) from one whose gate has
// since flipped (mark Degraded, keep the runtime).
func priorRuntimeProjected(cm *controllerutils.ConditionManager) bool {
	prior := cm.Get(aimv1alpha2.AIMProfileConditionRuntimeProjected)
	return prior != nil && prior.Status == metav1.ConditionTrue
}

// decorateRuntimeProjection records the RuntimeProjected condition: True when a
// runtime is projected, False (RuntimeDegraded) when the projection gate flipped
// but a previously-projected runtime is kept rather than deleted. Stays silent
// when nothing was ever projected, and during a transient node-list failure (so
// a momentary API hiccup does not flap the condition to Degraded). This is
// informational and does not gate the aggregated Ready status (the type does not
// end in "Ready").
func decorateRuntimeProjection(cm *controllerutils.ConditionManager, projectable, wasProjected bool, nodeErr error) {
	if projectable {
		cm.MarkTrue(
			aimv1alpha2.AIMProfileConditionRuntimeProjected,
			aimv1alpha2.AIMProfileReasonRuntimeProjected,
			"Runtime projected for this profile",
		)
		return
	}
	if !wasProjected || nodeErr != nil {
		return
	}
	cm.MarkFalse(
		aimv1alpha2.AIMProfileConditionRuntimeProjected,
		aimv1alpha2.AIMProfileReasonRuntimeDegraded,
		"Projection gate no longer satisfied; existing runtime kept (not deleted)",
	)
}
