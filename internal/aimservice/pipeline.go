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

// Package aimservice contains policy shared by the AIMService reconciliation
// pipelines.
package aimservice

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

// Pipeline identifies an AIMService reconciliation pipeline.
type Pipeline string

const (
	// PipelineTemplate is the legacy model/template reconciliation path.
	PipelineTemplate Pipeline = constants.ReconcilerPipelineTemplate
	// PipelineProfile is the profile-based reconciliation path.
	PipelineProfile Pipeline = constants.ReconcilerPipelineProfile
)

// LegacyView returns an isolated v1alpha1 view for domain code that still uses
// the legacy type while the controller reads and writes the canonical v1alpha2
// object. The deep copy prevents domain mutations from leaking across the
// version boundary.
func LegacyView(service *aimv1alpha2.AIMService) *aimv1alpha1.AIMService {
	if service == nil {
		return nil
	}
	copied := service.DeepCopy()
	return &aimv1alpha1.AIMService{
		TypeMeta:   copied.TypeMeta,
		ObjectMeta: copied.ObjectMeta,
		Spec:       copied.Spec,
		Status:     copied.Status,
	}
}

var (
	templateOwnedConditions = map[string]struct{}{
		"ModelReady":    {},
		"TemplateReady": {},
		"CacheReady":    {},
		controllerutils.ConditionTypeMigrationRequired: {},
	}
	profileOwnedConditions = map[string]struct{}{
		"ProfileReady":        {},
		"ProfileConfigReady":  {},
		"ProfileCacheReady":   {},
		"ProfileRuntimeReady": {},
	}
	pipelineAggregateConditions = map[string]struct{}{
		"Ready":                 {},
		"ConfigValid":           {},
		"AuthValid":             {},
		"DependenciesReachable": {},
	}
)

// SelectPipeline returns the active pipeline selected by a supported explicit
// annotation or, otherwise, by the service spec shape.
//
// Empty, unknown, and template-without-model markers intentionally fall back to
// spec-shape inference. PipelineMarkerWarning exposes those cases for
// administrator correction without breaking historical services.
func SelectPipeline(service *aimv1alpha1.AIMService) Pipeline {
	if override, exists := service.GetAnnotations()[constants.AnnotationReconcilerPipeline]; exists {
		switch override {
		case constants.ReconcilerPipelineProfile:
			return PipelineProfile
		case constants.ReconcilerPipelineTemplate:
			if service.Spec.Model != nil {
				return PipelineTemplate
			}
		}
	}
	if service.Spec.Profile != nil {
		return PipelineProfile
	}
	return PipelineTemplate
}

// PipelineMarkerWarning describes a malformed explicit marker that SelectPipeline
// ignores in favor of spec-shape inference.
func PipelineMarkerWarning(service *aimv1alpha1.AIMService) string {
	override, exists := service.GetAnnotations()[constants.AnnotationReconcilerPipeline]
	if !exists {
		return ""
	}
	switch {
	case override == constants.ReconcilerPipelineProfile:
		return ""
	case override == constants.ReconcilerPipelineTemplate && service.Spec.Model != nil:
		return ""
	case override == constants.ReconcilerPipelineTemplate:
		return fmt.Sprintf(
			"annotation %q value %q requires spec.model; using spec-shape pipeline selection",
			constants.AnnotationReconcilerPipeline,
			override,
		)
	case override == "":
		return fmt.Sprintf(
			"annotation %q is empty; using spec-shape pipeline selection",
			constants.AnnotationReconcilerPipeline,
		)
	default:
		return fmt.Sprintf(
			"annotation %q has unsupported value %q; using spec-shape pipeline selection",
			constants.AnnotationReconcilerPipeline,
			override,
		)
	}
}

// UsesPipeline reports whether a service is actively reconciled by pipeline.
func UsesPipeline(service *aimv1alpha1.AIMService, pipeline Pipeline) bool {
	return SelectPipeline(service) == pipeline
}

// PrepareStatusForPipeline records the selected pipeline and retires status
// owned by the previous pipeline. When provenance is absent, it removes only
// opposite-path state, preserves status explicitly tied to the selected path,
// and clears ambiguous model/cache storage lineage. It returns true when callers
// must persist the status and requeue before dispatching.
func PrepareStatusForPipeline(status *aimv1alpha1.AIMServiceStatus, selected Pipeline) bool {
	previous := Pipeline(status.ReconcilerPipeline)
	if previous == selected {
		return false
	}

	if previous == "" {
		if selected == PipelineProfile {
			retirePipelineOwnedStatus(status, PipelineTemplate)
		} else {
			retirePipelineOwnedStatus(status, PipelineProfile)
		}
		status.ResolvedModel = nil
		if bootstrapCacheIsUnsafe(status.Cache, selected) {
			status.Cache = nil
		}
		clearPipelineDerivedStorageBinding(status)
	} else {
		retirePipelineOwnedStatus(status, previous)
		// ResolvedModel and cache lineage have different meanings on the two
		// paths, so neither is safe to carry across a transition.
		status.ResolvedModel = nil
		status.Cache = nil
		clearPipelineDerivedStorageBinding(status)
		status.Conditions = removeConditions(status.Conditions, pipelineAggregateConditions)
		status.SetStatus(string(constants.AIMStatusPending))
	}

	status.ReconcilerPipeline = string(selected)
	return true
}

func retirePipelineOwnedStatus(status *aimv1alpha1.AIMServiceStatus, pipeline Pipeline) {
	switch pipeline {
	case PipelineTemplate:
		status.Conditions = removeConditions(status.Conditions, templateOwnedConditions)
		status.ResolvedTemplate = nil
	case PipelineProfile:
		status.Conditions = removeConditions(status.Conditions, profileOwnedConditions)
		status.ResolvedProfile = nil
	default:
		status.Conditions = removeConditions(status.Conditions, templateOwnedConditions)
		status.Conditions = removeConditions(status.Conditions, profileOwnedConditions)
		status.ResolvedTemplate = nil
		status.ResolvedProfile = nil
	}
}

func bootstrapCacheIsUnsafe(cache *aimv1alpha1.AIMServiceCacheStatus, selected Pipeline) bool {
	if cache == nil {
		return false
	}
	switch selected {
	case PipelineProfile:
		return cache.ProfileCacheRef == nil || cache.TemplateCacheRef != nil
	case PipelineTemplate:
		return cache.TemplateCacheRef == nil || cache.ProfileCacheRef != nil
	default:
		return true
	}
}

func clearPipelineDerivedStorageBinding(status *aimv1alpha1.AIMServiceStatus) {
	status.AdapterSubtreeSyncKey = ""
	status.AdapterDiskPersistentVolumeClaim = ""
	status.AdapterDiskPersistentVolumeClaimUID = ""
}

func removeConditions(
	conditions []metav1.Condition,
	owned map[string]struct{},
) []metav1.Condition {
	retained := make([]metav1.Condition, 0, len(conditions))
	for i := range conditions {
		if _, shouldRemove := owned[conditions[i].Type]; !shouldRemove {
			retained = append(retained, conditions[i])
		}
	}
	return retained
}
