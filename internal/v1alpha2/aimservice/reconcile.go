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
	"context"
	"fmt"
	"time"

	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/aimadapter"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	aimruntimeconfig "github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/aimruntimeconfig"
	v1alpha1service "github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/aimservice"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimprofile"
)

// ProfileServiceReconciler implements the domain logic for profile-based
// AIMService reconciliation. It uses the v1alpha1 Go types because v1alpha1
// is the storage version; the profile-specific spec fields live on
// aimv1alpha1.AIMService while the referenced profile is defined in v1alpha2.
type ProfileServiceReconciler struct {
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// GetApplyOptions returns apply options derived from the merged runtime
// config so label propagation rules are honoured on child resources
// (InferenceService, HTTPRoute, ConfigMap, AIMProfileCache).
func (r *ProfileServiceReconciler) GetApplyOptions(obs ServiceObservation) controllerutils.ApplyOptions {
	return aimruntimeconfig.GetApplyOptions(obs.mergedRuntimeConfig.Value)
}

// ServiceFetchResult holds all resources fetched for a profile-based
// AIMService reconcile.
type ServiceFetchResult struct {
	service *aimv1alpha1.AIMService

	profile        controllerutils.FetchResult[*aimv1alpha2.AIMProfile]
	clusterProfile controllerutils.FetchResult[*aimv1alpha2.AIMClusterProfile]
	overlayProfile controllerutils.FetchResult[*aimv1alpha2.AIMProfile]

	// resolution carries the outcome of the multi-mode resolver: which
	// shape was used, the candidates considered (for ambiguity reporting),
	// and any non-fatal note (e.g. "selector matched 3 profiles; picked
	// alphabetical winner") to surface through getProfileHealth.
	resolution profileResolution

	profileCache controllerutils.FetchResult[*aimv1alpha2.AIMProfileCache]

	inferenceService     controllerutils.FetchResult[*servingv1beta1.InferenceService]
	inferenceServicePods *controllerutils.FetchResult[*corev1.PodList]
	hpa                  controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler]
	httpRoute            controllerutils.FetchResult[*gatewayapiv1.HTTPRoute]
	gateway              controllerutils.FetchResult[*gatewayapiv1.Gateway]

	mergedRuntimeConfig controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]

	// adapterDeps holds the per-adapter artifacts, staging Jobs, and resolved
	// base-model artifact for spec.adapters. Populated only when the service
	// declares adapters. See internal/aimadapter.
	adapterDeps aimadapter.Dependencies
}

// ServiceObservation holds state derived from the fetch result plus any
// artefacts computed once in ComposeState for reuse during planning and
// status decoration.
type ServiceObservation struct {
	ServiceFetchResult

	resolvedProfileSpec   *aimv1alpha2.AIMProfileSpecCommon
	resolvedProfileStatus *aimv1alpha2.AIMProfileStatus
	profileName           string
	profileScope          aimv1alpha1.AIMResolutionScope

	// desiredOverlayProfile, when non-nil, is a service-owned AIMProfile
	// materialised from the resolved seed profile with
	// service.Spec.ProfileOverrides applied. It is the AIMProfile every
	// downstream resource (cache, ConfigMap, InferenceService) is keyed
	// against — profileName/profileScope/resolvedProfileSpec all already
	// point at it once the overlay path is taken in ComposeState.
	desiredOverlayProfile *aimv1alpha2.AIMProfile

	hasModelSources   bool
	profileCacheReady bool

	// Pre-computed names and rendered profile artefacts. These are produced
	// once in ComposeState so the ConfigMap, the InferenceService volume
	// mount, and the AIM_PROFILE_ID env var cannot drift. On failure these
	// stay empty and configErr is set, which getConfigHealth surfaces as an
	// InvalidSpec condition on the AIMService.
	isvcName         string
	configMapName    string
	profileYAML      []byte
	profileYAMLName  string
	profileAssembled bool

	// configErr records failures encountered while deriving profile-dependent
	// artefacts (e.g. ISVC name generation, profile YAML assembly). It is
	// surfaced through the component health pipeline so the user sees a
	// ConfigValid=False condition instead of a silently stalled reconcile.
	configErr error

	// runtimeStatus captures replica counts (from HPA or spec defaults) that
	// feed the AIMService Replicas printcolumn.
	runtimeStatus *aimv1alpha1.AIMServiceRuntimeStatus

	// adapterState is the computed adapter staging state (spec.adapters),
	// produced in ComposeState by the shared internal/aimadapter engine.
	adapterState aimadapter.State
}

// GetComponentHealth returns health entries for each component the service
// depends on or owns.
func (obs ServiceObservation) GetComponentHealth(ctx context.Context, clientset kubernetes.Interface) []controllerutils.ComponentHealth {
	var health []controllerutils.ComponentHealth

	if cfg := obs.getConfigHealth(); cfg.Component != "" {
		health = append(health, cfg)
	}

	// Scale-from-zero requires routing; surface the invalid combination as
	// ConfigValid=False. Shared with the v1alpha1 pipeline so the validation is
	// identical regardless of which pipeline owns the service.
	if cfg := v1alpha1service.ScaleToZeroRoutingComponentHealth(obs.service, obs.mergedRuntimeConfig.Value); cfg.Component != "" {
		health = append(health, cfg)
	}

	// Routing on a multi-listener gateway requires a hostname pin; otherwise
	// the route attaches to every listener and can bypass authentication.
	// Surface as ConfigValid=False even before a profile resolves (the route
	// is also not created). Shared with the v1alpha1 pipeline.
	if cfg := v1alpha1service.RoutingHostnameComponentHealth(obs.service, obs.mergedRuntimeConfig.Value, obs.gateway); cfg.Component != "" {
		health = append(health, cfg)
	}

	// Autoscaling configured but no trigger resolves -> ConfigValid=False.
	// Shared with the v1alpha1 pipeline so the invariant (external autoscaler
	// class implies a ScaledObject) holds regardless of which pipeline owns the
	// service.
	if cfg := v1alpha1service.AutoscalingTriggerComponentHealth(obs.service); cfg.Component != "" {
		health = append(health, cfg)
	}

	health = append(health, obs.getProfileHealth())

	if obs.mergedRuntimeConfig.Value != nil || obs.mergedRuntimeConfig.Error != nil {
		health = append(health, obs.mergedRuntimeConfig.ToUpstreamComponentHealth(
			"RuntimeConfig",
			func(cfg *aimv1alpha1.AIMRuntimeConfigCommon) controllerutils.ComponentHealth {
				return controllerutils.ComponentHealth{
					State:  constants.AIMStatusReady,
					Reason: "RuntimeConfigResolved",
				}
			},
		))
	}

	// When the profile is in a terminal non-ready state (unresolved or
	// base profile), the planner intentionally skips creating the ProfileCache,
	// InferenceService, and HTTPRoute (see PlanResources). Reporting those
	// downstream components as "Creating"/"not found" in that case is
	// misleading — the controller is not creating anything. Surface only
	// the gating Profile condition so the user sees a single, accurate
	// reason (ProfileNotFound or BaseProfile) on both ProfileReady and
	// the aggregate Ready condition. Profile health is still
	// progressing-aware: a resolved-but-not-yet-ready profile keeps
	// downstream conditions visible so users see cache / ISVC progress.
	if obs.resolvedProfileSpec == nil || !obs.isDeployable() {
		return health
	}

	if obs.hasModelSources {
		health = append(health, obs.getProfileCacheHealth())
	}
	// Only report InferenceService health once the fetch has actually
	// produced a result (value or error). Skipping on a zero fetch avoids
	// spurious "not found" conditions on the first reconcile pass when the
	// profile has not yet been resolved.
	if obs.inferenceService.Value != nil || obs.inferenceService.Error != nil {
		health = append(health, obs.getInferenceServiceHealth())
	}

	// Predictor pod health (downstream). Shared with the v1alpha1 pipeline so a
	// healthily-idled scale-to-zero service reports Ready (ScaledToZero) instead
	// of stalling at "no pods" forever.
	if podsHealth, ok := v1alpha1service.InferenceServicePodsComponentHealth(
		ctx, clientset, obs.service, obs.hpa, obs.inferenceServicePods,
	); ok {
		health = append(health, podsHealth)
	}

	// HPA health (autoscaling configured). Shared with v1alpha1 so the
	// activation metric never gates readiness under scale-to-zero.
	if hpaHealth := v1alpha1service.HPAComponentHealth(
		obs.service,
		obs.hpa,
		podItemCount(obs.inferenceServicePods),
		v1alpha1service.InferenceServiceReady(obs.inferenceService),
	); hpaHealth.Component != "" {
		health = append(health, hpaHealth)
	}

	if route := v1alpha1service.HTTPRouteComponentHealth(obs.service, obs.mergedRuntimeConfig.Value, obs.httpRoute); route.Component != "" {
		health = append(health, route)
	}

	if aimadapter.IsActive(obs.service) {
		health = append(health, aimadapter.Health(obs.adapterState, len(obs.service.Spec.Adapters)))
	}

	return health
}

// podItemCount returns the number of pods in a (possibly nil) pod-list fetch
// result, treating a missing or failed fetch as zero. Mirrors the v1alpha1
// helper so the shared HPA verdict sees an identical pod count.
func podItemCount(pods *controllerutils.FetchResult[*corev1.PodList]) int {
	if pods == nil || !pods.OK() || pods.Value == nil {
		return 0
	}
	return len(pods.Value.Items)
}

// getConfigHealth reports configuration errors produced during ComposeState
// (name generation, profile YAML assembly). Returning a zero value signals
// "no config component to report" so the framework does not emit a stray
// condition when there is nothing wrong.
func (obs ServiceObservation) getConfigHealth() controllerutils.ComponentHealth {
	if obs.configErr == nil {
		return controllerutils.ComponentHealth{}
	}
	return controllerutils.ComponentHealth{
		Component:      "ProfileConfig",
		State:          constants.AIMStatusFailed,
		DependencyType: controllerutils.DependencyTypeUpstream,
		Errors: []error{
			controllerutils.NewInvalidSpecError(
				"ProfileConfigInvalid",
				obs.configErr.Error(),
				obs.configErr,
			),
		},
	}
}

func (obs ServiceObservation) getProfileCacheHealth() controllerutils.ComponentHealth {
	health := controllerutils.ComponentHealth{
		Component:      "ProfileCache",
		DependencyType: controllerutils.DependencyTypeDownstream,
	}

	if !obs.profileCache.OK() {
		if obs.profileCache.IsNotFound() {
			health.State = constants.AIMStatusProgressing
			health.Reason = "ProfileCacheNotFound"
			health.Message = "Profile cache not yet created"
			return health
		}
		health.State = constants.AIMStatusFailed
		health.Reason = "FetchError"
		health.Message = obs.profileCache.Error.Error()
		health.Errors = []error{obs.profileCache.Error}
		return health
	}

	pc := obs.profileCache.Value
	if pc.Status.Status == constants.AIMStatusReady {
		health.State = constants.AIMStatusReady
		health.Reason = "ProfileCacheReady"
		health.Message = "Profile cache is ready"
		return health
	}

	health.State = constants.AIMStatusProgressing
	health.Reason = "ProfileCacheNotReady"
	health.Message = fmt.Sprintf("Profile cache %s is %s", pc.Name, pc.Status.Status)
	return health
}

func (obs ServiceObservation) getProfileHealth() controllerutils.ComponentHealth {
	health := controllerutils.ComponentHealth{
		Component:      "Profile",
		DependencyType: controllerutils.DependencyTypeUpstream,
	}

	// Resolver list failures (transient infra/RBAC issues) populate
	// Error on the relevant FetchResult; surface them as infrastructure
	// errors so the framework lights up DependenciesReachable=False
	// rather than the terminal ProfileNotFound user-config reason.
	if obs.profile.Error != nil && !obs.profile.IsNotFound() {
		health.State = constants.AIMStatusFailed
		health.Errors = []error{obs.profile.Error}
		return health
	}
	if obs.clusterProfile.Error != nil && !obs.clusterProfile.IsNotFound() {
		health.State = constants.AIMStatusFailed
		health.Errors = []error{obs.clusterProfile.Error}
		return health
	}

	if obs.resolvedProfileSpec != nil {
		// Base-profile rejection: a profile that has not yet been derived
		// (no aimId or no modelSources) cannot back an AIMService. Selector
		// path label-filtering already excludes role=base profiles, but
		// the resolved profile's own `status.deployable` is the source of
		// truth — defensively check it for the by-name path where a user
		// can target a base profile directly. We accept a spec-based
		// fallback for the (rare) transient state where the AIMProfile
		// reconciler hasn't yet stamped `status.deployable=true` on a
		// structurally-deployable profile, so we don't briefly degrade
		// healthy services.
		if !obs.isDeployable() {
			health.State = constants.AIMStatusFailed
			health.Reason = aimv1alpha1.AIMServiceReasonBaseProfile
			health.Message = fmt.Sprintf(
				"Profile %s is not deployable (base profile); set spec.aimId and spec.modelSources or use a different profile",
				obs.profileName,
			)
			return health
		}
		if obs.resolvedProfileStatus != nil && obs.resolvedProfileStatus.Status == constants.AIMStatusReady {
			health.State = constants.AIMStatusReady
			health.Reason = aimv1alpha1.AIMServiceReasonProfileResolved
			health.Message = fmt.Sprintf("Profile %s is ready", obs.profileName)
			return health
		}
		health.State = constants.AIMStatusProgressing
		health.Reason = aimv1alpha1.AIMServiceReasonProfileNotReady
		health.Message = fmt.Sprintf("Profile %s is not ready yet", obs.profileName)
		return health
	}

	// ProfileNotFound is terminal until the user changes the spec or
	// creates a matching profile, so emit a Failed state (not Pending).
	// This makes the framework's Ready aggregator surface
	// reason=ProfileNotFound on the aggregate Ready condition, matching
	// the BaseProfile path. A Pending state would otherwise be
	// rolled up as the generic "Progressing" reason because the
	// framework's firstErrorComponent picker only considers Failed/
	// Degraded/NotAvailable states (see processComponentStatus in
	// internal/controller/utils/reconciler.go).
	health.State = constants.AIMStatusFailed
	health.Reason = aimv1alpha1.AIMServiceReasonProfileNotFound
	if msg := obs.resolution.notFoundMessage; msg != "" {
		health.Message = msg
	} else {
		health.Message = "No profile found for service"
	}
	return health
}

func (obs ServiceObservation) getInferenceServiceHealth() controllerutils.ComponentHealth {
	health := controllerutils.ComponentHealth{
		Component:      "InferenceService",
		DependencyType: controllerutils.DependencyTypeDownstream,
	}

	if !obs.inferenceService.OK() {
		if obs.inferenceService.IsNotFound() {
			health.State = constants.AIMStatusProgressing
			health.Reason = aimv1alpha1.AIMServiceReasonCreatingRuntime
			health.Message = "InferenceService not found"
			return health
		}
		health.State = constants.AIMStatusFailed
		health.Reason = "FetchError"
		health.Message = obs.inferenceService.Error.Error()
		health.Errors = []error{obs.inferenceService.Error}
		return health
	}

	isvc := obs.inferenceService.Value
	for _, cond := range isvc.Status.Conditions {
		if cond.Type == "Ready" && cond.Status == "True" {
			health.State = constants.AIMStatusReady
			health.Reason = aimv1alpha1.AIMServiceReasonRuntimeReady
			health.Message = "InferenceService is ready"
			return health
		}
	}

	health.State = constants.AIMStatusProgressing
	health.Reason = aimv1alpha1.AIMServiceReasonCreatingRuntime
	health.Message = "InferenceService is not ready"
	return health
}

// FetchRemoteState fetches profile, profile cache, runtime config, and existing
// downstream resources for a profile-based AIMService.
func (r *ProfileServiceReconciler) FetchRemoteState(
	ctx context.Context,
	c client.Client,
	reconcileCtx controllerutils.ReconcileContext[*aimv1alpha1.AIMService],
) ServiceFetchResult {
	service := reconcileCtx.Object
	logger := log.FromContext(ctx).WithValues(
		"phase", "fetch",
		"pipeline", "profile",
		"service", service.Name,
		"namespace", service.Namespace,
	)

	result := ServiceFetchResult{service: service}

	result.inferenceService = fetchInferenceService(ctx, c, service)

	// Fetch HPA and predictor pods if the InferenceService exists. The HPA is
	// only present when KServe has created the predictor (KEDA names it
	// keda-hpa-{isvc}-predictor). Pods feed the shared scale-to-zero idle
	// verdict so a healthily-idled service reports Ready rather than stalling
	// at "no pods" forever.
	if result.inferenceService.OK() && result.inferenceService.Value != nil {
		result.hpa = v1alpha1service.FetchHPA(ctx, c, result.inferenceService.Value)
		pods := v1alpha1service.FetchPredictorPods(ctx, c, result.inferenceService.Value)
		result.inferenceServicePods = &pods
	}

	// Resolve the profile using whichever of the four supported shapes the
	// AIMService spec authored (name, model, model+selector, selector). The
	// resolver desugars spec.model.name into selector.modelRef.name, forces
	// role=Deployable, and emits a ProfileSelectorAmbiguous event when more
	// than one candidate tied under ranking.
	result.profile, result.clusterProfile, result.resolution = resolveProfileCandidates(ctx, c, r.Recorder, service)

	// If the resolver picked a candidate, fetch any service-owned overlay
	// derived from it (so ComposeState can switch downstream resolution
	// onto the overlay) and the AIMProfileCache feeding the runtime.
	seedObs := ServiceObservation{ServiceFetchResult: result}
	seedObs.resolveFetchedProfile()
	if seedObs.resolvedProfileSpec != nil {
		cacheProfileName := seedObs.profileName
		cacheProfileScope := seedObs.profileScope
		if hasProfileOverrides(service.Spec.ProfileOverrides) {
			overlay, _, err := buildServiceOverlayProfile(service, seedObs)
			if err == nil {
				cacheProfileName = overlay.Name
				// Overlays are always namespace-scoped AIMProfiles in
				// the service's own namespace, regardless of the
				// underlying seed's scope.
				cacheProfileScope = aimv1alpha1.AIMResolutionScopeNamespace
				result.overlayProfile = controllerutils.Fetch(ctx, c, client.ObjectKey{
					Namespace: service.Namespace,
					Name:      overlay.Name,
				}, &aimv1alpha2.AIMProfile{})
			}
		}
		result.profileCache = fetchProfileCache(ctx, c, service, cacheProfileName, cacheProfileScope)
	}

	// Fetch merged runtime config. Needed for routing (HTTPRoute) and label
	// propagation. FetchMergedRuntimeConfig falls back to the default
	// cluster-scoped runtime config when the ref is empty.
	runtimeConfigRef := service.GetRuntimeConfigRef()
	result.mergedRuntimeConfig = aimruntimeconfig.FetchMergedRuntimeConfig(ctx, c, runtimeConfigRef.Name, service.Namespace)

	// Fetch the HTTPRoute (only exists when routing is enabled on the
	// service or runtime config).
	result.httpRoute = v1alpha1service.FetchHTTPRoute(ctx, c, service, result.mergedRuntimeConfig.Value)

	// Fetch the parent Gateway so the host-pinning guard can see how many
	// listeners it exposes (a multi-listener gateway requires a hostname pin).
	result.gateway = v1alpha1service.FetchGateway(ctx, c, service, result.mergedRuntimeConfig.Value)

	// Adapter staging dependencies (spec.adapters). The parent model artifact is
	// resolved indirectly via the profile cache's resolved artifacts (keyed on
	// the profile's modelId), so it is only available once the cache exists. The
	// remaining staging mechanics are shared via internal/aimadapter.
	if aimadapter.IsActive(service) {
		parentName, parentErr := resolveAdapterParentName(&seedObs, &result)
		result.adapterDeps = aimadapter.Fetch(ctx, c, service, parentName)
		result.adapterDeps.ParentResolutionErr = parentErr
	}

	logger.V(1).Info("Fetch complete",
		"profileFound", result.profile.OK() || result.clusterProfile.OK(),
		"profileCacheFound", result.profileCache.OK(),
		"isvcFound", result.inferenceService.OK(),
	)

	return result
}

// ComposeState interprets the fetch result and pre-computes derived artefacts
// (ISVC name, ConfigMap name, rendered profile YAML, runtime status) that
// multiple plan steps and the status decorator consume. Computing these in
// one place guarantees a single source of truth per reconcile cycle.
func (r *ProfileServiceReconciler) ComposeState(
	ctx context.Context,
	_ controllerutils.ReconcileContext[*aimv1alpha1.AIMService],
	fetch ServiceFetchResult,
) ServiceObservation {
	obs := ServiceObservation{ServiceFetchResult: fetch}

	obs.resolveFetchedProfile()

	// If the AIMService declared spec.ProfileOverrides, build a service-owned
	// overlay AIMProfile from the resolved seed and switch every downstream
	// resolution (cache name, ConfigMap, KServe env wiring) onto the overlay.
	// Doing this before hasModelSources ensures the cache is sized against
	// the overlay's modelSources, not the seed's. Failures in override
	// application surface through configErr so the AIMService gets a clean
	// ConfigValid=False condition rather than silently stalling.
	if obs.resolvedProfileSpec != nil && hasProfileOverrides(fetch.service.Spec.ProfileOverrides) {
		overlay, overlaySpec, err := buildServiceOverlayProfile(fetch.service, obs)
		if err != nil {
			obs.configErr = fmt.Errorf("apply service profile overrides: %w", err)
		} else {
			obs.desiredOverlayProfile = overlay
			obs.profileName = overlay.Name
			obs.profileScope = aimv1alpha1.AIMResolutionScopeNamespace
			if fetch.overlayProfile.OK() && fetch.overlayProfile.Value != nil {
				obs.resolvedProfileSpec = &fetch.overlayProfile.Value.Spec.AIMProfileSpecCommon
				obs.resolvedProfileStatus = &fetch.overlayProfile.Value.Status
			} else {
				obs.resolvedProfileSpec = &overlaySpec
				obs.resolvedProfileStatus = nil
			}
		}
	}

	if obs.resolvedProfileSpec != nil {
		obs.hasModelSources = len(obs.resolvedProfileSpec.ModelSources) > 0
	}

	if obs.hasModelSources && fetch.profileCache.OK() && fetch.profileCache.Value != nil {
		obs.profileCacheReady = fetch.profileCache.Value.Status.Status == constants.AIMStatusReady
	}

	r.composeDerivedNames(ctx, &obs)

	// Runtime status is always computed so the Replicas printcolumn stays
	// current even when routing, profile, or cache components are degraded.
	obs.runtimeStatus = v1alpha1service.ComputeRuntimeStatus(fetch.service, fetch.hpa)

	// Validate and interpret declared adapters (spec.adapters), and keep computing
	// while removed adapters are still being reclaimed (status carries Deleting).
	if aimadapter.IsActive(fetch.service) {
		// Gate adapters on the resolved profile advertising the LoRA feature.
		if obs.resolvedProfileSpec != nil {
			supports := obs.resolvedProfileSpec.SupportsAdapters()
			fetch.adapterDeps.ProfileSupportsAdapters = &supports
		}
		obs.adapterState = aimadapter.Compose(fetch.service, fetch.adapterDeps)
	}

	return obs
}

// isDeployable reports whether the resolved profile is allowed to back an
// AIMService. Trusts `status.deployable` when the producer has stamped it
// and falls back to the structural definition (aimId + modelSources both
// populated) to ride out the brief window where a fresh AIMProfile has
// not yet been observed by its own reconciler. Returns false when no
// profile has been resolved so callers can branch on the same predicate.
func (obs *ServiceObservation) isDeployable() bool {
	if obs.resolvedProfileSpec == nil {
		return false
	}
	if obs.resolvedProfileStatus != nil && obs.resolvedProfileStatus.Deployable {
		return true
	}
	return aimprofile.IsProfileDeployable(*obs.resolvedProfileSpec)
}

func (obs *ServiceObservation) resolveFetchedProfile() {
	if obs.profile.OK() && obs.profile.Value != nil {
		obs.resolvedProfileSpec = &obs.profile.Value.Spec.AIMProfileSpecCommon
		obs.resolvedProfileStatus = &obs.profile.Value.Status
		obs.profileName = obs.profile.Value.Name
		obs.profileScope = aimv1alpha1.AIMResolutionScopeNamespace
	} else if obs.clusterProfile.OK() && obs.clusterProfile.Value != nil {
		obs.resolvedProfileSpec = &obs.clusterProfile.Value.Spec.AIMProfileSpecCommon
		obs.resolvedProfileStatus = &obs.clusterProfile.Value.Status
		obs.profileName = obs.clusterProfile.Value.Name
		obs.profileScope = aimv1alpha1.AIMResolutionScopeCluster
	}
}

// composeDerivedNames assembles the ISVC name, profile ConfigMap name, and the
// rendered profile YAML (bytes + filename) on the observation. Any failure is
// captured on configErr so the plan phase can skip cleanly while the
// framework surfaces the error through the component health pipeline.
func (r *ProfileServiceReconciler) composeDerivedNames(ctx context.Context, obs *ServiceObservation) {
	service := obs.service
	logger := log.FromContext(ctx).WithName("compose").WithValues("pipeline", "profile")

	isvcName, err := v1alpha1service.GenerateInferenceServiceName(service.Name, service.Namespace)
	if err != nil {
		logger.Error(err, "failed to generate InferenceService name", "service", service.Name)
		obs.configErr = fmt.Errorf("generate InferenceService name: %w", err)
		return
	}
	obs.isvcName = isvcName

	cmName, err := profileConfigMapName(service.Name)
	if err != nil {
		logger.Error(err, "failed to generate profile ConfigMap name", "service", service.Name)
		obs.configErr = fmt.Errorf("generate profile ConfigMap name: %w", err)
		return
	}
	obs.configMapName = cmName

	if obs.resolvedProfileSpec == nil {
		return
	}

	yamlBytes, filename, err := assembleProfileYAML(obs.resolvedProfileSpec)
	if err != nil {
		logger.Error(err, "failed to assemble profile YAML",
			"service", service.Name, "profile", obs.profileName)
		obs.configErr = fmt.Errorf("assemble profile YAML: %w", err)
		return
	}
	obs.profileYAML = yamlBytes
	obs.profileYAMLName = filename
	obs.profileAssembled = true
}

// PlanResources determines the resources to create or update for the profile
// pipeline.
func (r *ProfileServiceReconciler) PlanResources(
	ctx context.Context,
	_ controllerutils.ReconcileContext[*aimv1alpha1.AIMService],
	obs ServiceObservation,
) controllerutils.PlanResult {
	logger := log.FromContext(ctx).WithName("plan").WithValues("pipeline", "profile")
	service := obs.service

	planResult := controllerutils.PlanResult{}

	if obs.configErr != nil {
		logger.V(1).Info("Config error, skipping resource planning", "err", obs.configErr.Error())
		return planResult
	}

	// v1alpha2 quick-start: a service authored with spec.model.image and
	// dispatched onto the profile pipeline via the reconciler-pipeline
	// annotation has no AIMModel yet. Plant the seed (dedicated, owned
	// by this service) so the v1alpha2 model controller runs discovery
	// and the next reconcile resolves via the standard model-ref path.
	// Skip everything else this pass — there is nothing to apply until
	// the model materialises a Ready AIMProfile.
	if shouldPlanAutoCreatedModel(obs) {
		autoModel, err := buildAutoCreatedAIMModel(service, obs.resolution.autoModelImage)
		if err != nil {
			logger.Error(err, "failed to build auto-created AIMModel",
				"image", obs.resolution.autoModelImage)
			return planResult
		}
		logger.V(1).Info("planning dedicated auto-created AIMModel for image-shape service",
			"image", obs.resolution.autoModelImage, "name", autoModel.Name)
		planResult.Apply(autoModel)
		return planResult
	}

	if obs.resolvedProfileSpec == nil {
		logger.V(1).Info("No profile resolved, skipping resource planning")
		return planResult
	}

	// Reject base profiles up-front: status.deployable=false means the
	// profile is missing aimId or modelSources and cannot back a runtime.
	// getProfileHealth already surfaces the condition; the explicit gate
	// here keeps cache / overlay / ISVC from being applied against an
	// unfinished spec.
	if !obs.isDeployable() {
		logger.V(1).Info("Profile is a base profile (status.deployable=false); skipping resource planning",
			"profile", obs.profileName)
		return planResult
	}

	if obs.desiredOverlayProfile != nil {
		planResult.Apply(obs.desiredOverlayProfile)
	}

	if obs.resolvedProfileStatus == nil || obs.resolvedProfileStatus.Status != constants.AIMStatusReady {
		logger.V(1).Info("Profile not ready, skipping resource planning", "profile", obs.profileName)
		return planResult
	}

	// 1. Plan the service-owned overlay AIMProfile, if spec.profileOverrides
	// asked us to materialise one. Always service-owned (Apply, not
	// ApplyWithoutOwnerRef) so the overlay GCs with the AIMService — every
	// downstream resource (cache, ConfigMap, ISVC) is now keyed against the
	// overlay's name via obs.profileName, so leaving an orphan overlay
	// behind would leave a stale, cache-eligible profile in the namespace.
	// 2. Plan AIMProfileCache if the profile has model sources.
	//
	// Cache ownership tracks the service's caching mode, mirroring v1alpha1:
	//   - Shared: applied without an owner reference so the cache persists
	//     across service deletions and is shared by every service in the
	//     namespace that resolves to the same AIMProfile.
	//   - Dedicated: applied with the AIMService as owner so the cache (and
	//     transitively its Artifacts/PVC) is garbage-collected when the
	//     service is deleted.
	if obs.hasModelSources {
		if pc := planProfileCache(service, obs); pc != nil {
			if service.Spec.GetCachingMode() == aimv1alpha1.CachingModeShared {
				planResult.ApplyWithoutOwnerRef(pc)
			} else {
				planResult.Apply(pc)
			}
		}

		if !obs.profileCacheReady {
			logger.V(1).Info("Profile cache not ready, deferring ISVC creation", "profile", obs.profileName)
			return planResult
		}
	}

	// Adapter staging (v1alpha2 spec.adapters). Adapters load dynamically, so the
	// ISVC is never gated on downloads: we ensure the per-service subtree exists
	// and stage adapters asynchronously (the aim-runtime hot-loads each as it
	// lands). ISVC *creation* is gated only on the subtree being mountable
	// (config valid + adapter disk resolved + subtree dir present); a config error
	// keeps the ISVC from being created and surfaces via getAdaptersHealth. Once
	// the ISVC exists, adapter add/remove never blocks its updates.
	// Run the adapter engine while adapters are declared OR still being reclaimed
	// (a removal — including dropping the last adapter — drives its prune Job to
	// completion via status-carried Deleting entries).
	if aimadapter.IsActive(service) {
		aimadapter.Plan(&planResult, service, obs.adapterDeps, obs.adapterState, obs.mergedRuntimeConfig.Value)
	}
	// ISVC creation is gated whenever the service needs the adapter disk —
	// adapters declared, or dynamic mode (which mounts even at zero adapters) —
	// because the read-only subPath mount requires the subtree directory to exist
	// before the pod starts (the aim-runtime errors on a missing subPath). This
	// gate is on the subtree being mountable, never on downloads.
	isvcExists := obs.inferenceService.OK() && obs.inferenceService.Value != nil
	if service.Spec.AdaptersEnabled() {
		if !isvcExists && !obs.adapterState.Ready {
			logger.V(1).Info("Adapter subtree not mountable yet; deferring ISVC creation",
				"configErr", obs.adapterState.ConfigErr,
				"adapterDiskPVC", obs.adapterState.AdapterDiskPVC,
				"subtreeReady", obs.adapterState.SubtreeReady)
			return planResult
		}
	}

	// 2. Plan the profile ConfigMap (owned by AIMService) using the YAML
	// rendered once in ComposeState.
	if obs.profileAssembled {
		planResult.Apply(buildProfileConfigMap(service, obs.configMapName, obs.profileYAMLName, obs.profileYAML))
	}

	// 3. Plan the InferenceService. When the service needs the adapter disk, mount
	// the service's adapter subtree read-only at /adapters — present even at zero
	// adapters in dynamic mode, so add/remove never restarts the pod.
	if isvc := buildInferenceServiceFromProfile(service, obs); isvc != nil {
		switch {
		case aimadapter.PreserveExistingMount(service, obs.adapterState) && isvcExists:
			// Preserve-on-unknown: the adapter disk PVC didn't resolve this cycle.
			// Re-applying would drop the adapter mount and restart the running
			// predictor, so leave the existing ISVC untouched and retry.
			logger.V(1).Info("Adapter disk PVC unresolved; preserving running ISVC adapter wiring")
			if planResult.RequeueAfter == 0 {
				planResult.RequeueAfter = 5 * time.Second
			}
		default:
			if service.Spec.AdaptersEnabled() {
				aimadapter.AddVolumeMount(isvc, service, obs.adapterState.AdapterDiskPVC)
			}
			planResult.Apply(isvc)
		}
	}

	// 4. Plan the HTTPRoute if routing is enabled on the merged runtime
	// config. The builder and naming scheme are shared with the v1alpha1
	// pipeline so routing behaves identically regardless of which pipeline
	// owns the service.
	if route := v1alpha1service.PlanHTTPRoute(ctx, service, obs.mergedRuntimeConfig.Value, obs.gateway.Value); route != nil {
		planResult.Apply(route)
	}

	// 5. Plan the KEDA ScaledObject. effectiveResources is nil until the
	// profile is Ready, in which case PlanScaledObject falls back to its flat
	// cooldown default and the next reconcile re-plans idempotently.
	effectiveResources := resolveEffectiveResourcesFromProfile(service, obs.resolvedProfileSpec, obs.resolvedProfileStatus)
	if so := v1alpha1service.PlanScaledObject(ctx, service, effectiveResources); so != nil {
		planResult.Apply(so)
	}

	return planResult
}

// DecorateStatus fills in profile-pipeline-specific status fields. Resolved
// references are only set when the upstream resource is Ready, so the
// operator can re-resolve them on the next reconcile until they become
// stable.
func (r *ProfileServiceReconciler) DecorateStatus(
	status *aimv1alpha1.AIMServiceStatus,
	_ *controllerutils.ConditionManager,
	obs ServiceObservation,
) {
	if obs.profileName != "" && obs.resolvedProfileStatus != nil &&
		obs.resolvedProfileStatus.Status == constants.AIMStatusReady {
		status.ResolvedProfile = &aimv1alpha1.AIMResolvedReference{
			Name:  obs.profileName,
			Scope: obs.profileScope,
		}
		// Key off the resolved scope (set in ComposeState) rather than which
		// FetchResult happens to be populated, so cluster vs. namespace is a
		// single source of truth.
		switch obs.profileScope {
		case aimv1alpha1.AIMResolutionScopeNamespace:
			if obs.overlayProfile.OK() && obs.overlayProfile.Value != nil {
				status.ResolvedProfile.UID = obs.overlayProfile.Value.UID
				status.ResolvedProfile.Namespace = obs.overlayProfile.Value.Namespace
			} else if obs.desiredOverlayProfile == nil && obs.profile.OK() && obs.profile.Value != nil {
				status.ResolvedProfile.UID = obs.profile.Value.UID
				status.ResolvedProfile.Namespace = obs.profile.Value.Namespace
			}
		case aimv1alpha1.AIMResolutionScopeCluster:
			if obs.clusterProfile.OK() && obs.clusterProfile.Value != nil {
				status.ResolvedProfile.UID = obs.clusterProfile.Value.UID
			}
		}

		// Surface the model ID carried on the resolved profile so the Model
		// printcolumn on the AIMService matches what the runtime will serve.
		if obs.resolvedProfileSpec != nil && obs.resolvedProfileSpec.ModelId != "" {
			status.ResolvedModel = &aimv1alpha1.AIMResolvedReference{
				Name:  obs.resolvedProfileSpec.ModelId,
				Scope: obs.profileScope,
			}
		}
	}

	if obs.profileCache.Value != nil && obs.profileCache.Value.Status.Status == constants.AIMStatusReady {
		status.Cache = &aimv1alpha1.AIMServiceCacheStatus{
			ProfileCacheRef: &aimv1alpha1.AIMResolvedReference{
				Name:      obs.profileCache.Value.Name,
				Namespace: obs.profileCache.Value.Namespace,
				UID:       obs.profileCache.Value.UID,
			},
		}
	}

	if obs.httpRoute.Value != nil {
		status.Routing = &aimv1alpha1.AIMServiceRoutingStatus{}
	}

	if obs.runtimeStatus != nil {
		status.Runtime = obs.runtimeStatus
	}

	if aimadapter.IsActive(obs.service) {
		aimadapter.DecorateStatus(status, obs.adapterState)
	}
}

// fetchProfileCache resolves the AIMProfileCache the AIMService should consume,
// honouring the service's caching mode and reusing any existing Shared cache
// that already references the same profile (e.g. one materialised by the
// AIMProfile reconciler when the profile opts into caching via
// spec.caching.enabled).
//
// Shared mode: list AIMProfileCaches in the namespace whose
// spec.profileName matches the resolved profile and spec.mode is Shared, and
// pick the healthiest. This is the v1alpha1 pattern (see
// internal/v1alpha1/aimservice/caching.go::searchTemplateCaches) and is what
// guarantees that a profile-driven cache and a service-driven cache cannot
// coexist for the same profile in the same namespace — one cache owns the
// downstream artifact, eliminating the watch-handler ambiguity that exists
// when two caches both create artifacts under the deterministic-by-weights
// name scheme.
//
// Dedicated mode: deterministic lookup by the per-service name. Dedicated is
// explicit per-service ownership, so we never reuse a Shared cache here.
//
// profileScope must match the scope the resolver settled on (Namespace or
// Cluster) so a namespace AIMProfile and a same-named cluster
// AIMClusterProfile in the same service namespace don't collide on a
// single cache. Empty scope is treated as Namespace for backwards
// compatibility with callers that haven't been updated.
func fetchProfileCache(
	ctx context.Context,
	c client.Client,
	service *aimv1alpha1.AIMService,
	profileName string,
	profileScope aimv1alpha1.AIMResolutionScope,
) controllerutils.FetchResult[*aimv1alpha2.AIMProfileCache] {
	if profileName == "" {
		return controllerutils.FetchResult[*aimv1alpha2.AIMProfileCache]{}
	}

	cachingMode := service.Spec.GetCachingMode()

	if cachingMode == aimv1alpha1.CachingModeDedicated {
		cacheName, err := GenerateProfileCacheName(
			profileName,
			service.Namespace,
			service.Name,
			string(service.UID),
			cachingMode,
			profileScope,
		)
		if err != nil {
			return controllerutils.FetchResult[*aimv1alpha2.AIMProfileCache]{Error: err}
		}

		return controllerutils.Fetch(ctx, c, client.ObjectKey{
			Namespace: service.Namespace,
			Name:      cacheName,
		}, &aimv1alpha2.AIMProfileCache{})
	}

	// Shared: search the namespace for any cache that already references this
	// profile in Shared mode and prefer the healthiest. Mirrors v1alpha1's
	// AIMTemplateCache resolution. ProfileScope must also match so we don't
	// reuse a cache that was created for a different scope's same-named
	// profile.
	cacheList := controllerutils.FetchList(ctx, c, &aimv1alpha2.AIMProfileCacheList{}, client.InNamespace(service.Namespace))
	if cacheList.Error != nil {
		return controllerutils.FetchResult[*aimv1alpha2.AIMProfileCache]{Error: cacheList.Error}
	}

	wantScope := profileScope
	if wantScope == "" {
		wantScope = aimv1alpha1.AIMResolutionScopeNamespace
	}
	matching := make([]aimv1alpha2.AIMProfileCache, 0)
	for _, cache := range cacheList.Value.Items {
		if cache.Spec.ProfileName != profileName {
			continue
		}
		if cache.Spec.Mode != aimv1alpha2.ProfileCacheModeShared {
			continue
		}
		gotScope := cache.Spec.ProfileScope
		if gotScope == "" {
			gotScope = aimv1alpha1.AIMResolutionScopeNamespace
		}
		if gotScope != wantScope {
			continue
		}
		matching = append(matching, cache)
	}
	if len(matching) == 0 {
		// Return a not-found result so downstream callers (and the
		// component-health logic) treat this as "cache not yet created"
		// — same semantics as a deterministic-name Fetch that 404s.
		// Without this, FetchResult{} with both Value and Error nil would
		// trip getProfileCacheHealth's `OK() == true → Value != nil`
		// invariant and panic.
		gvk := schema.GroupResource{Group: aimv1alpha2.GroupVersion.Group, Resource: "aimprofilecaches"}
		return controllerutils.FetchResult[*aimv1alpha2.AIMProfileCache]{Error: apierrors.NewNotFound(gvk, profileName)}
	}

	best := utils.SelectBestPtr(matching, func(cache *aimv1alpha2.AIMProfileCache) constants.AIMStatus {
		return cache.Status.GetAIMStatus()
	})
	if best == nil {
		gvk := schema.GroupResource{Group: aimv1alpha2.GroupVersion.Group, Resource: "aimprofilecaches"}
		return controllerutils.FetchResult[*aimv1alpha2.AIMProfileCache]{Error: apierrors.NewNotFound(gvk, profileName)}
	}
	return controllerutils.FetchResult[*aimv1alpha2.AIMProfileCache]{Value: best}
}

// planProfileCache builds the desired AIMProfileCache for the resolved profile
// when the profile actually carries model sources. Caching mode is honored
// here so the v1alpha2 chain (AIMService → AIMProfile → AIMProfileCache →
// AIMArtifact) mirrors v1alpha1's (AIMService → AIMServiceTemplate →
// AIMTemplateCache → AIMArtifact) Shared/Dedicated semantics. The caller is
// responsible for routing the result through Apply vs ApplyWithoutOwnerRef
// based on the same mode (see PlanResources) so Shared caches persist
// independently of any one service.
func planProfileCache(
	service *aimv1alpha1.AIMService,
	obs ServiceObservation,
) *aimv1alpha2.AIMProfileCache {
	if obs.profileCache.Value != nil {
		return nil
	}

	if obs.resolvedProfileSpec == nil || len(obs.resolvedProfileSpec.ModelSources) == 0 {
		return nil
	}

	cachingMode := service.Spec.GetCachingMode()
	cacheName, err := GenerateProfileCacheName(
		obs.profileName,
		service.Namespace,
		service.Name,
		string(service.UID),
		cachingMode,
		obs.profileScope,
	)
	if err != nil {
		return nil
	}

	serviceLabelValue, _ := utils.SanitizeLabelValue(service.Name)

	// Download-auth env, merged in increasing precedence:
	//  1. profile caching.env  - namespace-scoped profiles only; nil for
	//     cluster profiles (no caching field) and per-service overlays.
	//  2. service caching.env  - scope-agnostic, per-service. This is the
	//     path that reaches the download Job for cluster/overlay profiles.
	// Service wins on conflicting names. Empty leaves the profile cache
	// controller on its own defaults.
	var cacheEnv []corev1.EnvVar
	if obs.profile.Value != nil && obs.profile.Value.Spec.Caching != nil && len(obs.profile.Value.Spec.Caching.Env) > 0 {
		cacheEnv = utils.MergeEnvVars(cacheEnv, obs.profile.Value.Spec.Caching.Env)
	}
	if service.Spec.Caching != nil && len(service.Spec.Caching.Env) > 0 {
		cacheEnv = utils.MergeEnvVars(cacheEnv, service.Spec.Caching.Env)
	}

	storageClassName := ""
	if service.Spec.Storage != nil && service.Spec.Storage.DefaultStorageClassName != nil {
		storageClassName = *service.Spec.Storage.DefaultStorageClassName
	}

	return &aimv1alpha2.AIMProfileCache{
		TypeMeta: metav1.TypeMeta{
			APIVersion: aimv1alpha2.GroupVersion.String(),
			Kind:       "AIMProfileCache",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      cacheName,
			Namespace: service.Namespace,
			Labels: map[string]string{
				constants.LabelService: serviceLabelValue,
			},
		},
		Spec: aimv1alpha2.AIMProfileCacheSpec{
			ProfileName:      obs.profileName,
			ProfileScope:     obs.profileScope,
			StorageClassName: storageClassName,
			Mode:             profileCacheModeFor(cachingMode),
			Env:              cacheEnv,
			RuntimeConfigRef: service.GetRuntimeConfigRef(),
			// Adapters need the base artifact to carry a shared RWX adapter disk.
			RequiresAdapterDisk: service.Spec.AdaptersEnabled(),
		},
	}
}

// profileCacheModeFor maps the canonical AIMService caching mode (Shared or
// Dedicated, with legacy aliases already collapsed by GetCachingMode) onto
// the AIMProfileCache mode enum.
func profileCacheModeFor(cachingMode aimv1alpha1.AIMCachingMode) aimv1alpha2.AIMProfileCacheMode {
	if cachingMode == aimv1alpha1.CachingModeDedicated {
		return aimv1alpha2.ProfileCacheModeDedicated
	}
	return aimv1alpha2.ProfileCacheModeShared
}

// GenerateProfileCacheName creates a deterministic name for an AIMProfileCache.
// The naming scheme mirrors v1alpha1's GenerateTemplateCacheName so the two
// pipelines share their Shared/Dedicated identity model:
//
//   - Shared (default): name is derived from (profileName) hashed against
//     (namespace+"|"+profileScope, profileName). Two services in the same
//     namespace pointing at the same profile (same scope) converge on the
//     same cache and reuse it; same-named namespace and cluster profiles
//     get distinct caches because profileScope contributes to the hash. The
//     profile name is also included in the hash so two long profile names
//     that share a prefix do not collide on the same cache resource after
//     truncation.
//   - Dedicated: name is derived from (profileName, serviceName) hashed
//     against the service UID and profileScope. This keeps the visible
//     name readable while guaranteeing uniqueness across delete-and-recreate
//     of the same service name. Per-service-instance caches.
//
// serviceName / serviceUID are ignored for Shared mode and may be empty.
// profileScope must match the scope the resolver produced (Namespace or
// Cluster) so cache identity tracks the chosen profile.
func GenerateProfileCacheName(
	profileName, namespace, serviceName, serviceUID string,
	cachingMode aimv1alpha1.AIMCachingMode,
	profileScope aimv1alpha1.AIMResolutionScope,
) (string, error) {
	scope := string(profileScope)
	if scope == "" {
		// Treat empty as Namespace to keep cache names stable for
		// callers (tests, legacy migrations) that don't yet specify a
		// scope and would otherwise rotate cache identity.
		scope = string(aimv1alpha1.AIMResolutionScopeNamespace)
	}
	if cachingMode == aimv1alpha1.CachingModeDedicated {
		return utils.GenerateDerivedName(
			[]string{profileName, serviceName, "cache"},
			utils.WithHashSource(serviceUID+"|"+scope),
		)
	}
	return utils.GenerateDerivedName(
		[]string{profileName, "cache"},
		utils.WithHashSource(namespace+"|"+scope, profileName),
	)
}
