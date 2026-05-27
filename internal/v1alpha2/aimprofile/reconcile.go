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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

// ============================================================================
// RECONCILERS
// ============================================================================

// ProfileReconciler implements the DomainReconciler interface for namespace-scoped profiles.
type ProfileReconciler struct {
	Client client.Client
	Scheme *runtime.Scheme
}

// ClusterProfileReconciler implements the DomainReconciler interface for cluster-scoped profiles.
type ClusterProfileReconciler struct {
	Client client.Client
	Scheme *runtime.Scheme
}

// ============================================================================
// FETCH RESULT
// ============================================================================

// ProfileFetchResult holds fetched resources for namespace-scoped profiles.
type ProfileFetchResult struct {
	profile *aimv1alpha2.AIMProfile
	nodes   []corev1.Node
	nodeErr error
}

// ClusterProfileFetchResult holds fetched resources for cluster-scoped profiles.
type ClusterProfileFetchResult struct {
	profile *aimv1alpha2.AIMClusterProfile
	nodes   []corev1.Node
	nodeErr error
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

	if HasAcceleratorRequirement(profile.Spec.AcceleratorModel, profile.Spec.AcceleratorCount, profile.Spec.Resources) {
		nodes, err := listNodes(ctx, c)
		result.nodes = nodes
		result.nodeErr = err
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

	if HasAcceleratorRequirement(profile.Spec.AcceleratorModel, profile.Spec.AcceleratorCount, profile.Spec.Resources) {
		nodes, err := listNodes(ctx, c)
		result.nodes = nodes
		result.nodeErr = err
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
}

// GetComponentHealth returns health of all components for automatic status management.
func (obs ProfileObservation) GetComponentHealth(_ context.Context, _ kubernetes.Interface) []controllerutils.ComponentHealth {
	spec := obs.profile.Spec.AIMProfileSpecCommon
	return buildComponentHealth(
		spec.AcceleratorModel, spec.AcceleratorCount,
		obs.resolvedResources,
		obs.nodeErr,
		obs.matchResult,
	)
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
}

// GetComponentHealth returns health of all components for automatic status management.
func (obs ClusterProfileObservation) GetComponentHealth(_ context.Context, _ kubernetes.Interface) []controllerutils.ComponentHealth {
	spec := obs.profile.Spec.AIMProfileSpecCommon
	return buildComponentHealth(
		spec.AcceleratorModel, spec.AcceleratorCount,
		obs.resolvedResources,
		obs.nodeErr,
		obs.matchResult,
	)
}

func (r *ProfileReconciler) ComposeState(
	_ context.Context,
	_ controllerutils.ReconcileContext[*aimv1alpha2.AIMProfile],
	fetch ProfileFetchResult,
) ProfileObservation {
	obs := ProfileObservation{ProfileFetchResult: fetch}
	spec := fetch.profile.Spec.AIMProfileSpecCommon
	obs.resolvedResources = ResolveResources(spec.AcceleratorType, spec.AcceleratorCount, spec.Resources)
	if HasAcceleratorRequirement(spec.AcceleratorModel, spec.AcceleratorCount, spec.Resources) {
		obs.matchResult = MatchNodes(fetch.nodes, spec.AcceleratorModel, obs.resolvedResources)
	}
	obs.deployable = IsProfileDeployable(spec)
	obs.sourceModel = SourceModelFromOwnerRefs(fetch.profile, fetch.profile.Namespace)
	obs.origin = DeriveProfileOrigin(fetch.profile)
	obs.baseImage = BaseImageFromProfile(fetch.profile, fetch.profile.Status.BaseImage)
	return obs
}

func (r *ClusterProfileReconciler) ComposeState(
	_ context.Context,
	_ controllerutils.ReconcileContext[*aimv1alpha2.AIMClusterProfile],
	fetch ClusterProfileFetchResult,
) ClusterProfileObservation {
	obs := ClusterProfileObservation{ClusterProfileFetchResult: fetch}
	spec := fetch.profile.Spec.AIMProfileSpecCommon
	obs.resolvedResources = ResolveResources(spec.AcceleratorType, spec.AcceleratorCount, spec.Resources)
	if HasAcceleratorRequirement(spec.AcceleratorModel, spec.AcceleratorCount, spec.Resources) {
		obs.matchResult = MatchNodes(fetch.nodes, spec.AcceleratorModel, obs.resolvedResources)
	}
	obs.deployable = IsProfileDeployable(spec)
	// Cluster profiles have no namespace by definition; SourceModelFromOwnerRefs
	// only writes Namespace for AIMModel owners (which a cluster profile cannot
	// legitimately have), so the empty string is correct.
	obs.sourceModel = SourceModelFromOwnerRefs(fetch.profile, "")
	obs.origin = DeriveProfileOrigin(fetch.profile)
	obs.baseImage = BaseImageFromProfile(fetch.profile, fetch.profile.Status.BaseImage)
	return obs
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
	_ context.Context,
	reconcileCtx controllerutils.ReconcileContext[*aimv1alpha2.AIMProfile],
	_ ProfileObservation,
) controllerutils.PlanResult {
	plan := controllerutils.PlanResult{}
	profile := reconcileCtx.Object
	if profile == nil {
		return plan
	}
	if profile.Spec.Caching == nil || !profile.Spec.Caching.Enabled {
		return plan
	}
	if len(profile.Spec.ModelSources) == 0 {
		return plan
	}
	plan.Apply(buildProfileOwnedCache(profile))
	return plan
}

// PlanResources for cluster-scoped AIMClusterProfile is intentionally a no-op
// for caching: a cluster-scoped profile does not own a target namespace, so
// it cannot directly create a namespaced AIMProfileCache. Consumers in any
// namespace (e.g. an AIMService that targets the cluster profile) drive cache
// creation via the service-side path; that cache then references the
// cluster-scoped profile via spec.profileScope=Cluster.
func (r *ClusterProfileReconciler) PlanResources(
	_ context.Context,
	_ controllerutils.ReconcileContext[*aimv1alpha2.AIMClusterProfile],
	_ ClusterProfileObservation,
) controllerutils.PlanResult {
	return controllerutils.PlanResult{}
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
	status.HardwareSummary = FormatHardwareSummary(spec.AcceleratorModel, spec.AcceleratorCount)
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

	if !HasAcceleratorRequirement(spec.AcceleratorModel, spec.AcceleratorCount, spec.Resources) {
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
	accelModel string,
	accelCount int32,
	resolvedResources *corev1.ResourceRequirements,
	nodeErr error,
	matchResult NodeMatchResult,
) []controllerutils.ComponentHealth {
	if !HasAcceleratorRequirement(accelModel, accelCount, resolvedResources) {
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
