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

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	kserveconstants "github.com/kserve/kserve/pkg/constants"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/jsonpath"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"
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

func GetRouteNameForService(service *aimv1alpha1.AIMService) string {
	return service.Name
}

// ============================================================================
// FETCH
// ============================================================================

type ServiceHTTPRouteFetchResult struct {
	Route *gatewayapiv1.HTTPRoute
}

func fetchServiceHTTPRouteResult(ctx context.Context, c client.Client, service *aimv1alpha1.AIMService) (ServiceHTTPRouteFetchResult, error) {
	result := ServiceHTTPRouteFetchResult{}
	route := &gatewayapiv1.HTTPRoute{}
	key := client.ObjectKey{Name: GetRouteNameForService(service), Namespace: service.Namespace}
	if err := c.Get(ctx, key, route); err != nil && !errors.IsNotFound(err) {
		return result, fmt.Errorf("failed to fetch HTTPRoute: %w", err)
	} else if err == nil {
		result.Route = route
	}
	return result, nil
}

// ============================================================================
// OBSERVE
// ============================================================================

type ServiceHTTPRouteObservation struct {
	RoutingEnabled      bool
	RouteExists         bool
	NormalizedRoutePath string
	RouteTimeout        *metav1.Duration
	GatewayRef          *gatewayapiv1.ParentReference
	Annotations         map[string]string
	PathTemplateErr     error
	ShouldCreateRoute   bool
}

func observeServiceHTTPRoute(
	result ServiceHTTPRouteFetchResult,
	service *aimv1alpha1.AIMService,
	runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon,
) ServiceHTTPRouteObservation {
	obs := ServiceHTTPRouteObservation{}

	// Resolve routing configuration (service overrides runtime config)
	routingConfig := resolveRoutingConfig(service, runtimeConfig)
	obs.RoutingEnabled = routingConfig.Enabled
	obs.GatewayRef = routingConfig.GatewayRef
	obs.Annotations = routingConfig.Annotations

	if !obs.RoutingEnabled {
		// Routing not enabled, nothing to do
		return obs
	}

	// Compute route path from path template
	path, err := resolveRoutePath(service, routingConfig.PathTemplate)
	if err != nil {
		obs.PathTemplateErr = err
		return obs
	}
	obs.NormalizedRoutePath = path

	// Resolve route timeout
	obs.RouteTimeout = resolveRouteTimeout(service, runtimeConfig)

	// Check if route exists
	if result.Route != nil {
		obs.RouteExists = true
	} else {
		obs.ShouldCreateRoute = true
	}

	return obs
}

// ============================================================================
// PLAN
// ============================================================================

func planServiceHTTPRoute(
	service *aimv1alpha1.AIMService,
	obs ServiceHTTPRouteObservation,
	inferenceServiceName string,
	modelName string,
	templateName string,
) (client.Object, error) {
	if !obs.RoutingEnabled || !obs.ShouldCreateRoute {
		return nil, nil
	}

	return buildHTTPRoute(service, obs, inferenceServiceName, modelName, templateName), nil
}

// buildHTTPRoute creates a Gateway API HTTPRoute for the AIMService
func buildHTTPRoute(
	service *aimv1alpha1.AIMService,
	obs ServiceHTTPRouteObservation,
	inferenceServiceName string,
	modelName string,
	templateName string,
) *gatewayapiv1.HTTPRoute {
	annotations := make(map[string]string)
	if len(obs.Annotations) > 0 {
		for k, v := range obs.Annotations {
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
			Name:      GetRouteNameForService(service),
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
	if obs.GatewayRef != nil {
		parent := obs.GatewayRef.DeepCopy()
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
	pathPrefix := obs.NormalizedRoutePath

	port := gatewayapiv1.PortNumber(kserveconstants.CommonDefaultHttpPort)

	// Backend points to the InferenceService predictor service
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
	if obs.RouteTimeout != nil {
		timeout := gatewayapiv1.Duration(obs.RouteTimeout.Duration)
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
	obs ServiceHTTPRouteObservation,
) bool {
	if !obs.RoutingEnabled {
		// Routing not enabled, nothing to project
		return false
	}

	if obs.PathTemplateErr != nil {
		h.Degraded(aimv1alpha1.AIMServiceReasonPathTemplateInvalid, obs.PathTemplateErr.Error())
		cm.MarkFalse(aimv1alpha1.AIMServiceConditionRoutingReady, aimv1alpha1.AIMServiceReasonPathTemplateInvalid, obs.PathTemplateErr.Error(), controllerutils.LevelWarning)
		return true
	}

	if obs.ShouldCreateRoute {
		h.Progressing(aimv1alpha1.AIMServiceReasonConfiguringRoute, "Creating HTTPRoute")
		cm.MarkFalse(aimv1alpha1.AIMServiceConditionRoutingReady, aimv1alpha1.AIMServiceReasonConfiguringRoute, "HTTPRoute being created", controllerutils.LevelNormal)
		return false
	}

	if obs.RouteExists {
		cm.MarkTrue(aimv1alpha1.AIMServiceConditionRoutingReady, aimv1alpha1.AIMServiceReasonRouteReady, "HTTPRoute is ready", controllerutils.LevelNormal)

		// Set routing status
		if status.Routing == nil {
			status.Routing = &aimv1alpha1.AIMServiceRoutingStatus{}
		}
		status.Routing.Path = obs.NormalizedRoutePath
	}

	return false
}

// ============================================================================
// HELPER FUNCTIONS - Routing Configuration Resolution
// ============================================================================

type resolvedRoutingConfig struct {
	Enabled      bool
	GatewayRef   *gatewayapiv1.ParentReference
	Annotations  map[string]string
	PathTemplate string
}

// resolveRoutingConfig merges service-level routing config with runtime defaults
func resolveRoutingConfig(service *aimv1alpha1.AIMService, runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon) resolvedRoutingConfig {
	var resolved resolvedRoutingConfig
	var serviceRouting *aimv1alpha1.AIMServiceRouting
	var runtimeRouting *aimv1alpha1.AIMRuntimeRoutingConfig

	if service != nil {
		serviceRouting = service.Spec.Routing
	}

	if runtimeConfig != nil {
		runtimeRouting = runtimeConfig.Routing
	}

	// Start with runtime config defaults
	if runtimeRouting != nil {
		if runtimeRouting.Enabled != nil {
			resolved.Enabled = *runtimeRouting.Enabled
		}
		if runtimeRouting.GatewayRef != nil {
			resolved.GatewayRef = runtimeRouting.GatewayRef.DeepCopy()
		}
		resolved.PathTemplate = runtimeRouting.PathTemplate
	}

	// Override with service-level config when present
	if serviceRouting != nil {
		// Only override enabled if explicitly set (non-nil)
		if serviceRouting.Enabled != nil {
			resolved.Enabled = *serviceRouting.Enabled
		}

		// Override GatewayRef if service specifies one
		if serviceRouting.GatewayRef != nil {
			resolved.GatewayRef = serviceRouting.GatewayRef.DeepCopy()
		}

		// Service annotations (only service level has annotations)
		if len(serviceRouting.Annotations) > 0 {
			resolved.Annotations = make(map[string]string, len(serviceRouting.Annotations))
			for k, v := range serviceRouting.Annotations {
				resolved.Annotations[k] = v
			}
		}

		// Override PathTemplate if service specifies one
		if serviceRouting.PathTemplate != "" {
			resolved.PathTemplate = serviceRouting.PathTemplate
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
