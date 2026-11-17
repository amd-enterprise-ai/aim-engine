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
	"errors"
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8sptr "k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

// Test constants
const testSourceName = "test-source"

// ============================================================================
// GetComponentHealth Tests
// ============================================================================

func TestClusterModelSourceFetch_GetComponentHealth_AllSuccess(t *testing.T) {
	fetch := ClusterModelSourceFetch{
		source: &aimv1alpha1.AIMClusterModelSource{
			ObjectMeta: metav1.ObjectMeta{Name: testSourceName},
		},
		existingModels: controllerutils.FetchResult[*aimv1alpha2.AIMClusterModelList]{
			Value: &aimv1alpha2.AIMClusterModelList{
				Items: []aimv1alpha2.AIMClusterModel{
					{ObjectMeta: metav1.ObjectMeta{Name: "model-1"}},
					{ObjectMeta: metav1.ObjectMeta{Name: "model-2"}},
				},
			},
		},
		filterResults: []FilterResult{
			{
				Filter: aimv1alpha1.ModelSourceFilter{Image: "org/model-*"},
				Images: []RegistryImage{{Registry: "ghcr.io", Repository: "org/model-1", Tag: "1.0.0"}},
				Error:  nil,
			},
		},
	}

	health := fetch.GetComponentHealth()

	if len(health) != 2 {
		t.Fatalf("expected 2 health components, got %d", len(health))
	}

	// Check ExistingModels health
	existingHealth := health[0]
	if existingHealth.Component != "ExistingModels" {
		t.Errorf("expected component 'ExistingModels', got %q", existingHealth.Component)
	}
	if existingHealth.State != constants.AIMStatusReady {
		t.Errorf("expected state Ready, got %v", existingHealth.State)
	}

	// Check Filters health
	filterHealth := health[1]
	if filterHealth.Component != "Filters" {
		t.Errorf("expected component 'Filters', got %q", filterHealth.Component)
	}
	if filterHealth.State != constants.AIMStatusReady {
		t.Errorf("expected state Ready, got %v", filterHealth.State)
	}
}

func TestClusterModelSourceFetch_GetComponentHealth_SomeFiltersFailed(t *testing.T) {
	fetch := ClusterModelSourceFetch{
		source: &aimv1alpha1.AIMClusterModelSource{
			ObjectMeta: metav1.ObjectMeta{Name: testSourceName},
		},
		existingModels: controllerutils.FetchResult[*aimv1alpha2.AIMClusterModelList]{
			Value: &aimv1alpha2.AIMClusterModelList{},
		},
		filterResults: []FilterResult{
			{
				Filter: aimv1alpha1.ModelSourceFilter{Image: "org/model-a"},
				Images: []RegistryImage{{Registry: "ghcr.io", Repository: "org/model-a", Tag: "1.0.0"}},
				Error:  nil,
			},
			{
				Filter: aimv1alpha1.ModelSourceFilter{Image: "org/model-b"},
				Images: nil,
				Error:  errors.New("registry unreachable"),
			},
		},
	}

	health := fetch.GetComponentHealth()
	filterHealth := health[1]

	if filterHealth.State != constants.AIMStatusDegraded {
		t.Errorf("expected state Degraded for partial failure, got %v", filterHealth.State)
	}
	if filterHealth.Reason != "SomeFiltersFailed" {
		t.Errorf("expected reason 'SomeFiltersFailed', got %q", filterHealth.Reason)
	}
}

func TestClusterModelSourceFetch_GetComponentHealth_AllFiltersFailed(t *testing.T) {
	fetch := ClusterModelSourceFetch{
		source: &aimv1alpha1.AIMClusterModelSource{
			ObjectMeta: metav1.ObjectMeta{Name: testSourceName},
		},
		existingModels: controllerutils.FetchResult[*aimv1alpha2.AIMClusterModelList]{
			Value: &aimv1alpha2.AIMClusterModelList{},
		},
		filterResults: []FilterResult{
			{
				Filter: aimv1alpha1.ModelSourceFilter{Image: "org/model-a"},
				Error:  errors.New("registry unreachable"),
			},
			{
				Filter: aimv1alpha1.ModelSourceFilter{Image: "org/model-b"},
				Error:  errors.New("auth failed"),
			},
		},
	}

	health := fetch.GetComponentHealth()
	filterHealth := health[1]

	if filterHealth.State != constants.AIMStatusFailed {
		t.Errorf("expected state Failed for all filters failed, got %v", filterHealth.State)
	}
	if filterHealth.Reason != "AllFiltersFailed" {
		t.Errorf("expected reason 'AllFiltersFailed', got %q", filterHealth.Reason)
	}
}

func TestClusterModelSourceFetch_GetComponentHealth_NoFilters(t *testing.T) {
	fetch := ClusterModelSourceFetch{
		source: &aimv1alpha1.AIMClusterModelSource{
			ObjectMeta: metav1.ObjectMeta{Name: testSourceName},
		},
		existingModels: controllerutils.FetchResult[*aimv1alpha2.AIMClusterModelList]{
			Value: &aimv1alpha2.AIMClusterModelList{},
		},
		filterResults: []FilterResult{},
	}

	health := fetch.GetComponentHealth()
	filterHealth := health[1]

	if filterHealth.State != constants.AIMStatusProgressing {
		t.Errorf("expected state Progressing for no filters, got %v", filterHealth.State)
	}
	if filterHealth.Reason != "NoFilters" {
		t.Errorf("expected reason 'NoFilters', got %q", filterHealth.Reason)
	}
}

// ============================================================================
// ComposeState Tests
// ============================================================================

func TestComposeState_Basic(t *testing.T) {
	reconciler := &ClusterModelSourceReconciler{
		Scheme: runtime.NewScheme(),
	}

	source := &aimv1alpha1.AIMClusterModelSource{
		ObjectMeta: metav1.ObjectMeta{Name: testSourceName},
		Spec: aimv1alpha1.AIMClusterModelSourceSpec{
			MaxModels: k8sptr.To(100),
		},
	}

	fetch := ClusterModelSourceFetch{
		source: source,
		existingModels: controllerutils.FetchResult[*aimv1alpha2.AIMClusterModelList]{
			Value: &aimv1alpha2.AIMClusterModelList{
				Items: []aimv1alpha2.AIMClusterModel{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "existing-1"},
						Spec:       aimv1alpha1.AIMModelSpec{Image: "ghcr.io/org/model-1:1.0.0"},
					},
				},
			},
		},
		filterResults: []FilterResult{
			{
				Images: []RegistryImage{
					{Registry: "ghcr.io", Repository: "org/model-1", Tag: "1.0.0"}, // existing
					{Registry: "ghcr.io", Repository: "org/model-2", Tag: "1.0.0"}, // new
				},
			},
		},
	}

	reconcileCtx := controllerutils.ReconcileContext[*aimv1alpha1.AIMClusterModelSource]{
		Object: source,
	}
	obs := reconciler.ComposeState(context.Background(), reconcileCtx, fetch)

	// Check existing map
	if len(obs.existingByURI) != 1 {
		t.Errorf("expected 1 existing model in map, got %d", len(obs.existingByURI))
	}

	// Check new images (should not include existing)
	if len(obs.newImages) != 1 {
		t.Errorf("expected 1 new image, got %d", len(obs.newImages))
	}
	if obs.newImages[0].Repository != "org/model-2" {
		t.Errorf("expected new image to be model-2, got %s", obs.newImages[0].Repository)
	}

	// Check totals
	if obs.totalFiltered != 2 {
		t.Errorf("expected totalFiltered=2, got %d", obs.totalFiltered)
	}
	if obs.totalDiscovered != 2 { // 1 existing + 1 new
		t.Errorf("expected totalDiscovered=2, got %d", obs.totalDiscovered)
	}
}

func TestComposeState_MaxModelsLimit(t *testing.T) {
	reconciler := &ClusterModelSourceReconciler{
		Scheme: runtime.NewScheme(),
	}

	source := &aimv1alpha1.AIMClusterModelSource{
		ObjectMeta: metav1.ObjectMeta{Name: testSourceName},
		Spec: aimv1alpha1.AIMClusterModelSourceSpec{
			MaxModels: k8sptr.To(2), // Limit to 2 models
		},
	}

	fetch := ClusterModelSourceFetch{
		source: source,
		existingModels: controllerutils.FetchResult[*aimv1alpha2.AIMClusterModelList]{
			Value: &aimv1alpha2.AIMClusterModelList{
				Items: []aimv1alpha2.AIMClusterModel{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "existing-1"},
						Spec:       aimv1alpha1.AIMModelSpec{Image: "ghcr.io/org/model-1:1.0.0"},
					},
				},
			},
		},
		filterResults: []FilterResult{
			{
				Images: []RegistryImage{
					{Registry: "ghcr.io", Repository: "org/model-1", Tag: "1.0.0"}, // existing, covered (budget=1)
					{Registry: "ghcr.io", Repository: "org/model-2", Tag: "1.0.0"}, // new, added (budget=2)
					{Registry: "ghcr.io", Repository: "org/model-3", Tag: "1.0.0"}, // skipped (limit reached)
					{Registry: "ghcr.io", Repository: "org/model-4", Tag: "1.0.0"}, // skipped
				},
			},
		},
	}

	reconcileCtx := controllerutils.ReconcileContext[*aimv1alpha1.AIMClusterModelSource]{
		Object: source,
	}
	obs := reconciler.ComposeState(context.Background(), reconcileCtx, fetch)

	// One discovered image is already covered by an existing model, leaving room
	// for exactly 1 new model before maxModels (2) is reached.
	if len(obs.newImages) != 1 {
		t.Errorf("expected 1 new image due to limit, got %d", len(obs.newImages))
	}
	if len(obs.newImages) == 1 && obs.newImages[0].Repository != "org/model-2" {
		t.Errorf("expected new image to be model-2, got %s", obs.newImages[0].Repository)
	}

	// Total filtered should count all discovered images
	if obs.totalFiltered != 4 {
		t.Errorf("expected totalFiltered=4, got %d", obs.totalFiltered)
	}
}

func TestComposeState_FilterErrors(t *testing.T) {
	reconciler := &ClusterModelSourceReconciler{
		Scheme: runtime.NewScheme(),
	}

	source := &aimv1alpha1.AIMClusterModelSource{
		ObjectMeta: metav1.ObjectMeta{Name: testSourceName},
	}

	fetch := ClusterModelSourceFetch{
		source: source,
		existingModels: controllerutils.FetchResult[*aimv1alpha2.AIMClusterModelList]{
			Value: &aimv1alpha2.AIMClusterModelList{},
		},
		filterResults: []FilterResult{
			{
				Images: []RegistryImage{{Registry: "ghcr.io", Repository: "org/model-1", Tag: "1.0.0"}},
				Error:  nil,
			},
			{
				Images: nil,
				Error:  errors.New("auth failed"),
			},
			{
				Images: nil,
				Error:  errors.New("network error"),
			},
		},
	}

	reconcileCtx := controllerutils.ReconcileContext[*aimv1alpha1.AIMClusterModelSource]{
		Object: source,
	}
	obs := reconciler.ComposeState(context.Background(), reconcileCtx, fetch)

	if obs.filtersWithErrors != 2 {
		t.Errorf("expected filtersWithErrors=2, got %d", obs.filtersWithErrors)
	}
}

// ============================================================================
// PlanResources Tests
// ============================================================================

func TestPlanResources_CreatesNewModels(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = aimv1alpha1.AddToScheme(scheme)

	reconciler := &ClusterModelSourceReconciler{
		Scheme: scheme,
	}

	source := &aimv1alpha1.AIMClusterModelSource{
		ObjectMeta: metav1.ObjectMeta{
			Name: testSourceName,
			UID:  "test-uid",
		},
	}

	obs := ClusterModelSourceObservation{
		ClusterModelSourceFetch: ClusterModelSourceFetch{
			source: source,
		},
		newImages: []RegistryImage{
			{Registry: "ghcr.io", Repository: "org/model-1", Tag: "1.0.0"},
			{Registry: "ghcr.io", Repository: "org/model-2", Tag: "2.0.0"},
		},
	}

	reconcileCtx := controllerutils.ReconcileContext[*aimv1alpha1.AIMClusterModelSource]{
		Object: source,
	}
	plan := reconciler.PlanResources(context.Background(), reconcileCtx, obs)

	// Check that models are created
	toApply := plan.GetToApply()
	if len(toApply) != 2 {
		t.Fatalf("expected 2 models to apply, got %d", len(toApply))
	}

	// Check models have correct labels (owner references are set during apply phase, not plan)
	for i, obj := range toApply {
		model, ok := obj.(*aimv1alpha2.AIMClusterModel)
		if !ok {
			t.Errorf("apply[%d] is not an AIMClusterModel", i)
			continue
		}

		// Check label
		if model.Labels[LabelKeyModelSource] != testSourceName {
			t.Errorf("model source label = %q, want 'test-source'", model.Labels[LabelKeyModelSource])
		}
	}

	// Check no deletions (append-only)
	toDelete := plan.GetToDelete()
	if len(toDelete) != 0 {
		t.Errorf("expected 0 deletions (append-only), got %d", len(toDelete))
	}
}

func TestPlanResources_NoNewImages(t *testing.T) {
	reconciler := &ClusterModelSourceReconciler{
		Scheme: runtime.NewScheme(),
	}

	source := &aimv1alpha1.AIMClusterModelSource{
		ObjectMeta: metav1.ObjectMeta{Name: testSourceName},
	}

	obs := ClusterModelSourceObservation{
		ClusterModelSourceFetch: ClusterModelSourceFetch{
			source: source,
		},
		newImages: []RegistryImage{}, // Empty
	}

	reconcileCtx := controllerutils.ReconcileContext[*aimv1alpha1.AIMClusterModelSource]{
		Object: source,
	}
	plan := reconciler.PlanResources(context.Background(), reconcileCtx, obs)

	toApply := plan.GetToApply()
	if len(toApply) != 0 {
		t.Errorf("expected 0 models to apply, got %d", len(toApply))
	}
}

// ============================================================================
// DecorateStatus Tests
// ============================================================================

func TestDecorateStatus_Basic(t *testing.T) {
	reconciler := &ClusterModelSourceReconciler{}

	status := &aimv1alpha1.AIMClusterModelSourceStatus{}
	cm := controllerutils.NewConditionManager(nil)

	obs := ClusterModelSourceObservation{
		totalDiscovered:   5,
		totalFiltered:     8,
		filtersWithErrors: 0,
	}

	reconciler.DecorateStatus(status, cm, obs)

	if status.DiscoveredModels != 5 {
		t.Errorf("DiscoveredModels = %d, want 5", status.DiscoveredModels)
	}
	if status.AvailableModels != 8 {
		t.Errorf("AvailableModels = %d, want 8", status.AvailableModels)
	}
	if status.ModelsLimitReached != true {
		t.Errorf("ModelsLimitReached = %v, want true (8 > 5)", status.ModelsLimitReached)
	}
	if status.LastSyncTime == nil {
		t.Error("LastSyncTime should be set")
	}
}

func TestDecorateStatus_LimitNotReached(t *testing.T) {
	reconciler := &ClusterModelSourceReconciler{}

	status := &aimv1alpha1.AIMClusterModelSourceStatus{}
	cm := controllerutils.NewConditionManager(nil)

	obs := ClusterModelSourceObservation{
		totalDiscovered: 5,
		totalFiltered:   5, // Same as discovered = no limit reached
	}

	reconciler.DecorateStatus(status, cm, obs)

	if status.ModelsLimitReached != false {
		t.Errorf("ModelsLimitReached = %v, want false", status.ModelsLimitReached)
	}
}

// ============================================================================
// composeFilterHealth Tests
// ============================================================================

func TestComposeFilterHealth(t *testing.T) {
	tests := []struct {
		name       string
		results    []FilterResult
		wantState  constants.AIMStatus
		wantReason string
	}{
		{
			name:       "no filters",
			results:    []FilterResult{},
			wantState:  constants.AIMStatusProgressing,
			wantReason: "NoFilters",
		},
		{
			name: "all filters succeeded",
			results: []FilterResult{
				{Error: nil},
				{Error: nil},
			},
			wantState:  constants.AIMStatusReady,
			wantReason: "AllFiltersSucceeded",
		},
		{
			name: "some filters failed",
			results: []FilterResult{
				{Error: nil},
				{Error: errors.New("failed")},
			},
			wantState:  constants.AIMStatusDegraded,
			wantReason: "SomeFiltersFailed",
		},
		{
			name: "all filters failed",
			results: []FilterResult{
				{Error: errors.New("failed-1")},
				{Error: errors.New("failed-2")},
			},
			wantState:  constants.AIMStatusFailed,
			wantReason: "AllFiltersFailed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			health := composeFilterHealth(tt.results, false)

			if health.State != tt.wantState {
				t.Errorf("state = %v, want %q", health.State, tt.wantState)
			}
			if health.Reason != tt.wantReason {
				t.Errorf("reason = %q, want %q", health.Reason, tt.wantReason)
			}
		})
	}
}

func TestClusterModelSource_DeclaredModelCreatesV1Alpha2ClusterModel(t *testing.T) {
	t.Parallel()

	source := &aimv1alpha1.AIMClusterModelSource{
		ObjectMeta: metav1.ObjectMeta{Name: "models"},
		Spec: aimv1alpha1.AIMClusterModelSourceSpec{
			Models: []aimv1alpha1.AIMClusterModelSourceModel{{
				ModelID: "Qwen/Qwen3.5-0.8B",
			}},
		},
	}
	reconciler := &ClusterModelSourceReconciler{}
	fetch := ClusterModelSourceFetch{
		source: source,
		existingModels: controllerutils.FetchResult[*aimv1alpha2.AIMClusterModelList]{
			Value: &aimv1alpha2.AIMClusterModelList{},
		},
	}
	obs := reconciler.ComposeState(context.Background(), controllerutils.ReconcileContext[*aimv1alpha1.AIMClusterModelSource]{Object: source}, fetch)
	plan := reconciler.PlanResources(context.Background(), controllerutils.ReconcileContext[*aimv1alpha1.AIMClusterModelSource]{Object: source}, obs)
	if len(plan.GetToApply()) != 1 {
		t.Fatalf("len(toApply) = %d, want 1", len(plan.GetToApply()))
	}
	// Declarations must be written as v1alpha2: v1alpha1 CEL rejects spec.modelId.
	model, ok := plan.GetToApply()[0].(*aimv1alpha2.AIMClusterModel)
	if !ok {
		t.Fatalf("applied object = %T, want v1alpha2 AIMClusterModel", plan.GetToApply()[0])
	}
	if model.Spec.ModelID != "Qwen/Qwen3.5-0.8B" {
		t.Fatalf("modelId = %q", model.Spec.ModelID)
	}
	if model.Labels[LabelKeyModelSource] != source.Name {
		t.Fatalf("model source label = %q", model.Labels[LabelKeyModelSource])
	}
}

func TestClusterModelSource_DeclaredModelRespectsIndependentModel(t *testing.T) {
	t.Parallel()

	source := &aimv1alpha1.AIMClusterModelSource{
		ObjectMeta: metav1.ObjectMeta{Name: "models"},
		Spec: aimv1alpha1.AIMClusterModelSourceSpec{
			Models: []aimv1alpha1.AIMClusterModelSourceModel{{
				ModelID: "Qwen/Qwen3.5-0.8B",
			}},
		},
	}
	existing := aimv1alpha2.AIMClusterModel{
		ObjectMeta: metav1.ObjectMeta{Name: "independent"},
		Spec:       aimv1alpha1.AIMModelSpec{ModelID: "Qwen/Qwen3.5-0.8B"},
	}
	reconciler := &ClusterModelSourceReconciler{}
	fetch := ClusterModelSourceFetch{
		source: source,
		existingModels: controllerutils.FetchResult[*aimv1alpha2.AIMClusterModelList]{
			Value: &aimv1alpha2.AIMClusterModelList{Items: []aimv1alpha2.AIMClusterModel{existing}},
		},
	}
	obs := reconciler.ComposeState(context.Background(), controllerutils.ReconcileContext[*aimv1alpha1.AIMClusterModelSource]{Object: source}, fetch)
	plan := reconciler.PlanResources(context.Background(), controllerutils.ReconcileContext[*aimv1alpha1.AIMClusterModelSource]{Object: source}, obs)
	if len(plan.GetToApply()) != 0 {
		t.Fatalf("len(toApply) = %d, want independent model to be respected", len(plan.GetToApply()))
	}
}

func TestClusterModelSource_DeclaredModelUpdatesOwnedModel(t *testing.T) {
	t.Parallel()

	source := &aimv1alpha1.AIMClusterModelSource{
		ObjectMeta: metav1.ObjectMeta{Name: "models"},
		Spec: aimv1alpha1.AIMClusterModelSourceSpec{
			Models: []aimv1alpha1.AIMClusterModelSourceModel{{
				ModelID: "acme/qwen-finetune",
				AimID:   "Qwen/Qwen3.5-0.8B",
			}},
		},
	}
	existing := aimv1alpha2.AIMClusterModel{
		ObjectMeta: metav1.ObjectMeta{
			Name: "owned",
			Labels: map[string]string{
				LabelKeyModelSource:      source.Name,
				constants.LabelKeyOrigin: constants.LabelValueOriginAutoGenerated,
			},
		},
		Spec: aimv1alpha1.AIMModelSpec{ModelID: "acme/qwen-finetune"},
	}
	reconciler := &ClusterModelSourceReconciler{}
	fetch := ClusterModelSourceFetch{
		source: source,
		existingModels: controllerutils.FetchResult[*aimv1alpha2.AIMClusterModelList]{
			Value: &aimv1alpha2.AIMClusterModelList{Items: []aimv1alpha2.AIMClusterModel{existing}},
		},
	}
	obs := reconciler.ComposeState(context.Background(), controllerutils.ReconcileContext[*aimv1alpha1.AIMClusterModelSource]{Object: source}, fetch)
	plan := reconciler.PlanResources(context.Background(), controllerutils.ReconcileContext[*aimv1alpha1.AIMClusterModelSource]{Object: source}, obs)
	if len(plan.GetToApply()) != 1 {
		t.Fatalf("len(toApply) = %d, want owned model update", len(plan.GetToApply()))
	}
	model := plan.GetToApply()[0].(*aimv1alpha2.AIMClusterModel)
	if model.Name != existing.Name || model.Spec.AimId != source.Spec.Models[0].AimID {
		t.Fatalf("updated model = %#v", model)
	}
	if len(plan.GetToDelete()) != 0 {
		t.Fatal("declared models are kept in sync, never deleted")
	}
}

func TestClusterModelSource_DiscoveredImageCreatesV1Alpha2ClusterModel(t *testing.T) {
	t.Parallel()

	source := &aimv1alpha1.AIMClusterModelSource{
		ObjectMeta: metav1.ObjectMeta{Name: "images"},
		Spec: aimv1alpha1.AIMClusterModelSourceSpec{
			Images: []string{"amdenterpriseai/aim-qwen-qwen3-32b:0.8.4"},
		},
	}
	reconciler := &ClusterModelSourceReconciler{}
	fetch := ClusterModelSourceFetch{
		source: source,
		existingModels: controllerutils.FetchResult[*aimv1alpha2.AIMClusterModelList]{
			Value: &aimv1alpha2.AIMClusterModelList{},
		},
		filterResults: []FilterResult{{
			Images: []RegistryImage{{
				Registry:   "docker.io",
				Repository: "amdenterpriseai/aim-qwen-qwen3-32b",
				Tag:        "0.8.4",
			}},
		}},
	}
	obs := reconciler.ComposeState(context.Background(), controllerutils.ReconcileContext[*aimv1alpha1.AIMClusterModelSource]{Object: source}, fetch)
	plan := reconciler.PlanResources(context.Background(), controllerutils.ReconcileContext[*aimv1alpha1.AIMClusterModelSource]{Object: source}, obs)
	if len(plan.GetToApply()) != 1 {
		t.Fatalf("len(toApply) = %d, want 1", len(plan.GetToApply()))
	}
	if _, ok := plan.GetToApply()[0].(*aimv1alpha2.AIMClusterModel); !ok {
		t.Fatalf("applied object = %T, want v1alpha2 AIMClusterModel", plan.GetToApply()[0])
	}
}

func TestFetchRemoteStateRewritesDiscoveredModelOwnerReferences(t *testing.T) {
	source := &aimv1alpha1.AIMClusterModelSource{
		ObjectMeta: metav1.ObjectMeta{Name: "images", UID: types.UID("source-uid")},
	}
	spec := aimv1alpha1.AIMModelSpec{Image: "amdenterpriseai/aim-dummy:0.2.2"}
	labels := map[string]string{
		LabelKeyModelSource:      source.Name,
		constants.LabelKeyOrigin: constants.LabelValueOriginAutoGenerated,
	}
	annotations := map[string]string{
		annotationSourceRegistry:   "docker.io",
		annotationSourceRepository: "amdenterpriseai/aim-dummy",
		annotationSourceTag:        "0.2.2",
	}
	owner := metav1.OwnerReference{
		APIVersion:         "aim.eai.amd.com/v1alpha1",
		Kind:               "AIMClusterModelSource",
		Name:               source.Name,
		UID:                source.UID,
		Controller:         k8sptr.To(true),
		BlockOwnerDeletion: k8sptr.To(true),
	}
	other := metav1.OwnerReference{
		APIVersion:         "example.com/v1",
		Kind:               "Widget",
		Name:               "other",
		UID:                types.UID("other-uid"),
		Controller:         k8sptr.To(false),
		BlockOwnerDeletion: k8sptr.To(false),
	}
	discovered := &aimv1alpha2.AIMClusterModel{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "discovered",
			UID:             types.UID("discovered-uid"),
			ResourceVersion: "1",
			Labels:          labels,
			Annotations:     annotations,
			OwnerReferences: []metav1.OwnerReference{owner, other},
		},
		Spec: spec,
	}
	otherSource := &aimv1alpha2.AIMClusterModel{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "other-source",
			ResourceVersion: "1",
			Labels:          map[string]string{LabelKeyModelSource: "someone-else"},
			Annotations:     annotations,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion:         "aim.eai.amd.com/v1alpha1",
				Kind:               "AIMClusterModelSource",
				Name:               "someone-else",
				UID:                types.UID("other-source-uid"),
				Controller:         k8sptr.To(true),
				BlockOwnerDeletion: k8sptr.To(false),
			}},
		},
		Spec: spec,
	}
	unowned := &aimv1alpha2.AIMClusterModel{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "unowned",
			ResourceVersion: "1",
			Labels:          map[string]string{"keep": "label"},
			Annotations:     map[string]string{"keep": "annotation"},
		},
		Spec: spec,
	}

	scheme := runtime.NewScheme()
	if err := aimv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatalf("add to scheme: %v", err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(discovered, otherSource, unowned).Build()
	reconciler := &ClusterModelSourceReconciler{}
	ctx := context.Background()
	reconcileCtx := controllerutils.ReconcileContext[*aimv1alpha1.AIMClusterModelSource]{Object: source}

	fetch := reconciler.FetchRemoteState(ctx, c, reconcileCtx)
	if fetch.existingModels.Error != nil {
		t.Fatalf("rewrite failed: %v", fetch.existingModels.Error)
	}

	wantOwner := owner
	wantOwner.APIVersion = "aim.eai.amd.com/v1alpha2"
	got := &aimv1alpha2.AIMClusterModel{}
	if err := c.Get(ctx, client.ObjectKey{Name: discovered.Name}, got); err != nil {
		t.Fatalf("get discovered model: %v", err)
	}
	if got.UID != discovered.UID {
		t.Fatalf("discovered model UID = %s, want %s", got.UID, discovered.UID)
	}
	if !reflect.DeepEqual(got.OwnerReferences, []metav1.OwnerReference{wantOwner, other}) {
		t.Errorf("owner references = %#v", got.OwnerReferences)
	}
	if !reflect.DeepEqual(got.Spec, spec) || !reflect.DeepEqual(got.Labels, labels) || !reflect.DeepEqual(got.Annotations, annotations) {
		t.Errorf("spec, labels, or annotations changed: %#v", got)
	}

	assertUntouched := func(name string) {
		t.Helper()
		stored := &aimv1alpha2.AIMClusterModel{}
		if err := c.Get(ctx, client.ObjectKey{Name: name}, stored); err != nil {
			t.Fatalf("get %s: %v", name, err)
		}
		if stored.ResourceVersion != "1" {
			t.Errorf("%s resourceVersion = %s, want 1", name, stored.ResourceVersion)
		}
		switch name {
		case otherSource.Name:
			if len(stored.OwnerReferences) != 1 || stored.OwnerReferences[0].APIVersion != "aim.eai.amd.com/v1alpha1" || stored.OwnerReferences[0].UID != "other-source-uid" {
				t.Errorf("%s owner references = %#v", name, stored.OwnerReferences)
			}
		case unowned.Name:
			if len(stored.OwnerReferences) != 0 {
				t.Errorf("%s owner references = %#v", name, stored.OwnerReferences)
			}
		}
	}
	assertUntouched(otherSource.Name)
	assertUntouched(unowned.Name)

	discoveredVersion := got.ResourceVersion
	if err := reconciler.FetchRemoteState(ctx, c, reconcileCtx).existingModels.Error; err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if err := c.Get(ctx, client.ObjectKey{Name: discovered.Name}, got); err != nil {
		t.Fatalf("get discovered model after second fetch: %v", err)
	}
	if got.ResourceVersion != discoveredVersion {
		t.Errorf("second fetch changed resourceVersion from %s to %s", discoveredVersion, got.ResourceVersion)
	}
}
