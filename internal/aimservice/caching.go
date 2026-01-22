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

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
// DEDICATED MODEL CACHE (UNIFIED DOWNLOAD ARCHITECTURE)
// =======================================================
// For non-cached modes (Never/Auto without existing cache), we create dedicated
// AIMModelCache resources with owner references to the AIMService. This ensures:
// 1. Downloads are always handled by the engine (not the inference container)
// 2. Credentials are never exposed to inference pods
// 3. Downloads run on CPU nodes (GPU-independent)
// 4. PVC lifecycle is tied to the service (deleted when service is deleted)

// GenerateDedicatedModelCacheName creates a deterministic name for a service's dedicated model cache.
func GenerateDedicatedModelCacheName(serviceName, modelID string) (string, error) {
	return utils.GenerateDerivedName([]string{serviceName, "dedicated"}, utils.WithHashSource(modelID))
}

// planDedicatedModelCaches creates AIMModelCache resources for non-cached mode.
// These caches are owned by the service and deleted when the service is deleted.
// Returns a list of model caches to create.
func planDedicatedModelCaches(
	service *aimv1alpha1.AIMService,
	templateStatus *aimv1alpha1.AIMServiceTemplateStatus,
	obs ServiceObservation,
) []client.Object {
	cachingMode := service.Spec.GetCachingMode()

	// If caching is required (Always mode), don't create dedicated caches
	if cachingMode == aimv1alpha1.CachingModeAlways {
		return nil
	}

	// If template cache exists and is ready, use shared cache instead
	if obs.templateCache.Value != nil &&
		obs.templateCache.Value.Status.Status == constants.AIMStatusReady {
		return nil
	}

	// Need model sources to determine what to cache
	if templateStatus == nil || len(templateStatus.ModelSources) == 0 {
		return nil
	}

	// Check which model sources already have dedicated caches
	existingCaches := make(map[string]bool)
	if obs.dedicatedModelCaches.Value != nil {
		for _, cache := range obs.dedicatedModelCaches.Value.Items {
			existingCaches[cache.Spec.SourceURI] = true
		}
	}

	storageClassName := resolveStorageClassName(service, obs)
	serviceLabelValue, _ := utils.SanitizeLabelValue(service.Name)

	var result []client.Object
	for _, modelSource := range templateStatus.ModelSources {
		// Skip if cache already exists for this source
		if existingCaches[modelSource.SourceURI] {
			continue
		}

		// Skip if size is not available (template still resolving)
		if modelSource.Size == nil || modelSource.Size.IsZero() {
			continue
		}

		cacheName, err := GenerateDedicatedModelCacheName(service.Name, modelSource.ModelID)
		if err != nil {
			continue
		}

		cache := &aimv1alpha1.AIMModelCache{
			TypeMeta: metav1.TypeMeta{
				APIVersion: aimv1alpha1.GroupVersion.String(),
				Kind:       "AIMModelCache",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      cacheName,
				Namespace: service.Namespace,
				Labels: map[string]string{
					constants.LabelK8sManagedBy: constants.LabelValueManagedBy,
					constants.LabelK8sComponent: constants.ComponentModelStorage,
					constants.LabelService:      serviceLabelValue,
					constants.LabelCacheType:    constants.LabelValueCacheTypeDedicated,
				},
			},
			Spec: aimv1alpha1.AIMModelCacheSpec{
				SourceURI:        modelSource.SourceURI,
				ModelID:          modelSource.ModelID,
				Size:             *modelSource.Size,
				StorageClassName: storageClassName,
				// Merge base-level env with per-source env (source takes precedence)
				Env:              utils.MergeEnvVars(service.Spec.Env, modelSource.Env),
				RuntimeConfigRef: service.Spec.RuntimeConfigRef,
			},
		}

		result = append(result, cache)
	}

	return result
}

// fetchDedicatedModelCaches fetches the service's dedicated model caches.
func fetchDedicatedModelCaches(
	ctx context.Context,
	c client.Client,
	service *aimv1alpha1.AIMService,
) controllerutils.FetchResult[*aimv1alpha1.AIMModelCacheList] {
	serviceLabelValue, _ := utils.SanitizeLabelValue(service.Name)

	return controllerutils.FetchList(ctx, c, &aimv1alpha1.AIMModelCacheList{},
		client.InNamespace(service.Namespace),
		client.MatchingLabels{
			constants.LabelService:   serviceLabelValue,
			constants.LabelCacheType: constants.LabelValueCacheTypeDedicated,
		},
	)
}

// areDedicatedCachesReady returns true if all dedicated model caches are ready.
func areDedicatedCachesReady(caches *aimv1alpha1.AIMModelCacheList, modelSources []aimv1alpha1.AIMModelSource) bool {
	if caches == nil || len(modelSources) == 0 {
		return false
	}

	// Build map of ready caches by source URI
	readyCaches := make(map[string]bool)
	for _, cache := range caches.Items {
		if cache.Status.Status == constants.AIMStatusReady {
			readyCaches[cache.Spec.SourceURI] = true
		}
	}

	// Check all model sources have a ready cache
	for _, source := range modelSources {
		if !readyCaches[source.SourceURI] {
			return false
		}
	}
	return true
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
	templateSpec *aimv1alpha1.AIMServiceTemplateSpec,
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

	// Copy env from template spec (used for download authentication)
	if templateSpec != nil && len(templateSpec.Env) > 0 {
		cache.Spec.Env = utils.CopyEnvVars(templateSpec.Env)
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
			return resource.Quantity{}, fmt.Errorf("model source %q has no size specified", source.ModelID)
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
