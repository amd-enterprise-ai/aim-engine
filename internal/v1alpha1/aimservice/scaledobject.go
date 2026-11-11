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
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

// kedaScaledObjectGVK is the schema for the KEDA ScaledObject CRD.
// Modeled as unstructured so the controller does not need to vendor KEDA's
// Go types.
var kedaScaledObjectGVK = schema.GroupVersionKind{
	Group:   "keda.sh",
	Version: "v1alpha1",
	Kind:    "ScaledObject",
}

// Envoy Gateway's shared Lua extension emits one counter per HTTPRoute. The
// Prometheus stats sink prepends envoy_http_lua_ and converts punctuation to
// underscores. The Lua script hex-escapes every non-alphanumeric identity byte
// before that normalization, and length prefixes preserve component boundaries.
// The OTel collector forwards these counters unchanged, so the controller can
// query the exact name without per-route Lua or collector-side label parsing.
//
// JOINED INVARIANT with the gateway Lua script and both collector manifests --
// change them together (see TestGatewayActivationInvariant).
const envoyGatewayActivationMetricPrefix = "envoy_http_lua_aim_activation_requests_"

// gatewayActivationMetricName is retained for the legacy kgateway collector,
// which emits one deployment-labeled metric rather than route-specific names.
const gatewayActivationMetricName = "envoy_cluster_external_upstream_rq_completed"

// Scale-to-zero polling and cooldown defaults. KEDA platform defaults
// (30 / 300) are too slow for single-request activation; the values
// below trigger 0->1 within ~10 s of a single request.
//
// cooldownPeriodMinSeconds is both the additive base of the memory-derived
// formula and the clamp floor; cooldownPeriodMaxSeconds caps the
// extrapolation so multi-GPU services (whose linear-in-memory formula
// overestimates cold-start) don't strand replicas for hours.
const (
	defaultPollingIntervalScaleToZero = int32(5)
	defaultCooldownPeriodScaleToZero  = int32(300)
	cooldownPeriodMinSeconds          = int32(300)
	cooldownPeriodMaxSeconds          = int32(1200)
)

// cooldownSecondsPerGiMemory returns the per-GiB multiplier used to derive
// the cooldown from the predictor's memory request. Tuned via
// EnvAIMCooldownSecondsPerGiMemory; "0" disables the memory contribution.
func cooldownSecondsPerGiMemory() int32 {
	v := os.Getenv(constants.EnvAIMCooldownSecondsPerGiMemory)
	if v == "" {
		return constants.DefaultCooldownSecondsPerGiMemory
	}
	parsed, err := strconv.ParseInt(v, 10, 32)
	if err != nil || parsed < 0 {
		return constants.DefaultCooldownSecondsPerGiMemory
	}
	return int32(parsed)
}

// kedaOTelScalerAddress returns the gRPC endpoint of the keda-otel-add-on
// scaler. Overridden via EnvAIMKEDAOTelScalerAddress (set by the Helm chart).
func kedaOTelScalerAddress() string {
	if v := os.Getenv(constants.EnvAIMKEDAOTelScalerAddress); v != "" {
		return v
	}
	return constants.DefaultKEDAOTelScalerAddress
}

// planScaledObject returns a controller-owned KEDA ScaledObject for the
// predictor Deployment whenever the AIMService asks for autoscaling.
//
// The object carries up to two trigger sources:
//   - Gateway-rate activation trigger (only when minReplicas=0), the only
//     metric available at zero replicas.
//   - The user's warm-state triggers (spec.autoScaling.metrics), driving 1->N.
//
// effectiveResources is the fully-merged predictor resource block from
// resolveResources(). Pass nil before the template has resolved; the
// reconciler re-plans on the next pass once it has.
//
// Returns nil for the legacy fixed-replica path.
func planScaledObject(
	ctx context.Context,
	service *aimv1alpha1.AIMService,
	effectiveResources *corev1.ResourceRequirements,
	runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon,
) client.Object {
	logger := log.FromContext(ctx).WithName("planScaledObject")

	if !HasAutoscaling(service) {
		return nil
	}

	// scaleTargetRef must match the KServe-owned predictor Deployment name
	// (`<isvcName>-predictor` where isvcName is the hashed-derived name).
	isvcName, err := GenerateInferenceServiceName(service.Name, service.Namespace)
	if err != nil {
		logger.Error(err, "cannot derive InferenceService name; skipping ScaledObject",
			"service", service.Name, "namespace", service.Namespace)
		return nil
	}
	return planScaledObjectForDeployment(ctx, service, effectiveResources, runtimeConfig, isvcName+constants.PredictorServiceSuffix)
}

// HasAutoscaling reports whether the AIMService asks for autoscaling.
func HasAutoscaling(service *aimv1alpha1.AIMService) bool {
	return service.Spec.AutoScaling != nil ||
		service.Spec.MinReplicas != nil ||
		service.Spec.MaxReplicas != nil
}

// planScaledObjectForDeployment is planScaledObject for an explicit serving
// Deployment. The ScaledObject is named after it, scales it, and scopes bare
// user metric queries to it.
func planScaledObjectForDeployment(
	ctx context.Context,
	service *aimv1alpha1.AIMService,
	effectiveResources *corev1.ResourceRequirements,
	runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon,
	predictorName string,
) client.Object {
	logger := log.FromContext(ctx).WithName("planScaledObject")
	if !HasAutoscaling(service) {
		return nil
	}

	minReplicas, maxReplicas := resolveReplicaBounds(service)
	scaleToZero := minReplicas == 0
	activationQuery, activationRequired, activationErr := resolveScaleToZeroActivationMetricForDeployment(
		service, runtimeConfig, predictorName,
	)
	if activationRequired && activationErr != nil {
		logger.Error(activationErr,
			"cannot build gateway activation metric query; skipping ScaledObject",
			"service", service.Name,
			"namespace", service.Namespace,
		)
		return nil
	}

	triggers := make([]interface{}, 0, 2)
	if scaleToZero {
		triggers = append(triggers, buildGatewayActivationTrigger(activationQuery))
	}
	for _, m := range collectUserMetrics(service) {
		if t := buildUserMetricTrigger(m, service.Namespace, predictorName); t != nil {
			triggers = append(triggers, t)
		}
	}

	if len(triggers) == 0 {
		// KEDA rejects ScaledObjects with empty triggers; skip planning.
		logger.V(1).Info("autoscaling requested but no triggers resolved; skipping ScaledObject",
			"service", service.Name,
			"namespace", service.Namespace,
		)
		return nil
	}

	pollingInterval, cooldownPeriod := resolvePollingAndCooldown(service, scaleToZero, effectiveResources)

	serviceLabelValue, _ := utils.SanitizeLabelValue(service.Name)

	so := &unstructured.Unstructured{}
	so.SetGroupVersionKind(kedaScaledObjectGVK)
	so.SetName(predictorName)
	so.SetNamespace(service.Namespace)
	so.SetLabels(map[string]string{
		constants.LabelK8sComponent: constants.ComponentAutoscaling,
		constants.LabelK8sManagedBy: constants.LabelValueManagedBy,
		constants.LabelService:      serviceLabelValue,
	})
	so.SetOwnerReferences([]metav1.OwnerReference{
		{
			APIVersion:         service.APIVersion,
			Kind:               service.Kind,
			Name:               service.Name,
			UID:                service.UID,
			Controller:         ptr.To(true),
			BlockOwnerDeletion: ptr.To(true),
		},
	})

	spec := map[string]interface{}{
		"scaleTargetRef": map[string]interface{}{
			"name": predictorName,
		},
		"minReplicaCount": int64(minReplicas),
		"maxReplicaCount": int64(maxReplicas),
		"triggers":        triggers,
	}
	if pollingInterval != nil {
		spec["pollingInterval"] = int64(*pollingInterval)
	}
	if cooldownPeriod != nil {
		spec["cooldownPeriod"] = int64(*cooldownPeriod)
	}

	so.Object["spec"] = spec
	return so
}

// resolveReplicaBounds is the single source of truth for min/max replica
// defaulting, consumed by both the ISVC path (configureReplicasAndAutoscaling)
// and the ScaledObject path (planScaledObject). When only minReplicas is set,
// max defaults to min but never below 1 so a scale-to-zero service can recover.
func resolveReplicaBounds(service *aimv1alpha1.AIMService) (minReplicas, maxReplicas int32) {
	if service.Spec.MinReplicas != nil {
		minReplicas = *service.Spec.MinReplicas
	} else {
		minReplicas = 1
	}
	switch {
	case service.Spec.MaxReplicas != nil:
		maxReplicas = *service.Spec.MaxReplicas
	case service.Spec.MinReplicas != nil:
		maxReplicas = *service.Spec.MinReplicas
		if maxReplicas < 1 {
			maxReplicas = 1
		}
	default:
		maxReplicas = 1
	}
	return minReplicas, maxReplicas
}

// resolvePollingAndCooldown returns the effective polling interval and
// cooldown period. Precedence (first non-nil wins):
//  1. spec.autoScaling.{pollingInterval,cooldownPeriod} -- operator override.
//  2. Memory-derived heuristic via computePredictorCooldown (scale-to-zero only).
//  3. defaultCooldownPeriodScaleToZero -- fallback when memory is unknown.
//
// Outside scale-to-zero both are left nil so KEDA's platform defaults apply.
func resolvePollingAndCooldown(service *aimv1alpha1.AIMService, scaleToZero bool, effectiveResources *corev1.ResourceRequirements) (pollingInterval, cooldownPeriod *int32) {
	if as := service.Spec.AutoScaling; as != nil {
		if as.PollingInterval != nil {
			pollingInterval = as.PollingInterval
		}
		if as.CooldownPeriod != nil {
			cooldownPeriod = as.CooldownPeriod
		}
	}
	if scaleToZero {
		if pollingInterval == nil {
			pollingInterval = ptr.To(defaultPollingIntervalScaleToZero)
		}
		if cooldownPeriod == nil {
			cooldownPeriod = ptr.To(computePredictorCooldown(service, effectiveResources))
		}
	}
	return pollingInterval, cooldownPeriod
}

// computePredictorCooldown derives a cold-start-aware cooldownPeriod from
// the predictor's memory request using:
//
//	cooldownPeriod = clamp(min + memGiB * perGiB, min, max)
//
// where min=300, max=1200 and perGiB defaults to 5 s/GiB. The constants
// budget for ~1.6 GB/s warm-cache throughput (deliberate overestimate
// against the typical 2 GB/s) and absorb the scaler's 120 s rate-decay
// window. Falls back to the flat default when memory cannot be determined.
// Operators with measured cold-start times should override via
// spec.autoScaling.cooldownPeriod.
func computePredictorCooldown(service *aimv1alpha1.AIMService, effectiveResources *corev1.ResourceRequirements) int32 {
	memGiB := predictorMemoryGiB(service, effectiveResources)
	if memGiB == 0 {
		return defaultCooldownPeriodScaleToZero
	}
	cd := cooldownPeriodMinSeconds + memGiB*cooldownSecondsPerGiMemory()
	if cd < cooldownPeriodMinSeconds {
		cd = cooldownPeriodMinSeconds
	}
	if cd > cooldownPeriodMaxSeconds {
		cd = cooldownPeriodMaxSeconds
	}
	return cd
}

// predictorMemoryGiB returns the predictor's effective memory footprint in
// GiB (binary). Prefers the fully-merged effectiveResources; falls back to
// service.Spec.Resources when the template hasn't resolved yet.
//
// Reads max(requests, limits) rather than requests-first because the
// cooldown heuristic is estimating the workload's working-set size, not
// the kubelet's admission-time guarantee -- AIM templates typically set
// requests low (for scheduling) and limits at the actual cold-start
// footprint (for KV cache + page cache headroom).
func predictorMemoryGiB(service *aimv1alpha1.AIMService, effectiveResources *corev1.ResourceRequirements) int32 {
	if g := memoryGiBFromRequirements(effectiveResources); g > 0 {
		return g
	}
	if service != nil {
		return memoryGiBFromRequirements(service.Spec.Resources)
	}
	return 0
}

// memoryGiBFromRequirements returns max(requests.memory, limits.memory) in
// GiB (binary), or 0 when neither is set.
func memoryGiBFromRequirements(rr *corev1.ResourceRequirements) int32 {
	if rr == nil {
		return 0
	}
	req := quantityToGiB(rr.Requests[corev1.ResourceMemory])
	lim := quantityToGiB(rr.Limits[corev1.ResourceMemory])
	if lim > req {
		return lim
	}
	return req
}

// quantityToGiB returns q in GiB (binary, rounded down), or 0 for
// non-positive or overflow-unsafe quantities.
func quantityToGiB(q resource.Quantity) int32 {
	bytes, ok := q.AsInt64()
	if !ok || bytes <= 0 {
		return 0
	}
	const gibibyte = int64(1) << 30
	return int32(bytes / gibibyte)
}

// collectUserMetrics returns the user-declared autoscaling metrics, if any.
func collectUserMetrics(service *aimv1alpha1.AIMService) []aimv1alpha1.AIMServiceMetricsSpec {
	if service.Spec.AutoScaling == nil {
		return nil
	}
	return service.Spec.AutoScaling.Metrics
}

// resolvedUserMetricTriggerCount reports how many user-declared metrics produce
// KEDA triggers. Activation status uses this to distinguish the reconciled HPA
// shape from a stale user-only HPA during scale-to-zero configuration changes.
func resolvedUserMetricTriggerCount(service *aimv1alpha1.AIMService, predictorName string) int {
	count := 0
	for _, metric := range collectUserMetrics(service) {
		if buildUserMetricTrigger(metric, service.Namespace, predictorName) != nil {
			count++
		}
	}
	return count
}

// validateScaleToZeroActivationMetric is the single source of truth for whether
// a zero-to-one activation metric is required for the InferenceService
// predictor and whether its provider-neutral query can be built. Provider
// selection remains encapsulated in gatewayActivationMetricQuery.
func validateScaleToZeroActivationMetric(
	service *aimv1alpha1.AIMService,
	runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon,
) (required bool, err error) {
	if !isScaleToZero(service) {
		return false, nil
	}
	isvcName, err := GenerateInferenceServiceName(service.Name, service.Namespace)
	if err != nil {
		return true, err
	}
	_, required, err = resolveScaleToZeroActivationMetricForDeployment(
		service, runtimeConfig, isvcName+constants.PredictorServiceSuffix,
	)
	return required, err
}

// resolveScaleToZeroActivationMetricForDeployment resolves the activation
// query for an explicit serving Deployment, which the legacy kgateway scope
// and the ${predictorDeployment} template placeholder refer to.
func resolveScaleToZeroActivationMetricForDeployment(
	service *aimv1alpha1.AIMService,
	runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon,
	deploymentName string,
) (query string, required bool, err error) {
	if !isScaleToZero(service) {
		return "", false, nil
	}
	query, err = gatewayActivationMetricQuery(service, deploymentName, runtimeConfig)
	return query, true, err
}

// buildGatewayActivationTrigger constructs the synthetic KEDA trigger that
// flips activation from 0->1 on a single request. Query resolution is kept
// separate so an invalid activation query cannot leave a user metric in the
// first trigger slot and make KEDA's s0 identity ambiguous.
//
// targetValue and operationOverTime are compiled-in constants, not knobs: the
// trigger is activation-only (targetValue is a neutralizing ceiling) and
// operationOverTime is a JOINED INVARIANT with the selected gateway pipeline:
// Envoy Gateway pushes source-side deltas, while kgateway currently converts
// cumulative counters in its collector. It must stay `avg` (see the constants
// and TestGatewayActivationInvariant). The query may come from a service or
// RuntimeConfig, while scalerAddress remains an install-time setting.
func buildGatewayActivationTrigger(query string) map[string]interface{} {
	return map[string]interface{}{
		"type": "external",
		"metadata": map[string]interface{}{
			"scalerAddress":     kedaOTelScalerAddress(),
			"metricQuery":       query,
			"targetValue":       constants.DefaultGatewayActivationTargetValue,
			"operationOverTime": constants.DefaultGatewayActivationOperationOverTime,
		},
	}
}

// gatewayActivationScope returns the gateway metric scheme the activation
// trigger must use. Gateway activation is disabled unless the operator
// installation explicitly selects a provider. The Helm chart maps
// gatewayProvider to this internal value so the controller and
// provider-specific collector use the same metric contract.
func gatewayActivationScope() string {
	switch v := strings.ToLower(strings.TrimSpace(os.Getenv(constants.EnvAIMGatewayActivationScope))); v {
	case constants.GatewayActivationScopeHTTPRoute:
		return constants.GatewayActivationScopeHTTPRoute
	case constants.GatewayActivationScopeDeployment:
		return constants.GatewayActivationScopeDeployment
	case constants.GatewayActivationScopeCustom:
		return constants.GatewayActivationScopeCustom
	case constants.GatewayActivationScopeNone:
		return constants.GatewayActivationScopeNone
	default:
		return constants.GatewayActivationScopeNone
	}
}

// gatewayActivationMetricQuery resolves a service override, then a merged
// RuntimeConfig default, before falling back to the provider-derived query.
func gatewayActivationMetricQuery(
	service *aimv1alpha1.AIMService,
	predictorName string,
	runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon,
) (string, error) {
	scope := gatewayActivationScope()
	if scope == constants.GatewayActivationScopeNone {
		return "", fmt.Errorf(
			"gatewayProvider=none disables scale-from-zero activation; " +
				"select envoyGateway, kgateway, or custom",
		)
	}

	if queryTemplate := activationMetricQueryTemplate(service, runtimeConfig); queryTemplate != "" {
		return renderActivationMetricQueryTemplate(service, predictorName, queryTemplate)
	}

	switch scope {
	case constants.GatewayActivationScopeDeployment:
		// Legacy kgateway: the collector names the upstream cluster
		// kube_<ns>_<svc>_<port> and labels the series by the predictor
		// Deployment, identical to the user-metric scoping.
		return scopedMetricQuery(gatewayActivationMetricName, service.Namespace, predictorName), nil
	case constants.GatewayActivationScopeCustom:
		return "", fmt.Errorf(
			"gatewayProvider=custom requires spec.scaleFromZero.activationMetricQueryTemplate " +
				"on the AIMService or its RuntimeConfig",
		)
	}

	// Envoy Gateway: one gateway-wide Lua extension derives the
	// matched HTTPRoute from streamInfo().routeName() and increments a
	// route-specific counter. Query that exact metric; no per-service script or
	// collector-side label extraction is needed.
	routeName, err := GenerateHTTPRouteName(service.Name, service.Namespace)
	if err != nil {
		return "", fmt.Errorf("derive HTTPRoute name for activation query: %w", err)
	}
	return fmt.Sprintf("sum(%s)", envoyGatewayActivationMetricName(service.Namespace, routeName)), nil
}

func activationMetricQueryTemplate(
	service *aimv1alpha1.AIMService,
	runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon,
) string {
	if service.Spec.ScaleFromZero != nil &&
		strings.TrimSpace(service.Spec.ScaleFromZero.ActivationMetricQueryTemplate) != "" {
		return service.Spec.ScaleFromZero.ActivationMetricQueryTemplate
	}
	if runtimeConfig != nil && runtimeConfig.ScaleFromZero != nil {
		return runtimeConfig.ScaleFromZero.ActivationMetricQueryTemplate
	}
	return ""
}

func renderActivationMetricQueryTemplate(
	service *aimv1alpha1.AIMService,
	predictorName string,
	queryTemplate string,
) (string, error) {
	query := strings.TrimSpace(queryTemplate)
	if query == "" {
		return "", fmt.Errorf("activationMetricQueryTemplate must not be empty")
	}

	routeName := ""
	if strings.Contains(query, "${httpRouteName}") {
		var err error
		routeName, err = GenerateHTTPRouteName(service.Name, service.Namespace)
		if err != nil {
			return "", fmt.Errorf("derive HTTPRoute name for activation query template: %w", err)
		}
	}

	query = strings.NewReplacer(
		"${namespace}", service.Namespace,
		"${serviceName}", service.Name,
		"${httpRouteName}", routeName,
		"${predictorDeployment}", predictorName,
	).Replace(query)

	if start := strings.Index(query, "${"); start >= 0 {
		placeholder := query[start:]
		if end := strings.IndexByte(placeholder, '}'); end >= 0 {
			placeholder = placeholder[:end+1]
		}
		return "", fmt.Errorf("activationMetricQueryTemplate contains unsupported placeholder %q", placeholder)
	}

	return query, nil
}

// envoyGatewayActivationMetricName mirrors the gateway-wide Lua extension's
// stat naming and Envoy's Prometheus normalization.
func envoyGatewayActivationMetricName(namespace, routeName string) string {
	return fmt.Sprintf(
		"%sn%d_%s_r%d_%s",
		envoyGatewayActivationMetricPrefix,
		len(namespace),
		prometheusMetricToken(namespace),
		len(routeName),
		prometheusMetricToken(routeName),
	)
}

// prometheusMetricToken matches the Lua script's byte-wise escaping. Every
// non-alphanumeric byte (including underscore) is encoded as _xHH. This is
// injective even when Envoy later normalizes stat punctuation to underscores:
// Kubernetes resource names cannot contain an unescaped underscore.
func prometheusMetricToken(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c >= 'a' && c <= 'z',
			c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "_x%02x", c)
		}
	}
	return b.String()
}

// buildUserMetricTrigger translates one AIMServiceMetricsSpec into an
// external-type KEDA trigger. Bare metric names are auto-scoped to
// {namespace,deployment}; queries that already carry a label selector are
// passed through verbatim. Returns nil for metric types the controller
// cannot translate.
func buildUserMetricTrigger(metric aimv1alpha1.AIMServiceMetricsSpec, namespace, predictorName string) map[string]interface{} {
	if metric.Type != "PodMetric" || metric.PodMetric == nil || metric.PodMetric.Metric == nil || metric.PodMetric.Target == nil {
		return nil
	}
	pm := metric.PodMetric

	metricQuery := pm.Metric.Query
	if metricQuery == "" && len(pm.Metric.MetricNames) > 0 {
		metricQuery = pm.Metric.MetricNames[0]
	}
	if metricQuery == "" {
		return nil
	}
	if !queryHasLabelSelector(metricQuery) {
		metricQuery = scopedMetricQuery(metricQuery, namespace, predictorName)
	}

	metadata := map[string]interface{}{
		"scalerAddress": resolveUserScalerAddress(pm.Metric.ServerAddress),
		"metricQuery":   metricQuery,
		"targetValue":   resolveUserTargetValue(pm.Target),
	}
	if pm.Metric.OperationOverTime != "" {
		metadata["operationOverTime"] = pm.Metric.OperationOverTime
	}

	return map[string]interface{}{
		"type":     "external",
		"metadata": metadata,
	}
}

// scopedMetricQuery wraps a bare metric name in a keda-otel-add-on
// PromQL-shaped query scoped to a specific Deployment's series:
// `sum(<expr>{namespace="<ns>",deployment="<dep>"})`. Used for user 1->N
// metrics (in-pod vLLM stats carry the deployment label).
func scopedMetricQuery(metricExpr, namespace, predictorName string) string {
	return fmt.Sprintf(`sum(%s{namespace="%s",deployment="%s"})`, metricExpr, namespace, predictorName)
}

// queryHasLabelSelector returns true when the query already includes an
// inline `{...}` label selector.
func queryHasLabelSelector(query string) bool {
	return strings.Contains(query, "{")
}

// resolveUserScalerAddress falls back to the controller-wide default when
// the user did not pin a per-metric serverAddress.
func resolveUserScalerAddress(userAddress string) string {
	if userAddress != "" {
		return userAddress
	}
	return kedaOTelScalerAddress()
}

// resolveUserTargetValue extracts the string-valued target from the user's
// metric target. KEDA's external scaler expects targetValue as a string.
func resolveUserTargetValue(target *aimv1alpha1.AIMServiceMetricTarget) string {
	if target == nil {
		return "0"
	}
	switch {
	case target.Value != "":
		return target.Value
	case target.AverageValue != "":
		return target.AverageValue
	case target.AverageUtilization != nil:
		return strconv.FormatInt(int64(*target.AverageUtilization), 10)
	default:
		return "0"
	}
}
