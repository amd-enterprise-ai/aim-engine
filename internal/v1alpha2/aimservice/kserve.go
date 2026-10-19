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
	"path/filepath"
	"sort"
	"strings"

	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/trust"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/aimartifact"
	v1alpha1svc "github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/aimservice"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimprofile"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/serving"
)

// fetchInferenceService fetches the existing InferenceService for the
// service. A name-generation failure is recorded on the FetchResult so
// ComposeState can surface it through component health instead of being
// silently dropped.
func fetchInferenceService(
	ctx context.Context,
	c client.Client,
	service *aimv1alpha1.AIMService,
) controllerutils.FetchResult[*servingv1beta1.InferenceService] {
	isvcName, err := v1alpha1svc.GenerateInferenceServiceName(service.Name, service.Namespace)
	if err != nil {
		return controllerutils.FetchResult[*servingv1beta1.InferenceService]{Error: err}
	}

	return controllerutils.Fetch(ctx, c, client.ObjectKey{
		Namespace: service.Namespace,
		Name:      isvcName,
	}, &servingv1beta1.InferenceService{})
}

// fetchLegacyProfileConfigMap fetches the legacy inline-predictor service-owned profile
// ConfigMap (<service>-profile-<hash>) by its deterministic legacy name. It is
// present only for services created before the runtime-reference rewrite; a
// not-found result is the normal case and is left for PlanResources to skip. A
// name-generation failure is recorded on the FetchResult so it surfaces through
// the fetch pipeline rather than being silently dropped.
func fetchLegacyProfileConfigMap(
	ctx context.Context,
	c client.Client,
	service *aimv1alpha1.AIMService,
) controllerutils.FetchResult[*corev1.ConfigMap] {
	name, err := legacyProfileConfigMapName(service.Name)
	if err != nil {
		return controllerutils.FetchResult[*corev1.ConfigMap]{Error: err}
	}

	return controllerutils.Fetch(ctx, c, client.ObjectKey{
		Namespace: service.Namespace,
		Name:      name,
	}, &corev1.ConfigMap{})
}

// buildInferenceServiceFromProfile constructs a KServe InferenceService that
// references the runtime projected from the resolved profile
// (aim-<profile.Name>) and overlays only service-specific fields. The inline
// predictor — image, base resources, affinity, profile ConfigMap, the full
// profile-derived framework env, and the profile-owned cache mount — lives on
// the referenced ServingRuntime (pre-materialized by the AIMService, projected
// lazily by the InferenceService-watch reconciler, or eagerly by the profile
// reconcilers), so the service and any native KServe consumer share one serving
// definition.
//
// The overlay carries only service-owned bits: replicas/autoscaling, auth
// annotations, service-level resource / pull-secret / service-account
// overrides, and a service-owned (Dedicated) cache.
func buildInferenceServiceFromProfile(
	service *aimv1alpha1.AIMService,
	obs ServiceObservation,
) *servingv1beta1.InferenceService {
	profileSpec := obs.resolvedProfileSpec
	profileStatus := obs.resolvedProfileStatus
	if profileSpec == nil || profileStatus == nil {
		return nil
	}
	if profileStatus.Status != constants.AIMStatusReady && service.Spec.Resources == nil {
		return nil
	}
	if obs.isvcName == "" {
		return nil
	}

	// The model overlay references the runtime by name. When the service carries
	// a resource override, write the fully merged block rather than the partial
	// override: KServe preserves omitted runtime keys during its overlay, which
	// could otherwise leave a service request above the retained runtime limit.
	model := &servingv1beta1.ModelSpec{
		ModelFormat: servingv1beta1.ModelFormat{Name: serving.RuntimeModelFormat},
		Runtime:     ptr.To(serving.RuntimeName(obs.profileName)),
	}
	if service.Spec.Resources != nil {
		if obs.effectiveResources != nil {
			model.Resources = *obs.effectiveResources.DeepCopy()
		} else {
			model.Resources = resolveResourcesFromProfile(service, profileSpec, profileStatus)
		}
	}

	serviceAccountName := service.Spec.ServiceAccountName
	if serviceAccountName == "" {
		serviceAccountName = profileSpec.ServiceAccountName
	}

	isvc := &servingv1beta1.InferenceService{
		TypeMeta: metav1.TypeMeta{
			APIVersion: servingv1beta1.SchemeGroupVersion.String(),
			Kind:       "InferenceService",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        obs.isvcName,
			Namespace:   service.Namespace,
			Labels:      buildInferenceServiceLabels(service, obs, profileSpec),
			Annotations: buildInferenceServiceAnnotations(service, obs, profileSpec),
		},
		Spec: servingv1beta1.InferenceServiceSpec{
			Predictor: servingv1beta1.PredictorSpec{
				PodSpec: servingv1beta1.PodSpec{
					ImagePullSecrets:   utils.CopyPullSecrets(service.Spec.ImagePullSecrets),
					ServiceAccountName: serviceAccountName,
					PriorityClassName:  service.Spec.PriorityClassName,
				},
				Model: model,
			},
		},
	}

	// Wire replicas, KEDA autoscaling, and autoscaler annotations. Shared with
	// the v1alpha1 template pipeline so both paths produce the same HPA shape
	// and semantics for Spec.Replicas / MinReplicas / MaxReplicas / AutoScaling.
	v1alpha1svc.ConfigureReplicasAndAutoscaling(isvc, service)

	// A service-owned (Dedicated) cache is overlaid onto the referenced runtime;
	// a profile-owned (Shared) cache is mounted by the runtime itself.
	addServiceOwnedCacheOverlay(isvc, service, obs)
	addRuntimeTrustOverlay(
		isvc,
		obs.mergedRuntimeConfig.Value,
		obs.additionalCABundle.Value,
	)

	return isvc
}

func addRuntimeTrustOverlay(
	isvc *servingv1beta1.InferenceService,
	runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon,
	caConfigMap *corev1.ConfigMap,
) {
	if runtimeConfig == nil ||
		runtimeConfig.AdditionalCABundleRef == nil ||
		caConfigMap == nil ||
		isvc.Spec.Predictor.Model == nil {
		return
	}
	projection := trust.BuildProjection(trust.Options{
		AdditionalCABundleRef: runtimeConfig.AdditionalCABundleRef,
		Image:                 aimartifact.ResolveDownloadImage(nil, runtimeConfig),
	})
	if projection == nil {
		return
	}
	predictor := &isvc.Spec.Predictor
	predictor.InitContainers = append(predictor.InitContainers, projection.InitContainer)
	predictor.Volumes = append(predictor.Volumes, projection.Volumes...)
	predictor.Model.VolumeMounts = append(predictor.Model.VolumeMounts, projection.VolumeMounts...)
	predictor.Model.Env = trust.UpsertEnv(predictor.Model.Env, projection.Env)
}

// buildInferenceServiceLabels builds the correlator labels stamped on the
// overlay ISVC so it can be traced back to its service and profile.
func buildInferenceServiceLabels(
	service *aimv1alpha1.AIMService,
	obs ServiceObservation,
	profileSpec *aimv1alpha2.AIMProfileSpecCommon,
) map[string]string {
	serviceLabelValue, _ := utils.SanitizeLabelValue(service.Name)
	profileLabelValue, _ := utils.SanitizeLabelValue(obs.profileName)

	labels := map[string]string{
		constants.LabelK8sComponent: constants.ComponentInference,
		constants.LabelK8sManagedBy: constants.LabelValueManagedBy,
		constants.LabelService:      serviceLabelValue,
		constants.LabelProfile:      profileLabelValue,
	}
	if string(profileSpec.Metric) != "" {
		metricVal, _ := utils.SanitizeLabelValue(string(profileSpec.Metric))
		labels[constants.LabelMetric] = metricVal
	}
	if string(profileSpec.Precision) != "" {
		precisionVal, _ := utils.SanitizeLabelValue(string(profileSpec.Precision))
		labels[constants.LabelPrecision] = precisionVal
	}
	return labels
}

// buildInferenceServiceAnnotations propagates auth annotations (cluster-auth/*),
// stamps the controller-owned model-id, and records the backing profile name so
// the lazy runtime-projection watcher can resolve and materialize the runtime in
// the service's namespace before it exists (the fast-path for a runtime not yet
// created). Stamped for BOTH scopes: a cluster profile is the cross-scope case,
// and a namespace profile needs it in Reduced mode, where no eager per-profile
// runtime exists and the watcher resolves the namespace AIMProfile in the
// service's namespace from this annotation. The native managed-CSR flow still
// needs no annotation.
func buildInferenceServiceAnnotations(
	service *aimv1alpha1.AIMService,
	obs ServiceObservation,
	profileSpec *aimv1alpha2.AIMProfileSpecCommon,
) map[string]string {
	annotations := utils.FilterAnnotationsByPrefix(service.Annotations, constants.AnnotationPrefixClusterAuth)
	if modelId := resolvedModelId(profileSpec); modelId != "" {
		annotations[constants.AnnotationModelId] = modelId
	}
	if obs.profileName != "" {
		annotations[constants.AnnotationRuntimeProfile] = obs.profileName
	}
	return annotations
}

// upsertEnvVars returns base with overrides applied: for each entry in overrides,
// any existing entry in base with the same Name is replaced in place; otherwise
// it is appended. Nil base is treated as empty. The returned slice is always
// distinct from base (no aliasing of the caller's slice header).
func upsertEnvVars(base, overrides []corev1.EnvVar) []corev1.EnvVar {
	result := append([]corev1.EnvVar(nil), base...)
	for _, o := range overrides {
		replaced := false
		for i := range result {
			if result[i].Name == o.Name {
				result[i] = o
				replaced = true
				break
			}
		}
		if !replaced {
			result = append(result, o)
		}
	}
	return result
}

// addServiceOwnedCacheOverlay overlays a service-owned cache onto the referenced
// runtime: the PVC volume(s), the volume mount(s) on the model container, and
// the framework redirect env that points model loading at the local cache. The
// env overrides the runtime container env by name (KServe merges predictor.model
// over the runtime container with the ISVC winning).
//
// Only a service-owned (Dedicated) cache is overlaid here. A Shared cache is
// profile-owned and mounted by the runtime itself, so overlaying it would
// duplicate the runtime's volume and is skipped.
func addServiceOwnedCacheOverlay(
	isvc *servingv1beta1.InferenceService,
	service *aimv1alpha1.AIMService,
	obs ServiceObservation,
) {
	if service.Spec.GetCachingMode() != aimv1alpha1.CachingModeDedicated {
		return
	}
	if !obs.profileCacheReady || obs.profileCache.Value == nil {
		return
	}
	model := isvc.Spec.Predictor.Model
	if model == nil {
		return
	}

	model.Env = upsertEnvVars(model.Env, buildFrameworkEnvVars(obs.resolvedProfileSpec, obs.profileYAMLName))
	model.Env = upsertEnvVars(model.Env, serving.BuildDirectVLLMCacheEnv(obs.resolvedProfileSpec, obs.profileCache.Value))

	for _, resolved := range sortedReadyArtifacts(obs.profileCache.Value) {
		volumeName := strings.ReplaceAll(utils.MakeRFC1123Compliant(resolved.Name), ".", "-")

		isvc.Spec.Predictor.Volumes = append(isvc.Spec.Predictor.Volumes, corev1.Volume{
			Name: volumeName,
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: resolved.PersistentVolumeClaim,
				},
			},
		})

		mountPath := resolved.MountPoint
		if mountPath == "" {
			safeModelName := strings.ReplaceAll(resolved.Model, "..", "")
			if safeModelName == "" || safeModelName == "." {
				safeModelName = volumeName
			}
			mountPath = filepath.Join(constants.AIMCacheBasePath, safeModelName)
		}

		model.VolumeMounts = append(model.VolumeMounts, corev1.VolumeMount{
			Name:      volumeName,
			MountPath: mountPath,
		})
	}
}

// sortedReadyArtifacts returns the cache's ready, PVC-backed artifacts in name
// order so the overlay is deterministic (stable SSA, stable tests).
func sortedReadyArtifacts(cache *aimv1alpha2.AIMProfileCache) []aimv1alpha1.AIMResolvedArtifact {
	names := make([]string, 0, len(cache.Status.Artifacts))
	for name := range cache.Status.Artifacts {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]aimv1alpha1.AIMResolvedArtifact, 0, len(names))
	for _, name := range names {
		resolved := cache.Status.Artifacts[name]
		if resolved.Status != constants.AIMStatusReady || resolved.PersistentVolumeClaim == "" {
			continue
		}
		out = append(out, resolved)
	}
	return out
}

// buildFrameworkEnvVars returns the operator-managed AIM_* environment that
// locates the projected profile (and redirects model loading when caching is
// active). It is purely profile-derived, so the implementation lives on the
// shared serving builder; the AIMService reconciler layers it on top of the
// resolved profile's containerEnv so users cannot reshape AIM_* identity vars.
func buildFrameworkEnvVars(profileSpec *aimv1alpha2.AIMProfileSpecCommon, profileYAMLFilename string) []corev1.EnvVar {
	return serving.BuildFrameworkEnvVars(profileSpec, profileYAMLFilename)
}

// resolveEffectiveResourcesFromProfile returns the fully-merged predictor
// ResourceRequirements, or nil until the profile has produced status.
// PlanScaledObject uses this to derive a memory-aware cooldown.
func resolveEffectiveResourcesFromProfile(
	service *aimv1alpha1.AIMService,
	profileSpec *aimv1alpha2.AIMProfileSpecCommon,
	profileStatus *aimv1alpha2.AIMProfileStatus,
) *corev1.ResourceRequirements {
	if profileSpec == nil || profileStatus == nil {
		return nil
	}
	if profileStatus.Status != constants.AIMStatusReady && service.Spec.Resources == nil {
		return nil
	}
	rr := resolveResourcesFromProfile(service, profileSpec, profileStatus)
	return &rr
}

// resolvedModelId returns the model id the runtime serves under, mirroring its
// resolution order: ModelId, else modelSources[0].modelId, else aimId.
func resolvedModelId(profileSpec *aimv1alpha2.AIMProfileSpecCommon) string {
	if profileSpec.ModelId != "" {
		return profileSpec.ModelId
	}
	if len(profileSpec.ModelSources) > 0 {
		return profileSpec.ModelSources[0].ModelID
	}
	return profileSpec.AimId
}

func resolveResourcesFromProfile(
	service *aimv1alpha1.AIMService,
	profileSpec *aimv1alpha2.AIMProfileSpecCommon,
	profileStatus *aimv1alpha2.AIMProfileStatus,
) corev1.ResourceRequirements {
	// Use profile status.resources (computed by profile controller)
	if profileStatus != nil && profileStatus.Resources != nil {
		return mergeResourceRequirements(profileStatus.Resources, service.Spec.Resources)
	}

	// Fall back to spec.resources
	if profileSpec.Resources != nil {
		return mergeResourceRequirements(profileSpec.Resources, service.Spec.Resources)
	}

	return mergeResourceRequirements(nil, service.Spec.Resources)
}

// effectiveResourcesForService resolves profile defaults directly when status
// has not populated resources yet, then applies the service's per-key overlay.
func effectiveResourcesForService(
	service *aimv1alpha1.AIMService,
	profileSpec *aimv1alpha2.AIMProfileSpecCommon,
	profileStatus *aimv1alpha2.AIMProfileStatus,
) *corev1.ResourceRequirements {
	if profileSpec == nil {
		return nil
	}
	if profileStatus != nil && profileStatus.Resources != nil {
		merged := mergeResourceRequirements(profileStatus.Resources, service.Spec.Resources)
		return &merged
	}
	base := aimprofile.ResolveProfileResources(*profileSpec)
	merged := mergeResourceRequirements(base, service.Spec.Resources)
	return &merged
}

// mergeResourceRequirements overlays requests and limits independently and
// returns a deep copy, preserving unspecified profile keys.
func mergeResourceRequirements(
	base *corev1.ResourceRequirements,
	override *corev1.ResourceRequirements,
) corev1.ResourceRequirements {
	var merged corev1.ResourceRequirements
	if base != nil {
		merged = *base.DeepCopy()
	}
	if override == nil {
		return merged
	}
	if len(override.Requests) > 0 {
		if merged.Requests == nil {
			merged.Requests = make(corev1.ResourceList)
		}
		for name, qty := range override.Requests {
			merged.Requests[name] = qty.DeepCopy()
		}
	}
	if len(override.Limits) > 0 {
		if merged.Limits == nil {
			merged.Limits = make(corev1.ResourceList)
		}
		for name, qty := range override.Limits {
			merged.Limits[name] = qty.DeepCopy()
		}
	}

	// Extended device resources are non-overcommitable: when a service changes
	// only one side, mirror it to the other side instead of retaining a
	// different runtime value.
	for name, request := range override.Requests {
		if isExtendedDeviceResource(name) {
			if _, explicitlyLimited := override.Limits[name]; !explicitlyLimited {
				if merged.Limits == nil {
					merged.Limits = make(corev1.ResourceList)
				}
				merged.Limits[name] = request.DeepCopy()
			}
		}
	}
	for name, limit := range override.Limits {
		if isExtendedDeviceResource(name) {
			if _, explicitlyRequested := override.Requests[name]; !explicitlyRequested {
				if merged.Requests == nil {
					merged.Requests = make(corev1.ResourceList)
				}
				merged.Requests[name] = limit.DeepCopy()
			}
		}
	}

	// A one-sided service override is authoritative over the inherited pair.
	// Adjust the omitted side when retaining it would violate request<=limit.
	for name, request := range override.Requests {
		if _, explicitlyLimited := override.Limits[name]; explicitlyLimited {
			continue
		}
		if limit, exists := merged.Limits[name]; exists && request.Cmp(limit) > 0 {
			merged.Limits[name] = request.DeepCopy()
		}
	}
	for name, limit := range override.Limits {
		if _, explicitlyRequested := override.Requests[name]; explicitlyRequested {
			continue
		}
		if request, exists := merged.Requests[name]; exists && request.Cmp(limit) > 0 {
			merged.Requests[name] = limit.DeepCopy()
		}
	}
	return merged
}

func validateResourceRequirements(resources *corev1.ResourceRequirements) error {
	if resources == nil {
		return nil
	}
	for name, request := range resources.Requests {
		if limit, exists := resources.Limits[name]; exists &&
			(request.Cmp(limit) > 0 || isExtendedDeviceResource(name) && request.Cmp(limit) != 0) {
			return fmt.Errorf(
				"requests.%s (%s) is incompatible with limits.%s (%s)",
				name,
				request.String(),
				name,
				limit.String(),
			)
		}
	}
	return nil
}

func isExtendedDeviceResource(name corev1.ResourceName) bool {
	return strings.Contains(string(name), "/")
}
