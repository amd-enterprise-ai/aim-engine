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

package aimmodel

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/aimservicetemplate"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

// ==============================
// FETCH
// ==============================

type clusterModelServiceTemplateFetchResult struct {
	clusterServiceTemplates []aimv1alpha1.AIMClusterServiceTemplate
}

func fetchClusterModelServiceTemplateResult(ctx context.Context, c client.Client, clusterModel aimv1alpha1.AIMClusterModel) (clusterModelServiceTemplateFetchResult, error) {
	logger := log.FromContext(ctx).WithValues("function", "fetchClusterModelServiceTemplateResult")
	result := clusterModelServiceTemplateFetchResult{}

	utils.Debug(logger, "Fetching cluster service templates")

	var templates aimv1alpha1.AIMClusterServiceTemplateList
	templatesErr := c.List(ctx, &templates,
		client.MatchingFields{aimv1alpha1.ServiceTemplateModelNameIndexKey: clusterModel.Name},
	)
	if templatesErr != nil {
		logger.Error(templatesErr, "Failed to list cluster service templates")
		return result, fmt.Errorf("failed to fetch cluster service templates: %w", templatesErr)
	}
	result.clusterServiceTemplates = templates.Items
	utils.Debug(logger, "Fetched cluster service templates", "count", len(templates.Items))
	return result, nil
}

type modelServiceTemplateFetchResult struct {
	serviceTemplates []aimv1alpha1.AIMServiceTemplate
}

func fetchModelServiceTemplateResult(ctx context.Context, c client.Client, model aimv1alpha1.AIMModel) (modelServiceTemplateFetchResult, error) {
	logger := log.FromContext(ctx).WithValues("function", "fetchModelServiceTemplateResult")
	result := modelServiceTemplateFetchResult{}

	utils.Debug(logger, "Fetching service templates")

	var templates aimv1alpha1.AIMServiceTemplateList
	templatesErr := c.List(ctx, &templates,
		client.InNamespace(model.Namespace),
		client.MatchingFields{aimv1alpha1.ServiceTemplateModelNameIndexKey: model.Name},
	)
	if templatesErr != nil {
		logger.Error(templatesErr, "Failed to list service templates")
		return result, fmt.Errorf("failed to fetch service templates: %w", templatesErr)
	}
	result.serviceTemplates = templates.Items
	utils.Debug(logger, "Fetched service templates", "count", len(templates.Items))
	return result, nil
}

// ==============================
// OBSERVE
// ==============================

type clusterModelServiceTemplateObservation struct {
	shouldCreateTemplates bool
	existingTemplates     []aimv1alpha1.AIMClusterServiceTemplate
}

func observeClusterModelServiceTemplate(ctx context.Context, fetchResult clusterModelServiceTemplateFetchResult, clusterModel aimv1alpha1.AIMClusterModel, config *aimv1alpha1.AIMRuntimeConfigCommon) clusterModelServiceTemplateObservation {
	logger := log.FromContext(ctx).WithValues("function", "observeClusterModelServiceTemplate")

	shouldCreate := shouldCreateTemplates(clusterModel.Spec, config)

	utils.Debug(logger, "Observing cluster service templates",
		"existingCount", len(fetchResult.clusterServiceTemplates),
		"shouldCreateTemplates", shouldCreate,
	)

	obs := clusterModelServiceTemplateObservation{
		shouldCreateTemplates: shouldCreate,
		existingTemplates:     fetchResult.clusterServiceTemplates,
	}

	return obs
}

type modelServiceTemplateObservation struct {
	shouldCreateTemplates bool
	existingTemplates     []aimv1alpha1.AIMServiceTemplate
}

func observeModelServiceTemplate(ctx context.Context, fetchResult modelServiceTemplateFetchResult, model aimv1alpha1.AIMModel, config *aimv1alpha1.AIMRuntimeConfigCommon) modelServiceTemplateObservation {
	logger := log.FromContext(ctx).WithValues("function", "observeModelServiceTemplate")

	shouldCreate := shouldCreateTemplates(model.Spec, config)

	utils.Debug(logger, "Observing service templates",
		"existingCount", len(fetchResult.serviceTemplates),
		"shouldCreateTemplates", shouldCreate,
	)

	obs := modelServiceTemplateObservation{
		shouldCreateTemplates: shouldCreate,
		existingTemplates:     fetchResult.serviceTemplates,
	}

	return obs
}

func shouldCreateTemplates(modelSpec aimv1alpha1.AIMModelSpec, config *aimv1alpha1.AIMRuntimeConfigCommon) bool {
	// Only disable template creation if it is explicitly disabled in the referenced (or default) config
	if config != nil {
		if autoDiscovery := config.Model.AutoDiscovery; autoDiscovery != nil && !*autoDiscovery {
			return false
		}
	}

	// Check if autoCreateTemplates is disabled in the model spec
	if discovery := modelSpec.Discovery; discovery != nil && !*discovery.AutoCreateTemplates {
		return false
	}

	return true
}

// ==============================
// PLAN
// ==============================

// templateBuilderOutputs contains the common parts built by buildTemplateComponents
type templateBuilderOutputs struct {
	Name   string
	Labels map[string]string
	Spec   aimv1alpha1.AIMServiceTemplateSpecCommon
}

func planClusterModelServiceTemplates(ctx context.Context, templateObs clusterModelServiceTemplateObservation, metadataObs modelMetadataObservation, clusterModel aimv1alpha1.AIMClusterModel) []client.Object {
	logger := log.FromContext(ctx).WithValues("function", "planClusterModelServiceTemplates")

	var templates []client.Object

	utils.Debug(logger, "Planning cluster service templates",
		"shouldCreateTemplates", templateObs.shouldCreateTemplates,
		"hasMetadataError", metadataObs.Error != nil,
		"hasExtractedMetadata", metadataObs.ExtractedMetadata != nil,
		"existingTemplateCount", len(templateObs.existingTemplates),
	)

	if !templateObs.shouldCreateTemplates {
		utils.Debug(logger, "Skipping template creation: shouldCreateTemplates is false")
		return templates
	}

	if metadataObs.Error != nil {
		utils.Debug(logger, "Skipping template creation: metadata has error", "error", metadataObs.Error)
		return templates
	}

	if metadataObs.ExtractedMetadata == nil {
		utils.Debug(logger, "Skipping template creation: no extracted metadata")
		return templates
	}

	recommendedCount := len(metadataObs.ExtractedMetadata.Model.RecommendedDeployments)
	utils.Debug(logger, "Creating templates from metadata", "recommendedDeploymentCount", recommendedCount)

	for _, recommendedDeployment := range metadataObs.ExtractedMetadata.Model.RecommendedDeployments {
		templateComponents := buildTemplateComponents(clusterModel.Name, clusterModel.Spec, recommendedDeployment)
		serviceTemplate := &aimv1alpha1.AIMClusterServiceTemplate{
			ObjectMeta: metav1.ObjectMeta{
				Name:   templateComponents.Name,
				Labels: templateComponents.Labels,
			},
			Spec: aimv1alpha1.AIMClusterServiceTemplateSpec{
				AIMServiceTemplateSpecCommon: templateComponents.Spec,
			},
		}
		templates = append(templates, serviceTemplate)
		utils.Debug(logger, "Planned cluster service template", "templateName", templateComponents.Name)
	}

	utils.Debug(logger, "Finished planning cluster service templates", "templatesPlanned", len(templates))
	return templates
}

func planModelServiceTemplates(ctx context.Context, templateObs modelServiceTemplateObservation, metadataObs modelMetadataObservation, model aimv1alpha1.AIMModel) []client.Object {
	logger := log.FromContext(ctx).WithValues("function", "planModelServiceTemplates")

	var templates []client.Object

	utils.Debug(logger, "Planning service templates",
		"shouldCreateTemplates", templateObs.shouldCreateTemplates,
		"hasMetadataError", metadataObs.Error != nil,
		"hasExtractedMetadata", metadataObs.ExtractedMetadata != nil,
		"existingTemplateCount", len(templateObs.existingTemplates),
	)

	if !templateObs.shouldCreateTemplates {
		utils.Debug(logger, "Skipping template creation: shouldCreateTemplates is false")
		return templates
	}

	if metadataObs.Error != nil {
		utils.Debug(logger, "Skipping template creation: metadata has error", "error", metadataObs.Error)
		return templates
	}

	if metadataObs.ExtractedMetadata == nil {
		utils.Debug(logger, "Skipping template creation: no extracted metadata")
		return templates
	}

	recommendedCount := len(metadataObs.ExtractedMetadata.Model.RecommendedDeployments)
	utils.Debug(logger, "Creating templates from metadata", "recommendedDeploymentCount", recommendedCount)

	for _, recommendedDeployment := range metadataObs.ExtractedMetadata.Model.RecommendedDeployments {
		templateComponents := buildTemplateComponents(model.Name, model.Spec, recommendedDeployment)
		serviceTemplate := &aimv1alpha1.AIMServiceTemplate{
			ObjectMeta: metav1.ObjectMeta{
				Name:      templateComponents.Name,
				Namespace: model.Namespace,
				Labels:    templateComponents.Labels,
			},
			Spec: aimv1alpha1.AIMServiceTemplateSpec{
				AIMServiceTemplateSpecCommon: templateComponents.Spec,
			},
		}
		templates = append(templates, serviceTemplate)
		utils.Debug(logger, "Planned service template", "templateName", templateComponents.Name)
	}

	utils.Debug(logger, "Finished planning service templates", "templatesPlanned", len(templates))
	return templates
}

// buildTemplateComponents builds the common components (name, labels, spec) for a service template
func buildTemplateComponents(modelName string, modelSpec aimv1alpha1.AIMModelSpec, deployment aimv1alpha1.RecommendedDeployment) templateBuilderOutputs {
	// Generate template name using the specified format
	templateName := aimservicetemplate.GenerateTemplateName(modelSpec.Image, deployment)

	// Build common spec
	commonSpec := aimv1alpha1.AIMServiceTemplateSpecCommon{
		ModelName:          modelName,
		ImagePullSecrets:   modelSpec.ImagePullSecrets,
		ServiceAccountName: modelSpec.ServiceAccountName,
	}

	// Set runtime parameters from deployment
	if deployment.Metric != "" {
		metric := aimv1alpha1.AIMMetric(deployment.Metric)
		commonSpec.Metric = &metric
	}
	if deployment.Precision != "" {
		precision := aimv1alpha1.AIMPrecision(deployment.Precision)
		commonSpec.Precision = &precision
	}
	if deployment.GPUModel != "" && deployment.GPUCount > 0 {
		commonSpec.GpuSelector = &aimv1alpha1.AIMGpuSelector{
			Model: deployment.GPUModel,
			Count: deployment.GPUCount,
		}
	}

	// Common labels
	labels := map[string]string{
		constants.LabelKeyAutoGenerated: constants.LabelValueAutoGenerated,
		constants.LabelKeyModelName:     modelName,
	}

	return templateBuilderOutputs{
		Name:   templateName,
		Labels: labels,
		Spec:   commonSpec,
	}
}

// ==============================
// PROJECT
// ==============================

func projectModelStatusFromTemplates(
	status *aimv1alpha1.AIMModelStatus,
	h *controllerutils.StatusHelper,
	templateStatuses []aimv1alpha1.AIMServiceTemplateStatus,
) {
	if status == nil || len(templateStatuses) == 0 {
		// Leave as pending until there are templates
		return
	}

	var ready, progressing, degradedOrFailed, notAvailable int
	for _, templateStatus := range templateStatuses {
		switch templateStatus.Status {
		case constants.AIMStatusReady:
			ready++
		case constants.AIMStatusProgressing, constants.AIMStatusPending:
			progressing++
		case constants.AIMStatusDegraded, constants.AIMStatusFailed:
			degradedOrFailed++
		case constants.AIMStatusNotAvailable:
			notAvailable++
		}
	}

	total := len(templateStatuses)

	switch {
	case degradedOrFailed == total:
		// All templates failed
		h.Failed(aimv1alpha1.AIMModelReasonAllTemplatesFailed, fmt.Sprintf("All %d template(s) are degraded or failed", total))
	case notAvailable == total:
		// All templates are not available (e.g., GPU requirements not met)
		h.NotAvailable(aimv1alpha1.AIMModelReasonNoTemplatesAvailable, fmt.Sprintf("None of the %d template(s) are available", total))
	case degradedOrFailed > 0:
		// Some templates failed or degraded
		h.Degraded(aimv1alpha1.AIMModelReasonSomeTemplatesDegraded, fmt.Sprintf("%d of %d template(s) are degraded or failed", degradedOrFailed, total))
	case progressing > 0:
		// Some templates still processing (discovery running, etc.)
		h.Progressing(aimv1alpha1.AIMModelReasonTemplatesProgressing, fmt.Sprintf("%d of %d template(s) are progressing", progressing, total))
	case ready+notAvailable == total && ready > 0:
		// All templates finished processing: some ready, some not available
		h.Ready(aimv1alpha1.AIMModelReasonAllTemplatesReady, fmt.Sprintf("%d of %d template(s) ready (%d not available)", ready, total, notAvailable))
	default:
		// leave as Pending
	}
}
