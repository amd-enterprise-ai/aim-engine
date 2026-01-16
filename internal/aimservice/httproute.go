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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

// GenerateHTTPRouteName creates a deterministic name for the HTTPRoute.
func GenerateHTTPRouteName(serviceName, namespace string) (string, error) {
	return utils.GenerateDerivedName([]string{serviceName}, utils.WithHashSource(namespace))
}

// fetchHTTPRoute fetches the existing HTTPRoute for the service.
func fetchHTTPRoute(
	ctx context.Context,
	c client.Client,
	service *aimv1alpha1.AIMService,
	runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon,
) controllerutils.FetchResult[*gatewayapiv1.HTTPRoute] {
	// Check if routing is enabled
	if !isRoutingEnabled(service, runtimeConfig) {
		return controllerutils.FetchResult[*gatewayapiv1.HTTPRoute]{}
	}

	routeName, err := GenerateHTTPRouteName(service.Name, service.Namespace)
	if err != nil {
		return controllerutils.FetchResult[*gatewayapiv1.HTTPRoute]{Error: err}
	}

	return controllerutils.Fetch(ctx, c, client.ObjectKey{
		Namespace: service.Namespace,
		Name:      routeName,
	}, &gatewayapiv1.HTTPRoute{})
}

// planHTTPRoute creates the HTTPRoute if routing is enabled.
func planHTTPRoute(
	ctx context.Context,
	service *aimv1alpha1.AIMService,
	obs ServiceObservation,
) client.Object {
	logger := log.FromContext(ctx).WithName("planHTTPRoute")
	runtimeConfig := obs.mergedRuntimeConfig.Value

	logger.V(1).Info("checking routing",
		"runtimeConfigNil", runtimeConfig == nil,
		"serviceRoutingNil", service.Spec.Routing == nil,
	)

	if !isRoutingEnabled(service, runtimeConfig) {
		logger.V(1).Info("routing not enabled")
		return nil
	}

	// Need gateway ref to create route
	gatewayRef := resolveGatewayRef(service, runtimeConfig)
	if gatewayRef == nil {
		logger.V(1).Info("gateway ref not configured")
		return nil
	}

	logger.V(1).Info("creating HTTPRoute", "gatewayRef", gatewayRef.Name)
	return buildHTTPRoute(service, gatewayRef, runtimeConfig)
}

// resolveGatewayRef gets the gateway reference from service or runtime config.
func resolveGatewayRef(service *aimv1alpha1.AIMService, runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon) *gatewayapiv1.ParentReference {
	// Service-level override
	if service.Spec.Routing != nil && service.Spec.Routing.GatewayRef != nil {
		return service.Spec.Routing.GatewayRef
	}

	// Fall back to runtime config
	if runtimeConfig != nil && runtimeConfig.Routing != nil && runtimeConfig.Routing.GatewayRef != nil {
		return runtimeConfig.Routing.GatewayRef
	}

	return nil
}

// buildHTTPRoute constructs an HTTPRoute for the service.
func buildHTTPRoute(
	service *aimv1alpha1.AIMService,
	gatewayRef *gatewayapiv1.ParentReference,
	runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon,
) *gatewayapiv1.HTTPRoute {
	routeName, _ := GenerateHTTPRouteName(service.Name, service.Namespace)

	// Build labels
	serviceLabelValue, _ := utils.SanitizeLabelValue(service.Name)
	labels := map[string]string{
		constants.LabelK8sComponent: constants.ComponentRouting,
		constants.LabelK8sManagedBy: constants.LabelValueManagedBy,
		constants.LabelService:      serviceLabelValue,
	}

	// Build annotations
	annotations := mergeRouteAnnotations(runtimeConfig)

	// Resolve path using JSONPath template
	path, err := ResolveServiceRoutePath(service, runtimeConfig)
	if err != nil {
		// Use a fallback path based on namespace/UID if template fails
		path = DefaultRoutePath(service)
	}

	// Build parent reference
	parentRefs := []gatewayapiv1.ParentReference{*gatewayRef}

	// Build path match
	pathMatchType := gatewayapiv1.PathMatchPathPrefix
	pathMatch := gatewayapiv1.HTTPPathMatch{
		Type:  &pathMatchType,
		Value: ptr.To(path),
	}

	// Build backend reference - points to KServe predictor service
	isvcName, _ := GenerateInferenceServiceName(service.Name, service.Namespace)
	predictorServiceName := isvcName + constants.PredictorServiceSuffix
	backendRef := gatewayapiv1.HTTPBackendRef{
		BackendRef: gatewayapiv1.BackendRef{
			BackendObjectReference: gatewayapiv1.BackendObjectReference{
				Kind:      ptr.To(gatewayapiv1.Kind("Service")),
				Name:      gatewayapiv1.ObjectName(predictorServiceName),
				Namespace: ptr.To(gatewayapiv1.Namespace(service.Namespace)),
				Port:      ptr.To(gatewayapiv1.PortNumber(constants.DefaultGatewayPort)),
			},
		},
	}

	// Build URL rewrite filter to strip the path prefix
	// The backend expects requests at /v1/... but we match on a prefix like /namespace/service/...
	rewriteType := gatewayapiv1.PrefixMatchHTTPPathModifier
	urlRewriteFilter := gatewayapiv1.HTTPRouteFilter{
		Type: gatewayapiv1.HTTPRouteFilterURLRewrite,
		URLRewrite: &gatewayapiv1.HTTPURLRewriteFilter{
			Path: &gatewayapiv1.HTTPPathModifier{
				Type:               rewriteType,
				ReplacePrefixMatch: ptr.To("/"),
			},
		},
	}

	// Build rule
	rule := gatewayapiv1.HTTPRouteRule{
		Matches: []gatewayapiv1.HTTPRouteMatch{
			{
				Path: &pathMatch,
			},
		},
		Filters:     []gatewayapiv1.HTTPRouteFilter{urlRewriteFilter},
		BackendRefs: []gatewayapiv1.HTTPBackendRef{backendRef},
	}

	// Add timeout if configured
	timeout := resolveRequestTimeout(service, runtimeConfig)
	if timeout != nil {
		rule.Timeouts = &gatewayapiv1.HTTPRouteTimeouts{
			Request: ptr.To(gatewayapiv1.Duration(timeout.Duration.String())),
		}
	}

	route := &gatewayapiv1.HTTPRoute{
		TypeMeta: metav1.TypeMeta{
			APIVersion: gatewayapiv1.GroupVersion.String(),
			Kind:       "HTTPRoute",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        routeName,
			Namespace:   service.Namespace,
			Labels:      labels,
			Annotations: annotations,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         service.APIVersion,
					Kind:               service.Kind,
					Name:               service.Name,
					UID:                service.UID,
					Controller:         ptr.To(true),
					BlockOwnerDeletion: ptr.To(true),
				},
			},
		},
		Spec: gatewayapiv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayapiv1.CommonRouteSpec{
				ParentRefs: parentRefs,
			},
			Rules: []gatewayapiv1.HTTPRouteRule{rule},
		},
	}

	return route
}

// isRoutingEnabled checks if routing is enabled for the service.
func isRoutingEnabled(service *aimv1alpha1.AIMService, runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon) bool {
	// Service-level routing override takes precedence
	if service.Spec.Routing != nil && service.Spec.Routing.Enabled != nil {
		return *service.Spec.Routing.Enabled
	}

	// Fall back to runtime config
	if runtimeConfig != nil && runtimeConfig.Routing != nil && runtimeConfig.Routing.Enabled != nil {
		return *runtimeConfig.Routing.Enabled
	}

	return false
}

// mergeRouteAnnotations merges annotations from runtime config.
func mergeRouteAnnotations(runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon) map[string]string {
	annotations := make(map[string]string)

	// Start with runtime config annotations
	if runtimeConfig != nil && runtimeConfig.Routing != nil {
		for k, v := range runtimeConfig.Routing.Annotations {
			annotations[k] = v
		}
	}

	return annotations
}

// resolveRequestTimeout gets the request timeout to use.
func resolveRequestTimeout(service *aimv1alpha1.AIMService, runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon) *metav1.Duration {
	// Service-level override
	if service.Spec.Routing != nil && service.Spec.Routing.RequestTimeout != nil {
		return service.Spec.Routing.RequestTimeout
	}

	// Runtime config default
	if runtimeConfig != nil && runtimeConfig.Routing != nil && runtimeConfig.Routing.RequestTimeout != nil {
		return runtimeConfig.Routing.RequestTimeout
	}

	return nil
}
