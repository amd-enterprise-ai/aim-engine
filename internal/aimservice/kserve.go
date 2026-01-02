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
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

const (
	// DefaultGPUResourceName is the default resource name for AMD GPUs
	DefaultGPUResourceName = "amd.com/gpu"
	// DefaultSharedMemorySize is the default size for /dev/shm
	DefaultSharedMemorySize = "8Gi"
	// AIMCacheBasePath is the base directory for cached models
	AIMCacheBasePath = "/workspace/model-cache"
)

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
		"app.kubernetes.io/name":       "aim-inference-service",
		"app.kubernetes.io/component":  "inference",
		"app.kubernetes.io/managed-by": constants.LabelValueManagedBy,
		constants.LabelTemplate:        templateLabelValue,
		constants.LabelService:         serviceLabelValue,
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
	envVars := buildMergedEnvVars(templateSpec, templateStatus, obs)

	// Determine image from the resolved model
	image := ""
	if obs.model.Value != nil {
		image = obs.model.Value.Spec.Image
	} else if obs.clusterModel.Value != nil {
		image = obs.clusterModel.Value.Spec.Image
	}

	// Get GPU count from template status
	gpuCount := int64(0)
	if templateStatus != nil && templateStatus.Profile != nil {
		gpuCount = int64(templateStatus.Profile.Metadata.GPUCount)
	}

	// GPU resource name is always the default AMD GPU resource
	gpuResourceName := corev1.ResourceName(DefaultGPUResourceName)

	// Build resource requirements
	resources := resolveResources(service, templateSpec, gpuCount, gpuResourceName)

	// Build shared memory volume
	dshmSizeLimit := resource.MustParse(DefaultSharedMemorySize)

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
							Name:      "kserve-container",
							Image:     image,
							Env:       envVars,
							Resources: resources,
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: 8000,
									Name:          "http",
									Protocol:      corev1.ProtocolTCP,
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "dshm",
									MountPath: "/dev/shm",
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "dshm",
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
// 1. Profile EnvVars (from template status)
// 2. Template.Spec.Env
// 3. Runtime config env vars
// 4. System defaults
func buildMergedEnvVars(
	templateSpec *aimv1alpha1.AIMServiceTemplateSpec,
	templateStatus *aimv1alpha1.AIMServiceTemplateStatus,
	obs ServiceObservation,
) []corev1.EnvVar {
	// Start with system defaults
	envVars := []corev1.EnvVar{
		{Name: "AIM_CACHE_PATH", Value: AIMCacheBasePath},
		{Name: "VLLM_ENABLE_METRICS", Value: "true"},
	}

	// Merge runtime config env vars
	if obs.mergedRuntimeConfig.Value != nil && len(obs.mergedRuntimeConfig.Value.Env) > 0 {
		envVars = utils.MergeEnvVars(envVars, obs.mergedRuntimeConfig.Value.Env)
	}

	// Add metric if set on template
	if templateSpec != nil && templateSpec.Metric != nil {
		envVars = append(envVars, corev1.EnvVar{Name: "AIM_METRIC", Value: string(*templateSpec.Metric)})
	}

	// Add precision if set on template
	if templateSpec != nil && templateSpec.Precision != nil {
		envVars = append(envVars, corev1.EnvVar{Name: "AIM_PRECISION", Value: string(*templateSpec.Precision)})
	}

	// Merge template spec env vars
	if templateSpec != nil && len(templateSpec.Env) > 0 {
		envVars = utils.MergeEnvVars(envVars, templateSpec.Env)
	}

	// Merge profile env vars (highest precedence)
	if templateStatus != nil && templateStatus.Profile != nil && len(templateStatus.Profile.EnvVars) > 0 {
		profileEnvVars := make([]corev1.EnvVar, 0, len(templateStatus.Profile.EnvVars))
		for name, value := range templateStatus.Profile.EnvVars {
			profileEnvVars = append(profileEnvVars, corev1.EnvVar{Name: name, Value: value})
		}
		envVars = utils.MergeEnvVars(envVars, profileEnvVars)
	}

	// Sort for deterministic ordering
	sort.Slice(envVars, func(i, j int) bool {
		return envVars[i].Name < envVars[j].Name
	})

	return envVars
}

// resolveResources builds resource requirements for the inference container.
func resolveResources(
	service *aimv1alpha1.AIMService,
	templateSpec *aimv1alpha1.AIMServiceTemplateSpec,
	gpuCount int64,
	gpuResourceName corev1.ResourceName,
) corev1.ResourceRequirements {
	// Start with defaults based on GPU count
	resources := defaultResourceRequirementsForGPU(gpuCount)

	// Override with template spec resources
	if templateSpec != nil && templateSpec.Resources != nil {
		resources = mergeResourceRequirements(resources, templateSpec.Resources)
	}

	// Override with service spec resources
	if service.Spec.Resources != nil {
		resources = mergeResourceRequirements(resources, service.Spec.Resources)
	}

	// Ensure GPU resources are set
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

// configureReplicasAndAutoscaling sets up replica counts.
func configureReplicasAndAutoscaling(isvc *servingv1beta1.InferenceService, service *aimv1alpha1.AIMService) {
	// Disable HPA by default
	disableHPA(isvc)

	if service.Spec.Replicas != nil {
		// Use the specified replica count
		isvc.Spec.Predictor.MinReplicas = service.Spec.Replicas
		isvc.Spec.Predictor.MaxReplicas = *service.Spec.Replicas
	} else {
		// Default: 1 replica
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
	if _, exists := isvc.Annotations["serving.kserve.io/autoscalerClass"]; !exists {
		isvc.Annotations["serving.kserve.io/autoscalerClass"] = "none"
	}
}

// addStorageVolumes adds cache or PVC volumes to the InferenceService.
func addStorageVolumes(isvc *servingv1beta1.InferenceService, obs ServiceObservation) {
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
	volumeName := "model-storage"

	isvc.Spec.Predictor.Volumes = append(isvc.Spec.Predictor.Volumes, corev1.Volume{
		Name: volumeName,
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: pvcName,
			},
		},
	})

	container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
		Name:      volumeName,
		MountPath: AIMCacheBasePath,
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

	// Mount at cache path + model name
	mountPath := filepath.Join(AIMCacheBasePath, modelName)

	container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
		Name:      volumeName,
		MountPath: mountPath,
	})
}
