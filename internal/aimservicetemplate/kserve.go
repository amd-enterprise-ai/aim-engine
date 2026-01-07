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

package aimservicetemplate

import (
	"regexp"
	"strings"

	servingv1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/utils/ptr"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

const (
	// DefaultGPUResourceName is the default resource name for AMD GPUs in Kubernetes.
	DefaultGPUResourceName = "amd.com/gpu"

	// DefaultSharedMemorySize is the default size allocated for /dev/shm in inference containers.
	// This is required for efficient inter-process communication in model serving workloads.
	DefaultSharedMemorySize = "8Gi"

	// KubernetesLabelValueMaxLength is the maximum length for a Kubernetes label value.
	KubernetesLabelValueMaxLength = 63
)

var labelValueRegex = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// SanitizeLabelValue converts a string to a valid Kubernetes label value.
// Valid label values must:
// - Be empty or consist of alphanumeric characters, '-', '_' or '.'
// - Start and end with an alphanumeric character
// - Be at most 63 characters
// Returns "unknown" if the sanitized value is empty.
func SanitizeLabelValue(s string) string {
	// Replace invalid characters with underscores
	sanitized := labelValueRegex.ReplaceAllString(s, "_")

	// Trim leading and trailing non-alphanumeric characters
	sanitized = strings.TrimLeft(sanitized, "_.-")
	sanitized = strings.TrimRight(sanitized, "_.-")

	// Truncate to maximum label value length
	if len(sanitized) > KubernetesLabelValueMaxLength {
		sanitized = sanitized[:KubernetesLabelValueMaxLength]
		// Trim trailing non-alphanumeric after truncation
		sanitized = strings.TrimRight(sanitized, "_.-")
	}

	// Return "unknown" if fully sanitized string is empty
	if sanitized == "" {
		return "unknown"
	}

	return sanitized
}

// BuildServingRuntime creates a KServe ServingRuntime for a namespace-scoped template.
func BuildServingRuntime(
	template *aimv1alpha1.AIMServiceTemplate,
	model *aimv1alpha1.AIMModel,
	runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon,
) *servingv1alpha1.ServingRuntime {
	runtime := &servingv1alpha1.ServingRuntime{
		TypeMeta: metav1.TypeMeta{
			APIVersion: servingv1alpha1.SchemeGroupVersion.String(),
			Kind:       "ServingRuntime",
		},
		ObjectMeta: buildServingRuntimeObjectMeta(template.Name, &template.Namespace, template.Spec.ModelName),
		Spec:       buildServingRuntimeSpec(template.Spec.AIMServiceTemplateSpecCommon, model.Spec.Image, model.Spec.ImagePullSecrets, &template.Status),
	}

	return runtime
}

// BuildClusterServingRuntime creates a KServe ClusterServingRuntime for a cluster-scoped template.
func BuildClusterServingRuntime(
	template *aimv1alpha1.AIMClusterServiceTemplate,
	clusterModel *aimv1alpha1.AIMClusterModel,
	runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon,
) *servingv1alpha1.ClusterServingRuntime {
	runtime := &servingv1alpha1.ClusterServingRuntime{
		TypeMeta: metav1.TypeMeta{
			APIVersion: servingv1alpha1.SchemeGroupVersion.String(),
			Kind:       "ClusterServingRuntime",
		},
		ObjectMeta: buildServingRuntimeObjectMeta(template.Name, nil, template.Spec.ModelName),
		Spec:       buildServingRuntimeSpec(template.Spec.AIMServiceTemplateSpecCommon, clusterModel.Spec.Image, clusterModel.Spec.ImagePullSecrets, &template.Status),
	}

	return runtime
}

func buildServingRuntimeObjectMeta(name string, namespace *string, modelName string) metav1.ObjectMeta {
	meta := metav1.ObjectMeta{
		Name: name,
		Labels: map[string]string{
			"app.kubernetes.io/name":       "aim-runtime",
			"app.kubernetes.io/component":  "serving-runtime",
			"app.kubernetes.io/managed-by": constants.LabelValueManagedByController,
			constants.LabelKeyModel:        SanitizeLabelValue(modelName),
		},
	}

	if namespace != nil {
		meta.Namespace = *namespace
	}

	return meta
}

func buildServingRuntimeSpec(
	spec aimv1alpha1.AIMServiceTemplateSpecCommon,
	image string,
	imagePullSecrets []corev1.LocalObjectReference,
	status *aimv1alpha1.AIMServiceTemplateStatus,
) servingv1alpha1.ServingRuntimeSpec {
	dshmSizeLimit := resource.MustParse(DefaultSharedMemorySize)

	// Get GPU resource name and count from template
	gpuResourceName := getGPUResourceName(spec)
	gpuCount := getGPUCount(status)

	runtimeSpec := servingv1alpha1.ServingRuntimeSpec{
		// The AIM containers handle downloading themselves
		StorageHelper: &servingv1alpha1.StorageHelper{
			Disabled: true,
		},
		SupportedModelFormats: []servingv1alpha1.SupportedModelFormat{
			{
				Name:    "aim",
				Version: ptr.To("1"),
			},
		},
		ServingRuntimePodSpec: servingv1alpha1.ServingRuntimePodSpec{
			ImagePullSecrets: copyPullSecrets(imagePullSecrets),
			Containers: []corev1.Container{
				{
					Name:  "kserve-container",
					Image: image,
					Env: []corev1.EnvVar{
						{Name: "VLLM_ENABLE_METRICS", Value: "true"},
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							gpuResourceName: *resource.NewQuantity(gpuCount, resource.DecimalSI),
						},
						Limits: corev1.ResourceList{
							gpuResourceName: *resource.NewQuantity(gpuCount, resource.DecimalSI),
						},
					},
					Ports: []corev1.ContainerPort{
						{
							ContainerPort: 8000,
							Name:          "http",
							Protocol:      corev1.ProtocolTCP,
						},
					},
					VolumeMounts: []corev1.VolumeMount{
						{Name: "dshm", MountPath: "/dev/shm"},
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
	}

	return runtimeSpec
}

// getGPUResourceName returns the GPU resource name for the template.
// If the ResourceName is specified in gpuSelector, it will be used.
// Otherwise, the default value of "amd.com/gpu" is returned.
func getGPUResourceName(spec aimv1alpha1.AIMServiceTemplateSpecCommon) corev1.ResourceName {
	if spec.GpuSelector != nil && spec.GpuSelector.ResourceName != "" {
		return corev1.ResourceName(spec.GpuSelector.ResourceName)
	}
	return corev1.ResourceName(DefaultGPUResourceName)
}

// getGPUCount returns the GPU count from the template status profile.
func getGPUCount(status *aimv1alpha1.AIMServiceTemplateStatus) int64 {
	if status == nil || status.Profile == nil {
		return 1 // Default to 1 GPU if no profile
	}
	if status.Profile.Metadata.GPUCount <= 0 {
		return 1
	}
	return int64(status.Profile.Metadata.GPUCount)
}

// copyPullSecrets creates a deep copy of image pull secrets.
func copyPullSecrets(secrets []corev1.LocalObjectReference) []corev1.LocalObjectReference {
	if len(secrets) == 0 {
		return nil
	}
	result := make([]corev1.LocalObjectReference, len(secrets))
	copy(result, secrets)
	return result
}
