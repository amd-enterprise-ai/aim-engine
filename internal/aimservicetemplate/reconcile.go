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
	RuntimeConfig aimruntimeconfig.RuntimeConfigFetchResult
	Discovery     *ServiceTemplateDiscoveryFetchResult
	Cluster       ServiceTemplateClusterFetchResult
	Model         ClusterServiceTemplateModelFetchResult
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
	result.RuntimeConfig = runtimeConfig

	modelResult, err := FetchClusterServiceTemplateModelResult(ctx, c, clusterServiceTemplate)
	if err != nil {
		return result, fmt.Errorf("failed to fetch cluster model result: %w", err)
	}
	result.Model = modelResult

	// TODO add a check if the check needs to be done (if there's a terminal error?)
	discoveryResult, err := fetchDiscoveryResult(ctx, c, r.Clientset, clusterServiceTemplate.Status)
	if err != nil {
		return result, fmt.Errorf("failed to fetch discovery results: %w", err)
	}
	result.Discovery = discoveryResult

	clusterResult, err := fetchServiceTemplateClusterResult(ctx, c)
	if err != nil {
		return result, fmt.Errorf("failed to fetch cluster results: %w", err)
	}
	result.Cluster = clusterResult

	return result, nil
}

type ServiceTemplateFetchResult struct {
	RuntimeConfig aimruntimeconfig.RuntimeConfigFetchResult
	Discovery     *ServiceTemplateDiscoveryFetchResult
	Cache         ServiceTemplateCacheFetchResult
	Cluster       ServiceTemplateClusterFetchResult
	Model         ServiceTemplateModelFetchResult
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
	result.RuntimeConfig = runtimeConfig

	modelResult, err := FetchServiceTemplateModelResult(ctx, c, serviceTemplate)
	if err != nil {
		return result, fmt.Errorf("failed to fetch model result: %w", err)
	}
	result.Model = modelResult

	discoveryResult, err := fetchDiscoveryResult(ctx, c, r.Clientset, serviceTemplate.Status)
	if err != nil {
		return result, fmt.Errorf("failed to fetch discovery results: %w", err)
	}
	result.Discovery = discoveryResult

	clusterResult, err := fetchServiceTemplateClusterResult(ctx, c)
	if err != nil {
		return result, fmt.Errorf("failed to fetch cluster results: %w", err)
	}
	result.Cluster = clusterResult

	cacheResult, err := fetchServiceTemplateCacheResult(ctx, c, serviceTemplate, &serviceTemplate.Status)
	if err != nil {
		return result, fmt.Errorf("failed to fetch cache results: %w", err)
	}
	result.Cache = cacheResult

	return result, nil
}

// ============================================================================
// OBSERVE
// ============================================================================

type ClusterServiceTemplateObservation struct {
	RuntimeConfig aimruntimeconfig.RuntimeConfigObservation
	Cluster       ServiceTemplateClusterObservation
	Discovery     ServiceTemplateDiscoveryObservation
	Model         ServiceTemplateModelObservation
}

func (r *ClusterServiceTemplateReconciler) Observe(
	ctx context.Context,
	clusterServiceTemplate *aimv1alpha1.AIMClusterServiceTemplate,
	fetchResult ClusterServiceTemplateFetchResult,
) (ClusterServiceTemplateObservation, error) {
	obs := ClusterServiceTemplateObservation{
		RuntimeConfig: aimruntimeconfig.ObserveRuntimeConfig(fetchResult.RuntimeConfig, clusterServiceTemplate.Spec.RuntimeConfigName),
		Model:         ObserveClusterServiceTemplateModel(fetchResult.Model),
		Cluster:       observeServiceTemplateCluster(fetchResult.Cluster, clusterServiceTemplate.Spec.AIMServiceTemplateSpecCommon),
		Discovery:     observeDiscovery(fetchResult.Discovery, clusterServiceTemplate.Status),
	}

	return obs, nil
}

type ServiceTemplateObservation struct {
	RuntimeConfig aimruntimeconfig.RuntimeConfigObservation
	Discovery     ServiceTemplateDiscoveryObservation
	Cache         ServiceTemplateCacheObservation
	Cluster       ServiceTemplateClusterObservation
	Model         ServiceTemplateModelObservation
}

func (r *ServiceTemplateReconciler) Observe(
	ctx context.Context,
	serviceTemplate *aimv1alpha1.AIMServiceTemplate,
	fetchResult ServiceTemplateFetchResult,
) (ServiceTemplateObservation, error) {
	obs := ServiceTemplateObservation{
		RuntimeConfig: aimruntimeconfig.ObserveRuntimeConfig(fetchResult.RuntimeConfig, serviceTemplate.Spec.RuntimeConfigName),
		Cluster:       observeServiceTemplateCluster(fetchResult.Cluster, serviceTemplate.Spec.AIMServiceTemplateSpecCommon),
		Discovery:     observeDiscovery(fetchResult.Discovery, serviceTemplate.Status),
		Model:         ObserveServiceTemplateModel(fetchResult.Model),
		Cache:         observeServiceTemplateCache(fetchResult.Cache, *serviceTemplate),
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
) ([]client.Object, error) {
	var objects []client.Object

	if !observation.Model.ModelFound {
		// Return early if the model doesn't exist
		return objects, nil
	}

	if observation.Discovery.ShouldRun {
		discoveryJob := BuildDiscoveryJob(DiscoveryJobBuilderInputs{
			TemplateName: clusterServiceTemplate.Name,
			TemplateSpec: clusterServiceTemplate.Spec.AIMServiceTemplateSpecCommon,
			Namespace:    clusterServiceTemplate.Namespace,
			Image:        observation.Model.ModelSpec.Image,
			// TODO should cluster service template have envs?
			// Env:          clusterServiceTemplate.Spec.Env,
		})
		_ = controllerutil.SetOwnerReference(clusterServiceTemplate, discoveryJob, r.Scheme)
		objects = append(objects, discoveryJob)
	}
	return objects, nil
}

func (r *ServiceTemplateReconciler) Plan(
	ctx context.Context,
	serviceTemplate *aimv1alpha1.AIMServiceTemplate,
	observation ServiceTemplateObservation,
) ([]client.Object, error) {
	var objects []client.Object
	if observation.Cache.ShouldCreateCache {
		templateCache := buildServiceTemplateCache(*serviceTemplate, observation.RuntimeConfig.MergedConfig)
		_ = controllerutil.SetOwnerReference(serviceTemplate, templateCache, r.Scheme)
		objects = append(objects, templateCache)
	}

	if observation.Discovery.ShouldRun {
		discoveryJob := BuildDiscoveryJob(DiscoveryJobBuilderInputs{
			TemplateName: serviceTemplate.Name,
			TemplateSpec: serviceTemplate.Spec.AIMServiceTemplateSpecCommon,
			Namespace:    serviceTemplate.Namespace,
			Image:        observation.Model.ModelSpec.Image,
			Env:          serviceTemplate.Spec.Env,
		})
		_ = controllerutil.SetOwnerReference(serviceTemplate, discoveryJob, r.Scheme)
		objects = append(objects, discoveryJob)
	}

	return objects, nil
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

	// Project RuntimeConfig first - highest priority
	if fatal := aimruntimeconfig.ProjectRuntimeConfigObservation(cm, sh, observation.RuntimeConfig); fatal {
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

	// Project discovery (lowest priority)
	projectDiscovery(status, cm, sh, observation.Discovery)
}

func (r *ServiceTemplateReconciler) Project(
	status *aimv1alpha1.AIMServiceTemplateStatus,
	cm *controllerutils.ConditionManager,
	observation ServiceTemplateObservation,
) {
	sh := controllerutils.NewStatusHelper(status, cm)

	// Project RuntimeConfig first - highest priority
	if fatal := aimruntimeconfig.ProjectRuntimeConfigObservation(cm, sh, observation.RuntimeConfig); fatal {
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
	if fatal := projectServiceTemplateCache(status, cm, sh, observation.Cache); fatal {
		return
	}

	// Project discovery (lowest priority)
	projectDiscovery(status, cm, sh, observation.Discovery)
}
