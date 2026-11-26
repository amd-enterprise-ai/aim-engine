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
	"errors"
	"reflect"
	"testing"
	"time"

	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	fakediscovery "k8s.io/client-go/discovery/fake"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

const otelTestServiceUID = types.UID("service-uid")

// autoscaledService returns a warm autoscaled service with the given user
// metrics.
func autoscaledService(metrics ...aimv1alpha1.AIMServiceMetricsSpec) *aimv1alpha2.AIMService {
	service := NewService("svc").Build()
	service.UID = otelTestServiceUID
	service.Spec.MinReplicas = ptr.To(int32(1))
	service.Spec.MaxReplicas = ptr.To(int32(3))
	service.Spec.AutoScaling = &aimv1alpha1.AIMServiceAutoScaling{Metrics: metrics}
	return service
}

func podMetric(query string, names ...string) aimv1alpha1.AIMServiceMetricsSpec {
	metric := validVLLMMetric()
	metric.PodMetric.Metric.Query = query
	metric.PodMetric.Metric.MetricNames = names
	return metric
}

func otelCollectorNames(t *testing.T, service *aimv1alpha2.AIMService) (deploymentName, isvcName string) {
	t.Helper()
	isvcName, err := GenerateInferenceServiceName(service.Name, service.Namespace)
	if err != nil {
		t.Fatalf("GenerateInferenceServiceName: %v", err)
	}
	return isvcName + constants.PredictorServiceSuffix, isvcName
}

func TestOTelCollectorMetricNames(t *testing.T) {
	scaleToZeroOnly := NewService("svc").Build()
	scaleToZeroOnly.Spec.MinReplicas = ptr.To(int32(0))
	scaleToZeroOnly.Spec.MaxReplicas = ptr.To(int32(3))

	noTarget := podMetric("q", "m")
	noTarget.PodMetric.Target = nil

	tests := []struct {
		name       string
		service    *aimv1alpha2.AIMService
		wantNeeded bool
		wantNames  []string
	}{
		{
			name:    "fixed replicas",
			service: NewService("svc").Build(),
		},
		{
			name:    "scale-to-zero without user metrics",
			service: scaleToZeroOnly,
		},
		{
			name:    "metric without a target yields no trigger",
			service: autoscaledService(noTarget),
		},
		{
			name:       "declared names are forwarded",
			service:    autoscaledService(podMetric("vllm:num_requests_running", "vllm:num_requests_running")),
			wantNeeded: true,
			wantNames:  []string{"vllm:num_requests_running"},
		},
		{
			name: "names across metrics are deduplicated",
			service: autoscaledService(
				podMetric("a", "a", "b"),
				podMetric("b", "b", "c"),
			),
			wantNeeded: true,
			wantNames:  []string{"a", "b", "c"},
		},
		{
			name: "query-only metric forwards every scraped metric",
			service: autoscaledService(
				podMetric("a", "a"),
				podMetric(`sum(rate(vllm:request_success_total[1m]))`),
			),
			wantNeeded: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			names, needed := otelCollectorMetricNames(tt.service)
			if needed != tt.wantNeeded {
				t.Fatalf("needed = %v, want %v", needed, tt.wantNeeded)
			}
			if !reflect.DeepEqual(names, tt.wantNames) {
				t.Errorf("names = %v, want %v", names, tt.wantNames)
			}
		})
	}
}

func TestBuildOTelCollector(t *testing.T) {
	if got := buildOTelCollector(NewService("svc").Build(), "svc-predictor"); got != nil {
		t.Fatalf("fixed-replica service must not get a collector, got %v", got.Object)
	}

	service := autoscaledService(podMetric("vllm:num_requests_running", "vllm:num_requests_running"))
	collector := buildOTelCollector(service, "isvc-predictor")
	if collector == nil {
		t.Fatal("expected a collector")
	}
	if collector.GroupVersionKind() != openTelemetryCollectorGVK {
		t.Errorf("GVK = %v", collector.GroupVersionKind())
	}
	if collector.GetName() != "isvc-predictor" || collector.GetNamespace() != service.Namespace {
		t.Errorf("key = %s/%s", collector.GetNamespace(), collector.GetName())
	}
	if got := collector.GetLabels()[constants.LabelService]; got != "svc" {
		t.Errorf("service label = %q", got)
	}
	if mode, _, _ := unstructured.NestedString(collector.Object, "spec", "mode"); mode != "sidecar" {
		t.Errorf("mode = %q", mode)
	}

	// Only the container-level settings of Pod Security restricted: the
	// non-root user and the seccomp profile come from the predictor's Pod
	// security context, and OpenShift's anyuid SCC refuses a seccomp profile.
	securityContext := mustNestedMap(t, collector.Object, "spec", "securityContext")
	wantSecurityContext := map[string]interface{}{
		"allowPrivilegeEscalation": false,
		"capabilities":             map[string]interface{}{"drop": []interface{}{"ALL"}},
	}
	if !reflect.DeepEqual(securityContext, wantSecurityContext) {
		t.Errorf("securityContext = %v, want %v", securityContext, wantSecurityContext)
	}

	config := mustNestedMap(t, collector.Object, "spec", "config")
	scrapeConfigs, _, _ := unstructured.NestedSlice(config, "receivers", "prometheus", "config", "scrape_configs")
	if len(scrapeConfigs) != 1 {
		t.Fatalf("scrape_configs = %v", scrapeConfigs)
	}
	targets, _, _ := unstructured.NestedStringSlice(
		scrapeConfigs[0].(map[string]interface{})["static_configs"].([]interface{})[0].(map[string]interface{}),
		"targets",
	)
	if !reflect.DeepEqual(targets, []string{"localhost:" + constants.DefaultPrometheusPort}) {
		t.Errorf("scrape targets = %v", targets)
	}
	if endpoint, _, _ := unstructured.NestedString(config, "exporters", "otlp", "endpoint"); endpoint != constants.DefaultKEDAOTelReceiverEndpoint {
		t.Errorf("exporter endpoint = %q", endpoint)
	}
	filterNames, _, _ := unstructured.NestedStringSlice(config, "processors", "filter/metrics", "metrics", "include", "metric_names")
	if !reflect.DeepEqual(filterNames, []string{"vllm:num_requests_running"}) {
		t.Errorf("filter metric names = %v", filterNames)
	}
	processors, _, _ := unstructured.NestedStringSlice(config, "service", "pipelines", "metrics", "processors")
	if !reflect.DeepEqual(processors, []string{"resourcedetection/env", "transform", "filter/metrics"}) {
		t.Errorf("pipeline processors = %v", processors)
	}
	if _, err := runtime.DefaultUnstructuredConverter.ToUnstructured(collector); err != nil {
		t.Errorf("collector is not serializable: %v", err)
	}
}

func TestBuildOTelCollector_ForwardsEverythingForQueryOnlyMetrics(t *testing.T) {
	service := autoscaledService(podMetric(`sum(vllm:num_requests_running)`))
	config := mustNestedMap(t, buildOTelCollector(service, "isvc-predictor").Object, "spec", "config")
	if _, found, _ := unstructured.NestedMap(config, "processors", "filter/metrics"); found {
		t.Error("query-only metrics must not configure a metric filter")
	}
	processors, _, _ := unstructured.NestedStringSlice(config, "service", "pipelines", "metrics", "processors")
	if !reflect.DeepEqual(processors, []string{"resourcedetection/env", "transform"}) {
		t.Errorf("pipeline processors = %v", processors)
	}
}

func TestBuildOTelCollector_ReceiverEndpointEnvOverride(t *testing.T) {
	t.Setenv(constants.EnvAIMKEDAOTelReceiverEndpoint, "otel-receiver.metrics.svc:4317")
	service := autoscaledService(validVLLMMetric())
	endpoint, _, _ := unstructured.NestedString(
		buildOTelCollector(service, "isvc-predictor").Object,
		"spec", "config", "exporters", "otlp", "endpoint",
	)
	if endpoint != "otel-receiver.metrics.svc:4317" {
		t.Errorf("exporter endpoint = %q", endpoint)
	}
}

func TestPlanOTelCollector(t *testing.T) {
	service := autoscaledService(validVLLMMetric())
	fixed := NewService("svc").Build()
	fixed.UID = otelTestServiceUID
	collectorName, isvcName := otelCollectorNames(t, service)

	notFound := apierrors.NewNotFound(schema.GroupResource{Group: "opentelemetry.io", Resource: "opentelemetrycollectors"}, collectorName)
	collector := func(owner metav1.OwnerReference, deleting bool) controllerutils.FetchResult[*metav1.PartialObjectMetadata] {
		obj := newOTelCollectorMetadata()
		obj.Name = collectorName
		obj.Namespace = service.Namespace
		obj.OwnerReferences = []metav1.OwnerReference{owner}
		if deleting {
			obj.DeletionTimestamp = ptr.To(metav1.Now())
		}
		return controllerutils.FetchResult[*metav1.PartialObjectMetadata]{Value: obj}
	}
	kserveOwner := metav1.OwnerReference{
		APIVersion: servingv1beta1.SchemeGroupVersion.String(),
		Kind:       "InferenceService",
		Name:       isvcName,
		UID:        "isvc-uid",
		Controller: ptr.To(true),
	}
	aimOwner := metav1.OwnerReference{
		APIVersion: "aim.eai.amd.com/v1alpha2",
		Kind:       "AIMService",
		Name:       service.Name,
		UID:        otelTestServiceUID,
		Controller: ptr.To(true),
	}
	foreignOwner := metav1.OwnerReference{
		APIVersion: "example.com/v1",
		Kind:       "Other",
		Name:       "other",
		UID:        "other-uid",
		Controller: ptr.To(true),
	}

	isvc := func(declaresMetrics bool) controllerutils.FetchResult[*servingv1beta1.InferenceService] {
		obj := &servingv1beta1.InferenceService{ObjectMeta: metav1.ObjectMeta{Name: isvcName, Namespace: service.Namespace}}
		if declaresMetrics {
			obj.Spec.Predictor.AutoScaling = &servingv1beta1.AutoScalingSpec{
				Metrics: []servingv1beta1.MetricsSpec{{
					Type: servingv1beta1.PodMetricSourceType,
					PodMetric: &servingv1beta1.PodMetricSource{
						Metric: servingv1beta1.PodMetrics{
							Backend:     servingv1beta1.OpenTelemetryBackend,
							MetricNames: []string{"vllm:num_requests_running"},
						},
					},
				}},
			}
		}
		return controllerutils.FetchResult[*servingv1beta1.InferenceService]{Value: obj}
	}
	isvcNotFound := controllerutils.FetchResult[*servingv1beta1.InferenceService]{
		Error: apierrors.NewNotFound(schema.GroupResource{Group: "serving.kserve.io", Resource: "inferenceservices"}, isvcName),
	}
	transient := errors.New("connection refused")

	tests := []struct {
		name        string
		service     *aimv1alpha2.AIMService
		served      bool
		isvc        controllerutils.FetchResult[*servingv1beta1.InferenceService]
		observed    controllerutils.FetchResult[*metav1.PartialObjectMetadata]
		wantApply   bool
		wantDelete  bool
		wantRequeue bool
	}{
		{
			name:     "API not served",
			service:  service,
			isvc:     isvcNotFound,
			observed: controllerutils.FetchResult[*metav1.PartialObjectMetadata]{},
		},
		{
			name:      "first reconcile creates the collector before the InferenceService",
			service:   service,
			served:    true,
			isvc:      isvcNotFound,
			observed:  controllerutils.FetchResult[*metav1.PartialObjectMetadata]{Error: notFound},
			wantApply: true,
		},
		{
			name:     "no collector needed and none present",
			service:  fixed,
			served:   true,
			isvc:     isvc(false),
			observed: controllerutils.FetchResult[*metav1.PartialObjectMetadata]{Error: notFound},
		},
		{
			name:     "collector fetch error",
			service:  service,
			served:   true,
			isvc:     isvc(false),
			observed: controllerutils.FetchResult[*metav1.PartialObjectMetadata]{Error: transient},
		},
		{
			name:     "InferenceService fetch error",
			service:  service,
			served:   true,
			isvc:     controllerutils.FetchResult[*servingv1beta1.InferenceService]{Error: transient},
			observed: collector(kserveOwner, false),
		},
		{
			name:     "KServe still manages its collector",
			service:  service,
			served:   true,
			isvc:     isvc(true),
			observed: collector(kserveOwner, false),
		},
		{
			name:        "handover deletes the KServe collector and waits",
			service:     service,
			served:      true,
			isvc:        isvc(false),
			observed:    collector(kserveOwner, false),
			wantDelete:  true,
			wantRequeue: true,
		},
		{
			name:       "KServe collector is removed when no longer needed",
			service:    fixed,
			served:     true,
			isvc:       isvc(false),
			observed:   collector(kserveOwner, false),
			wantDelete: true,
		},
		{
			name:        "handover waits for the finalizer",
			service:     service,
			served:      true,
			isvc:        isvc(false),
			observed:    collector(kserveOwner, true),
			wantRequeue: true,
		},
		{
			name:      "owned collector is kept up to date",
			service:   service,
			served:    true,
			isvc:      isvc(false),
			observed:  collector(aimOwner, false),
			wantApply: true,
		},
		{
			name:       "owned collector is removed when no longer needed",
			service:    fixed,
			served:     true,
			isvc:       isvc(false),
			observed:   collector(aimOwner, false),
			wantDelete: true,
		},
		{
			name:      "foreign collector surfaces through the apply conflict",
			service:   service,
			served:    true,
			isvc:      isvc(false),
			observed:  collector(foreignOwner, false),
			wantApply: true,
		},
		{
			name:     "foreign collector is left alone when not needed",
			service:  fixed,
			served:   true,
			isvc:     isvc(false),
			observed: collector(foreignOwner, false),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var plan controllerutils.PlanResult
			planOTelCollector(&plan, tt.service, tt.isvc, tt.observed, tt.served)

			if got := countOTelCollectors(plan.GetToApply()); got != boolToInt(tt.wantApply) {
				t.Errorf("applied collectors = %d, want apply %v", got, tt.wantApply)
			}
			if got := countOTelCollectors(plan.GetToDelete()); got != boolToInt(tt.wantDelete) {
				t.Errorf("deleted collectors = %d, want delete %v", got, tt.wantDelete)
			}
			if got := plan.RequeueAfter; (got > 0) != tt.wantRequeue {
				t.Errorf("requeueAfter = %v, want requeue %v", got, tt.wantRequeue)
			}
			if tt.wantRequeue && plan.RequeueAfter > 10*time.Second {
				t.Errorf("handover requeue %v is too slow", plan.RequeueAfter)
			}
		})
	}
}

func countOTelCollectors(objs []client.Object) int {
	count := 0
	for _, obj := range objs {
		if obj.GetObjectKind().GroupVersionKind() == openTelemetryCollectorGVK {
			count++
		}
	}
	return count
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func TestOTelCollectorComponentHealth(t *testing.T) {
	service := autoscaledService(validVLLMMetric())

	if got := otelCollectorComponentHealth(NewService("svc").Build(), false, controllerutils.FetchResult[*metav1.PartialObjectMetadata]{}); got.Component != "" {
		t.Errorf("fixed-replica service must not report collector health, got %+v", got)
	}

	unavailable := otelCollectorComponentHealth(service, false, controllerutils.FetchResult[*metav1.PartialObjectMetadata]{})
	if unavailable.Component != ComponentOpenTelemetryCollector {
		t.Fatalf("component = %q", unavailable.Component)
	}
	if unavailable.Reason != aimv1alpha1.AIMServiceReasonOpenTelemetryCollectorUnavailable {
		t.Errorf("reason = %q", unavailable.Reason)
	}
	if len(unavailable.Errors) != 1 {
		t.Fatalf("errors = %v", unavailable.Errors)
	}
	var stateErr controllerutils.StateEngineError
	if !errors.As(unavailable.Errors[0], &stateErr) || stateErr.Category() != controllerutils.ErrorCategoryInvalidSpec {
		t.Errorf("missing API must be an invalid-spec error, got %v", unavailable.Errors[0])
	}

	fetchErr := otelCollectorComponentHealth(service, true, controllerutils.FetchResult[*metav1.PartialObjectMetadata]{Error: errors.New("boom")})
	if fetchErr.Component != ComponentOpenTelemetryCollector || len(fetchErr.Errors) != 1 {
		t.Errorf("fetch error must be reported, got %+v", fetchErr)
	}

	notFound := apierrors.NewNotFound(schema.GroupResource{Resource: "opentelemetrycollectors"}, "x")
	if got := otelCollectorComponentHealth(service, true, controllerutils.FetchResult[*metav1.PartialObjectMetadata]{Error: notFound}); got.Component != "" {
		t.Errorf("a not-yet-created collector is not a problem, got %+v", got)
	}
}

func TestClearRecoveredOTelCollectorCondition(t *testing.T) {
	condType := ComponentOpenTelemetryCollector + controllerutils.ComponentConditionSuffix
	service := autoscaledService(validVLLMMetric())

	clearRecoveredOTelCollectorCondition(nil, service, true, controllerutils.FetchResult[*metav1.PartialObjectMetadata]{})

	cm := controllerutils.NewConditionManager(nil)
	cm.MarkFalse(condType, aimv1alpha1.AIMServiceReasonOpenTelemetryCollectorUnavailable, "missing")
	clearRecoveredOTelCollectorCondition(cm, service, false, controllerutils.FetchResult[*metav1.PartialObjectMetadata]{})
	if cm.Get(condType) == nil {
		t.Fatal("condition must stay while the API is missing")
	}

	clearRecoveredOTelCollectorCondition(cm, service, true, controllerutils.FetchResult[*metav1.PartialObjectMetadata]{})
	if cm.Get(condType) != nil {
		t.Error("condition must be removed once the problem is resolved")
	}
}

func TestDetectOpenTelemetryCollectorAPI(t *testing.T) {
	served := &fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{}}
	served.Resources = []*metav1.APIResourceList{{
		GroupVersion: "opentelemetry.io/v1beta1",
		APIResources: []metav1.APIResource{{Name: "opentelemetrycollectors", Kind: "OpenTelemetryCollector"}},
	}}
	if ok, err := detectOpenTelemetryCollectorAPI(served); err != nil || !ok {
		t.Errorf("served API: got (%v, %v)", ok, err)
	}

	otherVersion := &fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{}}
	otherVersion.Resources = []*metav1.APIResourceList{{
		GroupVersion: "opentelemetry.io/v1alpha1",
		APIResources: []metav1.APIResource{{Name: "opentelemetrycollectors", Kind: "OpenTelemetryCollector"}},
	}}
	if ok, err := detectOpenTelemetryCollectorAPI(otherVersion); err != nil || ok {
		t.Errorf("only v1alpha1 served: got (%v, %v)", ok, err)
	}

	otherKind := &fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{}}
	otherKind.Resources = []*metav1.APIResourceList{{
		GroupVersion: "opentelemetry.io/v1beta1",
		APIResources: []metav1.APIResource{{Name: "targetallocators", Kind: "TargetAllocator"}},
	}}
	if ok, err := detectOpenTelemetryCollectorAPI(otherKind); err != nil || ok {
		t.Errorf("group version without collectors: got (%v, %v)", ok, err)
	}

	failing := &fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{}}
	failing.PrependReactor("get", "resource", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("connection refused")
	})
	if _, err := detectOpenTelemetryCollectorAPI(failing); err == nil {
		t.Error("a discovery failure must be returned, not read as a missing API")
	}
}
