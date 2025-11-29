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

	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

// ============================================================================
// FETCH
// ============================================================================

type ServiceTemplateFetchResult struct {
	// Template fetched from explicit templateRef (when no model specified)
	NamespaceTemplate *aimv1alpha1.AIMServiceTemplate
	ClusterTemplate   *aimv1alpha1.AIMClusterServiceTemplate

	// Model derived from template (when templateRef specified but no model)
	ModelFromTemplate        *aimv1alpha1.AIMModel
	ClusterModelFromTemplate *aimv1alpha1.AIMClusterModel
}

// fetchServiceTemplateResult fetches a template when service has templateRef but no model
// Also fetches the model that the template references
func fetchServiceTemplateResult(ctx context.Context, c client.Client, service *aimv1alpha1.AIMService) (ServiceTemplateFetchResult, error) {
	result := ServiceTemplateFetchResult{}

	templateName := service.Spec.TemplateName

	// Only fetch if templateName is specified but model is not
	if templateName == "" {
		return result, nil
	}

	// Model is specified - templates already fetched in model fetch phase
	if service.Spec.Model.Ref != nil || service.Spec.Model.Image != nil {
		return result, nil
	}

	// Try namespace-scoped template first
	nsTemplate := &aimv1alpha1.AIMServiceTemplate{}
	if err := c.Get(ctx, client.ObjectKey{Name: templateName, Namespace: service.Namespace}, nsTemplate); err != nil && !errors.IsNotFound(err) {
		return result, fmt.Errorf("failed to fetch namespace template: %w", err)
	} else if err == nil {
		result.NamespaceTemplate = nsTemplate

		// Fetch the model referenced by this template
		// Note: We swallow NotFound errors - the template might reference a model that doesn't exist yet
		// This will be handled in the project phase
		if nsTemplate.Spec.ModelName != "" {
			model := &aimv1alpha1.AIMModel{}
			if err := c.Get(ctx, client.ObjectKey{Name: nsTemplate.Spec.ModelName, Namespace: service.Namespace}, model); err == nil {
				result.ModelFromTemplate = model
			}
		}
		return result, nil
	}

	// Try cluster-scoped template
	clusterTemplate := &aimv1alpha1.AIMClusterServiceTemplate{}
	if err := c.Get(ctx, client.ObjectKey{Name: templateName}, clusterTemplate); err != nil && !errors.IsNotFound(err) {
		return result, fmt.Errorf("failed to fetch cluster template: %w", err)
	} else if err == nil {
		result.ClusterTemplate = clusterTemplate

		// Fetch the model referenced by this template
		// Note: We swallow NotFound errors - the template might reference a model that doesn't exist yet
		// This will be handled in the project phase
		if clusterTemplate.Spec.ModelName != "" {
			clusterModel := &aimv1alpha1.AIMClusterModel{}
			if err := c.Get(ctx, client.ObjectKey{Name: clusterTemplate.Spec.ModelName}, clusterModel); err == nil {
				result.ClusterModelFromTemplate = clusterModel
			}
		}
	}

	return result, nil
}

// ============================================================================
// OBSERVE
// ============================================================================

type ServiceTemplateObservation struct {
	TemplateName           string
	TemplateNamespace      string
	TemplateFound          bool
	TemplateAvailable      bool
	TemplateMatchesModel   bool
	TemplateSpec           *aimv1alpha1.AIMServiceTemplateSpec
	TemplateSpecCommon     *aimv1alpha1.AIMServiceTemplateSpecCommon
	TemplateStatus         *aimv1alpha1.AIMServiceTemplateStatus
	ResolvedRuntimeConfig  *aimv1alpha1.AIMResolvedReference
	ResolvedModel          *aimv1alpha1.AIMResolvedReference
	TemplateSelectionError error
	Scope                  aimv1alpha1.AIMResolutionScope
	HasOverrides           bool
}

func observeServiceTemplate(
	service *aimv1alpha1.AIMService,
	modelObs ServiceModelObservation,
	modelFetchResult ServiceModelFetchResult,
	templateFetchResult ServiceTemplateFetchResult,
) (ServiceTemplateObservation, error) {
	var obs ServiceTemplateObservation
	var err error

	// Check if service has overrides
	hasOverrides := service.Spec.Overrides != nil

	// Case 1: Service has explicit templateName with model specified
	if service.Spec.TemplateName != "" && modelObs.ModelFound {
		obs, err = observeExplicitTemplateWithModel(service, modelObs, modelFetchResult)
	} else if service.Spec.TemplateName != "" && !modelObs.ModelFound {
		// Case 2: Service has only templateName (no model) - use template fetch result
		obs, err = observeExplicitTemplateOnly(service, templateFetchResult)
	} else if modelObs.ModelFound {
		// Case 3: No explicit templateName - select from model's templates
		obs, err = observeAutoSelectTemplate(service, modelObs, modelFetchResult)
	} else {
		// No model and no templateName - can't resolve template
		obs.TemplateSelectionError = fmt.Errorf("cannot resolve template: no model or templateName specified")
		return obs, nil
	}

	if err != nil {
		return obs, err
	}

	// Handle overrides if present
	if hasOverrides && obs.TemplateFound {
		obs.HasOverrides = true

		// Check if a template with matching overrides already exists
		matchingTemplate := findTemplateMatchingOverrides(service, modelFetchResult)
		if matchingTemplate != "" {
			// Use the existing template that matches overrides
			obs.TemplateName = matchingTemplate
			populateTemplateDetailsFromModel(&obs, modelFetchResult)
		} else {
			// No matching template - service cannot proceed
			obs.TemplateSelectionError = fmt.Errorf("no template found matching service overrides (metric: %v, precision: %v, gpu: %v)",
				service.Spec.Overrides.Metric,
				service.Spec.Overrides.Precision,
				service.Spec.Overrides.GpuSelector)
			obs.TemplateFound = false
		}
	}

	return obs, nil
}

// observeExplicitTemplateWithModel handles the case where both templateRef and model are specified
//
//nolint:unparam // error return kept for API consistency
func observeExplicitTemplateWithModel(
	service *aimv1alpha1.AIMService,
	modelObs ServiceModelObservation,
	modelFetchResult ServiceModelFetchResult,
) (ServiceTemplateObservation, error) {
	obs := ServiceTemplateObservation{}
	obs.TemplateName = service.Spec.TemplateName

	// Validate that this template belongs to the model
	obs.TemplateMatchesModel = validateTemplateMatchesModel(obs.TemplateName, modelFetchResult)
	if !obs.TemplateMatchesModel {
		obs.TemplateSelectionError = fmt.Errorf("template %q does not belong to model %q", obs.TemplateName, modelObs.ModelName)
		return obs, nil
	}

	// Find and populate template details from model's templates
	populateTemplateDetailsFromModel(&obs, modelFetchResult)
	return obs, nil
}

// observeExplicitTemplateOnly handles the case where only templateRef is specified (no model)
//
//nolint:unparam // error return kept for API consistency
func observeExplicitTemplateOnly(
	service *aimv1alpha1.AIMService,
	templateFetchResult ServiceTemplateFetchResult,
) (ServiceTemplateObservation, error) {
	obs := ServiceTemplateObservation{}
	obs.TemplateName = service.Spec.TemplateName

	// Use the template from fetch result
	if templateFetchResult.NamespaceTemplate != nil {
		populateTemplateDetailsFromFetch(&obs, templateFetchResult.NamespaceTemplate, nil)
	} else if templateFetchResult.ClusterTemplate != nil {
		populateTemplateDetailsFromFetch(&obs, nil, templateFetchResult.ClusterTemplate)
	} else {
		obs.TemplateSelectionError = fmt.Errorf("template %q not found", obs.TemplateName)
	}
	return obs, nil
}

// observeAutoSelectTemplate handles the case where model is specified but no templateRef (auto-select)
//
//nolint:unparam // error return kept for API consistency
func observeAutoSelectTemplate(
	service *aimv1alpha1.AIMService,
	modelObs ServiceModelObservation,
	modelFetchResult ServiceModelFetchResult,
) (ServiceTemplateObservation, error) {
	obs := ServiceTemplateObservation{}

	// TODO: Implement template selection logic
	selectedTemplate, scope, err := selectTemplateForService(service, modelObs, modelFetchResult)
	if err != nil {
		obs.TemplateSelectionError = err
		return obs, nil
	}

	obs.TemplateName = selectedTemplate
	obs.Scope = scope
	populateTemplateDetailsFromModel(&obs, modelFetchResult)
	return obs, nil
}

// validateTemplateMatchesModel checks if a template name exists in the model's templates
func validateTemplateMatchesModel(templateName string, modelFetchResult ServiceModelFetchResult) bool {
	// Check namespace templates
	for _, template := range modelFetchResult.NamespaceTemplatesForModel {
		if template.Name == templateName {
			return true
		}
	}

	// Check cluster templates
	for _, template := range modelFetchResult.ClusterTemplatesForModel {
		if template.Name == templateName {
			return true
		}
	}

	return false
}

// populateTemplateDetailsFromModel finds and populates template details from model's template list
func populateTemplateDetailsFromModel(obs *ServiceTemplateObservation, modelFetchResult ServiceModelFetchResult) {
	// Check namespace templates
	for _, template := range modelFetchResult.NamespaceTemplatesForModel {
		if template.Name == obs.TemplateName {
			obs.TemplateFound = true
			obs.TemplateNamespace = template.Namespace
			obs.TemplateAvailable = template.Status.Status == aimv1alpha1.AIMTemplateStatusReady
			obs.TemplateSpec = template.Spec.DeepCopy()
			obs.TemplateSpecCommon = &template.Spec.AIMServiceTemplateSpecCommon
			obs.TemplateStatus = template.Status.DeepCopy()
			obs.ResolvedRuntimeConfig = template.Status.ResolvedRuntimeConfig
			obs.ResolvedModel = template.Status.ResolvedModel
			obs.Scope = aimv1alpha1.AIMResolutionScopeNamespace
			obs.TemplateMatchesModel = true
			return
		}
	}

	// Check cluster templates
	for _, template := range modelFetchResult.ClusterTemplatesForModel {
		if template.Name == obs.TemplateName {
			obs.TemplateFound = true
			obs.TemplateAvailable = template.Status.Status == aimv1alpha1.AIMTemplateStatusReady
			obs.TemplateSpecCommon = &template.Spec.AIMServiceTemplateSpecCommon
			obs.TemplateSpec = &aimv1alpha1.AIMServiceTemplateSpec{
				AIMServiceTemplateSpecCommon: template.Spec.AIMServiceTemplateSpecCommon,
			}
			obs.TemplateStatus = template.Status.DeepCopy()
			obs.ResolvedRuntimeConfig = template.Status.ResolvedRuntimeConfig
			obs.ResolvedModel = template.Status.ResolvedModel
			obs.Scope = aimv1alpha1.AIMResolutionScopeCluster
			obs.TemplateMatchesModel = true
			return
		}
	}
}

// populateTemplateDetailsFromFetch populates template details from explicit fetch result
func populateTemplateDetailsFromFetch(obs *ServiceTemplateObservation, nsTemplate *aimv1alpha1.AIMServiceTemplate, clusterTemplate *aimv1alpha1.AIMClusterServiceTemplate) {
	if nsTemplate != nil {
		obs.TemplateFound = true
		obs.TemplateNamespace = nsTemplate.Namespace
		obs.TemplateAvailable = nsTemplate.Status.Status == aimv1alpha1.AIMTemplateStatusReady
		obs.TemplateSpec = nsTemplate.Spec.DeepCopy()
		obs.TemplateSpecCommon = &nsTemplate.Spec.AIMServiceTemplateSpecCommon
		obs.TemplateStatus = nsTemplate.Status.DeepCopy()
		obs.ResolvedRuntimeConfig = nsTemplate.Status.ResolvedRuntimeConfig
		obs.ResolvedModel = nsTemplate.Status.ResolvedModel
		obs.Scope = aimv1alpha1.AIMResolutionScopeNamespace
	} else if clusterTemplate != nil {
		obs.TemplateFound = true
		obs.TemplateAvailable = clusterTemplate.Status.Status == aimv1alpha1.AIMTemplateStatusReady
		obs.TemplateSpecCommon = &clusterTemplate.Spec.AIMServiceTemplateSpecCommon
		obs.TemplateSpec = &aimv1alpha1.AIMServiceTemplateSpec{
			AIMServiceTemplateSpecCommon: clusterTemplate.Spec.AIMServiceTemplateSpecCommon,
		}
		obs.TemplateStatus = clusterTemplate.Status.DeepCopy()
		obs.ResolvedRuntimeConfig = clusterTemplate.Status.ResolvedRuntimeConfig
		obs.ResolvedModel = clusterTemplate.Status.ResolvedModel
		obs.Scope = aimv1alpha1.AIMResolutionScopeCluster
	}
}

// ============================================================================
// PROJECT
// ============================================================================

func projectServiceTemplate(
	status *aimv1alpha1.AIMServiceStatus,
	cm *controllerutils.ConditionManager,
	h *controllerutils.StatusHelper,
	obs ServiceTemplateObservation,
) bool {
	if obs.TemplateSelectionError != nil {
		// Check if it's an overrides mismatch error - this is a terminal failure
		if obs.HasOverrides && !obs.TemplateFound {
			h.Failed(aimv1alpha1.AIMServiceReasonValidationFailed, obs.TemplateSelectionError.Error())
			cm.MarkFalse(aimv1alpha1.AIMServiceConditionResolved, aimv1alpha1.AIMServiceReasonValidationFailed, obs.TemplateSelectionError.Error(), controllerutils.LevelWarning)
		} else {
			h.Degraded("TemplateSelectionFailed", obs.TemplateSelectionError.Error())
			cm.MarkFalse(aimv1alpha1.AIMServiceConditionResolved, "TemplateSelectionFailed", obs.TemplateSelectionError.Error(), controllerutils.LevelWarning)
		}
		return true
	}

	if !obs.TemplateFound {
		h.Degraded(aimv1alpha1.AIMServiceReasonTemplateNotFound, fmt.Sprintf("Template %q not found", obs.TemplateName))
		cm.MarkFalse(aimv1alpha1.AIMServiceConditionResolved, aimv1alpha1.AIMServiceReasonTemplateNotFound, fmt.Sprintf("Template %q not found", obs.TemplateName), controllerutils.LevelWarning)
		return true
	}

	if !obs.TemplateMatchesModel {
		h.Degraded(aimv1alpha1.AIMServiceReasonValidationFailed, fmt.Sprintf("Template %q does not belong to the model", obs.TemplateName))
		cm.MarkFalse(aimv1alpha1.AIMServiceConditionResolved, aimv1alpha1.AIMServiceReasonValidationFailed, "Template does not match model", controllerutils.LevelWarning)
		return true
	}

	if !obs.TemplateAvailable {
		h.Progressing("TemplateNotReady", fmt.Sprintf("Template %q is not ready", obs.TemplateName))
		cm.MarkFalse(aimv1alpha1.AIMServiceConditionResolved, "TemplateNotReady", fmt.Sprintf("Template %q status: %s", obs.TemplateName, obs.TemplateStatus.Status), controllerutils.LevelNormal)
		return true
	}

	// Template found and available
	cm.MarkTrue(aimv1alpha1.AIMServiceConditionResolved, aimv1alpha1.AIMServiceReasonResolved, fmt.Sprintf("Using template %q", obs.TemplateName), controllerutils.LevelNormal)
	status.ResolvedTemplate = &aimv1alpha1.AIMResolvedReference{
		Name:      obs.TemplateName,
		Namespace: obs.TemplateNamespace,
		Kind:      "AIMServiceTemplate",
		Scope:     obs.Scope,
	}
	if obs.Scope == aimv1alpha1.AIMResolutionScopeCluster {
		status.ResolvedTemplate.Kind = "AIMClusterServiceTemplate"
	}

	// Set resolved refs from template
	if obs.ResolvedRuntimeConfig != nil {
		status.ResolvedRuntimeConfig = obs.ResolvedRuntimeConfig
	}
	if obs.ResolvedModel != nil {
		status.ResolvedModel = obs.ResolvedModel
	}

	return false
}
