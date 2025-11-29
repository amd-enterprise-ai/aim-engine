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

const (
	// ModelImageIndexKey is the field index key for AIMModel.Spec.Image
	// TODO: Register this indexer in the controller setup:
	//   mgr.GetFieldIndexer().IndexField(ctx, &aimv1alpha1.AIMModel{}, ModelImageIndexKey,
	//       func(obj client.Object) []string {
	//           return []string{obj.(*aimv1alpha1.AIMModel).Spec.Image}
	//       })
	ModelImageIndexKey = "spec.image"

	// ClusterModelImageIndexKey is the field index key for AIMClusterModel.Spec.Image
	// TODO: Register this indexer in the controller setup:
	//   mgr.GetFieldIndexer().IndexField(ctx, &aimv1alpha1.AIMClusterModel{}, ClusterModelImageIndexKey,
	//       func(obj client.Object) []string {
	//           return []string{obj.(*aimv1alpha1.AIMClusterModel).Spec.Image}
	//       })
	ClusterModelImageIndexKey = "spec.image"

	// ServiceTemplateModelNameIndexKey is the field index key for AIMServiceTemplate.Spec.ModelName
	// TODO: Register this indexer in the controller setup:
	//   mgr.GetFieldIndexer().IndexField(ctx, &aimv1alpha1.AIMServiceTemplate{}, ServiceTemplateModelNameIndexKey,
	//       func(obj client.Object) []string {
	//           return []string{obj.(*aimv1alpha1.AIMServiceTemplate).Spec.ModelName}
	//       })
	ServiceTemplateModelNameIndexKey = "spec.modelName"

	// ClusterServiceTemplateModelNameIndexKey is the field index key for AIMClusterServiceTemplate.Spec.ModelName
	// TODO: Register this indexer in the controller setup:
	//   mgr.GetFieldIndexer().IndexField(ctx, &aimv1alpha1.AIMClusterServiceTemplate{}, ClusterServiceTemplateModelNameIndexKey,
	//       func(obj client.Object) []string {
	//           return []string{obj.(*aimv1alpha1.AIMClusterServiceTemplate).Spec.ModelName}
	//       })
	ClusterServiceTemplateModelNameIndexKey = "spec.modelName"
)

// ============================================================================
// FETCH
// ============================================================================

type ServiceModelFetchResult struct {
	// Resolved model (either from Ref or Image lookup)
	NamespaceModel *aimv1alpha1.AIMModel
	ClusterModel   *aimv1alpha1.AIMClusterModel

	// For Image-based resolution: track if multiple models found (error case)
	MultipleModelsFound bool
	MultipleModelsError error

	// Templates for the resolved model
	NamespaceTemplatesForModel []aimv1alpha1.AIMServiceTemplate
	ClusterTemplatesForModel   []aimv1alpha1.AIMClusterServiceTemplate
}

func fetchServiceModelResult(ctx context.Context, c client.Client, service *aimv1alpha1.AIMService) (ServiceModelFetchResult, error) {
	result := ServiceModelFetchResult{}

	// Case 1: Model specified by Ref
	if modelName := service.Spec.Model.Ref; modelName != nil && *modelName != "" {
		return fetchServiceModelResultForModelRef(ctx, c, *modelName, service.Namespace)
	}

	// Case 2: Model specified by Image - search for existing models with this image
	if modelImage := service.Spec.Model.Image; modelImage != nil && *modelImage != "" {
		return fetchServiceModelResultForModelImage(ctx, c, *modelImage, service.Namespace)
	}

	// TODO handle custom image case here (later)

	return result, nil
}

func fetchServiceModelResultForModelRef(ctx context.Context, c client.Client, modelName string, namespace string) (ServiceModelFetchResult, error) {
	result := ServiceModelFetchResult{}

	// Try namespace-scoped model first
	model := &aimv1alpha1.AIMModel{}
	if err := c.Get(ctx, client.ObjectKey{Name: modelName, Namespace: namespace}, model); err != nil && !errors.IsNotFound(err) {
		return result, fmt.Errorf("failed to fetch namespace model: %w", err)
	} else if err == nil {
		result.NamespaceModel = model
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
		result.ClusterModel = clusterModel
		// Fetch templates for this cluster model
		if err := fetchTemplatesForModel(ctx, c, modelName, "", &result); err != nil {
			return result, err
		}
	}
	return result, nil
}

func fetchServiceModelResultForModelImage(ctx context.Context, c client.Client, modelImage string, namespace string) (ServiceModelFetchResult, error) {
	result := ServiceModelFetchResult{}

	// List namespace-scoped models with this image using field indexer
	var nsModels aimv1alpha1.AIMModelList
	if err := c.List(ctx, &nsModels,
		client.InNamespace(namespace),
		client.MatchingFields{ModelImageIndexKey: modelImage},
	); err != nil {
		return result, fmt.Errorf("failed to list namespace models by image: %w", err)
	}

	if len(nsModels.Items) == 1 {
		result.NamespaceModel = &nsModels.Items[0]
		// Fetch templates for this namespace model
		err := fetchTemplatesForModel(ctx, c, result.NamespaceModel.Name, namespace, &result)
		return result, err
	} else if len(nsModels.Items) > 1 {
		result.MultipleModelsFound = true
		result.MultipleModelsError = fmt.Errorf("more than one model found for image %q in the same scope", modelImage)
		return result, nil
	}

	// List cluster-scoped models with this image using field indexer
	var clusterModels aimv1alpha1.AIMClusterModelList
	if err := c.List(ctx, &clusterModels,
		client.MatchingFields{ClusterModelImageIndexKey: modelImage},
	); err != nil {
		return result, fmt.Errorf("failed to list cluster models by image: %w", err)
	}

	// Check: max 1 namespace model and max 1 cluster model
	// Namespace takes precedence over cluster
	if len(clusterModels.Items) == 1 {
		result.ClusterModel = &clusterModels.Items[0]
		// Fetch templates for this cluster model
		err := fetchTemplatesForModel(ctx, c, result.ClusterModel.Name, "", &result)
		return result, err
	} else if len(clusterModels.Items) > 1 {
		// Multiple models in same scope - error case
		result.MultipleModelsFound = true
		result.MultipleModelsError = fmt.Errorf("more than one model found for image %q in the same scope", modelImage)
		return result, nil
	}
	// If no models found, result will be empty (needs to be created)
	return result, nil
}

// fetchTemplatesForModel fetches templates for a given model based on scope
// If namespace is provided, fetches namespace-scoped templates
// If namespace is empty, fetches cluster-scoped templates
func fetchTemplatesForModel(ctx context.Context, c client.Client, modelName string, namespace string, result *ServiceModelFetchResult) error {
	if namespace != "" {
		// Namespace-scoped model - fetch namespace templates only
		var nsTemplates aimv1alpha1.AIMServiceTemplateList
		if err := c.List(ctx, &nsTemplates,
			client.InNamespace(namespace),
			client.MatchingFields{ServiceTemplateModelNameIndexKey: modelName},
		); err != nil {
			return fmt.Errorf("failed to list namespace templates for model: %w", err)
		}
		result.NamespaceTemplatesForModel = nsTemplates.Items
	} else {
		// Cluster-scoped model - fetch cluster templates only
		var clusterTemplates aimv1alpha1.AIMClusterServiceTemplateList
		if err := c.List(ctx, &clusterTemplates,
			client.MatchingFields{ClusterServiceTemplateModelNameIndexKey: modelName},
		); err != nil {
			return fmt.Errorf("failed to list cluster templates for model: %w", err)
		}
		result.ClusterTemplatesForModel = clusterTemplates.Items
	}

	return nil
}

// ============================================================================
// OBSERVE
// ============================================================================

type ServiceModelObservation struct {
	ModelName          string
	ModelNamespace     string // Namespace of the resolved model (empty for cluster-scoped models)
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

func observeServiceModel(_ context.Context, _ client.Client, service *aimv1alpha1.AIMService, result ServiceModelFetchResult) ServiceModelObservation {
	obs := ServiceModelObservation{}

	// Case 1: Model specified by Ref
	if service.Spec.Model.Ref != nil && *service.Spec.Model.Ref != "" {
		if result.NamespaceModel != nil {
			obs.ModelName = result.NamespaceModel.Name
			obs.ModelNamespace = result.NamespaceModel.Namespace
			obs.ModelFound = true
			obs.ModelReady = result.NamespaceModel.Status.Status == aimv1alpha1.AIMModelStatusReady
			obs.ModelSpec = &result.NamespaceModel.Spec
			obs.Scope = aimv1alpha1.AIMResolutionScopeNamespace
		} else if result.ClusterModel != nil {
			obs.ModelName = result.ClusterModel.Name
			obs.ModelNamespace = "" // Cluster-scoped models have no namespace
			obs.ModelFound = true
			obs.ModelReady = result.ClusterModel.Status.Status == aimv1alpha1.AIMModelStatusReady
			obs.ModelSpec = &result.ClusterModel.Spec
			obs.Scope = aimv1alpha1.AIMResolutionScopeCluster
		} else {
			// Model ref not found
			obs.ModelResolutionErr = fmt.Errorf("model %q not found", *service.Spec.Model.Ref)
		}
		return obs
	}

	// Case 2: Model specified by Image
	if service.Spec.Model.Image != nil && *service.Spec.Model.Image != "" {
		// Check for multiple models error from fetch
		if result.MultipleModelsFound {
			obs.MultipleModels = true
			obs.ModelResolutionErr = result.MultipleModelsError
			return obs
		}

		// Check if a model was found
		if result.NamespaceModel != nil {
			obs.ModelName = result.NamespaceModel.Name
			obs.ModelNamespace = result.NamespaceModel.Namespace
			obs.ModelFound = true
			obs.ModelReady = result.NamespaceModel.Status.Status == aimv1alpha1.AIMModelStatusReady
			obs.ModelSpec = &result.NamespaceModel.Spec
			obs.Scope = aimv1alpha1.AIMResolutionScopeNamespace
		} else if result.ClusterModel != nil {
			obs.ModelName = result.ClusterModel.Name
			obs.ModelNamespace = "" // Cluster-scoped models have no namespace
			obs.ModelFound = true
			obs.ModelReady = result.ClusterModel.Status.Status == aimv1alpha1.AIMModelStatusReady
			obs.ModelSpec = &result.ClusterModel.Spec
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

//nolint:unused // will be used when Plan phase is fully implemented
func planServiceModel(obs ServiceModelObservation, service *aimv1alpha1.AIMService) client.Object {
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
	obs ServiceModelObservation,
) bool {
	// Check for image parse errors (terminal error)
	if obs.ImageParseErr != nil {
		h.Failed("InvalidImageReference", obs.ImageParseErr.Error())
		cm.MarkFalse("ModelResolved", "InvalidImageReference", obs.ImageParseErr.Error(), controllerutils.LevelWarning)
		return true // Terminal error, stop reconciliation
	}

	if obs.ModelResolutionErr != nil {
		if obs.MultipleModels {
			h.Degraded("MultipleModelsFound", obs.ModelResolutionErr.Error())
			cm.MarkFalse("ModelResolved", "MultipleModelsFound", "Multiple models found with same image", controllerutils.LevelWarning)
		} else {
			h.Degraded("ModelNotFound", obs.ModelResolutionErr.Error())
			cm.MarkFalse("ModelResolved", "ModelNotFound", obs.ModelResolutionErr.Error(), controllerutils.LevelWarning)
		}
		return true
	}

	if obs.ShouldCreateModel {
		h.Progressing("CreatingModel", "Creating model for service")
		cm.MarkFalse("ModelResolved", "CreatingModel", "Model being created", controllerutils.LevelNormal)
		return false
	}

	if !obs.ModelFound {
		// Should not happen - either found, should create, or has error
		return false
	}

	if !obs.ModelReady {
		h.Progressing("ModelNotReady", fmt.Sprintf("Model %q is not ready", obs.ModelName))
		cm.MarkFalse("ModelResolved", "ModelNotReady", fmt.Sprintf("Model %q is not ready", obs.ModelName), controllerutils.LevelNormal)
		return true
	}

	// Model found and ready
	cm.MarkTrue("ModelResolved", "ModelResolved", fmt.Sprintf("Model %q is ready", obs.ModelName), controllerutils.LevelNormal)
	status.ResolvedModel = &aimv1alpha1.AIMResolvedReference{
		Name:  obs.ModelName,
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
