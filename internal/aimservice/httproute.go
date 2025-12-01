/*
MIT License

Copyright (c) 2025 Advanced Micro Devices, Inc.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package aimservice

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"strings"

	kserveconstants "github.com/kserve/kserve/pkg/constants"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/jsonpath"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

const (
	// MaxRoutePathLength is the maximum allowed length for a route path
	MaxRoutePathLength = 200
)

var (
	routeTemplatePattern      = regexp.MustCompile(`\{([^{}]+)\}`)
	labelAccessPattern        = regexp.MustCompile(`^\.metadata\.labels\[['"]([^'"]+)['"]\]$`)
	annotationAccessPattern   = regexp.MustCompile(`^\.metadata\.annotations\[['"]([^'"]+)['"]\]$`)
	singleQuoteBracketPattern = regexp.MustCompile(`\['([^']*)'\]`)
)

func getRouteNameForService(service *aimv1alpha1.AIMService) string {
	return service.Name
}

// ============================================================================
// FETCH
// ============================================================================

type serviceHTTPRouteFetchResult struct {
	route *gatewayapiv1.HTTPRoute
}

func fetchServiceHTTPRouteResult(ctx context.Context, c client.Client, service *aimv1alpha1.AIMService) (serviceHTTPRouteFetchResult, error) {
	result := serviceHTTPRouteFetchResult{}
	route := &gatewayapiv1.HTTPRoute{}
	key := client.ObjectKey{Name: getRouteNameForService(service), Namespace: service.Namespace}
	if err := c.Get(ctx, key, route); err != nil && !errors.IsNotFound(err) {
		return result, fmt.Errorf("failed to fetch HTTPRoute: %w", err)
	} else if err == nil {
		result.route = route
	}
	return result, nil
}

// ============================================================================
// OBSERVE
// ============================================================================

type serviceHTTPRouteObservation struct {
	routingEnabled         bool
	routeExists            bool
	routeReady             bool
	routeAcceptedByGateway bool
	routeStatusReason      string
	routeStatusMessage     string
	normalizedRoutePath    string
	routeTimeout           *metav1.Duration
	gatewayRef             *gatewayapiv1.ParentReference
	annotations            map[string]string
	pathTemplateErr        error
	shouldCreateRoute      bool
}

func observeServiceHTTPRoute(
	result serviceHTTPRouteFetchResult,
	service *aimv1alpha1.AIMService,
	runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon,
) serviceHTTPRouteObservation {
	obs := serviceHTTPRouteObservation{}

	// Resolve routing configuration (service overrides runtime config)
	routingConfig := resolveRoutingConfig(service, runtimeConfig)
	obs.routingEnabled = routingConfig.enabled
	obs.gatewayRef = routingConfig.gatewayRef
	obs.annotations = routingConfig.annotations

	if !obs.routingEnabled {
		// Routing not enabled, nothing to do
		return obs
	}

	// Compute route path from path template
	path, err := resolveRoutePath(service, routingConfig.pathTemplate)
	if err != nil {
		obs.pathTemplateErr = err
		return obs
	}
	obs.normalizedRoutePath = path

	// Resolve route timeout
	obs.routeTimeout = resolveRouteTimeout(service, runtimeConfig)

	// Check if route exists and evaluate its status
	if result.route != nil {
		obs.routeExists = true

		// Evaluate HTTPRoute status by checking Gateway API conditions
		route := result.route
		if len(route.Status.Parents) == 0 {
			// No parent gateway status yet
			obs.routeReady = false
			obs.routeAcceptedByGateway = false
			obs.routeStatusReason = aimv1alpha1.AIMServiceReasonConfiguringRoute
			obs.routeStatusMessage = "HTTPRoute has no parent status"
		} else {
			// Check all parent gateways - route is ready only if accepted by all
			allAccepted := true
			for _, parent := range route.Status.Parents {
				// Find the Accepted condition
				var acceptedCond *metav1.Condition
				for i := range parent.Conditions {
					if parent.Conditions[i].Type == string(gatewayapiv1.RouteConditionAccepted) {
						acceptedCond = &parent.Conditions[i]
						break
					}
				}

				if acceptedCond == nil {
					allAccepted = false
					obs.routeStatusReason = aimv1alpha1.AIMServiceReasonConfiguringRoute
					obs.routeStatusMessage = "HTTPRoute Accepted condition not found"
					break
				}

				if acceptedCond.Status != metav1.ConditionTrue {
					allAccepted = false
					obs.routeStatusReason = acceptedCond.Reason
					if obs.routeStatusReason == "" {
						obs.routeStatusReason = aimv1alpha1.AIMServiceReasonRouteFailed
					}
					obs.routeStatusMessage = acceptedCond.Message
					if obs.routeStatusMessage == "" {
						obs.routeStatusMessage = "HTTPRoute not accepted by gateway"
					}
					break
				}
			}

			if allAccepted {
				obs.routeReady = true
				obs.routeAcceptedByGateway = true
				obs.routeStatusReason = aimv1alpha1.AIMServiceReasonRouteReady
				obs.routeStatusMessage = "HTTPRoute is ready"
			}
		}
	} else {
		obs.shouldCreateRoute = true
	}

	return obs
}

// ============================================================================
// PLAN
// ============================================================================

//nolint:unparam // error return kept for API consistency with other plan functions
func planServiceHTTPRoute(
	service *aimv1alpha1.AIMService,
	obs serviceHTTPRouteObservation,
	inferenceServiceName string,
	modelName string,
	templateName string,
) (client.Object, error) {
	if !obs.routingEnabled || !obs.shouldCreateRoute {
		return nil, nil
	}

	return buildHTTPRoute(service, obs, inferenceServiceName, modelName, templateName), nil
}

// buildHTTPRoute creates a Gateway API HTTPRoute for the AIMService
func buildHTTPRoute(
	service *aimv1alpha1.AIMService,
	obs serviceHTTPRouteObservation,
	inferenceServiceName string,
	modelName string,
	templateName string,
) *gatewayapiv1.HTTPRoute {
	annotations := make(map[string]string)
	if len(obs.annotations) > 0 {
		for k, v := range obs.annotations {
			annotations[k] = v
		}
	}

	modelNameLabel, _ := utils.SanitizeLabelValue(modelName)
	serviceNameLabel, _ := utils.SanitizeLabelValue(service.Name)

	route := &gatewayapiv1.HTTPRoute{
		TypeMeta: metav1.TypeMeta{
			APIVersion: gatewayapiv1.GroupVersion.String(),
			Kind:       "HTTPRoute",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      getRouteNameForService(service),
			Namespace: service.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":       constants.LabelValueServiceName,
				"app.kubernetes.io/component":  constants.LabelValueServiceComponent,
				"app.kubernetes.io/managed-by": constants.LabelValueManagedBy,
				constants.LabelKeyTemplate:     templateName,
				constants.LabelKeyModelName:    modelNameLabel,
				constants.LabelKeyServiceName:  serviceNameLabel,
			},
			Annotations: annotations,
		},
		Spec: gatewayapiv1.HTTPRouteSpec{},
	}

	// Set parent gateway reference
	if obs.gatewayRef != nil {
		parent := obs.gatewayRef.DeepCopy()
		if parent.Group == nil || *parent.Group == "" {
			parent.Group = ptr.To(gatewayapiv1.Group(gatewayapiv1.GroupVersion.Group))
		}
		if parent.Kind == nil || *parent.Kind == "" {
			parent.Kind = ptr.To(gatewayapiv1.Kind(kserveconstants.GatewayKind))
		}
		if parent.Namespace == nil || *parent.Namespace == "" {
			ns := gatewayapiv1.Namespace(service.Namespace)
			parent.Namespace = &ns
		}
		route.Spec.ParentRefs = []gatewayapiv1.ParentReference{*parent}
	}

	// Path is already normalized from observation phase
	pathPrefix := obs.normalizedRoutePath

	port := gatewayapiv1.PortNumber(kserveconstants.CommonDefaultHttpPort)

	// Backend points to the inferenceService predictor service
	backend := gatewayapiv1.HTTPBackendRef{
		BackendRef: gatewayapiv1.BackendRef{
			BackendObjectReference: gatewayapiv1.BackendObjectReference{
				Kind:      ptr.To(gatewayapiv1.Kind(kserveconstants.ServiceKind)),
				Name:      gatewayapiv1.ObjectName(kserveconstants.PredictorServiceName(inferenceServiceName)),
				Namespace: (*gatewayapiv1.Namespace)(&service.Namespace),
				Port:      &port,
			},
		},
	}

	rule := gatewayapiv1.HTTPRouteRule{
		Matches: []gatewayapiv1.HTTPRouteMatch{
			{
				Path: &gatewayapiv1.HTTPPathMatch{
					Type:  ptr.To(gatewayapiv1.PathMatchPathPrefix),
					Value: ptr.To(pathPrefix),
				},
			},
		},
		Filters: []gatewayapiv1.HTTPRouteFilter{
			{
				Type: gatewayapiv1.HTTPRouteFilterURLRewrite,
				URLRewrite: &gatewayapiv1.HTTPURLRewriteFilter{
					Path: &gatewayapiv1.HTTPPathModifier{
						Type:               gatewayapiv1.PrefixMatchHTTPPathModifier,
						ReplacePrefixMatch: ptr.To("/"),
					},
				},
			},
		},
		BackendRefs: []gatewayapiv1.HTTPBackendRef{backend},
	}

	// Set request timeout if configured
	if obs.routeTimeout != nil {
		timeout := gatewayapiv1.Duration(rune(obs.routeTimeout.Duration))
		rule.Timeouts = &gatewayapiv1.HTTPRouteTimeouts{
			Request: &timeout,
		}
	}

	route.Spec.Rules = []gatewayapiv1.HTTPRouteRule{rule}

	return route
}

// ============================================================================
// PROJECT
// ============================================================================

func projectServiceHTTPRoute(
	status *aimv1alpha1.AIMServiceStatus,
	cm *controllerutils.ConditionManager,
	h *controllerutils.StatusHelper,
	obs serviceHTTPRouteObservation,
) bool {
	if !obs.routingEnabled {
		// Routing not enabled, nothing to project
		return false
	}

	if obs.pathTemplateErr != nil {
		h.Degraded(aimv1alpha1.AIMServiceReasonPathTemplateInvalid, obs.pathTemplateErr.Error())
		cm.MarkFalse(aimv1alpha1.AIMServiceConditionRoutingReady, aimv1alpha1.AIMServiceReasonPathTemplateInvalid, obs.pathTemplateErr.Error(), controllerutils.AsWarning())
		return true // Fatal error
	}

	if obs.shouldCreateRoute {
		h.Progressing(aimv1alpha1.AIMServiceReasonConfiguringRoute, "Creating HTTPRoute")
		cm.MarkFalse(aimv1alpha1.AIMServiceConditionRoutingReady, aimv1alpha1.AIMServiceReasonConfiguringRoute, "HTTPRoute being created", controllerutils.AsInfo())
		return false
	}

	if obs.routeExists {
		// Set routing status path
		if status.Routing == nil {
			status.Routing = &aimv1alpha1.AIMServiceRoutingStatus{}
		}
		status.Routing.Path = obs.normalizedRoutePath

		// Check if route is ready (accepted by gateway)
		if obs.routeReady {
			cm.MarkTrue(aimv1alpha1.AIMServiceConditionRoutingReady, aimv1alpha1.AIMServiceReasonRouteReady, "HTTPRoute is ready", controllerutils.AsInfo())
		} else {
			// Route exists but not ready yet
			if obs.routeStatusReason == aimv1alpha1.AIMServiceReasonRouteFailed {
				// Gateway rejected the route - this is a degraded state
				h.Degraded(obs.routeStatusReason, obs.routeStatusMessage)
				cm.MarkFalse(aimv1alpha1.AIMServiceConditionRoutingReady, obs.routeStatusReason, obs.routeStatusMessage, controllerutils.AsWarning())
			} else {
				// Route is still being configured
				h.Progressing(obs.routeStatusReason, obs.routeStatusMessage)
				cm.MarkFalse(aimv1alpha1.AIMServiceConditionRoutingReady, obs.routeStatusReason, obs.routeStatusMessage, controllerutils.AsInfo())
			}

			// Degraded state is non-fatal - gateway might accept it later
			return obs.routeStatusReason == aimv1alpha1.AIMServiceReasonRouteFailed
		}
	}

	return false
}

// ============================================================================
// HELPER FUNCTIONS - Routing Configuration Resolution
// ============================================================================

type resolvedRoutingConfig struct {
	enabled      bool
	gatewayRef   *gatewayapiv1.ParentReference
	annotations  map[string]string
	pathTemplate string
}

// resolveRoutingConfig gets routing config from runtime config
func resolveRoutingConfig(_ *aimv1alpha1.AIMService, runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon) resolvedRoutingConfig {
	var resolved resolvedRoutingConfig
	var runtimeRouting *aimv1alpha1.AIMRuntimeRoutingConfig

	if runtimeConfig != nil {
		runtimeRouting = runtimeConfig.Routing
	}

	// Use runtime config for routing settings
	if runtimeRouting != nil {
		if runtimeRouting.Enabled != nil {
			resolved.enabled = *runtimeRouting.Enabled
		}
		if runtimeRouting.GatewayRef != nil {
			resolved.gatewayRef = runtimeRouting.GatewayRef.DeepCopy()
		}
		if runtimeRouting.PathTemplate != nil {
			resolved.pathTemplate = *runtimeRouting.PathTemplate
		}
	}

	return resolved
}

// resolveRoutePath renders the HTTP route path using the path template
func resolveRoutePath(service *aimv1alpha1.AIMService, pathTemplate string) (string, error) {
	if pathTemplate == "" {
		return defaultRoutePath(service), nil
	}

	rendered, err := renderRouteTemplate(pathTemplate, service)
	if err != nil {
		return "", err
	}

	return normalizeRoutePath(rendered)
}

// defaultRoutePath returns the default HTTP route path
func defaultRoutePath(service *aimv1alpha1.AIMService) string {
	path, err := normalizeRoutePath(fmt.Sprintf("/%s/%s", service.Namespace, string(service.UID)))
	if err != nil {
		return "/"
	}
	return path
}

// resolveRouteTimeout resolves the HTTP route timeout
func resolveRouteTimeout(service *aimv1alpha1.AIMService, runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon) *metav1.Duration {
	// Service-level timeout has priority
	if service.Spec.Routing != nil && service.Spec.Routing.RequestTimeout != nil {
		return service.Spec.Routing.RequestTimeout
	}
	// Falls back to runtime config
	if runtimeConfig != nil && runtimeConfig.Routing != nil && runtimeConfig.Routing.RequestTimeout != nil {
		return runtimeConfig.Routing.RequestTimeout
	}
	// No timeout configured
	return nil
}

// ============================================================================
// HELPER FUNCTIONS - Path Template Rendering
// ============================================================================

// renderRouteTemplate processes the path template and evaluates JSONPath expressions
func renderRouteTemplate(template string, service *aimv1alpha1.AIMService) (string, error) {
	matches := routeTemplatePattern.FindAllStringSubmatchIndex(template, -1)
	if len(matches) == 0 {
		return template, nil
	}

	var builder strings.Builder
	last := 0
	for _, m := range matches {
		start, end := m[0], m[1]
		exprStart, exprEnd := m[2], m[3]

		builder.WriteString(template[last:start])

		expr := strings.TrimSpace(template[exprStart:exprEnd])
		value, err := evaluateJSONPath(expr, service)
		if err != nil {
			return "", fmt.Errorf("failed to evaluate route template %q: %w", expr, err)
		}
		builder.WriteString(applyTemplateValueModifiers(expr, value))

		last = end
	}
	builder.WriteString(template[last:])

	return builder.String(), nil
}

// evaluateJSONPath evaluates a JSONPath expression against the service object
func evaluateJSONPath(expr string, obj interface{}) (string, error) {
	if expr == "" {
		return "", fmt.Errorf("jsonpath expression is empty")
	}

	// Handle special label and annotation access patterns
	if service, ok := obj.(*aimv1alpha1.AIMService); ok {
		if match := labelAccessPattern.FindStringSubmatch(expr); len(match) == 2 {
			value, ok := service.Labels[match[1]]
			if !ok {
				return "", fmt.Errorf("jsonpath evaluation error: label %q not found", match[1])
			}
			return value, nil
		}
		if match := annotationAccessPattern.FindStringSubmatch(expr); len(match) == 2 {
			value, ok := service.Annotations[match[1]]
			if !ok {
				return "", fmt.Errorf("jsonpath evaluation error: annotation %q not found", match[1])
			}
			return value, nil
		}
	}

	// Parse and evaluate standard JSONPath
	parsed := fmt.Sprintf("{%s}", normalizeBracketKeys(expr))
	jp := jsonpath.New("route")
	jp.AllowMissingKeys(false)
	if err := jp.Parse(parsed); err != nil {
		return "", fmt.Errorf("invalid jsonpath expression: %w", err)
	}

	results, err := jp.FindResults(obj)
	if err != nil {
		return "", fmt.Errorf("jsonpath evaluation error: %w", err)
	}

	if len(results) == 0 || len(results[0]) == 0 {
		return "", fmt.Errorf("jsonpath returned no results")
	}
	if len(results) > 1 || len(results[0]) > 1 {
		return "", fmt.Errorf("jsonpath returned multiple results")
	}

	val := results[0][0]
	if !val.IsValid() {
		return "", fmt.Errorf("jsonpath returned invalid value")
	}
	if val.Kind() == reflect.Ptr && val.IsNil() {
		return "", fmt.Errorf("jsonpath returned nil value")
	}

	// Dereference pointers
	for val.Kind() == reflect.Ptr {
		val = val.Elem()
		if !val.IsValid() {
			return "", fmt.Errorf("jsonpath returned nil pointer")
		}
	}

	value := val.Interface()
	switch typed := value.(type) {
	case string:
		return typed, nil
	case fmt.Stringer:
		return typed.String(), nil
	default:
		return fmt.Sprint(value), nil
	}
}

// normalizeRoutePath validates and normalizes a route path
func normalizeRoutePath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("route template produced an empty path")
	}
	if !strings.HasPrefix(raw, "/") {
		raw = "/" + raw
	}

	normalized := strings.TrimRight(raw, "/")
	if normalized == "" {
		normalized = "/"
	}
	if len(normalized) > MaxRoutePathLength {
		return "", fmt.Errorf("route path %q exceeds %d characters", normalized, MaxRoutePathLength)
	}

	segments := strings.Split(normalized, "/")
	encoded := make([]string, 0, len(segments))
	for i, segment := range segments {
		if i == 0 {
			encoded = append(encoded, "")
			continue
		}
		if segment == "" {
			continue
		}
		encodedSegment := encodeRouteSegment(segment)
		if encodedSegment == "" {
			continue
		}
		encoded = append(encoded, encodedSegment)
	}

	path := "/"
	if len(encoded) > 1 {
		path = "/" + strings.Join(encoded[1:], "/")
	}

	path = strings.TrimRight(path, "/")
	if path == "" {
		path = "/"
	}

	// No need for second length check - already validated above
	return path, nil
}

// encodeRouteSegment encodes a path segment to be RFC1123 compliant
func encodeRouteSegment(segment string) string {
	segment = strings.TrimSpace(segment)
	if segment == "" {
		return ""
	}
	return utils.MakeRFC1123Compliant(segment)
}

// applyTemplateValueModifiers applies transformations to template values
func applyTemplateValueModifiers(expr, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return value
	}
	// Annotations are not transformed, everything else is made RFC1123 compliant
	if annotationAccessPattern.MatchString(expr) {
		return value
	}
	return utils.MakeRFC1123Compliant(value)
}

// normalizeBracketKeys converts single-quoted bracket notation to double-quoted
func normalizeBracketKeys(expr string) string {
	return singleQuoteBracketPattern.ReplaceAllStringFunc(expr, func(match string) string {
		groups := singleQuoteBracketPattern.FindStringSubmatch(match)
		if len(groups) != 2 {
			return match
		}
		key := groups[1]
		key = strings.ReplaceAll(key, `\`, `\\`)
		key = strings.ReplaceAll(key, `"`, `\\"`)
		return fmt.Sprintf(`["%s"]`, key)
	})
}
