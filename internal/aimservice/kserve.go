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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
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

	return utils.GenerateDerivedName([]string{serviceName},
		utils.WithHashSource(namespace),
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

// planInferenceService creates the KServe InferenceService.
func planInferenceService(
	ctx context.Context,
	service *aimv1alpha1.AIMService,
	templateName string,
	templateSpec *aimv1alpha1.AIMServiceTemplateSpec,
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

// isReadyForInferenceService checks if all prerequisites are met to create the InferenceService.
func isReadyForInferenceService(service *aimv1alpha1.AIMService, obs ServiceObservation) bool {
	cachingMode := service.Spec.GetCachingMode()

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

	// Check if we have storage
	switch cachingMode {
	case aimv1alpha1.CachingModeAlways:
		// Require template cache to be ready
		if obs.templateCache.Value == nil ||
			obs.templateCache.Value.Status.Status != constants.AIMStatusReady {
			return false
		}
	case aimv1alpha1.CachingModeAuto:
		// Either cache or PVC is fine
		hasCache := obs.templateCache.Value != nil &&
			obs.templateCache.Value.Status.Status == constants.AIMStatusReady
		hasPVC := obs.pvc.Value != nil
		if !hasCache && !hasPVC {
			return false
		}
	case aimv1alpha1.CachingModeNever:
		// No cache required, but need PVC for download
		if obs.pvc.Value == nil {
			return false
		}
	}

	return true
}

// buildInferenceService constructs a KServe InferenceService with inline container spec.
// This approach embeds the container configuration directly in the InferenceService
// instead of referencing a separate ServingRuntime resource.
func buildInferenceService(
	service *aimv1alpha1.AIMService,
	templateName string,
	templateSpec *aimv1alpha1.AIMServiceTemplateSpec,
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

	// Build environment variables
	envVars := buildMergedEnvVars(service, templateSpec, templateStatus, obs)

	// Determine image from the resolved model
	image := ""
	if obs.modelResult.Model.Value != nil {
		image = obs.modelResult.Model.Value.Spec.Image
	} else if obs.modelResult.ClusterModel.Value != nil {
		image = obs.modelResult.ClusterModel.Value.Spec.Image
	}

	// Get GPU count from template status
	gpuCount := int64(0)
	if templateStatus != nil && templateStatus.Profile != nil {
		gpuCount = int64(templateStatus.Profile.Metadata.GPUCount)
	}

	// GPU resource name is always the default AMD GPU resource
	gpuResourceName := corev1.ResourceName(constants.DefaultGPUResourceName)

	// Build resource requirements
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
			Annotations: make(map[string]string),
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
					Containers: []corev1.Container{
						{
							Name:      constants.ContainerKServe,
							Image:     image,
							Env:       envVars,
							Resources: resources,
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

	// Add GPU node affinity
	if templateStatus != nil && templateStatus.Profile != nil && templateStatus.Profile.Metadata.GPU != "" {
		addGPUNodeAffinity(inferenceService, templateStatus.Profile.Metadata.GPU)
	}

	// Add storage volumes (cache or PVC)
	addStorageVolumes(inferenceService, obs)

	return inferenceService
}

// buildMergedEnvVars builds environment variables with hierarchical merging.
// Precedence order (highest to lowest):
// 1. Service.Spec.Env (user-specified on service)
// 2. Profile EnvVars (from template status)
// 3. Template.Spec.Env
// 4. Runtime config env vars
// 5. System defaults
func buildMergedEnvVars(
	service *aimv1alpha1.AIMService,
	templateSpec *aimv1alpha1.AIMServiceTemplateSpec,
	templateStatus *aimv1alpha1.AIMServiceTemplateStatus,
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

	// Merge profile env vars
	// AIM_ENGINE_ARGS is deep-merged as JSON to preserve contributions from all sources
	if templateStatus != nil && templateStatus.Profile != nil && len(templateStatus.Profile.EnvVars) > 0 {
		profileEnvVars := make([]corev1.EnvVar, 0, len(templateStatus.Profile.EnvVars))
		for name, value := range templateStatus.Profile.EnvVars {
			profileEnvVars = append(profileEnvVars, corev1.EnvVar{Name: name, Value: value})
		}
		envVars = utils.MergeEnvVars(envVars, profileEnvVars, utils.EnvVarAIMEngineArgs)
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

// resolveResources builds resource requirements for the inference container.
// Priority order (highest to lowest):
// 1. Service spec resources (user override)
// 2. Template spec resources
// 3. Default GPU resources from profile
// 4. Default CPU/memory based on GPU count
func resolveResources(
	service *aimv1alpha1.AIMService,
	templateSpec *aimv1alpha1.AIMServiceTemplateSpec,
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
		// Enable KEDA autoscaling
		injectAutoscalingAnnotations(isvc)

		// Set min replicas
		if service.Spec.MinReplicas != nil {
			isvc.Spec.Predictor.MinReplicas = service.Spec.MinReplicas
		} else {
			one := int32(1)
			isvc.Spec.Predictor.MinReplicas = &one
		}

		// Set max replicas
		if service.Spec.MaxReplicas != nil {
			isvc.Spec.Predictor.MaxReplicas = *service.Spec.MaxReplicas
		} else if service.Spec.MinReplicas != nil {
			// Default max to min if only min specified
			isvc.Spec.Predictor.MaxReplicas = *service.Spec.MinReplicas
		} else {
			isvc.Spec.Predictor.MaxReplicas = 1
		}

		// Apply autoscaling configuration if provided
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
func disableHPA(isvc *servingv1beta1.InferenceService) {
	if isvc.Annotations == nil {
		isvc.Annotations = make(map[string]string)
	}
	if _, exists := isvc.Annotations[constants.AnnotationKServeAutoscalerClass]; !exists {
		isvc.Annotations[constants.AnnotationKServeAutoscalerClass] = constants.AutoscalerClassNone
	}
}

// injectAutoscalingAnnotations adds required annotations for KEDA autoscaling.
// This includes KEDA autoscaler class, OpenTelemetry sidecar injection, and Prometheus metrics port.
func injectAutoscalingAnnotations(isvc *servingv1beta1.InferenceService) {
	if isvc.Annotations == nil {
		isvc.Annotations = make(map[string]string)
	}

	// Add KEDA autoscaler class annotation if not already present
	if _, exists := isvc.Annotations[constants.AnnotationKServeAutoscalerClass]; !exists {
		isvc.Annotations[constants.AnnotationKServeAutoscalerClass] = constants.AutoscalerClassKeda
	}

	// Add OpenTelemetry sidecar injection annotation if not already present
	if _, exists := isvc.Annotations[constants.AnnotationOTelSidecarInject]; !exists {
		predictorName := isvc.Name + constants.PredictorServiceSuffix
		isvc.Annotations[constants.AnnotationOTelSidecarInject] = predictorName
	}

	// Add Prometheus metrics port annotation if not already present
	if _, exists := isvc.Annotations[constants.AnnotationPrometheusPort]; !exists {
		isvc.Annotations[constants.AnnotationPrometheusPort] = constants.DefaultPrometheusPort
	}
}

// convertToKServeAutoScaling converts AIM autoscaling config to KServe AutoScalingSpec.
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

// addStorageVolumes adds cache or PVC volumes to the InferenceService.
func addStorageVolumes(isvc *servingv1beta1.InferenceService, obs ServiceObservation) {
	if len(isvc.Spec.Predictor.Containers) == 0 {
		return
	}
	container := &isvc.Spec.Predictor.Containers[0]

	// Check if we have template cache with model caches
	if obs.templateCache.Value != nil &&
		obs.templateCache.Value.Status.Status == constants.AIMStatusReady &&
		obs.modelCaches.Value != nil {

		// Mount model cache PVCs
		for _, modelCache := range obs.modelCaches.Value.Items {
			if modelCache.Status.Status != constants.AIMStatusReady {
				continue
			}
			if modelCache.Status.PersistentVolumeClaim == "" {
				continue
			}

			// Find the model name from the model cache spec
			modelName := modelCache.Spec.SourceURI
			addModelCacheMount(isvc, container, &modelCache, modelName)
		}
	} else if obs.pvc.Value != nil {
		// Mount service PVC for downloads
		addServicePVCMount(isvc, container, obs.pvc.Value.Name)
	}
}

// addServicePVCMount adds a service PVC volume mount.
func addServicePVCMount(isvc *servingv1beta1.InferenceService, container *corev1.Container, pvcName string) {
	isvc.Spec.Predictor.Volumes = append(isvc.Spec.Predictor.Volumes, corev1.Volume{
		Name: constants.VolumeModelStorage,
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: pvcName,
			},
		},
	})

	container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
		Name:      constants.VolumeModelStorage,
		MountPath: constants.AIMCacheBasePath,
	})
}

// addModelCacheMount adds a model cache PVC volume mount.
func addModelCacheMount(isvc *servingv1beta1.InferenceService, container *corev1.Container, modelCache *aimv1alpha1.AIMModelCache, modelName string) {
	// Sanitize volume name
	volumeName := utils.MakeRFC1123Compliant(modelCache.Name)
	volumeName = strings.ReplaceAll(volumeName, ".", "-")

	isvc.Spec.Predictor.Volumes = append(isvc.Spec.Predictor.Volumes, corev1.Volume{
		Name: volumeName,
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: modelCache.Status.PersistentVolumeClaim,
			},
		},
	})

	// Sanitize model name to prevent path traversal (remove ".." and other unsafe sequences)
	// and ensure it's a valid path component
	safeModelName := filepath.Base(strings.ReplaceAll(modelName, "..", ""))
	if safeModelName == "" || safeModelName == "." {
		safeModelName = volumeName // Fall back to volume name if model name is invalid
	}
	mountPath := filepath.Join(constants.AIMCacheBasePath, safeModelName)

	container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
		Name:      volumeName,
		MountPath: mountPath,
	})
}

// addGPUNodeAffinity adds node affinity rules for GPU selection to the InferenceService.
// Uses device ID-based matching which is more reliable than product name labels.
func addGPUNodeAffinity(isvc *servingv1beta1.InferenceService, gpuModel string) {
	if gpuModel == "" {
		return
	}

	// Normalize and get all device IDs for this GPU model
	deviceIDs := utils.GetAMDDeviceIDsForModel(gpuModel)
	if len(deviceIDs) == 0 {
		// Unknown GPU model, skip affinity (will schedule on any GPU node)
		return
	}

	// Create the node selector requirement using device ID label
	requirement := corev1.NodeSelectorRequirement{
		Key:      utils.LabelAMDGPUDeviceID,
		Operator: corev1.NodeSelectorOpIn,
		Values:   deviceIDs,
	}

	// Ensure Affinity exists
	if isvc.Spec.Predictor.Affinity == nil {
		isvc.Spec.Predictor.Affinity = &corev1.Affinity{}
	}
	if isvc.Spec.Predictor.Affinity.NodeAffinity == nil {
		isvc.Spec.Predictor.Affinity.NodeAffinity = &corev1.NodeAffinity{}
	}

	// Add required node selector terms
	nodeAffinity := isvc.Spec.Predictor.Affinity.NodeAffinity
	if nodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		nodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution = &corev1.NodeSelector{
			NodeSelectorTerms: []corev1.NodeSelectorTerm{},
		}
	}

	// Add or update the node selector term
	terms := nodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
	if len(terms) == 0 {
		terms = append(terms, corev1.NodeSelectorTerm{
			MatchExpressions: []corev1.NodeSelectorRequirement{requirement},
		})
	} else {
		// Add to existing term
		terms[0].MatchExpressions = append(terms[0].MatchExpressions, requirement)
	}
	nodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms = terms
}
