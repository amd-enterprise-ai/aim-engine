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
	"crypto/sha256"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

const templateNameMaxLength = 63

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
				"app.kubernetes.io/managed-by": constants.LabelValueManagedBy,
				constants.LabelDerivedTemplate: "true",
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

	extra := "-ovr-" + suffix
	maxBaseLen := templateNameMaxLength - len(extra)
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
		return "default"
	}
	return name
}
