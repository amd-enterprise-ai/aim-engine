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
	RuntimeConfig           aimruntimeconfig.RuntimeConfigFetchResult
	ClusterServiceTemplates ClusterModelServiceTemplateFetchResult
	ImageMetadata           *ModelMetadataFetchResult
}

func (r *ClusterModelReconciler) Fetch(
	ctx context.Context,
	c client.Client,
	clusterModel *aimv1alpha1.AIMClusterModel,
) (ClusterModelFetchResult, error) {
	result := ClusterModelFetchResult{}

	runtimeConfig, err := aimruntimeconfig.FetchRuntimeConfig(ctx, c, clusterModel.Spec.RuntimeConfigName, "")
	if err != nil {
		return result, fmt.Errorf("failed to fetch runtimeConfig: %w", err)
	}
	result.RuntimeConfig = runtimeConfig

	templates, templatesErr := fetchClusterModelServiceTemplateResult(ctx, c, *clusterModel)
	if templatesErr != nil {
		return result, fmt.Errorf("failed to fetch cluster model service templates: %w", templatesErr)
	}
	result.ClusterServiceTemplates = templates

	// Fetch image metadata only if needed
	if ShouldExtractMetadata(&clusterModel.Status) {
		metadataResult := FetchModelMetadataResult(ctx, r.Clientset, clusterModel.Spec, constants.GetOperatorNamespace())
		result.ImageMetadata = &metadataResult
	}

	return result, nil
}

type ModelFetchResult struct {
	RuntimeConfig    aimruntimeconfig.RuntimeConfigFetchResult
	ServiceTemplates ModelServiceTemplateFetchResult
	ImageMetadata    *ModelMetadataFetchResult
}

func (r *ModelReconciler) Fetch(
	ctx context.Context,
	c client.Client,
	model *aimv1alpha1.AIMModel,
) (ModelFetchResult, error) {
	result := ModelFetchResult{}

	runtimeConfig, err := aimruntimeconfig.FetchRuntimeConfig(ctx, c, model.Spec.RuntimeConfigName, "")
	if err != nil {
		return result, fmt.Errorf("failed to fetch runtimeConfig: %w", err)
	}
	result.RuntimeConfig = runtimeConfig

	templates, templatesErr := fetchModelServiceTemplateResult(ctx, c, *model)
	if templatesErr != nil {
		return result, fmt.Errorf("failed to fetch model service templates: %w", templatesErr)
	}
	result.ServiceTemplates = templates

	// Fetch image metadata if needed
	if ShouldExtractMetadata(&model.Status) {
		metadataResult := FetchModelMetadataResult(ctx, r.Clientset, model.Spec, model.Namespace)
		result.ImageMetadata = &metadataResult
	}

	return result, nil
}

// ============================================================================
// OBSERVATION
// ============================================================================

type ClusterModelObservation struct {
	RuntimeConfig aimruntimeconfig.RuntimeConfigObservation
	Metadata      ModelMetadataObservation
	Templates     ClusterModelServiceTemplateObservation
}

func (r *ClusterModelReconciler) Observe(
	ctx context.Context,
	obj *aimv1alpha1.AIMClusterModel,
	fetchResult ClusterModelFetchResult,
) (ClusterModelObservation, error) {
	obs := ClusterModelObservation{
		RuntimeConfig: aimruntimeconfig.ObserveRuntimeConfig(fetchResult.RuntimeConfig, obj.Spec.RuntimeConfigName),
	}

	obs.Metadata = ObserveModelMetadata(&obj.Status, fetchResult.ImageMetadata)
	obs.Templates = ObserveClusterModelServiceTemplate(fetchResult.ClusterServiceTemplates, *obj, obs.RuntimeConfig.MergedConfig)

	return obs, nil
}

type ModelObservation struct {
	RuntimeConfig aimruntimeconfig.RuntimeConfigObservation
	Metadata      ModelMetadataObservation
	Templates     ModelServiceTemplateObservation
}

func (r *ModelReconciler) Observe(
	ctx context.Context,
	obj *aimv1alpha1.AIMModel,
	fetchResult ModelFetchResult,
) (ModelObservation, error) {
	obs := ModelObservation{
		RuntimeConfig: aimruntimeconfig.ObserveRuntimeConfig(fetchResult.RuntimeConfig, obj.Spec.RuntimeConfigName),
	}

	obs.Metadata = ObserveModelMetadata(&obj.Status, fetchResult.ImageMetadata)
	obs.Templates = ObserveModelServiceTemplate(fetchResult.ServiceTemplates, *obj, obs.RuntimeConfig.MergedConfig)

	return obs, nil
}

// ============================================================================
// PLAN
// ============================================================================

func (r *ClusterModelReconciler) Plan(
	ctx context.Context,
	obj *aimv1alpha1.AIMClusterModel,
	obs ClusterModelObservation,
) ([]client.Object, error) {
	// Return if nothing to create
	if !obs.Templates.ShouldCreateTemplates {
		return nil, nil
	}

	var templates []client.Object

	clusterServiceTemplates := planClusterModelServiceTemplates(obs.Templates, obs.Metadata, *obj)
	for _, template := range clusterServiceTemplates {
		if err := controllerutil.SetOwnerReference(obj, template, r.Scheme, controllerutil.WithBlockOwnerDeletion(false)); err != nil {
			return templates, err
		}
		templates = append(templates, template)
	}

	return templates, nil
}

func (r *ModelReconciler) Plan(
	ctx context.Context,
	obj *aimv1alpha1.AIMModel,
	obs ModelObservation,
) ([]client.Object, error) {
	// Return if nothing to create
	if !obs.Templates.ShouldCreateTemplates {
		return nil, nil
	}

	var templates []client.Object

	serviceTemplates := planModelServiceTemplates(obs.Templates, obs.Metadata, *obj)
	for _, template := range serviceTemplates {
		if err := controllerutil.SetOwnerReference(obj, template, r.Scheme, controllerutil.WithBlockOwnerDeletion(false)); err != nil {
			return templates, err
		}
		templates = append(templates, template)
	}

	return templates, nil
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

	aimruntimeconfig.ProjectRuntimeConfigObservation(cm, observation.RuntimeConfig)

	projectModelMetadata(cm, sh, observation.Metadata)
	var templateStatuses []aimv1alpha1.AIMServiceTemplateStatus
	for _, templateStatus := range observation.Templates.ExistingTemplates {
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

	aimruntimeconfig.ProjectRuntimeConfigObservation(cm, observation.RuntimeConfig)

	projectModelMetadata(cm, sh, observation.Metadata)
	var templateStatuses []aimv1alpha1.AIMServiceTemplateStatus
	for _, templateStatus := range observation.Templates.ExistingTemplates {
		templateStatuses = append(templateStatuses, templateStatus.Status)
	}

	projectModelStatusFromTemplates(status, sh, templateStatuses)
}
