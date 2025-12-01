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

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/aimruntimeconfig"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

type ClusterModelReconciler struct {
	Clientset kubernetes.Interface
	Scheme    *runtime.Scheme
}

type ModelReconciler struct {
	Clientset kubernetes.Interface
	Scheme    *runtime.Scheme
}

// ============================================================================
// FETCH
// ============================================================================

type ClusterModelFetchResult struct {
	runtimeConfig           aimruntimeconfig.RuntimeConfigFetchResult
	clusterServiceTemplates clusterModelServiceTemplateFetchResult
	imageMetadata           *modelMetadataFetchResult
}

func (r *ClusterModelReconciler) Fetch(
	ctx context.Context,
	c client.Client,
	clusterModel *aimv1alpha1.AIMClusterModel,
) (ClusterModelFetchResult, error) {
	ctx = log.IntoContext(ctx, log.FromContext(ctx).WithValues(
		"phase", "fetch",
		"clusterModel", clusterModel.Name,
		"image", clusterModel.Spec.Image,
	))

	result := ClusterModelFetchResult{}

	runtimeConfig, err := aimruntimeconfig.FetchRuntimeConfig(ctx, c, clusterModel.Spec.RuntimeConfigName, "")
	if err != nil {
		return result, fmt.Errorf("failed to fetch runtimeConfig: %w", err)
	}
	result.runtimeConfig = runtimeConfig

	templates, templatesErr := fetchClusterModelServiceTemplateResult(ctx, c, *clusterModel)
	if templatesErr != nil {
		return result, fmt.Errorf("failed to fetch cluster model service templates: %w", templatesErr)
	}
	result.clusterServiceTemplates = templates

	// Fetch image metadata only if needed
	if shouldExtractMetadata(&clusterModel.Status) {
		metadataResult := fetchModelMetadataResult(ctx, r.Clientset, clusterModel.Spec, constants.GetOperatorNamespace())
		result.imageMetadata = &metadataResult
	}

	return result, nil
}

type ModelFetchResult struct {
	runtimeConfig    aimruntimeconfig.RuntimeConfigFetchResult
	serviceTemplates modelServiceTemplateFetchResult
	imageMetadata    *modelMetadataFetchResult
}

func (r *ModelReconciler) Fetch(
	ctx context.Context,
	c client.Client,
	model *aimv1alpha1.AIMModel,
) (ModelFetchResult, error) {
	ctx = log.IntoContext(ctx, log.FromContext(ctx).WithValues(
		"phase", "fetch",
		"model", model.Name,
		"namespace", model.Namespace,
		"image", model.Spec.Image,
	))

	result := ModelFetchResult{}

	runtimeConfig, err := aimruntimeconfig.FetchRuntimeConfig(ctx, c, model.Spec.RuntimeConfigName, "")
	if err != nil {
		return result, fmt.Errorf("failed to fetch runtimeConfig: %w", err)
	}
	result.runtimeConfig = runtimeConfig

	templates, templatesErr := fetchModelServiceTemplateResult(ctx, c, *model)
	if templatesErr != nil {
		return result, fmt.Errorf("failed to fetch model service templates: %w", templatesErr)
	}
	result.serviceTemplates = templates

	// Fetch image metadata if needed
	if shouldExtractMetadata(&model.Status) {
		metadataResult := fetchModelMetadataResult(ctx, r.Clientset, model.Spec, model.Namespace)
		result.imageMetadata = &metadataResult
	}

	return result, nil
}

// ============================================================================
// OBSERVATION
// ============================================================================

type ClusterModelObservation struct {
	runtimeConfig aimruntimeconfig.RuntimeConfigObservation
	metadata      modelMetadataObservation
	templates     clusterModelServiceTemplateObservation
}

func (r *ClusterModelReconciler) Observe(
	ctx context.Context,
	obj *aimv1alpha1.AIMClusterModel,
	fetchResult ClusterModelFetchResult,
) (ClusterModelObservation, error) {
	ctx = log.IntoContext(ctx, log.FromContext(ctx).WithValues(
		"phase", "observe",
		"clusterModel", obj.Name,
	))

	obs := ClusterModelObservation{
		runtimeConfig: aimruntimeconfig.ObserveRuntimeConfig(fetchResult.runtimeConfig, obj.Spec.RuntimeConfigName),
	}

	obs.metadata = observeModelMetadata(&obj.Status, fetchResult.imageMetadata)
	obs.templates = observeClusterModelServiceTemplate(ctx, fetchResult.clusterServiceTemplates, *obj, obs.runtimeConfig.MergedConfig)

	return obs, nil
}

type ModelObservation struct {
	runtimeConfig aimruntimeconfig.RuntimeConfigObservation
	metadata      modelMetadataObservation
	templates     modelServiceTemplateObservation
}

func (r *ModelReconciler) Observe(
	ctx context.Context,
	obj *aimv1alpha1.AIMModel,
	fetchResult ModelFetchResult,
) (ModelObservation, error) {
	ctx = log.IntoContext(ctx, log.FromContext(ctx).WithValues(
		"phase", "observe",
		"model", obj.Name,
		"namespace", obj.Namespace,
	))

	obs := ModelObservation{
		runtimeConfig: aimruntimeconfig.ObserveRuntimeConfig(fetchResult.runtimeConfig, obj.Spec.RuntimeConfigName),
	}

	obs.metadata = observeModelMetadata(&obj.Status, fetchResult.imageMetadata)
	obs.templates = observeModelServiceTemplate(ctx, fetchResult.serviceTemplates, *obj, obs.runtimeConfig.MergedConfig)

	return obs, nil
}

// ============================================================================
// PLAN
// ============================================================================

func (r *ClusterModelReconciler) Plan(
	ctx context.Context,
	obj *aimv1alpha1.AIMClusterModel,
	obs ClusterModelObservation,
) (controllerutils.PlanResult, error) {
	ctx = log.IntoContext(ctx, log.FromContext(ctx).WithValues(
		"phase", "plan",
		"clusterModel", obj.Name,
	))

	// Return if nothing to create
	if !obs.templates.shouldCreateTemplates {
		return controllerutils.PlanResult{}, nil
	}

	var templates []client.Object

	clusterServiceTemplates := planClusterModelServiceTemplates(ctx, obs.templates, obs.metadata, *obj)
	for _, template := range clusterServiceTemplates {
		if err := controllerutil.SetControllerReference(obj, template, r.Scheme); err != nil {
			return controllerutils.PlanResult{}, err
		}
		templates = append(templates, template)
	}

	return controllerutils.PlanResult{Apply: templates}, nil
}

func (r *ModelReconciler) Plan(
	ctx context.Context,
	obj *aimv1alpha1.AIMModel,
	obs ModelObservation,
) (controllerutils.PlanResult, error) {
	ctx = log.IntoContext(ctx, log.FromContext(ctx).WithValues(
		"phase", "plan",
		"model", obj.Name,
		"namespace", obj.Namespace,
	))

	// Return if nothing to create
	if !obs.templates.shouldCreateTemplates {
		return controllerutils.PlanResult{}, nil
	}

	var templates []client.Object

	serviceTemplates := planModelServiceTemplates(ctx, obs.templates, obs.metadata, *obj)
	for _, template := range serviceTemplates {
		if err := controllerutil.SetControllerReference(obj, template, r.Scheme); err != nil {
			return controllerutils.PlanResult{}, err
		}
		templates = append(templates, template)
	}

	return controllerutils.PlanResult{Apply: templates}, nil
}

// ============================================================================
// PROJECT
// ============================================================================

func (r *ClusterModelReconciler) Project(
	status *aimv1alpha1.AIMModelStatus,
	cm *controllerutils.ConditionManager,
	observation ClusterModelObservation,
) {
	sh := controllerutils.NewStatusHelper(status, cm)

	// Project runtime config first - if it fails, don't proceed
	if fatal := aimruntimeconfig.ProjectRuntimeConfigObservation(cm, sh, observation.runtimeConfig); fatal {
		return
	}

	// Project metadata - if it fails fatally, don't proceed
	if fatal := projectModelMetadata(status, cm, sh, observation.metadata); fatal {
		return
	}

	// Only project template status if no higher-priority errors
	var templateStatuses []aimv1alpha1.AIMServiceTemplateStatus
	for _, templateStatus := range observation.templates.existingTemplates {
		templateStatuses = append(templateStatuses, templateStatus.Status)
	}

	projectModelStatusFromTemplates(status, sh, templateStatuses)
}

func (r *ModelReconciler) Project(
	status *aimv1alpha1.AIMModelStatus,
	cm *controllerutils.ConditionManager,
	observation ModelObservation,
) {
	sh := controllerutils.NewStatusHelper(status, cm)

	// Project runtime config first - if it fails, don't proceed
	if fatal := aimruntimeconfig.ProjectRuntimeConfigObservation(cm, sh, observation.runtimeConfig); fatal {
		return
	}

	// Project metadata - if it fails fatally, don't proceed
	if fatal := projectModelMetadata(status, cm, sh, observation.metadata); fatal {
		return
	}

	// Only project template status if no higher-priority errors
	var templateStatuses []aimv1alpha1.AIMServiceTemplateStatus
	for _, templateStatus := range observation.templates.existingTemplates {
		templateStatuses = append(templateStatuses, templateStatus.Status)
	}

	projectModelStatusFromTemplates(status, sh, templateStatuses)
}
