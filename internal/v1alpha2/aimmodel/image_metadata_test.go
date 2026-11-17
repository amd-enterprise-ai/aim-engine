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
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/imagemetadata"
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
			Status:     aimv1alpha1.AIMModelStatus{Conditions: staleMixedModelConditions(componentServiceTemplates)},
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

			requireImageModelOutputs(t, reconcile.name, model.Status, obs.componentHealth, obs.DesiredProfiles, componentServiceTemplates)
		}
	})

	t.Run("AIMClusterModel", func(t *testing.T) {
		t.Parallel()
		model := &aimv1alpha2.AIMClusterModel{
			ObjectMeta: metav1.ObjectMeta{Name: "qwen", UID: "model-uid"},
			Spec:       aimv1alpha1.AIMModelSpec{Image: testImageModelImage},
			Status:     aimv1alpha1.AIMModelStatus{Conditions: staleMixedModelConditions(componentClusterServiceTemplates)},
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

			requireImageModelOutputs(t, reconcile.name, model.Status, obs.componentHealth, obs.DesiredProfiles, componentClusterServiceTemplates)
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
		reconciler := &ModelReconciler{Client: imageModelFakeClient(t), Scheme: dualPathScheme(t)}
		rc := controllerutils.ReconcileContext[*aimv1alpha2.AIMModel]{Object: model}
		obs := reconciler.ComposeState(context.Background(), rc, reconciler.FetchRemoteState(context.Background(), reconciler.Client, rc))
		reconciler.DecorateStatus(&model.Status, controllerutils.NewConditionManager(nil), obs)

		requireUninspectedImageModel(t, model.Status, obs.componentHealth)
	})

	t.Run("AIMClusterModel", func(t *testing.T) {
		t.Parallel()
		model := &aimv1alpha2.AIMClusterModel{
			ObjectMeta: metav1.ObjectMeta{Name: "no-inspect", UID: "model-uid"},
			Spec:       spec,
		}
		reconciler := &ClusterModelReconciler{Client: imageModelFakeClient(t), Scheme: dualPathScheme(t)}
		rc := controllerutils.ReconcileContext[*aimv1alpha2.AIMClusterModel]{Object: model}
		obs := reconciler.ComposeState(context.Background(), rc, reconciler.FetchRemoteState(context.Background(), reconciler.Client, rc))
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
		reconciler := &ModelReconciler{Client: imageModelFakeClient(t, runtimeConfig.DeepCopy()), Scheme: dualPathScheme(t)}
		rc := controllerutils.ReconcileContext[*aimv1alpha2.AIMModel]{Object: model}
		fetch := reconciler.FetchRemoteState(context.Background(), reconciler.Client, rc)
		fetch.legacyFetch = nil
		obs := reconciler.ComposeState(context.Background(), rc, fetch)

		requireNativeRuntimeConfig(t, obs.componentHealth, reconciler.GetApplyOptions(obs), want)
	})

	t.Run("AIMClusterModel", func(t *testing.T) {
		t.Parallel()
		model := &aimv1alpha2.AIMClusterModel{
			ObjectMeta: metav1.ObjectMeta{Name: "qwen", UID: "model-uid"},
			Spec:       spec,
		}
		reconciler := &ClusterModelReconciler{Client: imageModelFakeClient(t, runtimeConfig.DeepCopy()), Scheme: dualPathScheme(t)}
		rc := controllerutils.ReconcileContext[*aimv1alpha2.AIMClusterModel]{Object: model}
		fetch := reconciler.FetchRemoteState(context.Background(), reconciler.Client, rc)
		fetch.legacyFetch = nil
		obs := reconciler.ComposeState(context.Background(), rc, fetch)

		requireNativeRuntimeConfig(t, obs.componentHealth, reconciler.GetApplyOptions(obs), want)
	})
}

// While legacy mode still runs, both paths write the image status fields.
// Repeated reconciles must converge on the native-only status.
func TestImageModelStatus_LegacyAndNativeWritersAgree(t *testing.T) {
	t.Parallel()

	t.Run("AIMModel", func(t *testing.T) {
		t.Parallel()
		reconcileModel := func(withLegacy bool) []aimv1alpha1.AIMModelStatus {
			model := &aimv1alpha2.AIMModel{
				ObjectMeta: metav1.ObjectMeta{Name: "qwen", Namespace: "team-a", UID: "model-uid"},
				Spec:       legacyServingSpec(),
			}
			var statuses []aimv1alpha1.AIMModelStatus
			for range 3 {
				reconciler, fetch := &ModelReconciler{}, ModelFetchResult{model: model}
				if withLegacy {
					reconciler, fetch = namespaceLegacyFixture(t, model)
				}
				fetch.imageMetadata = imagemetadata.Fetch(context.Background(), nil, model.Spec, &model.Status, model.Namespace)
				obs := reconciler.ComposeState(
					context.Background(),
					controllerutils.ReconcileContext[*aimv1alpha2.AIMModel]{Object: model},
					fetch,
				)
				cm := controllerutils.NewConditionManager(model.Status.Conditions)
				reconciler.DecorateStatus(&model.Status, cm, obs)
				model.Status.Conditions = cm.Conditions()
				statuses = append(statuses, *model.Status.DeepCopy())
			}
			return statuses
		}
		requireStatusesAgree(t, reconcileModel(true), reconcileModel(false))
	})

	t.Run("AIMClusterModel", func(t *testing.T) {
		t.Parallel()
		reconcileModel := func(withLegacy bool) []aimv1alpha1.AIMModelStatus {
			model := &aimv1alpha2.AIMClusterModel{
				ObjectMeta: metav1.ObjectMeta{Name: "qwen", UID: "model-uid"},
				Spec:       legacyServingSpec(),
			}
			var statuses []aimv1alpha1.AIMModelStatus
			for range 3 {
				reconciler, fetch := &ClusterModelReconciler{}, ClusterModelFetchResult{model: model}
				if withLegacy {
					reconciler, fetch = clusterLegacyFixture(t, model)
				}
				fetch.imageMetadata = imagemetadata.Fetch(context.Background(), nil, model.Spec, &model.Status, constants.GetOperatorNamespace())
				obs := reconciler.ComposeState(
					context.Background(),
					controllerutils.ReconcileContext[*aimv1alpha2.AIMClusterModel]{Object: model},
					fetch,
				)
				cm := controllerutils.NewConditionManager(model.Status.Conditions)
				reconciler.DecorateStatus(&model.Status, cm, obs)
				model.Status.Conditions = cm.Conditions()
				statuses = append(statuses, *model.Status.DeepCopy())
			}
			return statuses
		}
		requireStatusesAgree(t, reconcileModel(true), reconcileModel(false))
	})
}

func requireImageModelOutputs(
	t *testing.T,
	reconcile string,
	status aimv1alpha1.AIMModelStatus,
	health []controllerutils.ComponentHealth,
	desired []desiredProfile,
	templateComponent string,
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
	requireConditionAbsent(t, status.Conditions, templateComponent+controllerutils.ComponentConditionSuffix)

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

func requireStatusesAgree(t *testing.T, dual, native []aimv1alpha1.AIMModelStatus) {
	t.Helper()
	withoutTransitionTimes := func(status aimv1alpha1.AIMModelStatus) aimv1alpha1.AIMModelStatus {
		status = *status.DeepCopy()
		for i := range status.Conditions {
			status.Conditions[i].LastTransitionTime = metav1.Time{}
		}
		return status
	}
	for i := range dual {
		if !reflect.DeepEqual(dual[i], dual[0]) {
			t.Errorf("dual-path status changed on reconcile %d:\n got %#v\nwant %#v", i+1, dual[i], dual[0])
		}
		if !reflect.DeepEqual(withoutTransitionTimes(dual[i]), withoutTransitionTimes(native[i])) {
			t.Errorf("reconcile %d: dual-path status differs from native-only:\n dual %#v\n native %#v", i+1, dual[i], native[i])
		}
	}
	if dual[0].ImageMetadata == nil || dual[0].Version != "1.0" || dual[0].SourceType != aimv1alpha1.AIMModelSourceTypeImage {
		t.Errorf("image status fields not set: %#v", dual[0])
	}
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
		WithScheme(dualPathScheme(t)).
		WithObjects(objects...).
		WithIndex(&aimv1alpha1.AIMServiceTemplate{}, aimv1alpha1.ServiceTemplateModelNameIndexKey, noIndexValues).
		WithIndex(&aimv1alpha1.AIMClusterServiceTemplate{}, aimv1alpha1.ServiceTemplateModelNameIndexKey, noIndexValues).
		WithIndex(&aimv1alpha2.AIMProfile{}, ManagedModelUIDIndexKey, noIndexValues).
		WithIndex(&aimv1alpha2.AIMClusterProfile{}, ManagedModelUIDIndexKey, noIndexValues).
		Build()
}
