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
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/aimruntimeconfig"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

// ============================================================================
// RECONCILERS
// ============================================================================

// ServiceTemplateReconciler implements the DomainReconciler interface for namespace-scoped templates.
type ServiceTemplateReconciler struct {
	Clientset kubernetes.Interface
	Scheme    *runtime.Scheme
}

// ClusterServiceTemplateReconciler implements the DomainReconciler interface for cluster-scoped templates.
type ClusterServiceTemplateReconciler struct {
	Clientset kubernetes.Interface
	Scheme    *runtime.Scheme
}

// ============================================================================
// FETCH - Namespace-scoped (AIMServiceTemplate)
// ============================================================================

// ServiceTemplateFetchResult holds fetched resources for namespace-scoped templates.
type ServiceTemplateFetchResult struct {
	template *aimv1alpha1.AIMServiceTemplate

	mergedRuntimeConfig controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]
	model               controllerutils.FetchResult[*aimv1alpha1.AIMModel]
	discoveryJob        controllerutils.FetchResult[*batchv1.Job]
	discoveryJobPods    controllerutils.FetchResult[*corev1.PodList]
	templateCaches      controllerutils.FetchResult[*aimv1alpha1.AIMTemplateCacheList]

	// Parsed discovery results (populated when discovery job has succeeded)
	parsedDiscovery *ParsedDiscovery

	// GPU availability state
	gpuResources map[string]utils.GPUResourceInfo
	gpuFetchErr  error

	// Active discovery job count across all namespaces (for concurrent limit)
	activeDiscoveryJobCount int
	activeJobCountErr       error
}

// FetchRemoteState fetches all required resources for namespace-scoped templates.
func (r *ServiceTemplateReconciler) FetchRemoteState(
	ctx context.Context,
	c client.Client,
	reconcileCtx controllerutils.ReconcileContext[*aimv1alpha1.AIMServiceTemplate],
) ServiceTemplateFetchResult {
	template := reconcileCtx.Object
	ctx = log.IntoContext(ctx, log.FromContext(ctx).WithValues(
		"phase", "fetch",
		"template", template.Name,
		"namespace", template.Namespace,
		"modelName", template.Spec.ModelName,
	))

	result := ServiceTemplateFetchResult{
		template:            template,
		mergedRuntimeConfig: reconcileCtx.MergedRuntimeConfig,
	}

	// Fetch AIMModel by name to get the image
	result.model = controllerutils.Fetch(ctx, c,
		client.ObjectKey{Name: template.Spec.ModelName, Namespace: template.Namespace},
		&aimv1alpha1.AIMModel{},
	)

	// Fetch GPU resources if GPU is required
	if TemplateRequiresGPU(template.Spec.AIMServiceTemplateSpecCommon) {
		result.gpuResources, result.gpuFetchErr = utils.GetClusterGPUResources(ctx, c)
	}

	// Fetch discovery job if template is not yet ready and has no inline model sources
	if ShouldCheckDiscoveryJob(template) {
		result.discoveryJob = FetchDiscoveryJob(ctx, c, template.Namespace, template.Name)

		// Fetch discovery job pods for health inspection
		if result.discoveryJob.OK() && result.discoveryJob.Value != nil {
			job := result.discoveryJob.Value
			result.discoveryJobPods = controllerutils.FetchList(ctx, c, &corev1.PodList{},
				client.InNamespace(template.Namespace),
				client.MatchingLabels{"job-name": job.Name},
			)

			// Parse discovery logs if job succeeded
			if IsJobSucceeded(job) {
				logger := log.FromContext(ctx)
				discovery, err := ParseDiscoveryLogs(ctx, c, r.Clientset, job)
				if err != nil {
					// Log error but don't fail - status will show job as complete
					logger.Error(err, "Failed to parse discovery logs", "job", job.Name)
				} else {
					result.parsedDiscovery = discovery
				}
			}
		}

		// Fetch active discovery job count for concurrent limit enforcement
		// Only needed if we might create a new job (no active or completed job exists)
		if !HasActiveDiscoveryJob(result.discoveryJob) && !HasCompletedDiscoveryJob(result.discoveryJob) {
			result.activeDiscoveryJobCount, result.activeJobCountErr = CountActiveDiscoveryJobs(ctx, c)
		}
	}

	// Fetch template caches if caching is enabled
	if template.Spec.Caching != nil && template.Spec.Caching.Enabled {
		result.templateCaches = controllerutils.FetchList(ctx, c, &aimv1alpha1.AIMTemplateCacheList{},
			client.InNamespace(template.Namespace),
		)
	}

	return result
}

// GetComponentHealth returns the health of all components for automatic status management.
func (result ServiceTemplateFetchResult) GetComponentHealth(ctx context.Context, clientset kubernetes.Interface) []controllerutils.ComponentHealth {
	health := []controllerutils.ComponentHealth{
		result.mergedRuntimeConfig.ToComponentHealth("RuntimeConfig", aimruntimeconfig.GetRuntimeConfigHealth),
		result.model.ToUpstreamComponentHealth("Model", GetModelHealth),
	}

	// Only check discovery job/pods while not yet ready
	if ShouldCheckDiscoveryJob(result.template) {
		// Discovery job health
		health = append(health, result.discoveryJob.ToDownstreamComponentHealth("DiscoveryJob", GetDiscoveryJobHealth))

		// Discovery job pods health (for detailed error categorization from logs)
		if result.discoveryJobPods.OK() && result.discoveryJobPods.Value != nil && len(result.discoveryJobPods.Value.Items) > 0 {
			health = append(health, result.discoveryJobPods.ToComponentHealthWithContext(ctx, clientset, "DiscoveryPods", controllerutils.GetPodsHealth))
		}
	}

	// GPU availability check
	gpuHealth := result.getGPUHealth()
	if gpuHealth.Component != "" {
		health = append(health, gpuHealth)
	}

	return health
}

// getGPUHealth returns the GPU availability health based on pre-fetched GPU resources.
func (result ServiceTemplateFetchResult) getGPUHealth() controllerutils.ComponentHealth {
	return GetGPUHealthFromResources(
		result.template.Spec.AIMServiceTemplateSpecCommon,
		result.gpuResources,
		result.gpuFetchErr,
	)
}

// ============================================================================
// FETCH - Cluster-scoped (AIMClusterServiceTemplate)
// ============================================================================

// ClusterServiceTemplateFetchResult holds fetched resources for cluster-scoped templates.
type ClusterServiceTemplateFetchResult struct {
	template *aimv1alpha1.AIMClusterServiceTemplate

	mergedRuntimeConfig controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]
	clusterModel        controllerutils.FetchResult[*aimv1alpha1.AIMClusterModel]
	discoveryJob        controllerutils.FetchResult[*batchv1.Job]
	discoveryJobPods    controllerutils.FetchResult[*corev1.PodList]

	// Parsed discovery results (populated when discovery job has succeeded)
	parsedDiscovery *ParsedDiscovery

	// GPU availability state
	gpuResources map[string]utils.GPUResourceInfo
	gpuFetchErr  error

	// Active discovery job count across all namespaces (for concurrent limit)
	activeDiscoveryJobCount int
	activeJobCountErr       error
}

// FetchRemoteState fetches all required resources for cluster-scoped templates.
func (r *ClusterServiceTemplateReconciler) FetchRemoteState(
	ctx context.Context,
	c client.Client,
	reconcileCtx controllerutils.ReconcileContext[*aimv1alpha1.AIMClusterServiceTemplate],
) ClusterServiceTemplateFetchResult {
	template := reconcileCtx.Object
	ctx = log.IntoContext(ctx, log.FromContext(ctx).WithValues(
		"phase", "fetch",
		"template", template.Name,
		"modelName", template.Spec.ModelName,
	))

	result := ClusterServiceTemplateFetchResult{
		template:            template,
		mergedRuntimeConfig: reconcileCtx.MergedRuntimeConfig,
	}

	// Fetch AIMClusterModel by name to get the image
	result.clusterModel = controllerutils.Fetch(ctx, c,
		client.ObjectKey{Name: template.Spec.ModelName},
		&aimv1alpha1.AIMClusterModel{},
	)

	// Fetch GPU resources if GPU is required
	if TemplateRequiresGPU(template.Spec.AIMServiceTemplateSpecCommon) {
		result.gpuResources, result.gpuFetchErr = utils.GetClusterGPUResources(ctx, c)
	}

	// Fetch discovery job if template is not yet ready and has no inline model sources
	// Cluster templates run discovery jobs in the operator namespace
	operatorNamespace := constants.GetOperatorNamespace()
	if ShouldCheckClusterTemplateDiscoveryJob(template) {
		result.discoveryJob = FetchDiscoveryJob(ctx, c, operatorNamespace, template.Name)

		// Fetch discovery job pods for health inspection
		if result.discoveryJob.OK() && result.discoveryJob.Value != nil {
			job := result.discoveryJob.Value
			result.discoveryJobPods = controllerutils.FetchList(ctx, c, &corev1.PodList{},
				client.InNamespace(operatorNamespace),
				client.MatchingLabels{"job-name": job.Name},
			)

			// Parse discovery logs if job succeeded
			if IsJobSucceeded(job) {
				logger := log.FromContext(ctx)
				discovery, err := ParseDiscoveryLogs(ctx, c, r.Clientset, job)
				if err != nil {
					// Log error but don't fail - status will show job as complete
					logger.Error(err, "Failed to parse discovery logs", "job", job.Name)
				} else {
					result.parsedDiscovery = discovery
				}
			}
		}

		// Fetch active discovery job count for concurrent limit enforcement
		// Only needed if we might create a new job (no active or completed job exists)
		if !HasActiveDiscoveryJob(result.discoveryJob) && !HasCompletedDiscoveryJob(result.discoveryJob) {
			result.activeDiscoveryJobCount, result.activeJobCountErr = CountActiveDiscoveryJobs(ctx, c)
		}
	}

	return result
}

// GetComponentHealth returns the health of all components for automatic status management.
func (result ClusterServiceTemplateFetchResult) GetComponentHealth(ctx context.Context, clientset kubernetes.Interface) []controllerutils.ComponentHealth {
	health := []controllerutils.ComponentHealth{
		result.mergedRuntimeConfig.ToComponentHealth("RuntimeConfig", aimruntimeconfig.GetRuntimeConfigHealth),
		result.clusterModel.ToUpstreamComponentHealth("ClusterModel", GetClusterModelHealth),
	}

	// Only check discovery job/pods while not yet ready
	if ShouldCheckClusterTemplateDiscoveryJob(result.template) {
		// Discovery job health
		health = append(health, result.discoveryJob.ToDownstreamComponentHealth("DiscoveryJob", GetDiscoveryJobHealth))

		// Discovery job pods health (for detailed error categorization from logs)
		if result.discoveryJobPods.OK() && result.discoveryJobPods.Value != nil && len(result.discoveryJobPods.Value.Items) > 0 {
			health = append(health, result.discoveryJobPods.ToComponentHealthWithContext(ctx, clientset, "DiscoveryPods", controllerutils.GetPodsHealth))
		}
	}

	// GPU availability check
	gpuHealth := result.getGPUHealth()
	if gpuHealth.Component != "" {
		health = append(health, gpuHealth)
	}

	return health
}

// getGPUHealth returns the GPU availability health based on pre-fetched GPU resources.
func (result ClusterServiceTemplateFetchResult) getGPUHealth() controllerutils.ComponentHealth {
	return GetGPUHealthFromResources(
		result.template.Spec.AIMServiceTemplateSpecCommon,
		result.gpuResources,
		result.gpuFetchErr,
	)
}

// ============================================================================
// OBSERVATION
// ============================================================================

// ServiceTemplateObservation embeds the fetch result. The observation phase is minimal
// since FetchResult.GetComponentHealth() handles health derivation and PlanResources
// uses spec helper methods directly for planning decisions.
type ServiceTemplateObservation struct {
	ServiceTemplateFetchResult
}

// ComposeState interprets fetched resources into an observation for namespace-scoped templates.
func (r *ServiceTemplateReconciler) ComposeState(
	_ context.Context,
	_ controllerutils.ReconcileContext[*aimv1alpha1.AIMServiceTemplate],
	fetch ServiceTemplateFetchResult,
) ServiceTemplateObservation {
	return ServiceTemplateObservation{ServiceTemplateFetchResult: fetch}
}

// ClusterServiceTemplateObservation embeds the fetch result for cluster-scoped templates.
type ClusterServiceTemplateObservation struct {
	ClusterServiceTemplateFetchResult
}

// ComposeState interprets fetched resources into an observation for cluster-scoped templates.
func (r *ClusterServiceTemplateReconciler) ComposeState(
	_ context.Context,
	_ controllerutils.ReconcileContext[*aimv1alpha1.AIMClusterServiceTemplate],
	fetch ClusterServiceTemplateFetchResult,
) ClusterServiceTemplateObservation {
	return ClusterServiceTemplateObservation{ClusterServiceTemplateFetchResult: fetch}
}

// isGPUAvailable checks if the required GPU is available based on pre-fetched GPU resources.
func (obs ServiceTemplateObservation) isGPUAvailable() bool {
	return IsGPUAvailableForSpec(obs.template.Spec.AIMServiceTemplateSpecCommon, obs.gpuResources, obs.gpuFetchErr)
}

// isGPUAvailable checks if the required GPU is available based on pre-fetched GPU resources.
func (obs ClusterServiceTemplateObservation) isGPUAvailable() bool {
	return IsGPUAvailableForSpec(obs.template.Spec.AIMServiceTemplateSpecCommon, obs.gpuResources, obs.gpuFetchErr)
}

// ============================================================================
// PLAN - Namespace-scoped
// ============================================================================

// PlanResources derives desired state changes for namespace-scoped templates.
func (r *ServiceTemplateReconciler) PlanResources(
	ctx context.Context,
	reconcileCtx controllerutils.ReconcileContext[*aimv1alpha1.AIMServiceTemplate],
	obs ServiceTemplateObservation,
) controllerutils.PlanResult {
	logger := log.FromContext(ctx).WithName("plan")
	template := obs.template
	planResult := controllerutils.PlanResult{}

	// Check if model is available - required for both inline and discovery flows
	if !obs.model.OK() {
		logger.V(1).Info("model not found, waiting for model", "modelName", template.Spec.ModelName)
		return planResult
	}

	model := obs.model.Value
	image := model.Spec.Image
	if image == "" {
		logger.V(1).Info("model has no image specified", "modelName", template.Spec.ModelName)
		return planResult
	}

	// Check if inline model sources are provided - template is immediately ready
	// NOTE: Inline model sources bypass GPU availability check - GPU is a runtime concern, not a definition concern
	if len(template.Spec.ModelSources) > 0 {
		logger.V(1).Info("template has inline model sources, skipping discovery")

		// Create template cache if caching is enabled
		if template.Spec.Caching != nil && template.Spec.Caching.Enabled {
			if !HasExistingTemplateCache(template.UID, obs.templateCaches) {
				cache := BuildTemplateCache(template)
				planResult.Apply(cache)
			}
		}

		return planResult
	}

	// Check GPU availability - only required for discovery flow (not inline model sources)
	if !obs.isGPUAvailable() {
		logger.V(1).Info("required GPU not available, skipping resource planning")
		return planResult
	}

	// If template is Ready, create template cache if caching is enabled
	if template.Status.Status == constants.AIMStatusReady {
		if template.Spec.Caching != nil && template.Spec.Caching.Enabled && len(template.Status.ModelSources) > 0 {
			if !HasExistingTemplateCache(template.UID, obs.templateCaches) {
				cache := BuildTemplateCache(template)
				planResult.Apply(cache)
			}
		}

		return planResult
	}

	// Semaphore key is based on template identity (namespace/name)
	semaphoreKey := JobKey(template.Namespace, template.Name)

	// Release semaphore slot if job has completed (success or failure)
	if HasCompletedDiscoveryJob(obs.discoveryJob) {
		if GetGlobalSemaphore().Release(semaphoreKey) {
			logger.Info("released semaphore slot for completed discovery job",
				"semaphoreKey", semaphoreKey,
				"activeSlots", GetGlobalSemaphore().ActiveCount(),
				"availableSlots", GetGlobalSemaphore().AvailableSlots())
		}
	}

	// Template not ready - check if we need to create discovery job
	hasCompletedJob := HasCompletedDiscoveryJob(obs.discoveryJob)
	hasActiveJob := HasActiveDiscoveryJob(obs.discoveryJob)

	logger.V(1).Info("discovery job state check",
		"semaphoreKey", semaphoreKey,
		"hasCompletedJob", hasCompletedJob,
		"hasActiveJob", hasActiveJob,
		"jobExists", obs.discoveryJob.Value != nil)

	if !hasCompletedJob && !hasActiveJob {
		// Check backoff timing from previous failed attempts
		shouldCreate, reason, message := ShouldCreateDiscoveryJob(template.Status.Discovery, time.Now())
		if !shouldCreate {
			logger.V(1).Info("discovery job creation blocked by backoff",
				"reason", reason,
				"message", message,
				"attempts", template.Status.Discovery.Attempts)
			return planResult
		}

		logger.V(1).Info("attempting to acquire semaphore slot",
			"semaphoreKey", semaphoreKey,
			"slotCurrentlyHeld", GetGlobalSemaphore().IsHeld(semaphoreKey),
			"activeSlots", GetGlobalSemaphore().ActiveCount(),
			"availableSlots", GetGlobalSemaphore().AvailableSlots())

		// Serialize the semaphore check with other reconciliations to prevent race conditions.
		// Without this lock, multiple reconciliations could all see "no job exists" in FetchRemoteState,
		// then all try to acquire semaphore slots before any of them actually create the job.
		var shouldCreateJob bool
		WithJobCreationLock(func() {
			// Double-check: another reconciliation may have acquired a slot for this key
			// while we were waiting for the lock. If so, the job is being created by that
			// reconciliation, so we should skip creating it here.
			if GetGlobalSemaphore().IsHeld(semaphoreKey) {
				logger.Info("semaphore slot already held for this template, skipping job creation",
					"semaphoreKey", semaphoreKey)
				shouldCreateJob = false
				return
			}

			// Try to acquire a semaphore slot (non-blocking)
			shouldCreateJob = GetGlobalSemaphore().TryAcquire(semaphoreKey)
			logger.Info("semaphore TryAcquire result",
				"semaphoreKey", semaphoreKey,
				"acquired", shouldCreateJob,
				"activeSlots", GetGlobalSemaphore().ActiveCount(),
				"availableSlots", GetGlobalSemaphore().AvailableSlots())
		})

		if !shouldCreateJob {
			if !GetGlobalSemaphore().IsHeld(semaphoreKey) {
				// Not held and not acquired means at capacity
				logger.Info("discovery job creation blocked by concurrent limit (semaphore)",
					"semaphoreKey", semaphoreKey,
					"activeSlots", GetGlobalSemaphore().ActiveCount(),
					"availableSlots", GetGlobalSemaphore().AvailableSlots(),
					"limit", constants.MaxConcurrentDiscoveryJobs)
			}
			return planResult
		}

		logger.Info("acquired semaphore slot for discovery job",
			"semaphoreKey", semaphoreKey,
			"activeSlots", GetGlobalSemaphore().ActiveCount(),
			"availableSlots", GetGlobalSemaphore().AvailableSlots())

		job := BuildDiscoveryJob(DiscoveryJobSpec{
			TemplateName:     template.Name,
			Namespace:        template.Namespace,
			ModelID:          template.Spec.ModelName,
			Image:            image,
			Env:              template.Spec.Env,
			ImagePullSecrets: model.Spec.ImagePullSecrets,
			ServiceAccount:   model.Spec.ServiceAccountName,
			TemplateSpec:     template.Spec.AIMServiceTemplateSpecCommon,
			OwnerRef: metav1.OwnerReference{
				APIVersion:         aimv1alpha1.GroupVersion.String(),
				Kind:               "AIMServiceTemplate",
				Name:               template.Name,
				UID:                template.UID,
				Controller:         ptr.To(true),
				BlockOwnerDeletion: ptr.To(true),
			},
		})
		logger.Info("adding discovery job to plan result",
			"semaphoreKey", semaphoreKey,
			"jobName", job.Name)
		planResult.Apply(job)
	}

	return planResult
}

// ============================================================================
// PLAN - Cluster-scoped
// ============================================================================

// PlanResources derives desired state changes for cluster-scoped templates.
func (r *ClusterServiceTemplateReconciler) PlanResources(
	ctx context.Context,
	reconcileCtx controllerutils.ReconcileContext[*aimv1alpha1.AIMClusterServiceTemplate],
	obs ClusterServiceTemplateObservation,
) controllerutils.PlanResult {
	logger := log.FromContext(ctx).WithName("plan")
	template := obs.template
	planResult := controllerutils.PlanResult{}

	// Check if cluster model is available - required for both inline and discovery flows
	if !obs.clusterModel.OK() {
		logger.V(1).Info("cluster model not found, waiting for model", "modelName", template.Spec.ModelName)
		return planResult
	}

	clusterModel := obs.clusterModel.Value
	image := clusterModel.Spec.Image
	if image == "" {
		logger.V(1).Info("cluster model has no image specified", "modelName", template.Spec.ModelName)
		return planResult
	}

	// Check if inline model sources are provided - template is immediately ready
	// NOTE: Inline model sources bypass GPU availability check - GPU is a runtime concern, not a definition concern
	if len(template.Spec.ModelSources) > 0 {
		logger.V(1).Info("template has inline model sources, skipping discovery")
		return planResult
	}

	// Check GPU availability - only required for discovery flow (not inline model sources)
	if !obs.isGPUAvailable() {
		logger.V(1).Info("required GPU not available, skipping resource planning")
		return planResult
	}

	// If template is Ready, nothing more to plan
	if template.Status.Status == constants.AIMStatusReady {
		return planResult
	}

	// Semaphore key is based on template identity (cluster-scoped, so just name)
	operatorNamespace := constants.GetOperatorNamespace()
	semaphoreKey := JobKey("", template.Name)

	// Release semaphore slot if job has completed (success or failure)
	if HasCompletedDiscoveryJob(obs.discoveryJob) {
		if GetGlobalSemaphore().Release(semaphoreKey) {
			logger.Info("released semaphore slot for completed discovery job",
				"semaphoreKey", semaphoreKey,
				"activeSlots", GetGlobalSemaphore().ActiveCount(),
				"availableSlots", GetGlobalSemaphore().AvailableSlots())
		}
	}

	// Template not ready - check if we need to create discovery job
	if !HasCompletedDiscoveryJob(obs.discoveryJob) && !HasActiveDiscoveryJob(obs.discoveryJob) {
		// Check backoff timing from previous failed attempts
		shouldCreate, reason, message := ShouldCreateDiscoveryJob(template.Status.Discovery, time.Now())
		if !shouldCreate {
			logger.V(1).Info("discovery job creation blocked by backoff",
				"reason", reason,
				"message", message,
				"attempts", template.Status.Discovery.Attempts)
			return planResult
		}

		// Serialize the semaphore check with other reconciliations to prevent race conditions.
		// Without this lock, multiple reconciliations could all see "no job exists" in FetchRemoteState,
		// then all try to acquire semaphore slots before any of them actually create the job.
		var shouldCreateJob bool
		WithJobCreationLock(func() {
			// Double-check: another reconciliation may have acquired a slot for this key
			// while we were waiting for the lock. If so, the job is being created by that
			// reconciliation, so we should skip creating it here.
			if GetGlobalSemaphore().IsHeld(semaphoreKey) {
				logger.V(1).Info("semaphore slot already held for this template, skipping job creation",
					"semaphoreKey", semaphoreKey)
				shouldCreateJob = false
				return
			}

			// Try to acquire a semaphore slot (non-blocking)
			shouldCreateJob = GetGlobalSemaphore().TryAcquire(semaphoreKey)
		})

		if !shouldCreateJob {
			if !GetGlobalSemaphore().IsHeld(semaphoreKey) {
				// Not held and not acquired means at capacity
				logger.Info("discovery job creation blocked by concurrent limit (semaphore)",
					"semaphoreKey", semaphoreKey,
					"activeSlots", GetGlobalSemaphore().ActiveCount(),
					"availableSlots", GetGlobalSemaphore().AvailableSlots(),
					"limit", constants.MaxConcurrentDiscoveryJobs)
			}
			return planResult
		}

		logger.Info("acquired semaphore slot for discovery job",
			"semaphoreKey", semaphoreKey,
			"activeSlots", GetGlobalSemaphore().ActiveCount(),
			"availableSlots", GetGlobalSemaphore().AvailableSlots())

		job := BuildDiscoveryJob(DiscoveryJobSpec{
			TemplateName:     template.Name,
			Namespace:        operatorNamespace,
			ModelID:          template.Spec.ModelName,
			Image:            image,
			Env:              nil, // Cluster templates don't have env vars
			ImagePullSecrets: clusterModel.Spec.ImagePullSecrets,
			ServiceAccount:   clusterModel.Spec.ServiceAccountName,
			TemplateSpec:     template.Spec.AIMServiceTemplateSpecCommon,
			OwnerRef: metav1.OwnerReference{
				APIVersion:         aimv1alpha1.GroupVersion.String(),
				Kind:               "AIMClusterServiceTemplate",
				Name:               template.Name,
				UID:                template.UID,
				Controller:         ptr.To(true),
				BlockOwnerDeletion: ptr.To(true),
			},
		})
		planResult.Apply(job)
	}

	return planResult
}

// ============================================================================
// STATUS DECORATION
// ============================================================================

// DecorateStatus adds domain-specific status fields for namespace-scoped templates.
func (r *ServiceTemplateReconciler) DecorateStatus(
	status *aimv1alpha1.AIMServiceTemplateStatus,
	cm *controllerutils.ConditionManager,
	obs ServiceTemplateObservation,
) {
	decorateTemplateStatus(status, cm, obs.template.Spec.ModelSources, obs.discoveryJob, obs.parsedDiscovery, obs.model.Value)
}

// DecorateStatus adds domain-specific status fields for cluster-scoped templates.
func (r *ClusterServiceTemplateReconciler) DecorateStatus(
	status *aimv1alpha1.AIMServiceTemplateStatus,
	cm *controllerutils.ConditionManager,
	obs ClusterServiceTemplateObservation,
) {
	decorateClusterTemplateStatus(status, cm, obs.template.Spec.ModelSources, obs.discoveryJob, obs.parsedDiscovery, obs.clusterModel.Value)
}

// decorateTemplateStatus handles common status decoration for namespace-scoped templates.
func decorateTemplateStatus(
	status *aimv1alpha1.AIMServiceTemplateStatus,
	cm *controllerutils.ConditionManager,
	specModelSources []aimv1alpha1.AIMModelSource,
	discoveryJobResult controllerutils.FetchResult[*batchv1.Job],
	parsedDiscovery *ParsedDiscovery,
	model *aimv1alpha1.AIMModel,
) {
	// Set resolved model reference if available
	if model != nil {
		status.ResolvedModel = &aimv1alpha1.AIMResolvedReference{
			Name:      model.Name,
			Namespace: model.Namespace,
		}
	}

	// Handle inline model sources - copy from spec to status
	// This takes precedence over discovery results
	if len(specModelSources) > 0 {
		status.ModelSources = specModelSources
		cm.MarkTrue("Discovered", "InlineModelSources", "Model sources provided in-line in spec")
		return
	}

	// Set discovery job reference if available
	if discoveryJobResult.OK() && discoveryJobResult.Value != nil {
		job := discoveryJobResult.Value
		status.DiscoveryJob = &aimv1alpha1.AIMResolvedReference{
			Name:      job.Name,
			Namespace: job.Namespace,
		}

		// Track discovery state for failed jobs (for backoff)
		if IsJobFailed(job) {
			updateDiscoveryStateOnFailure(status, job)
		}
	}

	// Set parsed discovery results if available
	if parsedDiscovery != nil {
		status.ModelSources = parsedDiscovery.ModelSources
		if parsedDiscovery.Profile != nil {
			status.Profile = parsedDiscovery.Profile
		}
		// Clear discovery state on success
		status.Discovery = nil
		cm.MarkTrue("Discovered", "DiscoveryComplete", "Discovery job completed successfully")
		return
	}

	// If waiting due to concurrent limit, set appropriate status
	// Check semaphore state for accurate status (no available slots = at capacity)
	if GetGlobalSemaphore().AvailableSlots() == 0 {
		cm.MarkFalse("Discovered", aimv1alpha1.AIMTemplateReasonAwaitingDiscovery,
			fmt.Sprintf("Waiting for discovery slot (%d/%d slots in use)",
				GetGlobalSemaphore().ActiveCount(), constants.MaxConcurrentDiscoveryJobs))
	}
}

// decorateClusterTemplateStatus handles common status decoration for cluster-scoped templates.
func decorateClusterTemplateStatus(
	status *aimv1alpha1.AIMServiceTemplateStatus,
	cm *controllerutils.ConditionManager,
	specModelSources []aimv1alpha1.AIMModelSource,
	discoveryJobResult controllerutils.FetchResult[*batchv1.Job],
	parsedDiscovery *ParsedDiscovery,
	clusterModel *aimv1alpha1.AIMClusterModel,
) {
	// Set resolved model reference if available
	if clusterModel != nil {
		status.ResolvedModel = &aimv1alpha1.AIMResolvedReference{
			Name: clusterModel.Name,
		}
	}

	// Handle inline model sources - copy from spec to status
	// This takes precedence over discovery results
	if len(specModelSources) > 0 {
		status.ModelSources = specModelSources
		cm.MarkTrue("Discovered", "InlineModelSources", "Model sources provided in-line in spec")
		return
	}

	// Set discovery job reference if available
	if discoveryJobResult.OK() && discoveryJobResult.Value != nil {
		job := discoveryJobResult.Value
		status.DiscoveryJob = &aimv1alpha1.AIMResolvedReference{
			Name:      job.Name,
			Namespace: job.Namespace,
		}

		// Track discovery state for failed jobs (for backoff)
		if IsJobFailed(job) {
			updateDiscoveryStateOnFailure(status, job)
		}
	}

	// Set parsed discovery results if available
	if parsedDiscovery != nil {
		status.ModelSources = parsedDiscovery.ModelSources
		if parsedDiscovery.Profile != nil {
			status.Profile = parsedDiscovery.Profile
		}
		// Clear discovery state on success
		status.Discovery = nil
		cm.MarkTrue("Discovered", "DiscoveryComplete", "Discovery job completed successfully")
		return
	}

	// If waiting due to concurrent limit, set appropriate status
	// Check semaphore state for accurate status (no available slots = at capacity)
	if GetGlobalSemaphore().AvailableSlots() == 0 {
		cm.MarkFalse("Discovered", aimv1alpha1.AIMTemplateReasonAwaitingDiscovery,
			fmt.Sprintf("Waiting for discovery slot (%d/%d slots in use)",
				GetGlobalSemaphore().ActiveCount(), constants.MaxConcurrentDiscoveryJobs))
	}
}

// updateDiscoveryStateOnFailure updates the discovery tracking state when a job fails.
// This increments the attempt counter and records the failure time for backoff calculation.
func updateDiscoveryStateOnFailure(status *aimv1alpha1.AIMServiceTemplateStatus, job *batchv1.Job) {
	now := metav1.Now()

	// Initialize discovery state if needed
	if status.Discovery == nil {
		status.Discovery = &aimv1alpha1.DiscoveryState{}
	}

	// Only increment if this is a new failure (job creation time > last attempt time)
	// This prevents double-counting on subsequent reconciles
	if status.Discovery.LastAttemptTime == nil || job.CreationTimestamp.After(status.Discovery.LastAttemptTime.Time) {
		status.Discovery.Attempts++
		status.Discovery.LastAttemptTime = &now
		status.Discovery.LastFailureReason = GetJobFailureReason(job)
	}
}
