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

	// Model resolution (namespace-scoped first, then cluster-scoped)
	model        controllerutils.FetchResult[*aimv1alpha1.AIMModel]
	clusterModel controllerutils.FetchResult[*aimv1alpha1.AIMClusterModel]

	// Template resolution
	template        controllerutils.FetchResult[*aimv1alpha1.AIMServiceTemplate]
	clusterTemplate controllerutils.FetchResult[*aimv1alpha1.AIMClusterServiceTemplate]

	// Template selection results (when auto-selecting)
	templateSelection *TemplateSelectionResult

	// Existing downstream resources
	inferenceService controllerutils.FetchResult[*servingv1beta1.InferenceService]
	httpRoute        controllerutils.FetchResult[*gatewayapiv1.HTTPRoute]
	templateCache    controllerutils.FetchResult[*aimv1alpha1.AIMTemplateCache]
	pvc              controllerutils.FetchResult[*corev1.PersistentVolumeClaim]

	// Model caches (for template cache)
	modelCaches controllerutils.FetchResult[*aimv1alpha1.AIMModelCacheList]
}

// FetchRemoteState fetches all resources needed for AIMService reconciliation.
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

	// 1. Resolve model (handles ref, image, and custom modes)
	result.model, result.clusterModel = fetchModel(ctx, c, r.Clientset, service)

	// 2. Resolve template (explicit or auto-select)
	result.template, result.clusterTemplate, result.templateSelection = fetchTemplate(
		ctx, c, service, result.model, result.clusterModel,
	)

	// 3. Fetch existing InferenceService
	result.inferenceService = fetchInferenceService(ctx, c, service)

	// 4. Fetch HTTPRoute if routing might be enabled
	result.httpRoute = fetchHTTPRoute(ctx, c, service, reconcileCtx.MergedRuntimeConfig.Value)

	// 5. Fetch TemplateCache
	result.templateCache = fetchTemplateCache(ctx, c, service, result.template, result.clusterTemplate)

	// 6. Fetch ModelCaches if template cache exists
	if result.templateCache.OK() && result.templateCache.Value != nil {
		result.modelCaches = fetchModelCaches(ctx, c, service.Namespace)
	}

	// 7. Fetch PVC if not using template cache
	if !result.templateCache.OK() || result.templateCache.Value == nil {
		result.pvc = fetchServicePVC(ctx, c, service)
	}

	return result
}

// GetComponentHealth returns health status for each component.
func (f ServiceFetchResult) GetComponentHealth() []controllerutils.ComponentHealth {
	var health []controllerutils.ComponentHealth

	// Model health
	health = append(health, f.getModelHealth())

	// Template health
	health = append(health, f.getTemplateHealth())

	// Runtime config health (optional upstream dependency)
	if f.mergedRuntimeConfig.Value != nil || f.mergedRuntimeConfig.Error != nil {
		health = append(health, f.mergedRuntimeConfig.ToUpstreamComponentHealth(
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
	if f.inferenceService.Value != nil || f.inferenceService.Error != nil {
		health = append(health, f.getInferenceServiceHealth())
	}

	// Cache health (if caching is enabled)
	health = append(health, f.getCacheHealth())

	return health
}

func (f ServiceFetchResult) getModelHealth() controllerutils.ComponentHealth {
	// Check namespace-scoped model first
	if f.model.Value != nil {
		return evaluateModelStatus(f.model.Value.Status.Status, "AIMModel", f.model.Value.Name)
	}
	if f.model.Error != nil {
		return controllerutils.ComponentHealth{
			Component:      "Model",
			State:          constants.AIMStatusFailed,
			Reason:         aimv1alpha1.AIMServiceReasonModelNotFound,
			Message:        f.model.Error.Error(),
			Errors:         []error{f.model.Error},
			DependencyType: controllerutils.DependencyTypeUpstream,
		}
	}

	// Check cluster-scoped model
	if f.clusterModel.Value != nil {
		return evaluateModelStatus(f.clusterModel.Value.Status.Status, "AIMClusterModel", f.clusterModel.Value.Name)
	}
	if f.clusterModel.Error != nil {
		return controllerutils.ComponentHealth{
			Component:      "Model",
			State:          constants.AIMStatusFailed,
			Reason:         aimv1alpha1.AIMServiceReasonModelNotFound,
			Message:        f.clusterModel.Error.Error(),
			Errors:         []error{f.clusterModel.Error},
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

func (f ServiceFetchResult) getTemplateHealth() controllerutils.ComponentHealth {
	health := controllerutils.ComponentHealth{
		Component:      "Template",
		DependencyType: controllerutils.DependencyTypeUpstream,
	}

	// Check namespace-scoped template first
	if f.template.Value != nil {
		return evaluateTemplateStatus(f.template.Value.Status.Status, "AIMServiceTemplate", f.template.Value.Name)
	}

	// Check cluster-scoped template
	if f.clusterTemplate.Value != nil {
		return evaluateTemplateStatus(f.clusterTemplate.Value.Status.Status, "AIMClusterServiceTemplate", f.clusterTemplate.Value.Name)
	}

	// Check for selection errors
	if f.templateSelection != nil {
		if f.templateSelection.Error != nil {
			health.State = constants.AIMStatusFailed
			health.Reason = aimv1alpha1.AIMServiceReasonTemplateSelectionFailed
			health.Message = f.templateSelection.Error.Error()
			health.Errors = []error{f.templateSelection.Error}
			return health
		}
		if f.templateSelection.TemplatesExistButNotReady {
			health.State = constants.AIMStatusProgressing
			health.Reason = aimv1alpha1.AIMServiceReasonTemplateNotReady
			health.Message = "Templates exist but are not ready yet"
			return health
		}
	}

	// Check for fetch errors
	if f.template.Error != nil {
		health.State = constants.AIMStatusFailed
		health.Reason = aimv1alpha1.AIMServiceReasonTemplateNotFound
		health.Message = f.template.Error.Error()
		health.Errors = []error{f.template.Error}
		return health
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

	if f.inferenceService.Error != nil {
		health.State = constants.AIMStatusFailed
		health.Reason = aimv1alpha1.AIMServiceReasonRuntimeFailed
		health.Message = f.inferenceService.Error.Error()
		health.Errors = []error{f.inferenceService.Error}
		return health
	}

	if f.inferenceService.Value == nil {
		health.State = constants.AIMStatusProgressing
		health.Reason = aimv1alpha1.AIMServiceReasonCreatingRuntime
		health.Message = "InferenceService not yet created"
		return health
	}

	// Check InferenceService conditions
	isvc := f.inferenceService.Value
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
	} else {
		health.State = constants.AIMStatusProgressing
		health.Reason = aimv1alpha1.AIMServiceReasonCreatingRuntime
		health.Message = "InferenceService is not ready"
	}

	return health
}

func (f ServiceFetchResult) getCacheHealth() controllerutils.ComponentHealth {
	health := controllerutils.ComponentHealth{
		Component:      "Cache",
		DependencyType: controllerutils.DependencyTypeDownstream,
	}

	cachingMode := f.service.Spec.GetCachingMode()

	// If caching is disabled, cache is always ready
	if cachingMode == aimv1alpha1.CachingModeNever {
		health.State = constants.AIMStatusReady
		health.Reason = aimv1alpha1.AIMServiceReasonCacheReady
		health.Message = "Caching disabled"
		return health
	}

	// Check template cache
	if f.templateCache.Value != nil {
		switch f.templateCache.Value.Status.Status {
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
			health.Message = "Template cache status: " + string(f.templateCache.Value.Status.Status)
		}
		return health
	}

	// No template cache - check PVC for fallback storage
	if f.pvc.Value != nil {
		if f.pvc.Value.Status.Phase == corev1.ClaimBound {
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

// ServiceObservation embeds the fetch result. The observation phase is minimal
// since FetchResult.GetComponentHealth() handles health derivation and PlanResources
// uses the fetched data directly for planning decisions.
type ServiceObservation struct {
	ServiceFetchResult
}

// ComposeState creates the observation from fetched data.
func (r *ServiceReconciler) ComposeState(
	_ context.Context,
	_ controllerutils.ReconcileContext[*aimv1alpha1.AIMService],
	fetch ServiceFetchResult,
) ServiceObservation {
	return ServiceObservation{ServiceFetchResult: fetch}
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
	if templateCache := planTemplateCache(service, templateName, templateStatus, obs); templateCache != nil {
		planResult.Apply(templateCache)
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
	if obs.model.Value != nil {
		return obs.model.Value.Name, &obs.model.Value.Status, false
	}
	if obs.clusterModel.Value != nil {
		return obs.clusterModel.Value.Name, &obs.clusterModel.Value.Status, true
	}
	return "", nil, false
}

// ============================================================================
// STATUS
// ============================================================================

// DecorateStatus sets domain-specific status fields.
func (r *ServiceReconciler) DecorateStatus(
	status *aimv1alpha1.AIMServiceStatus,
	_ *controllerutils.ConditionManager,
	obs ServiceObservation,
) {
	// Set resolved model reference
	modelName, modelStatus, isClusterScoped := obs.getResolvedModel()
	if modelName != "" {
		scope := aimv1alpha1.AIMResolutionScopeNamespace
		if isClusterScoped {
			scope = aimv1alpha1.AIMResolutionScopeCluster
		}
		status.ResolvedModel = &aimv1alpha1.AIMResolvedReference{
			Name:  modelName,
			Scope: scope,
		}
		if modelStatus != nil {
			if obs.model.Value != nil {
				status.ResolvedModel.UID = obs.model.Value.UID
			} else if obs.clusterModel.Value != nil {
				status.ResolvedModel.UID = obs.clusterModel.Value.UID
			}
		}
	}

	// Set resolved template reference
	templateName, templateNamespace, _, _ := obs.getResolvedTemplate()
	if templateName != "" {
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

	// Set cache status
	if obs.templateCache.Value != nil {
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
