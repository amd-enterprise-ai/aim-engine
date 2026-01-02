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
	"fmt"
	"strings"
	"text/template"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

const (
	// DefaultPathTemplate is used when no path template is specified
	DefaultPathTemplate = "/{{.Namespace}}/{{.Name}}"
	// MaxPathLength is the maximum length for rendered paths
	MaxPathLength = 200
)

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
		"app.kubernetes.io/name":       "aim-http-route",
		"app.kubernetes.io/component":  "routing",
		"app.kubernetes.io/managed-by": constants.LabelValueManagedBy,
		constants.LabelService:         serviceLabelValue,
	}

	// Build annotations
	annotations := mergeRouteAnnotations(runtimeConfig)

	// Resolve path template
	pathTemplate := resolvePathTemplate(service, runtimeConfig)
	path, err := renderPathTemplate(pathTemplate, service)
	if err != nil {
		// Use a fallback path based on name if template fails
		path = "/" + service.Namespace + "/" + service.Name
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
	predictorServiceName := isvcName + "-predictor"
	backendRef := gatewayapiv1.HTTPBackendRef{
		BackendRef: gatewayapiv1.BackendRef{
			BackendObjectReference: gatewayapiv1.BackendObjectReference{
				Kind:      ptr.To(gatewayapiv1.Kind("Service")),
				Name:      gatewayapiv1.ObjectName(predictorServiceName),
				Namespace: ptr.To(gatewayapiv1.Namespace(service.Namespace)),
				Port:      ptr.To(gatewayapiv1.PortNumber(80)),
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

// resolvePathTemplate gets the path template to use.
func resolvePathTemplate(service *aimv1alpha1.AIMService, runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon) string {
	// Service-level override
	if service.Spec.Routing != nil && service.Spec.Routing.PathTemplate != nil {
		return *service.Spec.Routing.PathTemplate
	}

	// Runtime config default
	if runtimeConfig != nil && runtimeConfig.Routing != nil && runtimeConfig.Routing.PathTemplate != nil {
		return *runtimeConfig.Routing.PathTemplate
	}

	return DefaultPathTemplate
}

// renderPathTemplate renders the path template with service data.
func renderPathTemplate(pathTemplate string, service *aimv1alpha1.AIMService) (string, error) {
	// Create template data
	data := struct {
		Name      string
		Namespace string
		UID       string
		Labels    map[string]string
	}{
		Name:      service.Name,
		Namespace: service.Namespace,
		UID:       string(service.UID),
		Labels:    service.Labels,
	}

	// Parse and execute template
	tmpl, err := template.New("path").Parse(pathTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to parse path template: %w", err)
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute path template: %w", err)
	}

	path := buf.String()

	// Validate path
	if len(path) > MaxPathLength {
		return "", fmt.Errorf("rendered path exceeds maximum length of %d characters", MaxPathLength)
	}

	// Ensure path starts with /
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	return path, nil
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
