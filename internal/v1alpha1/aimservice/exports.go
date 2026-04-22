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
func ComputeRuntimeStatus(
	service *aimv1alpha1.AIMService,
	hpa controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler],
) *aimv1alpha1.AIMServiceRuntimeStatus {
	status := &aimv1alpha1.AIMServiceRuntimeStatus{}

	if hpa.OK() && hpa.Value != nil {
		h := hpa.Value
		if h.Spec.MinReplicas != nil {
			status.MinReplicas = *h.Spec.MinReplicas
		}
		status.MaxReplicas = h.Spec.MaxReplicas
		status.CurrentReplicas = h.Status.CurrentReplicas
		if h.Status.DesiredReplicas == 0 {
			status.DesiredReplicas = status.MinReplicas
		} else {
			status.DesiredReplicas = h.Status.DesiredReplicas
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
