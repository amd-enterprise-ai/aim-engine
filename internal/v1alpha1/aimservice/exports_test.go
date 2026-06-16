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
	"testing"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

// hpaWithReplicas returns a minimal HPA whose Spec/Status replica fields
// are populated for the supplied values. minReplicas==nil leaves the spec
// pointer nil (the legacy case before KEDA has reconciled).
func hpaWithReplicas(minReplicas *int32, maxReplicas, current, desired int32) *autoscalingv2.HorizontalPodAutoscaler {
	return &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Name: "hpa"},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			MinReplicas: minReplicas,
			MaxReplicas: maxReplicas,
		},
		Status: autoscalingv2.HorizontalPodAutoscalerStatus{
			CurrentReplicas: current,
			DesiredReplicas: desired,
		},
	}
}

func TestComputeRuntimeStatus_ScaleToZeroOverridesHPAMinFloor(t *testing.T) {
	// KEDA's documented contract: ScaledObject.minReplicaCount: 0 -> HPA
	// pinned to Spec.MinReplicas: 1 (Kubernetes HPA v2 still validates
	// MinReplicas>=1 unless the alpha HPAScaleToZero feature gate is on).
	// ComputeRuntimeStatus must surface the user's spec intent (0), not
	// the HPA's enforced floor (1).
	svc := NewService("svc").WithModelImage("test-image:v1").Build()
	svc.Spec.MinReplicas = ptr.To(int32(0))
	svc.Spec.MaxReplicas = ptr.To(int32(3))

	hpa := controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler]{
		Value: hpaWithReplicas(ptr.To(int32(1)), 3, 1, 1), // KEDA pins to 1
	}

	got := ComputeRuntimeStatus(svc, hpa)
	if got.MinReplicas != 0 {
		t.Errorf("MinReplicas=%d, want 0 (spec override of HPA's KEDA-pinned 1)", got.MinReplicas)
	}
	if got.MaxReplicas != 3 {
		t.Errorf("MaxReplicas=%d, want 3", got.MaxReplicas)
	}
	if got.CurrentReplicas != 1 {
		t.Errorf("CurrentReplicas=%d, want 1 (from HPA status)", got.CurrentReplicas)
	}
	if got.DesiredReplicas != 1 {
		t.Errorf("DesiredReplicas=%d, want 1 (from HPA status)", got.DesiredReplicas)
	}
}

func TestComputeRuntimeStatus_ScaleToZeroIdleReportsZero(t *testing.T) {
	// Once KEDA has idled the deployment, HPA reports
	// CurrentReplicas=0, DesiredReplicas=0. The legacy "DesiredReplicas==0
	// falls back to MinReplicas" path used to silently flip the
	// status row to "1/1 (1-3)" -- under scale-to-zero we want a true
	// "0/0 (0-3)" reading.
	svc := NewService("svc").WithModelImage("test-image:v1").Build()
	svc.Spec.MinReplicas = ptr.To(int32(0))
	svc.Spec.MaxReplicas = ptr.To(int32(3))

	hpa := controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler]{
		Value: hpaWithReplicas(ptr.To(int32(1)), 3, 0, 0),
	}

	got := ComputeRuntimeStatus(svc, hpa)
	if got.MinReplicas != 0 {
		t.Errorf("MinReplicas=%d, want 0", got.MinReplicas)
	}
	if got.CurrentReplicas != 0 {
		t.Errorf("CurrentReplicas=%d, want 0", got.CurrentReplicas)
	}
	if got.DesiredReplicas != 0 {
		t.Errorf("DesiredReplicas=%d, want 0 (idle, not silently bumped to 1)", got.DesiredReplicas)
	}
	if got.Replicas != "0/0 (0-3)" {
		t.Errorf("Replicas=%q, want %q", got.Replicas, "0/0 (0-3)")
	}
}

func TestComputeRuntimeStatus_NonScaleToZeroPreservesLegacyDesiredFallback(t *testing.T) {
	// For min>=1 the historic behaviour is preserved: if the HPA hasn't
	// yet emitted Status.DesiredReplicas (zero-value int32) we report
	// MinReplicas as the desired floor. This is what HPAs do during their
	// first scrape interval on non-scale-to-zero services.
	svc := NewService("svc").WithModelImage("test-image:v1").Build()
	svc.Spec.MinReplicas = ptr.To(int32(2))
	svc.Spec.MaxReplicas = ptr.To(int32(5))

	hpa := controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler]{
		Value: hpaWithReplicas(ptr.To(int32(2)), 5, 2, 0),
	}

	got := ComputeRuntimeStatus(svc, hpa)
	if got.MinReplicas != 2 {
		t.Errorf("MinReplicas=%d, want 2", got.MinReplicas)
	}
	if got.DesiredReplicas != 2 {
		t.Errorf("DesiredReplicas=%d, want 2 (fallback to MinReplicas when HPA.Status.DesiredReplicas==0)", got.DesiredReplicas)
	}
}

func TestComputeRuntimeStatus_NoHPAFallsBackToSpec(t *testing.T) {
	// Before KEDA reconciles -- the HPA fetch returns not-found. The
	// fallback path reads from spec. Min=0 must round-trip without
	// being floored to 1.
	svc := NewService("svc").WithModelImage("test-image:v1").Build()
	svc.Spec.MinReplicas = ptr.To(int32(0))
	svc.Spec.MaxReplicas = ptr.To(int32(3))
	svc.Spec.AutoScaling = &aimv1alpha1.AIMServiceAutoScaling{}

	got := ComputeRuntimeStatus(svc, controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler]{})
	if got.MinReplicas != 0 {
		t.Errorf("MinReplicas=%d, want 0 (from spec fallback)", got.MinReplicas)
	}
	if got.MaxReplicas != 3 {
		t.Errorf("MaxReplicas=%d, want 3", got.MaxReplicas)
	}
	if got.CurrentReplicas != 0 {
		t.Errorf("CurrentReplicas=%d, want 0 (autoscaling set, HPA absent -> not yet observed)", got.CurrentReplicas)
	}
}

func TestScaleToZeroRoutingComponentHealth(t *testing.T) {
	routingConfig := func(enabled bool) *aimv1alpha1.AIMRuntimeConfigCommon {
		return &aimv1alpha1.AIMRuntimeConfigCommon{
			AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
				Routing: &aimv1alpha1.AIMRuntimeRoutingConfig{Enabled: ptr.To(enabled)},
			},
		}
	}

	tests := []struct {
		name          string
		minReplicas   *int32
		serviceRoute  *bool
		runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon
		wantFailed    bool
	}{
		{
			name:        "scale-to-zero with routing disabled is invalid",
			minReplicas: ptr.To(int32(0)),
			wantFailed:  true,
		},
		{
			name:         "scale-to-zero with service routing enabled is valid",
			minReplicas:  ptr.To(int32(0)),
			serviceRoute: ptr.To(true),
		},
		{
			name:          "scale-to-zero with runtime-config routing enabled is valid",
			minReplicas:   ptr.To(int32(0)),
			runtimeConfig: routingConfig(true),
		},
		{
			name:          "scale-to-zero with runtime-config routing explicitly disabled is invalid",
			minReplicas:   ptr.To(int32(0)),
			runtimeConfig: routingConfig(false),
			wantFailed:    true,
		},
		{
			name:        "service routing override beats enabled runtime config",
			minReplicas: ptr.To(int32(0)),
			// Service disables routing even though the runtime config enables it.
			serviceRoute:  ptr.To(false),
			runtimeConfig: routingConfig(true),
			wantFailed:    true,
		},
		{
			name:        "minReplicas>=1 is not scale-to-zero, routing not required",
			minReplicas: ptr.To(int32(1)),
		},
		{
			name:        "minReplicas unset is not scale-to-zero, routing not required",
			minReplicas: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService("svc").WithModelImage("test-image:v1").Build()
			svc.Spec.MinReplicas = tt.minReplicas
			if tt.serviceRoute != nil {
				svc.Spec.Routing = &aimv1alpha1.AIMRuntimeRoutingConfig{Enabled: tt.serviceRoute}
			}

			got := ScaleToZeroRoutingComponentHealth(svc, tt.runtimeConfig)

			if !tt.wantFailed {
				if got.Component != "" {
					t.Fatalf("expected no component health, got %+v", got)
				}
				return
			}

			if got.Component != ComponentScaleToZeroConfig {
				t.Errorf("Component=%q, want %q", got.Component, ComponentScaleToZeroConfig)
			}
			if got.State != constants.AIMStatusFailed {
				t.Errorf("State=%q, want %q", got.State, constants.AIMStatusFailed)
			}
			if got.Reason != aimv1alpha1.AIMServiceReasonRoutingRequired {
				t.Errorf("Reason=%q, want %q", got.Reason, aimv1alpha1.AIMServiceReasonRoutingRequired)
			}
			if len(got.Errors) != 1 {
				t.Fatalf("expected exactly one error, got %d", len(got.Errors))
			}
			// The InvalidSpec category is what drives ConfigValid=False and
			// blocks apply in the state engine.
			if cat := controllerutils.CategorizeError(got.Errors[0]).Category(); cat != controllerutils.ErrorCategoryInvalidSpec {
				t.Errorf("error category=%v, want InvalidSpec", cat)
			}
		})
	}
}

func TestAutoscalingTriggerComponentHealth(t *testing.T) {
	withMetric := &aimv1alpha1.AIMServiceAutoScaling{
		Metrics: []aimv1alpha1.AIMServiceMetricsSpec{{}},
	}

	tests := []struct {
		name        string
		minReplicas *int32
		maxReplicas *int32
		replicas    *int32
		autoScaling *aimv1alpha1.AIMServiceAutoScaling
		wantFailed  bool
	}{
		{
			name: "no autoscaling configured is valid",
		},
		{
			name:     "fixed replicas (not autoscaling) is valid",
			replicas: ptr.To(int32(2)),
		},
		{
			name:        "maxReplicas only with no metrics is invalid",
			maxReplicas: ptr.To(int32(5)),
			wantFailed:  true,
		},
		{
			name:        "min/max range with no metrics is invalid",
			minReplicas: ptr.To(int32(1)),
			maxReplicas: ptr.To(int32(5)),
			wantFailed:  true,
		},
		{
			name:        "scale-from-zero supplies the activation trigger, valid without metrics",
			minReplicas: ptr.To(int32(0)),
			maxReplicas: ptr.To(int32(5)),
		},
		{
			name:        "user metrics supply a trigger, valid",
			minReplicas: ptr.To(int32(1)),
			maxReplicas: ptr.To(int32(5)),
			autoScaling: withMetric,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService("svc").WithModelImage("test-image:v1").Build()
			svc.Spec.MinReplicas = tt.minReplicas
			svc.Spec.MaxReplicas = tt.maxReplicas
			svc.Spec.Replicas = tt.replicas
			svc.Spec.AutoScaling = tt.autoScaling

			got := AutoscalingTriggerComponentHealth(svc)

			if !tt.wantFailed {
				if got.Component != "" {
					t.Fatalf("expected no component health, got %+v", got)
				}
				return
			}

			if got.Component != ComponentAutoscalingConfig {
				t.Errorf("Component=%q, want %q", got.Component, ComponentAutoscalingConfig)
			}
			if got.State != constants.AIMStatusFailed {
				t.Errorf("State=%q, want %q", got.State, constants.AIMStatusFailed)
			}
			if got.Reason != aimv1alpha1.AIMServiceReasonAutoscalingRequiresMetrics {
				t.Errorf("Reason=%q, want %q", got.Reason, aimv1alpha1.AIMServiceReasonAutoscalingRequiresMetrics)
			}
			if len(got.Errors) != 1 {
				t.Fatalf("expected exactly one error, got %d", len(got.Errors))
			}
			if cat := controllerutils.CategorizeError(got.Errors[0]).Category(); cat != controllerutils.ErrorCategoryInvalidSpec {
				t.Errorf("error category=%v, want InvalidSpec", cat)
			}
		})
	}
}

func TestComputeRuntimeStatus_HPAValueOnlyUsedWhenSpecAbsent(t *testing.T) {
	// If the service didn't set MinReplicas (e.g. older AIMService that
	// only set Replicas, or relies entirely on KEDA defaults), the HPA's
	// value remains the source of truth -- no behavioural regression for
	// non-scale-to-zero callers that have never written MinReplicas.
	svc := NewService("svc").WithModelImage("test-image:v1").Build()
	svc.Spec.MinReplicas = nil
	svc.Spec.MaxReplicas = ptr.To(int32(5))

	hpa := controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler]{
		Value: hpaWithReplicas(ptr.To(int32(2)), 5, 3, 3),
	}

	got := ComputeRuntimeStatus(svc, hpa)
	if got.MinReplicas != 2 {
		t.Errorf("MinReplicas=%d, want 2 (from HPA, spec is nil)", got.MinReplicas)
	}
}
