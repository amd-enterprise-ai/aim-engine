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

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/aimruntimeconfig"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

// ============================================================================
// DOMAIN RECONCILER
// ============================================================================

// Reconciler implements the domain reconciliation logic for AIMService.
type Reconciler struct {
	Scheme *runtime.Scheme
}

// ============================================================================
// FETCH PHASE
// ============================================================================

// ServiceFetchResult aggregates all fetched resources for an AIMService.
type ServiceFetchResult struct {
	Model         ServiceModelFetchResult
	Template      ServiceTemplateFetchResult
	Caching       ServiceCachingFetchResult
	PVC           ServicePVCFetchResult
	KServe        ServiceKServeFetchResult
	HTTPRoute     ServiceHTTPRouteFetchResult
	RuntimeConfig aimruntimeconfig.RuntimeConfigFetchResult
}

func (r *Reconciler) Fetch(
	ctx context.Context,
	c client.Client,
	service *aimv1alpha1.AIMService,
) (ServiceFetchResult, error) {
	result := ServiceFetchResult{}

	// 0. Fetch config
	runtimeConfigFetchResult, err := aimruntimeconfig.FetchRuntimeConfig(ctx, c, service.Spec.RuntimeConfigName, service.Namespace)
	if err != nil {
		return result, err
	}
	result.RuntimeConfig = runtimeConfigFetchResult

	// 1. Fetch model
	modelFetchResult, err := fetchServiceModelResult(ctx, c, service)
	if err != nil {
		return result, err
	}
	result.Model = modelFetchResult

	// 2. Fetch template (depends on model for validation)
	templateFetchResult, err := fetchServiceTemplateResult(ctx, c, service)
	if err != nil {
		return result, err
	}
	result.Template = templateFetchResult

	// 3. Fetch caching (depends on template name/namespace)
	templateName, templateNamespace := getResolvedTemplateName(service, templateFetchResult)
	if templateName != "" {
		cachingFetchResult, err := fetchServiceCachingResult(ctx, c, service, templateName, templateNamespace)
		if err != nil {
			return result, err
		}
		result.Caching = cachingFetchResult
	}

	// 4. Fetch PVC
	pvcFetchResult, err := fetchServicePVCResult(ctx, c, service)
	if err != nil {
		return result, err
	}
	result.PVC = pvcFetchResult

	// 5. Fetch KServe InferenceService
	kserveFetchResult, err := fetchServiceKServeResult(ctx, c, service)
	if err != nil {
		return result, err
	}
	result.KServe = kserveFetchResult

	// 6. Fetch HTTPRoute
	httprouteFetchResult, err := fetchServiceHTTPRouteResult(ctx, c, service)
	if err != nil {
		return result, err
	}
	result.HTTPRoute = httprouteFetchResult

	return result, nil
}

// ============================================================================
// OBSERVE PHASE
// ============================================================================

// ServiceObservation aggregates all observed state for an AIMService.
type ServiceObservation struct {
	Model         ServiceModelObservation
	Template      ServiceTemplateObservation
	Caching       ServiceCachingObservation
	PVC           ServicePVCObservation
	KServe        ServiceKServeObservation
	HTTPRoute     ServiceHTTPRouteObservation
	RuntimeConfig aimruntimeconfig.RuntimeConfigObservation

	// MergedConfig is the final merged runtime config including service overrides
	// This is the result of: cluster config → namespace config → service inline config
	MergedConfig aimv1alpha1.AIMRuntimeConfigCommon
}

func (r *Reconciler) Observe(
	ctx context.Context,
	service *aimv1alpha1.AIMService,
	fetchResult ServiceFetchResult,
) (ServiceObservation, error) {
	obs := ServiceObservation{}

	// 0. Observe runtime config
	obs.RuntimeConfig = aimruntimeconfig.ObserveRuntimeConfig(fetchResult.RuntimeConfig, service.Spec.RuntimeConfigName)

	// Only merge if no error
	if obs.RuntimeConfig.Error == nil {
		// Merge with service inline config to get final effective config
		obs.MergedConfig = MergeServiceRuntimeConfig(obs.RuntimeConfig, &service.Spec.AIMServiceRuntimeConfig)
	}

	// 1. Observe model
	obs.Model = observeServiceModel(ctx, nil, service, fetchResult.Model)

	// 2. Observe template
	templateObs, err := observeServiceTemplate(service, obs.Model, fetchResult.Model, fetchResult.Template)
	if err != nil {
		return obs, err
	}
	obs.Template = templateObs

	// 3. Observe caching
	if obs.Template.TemplateSpec != nil {
		obs.Caching = observeServiceCaching(
			fetchResult.Caching,
			service,
			obs.Template.TemplateSpec,
			obs.Template.TemplateStatus,
		)
	}

	// 4. Observe PVC
	obs.PVC = observeServicePVC(fetchResult.PVC, service, obs.Caching)

	// 5. Observe KServe
	obs.KServe = observeServiceKServe(fetchResult.KServe)

	// 6. Observe HTTPRoute
	obs.HTTPRoute = observeServiceHTTPRoute(fetchResult.HTTPRoute, service, &obs.MergedConfig)

	return obs, nil
}

// ============================================================================
// PLAN PHASE
// ============================================================================

func (r *Reconciler) Plan(
	ctx context.Context,
	service *aimv1alpha1.AIMService,
	obs ServiceObservation,
) ([]client.Object, error) {
	var objectsToApply []client.Object

	// Handle cache retry deletions BEFORE planning new objects
	if obs.Caching.ShouldRequestCacheRetry {
		// Note: Cache retry deletion should be handled in a pre-plan phase
		// For now, we document this as a TODO for the controller to handle
		// The controller should delete failed model caches before calling Plan
	}

	// Get template name for resource creation
	_, _ = getResolvedTemplateName(service, ServiceTemplateFetchResult{
		// We don't have fetch result here, so we rely on status
	})

	// TODO: All plan functions need RuntimeConfig inputs
	// Until RuntimeConfig is available, we cannot complete planning

	// 1. Plan caching (TemplateCache creation)
	// Need: service, cachingObs, templateName, templateNamespace, storageClassName
	// if obs.Caching.ShouldCreateCache && templateName != "" {
	// 	cacheObj, err := planServiceCache(service, obs.Caching, templateName, templateNamespace, storageClassName)
	// 	if err != nil {
	// 		return nil, err
	// 	}
	// 	if cacheObj != nil {
	// 		objectsToApply = append(objectsToApply, cacheObj)
	// 	}
	// }

	// 2. Plan PVC (temporary service PVC)
	// Need: service, pvcObs, templateStatus, storageClassName, headroomPercent
	// if obs.PVC.ShouldCreatePVC {
	// 	pvcObj, err := planServicePVC(service, obs.PVC, obs.Template.TemplateStatus, storageClassName, headroomPercent)
	// 	if err != nil {
	// 		return nil, err
	// 	}
	// 	if pvcObj != nil {
	// 		objectsToApply = append(objectsToApply, pvcObj)
	// 	}
	// }

	// 3. Plan KServe InferenceService
	// Need: service, kserveObs, modelImage, modelName, templateName, templateSpec, templateStatus, pvcObs, cachingObs
	// if obs.Model.ModelSpec != nil && obs.Template.TemplateSpec != nil {
	// 	kserveObj, err := planServiceInferenceService(
	// 		service, obs.KServe,
	// 		obs.Model.ModelSpec.Image, obs.Model.ModelName,
	// 		templateName, obs.Template.TemplateSpec, obs.Template.TemplateStatus,
	// 		obs.PVC, obs.Caching,
	// 	)
	// 	if err != nil {
	// 		return nil, err
	// 	}
	// 	if kserveObj != nil {
	// 		objectsToApply = append(objectsToApply, kserveObj)
	// 	}
	// }

	// 4. Plan HTTPRoute
	// Need: service, httprouteObs, isvcName, modelName, templateName
	// if obs.KServe.InferenceServiceExists {
	// 	httprouteObj, err := planServiceHTTPRoute(
	// 		service, obs.HTTPRoute,
	// 		InferenceServiceNameForService(service),
	// 		obs.Model.ModelName, templateName,
	// 	)
	// 	if err != nil {
	// 		return nil, err
	// 	}
	// 	if httprouteObj != nil {
	// 		objectsToApply = append(objectsToApply, httprouteObj)
	// 	}
	// }

	return objectsToApply, nil
}

// ============================================================================
// PROJECT PHASE
// ============================================================================

func (r *Reconciler) Project(
	status *aimv1alpha1.AIMServiceStatus,
	cm *controllerutils.ConditionManager,
	obs ServiceObservation,
) {
	h := controllerutils.NewStatusHelper(status, cm)

	// Project in order, checking for blocking errors after each domain
	// Each project function returns true if it's a blocking error

	// 1. Model projection
	blocking := projectServiceModel(status, cm, h, obs.Model)
	if blocking {
		return
	}

	// 2. Template projection
	blocking = projectServiceTemplate(status, cm, h, obs.Template)
	if blocking {
		return
	}

	// 3. Caching projection
	blocking = projectServiceCaching(status, cm, h, obs.Caching)
	if blocking {
		return // Cache failed after retry - blocking error
	}

	// 4. PVC projection
	blocking = projectServicePVC(status, cm, h, obs.PVC)
	if blocking {
		return
	}

	// 5. KServe projection
	blocking = projectServiceKServe(status, cm, h, obs.KServe)
	if blocking {
		return
	}

	// 6. HTTPRoute projection
	blocking = projectServiceHTTPRoute(status, cm, h, obs.HTTPRoute)
	if blocking {
		return
	}

	// TODO: Set overall service status based on all conditions
	// For now, individual domain projections handle their own status
}

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================

// getResolvedTemplateName extracts the template name and namespace from the template fetch result.
func getResolvedTemplateName(service *aimv1alpha1.AIMService, fetchResult ServiceTemplateFetchResult) (string, string) {
	// Check resolved template from status first
	if service.Status.ResolvedTemplate != nil {
		return service.Status.ResolvedTemplate.Name, service.Status.ResolvedTemplate.Namespace
	}

	// Fall back to fetch result
	if fetchResult.NamespaceTemplate != nil {
		return fetchResult.NamespaceTemplate.Name, fetchResult.NamespaceTemplate.Namespace
	}
	if fetchResult.ClusterTemplate != nil {
		return fetchResult.ClusterTemplate.Name, ""
	}

	return "", ""
}

// ============================================================================
// CACHE RETRY DELETION HANDLER
// ============================================================================

// HandleCacheRetryDeletion deletes failed model caches for retry.
// This should be called by the controller BEFORE calling Plan.
func HandleCacheRetryDeletion(
	ctx context.Context,
	c client.Client,
	obs ServiceObservation,
) error {
	if !obs.Caching.ShouldRequestCacheRetry {
		return nil
	}

	for _, mc := range obs.Caching.FailedModelCachesToRetry {
		if err := c.Delete(ctx, &mc); err != nil && !errors.IsNotFound(err) {
			return err
		}
	}

	return nil
}
