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
	"slices"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
	"knative.dev/pkg/kmeta"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	v1alpha1svc "github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/aimservice"
)

// An autoscaled LLMInferenceService is scaled by AIM Engine, not KServe: the
// LLMInferenceService leaves spec.replicas unset (KServe then preserves the
// Deployment's replica count), the shared KEDA ScaledObject targets the
// <name>-kserve Deployment, and AIM Engine creates the per-service
// OpenTelemetry collector sidecar that KServe creates for an autoscaled
// InferenceService. The collector mirrors KServe's: it scrapes the pod's
// metrics port, labels each point with namespace/deployment/pod, and exports
// over OTLP to the keda-otel-add-on that the ScaledObject triggers query.

var openTelemetryCollectorGVK = schema.GroupVersionKind{
	Group:   "opentelemetry.io",
	Version: "v1beta1",
	Kind:    "OpenTelemetryCollector",
}

const (
	otelCollectorScrapeInterval = "5s"
	otelProcessorResourceDetect = "resourcedetection/env"
	otelProcessorTransform      = "transform"
	otelProcessorFilterMetrics  = "filter/metrics"
)

// llmDeploymentName is the Deployment KServe runs an LLMInferenceService's
// main workload in.
func llmDeploymentName(llmServiceName string) string {
	return kmeta.ChildName(llmServiceName, "-kserve")
}

// otelReceiverEndpoint is the OTLP endpoint the collector exports to: the
// first user metric's serverAddress, else the keda-otel-add-on default.
func otelReceiverEndpoint(service *aimv1alpha1.AIMService) string {
	if service.Spec.AutoScaling != nil {
		for _, metric := range service.Spec.AutoScaling.Metrics {
			if metric.PodMetric != nil && metric.PodMetric.Metric != nil && metric.PodMetric.Metric.ServerAddress != "" {
				return metric.PodMetric.Metric.ServerAddress
			}
		}
	}
	return constants.DefaultKEDAOTelReceiverEndpoint
}

// otelMetricNames returns the declared metric names the collector forwards.
// Empty forwards every scraped metric.
func otelMetricNames(service *aimv1alpha1.AIMService) []string {
	var names []string
	if service.Spec.AutoScaling == nil {
		return names
	}
	for _, metric := range service.Spec.AutoScaling.Metrics {
		if metric.PodMetric == nil || metric.PodMetric.Metric == nil {
			continue
		}
		for _, name := range metric.PodMetric.Metric.MetricNames {
			if !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}
	return names
}

// buildLLMOTelCollector builds the sidecar-mode OpenTelemetryCollector the
// autoscaled LLMInferenceService workload pods inject (named after the
// workload Deployment, like KServe's predictor collector).
func buildLLMOTelCollector(service *aimv1alpha1.AIMService, deploymentName string) *unstructured.Unstructured {
	processors := map[string]interface{}{
		otelProcessorResourceDetect: map[string]interface{}{
			"detectors": []interface{}{"env"},
			"timeout":   "2s",
			"override":  false,
		},
		otelProcessorTransform: map[string]interface{}{
			"metric_statements": []interface{}{map[string]interface{}{
				"context": "datapoint",
				"statements": []interface{}{
					`set(attributes["namespace"], resource.attributes["k8s.namespace.name"])`,
					`set(attributes["deployment"], resource.attributes["k8s.deployment.name"])`,
					`set(attributes["pod"], resource.attributes["k8s.pod.name"])`,
				},
			}},
		},
	}
	pipelineProcessors := []interface{}{otelProcessorResourceDetect, otelProcessorTransform}
	if names := otelMetricNames(service); len(names) > 0 {
		metricNames := make([]interface{}, 0, len(names))
		for _, name := range names {
			metricNames = append(metricNames, name)
		}
		processors[otelProcessorFilterMetrics] = map[string]interface{}{
			"metrics": map[string]interface{}{
				"include": map[string]interface{}{
					"match_type":   "strict",
					"metric_names": metricNames,
				},
			},
		}
		pipelineProcessors = append(pipelineProcessors, otelProcessorFilterMetrics)
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
	collector.SetOwnerReferences([]metav1.OwnerReference{{
		APIVersion:         service.APIVersion,
		Kind:               service.Kind,
		Name:               service.Name,
		UID:                service.UID,
		Controller:         ptr.To(true),
		BlockOwnerDeletion: ptr.To(true),
	}})
	collector.Object["spec"] = map[string]interface{}{
		"mode": "sidecar",
		// KServe's default sidecar collector resources.
		"resources": map[string]interface{}{
			"requests": map[string]interface{}{"cpu": "200m", "memory": "512Mi"},
			"limits":   map[string]interface{}{"cpu": "1", "memory": "2Gi"},
		},
		"config": map[string]interface{}{
			"receivers": map[string]interface{}{
				"prometheus": map[string]interface{}{
					"config": map[string]interface{}{
						"scrape_configs": []interface{}{map[string]interface{}{
							"job_name":        "otel-collector",
							"scrape_interval": otelCollectorScrapeInterval,
							"static_configs": []interface{}{map[string]interface{}{
								"targets": []interface{}{"localhost:" + constants.DefaultPrometheusPort},
							}},
						}},
					},
				},
			},
			"processors": processors,
			"exporters": map[string]interface{}{
				"otlp": map[string]interface{}{
					"endpoint":    otelReceiverEndpoint(service),
					"compression": "none",
					"tls":         map[string]interface{}{"insecure": true},
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

// planAutoscaling returns the autoscaling resources for the serving workload:
// the KEDA ScaledObject for the InferenceService predictor, or the
// ScaledObject and OpenTelemetry collector for the LLMInferenceService
// Deployment. Empty when the service does not autoscale.
func planAutoscaling(
	ctx context.Context,
	service *aimv1alpha1.AIMService,
	obs ServiceObservation,
	effectiveResources *corev1.ResourceRequirements,
) []client.Object {
	if !obs.servesLLM {
		if so := v1alpha1svc.PlanScaledObject(ctx, service, effectiveResources, obs.mergedRuntimeConfig.Value); so != nil {
			return []client.Object{so}
		}
		return nil
	}
	if !v1alpha1svc.HasAutoscaling(service) || obs.isvcName == "" {
		return nil
	}
	deploymentName := llmDeploymentName(obs.isvcName)
	objects := []client.Object{buildLLMOTelCollector(service, deploymentName)}
	if so := v1alpha1svc.PlanScaledObjectForDeployment(
		ctx, service, effectiveResources, obs.mergedRuntimeConfig.Value, deploymentName,
	); so != nil {
		objects = append(objects, so)
	}
	return objects
}

// workloadPodCount is the number of observed serving pods: LLMInferenceService
// workload pods or InferenceService predictor pods.
func (obs ServiceObservation) workloadPodCount() int {
	if obs.servesLLM {
		return podItemCount(obs.llmInferenceServicePods)
	}
	return podItemCount(obs.inferenceServicePods)
}

// workloadReady reports whether the serving workload reports Ready.
func (obs ServiceObservation) workloadReady() bool {
	if obs.servesLLM {
		return llmInferenceServiceReady(obs.llmInferenceService)
	}
	return v1alpha1svc.InferenceServiceReady(obs.inferenceService)
}
