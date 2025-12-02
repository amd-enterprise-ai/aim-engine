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

package aimclustermodelsource

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

// ClusterModelSourceReconciler implements domain reconciliation for AIMClusterModelSource.
type ClusterModelSourceReconciler struct {
	Clientset kubernetes.Interface
	Scheme    *runtime.Scheme
}

// ============================================================================
// FETCH
// ============================================================================

// ClusterModelSourceFetchResult contains data gathered during the Fetch phase.
type ClusterModelSourceFetchResult struct {
	existingModels []aimv1alpha1.AIMClusterModel
	registryImages []RegistryImage
	registryError  error // Non-fatal, captured for status
}

// Fetch gathers all data needed for reconciliation.
// This includes existing AIMClusterModel resources and available images from the registry.
func (r *ClusterModelSourceReconciler) Fetch(
	ctx context.Context,
	c client.Client,
	source *aimv1alpha1.AIMClusterModelSource,
) (ClusterModelSourceFetchResult, error) {
	result := ClusterModelSourceFetchResult{}

	// 1. List existing models owned by this source
	modelList := &aimv1alpha1.AIMClusterModelList{}
	if err := c.List(ctx, modelList, client.MatchingLabels{
		constants.LabelKeyModelSource: source.Name,
	}); err != nil {
		return result, fmt.Errorf("failed to list models: %w", err)
	}
	result.existingModels = modelList.Items

	// 2. Check if we can skip registry queries (all filters are exact image references)
	staticImages := extractStaticImages(source.Spec.Filters)
	if len(staticImages) > 0 && len(staticImages) == len(source.Spec.Filters) {
		// All filters are exact references - no registry query needed
		result.registryImages = staticImages
		return result, nil
	}

	// 3. Query registry for available images
	registryClient := NewRegistryClient(r.Clientset, constants.GetOperatorNamespace())
	images, err := registryClient.ListImages(ctx, source.Spec)
	if err != nil {
		// Capture error but don't fail - will be handled in Observe
		result.registryError = err
	} else {
		result.registryImages = images
	}

	return result, nil
}

// ============================================================================
// OBSERVE
// ============================================================================

// ClusterModelSourceObservation contains interpreted state from fetched data.
type ClusterModelSourceObservation struct {
	filteredImages    []RegistryImage                         // After all filters applied
	newImages         []RegistryImage                         // Need model creation
	existingByURI     map[string]*aimv1alpha1.AIMClusterModel // Lookup map
	registryReachable bool
	registryError     error
	totalScanned      int
	totalFiltered     int
}

// Observe interprets fetched data into domain observations.
// This is a pure function - no client calls allowed.
func (r *ClusterModelSourceReconciler) Observe(
	ctx context.Context,
	source *aimv1alpha1.AIMClusterModelSource,
	fetched ClusterModelSourceFetchResult,
) (ClusterModelSourceObservation, error) {
	obs := ClusterModelSourceObservation{
		existingByURI: make(map[string]*aimv1alpha1.AIMClusterModel),
	}

	// Build lookup map of existing models by image URI
	for i := range fetched.existingModels {
		model := &fetched.existingModels[i]
		obs.existingByURI[model.Spec.Image] = model
	}

	// Handle registry errors
	if fetched.registryError != nil {
		obs.registryReachable = false
		obs.registryError = fetched.registryError
		return obs, nil
	}

	obs.registryReachable = true
	obs.totalScanned = len(fetched.registryImages)

	// Apply filters to registry images
	for _, img := range fetched.registryImages {
		if matchesFilters(img, source.Spec.Filters, source.Spec.Versions) {
			obs.filteredImages = append(obs.filteredImages, img)

			// Check if model already exists
			imageURI := img.ToImageURI()
			if _, exists := obs.existingByURI[imageURI]; !exists {
				obs.newImages = append(obs.newImages, img)
			}
		}
	}
	obs.totalFiltered = len(obs.filteredImages)

	return obs, nil
}

// ============================================================================
// PLAN
// ============================================================================

// Plan derives desired state changes based on observations.
// This is a pure function - returns only what should be created/deleted.
func (r *ClusterModelSourceReconciler) Plan(
	ctx context.Context,
	source *aimv1alpha1.AIMClusterModelSource,
	obs ClusterModelSourceObservation,
) (controllerutils.PlanResult, error) {
	result := controllerutils.PlanResult{}

	// Only create models for new images (append-only lifecycle)
	for _, img := range obs.newImages {
		model := buildClusterModel(source, img)

		// Set owner reference (non-blocking deletion)
		if err := controllerutil.SetOwnerReference(
			source, model, r.Scheme,
			controllerutil.WithBlockOwnerDeletion(false),
		); err != nil {
			return result, fmt.Errorf("failed to set owner reference: %w", err)
		}

		result.Apply = append(result.Apply, model)
	}

	// Never delete - append-only lifecycle
	return result, nil
}

// ============================================================================
// PROJECT
// ============================================================================

// Project updates status based on observations.
// This mutates the status but doesn't write to the API server.
func (r *ClusterModelSourceReconciler) Project(
	status *aimv1alpha1.AIMClusterModelSourceStatus,
	cm *controllerutils.ConditionManager,
	obs ClusterModelSourceObservation,
) {
	helper := controllerutils.NewStatusHelper(status, cm)

	// RegistryReachable condition
	if obs.registryReachable {
		cm.MarkTrue("RegistryReachable", "Connected",
			"Successfully connected to registry",
			controllerutils.AsInfo())
	} else {
		cm.MarkFalse("RegistryReachable", "ConnectionFailed",
			fmt.Sprintf("Failed to connect to registry: %v", obs.registryError),
			controllerutils.AsWarning())
	}

	// Syncing condition
	if len(obs.newImages) > 0 {
		cm.MarkTrue("Syncing", "CreatingModels",
			fmt.Sprintf("Creating %d new model(s)", len(obs.newImages)),
			controllerutils.AsInfo())
	} else {
		cm.MarkFalse("Syncing", "SyncComplete",
			"No new models to create",
			controllerutils.AsInfo())
	}

	// Ready condition and overall status
	if !obs.registryReachable {
		if len(obs.existingByURI) > 0 {
			helper.Degraded("RegistryUnreachable",
				fmt.Sprintf("Registry unreachable but %d existing model(s) remain", len(obs.existingByURI)))
		} else {
			helper.Failed("RegistryUnreachable", "Cannot reach registry")
		}
	} else if obs.totalFiltered == 0 {
		cm.MarkFalse("Ready", "NoMatches",
			"No images match the configured filters",
			controllerutils.AsInfo())
		status.Status = string(constants.AIMStatusPending)
	} else {
		cm.MarkTrue("Ready", "ModelsDiscovered",
			fmt.Sprintf("Discovered %d model(s) matching filters", len(obs.filteredImages)),
			controllerutils.AsInfo())
		status.Status = string(constants.AIMStatusReady)
	}

	// Update metrics
	status.DiscoveredModels = len(obs.existingByURI) + len(obs.newImages)

	// Update LastSyncTime on every successful sync
	// Status updates don't trigger reconciliations due to GenerationChangedPredicate
	if obs.registryReachable {
		now := metav1.NewTime(time.Now())
		status.LastSyncTime = &now
	}

	// Update discovered images summary (limited to most recent 50)
	status.DiscoveredImages = buildDiscoveredImagesSummary(obs.filteredImages, obs.existingByURI)
}
