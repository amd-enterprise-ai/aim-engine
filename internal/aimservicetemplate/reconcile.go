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
	Client    client.Client
	Clientset kubernetes.Interface
	Scheme    *runtime.Scheme
}

// ClusterServiceTemplateReconciler implements the DomainReconciler interface for cluster-scoped templates.
type ClusterServiceTemplateReconciler struct {
	Client    client.Client
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

	// Check GPU availability first - required for both custom and discovery-based models
	if !obs.isGPUAvailable() {
		logger.V(1).Info("required GPU not available, skipping resource planning")
		return planResult
	}

	// Check if inline model sources are provided - template is immediately ready (no discovery needed)
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

	// Template not ready - check if we need to create discovery job
	hasCompletedJob := HasCompletedDiscoveryJob(obs.discoveryJob)
	hasActiveJob := HasActiveDiscoveryJob(obs.discoveryJob)

	logger.V(1).Info("discovery job state check",
		"hasCompletedJob", hasCompletedJob,
		"hasActiveJob", hasActiveJob,
		"jobExists", obs.discoveryJob.Value != nil)

	if !hasCompletedJob && !hasActiveJob {
		// Compute spec hash for backoff reset detection
		specHash := ComputeDiscoverySpecHash(template.Spec.AIMServiceTemplateSpecCommon, template.Spec.ModelName, image)

		// Check backoff timing from previous failed attempts
		shouldCreate, reason, message := ShouldCreateDiscoveryJob(template.Status.Discovery, specHash, time.Now())
		if !shouldCreate {
			logger.V(1).Info("discovery job creation blocked by backoff",
				"reason", reason,
				"message", message,
				"attempts", template.Status.Discovery.Attempts)
			return planResult
		}

		// Check concurrent job limit - we're protected by the discovery lock at the controller level
		// so this count + create is atomic across all reconcilers
		activeJobs, err := CountActiveDiscoveryJobs(ctx, r.Client)
		if err != nil {
			logger.Error(err, "failed to count active discovery jobs")
			return planResult
		}

		if activeJobs >= constants.MaxConcurrentDiscoveryJobs {
			logger.V(1).Info("discovery job creation blocked by concurrent limit",
				"activeJobs", activeJobs,
				"limit", constants.MaxConcurrentDiscoveryJobs)
			// Signal controller to requeue after a delay to try again
			planResult.RequeueAfter = 5 * time.Second
			return planResult
		}

		logger.V(1).Info("creating discovery job",
			"activeJobs", activeJobs,
			"limit", constants.MaxConcurrentDiscoveryJobs)

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

	// Check GPU availability first - required for both custom and discovery-based models
	if !obs.isGPUAvailable() {
		logger.V(1).Info("required GPU not available, skipping resource planning")
		return planResult
	}

	// Check if inline model sources are provided - template is immediately ready (no discovery needed)
	if len(template.Spec.ModelSources) > 0 {
		logger.V(1).Info("template has inline model sources, skipping discovery")
		return planResult
	}

	// If template is Ready, nothing more to plan
	if template.Status.Status == constants.AIMStatusReady {
		return planResult
	}

	// Template not ready - check if we need to create discovery job
	hasCompletedJob := HasCompletedDiscoveryJob(obs.discoveryJob)
	hasActiveJob := HasActiveDiscoveryJob(obs.discoveryJob)

	operatorNamespace := constants.GetOperatorNamespace()

	if !hasCompletedJob && !hasActiveJob {
		// Compute spec hash for backoff reset detection
		specHash := ComputeDiscoverySpecHash(template.Spec.AIMServiceTemplateSpecCommon, template.Spec.ModelName, image)

		// Check backoff timing from previous failed attempts
		shouldCreate, reason, message := ShouldCreateDiscoveryJob(template.Status.Discovery, specHash, time.Now())
		if !shouldCreate {
			logger.V(1).Info("discovery job creation blocked by backoff",
				"reason", reason,
				"message", message,
				"attempts", template.Status.Discovery.Attempts)
			return planResult
		}

		// Check concurrent job limit - we're protected by the discovery lock at the controller level
		// so this count + create is atomic across all reconcilers
		activeJobs, err := CountActiveDiscoveryJobs(ctx, r.Client)
		if err != nil {
			logger.Error(err, "failed to count active discovery jobs")
			return planResult
		}

		if activeJobs >= constants.MaxConcurrentDiscoveryJobs {
			logger.V(1).Info("discovery job creation blocked by concurrent limit",
				"activeJobs", activeJobs,
				"limit", constants.MaxConcurrentDiscoveryJobs)
			// Signal controller to requeue after a delay to try again
			planResult.RequeueAfter = 5 * time.Second
			return planResult
		}

		logger.V(1).Info("creating discovery job",
			"activeJobs", activeJobs,
			"limit", constants.MaxConcurrentDiscoveryJobs)

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
	// Compute spec hash for failure state tracking
	var specHash string
	if obs.model.Value != nil {
		specHash = ComputeDiscoverySpecHash(obs.template.Spec.AIMServiceTemplateSpecCommon, obs.template.Spec.ModelName, obs.model.Value.Spec.Image)
	}

	decorateTemplateStatusCommon(
		status, cm, &obs.template.Spec.AIMServiceTemplateSpecCommon, obs.discoveryJob, obs.parsedDiscovery,
		obs.template.Status.Discovery, specHash,
	)

	// Set resolved model reference if available
	if obs.model.Value != nil {
		status.ResolvedModel = &aimv1alpha1.AIMResolvedReference{
			Name:      obs.model.Value.Name,
			Namespace: obs.model.Value.Namespace,
		}
	}
}

// DecorateStatus adds domain-specific status fields for cluster-scoped templates.
func (r *ClusterServiceTemplateReconciler) DecorateStatus(
	status *aimv1alpha1.AIMServiceTemplateStatus,
	cm *controllerutils.ConditionManager,
	obs ClusterServiceTemplateObservation,
) {
	// Compute spec hash for failure state tracking
	var specHash string
	if obs.clusterModel.Value != nil {
		specHash = ComputeDiscoverySpecHash(obs.template.Spec.AIMServiceTemplateSpecCommon, obs.template.Spec.ModelName, obs.clusterModel.Value.Spec.Image)
	}

	decorateTemplateStatusCommon(
		status, cm, &obs.template.Spec.AIMServiceTemplateSpecCommon, obs.discoveryJob, obs.parsedDiscovery,
		obs.template.Status.Discovery, specHash,
	)

	// Set resolved model reference if available
	if obs.clusterModel.Value != nil {
		status.ResolvedModel = &aimv1alpha1.AIMResolvedReference{
			Name: obs.clusterModel.Value.Name,
		}
	}
}

// decorateTemplateStatusCommon handles shared status decoration for both namespace and cluster-scoped templates.
func decorateTemplateStatusCommon(
	status *aimv1alpha1.AIMServiceTemplateStatus,
	cm *controllerutils.ConditionManager,
	spec *aimv1alpha1.AIMServiceTemplateSpecCommon,
	discoveryJobResult controllerutils.FetchResult[*batchv1.Job],
	parsedDiscovery *ParsedDiscovery,
	currentDiscoveryState *aimv1alpha1.DiscoveryState,
	specHash string,
) {
	// Handle inline model sources - copy from spec to status
	// This takes precedence over discovery results
	if len(spec.ModelSources) > 0 {
		status.ModelSources = spec.ModelSources
		// Build profile from spec for custom models (no discovery runs)
		status.Profile = buildProfileFromSpec(spec)
		// Resolve hardware from spec (no discovery)
		status.ResolvedHardware = resolveHardware(nil, spec)
		status.HardwareSummary = formatHardwareSummary(status.ResolvedHardware)
		status.Discovery = nil // Clear stale discovery state
		cm.MarkTrue(aimv1alpha1.AIMTemplateDiscoveryConditionType, "InlineModelSources", "Model sources provided in-line in spec")
		return
	}

	// Don't regress the Discovered condition if it's already True.
	// This prevents stale reconciles (that started before the job completed) from
	// overwriting Discovered=True back to False.
	existingDiscovered := cm.Get(aimv1alpha1.AIMTemplateDiscoveryConditionType)
	if existingDiscovered != nil && existingDiscovered.Status == metav1.ConditionTrue {
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
			updateDiscoveryStateOnFailure(status, job, specHash)
		}
	}

	// Set appropriate status message based on waiting reason
	if currentDiscoveryState != nil && currentDiscoveryState.Attempts > 0 && currentDiscoveryState.LastAttemptTime != nil {
		// Check if we're in backoff period
		backoffDuration := CalculateBackoffDuration(currentDiscoveryState.Attempts)
		nextAttemptTime := currentDiscoveryState.LastAttemptTime.Add(backoffDuration)
		now := time.Now()

		if now.Before(nextAttemptTime) {
			remaining := nextAttemptTime.Sub(now).Round(time.Second)
			cm.MarkFalse(aimv1alpha1.AIMTemplateDiscoveryConditionType, aimv1alpha1.AIMTemplateReasonAwaitingDiscovery,
				fmt.Sprintf("Waiting %s before retry (attempt %d failed)", remaining, currentDiscoveryState.Attempts))
			return
		}
	}

	// Set parsed discovery results if available
	if parsedDiscovery != nil {
		status.ModelSources = parsedDiscovery.ModelSources
		if parsedDiscovery.Profile != nil {
			status.Profile = parsedDiscovery.Profile
		}
		// Resolve hardware from discovery + spec fallback // current
		status.ResolvedHardware = resolveHardware(parsedDiscovery, spec)
		status.HardwareSummary = formatHardwareSummary(status.ResolvedHardware)
		cm.MarkTrue("Discovered", "DiscoveryComplete", "Discovery job completed successfully")
	}

	// Generic awaiting discovery message if no other message was set
	cm.MarkFalse(aimv1alpha1.AIMTemplateDiscoveryConditionType, aimv1alpha1.AIMTemplateReasonAwaitingDiscovery,
		"Waiting for discovery to complete")
}

// buildProfileFromSpec creates an AIMProfile from template spec for custom models.
// This is used when discovery doesn't run (inline model sources) to populate
// the status.Profile with GPU count and other metadata from the spec.
func buildProfileFromSpec(spec *aimv1alpha1.AIMServiceTemplateSpecCommon) *aimv1alpha1.AIMProfile {
	profile := &aimv1alpha1.AIMProfile{
		Metadata: aimv1alpha1.AIMProfileMetadata{},
	}

	// Set GPU info from spec
	if spec.Hardware != nil && spec.Hardware.GPU != nil {
		profile.Metadata.GPUCount = spec.Hardware.GPU.Requests
		if spec.Hardware.GPU.Model != "" {
			profile.Metadata.GPU = spec.Hardware.GPU.Model
		}
	}

	// Set metric and precision from spec
	if spec.Metric != nil {
		profile.Metadata.Metric = *spec.Metric
	}
	if spec.Precision != nil {
		profile.Metadata.Precision = *spec.Precision
	}

	// Set type from spec if explicitly set
	if spec.Type != nil {
		profile.Metadata.Type = *spec.Type
	}

	return profile
}

// resolveHardware computes the final hardware requirements from discovery and spec.
// If discovery ran, use discovery values (even if 0). Otherwise use spec values.
// Returns nil if no hardware requirements can be resolved (to avoid CEL validation errors).
func resolveHardware(discovery *ParsedDiscovery, spec *aimv1alpha1.AIMServiceTemplateSpecCommon) *aimv1alpha1.AIMHardwareRequirements {
	var gpuCount int32
	var gpuModel string
	var resourceName string

	// Resource name always comes from spec (discovery doesn't provide this)
	if spec.Hardware != nil && spec.Hardware.GPU != nil {
		resourceName = spec.Hardware.GPU.ResourceName
	}

	if discovery != nil && discovery.Profile != nil {
		// Discovery ran - use discovery values (even if 0)
		gpuCount = discovery.Profile.Metadata.GPUCount
		gpuModel = discovery.Profile.Metadata.GPU
	} else {
		// No discovery (custom models with inline model sources) - use spec values
		if spec.Hardware != nil && spec.Hardware.GPU != nil {
			gpuCount = spec.Hardware.GPU.Requests
			gpuModel = spec.Hardware.GPU.Model
		}
	}

	// Build the resolved hardware requirements
	// CEL validation requires at least gpu or cpu to be specified
	resolved := &aimv1alpha1.AIMHardwareRequirements{}
	hasHardware := false

	// Add GPU if we have any GPU values
	if gpuCount > 0 || gpuModel != "" || resourceName != "" {
		resolved.GPU = &aimv1alpha1.AIMGpuRequirements{
			Requests: gpuCount,
		}
		if gpuModel != "" {
			resolved.GPU.Model = gpuModel
		}
		if resourceName != "" {
			resolved.GPU.ResourceName = resourceName
		}
		// Copy minVram from spec (not provided by discovery)
		if spec.Hardware != nil && spec.Hardware.GPU != nil && spec.Hardware.GPU.MinVRAM != nil {
			minVRAMCopy := spec.Hardware.GPU.MinVRAM.DeepCopy()
			resolved.GPU.MinVRAM = &minVRAMCopy
		}
		hasHardware = true
	}

	// Add CPU if spec has CPU requirements (CPU-only templates)
	if spec.Hardware != nil && spec.Hardware.CPU != nil {
		resolved.CPU = spec.Hardware.CPU.DeepCopy()
		hasHardware = true
	}

	// Return nil if no hardware to avoid CEL validation error
	if !hasHardware {
		return nil
	}

	return resolved
}

// formatHardwareSummary creates a human-readable string describing the hardware requirements.
// Returns "{count} x {model}" for GPU (e.g., "2 x MI300X") or "CPU" for CPU-only.
func formatHardwareSummary(hw *aimv1alpha1.AIMHardwareRequirements) string {
	if hw == nil {
		return ""
	}

	// Check for GPU configuration
	if hw.GPU != nil && (hw.GPU.Requests > 0 || hw.GPU.Model != "") {
		model := hw.GPU.Model
		if model == "" {
			model = "GPU"
		}
		if hw.GPU.Requests > 0 {
			return fmt.Sprintf("%d x %s", hw.GPU.Requests, model)
		}
		// requests=0 but model specified (testing scenario)
		return model
	}

	// No GPU means CPU-only
	return "CPU"
}

// updateDiscoveryStateOnFailure updates the discovery tracking state when a job fails.
// This increments the attempt counter and records the failure time for backoff calculation.
// The specHash parameter is stored to enable backoff reset when the spec changes.
func updateDiscoveryStateOnFailure(status *aimv1alpha1.AIMServiceTemplateStatus, job *batchv1.Job, specHash string) {
	now := metav1.Now()

	// Initialize discovery state if needed
	if status.Discovery == nil {
		status.Discovery = &aimv1alpha1.DiscoveryState{}
	}

	// Check if spec hash changed - if so, reset the attempt counter
	if status.Discovery.SpecHash != "" && status.Discovery.SpecHash != specHash {
		status.Discovery.Attempts = 0
	}

	// Only increment if this is a new failure (job creation time > last attempt time)
	// This prevents double-counting on subsequent reconciles
	if status.Discovery.LastAttemptTime == nil || job.CreationTimestamp.After(status.Discovery.LastAttemptTime.Time) {
		status.Discovery.Attempts++
		status.Discovery.LastAttemptTime = &now
		status.Discovery.LastFailureReason = GetJobFailureReason(job)
		status.Discovery.SpecHash = specHash // incoming 2
	}
}
