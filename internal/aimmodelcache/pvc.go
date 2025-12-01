/*
MIT License

Copyright (c) 2025 Advanced Micro Devices, Inc.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package aimmodelcache

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

// ============================================================================
// FETCH
// ============================================================================

type pvcFetchResult struct {
	PVC   *corev1.PersistentVolumeClaim
	error error
}

func fetchPVC(ctx context.Context, c client.Client, cache *aimv1alpha1.AIMModelCache) (pvcFetchResult, error) {
	result := pvcFetchResult{}

	pvcName := pvcNameForCache(cache)
	var pvc corev1.PersistentVolumeClaim
	err := c.Get(ctx, types.NamespacedName{Namespace: cache.Namespace, Name: pvcName}, &pvc)

	if err != nil && client.IgnoreNotFound(err) != nil {
		return result, err
	}

	result.PVC = &pvc
	result.error = err
	return result, nil
}

type storageClassFetchResult struct {
	storageClass *storagev1.StorageClass
	error        error
}

func fetchStorageClass(ctx context.Context, c client.Client, pvc *corev1.PersistentVolumeClaim) (storageClassFetchResult, error) {
	result := storageClassFetchResult{}

	if pvc.Spec.StorageClassName == nil || *pvc.Spec.StorageClassName == "" {
		return result, nil
	}

	var sc storagev1.StorageClass
	err := c.Get(ctx, types.NamespacedName{Name: *pvc.Spec.StorageClassName}, &sc)

	if err != nil && client.IgnoreNotFound(err) != nil {
		return result, err
	}

	result.storageClass = &sc
	result.error = err
	return result, nil
}

// ============================================================================
// OBSERVE
// ============================================================================

// pvcObservation contains information about the PersistentVolumeClaim.
type pvcObservation struct {
	found bool
	pvc   *corev1.PersistentVolumeClaim
	bound bool
	ready bool // Phase == Bound
	lost  bool // Phase == Lost
}

func observePVC(result pvcFetchResult) pvcObservation {
	obs := pvcObservation{}

	if result.error != nil {
		obs.found = false
		return obs
	}

	obs.found = true
	obs.pvc = result.PVC
	obs.bound = result.PVC.Status.Phase == corev1.ClaimBound
	obs.ready = result.PVC.Status.Phase == corev1.ClaimBound
	obs.lost = result.PVC.Status.Phase == corev1.ClaimLost

	return obs
}

// storageClassObservation contains information about the StorageClass binding mode.
type storageClassObservation struct {
	found                bool
	storageClass         *storagev1.StorageClass
	waitForFirstConsumer bool
}

func observeStorageClass(result storageClassFetchResult) storageClassObservation {
	obs := storageClassObservation{}

	if result.error != nil {
		obs.found = false
		return obs
	}

	obs.found = true
	obs.storageClass = result.storageClass

	if result.storageClass.VolumeBindingMode != nil {
		obs.waitForFirstConsumer = *result.storageClass.VolumeBindingMode == storagev1.VolumeBindingWaitForFirstConsumer
	}

	return obs
}

// ============================================================================
// PLAN
// ============================================================================

func planPVC(cache *aimv1alpha1.AIMModelCache, obs Observation, scheme *runtime.Scheme) client.Object {
	// Only create PVC if it doesn't exist yet
	// Once created, PVCs are immutable - we never modify them to avoid:
	// 1. StorageClassName mutation errors (forbidden by Kubernetes)
	// 2. Storage size shrinkage errors (forbidden by Kubernetes)
	// 3. Unexpected PVC expansion from runtime config changes
	if obs.pvc.found {
		return nil
	}

	pvc := buildPVC(cache, obs)
	if err := controllerutil.SetControllerReference(cache, pvc, scheme); err != nil {
		return nil
	}
	return pvc
}

// buildPVC creates a PersistentVolumeClaim for the model cache.
func buildPVC(cache *aimv1alpha1.AIMModelCache, obs Observation) *corev1.PersistentVolumeClaim {
	// Handle nil MergedConfig (e.g., default config not found)
	var headroomPercent *int32
	if obs.runtimeConfig.MergedConfig != nil && obs.runtimeConfig.MergedConfig.Storage != nil {
		headroomPercent = obs.runtimeConfig.MergedConfig.Storage.PVCHeadroomPercent
	}
	if headroomPercent == nil {
		headroomPercent = ptr.To(utils.DefaultPVCHeadroomPercent)
	}

	storageClassName := utils.ResolveStorageClass(cache.Spec.StorageClassName, obs.runtimeConfig.MergedConfig)
	pvcSize := utils.QuantityWithHeadroom(cache.Spec.Size.Value(), *headroomPercent)

	// Storage class: empty string means use cluster default
	var sc *string
	if storageClassName != "" {
		sc = &storageClassName
	}

	// Determine cache type based on whether this was created by a template cache
	cacheType := constants.LabelValueCacheTypeModelCache
	if cache.Labels == nil || cache.Labels["template-created"] != "true" {
		cacheType = "" // Standalone model cache (not template or service cache)
	}

	// Build labels with type and source information
	labels := map[string]string{
		"app.kubernetes.io/managed-by": "modelcache-controller",
		constants.LabelKeyModelCache:   cache.Name,
	}

	// Add cache type if it's a template cache
	if cacheType != "" {
		labels[constants.LabelKeyCacheType] = cacheType
	}

	// Extract model name from sourceURI (e.g., "hf://amd/Llama-3.1-8B" → "amd/Llama-3.1-8B")
	if cache.Spec.SourceURI != "" {
		if modelName := extractModelFromSourceURI(cache.Spec.SourceURI); modelName != "" {
			labelValue, _ := utils.SanitizeLabelValue(modelName)
			labels[constants.LabelKeySourceModel] = labelValue
		}
	}

	pvcName := pvcNameForCache(cache)

	return &corev1.PersistentVolumeClaim{
		TypeMeta: metav1.TypeMeta{APIVersion: corev1.SchemeGroupVersion.String(), Kind: "PersistentVolumeClaim"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Namespace: cache.Namespace,
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

// ============================================================================
// PROJECT
// ============================================================================

// projectPVC updates the PVC reference in status.
func projectPVC(status *aimv1alpha1.AIMModelCacheStatus, obs Observation) {
	if obs.pvc.found {
		status.PersistentVolumeClaim = obs.pvc.pvc.Name
	}
}

// projectStorageReadyCondition sets the StorageReady condition.
func projectStorageReadyCondition(cm *controllerutils.ConditionManager, obs Observation) {
	switch {
	case !obs.pvc.found:
		cm.Set(aimv1alpha1.AIMModelCacheConditionStorageReady, metav1.ConditionFalse,
			aimv1alpha1.AIMModelCacheReasonPVCPending, "PVC not created yet", controllerutils.AsInfo())
	case obs.pvc.pvc.Status.Phase == corev1.ClaimBound:
		cm.Set(aimv1alpha1.AIMModelCacheConditionStorageReady, metav1.ConditionTrue,
			aimv1alpha1.AIMModelCacheReasonPVCBound, "", controllerutils.AsInfo())
	case obs.pvc.pvc.Status.Phase == corev1.ClaimPending:
		cm.Set(aimv1alpha1.AIMModelCacheConditionStorageReady, metav1.ConditionFalse,
			aimv1alpha1.AIMModelCacheReasonPVCProvisioning, "PVC is provisioning", controllerutils.AsInfo())
	case obs.pvc.pvc.Status.Phase == corev1.ClaimLost:
		cm.Set(aimv1alpha1.AIMModelCacheConditionStorageReady, metav1.ConditionFalse,
			aimv1alpha1.AIMModelCacheReasonPVCLost, "PVC lost", controllerutils.AsWarning())
	default:
		cm.Set(aimv1alpha1.AIMModelCacheConditionStorageReady, metav1.ConditionUnknown,
			string(obs.pvc.pvc.Status.Phase), "", controllerutils.AsWarning())
	}
}
