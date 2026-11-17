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
	"reflect"
	"strings"
	"testing"

	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

// ============================================================================
// GENERATE INFERENCE SERVICE NAME TESTS
// ============================================================================

func TestGenerateInferenceServiceName(t *testing.T) {
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
			name:         "long service name is truncated",
			serviceName:  "very-long-service-name-that-might-exceed-kubernetes-limits",
			namespace:    "default",
			wantContains: []string{}, // Just verify it doesn't error
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := GenerateInferenceServiceName(tt.serviceName, tt.namespace)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			// Verify k8s name constraints
			if len(result) > 63 {
				t.Errorf("name too long: %d chars", len(result))
			}

			for _, want := range tt.wantContains {
				if !strings.Contains(result, want) {
					t.Errorf("expected result to contain %q, got %q", want, result)
				}
			}
		})
	}
}

func TestGenerateInferenceServiceName_Deterministic(t *testing.T) {
	result1, _ := GenerateInferenceServiceName("svc", "ns")
	result2, _ := GenerateInferenceServiceName("svc", "ns")

	if result1 != result2 {
		t.Errorf("expected deterministic output, got %q and %q", result1, result2)
	}
}

// Long namespaces leave only a few chars for the visible serviceName prefix, so
// the hash suffix has to carry the uniqueness. Two services in the same namespace
// must still produce distinct names.
func TestGenerateInferenceServiceName_NoCollisionInLongNamespace(t *testing.T) {
	namespace := "qa-another-long-project-name-that-is-max" // 40 chars
	result1, err1 := GenerateInferenceServiceName("wb-aim-c4b421e5", namespace)
	result2, err2 := GenerateInferenceServiceName("wb-aim-7adf99e9", namespace)

	if err1 != nil || err2 != nil {
		t.Fatalf("unexpected errors: %v, %v", err1, err2)
	}
	if result1 == result2 {
		t.Errorf("expected distinct InferenceService names for different AIMServices "+
			"in the same namespace, both got %q", result1)
	}
}

// Same serviceName in different namespaces must still yield different names.
func TestGenerateInferenceServiceName_DistinctNamespaces(t *testing.T) {
	result1, err1 := GenerateInferenceServiceName("svc", "ns-one")
	result2, err2 := GenerateInferenceServiceName("svc", "ns-two")

	if err1 != nil || err2 != nil {
		t.Fatalf("unexpected errors: %v, %v", err1, err2)
	}
	if result1 == result2 {
		t.Errorf("expected distinct InferenceService names across namespaces, both got %q", result1)
	}
}

// ============================================================================
// IS READY FOR INFERENCE SERVICE TESTS
// ============================================================================

// ============================================================================
// PRESERVE EXISTING STORAGE VOLUMES TESTS
// ============================================================================

// ============================================================================
// PLAN INFERENCE SERVICE TESTS - MUTABLE FIELD PROPAGATION
// ============================================================================

// ============================================================================
// PRIORITY CLASS NAME TESTS
// ============================================================================

// ============================================================================
// RESOLVE DEPLOYMENT IMAGE TESTS
// ============================================================================

// ============================================================================
// RESOLVE RESOURCES TESTS
// ============================================================================

// ============================================================================
// DEFAULT RESOURCE REQUIREMENTS TESTS
// ============================================================================

// ============================================================================
// MERGE RESOURCE REQUIREMENTS TESTS
// ============================================================================

// ============================================================================
// BUILD MERGED ENV VARS TESTS
// ============================================================================

// TestConfigureReplicasAndAutoscaling_AutoscalerClass verifies the ISVC
// carries autoscalerClass=external when autoscaling is requested and
// autoscalerClass=none on the legacy fixed-replica path.
func TestConfigureReplicasAndAutoscaling_AutoscalerClass(t *testing.T) {
	tests := []struct {
		name        string
		minReplicas *int32
		maxReplicas *int32
		autoScaling *aimv1alpha1.AIMServiceAutoScaling
		replicas    *int32
		expectClass string
	}{
		{
			name:        "scale-to-zero service",
			minReplicas: ptr.To(int32(0)),
			maxReplicas: ptr.To(int32(3)),
			expectClass: constants.AutoscalerClassExternal,
		},
		{
			name:        "warm autoscaling service",
			minReplicas: ptr.To(int32(1)),
			maxReplicas: ptr.To(int32(4)),
			autoScaling: &aimv1alpha1.AIMServiceAutoScaling{
				Metrics: []aimv1alpha1.AIMServiceMetricsSpec{{Type: "PodMetric"}},
			},
			expectClass: constants.AutoscalerClassExternal,
		},
		{
			name:        "legacy fixed-replica path stays on autoscalerClass=none",
			replicas:    ptr.To(int32(2)),
			expectClass: constants.AutoscalerClassNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := NewService("svc").Build()
			service.Spec.MinReplicas = tt.minReplicas
			service.Spec.MaxReplicas = tt.maxReplicas
			service.Spec.AutoScaling = tt.autoScaling
			service.Spec.Replicas = tt.replicas

			isvc := &servingv1beta1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{Name: "isvc", Namespace: testNamespace},
			}
			configureReplicasAndAutoscaling(isvc, v1alpha2View(service))

			if got := isvc.Annotations[constants.AnnotationKServeAutoscalerClass]; got != tt.expectClass {
				t.Errorf("autoscalerClass: expected %q, got %q", tt.expectClass, got)
			}
		})
	}
}

// TestConfigureReplicasAndAutoscaling_ForwardsUserMetricsForOTelCollector
// verifies user metrics are written into isvc.Spec.Predictor.AutoScaling.Metrics
// so KServe's RawKubeReconciler creates the OpenTelemetryCollector CR that
// feeds in-pod sidecar injection. The gateway-rate metric must NOT appear
// here -- that trigger belongs solely on the controller-owned ScaledObject.
func TestConfigureReplicasAndAutoscaling_ForwardsUserMetricsForOTelCollector(t *testing.T) {
	service := NewService("svc").Build()
	service.Spec.MinReplicas = ptr.To(int32(0))
	service.Spec.MaxReplicas = ptr.To(int32(3))
	service.Spec.AutoScaling = &aimv1alpha1.AIMServiceAutoScaling{
		Metrics: []aimv1alpha1.AIMServiceMetricsSpec{
			{
				Type: "PodMetric",
				PodMetric: &aimv1alpha1.AIMServicePodMetricSource{
					Metric: &aimv1alpha1.AIMServicePodMetric{
						Backend:           "opentelemetry",
						MetricNames:       []string{"vllm:num_requests_running"},
						Query:             "vllm:num_requests_running",
						OperationOverTime: "avg",
					},
					Target: &aimv1alpha1.AIMServiceMetricTarget{
						Type:  "Value",
						Value: "1",
					},
				},
			},
		},
	}

	isvc := &servingv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{Name: "isvc", Namespace: testNamespace},
	}
	configureReplicasAndAutoscaling(isvc, v1alpha2View(service))

	if isvc.Spec.Predictor.AutoScaling == nil {
		t.Fatal("isvc.Spec.Predictor.AutoScaling must be populated; got nil")
	}
	if len(isvc.Spec.Predictor.AutoScaling.Metrics) != 1 {
		t.Fatalf("expected 1 forwarded user metric, got %d", len(isvc.Spec.Predictor.AutoScaling.Metrics))
	}

	got := isvc.Spec.Predictor.AutoScaling.Metrics[0]
	if got.PodMetric == nil {
		t.Fatalf("forwarded metric must be a PodMetric; got %+v", got)
	}
	if string(got.PodMetric.Metric.Backend) != "opentelemetry" {
		t.Errorf("forwarded metric backend must be 'opentelemetry'; got %q", got.PodMetric.Metric.Backend)
	}
	if !reflect.DeepEqual(got.PodMetric.Metric.MetricNames, []string{"vllm:num_requests_running"}) {
		t.Errorf("forwarded metric names mismatch; got %v", got.PodMetric.Metric.MetricNames)
	}

	for _, m := range isvc.Spec.Predictor.AutoScaling.Metrics {
		if m.PodMetric != nil && strings.Contains(m.PodMetric.Metric.Query, "aim_activation_requests") {
			t.Fatalf("controller must not inject the gateway activation metric into the ISVC AutoScaling spec; got: %+v", m)
		}
	}
}

// TestConfigureReplicasAndAutoscaling_SidecarAnnotations verifies the OTel
// sidecar and Prometheus port annotations are applied to autoscaled services.
func TestConfigureReplicasAndAutoscaling_SidecarAnnotations(t *testing.T) {
	service := NewService("qwen").Build()
	service.Spec.MinReplicas = ptr.To(int32(0))
	service.Spec.MaxReplicas = ptr.To(int32(3))

	isvc := &servingv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen", Namespace: testNamespace},
	}
	configureReplicasAndAutoscaling(isvc, v1alpha2View(service))

	want := "qwen" + constants.PredictorServiceSuffix
	if got := isvc.Annotations[constants.AnnotationOTelSidecarInject]; got != want {
		t.Errorf("expected sidecar inject annotation %q; got %q", want, got)
	}
	if got := isvc.Annotations[constants.AnnotationPrometheusPort]; got != constants.DefaultPrometheusPort {
		t.Errorf("expected Prometheus port annotation %q; got %q", constants.DefaultPrometheusPort, got)
	}
}
