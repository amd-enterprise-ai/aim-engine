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

// Package profilecache holds the single cache-resolution shared by the two
// runtime-projection paths, so an eager per-profile runtime and a lazy shadow
// mount the identical profile-owned cache and serving correctness never depends
// on the projection mode.
package profilecache

import (
	"context"
	"fmt"
	"sort"

	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

// StatusStaleError reports that a matching cache exists, but its status has not
// caught up to the current spec generation. Callers should preserve their last
// known-good projection and retry rather than treating this convergence state
// as an infrastructure failure or as proof that no cache exists.
type StatusStaleError struct {
	Namespace          string
	Name               string
	Generation         int64
	ObservedGeneration int64
}

func (e *StatusStaleError) Error() string {
	return fmt.Sprintf(
		"AIMProfileCache %s/%s status has not observed metadata generation %d (observedGeneration=%d)",
		e.Namespace,
		e.Name,
		e.Generation,
		e.ObservedGeneration,
	)
}

// FindReadyShared returns a Ready, profile-owned (Shared) AIMProfileCache in
// namespace that caches the profile named profileName at the given scope, or nil
// when none exists. It returns an error instead of nil when a matching cache's
// readiness is unknown, preventing callers from treating an outage as no cache.
//
// This is the single cache-resolution used by BOTH runtime-projection paths: the
// eager profile reconciler (which mounts the cache on the per-profile / model-slug
// ServingRuntime it projects) and the lazy InferenceService-watch reconciler
// (which mounts it on the namespace shadow it materializes). Keeping them on one
// helper is what makes serving correctness mode-independent — an eager complete
// namespace runtime and a lazy shadow resolve and mount the identical cache.
//
// Resolution is deliberately decoupled from the profile's spec.caching.enabled
// flag and from the cache's object name. Both a profile-owned cache (created when
// the profile opts into caching, named after the profile) and a service-driven
// Shared cache (created by an AIMService in Shared mode, named
// <profile>-cache-<hash>) qualify, because both carry spec.profileName +
// spec.mode=Shared referencing the same profile. Only a Ready Shared cache whose
// profileName and scope match is returned; a service-owned (Dedicated) cache is
// overlaid on its own consuming ISVC instead, never mounted on the shared runtime.
//
// Scope comparison mirrors the AIMService resolver's convention: an empty scope
// (on either the query or a cache's spec) is normalized to Namespace, so a cache
// written without an explicit scope still resolves for a namespace-scoped query.
func FindReadyShared(
	ctx context.Context,
	c client.Client,
	namespace, profileName string,
	scope aimv1alpha1.AIMResolutionScope,
) (*aimv1alpha2.AIMProfileCache, error) {
	if profileName == "" {
		return nil, nil
	}

	var caches aimv1alpha2.AIMProfileCacheList
	if err := c.List(ctx, &caches, client.InNamespace(namespace)); err != nil {
		return nil, err
	}

	var ready *aimv1alpha2.AIMProfileCache
	var unknown *aimv1alpha2.AIMProfileCache
	var stale *aimv1alpha2.AIMProfileCache
	for i := range caches.Items {
		cache := &caches.Items[i]
		if !matchesShared(cache, profileName, scope) {
			continue
		}
		if !StatusCurrent(cache) {
			if stale == nil || cache.Name < stale.Name {
				stale = cache
			}
			continue
		}
		if cacheReadinessUnknown(cache) {
			if unknown == nil || cache.Name < unknown.Name {
				unknown = cache
			}
			continue
		}
		if cache.Status.Status == constants.AIMStatusReady &&
			(ready == nil || cache.Name < ready.Name) {
			ready = cache
		}
	}
	if ready != nil {
		return ready, nil
	}
	if unknown != nil {
		return nil, fmt.Errorf(
			"AIMProfileCache %s/%s readiness is unknown because reconciliation has an active error condition",
			unknown.Namespace,
			unknown.Name,
		)
	}
	if stale != nil {
		return nil, &StatusStaleError{
			Namespace:          stale.Namespace,
			Name:               stale.Name,
			Generation:         stale.Generation,
			ObservedGeneration: stale.Status.ObservedGeneration,
		}
	}
	return nil, nil
}

func matchesShared(
	cache *aimv1alpha2.AIMProfileCache,
	profileName string,
	scope aimv1alpha1.AIMResolutionScope,
) bool {
	if cache.Spec.ProfileName != profileName {
		return false
	}
	if normalizeScope(cache.Spec.ProfileScope) != normalizeScope(scope) {
		return false
	}
	return cache.Spec.Mode == aimv1alpha2.ProfileCacheModeShared
}

func cacheReadinessUnknown(cache *aimv1alpha2.AIMProfileCache) bool {
	for _, conditionType := range []string{
		controllerutils.ConditionTypeDependenciesReachable,
		controllerutils.ConditionTypeAuthValid,
		controllerutils.ConditionTypeConfigValid,
	} {
		if meta.IsStatusConditionFalse(cache.Status.Conditions, conditionType) {
			return true
		}
	}
	return false
}

// StatusCurrent reports whether the cache's status describes its current spec
// generation. A stale status (including one never written) must not be trusted
// for readiness or resolved artifacts.
func StatusCurrent(cache *aimv1alpha2.AIMProfileCache) bool {
	return cache.Status.ObservedGeneration == cache.Generation
}

// SelectBestShared returns the Shared cache matching the profile and scope that
// a service should use. Caches whose status describes their current spec
// generation are ranked by health. When every match is stale (just created, or
// its spec just changed), the first by name is returned anyway: the cache exists
// and will converge, and reporting it as absent would make the caller plan a
// replacement on top of it. Callers must check StatusCurrent before trusting
// the returned cache's status. Equal-health candidates are ordered by name so
// every caller chooses the same cache independently of API list order.
func SelectBestShared(
	caches []aimv1alpha2.AIMProfileCache,
	profileName string,
	scope aimv1alpha1.AIMResolutionScope,
) *aimv1alpha2.AIMProfileCache {
	if profileName == "" {
		return nil
	}

	current := make([]aimv1alpha2.AIMProfileCache, 0, len(caches))
	var stale *aimv1alpha2.AIMProfileCache
	for i := range caches {
		cache := caches[i]
		// Only a profile-owned (Shared) cache belongs on the shared runtime. A
		// service-owned (Dedicated) cache is overlaid on its own consuming ISVC
		// instead, so mounting it here too would duplicate the volume.
		if !matchesShared(&cache, profileName, scope) {
			continue
		}
		if !StatusCurrent(&cache) {
			if stale == nil || cache.Name < stale.Name {
				stale = &cache
			}
			continue
		}
		current = append(current, cache)
	}
	if len(current) == 0 {
		return stale
	}
	sort.Slice(current, func(i, j int) bool {
		return current[i].Name < current[j].Name
	})
	return utils.SelectBestPtr(current, func(cache *aimv1alpha2.AIMProfileCache) constants.AIMStatus {
		return cache.Status.GetAIMStatus()
	})
}

// normalizeScope treats an empty scope as Namespace, matching the AIMService
// resolver so caches written without an explicit scope still resolve.
func normalizeScope(scope aimv1alpha1.AIMResolutionScope) aimv1alpha1.AIMResolutionScope {
	if scope == "" {
		return aimv1alpha1.AIMResolutionScopeNamespace
	}
	return scope
}
