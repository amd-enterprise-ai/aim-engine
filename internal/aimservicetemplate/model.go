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

package aimservicetemplate

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

// ============================================================================
// FETCH
// ============================================================================

type clusterServiceTemplateModelFetchResult struct {
	clusterModel *aimv1alpha1.AIMClusterModel
}

func fetchClusterServiceTemplateModelResult(ctx context.Context, c client.Client, clusterServiceTemplate *aimv1alpha1.AIMClusterServiceTemplate) (clusterServiceTemplateModelFetchResult, error) {
	result := clusterServiceTemplateModelFetchResult{}

	clusterModel := &aimv1alpha1.AIMClusterModel{}
	key := client.ObjectKey{Name: clusterServiceTemplate.Spec.ModelName}
	if err := c.Get(ctx, key, clusterModel); err != nil && !errors.IsNotFound(err) {
		return result, fmt.Errorf("failed to fetch cluster model: %w", err)
	} else if err == nil {
		result.clusterModel = clusterModel
	}
	return result, nil
}

type serviceTemplateModelFetchResult struct {
	model *aimv1alpha1.AIMModel
}

func fetchServiceTemplateModelResult(ctx context.Context, c client.Client, serviceTemplate *aimv1alpha1.AIMServiceTemplate) (serviceTemplateModelFetchResult, error) {
	result := serviceTemplateModelFetchResult{}

	model := &aimv1alpha1.AIMModel{}
	key := client.ObjectKey{Name: serviceTemplate.Spec.ModelName, Namespace: serviceTemplate.Namespace}
	if err := c.Get(ctx, key, model); err != nil && !errors.IsNotFound(err) {
		return result, fmt.Errorf("failed to cluster model: %w", err)
	} else if err == nil {
		result.model = model
	}
	return result, nil
}

// ============================================================================
// OBSERVE
// ============================================================================

type serviceTemplateModelObservation struct {
	modelName     string
	modelFound    bool
	modelSpec     *aimv1alpha1.AIMModelSpec
	resolvedModel *aimv1alpha1.AIMResolvedReference
}

func observeClusterServiceTemplateModel(result clusterServiceTemplateModelFetchResult) serviceTemplateModelObservation {
	obs := serviceTemplateModelObservation{}

	if result.clusterModel == nil {
		return obs
	}

	obs.modelName = result.clusterModel.Name
	obs.modelFound = true
	obs.resolvedModel = &aimv1alpha1.AIMResolvedReference{
		Name:  result.clusterModel.Name,
		Scope: aimv1alpha1.AIMResolutionScopeCluster,
		Kind:  result.clusterModel.Kind,
		UID:   result.clusterModel.UID,
	}
	obs.modelSpec = &result.clusterModel.Spec

	return obs
}

func observeServiceTemplateModel(result serviceTemplateModelFetchResult) serviceTemplateModelObservation {
	obs := serviceTemplateModelObservation{}

	if result.model == nil {
		return obs
	}

	obs.modelName = result.model.Name
	obs.modelFound = true
	obs.resolvedModel = &aimv1alpha1.AIMResolvedReference{
		Name:      result.model.Name,
		Scope:     aimv1alpha1.AIMResolutionScopeNamespace,
		Namespace: result.model.Namespace,
		Kind:      result.model.Kind,
		UID:       result.model.UID,
	}
	obs.modelSpec = &result.model.Spec

	return obs
}

// ============================================================================
// PROJECT
// ============================================================================

// projectServiceTemplateModel projects the model observation.
// Returns true if a fatal error occurred (should stop reconciliation), false otherwise.
func projectServiceTemplateModel(
	status *aimv1alpha1.AIMServiceTemplateStatus,
	cm *controllerutils.ConditionManager,
	h *controllerutils.StatusHelper,
	obs serviceTemplateModelObservation,
) bool {
	if !obs.modelFound {
		msg := fmt.Sprintf("Model %s not found in scope", obs.modelName)
		h.Degraded(aimv1alpha1.AIMTemplateModelNotFound, msg)
		cm.MarkFalse(aimv1alpha1.AIMServiceTemplateConditionModelFound, aimv1alpha1.AIMTemplateModelNotFound, msg, controllerutils.LevelWarning)
		return true // Fatal - stop reconciliation
	}

	cm.MarkTrue(aimv1alpha1.AIMServiceTemplateConditionModelFound, aimv1alpha1.AIMServiceTemplateConditionModelFound, fmt.Sprintf("Model '%s' found", obs.modelName), controllerutils.LevelNone)
	status.ResolvedModel = obs.resolvedModel
	return false // Continue
}
