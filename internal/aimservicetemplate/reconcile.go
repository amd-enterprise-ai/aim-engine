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

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/aimruntimeconfig"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

type ClusterServiceTemplateReconciler struct {
	Clientset kubernetes.Interface
	Scheme    *runtime.Scheme
}

type ServiceTemplateReconciler struct {
	Clientset kubernetes.Interface
	Scheme    *runtime.Scheme
}

// ============================================================================
// FETCH
// ============================================================================

type ClusterServiceTemplateFetchResult struct {
	runtimeConfig aimruntimeconfig.RuntimeConfigFetchResult
	discovery     *serviceTemplateDiscoveryFetchResult
	cluster       serviceTemplateClusterFetchResult
	model         clusterServiceTemplateModelFetchResult
}

func (r *ClusterServiceTemplateReconciler) Fetch(
	ctx context.Context,
	c client.Client,
	clusterServiceTemplate *aimv1alpha1.AIMClusterServiceTemplate,
) (ClusterServiceTemplateFetchResult, error) {
	result := ClusterServiceTemplateFetchResult{}

	runtimeConfig, err := aimruntimeconfig.FetchRuntimeConfig(ctx, c, clusterServiceTemplate.Spec.RuntimeConfigName, "")
	if err != nil {
		return result, err
	}
	result.runtimeConfig = runtimeConfig

	modelResult, err := fetchClusterServiceTemplateModelResult(ctx, c, clusterServiceTemplate)
	if err != nil {
		return result, fmt.Errorf("failed to fetch cluster model result: %w", err)
	}
	result.model = modelResult

	// TODO add a check if the check needs to be done (if there's a terminal error?)
	discoveryResult, err := fetchDiscoveryResult(ctx, c, r.Clientset, clusterServiceTemplate.Status)
	if err != nil {
		return result, fmt.Errorf("failed to fetch discovery results: %w", err)
	}
	result.discovery = discoveryResult

	clusterResult, err := fetchServiceTemplateClusterResult(ctx, c)
	if err != nil {
		return result, fmt.Errorf("failed to fetch cluster results: %w", err)
	}
	result.cluster = clusterResult

	return result, nil
}

type ServiceTemplateFetchResult struct {
	runtimeConfig aimruntimeconfig.RuntimeConfigFetchResult
	discovery     *serviceTemplateDiscoveryFetchResult
	cache         serviceTemplateCacheFetchResult
	cluster       serviceTemplateClusterFetchResult
	model         serviceTemplateModelFetchResult
}

func (r *ServiceTemplateReconciler) Fetch(
	ctx context.Context,
	c client.Client,
	serviceTemplate *aimv1alpha1.AIMServiceTemplate,
) (ServiceTemplateFetchResult, error) {
	result := ServiceTemplateFetchResult{}

	runtimeConfig, err := aimruntimeconfig.FetchRuntimeConfig(ctx, c, serviceTemplate.Spec.RuntimeConfigName, serviceTemplate.Namespace)
	if err != nil {
		return result, err
	}
	result.runtimeConfig = runtimeConfig

	modelResult, err := fetchServiceTemplateModelResult(ctx, c, serviceTemplate)
	if err != nil {
		return result, fmt.Errorf("failed to fetch model result: %w", err)
	}
	result.model = modelResult

	discoveryResult, err := fetchDiscoveryResult(ctx, c, r.Clientset, serviceTemplate.Status)
	if err != nil {
		return result, fmt.Errorf("failed to fetch discovery results: %w", err)
	}
	result.discovery = discoveryResult

	clusterResult, err := fetchServiceTemplateClusterResult(ctx, c)
	if err != nil {
		return result, fmt.Errorf("failed to fetch cluster results: %w", err)
	}
	result.cluster = clusterResult

	cacheResult, err := fetchServiceTemplateCacheResult(ctx, c, serviceTemplate, &serviceTemplate.Status)
	if err != nil {
		return result, fmt.Errorf("failed to fetch cache results: %w", err)
	}
	result.cache = cacheResult

	return result, nil
}

// ============================================================================
// OBSERVE
// ============================================================================

type ClusterServiceTemplateObservation struct {
	runtimeConfig aimruntimeconfig.RuntimeConfigObservation
	cluster       serviceTemplateClusterObservation
	discovery     serviceTemplateDiscoveryObservation
	model         serviceTemplateModelObservation
}

func (r *ClusterServiceTemplateReconciler) Observe(
	ctx context.Context,
	clusterServiceTemplate *aimv1alpha1.AIMClusterServiceTemplate,
	fetchResult ClusterServiceTemplateFetchResult,
) (ClusterServiceTemplateObservation, error) {
	obs := ClusterServiceTemplateObservation{
		runtimeConfig: aimruntimeconfig.ObserveRuntimeConfig(fetchResult.runtimeConfig, clusterServiceTemplate.Spec.RuntimeConfigName),
		model:         observeClusterServiceTemplateModel(fetchResult.model),
		cluster:       observeServiceTemplateCluster(fetchResult.cluster, clusterServiceTemplate.Spec.AIMServiceTemplateSpecCommon),
		discovery:     observeDiscovery(fetchResult.discovery, clusterServiceTemplate.Status),
	}

	return obs, nil
}

type ServiceTemplateObservation struct {
	runtimeConfig aimruntimeconfig.RuntimeConfigObservation
	discovery     serviceTemplateDiscoveryObservation
	cache         serviceTemplateCacheObservation
	Cluster       serviceTemplateClusterObservation
	Model         serviceTemplateModelObservation
}

func (r *ServiceTemplateReconciler) Observe(
	ctx context.Context,
	serviceTemplate *aimv1alpha1.AIMServiceTemplate,
	fetchResult ServiceTemplateFetchResult,
) (ServiceTemplateObservation, error) {
	obs := ServiceTemplateObservation{
		runtimeConfig: aimruntimeconfig.ObserveRuntimeConfig(fetchResult.runtimeConfig, serviceTemplate.Spec.RuntimeConfigName),
		Cluster:       observeServiceTemplateCluster(fetchResult.cluster, serviceTemplate.Spec.AIMServiceTemplateSpecCommon),
		discovery:     observeDiscovery(fetchResult.discovery, serviceTemplate.Status),
		Model:         observeServiceTemplateModel(fetchResult.model),
		cache:         observeServiceTemplateCache(fetchResult.cache, *serviceTemplate),
	}
	return obs, nil
}

// ============================================================================
// PLAN
// ============================================================================

func (r *ClusterServiceTemplateReconciler) Plan(
	ctx context.Context,
	clusterServiceTemplate *aimv1alpha1.AIMClusterServiceTemplate,
	observation ClusterServiceTemplateObservation,
) (controllerutils.PlanResult, error) {
	var objects []client.Object

	if !observation.model.modelFound {
		// Return early if the model doesn't exist
		return controllerutils.PlanResult{Apply: objects}, nil
	}

	if observation.discovery.shouldRun {
		discoveryJob := buildDiscoveryJob(discoveryJobBuilderInputs{
			templateName: clusterServiceTemplate.Name,
			templateSpec: clusterServiceTemplate.Spec.AIMServiceTemplateSpecCommon,
			namespace:    clusterServiceTemplate.Namespace,
			image:        observation.model.modelSpec.Image,
			// TODO should cluster service template have envs?
			// env:          clusterServiceTemplate.Spec.env,
		})
		_ = controllerutil.SetOwnerReference(clusterServiceTemplate, discoveryJob, r.Scheme)
		objects = append(objects, discoveryJob)
	}
	return controllerutils.PlanResult{Apply: objects}, nil
}

func (r *ServiceTemplateReconciler) Plan(
	ctx context.Context,
	serviceTemplate *aimv1alpha1.AIMServiceTemplate,
	observation ServiceTemplateObservation,
) (controllerutils.PlanResult, error) {
	var objects []client.Object
	if observation.cache.shouldCreateCache {
		templateCache := buildServiceTemplateCache(*serviceTemplate, observation.runtimeConfig.MergedConfig)
		_ = controllerutil.SetOwnerReference(serviceTemplate, templateCache, r.Scheme)
		objects = append(objects, templateCache)
	}

	if observation.discovery.shouldRun {
		discoveryJob := buildDiscoveryJob(discoveryJobBuilderInputs{
			templateName: serviceTemplate.Name,
			templateSpec: serviceTemplate.Spec.AIMServiceTemplateSpecCommon,
			namespace:    serviceTemplate.Namespace,
			image:        observation.Model.modelSpec.Image,
			env:          serviceTemplate.Spec.Env,
		})
		_ = controllerutil.SetOwnerReference(serviceTemplate, discoveryJob, r.Scheme)
		objects = append(objects, discoveryJob)
	}

	return controllerutils.PlanResult{Apply: objects}, nil
}

// ============================================================================
// PROJECT
// ============================================================================

func (r *ClusterServiceTemplateReconciler) Project(
	status *aimv1alpha1.AIMServiceTemplateStatus,
	cm *controllerutils.ConditionManager,
	observation ClusterServiceTemplateObservation,
) {
	sh := controllerutils.NewStatusHelper(status, cm)

	// Project runtimeConfig first - highest priority
	if fatal := aimruntimeconfig.ProjectRuntimeConfigObservation(cm, sh, observation.runtimeConfig); fatal {
		return
	}

	// Project model - if not found, stop
	if fatal := projectServiceTemplateModel(status, cm, sh, observation.model); fatal {
		return
	}

	// Project cluster GPU availability
	if fatal := projectServiceTemplateCluster(status, cm, sh, observation.cluster); fatal {
		return
	}

	// Project discovery (lowest priority)
	projectDiscovery(status, cm, sh, observation.discovery)
}

func (r *ServiceTemplateReconciler) Project(
	status *aimv1alpha1.AIMServiceTemplateStatus,
	cm *controllerutils.ConditionManager,
	observation ServiceTemplateObservation,
) {
	sh := controllerutils.NewStatusHelper(status, cm)

	// Project runtimeConfig first - highest priority
	if fatal := aimruntimeconfig.ProjectRuntimeConfigObservation(cm, sh, observation.runtimeConfig); fatal {
		return
	}

	// Project model - if not found, stop
	if fatal := projectServiceTemplateModel(status, cm, sh, observation.Model); fatal {
		return
	}

	// Project cluster GPU availability
	if fatal := projectServiceTemplateCluster(status, cm, sh, observation.Cluster); fatal {
		return
	}

	// Project cache status
	if fatal := projectServiceTemplateCache(status, cm, sh, observation.cache); fatal {
		return
	}

	// Project discovery (lowest priority)
	projectDiscovery(status, cm, sh, observation.discovery)
}
