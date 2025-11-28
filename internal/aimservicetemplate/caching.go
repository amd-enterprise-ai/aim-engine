package aimservicetemplate

import (
	"context"
	"fmt"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/aimtemplatecache"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ============================================================================
// FETCH
// ============================================================================


type ServiceTemplateCacheFetchResult struct {
	ExistingTemplateCaches []aimv1alpha1.AIMTemplateCache
}

func fetchServiceTemplateCacheResult(ctx context.Context, c client.Client, template client.Object, status *aimv1alpha1.AIMServiceTemplateStatus) (ServiceTemplateCacheFetchResult, error) {
	result := ServiceTemplateCacheFetchResult{}
	if status.ResolvedCache != nil {
		templateCache := &aimv1alpha1.AIMTemplateCache{}
		if err := c.Get(ctx, status.ResolvedCache.NamespacedName(), templateCache); err != nil {
			if errors.IsNotFound(err) {
				// Cache was deleted, re-fetch (clear from status)
				status.ResolvedCache = nil
			} else {
				return result, fmt.Errorf("error fetching template cache object: %w", err)
			}
		} else {
			// Cache still exists, use that
			result.ExistingTemplateCaches = []aimv1alpha1.AIMTemplateCache{*templateCache}
		}
	}
	// If the resolved cache is nil, or was reset in the above step
	if status.ResolvedCache == nil {
		var caches aimv1alpha1.AIMTemplateCacheList

		if err := c.List(ctx, &caches,
			client.InNamespace(template.GetNamespace()),
			client.MatchingFields{
				aimtemplatecache.TemplateCacheTemplateNameIndexKey: template.GetName(),
			},
		); err != nil {
			return result, fmt.Errorf("error listing template cache objects: %w", err)
		}

		result.ExistingTemplateCaches = caches.Items
	}
	return result, nil
}

// ============================================================================
// OBSERVE
// ============================================================================


type ServiceTemplateCacheObservation struct {
	ShouldCreateCache  bool
	BestAvailableCache *aimv1alpha1.AIMTemplateCache
}

func observeServiceTemplateCache(result ServiceTemplateCacheFetchResult, serviceTemplate aimv1alpha1.AIMServiceTemplate) ServiceTemplateCacheObservation {
	obs := ServiceTemplateCacheObservation{}

	// If we have existing template caches, determine the one that's closest to availability and the newest
	if len(result.ExistingTemplateCaches) > 0 {
		// Find the cache with the best status (highest priority)
		// If multiple caches have the same status, choose the newest one
		var bestCache *aimv1alpha1.AIMTemplateCache
		bestPriority := -1

		for i := range result.ExistingTemplateCaches {
			cache := &result.ExistingTemplateCaches[i]
			priority := constants.AIMStatusPriority[cache.Status.Status]

			if bestCache == nil {
				bestCache = cache
				bestPriority = priority
				continue
			}

			// Choose cache with higher priority status
			if priority > bestPriority {
				bestCache = cache
				bestPriority = priority
			} else if priority == bestPriority {
				// If same priority, choose newer cache
				if cache.CreationTimestamp.After(bestCache.CreationTimestamp.Time) {
					bestCache = cache
				}
			}
		}

		obs.BestAvailableCache = bestCache
	} else if serviceTemplate.Spec.Caching != nil && serviceTemplate.Spec.Caching.Enabled {
		// Should create cache if no cache exists but caching is enabled
		obs.ShouldCreateCache = true
	}

	return obs
}

// ============================================================================
// BUILD
// ============================================================================


func buildServiceTemplateCache(serviceTemplate aimv1alpha1.AIMServiceTemplate, config *aimv1alpha1.AIMRuntimeConfigCommon) *aimv1alpha1.AIMTemplateCache {
	storageClassName := utils.ResolveStorageClass("", config)
	templateCache := &aimv1alpha1.AIMTemplateCache{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "aimv1alpha1",
			Kind:       "AIMTemplateCache",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceTemplate.Name,
			Namespace: serviceTemplate.Namespace,
		},
		Spec: aimv1alpha1.AIMTemplateCacheSpec{
			TemplateName:     serviceTemplate.Name,
			StorageClassName: storageClassName,
			Env:              serviceTemplate.Spec.Env,
			ModelSources:     serviceTemplate.Spec.ModelSources,
		},
	}
	return templateCache
}

// ============================================================================
// PROJECT
// ============================================================================


func projectServiceTemplateCache(
	status *aimv1alpha1.AIMServiceTemplateStatus,
	cm *controllerutils.ConditionManager,
	h *controllerutils.StatusHelper,
	observation ServiceTemplateCacheObservation,
) bool {
	if cache := observation.BestAvailableCache; cache != nil {
		switch cache.Status.Status {
		case constants.AIMStatusReady:
			cm.MarkTrue(aimv1alpha1.AIMTemplateCacheWarmConditionType, string(constants.AIMStatusReady), "Cache is available and ready", controllerutils.LevelNormal)

			// Set the cache resolution if the best cache is available
			status.ResolvedCache = &aimv1alpha1.AIMResolvedReference{
				Name:      cache.Name,
				Namespace: cache.Namespace,
				Scope:     aimv1alpha1.AIMResolutionScopeNamespace,
				Kind:      "AIMTemplateCache",
				UID:       cache.UID,
			}

		case constants.AIMStatusPending, constants.AIMStatusProgressing:
			cm.MarkFalse(aimv1alpha1.AIMTemplateCacheWarmConditionType, string(constants.AIMStatusReady), "Waiting for cache to be ready", controllerutils.LevelNormal)
			h.Progressing("WaitingForCache", "Waiting for cache to be ready")
		case constants.AIMStatusDegraded:
			cm.MarkFalse(aimv1alpha1.AIMTemplateCacheWarmConditionType, string(constants.AIMStatusReady), "Cache degraded", controllerutils.LevelWarning)
			h.Degraded("CacheDegraded", "Cache is degraded")
			return true
		case constants.AIMStatusFailed:
			cm.MarkFalse(aimv1alpha1.AIMTemplateCacheWarmConditionType, string(constants.AIMStatusReady), "Cache failed", controllerutils.LevelWarning)
			h.Failed("CacheFailed", "Cache has failed")
			return true
		}
	}
	return false
}
