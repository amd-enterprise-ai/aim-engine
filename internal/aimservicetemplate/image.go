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
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

// ErrImageNotFound is returned when an image is not found in the catalog.
var ErrImageNotFound = errors.New("image not found in catalog")

// ImageLookupResult captures the resolved image metadata from the model catalog.
type ImageLookupResult struct {
	// Image is the container image URI.
	Image string

	// Resources contains resource requirements from the model spec.
	Resources corev1.ResourceRequirements

	// ImagePullSecrets contains image pull secrets from the model spec.
	ImagePullSecrets []corev1.LocalObjectReference

	// ServiceAccountName from the model spec.
	ServiceAccountName string
}

// DeepCopy returns a deep copy of the ImageLookupResult.
func (r *ImageLookupResult) DeepCopy() *ImageLookupResult {
	if r == nil {
		return nil
	}
	result := &ImageLookupResult{
		Image:              r.Image,
		ServiceAccountName: r.ServiceAccountName,
	}
	result.Resources = *r.Resources.DeepCopy()
	if len(r.ImagePullSecrets) > 0 {
		result.ImagePullSecrets = make([]corev1.LocalObjectReference, len(r.ImagePullSecrets))
		copy(result.ImagePullSecrets, r.ImagePullSecrets)
	}
	return result
}

// LookupImageForNamespaceTemplate looks up the container image for a namespace-scoped template.
// It searches AIMModel resources in the specified namespace first, then falls back to
// cluster-scoped AIMClusterModel resources.
// Returns ErrImageNotFound if no image is found in either location.
func LookupImageForNamespaceTemplate(ctx context.Context, c client.Client, namespace, modelName string) controllerutils.FetchResult[*ImageLookupResult] {
	// Try namespace-scoped AIMModel first
	nsModel := &aimv1alpha1.AIMModel{}
	if err := c.Get(ctx, client.ObjectKey{Name: modelName, Namespace: namespace}, nsModel); err == nil {
		return controllerutils.FetchResult[*ImageLookupResult]{
			Value: &ImageLookupResult{
				Image:              nsModel.Spec.Image,
				Resources:          *nsModel.Spec.Resources.DeepCopy(),
				ImagePullSecrets:   copyImagePullSecrets(nsModel.Spec.ImagePullSecrets),
				ServiceAccountName: nsModel.Spec.ServiceAccountName,
			},
		}
	} else if !apierrors.IsNotFound(err) {
		return controllerutils.FetchResult[*ImageLookupResult]{
			Error: fmt.Errorf("failed to lookup AIMModel: %w", err),
		}
	}

	// Fall back to cluster-scoped AIMClusterModel
	return LookupImageForClusterTemplate(ctx, c, modelName)
}

// LookupImageForClusterTemplate looks up the container image for a cluster-scoped template.
// It searches only in AIMClusterModel resources.
// Returns ErrImageNotFound if no image is found in the catalog.
func LookupImageForClusterTemplate(ctx context.Context, c client.Client, modelName string) controllerutils.FetchResult[*ImageLookupResult] {
	clusterModel := &aimv1alpha1.AIMClusterModel{}
	if err := c.Get(ctx, client.ObjectKey{Name: modelName}, clusterModel); err == nil {
		return controllerutils.FetchResult[*ImageLookupResult]{
			Value: &ImageLookupResult{
				Image:              clusterModel.Spec.Image,
				Resources:          *clusterModel.Spec.Resources.DeepCopy(),
				ImagePullSecrets:   copyImagePullSecrets(clusterModel.Spec.ImagePullSecrets),
				ServiceAccountName: clusterModel.Spec.ServiceAccountName,
			},
		}
	} else if !apierrors.IsNotFound(err) {
		return controllerutils.FetchResult[*ImageLookupResult]{
			Error: fmt.Errorf("failed to lookup AIMClusterModel: %w", err),
		}
	}

	// Not found in either location
	return controllerutils.FetchResult[*ImageLookupResult]{
		Error: controllerutils.NewMissingUpstreamDependencyError(
			"ModelNotFound",
			fmt.Sprintf("no AIMModel or AIMClusterModel found with name %q", modelName),
			ErrImageNotFound,
		),
	}
}

// GetImageLookupHealth inspects an ImageLookupResult to determine component health.
func GetImageLookupHealth(result *ImageLookupResult) controllerutils.ComponentHealth {
	if result == nil {
		return controllerutils.ComponentHealth{
			Reason:  "ImageNotResolved",
			Message: "Image lookup result is nil",
		}
	}

	if result.Image == "" {
		return controllerutils.ComponentHealth{
			Reason:  "ImageNotSpecified",
			Message: "Model does not specify an image",
		}
	}

	return controllerutils.ComponentHealth{
		Reason:  "ImageResolved",
		Message: "Image successfully resolved from model",
	}
}

// copyImagePullSecrets creates a deep copy of image pull secrets.
func copyImagePullSecrets(secrets []corev1.LocalObjectReference) []corev1.LocalObjectReference {
	if len(secrets) == 0 {
		return nil
	}
	result := make([]corev1.LocalObjectReference, len(secrets))
	copy(result, secrets)
	return result
}
