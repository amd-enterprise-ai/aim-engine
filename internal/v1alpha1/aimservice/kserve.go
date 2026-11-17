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

	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

// GenerateInferenceServiceName creates a deterministic name for the InferenceService.
// KServe creates hostnames in format {isvc-name}-predictor-{namespace}, which must be ≤ 63 chars.
// We calculate the maximum allowed InferenceService name length based on the namespace length
// to ensure the final hostname stays within DNS limits.
func GenerateInferenceServiceName(serviceName, namespace string) (string, error) {
	// KServe hostname format: {isvc-name}-predictor-{namespace}.{domain}
	// The first DNS label ({isvc-name}-predictor-{namespace}) must be ≤ 63 chars
	// "-predictor-" is 11 characters
	predictorSuffix := len("-predictor-") + len(namespace)
	maxIsvcNameLength := utils.MaxKubernetesNameLength - predictorSuffix

	// Ensure minimum length for the name
	if maxIsvcNameLength < 10 {
		return "", fmt.Errorf("namespace %q is too long (%d chars); InferenceService hostname would exceed 63 characters", namespace, len(namespace))
	}

	// Hash on (namespace, serviceName) so the suffix stays unique per AIMService
	// even when the visible name part is truncated to fit a long namespace.
	return utils.GenerateDerivedName([]string{serviceName},
		utils.WithHashSource(namespace, serviceName),
		utils.WithMaxLength(maxIsvcNameLength))
}

// fetchHPA fetches the HorizontalPodAutoscaler for the InferenceService.
// KEDA creates HPAs with the naming pattern: keda-hpa-{isvc-name}-predictor
func fetchHPA(
	ctx context.Context,
	c client.Client,
	isvc *servingv1beta1.InferenceService,
) controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler] {
	// KEDA HPA naming pattern: keda-hpa-{isvc-name}-predictor
	hpaName := "keda-hpa-" + isvc.Name + constants.PredictorServiceSuffix

	return controllerutils.Fetch(ctx, c, client.ObjectKey{
		Namespace: isvc.Namespace,
		Name:      hpaName,
	}, &autoscalingv2.HorizontalPodAutoscaler{})
}

// configureReplicasAndAutoscaling sets up replica counts and autoscaling configuration.
func configureReplicasAndAutoscaling(isvc *servingv1beta1.InferenceService, service *aimv1alpha2.AIMService) {
	// Check if autoscaling is configured (new fields take precedence)
	hasAutoscaling := service.Spec.AutoScaling != nil ||
		service.Spec.MinReplicas != nil ||
		service.Spec.MaxReplicas != nil

	if hasAutoscaling {
		// autoscalerClass=external -- KServe writes no ScaledObject; the
		// controller-owned one in scaledobject.go is the sole author.
		injectAutoscalingAnnotations(isvc)

		// resolveReplicaBounds is the single source of truth for min/max
		// defaulting; the ScaledObject path consumes the same helper.
		minReplicas, maxReplicas := resolveReplicaBounds(service)
		isvc.Spec.Predictor.MinReplicas = ptr.To(minReplicas)
		isvc.Spec.Predictor.MaxReplicas = maxReplicas

		// Under autoscalerClass=external, KServe still uses
		// Spec.Predictor.AutoScaling.Metrics to size the per-ISVC
		// OpenTelemetryCollector CR that drives in-pod sidecar injection.
		// The duplication with planScaledObject's triggers is intentional:
		// KServe reads this for the sidecar; the ScaledObject is the sole
		// authority on trigger shape.
		if service.Spec.AutoScaling != nil {
			isvc.Spec.Predictor.AutoScaling = convertToKServeAutoScaling(service.Spec.AutoScaling)
		}
	} else if service.Spec.Replicas != nil {
		// Legacy: fixed replica count, disable HPA
		disableHPA(isvc)
		isvc.Spec.Predictor.MinReplicas = service.Spec.Replicas
		isvc.Spec.Predictor.MaxReplicas = *service.Spec.Replicas
	} else {
		// Default: 1 replica, disable HPA
		disableHPA(isvc)
		one := int32(1)
		isvc.Spec.Predictor.MinReplicas = &one
		isvc.Spec.Predictor.MaxReplicas = 1
	}
}

// disableHPA sets autoscaler to none to prevent HPA creation.
// Always overwrites the annotation so that switching from autoscaling (keda)
// to fixed replicas correctly updates the autoscaler class.
func disableHPA(isvc *servingv1beta1.InferenceService) {
	if isvc.Annotations == nil {
		isvc.Annotations = make(map[string]string)
	}
	isvc.Annotations[constants.AnnotationKServeAutoscalerClass] = constants.AutoscalerClassNone
}

// injectAutoscalingAnnotations adds the annotations the autoscaled predictor
// needs from KServe. autoscalerClass=external keeps KServe's KEDA reconciler
// out of the way so the controller can own the ScaledObject directly. OTel
// sidecar and Prometheus annotations are also injected for warm-state
// metrics. Always overwrites so transitions to/from fixed-replica
// mode update the values rather than leaving stale state.
func injectAutoscalingAnnotations(isvc *servingv1beta1.InferenceService) {
	if isvc.Annotations == nil {
		isvc.Annotations = make(map[string]string)
	}

	isvc.Annotations[constants.AnnotationKServeAutoscalerClass] = constants.AutoscalerClassExternal

	predictorName := isvc.Name + constants.PredictorServiceSuffix
	isvc.Annotations[constants.AnnotationOTelSidecarInject] = predictorName

	if _, exists := isvc.Annotations[constants.AnnotationPrometheusPort]; !exists {
		isvc.Annotations[constants.AnnotationPrometheusPort] = constants.DefaultPrometheusPort
	}
}

// convertToKServeAutoScaling converts AIM autoscaling config to a KServe
// AutoScalingSpec. Consumed by KServe only to configure the
// OpenTelemetryCollector for in-pod sidecar injection.
func convertToKServeAutoScaling(aimAutoScaling *aimv1alpha1.AIMServiceAutoScaling) *servingv1beta1.AutoScalingSpec {
	if aimAutoScaling == nil {
		return nil
	}

	kserveAutoScaling := &servingv1beta1.AutoScalingSpec{}

	if len(aimAutoScaling.Metrics) > 0 {
		kserveAutoScaling.Metrics = make([]servingv1beta1.MetricsSpec, len(aimAutoScaling.Metrics))
		for i, metric := range aimAutoScaling.Metrics {
			kserveMetric := servingv1beta1.MetricsSpec{
				Type: servingv1beta1.MetricSourceType(metric.Type),
			}

			if metric.Type == "PodMetric" && metric.PodMetric != nil {
				kserveMetric.PodMetric = &servingv1beta1.PodMetricSource{}

				if metric.PodMetric.Metric != nil {
					kserveMetric.PodMetric.Metric = servingv1beta1.PodMetrics{
						Backend:           servingv1beta1.PodsMetricsBackend(metric.PodMetric.Metric.Backend),
						ServerAddress:     metric.PodMetric.Metric.ServerAddress,
						MetricNames:       metric.PodMetric.Metric.MetricNames,
						Query:             metric.PodMetric.Metric.Query,
						OperationOverTime: metric.PodMetric.Metric.OperationOverTime,
					}
				}

				if metric.PodMetric.Target != nil {
					kserveMetric.PodMetric.Target = servingv1beta1.MetricTarget{
						Type: servingv1beta1.MetricTargetType(metric.PodMetric.Target.Type),
					}

					if metric.PodMetric.Target.Value != "" {
						kserveMetric.PodMetric.Target.Value = servingv1beta1.NewMetricQuantity(metric.PodMetric.Target.Value)
					}
					if metric.PodMetric.Target.AverageValue != "" {
						kserveMetric.PodMetric.Target.AverageValue = servingv1beta1.NewMetricQuantity(metric.PodMetric.Target.AverageValue)
					}
					if metric.PodMetric.Target.AverageUtilization != nil {
						kserveMetric.PodMetric.Target.AverageUtilization = metric.PodMetric.Target.AverageUtilization
					}
				}
			}

			kserveAutoScaling.Metrics[i] = kserveMetric
		}
	}

	return kserveAutoScaling
}
