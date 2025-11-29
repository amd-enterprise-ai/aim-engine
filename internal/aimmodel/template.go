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

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/aimservicetemplate"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

// ==============================
// FETCH
// ==============================

type ClusterModelServiceTemplateFetchResult struct {
	ClusterServiceTemplates []aimv1alpha1.AIMClusterServiceTemplate
}

func fetchClusterModelServiceTemplateResult(ctx context.Context, c client.Client, clusterModel aimv1alpha1.AIMClusterModel) (ClusterModelServiceTemplateFetchResult, error) {
	result := ClusterModelServiceTemplateFetchResult{}

	var templates aimv1alpha1.AIMClusterServiceTemplateList
	templatesErr := c.List(ctx, &templates,
		client.MatchingFields{constants.ServiceTemplateModelNameIndexKey: clusterModel.Name},
	)
	if templatesErr != nil {
		return result, fmt.Errorf("failed to fetch cluster service templates: %w", templatesErr)
	}
	result.ClusterServiceTemplates = templates.Items
	return result, nil
}

type ModelServiceTemplateFetchResult struct {
	ServiceTemplates []aimv1alpha1.AIMServiceTemplate
}

func fetchModelServiceTemplateResult(ctx context.Context, c client.Client, model aimv1alpha1.AIMModel) (ModelServiceTemplateFetchResult, error) {
	result := ModelServiceTemplateFetchResult{}

	var templates aimv1alpha1.AIMServiceTemplateList
	templatesErr := c.List(ctx, &templates,
		client.InNamespace(model.Namespace),
		client.MatchingFields{constants.ServiceTemplateModelNameIndexKey: model.Name},
	)
	if templatesErr != nil {
		return result, fmt.Errorf("failed to fetch service templates: %w", templatesErr)
	}
	result.ServiceTemplates = templates.Items
	return result, nil
}

// ==============================
// OBSERVE
// ==============================

type ClusterModelServiceTemplateObservation struct {
	ShouldCreateTemplates bool
	ExistingTemplates     []aimv1alpha1.AIMClusterServiceTemplate
}

func ObserveClusterModelServiceTemplate(fetchResult ClusterModelServiceTemplateFetchResult, clusterModel aimv1alpha1.AIMClusterModel, config *aimv1alpha1.AIMRuntimeConfigCommon) ClusterModelServiceTemplateObservation {
	obs := ClusterModelServiceTemplateObservation{
		ShouldCreateTemplates: shouldCreateTemplates(clusterModel.Spec, config),
		ExistingTemplates:     fetchResult.ClusterServiceTemplates,
	}

	return obs
}

type ModelServiceTemplateObservation struct {
	ShouldCreateTemplates bool
	ExistingTemplates     []aimv1alpha1.AIMServiceTemplate
}

func ObserveModelServiceTemplate(fetchResult ModelServiceTemplateFetchResult, model aimv1alpha1.AIMModel, config *aimv1alpha1.AIMRuntimeConfigCommon) ModelServiceTemplateObservation {
	obs := ModelServiceTemplateObservation{
		ShouldCreateTemplates: shouldCreateTemplates(model.Spec, config),
		ExistingTemplates:     fetchResult.ServiceTemplates,
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

// TemplateBuilderOutputs contains the common parts built by BuildTemplateComponents
type TemplateBuilderOutputs struct {
	Name   string
	Labels map[string]string
	Spec   aimv1alpha1.AIMServiceTemplateSpecCommon
}

func planClusterModelServiceTemplates(templateObs ClusterModelServiceTemplateObservation, metadataObs ModelMetadataObservation, clusterModel aimv1alpha1.AIMClusterModel) []client.Object {
	var templates []client.Object
	if !templateObs.ShouldCreateTemplates || metadataObs.Error != nil || metadataObs.ExtractedMetadata == nil {
		return templates
	}

	for _, recommendedDeployment := range metadataObs.ExtractedMetadata.Model.RecommendedDeployments {
		templateComponents := BuildTemplateComponents(clusterModel.Name, clusterModel.Spec, recommendedDeployment)
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
	}

	return templates
}

func planModelServiceTemplates(templateObs ModelServiceTemplateObservation, metadataObs ModelMetadataObservation, model aimv1alpha1.AIMModel) []client.Object {
	var templates []client.Object
	if !templateObs.ShouldCreateTemplates || metadataObs.Error != nil {
		return templates
	}

	for _, recommendedDeployment := range metadataObs.ExtractedMetadata.Model.RecommendedDeployments {
		templateComponents := BuildTemplateComponents(model.Name, model.Spec, recommendedDeployment)
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
	}

	return templates
}

// BuildTemplateComponents builds the common components (name, labels, spec) for a service template
func BuildTemplateComponents(modelName string, modelSpec aimv1alpha1.AIMModelSpec, deployment aimv1alpha1.RecommendedDeployment) TemplateBuilderOutputs {
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

	return TemplateBuilderOutputs{
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
		case aimv1alpha1.AIMTemplateStatusReady:
			ready++
		case aimv1alpha1.AIMTemplateStatusProgressing, aimv1alpha1.AIMTemplateStatusPending:
			progressing++
		case aimv1alpha1.AIMTemplateStatusDegraded, aimv1alpha1.AIMTemplateStatusFailed:
			degradedOrFailed++
		case aimv1alpha1.AIMTemplateStatusNotAvailable:
			notAvailable++
		}
	}

	total := len(templateStatuses)

	switch {
	case degradedOrFailed == total:
		h.Failed("AllTemplatesFailed", fmt.Sprintf("All %d template(s) are degraded or failed", total))
	case notAvailable == total:
		h.Degraded("NoTemplatesAvailable", fmt.Sprintf("None of the %d template(s) are available", total))
	case degradedOrFailed > 0:
		h.Degraded("SomeTemplatesDegraded", fmt.Sprintf("%d of %d template(s) are degraded or failed", degradedOrFailed, total))
	case progressing > 0:
		h.Progressing("TemplatesProgressing", fmt.Sprintf("%d of %d template(s) are progressing", progressing, total))
	case ready == total:
		h.Ready("AllTemplatesReady", fmt.Sprintf("All %d template(s) have finished processing", total))
	default:
		// leave as Pending
	}
}
