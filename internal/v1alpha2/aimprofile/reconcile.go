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
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/profilecache"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/profileyaml"
)

const profileCacheReconcilingReason = "ProfileCacheReconciling"

// ============================================================================
// RECONCILERS
// ============================================================================

// ProfileReconciler implements the DomainReconciler interface for namespace-scoped profiles.
type ProfileReconciler struct {
	Client client.Client
	Scheme *runtime.Scheme

	// ProjectionMode governs whether this reconciler eagerly projects a
	// ServingRuntime for the profile (Exhaustive / Both project per-profile;
	// Reduced does not).
	ProjectionMode aimv1alpha2.RuntimeProjectionMode
}

// ClusterProfileReconciler implements the DomainReconciler interface for cluster-scoped profiles.
type ClusterProfileReconciler struct {
	Client client.Client
	Scheme *runtime.Scheme

	// ProjectionMode governs whether this reconciler eagerly projects a
	// ClusterServingRuntime for the profile (Exhaustive / Both project
	// per-profile; Reduced does not).
	ProjectionMode aimv1alpha2.RuntimeProjectionMode
}

// ============================================================================
// FETCH RESULT
// ============================================================================

// ProfileFetchResult holds fetched resources for namespace-scoped profiles.
type ProfileFetchResult struct {
	profile *aimv1alpha2.AIMProfile
	nodes   []corev1.Node
	nodeErr error

	// modelSlugWinner is true only for the deterministic winner among
	// same-namespace primary profiles sharing this profile's aimId.
	modelSlugWinner      bool
	modelSlugElectionErr error

	// profileCache is the Ready, profile-owned (Shared) AIMProfileCache backing
	// this profile — whether created by the profile (caching.enabled) or by an
	// AIMService in Shared mode. Contributes the cache mount on the projected
	// runtime; resolved identically to the lazy shadow so serving does not depend
	// on the projection mode. Nil when no Ready Shared cache exists.
	profileCache    *aimv1alpha2.AIMProfileCache
	profileCacheErr error
}

// ClusterProfileFetchResult holds fetched resources for cluster-scoped profiles.
type ClusterProfileFetchResult struct {
	profile *aimv1alpha2.AIMClusterProfile
	nodes   []corev1.Node
	nodeErr error

	// modelSlugWinner is true only for the deterministic winner among
	// cluster primary profiles sharing this profile's aimId.
	modelSlugWinner      bool
	modelSlugElectionErr error
}

// ============================================================================
// FETCH — Namespace-scoped
// ============================================================================

func (r *ProfileReconciler) FetchRemoteState(
	ctx context.Context,
	c client.Client,
	reconcileCtx controllerutils.ReconcileContext[*aimv1alpha2.AIMProfile],
) ProfileFetchResult {
	profile := reconcileCtx.Object
	result := ProfileFetchResult{profile: profile}

	if HasProfileAcceleratorRequirement(profile.Spec.AIMProfileSpecCommon) {
		nodes, err := listNodes(ctx, c)
		result.nodes = nodes
		result.nodeErr = err
	}

	if r.ProjectionMode.ProjectsModelSlug() {
		result.modelSlugWinner, result.modelSlugElectionErr = electNamespaceModelSlugWinner(ctx, c, profile)
	}

	// The cache mount is needed by both the per-profile runtime and the model-slug
	// primary, so resolve it whenever the mode projects either.
	if r.ProjectionMode.ProjectsPerProfile() || r.ProjectionMode.ProjectsModelSlug() {
		result.profileCache, result.profileCacheErr = fetchMountableCache(ctx, c, profile)
	}

	return result
}

// ============================================================================
// FETCH — Cluster-scoped
// ============================================================================

func (r *ClusterProfileReconciler) FetchRemoteState(
	ctx context.Context,
	c client.Client,
	reconcileCtx controllerutils.ReconcileContext[*aimv1alpha2.AIMClusterProfile],
) ClusterProfileFetchResult {
	profile := reconcileCtx.Object
	result := ClusterProfileFetchResult{profile: profile}

	if HasProfileAcceleratorRequirement(profile.Spec.AIMProfileSpecCommon) {
		nodes, err := listNodes(ctx, c)
		result.nodes = nodes
		result.nodeErr = err
	}

	if r.ProjectionMode.ProjectsModelSlug() {
		result.modelSlugWinner, result.modelSlugElectionErr = electClusterModelSlugWinner(ctx, c, profile)
	}

	return result
}

// ============================================================================
// OBSERVATION
// ============================================================================

// ProfileObservation embeds the fetch result.
type ProfileObservation struct {
	ProfileFetchResult
	matchResult       NodeMatchResult
	resolvedResources *corev1.ResourceRequirements
	deployable        bool
	sourceModel       *aimv1alpha2.ProfileSourceModel
	origin            aimv1alpha1.ProfileOrigin
	baseImage         string
	yamlContract      profileyaml.Contract
	// projectable reports whether a runtime should be projected for this profile
	// (deployable, has an image, and hardware is available — freshly computed).
	projectable        bool
	projectionDeferred bool
	projectionWaiting  bool
	projectionErr      error
}

// GetComponentHealth returns health of all components for automatic status management.
func (obs ProfileObservation) GetComponentHealth(_ context.Context, _ kubernetes.Interface) []controllerutils.ComponentHealth {
	health := buildComponentHealth(
		obs.profile.Spec.AIMProfileSpecCommon,
		obs.nodeErr,
		obs.matchResult,
	)
	health = appendRuntimeProjectionElectionHealth(health, obs.modelSlugElectionErr)
	if obs.projectionWaiting {
		health = append(health, controllerutils.ComponentHealth{
			Component:      runtimeProjectionComponent,
			State:          constants.AIMStatusProgressing,
			Reason:         profileCacheReconcilingReason,
			Message:        fmt.Sprintf("Waiting for AIMProfileCache status to catch up: %v", obs.profileCacheErr),
			DependencyType: controllerutils.DependencyTypeDownstream,
		})
	}
	return appendProjectionComponentHealth(health, obs.projectionErr)
}

// ClusterProfileObservation embeds the fetch result.
type ClusterProfileObservation struct {
	ClusterProfileFetchResult
	matchResult       NodeMatchResult
	resolvedResources *corev1.ResourceRequirements
	deployable        bool
	sourceModel       *aimv1alpha2.ProfileSourceModel
	origin            aimv1alpha1.ProfileOrigin
	baseImage         string
	yamlContract      profileyaml.Contract
	// projectable reports whether a runtime should be projected for this profile
	// (deployable, has an image, and hardware is available — freshly computed).
	projectable   bool
	projectionErr error
}

// GetComponentHealth returns health of all components for automatic status management.
func (obs ClusterProfileObservation) GetComponentHealth(_ context.Context, _ kubernetes.Interface) []controllerutils.ComponentHealth {
	health := buildComponentHealth(
		obs.profile.Spec.AIMProfileSpecCommon,
		obs.nodeErr,
		obs.matchResult,
	)
	health = appendRuntimeProjectionElectionHealth(health, obs.modelSlugElectionErr)
	return appendProjectionComponentHealth(health, obs.projectionErr)
}

func (r *ProfileReconciler) ComposeState(
	_ context.Context,
	_ controllerutils.ReconcileContext[*aimv1alpha2.AIMProfile],
	fetch ProfileFetchResult,
) ProfileObservation {
	obs := ProfileObservation{ProfileFetchResult: fetch}
	spec := fetch.profile.Spec.AIMProfileSpecCommon
	obs.resolvedResources = ResolveProfileResources(spec)
	if HasProfileAcceleratorRequirement(spec) {
		obs.matchResult = MatchProfileNodes(fetch.nodes, spec, obs.resolvedResources)
	}
	obs.deployable = IsProfileDeployable(spec)
	obs.sourceModel = SourceModelFromOwnerRefs(fetch.profile, fetch.profile.Namespace)
	obs.origin = DeriveProfileOrigin(fetch.profile)
	obs.baseImage = BaseImageFromProfile(fetch.profile, fetch.profile.Status.BaseImage)
	obs.projectable = runtimeProjectable(spec, obs.deployable, obs.matchResult)
	projectionNeeded := runtimeProjectionNeedsContract(r.ProjectionMode, spec, obs.projectable, obs.modelSlugWinner) ||
		shouldMaintainNamespaceRuntime(r.ProjectionMode, fetch.profile, obs.projectable) ||
		shouldMaintainNamespaceModelSlugRuntime(
			r.ProjectionMode, fetch.profile, obs.projectable, obs.modelSlugWinner,
		)
	if !projectionNeeded {
		return obs
	}
	if fetch.profileCacheErr != nil {
		var staleCacheStatus *profilecache.StatusStaleError
		if errors.As(fetch.profileCacheErr, &staleCacheStatus) {
			obs.projectionDeferred = true
			obs.projectionWaiting = !hasRecordedNamespaceProjection(fetch.profile)
			return obs
		}
		obs.projectionErr = controllerutils.NewInfrastructureError(
			"ProfileCacheLookupFailed",
			fmt.Sprintf("Failed to resolve the Shared AIMProfileCache for runtime projection %q", fetch.profile.Name),
			fetch.profileCacheErr,
		)
		return obs
	}
	obs.yamlContract, obs.projectionErr = profileyaml.ForProfile(fetch.profile.Annotations, obs.origin, &spec)
	if obs.projectionErr == nil {
		obs.projectionErr = validateNamespaceRuntimeProjection(r.ProjectionMode, fetch.profile, obs)
	}
	return obs
}

func (r *ClusterProfileReconciler) ComposeState(
	_ context.Context,
	_ controllerutils.ReconcileContext[*aimv1alpha2.AIMClusterProfile],
	fetch ClusterProfileFetchResult,
) ClusterProfileObservation {
	obs := ClusterProfileObservation{ClusterProfileFetchResult: fetch}
	spec := fetch.profile.Spec.AIMProfileSpecCommon
	obs.resolvedResources = ResolveProfileResources(spec)
	if HasProfileAcceleratorRequirement(spec) {
		obs.matchResult = MatchProfileNodes(fetch.nodes, spec, obs.resolvedResources)
	}
	obs.deployable = IsProfileDeployable(spec)
	// Cluster profiles have no namespace by definition; SourceModelFromOwnerRefs
	// only writes Namespace for AIMModel owners (which a cluster profile cannot
	// legitimately have), so the empty string is correct.
	obs.sourceModel = SourceModelFromOwnerRefs(fetch.profile, "")
	obs.origin = DeriveProfileOrigin(fetch.profile)
	obs.baseImage = BaseImageFromProfile(fetch.profile, fetch.profile.Status.BaseImage)
	obs.projectable = runtimeProjectable(spec, obs.deployable, obs.matchResult)
	if runtimeProjectionNeedsContract(r.ProjectionMode, spec, obs.projectable, obs.modelSlugWinner) {
		obs.yamlContract, obs.projectionErr = profileyaml.ForProfile(fetch.profile.Annotations, obs.origin, &spec)
		if obs.projectionErr == nil {
			obs.projectionErr = validateClusterRuntimeProjection(r.ProjectionMode, fetch.profile, obs)
		}
	} else if shouldMaintainClusterRuntime(r.ProjectionMode, fetch.profile, obs.projectable) ||
		shouldMaintainClusterModelSlugRuntime(
			r.ProjectionMode, fetch.profile, obs.projectable, obs.modelSlugWinner,
		) {
		// A bare ClusterServingRuntime does not consume profile YAML, so a
		// retained projection can be rebuilt directly after its gate flips.
		obs.projectionErr = validateClusterRuntimeProjection(r.ProjectionMode, fetch.profile, obs)
	}
	return obs
}

func runtimeProjectionNeedsContract(
	mode aimv1alpha2.RuntimeProjectionMode,
	spec aimv1alpha2.AIMProfileSpecCommon,
	projectable, modelSlugWinner bool,
) bool {
	return projectable &&
		(mode.ProjectsPerProfile() ||
			(mode.ProjectsModelSlug() && modelSlugProjectable(spec, projectable, modelSlugWinner)))
}

// ============================================================================
// PLAN — Profiles don't create child resources
// ============================================================================

// PlanResources for namespace-scoped AIMProfile materialises a Profile-owned
// AIMProfileCache when the profile opts into caching via spec.caching.enabled.
// The cache is named after the profile, owner-ref'd by the profile, and lives
// in the same namespace, so it is garbage-collected when the profile is
// deleted. This mirrors v1alpha1's AIMServiceTemplate -> AIMTemplateCache
// path (see internal/v1alpha1/aimservicetemplate/cache.go::BuildTemplateCache).
//
// AIMService-driven caches (set via service.Spec.Caching.Mode) coexist with
// Profile-owned caches: the two paths produce caches under different names
// and the underlying AIMArtifacts dedupe by SourceURI hash regardless. Users
// pick the lifecycle that matches their intent — Profile-owned for
// "cache lives with the model definition", AIMService-owned for "cache is
// per-service workload".
func (r *ProfileReconciler) PlanResources(
	ctx context.Context,
	reconcileCtx controllerutils.ReconcileContext[*aimv1alpha2.AIMProfile],
	obs ProfileObservation,
) controllerutils.PlanResult {
	plan := controllerutils.PlanResult{}
	profile := reconcileCtx.Object
	if profile == nil {
		return plan
	}

	if profile.Spec.Caching != nil && profile.Spec.Caching.Enabled && len(profile.Spec.ModelSources) > 0 {
		plan.Apply(buildProfileOwnedCache(profile))
	}

	if obs.projectionDeferred {
		plan.RequestRequeueAfter(time.Second)
		return plan
	}

	if obs.projectionErr == nil {
		if shouldMaintainNamespaceRuntime(r.ProjectionMode, profile, obs.projectable) {
			planNamespaceRuntime(ctx, &plan, r.ProjectionMode, profile, obs)
		}
		if shouldMaintainNamespaceModelSlugRuntime(
			r.ProjectionMode, profile, obs.projectable, obs.modelSlugWinner,
		) {
			planNamespaceModelSlugRuntime(ctx, &plan, r.ProjectionMode, profile, obs)
		}
	}

	return plan
}

// PlanResources for cluster-scoped AIMClusterProfile is intentionally a no-op
// for caching: a cluster-scoped profile does not own a target namespace, so
// it cannot directly create a namespaced AIMProfileCache. Consumers in any
// namespace (e.g. an AIMService that targets the cluster profile) drive cache
// creation via the service-side path; that cache then references the
// cluster-scoped profile via spec.profileScope=Cluster.
func (r *ClusterProfileReconciler) PlanResources(
	ctx context.Context,
	reconcileCtx controllerutils.ReconcileContext[*aimv1alpha2.AIMClusterProfile],
	obs ClusterProfileObservation,
) controllerutils.PlanResult {
	plan := controllerutils.PlanResult{}
	profile := reconcileCtx.Object
	if profile == nil {
		return plan
	}
	if obs.projectionErr == nil {
		if shouldMaintainClusterRuntime(r.ProjectionMode, profile, obs.projectable) {
			planClusterRuntime(ctx, &plan, r.ProjectionMode, profile, obs)
		}
		if shouldMaintainClusterModelSlugRuntime(
			r.ProjectionMode, profile, obs.projectable, obs.modelSlugWinner,
		) {
			planClusterModelSlugRuntime(ctx, &plan, r.ProjectionMode, profile, obs)
		}
	}
	return plan
}

// ============================================================================
// STATUS DECORATION
// ============================================================================

func (r *ProfileReconciler) DecorateStatus(
	status *aimv1alpha2.AIMProfileStatus,
	cm *controllerutils.ConditionManager,
	obs ProfileObservation,
) {
	decorateProfileStatus(
		status, cm,
		obs.profile.Spec.AIMProfileSpecCommon,
		obs.resolvedResources, obs.nodeErr, obs.matchResult,
		obs.deployable, obs.sourceModel, obs.origin, obs.baseImage,
	)
	clearRecoveredRuntimeProjectionElectionCondition(cm, obs.modelSlugElectionErr)
	if obs.projectionDeferred {
		if !obs.projectionWaiting {
			cm.Delete(runtimeProjectionReadyCondition)
		}
		return
	}
	decorateProjectionCondition(cm, r.ProjectionMode, obs.modelSlugWinner, obs.projectable, obs.nodeErr, obs.projectionErr)
	if obs.projectionErr == nil {
		recordProjectedRuntimeNames(
			status,
			r.ProjectionMode,
			obs.profile.Spec.AIMProfileSpecCommon,
			obs.profile.Name,
			obs.projectable,
			obs.modelSlugWinner,
		)
	}
}

func hasRecordedNamespaceProjection(profile *aimv1alpha2.AIMProfile) bool {
	return profile != nil &&
		(profile.Status.ProjectedRuntimeName != "" ||
			profile.Status.ProjectedModelSlugRuntimeName != "")
}

func (r *ClusterProfileReconciler) DecorateStatus(
	status *aimv1alpha2.AIMProfileStatus,
	cm *controllerutils.ConditionManager,
	obs ClusterProfileObservation,
) {
	decorateProfileStatus(
		status, cm,
		obs.profile.Spec.AIMProfileSpecCommon,
		obs.resolvedResources, obs.nodeErr, obs.matchResult,
		obs.deployable, obs.sourceModel, obs.origin, obs.baseImage,
	)
	clearRecoveredRuntimeProjectionElectionCondition(cm, obs.modelSlugElectionErr)
	decorateProjectionCondition(cm, r.ProjectionMode, obs.modelSlugWinner, obs.projectable, obs.nodeErr, obs.projectionErr)
	if obs.projectionErr == nil {
		recordProjectedRuntimeNames(
			status,
			r.ProjectionMode,
			obs.profile.Spec.AIMProfileSpecCommon,
			obs.profile.Name,
			obs.projectable,
			obs.modelSlugWinner,
		)
	}
}

func decorateProfileStatus(
	status *aimv1alpha2.AIMProfileStatus,
	cm *controllerutils.ConditionManager,
	spec aimv1alpha2.AIMProfileSpecCommon,
	resolvedResources *corev1.ResourceRequirements,
	nodeErr error,
	matchResult NodeMatchResult,
	deployable bool,
	sourceModel *aimv1alpha2.ProfileSourceModel,
	origin aimv1alpha1.ProfileOrigin,
	baseImage string,
) {
	status.Version = ExtractVersionFromImage(spec.Image)
	status.HardwareSummary = FormatProfileHardwareSummary(spec)
	status.Resources = resolvedResources
	status.ResolvedNodeAffinity = matchResult.NodeAffinity
	status.MatchingNodes = matchResult.MatchingNodes
	status.Deployable = deployable
	status.SourceModel = sourceModel
	status.Origin = origin
	status.BaseImage = baseImage

	if deployable {
		cm.MarkTrue(
			aimv1alpha2.AIMProfileConditionDeployable,
			aimv1alpha2.AIMProfileReasonDeployable,
			"Profile has aimId and modelSources populated",
		)
	} else {
		cm.MarkFalse(
			aimv1alpha2.AIMProfileConditionDeployable,
			aimv1alpha2.AIMProfileReasonBaseProfile,
			"Profile is a base profile awaiting derivation (missing aimId or modelSources)",
		)
	}

	if !HasProfileAcceleratorRequirement(spec) {
		cm.MarkTrue(
			aimv1alpha2.AIMProfileConditionHardwareAvailable,
			aimv1alpha2.AIMProfileReasonNoAccelerator,
			"No accelerator requirements specified",
		)
		return
	}

	// When node listing failed, skip the HardwareAvailable condition — the pipeline's
	// NodesReady/Degraded condition (from buildComponentHealth) is the accurate signal.
	// Setting HardwareNotAvailable here would be misleading since we don't know
	// whether matching hardware exists.
	if nodeErr != nil {
		return
	}

	if matchResult.MatchingNodes > 0 {
		cm.MarkTrue(
			aimv1alpha2.AIMProfileConditionHardwareAvailable,
			aimv1alpha2.AIMProfileReasonHardwareAvailable,
			"Matching nodes found in cluster",
		)
	} else {
		cm.MarkFalse(
			aimv1alpha2.AIMProfileConditionHardwareAvailable,
			aimv1alpha2.AIMProfileReasonHardwareNotAvailable,
			"No cluster nodes match accelerator labels and resource requests",
		)
	}
}

// ============================================================================
// HELPERS
// ============================================================================

// TODO: For large clusters, consider filtering nodes by accelerator labels using a label
// selector built from spec.acceleratorModel. This would reduce the working set
// from all nodes to only relevant ones.
func listNodes(ctx context.Context, c client.Client) ([]corev1.Node, error) {
	var nodeList corev1.NodeList
	if err := c.List(ctx, &nodeList); err != nil {
		log.FromContext(ctx).Error(err, "failed to list nodes")
		return nil, err
	}
	return nodeList.Items, nil
}

func buildComponentHealth(
	spec aimv1alpha2.AIMProfileSpecCommon,
	nodeErr error,
	matchResult NodeMatchResult,
) []controllerutils.ComponentHealth {
	if !HasProfileAcceleratorRequirement(spec) {
		return nil
	}

	if nodeErr != nil {
		return []controllerutils.ComponentHealth{
			{
				Component: "Nodes",
				State:     constants.AIMStatusDegraded,
				Reason:    "NodeListFailed",
				Message:   "Failed to list cluster nodes",
			},
		}
	}

	if matchResult.MatchingNodes > 0 {
		return []controllerutils.ComponentHealth{
			{
				Component: "Hardware",
				State:     constants.AIMStatusReady,
				Reason:    aimv1alpha2.AIMProfileReasonHardwareAvailable,
			},
		}
	}

	return []controllerutils.ComponentHealth{
		{
			Component: "Hardware",
			State:     constants.AIMStatusNotAvailable,
			Reason:    aimv1alpha2.AIMProfileReasonHardwareNotAvailable,
			Message:   "No cluster nodes match accelerator labels and resource requests",
		},
	}
}

func appendRuntimeProjectionElectionHealth(
	health []controllerutils.ComponentHealth,
	err error,
) []controllerutils.ComponentHealth {
	if err == nil {
		return health
	}
	return append(health, controllerutils.ComponentHealth{
		Component:      runtimeProjectionElectionComponent,
		Errors:         []error{err},
		DependencyType: controllerutils.DependencyTypeUpstream,
	})
}

func clearRecoveredRuntimeProjectionElectionCondition(
	cm *controllerutils.ConditionManager,
	err error,
) {
	if err == nil {
		cm.Delete(runtimeProjectionElectionReadyCondition)
	}
}

func appendProjectionComponentHealth(
	health []controllerutils.ComponentHealth,
	projectionErr error,
) []controllerutils.ComponentHealth {
	if projectionErr == nil {
		return health
	}
	var categorized controllerutils.StateEngineError
	if errors.As(projectionErr, &categorized) {
		return append(health, controllerutils.ComponentHealth{
			Component: runtimeProjectionComponent,
			Errors:    []error{projectionErr},
		})
	}
	return append(health, controllerutils.ComponentHealth{
		Component: runtimeProjectionComponent,
		State:     constants.AIMStatusFailed,
		Reason:    aimv1alpha2.AIMProfileReasonRuntimeProjectionFailed,
		Message:   fmt.Sprintf("Failed to build projected runtime: %v", projectionErr),
		Errors: []error{
			controllerutils.NewInvalidSpecError(
				aimv1alpha2.AIMProfileReasonRuntimeProjectionFailed,
				"Failed to build projected runtime",
				projectionErr,
			),
		},
	})
}
