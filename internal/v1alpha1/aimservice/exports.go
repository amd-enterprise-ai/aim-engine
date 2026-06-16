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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

// FetchHPA fetches the KEDA-managed HorizontalPodAutoscaler associated with
// the given InferenceService. It is exported so other pipelines (v1alpha2
// profile-based AIMService) can reuse the same naming convention.
func FetchHPA(
	ctx context.Context,
	c client.Client,
	isvc *servingv1beta1.InferenceService,
) controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler] {
	return fetchHPA(ctx, c, isvc)
}

// FetchPredictorPods lists the predictor pods owned by the given
// InferenceService (matched via the KServe inference-service label). Exported
// so the v1alpha2 profile pipeline observes the same pod set the template
// pipeline uses to derive scale-to-zero and pod health.
func FetchPredictorPods(
	ctx context.Context,
	c client.Client,
	isvc *servingv1beta1.InferenceService,
) controllerutils.FetchResult[*corev1.PodList] {
	return controllerutils.FetchList(ctx, c, &corev1.PodList{},
		client.InNamespace(isvc.Namespace),
		client.MatchingLabels{constants.LabelKServeInferenceService: isvc.Name},
	)
}

// HPAComponentHealth returns the HPA component health for an autoscaled
// service, applying the shared scale-to-zero semantics (idle is Ready, the
// activation metric never gates readiness). podCount is the number of observed
// predictor pods and isvcReady reflects the InferenceService Ready condition;
// callers can derive both via FetchPredictorPods and InferenceServiceReady.
// Shared between the template and profile pipelines.
func HPAComponentHealth(
	service *aimv1alpha1.AIMService,
	hpa controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler],
	podCount int,
	isvcReady bool,
) controllerutils.ComponentHealth {
	return hpaComponentHealth(service, hpa, podCount, isvcReady)
}

// InferenceServicePodsComponentHealth returns the InferenceServicePods
// component health, treating an empty pod list under scale-to-zero as Ready
// (ScaledToZero) rather than a failure. ok is false when there is nothing to
// report (pods were never fetched). Shared between the template and profile
// pipelines.
func InferenceServicePodsComponentHealth(
	ctx context.Context,
	clientset kubernetes.Interface,
	service *aimv1alpha1.AIMService,
	hpa controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler],
	pods *controllerutils.FetchResult[*corev1.PodList],
) (controllerutils.ComponentHealth, bool) {
	return inferenceServicePodsHealth(ctx, clientset, service, hpa, pods)
}

// InferenceServiceReady reports whether the fetched InferenceService has
// Ready=True. Exported so the profile pipeline can contextualize HPA health
// identically to the template pipeline.
func InferenceServiceReady(
	isvc controllerutils.FetchResult[*servingv1beta1.InferenceService],
) bool {
	return inferenceServiceReady(isvc)
}

// FetchHTTPRoute fetches the HTTPRoute owned by the AIMService, if routing is
// enabled on the service or its merged runtime config. Exported for reuse by
// the profile-based pipeline.
func FetchHTTPRoute(
	ctx context.Context,
	c client.Client,
	service *aimv1alpha1.AIMService,
	runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon,
) controllerutils.FetchResult[*gatewayapiv1.HTTPRoute] {
	return fetchHTTPRoute(ctx, c, service, runtimeConfig)
}

// ComputeRuntimeStatus derives replica counts and the formatted Replicas
// string for an AIMService from its HPA (if present) or spec defaults. The
// v1alpha2 profile pipeline reuses this so the Replicas printcolumn behaves
// consistently across both pipelines.
//
// MinReplicas override (scale-to-zero):
// Kubernetes' HPA v2 API validates `spec.minReplicas >= 1` unless the
// alpha `HPAScaleToZero` feature gate (KEP-2021, alpha since v1.16) is
// enabled on both the API server and controller manager. KEDA's
// documented contract reflects this: when a ScaledObject has
// `minReplicaCount: 0` KEDA creates the HPA with `Spec.MinReplicas: 1`
// and drives the 0<->1 transition itself outside the HPA (the HPA
// reports `ScalingActive=False / Reason=ScalingDisabled` while the
// target is at 0). Reading status.runtime.MinReplicas straight from the
// HPA therefore lies to the user -- the spec said 0, KEDA happily
// idles to 0, and only the HPA's enforced API floor leaks through.
// We honour `service.Spec.MinReplicas` (the authoritative user intent)
// over `HPA.Spec.MinReplicas` here. When the feature gate eventually
// graduates and KEDA stops pinning HPA min to 1, both values will
// agree and this override becomes a no-op.
func ComputeRuntimeStatus(
	service *aimv1alpha1.AIMService,
	hpa controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler],
) *aimv1alpha1.AIMServiceRuntimeStatus {
	status := &aimv1alpha1.AIMServiceRuntimeStatus{}

	if hpa.OK() && hpa.Value != nil {
		h := hpa.Value
		switch {
		case service.Spec.MinReplicas != nil:
			status.MinReplicas = *service.Spec.MinReplicas
		case h.Spec.MinReplicas != nil:
			status.MinReplicas = *h.Spec.MinReplicas
		}
		status.MaxReplicas = h.Spec.MaxReplicas
		status.CurrentReplicas = h.Status.CurrentReplicas
		// Under scale-to-zero an HPA at the idle floor reports
		// DesiredReplicas=0; surfacing the spec min (also 0) preserves
		// that. The legacy fallback ("DesiredReplicas==0 -> MinReplicas")
		// only matters before the HPA has emitted its first scrape on
		// non-scale-to-zero services, so we keep it gated on min>0.
		switch {
		case h.Status.DesiredReplicas != 0:
			status.DesiredReplicas = h.Status.DesiredReplicas
		case status.MinReplicas == 0:
			status.DesiredReplicas = 0
		default:
			status.DesiredReplicas = status.MinReplicas
		}
	} else {
		var minReplicas int32 = 1
		if service.Spec.MinReplicas != nil {
			minReplicas = *service.Spec.MinReplicas
		} else if service.Spec.Replicas != nil {
			minReplicas = *service.Spec.Replicas
		}

		maxReplicas := minReplicas
		if service.Spec.MaxReplicas != nil {
			maxReplicas = *service.Spec.MaxReplicas
		}

		status.MinReplicas = minReplicas
		status.MaxReplicas = maxReplicas
		status.DesiredReplicas = minReplicas
		if service.Spec.AutoScaling != nil {
			status.CurrentReplicas = 0
		} else {
			status.CurrentReplicas = minReplicas
		}
	}

	if status.MinReplicas == status.MaxReplicas {
		status.Replicas = fmt.Sprintf("%d", status.CurrentReplicas)
	} else {
		status.Replicas = fmt.Sprintf("%d/%d (%d-%d)",
			status.CurrentReplicas, status.DesiredReplicas,
			status.MinReplicas, status.MaxReplicas)
	}

	return status
}

// ConfigureReplicasAndAutoscaling writes the replica count, autoscaling
// annotations, and KEDA metrics onto the InferenceService based on the
// AIMService spec. Exported so the v1alpha2 profile-based pipeline can share
// the exact same replica/autoscaling semantics as the v1alpha1 template
// pipeline (Replicas → fixed, MinReplicas/MaxReplicas/AutoScaling → KEDA
// autoscaling, unset → single replica).
func ConfigureReplicasAndAutoscaling(
	isvc *servingv1beta1.InferenceService,
	service *aimv1alpha1.AIMService,
) {
	configureReplicasAndAutoscaling(isvc, service)
}

// PlanScaledObject returns the controller-owned KEDA ScaledObject for the
// predictor Deployment when autoscaling is requested. Exported for the
// v1alpha2 profile pipeline so both pipelines share the same trigger shape
// and memory-aware cooldown.
func PlanScaledObject(
	ctx context.Context,
	service *aimv1alpha1.AIMService,
	effectiveResources *corev1.ResourceRequirements,
) client.Object {
	return planScaledObject(ctx, service, effectiveResources)
}

// HTTPRouteComponentHealth translates an HTTPRoute fetch result into a
// ComponentHealth entry, taking the routing toggle and gateway configuration
// into account. Shared between the template and profile pipelines so routing
// health is reported identically.
func HTTPRouteComponentHealth(
	service *aimv1alpha1.AIMService,
	runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon,
	httpRoute controllerutils.FetchResult[*gatewayapiv1.HTTPRoute],
) controllerutils.ComponentHealth {
	health := controllerutils.ComponentHealth{
		Component:      "HTTPRoute",
		DependencyType: controllerutils.DependencyTypeDownstream,
	}

	if !isRoutingEnabled(service, runtimeConfig) {
		return controllerutils.ComponentHealth{}
	}

	if resolveGatewayRef(service, runtimeConfig) == nil {
		health.State = constants.AIMStatusFailed
		health.Reason = "GatewayNotConfigured"
		health.Message = "Routing is enabled but no gatewayRef is configured in service or runtime config"
		health.Errors = []error{
			controllerutils.NewInvalidSpecError(
				"GatewayNotConfigured",
				"Routing is enabled but no gatewayRef is configured. Set spec.routing.gatewayRef on the service or runtimeConfig.routing.gatewayRef on the runtime config.",
				nil,
			),
		}
		return health
	}

	if httpRoute.Error != nil {
		if httpRoute.IsNotFound() {
			health.State = constants.AIMStatusProgressing
			health.Reason = "HTTPRouteCreating"
			health.Message = "HTTPRoute is being created"
			return health
		}
		health.State = constants.AIMStatusFailed
		health.Reason = "HTTPRouteFetchError"
		health.Message = httpRoute.Error.Error()
		health.Errors = []error{httpRoute.Error}
		return health
	}

	return httpRoute.ToComponentHealth("HTTPRoute", controllerutils.GetHTTPRouteHealth)
}

// ComponentScaleToZeroConfig is the ComponentHealth name (and condition-type
// stem, ScaleToZeroConfigReady) for the scale-from-zero routing-prerequisite
// check. Exported so both pipelines and their tests reference one source of
// truth.
const ComponentScaleToZeroConfig = "ScaleToZeroConfig"

// ComponentAutoscalingConfig is the ComponentHealth name (and condition-type
// stem, AutoscalingConfigReady) for the autoscaling-trigger prerequisite
// check. Exported so both pipelines and their tests reference one source of
// truth.
const ComponentAutoscalingConfig = "AutoscalingConfig"

// AutoscalingTriggerComponentHealth enforces the invariant that whenever the
// controller stamps autoscalerClass=external (i.e. autoscaling is configured
// via minReplicas/maxReplicas/autoScaling) a KEDA ScaledObject with at least
// one trigger is also authored. planScaledObject only emits a ScaledObject
// when a trigger resolves -- the scale-from-zero gateway activation trigger
// (minReplicas=0) or a user-defined autoScaling.metrics entry. Configuring
// autoscaling with neither leaves the predictor under external scaling control
// with nothing to drive it: the declared replica bounds are never enforced and
// the service strands (e.g. maxReplicas set but the deployment never scales).
// That combination is surfaced as ConfigValid=False (blocking apply) instead
// of silently mis-scaling.
//
// Returns a zero ComponentHealth (Component == "") when the configuration is
// valid. Shared between the template (v1alpha1) and profile (v1alpha2)
// pipelines so the validation is identical regardless of which pipeline owns
// the service.
func AutoscalingTriggerComponentHealth(
	service *aimv1alpha1.AIMService,
) controllerutils.ComponentHealth {
	hasAutoscaling := service.Spec.AutoScaling != nil ||
		service.Spec.MinReplicas != nil ||
		service.Spec.MaxReplicas != nil
	if !hasAutoscaling {
		return controllerutils.ComponentHealth{}
	}
	// Scale-from-zero always contributes the gateway activation trigger, and a
	// user metric contributes its own; either makes the ScaledObject valid.
	if isScaleToZero(service) || len(collectUserMetrics(service)) > 0 {
		return controllerutils.ComponentHealth{}
	}

	message := "Autoscaling is configured (minReplicas/maxReplicas/autoScaling) but no scaling " +
		"trigger resolves, so no KEDA ScaledObject is created and the declared replica bounds are " +
		"never enforced. Add a scaling metric (spec.autoScaling.metrics, e.g. a vLLM PodMetric), set " +
		"minReplicas=0 to enable scale-from-zero, or use spec.replicas for a fixed replica count."

	return controllerutils.ComponentHealth{
		Component:      ComponentAutoscalingConfig,
		State:          constants.AIMStatusFailed,
		Reason:         aimv1alpha1.AIMServiceReasonAutoscalingRequiresMetrics,
		Message:        message,
		DependencyType: controllerutils.DependencyTypeUpstream,
		Errors: []error{
			controllerutils.NewInvalidSpecError(
				aimv1alpha1.AIMServiceReasonAutoscalingRequiresMetrics,
				message,
				nil,
			),
		},
	}
}

// ScaleToZeroRoutingComponentHealth validates the scale-from-zero prerequisite
// that routing be enabled. The 0->1 activation trigger scrapes the gateway's
// Envoy counter (envoy_cluster_external_upstream_rq_completed), whose series
// only exists once traffic flows through the gateway via an HTTPRoute. With
// routing disabled a minReplicas=0 service idles to zero and can never wake, so
// the combination is surfaced as ConfigValid=False (blocking apply) instead of
// silently sleeping forever.
//
// Returns a zero ComponentHealth (Component == "") when the configuration is
// valid, so callers can skip appending it. Shared between the template
// (v1alpha1) and profile (v1alpha2) pipelines so the validation is identical
// regardless of which pipeline owns the service.
func ScaleToZeroRoutingComponentHealth(
	service *aimv1alpha1.AIMService,
	runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon,
) controllerutils.ComponentHealth {
	if !isScaleToZero(service) || isRoutingEnabled(service, runtimeConfig) {
		return controllerutils.ComponentHealth{}
	}

	message := "Scale-from-zero (minReplicas=0) requires routing to be enabled: " +
		"the 0->1 activation trigger queries gateway-side Envoy metrics that only " +
		"exist once an HTTPRoute is wired up, so with routing disabled the service " +
		"can never wake from zero. Enable routing (spec.routing.enabled or " +
		"runtimeConfig.routing.enabled) or set minReplicas>=1."

	return controllerutils.ComponentHealth{
		Component:      ComponentScaleToZeroConfig,
		State:          constants.AIMStatusFailed,
		Reason:         aimv1alpha1.AIMServiceReasonRoutingRequired,
		Message:        message,
		DependencyType: controllerutils.DependencyTypeUpstream,
		Errors: []error{
			controllerutils.NewInvalidSpecError(
				aimv1alpha1.AIMServiceReasonRoutingRequired,
				message,
				nil,
			),
		},
	}
}
