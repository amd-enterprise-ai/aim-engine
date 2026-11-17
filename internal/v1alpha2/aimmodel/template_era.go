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
	"strings"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

// templateEraConditionTypes are the AIMModel and AIMClusterModel conditions
// that only the AIMServiceTemplate-producing model pipeline set. The *Ready
// ones would otherwise keep gating readiness.
var templateEraConditionTypes = []string{
	controllerutils.ConditionTypeMigrationRequired,
	"ServiceTemplatesReady",
	"ClusterServiceTemplatesReady",
	"LegacyAutoCustomSpecReady",
}

func clearTemplateEraStatus(cm *controllerutils.ConditionManager) {
	for _, conditionType := range templateEraConditionTypes {
		cm.Delete(conditionType)
	}
}

// setTemplateFieldsIgnoredCondition warns about spec fields that only
// configured the AIMServiceTemplates a model used to create. createServiceTemplates
// is reported only when false, because the CRD defaults it to true whenever
// spec.discovery is set.
func setTemplateFieldsIgnoredCondition(cm *controllerutils.ConditionManager, spec *aimv1alpha1.AIMModelSpec) {
	var fields []string
	if !spec.Discovery.IsCreateServiceTemplatesEnabled() {
		fields = append(fields, "spec.discovery.createServiceTemplates")
	}
	if spec.DefaultServiceTemplate != "" {
		fields = append(fields, "spec.defaultServiceTemplate")
	}
	if spec.Custom != nil {
		fields = append(fields, "spec.custom")
	}
	if len(spec.CustomTemplates) > 0 {
		fields = append(fields, "spec.customTemplates")
	}
	if len(spec.Env) > 0 {
		fields = append(fields, "spec.env")
	}
	if len(spec.Resources.Limits) > 0 || len(spec.Resources.Requests) > 0 || len(spec.Resources.Claims) > 0 {
		fields = append(fields, "spec.resources")
	}
	if len(fields) == 0 {
		cm.Delete(controllerutils.ConditionTypeTemplateFieldsIgnored)
		return
	}
	cm.MarkTrue(
		controllerutils.ConditionTypeTemplateFieldsIgnored,
		controllerutils.ReasonTemplateFieldsSet,
		"These fields have no effect on profile-based serving and can be removed: "+strings.Join(fields, ", "),
		controllerutils.AsWarning(),
	)
}
