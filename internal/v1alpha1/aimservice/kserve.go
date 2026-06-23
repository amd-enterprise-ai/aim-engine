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
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	v1alpha1utils "github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/utils"
)

// GenerateInferenceServiceName creates a deterministic name for the InferenceService.
// KServe creates hostnames in format {isvc-name}-predictor-{namespace}, which must be ≤ 63 chars.
// We calculate the maximum allowed InferenceService name length based on the namespace length
// to ensure the final hostname stays within DNS limits.
func GenerateInferenceServiceName(serviceName, namespace string) (string, error) {
	// KServe hostname format: {isvc-name}-predictor-{namespace}.{domain}
	// The first DNS label ({isvc-name}-predictor-{namespace}) must be ≤ 63 chars
	// "-predictor-" is 11 characters
	predictorSuffix := len("-predictor-") + len(namespace)
	maxIsvcNameLength := utils.MaxKubernetesNameLength - predictorSuffix

	// Ensure minimum length for the name
	if maxIsvcNameLength < 10 {
		return "", fmt.Errorf("namespace %q is too long (%d chars); InferenceService hostname would exceed 63 characters", namespace, len(namespace))
	}

	// Hash on (namespace, serviceName) so the suffix stays unique per AIMService
	// even when the visible name part is truncated to fit a long namespace.
	return utils.GenerateDerivedName([]string{serviceName},
		utils.WithHashSource(namespace, serviceName),
		utils.WithMaxLength(maxIsvcNameLength))
}

// fetchInferenceService fetches the existing InferenceService for the service.
func fetchInferenceService(
	ctx context.Context,
	c client.Client,
	service *aimv1alpha1.AIMService,
) controllerutils.FetchResult[*servingv1beta1.InferenceService] {
	isvcName, err := GenerateInferenceServiceName(service.Name, service.Namespace)
	if err != nil {
		return controllerutils.FetchResult[*servingv1beta1.InferenceService]{Error: err}
	}

	return controllerutils.Fetch(ctx, c, client.ObjectKey{
		Namespace: service.Namespace,
		Name:      isvcName,
	}, &servingv1beta1.InferenceService{})
}

// fetchInferenceServiceEvents fetches events for the InferenceService to detect configuration errors.
// Events are filtered by UID to avoid matching stale events from previous objects with the same name.
func fetchInferenceServiceEvents(
	ctx context.Context,
	c client.Client,
	isvc *servingv1beta1.InferenceService,
) controllerutils.FetchResult[*corev1.EventList] {
	result := controllerutils.FetchList(ctx, c, &corev1.EventList{},
		client.InNamespace(isvc.Namespace),
		client.MatchingFields{"involvedObject.name": isvc.Name},
	)

	// Filter events by UID to only include events for the current object
	if result.OK() && result.Value != nil {
		filtered := make([]corev1.Event, 0, len(result.Value.Items))
		for _, event := range result.Value.Items {
			if event.InvolvedObject.UID == isvc.UID {
				filtered = append(filtered, event)
			}
		}
		result.Value.Items = filtered
	}

	return result
}

// fetchHPA fetches the HorizontalPodAutoscaler for the InferenceService.
// KEDA creates HPAs with the naming pattern: keda-hpa-{isvc-name}-predictor
func fetchHPA(
	ctx context.Context,
	c client.Client,
	isvc *servingv1beta1.InferenceService,
) controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler] {
	// KEDA HPA naming pattern: keda-hpa-{isvc-name}-predictor
	hpaName := "keda-hpa-" + isvc.Name + constants.PredictorServiceSuffix

	return controllerutils.Fetch(ctx, c, client.ObjectKey{
		Namespace: isvc.Namespace,
		Name:      hpaName,
	}, &autoscalingv2.HorizontalPodAutoscaler{})
}

// planInferenceService creates the KServe InferenceService.
func planInferenceService(
	ctx context.Context,
	service *aimv1alpha1.AIMService,
	templateName string,
	templateSpec *aimv1alpha1.AIMServiceTemplateSpecCommon,
	templateStatus *aimv1alpha1.AIMServiceTemplateStatus,
	obs ServiceObservation,
) client.Object {
	logger := log.FromContext(ctx)

	// Check if we're ready to create the InferenceService
	if !isReadyForInferenceService(service, obs) {
		logger.V(1).Info("not ready to create InferenceService")
		return nil
	}

	// Build the InferenceService
	return buildInferenceService(service, templateName, templateSpec, templateStatus, obs)
}

// isReadyForInferenceService checks if all prerequisites are met to create or update the InferenceService.
func isReadyForInferenceService(_ *aimv1alpha1.AIMService, obs ServiceObservation) bool {
	// If the ISVC already exists, we're on the update path - always proceed.
	// Mutable fields (replicas, autoscaling, env, resources, etc.) should propagate
	// even if model or cache are transiently unhealthy.
	if obs.inferenceService.OK() && obs.inferenceService.Value != nil {
		return true
	}

	// Creation path: check model and cache readiness before creating ISVC.

	// Check model is ready
	modelReady := false
	if obs.modelResult.Model.Value != nil {
		modelReady = obs.modelResult.Model.Value.Status.Status == constants.AIMStatusReady
	} else if obs.modelResult.ClusterModel.Value != nil {
		modelReady = obs.modelResult.ClusterModel.Value.Status.Status == constants.AIMStatusReady
	}
	if !modelReady {
		return false
	}

	// All caching modes now use template cache (both Dedicated and Shared modes)
	// Template cache must be ready before creating InferenceService
	if obs.templateCache.Value == nil ||
		obs.templateCache.Value.Status.Status != constants.AIMStatusReady {
		return false
	}

	return true
}

// buildInferenceService constructs a KServe InferenceService with inline container spec.
// This approach embeds the container configuration directly in the InferenceService
// instead of referencing a separate ServingRuntime resource.
func buildInferenceService(
	service *aimv1alpha1.AIMService,
	templateName string,
	templateSpec *aimv1alpha1.AIMServiceTemplateSpecCommon,
	templateStatus *aimv1alpha1.AIMServiceTemplateStatus,
	obs ServiceObservation,
) *servingv1beta1.InferenceService {
	isvcName, _ := GenerateInferenceServiceName(service.Name, service.Namespace)

	// Build labels
	serviceLabelValue, _ := utils.SanitizeLabelValue(service.Name)
	templateLabelValue, _ := utils.SanitizeLabelValue(templateName)
	modelLabelValue := ""
	if templateSpec != nil {
		modelLabelValue, _ = utils.SanitizeLabelValue(templateSpec.ModelName)
	}

	labels := map[string]string{
		constants.LabelK8sComponent: constants.ComponentInference,
		constants.LabelK8sManagedBy: constants.LabelValueManagedBy,
		constants.LabelTemplate:     templateLabelValue,
		constants.LabelService:      serviceLabelValue,
	}
	if modelLabelValue != "" {
		labels[constants.LabelModelID] = modelLabelValue
	}

	// Add metric and precision labels from template status
	if templateStatus != nil && templateStatus.Profile != nil {
		if templateStatus.Profile.Metadata.Metric != "" {
			metricValue, _ := utils.SanitizeLabelValue(string(templateStatus.Profile.Metadata.Metric))
			labels[constants.LabelMetric] = metricValue
		}
		if templateStatus.Profile.Metadata.Precision != "" {
			precisionValue, _ := utils.SanitizeLabelValue(string(templateStatus.Profile.Metadata.Precision))
			labels[constants.LabelPrecision] = precisionValue
		}
	}

	// Propagate auth annotations (cluster-auth/*) from the AIMService, then
	// stamp the controller-owned model-id from the resolved template.
	annotations := utils.FilterAnnotationsByPrefix(service.Annotations, constants.AnnotationPrefixClusterAuth)
	if modelId := resolvedModelId(templateSpec); modelId != "" {
		annotations[constants.AnnotationModelId] = modelId
	}

	// Build environment variables
	envVars := buildMergedEnvVars(service, templateSpec, obs)

	// Determine the deployment image. Fine-tuned template copies stamp the
	// resolved image as the AnnotationDeploymentImageRef annotation at build
	// time so each copy can target a different base image (different version
	// per copy under versionPolicy=any, or different base family across owners
	// like aim-base vs aim-epyc-base for the same aimId). Otherwise fall back
	// to the resolved AIMModel's spec.image (the conventional path for image-
	// based and custom models).
	image := resolveDeploymentImage(obs)

	// Build resource requirements. Shared with resolveEffectiveResources
	// so planScaledObject sees the same merged resources as the ISVC.
	gpuCount, gpuResourceName := extractGPUFromTemplateStatus(templateStatus)
	resources := resolveResources(service, templateSpec, gpuCount, gpuResourceName)

	// Build shared memory volume
	dshmSizeLimit := resource.MustParse(constants.DefaultSharedMemorySize)

	inferenceService := &servingv1beta1.InferenceService{
		TypeMeta: metav1.TypeMeta{
			APIVersion: servingv1beta1.SchemeGroupVersion.String(),
			Kind:       "InferenceService",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        isvcName,
			Namespace:   service.Namespace,
			Labels:      labels,
			Annotations: annotations,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         service.APIVersion,
					Kind:               service.Kind,
					Name:               service.Name,
					UID:                service.UID,
					Controller:         ptr.To(true),
					BlockOwnerDeletion: ptr.To(true),
				},
			},
		},
		Spec: servingv1beta1.InferenceServiceSpec{
			Predictor: servingv1beta1.PredictorSpec{
				ComponentExtensionSpec: servingv1beta1.ComponentExtensionSpec{},
				PodSpec: servingv1beta1.PodSpec{
					ImagePullSecrets:   utils.CopyPullSecrets(service.Spec.ImagePullSecrets),
					ServiceAccountName: service.Spec.ServiceAccountName,
					PriorityClassName:  service.Spec.PriorityClassName,
					Containers: []corev1.Container{
						{
							Name:            constants.ContainerKServe,
							Image:           image,
							ImagePullPolicy: utils.PullPolicyForImage(image),
							Env:             envVars,
							Resources:       resources,
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: constants.DefaultHTTPPort,
									Name:          "http",
									Protocol:      corev1.ProtocolTCP,
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      constants.VolumeSharedMemory,
									MountPath: constants.MountPathSharedMemory,
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: constants.VolumeSharedMemory,
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{
									Medium:    corev1.StorageMediumMemory,
									SizeLimit: &dshmSizeLimit,
								},
							},
						},
					},
				},
			},
		},
	}

	// Configure replicas and autoscaling
	configureReplicasAndAutoscaling(inferenceService, service)

	// Apply GPU node affinity from template status.
	// The template controller computes resolvedNodeAffinity from GPU requirements
	// and actual cluster GPU resources (including VRAM from node labels).
	if templateStatus != nil && templateStatus.ResolvedNodeAffinity != nil {
		applyNodeAffinity(inferenceService, templateStatus.ResolvedNodeAffinity)
	}

	// Add custom profile volume and mount when template has a custom profile
	if v1alpha1utils.HasCustomProfile(templateSpec) {
		if _, _, err := v1alpha1utils.AssembleProfileYAML(templateSpec); err == nil {
			cmName := v1alpha1utils.ServiceCustomProfileConfigMapName(service.Name)
			vol := v1alpha1utils.BuildCustomProfileVolume(cmName)
			mount := v1alpha1utils.BuildCustomProfileVolumeMount(templateSpec.AimId)

			inferenceService.Spec.Predictor.Volumes = append(inferenceService.Spec.Predictor.Volumes, vol)
			inferenceService.Spec.Predictor.Containers[0].VolumeMounts = append(
				inferenceService.Spec.Predictor.Containers[0].VolumeMounts, mount,
			)
		}
	}

	// Add storage volumes (cache or PVC).
	// On the update path (ISVC already exists), preserve the existing volume spec
	// rather than re-resolving from artifacts. Artifacts or their PVCs may be
	// transiently unavailable, and re-resolving would cause SSA to strip the
	// storage volumes off the running ISVC.
	if obs.inferenceService.OK() && obs.inferenceService.Value != nil {
		preserveExistingStorageVolumes(inferenceService, obs.inferenceService.Value)
	} else {
		addStorageVolumes(inferenceService, obs)
	}

	return inferenceService
}

// resolvedModelId returns the model id the runtime serves under, mirroring its
// resolution order: ModelId, else modelSources[0].modelId, else aimId.
func resolvedModelId(templateSpec *aimv1alpha1.AIMServiceTemplateSpecCommon) string {
	if templateSpec == nil {
		return ""
	}
	if templateSpec.ModelId != "" {
		return templateSpec.ModelId
	}
	if len(templateSpec.ModelSources) > 0 {
		return templateSpec.ModelSources[0].ModelID
	}
	return templateSpec.AimId
}

// buildMergedEnvVars builds environment variables with hierarchical merging.
// Precedence order (highest to lowest):
// 1. Service.Spec.Env (user-specified on service)
// 2. Template.Spec.Env
// 3. Runtime config env vars
// 4. System defaults
func buildMergedEnvVars(
	service *aimv1alpha1.AIMService,
	templateSpec *aimv1alpha1.AIMServiceTemplateSpecCommon,
	obs ServiceObservation,
) []corev1.EnvVar {
	// Start with system defaults
	envVars := []corev1.EnvVar{
		{Name: constants.EnvAIMCachePath, Value: constants.AIMCacheBasePath},
		{Name: constants.EnvVLLMEnableMetrics, Value: "true"},
	}

	// Merge runtime config env vars
	// AIM_ENGINE_ARGS is deep-merged as JSON to preserve contributions from all sources
	if obs.mergedRuntimeConfig.Value != nil && len(obs.mergedRuntimeConfig.Value.Env) > 0 {
		envVars = utils.MergeEnvVars(envVars, obs.mergedRuntimeConfig.Value.Env, utils.EnvVarAIMEngineArgs)
	}

	hasCustomProfile := v1alpha1utils.HasCustomProfile(templateSpec)
	hasModelSources := templateSpec != nil && len(templateSpec.ModelSources) > 0

	// AIM_PROFILE_ID selection. Custom profile wins (ConfigMap-mounted file
	// under /workspace/aim-runtime/profiles/custom/); fall back to the built-in
	// profile id baked into the image.
	switch {
	case hasCustomProfile:
		if _, filename, err := v1alpha1utils.AssembleProfileYAML(templateSpec); err == nil {
			envVars = append(envVars, corev1.EnvVar{
				Name:  constants.EnvAIMProfileID,
				Value: v1alpha1utils.CustomProfileID(templateSpec.AimId, filename),
			})
		}
	case templateSpec != nil && templateSpec.ProfileId != "":
		envVars = append(envVars, corev1.EnvVar{Name: constants.EnvAIMProfileID, Value: templateSpec.ProfileId})
	}

	// Add metric if set on template
	if templateSpec != nil && templateSpec.Metric != nil {
		envVars = append(envVars, corev1.EnvVar{Name: constants.EnvAIMMetric, Value: string(*templateSpec.Metric)})
	}

	// Add precision if set on template
	if templateSpec != nil && templateSpec.Precision != nil {
		envVars = append(envVars, corev1.EnvVar{Name: constants.EnvAIMPrecision, Value: string(*templateSpec.Precision)})
	}

	// Merge template spec env vars
	// AIM_ENGINE_ARGS is deep-merged as JSON to preserve contributions from all sources
	if templateSpec != nil && len(templateSpec.Env) > 0 {
		envVars = utils.MergeEnvVars(envVars, templateSpec.Env, utils.EnvVarAIMEngineArgs)
	}

	// AIM_ID and AIM_MODEL_ID are mutually exclusive in the aim-runtime —
	// enforced at container startup (see aim-build's aim_runtime/config.py
	// and docs/custom_profiles.md). Two modes:
	//
	//   AIM_ID       — model-specific profile (family baked into image or
	//                  mounted as a custom profile at
	//                  /workspace/aim-runtime/profiles/custom/<aimId>/)
	//   AIM_MODEL_ID — general profile + custom weights (weight redirect
	//                  against a generic base image)
	//
	// Pick exactly one:
	//
	//   - hasCustomProfile: the user (or the fine-tune matcher) supplied a
	//     profile; mount it and let the runtime select it via AIM_ID. This
	//     applies whether or not modelSources is also set — a model-specific
	//     custom profile is the right mode when both are present.
	//   - hasModelSources only (no custom profile): emit AIM_MODEL_ID and
	//     explicitly clobber any "ENV AIM_ID=<family>" baked into a per-AIM
	//     container image; otherwise the runtime's mutual-exclusion check
	//     rejects the pod.
	//   - neither: leave it to the image's baked-in ENV AIM_ID.
	switch {
	case hasCustomProfile:
		envVars = append(envVars, corev1.EnvVar{Name: constants.EnvAIMID, Value: templateSpec.AimId})
	case hasModelSources:
		envVars = append(envVars,
			corev1.EnvVar{Name: constants.EnvAIMID, Value: ""},
			corev1.EnvVar{Name: constants.EnvAIMModelID, Value: templateSpec.ModelSources[0].ModelID},
		)
	}

	// Merge service-level env vars (highest precedence)
	// AIM_ENGINE_ARGS is deep-merged as JSON to preserve contributions from all sources
	if len(service.Spec.Env) > 0 {
		envVars = utils.MergeEnvVars(envVars, service.Spec.Env, utils.EnvVarAIMEngineArgs)
	}

	// Sort for deterministic ordering
	sort.Slice(envVars, func(i, j int) bool {
		return envVars[i].Name < envVars[j].Name
	})

	return envVars
}

// extractGPUFromTemplateStatus returns the GPU count and resource name
// from ResolvedHardware, falling back to DefaultGPUResourceName when the
// template hasn't named a resource.
func extractGPUFromTemplateStatus(templateStatus *aimv1alpha1.AIMServiceTemplateStatus) (int64, corev1.ResourceName) {
	gpuCount := int64(0)
	gpuResourceName := corev1.ResourceName(constants.DefaultGPUResourceName)
	if templateStatus != nil && templateStatus.ResolvedHardware != nil && templateStatus.ResolvedHardware.GPU != nil {
		gpuCount = int64(templateStatus.ResolvedHardware.GPU.Requests)
		if templateStatus.ResolvedHardware.GPU.ResourceName != "" {
			gpuResourceName = corev1.ResourceName(templateStatus.ResolvedHardware.GPU.ResourceName)
		}
	}
	return gpuCount, gpuResourceName
}

// resolveEffectiveResources returns the fully-merged predictor
// ResourceRequirements (service override > template > GPU-count default),
// or nil when the template is not yet Ready. planScaledObject uses this to
// derive a memory-aware cooldown; nil during the transient phase makes the
// cooldown fall back to the flat default until the next reconcile.
func resolveEffectiveResources(
	service *aimv1alpha1.AIMService,
	templateSpec *aimv1alpha1.AIMServiceTemplateSpecCommon,
	templateStatus *aimv1alpha1.AIMServiceTemplateStatus,
) *corev1.ResourceRequirements {
	if templateSpec == nil || templateStatus == nil || templateStatus.Status != constants.AIMStatusReady {
		return nil
	}
	gpuCount, gpuResourceName := extractGPUFromTemplateStatus(templateStatus)
	rr := resolveResources(service, templateSpec, gpuCount, gpuResourceName)
	return &rr
}

// resolveResources builds resource requirements for the inference container.
// Priority order (highest to lowest):
// 1. Service spec resources (user override)
// 2. Template spec resources
// 3. Default GPU resources from profile
// 4. Default CPU/memory based on GPU count
func resolveResources(
	service *aimv1alpha1.AIMService,
	templateSpec *aimv1alpha1.AIMServiceTemplateSpecCommon,
	gpuCount int64,
	gpuResourceName corev1.ResourceName,
) corev1.ResourceRequirements {
	// Start with defaults based on GPU count
	resources := defaultResourceRequirementsForGPU(gpuCount)

	// Set default GPU resources from template profile
	if gpuCount > 0 {
		if resources.Requests == nil {
			resources.Requests = corev1.ResourceList{}
		}
		if resources.Limits == nil {
			resources.Limits = corev1.ResourceList{}
		}
		qty := resource.NewQuantity(gpuCount, resource.DecimalSI)
		resources.Requests[gpuResourceName] = *qty
		resources.Limits[gpuResourceName] = *qty
	}

	// Override with template spec resources
	if templateSpec != nil && templateSpec.Resources != nil {
		resources = mergeResourceRequirements(resources, templateSpec.Resources)
	}

	// Override with service spec resources (highest priority - user can override GPU count)
	if service.Spec.Resources != nil {
		resources = mergeResourceRequirements(resources, service.Spec.Resources)
	}

	return resources
}

// defaultResourceRequirementsForGPU returns default CPU/memory based on GPU count.
func defaultResourceRequirementsForGPU(gpuCount int64) corev1.ResourceRequirements {
	if gpuCount <= 0 {
		return corev1.ResourceRequirements{}
	}

	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    *resource.NewQuantity(gpuCount*4, resource.DecimalSI),
			corev1.ResourceMemory: resource.MustParse(fmt.Sprintf("%dGi", gpuCount*32)),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse(fmt.Sprintf("%dGi", gpuCount*48)),
		},
	}
}

// mergeResourceRequirements merges override resources into base.
func mergeResourceRequirements(base corev1.ResourceRequirements, override *corev1.ResourceRequirements) corev1.ResourceRequirements {
	if override == nil {
		return base
	}

	if len(override.Requests) > 0 {
		if base.Requests == nil {
			base.Requests = corev1.ResourceList{}
		}
		for name, qty := range override.Requests {
			base.Requests[name] = qty.DeepCopy()
		}
	}

	if len(override.Limits) > 0 {
		if base.Limits == nil {
			base.Limits = corev1.ResourceList{}
		}
		for name, qty := range override.Limits {
			base.Limits[name] = qty.DeepCopy()
		}
	}

	return base
}

// configureReplicasAndAutoscaling sets up replica counts and autoscaling configuration.
func configureReplicasAndAutoscaling(isvc *servingv1beta1.InferenceService, service *aimv1alpha1.AIMService) {
	// Check if autoscaling is configured (new fields take precedence)
	hasAutoscaling := service.Spec.AutoScaling != nil ||
		service.Spec.MinReplicas != nil ||
		service.Spec.MaxReplicas != nil

	if hasAutoscaling {
		// autoscalerClass=external -- KServe writes no ScaledObject; the
		// controller-owned one in scaledobject.go is the sole author.
		injectAutoscalingAnnotations(isvc)

		// resolveReplicaBounds is the single source of truth for min/max
		// defaulting; the ScaledObject path consumes the same helper.
		minReplicas, maxReplicas := resolveReplicaBounds(service)
		isvc.Spec.Predictor.MinReplicas = ptr.To(minReplicas)
		isvc.Spec.Predictor.MaxReplicas = maxReplicas

		// Under autoscalerClass=external, KServe still uses
		// Spec.Predictor.AutoScaling.Metrics to size the per-ISVC
		// OpenTelemetryCollector CR that drives in-pod sidecar injection.
		// The duplication with planScaledObject's triggers is intentional:
		// KServe reads this for the sidecar; the ScaledObject is the sole
		// authority on trigger shape.
		if service.Spec.AutoScaling != nil {
			isvc.Spec.Predictor.AutoScaling = convertToKServeAutoScaling(service.Spec.AutoScaling)
		}
	} else if service.Spec.Replicas != nil {
		// Legacy: fixed replica count, disable HPA
		disableHPA(isvc)
		isvc.Spec.Predictor.MinReplicas = service.Spec.Replicas
		isvc.Spec.Predictor.MaxReplicas = *service.Spec.Replicas
	} else {
		// Default: 1 replica, disable HPA
		disableHPA(isvc)
		one := int32(1)
		isvc.Spec.Predictor.MinReplicas = &one
		isvc.Spec.Predictor.MaxReplicas = 1
	}
}

// disableHPA sets autoscaler to none to prevent HPA creation.
// Always overwrites the annotation so that switching from autoscaling (keda)
// to fixed replicas correctly updates the autoscaler class.
func disableHPA(isvc *servingv1beta1.InferenceService) {
	if isvc.Annotations == nil {
		isvc.Annotations = make(map[string]string)
	}
	isvc.Annotations[constants.AnnotationKServeAutoscalerClass] = constants.AutoscalerClassNone
}

// injectAutoscalingAnnotations adds the annotations the autoscaled predictor
// needs from KServe. autoscalerClass=external keeps KServe's KEDA reconciler
// out of the way so the controller can own the ScaledObject directly. OTel
// sidecar and Prometheus annotations are also injected for warm-state
// metrics. Always overwrites so transitions to/from fixed-replica
// mode update the values rather than leaving stale state.
func injectAutoscalingAnnotations(isvc *servingv1beta1.InferenceService) {
	if isvc.Annotations == nil {
		isvc.Annotations = make(map[string]string)
	}

	isvc.Annotations[constants.AnnotationKServeAutoscalerClass] = constants.AutoscalerClassExternal

	predictorName := isvc.Name + constants.PredictorServiceSuffix
	isvc.Annotations[constants.AnnotationOTelSidecarInject] = predictorName

	if _, exists := isvc.Annotations[constants.AnnotationPrometheusPort]; !exists {
		isvc.Annotations[constants.AnnotationPrometheusPort] = constants.DefaultPrometheusPort
	}
}

// convertToKServeAutoScaling converts AIM autoscaling config to a KServe
// AutoScalingSpec. Consumed by KServe only to configure the
// OpenTelemetryCollector for in-pod sidecar injection.
func convertToKServeAutoScaling(aimAutoScaling *aimv1alpha1.AIMServiceAutoScaling) *servingv1beta1.AutoScalingSpec {
	if aimAutoScaling == nil {
		return nil
	}

	kserveAutoScaling := &servingv1beta1.AutoScalingSpec{}

	if len(aimAutoScaling.Metrics) > 0 {
		kserveAutoScaling.Metrics = make([]servingv1beta1.MetricsSpec, len(aimAutoScaling.Metrics))
		for i, metric := range aimAutoScaling.Metrics {
			kserveMetric := servingv1beta1.MetricsSpec{
				Type: servingv1beta1.MetricSourceType(metric.Type),
			}

			if metric.Type == "PodMetric" && metric.PodMetric != nil {
				kserveMetric.PodMetric = &servingv1beta1.PodMetricSource{}

				if metric.PodMetric.Metric != nil {
					kserveMetric.PodMetric.Metric = servingv1beta1.PodMetrics{
						Backend:           servingv1beta1.PodsMetricsBackend(metric.PodMetric.Metric.Backend),
						ServerAddress:     metric.PodMetric.Metric.ServerAddress,
						MetricNames:       metric.PodMetric.Metric.MetricNames,
						Query:             metric.PodMetric.Metric.Query,
						OperationOverTime: metric.PodMetric.Metric.OperationOverTime,
					}
				}

				if metric.PodMetric.Target != nil {
					kserveMetric.PodMetric.Target = servingv1beta1.MetricTarget{
						Type: servingv1beta1.MetricTargetType(metric.PodMetric.Target.Type),
					}

					if metric.PodMetric.Target.Value != "" {
						kserveMetric.PodMetric.Target.Value = servingv1beta1.NewMetricQuantity(metric.PodMetric.Target.Value)
					}
					if metric.PodMetric.Target.AverageValue != "" {
						kserveMetric.PodMetric.Target.AverageValue = servingv1beta1.NewMetricQuantity(metric.PodMetric.Target.AverageValue)
					}
					if metric.PodMetric.Target.AverageUtilization != nil {
						kserveMetric.PodMetric.Target.AverageUtilization = metric.PodMetric.Target.AverageUtilization
					}
				}
			}

			kserveAutoScaling.Metrics[i] = kserveMetric
		}
	}

	return kserveAutoScaling
}

// addStorageVolumes adds cache volumes to the InferenceService.
// artifacts are resolved through the template cache status, which contains
// the list of ready artifacts with their PVC names and mount points.
func addStorageVolumes(isvc *servingv1beta1.InferenceService, obs ServiceObservation) {
	if len(isvc.Spec.Predictor.Containers) == 0 {
		return
	}
	container := &isvc.Spec.Predictor.Containers[0]

	// All caching now flows through template cache
	if obs.templateCache.Value == nil ||
		obs.templateCache.Value.Status.Status != constants.AIMStatusReady {
		return
	}

	// Use resolved artifacts from template cache status
	// This avoids fetching artifacts separately and keeps the CRD relationships explicit
	for _, resolvedCache := range obs.templateCache.Value.Status.Artifacts {
		if resolvedCache.Status != constants.AIMStatusReady {
			continue
		}
		if resolvedCache.PersistentVolumeClaim == "" {
			continue
		}

		addResolvedCacheMount(isvc, container, resolvedCache)
	}
}

// preserveExistingStorageVolumes copies storage volumes and volume mounts from the existing
// InferenceService onto the new one being built for SSA. This is used on the update path
// to avoid re-resolving from artifacts, which may be transiently unavailable.
// Only non-base volumes are copied (the shared memory volume is already in the new ISVC).
func preserveExistingStorageVolumes(newISVC, existingISVC *servingv1beta1.InferenceService) {
	if len(newISVC.Spec.Predictor.Containers) == 0 || len(existingISVC.Spec.Predictor.Containers) == 0 {
		return
	}

	// Copy volumes from existing ISVC that are not already present in the new one
	existingVolumeNames := make(map[string]bool)
	for _, v := range newISVC.Spec.Predictor.Volumes {
		existingVolumeNames[v.Name] = true
	}
	for _, v := range existingISVC.Spec.Predictor.Volumes {
		if !existingVolumeNames[v.Name] {
			newISVC.Spec.Predictor.Volumes = append(newISVC.Spec.Predictor.Volumes, *v.DeepCopy())
		}
	}

	// Copy volume mounts from existing container that are not already present
	newContainer := &newISVC.Spec.Predictor.Containers[0]
	existingMountNames := make(map[string]bool)
	for _, vm := range newContainer.VolumeMounts {
		existingMountNames[vm.Name] = true
	}
	existingContainer := &existingISVC.Spec.Predictor.Containers[0]
	for _, vm := range existingContainer.VolumeMounts {
		if !existingMountNames[vm.Name] {
			newContainer.VolumeMounts = append(newContainer.VolumeMounts, *vm.DeepCopy())
		}
	}
}

// addResolvedCacheMount adds a resolved artifact PVC volume mount.
func addResolvedCacheMount(isvc *servingv1beta1.InferenceService, container *corev1.Container, cache aimv1alpha1.AIMResolvedArtifact) {
	// Sanitize volume name from the artifact name
	volumeName := utils.MakeRFC1123Compliant(cache.Name)
	volumeName = strings.ReplaceAll(volumeName, ".", "-")

	isvc.Spec.Predictor.Volumes = append(isvc.Spec.Predictor.Volumes, corev1.Volume{
		Name: volumeName,
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: cache.PersistentVolumeClaim,
			},
		},
	})

	// TODO: Consider removing MountPoint field if it's never used
	// Use mount point from resolved cache if available, otherwise derive from model ID
	mountPath := cache.MountPoint
	if mountPath == "" {
		// Use full model ID (e.g., "Qwen/Qwen2-0.5B") as mount path
		// Sanitize to prevent path traversal (remove ".." sequences)
		safeModelName := strings.ReplaceAll(cache.Model, "..", "")
		if safeModelName == "" || safeModelName == "." {
			safeModelName = volumeName // Fall back to volume name if model name is invalid
		}
		mountPath = filepath.Join(constants.AIMCacheBasePath, safeModelName)
	}

	container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
		Name:      volumeName,
		MountPath: mountPath,
	})
}

// applyNodeAffinity applies the pre-computed node affinity from the template status to the InferenceService.
// The template controller computes resolvedNodeAffinity from GPU requirements and actual cluster
// GPU resources (including VRAM from node labels), so this function simply applies it.
func applyNodeAffinity(isvc *servingv1beta1.InferenceService, nodeAffinity *corev1.NodeAffinity) {
	if nodeAffinity == nil {
		return
	}

	// Ensure Affinity exists
	if isvc.Spec.Predictor.Affinity == nil {
		isvc.Spec.Predictor.Affinity = &corev1.Affinity{}
	}

	// Apply the node affinity directly
	// TODO: In the future, merge with any existing service-level affinity if needed
	isvc.Spec.Predictor.Affinity.NodeAffinity = nodeAffinity.DeepCopy()
}

// resolveDeploymentImage returns the container image to deploy for this service.
//
// Priority:
//
//  1. AnnotationDeploymentImageRef on the resolved AIM(Cluster)ServiceTemplate:
//     stamped by the AIMModel controller onto fine-tuned template copies so
//     each copy carries its specifically resolved base image. This is the
//     authoritative source for fine-tuned services and lets sibling copies
//     under one fine-tuned AIMModel target different images (e.g. one copy
//     per version with versionPolicy=any, or aim-base vs aim-epyc-base for
//     the same aimId across owners).
//  2. AIMModel/AIMClusterModel spec.image: conventional path for image-based
//     and custom models, and the fallback when a template hasn't been
//     stamped (e.g. older copies created before this annotation existed).
func resolveDeploymentImage(obs ServiceObservation) string {
	if t := obs.template.Value; t != nil {
		if ref := t.Annotations[constants.AnnotationDeploymentImageRef]; ref != "" {
			return ref
		}
	}
	if t := obs.clusterTemplate.Value; t != nil {
		if ref := t.Annotations[constants.AnnotationDeploymentImageRef]; ref != "" {
			return ref
		}
	}
	if m := obs.modelResult.Model.Value; m != nil {
		return m.Spec.Image
	}
	if m := obs.modelResult.ClusterModel.Value; m != nil {
		return m.Spec.Image
	}
	return ""
}
