/*
MIT License

Copyright (c) 2025 Advanced Micro Devices, Inc.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package aimservice

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

// ============================================================================
// FETCH
// ============================================================================

type serviceModelFetchResult struct {
	// Resolved model (either from Ref or image lookup)
	namespaceModel *aimv1alpha1.AIMModel
	clusterModel   *aimv1alpha1.AIMClusterModel

	// For image-based resolution: track if multiple models found (error case)
	multipleModelsFound bool
	multipleModelsError error

	// Templates for the resolved model
	namespaceTemplatesForModel []aimv1alpha1.AIMServiceTemplate
	clusterTemplatesForModel   []aimv1alpha1.AIMClusterServiceTemplate
}

func fetchServiceModelResult(ctx context.Context, c client.Client, service *aimv1alpha1.AIMService) (serviceModelFetchResult, error) {
	result := serviceModelFetchResult{}

	// Case 1: Model specified by Ref
	if modelName := service.Spec.Model.Ref; modelName != nil && *modelName != "" {
		return fetchServiceModelResultForModelRef(ctx, c, *modelName, service.Namespace)
	}

	// Case 2: Model specified by image - search for existing models with this image
	if modelImage := service.Spec.Model.Image; modelImage != nil && *modelImage != "" {
		return fetchServiceModelResultForModelImage(ctx, c, *modelImage, service.Namespace)
	}

	// TODO handle custom image case here (later)

	return result, nil
}

func fetchServiceModelResultForModelRef(ctx context.Context, c client.Client, modelName string, namespace string) (serviceModelFetchResult, error) {
	result := serviceModelFetchResult{}

	// Try namespace-scoped model first
	model := &aimv1alpha1.AIMModel{}
	if err := c.Get(ctx, client.ObjectKey{Name: modelName, Namespace: namespace}, model); err != nil && !errors.IsNotFound(err) {
		return result, fmt.Errorf("failed to fetch namespace model: %w", err)
	} else if err == nil {
		result.namespaceModel = model
		// Fetch templates for this namespace model
		if err := fetchTemplatesForModel(ctx, c, modelName, namespace, &result); err != nil {
			return result, err
		}
		return result, nil
	}

	// Try cluster-scoped model
	clusterModel := &aimv1alpha1.AIMClusterModel{}
	if err := c.Get(ctx, client.ObjectKey{Name: modelName}, clusterModel); err != nil && !errors.IsNotFound(err) {
		return result, fmt.Errorf("failed to fetch cluster model: %w", err)
	} else if err == nil {
		result.clusterModel = clusterModel
		// Fetch templates for this cluster model
		if err := fetchTemplatesForModel(ctx, c, modelName, "", &result); err != nil {
			return result, err
		}
	}
	return result, nil
}

func fetchServiceModelResultForModelImage(ctx context.Context, c client.Client, modelImage string, namespace string) (serviceModelFetchResult, error) {
	result := serviceModelFetchResult{}

	// List namespace-scoped models with this image using field indexer
	var nsModels aimv1alpha1.AIMModelList
	if err := c.List(ctx, &nsModels,
		client.InNamespace(namespace),
		client.MatchingFields{aimv1alpha1.ModelImageIndexKey: modelImage},
	); err != nil {
		return result, fmt.Errorf("failed to list namespace models by image: %w", err)
	}

	if len(nsModels.Items) == 1 {
		result.namespaceModel = &nsModels.Items[0]
		// Fetch templates for this namespace model
		err := fetchTemplatesForModel(ctx, c, result.namespaceModel.Name, namespace, &result)
		return result, err
	} else if len(nsModels.Items) > 1 {
		result.multipleModelsFound = true
		result.multipleModelsError = fmt.Errorf("more than one model found for image %q in the same scope", modelImage)
		return result, nil
	}

	// List cluster-scoped models with this image using field indexer
	var clusterModels aimv1alpha1.AIMClusterModelList
	if err := c.List(ctx, &clusterModels,
		client.MatchingFields{aimv1alpha1.ClusterModelImageIndexKey: modelImage},
	); err != nil {
		return result, fmt.Errorf("failed to list cluster models by image: %w", err)
	}

	// Check: max 1 namespace model and max 1 cluster model
	// namespace takes precedence over cluster
	if len(clusterModels.Items) == 1 {
		result.clusterModel = &clusterModels.Items[0]
		// Fetch templates for this cluster model
		err := fetchTemplatesForModel(ctx, c, result.clusterModel.Name, "", &result)
		return result, err
	} else if len(clusterModels.Items) > 1 {
		// Multiple models in same scope - error case
		result.multipleModelsFound = true
		result.multipleModelsError = fmt.Errorf("more than one model found for image %q in the same scope", modelImage)
		return result, nil
	}
	// If no models found, result will be empty (needs to be created)
	return result, nil
}

// fetchTemplatesForModel fetches templates for a given model based on scope
// If namespace is provided, fetches namespace-scoped templates
// If namespace is empty, fetches cluster-scoped templates
func fetchTemplatesForModel(ctx context.Context, c client.Client, modelName string, namespace string, result *serviceModelFetchResult) error {
	if namespace != "" {
		// namespace-scoped model - fetch namespace templates only
		var nsTemplates aimv1alpha1.AIMServiceTemplateList
		if err := c.List(ctx, &nsTemplates,
			client.InNamespace(namespace),
			client.MatchingFields{aimv1alpha1.ServiceTemplateModelNameIndexKey: modelName},
		); err != nil {
			return fmt.Errorf("failed to list namespace templates for model: %w", err)
		}
		result.namespaceTemplatesForModel = nsTemplates.Items
	} else {
		// Cluster-scoped model - fetch cluster templates only
		var clusterTemplates aimv1alpha1.AIMClusterServiceTemplateList
		if err := c.List(ctx, &clusterTemplates,
			client.MatchingFields{aimv1alpha1.ServiceTemplateModelNameIndexKey: modelName},
		); err != nil {
			return fmt.Errorf("failed to list cluster templates for model: %w", err)
		}
		result.clusterTemplatesForModel = clusterTemplates.Items
	}

	return nil
}

// ============================================================================
// OBSERVE
// ============================================================================

type serviceModelObservation struct {
	modelName          string
	ModelNamespace     string // namespace of the resolved model (empty for cluster-scoped models)
	ModelFound         bool
	ModelReady         bool
	ModelSpec          *aimv1alpha1.AIMModelSpec
	Scope              aimv1alpha1.AIMResolutionScope
	ShouldCreateModel  bool
	MultipleModels     bool // True when multiple models found with same image
	ModelResolutionErr error
	ImageParseErr      error  // Error parsing image reference for auto-creation
	GeneratedModelName string // Pre-computed model name for auto-creation
}

func observeServiceModel(_ context.Context, _ client.Client, service *aimv1alpha1.AIMService, result serviceModelFetchResult) serviceModelObservation {
	obs := serviceModelObservation{}

	// Case 1: Model specified by Ref
	if service.Spec.Model.Ref != nil && *service.Spec.Model.Ref != "" {
		if result.namespaceModel != nil {
			obs.modelName = result.namespaceModel.Name
			obs.ModelNamespace = result.namespaceModel.Namespace
			obs.ModelFound = true
			obs.ModelReady = result.namespaceModel.Status.Status == constants.AIMStatusReady
			obs.ModelSpec = &result.namespaceModel.Spec
			obs.Scope = aimv1alpha1.AIMResolutionScopeNamespace
		} else if result.clusterModel != nil {
			obs.modelName = result.clusterModel.Name
			obs.ModelNamespace = "" // Cluster-scoped models have no namespace
			obs.ModelFound = true
			obs.ModelReady = result.clusterModel.Status.Status == constants.AIMStatusReady
			obs.ModelSpec = &result.clusterModel.Spec
			obs.Scope = aimv1alpha1.AIMResolutionScopeCluster
		} else {
			// Model ref not found
			obs.ModelResolutionErr = fmt.Errorf("model %q not found", *service.Spec.Model.Ref)
		}
		return obs
	}

	// Case 2: Model specified by image
	if service.Spec.Model.Image != nil && *service.Spec.Model.Image != "" {
		// Check for multiple models error from fetch
		if result.multipleModelsFound {
			obs.MultipleModels = true
			obs.ModelResolutionErr = result.multipleModelsError
			return obs
		}

		// Check if a model was found
		if result.namespaceModel != nil {
			obs.modelName = result.namespaceModel.Name
			obs.ModelNamespace = result.namespaceModel.Namespace
			obs.ModelFound = true
			obs.ModelReady = result.namespaceModel.Status.Status == constants.AIMStatusReady
			obs.ModelSpec = &result.namespaceModel.Spec
			obs.Scope = aimv1alpha1.AIMResolutionScopeNamespace
		} else if result.clusterModel != nil {
			obs.modelName = result.clusterModel.Name
			obs.ModelNamespace = "" // Cluster-scoped models have no namespace
			obs.ModelFound = true
			obs.ModelReady = result.clusterModel.Status.Status == constants.AIMStatusReady
			obs.ModelSpec = &result.clusterModel.Spec
			obs.Scope = aimv1alpha1.AIMResolutionScopeCluster
		} else {
			// No existing model found - need to create one
			obs.ShouldCreateModel = true
			obs.ModelNamespace = service.Namespace
			obs.Scope = aimv1alpha1.AIMResolutionScopeNamespace

			// Pre-compute model name and check for errors during observe phase
			imageParts, err := utils.ExtractImageParts(*service.Spec.Model.Image)
			if err != nil {
				obs.ImageParseErr = fmt.Errorf("failed to parse image reference: %w", err)
				obs.ShouldCreateModel = false // Don't create if we can't parse the image
				return obs
			}

			// Generate model name from image parts
			modelName, err := utils.GenerateDerivedNameWithHashLength(
				[]string{imageParts.Name, imageParts.Tag},
				4,
				*service.Spec.Model.Image,
			)
			if err != nil {
				obs.ImageParseErr = fmt.Errorf("failed to generate model name: %w", err)
				obs.ShouldCreateModel = false
				return obs
			}

			obs.GeneratedModelName = modelName
		}
		return obs
	}

	return obs
}

// ============================================================================
// PLAN
// ============================================================================

func planServiceModel(obs serviceModelObservation, service *aimv1alpha1.AIMService) client.Object {
	// Don't create if there's an error or if we shouldn't create
	if !obs.ShouldCreateModel || obs.ImageParseErr != nil || obs.GeneratedModelName == "" {
		return nil
	}

	modelImage := service.Spec.Model.Image
	if modelImage == nil || *modelImage == "" {
		return nil
	}

	// Use pre-computed model name from observation
	// Build namespace-scoped AIMModel
	model := &aimv1alpha1.AIMModel{
		TypeMeta: metav1.TypeMeta{
			APIVersion: aimv1alpha1.GroupVersion.String(),
			Kind:       "AIMModel",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      obs.GeneratedModelName,
			Namespace: service.Namespace,
			Labels: map[string]string{
				constants.LabelAutoCreated: "true",
			},
		},
		Spec: aimv1alpha1.AIMModelSpec{
			Image:              *modelImage,
			RuntimeConfigName:  service.Spec.RuntimeConfigName,
			ImagePullSecrets:   service.Spec.ImagePullSecrets,
			ServiceAccountName: service.Spec.ServiceAccountName,
			Env:                service.Spec.Env,
		},
	}

	return model
}

// ============================================================================
// PROJECT
// ============================================================================

func projectServiceModel(
	status *aimv1alpha1.AIMServiceStatus,
	cm *controllerutils.ConditionManager,
	h *controllerutils.StatusHelper,
	obs serviceModelObservation,
) bool {
	// Check for image parse errors (terminal error)
	if obs.ImageParseErr != nil {
		h.Failed(aimv1alpha1.AIMServiceReasonInvalidImageReference, obs.ImageParseErr.Error())
		cm.MarkFalse(aimv1alpha1.AIMServiceConditionModelResolved, aimv1alpha1.AIMServiceReasonInvalidImageReference, obs.ImageParseErr.Error(), controllerutils.AsWarning())
		return true // Terminal error, stop reconciliation
	}

	if obs.ModelResolutionErr != nil {
		if obs.MultipleModels {
			h.Degraded(aimv1alpha1.AIMServiceReasonMultipleModelsFound, obs.ModelResolutionErr.Error())
			cm.MarkFalse(aimv1alpha1.AIMServiceConditionModelResolved, aimv1alpha1.AIMServiceReasonMultipleModelsFound, "Multiple models found with same image", controllerutils.AsWarning())
		} else {
			h.Degraded(aimv1alpha1.AIMServiceReasonModelNotFound, obs.ModelResolutionErr.Error())
			cm.MarkFalse(aimv1alpha1.AIMServiceConditionModelResolved, aimv1alpha1.AIMServiceReasonModelNotFound, obs.ModelResolutionErr.Error(), controllerutils.AsWarning())
		}
		return true
	}

	if obs.ShouldCreateModel {
		h.Progressing(aimv1alpha1.AIMServiceReasonCreatingModel, "Creating model for service")
		cm.MarkFalse(aimv1alpha1.AIMServiceConditionModelResolved, aimv1alpha1.AIMServiceReasonCreatingModel, "Model being created", controllerutils.AsInfo())
		return false
	}

	if !obs.ModelFound {
		// Should not happen - either found, should create, or has error
		return false
	}

	if !obs.ModelReady {
		h.Progressing(aimv1alpha1.AIMServiceReasonModelNotReady, fmt.Sprintf("Model %q is not ready", obs.modelName))
		cm.MarkFalse(aimv1alpha1.AIMServiceConditionModelResolved, aimv1alpha1.AIMServiceReasonModelNotReady, fmt.Sprintf("Model %q is not ready", obs.modelName), controllerutils.AsInfo())
		return true
	}

	// Model found and ready
	cm.MarkTrue(aimv1alpha1.AIMServiceConditionModelResolved, aimv1alpha1.AIMServiceReasonModelResolved, fmt.Sprintf("Model %q is ready", obs.modelName), controllerutils.AsInfo())
	status.ResolvedModel = &aimv1alpha1.AIMResolvedReference{
		Name:  obs.modelName,
		Scope: aimv1alpha1.AIMResolutionScopeNamespace,
		Kind:  "AIMModel",
	}
	switch obs.Scope {
	case aimv1alpha1.AIMResolutionScopeCluster:
		status.ResolvedModel.Scope = aimv1alpha1.AIMResolutionScopeCluster
		status.ResolvedModel.Kind = "AIMClusterModel"
	case aimv1alpha1.AIMResolutionScopeNamespace:
		status.ResolvedModel.Namespace = obs.ModelNamespace
	}

	return false
}
