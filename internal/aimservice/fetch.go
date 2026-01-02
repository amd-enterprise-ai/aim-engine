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
	"fmt"
	"strings"

	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

// fetchModel resolves the model for the service.
// It handles three modes:
// 1. Model.Name - reference to existing AIMModel or AIMClusterModel
// 2. Model.Image - container image URI (auto-creates model if needed)
// 3. Model.Custom - custom model configuration
func fetchModel(
	ctx context.Context,
	c client.Client,
	clientset kubernetes.Interface,
	service *aimv1alpha1.AIMService,
) (controllerutils.FetchResult[*aimv1alpha1.AIMModel], controllerutils.FetchResult[*aimv1alpha1.AIMClusterModel]) {
	logger := log.FromContext(ctx)

	var modelResult controllerutils.FetchResult[*aimv1alpha1.AIMModel]
	var clusterModelResult controllerutils.FetchResult[*aimv1alpha1.AIMClusterModel]

	// Case 1: Model.Name - look up by name
	if service.Spec.Model.Name != nil && *service.Spec.Model.Name != "" {
		modelName := strings.TrimSpace(*service.Spec.Model.Name)
		logger.V(1).Info("looking up model by ref", "modelName", modelName)

		// Try namespace-scoped first
		model := &aimv1alpha1.AIMModel{}
		err := c.Get(ctx, client.ObjectKey{
			Namespace: service.Namespace,
			Name:      modelName,
		}, model)

		if err == nil {
			modelResult.Value = model
			return modelResult, clusterModelResult
		}

		if !apierrors.IsNotFound(err) {
			modelResult.Error = err
			return modelResult, clusterModelResult
		}

		// Try cluster-scoped
		clusterModel := &aimv1alpha1.AIMClusterModel{}
		err = c.Get(ctx, client.ObjectKey{Name: modelName}, clusterModel)
		if err == nil {
			clusterModelResult.Value = clusterModel
			return modelResult, clusterModelResult
		}

		if apierrors.IsNotFound(err) {
			modelResult.Error = controllerutils.NewMissingUpstreamDependencyError(
				aimv1alpha1.AIMServiceReasonModelNotFound,
				fmt.Sprintf("model %q not found in namespace %s or cluster scope", modelName, service.Namespace),
				nil,
			)
		} else {
			clusterModelResult.Error = err
		}
		return modelResult, clusterModelResult
	}

	// Case 2: Model.Image - resolve or create model from image
	if service.Spec.Model.Image != nil && *service.Spec.Model.Image != "" {
		imageURI := strings.TrimSpace(*service.Spec.Model.Image)
		logger.V(1).Info("resolving model from image", "image", imageURI)

		modelResult, clusterModelResult = resolveOrCreateModelFromImage(ctx, c, clientset, service, imageURI)
		return modelResult, clusterModelResult
	}

	// Case 3: Model.Custom - custom model configuration
	if service.Spec.Model.Custom != nil {
		logger.V(1).Info("resolving custom model")
		// Custom models are handled differently - we create the model inline
		// For now, treat as an error until implemented
		modelResult.Error = fmt.Errorf("custom model configuration not yet implemented")
		return modelResult, clusterModelResult
	}

	// No model specified
	modelResult.Error = controllerutils.NewInvalidSpecError(
		aimv1alpha1.AIMServiceReasonModelNotFound,
		"no model specified in service spec",
		nil,
	)
	return modelResult, clusterModelResult
}

// fetchTemplate resolves the template for the service.
// It handles explicit template references and auto-selection.
func fetchTemplate(
	ctx context.Context,
	c client.Client,
	service *aimv1alpha1.AIMService,
	model controllerutils.FetchResult[*aimv1alpha1.AIMModel],
	clusterModel controllerutils.FetchResult[*aimv1alpha1.AIMClusterModel],
) (
	controllerutils.FetchResult[*aimv1alpha1.AIMServiceTemplate],
	controllerutils.FetchResult[*aimv1alpha1.AIMClusterServiceTemplate],
	*TemplateSelectionResult,
) {
	logger := log.FromContext(ctx)

	var templateResult controllerutils.FetchResult[*aimv1alpha1.AIMServiceTemplate]
	var clusterTemplateResult controllerutils.FetchResult[*aimv1alpha1.AIMClusterServiceTemplate]

	// Case 1: Explicit template name specified
	if service.Spec.Template.Name != "" {
		templateName := strings.TrimSpace(service.Spec.Template.Name)
		logger.V(1).Info("looking up template by name", "templateName", templateName)

		// Check for derived template (service has overrides)
		finalTemplateName := templateName
		if service.Spec.Overrides != nil {
			suffix := overridesSuffix(service.Spec.Overrides)
			if suffix != "" {
				finalTemplateName = derivedTemplateName(templateName, suffix)
				logger.V(1).Info("using derived template name", "derivedName", finalTemplateName)
			}
		}

		// Try namespace-scoped first
		template := &aimv1alpha1.AIMServiceTemplate{}
		err := c.Get(ctx, client.ObjectKey{
			Namespace: service.Namespace,
			Name:      finalTemplateName,
		}, template)

		if err == nil {
			templateResult.Value = template
			return templateResult, clusterTemplateResult, nil
		}

		if !apierrors.IsNotFound(err) {
			templateResult.Error = err
			return templateResult, clusterTemplateResult, nil
		}

		// Try cluster-scoped (only base name, derived templates are namespace-scoped)
		clusterTemplate := &aimv1alpha1.AIMClusterServiceTemplate{}
		err = c.Get(ctx, client.ObjectKey{Name: templateName}, clusterTemplate)
		if err == nil {
			clusterTemplateResult.Value = clusterTemplate
			return templateResult, clusterTemplateResult, nil
		}

		if apierrors.IsNotFound(err) {
			templateResult.Error = controllerutils.NewMissingUpstreamDependencyError(
				aimv1alpha1.AIMServiceReasonTemplateNotFound,
				fmt.Sprintf("template %q not found", finalTemplateName),
				nil,
			)
		} else {
			clusterTemplateResult.Error = err
		}
		return templateResult, clusterTemplateResult, nil
	}

	// Case 2: Auto-select template based on model
	logger.V(1).Info("auto-selecting template")

	// Get model name for template lookup
	var modelName string
	if model.Value != nil {
		modelName = model.Value.Name
	} else if clusterModel.Value != nil {
		modelName = clusterModel.Value.Name
	}

	if modelName == "" {
		// Can't auto-select without a model
		return templateResult, clusterTemplateResult, nil
	}

	// Perform template auto-selection
	selection := selectTemplateForModel(ctx, c, service, modelName)

	if selection.Error != nil {
		templateResult.Error = selection.Error
		return templateResult, clusterTemplateResult, selection
	}

	if selection.SelectedTemplate != nil {
		templateResult.Value = selection.SelectedTemplate
	} else if selection.SelectedClusterTemplate != nil {
		clusterTemplateResult.Value = selection.SelectedClusterTemplate
	}

	return templateResult, clusterTemplateResult, selection
}

// fetchInferenceService fetches the existing InferenceService for the service.
func fetchInferenceService(
	ctx context.Context,
	c client.Client,
	service *aimv1alpha1.AIMService,
) controllerutils.FetchResult[*servingv1beta1.InferenceService] {
	isvcName, err := GenerateInferenceServiceName(service.Name, service.Namespace)
	if err != nil {
		return controllerutils.FetchResult[*servingv1beta1.InferenceService]{Error: err}
	}

	isvc := &servingv1beta1.InferenceService{}
	err = c.Get(ctx, client.ObjectKey{
		Namespace: service.Namespace,
		Name:      isvcName,
	}, isvc)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return controllerutils.FetchResult[*servingv1beta1.InferenceService]{}
		}
		return controllerutils.FetchResult[*servingv1beta1.InferenceService]{Error: err}
	}

	return controllerutils.FetchResult[*servingv1beta1.InferenceService]{Value: isvc}
}

// fetchHTTPRoute fetches the existing HTTPRoute for the service.
func fetchHTTPRoute(
	ctx context.Context,
	c client.Client,
	service *aimv1alpha1.AIMService,
	runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon,
) controllerutils.FetchResult[*gatewayapiv1.HTTPRoute] {
	// Check if routing is enabled
	if !isRoutingEnabled(service, runtimeConfig) {
		return controllerutils.FetchResult[*gatewayapiv1.HTTPRoute]{}
	}

	routeName, err := GenerateHTTPRouteName(service.Name, service.Namespace)
	if err != nil {
		return controllerutils.FetchResult[*gatewayapiv1.HTTPRoute]{Error: err}
	}

	route := &gatewayapiv1.HTTPRoute{}
	err = c.Get(ctx, client.ObjectKey{
		Namespace: service.Namespace,
		Name:      routeName,
	}, route)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return controllerutils.FetchResult[*gatewayapiv1.HTTPRoute]{}
		}
		return controllerutils.FetchResult[*gatewayapiv1.HTTPRoute]{Error: err}
	}

	return controllerutils.FetchResult[*gatewayapiv1.HTTPRoute]{Value: route}
}

// fetchTemplateCache fetches the template cache for the resolved template.
func fetchTemplateCache(
	ctx context.Context,
	c client.Client,
	service *aimv1alpha1.AIMService,
	template controllerutils.FetchResult[*aimv1alpha1.AIMServiceTemplate],
	clusterTemplate controllerutils.FetchResult[*aimv1alpha1.AIMClusterServiceTemplate],
) controllerutils.FetchResult[*aimv1alpha1.AIMTemplateCache] {
	// Get template name
	var templateName string
	if template.Value != nil {
		templateName = template.Value.Name
	} else if clusterTemplate.Value != nil {
		templateName = clusterTemplate.Value.Name
	}

	if templateName == "" {
		return controllerutils.FetchResult[*aimv1alpha1.AIMTemplateCache]{}
	}

	// List template caches in the service namespace
	cacheList := &aimv1alpha1.AIMTemplateCacheList{}
	err := c.List(ctx, cacheList, client.InNamespace(service.Namespace))
	if err != nil {
		return controllerutils.FetchResult[*aimv1alpha1.AIMTemplateCache]{Error: err}
	}

	// Find cache matching our template
	for i := range cacheList.Items {
		cache := &cacheList.Items[i]
		if cache.Spec.TemplateName == templateName {
			return controllerutils.FetchResult[*aimv1alpha1.AIMTemplateCache]{Value: cache}
		}
	}

	return controllerutils.FetchResult[*aimv1alpha1.AIMTemplateCache]{}
}

// fetchModelCaches fetches model caches in the service namespace.
func fetchModelCaches(
	ctx context.Context,
	c client.Client,
	namespace string,
) controllerutils.FetchResult[*aimv1alpha1.AIMModelCacheList] {
	cacheList := &aimv1alpha1.AIMModelCacheList{}
	err := c.List(ctx, cacheList, client.InNamespace(namespace))
	if err != nil {
		return controllerutils.FetchResult[*aimv1alpha1.AIMModelCacheList]{Error: err}
	}
	return controllerutils.FetchResult[*aimv1alpha1.AIMModelCacheList]{Value: cacheList}
}

// fetchServicePVC fetches the service's temporary PVC for model downloads.
func fetchServicePVC(
	ctx context.Context,
	c client.Client,
	service *aimv1alpha1.AIMService,
) controllerutils.FetchResult[*corev1.PersistentVolumeClaim] {
	pvcName, err := GenerateServicePVCName(service.Name, service.Namespace)
	if err != nil {
		return controllerutils.FetchResult[*corev1.PersistentVolumeClaim]{Error: err}
	}

	pvc := &corev1.PersistentVolumeClaim{}
	err = c.Get(ctx, client.ObjectKey{
		Namespace: service.Namespace,
		Name:      pvcName,
	}, pvc)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return controllerutils.FetchResult[*corev1.PersistentVolumeClaim]{}
		}
		return controllerutils.FetchResult[*corev1.PersistentVolumeClaim]{Error: err}
	}

	return controllerutils.FetchResult[*corev1.PersistentVolumeClaim]{Value: pvc}
}

// ============================================================================
// NAME GENERATION
// ============================================================================

// GenerateInferenceServiceName creates a deterministic name for the InferenceService.
// KServe creates hostnames in format {isvc-name}-predictor-{namespace}, which must be ≤ 63 chars.
func GenerateInferenceServiceName(serviceName, namespace string) (string, error) {
	return utils.GenerateDerivedName([]string{serviceName}, namespace)
}

// GenerateHTTPRouteName creates a deterministic name for the HTTPRoute.
func GenerateHTTPRouteName(serviceName, namespace string) (string, error) {
	return utils.GenerateDerivedName([]string{serviceName, "route"}, namespace)
}

// GenerateServicePVCName creates a deterministic name for the service's temporary PVC.
func GenerateServicePVCName(serviceName, namespace string) (string, error) {
	return utils.GenerateDerivedName([]string{serviceName, "temp-cache"}, namespace)
}

// GenerateTemplateCacheName creates a deterministic name for a template cache.
func GenerateTemplateCacheName(templateName, namespace string) (string, error) {
	return utils.GenerateDerivedName([]string{templateName, "cache"}, namespace)
}
