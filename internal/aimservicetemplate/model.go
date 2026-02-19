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
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

// ErrModelNotFound is returned when a model is not found in the catalog.
var ErrModelNotFound = errors.New("model not found in catalog")

// ModelLookupResult captures the resolved model metadata from the model catalog.
type ModelLookupResult struct {
	// Image is the container image URI.
	Image string

	// Resources contains resource requirements from the model spec.
	Resources corev1.ResourceRequirements

	// ImagePullSecrets contains image pull secrets from the model spec.
	ImagePullSecrets []corev1.LocalObjectReference

	// ServiceAccountName from the model spec.
	ServiceAccountName string
}

// DeepCopy returns a deep copy of the ModelLookupResult.
func (r *ModelLookupResult) DeepCopy() *ModelLookupResult {
	if r == nil {
		return nil
	}
	result := &ModelLookupResult{
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

// LookupModelForNamespaceTemplate looks up the model for a namespace-scoped template.
// It searches AIMModel resources in the specified namespace first, then falls back to
// cluster-scoped AIMClusterModel resources.
// Returns ErrModelNotFound if no model is found in either location.
func LookupModelForNamespaceTemplate(ctx context.Context, c client.Client, namespace, modelName string) controllerutils.FetchResult[*ModelLookupResult] {
	// Try namespace-scoped AIMModel first
	nsModel := &aimv1alpha1.AIMModel{}
	if err := c.Get(ctx, client.ObjectKey{Name: modelName, Namespace: namespace}, nsModel); err == nil {
		return controllerutils.FetchResult[*ModelLookupResult]{
			Value: &ModelLookupResult{
				Image:              nsModel.Spec.Image,
				Resources:          *nsModel.Spec.Resources.DeepCopy(),
				ImagePullSecrets:   copyImagePullSecrets(nsModel.Spec.ImagePullSecrets),
				ServiceAccountName: nsModel.Spec.ServiceAccountName,
			},
		}
	} else if !apierrors.IsNotFound(err) {
		return controllerutils.FetchResult[*ModelLookupResult]{
			Error: fmt.Errorf("failed to lookup AIMModel: %w", err),
		}
	}

	// Fall back to cluster-scoped AIMClusterModel
	return LookupModelForClusterTemplate(ctx, c, modelName)
}

// LookupModelForClusterTemplate looks up the model for a cluster-scoped template.
// It searches only in AIMClusterModel resources.
// Returns ErrModelNotFound if no model is found in the catalog.
func LookupModelForClusterTemplate(ctx context.Context, c client.Client, modelName string) controllerutils.FetchResult[*ModelLookupResult] {
	clusterModel := &aimv1alpha1.AIMClusterModel{}
	if err := c.Get(ctx, client.ObjectKey{Name: modelName}, clusterModel); err == nil {
		return controllerutils.FetchResult[*ModelLookupResult]{
			Value: &ModelLookupResult{
				Image:              clusterModel.Spec.Image,
				Resources:          *clusterModel.Spec.Resources.DeepCopy(),
				ImagePullSecrets:   copyImagePullSecrets(clusterModel.Spec.ImagePullSecrets),
				ServiceAccountName: clusterModel.Spec.ServiceAccountName,
			},
		}
	} else if !apierrors.IsNotFound(err) {
		return controllerutils.FetchResult[*ModelLookupResult]{
			Error: fmt.Errorf("failed to lookup AIMClusterModel: %w", err),
		}
	}

	// Not found in either location
	return controllerutils.FetchResult[*ModelLookupResult]{
		Error: controllerutils.NewMissingUpstreamDependencyError(
			"ModelNotFound",
			fmt.Sprintf("no AIMModel or AIMClusterModel found with name %q", modelName),
			ErrModelNotFound,
		),
	}
}

// GetModelLookupHealth inspects a ModelLookupResult to determine component health.
func GetModelLookupHealth(result *ModelLookupResult) controllerutils.ComponentHealth {
	if result == nil {
		return controllerutils.ComponentHealth{
			State:   constants.AIMStatusFailed,
			Reason:  "ModelNotResolved",
			Message: "Model lookup result is nil",
		}
	}

	if result.Image == "" {
		return controllerutils.ComponentHealth{
			State:   constants.AIMStatusFailed,
			Reason:  "ImageNotSpecified",
			Message: "Model does not specify an image",
		}
	}

	return controllerutils.ComponentHealth{
		State:   constants.AIMStatusReady,
		Reason:  "ModelResolved",
		Message: "Model successfully resolved from catalog",
	}
}

// GetModelHealth inspects an AIMModel to determine component health.
// Used by ServiceTemplateReconciler to check upstream model availability.
//
// To avoid circular dependency deadlocks (model status depends on template status,
// template status depends on model status), we only propagate model failures that
// are NOT caused by template failures. If the model is Failed/Degraded solely because
// its templates are failing (ServiceTemplatesReady=False), we ignore it — otherwise
// the template and model would keep each other in a failed state permanently.
func GetModelHealth(model *aimv1alpha1.AIMModel) controllerutils.ComponentHealth {
	if model == nil {
		return controllerutils.ComponentHealth{
			State:   constants.AIMStatusPending,
			Reason:  "ModelNotFound",
			Message: "Waiting for AIMModel to be created",
		}
	}

	if model.Spec.Image == "" {
		return controllerutils.ComponentHealth{
			State:   constants.AIMStatusFailed,
			Reason:  "ImageNotSpecified",
			Message: "Model does not specify an image",
		}
	}

	if isModelUnhealthy(model.Status.Status) &&
		hasNonTemplateFailure(model.Status.Conditions, "ServiceTemplates") {
		return controllerutils.ComponentHealth{
			State:   model.Status.Status,
			Reason:  "ModelFailed",
			Message: fmt.Sprintf("AIMModel is in %s state", model.Status.Status),
		}
	}

	return controllerutils.ComponentHealth{
		State:   constants.AIMStatusReady,
		Reason:  "ModelFound",
		Message: "AIMModel found with valid image",
	}
}

// GetClusterModelHealth inspects an AIMClusterModel to determine component health.
// Used by ClusterServiceTemplateReconciler to check upstream model availability.
// See GetModelHealth for the circular dependency avoidance rationale.
func GetClusterModelHealth(model *aimv1alpha1.AIMClusterModel) controllerutils.ComponentHealth {
	if model == nil {
		return controllerutils.ComponentHealth{
			State:   constants.AIMStatusPending,
			Reason:  "ClusterModelNotFound",
			Message: "Waiting for AIMClusterModel to be created",
		}
	}

	if model.Spec.Image == "" {
		return controllerutils.ComponentHealth{
			State:   constants.AIMStatusFailed,
			Reason:  "ImageNotSpecified",
			Message: "Cluster model does not specify an image",
		}
	}

	if isModelUnhealthy(model.Status.Status) &&
		hasNonTemplateFailure(model.Status.Conditions, "ClusterServiceTemplates") {
		return controllerutils.ComponentHealth{
			State:   model.Status.Status,
			Reason:  "ClusterModelFailed",
			Message: fmt.Sprintf("AIMClusterModel is in %s state", model.Status.Status),
		}
	}

	return controllerutils.ComponentHealth{
		State:   constants.AIMStatusReady,
		Reason:  "ClusterModelFound",
		Message: "AIMClusterModel found with valid image",
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

// isModelUnhealthy returns true if the model status indicates a problem that
// should be propagated to templates (Failed or Degraded).
func isModelUnhealthy(status constants.AIMStatus) bool {
	return status == constants.AIMStatusFailed || status == constants.AIMStatusDegraded
}

// hasNonTemplateFailure checks whether a model has Failed for reasons other than
// its own templates failing. This prevents a circular dependency where templates
// and models keep each other in Failed state.
//
// templateComponent is the component name to skip (e.g. "ServiceTemplates" or
// "ClusterServiceTemplates"). The derived condition type "Ready" is also skipped
// since it's the overall rollup.
func hasNonTemplateFailure(conditions []metav1.Condition, templateComponent string) bool {
	templateCondition := templateComponent + controllerutils.ComponentConditionSuffix
	for _, c := range conditions {
		if c.Type == controllerutils.ConditionTypeReady || c.Type == templateCondition {
			continue
		}
		if strings.HasSuffix(c.Type, controllerutils.ComponentConditionSuffix) && c.Status == metav1.ConditionFalse {
			return true
		}
	}
	return false
}
