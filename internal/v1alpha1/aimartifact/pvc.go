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

package aimartifact

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	v1alpha1utils "github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/utils"
)

func GenerateCachePvcName(mc *aimv1alpha1.AIMArtifact) string {
	name, _ := utils.GenerateDerivedName([]string{mc.Name, "cache"}, utils.WithHashSource(mc.UID))
	return name
}

// DefaultAdapterDiskSize is the built-in default size for a model artifact's
// shared adapter disk PVC when not otherwise specified.
var DefaultAdapterDiskSize = resource.MustParse("50Gi")

// GenerateAdapterPvcName returns the deterministic name of the shared adapter disk
// PVC owned by a type=model artifact. UID-hashed (like the cache PVC) so that
// delete-then-recreate of the parent never collides on a stale PVC.
func GenerateAdapterPvcName(mc *aimv1alpha1.AIMArtifact) string {
	name, _ := utils.GenerateDerivedName([]string{mc.Name, "adapters"}, utils.WithHashSource(mc.UID))
	return name
}

// buildAdapterPvc builds the shared ReadWriteMany adapter disk PVC for a model
// artifact. It is owned by the model artifact (cascade-deleted with it) and shared
// by every AIMService that serves adapters on this base model.
func buildAdapterPvc(mc *aimv1alpha1.AIMArtifact, storageClassName string, size resource.Quantity) *corev1.PersistentVolumeClaim {
	var sc *string
	if storageClassName != "" {
		sc = &storageClassName
	}

	labels := map[string]string{
		constants.LabelKeyCacheName: mc.Name,
	}

	return &corev1.PersistentVolumeClaim{
		TypeMeta: metav1.TypeMeta{APIVersion: corev1.SchemeGroupVersion.String(), Kind: "PersistentVolumeClaim"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      GenerateAdapterPvcName(mc),
			Namespace: mc.Namespace,
			Labels:    labels,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: size,
				},
			},
			StorageClassName: sc,
		},
	}
}

// adapterDiskStorageClass resolves the RWX storage class for the adapter disk
// (see ResolveAdapterDiskStorageClass for precedence).
func adapterDiskStorageClass(mc *aimv1alpha1.AIMArtifact, runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon) string {
	explicit := ""
	if mc.Spec.AdapterDisk != nil {
		explicit = mc.Spec.AdapterDisk.StorageClassName
	}
	return v1alpha1utils.ResolveAdapterDiskStorageClass(explicit, runtimeConfig)
}

// adapterDiskSize resolves the adapter disk PVC size: the artifact's own
// adapterDisk.size wins, then the runtime config's Storage.AdapterDiskSize
// cluster default, then the built-in DefaultAdapterDiskSize.
func adapterDiskSize(mc *aimv1alpha1.AIMArtifact, runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon) resource.Quantity {
	if mc.Spec.AdapterDisk != nil && !mc.Spec.AdapterDisk.Size.IsZero() {
		return mc.Spec.AdapterDisk.Size
	}
	if runtimeConfig != nil && runtimeConfig.Storage != nil && runtimeConfig.Storage.AdapterDiskSize != nil && !runtimeConfig.Storage.AdapterDiskSize.IsZero() {
		return *runtimeConfig.Storage.AdapterDiskSize
	}
	return DefaultAdapterDiskSize
}

func buildCachePvc(mc *aimv1alpha1.AIMArtifact, pvcSize resource.Quantity, storageClassName string) *corev1.PersistentVolumeClaim {
	// Storage class: empty string means use cluster default
	var sc *string
	if storageClassName != "" {
		sc = &storageClassName
	}

	// Determine cache type based on whether this was created by a template cache
	cacheType := constants.LabelValueCacheTypeTemplateCache
	if mc.Labels == nil || mc.Labels["template-created"] != "true" {
		cacheType = "" // Standalone artifact (not template or service cache)
	}

	// Build labels with type and source information
	labels := map[string]string{}
	labels[constants.LabelKeyCacheName] = mc.Name

	// Add cache type if it's a template cache
	if cacheType != "" {
		labels[constants.LabelKeyCacheType] = cacheType
	}

	// Extract model name from sourceURI (e.g., "hf://amd/Llama-3.1-8B" → "amd/Llama-3.1-8B")
	if mc.Spec.SourceURI != "" {
		if modelName := extractModelFromSourceURI(mc.Spec.SourceURI); modelName != "" {
			labels[constants.LabelKeySourceModel], _ = utils.SanitizeLabelValue(modelName)
		}
	}

	return &corev1.PersistentVolumeClaim{
		TypeMeta: metav1.TypeMeta{APIVersion: corev1.SchemeGroupVersion.String(), Kind: "PersistentVolumeClaim"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      GenerateCachePvcName(mc),
			Namespace: mc.Namespace,
			Labels:    labels,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: pvcSize,
				},
			},
			StorageClassName: sc,
		},
	}
}

// extractModelFromSourceURI extracts the model name from a sourceURI.
// Examples:
//   - "hf://amd/Llama-3.1-8B-Instruct" → "amd/Llama-3.1-8B-Instruct"
//   - "s3://bucket/model-v1" → "bucket/model-v1"
func extractModelFromSourceURI(sourceURI string) string {
	// Remove the scheme prefix (hf://, s3://, etc.)
	if idx := strings.Index(sourceURI, "://"); idx != -1 {
		return sourceURI[idx+3:]
	}
	return sourceURI
}
