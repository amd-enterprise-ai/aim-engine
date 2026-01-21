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
)

const (
	// DefaultPVCHeadroomPercent is the default headroom percentage for PVC sizing
	DefaultPVCHeadroomPercent = 10
)

// =======================================================
// TEMPORARY SERVICE PVC WHEN CACHING IS NOT USED
// =======================================================

// GenerateServicePVCName creates a deterministic name for the service's temporary PVC.
func GenerateServicePVCName(serviceName, namespace string) (string, error) {
	return utils.GenerateDerivedName([]string{serviceName, "temp-cache"}, utils.WithHashSource(namespace))
}

// planServicePVC creates a PVC for the service if no template cache is available.
// Returns nil if PVC creation is not needed or prerequisites are not met.
func planServicePVC(
	service *aimv1alpha1.AIMService,
	templateName string,
	templateStatus *aimv1alpha1.AIMServiceTemplateStatus,
	obs ServiceObservation,
) client.Object {
	cachingMode := service.Spec.GetCachingMode()

	// If caching is required (Always mode), don't create a temp PVC
	if cachingMode == aimv1alpha1.CachingModeAlways {
		return nil
	}

	// If template cache exists and is ready, don't need PVC
	if obs.templateCache.Value != nil &&
		obs.templateCache.Value.Status.Status == constants.AIMStatusReady {
		return nil
	}

	// If PVC already exists, don't create again
	if obs.pvc.Value != nil {
		return nil
	}

	// Need model sources to calculate size - waiting for template to be ready
	if templateStatus == nil || len(templateStatus.ModelSources) == 0 {
		return nil
	}

	// Calculate required size - if this fails, model sources don't have sizes yet,
	// which means template is still resolving. Return nil to wait for next reconcile.
	headroomPercent := resolvePVCHeadroomPercent(service, obs)
	size, err := calculateRequiredStorageSize(templateStatus.ModelSources, headroomPercent)
	if err != nil {
		// Model sources exist but don't have sizes - template is still resolving
		return nil
	}

	pvcName, err := GenerateServicePVCName(service.Name, service.Namespace)
	if err != nil {
		// Name generation failed - this would be a programming error
		return nil
	}

	storageClassName := resolveStorageClassName(service, obs)
	var sc *string
	if storageClassName != "" {
		sc = &storageClassName
	}

	serviceLabelValue, _ := utils.SanitizeLabelValue(service.Name)
	templateLabelValue, _ := utils.SanitizeLabelValue(templateName)

	pvc := &corev1.PersistentVolumeClaim{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "PersistentVolumeClaim",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Namespace: service.Namespace,
			Labels: map[string]string{
				constants.LabelK8sManagedBy: constants.LabelValueManagedBy,
				constants.LabelK8sComponent: constants.ComponentModelStorage,
				constants.LabelService:      serviceLabelValue,
				constants.LabelCacheType:    constants.LabelValueCacheTypeTemp,
				constants.LabelTemplate:     templateLabelValue,
			},
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

	return pvc
}

// =======================================================
// TEMPLATE CACHE (AUTO-GENERATION WHEN CACHING REQUESTED)
// =======================================================

// GenerateTemplateCacheName creates a deterministic name for a template cache.
func GenerateTemplateCacheName(templateName, namespace string) (string, error) {
	return utils.GenerateDerivedName([]string{templateName}, utils.WithHashSource(namespace))
}

// planTemplateCache creates a template cache if caching mode is Always and one doesn't exist.
// Auto mode uses existing caches but doesn't create new ones.
func planTemplateCache(
	service *aimv1alpha1.AIMService,
	templateName string,
	templateStatus *aimv1alpha1.AIMServiceTemplateStatus,
	obs ServiceObservation,
) client.Object {
	cachingMode := service.Spec.GetCachingMode()

	// Only create cache for Always mode - Auto uses existing but doesn't create
	if cachingMode != aimv1alpha1.CachingModeAlways {
		return nil
	}

	// Don't create if template cache already exists
	if obs.templateCache.Value != nil {
		return nil
	}

	// Need model sources in template status to determine what to cache
	if templateStatus == nil || len(templateStatus.ModelSources) == 0 {
		return nil
	}

	// Resolve storage class
	storageClassName := resolveStorageClassName(service, obs)

	cacheName, err := GenerateTemplateCacheName(templateName, service.Namespace)
	if err != nil {
		// Name generation failed - this would be a programming error
		return nil
	}

	serviceLabelValue, _ := utils.SanitizeLabelValue(service.Name)

	cache := &aimv1alpha1.AIMTemplateCache{
		TypeMeta: metav1.TypeMeta{
			APIVersion: aimv1alpha1.GroupVersion.String(),
			Kind:       "AIMTemplateCache",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      cacheName,
			Namespace: service.Namespace,
			Labels: map[string]string{
				constants.LabelService: serviceLabelValue,
			},
		},
		Spec: aimv1alpha1.AIMTemplateCacheSpec{
			TemplateName:     templateName,
			TemplateScope:    aimv1alpha1.AIMServiceTemplateScopeNamespace, // Default to namespace scope
			StorageClassName: storageClassName,
			RuntimeConfigRef: service.Spec.RuntimeConfigRef,
		},
	}

	return cache
}

// fetchTemplateCache fetches the template cache for the service.
// Uses resolved reference only if the cache is still Ready; otherwise re-searches.
// Returns the best available cache for health/status visibility, even if not Ready.
func fetchTemplateCache(
	ctx context.Context,
	c client.Client,
	service *aimv1alpha1.AIMService,
) controllerutils.FetchResult[*aimv1alpha1.AIMTemplateCache] {
	// Try to use previously resolved cache if Ready
	if result, shouldContinue := tryFetchResolvedTemplateCache(ctx, c, service); !shouldContinue {
		return result
	}

	// Search for best available cache
	return searchTemplateCaches(ctx, c, service)
}

// tryFetchResolvedTemplateCache attempts to fetch a previously resolved template cache reference.
// Returns the result and whether to continue with search.
func tryFetchResolvedTemplateCache(
	ctx context.Context,
	c client.Client,
	service *aimv1alpha1.AIMService,
) (result controllerutils.FetchResult[*aimv1alpha1.AIMTemplateCache], shouldContinue bool) {
	if service.Status.Cache == nil || service.Status.Cache.TemplateCacheRef == nil {
		return result, true
	}

	logger := log.FromContext(ctx)
	ref := service.Status.Cache.TemplateCacheRef
	result = controllerutils.Fetch(ctx, c, ref.NamespacedName(), &aimv1alpha1.AIMTemplateCache{})

	if result.OK() && result.Value.Status.Status == constants.AIMStatusReady {
		logger.V(1).Info("using resolved template cache", "name", ref.Name)
		return result, false
	}

	// Not Ready or deleted - log and continue to search
	if result.OK() {
		logger.V(1).Info("resolved template cache not ready, searching for alternatives",
			"name", ref.Name, "status", result.Value.Status.Status)
	} else if result.IsNotFound() {
		logger.V(1).Info("resolved template cache deleted, searching for alternatives", "name", ref.Name)
	} else {
		return result, false // Real error - stop
	}

	return controllerutils.FetchResult[*aimv1alpha1.AIMTemplateCache]{}, true
}

// searchTemplateCaches lists and selects the best template cache matching the resolved template.
func searchTemplateCaches(
	ctx context.Context,
	c client.Client,
	service *aimv1alpha1.AIMService,
) controllerutils.FetchResult[*aimv1alpha1.AIMTemplateCache] {
	logger := log.FromContext(ctx)

	// Get template name for matching - use resolved template from status if available
	var templateName string
	if service.Status.ResolvedTemplate != nil {
		templateName = service.Status.ResolvedTemplate.Name
	}

	if templateName == "" {
		return controllerutils.FetchResult[*aimv1alpha1.AIMTemplateCache]{}
	}

	// List template caches in the service namespace
	cacheListResult := controllerutils.FetchList(ctx, c, &aimv1alpha1.AIMTemplateCacheList{}, client.InNamespace(service.Namespace))
	if cacheListResult.Error != nil {
		return controllerutils.FetchResult[*aimv1alpha1.AIMTemplateCache]{Error: cacheListResult.Error}
	}

	// Filter caches matching our template
	var matchingCaches []aimv1alpha1.AIMTemplateCache
	for _, cache := range cacheListResult.Value.Items {
		if cache.Spec.TemplateName == templateName {
			matchingCaches = append(matchingCaches, cache)
		}
	}

	if len(matchingCaches) == 0 {
		return controllerutils.FetchResult[*aimv1alpha1.AIMTemplateCache]{}
	}

	// Select the healthiest cache (prioritizes Ready)
	best := utils.SelectBestPtr(matchingCaches, func(cache *aimv1alpha1.AIMTemplateCache) constants.AIMStatus {
		return cache.Status.GetAIMStatus()
	})

	if best != nil {
		logger.V(1).Info("selected template cache", "name", best.Name, "status", best.Status.Status)
	}

	return controllerutils.FetchResult[*aimv1alpha1.AIMTemplateCache]{Value: best}
}

// fetchModelCaches lists all AIMModelCache resources in the namespace.
func fetchModelCaches(
	ctx context.Context,
	c client.Client,
	namespace string,
) controllerutils.FetchResult[*aimv1alpha1.AIMModelCacheList] {
	return controllerutils.FetchList(ctx, c, &aimv1alpha1.AIMModelCacheList{}, client.InNamespace(namespace))
}

// fetchServicePVC fetches the service's temporary PVC for model downloads.
func fetchServicePVC(
	ctx context.Context,
	c client.Client,
	service *aimv1alpha1.AIMService,
) controllerutils.FetchResult[*corev1.PersistentVolumeClaim] {
	pvcName, err := GenerateServicePVCName(service.Name, service.Namespace)
	if err != nil {
		return controllerutils.FetchResult[*corev1.PersistentVolumeClaim]{Error: err}
	}

	return controllerutils.Fetch(ctx, c, client.ObjectKey{
		Namespace: service.Namespace,
		Name:      pvcName,
	}, &corev1.PersistentVolumeClaim{})
}

// resolveStorageClassName determines the storage class to use.
func resolveStorageClassName(service *aimv1alpha1.AIMService, obs ServiceObservation) string {
	// Service-level storage config takes precedence
	if service.Spec.Storage != nil && service.Spec.Storage.DefaultStorageClassName != nil {
		return *service.Spec.Storage.DefaultStorageClassName
	}

	// Fall back to runtime config
	if obs.mergedRuntimeConfig.Value != nil && obs.mergedRuntimeConfig.Value.Storage != nil {
		if obs.mergedRuntimeConfig.Value.Storage.DefaultStorageClassName != nil {
			return *obs.mergedRuntimeConfig.Value.Storage.DefaultStorageClassName
		}
	}

	return ""
}

// resolvePVCHeadroomPercent determines the PVC headroom percentage.
func resolvePVCHeadroomPercent(service *aimv1alpha1.AIMService, obs ServiceObservation) int32 {
	// Service-level storage config takes precedence
	if service.Spec.Storage != nil && service.Spec.Storage.PVCHeadroomPercent != nil {
		return *service.Spec.Storage.PVCHeadroomPercent
	}

	// Fall back to runtime config
	if obs.mergedRuntimeConfig.Value != nil && obs.mergedRuntimeConfig.Value.Storage != nil {
		if obs.mergedRuntimeConfig.Value.Storage.PVCHeadroomPercent != nil {
			return *obs.mergedRuntimeConfig.Value.Storage.PVCHeadroomPercent
		}
	}

	return DefaultPVCHeadroomPercent
}

// calculateRequiredStorageSize computes total storage needed for model sources.
func calculateRequiredStorageSize(modelSources []aimv1alpha1.AIMModelSource, headroomPercent int32) (resource.Quantity, error) {
	if len(modelSources) == 0 {
		return resource.Quantity{}, fmt.Errorf("no model sources available")
	}

	var totalBytes int64
	for _, source := range modelSources {
		if source.Size == nil || source.Size.IsZero() {
			return resource.Quantity{}, fmt.Errorf("model source %q has no size specified", source.Name)
		}
		totalBytes += source.Size.Value()
	}

	if totalBytes == 0 {
		return resource.Quantity{}, fmt.Errorf("total model size is zero")
	}

	// Apply headroom and round to nearest Gi
	return quantityWithHeadroom(totalBytes, headroomPercent), nil
}

// quantityWithHeadroom adds headroom percentage and rounds to nearest Gi.
func quantityWithHeadroom(bytes int64, headroomPercent int32) resource.Quantity {
	// Add headroom
	withHeadroom := float64(bytes) * (1.0 + float64(headroomPercent)/100.0)

	// Convert to Gi and round up
	gi := withHeadroom / (1024 * 1024 * 1024)
	roundedGi := int64(gi + 0.999) // Round up

	if roundedGi < 1 {
		roundedGi = 1
	}

	return resource.MustParse(fmt.Sprintf("%dGi", roundedGi))
}
