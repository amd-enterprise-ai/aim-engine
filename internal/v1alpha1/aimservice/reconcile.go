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
	"strings"
	"time"

	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/aimadapter"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/aimruntimeconfig"
	v1alpha1utils "github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/utils"
)

// ServiceReconciler implements the domain logic for AIMService reconciliation.
type ServiceReconciler struct {
	Clientset kubernetes.Interface
	Scheme    *runtime.Scheme
}

func (r *ServiceReconciler) GetApplyOptions(obs ServiceObservation) controllerutils.ApplyOptions {
	return aimruntimeconfig.GetApplyOptions(obs.mergedRuntimeConfig.Value)
}

// ============================================================================
// FETCH
// ============================================================================

// ServiceFetchResult holds all fetched resources needed for AIMService reconciliation.
type ServiceFetchResult struct {
	service *aimv1alpha1.AIMService

	// Merged runtime config (provided by reconcile context)
	mergedRuntimeConfig controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]

	// Model resolution result (includes existing model or signals creation needed)
	modelResult ModelFetchResult

	// Template resolution
	template        controllerutils.FetchResult[*aimv1alpha1.AIMServiceTemplate]
	clusterTemplate controllerutils.FetchResult[*aimv1alpha1.AIMClusterServiceTemplate]

	// Template selection results (when auto-selecting)
	templateSelection *TemplateSelectionResult

	// Existing downstream resources
	inferenceService       controllerutils.FetchResult[*servingv1beta1.InferenceService]
	inferenceServiceEvents controllerutils.FetchResult[*corev1.EventList]
	inferenceServicePods   *controllerutils.FetchResult[*corev1.PodList]
	hpa                    controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler]
	httpRoute              controllerutils.FetchResult[*gatewayapiv1.HTTPRoute]
	gateway                controllerutils.FetchResult[*gatewayapiv1.Gateway]
	templateCache          controllerutils.FetchResult[*aimv1alpha1.AIMTemplateCache]

	// adapterDeps holds the per-adapter artifacts, staging Jobs, and resolved
	// base-model artifact for spec.adapters. Populated only when the service
	// declares adapters. See internal/aimadapter.
	adapterDeps aimadapter.Dependencies
}

// FetchRemoteState fetches all resources needed for AIMService reconciliation.
// - Always fetch: InferenceService, HTTPRoute, TemplateCache (for health visibility)
// - Fetch when ISVC not found OR successfully fetched: Model, Template (for both creation and update)
// - Skip on transient ISVC fetch errors: Model, Template (to avoid accidental SSA re-applies)
func (r *ServiceReconciler) FetchRemoteState(
	ctx context.Context,
	c client.Client,
	reconcileCtx controllerutils.ReconcileContext[*aimv1alpha1.AIMService],
) ServiceFetchResult {
	service := reconcileCtx.Object
	logger := log.FromContext(ctx).WithValues(
		"phase", "fetch",
		"service", service.Name,
		"namespace", service.Namespace,
	)
	ctx = log.IntoContext(ctx, logger)

	runtimeConfigRef := service.GetRuntimeConfigRef()
	result := ServiceFetchResult{
		service:             service,
		mergedRuntimeConfig: aimruntimeconfig.FetchMergedRuntimeConfig(ctx, c, runtimeConfigRef.Name, service.Namespace),
	}

	// 1. Fetch existing InferenceService first (gates other fetches)
	result.inferenceService = fetchInferenceService(ctx, c, service)

	// 1b. Fetch events and pods for InferenceService to detect configuration errors
	if result.inferenceService.OK() && result.inferenceService.Value != nil {
		result.inferenceServiceEvents = fetchInferenceServiceEvents(ctx, c, result.inferenceService.Value)

		// Fetch predictor pods to detect ImagePull errors, pending states, etc.
		isvc := result.inferenceService.Value
		podsFetchResult := controllerutils.FetchList(ctx, c, &corev1.PodList{},
			client.InNamespace(isvc.Namespace),
			client.MatchingLabels{constants.LabelKServeInferenceService: isvc.Name},
		)
		result.inferenceServicePods = &podsFetchResult

		// Fetch HPA to get replica status (KEDA creates HPA with name: keda-hpa-{isvc-name}-predictor)
		result.hpa = fetchHPA(ctx, c, isvc)
	}

	// 2. Fetch HTTPRoute if routing might be enabled (we own this, always check)
	result.httpRoute = fetchHTTPRoute(ctx, c, service, result.mergedRuntimeConfig.Value)

	// 2b. Fetch the parent Gateway so the host-pinning guard can see how many
	// listeners it exposes (a multi-listener gateway requires a hostname pin).
	result.gateway = fetchGateway(ctx, c, service, result.mergedRuntimeConfig.Value)

	// 3. Fetch TemplateCache (always fetch - cascades health from Artifact/PVC)
	// artifact status is resolved through TemplateCache.Status.Artifacts
	result.templateCache = fetchTemplateCache(ctx, c, service)

	// 4. Fetch Model and Template for both creation and update of the InferenceService.
	// Mutable fields (replicas, autoscaling, env, resources, etc.) must propagate to an
	// existing ISVC via SSA, so we always resolve upstream resources when the ISVC fetch
	// succeeded (OK) or when the ISVC doesn't exist yet (NotFound).
	// Skip only on transient fetch errors to avoid re-resolving with stale data, which
	// could cause SSA to update an existing resource unintentionally.
	if result.inferenceService.IsNotFound() || result.inferenceService.OK() {
		logger.V(1).Info("Fetching upstream resources",
			"isvcExists", result.inferenceService.OK(),
			"isvcNotFound", result.inferenceService.IsNotFound(),
		)

		// Resolve model (handles ref, image, and custom modes)
		result.modelResult = fetchModel(ctx, c, service)

		// Resolve template (explicit or auto-select)
		result.template, result.clusterTemplate, result.templateSelection = fetchTemplate(
			ctx, c, service, result.modelResult.Model, result.modelResult.ClusterModel,
		)
	} else {
		logger.V(1).Info("Transient error fetching InferenceService, skipping upstream fetch to avoid accidental changes")
	}

	// Adapter staging dependencies (spec.adapters). The parent model artifact is
	// resolved via the template cache's resolved artifacts (keyed on the
	// template's first model id); the remaining staging mechanics are shared via
	// internal/aimadapter.
	if aimadapter.IsActive(service) {
		parentName, parentErr := resolveAdapterParentName(result)
		result.adapterDeps = aimadapter.Fetch(ctx, c, service, parentName)
		result.adapterDeps.ParentResolutionErr = parentErr
	}

	return result
}

// GetComponentHealth returns health status for each component.
// NOTE: Unlike other controllers where this is on FetchResult, AIMService defines it on
// ServiceObservation because model health depends on derived state (needsModelCreation)
// computed in ComposeState. The template/isvc/cache health helpers remain on ServiceFetchResult
// and are accessible via embedding.
func (obs ServiceObservation) GetComponentHealth(ctx context.Context, clientset kubernetes.Interface) []controllerutils.ComponentHealth {
	var health []controllerutils.ComponentHealth

	// Model health (on ServiceObservation - needs needsModelCreation)
	health = append(health, obs.getModelHealth())

	// Template health
	health = append(health, obs.getTemplateHealth())

	// Runtime config health (optional upstream dependency)
	if obs.mergedRuntimeConfig.Value != nil || obs.mergedRuntimeConfig.Error != nil {
		health = append(health, obs.mergedRuntimeConfig.ToUpstreamComponentHealth(
			"RuntimeConfig",
			func(cfg *aimv1alpha1.AIMRuntimeConfigCommon) controllerutils.ComponentHealth {
				return controllerutils.ComponentHealth{
					State:  constants.AIMStatusReady,
					Reason: "RuntimeConfigResolved",
				}
			},
		))
	}

	// InferenceService health (downstream)
	if obs.inferenceService.Value != nil || obs.inferenceService.Error != nil {
		health = append(health, obs.getInferenceServiceHealth())
	}

	// InferenceService pod health (downstream). Under scale-to-zero an
	// empty pod list is the desired state, so we report Ready instead of
	// the default "no pods is a failure" verdict.
	if podsHealth, ok := inferenceServicePodsHealth(
		ctx, clientset, obs.service, obs.hpa, obs.inferenceServicePods,
	); ok {
		health = append(health, podsHealth)
	}

	// Cache health (if caching is enabled)
	health = append(health, obs.getCacheHealth())

	// HTTPRoute health (if routing is enabled)
	health = append(health, obs.getHTTPRouteHealth())

	// HPA health (if autoscaling is configured)
	health = append(health, obs.getHPAHealth())

	// Adapter staging health (if the service declares adapters)
	if aimadapter.IsActive(obs.service) {
		health = append(health, aimadapter.Health(obs.adapterState, len(obs.service.Spec.Adapters)))
	}

	// Scale-from-zero requires routing; surface the invalid combination as
	// ConfigValid=False instead of letting the service idle to a state it can
	// never wake from.
	if cfg := ScaleToZeroRoutingComponentHealth(obs.service, obs.mergedRuntimeConfig.Value); cfg.Component != "" {
		health = append(health, cfg)
	}

	// Routing on a multi-listener gateway requires a hostname pin; otherwise
	// the route attaches to every listener and can bypass authentication.
	// Surface as ConfigValid=False (the route is also not created).
	if cfg := RoutingHostnameComponentHealth(obs.service, obs.mergedRuntimeConfig.Value, obs.gateway); cfg.Component != "" {
		health = append(health, cfg)
	}

	// Autoscaling configured but no trigger resolves -> ConfigValid=False,
	// rather than stamping autoscalerClass=external with no ScaledObject to
	// enforce the declared bounds.
	if cfg := AutoscalingTriggerComponentHealth(obs.service); cfg.Component != "" {
		health = append(health, cfg)
	}

	return health
}

func (obs ServiceObservation) getModelHealth() controllerutils.ComponentHealth {
	mr := obs.modelResult

	// Check if model needs to be created (downstream dependency - pending state)
	if obs.needsModelCreation {
		message := "Model will be created"
		if mr.ImageURI != "" {
			message = "Model will be created for image " + mr.ImageURI
		} else if mr.CustomSpec != nil && len(mr.CustomSpec.ModelSources) > 0 {
			message = "Custom model will be created for " + mr.CustomSpec.ModelSources[0].ModelID
		}
		return controllerutils.ComponentHealth{
			Component:      "Model",
			State:          constants.AIMStatusPending,
			Reason:         aimv1alpha1.AIMServiceReasonCreatingModel,
			Message:        message,
			DependencyType: controllerutils.DependencyTypeDownstream,
		}
	}

	// Check namespace-scoped model first (check errors before value since Fetch always sets Value)
	// State is explicitly set to Failed for upstream dependency errors (requires user action).
	// Reason/Message are derived from the error via CategorizeError if already wrapped.
	if mr.Model.Error != nil {
		return controllerutils.ComponentHealth{
			Component:      "Model",
			State:          constants.AIMStatusFailed,
			Errors:         []error{mr.Model.Error},
			DependencyType: controllerutils.DependencyTypeUpstream,
		}
	}
	// Check OK() and that model was actually populated (Name != "" guards against empty Fetch result)
	if mr.Model.OK() && mr.Model.Value != nil && mr.Model.Value.Name != "" {
		return evaluateModelStatus(mr.Model.Value.Status.Status, "AIMModel", mr.Model.Value.Name)
	}

	// Check cluster-scoped model
	// State is explicitly set to Failed for upstream dependency errors (requires user action).
	// Reason/Message are derived from the error via CategorizeError if already wrapped.
	if mr.ClusterModel.Error != nil {
		return controllerutils.ComponentHealth{
			Component:      "Model",
			State:          constants.AIMStatusFailed,
			Errors:         []error{mr.ClusterModel.Error},
			DependencyType: controllerutils.DependencyTypeUpstream,
		}
	}
	// Check OK() and that model was actually populated (Name != "" guards against empty Fetch result)
	if mr.ClusterModel.OK() && mr.ClusterModel.Value != nil && mr.ClusterModel.Value.Name != "" {
		return evaluateModelStatus(mr.ClusterModel.Value.Status.Status, "AIMClusterModel", mr.ClusterModel.Value.Name)
	}

	// If InferenceService exists and model was previously resolved, report as ready.
	// When ISVC exists, we skip fetching upstream resources (optimization), so
	// we rely on the resolved reference in status.
	if obs.inferenceService.OK() && obs.service.Status.ResolvedModel != nil {
		return controllerutils.ComponentHealth{
			Component:      "Model",
			State:          constants.AIMStatusReady,
			Reason:         aimv1alpha1.AIMServiceReasonModelResolved,
			Message:        fmt.Sprintf("%s %s is ready", obs.service.Status.ResolvedModel.Scope, obs.service.Status.ResolvedModel.Name),
			DependencyType: controllerutils.DependencyTypeUpstream,
		}
	}

	// No model found
	return controllerutils.ComponentHealth{
		Component:      "Model",
		State:          constants.AIMStatusPending,
		Reason:         aimv1alpha1.AIMServiceReasonModelNotFound,
		Message:        "No model found for service",
		DependencyType: controllerutils.DependencyTypeUpstream,
	}
}

func evaluateModelStatus(status constants.AIMStatus, kind, name string) controllerutils.ComponentHealth {
	health := controllerutils.ComponentHealth{
		Component:      "Model",
		DependencyType: controllerutils.DependencyTypeUpstream,
	}

	switch status {
	case constants.AIMStatusReady:
		health.State = constants.AIMStatusReady
		health.Reason = aimv1alpha1.AIMServiceReasonModelResolved
		health.Message = kind + " " + name + " is ready"
	case constants.AIMStatusPending, constants.AIMStatusProgressing:
		health.State = constants.AIMStatusProgressing
		health.Reason = aimv1alpha1.AIMServiceReasonModelNotReady
		health.Message = kind + " " + name + " is not ready yet"
	case constants.AIMStatusFailed, constants.AIMStatusDegraded:
		health.State = constants.AIMStatusFailed
		health.Reason = aimv1alpha1.AIMServiceReasonModelNotReady
		health.Message = kind + " " + name + " is in failed state"
	default:
		health.State = constants.AIMStatusProgressing
		health.Reason = aimv1alpha1.AIMServiceReasonModelNotReady
		health.Message = kind + " " + name + " status: " + string(status)
	}

	return health
}

func (obs ServiceObservation) getTemplateHealth() controllerutils.ComponentHealth {
	health := controllerutils.ComponentHealth{
		Component:      "Template",
		DependencyType: controllerutils.DependencyTypeUpstream,
	}

	// Check for fetch errors first (Fetch always sets Value, so check errors before OK)
	// State is explicitly set to Failed for upstream dependency errors (requires user action).
	// Reason/Message are derived from the error via CategorizeError if already wrapped.
	if obs.template.Error != nil {
		health.State = constants.AIMStatusFailed
		health.Errors = []error{obs.template.Error}
		return health
	}

	// Check namespace-scoped template (OK() means no error, Name != "" guards against empty Fetch result)
	if obs.template.OK() && obs.template.Value != nil && obs.template.Value.Name != "" {
		return evaluateTemplateStatus(obs.template.Value.Status.Status, "AIMServiceTemplate", obs.template.Value.Name)
	}

	// Check cluster-scoped template (same guards for empty Fetch result)
	if obs.clusterTemplate.OK() && obs.clusterTemplate.Value != nil && obs.clusterTemplate.Value.Name != "" {
		return evaluateTemplateStatus(obs.clusterTemplate.Value.Status.Status, "AIMClusterServiceTemplate", obs.clusterTemplate.Value.Name)
	}

	// Check for selection errors
	// State is explicitly set to Failed for selection errors (requires user action).
	// Reason/Message are derived from the error via CategorizeError.
	if obs.templateSelection != nil {
		if obs.templateSelection.Error != nil {
			health.State = constants.AIMStatusFailed
			health.Errors = []error{obs.templateSelection.Error}
			return health
		}
		if obs.templateSelection.TemplatesExistButNotReady {
			health.State = constants.AIMStatusProgressing
			health.Reason = aimv1alpha1.AIMServiceReasonTemplateNotReady
			health.Message = "Templates exist but are not ready yet"
			return health
		}
		if obs.templateSelection.SelectionReason != "" || obs.templateSelection.SelectionMessage != "" {
			health.State = constants.AIMStatusPending
			health.Reason = obs.templateSelection.SelectionReason
			if health.Reason == "" {
				health.Reason = aimv1alpha1.AIMServiceReasonTemplateNotFound
			}
			health.Message = obs.templateSelection.SelectionMessage
			if health.Message == "" {
				health.Message = "No template found for service"
			}
			return health
		}
	}

	// If InferenceService exists and template was previously resolved, report as ready.
	// When ISVC exists, we skip fetching upstream resources (optimization), so
	// we rely on the resolved reference in status.
	if obs.inferenceService.OK() && obs.service.Status.ResolvedTemplate != nil {
		return controllerutils.ComponentHealth{
			Component:      "Template",
			State:          constants.AIMStatusReady,
			Reason:         aimv1alpha1.AIMServiceReasonResolved,
			Message:        fmt.Sprintf("%s %s is ready", obs.service.Status.ResolvedTemplate.Scope, obs.service.Status.ResolvedTemplate.Name),
			DependencyType: controllerutils.DependencyTypeUpstream,
		}
	}

	// No template found
	health.State = constants.AIMStatusPending
	health.Reason = aimv1alpha1.AIMServiceReasonTemplateNotFound
	health.Message = "No template found for service"
	return health
}

func evaluateTemplateStatus(status constants.AIMStatus, kind, name string) controllerutils.ComponentHealth {
	health := controllerutils.ComponentHealth{
		Component:      "Template",
		DependencyType: controllerutils.DependencyTypeUpstream,
	}

	switch status {
	case constants.AIMStatusReady:
		health.State = constants.AIMStatusReady
		health.Reason = aimv1alpha1.AIMServiceReasonResolved
		health.Message = kind + " " + name + " is ready"
	case constants.AIMStatusPending, constants.AIMStatusProgressing:
		health.State = constants.AIMStatusProgressing
		health.Reason = aimv1alpha1.AIMServiceReasonTemplateNotReady
		health.Message = kind + " " + name + " is not ready yet"
	case constants.AIMStatusNotAvailable:
		health.State = constants.AIMStatusNotAvailable
		health.Reason = aimv1alpha1.AIMServiceReasonTemplateNotReady
		health.Message = kind + " " + name + " is not available (no matching GPUs)"
	case constants.AIMStatusFailed, constants.AIMStatusDegraded:
		health.State = constants.AIMStatusFailed
		health.Reason = aimv1alpha1.AIMServiceReasonTemplateNotReady
		health.Message = kind + " " + name + " is in failed state"
	default:
		health.State = constants.AIMStatusProgressing
		health.Reason = aimv1alpha1.AIMServiceReasonTemplateNotReady
		health.Message = kind + " " + name + " status: " + string(status)
	}

	return health
}

func (f ServiceFetchResult) getInferenceServiceHealth() controllerutils.ComponentHealth {
	health := controllerutils.ComponentHealth{
		Component:      "InferenceService",
		DependencyType: controllerutils.DependencyTypeDownstream,
	}

	// Check if InferenceService exists
	if !f.inferenceService.OK() {
		if f.inferenceService.IsNotFound() {
			health.State = constants.AIMStatusProgressing
			health.Reason = aimv1alpha1.AIMServiceReasonCreatingRuntime
			health.Message = "InferenceService not found"
			return health
		}
		health.State = constants.AIMStatusFailed
		health.Reason = "FetchError"
		health.Message = f.inferenceService.Error.Error()
		health.Errors = []error{f.inferenceService.Error}
		return health
	}

	isvc := f.inferenceService.Value

	// Check for fatal configuration errors in events (e.g., ServerlessModeRejected, InternalError)
	if f.inferenceServiceEvents.OK() && f.inferenceServiceEvents.Value != nil {
		for _, event := range f.inferenceServiceEvents.Value.Items {
			if event.Reason == "ServerlessModeRejected" {
				health.State = constants.AIMStatusFailed
				health.Reason = "ServerlessModeRejected"
				health.Message = "Knative is not available. Configure KServe to use RawDeployment mode."
				return health
			}
			if event.Reason == "InternalError" && event.Type == "Warning" {
				// Skip transient conflict errors - KServe will retry these automatically
				if isTransientKServeError(event.Message) {
					continue
				}
				health.State = constants.AIMStatusFailed
				health.Reason = "InternalError"
				health.Message = event.Message
				return health
			}
		}
	}

	// Check InferenceService conditions
	ready := false
	for _, cond := range isvc.Status.Conditions {
		if cond.Type == "Ready" && cond.Status == "True" {
			ready = true
			break
		}
	}

	if ready {
		health.State = constants.AIMStatusReady
		health.Reason = aimv1alpha1.AIMServiceReasonRuntimeReady
		health.Message = "InferenceService is ready"
		return health
	}

	// If pods are still serving traffic, keep status running.
	if f.inferenceServicePods != nil && f.inferenceServicePods.OK() && f.inferenceServicePods.Value != nil {
		readyPodCount := 0
		for _, pod := range f.inferenceServicePods.Value.Items {
			for _, cond := range pod.Status.Conditions {
				if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
					readyPodCount++
					break
				}
			}
		}
		if readyPodCount > 0 {
			health.State = constants.AIMStatusReady
			health.Reason = aimv1alpha1.AIMServiceReasonRuntimeScaling
			health.Message = fmt.Sprintf("InferenceService has %d ready pod(s), scaling in progress", readyPodCount)
			return health
		}
	}

	health.State = constants.AIMStatusProgressing
	health.Reason = aimv1alpha1.AIMServiceReasonCreatingRuntime
	health.Message = "InferenceService is not ready"
	return health
}

// isTransientKServeError checks if a KServe error message indicates a transient condition
// that will be automatically retried. These should not cause AIMService to fail.
func isTransientKServeError(message string) bool {
	// Conflict errors from optimistic locking during status updates
	// Example: "Operation cannot be fulfilled on inferenceservices.serving.kserve.io: the object has been modified"
	if strings.Contains(message, "the object has been modified") {
		return true
	}
	if strings.Contains(message, "Operation cannot be fulfilled") {
		return true
	}
	return false
}

func (obs ServiceObservation) getHTTPRouteHealth() controllerutils.ComponentHealth {
	return HTTPRouteComponentHealth(obs.service, obs.mergedRuntimeConfig.Value, obs.httpRoute)
}

func (obs ServiceObservation) getHPAHealth() controllerutils.ComponentHealth {
	return hpaComponentHealth(obs.service, obs.hpa, obs.observedPodCount(), obs.isInferenceServiceReady())
}

// hpaComponentHealth is the pipeline-agnostic HPA health verdict shared by the
// template (v1alpha1) and profile (v1alpha2) pipelines. It takes the observed
// predictor pod count and the InferenceService readiness as explicit inputs so
// callers that model their observations differently can reuse the identical
// scale-to-zero semantics. HPAComponentHealth (exports.go) is the exported
// wrapper.
func hpaComponentHealth(
	service *aimv1alpha1.AIMService,
	hpa controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler],
	podCount int,
	isvcReady bool,
) controllerutils.ComponentHealth {
	health := controllerutils.ComponentHealth{
		Component:      "HPA",
		DependencyType: controllerutils.DependencyTypeDownstream,
	}

	// Check if autoscaling is configured (HPA is expected)
	hasAutoscaling := service.Spec.AutoScaling != nil ||
		service.Spec.MinReplicas != nil ||
		service.Spec.MaxReplicas != nil

	// If autoscaling is not configured, no health check needed
	if !hasAutoscaling {
		return controllerutils.ComponentHealth{}
	}

	// Autoscaling is configured - check if HPA exists
	if hpa.Error != nil {
		if hpa.IsNotFound() {
			// HPA doesn't exist yet - KEDA may still be creating it
			// This is expected during initial deployment, don't fail
			health.State = constants.AIMStatusProgressing
			health.Reason = "HPANotFound"
			health.Message = "Waiting for KEDA to create HorizontalPodAutoscaler"
			return health
		}
		// Other fetch error
		health.State = constants.AIMStatusFailed
		health.Reason = "HPAFetchError"
		health.Message = hpa.Error.Error()
		health.Errors = []error{hpa.Error}
		return health
	}

	// HPA not found (no error but nil value)
	if hpa.Value == nil {
		health.State = constants.AIMStatusProgressing
		health.Reason = "HPANotFound"
		health.Message = "Waiting for KEDA to create HorizontalPodAutoscaler"
		return health
	}

	// HPA exists - check its conditions for operational status
	h := hpa.Value

	// Get HPA conditions
	ableToScale := getHPACondition(h, autoscalingv2.AbleToScale)
	scalingActive := getHPACondition(h, autoscalingv2.ScalingActive)

	// Check ScalingActive condition - indicates if HPA can get metrics and calculate replicas
	if scalingActive == nil || scalingActive.Status != corev1.ConditionTrue {
		// Under scale-to-zero the HPA's ScalingActive signal is about
		// *waking* a sleeping deployment, not about *certifying* a
		// serving one. The activation metric series may legitimately
		// be empty (no traffic yet, user metric not produced by the
		// workload, gateway scrape not run, etc.) for the entire
		// lifetime of a healthy idle service. Pod / ISVC health is the
		// source of truth for liveness here, so we never escalate the
		// HPA to Failed *or* gate readiness on ScalingActive under
		// scale-to-zero. Genuine HPA-side breakage still surfaces via
		// the AbleToScale branch below (ScaleTargetNotReady) and via
		// the HPA-missing branch above (HPANotFound).
		//
		// Three buckets under scale-to-zero, all of which are Ready
		// from the HPA component's perspective:
		//   1. KEDA's authoritative idle reason (ScalingDisabled) -> ScaledToZero.
		//   2. No replicas running, no authoritative signal yet     -> ScaledToZero.
		//   3. Replicas running                                     -> HPAOperational.
		if isScaleToZero(service) {
			// isScaleToZeroIdle is the shared idle predicate (also used by
			// scaledToZeroNow) so the HPA verdict and the InferenceServicePods
			// verdict never disagree under scale-to-zero.
			if isScaleToZeroIdle(scalingActive, podCount) {
				health.State = constants.AIMStatusReady
				health.Reason = aimv1alpha1.AIMServiceReasonScaledToZero
				switch {
				case scalingActive != nil && scalingActive.Reason == hpaReasonScalingDisabled:
					health.Message = "Service is idle: KEDA has scaled the deployment to zero replicas; will scale up when the configured trigger becomes active"
				case scalingActive == nil:
					health.Message = "Scale-to-zero: deployment is idle (no replicas running) and the HPA has not yet emitted a ScalingActive condition"
				default:
					health.Message = fmt.Sprintf("Scale-to-zero: deployment is idle (no replicas running); activation metric series not yet available (%s)", scalingActive.Reason)
				}
				return health
			}
			// Pods running: service is operational regardless of the
			// HPA's metric state. Reason stays HPAOperational so the
			// framework reads HPAReady=True.
			health.State = constants.AIMStatusReady
			health.Reason = "HPAOperational"
			if scalingActive == nil {
				health.Message = "Scale-to-zero: replicas running; HPA has not yet emitted a ScalingActive condition (does not gate readiness)"
			} else {
				health.Message = fmt.Sprintf("Scale-to-zero: replicas running; HPA ScalingActive=%s/%s (does not gate readiness)", scalingActive.Status, scalingActive.Reason)
			}
			return health
		}
		if !isvcReady {
			// Expected during startup - ISVC pods not ready yet, so metrics aren't available
			health.State = constants.AIMStatusProgressing
			health.Reason = "WaitingForMetrics"
			health.Message = "Waiting for InferenceService to be ready before metrics are available"
			return health
		}
		// ISVC is ready but metrics still failing - this indicates a problem
		health.State = constants.AIMStatusFailed
		health.Reason = "MetricsFailed"
		if scalingActive != nil {
			health.Message = fmt.Sprintf("HPA cannot get metrics: %s", scalingActive.Message)
		} else {
			health.Message = "HPA ScalingActive condition not found"
		}
		return health
	}

	// ScalingActive is True - check AbleToScale condition
	if ableToScale != nil && ableToScale.Status != corev1.ConditionTrue {
		// HPA can get metrics but cannot update the scale target
		health.State = constants.AIMStatusProgressing
		health.Reason = "ScaleTargetNotReady"
		health.Message = fmt.Sprintf("HPA cannot update scale target: %s", ableToScale.Message)
		return health
	}

	// Both conditions are healthy (or AbleToScale not present, which is fine)
	health.State = constants.AIMStatusReady
	health.Reason = "HPAOperational"
	health.Message = "HorizontalPodAutoscaler is actively scaling"
	return health
}

// inferenceServicePodsHealth reports the InferenceServicePods component health.
// Under scale-to-zero an empty pod list is the desired state, so it reports
// Ready (ScaledToZero) instead of the default "no pods is a failure" verdict;
// otherwise it defers to the standard pod health inspector. Returns ok=false
// when there is nothing to report (pods were never fetched). Pipeline-agnostic
// so the profile pipeline produces identical pod health.
func inferenceServicePodsHealth(
	ctx context.Context,
	clientset kubernetes.Interface,
	service *aimv1alpha1.AIMService,
	hpa controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler],
	pods *controllerutils.FetchResult[*corev1.PodList],
) (controllerutils.ComponentHealth, bool) {
	if pods == nil {
		return controllerutils.ComponentHealth{}, false
	}

	if scaledToZeroNow(service, hpa, podItemCount(pods)) &&
		pods.OK() && pods.Value != nil && len(pods.Value.Items) == 0 {
		return controllerutils.ComponentHealth{
			Component:      "InferenceServicePods",
			State:          constants.AIMStatusReady,
			Reason:         aimv1alpha1.AIMServiceReasonScaledToZero,
			Message:        "Service is idle: KEDA has scaled the deployment to zero replicas; pods will be created on activity",
			DependencyType: controllerutils.DependencyTypeDownstream,
		}, true
	}

	// Predictor pods are a downstream resource the AIMService owns (controller →
	// InferenceService → Deployment → pod), not a user-referenced upstream. The
	// shared pod inspector classifies an unpullable image as a
	// MissingUpstreamDependency, which the state engine treats as a blocking
	// config error (ConfigValid=False, ShouldApply=false) — wrongly halting
	// unrelated convergence such as adapter subtree GC. Demote it to a downstream
	// dependency so a bad predictor image degrades readiness without blocking
	// apply.
	return controllerutils.DemoteUpstreamDependencyErrors(pods.ToComponentHealthWithContext(
		ctx, clientset, "InferenceServicePods", controllerutils.GetPodsHealth,
	)), true
}

// isInferenceServiceReady checks if the InferenceService has Ready=True condition.
func (obs ServiceObservation) isInferenceServiceReady() bool {
	return inferenceServiceReady(obs.inferenceService)
}

// inferenceServiceReady reports whether the fetched InferenceService has
// Ready=True. Pipeline-agnostic so the profile pipeline can contextualize HPA
// health identically.
func inferenceServiceReady(
	isvc controllerutils.FetchResult[*servingv1beta1.InferenceService],
) bool {
	if isvc.Error != nil || isvc.Value == nil {
		return false
	}
	for _, cond := range isvc.Value.Status.Conditions {
		if cond.Type == "Ready" && cond.Status == "True" {
			return true
		}
	}
	return false
}

// getHPACondition finds a condition by type in the HPA status.
func getHPACondition(hpa *autoscalingv2.HorizontalPodAutoscaler, condType autoscalingv2.HorizontalPodAutoscalerConditionType) *autoscalingv2.HorizontalPodAutoscalerCondition {
	for i := range hpa.Status.Conditions {
		if hpa.Status.Conditions[i].Type == condType {
			return &hpa.Status.Conditions[i]
		}
	}
	return nil
}

// hpaReasonScalingDisabled is the reason emitted by the K8s HPA on
// ScalingActive=False when the target has zero replicas (KEDA drives the
// Deployment directly under scale-to-zero). Not exported by client-go.
const hpaReasonScalingDisabled = "ScalingDisabled"

// isScaleToZero reports whether the user opted into scale-to-zero.
func isScaleToZero(service *aimv1alpha1.AIMService) bool {
	if service == nil || service.Spec.MinReplicas == nil {
		return false
	}
	return *service.Spec.MinReplicas == 0
}

// observedPodCount returns the number of predictor pods observed in the
// last fetch. Returns 0 when pods were not fetched or the fetch failed;
// callers that need to distinguish "no pods" from "unknown" should
// inspect obs.inferenceServicePods directly. Used by getHPAHealth to
// discriminate idle vs. 0->1 warmup under scale-to-zero.
func (obs ServiceObservation) observedPodCount() int {
	return podItemCount(obs.inferenceServicePods)
}

// podItemCount returns the number of pods in a (possibly nil) pod-list fetch
// result, treating a missing or failed fetch as zero. Pipeline-agnostic so the
// profile pipeline can derive the same idle verdict.
func podItemCount(pods *controllerutils.FetchResult[*corev1.PodList]) int {
	if pods == nil || !pods.OK() || pods.Value == nil {
		return 0
	}
	return len(pods.Value.Items)
}

// isScaledToZero reports whether the service is currently idled under
// scale-to-zero. Thin wrapper over scaledToZeroNow using this observation's
// fetched HPA and pod count.
func (obs ServiceObservation) isScaledToZero() bool {
	return scaledToZeroNow(obs.service, obs.hpa, obs.observedPodCount())
}

// scaledToZeroNow reports whether the service is currently idled under
// scale-to-zero -- scale-to-zero is enabled, the HPA is observable, it is not
// actively scaling a running deployment (ScalingActive!=True), and the shared
// isScaleToZeroIdle predicate considers it idle. Used to treat empty pod lists
// as the healthy desired state. Pipeline-agnostic (shared with v1alpha2).
func scaledToZeroNow(
	service *aimv1alpha1.AIMService,
	hpa controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler],
	podCount int,
) bool {
	if !isScaleToZero(service) {
		return false
	}
	if hpa.Value == nil {
		return false
	}
	scalingActive := getHPACondition(hpa.Value, autoscalingv2.ScalingActive)
	// ScalingActive=True means the HPA is actively managing a running
	// deployment -- not idle.
	if scalingActive != nil && scalingActive.Status == corev1.ConditionTrue {
		return false
	}
	return isScaleToZeroIdle(scalingActive, podCount)
}

// isScaleToZeroIdle reports whether a scale-to-zero service is currently idle
// (scaled, or being scaled, to zero replicas). It is the single idle predicate
// shared by getHPAHealth and scaledToZeroNow so the HPA verdict and the
// InferenceServicePods verdict can never disagree.
//
// Callers must have already established that scale-to-zero is enabled and that
// ScalingActive is not True. scalingActive is the HPA's ScalingActive
// condition and may be nil if the HPA has not emitted it yet.
func isScaleToZeroIdle(scalingActive *autoscalingv2.HorizontalPodAutoscalerCondition, podCount int) bool {
	// KEDA's authoritative idle reason: idle regardless of the observed pods.
	if scalingActive != nil && scalingActive.Reason == hpaReasonScalingDisabled {
		return true
	}
	// ScalingActive absent (not emitted yet) or False with a non-authoritative
	// reason (e.g. the activation metric series is not available yet): idle iff
	// no predictor pods are running.
	return podCount == 0
}

func (obs ServiceObservation) getCacheHealth() controllerutils.ComponentHealth {
	health := controllerutils.ComponentHealth{
		Component:      "Cache",
		DependencyType: controllerutils.DependencyTypeDownstream,
	}

	// All caching now goes through template cache (both Shared and Dedicated modes)
	if obs.templateCache.Value != nil {
		readyMsg := getCacheReadyMessage(obs.templateCache.Value)
		switch obs.templateCache.Value.Status.Status {
		case constants.AIMStatusReady:
			health.State = constants.AIMStatusReady
			health.Reason = aimv1alpha1.AIMServiceReasonCacheReady
			health.Message = "Template cache is ready"
		case constants.AIMStatusProgressing:
			health.State = constants.AIMStatusProgressing
			health.Reason = aimv1alpha1.AIMServiceReasonCacheNotReady
			health.Message = readyMsg
		case constants.AIMStatusFailed:
			health.State = constants.AIMStatusFailed
			health.Reason = aimv1alpha1.AIMServiceReasonCacheFailed
			health.Message = readyMsg
		default:
			health.State = constants.AIMStatusProgressing
			health.Reason = aimv1alpha1.AIMServiceReasonCacheCreating
			health.Message = readyMsg
		}
		return health
	}

	// Template cache doesn't exist - behavior depends on caching mode and ISVC state.
	if hasExistingCacheVolumes(obs) {
		cachingMode := obs.service.Spec.GetCachingMode()
		switch cachingMode {
		case aimv1alpha1.CachingModeShared:
			// Shared caches are not owned by the service; deletion is a true loss.
			// The service cannot recreate them - operator or user intervention is needed.
			health.State = constants.AIMStatusDegraded
			health.Reason = aimv1alpha1.AIMServiceReasonCacheLost
			health.Message = "Shared template cache was deleted but InferenceService still references cached storage volumes"
		default:
			// Dedicated caches are owned by the service and will be recreated
			// by the reconciler on the next planning cycle.
			health.State = constants.AIMStatusDegraded
			health.Reason = aimv1alpha1.AIMServiceReasonCacheCreating
			health.Message = "Dedicated template cache is being recreated"
		}
		return health
	}

	// Template cache doesn't exist yet - being created
	health.State = constants.AIMStatusProgressing
	health.Reason = aimv1alpha1.AIMServiceReasonCacheCreating
	health.Message = "Creating template cache"
	return health
}

// getCacheReadyMessage extracts the message from the template cache's Ready
// condition. This propagates the root cause (e.g., quota-blocked artifact)
// up to the AIMService status instead of using generic strings.
func getCacheReadyMessage(cache *aimv1alpha1.AIMTemplateCache) string {
	for _, cond := range cache.Status.Conditions {
		if cond.Type == controllerutils.ConditionTypeReady && cond.Message != "" {
			return cond.Message
		}
	}
	return "Template cache status: " + string(cache.Status.Status)
}

// hasExistingCacheVolumes checks whether the existing InferenceService has
// storage volumes beyond the base shared-memory volume, indicating it was
// configured with cache PVC mounts that may now be orphaned.
func hasExistingCacheVolumes(obs ServiceObservation) bool {
	if obs.inferenceService.Value == nil {
		return false
	}
	for _, v := range obs.inferenceService.Value.Spec.Predictor.Volumes {
		if v.Name != constants.VolumeSharedMemory && v.PersistentVolumeClaim != nil {
			return true
		}
	}
	return false
}

// ============================================================================
// OBSERVATION
// ============================================================================

// ServiceObservation embeds the fetch result and adds derived state.
type ServiceObservation struct {
	ServiceFetchResult

	// needsModelCreation is true when Model.Image is specified but no existing model matches.
	// Derived in ComposeState from the fetch result.
	needsModelCreation bool

	// pendingModelName is the validated model name to create (set when needsModelCreation is true).
	pendingModelName string

	// runtimeStatus captures the computed runtime status including replica counts and resource usage.
	// Derived in ComposeState from the InferenceService and pods.
	runtimeStatus *aimv1alpha1.AIMServiceRuntimeStatus

	// adapterState is the computed adapter staging state (spec.adapters),
	// produced in ComposeState by the shared internal/aimadapter engine.
	adapterState aimadapter.State
}

// ComposeState creates the observation from fetched data, deriving semantic state.
func (r *ServiceReconciler) ComposeState(
	_ context.Context,
	_ controllerutils.ReconcileContext[*aimv1alpha1.AIMService],
	fetch ServiceFetchResult,
) ServiceObservation {
	obs := ServiceObservation{ServiceFetchResult: fetch}

	mr := fetch.modelResult

	// Derive needsModelCreation for Image-based models:
	// When a service specifies Model.Image (image URI) instead of Model.Name (reference),
	// we search for existing models with that image. If no model is found AND no error occurred
	// during search, then we need to create a new AIMModel for this image.
	// The model is created without owner references so it can be shared across services.
	if mr.ImageURI != "" && mr.Model.Value == nil && mr.ClusterModel.Value == nil && mr.Model.Error == nil && mr.ClusterModel.Error == nil {
		// Validate the image URI can generate a valid model name
		modelName, err := GenerateModelName(mr.ImageURI)
		if err != nil {
			// Set validation error on the model result
			obs.modelResult.Model.Error = controllerutils.NewInvalidSpecError(
				aimv1alpha1.AIMServiceReasonInvalidImageReference,
				err.Error(),
				err,
			)
		} else {
			obs.needsModelCreation = true
			obs.pendingModelName = modelName
		}
	}

	// Derive needsModelCreation for Custom models:
	// When a service specifies Model.Custom, we search for existing models matching the spec.
	// If no model is found AND no error occurred during search, create a new AIMModel.
	// Custom models are created with owner references to the AIMService.
	if mr.CustomSpec != nil && mr.Model.Value == nil && mr.ClusterModel.Value == nil && mr.Model.Error == nil && mr.ClusterModel.Error == nil {
		modelName := GenerateCustomModelName(mr.CustomSpec)
		obs.needsModelCreation = true
		obs.pendingModelName = modelName
	}

	// Compute runtime status from InferenceService and pods
	obs.runtimeStatus = ComputeRuntimeStatus(fetch.service, fetch.hpa)

	// Validate and interpret declared adapters (spec.adapters), and keep computing
	// while removed adapters are still being reclaimed (status carries Deleting).
	if aimadapter.IsActive(fetch.service) {
		obs.adapterState = aimadapter.Compose(fetch.service, fetch.adapterDeps)
	}

	return obs
}

// ============================================================================
// PLAN
// ============================================================================

// PlanResources determines what resources need to be created or updated.
func (r *ServiceReconciler) PlanResources(
	ctx context.Context,
	_ controllerutils.ReconcileContext[*aimv1alpha1.AIMService],
	obs ServiceObservation,
) controllerutils.PlanResult {
	logger := log.FromContext(ctx).WithName("plan")
	service := obs.service

	planResult := controllerutils.PlanResult{}

	// 0. Plan model creation if needed (before template check - model can be created independently)
	// Both custom and image-based models are shared (no owner reference)
	// Custom models use service label for reconciliation tracking
	if model := planModel(service, obs); model != nil {
		planResult.ApplyWithoutOwnerRef(model)
	}

	// 1. Plan HTTPRoute if routing is enabled (independent of template resolution)
	// HTTPRoute only needs service + runtime config, not the template
	if route := planHTTPRoute(ctx, service, obs); route != nil {
		planResult.Apply(route)
	}

	// Resolve template up front so planScaledObject can read the merged
	// predictor resources for the memory-aware cooldown. Template-dependent
	// planning below still gates on AIMStatusReady.
	templateName, templateNamespace, templateSpec, templateStatus := obs.getResolvedTemplate()
	_ = templateNamespace // Used for future enhancements

	// 1c. Plan the KEDA ScaledObject. effectiveResources may be nil while
	// the template is still resolving; planScaledObject falls back to a
	// flat cooldown and the next reconcile re-plans idempotently.
	effectiveResources := resolveEffectiveResources(service, templateSpec, templateStatus)
	if so := planScaledObject(ctx, service, effectiveResources); so != nil {
		planResult.Apply(so)
	}

	if templateName == "" {
		logger.V(1).Info("no template resolved, skipping template-dependent resource planning")
		return planResult
	}

	// Check if template is ready
	if templateStatus == nil || templateStatus.Status != constants.AIMStatusReady {
		logger.V(1).Info("template not ready, skipping template-dependent resource planning", "template", templateName)
		return planResult
	}

	// 2. Plan derived template if service has overrides (only for namespace-scoped templates)
	if obs.template.Value != nil {
		if derivedTemplate := planDerivedTemplate(service, templateName, &obs.template.Value.Spec, obs); derivedTemplate != nil {
			planResult.Apply(derivedTemplate)
		}
	}

	// 3. Plan template cache for all caching modes
	// Ownership depends on caching mode:
	// - Shared: no owner reference, cache persists independently
	// - Dedicated: owned by service, garbage collected with it
	if templateCache := planTemplateCache(service, templateName, templateSpec, templateStatus, obs); templateCache != nil {
		cachingMode := service.Spec.GetCachingMode()
		if cachingMode == aimv1alpha1.CachingModeShared {
			// Shared mode: cache persists independently
			planResult.ApplyWithoutOwnerRef(templateCache)
		} else {
			// Dedicated mode: cache is owned by service
			planResult.Apply(templateCache)
		}
	}

	// 4. Plan custom profile ConfigMap (owned by AIMService)
	if v1alpha1utils.HasCustomProfile(templateSpec) {
		yamlBytes, filename, err := v1alpha1utils.AssembleProfileYAML(templateSpec)
		if err != nil {
			logger.Error(err, "failed to assemble custom profile YAML for service")
			// Avoid planning partially-configured runtime resources when profile assembly fails.
			return planResult
		}

		cmName := v1alpha1utils.ServiceCustomProfileConfigMapName(service.Name)
		cm := v1alpha1utils.BuildCustomProfileConfigMap(cmName, service.Namespace, yamlBytes, filename, map[string]string{
			constants.LabelService:      service.Name,
			constants.LabelK8sManagedBy: constants.LabelValueManagedBy,
			constants.LabelK8sComponent: constants.ComponentInference,
		})
		planResult.Apply(cm)
	}

	// 4b. Adapter staging (spec.adapters). Adapters load dynamically, so the ISVC
	// is never gated on downloads: we ensure the per-service subtree exists and
	// stage adapters asynchronously (the aim-runtime hot-loads each as it lands).
	// ISVC *creation* is gated only on the subtree being mountable (config valid
	// + adapter disk resolved + subtree dir present); a config error keeps the
	// ISVC from being created and surfaces via the Adapters component health.
	// Once the ISVC exists, adapter add/remove never blocks its updates.
	// Run the adapter engine while adapters are declared OR still being reclaimed
	// (a removal — including dropping the last adapter — drives its prune Job to
	// completion via status-carried Deleting entries).
	if aimadapter.IsActive(service) {
		aimadapter.Plan(&planResult, service, obs.adapterDeps, obs.adapterState, obs.mergedRuntimeConfig.Value)
	}
	// ISVC creation is gated whenever the service needs the adapter disk —
	// adapters declared, or dynamic mode (which mounts even at zero adapters) —
	// because the read-only subPath mount requires the subtree directory to exist
	// before the pod starts (the aim-runtime errors on a missing subPath). This
	// gate is on the subtree being mountable, never on downloads.
	isvcExists := obs.inferenceService.OK() && obs.inferenceService.Value != nil
	if service.Spec.AdaptersEnabled() {
		if !isvcExists && !obs.adapterState.Ready {
			logger.V(1).Info("Adapter subtree not mountable yet; deferring ISVC creation",
				"configErr", obs.adapterState.ConfigErr,
				"adapterDiskPVC", obs.adapterState.AdapterDiskPVC,
				"subtreeReady", obs.adapterState.SubtreeReady)
			return planResult
		}
	}

	// 5. Plan InferenceService. When the service needs the adapter disk, mount the
	// service's adapter subtree read-only at /adapters — present even at zero
	// adapters in dynamic mode, so add/remove never restarts the pod.
	if isvc := planInferenceService(ctx, service, templateName, templateSpec, templateStatus, obs); isvc != nil {
		switch {
		case aimadapter.PreserveExistingMount(service, obs.adapterState) && isvcExists:
			// Preserve-on-unknown: the adapter disk PVC didn't resolve this cycle.
			// Re-applying would drop the adapter mount and restart the running
			// predictor, so leave the existing ISVC untouched and retry.
			logger.V(1).Info("Adapter disk PVC unresolved; preserving running ISVC adapter wiring")
			if planResult.RequeueAfter == 0 {
				planResult.RequeueAfter = 5 * time.Second
			}
		default:
			if service.Spec.AdaptersEnabled() {
				if isvcObj, ok := isvc.(*servingv1beta1.InferenceService); ok {
					aimadapter.AddVolumeMount(isvcObj, service, obs.adapterState.AdapterDiskPVC)
				}
			}
			planResult.Apply(isvc)
		}
	}

	return planResult
}

// getResolvedTemplate returns the resolved template info from the observation.
// Returns the template name, namespace (empty for cluster templates),
// common spec (works for both namespace and cluster templates), and status.
func (obs ServiceObservation) getResolvedTemplate() (name, namespace string, spec *aimv1alpha1.AIMServiceTemplateSpecCommon, status *aimv1alpha1.AIMServiceTemplateStatus) {
	if obs.template.Value != nil {
		t := obs.template.Value
		return t.Name, t.Namespace, &t.Spec.AIMServiceTemplateSpecCommon, &t.Status
	}
	if obs.clusterTemplate.Value != nil {
		t := obs.clusterTemplate.Value
		return t.Name, "", &t.Spec.AIMServiceTemplateSpecCommon, &t.Status
	}
	return "", "", nil, nil
}

// getResolvedModel returns the resolved model from the observation.
func (obs ServiceObservation) getResolvedModel() (name string, status *aimv1alpha1.AIMModelStatus, isClusterScoped bool) {
	if obs.modelResult.Model.Value != nil {
		return obs.modelResult.Model.Value.Name, &obs.modelResult.Model.Value.Status, false
	}
	if obs.modelResult.ClusterModel.Value != nil {
		return obs.modelResult.ClusterModel.Value.Name, &obs.modelResult.ClusterModel.Value.Status, true
	}
	return "", nil, false
}

// ============================================================================
// STATUS
// ============================================================================

// DecorateStatus sets domain-specific status fields.
// Resolved references are only set when the upstream resource is Ready.
// This ensures we don't "lock in" a reference until it's actually usable,
// allowing the fetch logic to re-search for better alternatives on subsequent reconciles.
func (r *ServiceReconciler) DecorateStatus(
	status *aimv1alpha1.AIMServiceStatus,
	_ *controllerutils.ConditionManager,
	obs ServiceObservation,
) {
	// Set resolved model reference (only if Ready)
	modelName, modelStatus, isClusterScoped := obs.getResolvedModel()
	if modelName != "" && modelStatus != nil && modelStatus.Status == constants.AIMStatusReady {
		scope := aimv1alpha1.AIMResolutionScopeNamespace
		if isClusterScoped {
			scope = aimv1alpha1.AIMResolutionScopeCluster
		}
		status.ResolvedModel = &aimv1alpha1.AIMResolvedReference{
			Name:  modelName,
			Scope: scope,
		}
		if obs.modelResult.Model.Value != nil {
			status.ResolvedModel.UID = obs.modelResult.Model.Value.UID
		} else if obs.modelResult.ClusterModel.Value != nil {
			status.ResolvedModel.UID = obs.modelResult.ClusterModel.Value.UID
		}
	}

	// Set resolved template reference (only if Ready)
	templateName, templateNamespace, _, templateStatus := obs.getResolvedTemplate()
	if templateName != "" && templateStatus != nil && templateStatus.Status == constants.AIMStatusReady {
		scope := aimv1alpha1.AIMResolutionScopeCluster
		if templateNamespace != "" {
			scope = aimv1alpha1.AIMResolutionScopeNamespace
		}
		status.ResolvedTemplate = &aimv1alpha1.AIMResolvedReference{
			Name:      templateName,
			Namespace: templateNamespace,
			Scope:     scope,
		}
		if obs.template.Value != nil {
			status.ResolvedTemplate.UID = obs.template.Value.UID
		} else if obs.clusterTemplate.Value != nil {
			status.ResolvedTemplate.UID = obs.clusterTemplate.Value.UID
		}
	}

	// Set cache status (only if Ready)
	if obs.templateCache.Value != nil && obs.templateCache.Value.Status.Status == constants.AIMStatusReady {
		status.Cache = &aimv1alpha1.AIMServiceCacheStatus{
			TemplateCacheRef: &aimv1alpha1.AIMResolvedReference{
				Name:      obs.templateCache.Value.Name,
				Namespace: obs.templateCache.Value.Namespace,
				UID:       obs.templateCache.Value.UID,
			},
		}
	}

	// Set routing status
	if obs.httpRoute.Value != nil {
		// TODO: Extract path from HTTPRoute
		status.Routing = &aimv1alpha1.AIMServiceRoutingStatus{}
	}

	// Set runtime status (replica counts and resource usage)
	if obs.runtimeStatus != nil {
		status.Runtime = obs.runtimeStatus
	}

	// Mirror adapter staging state (spec.adapters) and any in-flight removals.
	if aimadapter.IsActive(obs.service) {
		aimadapter.DecorateStatus(status, obs.adapterState)
	}
}
