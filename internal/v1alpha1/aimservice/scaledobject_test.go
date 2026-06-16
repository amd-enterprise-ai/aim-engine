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
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/utils/ptr"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

// TestPlanScaledObject_GatedByAutoscalingFields verifies the controller
// only authors a ScaledObject when the user asks for autoscaling; the
// legacy fixed-replica path returns nil.
func TestPlanScaledObject_GatedByAutoscalingFields(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*aimv1alpha1.AIMService)
		expectObj bool
	}{
		{
			name:      "no autoscaling fields: returns nil",
			mutate:    func(s *aimv1alpha1.AIMService) {},
			expectObj: false,
		},
		{
			name: "spec.replicas only (legacy fixed): returns nil",
			mutate: func(s *aimv1alpha1.AIMService) {
				s.Spec.Replicas = ptr.To(int32(2))
			},
			expectObj: false,
		},
		{
			name: "minReplicas set: returns ScaledObject",
			mutate: func(s *aimv1alpha1.AIMService) {
				s.Spec.MinReplicas = ptr.To(int32(1))
				s.Spec.AutoScaling = &aimv1alpha1.AIMServiceAutoScaling{
					Metrics: []aimv1alpha1.AIMServiceMetricsSpec{validVLLMMetric()},
				}
			},
			expectObj: true,
		},
		{
			name: "minReplicas=0 + autoScaling: returns ScaledObject",
			mutate: func(s *aimv1alpha1.AIMService) {
				s.Spec.MinReplicas = ptr.To(int32(0))
				s.Spec.MaxReplicas = ptr.To(int32(3))
				s.Spec.AutoScaling = &aimv1alpha1.AIMServiceAutoScaling{
					Metrics: []aimv1alpha1.AIMServiceMetricsSpec{validVLLMMetric()},
				}
			},
			expectObj: true,
		},
		{
			// minReplicas>=1 has no gateway trigger, so an empty
			// trigger list -> skipped (KEDA rejects empty triggers).
			name: "autoscaling requested but no user triggers and minReplicas>=1: returns nil",
			mutate: func(s *aimv1alpha1.AIMService) {
				s.Spec.MinReplicas = ptr.To(int32(1))
			},
			expectObj: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := NewService("my-svc").Build()
			tt.mutate(service)

			obj := planScaledObject(context.Background(), service, nil)
			if tt.expectObj && obj == nil {
				t.Fatal("expected ScaledObject, got nil")
			}
			if !tt.expectObj && obj != nil {
				t.Fatalf("expected no ScaledObject, got %T", obj)
			}
		})
	}
}

// TestPlanScaledObject_ScaleToZeroTriggers verifies that minReplicas=0
// prepends the gateway-rate activation trigger before the user's vLLM
// triggers, that both triggers scope the metricQuery to the predictor
// Deployment, and that the gateway trigger keeps its high targetValue so
// it never recommends >1 replica.
func TestPlanScaledObject_ScaleToZeroTriggers(t *testing.T) {
	service := NewService("qwen-chat").Build()
	service.Spec.MinReplicas = ptr.To(int32(0))
	service.Spec.MaxReplicas = ptr.To(int32(4))
	service.Spec.AutoScaling = &aimv1alpha1.AIMServiceAutoScaling{
		Metrics: []aimv1alpha1.AIMServiceMetricsSpec{validVLLMMetric()},
	}

	obj := planScaledObject(context.Background(), service, nil)
	if obj == nil {
		t.Fatal("expected ScaledObject")
	}
	so, ok := obj.(*unstructured.Unstructured)
	if !ok {
		t.Fatalf("expected *Unstructured, got %T", obj)
	}

	gvk := so.GroupVersionKind()
	if gvk.Group != "keda.sh" || gvk.Version != "v1alpha1" || gvk.Kind != "ScaledObject" {
		t.Errorf("unexpected GVK: %+v", gvk)
	}

	isvcName, err := GenerateInferenceServiceName(service.Name, service.Namespace)
	if err != nil {
		t.Fatalf("GenerateInferenceServiceName: %v", err)
	}
	wantName := isvcName + constants.PredictorServiceSuffix
	if so.GetName() != wantName {
		t.Errorf("expected name %q, got %q", wantName, so.GetName())
	}
	if so.GetNamespace() != testNamespace {
		t.Errorf("expected namespace %q, got %q", testNamespace, so.GetNamespace())
	}

	spec := mustNestedMap(t, so.Object, "spec")

	scaleTargetRef, _, _ := unstructured.NestedMap(spec, "scaleTargetRef")
	if got, _ := scaleTargetRef["name"].(string); got != wantName {
		t.Errorf("scaleTargetRef.name must match the predictor Deployment %q, got %q", wantName, got)
	}

	if min, _, _ := unstructured.NestedInt64(spec, "minReplicaCount"); min != 0 {
		t.Errorf("expected minReplicaCount=0, got %d", min)
	}
	if max, _, _ := unstructured.NestedInt64(spec, "maxReplicaCount"); max != 4 {
		t.Errorf("expected maxReplicaCount=4, got %d", max)
	}

	if poll, _, _ := unstructured.NestedInt64(spec, "pollingInterval"); poll != int64(defaultPollingIntervalScaleToZero) {
		t.Errorf("expected pollingInterval=%d, got %d", defaultPollingIntervalScaleToZero, poll)
	}
	// No memory request -> flat default cooldown. The memory-derived path
	// is exercised in TestComputePredictorCooldown.
	if cd, _, _ := unstructured.NestedInt64(spec, "cooldownPeriod"); cd != int64(defaultCooldownPeriodScaleToZero) {
		t.Errorf("expected cooldownPeriod=%d, got %d", defaultCooldownPeriodScaleToZero, cd)
	}

	triggers, _, _ := unstructured.NestedSlice(spec, "triggers")
	if len(triggers) != 2 {
		t.Fatalf("expected 2 triggers (gateway + vllm), got %d", len(triggers))
	}

	// Trigger order matters: gateway first so operators reading
	// status.triggersActivity see s0=gateway, s1=vllm.
	gateway, ok := triggers[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected triggers[0] to be a map, got %T", triggers[0])
	}
	if gateway["type"] != "external" {
		t.Errorf("gateway trigger type should be external, got %v", gateway["type"])
	}
	gMeta, _ := gateway["metadata"].(map[string]interface{})
	wantGatewayQuery := `sum(` + gatewayActivationMetricName + `{namespace="` + service.Namespace + `",deployment="` + wantName + `"})`
	if got := gMeta["metricQuery"]; got != wantGatewayQuery {
		t.Errorf("gateway metricQuery: want %q, got %v", wantGatewayQuery, got)
	}
	if got := gMeta["targetValue"]; got != constants.DefaultGatewayActivationTargetValue {
		t.Errorf("gateway targetValue: want %q, got %v", constants.DefaultGatewayActivationTargetValue, got)
	}
	if got := gMeta["operationOverTime"]; got != constants.DefaultGatewayActivationOperationOverTime {
		t.Errorf("gateway operationOverTime: want %q, got %v", constants.DefaultGatewayActivationOperationOverTime, got)
	}
	if got := gMeta["scalerAddress"]; got != constants.DefaultKEDAOTelScalerAddress {
		t.Errorf("gateway scalerAddress: want %q, got %v", constants.DefaultKEDAOTelScalerAddress, got)
	}

	user, ok := triggers[1].(map[string]interface{})
	if !ok {
		t.Fatalf("expected triggers[1] to be a map, got %T", triggers[1])
	}
	uMeta, _ := user["metadata"].(map[string]interface{})
	wantUserQuery := `sum(vllm:num_requests_running{namespace="` + service.Namespace + `",deployment="` + wantName + `"})`
	if got := uMeta["metricQuery"]; got != wantUserQuery {
		t.Errorf("user metricQuery: want %q, got %v", wantUserQuery, got)
	}
	if got := uMeta["targetValue"]; got != "1" {
		t.Errorf("user targetValue should mirror the spec, got %v", got)
	}
}

// TestPlanScaledObject_WarmModeDefaults verifies that with minReplicas>=1
// we don't prepend a gateway trigger and don't override KEDA's
// pollingInterval/cooldownPeriod defaults.
func TestPlanScaledObject_WarmModeDefaults(t *testing.T) {
	service := NewService("qwen-chat").Build()
	service.Spec.MinReplicas = ptr.To(int32(1))
	service.Spec.MaxReplicas = ptr.To(int32(3))
	service.Spec.AutoScaling = &aimv1alpha1.AIMServiceAutoScaling{
		Metrics: []aimv1alpha1.AIMServiceMetricsSpec{validVLLMMetric()},
	}

	obj := planScaledObject(context.Background(), service, nil)
	if obj == nil {
		t.Fatal("expected ScaledObject")
	}
	so := obj.(*unstructured.Unstructured)
	spec := mustNestedMap(t, so.Object, "spec")

	if min, _, _ := unstructured.NestedInt64(spec, "minReplicaCount"); min != 1 {
		t.Errorf("expected minReplicaCount=1, got %d", min)
	}
	if _, ok, _ := unstructured.NestedInt64(spec, "pollingInterval"); ok {
		t.Errorf("warm-state should leave pollingInterval unset")
	}
	if _, ok, _ := unstructured.NestedInt64(spec, "cooldownPeriod"); ok {
		t.Errorf("warm-state should leave cooldownPeriod unset")
	}

	triggers, _, _ := unstructured.NestedSlice(spec, "triggers")
	if len(triggers) != 1 {
		t.Fatalf("warm-state must not prepend a gateway trigger; expected 1, got %d", len(triggers))
	}
	user := triggers[0].(map[string]interface{})
	uMeta := user["metadata"].(map[string]interface{})
	isvcName, err := GenerateInferenceServiceName(service.Name, service.Namespace)
	if err != nil {
		t.Fatalf("GenerateInferenceServiceName: %v", err)
	}
	wantUserQuery := `sum(vllm:num_requests_running{namespace="` + service.Namespace + `",deployment="` + isvcName + constants.PredictorServiceSuffix + `"})`
	if got := uMeta["metricQuery"]; got != wantUserQuery {
		t.Errorf("warm-state trigger metricQuery: want %q, got %v", wantUserQuery, got)
	}
}

// TestPlanScaledObject_UserQueryWithLabelSelectorPreserved verifies that a
// user query that already carries an inline label selector is passed
// through verbatim (no double-wrapping).
func TestPlanScaledObject_UserQueryWithLabelSelectorPreserved(t *testing.T) {
	service := NewService("qwen-chat").Build()
	service.Spec.MinReplicas = ptr.To(int32(1))
	service.Spec.MaxReplicas = ptr.To(int32(3))
	rawQuery := `sum(vllm:num_requests_running{model_name="qwen3-32b"})`
	metric := validVLLMMetric()
	metric.PodMetric.Metric.Query = rawQuery
	service.Spec.AutoScaling = &aimv1alpha1.AIMServiceAutoScaling{
		Metrics: []aimv1alpha1.AIMServiceMetricsSpec{metric},
	}

	obj := planScaledObject(context.Background(), service, nil)
	if obj == nil {
		t.Fatal("expected ScaledObject")
	}
	so := obj.(*unstructured.Unstructured)
	spec := mustNestedMap(t, so.Object, "spec")
	triggers, _, _ := unstructured.NestedSlice(spec, "triggers")
	uMeta := triggers[0].(map[string]interface{})["metadata"].(map[string]interface{})
	if got := uMeta["metricQuery"]; got != rawQuery {
		t.Errorf("user-qualified query must be passed through verbatim; want %q, got %v", rawQuery, got)
	}
}

// TestPlanScaledObject_PollingAndCooldownOverrides verifies user-supplied
// pollingInterval/cooldownPeriod take precedence over the defaults.
func TestPlanScaledObject_PollingAndCooldownOverrides(t *testing.T) {
	service := NewService("qwen-chat").Build()
	service.Spec.MinReplicas = ptr.To(int32(0))
	service.Spec.MaxReplicas = ptr.To(int32(3))
	service.Spec.AutoScaling = &aimv1alpha1.AIMServiceAutoScaling{
		PollingInterval: ptr.To(int32(10)),
		CooldownPeriod:  ptr.To(int32(120)),
		Metrics:         []aimv1alpha1.AIMServiceMetricsSpec{validVLLMMetric()},
	}

	obj := planScaledObject(context.Background(), service, nil)
	if obj == nil {
		t.Fatal("expected ScaledObject")
	}
	so := obj.(*unstructured.Unstructured)
	spec := mustNestedMap(t, so.Object, "spec")

	if poll, _, _ := unstructured.NestedInt64(spec, "pollingInterval"); poll != 10 {
		t.Errorf("user pollingInterval override must win; got %d", poll)
	}
	if cd, _, _ := unstructured.NestedInt64(spec, "cooldownPeriod"); cd != 120 {
		t.Errorf("user cooldownPeriod override must win; got %d", cd)
	}
}

// TestPlanScaledObject_ReplicaBoundsClampMax verifies maxReplicas is
// clamped to at least 1 when unspecified, even with minReplicas=0.
func TestPlanScaledObject_ReplicaBoundsClampMax(t *testing.T) {
	service := NewService("qwen-chat").Build()
	service.Spec.MinReplicas = ptr.To(int32(0))
	service.Spec.AutoScaling = &aimv1alpha1.AIMServiceAutoScaling{
		Metrics: []aimv1alpha1.AIMServiceMetricsSpec{validVLLMMetric()},
	}
	// MaxReplicas intentionally left nil.

	obj := planScaledObject(context.Background(), service, nil)
	if obj == nil {
		t.Fatal("expected ScaledObject")
	}
	so := obj.(*unstructured.Unstructured)
	spec := mustNestedMap(t, so.Object, "spec")
	if max, _, _ := unstructured.NestedInt64(spec, "maxReplicaCount"); max != 1 {
		t.Errorf("expected maxReplicaCount clamped to 1; got %d", max)
	}
}

// TestPlanScaledObject_OwnerReferenceAndLabels verifies the ScaledObject
// is garbage-collected with its owning AIMService and discoverable via
// the standard label selectors operators already use.
func TestPlanScaledObject_OwnerReferenceAndLabels(t *testing.T) {
	service := NewService("qwen-chat").Build()
	service.UID = testServiceUID
	service.Spec.MinReplicas = ptr.To(int32(0))
	service.Spec.AutoScaling = &aimv1alpha1.AIMServiceAutoScaling{
		Metrics: []aimv1alpha1.AIMServiceMetricsSpec{validVLLMMetric()},
	}

	obj := planScaledObject(context.Background(), service, nil)
	if obj == nil {
		t.Fatal("expected ScaledObject")
	}

	refs := obj.GetOwnerReferences()
	if len(refs) != 1 {
		t.Fatalf("expected exactly one owner reference, got %d", len(refs))
	}
	if refs[0].UID != service.UID {
		t.Errorf("expected owner UID %q, got %q", service.UID, refs[0].UID)
	}
	if refs[0].Controller == nil || !*refs[0].Controller {
		t.Errorf("expected controller=true on owner reference for GC")
	}

	labels := obj.GetLabels()
	if labels[constants.LabelK8sManagedBy] != constants.LabelValueManagedBy {
		t.Errorf("expected managed-by label, got %q", labels[constants.LabelK8sManagedBy])
	}
	if labels[constants.LabelK8sComponent] != constants.ComponentAutoscaling {
		t.Errorf("expected component=autoscaling label, got %q", labels[constants.LabelK8sComponent])
	}
	if labels[constants.LabelService] != "qwen-chat" {
		t.Errorf("expected service=qwen-chat label, got %q", labels[constants.LabelService])
	}
}

// TestPlanScaledObject_ScalerAddressEnvOverride verifies the scaler
// endpoint can be overridden via AIM_KEDA_OTEL_SCALER_ADDRESS.
func TestPlanScaledObject_ScalerAddressEnvOverride(t *testing.T) {
	t.Setenv(constants.EnvAIMKEDAOTelScalerAddress, "scaler.custom.svc:5555")

	service := NewService("qwen-chat").Build()
	service.Spec.MinReplicas = ptr.To(int32(0))
	service.Spec.AutoScaling = &aimv1alpha1.AIMServiceAutoScaling{
		Metrics: []aimv1alpha1.AIMServiceMetricsSpec{validVLLMMetric()},
	}

	obj := planScaledObject(context.Background(), service, nil)
	so := obj.(*unstructured.Unstructured)
	triggers, _, _ := unstructured.NestedSlice(so.Object, "spec", "triggers")
	for i, raw := range triggers {
		trig := raw.(map[string]interface{})
		meta := trig["metadata"].(map[string]interface{})
		if meta["scalerAddress"] != "scaler.custom.svc:5555" {
			t.Errorf("trigger %d: scalerAddress override not applied, got %v", i, meta["scalerAddress"])
		}
	}
}

// TestPlanScaledObject_GatewayActivationDefaultsAreCompiledIn verifies the
// gateway activation trigger always uses the compiled-in targetValue and
// operationOverTime constants. These are deliberately not env/Helm knobs:
// `avg` is the only correct operationOverTime (joined to the collector's
// cumulativetodelta pipeline; see the invariant guard in invariant_test.go).
func TestPlanScaledObject_GatewayActivationDefaultsAreCompiledIn(t *testing.T) {
	service := NewService("qwen-chat").Build()
	service.Spec.MinReplicas = ptr.To(int32(0))
	service.Spec.AutoScaling = &aimv1alpha1.AIMServiceAutoScaling{
		Metrics: []aimv1alpha1.AIMServiceMetricsSpec{validVLLMMetric()},
	}

	obj := planScaledObject(context.Background(), service, nil)
	so := obj.(*unstructured.Unstructured)
	triggers, _, _ := unstructured.NestedSlice(so.Object, "spec", "triggers")
	if len(triggers) == 0 {
		t.Fatal("expected at least the gateway activation trigger")
	}
	// Trigger order is fixed: gateway activation is triggers[0].
	gMeta := triggers[0].(map[string]interface{})["metadata"].(map[string]interface{})
	if got := gMeta["targetValue"]; got != constants.DefaultGatewayActivationTargetValue {
		t.Errorf("gateway targetValue: want %q, got %v", constants.DefaultGatewayActivationTargetValue, got)
	}
	if got := gMeta["operationOverTime"]; got != constants.DefaultGatewayActivationOperationOverTime {
		t.Errorf("gateway operationOverTime: want %q, got %v", constants.DefaultGatewayActivationOperationOverTime, got)
	}
}

// TestPlanScaledObject_Idempotent verifies the planner is pure -- two
// calls on the same service yield equal objects (required for SSA).
func TestPlanScaledObject_Idempotent(t *testing.T) {
	service := NewService("qwen-chat").Build()
	service.Spec.MinReplicas = ptr.To(int32(0))
	service.Spec.MaxReplicas = ptr.To(int32(3))
	service.Spec.AutoScaling = &aimv1alpha1.AIMServiceAutoScaling{
		Metrics: []aimv1alpha1.AIMServiceMetricsSpec{validVLLMMetric()},
	}

	a := planScaledObject(context.Background(), service, nil)
	b := planScaledObject(context.Background(), service, nil)
	if a == nil || b == nil {
		t.Fatal("expected ScaledObject from both calls")
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("planScaledObject must be pure; calls differ:\nfirst:  %#v\nsecond: %#v", a, b)
	}
}

// TestComputePredictorCooldown exercises the memory-derived cooldown
// heuristic across the model-size spectrum and degenerate cases (no
// request, oversized request).
func TestComputePredictorCooldown(t *testing.T) {
	t.Setenv(constants.EnvAIMCooldownSecondsPerGiMemory, "") // default 5 s/GiB

	tests := []struct {
		name   string
		memory string // empty = no request
		want   int32
	}{
		{
			name:   "no memory request falls back to flat default",
			memory: "",
			want:   defaultCooldownPeriodScaleToZero,
		},
		{
			name:   "1Gi yields base + perGiB",
			memory: "1Gi",
			want:   300 + 1*5,
		},
		{
			name:   "32Gi (7B fp16)",
			memory: "32Gi",
			want:   300 + 32*5,
		},
		{
			name:   "128Gi (32B fp16)",
			memory: "128Gi",
			want:   300 + 128*5,
		},
		{
			name:   "256Gi (70B fp16) clamps to ceiling",
			memory: "256Gi",
			want:   cooldownPeriodMaxSeconds,
		},
		{
			name:   "1Ti clamps to ceiling",
			memory: "1Ti",
			want:   cooldownPeriodMaxSeconds,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewService("test").Build()
			if tc.memory != "" {
				svc.Spec.Resources = &corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceMemory: resource.MustParse(tc.memory),
					},
				}
			}
			got := computePredictorCooldown(svc, nil)
			if got != tc.want {
				t.Errorf("computePredictorCooldown(memory=%q) = %d, want %d", tc.memory, got, tc.want)
			}
		})
	}
}

// TestComputePredictorCooldown_PerGiBOverride verifies operators can
// tune the per-GiB multiplier via EnvAIMCooldownSecondsPerGiMemory.
func TestComputePredictorCooldown_PerGiBOverride(t *testing.T) {
	svc := NewService("test").Build()
	svc.Spec.Resources = &corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse("128Gi"),
		},
	}

	tests := []struct {
		name      string
		override  string
		wantBase  int32 // expected = cooldownPeriodMinSeconds + 128*wantBase, clamped
		wantClamp bool
	}{
		{name: "default (env unset) uses 5 s/GiB", override: "", wantBase: 5},
		{name: "tuned-down to 2 s/GiB", override: "2", wantBase: 2},
		{name: "7 s/GiB at 128GiB stays under ceiling", override: "7", wantBase: 7},
		{name: "8 s/GiB at 128GiB clamps to ceiling", override: "8", wantClamp: true},
		{name: "garbage value falls back to default", override: "not-a-number", wantBase: 5},
		{name: "negative value falls back to default", override: "-1", wantBase: 5},
		{name: "zero collapses to flat min", override: "0", wantBase: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(constants.EnvAIMCooldownSecondsPerGiMemory, tc.override)
			got := computePredictorCooldown(svc, nil)
			if tc.wantClamp {
				if got != cooldownPeriodMaxSeconds {
					t.Errorf("expected clamp to %d, got %d", cooldownPeriodMaxSeconds, got)
				}
				return
			}
			want := cooldownPeriodMinSeconds + 128*tc.wantBase
			if want < cooldownPeriodMinSeconds {
				want = cooldownPeriodMinSeconds
			}
			if got != want {
				t.Errorf("override=%q: got %d, want %d", tc.override, got, want)
			}
		})
	}
}

// TestComputePredictorCooldown_EffectiveResourcesPriority pins the
// memory-source precedence: effectiveResources wins over
// service.Spec.Resources; the latter is the fallback when the template
// hasn't resolved yet.
func TestComputePredictorCooldown_EffectiveResourcesPriority(t *testing.T) {
	t.Setenv(constants.EnvAIMCooldownSecondsPerGiMemory, "") // default 5 s/GiB

	tests := []struct {
		name                string
		serviceMemory       string // service.Spec.Resources.Requests[memory]; empty = unset
		effectiveMemory     string // merged effective; empty = nil pointer
		wantCooldownSeconds int32
	}{
		{
			// The canonical real-world case from kaiwo-tw-1: no
			// service override, template+GPU default merges to
			// 48 GiB. Effective resources MUST be the source.
			name:                "no override, template-merged 48Gi -> formula on 48Gi",
			effectiveMemory:     "48Gi",
			wantCooldownSeconds: 300 + 48*5,
		},
		{
			// Operator who overrides spec.resources directly with
			// a 96 GiB request: effective resources reflect the
			// override (the merge in kserve.go uses service spec
			// as the highest precedence), and the cooldown
			// follows along. Picked 96 GiB rather than something
			// larger so the formula doesn't hit the 1200 s
			// ceiling -- the override precedence is what we're
			// testing here, not the clamp.
			name:                "service override + matching effective -> formula on overridden value",
			serviceMemory:       "96Gi",
			effectiveMemory:     "96Gi",
			wantCooldownSeconds: 300 + 96*5,
		},
		{
			// Defensive: if a caller (a future test, a refactor)
			// passes effectiveResources=nil but spec.resources is
			// set, fall back to the service value rather than
			// collapsing to the flat default. Lets the cooldown
			// stabilize one reconcile earlier when an override
			// IS present.
			name:                "nil effective, service override only -> formula on service value",
			serviceMemory:       "128Gi",
			wantCooldownSeconds: 300 + 128*5,
		},
		{
			// Both unset: the genuine "we have no information"
			// case. Returns flat default; the reconciler is
			// expected to re-plan once the template resolves.
			name:                "both unset -> flat default",
			wantCooldownSeconds: defaultCooldownPeriodScaleToZero,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewService("test").Build()
			if tc.serviceMemory != "" {
				svc.Spec.Resources = &corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceMemory: resource.MustParse(tc.serviceMemory),
					},
				}
			}
			var eff *corev1.ResourceRequirements
			if tc.effectiveMemory != "" {
				eff = &corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceMemory: resource.MustParse(tc.effectiveMemory),
					},
				}
			}
			got := computePredictorCooldown(svc, eff)
			if got != tc.wantCooldownSeconds {
				t.Errorf("got cooldown=%d, want %d", got, tc.wantCooldownSeconds)
			}
		})
	}
}

// TestComputePredictorCooldown_RequestsVsLimits pins the
// max(requests, limits) reduction inside memoryGiBFromRequirements.
func TestComputePredictorCooldown_RequestsVsLimits(t *testing.T) {
	t.Setenv(constants.EnvAIMCooldownSecondsPerGiMemory, "")

	tests := []struct {
		name                string
		requestsMemory      string // empty = key not set
		limitsMemory        string // empty = key not set
		wantCooldownSeconds int32
	}{
		{
			name:                "requests<limits uses limits (1 MI300X default)",
			requestsMemory:      "32Gi",
			limitsMemory:        "48Gi",
			wantCooldownSeconds: 300 + 48*5,
		},
		{
			name:                "requests<limits uses limits (2 MI300X default)",
			requestsMemory:      "64Gi",
			limitsMemory:        "96Gi",
			wantCooldownSeconds: 300 + 96*5,
		},
		{
			name:                "requests==limits uses the shared value",
			requestsMemory:      "48Gi",
			limitsMemory:        "48Gi",
			wantCooldownSeconds: 300 + 48*5,
		},
		{
			name:                "limits only uses limits",
			limitsMemory:        "48Gi",
			wantCooldownSeconds: 300 + 48*5,
		},
		{
			name:                "requests only uses requests",
			requestsMemory:      "16Gi",
			wantCooldownSeconds: 300 + 16*5,
		},
		{
			name:                "requests>limits (invalid) still returns max",
			requestsMemory:      "64Gi",
			limitsMemory:        "32Gi",
			wantCooldownSeconds: 300 + 64*5,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			eff := &corev1.ResourceRequirements{}
			if tc.requestsMemory != "" {
				eff.Requests = corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse(tc.requestsMemory),
				}
			}
			if tc.limitsMemory != "" {
				eff.Limits = corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse(tc.limitsMemory),
				}
			}
			svc := NewService("test").Build()
			got := computePredictorCooldown(svc, eff)
			if got != tc.wantCooldownSeconds {
				t.Errorf("got cooldown=%d, want %d", got, tc.wantCooldownSeconds)
			}
		})
	}
}

// TestResolvePollingAndCooldown_Precedence pins the cooldown-resolution
// precedence: user override > memory-derived heuristic > flat default.
func TestResolvePollingAndCooldown_Precedence(t *testing.T) {
	memSpec := &corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse("128Gi"),
		},
	}

	tests := []struct {
		name            string
		userCooldown    *int32
		resources       *corev1.ResourceRequirements
		scaleToZero     bool
		wantCooldownNil bool
		wantCooldownVal int32
	}{
		{
			name:            "warm state leaves cooldown unset (KEDA platform default applies)",
			scaleToZero:     false,
			resources:       memSpec,
			wantCooldownNil: true,
		},
		{
			name:            "scale-to-zero + user override wins over memory heuristic",
			userCooldown:    ptr.To(int32(900)),
			resources:       memSpec,
			scaleToZero:     true,
			wantCooldownVal: 900,
		},
		{
			name:            "scale-to-zero + memory request derives cooldown",
			resources:       memSpec,
			scaleToZero:     true,
			wantCooldownVal: 300 + 128*5,
		},
		{
			name:            "scale-to-zero + no memory request falls back to flat default",
			scaleToZero:     true,
			wantCooldownVal: defaultCooldownPeriodScaleToZero,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewService("test").Build()
			svc.Spec.Resources = tc.resources
			if tc.userCooldown != nil {
				svc.Spec.AutoScaling = &aimv1alpha1.AIMServiceAutoScaling{
					CooldownPeriod: tc.userCooldown,
				}
			}
			_, cd := resolvePollingAndCooldown(svc, tc.scaleToZero, nil)
			if tc.wantCooldownNil {
				if cd != nil {
					t.Errorf("expected cooldown to be unset for warm-state, got %d", *cd)
				}
				return
			}
			if cd == nil {
				t.Fatalf("expected cooldown to be set, got nil")
			}
			if *cd != tc.wantCooldownVal {
				t.Errorf("got cooldown=%d, want %d", *cd, tc.wantCooldownVal)
			}
		})
	}
}

// validVLLMMetric returns a minimal valid AIMServiceMetricsSpec matching
// the shape used by the e2e cached service fixture.
func validVLLMMetric() aimv1alpha1.AIMServiceMetricsSpec {
	return aimv1alpha1.AIMServiceMetricsSpec{
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
	}
}

func mustNestedMap(t *testing.T, obj map[string]interface{}, fields ...string) map[string]interface{} {
	t.Helper()
	m, found, err := unstructured.NestedMap(obj, fields...)
	if err != nil {
		t.Fatalf("nested map at %v: %v", fields, err)
	}
	if !found {
		t.Fatalf("nested map at %v not found", fields)
	}
	return m
}
