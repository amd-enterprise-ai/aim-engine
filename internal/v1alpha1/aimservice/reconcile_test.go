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
	"testing"

	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	duckv1 "knative.dev/pkg/apis/duck/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
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

			totalResources := len(plan.GetToApply()) + len(plan.GetToApplyWithoutOwnerRef())
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

// readyISVC returns an InferenceService whose Ready condition is True.
func readyISVC(name string) *servingv1beta1.InferenceService {
	isvc := &servingv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
	}
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
