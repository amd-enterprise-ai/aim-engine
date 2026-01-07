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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)


// TemplateFetchResult holds the result of fetching/resolving a template for the service.
type TemplateFetchResult struct {
	Template        controllerutils.FetchResult[*aimv1alpha1.AIMServiceTemplate]
	ClusterTemplate controllerutils.FetchResult[*aimv1alpha1.AIMClusterServiceTemplate]
}

// fetchTemplate resolves the template for the service.
// It handles explicit template references and auto-selection.
// Uses resolved reference only if the template is still Ready; otherwise re-resolves.
// Returns the template for health/status visibility, even if not Ready.
func fetchTemplate(
	ctx context.Context,
	c client.Client,
	service *aimv1alpha1.AIMService,
	model controllerutils.FetchResult[*aimv1alpha1.AIMModel],
	clusterModel controllerutils.FetchResult[*aimv1alpha1.AIMClusterModel],
) (
	controllerutils.FetchResult[*aimv1alpha1.AIMServiceTemplate],
	controllerutils.FetchResult[*aimv1alpha1.AIMClusterServiceTemplate],
	*TemplateSelectionResult,
) {
	logger := log.FromContext(ctx)

	// Try to use previously resolved template if Ready
	if result, shouldContinue := tryFetchResolvedTemplate(ctx, c, service); !shouldContinue {
		return result.Template, result.ClusterTemplate, nil
	}

	var templateResult controllerutils.FetchResult[*aimv1alpha1.AIMServiceTemplate]
	var clusterTemplateResult controllerutils.FetchResult[*aimv1alpha1.AIMClusterServiceTemplate]

	// Case 1: Explicit template name specified
	if service.Spec.Template.Name != "" {
		templateName := strings.TrimSpace(service.Spec.Template.Name)
		logger.V(1).Info("looking up template by name", "templateName", templateName)

		// Check for derived template (service has overrides)
		finalTemplateName := generateDerivedTemplateName(templateName, service.Spec.Overrides)
		if finalTemplateName != templateName {
			logger.V(1).Info("using derived template name", "derivedName", finalTemplateName)
		}

		// Try namespace-scoped first
		templateResult = controllerutils.Fetch(ctx, c, client.ObjectKey{
			Namespace: service.Namespace,
			Name:      finalTemplateName,
		}, &aimv1alpha1.AIMServiceTemplate{})

		if templateResult.OK() {
			return templateResult, clusterTemplateResult, nil
		}

		if !templateResult.IsNotFound() {
			// Real error, not just missing
			return templateResult, clusterTemplateResult, nil
		}

		// Try cluster-scoped (only base name, derived templates are namespace-scoped)
		clusterTemplateResult = controllerutils.Fetch(ctx, c, client.ObjectKey{
			Name: templateName,
		}, &aimv1alpha1.AIMClusterServiceTemplate{})

		if clusterTemplateResult.OK() {
			// Clear the namespace-scoped error since we found a cluster template
			templateResult.Error = nil
			return templateResult, clusterTemplateResult, nil
		}

		if clusterTemplateResult.IsNotFound() {
			// Neither found - report as missing upstream dependency
			templateResult.Error = controllerutils.NewMissingUpstreamDependencyError(
				aimv1alpha1.AIMServiceReasonTemplateNotFound,
				fmt.Sprintf("template %q not found", finalTemplateName),
				nil,
			)
			clusterTemplateResult.Error = nil
		}
		return templateResult, clusterTemplateResult, nil
	}

	// Case 2: Auto-select template based on model
	logger.V(1).Info("auto-selecting template")

	// Get model name for template lookup
	var modelName string
	if model.Value != nil {
		modelName = model.Value.Name
	} else if clusterModel.Value != nil {
		modelName = clusterModel.Value.Name
	}

	if modelName == "" {
		// Can't auto-select without a model
		return templateResult, clusterTemplateResult, nil
	}

	// Perform template auto-selection
	selection := selectTemplateForModel(ctx, c, service, modelName)

	if selection.Error != nil {
		templateResult.Error = selection.Error
		return templateResult, clusterTemplateResult, selection
	}

	if selection.SelectedTemplate != nil {
		templateResult.Value = selection.SelectedTemplate
	} else if selection.SelectedClusterTemplate != nil {
		clusterTemplateResult.Value = selection.SelectedClusterTemplate
	}

	return templateResult, clusterTemplateResult, selection
}

// tryFetchResolvedTemplate attempts to fetch a previously resolved template reference.
// Returns the result and whether to continue with normal resolution.
func tryFetchResolvedTemplate(
	ctx context.Context,
	c client.Client,
	service *aimv1alpha1.AIMService,
) (result TemplateFetchResult, shouldContinue bool) {
	if service.Status.ResolvedTemplate == nil {
		return result, true
	}

	logger := log.FromContext(ctx)
	ref := service.Status.ResolvedTemplate

	switch ref.Scope {
	case aimv1alpha1.AIMResolutionScopeNamespace:
		result.Template = controllerutils.Fetch(ctx, c, ref.NamespacedName(), &aimv1alpha1.AIMServiceTemplate{})
		if result.Template.OK() && result.Template.Value.Status.Status == constants.AIMStatusReady {
			logger.V(1).Info("using resolved template", "name", ref.Name)
			return result, false
		}
		// Not Ready or deleted - log and continue to search
		if result.Template.OK() {
			logger.V(1).Info("resolved template not ready, re-resolving",
				"name", ref.Name, "status", result.Template.Value.Status.Status)
		} else if result.Template.IsNotFound() {
			logger.V(1).Info("resolved template deleted, re-resolving", "name", ref.Name)
		} else {
			return result, false // Real error - stop
		}

	case aimv1alpha1.AIMResolutionScopeCluster:
		result.ClusterTemplate = controllerutils.Fetch(ctx, c, ref.NamespacedName(), &aimv1alpha1.AIMClusterServiceTemplate{})
		if result.ClusterTemplate.OK() && result.ClusterTemplate.Value.Status.Status == constants.AIMStatusReady {
			logger.V(1).Info("using resolved cluster template", "name", ref.Name)
			return result, false
		}
		// Not Ready or deleted - log and continue to search
		if result.ClusterTemplate.OK() {
			logger.V(1).Info("resolved cluster template not ready, re-resolving",
				"name", ref.Name, "status", result.ClusterTemplate.Value.Status.Status)
		} else if result.ClusterTemplate.IsNotFound() {
			logger.V(1).Info("resolved cluster template deleted, re-resolving", "name", ref.Name)
		} else {
			return result, false // Real error - stop
		}
	}

	return TemplateFetchResult{}, true
}

// planDerivedTemplate creates a derived template if the service has overrides.
func planDerivedTemplate(
	service *aimv1alpha1.AIMService,
	templateName string,
	templateSpec *aimv1alpha1.AIMServiceTemplateSpec,
	obs ServiceObservation,
) client.Object {
	// Only create derived template if service has overrides
	if service.Spec.Overrides == nil {
		return nil
	}

	// Check if we already have the derived template
	if obs.template.Value != nil {
		// Template already exists, check if it's our derived template
		if val, ok := obs.template.Value.Labels[constants.LabelKeyOrigin]; ok && val == constants.LabelValueOriginDerived {
			return nil // Already exists
		}
	}

	// Get model name for the derived template
	modelName := ""
	if obs.modelResult.Model.Value != nil {
		modelName = obs.modelResult.Model.Value.Name
	} else if obs.modelResult.ClusterModel.Value != nil {
		modelName = obs.modelResult.ClusterModel.Value.Name
	}

	// Calculate the derived template name
	derivedName := generateDerivedTemplateName(templateName, service.Spec.Overrides)

	return buildDerivedTemplate(service, derivedName, modelName, templateSpec)
}

// buildDerivedTemplate constructs an AIMServiceTemplate for a service with overrides.
// The template inherits from the base spec and applies service-specific customizations.
func buildDerivedTemplate(
	service *aimv1alpha1.AIMService,
	templateName string,
	resolvedModelName string,
	baseSpec *aimv1alpha1.AIMServiceTemplateSpec,
) *aimv1alpha1.AIMServiceTemplate {
	spec := aimv1alpha1.AIMServiceTemplateSpec{}
	if baseSpec != nil {
		spec = *baseSpec.DeepCopy()
	}

	specCommon := spec.AIMServiceTemplateSpecCommon

	// Set model name if not already set
	if specCommon.ModelName == "" {
		specCommon.ModelName = resolvedModelName
	}

	// Set runtime config name
	if rc := strings.TrimSpace(service.Spec.Name); rc != "" {
		specCommon.Name = normalizeRuntimeConfigName(rc)
	} else {
		specCommon.Name = normalizeRuntimeConfigName(specCommon.Name)
	}

	// Apply service overrides
	if service.Spec.Overrides != nil {
		if service.Spec.Overrides.Metric != nil {
			metric := *service.Spec.Overrides.Metric
			specCommon.Metric = &metric
		}
		if service.Spec.Overrides.Precision != nil {
			precision := *service.Spec.Overrides.Precision
			specCommon.Precision = &precision
		}
		if service.Spec.Overrides.GpuSelector != nil {
			selector := *service.Spec.Overrides.GpuSelector
			specCommon.GpuSelector = &selector
		}
	}

	spec.AIMServiceTemplateSpecCommon = specCommon

	// Inherit env vars from base template
	spec.Env = utils.CopyEnvVars(spec.Env)

	// Copy image pull secrets from service or inherit from base
	if len(service.Spec.ImagePullSecrets) > 0 {
		spec.ImagePullSecrets = utils.CopyPullSecrets(service.Spec.ImagePullSecrets)
	} else {
		spec.ImagePullSecrets = utils.CopyPullSecrets(spec.ImagePullSecrets)
	}

	// Copy resources from service or inherit from base
	if service.Spec.Resources != nil {
		spec.Resources = service.Spec.Resources.DeepCopy()
	} else if spec.Resources != nil {
		spec.Resources = spec.Resources.DeepCopy()
	}

	// Set caching config based on service
	cachingMode := service.Spec.GetCachingMode()
	if cachingMode == aimv1alpha1.CachingModeAlways {
		spec.Caching = &aimv1alpha1.AIMTemplateCachingConfig{
			Enabled: true,
		}
	} else if spec.Caching != nil {
		spec.Caching = spec.Caching.DeepCopy()
	}

	template := &aimv1alpha1.AIMServiceTemplate{
		TypeMeta: metav1.TypeMeta{
			APIVersion: aimv1alpha1.GroupVersion.String(),
			Kind:       "AIMServiceTemplate",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      templateName,
			Namespace: service.Namespace,
			Labels: map[string]string{
				constants.LabelK8sManagedBy: constants.LabelValueManagedBy,
				constants.LabelKeyOrigin:    constants.LabelValueOriginDerived,
			},
			OwnerReferences: []metav1.OwnerReference{},
		},
		Spec: spec,
	}

	return template
}

// generateDerivedTemplateName creates a unique, deterministic name for a derived template.
// It uses the base template name and service overrides to generate a hash suffix,
// ensuring that:
// 1. Same base template + overrides always produce the same name (deterministic)
// 2. Different overrides produce different names (collision-resistant)
// 3. The name is a valid Kubernetes resource name
//
// Example: base template "my-template" with metric=latency, precision=fp16
// -> "my-template-ovr-a1b2c3d4"
func generateDerivedTemplateName(baseTemplateName string, overrides *aimv1alpha1.AIMServiceOverrides) string {
	if overrides == nil {
		return baseTemplateName
	}

	// Build hash inputs from overrides in deterministic order
	hashInputs := buildOverrideHashInputs(overrides)
	if len(hashInputs) == 0 {
		return baseTemplateName
	}

	// Use GenerateDerivedName with the "ovr" suffix convention
	name, err := utils.GenerateDerivedName([]string{baseTemplateName, "ovr"}, hashInputs...)
	if err != nil {
		// Fall back to base name if generation fails (shouldn't happen with valid inputs)
		return baseTemplateName
	}
	return name
}

// buildOverrideHashInputs creates a slice of hash inputs from service overrides.
// The order is deterministic to ensure consistent hashing.
func buildOverrideHashInputs(overrides *aimv1alpha1.AIMServiceOverrides) []any {
	if overrides == nil {
		return nil
	}

	var inputs []any
	if overrides.Metric != nil {
		inputs = append(inputs, "metric", string(*overrides.Metric))
	}
	if overrides.Precision != nil {
		inputs = append(inputs, "precision", string(*overrides.Precision))
	}
	if overrides.GpuSelector != nil {
		if overrides.GpuSelector.Model != "" {
			inputs = append(inputs, "gpu", overrides.GpuSelector.Model)
		}
		if overrides.GpuSelector.Count > 0 {
			inputs = append(inputs, "count", overrides.GpuSelector.Count)
		}
	}
	return inputs
}

// normalizeRuntimeConfigName returns the runtime config name or "default" if empty.
func normalizeRuntimeConfigName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return constants.DefaultRuntimeConfigName
	}
	return name
}
