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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

// ErrMultipleModelsFound is returned when multiple models exist with the same image URI
var ErrMultipleModelsFound = errors.New("multiple models found with the same image")

// resolveOrCreateModelFromImage searches for existing models matching the image URI,
// or creates a new one if none exists.
func resolveOrCreateModelFromImage(
	ctx context.Context,
	c client.Client,
	_ kubernetes.Interface,
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
		// No models found - create one
		logger.V(1).Info("no existing model found, creating new one", "image", imageURI)
		model, err := createModelForImage(ctx, c, service, imageURI)
		if err != nil {
			modelResult.Error = err
			return modelResult, clusterModelResult
		}
		modelResult.Value = model
		return modelResult, clusterModelResult

	case 1:
		// Single match - fetch and return it
		ref := models[0]
		logger.V(1).Info("found existing model", "name", ref.Name, "scope", ref.Scope)

		if ref.Scope == TemplateScopeNamespace {
			model := &aimv1alpha1.AIMModel{}
			err := c.Get(ctx, client.ObjectKey{
				Namespace: service.Namespace,
				Name:      ref.Name,
			}, model)
			if err != nil {
				modelResult.Error = err
				return modelResult, clusterModelResult
			}
			modelResult.Value = model
		} else {
			model := &aimv1alpha1.AIMClusterModel{}
			err := c.Get(ctx, client.ObjectKey{Name: ref.Name}, model)
			if err != nil {
				clusterModelResult.Error = err
				return modelResult, clusterModelResult
			}
			clusterModelResult.Value = model
		}
		return modelResult, clusterModelResult

	default:
		// Multiple matches - error
		names := make([]string, len(models))
		for i, m := range models {
			if m.Scope == TemplateScopeNamespace {
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
	Scope TemplateScope
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
					Scope: TemplateScopeNamespace,
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
				Scope: TemplateScopeCluster,
			})
		}
	}

	return results, nil
}

// createModelForImage creates a new namespace-scoped AIMModel for the given image.
func createModelForImage(
	ctx context.Context,
	c client.Client,
	service *aimv1alpha1.AIMService,
	imageURI string,
) (*aimv1alpha1.AIMModel, error) {
	// Generate model name from image URI
	modelName, err := generateModelName(imageURI)
	if err != nil {
		return nil, fmt.Errorf("failed to generate model name: %w", err)
	}

	// Create namespace-scoped model
	model := &aimv1alpha1.AIMModel{
		TypeMeta: metav1.TypeMeta{
			APIVersion: aimv1alpha1.GroupVersion.String(),
			Kind:       "AIMModel",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      modelName,
			Namespace: service.Namespace,
			Labels: map[string]string{
				constants.LabelAutoCreated: "true",
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

	if err := c.Create(ctx, model); err != nil {
		if apierrors.IsAlreadyExists(err) {
			// Race condition - another controller created it, fetch and return
			existing := &aimv1alpha1.AIMModel{}
			if getErr := c.Get(ctx, client.ObjectKey{
				Namespace: service.Namespace,
				Name:      modelName,
			}, existing); getErr != nil {
				return nil, fmt.Errorf("model exists but failed to fetch: %w", getErr)
			}
			return existing, nil
		}
		return nil, fmt.Errorf("failed to create AIMModel: %w", err)
	}

	return model, nil
}

// generateModelName creates a Kubernetes-valid name from an image URI using utils.GenerateDerivedName.
func generateModelName(imageURI string) (string, error) {
	// Extract image parts
	parts, err := utils.ExtractImageParts(imageURI)
	if err != nil {
		// Fallback to simple hash-based name
		return utils.GenerateDerivedName([]string{"model"}, imageURI)
	}

	// Generate name from image name and tag with hash for uniqueness
	return utils.GenerateDerivedName([]string{parts.Name, parts.Tag}, imageURI)
}
