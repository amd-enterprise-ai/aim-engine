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

package aimclustermodelsource

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

// ClusterModelSourceReconciler implements domain reconciliation for AIMClusterModelSource.
type ClusterModelSourceReconciler struct {
	Clientset         kubernetes.Interface
	Scheme            *runtime.Scheme
	OperatorNamespace string
}

// ============================================================================
// FETCH
// ============================================================================

type ClusterModelSourceFetch struct {
	source *aimv1alpha1.AIMClusterModelSource

	// existingModels are all AIMClusterModels in the cluster, regardless of creator,
	// so overlapping sources respect a model that already exists for an image.
	existingModels controllerutils.FetchResult[*aimv1alpha2.AIMClusterModelList]

	// ownerRefRewriteErr is kept apart from existingModels. ComposeState reads a
	// failed list as no existing models and would plan every image and
	// declaration as new, re-owning or duplicating models this source does not
	// own.
	ownerRefRewriteErr error

	// filterResults contains per-filter registry query results
	filterResults []FilterResult
}

func (r *ClusterModelSourceReconciler) FetchRemoteState(
	ctx context.Context,
	c client.Client,
	reconcileCtx controllerutils.ReconcileContext[*aimv1alpha1.AIMClusterModelSource],
) ClusterModelSourceFetch {
	source := reconcileCtx.Object
	ctx = log.IntoContext(ctx, log.FromContext(ctx).WithValues(
		"phase", "fetch",
		"source", source.Name,
	))

	fetch := ClusterModelSourceFetch{source: source}

	// 1. List all cluster models (not just this source's) so existing images are respected.
	fetch.existingModels = controllerutils.FetchList(ctx, c,
		&aimv1alpha2.AIMClusterModelList{},
	)

	// 2. Query registry for each filter
	registryClient := NewRegistryClient(r.Clientset, r.OperatorNamespace)
	for _, filter := range EffectiveFilters(source.Spec) {
		result := registryClient.FetchFilter(ctx, source.Spec, filter)
		fetch.filterResults = append(fetch.filterResults, result)
	}

	// Patched from the models this list already loaded. Discovered models are
	// not re-applied, so this is what moves their owner reference to v1alpha2.
	// A reference whose UID is not this source's is left unchanged.
	if fetch.existingModels.OK() {
		for i := range fetch.existingModels.Value.Items {
			model := &fetch.existingModels.Value.Items[i]
			if err := controllerutils.RewriteV1alpha1OwnerReference(ctx, c, model, source.GetUID()); err != nil {
				fetch.ownerRefRewriteErr = err
				break
			}
		}
	}

	return fetch
}

// GetComponentHealth implements ComponentHealthProvider on FetchResult.
// This follows the aimmodel pattern where fetch results provide health directly.
func (fetch ClusterModelSourceFetch) GetComponentHealth() []controllerutils.ComponentHealth {
	existingModelsHealth := fetch.existingModels.ToComponentHealth("ExistingModels",
		func(list *aimv1alpha2.AIMClusterModelList) controllerutils.ComponentHealth {
			return controllerutils.ComponentHealth{
				State:   constants.AIMStatusReady,
				Reason:  "Listed",
				Message: fmt.Sprintf("Found %d cluster models", len(list.Items)),
			}
		},
	)
	if fetch.ownerRefRewriteErr != nil {
		existingModelsHealth = controllerutils.ComponentHealth{
			Component: "ExistingModels",
			Errors:    []error{fetch.ownerRefRewriteErr},
		}
	}

	filterHealth := composeFilterHealth(fetch.filterResults, len(fetch.source.Spec.Models) > 0)

	return []controllerutils.ComponentHealth{
		existingModelsHealth,
		filterHealth,
	}
}

// composeFilterHealth aggregates filter results into a single ComponentHealth.
func composeFilterHealth(results []FilterResult, hasModels bool) controllerutils.ComponentHealth {
	if len(results) == 0 {
		if hasModels {
			return controllerutils.ComponentHealth{
				Component: "Filters",
				State:     constants.AIMStatusReady,
				Reason:    "NoRegistryFilters",
				Message:   "Only static model declarations are configured",
			}
		}
		return controllerutils.ComponentHealth{
			Component: "Filters",
			State:     constants.AIMStatusProgressing,
			Reason:    "NoFilters",
			Message:   "No filters configured",
		}
	}

	var errCount int
	for _, r := range results {
		if r.Error != nil {
			errCount++
		}
	}

	total := len(results)
	if errCount == 0 {
		return controllerutils.ComponentHealth{
			Component: "Filters",
			State:     constants.AIMStatusReady,
			Reason:    "AllFiltersSucceeded",
			Message:   fmt.Sprintf("All %d filters succeeded", total),
		}
	}
	if errCount < total {
		return controllerutils.ComponentHealth{
			Component: "Filters",
			State:     constants.AIMStatusDegraded,
			Reason:    "SomeFiltersFailed",
			Message:   fmt.Sprintf("%d of %d filters had errors", errCount, total),
		}
	}
	return controllerutils.ComponentHealth{
		Component: "Filters",
		State:     constants.AIMStatusFailed,
		Reason:    "AllFiltersFailed",
		Message:   fmt.Sprintf("All %d filters failed", total),
	}
}

// ============================================================================
// OBSERVATION
// ============================================================================

// ClusterModelSourceObservation embeds the fetch result.
// Additional computed fields are added for PlanResources and DecorateStatus.
type ClusterModelSourceObservation struct {
	ClusterModelSourceFetch

	// Computed during ComposeState for PlanResources
	newImages        []RegistryImage
	desiredModels    []*aimv1alpha2.AIMClusterModel
	newDeclaredCount int
	existingByURI    map[string]*aimv1alpha2.AIMClusterModel
	existingByModel  map[string]*aimv1alpha2.AIMClusterModel

	// Computed during ComposeState for DecorateStatus
	totalFiltered     int
	totalDiscovered   int
	filtersWithErrors int
	buildErr          error
}

func (r *ClusterModelSourceReconciler) ComposeState(
	_ context.Context,
	_ controllerutils.ReconcileContext[*aimv1alpha1.AIMClusterModelSource],
	fetch ClusterModelSourceFetch,
) ClusterModelSourceObservation {
	obs := ClusterModelSourceObservation{
		ClusterModelSourceFetch: fetch,
		existingByURI:           make(map[string]*aimv1alpha2.AIMClusterModel),
		existingByModel:         make(map[string]*aimv1alpha2.AIMClusterModel),
	}

	// Build a lookup of images that already have an AIMClusterModel, keyed by image URI.
	if fetch.existingModels.OK() {
		for i := range fetch.existingModels.Value.Items {
			model := &fetch.existingModels.Value.Items[i]
			if model.Spec.Image != "" {
				obs.existingByURI[model.Spec.Image] = model
			}
			if model.Spec.ModelID != "" {
				obs.existingByModel[model.Spec.ModelID] = model
			}
		}
	}

	// Determine which discovered images still need a model.
	source := fetch.source
	maxModels := source.GetMaxModels()

	// coveredCount is discovered images that already have a model; with newImages
	// it bounds new models against maxModels.
	var coveredCount int
	for _, result := range fetch.filterResults {
		if result.Error != nil {
			obs.filtersWithErrors++
		}
		for _, img := range result.Images {
			obs.totalFiltered++
			imageURI := img.ToImageURI()
			if _, exists := obs.existingByURI[imageURI]; exists {
				// Model already exists for this image; respect it (never re-own).
				coveredCount++
				continue
			}
			if coveredCount+len(obs.newImages) < maxModels {
				obs.newImages = append(obs.newImages, img)
			}
		}
	}
	for _, declaration := range source.Spec.Models {
		obs.totalFiltered++
		existing, covered := obs.existingByModel[declaration.ModelID]
		if covered {
			coveredCount++
			// A model materialized by a *different* source (or hand-authored)
			// is respected, never re-owned — same rule as image discovery.
			if !isModelOwnedBySource(existing, source.Name) {
				continue
			}
		} else if coveredCount+len(obs.newImages)+obs.newDeclaredCount >= maxModels {
			continue
		}

		desired, err := buildDeclaredClusterModel(source, declaration)
		if err != nil {
			obs.buildErr = err
			continue
		}
		if covered {
			// Keep syncing the model this source already owns. Declarations are
			// desired state the user edits here, unlike discovered images.
			desired.Name = existing.Name
		} else {
			obs.newDeclaredCount++
		}
		obs.desiredModels = append(obs.desiredModels, desired)
	}

	obs.totalDiscovered = coveredCount + len(obs.newImages) + obs.newDeclaredCount
	return obs
}

// ============================================================================
// PLAN
// ============================================================================

func (r *ClusterModelSourceReconciler) PlanResources(
	ctx context.Context,
	_ controllerutils.ReconcileContext[*aimv1alpha1.AIMClusterModelSource],
	obs ClusterModelSourceObservation,
) controllerutils.PlanResult {
	logger := log.FromContext(ctx).WithName("plan")
	source := obs.source

	result := controllerutils.PlanResult{}

	// Discovered images are a snapshot of an external registry, so only images
	// without a model are planned — an existing model is never re-applied.
	for _, img := range obs.newImages {
		model := buildClusterModel(source, img)
		result.Apply(model)
	}
	// Declarations are desired state the user edits in this spec, so models this
	// source owns are re-applied every reconcile and stay in sync with their
	// declaration.
	for _, model := range obs.desiredModels {
		result.Apply(model)
	}

	logger.V(1).Info("planning models", "newImageCount", len(obs.newImages), "declaredCount", len(obs.desiredModels))

	// Never delete. Removing a filter or a declaration leaves its model behind
	// for an operator to remove deliberately.
	return result
}

// ============================================================================
// STATUS
// ============================================================================

func (r *ClusterModelSourceReconciler) DecorateStatus(
	status *aimv1alpha1.AIMClusterModelSourceStatus,
	cm *controllerutils.ConditionManager,
	obs ClusterModelSourceObservation,
) {
	// State engine already set:
	// - Ready condition
	// - FiltersReady condition
	// - ExistingModelsReady condition
	// - status.Status (Ready/Degraded/Failed/Progressing)

	// Add domain-specific fields
	status.DiscoveredModels = obs.totalDiscovered
	status.AvailableModels = obs.totalFiltered
	status.ModelsLimitReached = obs.totalFiltered > obs.totalDiscovered

	// Record the sync attempt so the controller's syncDue gate can throttle the
	// next registry listing and detect spec changes.
	now := metav1.Now()
	status.LastSyncTime = &now
	if obs.source != nil {
		status.ObservedGeneration = obs.source.Generation
	}

	// Add MaxModelsLimitReached condition (optional, informational)
	if status.ModelsLimitReached {
		cm.Set("MaxModelsLimitReached", metav1.ConditionTrue,
			"LimitReached",
			fmt.Sprintf("Limit reached: %d created, %d available",
				status.DiscoveredModels, status.AvailableModels),
			controllerutils.AsInfo(),
		)
	} else {
		cm.Set("MaxModelsLimitReached", metav1.ConditionFalse,
			"WithinLimit",
			fmt.Sprintf("Created %d models, within limit", status.DiscoveredModels),
			controllerutils.AsInfo(),
		)
	}

	// A declaration that cannot be turned into a valid model name is a user
	// configuration error, not a transient one. Surface it rather than silently
	// dropping the declaration from the plan.
	if obs.buildErr != nil {
		cm.Set("ModelDeclarationsValid", metav1.ConditionFalse,
			"InvalidDeclaration",
			obs.buildErr.Error(),
			controllerutils.AsWarning(),
		)
	} else {
		declarationCount := 0
		if obs.source != nil {
			declarationCount = len(obs.source.Spec.Models)
		}
		cm.Set("ModelDeclarationsValid", metav1.ConditionTrue,
			"DeclarationsValid",
			fmt.Sprintf("All %d model declarations are valid", declarationCount),
			controllerutils.AsInfo(),
		)
	}
}
