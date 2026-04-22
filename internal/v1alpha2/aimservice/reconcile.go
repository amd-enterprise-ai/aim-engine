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

	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	aimruntimeconfig "github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/aimruntimeconfig"
	v1alpha1service "github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/aimservice"
)

// ProfileServiceReconciler implements the domain logic for profile-based
// AIMService reconciliation. It uses the v1alpha1 Go types because v1alpha1
// is the storage version; the profile-specific spec fields live on
// aimv1alpha1.AIMService while the referenced profile is defined in v1alpha2.
type ProfileServiceReconciler struct {
	Scheme *runtime.Scheme
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

	profileCache controllerutils.FetchResult[*aimv1alpha2.AIMProfileCache]

	inferenceService controllerutils.FetchResult[*servingv1beta1.InferenceService]
	hpa              controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler]
	httpRoute        controllerutils.FetchResult[*gatewayapiv1.HTTPRoute]

	mergedRuntimeConfig controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]
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
}

// GetComponentHealth returns health entries for each component the service
// depends on or owns.
func (obs ServiceObservation) GetComponentHealth(_ context.Context, _ kubernetes.Interface) []controllerutils.ComponentHealth {
	var health []controllerutils.ComponentHealth

	if cfg := obs.getConfigHealth(); cfg.Component != "" {
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

	if route := v1alpha1service.HTTPRouteComponentHealth(obs.service, obs.mergedRuntimeConfig.Value, obs.httpRoute); route.Component != "" {
		health = append(health, route)
	}

	return health
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

	if obs.profile.Error != nil && !obs.profile.IsNotFound() {
		health.State = constants.AIMStatusFailed
		health.Errors = []error{obs.profile.Error}
		return health
	}

	if obs.resolvedProfileSpec != nil {
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

	health.State = constants.AIMStatusPending
	health.Reason = aimv1alpha1.AIMServiceReasonProfileNotFound
	health.Message = "No profile found for service"
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

	// Fetch HPA if the InferenceService exists. HPA is only present when
	// KServe has created the predictor (KEDA names it keda-hpa-{isvc}-predictor).
	if result.inferenceService.OK() && result.inferenceService.Value != nil {
		result.hpa = v1alpha1service.FetchHPA(ctx, c, result.inferenceService.Value)
	}

	// Fetch the profile (namespace-scoped takes precedence, cluster-scoped is
	// the fallback when the namespaced profile is not found).
	if service.Spec.Profile != nil {
		profileName := service.Spec.Profile.Name

		result.profile = controllerutils.Fetch(ctx, c, client.ObjectKey{
			Namespace: service.Namespace,
			Name:      profileName,
		}, &aimv1alpha2.AIMProfile{})

		if result.profile.IsNotFound() {
			result.clusterProfile = controllerutils.Fetch(ctx, c, client.ObjectKey{
				Name: profileName,
			}, &aimv1alpha2.AIMClusterProfile{})
		}
	}

	result.profileCache = fetchProfileCache(ctx, c, service)

	// Fetch merged runtime config. Needed for routing (HTTPRoute) and label
	// propagation. FetchMergedRuntimeConfig falls back to the default
	// cluster-scoped runtime config when the ref is empty.
	runtimeConfigRef := service.GetRuntimeConfigRef()
	result.mergedRuntimeConfig = aimruntimeconfig.FetchMergedRuntimeConfig(ctx, c, runtimeConfigRef.Name, service.Namespace)

	// Fetch the HTTPRoute (only exists when routing is enabled on the
	// service or runtime config).
	result.httpRoute = v1alpha1service.FetchHTTPRoute(ctx, c, service, result.mergedRuntimeConfig.Value)

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

	if fetch.profile.OK() && fetch.profile.Value != nil {
		obs.resolvedProfileSpec = &fetch.profile.Value.Spec.AIMProfileSpecCommon
		obs.resolvedProfileStatus = &fetch.profile.Value.Status
		obs.profileName = fetch.profile.Value.Name
		obs.profileScope = aimv1alpha1.AIMResolutionScopeNamespace
	} else if fetch.clusterProfile.OK() && fetch.clusterProfile.Value != nil {
		obs.resolvedProfileSpec = &fetch.clusterProfile.Value.Spec.AIMProfileSpecCommon
		obs.resolvedProfileStatus = &fetch.clusterProfile.Value.Status
		obs.profileName = fetch.clusterProfile.Value.Name
		obs.profileScope = aimv1alpha1.AIMResolutionScopeCluster
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

	return obs
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

	yamlBytes, filename, err := assembleProfileYAML(obs.resolvedProfileSpec, service.Spec.ProfileOverrides)
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

	if obs.resolvedProfileSpec == nil {
		logger.V(1).Info("No profile resolved, skipping resource planning")
		return planResult
	}

	if obs.resolvedProfileStatus == nil || obs.resolvedProfileStatus.Status != constants.AIMStatusReady {
		logger.V(1).Info("Profile not ready, skipping resource planning", "profile", obs.profileName)
		return planResult
	}

	// 1. Plan AIMProfileCache if the profile has model sources.
	if obs.hasModelSources {
		if pc := planProfileCache(service, obs); pc != nil {
			planResult.ApplyWithoutOwnerRef(pc)
		}

		if !obs.profileCacheReady {
			logger.V(1).Info("Profile cache not ready, deferring ISVC creation", "profile", obs.profileName)
			return planResult
		}
	}

	// 2. Plan the profile ConfigMap (owned by AIMService) using the YAML
	// rendered once in ComposeState.
	if obs.profileAssembled {
		planResult.Apply(buildProfileConfigMap(service, obs.configMapName, obs.profileYAMLName, obs.profileYAML))
	}

	// 3. Plan the InferenceService.
	if isvc := buildInferenceServiceFromProfile(service, obs); isvc != nil {
		planResult.Apply(isvc)
	}

	// 4. Plan the HTTPRoute if routing is enabled on the merged runtime
	// config. The builder and naming scheme are shared with the v1alpha1
	// pipeline so routing behaves identically regardless of which pipeline
	// owns the service.
	if route := v1alpha1service.PlanHTTPRoute(ctx, service, obs.mergedRuntimeConfig.Value); route != nil {
		planResult.Apply(route)
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
			if obs.profile.OK() && obs.profile.Value != nil {
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
			TemplateCacheRef: &aimv1alpha1.AIMResolvedReference{
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
}

// fetchProfileCache searches for an AIMProfileCache matching the service's profile reference.
func fetchProfileCache(
	ctx context.Context,
	c client.Client,
	service *aimv1alpha1.AIMService,
) controllerutils.FetchResult[*aimv1alpha2.AIMProfileCache] {
	if service.Spec.Profile == nil {
		return controllerutils.FetchResult[*aimv1alpha2.AIMProfileCache]{}
	}

	cacheName, err := GenerateProfileCacheName(service.Spec.Profile.Name, service.Namespace)
	if err != nil {
		return controllerutils.FetchResult[*aimv1alpha2.AIMProfileCache]{Error: err}
	}

	return controllerutils.Fetch(ctx, c, client.ObjectKey{
		Namespace: service.Namespace,
		Name:      cacheName,
	}, &aimv1alpha2.AIMProfileCache{})
}

// planProfileCache creates an AIMProfileCache for the resolved profile's model sources.
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

	cacheName, err := GenerateProfileCacheName(obs.profileName, service.Namespace)
	if err != nil {
		return nil
	}

	serviceLabelValue, _ := utils.SanitizeLabelValue(service.Name)

	// Prefer the profile's caching.env when present (namespace-scoped
	// profiles only); otherwise the profile cache controller falls back to
	// its own defaults.
	var cacheEnv []corev1.EnvVar
	if obs.profile.Value != nil && obs.profile.Value.Spec.Caching != nil && len(obs.profile.Value.Spec.Caching.Env) > 0 {
		cacheEnv = obs.profile.Value.Spec.Caching.Env
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
			Mode:             aimv1alpha2.ProfileCacheModeShared,
			Env:              cacheEnv,
		},
	}
}

// GenerateProfileCacheName creates a deterministic name for a profile cache.
// Shared caches are scoped to the profile name and namespace for reuse.
func GenerateProfileCacheName(profileName, namespace string) (string, error) {
	return utils.GenerateDerivedName(
		[]string{profileName, "cache"},
		utils.WithHashSource(namespace),
	)
}
