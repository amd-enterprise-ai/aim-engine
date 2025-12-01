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

package aimservice

import (
	"context"
	"fmt"

	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

const (
	// AIMCacheBasePath is the base directory where AIM expects to find cached models
	AIMCacheBasePath = "/workspace/model-cache"
)

func pvcNameForService(service *aimv1alpha1.AIMService) string {
	name, _ := utils.GenerateDerivedName([]string{"aimsvc", "temp", service.Name}, service.UID)
	return name
}

// ============================================================================
// FETCH
// ============================================================================

type ServicePVCFetchResult struct {
	// TemporaryServicePVC is the PVC that is used if caching is not used
	TemporaryServicePVC *corev1.PersistentVolumeClaim
}

func fetchServicePVCResult(ctx context.Context, c client.Client, service *aimv1alpha1.AIMService) (ServicePVCFetchResult, error) {
	result := ServicePVCFetchResult{}

	pvc := &corev1.PersistentVolumeClaim{}
	key := client.ObjectKey{Name: pvcNameForService(service), Namespace: service.Namespace}
	if err := c.Get(ctx, key, pvc); err != nil && !errors.IsNotFound(err) {
		return result, fmt.Errorf("failed to fetch service PVC: %w", err)
	} else if err == nil {
		result.TemporaryServicePVC = pvc
	}

	return result, nil
}

// ============================================================================
// OBSERVE
// ============================================================================

type ServicePVCObservation struct {
	PVCName         string
	PVCExists       bool
	PVCReady        bool
	ShouldCreatePVC bool
	ShouldUsePVC    bool
}

func observeServicePVC(
	result ServicePVCFetchResult,
	service *aimv1alpha1.AIMService,
	cachingObs serviceCachingObservation,
) ServicePVCObservation {
	obs := ServicePVCObservation{}

	// Determine if PVC should be used:
	// - If template cache exists (ready or not), use cache instead of PVC
	// - If caching mode is Never, use PVC
	// - If caching mode is Always, wait for cache (no PVC)
	// - If caching mode is Auto and no cache exists, use PVC

	templateCacheExists := cachingObs.templateCache != nil
	cachingMode := service.Spec.GetCachingMode()

	// Use PVC when no template cache exists and caching is not Always
	obs.ShouldUsePVC = !templateCacheExists && cachingMode != aimv1alpha1.CachingModeAlways

	if result.TemporaryServicePVC != nil {
		obs.PVCName = result.TemporaryServicePVC.Name
		obs.PVCExists = true
		// Check PVC bound status
		obs.PVCReady = result.TemporaryServicePVC.Status.Phase == corev1.ClaimBound
	} else if obs.ShouldUsePVC {
		// PVC needed but doesn't exist
		obs.PVCName = pvcNameForService(service)
		obs.ShouldCreatePVC = true
	}

	return obs
}

// ============================================================================
// PLAN
// ============================================================================

func planServicePVC(
	service *aimv1alpha1.AIMService,
	obs ServicePVCObservation,
	templateStatus *aimv1alpha1.AIMServiceTemplateStatus,
	mergedConfig *aimv1alpha1.AIMRuntimeConfigCommon,
) (client.Object, error) {
	if !obs.ShouldCreatePVC {
		return nil, nil
	}

	pvcName := pvcNameForService(service)

	// Extract storage config from mergedConfig
	storageClassName := utils.ResolveStorageClass("", mergedConfig)
	headroomPercent := utils.DefaultPVCHeadroomPercent
	if mergedConfig != nil && mergedConfig.Storage != nil && mergedConfig.Storage.PVCHeadroomPercent != nil {
		headroomPercent = *mergedConfig.Storage.PVCHeadroomPercent
	}

	// Calculate required size from model sources
	size, err := calculateRequiredStorageSize(templateStatus, headroomPercent)
	if err != nil {
		return nil, fmt.Errorf("cannot determine storage size: %w", err)
	}

	var sc *string
	if storageClassName != "" {
		sc = &storageClassName
	}

	serviceNameLabel, _ := utils.SanitizeLabelValue(service.Name)

	return &corev1.PersistentVolumeClaim{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "PersistentVolumeClaim",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Namespace: service.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "aim-service-controller",
				"app.kubernetes.io/component":  "model-storage",
				constants.LabelKeyServiceName:  serviceNameLabel,
				constants.LabelKeyCacheType:    constants.LabelValueCacheTypeTempService,
			},
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
	}, nil
}

// calculateRequiredStorageSize computes the total storage needed for model sources.
// Returns sum of all model sizes plus the specified headroom percentage, or an error if sizes aren't specified.
// headroomPercent represents the percentage (0-100) of extra space to add. For example, 10 means 10% extra.
func calculateRequiredStorageSize(templateStatus *aimv1alpha1.AIMServiceTemplateStatus, headroomPercent int32) (resource.Quantity, error) {
	if templateStatus == nil || len(templateStatus.ModelSources) == 0 {
		return resource.Quantity{}, fmt.Errorf("no model sources available in template")
	}

	var totalBytes int64
	for _, modelSource := range templateStatus.ModelSources {
		if modelSource.Size.IsZero() {
			return resource.Quantity{}, fmt.Errorf("model source %q has no size specified", modelSource.Name)
		}
		totalBytes += modelSource.Size.Value()
	}

	if totalBytes == 0 {
		return resource.Quantity{}, fmt.Errorf("total model size is zero")
	}

	// Apply headroom and round to nearest Gi using shared utility
	return utils.QuantityWithHeadroom(totalBytes, headroomPercent), nil
}

// ============================================================================
// PROJECT
// ============================================================================

//nolint:unparam // bool return kept for API consistency with other project functions
func projectServicePVC(
	_ *aimv1alpha1.AIMServiceStatus,
	cm *controllerutils.ConditionManager,
	h *controllerutils.StatusHelper,
	obs ServicePVCObservation,
) bool {
	if !obs.ShouldUsePVC {
		// PVC not needed (using cache instead)
		return false
	}

	if obs.ShouldCreatePVC {
		h.Progressing(aimv1alpha1.AIMServiceReasonCreatingPVC, "Creating service PVC for model storage")
		cm.MarkFalse(aimv1alpha1.AIMServiceConditionStorageReady, aimv1alpha1.AIMServiceReasonCreatingPVC, "PVC being created", controllerutils.LevelNormal)
		return false
	}

	if obs.PVCExists && !obs.PVCReady {
		h.Progressing(aimv1alpha1.AIMServiceReasonPVCNotBound, "Waiting for PVC to be bound")
		cm.MarkFalse(aimv1alpha1.AIMServiceConditionStorageReady, aimv1alpha1.AIMServiceReasonPVCNotBound, "PVC is not bound yet", controllerutils.LevelNormal)
		return false
	}

	if obs.PVCReady {
		cm.MarkTrue(aimv1alpha1.AIMServiceConditionStorageReady, aimv1alpha1.AIMServiceReasonStorageReady, "PVC is ready", controllerutils.LevelNormal)
	}

	return false
}

// ============================================================================
// VOLUME MOUNT HELPERS
// ============================================================================

// addServicePVCMount adds a service PVC volume mount to an inferenceService.
func addServicePVCMount(inferenceService *servingv1beta1.InferenceService, pvcName string) {
	volumeName := "model-storage"

	// Add the PVC volume
	inferenceService.Spec.Predictor.Volumes = append(inferenceService.Spec.Predictor.Volumes, corev1.Volume{
		Name: volumeName,
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: pvcName,
			},
		},
	})

	// Mount the volume in the kserve-container
	inferenceService.Spec.Predictor.Model.VolumeMounts = append(inferenceService.Spec.Predictor.Model.VolumeMounts, corev1.VolumeMount{
		Name:      volumeName,
		MountPath: AIMCacheBasePath,
	})
}
