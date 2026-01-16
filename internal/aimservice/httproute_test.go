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
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

// ============================================================================
// GENERATE HTTPROUTE NAME TESTS
// ============================================================================

func TestGenerateHTTPRouteName(t *testing.T) {
	tests := []struct {
		name         string
		serviceName  string
		namespace    string
		wantContains []string
	}{
		{
			name:         "simple service",
			serviceName:  "my-service",
			namespace:    "my-namespace",
			wantContains: []string{"my-service"},
		},
		{
			name:         "long service name",
			serviceName:  "very-long-service-name-that-might-exceed-limits",
			namespace:    "default",
			wantContains: []string{}, // Just verify it doesn't error
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := GenerateHTTPRouteName(tt.serviceName, tt.namespace)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			for _, want := range tt.wantContains {
				if !strings.Contains(result, want) {
					t.Errorf("expected result to contain %q, got %q", want, result)
				}
			}

			// Verify k8s name constraints
			if len(result) > 63 {
				t.Errorf("name too long: %d chars", len(result))
			}
		})
	}
}

func TestGenerateHTTPRouteName_Deterministic(t *testing.T) {
	result1, _ := GenerateHTTPRouteName("svc", "ns")
	result2, _ := GenerateHTTPRouteName("svc", "ns")

	if result1 != result2 {
		t.Errorf("expected deterministic output, got %q and %q", result1, result2)
	}
}

// ============================================================================
// IS ROUTING ENABLED TESTS
// ============================================================================

func TestIsRoutingEnabled(t *testing.T) {
	tests := []struct {
		name          string
		service       *aimv1alpha1.AIMService
		runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon
		expected      bool
	}{
		{
			name:          "no routing config - disabled",
			service:       NewService("svc").Build(),
			runtimeConfig: nil,
			expected:      false,
		},
		{
			name: "service routing enabled",
			service: func() *aimv1alpha1.AIMService {
				svc := NewService("svc").Build()
				svc.Spec.Routing = &aimv1alpha1.AIMRuntimeRoutingConfig{
					Enabled: ptr.To(true),
				}
				return svc
			}(),
			runtimeConfig: nil,
			expected:      true,
		},
		{
			name: "service routing explicitly disabled",
			service: func() *aimv1alpha1.AIMService {
				svc := NewService("svc").Build()
				svc.Spec.Routing = &aimv1alpha1.AIMRuntimeRoutingConfig{
					Enabled: ptr.To(false),
				}
				return svc
			}(),
			runtimeConfig: nil,
			expected:      false,
		},
		{
			name:    "runtime config routing enabled",
			service: NewService("svc").Build(),
			runtimeConfig: &aimv1alpha1.AIMRuntimeConfigCommon{
				AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
					Routing: &aimv1alpha1.AIMRuntimeRoutingConfig{
						Enabled: ptr.To(true),
					},
				},
			},
			expected: true,
		},
		{
			name: "service overrides runtime config - disables",
			service: func() *aimv1alpha1.AIMService {
				svc := NewService("svc").Build()
				svc.Spec.Routing = &aimv1alpha1.AIMRuntimeRoutingConfig{
					Enabled: ptr.To(false),
				}
				return svc
			}(),
			runtimeConfig: &aimv1alpha1.AIMRuntimeConfigCommon{
				AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
					Routing: &aimv1alpha1.AIMRuntimeRoutingConfig{
						Enabled: ptr.To(true),
					},
				},
			},
			expected: false,
		},
		{
			name: "service overrides runtime config - enables",
			service: func() *aimv1alpha1.AIMService {
				svc := NewService("svc").Build()
				svc.Spec.Routing = &aimv1alpha1.AIMRuntimeRoutingConfig{
					Enabled: ptr.To(true),
				}
				return svc
			}(),
			runtimeConfig: &aimv1alpha1.AIMRuntimeConfigCommon{
				AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
					Routing: &aimv1alpha1.AIMRuntimeRoutingConfig{
						Enabled: ptr.To(false),
					},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isRoutingEnabled(tt.service, tt.runtimeConfig)

			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

// ============================================================================
// RESOLVE GATEWAY REF TESTS
// ============================================================================

func TestResolveGatewayRef(t *testing.T) {
	serviceGateway := &gatewayapiv1.ParentReference{
		Name:      "service-gateway",
		Namespace: ptr.To(gatewayapiv1.Namespace("svc-ns")),
	}
	runtimeGateway := &gatewayapiv1.ParentReference{
		Name:      "runtime-gateway",
		Namespace: ptr.To(gatewayapiv1.Namespace("runtime-ns")),
	}

	tests := []struct {
		name          string
		service       *aimv1alpha1.AIMService
		runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon
		expectName    gatewayapiv1.ObjectName
		expectNil     bool
	}{
		{
			name:          "no gateway ref",
			service:       NewService("svc").Build(),
			runtimeConfig: nil,
			expectNil:     true,
		},
		{
			name: "service-level gateway ref",
			service: func() *aimv1alpha1.AIMService {
				svc := NewService("svc").Build()
				svc.Spec.Routing = &aimv1alpha1.AIMRuntimeRoutingConfig{
					GatewayRef: serviceGateway,
				}
				return svc
			}(),
			runtimeConfig: nil,
			expectName:    "service-gateway",
		},
		{
			name:    "runtime config gateway ref",
			service: NewService("svc").Build(),
			runtimeConfig: &aimv1alpha1.AIMRuntimeConfigCommon{
				AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
					Routing: &aimv1alpha1.AIMRuntimeRoutingConfig{
						GatewayRef: runtimeGateway,
					},
				},
			},
			expectName: "runtime-gateway",
		},
		{
			name: "service overrides runtime config",
			service: func() *aimv1alpha1.AIMService {
				svc := NewService("svc").Build()
				svc.Spec.Routing = &aimv1alpha1.AIMRuntimeRoutingConfig{
					GatewayRef: serviceGateway,
				}
				return svc
			}(),
			runtimeConfig: &aimv1alpha1.AIMRuntimeConfigCommon{
				AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
					Routing: &aimv1alpha1.AIMRuntimeRoutingConfig{
						GatewayRef: runtimeGateway,
					},
				},
			},
			expectName: "service-gateway",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := resolveGatewayRef(tt.service, tt.runtimeConfig)

			if tt.expectNil {
				if result != nil {
					t.Errorf("expected nil, got %v", result)
				}
				return
			}

			if result == nil {
				t.Error("expected gateway ref, got nil")
				return
			}

			if result.Name != tt.expectName {
				t.Errorf("expected name %s, got %s", tt.expectName, result.Name)
			}
		})
	}
}

// ============================================================================
// RESOLVE PATH TEMPLATE TESTS
// ============================================================================

func TestResolvePathTemplate(t *testing.T) {
	tests := []struct {
		name          string
		service       *aimv1alpha1.AIMService
		runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon
		expected      string
	}{
		{
			name:          "default template",
			service:       NewService("svc").Build(),
			runtimeConfig: nil,
			expected:      DefaultPathTemplate,
		},
		{
			name: "service-level template",
			service: func() *aimv1alpha1.AIMService {
				svc := NewService("svc").Build()
				svc.Spec.Routing = &aimv1alpha1.AIMRuntimeRoutingConfig{
					PathTemplate: ptr.To("/custom/{{.Name}}"),
				}
				return svc
			}(),
			runtimeConfig: nil,
			expected:      "/custom/{{.Name}}",
		},
		{
			name:    "runtime config template",
			service: NewService("svc").Build(),
			runtimeConfig: &aimv1alpha1.AIMRuntimeConfigCommon{
				AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
					Routing: &aimv1alpha1.AIMRuntimeRoutingConfig{
						PathTemplate: ptr.To("/runtime/{{.Namespace}}/{{.Name}}"),
					},
				},
			},
			expected: "/runtime/{{.Namespace}}/{{.Name}}",
		},
		{
			name: "service overrides runtime config",
			service: func() *aimv1alpha1.AIMService {
				svc := NewService("svc").Build()
				svc.Spec.Routing = &aimv1alpha1.AIMRuntimeRoutingConfig{
					PathTemplate: ptr.To("/service/{{.Name}}"),
				}
				return svc
			}(),
			runtimeConfig: &aimv1alpha1.AIMRuntimeConfigCommon{
				AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
					Routing: &aimv1alpha1.AIMRuntimeRoutingConfig{
						PathTemplate: ptr.To("/runtime/{{.Name}}"),
					},
				},
			},
			expected: "/service/{{.Name}}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := resolvePathTemplate(tt.service, tt.runtimeConfig)

			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

// ============================================================================
// RENDER PATH TEMPLATE TESTS
// ============================================================================

func TestRenderPathTemplate(t *testing.T) {
	service := NewService("my-service").Build()
	service.Namespace = "my-namespace"
	service.UID = "test-uid-12345"
	service.Labels = map[string]string{
		"app": "llm",
	}

	tests := []struct {
		name         string
		pathTemplate string
		service      *aimv1alpha1.AIMService
		expected     string
		wantErr      bool
	}{
		{
			name:         "default template",
			pathTemplate: DefaultPathTemplate,
			service:      service,
			expected:     "/my-namespace/my-service",
		},
		{
			name:         "name only",
			pathTemplate: "/{{.Name}}",
			service:      service,
			expected:     "/my-service",
		},
		{
			name:         "namespace only",
			pathTemplate: "/{{.Namespace}}",
			service:      service,
			expected:     "/my-namespace",
		},
		{
			name:         "with UID",
			pathTemplate: "/svc/{{.UID}}",
			service:      service,
			expected:     "/svc/test-uid-12345",
		},
		{
			name:         "with labels",
			pathTemplate: "/{{.Labels.app}}/{{.Name}}",
			service:      service,
			expected:     "/llm/my-service",
		},
		{
			name:         "adds leading slash",
			pathTemplate: "{{.Namespace}}/{{.Name}}",
			service:      service,
			expected:     "/my-namespace/my-service",
		},
		{
			name:         "invalid template syntax",
			pathTemplate: "/{{.Name",
			service:      service,
			wantErr:      true,
		},
		{
			name:         "path too long",
			pathTemplate: "/" + strings.Repeat("a", MaxPathLength+1),
			service:      service,
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := renderPathTemplate(tt.pathTemplate, tt.service)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

// ============================================================================
// RESOLVE REQUEST TIMEOUT TESTS
// ============================================================================

func TestResolveRequestTimeout(t *testing.T) {
	serviceTimeout := &metav1.Duration{Duration: 30 * time.Second}
	runtimeTimeout := &metav1.Duration{Duration: 60 * time.Second}

	tests := []struct {
		name          string
		service       *aimv1alpha1.AIMService
		runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon
		expectNil     bool
		expectSeconds int
	}{
		{
			name:          "no timeout",
			service:       NewService("svc").Build(),
			runtimeConfig: nil,
			expectNil:     true,
		},
		{
			name: "service-level timeout",
			service: func() *aimv1alpha1.AIMService {
				svc := NewService("svc").Build()
				svc.Spec.Routing = &aimv1alpha1.AIMRuntimeRoutingConfig{
					RequestTimeout: serviceTimeout,
				}
				return svc
			}(),
			runtimeConfig: nil,
			expectSeconds: 30,
		},
		{
			name:    "runtime config timeout",
			service: NewService("svc").Build(),
			runtimeConfig: &aimv1alpha1.AIMRuntimeConfigCommon{
				AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
					Routing: &aimv1alpha1.AIMRuntimeRoutingConfig{
						RequestTimeout: runtimeTimeout,
					},
				},
			},
			expectSeconds: 60,
		},
		{
			name: "service overrides runtime config",
			service: func() *aimv1alpha1.AIMService {
				svc := NewService("svc").Build()
				svc.Spec.Routing = &aimv1alpha1.AIMRuntimeRoutingConfig{
					RequestTimeout: serviceTimeout,
				}
				return svc
			}(),
			runtimeConfig: &aimv1alpha1.AIMRuntimeConfigCommon{
				AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
					Routing: &aimv1alpha1.AIMRuntimeRoutingConfig{
						RequestTimeout: runtimeTimeout,
					},
				},
			},
			expectSeconds: 30,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := resolveRequestTimeout(tt.service, tt.runtimeConfig)

			if tt.expectNil {
				if result != nil {
					t.Errorf("expected nil, got %v", result)
				}
				return
			}

			if result == nil {
				t.Error("expected timeout, got nil")
				return
			}

			if int(result.Seconds()) != tt.expectSeconds {
				t.Errorf("expected %d seconds, got %v", tt.expectSeconds, result.Duration)
			}
		})
	}
}

// ============================================================================
// MERGE ROUTE ANNOTATIONS TESTS
// ============================================================================

func TestMergeRouteAnnotations(t *testing.T) {
	tests := []struct {
		name             string
		runtimeConfig    *aimv1alpha1.AIMRuntimeConfigCommon
		expectedContains map[string]string
	}{
		{
			name:             "nil runtime config",
			runtimeConfig:    nil,
			expectedContains: map[string]string{},
		},
		{
			name: "with annotations",
			runtimeConfig: &aimv1alpha1.AIMRuntimeConfigCommon{
				AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
					Routing: &aimv1alpha1.AIMRuntimeRoutingConfig{
						Annotations: map[string]string{
							"nginx.ingress.kubernetes.io/ssl-redirect": "true",
							"custom-annotation":                        "value",
						},
					},
				},
			},
			expectedContains: map[string]string{
				"nginx.ingress.kubernetes.io/ssl-redirect": "true",
				"custom-annotation":                        "value",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mergeRouteAnnotations(tt.runtimeConfig)

			for key, expectedValue := range tt.expectedContains {
				if result[key] != expectedValue {
					t.Errorf("expected annotation %s=%s, got %s", key, expectedValue, result[key])
				}
			}
		})
	}
}

// ============================================================================
// PLAN HTTPROUTE TESTS
// ============================================================================

func TestPlanHTTPRoute(t *testing.T) {
	gatewayRef := &gatewayapiv1.ParentReference{
		Name:      "test-gateway",
		Namespace: ptr.To(gatewayapiv1.Namespace("gateway-ns")),
	}

	tests := []struct {
		name        string
		obs         ServiceObservation
		expectRoute bool
	}{
		{
			name: "routing disabled - no route",
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					service: NewService("svc").Build(),
				},
			},
			expectRoute: false,
		},
		{
			name: "routing enabled but no gateway - no route",
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					service: func() *aimv1alpha1.AIMService {
						svc := NewService("svc").Build()
						svc.Spec.Routing = &aimv1alpha1.AIMRuntimeRoutingConfig{
							Enabled: ptr.To(true),
						}
						return svc
					}(),
				},
			},
			expectRoute: false,
		},
		{
			name: "routing enabled with gateway - creates route",
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					service: func() *aimv1alpha1.AIMService {
						svc := NewService("svc").Build()
						svc.Spec.Routing = &aimv1alpha1.AIMRuntimeRoutingConfig{
							Enabled:    ptr.To(true),
							GatewayRef: gatewayRef,
						}
						return svc
					}(),
				},
			},
			expectRoute: true,
		},
		{
			name: "routing from runtime config with gateway - creates route",
			obs: ServiceObservation{
				ServiceFetchResult: ServiceFetchResult{
					service: NewService("svc").Build(),
					mergedRuntimeConfig: controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]{
						Value: &aimv1alpha1.AIMRuntimeConfigCommon{
							AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
								Routing: &aimv1alpha1.AIMRuntimeRoutingConfig{
									Enabled:    ptr.To(true),
									GatewayRef: gatewayRef,
								},
							},
						},
					},
				},
			},
			expectRoute: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := planHTTPRoute(context.Background(), tt.obs.service, tt.obs)

			if tt.expectRoute {
				if result == nil {
					t.Error("expected HTTPRoute, got nil")
					return
				}

				route, ok := result.(*gatewayapiv1.HTTPRoute)
				if !ok {
					t.Errorf("expected *HTTPRoute, got %T", result)
					return
				}

				// Verify basic structure
				if len(route.Spec.ParentRefs) != 1 {
					t.Errorf("expected 1 parent ref, got %d", len(route.Spec.ParentRefs))
				}
				if len(route.Spec.Rules) != 1 {
					t.Errorf("expected 1 rule, got %d", len(route.Spec.Rules))
				}
			} else {
				if result != nil {
					t.Errorf("expected no route, got %T", result)
				}
			}
		})
	}
}

func TestPlanHTTPRoute_Labels(t *testing.T) {
	gatewayRef := &gatewayapiv1.ParentReference{
		Name: "test-gateway",
	}

	service := NewService("my-svc").Build()
	service.Spec.Routing = &aimv1alpha1.AIMRuntimeRoutingConfig{
		Enabled:    ptr.To(true),
		GatewayRef: gatewayRef,
	}

	obs := ServiceObservation{
		ServiceFetchResult: ServiceFetchResult{
			service: service,
		},
	}

	result := planHTTPRoute(context.Background(), service, obs)
	if result == nil {
		t.Fatal("expected HTTPRoute, got nil")
	}

	route := result.(*gatewayapiv1.HTTPRoute)

	// Check labels
	if route.Labels[constants.LabelK8sManagedBy] != constants.LabelValueManagedBy {
		t.Errorf("expected managed-by label")
	}
	if route.Labels[constants.LabelK8sComponent] != constants.ComponentRouting {
		t.Errorf("expected component=routing label")
	}
}

func TestPlanHTTPRoute_OwnerReference(t *testing.T) {
	gatewayRef := &gatewayapiv1.ParentReference{
		Name: "test-gateway",
	}

	service := NewService("my-svc").Build()
	service.UID = "test-service-uid"
	service.Spec.Routing = &aimv1alpha1.AIMRuntimeRoutingConfig{
		Enabled:    ptr.To(true),
		GatewayRef: gatewayRef,
	}

	obs := ServiceObservation{
		ServiceFetchResult: ServiceFetchResult{
			service: service,
		},
	}

	result := planHTTPRoute(context.Background(), service, obs)
	if result == nil {
		t.Fatal("expected HTTPRoute, got nil")
	}

	route := result.(*gatewayapiv1.HTTPRoute)

	// Check owner reference
	if len(route.OwnerReferences) != 1 {
		t.Fatalf("expected 1 owner reference, got %d", len(route.OwnerReferences))
	}

	ownerRef := route.OwnerReferences[0]
	if ownerRef.Name != service.Name {
		t.Errorf("expected owner name %s, got %s", service.Name, ownerRef.Name)
	}
	if ownerRef.UID != service.UID {
		t.Errorf("expected owner UID %s, got %s", service.UID, ownerRef.UID)
	}
	if ownerRef.Controller == nil || !*ownerRef.Controller {
		t.Error("expected controller=true")
	}
}
