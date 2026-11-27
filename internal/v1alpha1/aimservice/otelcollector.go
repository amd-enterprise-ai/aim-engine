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
	"os"
	"regexp"
	"strings"
	"time"

	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

// openTelemetryCollectorGVK is the OpenTelemetry Operator collector kind.
// Modeled as unstructured so the controller does not need to vendor the
// operator's Go types.
var openTelemetryCollectorGVK = schema.GroupVersionKind{
	Group:   "opentelemetry.io",
	Version: "v1beta1",
	Kind:    "OpenTelemetryCollector",
}

const openTelemetryCollectorResource = "opentelemetrycollectors"

// ComponentOpenTelemetryCollector is the ComponentHealth name for the metrics
// collector sidecar that feeds the ScaledObject's user metric triggers.
const ComponentOpenTelemetryCollector = "OpenTelemetryCollector"

// A collector that AIM Engine needs to create may be terminating, for example
// the KServe-created collector it has asked to be deleted. The collector
// carries the OpenTelemetry Operator finalizer, so the name stays taken until
// the operator has removed it, which normally takes seconds. Its update and
// delete events reconcile the service; the requeues below only catch a missed
// event. After otelCollectorHandoverTimeout the service reports the handover as
// stuck and re-observes the collector every otelCollectorStuckRecheck.
const (
	otelCollectorHandoverTimeout = 2 * time.Minute
	otelCollectorStuckRecheck    = time.Minute
)

// The collector values match the KServe inferenceservice-config
// opentelemetryCollector defaults, which configured the sidecar before AIM
// Engine created it.
const (
	otelCollectorScrapeInterval = "5s"
	otelCollectorCPURequest     = "200m"
	otelCollectorMemoryRequest  = "512Mi"
	otelCollectorCPULimit       = "1"
	otelCollectorMemoryLimit    = "2Gi"
)

// otelCollectorSecurityContext is utils.RestrictedContainerSecurityContext in
// the unstructured form of the collector's spec.securityContext.
var otelCollectorSecurityContext = func() map[string]interface{} {
	securityContext, err := runtime.DefaultUnstructuredConverter.ToUnstructured(utils.RestrictedContainerSecurityContext())
	if err != nil {
		panic(fmt.Sprintf("convert the collector security context: %v", err))
	}
	return securityContext
}()

// kedaOTelReceiverEndpoint returns the OTLP gRPC endpoint of the
// keda-otel-add-on that the collector sidecars export to. Overridden via
// EnvAIMKEDAOTelReceiverEndpoint (set by the Helm chart).
func kedaOTelReceiverEndpoint() string {
	if v := os.Getenv(constants.EnvAIMKEDAOTelReceiverEndpoint); v != "" {
		return v
	}
	return constants.DefaultKEDAOTelReceiverEndpoint
}

// otelCollectorMetricNames reports whether the service needs a collector
// sidecar and which scraped metrics it forwards. A collector is needed when at
// least one user metric resolves to a ScaledObject trigger, since only those
// read pod metrics from the scaler. The collector forwards the declared
// metricNames and the metric that each trigger query reads. It forwards every
// scraped metric (nil names) only when a trigger declares no metricNames and
// its query names no metric, because the metric the scaler reads is not known.
func otelCollectorMetricNames(service *aimv1alpha2.AIMService) (names []string, needed bool) {
	forwardAll := false
	seen := map[string]bool{}
	add := func(name string) {
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	for _, metric := range collectUserMetrics(service) {
		if buildUserMetricTrigger(metric, service.Namespace, "") == nil {
			continue
		}
		needed = true
		declared := metric.PodMetric.Metric.MetricNames
		for _, name := range declared {
			add(name)
		}
		if name, ok := queryMetricName(userMetricQuery(metric.PodMetric.Metric, service.Namespace, "")); ok {
			add(name)
		} else if len(declared) == 0 {
			forwardAll = true
		}
	}
	if forwardAll {
		return nil, needed
	}
	return names, needed
}

// otelAddOnAggregations are the functions that may wrap a keda-otel-add-on
// metric query, in the order the add-on strips them.
var otelAddOnAggregations = []string{"sum", "avg", "min", "max", "count"}

// prometheusMetricName matches a Prometheus metric name, the form in which the
// collector's Prometheus receiver names scraped metrics.
var prometheusMetricName = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)

// queryMetricName returns the metric that the keda-otel-add-on reads for a
// query, following the add-on's parser: after trimming, it strips each
// aggregation that wraps the whole query, and the metric name is the text
// before the label selector, or the whole query without one. ok is false when
// that text is not a metric name, or the braces do not form a selector.
func queryMetricName(query string) (name string, ok bool) {
	q := strings.TrimSpace(query)
	for _, fn := range otelAddOnAggregations {
		if strings.HasPrefix(q, fn+"(") && strings.HasSuffix(q, ")") {
			q = strings.TrimSuffix(strings.TrimPrefix(q, fn+"("), ")")
		}
	}
	first, last := strings.Index(q, "{"), strings.LastIndex(q, "}")
	switch {
	case first == -1 && last == -1:
		name = q
	case first == -1 || last < first:
		return "", false
	default:
		name = q[:first]
	}
	if !prometheusMetricName.MatchString(name) {
		return "", false
	}
	return name, true
}

// buildOTelCollector returns the sidecar-mode OpenTelemetryCollector that the
// OpenTelemetry Operator injects into the predictor pods of deploymentName, or
// nil when the service needs no collector. The predictor carries the
// sidecar.opentelemetry.io/inject annotation naming this collector.
//
// The sidecar container gets the container settings that the Pod Security
// Standard restricted requires, so autoscaled services can run in namespaces
// that enforce it. The non-root user and the seccomp profile come from the
// predictor's Pod security context, as for the inference container; a
// container-level seccomp profile would make OpenShift's anyuid SCC refuse the
// Pod. In sidecar mode the operator applies spec.securityContext to the
// injected container and ignores spec.podSecurityContext.
func buildOTelCollector(service *aimv1alpha2.AIMService, deploymentName string) *unstructured.Unstructured {
	metricNames, needed := otelCollectorMetricNames(service)
	if !needed {
		return nil
	}

	processors := map[string]interface{}{
		"resourcedetection/env": map[string]interface{}{
			"detectors": []interface{}{"env"},
			"timeout":   "2s",
			"override":  false,
		},
		// The scaler queries scope series by these labels; see scopedMetricQuery.
		"transform": map[string]interface{}{
			"metric_statements": []interface{}{
				map[string]interface{}{
					"context": "datapoint",
					"statements": []interface{}{
						`set(attributes["namespace"], resource.attributes["k8s.namespace.name"])`,
						`set(attributes["deployment"], resource.attributes["k8s.deployment.name"])`,
						`set(attributes["pod"], resource.attributes["k8s.pod.name"])`,
					},
				},
			},
		},
	}
	pipelineProcessors := []interface{}{"resourcedetection/env", "transform"}
	if len(metricNames) > 0 {
		names := make([]interface{}, len(metricNames))
		for i, name := range metricNames {
			names[i] = name
		}
		processors["filter/metrics"] = map[string]interface{}{
			"metrics": map[string]interface{}{
				"include": map[string]interface{}{
					"match_type":   "strict",
					"metric_names": names,
				},
			},
		}
		pipelineProcessors = append(pipelineProcessors, "filter/metrics")
	}

	serviceLabelValue, _ := utils.SanitizeLabelValue(service.Name)

	collector := &unstructured.Unstructured{}
	collector.SetGroupVersionKind(openTelemetryCollectorGVK)
	collector.SetName(deploymentName)
	collector.SetNamespace(service.Namespace)
	collector.SetLabels(map[string]string{
		constants.LabelK8sComponent: constants.ComponentAutoscaling,
		constants.LabelK8sManagedBy: constants.LabelValueManagedBy,
		constants.LabelService:      serviceLabelValue,
	})
	collector.Object["spec"] = map[string]interface{}{
		"mode":            "sidecar",
		"securityContext": runtime.DeepCopyJSON(otelCollectorSecurityContext),
		"resources": map[string]interface{}{
			"requests": map[string]interface{}{
				"cpu":    otelCollectorCPURequest,
				"memory": otelCollectorMemoryRequest,
			},
			"limits": map[string]interface{}{
				"cpu":    otelCollectorCPULimit,
				"memory": otelCollectorMemoryLimit,
			},
		},
		"config": map[string]interface{}{
			"receivers": map[string]interface{}{
				"prometheus": map[string]interface{}{
					"config": map[string]interface{}{
						"scrape_configs": []interface{}{
							map[string]interface{}{
								"job_name":        "otel-collector",
								"scrape_interval": otelCollectorScrapeInterval,
								"static_configs": []interface{}{
									map[string]interface{}{
										"targets": []interface{}{"localhost:" + constants.DefaultPrometheusPort},
									},
								},
							},
						},
					},
				},
			},
			"processors": processors,
			"exporters": map[string]interface{}{
				"otlp": map[string]interface{}{
					"endpoint":    kedaOTelReceiverEndpoint(),
					"compression": "none",
					"tls": map[string]interface{}{
						"insecure": true,
					},
				},
			},
			"service": map[string]interface{}{
				"pipelines": map[string]interface{}{
					"metrics": map[string]interface{}{
						"receivers":  []interface{}{"prometheus"},
						"processors": pipelineProcessors,
						"exporters":  []interface{}{"otlp"},
					},
				},
			},
		},
	}
	return collector
}

// newOTelCollectorMetadata returns an empty metadata-only collector object, for
// reads and watches. Planning needs only owners and the deletion timestamp, and
// metadata reads are served from the informer cache, whereas the client reads
// unstructured objects from the API server.
func newOTelCollectorMetadata() *metav1.PartialObjectMetadata {
	collector := &metav1.PartialObjectMetadata{}
	collector.SetGroupVersionKind(openTelemetryCollectorGVK)
	return collector
}

// fetchOTelCollector fetches the metadata of the collector named after the
// predictor Deployment. It returns an empty result when the cluster does not
// serve the OpenTelemetryCollector API.
func fetchOTelCollector(
	ctx context.Context,
	c client.Client,
	service *aimv1alpha2.AIMService,
	served bool,
) controllerutils.FetchResult[*metav1.PartialObjectMetadata] {
	if !served {
		return controllerutils.FetchResult[*metav1.PartialObjectMetadata]{}
	}
	isvcName, err := GenerateInferenceServiceName(service.Name, service.Namespace)
	if err != nil {
		return controllerutils.FetchResult[*metav1.PartialObjectMetadata]{Error: err}
	}
	return controllerutils.Fetch(ctx, c, client.ObjectKey{
		Namespace: service.Namespace,
		Name:      isvcName + constants.PredictorServiceSuffix,
	}, newOTelCollectorMetadata())
}

// planOTelCollector plans the predictor's collector sidecar.
//
// Before AIM Engine created the collector, KServe created one with the same
// name for each InferenceService whose predictor.autoScaling declared
// OpenTelemetry pod metrics, controlled by the InferenceService. The API server
// refuses a second controller reference, so AIM Engine takes the name over in
// two passes: once the observed InferenceService no longer declares those
// metrics, KServe neither updates nor recreates its collector, and AIM Engine
// deletes it; after the OpenTelemetry Operator has finalized the deletion, AIM
// Engine applies its own. Running pods keep their injected sidecar, so the
// handover does not restart them. While the observed InferenceService still
// declares the metrics (for example a preserved legacy InferenceService),
// KServe keeps reconciling its collector and AIM Engine leaves it alone.
func planOTelCollector(
	planResult *controllerutils.PlanResult,
	service *aimv1alpha2.AIMService,
	inferenceService controllerutils.FetchResult[*servingv1beta1.InferenceService],
	observed controllerutils.FetchResult[*metav1.PartialObjectMetadata],
	served bool,
	now time.Time,
) {
	if !served ||
		(observed.Error != nil && !observed.IsNotFound()) ||
		(inferenceService.Error != nil && !inferenceService.IsNotFound()) {
		return
	}
	isvcName, err := GenerateInferenceServiceName(service.Name, service.Namespace)
	if err != nil {
		return
	}
	desired := buildOTelCollector(service, isvcName+constants.PredictorServiceSuffix)

	current := observed.Value
	if observed.IsNotFound() {
		current = nil
	}
	switch {
	case current == nil:
		if desired != nil {
			planResult.Apply(desired)
		}
	case current.GetDeletionTimestamp() != nil:
		if desired != nil {
			planResult.RequestRequeueAfter(otelCollectorTerminatingRequeue(current, now))
		}
	case isKServeOTelCollector(current, isvcName):
		if kserveManagesOTelCollector(inferenceService.Value) {
			return
		}
		planResult.Delete(current)
		if desired != nil {
			planResult.RequestRequeueAfter(otelCollectorHandoverTimeout)
		}
	case desired != nil:
		planResult.Apply(desired)
	case isControlledBy(current, service.UID):
		planResult.Delete(current)
	}
}

// otelCollectorTerminatingRequeue returns when to re-observe a terminating
// collector: at the end of otelCollectorHandoverTimeout, so a stuck handover is
// reported on time, and every otelCollectorStuckRecheck after that.
func otelCollectorTerminatingRequeue(collector metav1.Object, now time.Time) time.Duration {
	if remaining := collector.GetDeletionTimestamp().Add(otelCollectorHandoverTimeout).Sub(now); remaining > 0 {
		return remaining
	}
	return otelCollectorStuckRecheck
}

// otelCollectorHandoverStuck reports whether the collector has been
// terminating for longer than otelCollectorHandoverTimeout.
func otelCollectorHandoverStuck(collector metav1.Object, now time.Time) bool {
	deleted := collector.GetDeletionTimestamp()
	return deleted != nil && !now.Before(deleted.Add(otelCollectorHandoverTimeout))
}

// isKServeOTelCollector reports whether KServe created the collector for the
// named InferenceService.
func isKServeOTelCollector(collector metav1.Object, isvcName string) bool {
	ref := metav1.GetControllerOfNoCopy(collector)
	return ref != nil &&
		ref.Kind == "InferenceService" &&
		ref.Name == isvcName &&
		schema.FromAPIVersionAndKind(ref.APIVersion, ref.Kind).Group == servingv1beta1.SchemeGroupVersion.Group
}

// isControlledBy reports whether the object's controller reference points at
// the owner with the given UID.
func isControlledBy(obj metav1.Object, ownerUID types.UID) bool {
	ref := metav1.GetControllerOfNoCopy(obj)
	return ref != nil && ref.UID == ownerUID
}

// kserveManagesOTelCollector mirrors the condition under which the KServe
// InferenceService controller creates and updates a collector: a predictor
// PodMetric with the opentelemetry backend that names at least one metric.
func kserveManagesOTelCollector(isvc *servingv1beta1.InferenceService) bool {
	if isvc == nil || isvc.Spec.Predictor.AutoScaling == nil {
		return false
	}
	for _, metric := range isvc.Spec.Predictor.AutoScaling.Metrics {
		if metric.Type != servingv1beta1.PodMetricSourceType || metric.PodMetric == nil {
			continue
		}
		if metric.PodMetric.Metric.Backend == servingv1beta1.OpenTelemetryBackend &&
			len(metric.PodMetric.Metric.MetricNames) > 0 {
			return true
		}
	}
	return false
}

// otelCollectorComponentHealth reports an autoscaled service whose pod metric
// triggers cannot receive data: the cluster does not serve the
// OpenTelemetryCollector API (ConfigValid=False), the collector could not be
// fetched, or it has stayed terminating past otelCollectorHandoverTimeout
// (Degraded). Returns a zero ComponentHealth otherwise.
func otelCollectorComponentHealth(
	service *aimv1alpha2.AIMService,
	served bool,
	observed controllerutils.FetchResult[*metav1.PartialObjectMetadata],
	now time.Time,
) controllerutils.ComponentHealth {
	if _, needed := otelCollectorMetricNames(service); !needed {
		return controllerutils.ComponentHealth{}
	}
	if !served {
		reason := aimv1alpha1.AIMServiceReasonOpenTelemetryCollectorUnavailable
		message := "Autoscaling on spec.autoScaling.metrics requires the OpenTelemetry Operator " +
			"(opentelemetry.io/v1beta1 OpenTelemetryCollector) to collect the pod metrics; " +
			"install it and restart AIM Engine"
		return controllerutils.ComponentHealth{
			Component:      ComponentOpenTelemetryCollector,
			State:          constants.AIMStatusFailed,
			Reason:         reason,
			Message:        message,
			DependencyType: controllerutils.DependencyTypeUpstream,
			Errors:         []error{controllerutils.NewInvalidSpecError(reason, message, nil)},
		}
	}
	if observed.Error != nil && !observed.IsNotFound() {
		return controllerutils.ComponentHealth{
			Component:      ComponentOpenTelemetryCollector,
			DependencyType: controllerutils.DependencyTypeDownstream,
			Errors:         []error{observed.Error},
		}
	}
	if collector := observed.Value; !observed.IsNotFound() && collector != nil && otelCollectorHandoverStuck(collector, now) {
		return controllerutils.ComponentHealth{
			Component: ComponentOpenTelemetryCollector,
			State:     constants.AIMStatusDegraded,
			Reason:    aimv1alpha1.AIMServiceReasonOpenTelemetryCollectorHandoverStuck,
			Message: fmt.Sprintf("OpenTelemetryCollector %s has been terminating since %s, so AIM Engine cannot "+
				"create the collector that injects the metrics sidecar into new predictor pods; "+
				"check that the OpenTelemetry Operator is running and removes its finalizer",
				collector.GetName(), collector.GetDeletionTimestamp().UTC().Format(time.RFC3339)),
			DependencyType: controllerutils.DependencyTypeDownstream,
		}
	}
	return controllerutils.ComponentHealth{}
}

// clearRecoveredOTelCollectorCondition removes the OpenTelemetryCollectorReady
// condition once otelCollectorComponentHealth no longer reports a problem.
// The health entry exists only while something is wrong, so without this a
// resolved problem would keep gating Ready.
func clearRecoveredOTelCollectorCondition(
	cm *controllerutils.ConditionManager,
	service *aimv1alpha2.AIMService,
	served bool,
	observed controllerutils.FetchResult[*metav1.PartialObjectMetadata],
	now time.Time,
) {
	if cm == nil {
		return
	}
	if otelCollectorComponentHealth(service, served, observed, now).Component == "" {
		cm.Delete(ComponentOpenTelemetryCollector + controllerutils.ComponentConditionSuffix)
	}
}

// detectOpenTelemetryCollectorAPI reports whether the API server serves the
// opentelemetry.io/v1beta1 OpenTelemetryCollector kind.
func detectOpenTelemetryCollectorAPI(client discovery.DiscoveryInterface) (bool, error) {
	return utils.ServesResources(client, openTelemetryCollectorGVK.GroupVersion().String(), openTelemetryCollectorResource)
}
