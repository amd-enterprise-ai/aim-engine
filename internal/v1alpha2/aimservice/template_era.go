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
	"strings"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

// templateOwnedConditionTypes are the AIMService conditions that only the
// AIMServiceTemplate pipeline sets.
var templateOwnedConditionTypes = []string{
	"ModelReady",
	"TemplateReady",
	"CacheReady",
	controllerutils.ConditionTypeMigrationRequired,
}

// clearTemplateEraStatus removes the status that only the AIMServiceTemplate
// pipeline writes. It must run before the profile status is decorated: a
// template-era resolvedModel names an AIMModel, while the profile pipeline
// records the resolved AIMProfile's model ID there, so it is cleared together
// with the rest of the template-era state and set again from the profile.
func clearTemplateEraStatus(status *aimv1alpha1.AIMServiceStatus, cm *controllerutils.ConditionManager) {
	if cm == nil {
		return
	}
	hasTemplateCache := status.Cache != nil && status.Cache.TemplateCacheRef != nil
	hasTemplateEraStatus := hasTemplateCache || status.ResolvedTemplate != nil
	for _, conditionType := range templateOwnedConditionTypes {
		if cm.Get(conditionType) != nil {
			hasTemplateEraStatus = true
			cm.Delete(conditionType)
		}
	}
	if !hasTemplateEraStatus {
		return
	}

	status.ResolvedTemplate = nil
	status.ResolvedModel = nil
	if hasTemplateCache {
		status.Cache = nil
	}
}

// setTemplateFieldsIgnoredCondition warns about spec fields that only the
// AIMServiceTemplate pipeline reads. The profile pipeline ignores them.
func setTemplateFieldsIgnoredCondition(cm *controllerutils.ConditionManager, spec aimv1alpha1.AIMServiceSpec) {
	if cm == nil {
		return
	}
	var fields []string
	if spec.Template != nil {
		fields = append(fields, "spec.template")
	}
	if spec.Overrides != nil {
		fields = append(fields, "spec.overrides")
	}
	if spec.Model != nil && spec.Model.Custom != nil {
		fields = append(fields, "spec.model.custom")
	}
	if len(fields) == 0 {
		cm.Delete(controllerutils.ConditionTypeTemplateFieldsIgnored)
		return
	}
	cm.MarkTrue(
		controllerutils.ConditionTypeTemplateFieldsIgnored,
		controllerutils.ReasonTemplateFieldsSet,
		"These fields have no effect on a profile-based AIMService and can be removed: "+strings.Join(fields, ", "),
		controllerutils.AsWarning(),
	)
}
