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
	"crypto/sha256"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
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
		finalTemplateName := templateName
		if service.Spec.Overrides != nil {
			suffix := overridesSuffix(service.Spec.Overrides)
			if suffix != "" {
				finalTemplateName = derivedTemplateName(templateName, suffix)
				logger.V(1).Info("using derived template name", "derivedName", finalTemplateName)
			}
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
	suffix := overridesSuffix(service.Spec.Overrides)
	derivedName := derivedTemplateName(templateName, suffix)

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

// overridesSuffix computes a hash suffix for service overrides.
func overridesSuffix(overrides *aimv1alpha1.AIMServiceOverrides) string {
	if overrides == nil {
		return ""
	}

	// Build a deterministic string from overrides
	var parts []string
	if overrides.Metric != nil {
		parts = append(parts, "metric="+string(*overrides.Metric))
	}
	if overrides.Precision != nil {
		parts = append(parts, "precision="+string(*overrides.Precision))
	}
	if overrides.GpuSelector != nil {
		if overrides.GpuSelector.Model != "" {
			parts = append(parts, "gpu="+overrides.GpuSelector.Model)
		}
		if overrides.GpuSelector.Count > 0 {
			parts = append(parts, fmt.Sprintf("count=%d", overrides.GpuSelector.Count))
		}
	}

	if len(parts) == 0 {
		return ""
	}

	combined := strings.Join(parts, ",")
	sum := sha256.Sum256([]byte(combined))
	return fmt.Sprintf("%x", sum[:])[:8]
}

// derivedTemplateName constructs a template name from a base name and suffix.
// Ensures the final name does not exceed Kubernetes name length limits.
func derivedTemplateName(baseName, suffix string) string {
	if suffix == "" {
		return baseName
	}

	extra := constants.DerivedTemplateSuffix + suffix
	maxBaseLen := constants.TemplateNameMaxLength - len(extra)
	if maxBaseLen <= 0 {
		maxBaseLen = 1
	}

	trimmed := baseName
	if len(trimmed) > maxBaseLen {
		trimmed = strings.TrimRight(trimmed[:maxBaseLen], "-")
		if trimmed == "" {
			trimmed = baseName[:maxBaseLen]
		}
	}

	return fmt.Sprintf("%s%s", trimmed, extra)
}

// normalizeRuntimeConfigName returns the runtime config name or "default" if empty.
func normalizeRuntimeConfigName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return constants.DefaultRuntimeConfigName
	}
	return name
}
