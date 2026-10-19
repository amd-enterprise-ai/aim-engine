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

package aimservice

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	duckv1 "knative.dev/pkg/apis/duck/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	legacyaimmodel "github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/aimmodel"
)

// ============================================================================
// COMPOSE STATE TESTS
// ============================================================================

func TestComposeState_NeedsModelCreation(t *testing.T) {
	r := &ServiceReconciler{}

	tests := []struct {
		name                   string
		fetch                  ServiceFetchResult
		expectNeedsCreation    bool
		expectPendingModelName string
		expectError            bool
	}{
		{
			name: "no image URI - no creation needed",
			fetch: ServiceFetchResult{
				service: NewService("svc").Build(),
				modelResult: ModelFetchResult{
					ImageURI: "",
				},
			},
			expectNeedsCreation: false,
		},
		{
			name: "image URI with existing model - no creation",
			fetch: ServiceFetchResult{
				service: NewService("svc").Build(),
				modelResult: ModelFetchResult{
					ImageURI: "ghcr.io/amd/llama:v1",
					Model: controllerutils.FetchResult[*aimv1alpha1.AIMModel]{
						Value: NewModel("existing").Build(),
					},
				},
			},
			expectNeedsCreation: false,
		},
		{
			name: "image URI with existing cluster model - no creation",
			fetch: ServiceFetchResult{
				service: NewService("svc").Build(),
				modelResult: ModelFetchResult{
					ImageURI: "ghcr.io/amd/llama:v1",
					ClusterModel: controllerutils.FetchResult[*aimv1alpha1.AIMClusterModel]{
						Value: NewClusterModel("existing").Build(),
					},
				},
			},
			expectNeedsCreation: false,
		},
		{
			name: "image URI with fetch error - no creation",
			fetch: ServiceFetchResult{
				service: NewService("svc").Build(),
				modelResult: ModelFetchResult{
					ImageURI: "ghcr.io/amd/llama:v1",
					Model: controllerutils.FetchResult[*aimv1alpha1.AIMModel]{
						Error: ErrMultipleModelsFound,
					},
				},
			},
			expectNeedsCreation: false,
		},
		{
			name: "image URI with no model - needs creation",
			fetch: ServiceFetchResult{
				service: NewService("svc").Build(),
				modelResult: ModelFetchResult{
					ImageURI: "ghcr.io/amd/llama:v1",
				},
			},
			expectNeedsCreation:    true,
			expectPendingModelName: "llama-v1",
		},
		{
			name: "invalid image URI - sets error",
			fetch: ServiceFetchResult{
				service: NewService("svc").Build(),
				modelResult: ModelFetchResult{
					ImageURI: ":::invalid",
				},
			},
			expectNeedsCreation: false,
			expectError:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obs := r.ComposeState(testContext(), controllerutils.ReconcileContext[*aimv1alpha1.AIMService]{}, tt.fetch)

			if obs.needsModelCreation != tt.expectNeedsCreation {
				t.Errorf("needsModelCreation: expected %v, got %v", tt.expectNeedsCreation, obs.needsModelCreation)
			}

			if tt.expectPendingModelName != "" {
				if obs.pendingModelName == "" {
					t.Error("expected pendingModelName to be set")
				}
				// Just verify it's non-empty and contains expected substring
				// (actual name generation tested in model_test.go)
			}

			if tt.expectError {
				if obs.modelResult.Model.Error == nil {
					t.Error("expected model error to be set")
				}
			}
		})
	}
}

// ============================================================================
// GET COMPONENT HEALTH TESTS
// ============================================================================

func TestGetComponentHealth_ModelHealth(t *testing.T) {
	tests := []struct {
		name          string
		obs           ServiceObservation
		expectState   constants.AIMStatus
		expectReason  string
		expectMessage string
	}{
		{
			name: "needs model creation - pending",
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					service: NewService("svc").Build(),
					modelResult: ModelFetchResult{
						ImageURI: "ghcr.io/amd/llama:v1",
					},
				},
				needsModelCreation: true,
			},
			expectState:  constants.AIMStatusPending,
			expectReason: aimv1alpha1.AIMServiceReasonCreatingModel,
		},
		{
			name: "model ready",
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					service: NewService("svc").Build(),
					modelResult: ModelFetchResult{
						Model: controllerutils.FetchResult[*aimv1alpha1.AIMModel]{
							Value: NewModel("m").WithStatus(constants.AIMStatusReady).Build(),
						},
					},
				},
			},
			expectState:  constants.AIMStatusReady,
			expectReason: aimv1alpha1.AIMServiceReasonModelResolved,
		},
		{
			name: "model progressing",
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					service: NewService("svc").Build(),
					modelResult: ModelFetchResult{
						Model: controllerutils.FetchResult[*aimv1alpha1.AIMModel]{
							Value: NewModel("m").WithStatus(constants.AIMStatusProgressing).Build(),
						},
					},
				},
			},
			expectState:  constants.AIMStatusProgressing,
			expectReason: aimv1alpha1.AIMServiceReasonModelNotReady,
		},
		{
			name: "model failed",
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					service: NewService("svc").Build(),
					modelResult: ModelFetchResult{
						Model: controllerutils.FetchResult[*aimv1alpha1.AIMModel]{
							Value: NewModel("m").WithStatus(constants.AIMStatusFailed).Build(),
						},
					},
				},
			},
			expectState:  constants.AIMStatusFailed,
			expectReason: aimv1alpha1.AIMServiceReasonModelNotReady,
		},
		{
			name: "cluster model ready",
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					service: NewService("svc").Build(),
					modelResult: ModelFetchResult{
						ClusterModel: controllerutils.FetchResult[*aimv1alpha1.AIMClusterModel]{
							Value: NewClusterModel("cm").WithStatus(constants.AIMStatusReady).Build(),
						},
					},
				},
			},
			expectState:  constants.AIMStatusReady,
			expectReason: aimv1alpha1.AIMServiceReasonModelResolved,
		},
		{
			name: "no model found",
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					service:     NewService("svc").Build(),
					modelResult: ModelFetchResult{},
				},
			},
			expectState:  constants.AIMStatusPending,
			expectReason: aimv1alpha1.AIMServiceReasonModelNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			health := tt.obs.GetComponentHealth(context.Background(), nil)

			// Find model health
			var modelHealth *controllerutils.ComponentHealth
			for i := range health {
				if health[i].Component == "Model" {
					modelHealth = &health[i]
					break
				}
			}

			if modelHealth == nil {
				t.Fatal("Model health not found")
			}

			if modelHealth.State != tt.expectState {
				t.Errorf("expected state %s, got %s", tt.expectState, modelHealth.State)
			}

			if modelHealth.Reason != tt.expectReason {
				t.Errorf("expected reason %s, got %s", tt.expectReason, modelHealth.Reason)
			}
		})
	}
}

func TestGetComponentHealth_TemplateHealth(t *testing.T) {
	tests := []struct {
		name          string
		obs           ServiceObservation
		expectState   constants.AIMStatus
		expectReason  string
		expectMessage string
	}{
		{
			name: "no templates found for model",
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					service: NewService("svc").Build(),
					templateSelection: &TemplateSelectionResult{
						SelectionReason:  aimv1alpha1.AIMServiceReasonTemplateNotFound,
						SelectionMessage: `No templates found for model "m"`,
					},
				},
			},
			expectState:   constants.AIMStatusPending,
			expectReason:  aimv1alpha1.AIMServiceReasonTemplateNotFound,
			expectMessage: `No templates found for model "m"`,
		},
		{
			name: "templates exist but filtered by optimization level",
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					service: NewService("svc").Build(),
					templateSelection: &TemplateSelectionResult{
						SelectionReason:  aimv1alpha1.AIMServiceReasonTemplateNotFound,
						SelectionMessage: `No available templates match requirements for model "m": 1 unoptimized template(s) filtered out. Set allowUnoptimized to use them.`,
					},
				},
			},
			expectState:   constants.AIMStatusPending,
			expectReason:  aimv1alpha1.AIMServiceReasonTemplateNotFound,
			expectMessage: `No available templates match requirements for model "m": 1 unoptimized template(s) filtered out. Set allowUnoptimized to use them.`,
		},
		{
			name: "templates exist but not ready",
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					service: NewService("svc").Build(),
					templateSelection: &TemplateSelectionResult{
						TemplatesExistButNotReady: true,
					},
				},
			},
			expectState:   constants.AIMStatusProgressing,
			expectReason:  aimv1alpha1.AIMServiceReasonTemplateNotReady,
			expectMessage: "Templates exist but are not ready yet",
		},
		{
			name: "selection result missing details uses fallback message",
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					service: NewService("svc").Build(),
				},
			},
			expectState:   constants.AIMStatusPending,
			expectReason:  aimv1alpha1.AIMServiceReasonTemplateNotFound,
			expectMessage: "No template found for service",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			health := tt.obs.GetComponentHealth(context.Background(), nil)

			var templateHealth *controllerutils.ComponentHealth
			for i := range health {
				if health[i].Component == "Template" {
					templateHealth = &health[i]
					break
				}
			}

			if templateHealth == nil {
				t.Fatal("Template health not found")
			}

			if templateHealth.State != tt.expectState {
				t.Errorf("expected state %s, got %s", tt.expectState, templateHealth.State)
			}

			if templateHealth.Reason != tt.expectReason {
				t.Errorf("expected reason %s, got %s", tt.expectReason, templateHealth.Reason)
			}

			if templateHealth.Message != tt.expectMessage {
				t.Errorf("expected message %q, got %q", tt.expectMessage, templateHealth.Message)
			}
		})
	}
}

func TestGetComponentHealth_CacheHealth(t *testing.T) {
	tests := []struct {
		name         string
		obs          ServiceObservation
		expectState  constants.AIMStatus
		expectReason string
	}{
		{
			name: "no template cache - progressing (creating template cache)",
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					service: NewService("svc").WithCachingMode(aimv1alpha1.CachingModeDedicated).Build(),
				},
			},
			expectState:  constants.AIMStatusProgressing,
			expectReason: aimv1alpha1.AIMServiceReasonCacheCreating,
		},
		{
			name: "never mode with ready template cache",
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					service: NewService("svc").WithCachingMode(aimv1alpha1.CachingModeDedicated).Build(),
					templateCache: controllerutils.FetchResult[*aimv1alpha1.AIMTemplateCache]{
						Value: &aimv1alpha1.AIMTemplateCache{
							Spec: aimv1alpha1.AIMTemplateCacheSpec{
								Mode: aimv1alpha1.TemplateCacheModeDedicated,
							},
							Status: aimv1alpha1.AIMTemplateCacheStatus{
								Status: constants.AIMStatusReady,
							},
						},
					},
				},
			},
			expectState:  constants.AIMStatusReady,
			expectReason: aimv1alpha1.AIMServiceReasonCacheReady,
		},
		{
			name: "template cache ready",
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					service: NewService("svc").Build(),
					templateCache: controllerutils.FetchResult[*aimv1alpha1.AIMTemplateCache]{
						Value: &aimv1alpha1.AIMTemplateCache{
							Status: aimv1alpha1.AIMTemplateCacheStatus{
								Status: constants.AIMStatusReady,
							},
						},
					},
				},
			},
			expectState:  constants.AIMStatusReady,
			expectReason: aimv1alpha1.AIMServiceReasonCacheReady,
		},
		{
			name: "template cache progressing",
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					service: NewService("svc").Build(),
					templateCache: controllerutils.FetchResult[*aimv1alpha1.AIMTemplateCache]{
						Value: &aimv1alpha1.AIMTemplateCache{
							Status: aimv1alpha1.AIMTemplateCacheStatus{
								Status: constants.AIMStatusProgressing,
							},
						},
					},
				},
			},
			expectState:  constants.AIMStatusProgressing,
			expectReason: aimv1alpha1.AIMServiceReasonCacheNotReady,
		},
		{
			name: "template cache failed",
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					service: NewService("svc").Build(),
					templateCache: controllerutils.FetchResult[*aimv1alpha1.AIMTemplateCache]{
						Value: &aimv1alpha1.AIMTemplateCache{
							Status: aimv1alpha1.AIMTemplateCacheStatus{
								Status: constants.AIMStatusFailed,
							},
						},
					},
				},
			},
			expectState:  constants.AIMStatusFailed,
			expectReason: aimv1alpha1.AIMServiceReasonCacheFailed,
		},
		{
			name: "auto mode no cache - progressing (creating dedicated caches)",
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					service: NewService("svc").Build(), // Default is Auto
				},
			},
			expectState:  constants.AIMStatusProgressing,
			expectReason: aimv1alpha1.AIMServiceReasonCacheCreating,
		},
		{
			name: "always mode no cache - progressing",
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					service: NewService("svc").WithCachingMode(aimv1alpha1.CachingModeShared).Build(),
				},
			},
			expectState:  constants.AIMStatusProgressing,
			expectReason: aimv1alpha1.AIMServiceReasonCacheCreating,
		},
		{
			name: "shared cache lost - ISVC has cache PVC volumes but shared template cache gone",
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					service: NewService("svc").WithCachingMode(aimv1alpha1.CachingModeShared).Build(),
					inferenceService: controllerutils.FetchResult[*servingv1beta1.InferenceService]{
						Value: &servingv1beta1.InferenceService{
							Spec: servingv1beta1.InferenceServiceSpec{
								Predictor: servingv1beta1.PredictorSpec{
									PodSpec: servingv1beta1.PodSpec{
										Volumes: []corev1.Volume{
											{Name: "dshm"},
											{Name: "cache-vol", VolumeSource: corev1.VolumeSource{
												PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "pvc-1"},
											}},
										},
									},
								},
							},
						},
					},
				},
			},
			expectState:  constants.AIMStatusDegraded,
			expectReason: aimv1alpha1.AIMServiceReasonCacheLost,
		},
		{
			name: "dedicated cache recreating - ISVC has cache PVC volumes but dedicated template cache gone",
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					service: NewService("svc").WithCachingMode(aimv1alpha1.CachingModeDedicated).Build(),
					inferenceService: controllerutils.FetchResult[*servingv1beta1.InferenceService]{
						Value: &servingv1beta1.InferenceService{
							Spec: servingv1beta1.InferenceServiceSpec{
								Predictor: servingv1beta1.PredictorSpec{
									PodSpec: servingv1beta1.PodSpec{
										Volumes: []corev1.Volume{
											{Name: "dshm"},
											{Name: "cache-vol", VolumeSource: corev1.VolumeSource{
												PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "pvc-1"},
											}},
										},
									},
								},
							},
						},
					},
				},
			},
			expectState:  constants.AIMStatusDegraded,
			expectReason: aimv1alpha1.AIMServiceReasonCacheCreating,
		},
		{
			name: "no cache, no ISVC cache volumes - normal creating state",
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					service: NewService("svc").Build(),
					inferenceService: controllerutils.FetchResult[*servingv1beta1.InferenceService]{
						Value: &servingv1beta1.InferenceService{
							Spec: servingv1beta1.InferenceServiceSpec{
								Predictor: servingv1beta1.PredictorSpec{
									PodSpec: servingv1beta1.PodSpec{
										Volumes: []corev1.Volume{
											{Name: "dshm"},
										},
									},
								},
							},
						},
					},
				},
			},
			expectState:  constants.AIMStatusProgressing,
			expectReason: aimv1alpha1.AIMServiceReasonCacheCreating,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			health := tt.obs.GetComponentHealth(context.Background(), nil)

			// Find cache health
			var cacheHealth *controllerutils.ComponentHealth
			for i := range health {
				if health[i].Component == "Cache" {
					cacheHealth = &health[i]
					break
				}
			}

			if cacheHealth == nil {
				t.Fatal("Cache health not found")
			}

			if cacheHealth.State != tt.expectState {
				t.Errorf("expected state %s, got %s", tt.expectState, cacheHealth.State)
			}

			if cacheHealth.Reason != tt.expectReason {
				t.Errorf("expected reason %s, got %s", tt.expectReason, cacheHealth.Reason)
			}
		})
	}
}

// ============================================================================
// PLAN RESOURCES TESTS
// ============================================================================

func TestPlanResources_ModelCreation(t *testing.T) {
	r := &ServiceReconciler{}

	tests := []struct {
		name             string
		obs              ServiceObservation
		expectModelPlan  bool
		expectModelImage string
	}{
		{
			name: "needs model creation - plans model",
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					service: NewService("svc").WithModelImage("ghcr.io/amd/llama:v1").Build(),
					modelResult: ModelFetchResult{
						ImageURI: "ghcr.io/amd/llama:v1",
					},
				},
				needsModelCreation: true,
				pendingModelName:   "llama-v1-abc",
			},
			expectModelPlan:  true,
			expectModelImage: "ghcr.io/amd/llama:v1",
		},
		{
			name: "model exists - no plan",
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					service: NewService("svc").WithModelName("existing").Build(),
					modelResult: ModelFetchResult{
						Model: controllerutils.FetchResult[*aimv1alpha1.AIMModel]{
							Value: NewModel("existing").Build(),
						},
					},
				},
				needsModelCreation: false,
			},
			expectModelPlan: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := r.PlanResources(testContext(), controllerutils.ReconcileContext[*aimv1alpha1.AIMService]{}, tt.obs)

			foundModel := false
			for _, obj := range plan.GetToApplyWithoutOwnerRef() {
				if model, ok := obj.(*aimv1alpha1.AIMModel); ok {
					foundModel = true
					if tt.expectModelImage != "" && model.Spec.Image != tt.expectModelImage {
						t.Errorf("expected model image %s, got %s", tt.expectModelImage, model.Spec.Image)
					}
				}
			}

			if tt.expectModelPlan && !foundModel {
				t.Error("expected model in plan, not found")
			}
			if !tt.expectModelPlan && foundModel {
				t.Error("unexpected model in plan")
			}
		})
	}
}

func TestPlanResources_InlineCustomCompatibilityModel(t *testing.T) {
	custom := &aimv1alpha1.AIMServiceModelCustom{
		BaseImage: "ghcr.io/amd/aim-base:v1",
		ModelSources: []aimv1alpha1.AIMModelSource{{
			ModelID:   "acme/model",
			SourceURI: "s3://models/model",
		}},
		Hardware: aimv1alpha1.AIMHardwareRequirements{
			GPU: &aimv1alpha1.AIMGpuRequirements{},
		},
	}
	service := NewService("custom-source").Build()
	service.Spec.Model = &aimv1alpha1.AIMServiceModel{Custom: custom}

	fetch := ServiceFetchResult{
		service:     service,
		modelResult: fetchModel(context.Background(), newFakeClient(), service),
	}
	obs := (&ServiceReconciler{}).ComposeState(
		context.Background(),
		controllerutils.ReconcileContext[*aimv1alpha1.AIMService]{Object: service},
		fetch,
	)
	if !obs.needsModelCreation {
		t.Fatalf("expected fresh custom model creation, model error: %v", obs.modelResult.Model.Error)
	}

	plan := (&ServiceReconciler{}).PlanResources(
		context.Background(),
		controllerutils.ReconcileContext[*aimv1alpha1.AIMService]{Object: service},
		obs,
	)
	var planned *aimv1alpha2.AIMModel
	for _, object := range plan.GetToCreate() {
		if model, ok := object.(*aimv1alpha2.AIMModel); ok {
			planned = model
			break
		}
	}
	if planned == nil {
		t.Fatal("expected create-only v1alpha2 compatibility AIMModel in plan")
	}
	if planned.APIVersion != aimv1alpha2.GroupVersion.String() {
		t.Fatalf("planned apiVersion = %q, want %q", planned.APIVersion, aimv1alpha2.GroupVersion)
	}
	if want := GenerateCustomModelName(service, custom); planned.Name != want {
		t.Fatalf("planned name = %q, want service-scoped name %q", planned.Name, want)
	}
	if len(planned.Spec.ModelSources) != 0 || planned.Spec.Custom != nil {
		t.Fatalf("planned persisted spec restored predecessor-only fields: %#v", planned.Spec)
	}
	decoded, found, err := legacyaimmodel.DecodeLegacyAutoCustomModel(planned)
	if err != nil || !found {
		t.Fatalf("decode planned compatibility model = (%#v, %t, %v)", decoded, found, err)
	}
	if decoded.BaseImage != custom.BaseImage ||
		len(decoded.ModelSources) != 1 ||
		decoded.ModelSources[0].SourceURI != custom.ModelSources[0].SourceURI {
		t.Fatalf("decoded compatibility payload does not round trip: %#v", decoded)
	}
	controller := metav1.GetControllerOf(planned)
	if controller == nil || controller.UID != service.UID {
		t.Fatalf("planned model lacks UID-bound source ownership: %#v", planned.OwnerReferences)
	}
	if controller.APIVersion != aimv1alpha2.GroupVersion.String() || controller.Kind != "AIMService" {
		t.Fatalf("planned model owner = %s %s, want production AIMService root",
			controller.APIVersion, controller.Kind)
	}
}

func TestPlanResources_SkipsWithoutReadyTemplate(t *testing.T) {
	r := &ServiceReconciler{}

	tests := []struct {
		name            string
		obs             ServiceObservation
		expectResources int
	}{
		{
			name: "no template - skips planning",
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					service: NewService("svc").Build(),
				},
			},
			expectResources: 0,
		},
		{
			name: "template not ready - skips planning",
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					service: NewService("svc").Build(),
					template: controllerutils.FetchResult[*aimv1alpha1.AIMServiceTemplate]{
						Value: func() *aimv1alpha1.AIMServiceTemplate {
							t := NewTemplate("t").WithModelName(testModelName).Build()
							t.Status.Status = constants.AIMStatusProgressing
							return t
						}(),
					},
				},
			},
			expectResources: 0,
		},
		{
			name: "template ready - plans resources",
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					service: NewService("svc").Build(),
					template: controllerutils.FetchResult[*aimv1alpha1.AIMServiceTemplate]{
						Value: func() *aimv1alpha1.AIMServiceTemplate {
							t := NewTemplate("t").WithModelName(testModelName).Build()
							t.Status.Status = constants.AIMStatusReady
							t.Status.ModelSources = []aimv1alpha1.AIMModelSource{
								NewModelSource("hf://model/file.safetensors", 10*1024*1024*1024),
							}
							return t
						}(),
					},
					modelResult: ModelFetchResult{
						Model: controllerutils.FetchResult[*aimv1alpha1.AIMModel]{
							Value: NewModel(testModelName).WithStatus(constants.AIMStatusReady).Build(),
						},
					},
				},
			},
			expectResources: 2, // At minimum: PVC + template cache (or just one depending on mode)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := r.PlanResources(testContext(), controllerutils.ReconcileContext[*aimv1alpha1.AIMService]{}, tt.obs)

			totalResources := len(plan.GetToCreate()) +
				len(plan.GetToApply()) +
				len(plan.GetToApplyWithoutOwnerRef())
			if tt.expectResources == 0 && totalResources > 0 {
				t.Errorf("expected no resources planned, got %d", totalResources)
			}
			if tt.expectResources > 0 && totalResources == 0 {
				t.Errorf("expected resources to be planned, got none")
			}
		})
	}
}

func TestPlanResources_CustomProfileAssemblyFailureSkipsRuntimeResources(t *testing.T) {
	r := &ServiceReconciler{}
	metric := aimv1alpha1.AIMMetric("latency")
	precision := aimv1alpha1.AIMPrecision("fp16")

	template := NewTemplate("t").WithModelName(testModelName).Build()
	template.Spec.AimId = "meta-llama/Llama-3-8B"
	template.Spec.ModelId = "meta-llama/Llama-3-8B"
	template.Spec.Metric = &metric
	template.Spec.Precision = &precision
	template.Spec.Hardware = &aimv1alpha1.AIMHardwareRequirements{
		GPU: &aimv1alpha1.AIMGpuRequirements{
			Model:    "MI300X",
			Requests: 1,
		},
	}
	template.Spec.CustomProfile = &aimv1alpha1.AIMCustomProfile{
		// Invalid JSON type for map[string]any unmarshal in AssembleProfileYAML.
		EngineArgs: &apiextensionsv1.JSON{Raw: []byte(`"not-an-object"`)},
	}
	template.Status.Status = constants.AIMStatusReady

	obs := ServiceObservation{
		ServiceFetchResult: ServiceFetchResult{
			service: NewService("svc").Build(),
			template: controllerutils.FetchResult[*aimv1alpha1.AIMServiceTemplate]{
				Value: template,
			},
			modelResult: ModelFetchResult{
				Model: controllerutils.FetchResult[*aimv1alpha1.AIMModel]{
					Value: NewModel(testModelName).WithStatus(constants.AIMStatusReady).Build(),
				},
			},
		},
	}

	plan := r.PlanResources(testContext(), controllerutils.ReconcileContext[*aimv1alpha1.AIMService]{}, obs)
	for _, obj := range plan.GetToApply() {
		switch obj.(type) {
		case *corev1.ConfigMap:
			t.Fatalf("unexpected ConfigMap planned when custom profile assembly failed")
		case *servingv1beta1.InferenceService:
			t.Fatalf("unexpected InferenceService planned when custom profile assembly failed")
		}
	}
}

// ============================================================================
// DECORATE STATUS TESTS
// ============================================================================

func TestDecorateStatus_SignalsTemplateMigration(t *testing.T) {
	cm := controllerutils.NewConditionManager(nil)
	(&ServiceReconciler{}).DecorateStatus(
		&aimv1alpha1.AIMServiceStatus{},
		cm,
		ServiceObservation{ServiceFetchResult: ServiceFetchResult{
			service: NewService("legacy").WithModelImage("test-image:v1").Build(),
		}},
	)

	condition := cm.Get(controllerutils.ConditionTypeMigrationRequired)
	if condition == nil || condition.Status != metav1.ConditionTrue {
		t.Fatalf("MigrationRequired = %#v, want True", condition)
	}
}

func TestDecorateStatus_ResolvedReferences(t *testing.T) {
	r := &ServiceReconciler{}

	tests := []struct {
		name                   string
		obs                    ServiceObservation
		expectResolvedModel    bool
		expectResolvedTemplate bool
		expectCache            bool
		expectModelScope       aimv1alpha1.AIMResolutionScope
		expectTemplateScope    aimv1alpha1.AIMResolutionScope
	}{
		{
			name: "model and template ready - sets references",
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					service: NewService("svc").Build(),
					modelResult: ModelFetchResult{
						Model: controllerutils.FetchResult[*aimv1alpha1.AIMModel]{
							Value: NewModel("m").WithStatus(constants.AIMStatusReady).Build(),
						},
					},
					template: controllerutils.FetchResult[*aimv1alpha1.AIMServiceTemplate]{
						Value: func() *aimv1alpha1.AIMServiceTemplate {
							t := NewTemplate("t").Build()
							t.Status.Status = constants.AIMStatusReady
							return t
						}(),
					},
				},
			},
			expectResolvedModel:    true,
			expectResolvedTemplate: true,
			expectModelScope:       aimv1alpha1.AIMResolutionScopeNamespace,
			expectTemplateScope:    aimv1alpha1.AIMResolutionScopeNamespace,
		},
		{
			name: "model not ready - no reference",
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					service: NewService("svc").Build(),
					modelResult: ModelFetchResult{
						Model: controllerutils.FetchResult[*aimv1alpha1.AIMModel]{
							Value: NewModel("m").WithStatus(constants.AIMStatusProgressing).Build(),
						},
					},
				},
			},
			expectResolvedModel:    false,
			expectResolvedTemplate: false,
		},
		{
			name: "cluster model ready - cluster scope",
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					service: NewService("svc").Build(),
					modelResult: ModelFetchResult{
						ClusterModel: controllerutils.FetchResult[*aimv1alpha1.AIMClusterModel]{
							Value: NewClusterModel("cm").WithStatus(constants.AIMStatusReady).Build(),
						},
					},
				},
			},
			expectResolvedModel: true,
			expectModelScope:    aimv1alpha1.AIMResolutionScopeCluster,
		},
		{
			name: "cluster template ready - cluster scope",
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					service: NewService("svc").Build(),
					clusterTemplate: controllerutils.FetchResult[*aimv1alpha1.AIMClusterServiceTemplate]{
						Value: func() *aimv1alpha1.AIMClusterServiceTemplate {
							t := NewClusterTemplate("ct").Build()
							t.Status.Status = constants.AIMStatusReady
							return t
						}(),
					},
				},
			},
			expectResolvedTemplate: true,
			expectTemplateScope:    aimv1alpha1.AIMResolutionScopeCluster,
		},
		{
			name: "template cache ready - sets cache reference",
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					service: NewService("svc").Build(),
					templateCache: controllerutils.FetchResult[*aimv1alpha1.AIMTemplateCache]{
						Value: &aimv1alpha1.AIMTemplateCache{
							Status: aimv1alpha1.AIMTemplateCacheStatus{
								Status: constants.AIMStatusReady,
							},
						},
					},
				},
			},
			expectCache: true,
		},
		{
			name: "template cache not ready - no cache reference",
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					service: NewService("svc").Build(),
					templateCache: controllerutils.FetchResult[*aimv1alpha1.AIMTemplateCache]{
						Value: &aimv1alpha1.AIMTemplateCache{
							Status: aimv1alpha1.AIMTemplateCacheStatus{
								Status: constants.AIMStatusProgressing,
							},
						},
					},
				},
			},
			expectCache: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := &aimv1alpha1.AIMServiceStatus{}
			r.DecorateStatus(status, nil, tt.obs)

			if tt.expectResolvedModel {
				if status.ResolvedModel == nil {
					t.Error("expected ResolvedModel to be set")
				} else if status.ResolvedModel.Scope != tt.expectModelScope {
					t.Errorf("expected model scope %s, got %s", tt.expectModelScope, status.ResolvedModel.Scope)
				}
			} else {
				if status.ResolvedModel != nil {
					t.Error("unexpected ResolvedModel")
				}
			}

			if tt.expectResolvedTemplate {
				if status.ResolvedTemplate == nil {
					t.Error("expected ResolvedTemplate to be set")
				} else if status.ResolvedTemplate.Scope != tt.expectTemplateScope {
					t.Errorf("expected template scope %s, got %s", tt.expectTemplateScope, status.ResolvedTemplate.Scope)
				}
			} else {
				if status.ResolvedTemplate != nil {
					t.Error("unexpected ResolvedTemplate")
				}
			}

			if tt.expectCache {
				if status.Cache == nil || status.Cache.TemplateCacheRef == nil {
					t.Error("expected Cache.TemplateCacheRef to be set")
				}
			} else {
				if status.Cache != nil && status.Cache.TemplateCacheRef != nil {
					t.Error("unexpected Cache.TemplateCacheRef")
				}
			}
		})
	}
}

// ============================================================================
// GET RESOLVED TEMPLATE TESTS
// ============================================================================

func TestGetResolvedTemplate(t *testing.T) {
	tests := []struct {
		name            string
		obs             ServiceObservation
		expectName      string
		expectNamespace string
		expectNsSpec    bool
		expectStatus    bool
	}{
		{
			name: "no template",
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{},
			},
			expectName: "",
		},
		{
			name: "namespace template",
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					template: controllerutils.FetchResult[*aimv1alpha1.AIMServiceTemplate]{
						Value: NewTemplate("ns-template").Build(),
					},
				},
			},
			expectName:      "ns-template",
			expectNamespace: testNamespace,
			expectNsSpec:    true,
			expectStatus:    true,
		},
		{
			name: "cluster template",
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					clusterTemplate: controllerutils.FetchResult[*aimv1alpha1.AIMClusterServiceTemplate]{
						Value: NewClusterTemplate("cluster-template").Build(),
					},
				},
			},
			expectName:      "cluster-template",
			expectNamespace: "",
			expectNsSpec:    true, // Now returns common spec for cluster templates too
			expectStatus:    true,
		},
		{
			name: "namespace takes precedence",
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					template: controllerutils.FetchResult[*aimv1alpha1.AIMServiceTemplate]{
						Value: NewTemplate("ns-template").Build(),
					},
					clusterTemplate: controllerutils.FetchResult[*aimv1alpha1.AIMClusterServiceTemplate]{
						Value: NewClusterTemplate("cluster-template").Build(),
					},
				},
			},
			expectName:      "ns-template",
			expectNamespace: testNamespace,
			expectNsSpec:    true,
			expectStatus:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, namespace, nsSpec, status := tt.obs.getResolvedTemplate()

			if name != tt.expectName {
				t.Errorf("expected name %s, got %s", tt.expectName, name)
			}

			if namespace != tt.expectNamespace {
				t.Errorf("expected namespace %s, got %s", tt.expectNamespace, namespace)
			}

			if tt.expectNsSpec && nsSpec == nil {
				t.Error("expected nsSpec to be set")
			}
			if !tt.expectNsSpec && nsSpec != nil {
				t.Error("unexpected nsSpec")
			}

			if tt.expectStatus && status == nil {
				t.Error("expected status to be set")
			}
			if !tt.expectStatus && status != nil {
				t.Error("unexpected status")
			}
		})
	}
}

// hpaWithScalingActive returns a minimal HPA whose ScalingActive condition
// carries the given status/reason.
func hpaWithScalingActive(status corev1.ConditionStatus, reason string) *autoscalingv2.HorizontalPodAutoscaler {
	return &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Name: "hpa"},
		Status: autoscalingv2.HorizontalPodAutoscalerStatus{
			Conditions: []autoscalingv2.HorizontalPodAutoscalerCondition{
				{
					Type:    autoscalingv2.AbleToScale,
					Status:  corev1.ConditionTrue,
					Reason:  "SucceededGetScale",
					Message: "the HPA controller was able to get the target's current scale",
				},
				{
					Type:   autoscalingv2.ScalingActive,
					Status: status,
					Reason: reason,
				},
			},
		},
	}
}

const (
	testActivationMetricName = "s0-test-service"
	testUserMetricName       = "s1-test-service"
)

func externalMetricSpec(name string) autoscalingv2.MetricSpec {
	return autoscalingv2.MetricSpec{
		Type: autoscalingv2.ExternalMetricSourceType,
		External: &autoscalingv2.ExternalMetricSource{
			Metric: autoscalingv2.MetricIdentifier{Name: name},
			Target: autoscalingv2.MetricTarget{Type: autoscalingv2.AverageValueMetricType},
		},
	}
}

func externalMetricStatus(name, value string) autoscalingv2.MetricStatus {
	quantity := resource.MustParse(value)
	return autoscalingv2.MetricStatus{
		Type: autoscalingv2.ExternalMetricSourceType,
		External: &autoscalingv2.ExternalMetricStatus{
			Metric:  autoscalingv2.MetricIdentifier{Name: name},
			Current: autoscalingv2.MetricValueStatus{AverageValue: &quantity},
		},
	}
}

func hpaWithExternalMetrics(
	status corev1.ConditionStatus,
	reason string,
	specMetricNames []string,
	currentMetrics []autoscalingv2.MetricStatus,
) *autoscalingv2.HorizontalPodAutoscaler {
	hpa := hpaWithScalingActive(status, reason)
	hpa.Generation = 1
	hpa.Status.ObservedGeneration = ptr.To(int64(1))
	for _, name := range specMetricNames {
		hpa.Spec.Metrics = append(hpa.Spec.Metrics, externalMetricSpec(name))
	}
	hpa.Status.CurrentMetrics = currentMetrics
	return hpa
}

func TestIsScaleToZero(t *testing.T) {
	tests := []struct {
		name        string
		minReplicas *int32
		want        bool
	}{
		{name: "unset (nil) - not scale-to-zero", minReplicas: nil, want: false},
		{name: "minReplicas=0 - scale-to-zero", minReplicas: ptr.To(int32(0)), want: true},
		{name: "minReplicas=1 - not scale-to-zero", minReplicas: ptr.To(int32(1)), want: false},
		{name: "minReplicas=3 - not scale-to-zero", minReplicas: ptr.To(int32(3)), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService("svc").WithModelImage("test-image:v1").Build()
			svc.Spec.MinReplicas = tt.minReplicas
			if got := isScaleToZero(svc); got != tt.want {
				t.Errorf("isScaleToZero=%v, want %v", got, tt.want)
			}
		})
	}

	if got := isScaleToZero(nil); got {
		t.Errorf("isScaleToZero(nil)=true, want false")
	}
}

func TestServiceObservation_IsScaledToZero(t *testing.T) {
	tests := []struct {
		name        string
		minReplicas *int32
		hpa         *autoscalingv2.HorizontalPodAutoscaler
		pods        *corev1.PodList
		want        bool
	}{
		{
			name:        "scale-to-zero opted in and HPA reports ScalingDisabled - idle",
			minReplicas: ptr.To(int32(0)),
			hpa:         hpaWithScalingActive(corev1.ConditionFalse, hpaReasonScalingDisabled),
			want:        true,
		},
		{
			name:        "scale-to-zero opted in but HPA ScalingActive=True - not idle (running)",
			minReplicas: ptr.To(int32(0)),
			hpa:         hpaWithScalingActive(corev1.ConditionTrue, "ValidMetricFound"),
			want:        false,
		},
		{
			// Mirrors getHPAHealth: a non-authoritative ScalingActive=False
			// reason with zero pods is still idle (the activation metric series
			// just isn't available yet).
			name:        "scale-to-zero, ScalingActive=False non-authoritative reason, zero pods - idle",
			minReplicas: ptr.To(int32(0)),
			hpa:         hpaWithScalingActive(corev1.ConditionFalse, "FailedGetExternalMetric"),
			pods:        podsWithReady(0, 0),
			want:        true,
		},
		{
			name:        "scale-to-zero, ScalingActive=False non-authoritative reason, pods running - not idle",
			minReplicas: ptr.To(int32(0)),
			hpa:         hpaWithScalingActive(corev1.ConditionFalse, "FailedGetExternalMetric"),
			pods:        podsWithReady(1, 1),
			want:        false,
		},
		{
			// Regression: the HPA exists but has not emitted a ScalingActive
			// condition yet (scalingActive==nil). With zero pods this is idle;
			// the old predicate returned false here, leaving the service stuck
			// reporting NoPods / Starting.
			name:        "scale-to-zero, ScalingActive not emitted yet, zero pods - idle",
			minReplicas: ptr.To(int32(0)),
			hpa:         &autoscalingv2.HorizontalPodAutoscaler{ObjectMeta: metav1.ObjectMeta{Name: "hpa"}},
			pods:        podsWithReady(0, 0),
			want:        true,
		},
		{
			name:        "scale-to-zero, ScalingActive not emitted yet, pods running - not idle",
			minReplicas: ptr.To(int32(0)),
			hpa:         &autoscalingv2.HorizontalPodAutoscaler{ObjectMeta: metav1.ObjectMeta{Name: "hpa"}},
			pods:        podsWithReady(1, 0),
			want:        false,
		},
		{
			name:        "minReplicas=1 with ScalingDisabled - not scale-to-zero (something is wrong)",
			minReplicas: ptr.To(int32(1)),
			hpa:         hpaWithScalingActive(corev1.ConditionFalse, hpaReasonScalingDisabled),
			want:        false,
		},
		{
			name:        "scale-to-zero opted in but HPA not fetched yet - not idle yet",
			minReplicas: ptr.To(int32(0)),
			hpa:         nil,
			want:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService("svc").WithModelImage("test-image:v1").Build()
			svc.Spec.MinReplicas = tt.minReplicas

			fetch := ServiceFetchResult{
				service: svc,
				hpa: controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler]{
					Value: tt.hpa,
				},
			}
			if tt.pods != nil {
				fetch.inferenceServicePods = &controllerutils.FetchResult[*corev1.PodList]{
					Value: tt.pods,
				}
			}
			obs := ServiceObservation{ServiceFetchResult: fetch}

			if got := obs.isScaledToZero(); got != tt.want {
				t.Errorf("isScaledToZero=%v, want %v", got, tt.want)
			}
		})
	}
}

// awaitingCondition returns an existing ActivationMetricAvailable condition in
// the Unknown/Awaiting state whose clock started `age` ago.
func awaitingCondition(age time.Duration, now time.Time) *metav1.Condition {
	return &metav1.Condition{
		Type:               aimv1alpha1.AIMServiceConditionActivationMetricAvailable,
		Status:             metav1.ConditionUnknown,
		Reason:             aimv1alpha1.AIMServiceReasonAwaitingActivationMetric,
		LastTransitionTime: metav1.NewTime(now.Add(-age)),
	}
}

func TestActivationMetricCondition(t *testing.T) {
	t.Setenv(constants.EnvAIMGatewayActivationScope, constants.GatewayActivationScopeHTTPRoute)
	now := time.Now()

	tests := []struct {
		name                  string
		minReplicas           *int32
		routingDisabled       bool
		runtimeConfig         *aimv1alpha1.AIMRuntimeConfigCommon
		userMetric            bool
		hpa                   *autoscalingv2.HorizontalPodAutoscaler
		podCount              int
		existing              *metav1.Condition
		wantOK                bool
		wantStatus            metav1.ConditionStatus
		wantReason            string
		wantMessage           string
		wantAdditionalMessage string
	}{
		{
			name:        "not scale-to-zero - not reported",
			minReplicas: ptr.To(int32(1)),
			hpa:         hpaWithScalingActive(corev1.ConditionFalse, "FailedGetExternalMetric"),
			podCount:    1,
			wantOK:      false,
		},
		{
			// At zero replicas an absent series is the design's resting state:
			// the gateway Lua counter only exists once a request creates it.
			name:        "scale-to-zero at zero replicas - not reported",
			minReplicas: ptr.To(int32(0)),
			hpa:         hpaWithScalingActive(corev1.ConditionFalse, "FailedGetExternalMetric"),
			podCount:    0,
			wantOK:      false,
		},
		{
			name:        "HPA not observable - not reported",
			minReplicas: ptr.To(int32(0)),
			hpa:         nil,
			podCount:    1,
			wantOK:      false,
		},
		{
			name:            "scale-to-zero with routing disabled - activation condition not relevant",
			minReplicas:     ptr.To(int32(0)),
			routingDisabled: true,
			hpa: hpaWithExternalMetrics(
				corev1.ConditionTrue,
				"ValidMetricFound",
				[]string{testActivationMetricName},
				[]autoscalingv2.MetricStatus{externalMetricStatus(testActivationMetricName, "1")},
			),
			podCount: 1,
			wantOK:   false,
		},
		{
			name:        "scale-to-zero with invalid activation query - configuration health owns reporting",
			minReplicas: ptr.To(int32(0)),
			runtimeConfig: &aimv1alpha1.AIMRuntimeConfigCommon{
				AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
					ScaleFromZero: &aimv1alpha1.AIMScaleFromZeroConfig{
						ActivationMetricQueryTemplate: `sum(requests{unsupported="${unsupported}"})`,
					},
				},
			},
			hpa: hpaWithExternalMetrics(
				corev1.ConditionTrue,
				"ValidMetricFound",
				[]string{testActivationMetricName},
				[]autoscalingv2.MetricStatus{externalMetricStatus(testActivationMetricName, "1")},
			),
			podCount: 1,
			wantOK:   false,
		},
		{
			name:        "custom activation query with s0 reported - metric readable",
			minReplicas: ptr.To(int32(0)),
			runtimeConfig: &aimv1alpha1.AIMRuntimeConfigCommon{
				AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
					ScaleFromZero: &aimv1alpha1.AIMScaleFromZeroConfig{
						ActivationMetricQueryTemplate: `sum(custom_gateway_requests{namespace="${namespace}"})`,
					},
				},
			},
			hpa: hpaWithExternalMetrics(
				corev1.ConditionTrue,
				"ValidMetricFound",
				[]string{testActivationMetricName},
				[]autoscalingv2.MetricStatus{externalMetricStatus(testActivationMetricName, "1")},
			),
			podCount:    1,
			wantOK:      true,
			wantStatus:  metav1.ConditionTrue,
			wantReason:  aimv1alpha1.AIMServiceReasonActivationMetricAvailable,
			wantMessage: testActivationMetricName,
		},
		{
			name:        "stale user-only s0 HPA - awaits activation plus user metric shape",
			minReplicas: ptr.To(int32(0)),
			userMetric:  true,
			hpa: hpaWithExternalMetrics(
				corev1.ConditionTrue,
				"ValidMetricFound",
				[]string{testActivationMetricName},
				[]autoscalingv2.MetricStatus{externalMetricStatus(testActivationMetricName, "1")},
			),
			podCount:    1,
			wantOK:      true,
			wantStatus:  metav1.ConditionUnknown,
			wantReason:  aimv1alpha1.AIMServiceReasonAwaitingActivationMetric,
			wantMessage: "expected 2",
		},
		{
			name:        "s0 and s1 reported - zero-activation metric readable",
			minReplicas: ptr.To(int32(0)),
			userMetric:  true,
			hpa: hpaWithExternalMetrics(
				corev1.ConditionTrue,
				"ValidMetricFound",
				[]string{testActivationMetricName, testUserMetricName},
				[]autoscalingv2.MetricStatus{
					externalMetricStatus(testActivationMetricName, "1"),
					externalMetricStatus(testUserMetricName, "4"),
				},
			),
			podCount:    1,
			wantOK:      true,
			wantStatus:  metav1.ConditionTrue,
			wantReason:  aimv1alpha1.AIMServiceReasonActivationMetricAvailable,
			wantMessage: testActivationMetricName,
		},
		{
			name:        "s0 reported with observed generation absent - metric readable",
			minReplicas: ptr.To(int32(0)),
			hpa: func() *autoscalingv2.HorizontalPodAutoscaler {
				hpa := hpaWithExternalMetrics(
					corev1.ConditionTrue,
					"ValidMetricFound",
					[]string{testActivationMetricName},
					[]autoscalingv2.MetricStatus{externalMetricStatus(testActivationMetricName, "1")},
				)
				hpa.Status.ObservedGeneration = nil
				return hpa
			}(),
			podCount:    1,
			wantOK:      true,
			wantStatus:  metav1.ConditionTrue,
			wantReason:  aimv1alpha1.AIMServiceReasonActivationMetricAvailable,
			wantMessage: testActivationMetricName,
		},
		{
			name:        "s0 reported as zero - zero-activation metric readable",
			minReplicas: ptr.To(int32(0)),
			userMetric:  true,
			hpa: hpaWithExternalMetrics(
				corev1.ConditionFalse,
				"FailedGetExternalMetric",
				[]string{testActivationMetricName, testUserMetricName},
				[]autoscalingv2.MetricStatus{
					externalMetricStatus(testActivationMetricName, "0"),
					{},
				},
			),
			podCount:    1,
			wantOK:      true,
			wantStatus:  metav1.ConditionTrue,
			wantReason:  aimv1alpha1.AIMServiceReasonActivationMetricAvailable,
			wantMessage: testActivationMetricName,
		},
		{
			name:        "ScalingActive true with only s1 reported - awaits broken s0",
			minReplicas: ptr.To(int32(0)),
			userMetric:  true,
			hpa: hpaWithExternalMetrics(
				corev1.ConditionTrue,
				"ValidMetricFound",
				[]string{testActivationMetricName, testUserMetricName},
				[]autoscalingv2.MetricStatus{
					{},
					externalMetricStatus(testUserMetricName, "1"),
				},
			),
			podCount:              1,
			wantOK:                true,
			wantStatus:            metav1.ConditionUnknown,
			wantReason:            aimv1alpha1.AIMServiceReasonAwaitingActivationMetric,
			wantMessage:           testActivationMetricName,
			wantAdditionalMessage: testUserMetricName,
		},
		{
			name:        "ScalingActive true with only s1 beyond grace - s0 unavailable",
			minReplicas: ptr.To(int32(0)),
			userMetric:  true,
			hpa: hpaWithExternalMetrics(
				corev1.ConditionTrue,
				"ValidMetricFound",
				[]string{testActivationMetricName, testUserMetricName},
				[]autoscalingv2.MetricStatus{
					{},
					externalMetricStatus(testUserMetricName, "1"),
				},
			),
			podCount:              1,
			existing:              awaitingCondition(activationMetricGracePeriod+time.Minute, now),
			wantOK:                true,
			wantStatus:            metav1.ConditionFalse,
			wantReason:            aimv1alpha1.AIMServiceReasonActivationMetricUnavailable,
			wantMessage:           testActivationMetricName,
			wantAdditionalMessage: testUserMetricName,
		},
		{
			name:        "ScalingActive false with only s0 reported - metric readable",
			minReplicas: ptr.To(int32(0)),
			userMetric:  true,
			hpa: hpaWithExternalMetrics(
				corev1.ConditionFalse,
				"FailedGetExternalMetric",
				[]string{testActivationMetricName, testUserMetricName},
				[]autoscalingv2.MetricStatus{
					externalMetricStatus(testActivationMetricName, "2"),
					{},
				},
			),
			podCount:    1,
			wantOK:      true,
			wantStatus:  metav1.ConditionTrue,
			wantReason:  aimv1alpha1.AIMServiceReasonActivationMetricAvailable,
			wantMessage: testActivationMetricName,
		},
		{
			name:        "ScalingDisabled without s0 status - awaits metric evidence",
			minReplicas: ptr.To(int32(0)),
			hpa: hpaWithExternalMetrics(
				corev1.ConditionFalse,
				hpaReasonScalingDisabled,
				[]string{testActivationMetricName},
				nil,
			),
			podCount:    1,
			wantOK:      true,
			wantStatus:  metav1.ConditionUnknown,
			wantReason:  aimv1alpha1.AIMServiceReasonAwaitingActivationMetric,
			wantMessage: testActivationMetricName,
		},
		{
			name:        "missing s0 spec - awaits zero-activation metric",
			minReplicas: ptr.To(int32(0)),
			userMetric:  true,
			hpa: hpaWithExternalMetrics(
				corev1.ConditionTrue,
				"ValidMetricFound",
				[]string{testUserMetricName},
				[]autoscalingv2.MetricStatus{externalMetricStatus(testUserMetricName, "1")},
			),
			podCount:    1,
			wantOK:      true,
			wantStatus:  metav1.ConditionUnknown,
			wantReason:  aimv1alpha1.AIMServiceReasonAwaitingActivationMetric,
			wantMessage: "expected 2",
		},
		{
			name:        "duplicate s0 spec - awaits unambiguous zero-activation metric",
			minReplicas: ptr.To(int32(0)),
			userMetric:  true,
			hpa: hpaWithExternalMetrics(
				corev1.ConditionTrue,
				"ValidMetricFound",
				[]string{testActivationMetricName, "s0-duplicate"},
				[]autoscalingv2.MetricStatus{externalMetricStatus(testActivationMetricName, "1")},
			),
			podCount:    1,
			wantOK:      true,
			wantStatus:  metav1.ConditionUnknown,
			wantReason:  aimv1alpha1.AIMServiceReasonAwaitingActivationMetric,
			wantMessage: "contains 2",
		},
		{
			name:        "malformed s0 spec - awaits valid zero-activation metric",
			minReplicas: ptr.To(int32(0)),
			hpa: hpaWithExternalMetrics(
				corev1.ConditionTrue,
				"ValidMetricFound",
				[]string{"s0-"},
				nil,
			),
			podCount:    1,
			wantOK:      true,
			wantStatus:  metav1.ConditionUnknown,
			wantReason:  aimv1alpha1.AIMServiceReasonAwaitingActivationMetric,
			wantMessage: "malformed",
		},
		{
			name:        "duplicate s0 current status - awaits unambiguous evidence",
			minReplicas: ptr.To(int32(0)),
			hpa: hpaWithExternalMetrics(
				corev1.ConditionTrue,
				"ValidMetricFound",
				[]string{testActivationMetricName},
				[]autoscalingv2.MetricStatus{
					externalMetricStatus(testActivationMetricName, "1"),
					externalMetricStatus(testActivationMetricName, "1"),
				},
			),
			podCount:    1,
			wantOK:      true,
			wantStatus:  metav1.ConditionUnknown,
			wantReason:  aimv1alpha1.AIMServiceReasonAwaitingActivationMetric,
			wantMessage: "contains 2",
		},
		{
			name:        "matching s0 without a current value - awaits readable evidence",
			minReplicas: ptr.To(int32(0)),
			hpa: hpaWithExternalMetrics(
				corev1.ConditionTrue,
				"ValidMetricFound",
				[]string{testActivationMetricName},
				[]autoscalingv2.MetricStatus{{
					Type: autoscalingv2.ExternalMetricSourceType,
					External: &autoscalingv2.ExternalMetricStatus{
						Metric: autoscalingv2.MetricIdentifier{Name: testActivationMetricName},
					},
				}},
			),
			podCount:    1,
			wantOK:      true,
			wantStatus:  metav1.ConditionUnknown,
			wantReason:  aimv1alpha1.AIMServiceReasonAwaitingActivationMetric,
			wantMessage: "has no current value",
		},
		{
			name:        "stale HPA status - awaits current generation",
			minReplicas: ptr.To(int32(0)),
			hpa: func() *autoscalingv2.HorizontalPodAutoscaler {
				hpa := hpaWithExternalMetrics(
					corev1.ConditionTrue,
					"ValidMetricFound",
					[]string{testActivationMetricName},
					[]autoscalingv2.MetricStatus{externalMetricStatus(testActivationMetricName, "1")},
				)
				hpa.Generation = 2
				return hpa
			}(),
			podCount:    1,
			wantOK:      true,
			wantStatus:  metav1.ConditionUnknown,
			wantReason:  aimv1alpha1.AIMServiceReasonAwaitingActivationMetric,
			wantMessage: "generation 1",
		},
		{
			name:        "unreadable, no prior condition - awaiting within grace",
			minReplicas: ptr.To(int32(0)),
			hpa:         hpaWithScalingActive(corev1.ConditionFalse, "FailedGetExternalMetric"),
			podCount:    1,
			wantOK:      true,
			wantStatus:  metav1.ConditionUnknown,
			wantReason:  aimv1alpha1.AIMServiceReasonAwaitingActivationMetric,
		},
		{
			name:        "ScalingActive not emitted yet - awaiting within grace",
			minReplicas: ptr.To(int32(0)),
			hpa:         &autoscalingv2.HorizontalPodAutoscaler{ObjectMeta: metav1.ObjectMeta{Name: "hpa"}},
			podCount:    1,
			wantOK:      true,
			wantStatus:  metav1.ConditionUnknown,
			wantReason:  aimv1alpha1.AIMServiceReasonAwaitingActivationMetric,
		},
		{
			name:        "unreadable but still inside grace period - stays awaiting",
			minReplicas: ptr.To(int32(0)),
			hpa:         hpaWithScalingActive(corev1.ConditionFalse, "FailedGetExternalMetric"),
			podCount:    1,
			existing:    awaitingCondition(activationMetricGracePeriod/2, now),
			wantOK:      true,
			wantStatus:  metav1.ConditionUnknown,
			wantReason:  aimv1alpha1.AIMServiceReasonAwaitingActivationMetric,
		},
		{
			name:        "unreadable beyond grace period - escalates to unavailable",
			minReplicas: ptr.To(int32(0)),
			hpa:         hpaWithScalingActive(corev1.ConditionFalse, "FailedGetExternalMetric"),
			podCount:    1,
			existing:    awaitingCondition(activationMetricGracePeriod+time.Minute, now),
			wantOK:      true,
			wantStatus:  metav1.ConditionFalse,
			wantReason:  aimv1alpha1.AIMServiceReasonActivationMetricUnavailable,
		},
		{
			name:        "already escalated - does not de-escalate to awaiting",
			minReplicas: ptr.To(int32(0)),
			hpa:         hpaWithScalingActive(corev1.ConditionFalse, "FailedGetExternalMetric"),
			podCount:    1,
			existing: &metav1.Condition{
				Type:               aimv1alpha1.AIMServiceConditionActivationMetricAvailable,
				Status:             metav1.ConditionFalse,
				Reason:             aimv1alpha1.AIMServiceReasonActivationMetricUnavailable,
				LastTransitionTime: metav1.NewTime(now),
			},
			wantOK:     true,
			wantStatus: metav1.ConditionFalse,
			wantReason: aimv1alpha1.AIMServiceReasonActivationMetricUnavailable,
		},
		{
			// Recovering from a readable signal restarts the debounce clock.
			name:        "recovering from readable - restarts grace clock",
			minReplicas: ptr.To(int32(0)),
			hpa:         hpaWithScalingActive(corev1.ConditionFalse, "FailedGetExternalMetric"),
			podCount:    1,
			existing: &metav1.Condition{
				Type:               aimv1alpha1.AIMServiceConditionActivationMetricAvailable,
				Status:             metav1.ConditionTrue,
				Reason:             aimv1alpha1.AIMServiceReasonActivationMetricAvailable,
				LastTransitionTime: metav1.NewTime(now.Add(-time.Hour)),
			},
			wantOK:     true,
			wantStatus: metav1.ConditionUnknown,
			wantReason: aimv1alpha1.AIMServiceReasonAwaitingActivationMetric,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService("svc").WithModelImage("test-image:v1").Build()
			svc.Spec.MinReplicas = tt.minReplicas
			svc.Spec.Routing = &aimv1alpha1.AIMRuntimeRoutingConfig{Enabled: ptr.To(!tt.routingDisabled)}
			if tt.userMetric {
				svc.Spec.AutoScaling = &aimv1alpha1.AIMServiceAutoScaling{
					Metrics: []aimv1alpha1.AIMServiceMetricsSpec{validVLLMMetric()},
				}
			}

			status, reason, message, ok := activationMetricCondition(
				svc,
				tt.runtimeConfig,
				controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler]{Value: tt.hpa},
				tt.podCount,
				tt.existing,
				now,
			)

			if ok != tt.wantOK {
				t.Fatalf("ok=%v, want %v", ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if status != tt.wantStatus {
				t.Errorf("status=%q, want %q", status, tt.wantStatus)
			}
			if reason != tt.wantReason {
				t.Errorf("reason=%q, want %q", reason, tt.wantReason)
			}
			if message == "" {
				t.Error("expected a non-empty message")
			}
			if tt.wantMessage != "" && !strings.Contains(message, tt.wantMessage) {
				t.Errorf("message=%q, want it to contain %q", message, tt.wantMessage)
			}
			if tt.wantAdditionalMessage != "" && !strings.Contains(message, tt.wantAdditionalMessage) {
				t.Errorf("message=%q, want it to contain %q", message, tt.wantAdditionalMessage)
			}
		})
	}
}

func TestActivationMetricRequeueAfter(t *testing.T) {
	t.Setenv(constants.EnvAIMGatewayActivationScope, constants.GatewayActivationScopeHTTPRoute)
	now := time.Date(2026, time.August, 11, 10, 0, 0, 0, time.UTC)
	unreadableHPA := controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler]{
		Value: hpaWithExternalMetrics(
			corev1.ConditionFalse,
			"FailedGetExternalMetric",
			[]string{testActivationMetricName},
			nil,
		),
	}
	readableHPA := controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler]{
		Value: hpaWithExternalMetrics(
			corev1.ConditionTrue,
			"ValidMetricFound",
			[]string{testActivationMetricName},
			[]autoscalingv2.MetricStatus{externalMetricStatus(testActivationMetricName, "1")},
		),
	}

	tests := []struct {
		name       string
		minReplica int32
		hpa        controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler]
		podCount   int
		existing   *metav1.Condition
		want       time.Duration
	}{
		{
			name:       "new awaiting condition schedules the full grace period",
			minReplica: 0,
			hpa:        unreadableHPA,
			podCount:   1,
			want:       activationMetricGracePeriod,
		},
		{
			name:       "existing awaiting condition schedules its remaining grace period",
			minReplica: 0,
			hpa:        unreadableHPA,
			podCount:   1,
			existing:   awaitingCondition(time.Minute, now),
			want:       activationMetricGracePeriod - time.Minute,
		},
		{
			name:       "expired grace period does not schedule another timer",
			minReplica: 0,
			hpa:        unreadableHPA,
			podCount:   1,
			existing:   awaitingCondition(activationMetricGracePeriod, now),
			want:       0,
		},
		{
			name:       "condition beyond grace does not schedule another timer",
			minReplica: 0,
			hpa:        unreadableHPA,
			podCount:   1,
			existing:   awaitingCondition(activationMetricGracePeriod+time.Minute, now),
			want:       0,
		},
		{
			name:       "readable metric does not schedule a timer",
			minReplica: 0,
			hpa:        readableHPA,
			podCount:   1,
			existing:   awaitingCondition(time.Minute, now),
			want:       0,
		},
		{
			name:       "transition from readable restarts the full grace period",
			minReplica: 0,
			hpa:        unreadableHPA,
			podCount:   1,
			existing: &metav1.Condition{
				Type:               aimv1alpha1.AIMServiceConditionActivationMetricAvailable,
				Status:             metav1.ConditionTrue,
				Reason:             aimv1alpha1.AIMServiceReasonActivationMetricAvailable,
				LastTransitionTime: metav1.NewTime(now.Add(-time.Hour)),
			},
			want: activationMetricGracePeriod,
		},
		{
			name:       "future transition time is clamped to the grace period",
			minReplica: 0,
			hpa:        unreadableHPA,
			podCount:   1,
			existing:   awaitingCondition(-time.Minute, now),
			want:       activationMetricGracePeriod,
		},
		{
			name:       "scaled-to-zero service does not schedule a timer",
			minReplica: 0,
			hpa:        unreadableHPA,
			podCount:   0,
			want:       0,
		},
		{
			name:       "non-zero minimum does not schedule a timer",
			minReplica: 1,
			hpa:        unreadableHPA,
			podCount:   1,
			want:       0,
		},
		{
			name:       "already unavailable does not schedule a timer",
			minReplica: 0,
			hpa:        unreadableHPA,
			podCount:   1,
			existing: &metav1.Condition{
				Type:               aimv1alpha1.AIMServiceConditionActivationMetricAvailable,
				Status:             metav1.ConditionFalse,
				Reason:             aimv1alpha1.AIMServiceReasonActivationMetricUnavailable,
				LastTransitionTime: metav1.NewTime(now),
			},
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService("svc").WithModelImage("test-image:v1").Build()
			svc.Spec.MinReplicas = ptr.To(tt.minReplica)
			svc.Spec.Routing = &aimv1alpha1.AIMRuntimeRoutingConfig{Enabled: ptr.To(true)}
			if tt.existing != nil {
				svc.Status.Conditions = []metav1.Condition{*tt.existing}
			}

			got := activationMetricRequeueAfter(svc, nil, tt.hpa, tt.podCount, now)
			if got != tt.want {
				t.Errorf("activationMetricRequeueAfter()=%s, want %s", got, tt.want)
			}
		})
	}
}

func TestPlanResourcesSchedulesActivationMetricGraceDeadline(t *testing.T) {
	t.Setenv(constants.EnvAIMGatewayActivationScope, constants.GatewayActivationScopeHTTPRoute)
	svc := NewService("svc").WithModelImage("test-image:v1").Build()
	svc.Spec.MinReplicas = ptr.To(int32(0))
	svc.Spec.Routing = &aimv1alpha1.AIMRuntimeRoutingConfig{Enabled: ptr.To(true)}

	obs := ServiceObservation{ServiceFetchResult: ServiceFetchResult{
		service: svc,
		hpa: controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler]{
			Value: hpaWithExternalMetrics(
				corev1.ConditionFalse,
				"FailedGetExternalMetric",
				[]string{testActivationMetricName},
				nil,
			),
		},
		inferenceServicePods: &controllerutils.FetchResult[*corev1.PodList]{
			Value: &corev1.PodList{Items: []corev1.Pod{{}}},
		},
	}}

	plan := (&ServiceReconciler{}).PlanResources(
		testContext(),
		controllerutils.ReconcileContext[*aimv1alpha1.AIMService]{Object: svc},
		obs,
	)
	if plan.RequeueAfter < activationMetricGracePeriod-10*time.Second ||
		plan.RequeueAfter > activationMetricGracePeriod {
		t.Errorf("RequeueAfter=%s, want approximately %s", plan.RequeueAfter, activationMetricGracePeriod)
	}
}

func TestSetActivationMetricCondition(t *testing.T) {
	t.Setenv(constants.EnvAIMGatewayActivationScope, constants.GatewayActivationScopeHTTPRoute)
	condType := aimv1alpha1.AIMServiceConditionActivationMetricAvailable

	// The condition must never participate in the Ready rollup, which the
	// framework keys off the component "Ready" suffix.
	if strings.HasSuffix(condType, controllerutils.ComponentConditionSuffix) {
		t.Fatalf("condition type %q must not end in %q or it would gate readiness",
			condType, controllerutils.ComponentConditionSuffix)
	}

	newSvc := func(minReplicas *int32) *aimv1alpha1.AIMService {
		svc := NewService("svc").WithModelImage("test-image:v1").Build()
		svc.Spec.MinReplicas = minReplicas
		svc.Spec.Routing = &aimv1alpha1.AIMRuntimeRoutingConfig{Enabled: ptr.To(true)}
		return svc
	}
	unreadableHPA := controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler]{
		Value: hpaWithScalingActive(corev1.ConditionFalse, "FailedGetExternalMetric"),
	}

	t.Run("sets the condition when applicable", func(t *testing.T) {
		cm := controllerutils.NewConditionManager(nil)
		setActivationMetricCondition(cm, newSvc(ptr.To(int32(0))), nil, unreadableHPA, 1)

		got := cm.Get(condType)
		if got == nil {
			t.Fatalf("expected %s to be set", condType)
		}
		if got.Reason != aimv1alpha1.AIMServiceReasonAwaitingActivationMetric {
			t.Errorf("reason=%q, want %q", got.Reason, aimv1alpha1.AIMServiceReasonAwaitingActivationMetric)
		}
	})

	t.Run("removes a stale condition once it no longer applies", func(t *testing.T) {
		cm := controllerutils.NewConditionManager([]metav1.Condition{{
			Type:   condType,
			Status: metav1.ConditionFalse,
			Reason: aimv1alpha1.AIMServiceReasonActivationMetricUnavailable,
		}})
		// Idled to zero replicas: absence is the resting state, so the
		// condition must not linger.
		setActivationMetricCondition(cm, newSvc(ptr.To(int32(0))), nil, unreadableHPA, 0)

		if got := cm.Get(condType); got != nil {
			t.Errorf("expected %s to be removed, got %+v", condType, got)
		}
	})

	t.Run("nil condition manager is a no-op", func(t *testing.T) {
		setActivationMetricCondition(nil, newSvc(ptr.To(int32(0))), nil, unreadableHPA, 1)
	})
}

func TestInferenceServiceReadyRequiresCurrentGeneration(t *testing.T) {
	isvc := readyISVC("stale")
	isvc.Generation = 2

	if inferenceServiceReady(controllerutils.FetchResult[*servingv1beta1.InferenceService]{
		Value: isvc,
	}) {
		t.Fatal("inferenceServiceReady() accepted stale Ready=True")
	}

	isvc.Status.ObservedGeneration = isvc.Generation
	if !inferenceServiceReady(controllerutils.FetchResult[*servingv1beta1.InferenceService]{
		Value: isvc,
	}) {
		t.Fatal("inferenceServiceReady() rejected current Ready=True")
	}
}

func TestGetInferenceServiceHealth_ReadyPodsOverrideStaleGeneration(t *testing.T) {
	isvc := readyISVC("activating")
	isvc.Generation = 2

	tests := []struct {
		name      string
		pods      *corev1.PodList
		wantState constants.AIMStatus
	}{
		{
			name:      "ready predictor pod proves service is running",
			pods:      podsWithReady(1, 1),
			wantState: constants.AIMStatusReady,
		},
		{
			name:      "no ready predictor pod preserves generation guard",
			pods:      podsWithReady(1, 0),
			wantState: constants.AIMStatusProgressing,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fetch := ServiceFetchResult{
				inferenceService: controllerutils.FetchResult[*servingv1beta1.InferenceService]{Value: isvc},
				inferenceServicePods: &controllerutils.FetchResult[*corev1.PodList]{
					Value: tt.pods,
				},
			}

			health := fetch.getInferenceServiceHealth()
			if health.State != tt.wantState {
				t.Fatalf("state = %q, want %q: %#v", health.State, tt.wantState, health)
			}
		})
	}
}

// readyISVC returns an InferenceService whose Ready condition is True.
func readyISVC(name string) *servingv1beta1.InferenceService {
	isvc := &servingv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Namespace:  testNamespace,
			Generation: 1,
		},
	}
	isvc.Status.ObservedGeneration = isvc.Generation
	isvc.Status.Conditions = duckv1.Conditions{
		{Type: "Ready", Status: corev1.ConditionTrue},
	}
	return isvc
}

// podsWithReady returns a Pod list of length `total`, of which `ready`
// pods carry PodReady=True. Used to drive observedPodCount in
// getHPAHealth tests.
func podsWithReady(total, ready int) *corev1.PodList {
	items := make([]corev1.Pod, 0, total)
	for i := 0; i < total; i++ {
		p := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("p%d", i), Namespace: testNamespace}}
		if i < ready {
			p.Status.Conditions = []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionTrue},
			}
		}
		items = append(items, p)
	}
	return &corev1.PodList{Items: items}
}

func TestGetHPAHealth_ScaleToZero(t *testing.T) {
	tests := []struct {
		name          string
		minReplicas   *int32
		maxReplicas   *int32
		hpa           *autoscalingv2.HorizontalPodAutoscaler
		pods          *corev1.PodList
		expectState   constants.AIMStatus
		expectReason  string
		expectMessage string
	}{
		{
			name:          "scale-to-zero idle is healthy via authoritative ScalingDisabled",
			minReplicas:   ptr.To(int32(0)),
			maxReplicas:   ptr.To(int32(3)),
			hpa:           hpaWithScalingActive(corev1.ConditionFalse, hpaReasonScalingDisabled),
			expectState:   constants.AIMStatusReady,
			expectReason:  aimv1alpha1.AIMServiceReasonScaledToZero,
			expectMessage: "Service is idle: KEDA has scaled the deployment to zero replicas; will scale up when the configured trigger becomes active",
		},
		{
			name:         "scale-to-zero opted in, HPA actively scaling - HPAOperational",
			minReplicas:  ptr.To(int32(0)),
			maxReplicas:  ptr.To(int32(3)),
			hpa:          hpaWithScalingActive(corev1.ConditionTrue, "ValidMetricFound"),
			expectState:  constants.AIMStatusReady,
			expectReason: "HPAOperational",
		},
		{
			name:         "minReplicas>=1 with ScalingDisabled is still MetricsFailed (not a legitimate idle)",
			minReplicas:  ptr.To(int32(1)),
			maxReplicas:  ptr.To(int32(3)),
			hpa:          hpaWithScalingActive(corev1.ConditionFalse, hpaReasonScalingDisabled),
			expectState:  constants.AIMStatusFailed,
			expectReason: "MetricsFailed",
		},
		{
			name:         "minReplicas>=1 with a real metrics error is still MetricsFailed",
			minReplicas:  ptr.To(int32(1)),
			maxReplicas:  ptr.To(int32(3)),
			hpa:          hpaWithScalingActive(corev1.ConditionFalse, "FailedGetExternalMetric"),
			expectState:  constants.AIMStatusFailed,
			expectReason: "MetricsFailed",
		},
		{
			name:         "scale-to-zero with FailedGetExternalMetric AND zero pods is healthy idle",
			minReplicas:  ptr.To(int32(0)),
			maxReplicas:  ptr.To(int32(3)),
			hpa:          hpaWithScalingActive(corev1.ConditionFalse, "FailedGetExternalMetric"),
			pods:         podsWithReady(0, 0),
			expectState:  constants.AIMStatusReady,
			expectReason: aimv1alpha1.AIMServiceReasonScaledToZero,
		},
		{
			name:         "scale-to-zero with FailedGetExternalMetric AND running pods is HPAOperational (does not gate readiness)",
			minReplicas:  ptr.To(int32(0)),
			maxReplicas:  ptr.To(int32(3)),
			hpa:          hpaWithScalingActive(corev1.ConditionFalse, "FailedGetExternalMetric"),
			pods:         podsWithReady(1, 1),
			expectState:  constants.AIMStatusReady,
			expectReason: "HPAOperational",
		},
		{
			name:         "scale-to-zero with no ScalingActive condition AND zero pods is healthy idle",
			minReplicas:  ptr.To(int32(0)),
			maxReplicas:  ptr.To(int32(3)),
			hpa:          &autoscalingv2.HorizontalPodAutoscaler{ObjectMeta: metav1.ObjectMeta{Name: "hpa"}},
			pods:         podsWithReady(0, 0),
			expectState:  constants.AIMStatusReady,
			expectReason: aimv1alpha1.AIMServiceReasonScaledToZero,
		},
		{
			name:         "scale-to-zero with no ScalingActive condition AND running pods is HPAOperational (does not gate readiness)",
			minReplicas:  ptr.To(int32(0)),
			maxReplicas:  ptr.To(int32(3)),
			hpa:          &autoscalingv2.HorizontalPodAutoscaler{ObjectMeta: metav1.ObjectMeta{Name: "hpa"}},
			pods:         podsWithReady(1, 0),
			expectState:  constants.AIMStatusReady,
			expectReason: "HPAOperational",
		},
		{
			name:         "scale-to-zero with pods running and aim-dummy not emitting the user metric stays Ready (regression: was Activating/Progressing)",
			minReplicas:  ptr.To(int32(0)),
			maxReplicas:  ptr.To(int32(3)),
			hpa:          hpaWithScalingActive(corev1.ConditionFalse, "FailedGetExternalMetric"),
			pods:         podsWithReady(2, 2),
			expectState:  constants.AIMStatusReady,
			expectReason: "HPAOperational",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService("svc").WithModelImage("test-image:v1").Build()
			svc.Spec.MinReplicas = tt.minReplicas
			svc.Spec.MaxReplicas = tt.maxReplicas

			fetch := ServiceFetchResult{
				service: svc,
				inferenceService: controllerutils.FetchResult[*servingv1beta1.InferenceService]{
					Value: readyISVC("svc-isvc"),
				},
				hpa: controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler]{
					Value: tt.hpa,
				},
			}
			if tt.pods != nil {
				fetch.inferenceServicePods = &controllerutils.FetchResult[*corev1.PodList]{
					Value: tt.pods,
				}
			}
			obs := ServiceObservation{ServiceFetchResult: fetch}

			got := obs.getHPAHealth()
			if got.State != tt.expectState {
				t.Errorf("state=%q, want %q (msg=%q)", got.State, tt.expectState, got.Message)
			}
			if got.Reason != tt.expectReason {
				t.Errorf("reason=%q, want %q (msg=%q)", got.Reason, tt.expectReason, got.Message)
			}
			if tt.expectMessage != "" && got.Message != tt.expectMessage {
				t.Errorf("message=%q, want %q", got.Message, tt.expectMessage)
			}
		})
	}
}

// TestGetComponentHealth_ScaleToZeroRequiresRouting verifies the template
// pipeline surfaces the invalid scale-from-zero-without-routing combination
// through GetComponentHealth so the state engine sets ConfigValid=False.
func TestGetComponentHealth_ScaleToZeroRequiresRouting(t *testing.T) {
	t.Setenv(constants.EnvAIMGatewayActivationScope, constants.GatewayActivationScopeHTTPRoute)

	svc := NewService("svc").WithModelImage("test-image:v1").Build()
	svc.Spec.MinReplicas = ptr.To(int32(0))
	svc.Spec.MaxReplicas = ptr.To(int32(3))

	obs := ServiceObservation{ServiceFetchResult: ServiceFetchResult{service: svc}}

	health := obs.GetComponentHealth(context.Background(), nil)

	var cfg *controllerutils.ComponentHealth
	for i := range health {
		if health[i].Component == ComponentScaleToZeroConfig {
			cfg = &health[i]
		}
	}
	if cfg == nil {
		t.Fatalf("expected a ScaleToZeroConfig component health entry when scale-to-zero is set without routing")
	}
	if cfg.State != constants.AIMStatusFailed {
		t.Errorf("ScaleToZeroConfig state=%q, want Failed", cfg.State)
	}
	if cfg.Reason != aimv1alpha1.AIMServiceReasonRoutingRequired {
		t.Errorf("ScaleToZeroConfig reason=%q, want %q", cfg.Reason, aimv1alpha1.AIMServiceReasonRoutingRequired)
	}

	// Enabling routing clears the condition entirely.
	svc.Spec.Routing = &aimv1alpha1.AIMRuntimeRoutingConfig{Enabled: ptr.To(true)}
	for _, h := range obs.GetComponentHealth(context.Background(), nil) {
		if h.Component == ComponentScaleToZeroConfig {
			t.Errorf("ScaleToZeroConfig should not be reported once routing is enabled")
		}
	}
}
