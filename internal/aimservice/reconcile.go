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

	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

// ServiceReconciler implements the domain logic for AIMService reconciliation.
type ServiceReconciler struct {
	Clientset kubernetes.Interface
	Scheme    *runtime.Scheme
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
	httpRoute              controllerutils.FetchResult[*gatewayapiv1.HTTPRoute]
	templateCache          controllerutils.FetchResult[*aimv1alpha1.AIMTemplateCache]
	pvc                    controllerutils.FetchResult[*corev1.PersistentVolumeClaim]

	// Model caches (for template cache)
	modelCaches controllerutils.FetchResult[*aimv1alpha1.AIMModelCacheList]
}

// FetchRemoteState fetches all resources needed for AIMService reconciliation.
// Fetching is optimized based on whether the InferenceService already exists:
// - Always fetch: InferenceService, HTTPRoute, TemplateCache (for health visibility)
// - Conditionally fetch: Model, Template (only if ISVC doesn't exist or needs recreation)
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

	result := ServiceFetchResult{
		service:             service,
		mergedRuntimeConfig: reconcileCtx.MergedRuntimeConfig,
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
	}

	// 2. Fetch HTTPRoute if routing might be enabled (we own this, always check)
	result.httpRoute = fetchHTTPRoute(ctx, c, service, reconcileCtx.MergedRuntimeConfig.Value)

	// 3. Fetch TemplateCache (always fetch - cascades health from ModelCache/PVC)
	result.templateCache = fetchTemplateCache(ctx, c, service)

	// 4. Fetch ModelCaches if template cache exists
	if result.templateCache.OK() && result.templateCache.Value != nil {
		result.modelCaches = fetchModelCaches(ctx, c, service.Namespace)
	}

	// 5. Fetch service PVC when needed:
	// - When caching is Never: always need the service's temp PVC for downloads
	// - When caching is Auto: need PVC as fallback if template cache is not ready
	// - When caching is Always: don't need service PVC (use template cache)
	cachingMode := service.Spec.GetCachingMode()
	needsServicePVC := cachingMode == aimv1alpha1.CachingModeNever ||
		(cachingMode == aimv1alpha1.CachingModeAuto && (result.templateCache.Value == nil || result.templateCache.Value.Status.Status != constants.AIMStatusReady))

	if needsServicePVC {
		result.pvc = fetchServicePVC(ctx, c, service)
	}

	// 6. Only fetch Model and Template if InferenceService needs to be (re)created.
	// Once the ISVC exists, the config is baked in and we don't need these upstream resources.
	if !result.inferenceService.OK() {
		logger.V(1).Info("InferenceService not found, fetching upstream resources")

		// Resolve model (handles ref, image, and custom modes)
		result.modelResult = fetchModel(ctx, c, service)

		// Resolve template (explicit or auto-select)
		result.template, result.clusterTemplate, result.templateSelection = fetchTemplate(
			ctx, c, service, result.modelResult.Model, result.modelResult.ClusterModel,
		)
	} else {
		logger.V(1).Info("InferenceService exists, skipping upstream resource fetch")
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

	// InferenceService pod health (downstream) - for ImagePull errors, pending states, etc.
	if obs.inferenceServicePods != nil {
		health = append(health, obs.inferenceServicePods.ToComponentHealthWithContext(
			ctx, clientset, "InferenceServicePods", controllerutils.GetPodsHealth,
		))
	}

	// Cache health (if caching is enabled)
	health = append(health, obs.getCacheHealth())

	return health
}

func (obs ServiceObservation) getModelHealth() controllerutils.ComponentHealth {
	mr := obs.modelResult

	// Check if model needs to be created (downstream dependency - pending state)
	if obs.needsModelCreation {
		return controllerutils.ComponentHealth{
			Component:      "Model",
			State:          constants.AIMStatusPending,
			Reason:         aimv1alpha1.AIMServiceReasonCreatingModel,
			Message:        "Model will be created for image " + mr.ImageURI,
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

func (obs ServiceObservation) getCacheHealth() controllerutils.ComponentHealth {
	health := controllerutils.ComponentHealth{
		Component:      "Cache",
		DependencyType: controllerutils.DependencyTypeDownstream,
	}

	cachingMode := obs.service.Spec.GetCachingMode()

	// If caching is disabled, cache is always ready
	if cachingMode == aimv1alpha1.CachingModeNever {
		health.State = constants.AIMStatusReady
		health.Reason = aimv1alpha1.AIMServiceReasonCacheReady
		health.Message = "Caching disabled"
		return health
	}

	// Check for storage size calculation errors (invalid template configuration)
	if obs.storageSizeError != nil {
		health.State = constants.AIMStatusFailed
		health.Reason = aimv1alpha1.AIMServiceReasonStorageSizeError
		health.Message = obs.storageSizeError.Error()
		health.Errors = []error{obs.storageSizeError}
		return health
	}

	// Check template cache
	if obs.templateCache.Value != nil {
		switch obs.templateCache.Value.Status.Status {
		case constants.AIMStatusReady:
			health.State = constants.AIMStatusReady
			health.Reason = aimv1alpha1.AIMServiceReasonCacheReady
			health.Message = "Template cache is available"
		case constants.AIMStatusProgressing:
			health.State = constants.AIMStatusProgressing
			health.Reason = aimv1alpha1.AIMServiceReasonCacheNotReady
			health.Message = "Template cache is progressing"
		case constants.AIMStatusFailed:
			health.State = constants.AIMStatusFailed
			health.Reason = aimv1alpha1.AIMServiceReasonCacheFailed
			health.Message = "Template cache failed"
		default:
			health.State = constants.AIMStatusProgressing
			health.Reason = aimv1alpha1.AIMServiceReasonCacheCreating
			health.Message = "Template cache status: " + string(obs.templateCache.Value.Status.Status)
		}
		return health
	}

	// No template cache - check PVC for fallback storage
	if obs.pvc.Value != nil {
		if obs.pvc.Value.Status.Phase == corev1.ClaimBound {
			health.State = constants.AIMStatusReady
			health.Reason = aimv1alpha1.AIMServiceReasonStorageReady
			health.Message = "Service PVC is bound"
		} else {
			health.State = constants.AIMStatusProgressing
			health.Reason = aimv1alpha1.AIMServiceReasonPVCNotBound
			health.Message = "Service PVC is not bound yet"
		}
		return health
	}

	// For Auto mode, cache is optional
	if cachingMode == aimv1alpha1.CachingModeAuto {
		health.State = constants.AIMStatusReady
		health.Reason = aimv1alpha1.AIMServiceReasonCacheReady
		health.Message = "No cache available, using download mode"
		return health
	}

	// For Always mode, cache is required
	health.State = constants.AIMStatusProgressing
	health.Reason = aimv1alpha1.AIMServiceReasonCacheCreating
	health.Message = "Waiting for cache to be created"
	return health
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

	// storageSizeError is set when storage size calculation fails due to missing model source sizes.
	// This indicates the template hasn't fully resolved its model sources yet.
	storageSizeError error
}

// ComposeState creates the observation from fetched data, deriving semantic state.
func (r *ServiceReconciler) ComposeState(
	_ context.Context,
	_ controllerutils.ReconcileContext[*aimv1alpha1.AIMService],
	fetch ServiceFetchResult,
) ServiceObservation {
	obs := ServiceObservation{ServiceFetchResult: fetch}

	// Derive needsModelCreation: When a service specifies Model.Image (image URI) instead of
	// Model.Name (reference), we search for existing models with that image. If no model is found
	// AND no error occurred during search, then we need to create a new AIMModel for this image.
	// The model is created without owner references so it can be shared across services.
	mr := fetch.modelResult
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

	// Validate storage size can be calculated when template has model sources
	// This catches configuration issues early (model sources without sizes)
	obs.storageSizeError = r.validateStorageSize(fetch)

	return obs
}

// validateStorageSize checks if storage size can be calculated for the template's model sources.
// Returns an error if model sources exist but have invalid/missing sizes.
func (r *ServiceReconciler) validateStorageSize(fetch ServiceFetchResult) error {
	// Get template status with model sources
	var templateStatus *aimv1alpha1.AIMServiceTemplateStatus
	if fetch.template.Value != nil {
		templateStatus = &fetch.template.Value.Status
	} else if fetch.clusterTemplate.Value != nil {
		templateStatus = &fetch.clusterTemplate.Value.Status
	}

	// No template or no model sources - nothing to validate
	if templateStatus == nil || len(templateStatus.ModelSources) == 0 {
		return nil
	}

	// Template cache already exists and is ready - no need to calculate
	if fetch.templateCache.Value != nil && fetch.templateCache.Value.Status.Status == constants.AIMStatusReady {
		return nil
	}

	// PVC already exists - no need to calculate
	if fetch.pvc.Value != nil {
		return nil
	}

	// Try to calculate storage size
	headroomPercent := resolvePVCHeadroomPercent(fetch.service, ServiceObservation{ServiceFetchResult: fetch})
	_, err := calculateRequiredStorageSize(templateStatus.ModelSources, headroomPercent)
	return err
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
	if model := planModel(service, obs); model != nil {
		planResult.ApplyWithoutOwnerRef(model)
	}

	// Get resolved template info
	templateName, templateNamespace, templateNsSpec, templateStatus := obs.getResolvedTemplate()
	_ = templateNamespace // Used for future enhancements
	if templateName == "" {
		logger.V(1).Info("no template resolved, skipping resource planning")
		return planResult
	}

	// Check if template is ready
	if templateStatus == nil || templateStatus.Status != constants.AIMStatusReady {
		logger.V(1).Info("template not ready, skipping resource planning", "template", templateName)
		return planResult
	}

	// 1. Plan derived template if service has overrides (only for namespace-scoped templates)
	if templateNsSpec != nil {
		if derivedTemplate := planDerivedTemplate(service, templateName, templateNsSpec, obs); derivedTemplate != nil {
			planResult.Apply(derivedTemplate)
		}
	}

	// 2. Plan template cache if caching is enabled
	// Use ApplyWithoutOwnerRef so caches can be shared across services and outlive the creating service
	if templateCache := planTemplateCache(service, templateName, templateStatus, obs); templateCache != nil {
		planResult.ApplyWithoutOwnerRef(templateCache)
	}

	// 3. Plan PVC if no template cache available
	if pvc := planServicePVC(service, templateName, templateStatus, obs); pvc != nil {
		planResult.Apply(pvc)
	}

	// 4. Plan InferenceService
	if isvc := planInferenceService(ctx, service, templateName, templateNsSpec, templateStatus, obs); isvc != nil {
		planResult.Apply(isvc)
	}

	// 5. Plan HTTPRoute if routing is enabled
	if route := planHTTPRoute(service, obs); route != nil {
		planResult.Apply(route)
	}

	return planResult
}

// getResolvedTemplate returns the resolved template info from the observation.
// Returns the template name, namespace (empty for cluster templates),
// namespace-scoped spec (nil for cluster templates), and status.
func (obs ServiceObservation) getResolvedTemplate() (name, namespace string, nsSpec *aimv1alpha1.AIMServiceTemplateSpec, status *aimv1alpha1.AIMServiceTemplateStatus) {
	if obs.template.Value != nil {
		t := obs.template.Value
		return t.Name, t.Namespace, &t.Spec, &t.Status
	}
	if obs.clusterTemplate.Value != nil {
		t := obs.clusterTemplate.Value
		return t.Name, "", nil, &t.Status
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
}
