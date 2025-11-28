package aimservicetemplate

import (
	"context"
	"fmt"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ============================================================================
// FETCH
// ============================================================================

type ClusterServiceTemplateModelFetchResult struct {
	ClusterModel *aimv1alpha1.AIMClusterModel
}

func FetchClusterServiceTemplateModelResult(ctx context.Context, c client.Client, clusterServiceTemplate *aimv1alpha1.AIMClusterServiceTemplate) (ClusterServiceTemplateModelFetchResult, error) {
	result := ClusterServiceTemplateModelFetchResult{}

	clusterModel := &aimv1alpha1.AIMClusterModel{}
	key := client.ObjectKey{Name: clusterServiceTemplate.Spec.ModelName}
	if err := c.Get(ctx, key, clusterModel); err != nil && !errors.IsNotFound(err) {
		return result, fmt.Errorf("failed to fetch cluster model: %w", err)
	} else if err == nil {
		result.ClusterModel = clusterModel
	}
	return result, nil
}

type ServiceTemplateModelFetchResult struct {
	Model *aimv1alpha1.AIMModel
}

func FetchServiceTemplateModelResult(ctx context.Context, c client.Client, serviceTemplate *aimv1alpha1.AIMServiceTemplate) (ServiceTemplateModelFetchResult, error) {
	result := ServiceTemplateModelFetchResult{}

	model := &aimv1alpha1.AIMModel{}
	key := client.ObjectKey{Name: serviceTemplate.Spec.ModelName, Namespace: serviceTemplate.Namespace}
	if err := c.Get(ctx, key, model); err != nil && !errors.IsNotFound(err) {
		return result, fmt.Errorf("failed to cluster model: %w", err)
	} else if err == nil {
		result.Model = model
	}
	return result, nil
}

// ============================================================================
// OBSERVE
// ============================================================================

type ServiceTemplateModelObservation struct {
	ModelName     string
	ModelFound    bool
	ModelSpec     *aimv1alpha1.AIMModelSpec
	ResolvedModel *aimv1alpha1.AIMResolvedReference
}

func ObserveClusterServiceTemplateModel(result ClusterServiceTemplateModelFetchResult) ServiceTemplateModelObservation {
	obs := ServiceTemplateModelObservation{}

	if result.ClusterModel == nil {
		return obs
	}

	obs.ModelName = result.ClusterModel.Name
	obs.ModelFound = true
	obs.ResolvedModel = &aimv1alpha1.AIMResolvedReference{
		Name:  result.ClusterModel.Name,
		Scope: aimv1alpha1.AIMResolutionScopeCluster,
		Kind:  result.ClusterModel.Kind,
		UID:   result.ClusterModel.UID,
	}
	obs.ModelSpec = &result.ClusterModel.Spec

	return obs
}

func ObserveServiceTemplateModel(result ServiceTemplateModelFetchResult) ServiceTemplateModelObservation {
	obs := ServiceTemplateModelObservation{}

	if result.Model == nil {
		return obs
	}

	obs.ModelName = result.Model.Name
	obs.ModelFound = true
	obs.ResolvedModel = &aimv1alpha1.AIMResolvedReference{
		Name:      result.Model.Name,
		Scope:     aimv1alpha1.AIMResolutionScopeNamespace,
		Namespace: result.Model.Namespace,
		Kind:      result.Model.Kind,
		UID:       result.Model.UID,
	}
	obs.ModelSpec = &result.Model.Spec

	return obs
}

// ============================================================================
// PROJECT
// ============================================================================

// projectServiceTemplateModel projects the model's
func projectServiceTemplateModel(
	status *aimv1alpha1.AIMServiceTemplateStatus,
	cm *controllerutils.ConditionManager,
	h *controllerutils.StatusHelper,
	obs ServiceTemplateModelObservation,
) bool {
	if !obs.ModelFound {
		msg := fmt.Sprintf("Model %s not found in scope", obs.ModelName)
		h.Degraded("ModelNotFound", msg)
		cm.MarkFalse("ModelFound", "ModelNotFound", msg, controllerutils.LevelWarning)
		return false
	} else {
		cm.MarkTrue("ModelFound", "ModelFound", fmt.Sprintf("Model '%s' found", obs.ModelName), controllerutils.LevelNone)
		status.ResolvedModel = obs.ResolvedModel
		return true
	}
}
