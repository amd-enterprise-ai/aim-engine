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

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

// ============================================================================
// FETCH
// ============================================================================

type ServiceCachingFetchResult struct {
	templateCache *aimv1alpha1.AIMTemplateCache
	modelCaches   []aimv1alpha1.AIMModelCache
}

func fetchServiceCachingResult(
	ctx context.Context,
	c client.Client,
	service *aimv1alpha1.AIMService,
	templateName string,
	templateNamespace string,
) (ServiceCachingFetchResult, error) {
	result := ServiceCachingFetchResult{}

	if templateName == "" {
		return result, nil
	}

	// Fetch template cache for the resolved template
	templateCache := &aimv1alpha1.AIMTemplateCache{}
	if err := c.Get(ctx, client.ObjectKey{Name: templateName, Namespace: templateNamespace}, templateCache); err != nil && !errors.IsNotFound(err) {
		return result, fmt.Errorf("failed to fetch template cache: %w", err)
	} else if err == nil {
		result.templateCache = templateCache
	}

	// Fetch all model caches in the service namespace
	// These will be matched with model sources in the observe phase
	if result.templateCache != nil &&
		(result.templateCache.Status.Status == constants.AIMStatusReady ||
			result.templateCache.Status.Status == constants.AIMStatusProgressing) {

		modelCacheList := &aimv1alpha1.AIMModelCacheList{}
		if err := c.List(ctx, modelCacheList, client.InNamespace(service.Namespace)); err != nil {
			return result, fmt.Errorf("failed to fetch model caches: %w", err)
		}
		result.modelCaches = modelCacheList.Items
	}

	return result, nil
}

// ============================================================================
// OBSERVE
// ============================================================================

// serviceCachingObservation contains the observed state of caching for a service.
type serviceCachingObservation struct {
	templateCache            *aimv1alpha1.AIMTemplateCache
	templateCacheReady       bool
	templateCacheFailed      bool
	templateCacheRequested   bool
	shouldCreateCache        bool
	shouldRequestCacheRetry  bool
	failedModelCachesToRetry []aimv1alpha1.AIMModelCache
	modelCachesToMount       []modelCacheMount
}

// modelCacheMount represents a model cache that should be mounted in the inferenceService
type modelCacheMount struct {
	cache     aimv1alpha1.AIMModelCache
	modelName string // From model source
}

func observeServiceCaching(
	result ServiceCachingFetchResult,
	service *aimv1alpha1.AIMService,
	templateSpec *aimv1alpha1.AIMServiceTemplateSpec,
	templateStatus *aimv1alpha1.AIMServiceTemplateStatus,
) serviceCachingObservation {
	obs := serviceCachingObservation{}

	// Get effective caching mode (handles backward compatibility with CacheModel)
	cachingMode := service.Spec.GetCachingMode()
	templateCachingEnabled := templateSpec != nil && templateSpec.Caching != nil && templateSpec.Caching.Enabled

	// Determine if we should use/create cache based on mode
	var shouldUseCache, shouldCreateCache bool

	switch cachingMode {
	case aimv1alpha1.CachingModeNever:
		// Never use cache
		shouldUseCache = false
		shouldCreateCache = false

	case aimv1alpha1.CachingModeAlways:
		// Always use cache, create if needed
		shouldUseCache = true
		shouldCreateCache = result.templateCache == nil

	case aimv1alpha1.CachingModeAuto:
		// Auto mode: use if exists, create only if template requests it
		shouldUseCache = result.templateCache != nil || templateCachingEnabled
		shouldCreateCache = result.templateCache == nil && templateCachingEnabled
	}

	obs.templateCacheRequested = shouldUseCache

	// Observe template cache status
	if result.templateCache != nil {
		obs.templateCache = result.templateCache

		switch result.templateCache.Status.Status {
		case constants.AIMStatusReady:
			obs.templateCacheReady = true
		case constants.AIMStatusFailed:
			obs.templateCacheFailed = true

			// Check if this service has already attempted a retry
			// Retry attempts are tracked in the service's own status
			retryAttempts := 0
			if service.Status.Cache != nil {
				retryAttempts = service.Status.Cache.RetryAttempts
			}
			if retryAttempts == 0 {
				// Haven't retried yet - collect failed ModelCaches for deletion
				for _, mc := range result.modelCaches {
					if mc.Status.Status == aimv1alpha1.AIMServiceReasonCacheFailed {
						obs.failedModelCachesToRetry = append(obs.failedModelCachesToRetry, mc)
					}
				}
				obs.shouldRequestCacheRetry = len(obs.failedModelCachesToRetry) > 0
			} else {
				// Already retried - don't try again
				obs.shouldRequestCacheRetry = false
			}
		}
	} else if shouldCreateCache {
		obs.shouldCreateCache = true
	}

	// Match model caches with model sources for mounting
	if obs.templateCacheReady && templateStatus != nil {
		obs.modelCachesToMount = matchModelCachesWithSources(result.modelCaches, templateStatus.ModelSources)
	}

	return obs
}

// matchModelCachesWithSources matches available model caches with template model sources
func matchModelCachesWithSources(modelCaches []aimv1alpha1.AIMModelCache, modelSources []aimv1alpha1.AIMModelSource) []modelCacheMount {
	var mounts []modelCacheMount

	// For each model source, find a matching available cache
	for _, source := range modelSources {
		for _, cache := range modelCaches {
			if cache.Spec.SourceURI == source.SourceURI &&
				cache.Status.Status == constants.AIMStatusReady {
				mounts = append(mounts, modelCacheMount{
					cache:     cache,
					modelName: source.Name,
				})
				break // Found match for this source, move to next
			}
		}
	}

	return mounts
}

// ============================================================================
// PLAN
// ============================================================================

//nolint:unparam // error return kept for API consistency with other plan functions
func planServiceCache(
	service *aimv1alpha1.AIMService,
	obs serviceCachingObservation,
	templateName string,
	templateNamespace string,
	mergedConfig *aimv1alpha1.AIMRuntimeConfigCommon,
) (client.Object, error) {
	// Create new cache if needed
	if obs.shouldCreateCache && templateName != "" {
		return buildTemplateCache(service, templateName, templateNamespace, mergedConfig), nil
	}

	return nil, nil
}

// buildTemplateCache creates an AIMTemplateCache object
func buildTemplateCache(
	service *aimv1alpha1.AIMService,
	templateName string,
	templateNamespace string,
	mergedConfig *aimv1alpha1.AIMRuntimeConfigCommon,
) *aimv1alpha1.AIMTemplateCache {
	// Extract storage class from merged config
	storageClassName := ""
	if mergedConfig != nil && mergedConfig.Storage != nil && mergedConfig.Storage.DefaultStorageClassName != nil {
		storageClassName = *mergedConfig.Storage.DefaultStorageClassName
	}

	// Determine template scope based on namespace
	templateScope := aimv1alpha1.AIMServiceTemplateScopeNamespace
	if templateNamespace == "" {
		templateScope = aimv1alpha1.AIMServiceTemplateScopeCluster
	}

	cache := &aimv1alpha1.AIMTemplateCache{
		TypeMeta: metav1.TypeMeta{
			APIVersion: aimv1alpha1.GroupVersion.String(),
			Kind:       "AIMTemplateCache",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      templateName,
			Namespace: service.Namespace,
		},
		Spec: aimv1alpha1.AIMTemplateCacheSpec{
			TemplateName:     templateName,
			TemplateScope:    templateScope,
			StorageClassName: storageClassName,
			Env:              service.Spec.Env,
		},
	}

	return cache
}

// ============================================================================
// PROJECT
// ============================================================================

func projectServiceCaching(
	status *aimv1alpha1.AIMServiceStatus,
	cm *controllerutils.ConditionManager,
	h *controllerutils.StatusHelper,
	obs serviceCachingObservation,
) bool {
	if !obs.templateCacheRequested {
		// Caching not requested, nothing to project
		return false
	}

	if obs.templateCacheFailed {
		// Cache failed
		if obs.shouldRequestCacheRetry {
			// Increment retry counter
			if status.Cache == nil {
				status.Cache = &aimv1alpha1.AIMServiceCacheStatus{}
			}
			status.Cache.RetryAttempts++

			h.Progressing(aimv1alpha1.AIMServiceReasonCacheRetrying, "Deleting failed model caches for retry")
			cm.MarkFalse(aimv1alpha1.AIMServiceConditionCacheReady, aimv1alpha1.AIMServiceReasonCacheRetrying, fmt.Sprintf("Retrying cache download (attempt %d)", status.Cache.RetryAttempts), controllerutils.LevelWarning)
			return false
		} else {
			// Already retried - degrade with blocking error
			h.Degraded(aimv1alpha1.AIMServiceReasonCacheFailed, "Template cache failed after retry")
			cm.MarkFalse(aimv1alpha1.AIMServiceConditionCacheReady, aimv1alpha1.AIMServiceReasonCacheFailed, "Cache retry exhausted", controllerutils.LevelWarning)
			return true // Blocking error
		}
	}

	if obs.shouldCreateCache {
		// Cache requested but doesn't exist yet - progressing
		h.Progressing(aimv1alpha1.AIMServiceReasonCacheCreating, "Creating template cache")
		cm.MarkFalse(aimv1alpha1.AIMServiceConditionCacheReady, aimv1alpha1.AIMServiceReasonCacheCreating, "Template cache being created", controllerutils.LevelNormal)
		return false
	}

	if obs.templateCache != nil && !obs.templateCacheReady && !obs.templateCacheFailed {
		// Cache exists but not ready - progressing
		h.Progressing(aimv1alpha1.AIMServiceReasonCacheNotReady, "Waiting for template cache to become ready")
		cm.MarkFalse(aimv1alpha1.AIMServiceConditionCacheReady, aimv1alpha1.AIMServiceReasonCacheNotReady, fmt.Sprintf("Template cache status: %s", obs.templateCache.Status.Status), controllerutils.LevelNormal)
		return false
	}

	if obs.templateCacheReady {
		// Cache ready - set condition and status
		cm.MarkTrue(aimv1alpha1.AIMServiceConditionCacheReady, aimv1alpha1.AIMServiceReasonCacheReady, "Template cache is ready", controllerutils.LevelNormal)

		// Project cache reference to status
		if status.Cache == nil {
			status.Cache = &aimv1alpha1.AIMServiceCacheStatus{}
		}
		if status.Cache.TemplateCacheRef == nil {
			status.Cache.TemplateCacheRef = &aimv1alpha1.AIMResolvedReference{}
		}
		status.Cache.TemplateCacheRef.Name = obs.templateCache.Name
		status.Cache.TemplateCacheRef.Namespace = obs.templateCache.Namespace
		status.Cache.TemplateCacheRef.Kind = obs.templateCache.Kind
		status.Cache.TemplateCacheRef.UID = obs.templateCache.UID
	}

	return false
}
