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

	templateName := service.Spec.TemplateRef

	// Only fetch if templateRef is specified but model is not
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

	// Case 1: Service has explicit templateRef with model specified
	if service.Spec.TemplateRef != "" && modelObs.ModelFound {
		obs, err = observeExplicitTemplateWithModel(service, modelObs, modelFetchResult)
	} else if service.Spec.TemplateRef != "" && !modelObs.ModelFound {
		// Case 2: Service has only templateRef (no model) - use template fetch result
		obs, err = observeExplicitTemplateOnly(service, templateFetchResult)
	} else if modelObs.ModelFound {
		// Case 3: No explicit templateRef - select from model's templates
		obs, err = observeAutoSelectTemplate(service, modelObs, modelFetchResult)
	} else {
		// No model and no templateRef - can't resolve template
		obs.TemplateSelectionError = fmt.Errorf("cannot resolve template: no model or templateRef specified")
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
	obs.TemplateName = service.Spec.TemplateRef

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
	obs.TemplateName = service.Spec.TemplateRef

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

// Reference

//
//// HandleTemplateDegraded checks if the template is degraded, not available, or failed and updates status.
//// Returns true if the template is degraded, not available, or failed.
//func HandleTemplateDegraded(
//	status *aimv1alpha1.AIMServiceStatus,
//	obs *aimservicetemplate2.ServiceObservation,
//	setCondition func(conditionType string, conditionStatus metav1.ConditionStatus, reason, message string),
//) bool {
//	if obs.TemplateStatus == nil {
//		return false
//	}
//
//	// Handle Degraded, NotAvailable, and Failed template statuses
//	if obs.TemplateStatus.Status != aimv1alpha1.AIMTemplateStatusDegraded &&
//		obs.TemplateStatus.Status != aimv1alpha1.AIMTemplateStatusNotAvailable &&
//		obs.TemplateStatus.Status != aimv1alpha1.AIMTemplateStatusFailed {
//		return false
//	}
//
//	// Use Failed for terminal failures, Degraded for recoverable issues (including NotAvailable)
//	if obs.TemplateStatus.Status == aimv1alpha1.AIMTemplateStatusFailed {
//		status.Status = aimv1alpha1.AIMServiceStatusFailed
//	} else {
//		status.Status = aimv1alpha1.AIMServiceStatusDegraded
//	}
//
//	templateReason := "TemplateDegraded"
//	templateMessage := "Template is not available"
//
//	// Extract failure details from template conditions
//	for _, cond := range obs.TemplateStatus.Conditions {
//		if cond.Type == "Failure" && cond.Status == metav1.ConditionTrue {
//			if cond.Message != "" {
//				templateMessage = cond.Message
//			}
//			if cond.Reason != "" {
//				templateReason = cond.Reason
//			}
//			break
//		}
//	}
//
//	setCondition(aimv1alpha1.AIMServiceConditionFailure, metav1.ConditionTrue, templateReason, templateMessage)
//	setCondition(aimv1alpha1.AIMServiceConditionRuntimeReady, metav1.ConditionFalse, templateReason,
//		"Cannot create InferenceService due to template issues")
//	setCondition(aimv1alpha1.AIMServiceConditionProgressing, metav1.ConditionFalse, templateReason,
//		"Service cannot proceed due to template issues")
//	setCondition(aimv1alpha1.AIMServiceConditionReady, metav1.ConditionFalse, templateReason,
//		"Service cannot be ready due to template issues")
//	return true
//}
//
//// HandleTemplateNotAvailable checks if the template is not available and updates status.
//// Returns true if the template is not yet available (Pending or Progressing).
//// Sets the service to Pending state because it's waiting for a dependency (the template).
//func HandleTemplateNotAvailable(
//	status *aimv1alpha1.AIMServiceStatus,
//	obs *aimservicetemplate2.ServiceObservation,
//	setCondition func(conditionType string, conditionStatus metav1.ConditionStatus, reason, message string),
//) bool {
//	if obs.TemplateAvailable {
//		return false
//	}
//
//	// Service is Pending because it's waiting for the template to become available.
//	// The template itself may be Progressing (running discovery) or Pending.
//	status.Status = aimv1alpha1.AIMServiceStatusPending
//
//	reason := "TemplateNotAvailable"
//	setCondition(aimv1alpha1.AIMServiceConditionRuntimeReady, metav1.ConditionFalse, reason,
//		fmt.Sprintf("Template %q is not yet Available", obs.TemplateName))
//	setCondition(aimv1alpha1.AIMServiceConditionProgressing, metav1.ConditionTrue, reason,
//		"Waiting for template discovery to complete")
//	setCondition(aimv1alpha1.AIMServiceConditionReady, metav1.ConditionFalse, reason,
//		"Template is not available")
//	return true
//}
//
//// HandleTemplateSelectionFailure reports failures during automatic template selection.
//func HandleTemplateSelectionFailure(
//	status *aimv1alpha1.AIMServiceStatus,
//	obs *aimservicetemplate2.ServiceObservation,
//	setCondition func(conditionType string, conditionStatus metav1.ConditionStatus, reason, message string),
//) bool {
//	if obs == nil || obs.TemplateSelectionReason == "" {
//		return false
//	}
//
//	message := obs.TemplateSelectionMessage
//	if message == "" {
//		message = "Template selection failed"
//	}
//
//	status.Status = aimv1alpha1.AIMServiceStatusFailed
//	reason := obs.TemplateSelectionReason
//	setCondition(aimv1alpha1.AIMServiceConditionFailure, metav1.ConditionTrue, reason, message)
//	setCondition(aimv1alpha1.AIMServiceConditionResolved, metav1.ConditionFalse, reason, message)
//	setCondition(aimv1alpha1.AIMServiceConditionRuntimeReady, metav1.ConditionFalse, reason, "Cannot proceed without a unique template")
//	setCondition(aimv1alpha1.AIMServiceConditionProgressing, metav1.ConditionFalse, reason, "Template selection failed")
//	setCondition(aimv1alpha1.AIMServiceConditionReady, metav1.ConditionFalse, reason, message)
//	return true
//}

//// handleTemplateNotFound handles the case when no template is found.
//// Returns true if this handler applies.
//func handleTemplateNotFound(
//	obs *aimservicetemplate2.ServiceObservation,
//	status *aimv1alpha1.AIMServiceStatus,
//	setCondition func(conditionType string, conditionStatus metav1.ConditionStatus, reason, message string),
//) bool {
//	if obs != nil && obs.TemplateFound() {
//		return false
//	}
//
//	var message string
//	if obs != nil && obs.ShouldCreateTemplate {
//		status.Status = aimv1alpha1.AIMServiceStatusPending
//		message = "Template not found; creating derived template"
//		setCondition(aimv1alpha1.AIMServiceConditionResolved, metav1.ConditionFalse, aimv1alpha1.AIMServiceReasonTemplateNotFound, message)
//		setCondition(aimv1alpha1.AIMServiceConditionRuntimeReady, metav1.ConditionFalse, aimv1alpha1.AIMServiceReasonCreatingRuntime, "Waiting for template creation")
//		setCondition(aimv1alpha1.AIMServiceConditionProgressing, metav1.ConditionTrue, aimv1alpha1.AIMServiceReasonTemplateNotFound, "Waiting for template to be created")
//	} else if obs != nil && obs.TemplatesExistButNotReady {
//		// Templates exist but aren't Available yet - service should wait
//		status.Status = aimv1alpha1.AIMServiceStatusPending
//		message = "Waiting for templates to become Available"
//		setCondition(aimv1alpha1.AIMServiceConditionResolved, metav1.ConditionFalse, "TemplateNotAvailable", message)
//		setCondition(aimv1alpha1.AIMServiceConditionRuntimeReady, metav1.ConditionFalse, "TemplateNotAvailable", "Waiting for template discovery to complete")
//		setCondition(aimv1alpha1.AIMServiceConditionProgressing, metav1.ConditionTrue, "TemplateNotAvailable", "Templates exist but are not yet Available")
//	} else {
//		// No template could be resolved and no derived template will be created.
//		// This is a degraded state - the service cannot proceed.
//		status.Status = aimv1alpha1.AIMServiceStatusDegraded
//		if obs != nil {
//			switch {
//			case obs.TemplateSelectionMessage != "":
//				message = obs.TemplateSelectionMessage
//			case obs.BaseTemplateName == "":
//				message = "No template reference specified and no templates are available for the selected image. Provide spec.templateRef or create templates for the image."
//			default:
//				message = fmt.Sprintf("Template %q not found. Create the template or verify the template name.", obs.BaseTemplateName)
//			}
//		} else {
//			message = "Template not found"
//		}
//		setCondition(aimv1alpha1.AIMServiceConditionFailure, metav1.ConditionTrue, aimv1alpha1.AIMServiceReasonTemplateNotFound, message)
//		setCondition(aimv1alpha1.AIMServiceConditionResolved, metav1.ConditionFalse, aimv1alpha1.AIMServiceReasonTemplateNotFound, message)
//		setCondition(aimv1alpha1.AIMServiceConditionRuntimeReady, metav1.ConditionFalse, aimv1alpha1.AIMServiceReasonTemplateNotFound, "Referenced template does not exist")
//		setCondition(aimv1alpha1.AIMServiceConditionProgressing, metav1.ConditionFalse, aimv1alpha1.AIMServiceReasonTemplateNotFound, "Cannot proceed without template")
//	}
//	setCondition(aimv1alpha1.AIMServiceConditionReady, metav1.ConditionFalse, aimv1alpha1.AIMServiceReasonTemplateNotFound, "Template missing")
//	return true
//}
//

//
//// HandleMissingModelSource checks if the template is available but has no model sources.
//// Returns true if model sources are missing (discovery succeeded but produced no usable sources).
//func HandleMissingModelSource(
//	status *aimv1alpha1.AIMServiceStatus,
//	obs *aimservicetemplate2.ServiceObservation,
//	setCondition func(conditionType string, conditionStatus metav1.ConditionStatus, reason, message string),
//) bool {
//	if !obs.TemplateAvailable || obs.TemplateStatus == nil {
//		return false
//	}
//
//	// Check if template is Available but has no model sources
//	hasModelSources := len(obs.TemplateStatus.ModelSources) > 0
//	if hasModelSources {
//		return false
//	}
//
//	status.Status = aimv1alpha1.AIMServiceStatusDegraded
//	reason := "NoModelSources"
//	message := fmt.Sprintf("Template %q is Available but discovery produced no usable model sources", obs.TemplateName)
//
//	setCondition(aimv1alpha1.AIMServiceConditionFailure, metav1.ConditionTrue, reason, message)
//	setCondition(aimv1alpha1.AIMServiceConditionRuntimeReady, metav1.ConditionFalse, reason,
//		"Cannot create InferenceService without model sources")
//	setCondition(aimv1alpha1.AIMServiceConditionProgressing, metav1.ConditionFalse, reason,
//		"Service is degraded due to missing model sources")
//	setCondition(aimv1alpha1.AIMServiceConditionReady, metav1.ConditionFalse, reason,
//		"Service cannot be ready without model sources")
//	return true
//}
//
