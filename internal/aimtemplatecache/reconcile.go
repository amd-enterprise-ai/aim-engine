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

package aimtemplatecache

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

const (
	TemplateCacheTemplateNameIndexKey = ".spec.templateName"
)

// ============================================================================
// DOMAIN RECONCILER
// ============================================================================

// Reconciler implements the domain reconciliation logic for AIMTemplateCache.
type Reconciler struct {
	Scheme *runtime.Scheme
}

// ============================================================================
// FETCH PHASE
// ============================================================================

// FetchResult holds all fetched resources for an AIMTemplateCache.
type FetchResult struct {
	NamespaceTemplate controllerutils.FetchResult[*aimv1alpha1.AIMServiceTemplate]
	ClusterTemplate   controllerutils.FetchResult[*aimv1alpha1.AIMClusterServiceTemplate]
	ModelCaches       controllerutils.FetchResult[[]aimv1alpha1.AIMModelCache]
}

// Fetch retrieves all external dependencies for an AIMTemplateCache.
func (r *Reconciler) Fetch(
	ctx context.Context,
	c client.Client,
	cache *aimv1alpha1.AIMTemplateCache,
) (FetchResult, error) {
	result := FetchResult{}

	// Fetch namespace-scoped template
	var nsTemplate aimv1alpha1.AIMServiceTemplate
	nsErr := c.Get(ctx, client.ObjectKey{
		Namespace: cache.Namespace,
		Name:      cache.Spec.TemplateName,
	}, &nsTemplate)
	if nsErr != nil && !apierrors.IsNotFound(nsErr) {
		return result, fmt.Errorf("error fetching namespace template: %w", nsErr)
	}
	result.NamespaceTemplate = controllerutils.FetchResult[*aimv1alpha1.AIMServiceTemplate]{Result: &nsTemplate, Error: nsErr}

	// Fetch cluster-scoped template (only if namespace template not found)
	if apierrors.IsNotFound(nsErr) {
		var clusterTemplate aimv1alpha1.AIMClusterServiceTemplate
		clusterErr := c.Get(ctx, client.ObjectKey{
			Name: cache.Spec.TemplateName,
		}, &clusterTemplate)
		if clusterErr != nil && !apierrors.IsNotFound(clusterErr) {
			return result, fmt.Errorf("error fetching cluster template: %w", clusterErr)
		}
		result.ClusterTemplate = controllerutils.FetchResult[*aimv1alpha1.AIMClusterServiceTemplate]{Result: &clusterTemplate, Error: clusterErr}
	}

	// Fetch model caches
	var caches aimv1alpha1.AIMModelCacheList
	cachesErr := c.List(ctx, &caches, client.InNamespace(cache.Namespace))
	if cachesErr != nil {
		return result, fmt.Errorf("list model caches: %w", cachesErr)
	}
	result.ModelCaches = controllerutils.FetchResult[[]aimv1alpha1.AIMModelCache]{Result: caches.Items, Error: cachesErr}

	return result, nil
}

// ============================================================================
// OBSERVE PHASE
// ============================================================================

// ----- Main Observation Struct -----

// Observation holds all observed state for an AIMTemplateCache.
type Observation struct {
	template    templateObservation
	modelCaches modelCachesObservation
}

// ----- Template Sub-Domain -----

// templateObservation contains information about the referenced template.
type templateObservation struct {
	found        bool
	modelSources []aimv1alpha1.AIMModelSource
	error        string
}

type templateObservationInputs struct {
	modelSources []aimv1alpha1.AIMModelSource
	error        error
}

// buildTemplateObservation is a pure function that constructs Template observation.
func buildTemplateObservation(inputs templateObservationInputs) templateObservation {
	obs := templateObservation{}

	if inputs.error != nil {
		obs.found = false
		obs.error = inputs.error.Error()
		return obs
	}

	obs.found = true
	obs.modelSources = inputs.modelSources

	return obs
}

// ----- ModelCaches Sub-Domain -----

// modelCachesObservation contains information about model caches.
type modelCachesObservation struct {
	cacheStatus        map[string]constants.AIMStatus
	missingCaches      []aimv1alpha1.AIMModelSource
	allCachesAvailable bool
}

type modelCachesObservationInputs struct {
	modelSources     []aimv1alpha1.AIMModelSource
	availableCaches  []aimv1alpha1.AIMModelCache
	storageClassName string
}

// buildModelCachesObservation is a pure function that constructs ModelCaches observation.
func buildModelCachesObservation(inputs modelCachesObservationInputs) modelCachesObservation {
	obs := modelCachesObservation{
		cacheStatus: map[string]constants.AIMStatus{},
	}

	// Loop through model sources and check which caches are available
	for _, model := range inputs.modelSources {
		bestStatus := constants.AIMStatusPending
		for _, cached := range inputs.availableCaches {
			// ModelCache is a match if it has the same SourceURI and a StorageClass matching our config
			if cached.Spec.SourceURI == model.SourceURI &&
				(inputs.storageClassName == "" || inputs.storageClassName == cached.Spec.StorageClassName) {
				// Update bestStatus if the cached status is better (higher priority)
				if constants.CompareAIMStatus(cached.Status.Status, bestStatus) > 0 {
					bestStatus = cached.Status.Status
				}
			}
		}

		obs.cacheStatus[model.Name] = bestStatus
		if bestStatus == constants.AIMStatusPending {
			obs.missingCaches = append(obs.missingCaches, model)
		}
	}

	// Check if all caches are available
	obs.allCachesAvailable = len(obs.missingCaches) == 0 && len(inputs.modelSources) > 0

	return obs
}

// ----- Main Observe Method -----

// Observe builds a pure observation from fetched data.
// No client access - all fetching happens in the Fetch phase.
func (r *Reconciler) Observe(ctx context.Context, cache *aimv1alpha1.AIMTemplateCache, fetchResult FetchResult) (Observation, error) {
	obs := Observation{}

	// Build template observation from fetched data
	obs.template = buildTemplateObservation(templateObservationInputs{
		modelSources: func() []aimv1alpha1.AIMModelSource {
			// Prefer namespace template over cluster template
			if fetchResult.NamespaceTemplate.Error == nil {
				return fetchResult.NamespaceTemplate.Result.Status.ModelSources
			}
			if fetchResult.ClusterTemplate.Error == nil {
				return fetchResult.ClusterTemplate.Result.Status.ModelSources
			}
			return nil
		}(),
		error: func() error {
			// If both templates failed with NotFound, return a combined error
			if apierrors.IsNotFound(fetchResult.NamespaceTemplate.Error) && apierrors.IsNotFound(fetchResult.ClusterTemplate.Error) {
				return fmt.Errorf("template %q not found in namespace %q or cluster scope", cache.Spec.TemplateName, cache.Namespace)
			}
			// Return first non-NotFound error
			if fetchResult.NamespaceTemplate.Error != nil && !apierrors.IsNotFound(fetchResult.NamespaceTemplate.Error) {
				return fetchResult.NamespaceTemplate.Error
			}
			if fetchResult.ClusterTemplate.Error != nil && !apierrors.IsNotFound(fetchResult.ClusterTemplate.Error) {
				return fetchResult.ClusterTemplate.Error
			}
			return nil
		}(),
	})

	// Build model caches observation from fetched data
	obs.modelCaches = buildModelCachesObservation(modelCachesObservationInputs{
		modelSources:     obs.template.modelSources,
		availableCaches:  fetchResult.ModelCaches.Result,
		storageClassName: cache.Spec.StorageClassName,
	})

	return obs, nil
}

// ============================================================================
// PLAN PHASE
// ============================================================================

// Plan determines what Kubernetes objects should be created or updated
// based on the current observation.
func (r *Reconciler) Plan(ctx context.Context, cache *aimv1alpha1.AIMTemplateCache, obs Observation) (controllerutils.PlanResult, error) {
	var objects []client.Object

	// Only create model caches if template was found
	if !obs.template.found {
		return controllerutils.PlanResult{Apply: objects}, nil
	}

	// Create missing model caches with owner references
	for _, mc := range buildMissingModelCaches(cache, obs) {
		if err := controllerutil.SetOwnerReference(cache, mc, r.Scheme); err != nil {
			return controllerutils.PlanResult{}, fmt.Errorf("set owner reference for model cache: %w", err)
		}
		objects = append(objects, mc)
	}

	return controllerutils.PlanResult{Apply: objects}, nil
}

// ----- Plan Helpers -----

// buildMissingModelCaches creates AIMModelCache objects for missing caches.
func buildMissingModelCaches(tc *aimv1alpha1.AIMTemplateCache, obs Observation) []*aimv1alpha1.AIMModelCache {
	var caches []*aimv1alpha1.AIMModelCache

	for _, cache := range obs.modelCaches.missingCaches {
		// Sanitize the model name for use as a Kubernetes resource name
		// The original model name (with capitalization) is preserved in SourceURI for matching
		// Note: Don't add "-cache" suffix here as the ModelCache controller will add it when creating the PVC
		// Replace dots with dashes first to ensure DNS-compliant names (dots cause warnings in Pod names)
		nameWithoutDots := strings.ReplaceAll(cache.Name, ".", "-")
		sanitizedName := utils.MakeRFC1123Compliant(nameWithoutDots)

		sourceModel, _ := utils.SanitizeLabelValue(cache.Name)

		caches = append(caches,
			&aimv1alpha1.AIMModelCache{
				TypeMeta: metav1.TypeMeta{APIVersion: "aimv1alpha1", Kind: "AIMModelCache"},
				ObjectMeta: metav1.ObjectMeta{
					Name:      sanitizedName,
					Namespace: tc.Namespace,
					Labels: map[string]string{
						"template-created":              "true", // Backward compatibility
						constants.LabelKeyTemplateCache: tc.Name,
						constants.LabelKeySourceModel:   sourceModel,
					},
				},
				Spec: aimv1alpha1.AIMModelCacheSpec{
					StorageClassName:  tc.Spec.StorageClassName,
					SourceURI:         cache.SourceURI,
					Size:              *cache.Size,
					Env:               tc.Spec.Env,
					RuntimeConfigName: tc.Spec.RuntimeConfigName,
				},
			},
		)
	}

	return caches
}

// ============================================================================
// PROJECT PHASE
// ============================================================================

// Project updates the cache status based on observations.
func (r *Reconciler) Project(status *aimv1alpha1.AIMTemplateCacheStatus, cm *controllerutils.ConditionManager, obs Observation) {
	if status == nil {
		return
	}

	// Project template condition
	r.projectTemplateCondition(cm, obs)

	// Project overall status
	r.projectOverallStatus(status, obs)
}

// ----- Project Helpers -----

// projectTemplateCondition sets the TemplateFound condition.
func (r *Reconciler) projectTemplateCondition(cm *controllerutils.ConditionManager, obs Observation) {
	if !obs.template.found {
		// TODO set degraded and return
		cm.Set(aimv1alpha1.AIMTemplateCacheConditionTemplateFound, metav1.ConditionFalse, aimv1alpha1.AIMTemplateReasonAwaitingTemplate,
			fmt.Sprintf("Waiting for template to be created: %s", obs.template.error), controllerutils.LevelWarning)
	} else {
		cm.Set(aimv1alpha1.AIMTemplateCacheConditionTemplateFound, metav1.ConditionTrue, aimv1alpha1.AIMTemplateReasonTemplateFound, "", controllerutils.LevelNone)
	}
}

// projectOverallStatus determines the overall status enum.
func (r *Reconciler) projectOverallStatus(status *aimv1alpha1.AIMTemplateCacheStatus, obs Observation) {
	// If template not found, status is Pending
	if !obs.template.found {
		status.Status = constants.AIMStatusPending
		return
	}

	// Determine status from cache statuses - use the worst (lowest priority) status
	statusValues := slices.Collect(maps.Values(obs.modelCaches.cacheStatus))
	if len(statusValues) > 0 {
		worstCacheStatus := slices.MinFunc(statusValues, constants.CompareAIMStatus)
		status.Status = worstCacheStatus
	} else {
		// If there are no caches to track, mark as Pending
		status.Status = constants.AIMStatusPending
	}

	// Override if all caches are available
	if obs.modelCaches.allCachesAvailable {
		status.Status = constants.AIMStatusReady
	}
}
