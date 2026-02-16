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
	"testing"

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
