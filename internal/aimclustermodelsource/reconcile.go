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

	// existingModels are AIMClusterModels owned by this source
	existingModels controllerutils.FetchResult[*aimv1alpha1.AIMClusterModelList]

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

	// 1. List existing models owned by this source
	fetch.existingModels = controllerutils.FetchList(ctx, c,
		&aimv1alpha1.AIMClusterModelList{},
		client.MatchingLabels{LabelKeyModelSource: source.Name},
	)

	// 2. Query registry for each filter
	registryClient := NewRegistryClient(r.Clientset, r.OperatorNamespace)
	for _, filter := range source.Spec.Filters {
		result := registryClient.FetchFilter(ctx, source.Spec, filter)
		fetch.filterResults = append(fetch.filterResults, result)
	}

	return fetch
}

// GetComponentHealth implements ComponentHealthProvider on FetchResult.
// This follows the aimmodel pattern where fetch results provide health directly.
func (fetch ClusterModelSourceFetch) GetComponentHealth() []controllerutils.ComponentHealth {
	existingModelsHealth := fetch.existingModels.ToComponentHealth("ExistingModels",
		func(list *aimv1alpha1.AIMClusterModelList) controllerutils.ComponentHealth {
			return controllerutils.ComponentHealth{
				State:   constants.AIMStatusReady,
				Reason:  "Listed",
				Message: fmt.Sprintf("Found %d existing models", len(list.Items)),
			}
		},
	)

	filterHealth := composeFilterHealth(fetch.filterResults)

	return []controllerutils.ComponentHealth{
		existingModelsHealth,
		filterHealth,
	}
}

// composeFilterHealth aggregates filter results into a single ComponentHealth.
func composeFilterHealth(results []FilterResult) controllerutils.ComponentHealth {
	if len(results) == 0 {
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
	newImages     []RegistryImage
	existingByURI map[string]*aimv1alpha1.AIMClusterModel

	// Computed during ComposeState for DecorateStatus
	totalFiltered     int
	totalDiscovered   int
	filtersWithErrors int
}

func (r *ClusterModelSourceReconciler) ComposeState(
	_ context.Context,
	_ controllerutils.ReconcileContext[*aimv1alpha1.AIMClusterModelSource],
	fetch ClusterModelSourceFetch,
) ClusterModelSourceObservation {
	obs := ClusterModelSourceObservation{
		ClusterModelSourceFetch: fetch,
		existingByURI:           make(map[string]*aimv1alpha1.AIMClusterModel),
	}

	// Build lookup map from existing models
	if fetch.existingModels.OK() {
		for i := range fetch.existingModels.Value.Items {
			model := &fetch.existingModels.Value.Items[i]
			obs.existingByURI[model.Spec.Image] = model
		}
	}

	// Process filter results - determine new images to create
	// Access source from embedded fetch result
	source := fetch.source
	maxModels := source.GetMaxModels()
	existingCount := len(obs.existingByURI)

	for _, result := range fetch.filterResults {
		if result.Error != nil {
			obs.filtersWithErrors++
		}
		for _, img := range result.Images {
			obs.totalFiltered++
			imageURI := img.ToImageURI()
			if _, exists := obs.existingByURI[imageURI]; !exists {
				if existingCount+len(obs.newImages) < maxModels {
					obs.newImages = append(obs.newImages, img)
				}
			}
		}
	}

	obs.totalDiscovered = len(obs.existingByURI) + len(obs.newImages)
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

	// Only create models for new images (append-only lifecycle)
	result := controllerutils.PlanResult{}
	for _, img := range obs.newImages {
		model := buildClusterModel(source, img)
		result.Apply(model)
	}

	logger.V(1).Info("planning models", "newCount", len(obs.newImages))

	// Never delete - append-only lifecycle
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

	// Update sync time
	now := metav1.Now()
	status.LastSyncTime = &now

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
}
