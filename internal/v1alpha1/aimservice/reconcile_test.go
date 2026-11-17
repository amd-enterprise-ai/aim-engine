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
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	duckv1 "knative.dev/pkg/apis/duck/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

// ============================================================================
// COMPOSE STATE TESTS
// ============================================================================

// ============================================================================
// GET COMPONENT HEALTH TESTS
// ============================================================================

// ============================================================================
// PLAN RESOURCES TESTS
// ============================================================================

// ============================================================================
// DECORATE STATUS TESTS
// ============================================================================

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

func TestSetActivationMetricCondition(t *testing.T) {
	t.Setenv(constants.EnvAIMGatewayActivationScope, constants.GatewayActivationScopeHTTPRoute)
	condType := aimv1alpha1.AIMServiceConditionActivationMetricAvailable

	// The condition must never participate in the Ready rollup, which the
	// framework keys off the component "Ready" suffix.
	if strings.HasSuffix(condType, controllerutils.ComponentConditionSuffix) {
		t.Fatalf("condition type %q must not end in %q or it would gate readiness",
			condType, controllerutils.ComponentConditionSuffix)
	}

	newSvc := func(minReplicas *int32) *aimv1alpha2.AIMService {
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

func TestGetInferenceServiceHealth_PredictorPodCreationFailed(t *testing.T) {
	const refusal = `Pod "svc-predictor-6d8f-abcde" is invalid: ` +
		`spec.containers[0].resources.requests: Invalid value: "4": ` +
		`must be less than or equal to cpu limit of 300m`

	tests := []struct {
		name          string
		reason        string
		generationLag bool
		pods          *corev1.PodList
		podsErr       error
		wantState     constants.AIMStatus
		wantReason    string
		wantInMessage string
	}{
		{
			name:          "refused predictor pod is reported with the API server message",
			reason:        "FailedCreate",
			pods:          &corev1.PodList{},
			wantState:     constants.AIMStatusFailed,
			wantReason:    aimv1alpha1.AIMServiceReasonPodCreationFailed,
			wantInMessage: refusal,
		},
		{
			// KServe copies the observedGeneration of the Deployment, which
			// also changes when the replica count changes.
			name:          "refusal is reported while the observed generation differs",
			reason:        "FailedCreate",
			generationLag: true,
			pods:          &corev1.PodList{},
			wantState:     constants.AIMStatusFailed,
			wantReason:    aimv1alpha1.AIMServiceReasonPodCreationFailed,
			wantInMessage: refusal,
		},
		{
			// A rollout or a scale-up is refused while an older pod serves.
			name:          "a ready predictor pod keeps the service running and the refusal is shown",
			reason:        "FailedCreate",
			pods:          podsWithReady(1, 1),
			wantState:     constants.AIMStatusReady,
			wantReason:    aimv1alpha1.AIMServiceReasonRuntimeScaling,
			wantInMessage: refusal,
		},
		{
			// After a spec fix, the Deployment keeps the refused old
			// ReplicaSet until the pod of the new ReplicaSet is ready.
			name:          "a starting pod keeps the service progressing and the refusal is shown",
			reason:        "FailedCreate",
			pods:          podsInPhase(corev1.PodPending, false),
			wantState:     constants.AIMStatusProgressing,
			wantReason:    aimv1alpha1.AIMServiceReasonCreatingRuntime,
			wantInMessage: refusal,
		},
		{
			name:          "a terminating pod does not count as starting",
			reason:        "FailedCreate",
			pods:          podsInPhase(corev1.PodRunning, true),
			wantState:     constants.AIMStatusFailed,
			wantReason:    aimv1alpha1.AIMServiceReasonPodCreationFailed,
			wantInMessage: refusal,
		},
		{
			name:          "a failed pod does not count as starting",
			reason:        "FailedCreate",
			pods:          podsInPhase(corev1.PodFailed, false),
			wantState:     constants.AIMStatusFailed,
			wantReason:    aimv1alpha1.AIMServiceReasonPodCreationFailed,
			wantInMessage: refusal,
		},
		// An active pod next to the error makes the test fail if the error is ignored.
		{
			name:          "a failed pod-list fetch still reports PodCreationFailed",
			reason:        "FailedCreate",
			pods:          podsInPhase(corev1.PodPending, false),
			podsErr:       errors.New("pod list failed"),
			wantState:     constants.AIMStatusFailed,
			wantReason:    aimv1alpha1.AIMServiceReasonPodCreationFailed,
			wantInMessage: refusal,
		},
		{
			name:       "a predictor that is only starting is not a failure",
			reason:     "MinimumReplicasUnavailable",
			pods:       &corev1.PodList{},
			wantState:  constants.AIMStatusProgressing,
			wantReason: aimv1alpha1.AIMServiceReasonCreatingRuntime,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isvc := predictorNotReadyISVC("svc", tt.reason, refusal)
			if tt.generationLag {
				isvc.Status.ObservedGeneration = 2
			}
			health := inferenceServiceComponentHealth(
				controllerutils.FetchResult[*servingv1beta1.InferenceService]{Value: isvc},
				&controllerutils.FetchResult[*corev1.PodList]{Value: tt.pods, Error: tt.podsErr},
			)

			if health.State != tt.wantState || health.Reason != tt.wantReason {
				t.Fatalf("health = %s/%s, want %s/%s: %#v",
					health.State, health.Reason, tt.wantState, tt.wantReason, health)
			}
			if !strings.Contains(health.Message, tt.wantInMessage) {
				t.Errorf("message = %q, want it to contain %q", health.Message, tt.wantInMessage)
			}
			if len(health.Errors) != 0 {
				t.Errorf("errors = %v, want none: an error category would block applying a fixed spec", health.Errors)
			}
		})
	}
}

func TestGetInferenceServiceHealth_ScaleToZeroWithFailedCreate(t *testing.T) {
	const refusal = `pods "svc-predictor-6d8f-abcde" is forbidden: exceeded quota`
	service := &aimv1alpha2.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: testNamespace},
		Spec: aimv1alpha1.AIMServiceSpec{
			MinReplicas: ptr.To(int32(0)),
			MaxReplicas: ptr.To(int32(3)),
		},
	}
	isvc := controllerutils.FetchResult[*servingv1beta1.InferenceService]{Value: predictorNotReadyISVC("svc", "FailedCreate", refusal)}
	pods := &controllerutils.FetchResult[*corev1.PodList]{Value: &corev1.PodList{}}
	hpa := controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler]{Value: hpaWithScalingActive(corev1.ConditionFalse, hpaReasonScalingDisabled)}

	isvcHealth := inferenceServiceComponentHealth(isvc, pods)
	if isvcHealth.State != constants.AIMStatusFailed ||
		isvcHealth.Reason != aimv1alpha1.AIMServiceReasonPodCreationFailed {
		t.Fatalf("InferenceService = %s/%s, want Failed/PodCreationFailed", isvcHealth.State, isvcHealth.Reason)
	}
	if !strings.Contains(isvcHealth.Message, refusal) {
		t.Errorf("InferenceService message = %q, want it to contain %q", isvcHealth.Message, refusal)
	}

	podsHealth, ok := inferenceServicePodsHealth(context.Background(), nil, service, hpa, pods)
	if !ok || podsHealth.State != constants.AIMStatusReady ||
		podsHealth.Reason != aimv1alpha1.AIMServiceReasonScaledToZero {
		t.Errorf("InferenceServicePods = (%v, %s/%s), want (true, Ready/ScaledToZero)", ok, podsHealth.State, podsHealth.Reason)
	}

	hpaHealth := hpaComponentHealth(service, hpa, 0, false)
	if hpaHealth.State != constants.AIMStatusReady ||
		hpaHealth.Reason != aimv1alpha1.AIMServiceReasonScaledToZero {
		t.Errorf("HPA = %s/%s, want Ready/ScaledToZero", hpaHealth.State, hpaHealth.Reason)
	}
}

// podsWithReady returns a Pod list of length `total`, of which `ready`
// pods carry PodReady=True.
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

// podsInPhase returns one predictor pod that is not ready.
func podsInPhase(phase corev1.PodPhase, terminating bool) *corev1.PodList {
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-predictor-new", Namespace: testNamespace},
		Status:     corev1.PodStatus{Phase: phase},
	}
	if terminating {
		pod.DeletionTimestamp = ptr.To(metav1.Now())
	}
	return &corev1.PodList{Items: []corev1.Pod{pod}}
}

// predictorNotReadyISVC returns an InferenceService as KServe reports it in
// RawDeployment mode when the predictor Deployment is not available: KServe
// copies the reason and message of the Deployment condition into
// PredictorReady and Ready.
func predictorNotReadyISVC(name, reason, message string) *servingv1beta1.InferenceService {
	isvc := &servingv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Namespace:  testNamespace,
			Generation: 1,
		},
	}
	isvc.Status.ObservedGeneration = isvc.Generation
	isvc.Status.Conditions = duckv1.Conditions{
		{Type: servingv1beta1.PredictorReady, Status: corev1.ConditionFalse, Reason: reason, Message: message},
		{Type: "Ready", Status: corev1.ConditionFalse, Reason: reason, Message: message},
	}
	return isvc
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
