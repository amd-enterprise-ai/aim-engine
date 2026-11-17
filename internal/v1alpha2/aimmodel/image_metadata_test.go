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

package aimmodel

import (
	"context"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/aimruntimeconfig"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimprofile"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/profileyaml"
)

const testImageModelImage = "registry.example/aim-qwen:0.10.0"

type imageMetadataFetch = controllerutils.FetchResult[*aimv1alpha1.ImageMetadata]

// The first reconcile reads the registry; later ones skip it because status
// already holds the metadata.
func imageModelReconciles() []struct {
	name          string
	imageMetadata imageMetadataFetch
} {
	return []struct {
		name          string
		imageMetadata imageMetadataFetch
	}{
		{name: "registry read", imageMetadata: imageMetadataFetch{Value: recommendedImageMetadata()}},
		{name: "cached in status", imageMetadata: imageMetadataFetch{}},
	}
}

func TestImageModelWithoutLegacyFetch_SetsStatusAndPrimaryProfile(t *testing.T) {
	t.Parallel()

	t.Run("AIMModel", func(t *testing.T) {
		t.Parallel()
		model := &aimv1alpha2.AIMModel{
			ObjectMeta: metav1.ObjectMeta{Name: "qwen", Namespace: "team-a", UID: "model-uid"},
			Spec:       aimv1alpha1.AIMModelSpec{Image: testImageModelImage},
			Status:     aimv1alpha1.AIMModelStatus{Conditions: staleModelConditions()},
		}
		reconciler := &ModelReconciler{}
		for _, reconcile := range imageModelReconciles() {
			obs := reconciler.ComposeState(
				context.Background(),
				controllerutils.ReconcileContext[*aimv1alpha2.AIMModel]{Object: model},
				ModelFetchResult{
					model:         model,
					discovery:     recommendedCatalogDecision(),
					nodes:         controllerutils.FetchResult[[]corev1.Node]{Value: []corev1.Node{testMI300XNode()}},
					imageMetadata: reconcile.imageMetadata,
				},
			)
			cm := controllerutils.NewConditionManager(model.Status.Conditions)
			reconciler.DecorateStatus(&model.Status, cm, obs)
			model.Status.Conditions = cm.Conditions()

			requireImageModelOutputs(t, reconcile.name, model.Status, obs.componentHealth, obs.DesiredProfiles)
		}
	})

	t.Run("AIMClusterModel", func(t *testing.T) {
		t.Parallel()
		model := &aimv1alpha2.AIMClusterModel{
			ObjectMeta: metav1.ObjectMeta{Name: "qwen", UID: "model-uid"},
			Spec:       aimv1alpha1.AIMModelSpec{Image: testImageModelImage},
			Status:     aimv1alpha1.AIMModelStatus{Conditions: staleModelConditions()},
		}
		reconciler := &ClusterModelReconciler{}
		for _, reconcile := range imageModelReconciles() {
			obs := reconciler.ComposeState(
				context.Background(),
				controllerutils.ReconcileContext[*aimv1alpha2.AIMClusterModel]{Object: model},
				ClusterModelFetchResult{
					model:         model,
					discovery:     recommendedCatalogDecision(),
					nodes:         controllerutils.FetchResult[[]corev1.Node]{Value: []corev1.Node{testMI300XNode()}},
					imageMetadata: reconcile.imageMetadata,
				},
			)
			cm := controllerutils.NewConditionManager(model.Status.Conditions)
			reconciler.DecorateStatus(&model.Status, cm, obs)
			model.Status.Conditions = cm.Conditions()

			requireImageModelOutputs(t, reconcile.name, model.Status, obs.componentHealth, obs.DesiredProfiles)
		}
	})
}

func TestImageModelFetch_ExtractMetadataDisabledSkipsInspection(t *testing.T) {
	t.Parallel()

	// Inspecting this unparsable reference would surface an ImageMetadata error.
	spec := aimv1alpha1.AIMModelSpec{
		Image:     "registry.example/NOT-A-VALID-REPO:1.0",
		Discovery: &aimv1alpha1.AIMModelDiscoveryConfig{ExtractMetadata: ptr.To(false)},
	}

	t.Run("AIMModel", func(t *testing.T) {
		t.Parallel()
		model := &aimv1alpha2.AIMModel{
			ObjectMeta: metav1.ObjectMeta{Name: "no-inspect", Namespace: "team-a", UID: "model-uid"},
			Spec:       spec,
		}
		reconciler := &ModelReconciler{}
		rc := controllerutils.ReconcileContext[*aimv1alpha2.AIMModel]{Object: model}
		obs := reconciler.ComposeState(context.Background(), rc, reconciler.FetchRemoteState(context.Background(), imageModelFakeClient(t), rc))
		reconciler.DecorateStatus(&model.Status, controllerutils.NewConditionManager(nil), obs)

		requireUninspectedImageModel(t, model.Status, obs.componentHealth)
	})

	t.Run("AIMClusterModel", func(t *testing.T) {
		t.Parallel()
		model := &aimv1alpha2.AIMClusterModel{
			ObjectMeta: metav1.ObjectMeta{Name: "no-inspect", UID: "model-uid"},
			Spec:       spec,
		}
		reconciler := &ClusterModelReconciler{}
		rc := controllerutils.ReconcileContext[*aimv1alpha2.AIMClusterModel]{Object: model}
		obs := reconciler.ComposeState(context.Background(), rc, reconciler.FetchRemoteState(context.Background(), imageModelFakeClient(t), rc))
		reconciler.DecorateStatus(&model.Status, controllerutils.NewConditionManager(nil), obs)

		requireUninspectedImageModel(t, model.Status, obs.componentHealth)
	})
}

func TestImageModelWithoutLegacyFetch_UsesNativeRuntimeConfig(t *testing.T) {
	t.Parallel()

	// Extraction is disabled so the fetch makes no registry call.
	spec := aimv1alpha1.AIMModelSpec{
		Image:     testImageModelImage,
		Discovery: &aimv1alpha1.AIMModelDiscoveryConfig{ExtractMetadata: ptr.To(false)},
	}
	runtimeConfig := &aimv1alpha1.AIMClusterRuntimeConfig{
		ObjectMeta: metav1.ObjectMeta{Name: aimruntimeconfig.DefaultRuntimeConfigName},
		Spec: aimv1alpha1.AIMClusterRuntimeConfigSpec{
			AIMRuntimeConfigCommon: aimv1alpha1.AIMRuntimeConfigCommon{
				LabelPropagation: &aimv1alpha1.AIMRuntimeConfigLabelPropagationSpec{Enabled: true, Match: []string{"team.example/*"}},
			},
		},
	}
	want := &controllerutils.LabelPropagationSettings{Enabled: true, Match: []string{"team.example/*"}}

	t.Run("AIMModel", func(t *testing.T) {
		t.Parallel()
		model := &aimv1alpha2.AIMModel{
			ObjectMeta: metav1.ObjectMeta{Name: "qwen", Namespace: "team-a", UID: "model-uid"},
			Spec:       spec,
		}
		reconciler := &ModelReconciler{}
		rc := controllerutils.ReconcileContext[*aimv1alpha2.AIMModel]{Object: model}
		fetch := reconciler.FetchRemoteState(context.Background(), imageModelFakeClient(t, runtimeConfig.DeepCopy()), rc)
		obs := reconciler.ComposeState(context.Background(), rc, fetch)

		requireNativeRuntimeConfig(t, obs.componentHealth, reconciler.GetApplyOptions(obs), want)
	})

	t.Run("AIMClusterModel", func(t *testing.T) {
		t.Parallel()
		model := &aimv1alpha2.AIMClusterModel{
			ObjectMeta: metav1.ObjectMeta{Name: "qwen", UID: "model-uid"},
			Spec:       spec,
		}
		reconciler := &ClusterModelReconciler{}
		rc := controllerutils.ReconcileContext[*aimv1alpha2.AIMClusterModel]{Object: model}
		fetch := reconciler.FetchRemoteState(context.Background(), imageModelFakeClient(t, runtimeConfig.DeepCopy()), rc)
		obs := reconciler.ComposeState(context.Background(), rc, fetch)

		requireNativeRuntimeConfig(t, obs.componentHealth, reconciler.GetApplyOptions(obs), want)
	})
}

func TestApplyModelServingPathPolicy(t *testing.T) {
	t.Parallel()

	imageSpec := aimv1alpha1.AIMModelSpec{Image: "registry.example/native:1.0"}
	tests := []struct {
		name            string
		spec            aimv1alpha1.AIMModelSpec
		health          []controllerutils.ComponentHealth
		wantNonBlocking []string
	}{
		{
			name: "unresolved runtime config cannot block complete profiles",
			spec: imageSpec,
			health: []controllerutils.ComponentHealth{
				{Component: componentRuntimeConfig, State: constants.AIMStatusFailed},
				{Component: componentImageMetadata, State: constants.AIMStatusProgressing},
				{Component: componentDiscovery, State: constants.AIMStatusReady},
				{Component: componentNodeInventory, State: constants.AIMStatusReady},
				{Component: componentProfiles, State: constants.AIMStatusReady},
			},
			wantNonBlocking: []string{componentRuntimeConfig, componentImageMetadata},
		},
		{
			name: "failed image inspection cannot block complete profiles",
			spec: imageSpec,
			health: []controllerutils.ComponentHealth{
				{Component: componentRuntimeConfig, State: constants.AIMStatusReady},
				{Component: componentImageMetadata, State: constants.AIMStatusFailed},
				{Component: componentDiscovery, State: constants.AIMStatusReady},
				{Component: componentNodeInventory, State: constants.AIMStatusReady},
				{Component: componentProfiles, State: constants.AIMStatusReady},
			},
			wantNonBlocking: []string{componentRuntimeConfig, componentImageMetadata},
		},
		{
			name: "failed image inspection blocks incomplete profiles",
			spec: imageSpec,
			health: []controllerutils.ComponentHealth{
				{Component: componentRuntimeConfig, State: constants.AIMStatusReady},
				{Component: componentImageMetadata, State: constants.AIMStatusFailed},
				{Component: componentDiscovery, State: constants.AIMStatusProgressing},
				{Component: componentNodeInventory, State: constants.AIMStatusReady},
			},
		},
		{
			name: "image model without discovery keeps image components blocking",
			spec: aimv1alpha1.AIMModelSpec{
				Image:     "registry.example/native:1.0",
				Discovery: &aimv1alpha1.AIMModelDiscoveryConfig{ExtractMetadata: ptr.To(false)},
			},
			health: []controllerutils.ComponentHealth{
				{Component: componentRuntimeConfig, State: constants.AIMStatusFailed},
				{Component: componentImageMetadata, State: constants.AIMStatusReady},
			},
		},
		{
			name: "generated profile failure remains blocking",
			spec: aimv1alpha1.AIMModelSpec{ModelID: "Qwen/Qwen3.5-0.8B"},
			health: []controllerutils.ComponentHealth{
				{Component: componentProfiles, State: constants.AIMStatusNotAvailable},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			health := append([]controllerutils.ComponentHealth(nil), tt.health...)
			applyModelServingPathPolicy(&tt.spec, health)

			want := make(map[string]bool, len(tt.wantNonBlocking))
			for _, component := range tt.wantNonBlocking {
				want[component] = true
			}
			for i := range health {
				if health[i].NonBlocking != want[health[i].Component] {
					t.Errorf("component %q NonBlocking = %v, want %v", health[i].Component, health[i].NonBlocking, want[health[i].Component])
				}
			}
		})
	}
}

func TestRetireRemovedModelPathState(t *testing.T) {
	t.Parallel()

	t.Run("image model without discovery retires discovery state", func(t *testing.T) {
		t.Parallel()
		spec := aimv1alpha1.AIMModelSpec{
			Image:     "registry.example/native:1.0",
			Discovery: &aimv1alpha1.AIMModelDiscoveryConfig{ExtractMetadata: ptr.To(false)},
		}
		status := staleModelStatus()
		cm := controllerutils.NewConditionManager(staleModelConditions())
		health := []controllerutils.ComponentHealth{
			{Component: componentRuntimeConfig, State: constants.AIMStatusReady},
			{Component: componentImageMetadata, State: constants.AIMStatusReady},
		}

		retireRemovedModelPathState(&status, cm, &spec, health)

		for _, component := range []string{componentDiscovery, componentNodeInventory, componentProfiles, componentProfileSet} {
			requireConditionAbsent(t, cm.Conditions(), component+controllerutils.ComponentConditionSuffix)
		}
		requireConditionPresent(t, cm.Conditions(), componentImageMetadata+controllerutils.ComponentConditionSuffix)
		if status.Discovery != nil ||
			status.DiscoveryCacheRef != nil ||
			status.DiscoveredProfiles.Total != 0 ||
			status.ProfileSetRef != nil ||
			status.ManagedProfiles.Total != 0 ||
			status.ProfileGeneration != nil {
			t.Fatalf("discovery status was not retired: %#v", status)
		}
	})

	t.Run("derivation model retires image state but preserves active failure", func(t *testing.T) {
		t.Parallel()
		spec := aimv1alpha1.AIMModelSpec{
			Profiles: &aimv1alpha1.AIMModelProfilesSpec{
				DerivedFrom: &aimv1alpha1.AIMModelProfilesDerivedFrom{
					Selector: aimv1alpha1.ProfileSelector{Role: aimv1alpha1.ProfileSelectorRoleBase},
				},
			},
		}
		status := staleModelStatus()
		cm := controllerutils.NewConditionManager(staleModelConditions())
		health := []controllerutils.ComponentHealth{{
			Component: componentProfileSet,
			State:     constants.AIMStatusNotAvailable,
			Reason:    "ManagedProfilesNotAvailable",
		}}

		retireRemovedModelPathState(&status, cm, &spec, health)

		requireConditionAbsent(t, cm.Conditions(), componentRuntimeConfig+controllerutils.ComponentConditionSuffix)
		requireConditionAbsent(t, cm.Conditions(), componentImageMetadata+controllerutils.ComponentConditionSuffix)
		requireConditionPresent(t, cm.Conditions(), componentProfileSet+controllerutils.ComponentConditionSuffix)
		if status.SourceType != "" || status.ImageMetadata != nil || status.Version != "" {
			t.Fatalf("image status was not retired: %#v", status)
		}
		if status.ProfileSetRef == nil {
			t.Fatal("active profile-set status was retired")
		}
	})

	t.Run("image model keeps image state", func(t *testing.T) {
		t.Parallel()
		spec := aimv1alpha1.AIMModelSpec{Image: "registry.example/native:1.0"}
		status := staleModelStatus()
		cm := controllerutils.NewConditionManager(staleModelConditions())
		health := []controllerutils.ComponentHealth{
			{Component: componentRuntimeConfig, State: constants.AIMStatusReady},
			{Component: componentImageMetadata, State: constants.AIMStatusReady},
		}

		retireRemovedModelPathState(&status, cm, &spec, health)

		requireConditionPresent(t, cm.Conditions(), componentRuntimeConfig+controllerutils.ComponentConditionSuffix)
		requireConditionPresent(t, cm.Conditions(), componentImageMetadata+controllerutils.ComponentConditionSuffix)
		if status.SourceType == "" || status.ImageMetadata == nil || status.Version == "" {
			t.Fatalf("image status was retired: %#v", status)
		}
	})
}

func requireImageModelOutputs(
	t *testing.T,
	reconcile string,
	status aimv1alpha1.AIMModelStatus,
	health []controllerutils.ComponentHealth,
	desired []desiredProfile,
) {
	t.Helper()
	if status.SourceType != aimv1alpha1.AIMModelSourceTypeImage {
		t.Errorf("%s: status.sourceType = %q, want Image", reconcile, status.SourceType)
	}
	if status.Version != "0.10.0" {
		t.Errorf("%s: status.version = %q, want the spec.image tag", reconcile, status.Version)
	}
	if !reflect.DeepEqual(status.ImageMetadata, recommendedImageMetadata()) {
		t.Errorf("%s: status.imageMetadata = %#v, want the fetched metadata", reconcile, status.ImageMetadata)
	}

	for _, component := range []string{componentRuntimeConfig, componentImageMetadata} {
		found := false
		for i := range health {
			if health[i].Component == component {
				found = true
				if health[i].GetState() != constants.AIMStatusReady || len(health[i].Errors) != 0 {
					t.Errorf("%s: %s health = %#v, want Ready", reconcile, component, health[i])
				}
			}
		}
		if !found {
			t.Errorf("%s: no %s component health", reconcile, component)
		}
		requireConditionPresent(t, status.Conditions, component+controllerutils.ComponentConditionSuffix)
	}
	for _, conditionType := range templateEraModelConditions {
		requireConditionAbsent(t, status.Conditions, conditionType)
	}

	primary := map[string]bool{}
	for _, d := range desired {
		spec, _ := profileSpecCommon(d.Object)
		primary[spec.ProfileId] = spec.Primary
	}
	want := map[string]bool{"mi300x-fp16-latency": true, "mi300x-fp8-latency": false}
	if !reflect.DeepEqual(primary, want) {
		t.Errorf("%s: desired profile primary flags = %v, want %v", reconcile, primary, want)
	}
}

func requireUninspectedImageModel(t *testing.T, status aimv1alpha1.AIMModelStatus, health []controllerutils.ComponentHealth) {
	t.Helper()
	for i := range health {
		if health[i].Component == componentImageMetadata && len(health[i].Errors) != 0 {
			t.Fatalf("image was inspected: %v", health[i].Errors)
		}
	}
	if status.ImageMetadata != nil {
		t.Errorf("status.imageMetadata = %#v, want nil", status.ImageMetadata)
	}
	if status.Version != "1.0" || status.SourceType != aimv1alpha1.AIMModelSourceTypeImage {
		t.Errorf("status version/sourceType = %q/%q, want 1.0/Image", status.Version, status.SourceType)
	}
}

func requireNativeRuntimeConfig(
	t *testing.T,
	health []controllerutils.ComponentHealth,
	options controllerutils.ApplyOptions,
	want *controllerutils.LabelPropagationSettings,
) {
	t.Helper()
	reason := ""
	for i := range health {
		if health[i].Component == componentRuntimeConfig {
			reason = health[i].Reason
		}
	}
	if reason != "ConfigFound" {
		t.Errorf("RuntimeConfig health reason = %q, want ConfigFound", reason)
	}
	if !reflect.DeepEqual(options.LabelPropagation, want) {
		t.Errorf("apply options label propagation = %#v, want %#v", options.LabelPropagation, want)
	}
}

func requireConditionAbsent(t *testing.T, conditions []metav1.Condition, conditionType string) {
	t.Helper()
	for i := range conditions {
		if conditions[i].Type == conditionType {
			t.Fatalf("condition %q was retained: %#v", conditionType, conditions[i])
		}
	}
}

func requireConditionPresent(t *testing.T, conditions []metav1.Condition, conditionType string) {
	t.Helper()
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return
		}
	}
	t.Fatalf("condition %q was removed from %#v", conditionType, conditions)
}

// templateEraModelConditions are the conditions models carried while they
// still created AIMServiceTemplates.
var templateEraModelConditions = []string{
	"MigrationRequired",
	"ServiceTemplatesReady",
	"ClusterServiceTemplatesReady",
	"LegacyAutoCustomSpecReady",
}

// staleModelConditions holds a failed condition for every model component
// plus the template-era conditions.
func staleModelConditions() []metav1.Condition {
	conditionTypes := append([]string(nil), templateEraModelConditions...)
	for _, component := range []string{
		componentRuntimeConfig,
		componentImageMetadata,
		componentDiscovery,
		componentNodeInventory,
		componentProfiles,
		componentProfileSet,
	} {
		conditionTypes = append(conditionTypes, component+controllerutils.ComponentConditionSuffix)
	}
	conditions := make([]metav1.Condition, 0, len(conditionTypes))
	for _, conditionType := range conditionTypes {
		conditions = append(conditions, metav1.Condition{
			Type:   conditionType,
			Status: metav1.ConditionFalse,
			Reason: "StaleFailure",
		})
	}
	return conditions
}

func staleModelStatus() aimv1alpha1.AIMModelStatus {
	return aimv1alpha1.AIMModelStatus{
		SourceType:         aimv1alpha1.AIMModelSourceTypeImage,
		ImageMetadata:      &aimv1alpha1.ImageMetadata{},
		Version:            "1.0",
		ProfileGeneration:  &aimv1alpha1.AIMModelProfileGenerationStatus{},
		Discovery:          &aimv1alpha1.ModelDiscoveryState{Attempts: 2},
		DiscoveryCacheRef:  &aimv1alpha1.DiscoveryCacheReference{Name: "stale-catalog"},
		DiscoveredProfiles: aimv1alpha1.DiscoveredProfileCounts{Total: 2},
		ProfileSetRef:      &aimv1alpha1.ProfileSetReference{Name: "active-or-stale-set"},
		ManagedProfiles:    aimv1alpha1.ManagedProfileCounts{Total: 2},
	}
}

func modelTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := aimv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add v1alpha1 scheme: %v", err)
	}
	if err := aimv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatalf("add v1alpha2 scheme: %v", err)
	}
	return scheme
}

func recommendedImageMetadata() *aimv1alpha1.ImageMetadata {
	return &aimv1alpha1.ImageMetadata{
		Model: &aimv1alpha1.ModelMetadata{
			CanonicalName: "qwen/qwen3-32b",
			RecommendedDeployments: []aimv1alpha1.RecommendedDeployment{{
				GPUModel:  "MI300X",
				GPUCount:  1,
				Precision: "fp16",
				Metric:    "latency",
			}},
		},
	}
}

func recommendedCatalogDecision() DiscoveryDecision {
	entry := func(profileID string, precision aimv1alpha1.AIMPrecision) aimprofile.DiscoveryCatalogItem {
		return aimprofile.DiscoveryCatalogItem{
			YAMLContract: profileyaml.DefaultContract(),
			Spec: aimv1alpha2.AIMProfileSpecCommon{
				AimId:            "qwen/qwen3-32b",
				ModelId:          "qwen/qwen3-32b",
				ProfileId:        profileID,
				Engine:           "vllm",
				AcceleratorModel: "MI300X",
				AcceleratorCount: 1,
				Precision:        precision,
				Metric:           "latency",
			},
		}
	}
	return DiscoveryDecision{
		Catalog: &aimprofile.DiscoveryCatalog{
			AimID:    "qwen/qwen3-32b",
			Profiles: []aimprofile.DiscoveryCatalogItem{entry("mi300x-fp16-latency", "fp16"), entry("mi300x-fp8-latency", "fp8")},
		},
		State:  &aimv1alpha1.ModelDiscoveryState{Attempts: 1},
		Reason: "DiscoveryCacheValid",
	}
}

func testMI300XNode() corev1.Node {
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "mi300x",
			Labels: map[string]string{aimprofile.AcceleratorLabelPrefix + "MI300X": "8"},
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{corev1.ResourceName(constants.AMDGPUResourceName): resource.MustParse("8")},
		},
	}
}

func imageModelFakeClient(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()
	noIndexValues := func(client.Object) []string { return nil }
	return fake.NewClientBuilder().
		WithScheme(modelTestScheme(t)).
		WithObjects(objects...).
		WithStatusSubresource(&aimv1alpha2.AIMModel{}, &aimv1alpha2.AIMClusterModel{}).
		WithIndex(&aimv1alpha2.AIMProfile{}, ManagedModelUIDIndexKey, noIndexValues).
		WithIndex(&aimv1alpha2.AIMClusterProfile{}, ManagedModelUIDIndexKey, noIndexValues).
		Build()
}
