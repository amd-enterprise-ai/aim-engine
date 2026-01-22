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
	"context"
	"errors"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

// ErrMultipleModelsFound is returned when multiple models exist with the same image URI
var ErrMultipleModelsFound = errors.New("multiple models found with the same image")

// ModelFetchResult holds the result of fetching/resolving a model for the service.
type ModelFetchResult struct {
	Model        controllerutils.FetchResult[*aimv1alpha1.AIMModel]
	ClusterModel controllerutils.FetchResult[*aimv1alpha1.AIMClusterModel]
	// ImageURI is set when Model.Image is specified (needed for building the model in PlanResources).
	ImageURI string
	// CustomSpec is set when Model.Custom is specified (needed for building the model in PlanResources).
	CustomSpec *aimv1alpha1.AIMServiceModelCustom
}

// fetchModel resolves the model for the service.
// It handles three modes:
// 1. Model.Name - reference to existing AIMModel or AIMClusterModel
// 2. Model.Image - container image URI (signals creation needed if no match)
// 3. Model.Custom - custom model configuration
//
// If a resolved model reference exists in status (which implies it was Ready when stored)
// and is still Ready, it uses that directly. Otherwise, it re-resolves.
func fetchModel(
	ctx context.Context,
	c client.Client,
	service *aimv1alpha1.AIMService,
) ModelFetchResult {
	logger := log.FromContext(ctx)

	// Try to use previously resolved model if Ready
	if result, shouldContinue := tryFetchResolvedModel(ctx, c, service); !shouldContinue {
		return result
	}

	var result ModelFetchResult

	// Case 1: Model.Name - look up by name
	if modelNamePtr := service.Spec.Model.Name; modelNamePtr != nil && *modelNamePtr != "" {
		modelName := strings.TrimSpace(*modelNamePtr)
		logger.V(1).Info("looking up model by ref", "modelName", modelName)

		// Try namespace-scoped first
		result.Model = controllerutils.Fetch(ctx, c, client.ObjectKey{
			Namespace: service.Namespace,
			Name:      modelName,
		}, &aimv1alpha1.AIMModel{})

		if result.Model.OK() {
			return result
		}

		if !result.Model.IsNotFound() {
			// Real error, not just missing
			return result
		}

		// Try cluster-scoped
		result.ClusterModel = controllerutils.Fetch(ctx, c, client.ObjectKey{
			Name: modelName,
		}, &aimv1alpha1.AIMClusterModel{})

		if result.ClusterModel.OK() {
			// Clear the namespace-scoped error since we found a cluster model
			result.Model.Error = nil
			return result
		}

		if result.ClusterModel.IsNotFound() {
			// Neither found - report as missing upstream dependency
			result.Model.Error = controllerutils.NewMissingUpstreamDependencyError(
				aimv1alpha1.AIMServiceReasonModelNotFound,
				fmt.Sprintf("model %q not found in namespace %s or cluster scope", modelName, service.Namespace),
				nil,
			)
			result.ClusterModel.Error = nil
		}
		return result
	}

	// Case 2: Model.Image - resolve model from image
	if service.Spec.Model.Image != nil && *service.Spec.Model.Image != "" {
		imageURI := strings.TrimSpace(*service.Spec.Model.Image)
		logger.V(1).Info("resolving model from image", "image", imageURI)

		result.ImageURI = imageURI
		result.Model, result.ClusterModel = resolveModelFromImage(ctx, c, service, imageURI)
		return result
	}

	// Case 3: Model.Custom - custom model configuration
	// Custom models from AIMService are always namespace-scoped
	if service.Spec.Model.Custom != nil {
		logger.V(1).Info("resolving custom model")
		result.CustomSpec = service.Spec.Model.Custom
		result.Model = resolveCustomModel(ctx, c, service, service.Spec.Model.Custom)
		return result
	}

	// No model specified
	result.Model.Error = controllerutils.NewInvalidSpecError(
		aimv1alpha1.AIMServiceReasonModelNotFound,
		"no model specified in service spec",
		nil,
	)
	return result
}

// tryFetchResolvedModel attempts to fetch a previously resolved model reference.
// Returns the result and whether to continue with normal resolution.
// If the resolved model is Ready, returns (result, false) to use it directly.
// If not Ready, deleted, or has an error, returns appropriate state and whether to continue.
func tryFetchResolvedModel(
	ctx context.Context,
	c client.Client,
	service *aimv1alpha1.AIMService,
) (result ModelFetchResult, shouldContinue bool) {
	if service.Status.ResolvedModel == nil {
		return result, true
	}

	logger := log.FromContext(ctx)
	ref := service.Status.ResolvedModel

	switch ref.Scope {
	case aimv1alpha1.AIMResolutionScopeNamespace:
		result.Model = controllerutils.Fetch(ctx, c, ref.NamespacedName(), &aimv1alpha1.AIMModel{})
		if result.Model.OK() && result.Model.Value.Status.Status == constants.AIMStatusReady {
			logger.V(1).Info("using resolved model", "name", ref.Name)
			if service.Spec.Model.Image != nil && *service.Spec.Model.Image != "" {
				result.ImageURI = strings.TrimSpace(*service.Spec.Model.Image)
			}
			return result, false
		}
		// Not Ready or deleted - log and continue to search
		if result.Model.OK() {
			logger.V(1).Info("resolved model not ready, re-resolving",
				"name", ref.Name, "status", result.Model.Value.Status.Status)
		} else if result.Model.IsNotFound() {
			logger.V(1).Info("resolved model deleted, re-resolving", "name", ref.Name)
		} else {
			return result, false // Real error - stop
		}

	case aimv1alpha1.AIMResolutionScopeCluster:
		result.ClusterModel = controllerutils.Fetch(ctx, c, ref.NamespacedName(), &aimv1alpha1.AIMClusterModel{})
		if result.ClusterModel.OK() && result.ClusterModel.Value.Status.Status == constants.AIMStatusReady {
			logger.V(1).Info("using resolved cluster model", "name", ref.Name)
			if service.Spec.Model.Image != nil && *service.Spec.Model.Image != "" {
				result.ImageURI = strings.TrimSpace(*service.Spec.Model.Image)
			}
			return result, false
		}
		// Not Ready or deleted - log and continue to search
		if result.ClusterModel.OK() {
			logger.V(1).Info("resolved cluster model not ready, re-resolving",
				"name", ref.Name, "status", result.ClusterModel.Value.Status.Status)
		} else if result.ClusterModel.IsNotFound() {
			logger.V(1).Info("resolved cluster model deleted, re-resolving", "name", ref.Name)
		} else {
			return result, false // Real error - stop
		}
	}

	return ModelFetchResult{}, true
}

// resolveModelFromImage searches for existing models matching the image URI.
// Returns the found model (if any). If no model is found and no error occurred,
// both Model and ClusterModel will be nil - ComposeState determines if creation is needed.
//
// Note on concurrency: If multiple services with the same image URI reconcile concurrently,
// both may determine that model creation is needed. This is handled gracefully by Kubernetes'
// server-side apply (SSA) - the second apply will simply update the existing model.
// The model is created without an owner reference specifically to allow sharing across services.
func resolveModelFromImage(
	ctx context.Context,
	c client.Client,
	service *aimv1alpha1.AIMService,
	imageURI string,
) (controllerutils.FetchResult[*aimv1alpha1.AIMModel], controllerutils.FetchResult[*aimv1alpha1.AIMClusterModel]) {
	logger := log.FromContext(ctx)

	var modelResult controllerutils.FetchResult[*aimv1alpha1.AIMModel]
	var clusterModelResult controllerutils.FetchResult[*aimv1alpha1.AIMClusterModel]

	if imageURI == "" {
		modelResult.Error = fmt.Errorf("image URI is empty")
		return modelResult, clusterModelResult
	}

	// Search for existing models with this image
	models, err := findModelsWithImage(ctx, c, service.Namespace, imageURI)
	if err != nil {
		modelResult.Error = fmt.Errorf("failed to search for models: %w", err)
		return modelResult, clusterModelResult
	}

	switch len(models) {
	case 0:
		// No models found - return empty results (no error)
		// ComposeState will determine that creation is needed based on imageURI being set
		logger.V(1).Info("no existing model found for image", "image", imageURI)
		return modelResult, clusterModelResult

	case 1:
		// Single match - fetch and return it
		ref := models[0]
		logger.V(1).Info("found existing model", "name", ref.Name, "scope", ref.Scope)

		if ref.Scope == aimv1alpha1.AIMResolutionScopeNamespace {
			modelResult = controllerutils.Fetch(ctx, c, client.ObjectKey{
				Namespace: service.Namespace,
				Name:      ref.Name,
			}, &aimv1alpha1.AIMModel{})
		} else {
			clusterModelResult = controllerutils.Fetch(ctx, c, client.ObjectKey{
				Name: ref.Name,
			}, &aimv1alpha1.AIMClusterModel{})
		}
		return modelResult, clusterModelResult

	default:
		// Multiple matches - error
		names := make([]string, len(models))
		for i, m := range models {
			if m.Scope == aimv1alpha1.AIMResolutionScopeNamespace {
				names[i] = fmt.Sprintf("%s/%s (namespace)", service.Namespace, m.Name)
			} else {
				names[i] = fmt.Sprintf("%s (cluster)", m.Name)
			}
		}
		modelResult.Error = fmt.Errorf("%w with image %q: %s", ErrMultipleModelsFound, imageURI, strings.Join(names, ", "))
		return modelResult, clusterModelResult
	}
}

// modelReference represents a found model
type modelReference struct {
	Name  string
	Scope aimv1alpha1.AIMResolutionScope
}

// findModelsWithImage searches for AIMModel and AIMClusterModel resources with the specified image
func findModelsWithImage(
	ctx context.Context,
	c client.Client,
	namespace string,
	imageURI string,
) ([]modelReference, error) {
	var results []modelReference

	// Search namespace-scoped models
	if namespace != "" {
		var modelList aimv1alpha1.AIMModelList
		if err := c.List(ctx, &modelList, client.InNamespace(namespace)); err != nil {
			return nil, fmt.Errorf("failed to list AIMModels: %w", err)
		}
		for i := range modelList.Items {
			if modelList.Items[i].Spec.Image == imageURI {
				results = append(results, modelReference{
					Name:  modelList.Items[i].Name,
					Scope: aimv1alpha1.AIMResolutionScopeNamespace,
				})
			}
		}
	}

	// Search cluster-scoped models
	var clusterModelList aimv1alpha1.AIMClusterModelList
	if err := c.List(ctx, &clusterModelList); err != nil {
		return nil, fmt.Errorf("failed to list AIMClusterModels: %w", err)
	}
	for i := range clusterModelList.Items {
		if clusterModelList.Items[i].Spec.Image == imageURI {
			results = append(results, modelReference{
				Name:  clusterModelList.Items[i].Name,
				Scope: aimv1alpha1.AIMResolutionScopeCluster,
			})
		}
	}

	return results, nil
}

// planModel creates an AIMModel if the service specifies an image or custom model but no matching model exists.
// For image-based models, the model is created without an owner reference so it can be shared.
// For custom models, the model is created with an owner reference to the AIMService.
func planModel(
	service *aimv1alpha1.AIMService,
	obs ServiceObservation,
) client.Object {
	if !obs.needsModelCreation {
		return nil
	}

	// Custom model case
	if obs.modelResult.CustomSpec != nil {
		return buildModelForCustom(service, obs.modelResult.CustomSpec, obs.pendingModelName)
	}

	// Image URI case - model name was validated in ComposeState
	return buildModelForImage(service, obs.modelResult.ImageURI, obs.pendingModelName)
}

// buildModelForImage constructs a new namespace-scoped AIMModel for the given image.
// The model is not created - it should be added to PlanResult for SSA application.
// The caller should validate the imageURI first using GenerateModelName.
func buildModelForImage(
	service *aimv1alpha1.AIMService,
	imageURI string,
	modelName string,
) *aimv1alpha1.AIMModel {
	// Build namespace-scoped model
	return &aimv1alpha1.AIMModel{
		TypeMeta: metav1.TypeMeta{
			APIVersion: aimv1alpha1.GroupVersion.String(),
			Kind:       "AIMModel",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      modelName,
			Namespace: service.Namespace,
			Labels: map[string]string{
				constants.LabelKeyOrigin: constants.LabelValueOriginAutoGenerated,
			},
		},
		Spec: aimv1alpha1.AIMModelSpec{
			Image:              imageURI,
			RuntimeConfigRef:   service.Spec.RuntimeConfigRef,
			ImagePullSecrets:   utils.CopyPullSecrets(service.Spec.ImagePullSecrets),
			ServiceAccountName: service.Spec.ServiceAccountName,
			Resources:          corev1.ResourceRequirements{},
		},
	}
}

// GenerateModelName creates a Kubernetes-valid name from an image URI using utils.GenerateDerivedName.
// Returns an error if the image URI cannot be parsed.
func GenerateModelName(imageURI string) (string, error) {
	// Extract image parts - fail if image can't be parsed
	parts, err := utils.ExtractImageParts(imageURI)
	if err != nil {
		return "", fmt.Errorf("invalid image URI %q: %w", imageURI, err)
	}

	// Generate name from image name and tag with hash for uniqueness
	return utils.GenerateDerivedName([]string{parts.Name, parts.Tag}, utils.WithHashSource(imageURI))
}

// resolveCustomModel searches for an existing model matching the custom spec,
// or returns empty result to signal that a model needs to be created.
// Custom models from AIMService are always namespace-scoped.
func resolveCustomModel(
	ctx context.Context,
	c client.Client,
	service *aimv1alpha1.AIMService,
	custom *aimv1alpha1.AIMServiceModelCustom,
) controllerutils.FetchResult[*aimv1alpha1.AIMModel] {
	logger := log.FromContext(ctx)

	var modelResult controllerutils.FetchResult[*aimv1alpha1.AIMModel]

	// Search for existing matching model
	existingModel, err := FindMatchingCustomModel(ctx, c, service.Namespace, custom)
	if err != nil {
		modelResult.Error = fmt.Errorf("failed to search for matching custom model: %w", err)
		return modelResult
	}

	if existingModel != nil {
		logger.V(1).Info("found existing matching custom model", "name", existingModel.Name)
		modelResult.Value = existingModel
		return modelResult
	}

	// No matching model found - return empty result
	// ComposeState will determine that creation is needed based on CustomSpec being set
	logger.V(1).Info("no matching custom model found, will create new one")
	return modelResult
}

// buildModelForCustom constructs a new namespace-scoped AIMModel for a custom model spec.
// The model is not created - it should be added to PlanResult for SSA application.
func buildModelForCustom(
	service *aimv1alpha1.AIMService,
	custom *aimv1alpha1.AIMServiceModelCustom,
	modelName string,
) *aimv1alpha1.AIMModel {
	// Build namespace-scoped model with owner reference to AIMService
	model := &aimv1alpha1.AIMModel{
		TypeMeta: metav1.TypeMeta{
			APIVersion: aimv1alpha1.GroupVersion.String(),
			Kind:       "AIMModel",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      modelName,
			Namespace: service.Namespace,
			Labels: map[string]string{
				constants.LabelKeyOrigin:      constants.LabelValueOriginAutoGenerated,
				constants.LabelKeyCustomModel: "true",
			},
		},
		Spec: aimv1alpha1.AIMModelSpec{
			Image:              custom.BaseImage,
			ModelSources:       custom.ModelSources,
			Hardware:           &custom.Hardware,
			RuntimeConfigRef:   service.Spec.RuntimeConfigRef,
			ImagePullSecrets:   utils.CopyPullSecrets(service.Spec.ImagePullSecrets),
			ServiceAccountName: service.Spec.ServiceAccountName,
			Env:                service.Spec.Env,
		},
	}

	// Custom templates will be auto-generated from hardware when AIMModel is reconciled

	return model
}
