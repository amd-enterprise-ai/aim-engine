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
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimprofile"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/profileyaml"
)

func TestModelDecorateStatus_SignalsOnlyTemplateMigrationCohort(t *testing.T) {
	t.Parallel()

	nativeProfiles := &aimv1alpha1.AIMModelProfilesSpec{
		DerivedFrom: &aimv1alpha1.AIMModelProfilesDerivedFrom{
			Selector: aimv1alpha1.ProfileSelector{AimId: "aim/native"},
		},
	}
	tests := []struct {
		name     string
		cluster  bool
		spec     aimv1alpha1.AIMModelSpec
		required bool
	}{
		{
			name: "legacy custom model",
			spec: aimv1alpha1.AIMModelSpec{
				CustomTemplates: []aimv1alpha1.AIMCustomTemplate{{Name: "legacy"}},
			},
			required: true,
		},
		{
			name:     "image model still emitting cluster templates",
			cluster:  true,
			spec:     aimv1alpha1.AIMModelSpec{Image: "registry.example/aim:1.0"},
			required: true,
		},
		{
			name: "deprecated profileCopy alias",
			spec: aimv1alpha1.AIMModelSpec{
				ProfileCopy: &aimv1alpha1.AIMProfileSetSpec{},
			},
			required: true,
		},
		{
			name: "deprecated flat derivedFrom shape",
			spec: aimv1alpha1.AIMModelSpec{
				DerivedFrom: &aimv1alpha1.AIMProfileSetSpec{},
			},
			required: true,
		},
		{
			name: "deprecated custom any policy",
			spec: aimv1alpha1.AIMModelSpec{
				Custom: &aimv1alpha1.AIMCustomModelSpec{
					VersionPolicy: aimv1alpha1.AIMVersionPolicyAny,
				},
			},
			required: true,
		},
		{
			name: "image model with template output disabled",
			spec: aimv1alpha1.AIMModelSpec{
				Image: "registry.example/aim:1.0",
				Discovery: &aimv1alpha1.AIMModelDiscoveryConfig{
					CreateServiceTemplates: ptr.To(false),
				},
			},
		},
		{
			name:    "native grouped profiles",
			cluster: true,
			spec:    aimv1alpha1.AIMModelSpec{Profiles: nativeProfiles},
		},
		{
			name: "native generated model with runtime config",
			spec: aimv1alpha1.AIMModelSpec{
				ModelID:          "org/model",
				RuntimeConfigRef: aimv1alpha1.RuntimeConfigRef{Name: "accelerated"},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cm := controllerutils.NewConditionManager([]metav1.Condition{{
				Type:   controllerutils.ConditionTypeMigrationRequired,
				Status: metav1.ConditionTrue,
				Reason: controllerutils.ReasonTemplateAPIDeprecated,
			}})
			status := &aimv1alpha1.AIMModelStatus{}
			if test.cluster {
				model := &aimv1alpha2.AIMClusterModel{Spec: test.spec}
				(&ClusterModelReconciler{}).DecorateStatus(
					status,
					cm,
					ClusterModelObservation{ClusterModelFetchResult: ClusterModelFetchResult{model: model}},
				)
			} else {
				model := &aimv1alpha2.AIMModel{Spec: test.spec}
				(&ModelReconciler{}).DecorateStatus(
					status,
					cm,
					ModelObservation{ModelFetchResult: ModelFetchResult{model: model}},
				)
			}

			condition := cm.Get(controllerutils.ConditionTypeMigrationRequired)
			if test.required {
				if condition == nil || condition.Status != metav1.ConditionTrue {
					t.Fatalf("MigrationRequired = %#v, want True", condition)
				}
			} else if condition != nil {
				t.Fatalf("MigrationRequired = %#v, want absent", condition)
			}
		})
	}
}

func TestApplyModelServingPathPolicy(t *testing.T) {
	t.Parallel()

	legacySpec := legacyServingSpec()
	tests := []struct {
		name            string
		spec            aimv1alpha1.AIMModelSpec
		legacyObserved  bool
		health          []controllerutils.ComponentHealth
		wantNonBlocking []string
	}{
		{
			name:           "healthy legacy path makes failed native path non-blocking",
			spec:           legacySpec,
			legacyObserved: true,
			health: []controllerutils.ComponentHealth{
				{Component: componentRuntimeConfig, State: constants.AIMStatusReady},
				{Component: componentImageMetadata, State: constants.AIMStatusReady},
				{Component: componentServiceTemplates, State: constants.AIMStatusReady},
				{Component: componentDiscovery, State: constants.AIMStatusNotAvailable},
				{Component: componentNodeInventory, State: constants.AIMStatusReady},
				{Component: componentProfiles, State: constants.AIMStatusDegraded},
			},
			wantNonBlocking: []string{componentDiscovery, componentNodeInventory, componentProfiles},
		},
		{
			name:           "healthy native path makes failed legacy path non-blocking",
			spec:           legacySpec,
			legacyObserved: true,
			health: []controllerutils.ComponentHealth{
				{Component: componentRuntimeConfig, State: constants.AIMStatusReady},
				{Component: componentImageMetadata, State: constants.AIMStatusReady},
				{Component: componentServiceTemplates, State: constants.AIMStatusFailed},
				{Component: componentDiscovery, State: constants.AIMStatusReady},
				{Component: componentNodeInventory, State: constants.AIMStatusReady},
				{Component: componentProfiles, State: constants.AIMStatusReady},
			},
			wantNonBlocking: []string{componentRuntimeConfig, componentImageMetadata, componentServiceTemplates},
		},
		{
			name:           "mixed active failures remain blocking",
			spec:           legacySpec,
			legacyObserved: true,
			health: []controllerutils.ComponentHealth{
				{Component: componentRuntimeConfig, State: constants.AIMStatusReady},
				{Component: componentImageMetadata, State: constants.AIMStatusReady},
				{Component: componentServiceTemplates, State: constants.AIMStatusFailed},
				{Component: componentDiscovery, State: constants.AIMStatusNotAvailable},
				{Component: componentProfiles, State: constants.AIMStatusDegraded},
			},
		},
		{
			name:           "unresolved legacy path cannot block complete native path",
			spec:           aimv1alpha1.AIMModelSpec{Image: "registry.example/native:1.0"},
			legacyObserved: true,
			health: []controllerutils.ComponentHealth{
				{Component: componentRuntimeConfig, State: constants.AIMStatusFailed},
				{Component: componentImageMetadata, State: constants.AIMStatusProgressing},
				{Component: componentServiceTemplates, State: constants.AIMStatusProgressing},
				{Component: componentDiscovery, State: constants.AIMStatusReady},
				{Component: componentNodeInventory, State: constants.AIMStatusReady},
				{Component: componentProfiles, State: constants.AIMStatusReady},
			},
			wantNonBlocking: []string{componentRuntimeConfig, componentImageMetadata, componentServiceTemplates},
		},
		{
			name:           "incomplete native subset cannot excuse failed legacy path",
			spec:           legacySpec,
			legacyObserved: true,
			health: []controllerutils.ComponentHealth{
				{Component: componentRuntimeConfig, State: constants.AIMStatusReady},
				{Component: componentImageMetadata, State: constants.AIMStatusFailed},
				{Component: componentServiceTemplates, State: constants.AIMStatusFailed},
				{Component: componentDiscovery, State: constants.AIMStatusReady},
				{Component: componentNodeInventory, State: constants.AIMStatusReady},
			},
		},
		{
			name:           "native-only failure remains blocking",
			spec:           aimv1alpha1.AIMModelSpec{ModelID: "Qwen/Qwen3.5-0.8B"},
			legacyObserved: false,
			health: []controllerutils.ComponentHealth{
				{Component: componentProfiles, State: constants.AIMStatusNotAvailable},
			},
		},
	}

	for _, templateComponent := range []string{componentServiceTemplates, componentClusterServiceTemplates} {
		t.Run(templateComponent, func(t *testing.T) {
			t.Parallel()
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					health := append([]controllerutils.ComponentHealth(nil), tt.health...)
					for i := range health {
						if health[i].Component == componentServiceTemplates {
							health[i].Component = templateComponent
						}
					}

					applyModelServingPathPolicy(
						&tt.spec,
						tt.legacyObserved,
						templateComponent,
						health,
					)

					want := make(map[string]struct{}, len(tt.wantNonBlocking))
					for _, component := range tt.wantNonBlocking {
						if component == componentServiceTemplates {
							component = templateComponent
						}
						want[component] = struct{}{}
					}
					for i := range health {
						_, shouldBeNonBlocking := want[health[i].Component]
						if health[i].NonBlocking != shouldBeNonBlocking {
							t.Errorf(
								"component %q NonBlocking = %v, want %v",
								health[i].Component,
								health[i].NonBlocking,
								shouldBeNonBlocking,
							)
						}
					}
				})
			}
		})
	}
}

func TestRetireRemovedModelPathState(t *testing.T) {
	t.Parallel()

	for _, templateComponent := range []string{componentServiceTemplates, componentClusterServiceTemplates} {
		t.Run(templateComponent, func(t *testing.T) {
			t.Parallel()

			t.Run("legacy-only removes native conditions and status", func(t *testing.T) {
				spec := legacyServingSpec()
				spec.Discovery = &aimv1alpha1.AIMModelDiscoveryConfig{ExtractMetadata: ptr.To(false)}
				status := staleMixedModelStatus()
				cm := controllerutils.NewConditionManager(staleMixedModelConditions(templateComponent))
				health := []controllerutils.ComponentHealth{
					{Component: componentImageMetadata, State: constants.AIMStatusReady},
					{Component: templateComponent, State: constants.AIMStatusReady},
				}

				retireRemovedModelPathState(&status, cm, &spec, true, templateComponent, health)

				for _, component := range []string{
					componentDiscovery,
					componentNodeInventory,
					componentProfiles,
					componentProfileSet,
				} {
					requireConditionAbsent(t, cm.Conditions(), component+controllerutils.ComponentConditionSuffix)
				}
				requireConditionPresent(t, cm.Conditions(), templateComponent+controllerutils.ComponentConditionSuffix)
				if status.Discovery != nil ||
					status.DiscoveryCacheRef != nil ||
					status.DiscoveredProfiles.Total != 0 ||
					status.ProfileSetRef != nil ||
					status.ManagedProfiles.Total != 0 ||
					status.ProfileGeneration != nil {
					t.Fatalf("native status was not retired: %#v", status)
				}
			})

			t.Run("native-only removes legacy state but preserves active failure", func(t *testing.T) {
				spec := aimv1alpha1.AIMModelSpec{
					Profiles: &aimv1alpha1.AIMModelProfilesSpec{
						DerivedFrom: &aimv1alpha1.AIMModelProfilesDerivedFrom{
							Selector: aimv1alpha1.ProfileSelector{Role: aimv1alpha1.ProfileSelectorRoleBase},
						},
					},
				}
				status := staleMixedModelStatus()
				cm := controllerutils.NewConditionManager(staleMixedModelConditions(templateComponent))
				health := []controllerutils.ComponentHealth{{
					Component: componentProfileSet,
					State:     constants.AIMStatusNotAvailable,
					Reason:    "ManagedProfilesNotAvailable",
				}}

				retireRemovedModelPathState(&status, cm, &spec, false, templateComponent, health)

				for _, component := range []string{
					componentRuntimeConfig,
					componentImageMetadata,
					templateComponent,
				} {
					requireConditionAbsent(t, cm.Conditions(), component+controllerutils.ComponentConditionSuffix)
				}
				requireConditionPresent(t, cm.Conditions(), componentProfileSet+controllerutils.ComponentConditionSuffix)
				if status.SourceType != "" || status.ImageMetadata != nil || status.Version != "" {
					t.Fatalf("legacy status was not retired: %#v", status)
				}
				if status.ProfileSetRef == nil {
					t.Fatal("active profile-set status was retired")
				}
			})
		})
	}
}

func TestModelPlanResources_PreservesLegacyNativeAndMixedOutputs(t *testing.T) {
	t.Parallel()

	t.Run("legacy-only", func(t *testing.T) {
		model := &aimv1alpha2.AIMModel{
			ObjectMeta: metav1.ObjectMeta{Name: "legacy", Namespace: "team-a", UID: "model-uid"},
			Spec:       legacyServingSpec(),
		}
		model.Spec.Discovery = &aimv1alpha1.AIMModelDiscoveryConfig{ExtractMetadata: ptr.To(false)}
		reconciler, fetch := namespaceLegacyFixture(t, model)

		obs := reconciler.ComposeState(
			context.Background(),
			controllerutils.ReconcileContext[*aimv1alpha2.AIMModel]{Object: model},
			fetch,
		)
		plan := reconciler.PlanResources(
			context.Background(),
			controllerutils.ReconcileContext[*aimv1alpha2.AIMModel]{Object: model},
			obs,
		)
		requireNamespaceModelOutputs(t, plan, model.Namespace, 1, 0)
	})

	t.Run("native-only", func(t *testing.T) {
		model := &aimv1alpha2.AIMModel{
			ObjectMeta: metav1.ObjectMeta{Name: "native", Namespace: "team-a", UID: "model-uid"},
			Spec:       aimv1alpha1.AIMModelSpec{ModelID: "Qwen/Qwen3.5-0.8B"},
		}
		reconciler := &ModelReconciler{}
		fetch := ModelFetchResult{
			model: model,
			nodes: controllerutils.FetchResult[[]corev1.Node]{
				Value: []corev1.Node{testNVIDIANode()},
			},
			existingProfiles: controllerutils.FetchResult[[]managedProfile]{},
			runtimeConfig: controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]{
				Value: generatedProfileRuntimeConfig(),
			},
		}

		obs := reconciler.ComposeState(
			context.Background(),
			controllerutils.ReconcileContext[*aimv1alpha2.AIMModel]{Object: model},
			fetch,
		)
		plan := reconciler.PlanResources(
			context.Background(),
			controllerutils.ReconcileContext[*aimv1alpha2.AIMModel]{Object: model},
			obs,
		)
		requireNamespaceModelOutputs(t, plan, model.Namespace, 0, 1)
	})

	t.Run("grandfathered mixed", func(t *testing.T) {
		model := &aimv1alpha2.AIMModel{
			ObjectMeta: metav1.ObjectMeta{Name: "mixed", Namespace: "team-a", UID: "model-uid"},
			Spec:       legacyServingSpec(),
		}
		reconciler, fetch := namespaceLegacyFixture(t, model)
		fetch.discovery = readyDiscoveryDecision()
		fetch.nodes = controllerutils.FetchResult[[]corev1.Node]{}

		obs := reconciler.ComposeState(
			context.Background(),
			controllerutils.ReconcileContext[*aimv1alpha2.AIMModel]{Object: model},
			fetch,
		)
		plan := reconciler.PlanResources(
			context.Background(),
			controllerutils.ReconcileContext[*aimv1alpha2.AIMModel]{Object: model},
			obs,
		)
		requireNamespaceModelOutputs(t, plan, model.Namespace, 1, 1)
	})
}

func TestClusterModelPlanResources_PreservesLegacyNativeAndMixedOutputs(t *testing.T) {
	t.Parallel()

	t.Run("legacy-only", func(t *testing.T) {
		model := &aimv1alpha2.AIMClusterModel{
			ObjectMeta: metav1.ObjectMeta{Name: "legacy", UID: "model-uid"},
			Spec:       legacyServingSpec(),
		}
		model.Spec.Discovery = &aimv1alpha1.AIMModelDiscoveryConfig{ExtractMetadata: ptr.To(false)}
		reconciler, fetch := clusterLegacyFixture(t, model)

		obs := reconciler.ComposeState(
			context.Background(),
			controllerutils.ReconcileContext[*aimv1alpha2.AIMClusterModel]{Object: model},
			fetch,
		)
		plan := reconciler.PlanResources(
			context.Background(),
			controllerutils.ReconcileContext[*aimv1alpha2.AIMClusterModel]{Object: model},
			obs,
		)
		requireClusterModelOutputs(t, plan, 1, 0)
	})

	t.Run("native-only", func(t *testing.T) {
		model := &aimv1alpha2.AIMClusterModel{
			ObjectMeta: metav1.ObjectMeta{Name: "native", UID: "model-uid"},
			Spec:       aimv1alpha1.AIMModelSpec{ModelID: "Qwen/Qwen3.5-0.8B"},
		}
		reconciler := &ClusterModelReconciler{}
		fetch := ClusterModelFetchResult{
			model: model,
			nodes: controllerutils.FetchResult[[]corev1.Node]{
				Value: []corev1.Node{testNVIDIANode()},
			},
			existingClusterProfiles: controllerutils.FetchResult[[]managedProfile]{},
			runtimeConfig: controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]{
				Value: generatedProfileRuntimeConfig(),
			},
		}

		obs := reconciler.ComposeState(
			context.Background(),
			controllerutils.ReconcileContext[*aimv1alpha2.AIMClusterModel]{Object: model},
			fetch,
		)
		plan := reconciler.PlanResources(
			context.Background(),
			controllerutils.ReconcileContext[*aimv1alpha2.AIMClusterModel]{Object: model},
			obs,
		)
		requireClusterModelOutputs(t, plan, 0, 1)
	})

	t.Run("grandfathered mixed", func(t *testing.T) {
		model := &aimv1alpha2.AIMClusterModel{
			ObjectMeta: metav1.ObjectMeta{Name: "mixed", UID: "model-uid"},
			Spec:       legacyServingSpec(),
		}
		reconciler, fetch := clusterLegacyFixture(t, model)
		fetch.discovery = readyDiscoveryDecision()
		fetch.nodes = controllerutils.FetchResult[[]corev1.Node]{}

		obs := reconciler.ComposeState(
			context.Background(),
			controllerutils.ReconcileContext[*aimv1alpha2.AIMClusterModel]{Object: model},
			fetch,
		)
		plan := reconciler.PlanResources(
			context.Background(),
			controllerutils.ReconcileContext[*aimv1alpha2.AIMClusterModel]{Object: model},
			obs,
		)
		requireClusterModelOutputs(t, plan, 1, 1)
	})
}

func TestModelDiscoveryOwnerReferences_UseProductionIdentityAndDeletionProtection(t *testing.T) {
	t.Parallel()

	namespaceModel := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "namespace-model", Namespace: "team-a", UID: "namespace-uid"},
	}
	clusterModel := &aimv1alpha2.AIMClusterModel{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-model", UID: "cluster-uid"},
	}

	tests := []struct {
		name    string
		ref     metav1.OwnerReference
		kind    string
		uid     string
		objName string
	}{
		{
			name:    "namespace model",
			ref:     ownerReferenceForModel(namespaceModel),
			kind:    "AIMModel",
			uid:     "namespace-uid",
			objName: namespaceModel.Name,
		},
		{
			name:    "cluster model",
			ref:     ownerReferenceForClusterModel(clusterModel),
			kind:    "AIMClusterModel",
			uid:     "cluster-uid",
			objName: clusterModel.Name,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.ref.APIVersion != aimv1alpha2.GroupVersion.String() ||
				tt.ref.Kind != tt.kind ||
				tt.ref.Name != tt.objName ||
				string(tt.ref.UID) != tt.uid {
				t.Fatalf("owner reference identity = %#v", tt.ref)
			}
			if tt.ref.Controller == nil || !*tt.ref.Controller {
				t.Fatalf("owner reference controller = %v, want true", tt.ref.Controller)
			}
			if tt.ref.BlockOwnerDeletion == nil || !*tt.ref.BlockOwnerDeletion {
				t.Fatalf("owner reference blockOwnerDeletion = %v, want true", tt.ref.BlockOwnerDeletion)
			}
		})
	}
}

func namespaceLegacyFixture(
	t *testing.T,
	model *aimv1alpha2.AIMModel,
) (*ModelReconciler, ModelFetchResult) {
	t.Helper()
	scheme := dualPathScheme(t)
	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithIndex(
			&aimv1alpha1.AIMServiceTemplate{},
			aimv1alpha1.ServiceTemplateModelNameIndexKey,
			func(obj client.Object) []string {
				return []string{obj.(*aimv1alpha1.AIMServiceTemplate).Spec.ModelName}
			},
		).
		Build()
	reconciler := &ModelReconciler{Client: k8sClient, Scheme: scheme}
	legacyModel := asLegacyModel(model)
	legacyFetch := reconciler.legacy().FetchRemoteState(
		context.Background(),
		k8sClient,
		controllerutils.ReconcileContext[*aimv1alpha1.AIMModel]{Object: legacyModel},
	)
	return reconciler, ModelFetchResult{
		model:            model,
		legacyModel:      legacyModel,
		legacyFetch:      &legacyFetch,
		existingProfiles: controllerutils.FetchResult[[]managedProfile]{},
	}
}

func clusterLegacyFixture(
	t *testing.T,
	model *aimv1alpha2.AIMClusterModel,
) (*ClusterModelReconciler, ClusterModelFetchResult) {
	t.Helper()
	scheme := dualPathScheme(t)
	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithIndex(
			&aimv1alpha1.AIMClusterServiceTemplate{},
			aimv1alpha1.ServiceTemplateModelNameIndexKey,
			func(obj client.Object) []string {
				return []string{obj.(*aimv1alpha1.AIMClusterServiceTemplate).Spec.ModelName}
			},
		).
		Build()
	reconciler := &ClusterModelReconciler{Client: k8sClient, Scheme: scheme}
	legacyModel := asLegacyClusterModel(model)
	legacyFetch := reconciler.legacy().FetchRemoteState(
		context.Background(),
		k8sClient,
		controllerutils.ReconcileContext[*aimv1alpha1.AIMClusterModel]{Object: legacyModel},
	)
	return reconciler, ClusterModelFetchResult{
		model:                   model,
		legacyModel:             legacyModel,
		legacyFetch:             &legacyFetch,
		existingClusterProfiles: controllerutils.FetchResult[[]managedProfile]{},
	}
}

func dualPathScheme(t *testing.T) *runtime.Scheme {
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

func legacyServingSpec() aimv1alpha1.AIMModelSpec {
	return aimv1alpha1.AIMModelSpec{
		Image: "registry.example/legacy-qwen:1.0",
		ImageMetadata: &aimv1alpha1.ImageMetadata{
			Model: &aimv1alpha1.ModelMetadata{
				CanonicalName: "legacy-qwen",
				RecommendedDeployments: []aimv1alpha1.RecommendedDeployment{{
					GPUModel: "MI300X",
					GPUCount: 1,
				}},
			},
		},
	}
}

func generatedProfileRuntimeConfig() *aimv1alpha1.AIMRuntimeConfigCommon {
	return &aimv1alpha1.AIMRuntimeConfigCommon{
		Model: &aimv1alpha1.AIMModelConfig{
			ProfileGeneration: &aimv1alpha1.AIMProfileGenerationConfig{
				Fallbacks: []aimv1alpha1.AIMProfileGenerationFallback{
					testNVIDIAFallback("nvidia-vllm", 0),
				},
			},
		},
	}
}

func readyDiscoveryDecision() DiscoveryDecision {
	catalog := &aimprofile.DiscoveryCatalog{
		AimID: "qwen/qwen3-32b",
		Profiles: []aimprofile.DiscoveryCatalogItem{{
			BaseImage:    "registry.example/aim-base:1.0",
			YAMLContract: profileyaml.DefaultContract(),
			Spec: aimv1alpha2.AIMProfileSpecCommon{
				AimId:     "qwen/qwen3-32b",
				ModelId:   "qwen/qwen3-32b",
				ProfileId: "cpu",
				Engine:    "vllm",
				ModelSources: []aimv1alpha1.AIMModelSource{{
					ModelID:   "qwen/qwen3-32b",
					SourceURI: "hf://qwen/qwen3-32b",
				}},
			},
		}},
	}
	return DiscoveryDecision{
		Catalog: catalog,
		State:   &aimv1alpha1.ModelDiscoveryState{Attempts: 1},
		Reason:  "DiscoveryCacheValid",
	}
}

func staleMixedModelConditions(templateComponent string) []metav1.Condition {
	components := []string{
		componentRuntimeConfig,
		componentImageMetadata,
		templateComponent,
		componentDiscovery,
		componentNodeInventory,
		componentProfiles,
		componentProfileSet,
	}
	conditions := make([]metav1.Condition, 0, len(components))
	for _, component := range components {
		conditions = append(conditions, metav1.Condition{
			Type:   component + controllerutils.ComponentConditionSuffix,
			Status: metav1.ConditionFalse,
			Reason: "StaleFailure",
		})
	}
	return conditions
}

func staleMixedModelStatus() aimv1alpha1.AIMModelStatus {
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

func requireNamespaceModelOutputs(
	t *testing.T,
	plan controllerutils.PlanResult,
	namespace string,
	wantTemplates int,
	wantProfiles int,
) {
	t.Helper()
	var templates, profiles int
	for _, obj := range plan.GetToApply() {
		switch obj.(type) {
		case *aimv1alpha1.AIMServiceTemplate:
			templates++
			if obj.GetNamespace() != namespace {
				t.Errorf("template namespace = %q, want %q", obj.GetNamespace(), namespace)
			}
		case *aimv1alpha2.AIMProfile:
			profiles++
			if obj.GetNamespace() != namespace {
				t.Errorf("profile namespace = %q, want %q", obj.GetNamespace(), namespace)
			}
		}
	}
	if templates != wantTemplates || profiles != wantProfiles {
		t.Fatalf(
			"owned namespace outputs = %d template(s), %d profile(s); want %d, %d",
			templates,
			profiles,
			wantTemplates,
			wantProfiles,
		)
	}
	requireNoUnownedModelOutputs(t, plan)
}

func requireClusterModelOutputs(
	t *testing.T,
	plan controllerutils.PlanResult,
	wantTemplates int,
	wantProfiles int,
) {
	t.Helper()
	var templates, profiles int
	for _, obj := range plan.GetToApply() {
		switch obj.(type) {
		case *aimv1alpha1.AIMClusterServiceTemplate:
			templates++
			if obj.GetNamespace() != "" {
				t.Errorf("cluster template namespace = %q, want empty", obj.GetNamespace())
			}
		case *aimv1alpha2.AIMClusterProfile:
			profiles++
			if obj.GetNamespace() != "" {
				t.Errorf("cluster profile namespace = %q, want empty", obj.GetNamespace())
			}
		}
	}
	if templates != wantTemplates || profiles != wantProfiles {
		t.Fatalf(
			"owned cluster outputs = %d template(s), %d profile(s); want %d, %d",
			templates,
			profiles,
			wantTemplates,
			wantProfiles,
		)
	}
	requireNoUnownedModelOutputs(t, plan)
}

func requireNoUnownedModelOutputs(t *testing.T, plan controllerutils.PlanResult) {
	t.Helper()
	for _, obj := range plan.GetToApplyWithoutOwnerRef() {
		switch obj.(type) {
		case *aimv1alpha1.AIMServiceTemplate,
			*aimv1alpha1.AIMClusterServiceTemplate,
			*aimv1alpha2.AIMProfile,
			*aimv1alpha2.AIMClusterProfile:
			t.Errorf("%T %q was planned without controller ownership", obj, obj.GetName())
		}
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
