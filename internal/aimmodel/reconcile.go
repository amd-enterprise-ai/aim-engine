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

package aimmodel

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/aimruntimeconfig"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

type ClusterModelReconciler struct {
	Clientset kubernetes.Interface
	Scheme    *runtime.Scheme
}

type ModelReconciler struct {
	Clientset kubernetes.Interface
	Scheme    *runtime.Scheme
}

// ============================================================================
// FETCH
// ============================================================================

type ClusterModelFetchResult struct {
	model *aimv1alpha1.AIMClusterModel

	mergedRuntimeConfig     controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]
	imageMetadata           controllerutils.FetchResult[*aimv1alpha1.ImageMetadata]
	clusterServiceTemplates controllerutils.FetchResult[*aimv1alpha1.AIMClusterServiceTemplateList]
}

func (r *ClusterModelReconciler) FetchRemoteState(
	ctx context.Context,
	c client.Client,
	reconcileCtx controllerutils.ReconcileContext[*aimv1alpha1.AIMClusterModel],
) ClusterModelFetchResult {
	clusterModel := reconcileCtx.Object
	ctx = log.IntoContext(ctx, log.FromContext(ctx).WithValues(
		"phase", "fetch",
		"clusterModel", clusterModel.Name,
		"image", clusterModel.Spec.Image,
	))

	result := ClusterModelFetchResult{
		model: clusterModel,
	}

	// Runtime config
	result.mergedRuntimeConfig = reconcileCtx.MergedRuntimeConfig

	// Image metadata
	result.imageMetadata = fetchImageMetadata(ctx, r.Clientset, clusterModel.Spec, &clusterModel.Status, constants.GetOperatorNamespace())

	// Cluster service templates
	templates := &aimv1alpha1.AIMClusterServiceTemplateList{}
	result.clusterServiceTemplates = controllerutils.FetchList(ctx, c, templates, client.MatchingFields{aimv1alpha1.ServiceTemplateModelNameIndexKey: clusterModel.Name})

	return result
}

func (result ClusterModelFetchResult) GetComponentHealth() []controllerutils.ComponentHealth {
	// RuntimeConfig is optional for models - they can operate without one
	runtimeConfigHealth := result.mergedRuntimeConfig.ToUpstreamComponentHealth("RuntimeConfig", aimruntimeconfig.GetRuntimeConfigHealth)

	clusterServiceTemplateHealth := result.clusterServiceTemplates.ToUpstreamComponentHealth("ClusterServiceTemplates", func(list *aimv1alpha1.AIMClusterServiceTemplateList) controllerutils.ComponentHealth {
		return inspectClusterTemplateStatuses(
			result.model.Spec.ExpectsTemplates(&result.model.Status),
			list.Items,
		)
	})

	health := []controllerutils.ComponentHealth{runtimeConfigHealth}

	// Only report image metadata health for non-custom models
	if !IsCustomModel(&result.model.Spec) {
		imageMetadataHealth := result.imageMetadata.ToUpstreamComponentHealth("ImageMetadata", func(metadata *aimv1alpha1.ImageMetadata) controllerutils.ComponentHealth {
			return controllerutils.ComponentHealth{
				State:  constants.AIMStatusReady,
				Reason: "ImageMetadataFound",
			}
		})
		health = append(health, imageMetadataHealth)
	}

	health = append(health, clusterServiceTemplateHealth)
	return health
}

// inspectClusterTemplateStatuses aggregates cluster template statuses into a single ComponentState.
func inspectClusterTemplateStatuses(expectsTemplates *bool, templates []aimv1alpha1.AIMClusterServiceTemplate) controllerutils.ComponentHealth {
	statuses := make([]constants.AIMStatus, len(templates))
	for i := range templates {
		statuses[i] = templates[i].Status.Status
	}
	return aggregateTemplateStatuses(expectsTemplates, statuses)
}

type ModelFetchResult struct {
	model *aimv1alpha1.AIMModel

	mergedRuntimeConfig controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]
	imageMetadata       controllerutils.FetchResult[*aimv1alpha1.ImageMetadata]
	serviceTemplates    controllerutils.FetchResult[*aimv1alpha1.AIMServiceTemplateList]
}

func (result ModelFetchResult) GetComponentHealth() []controllerutils.ComponentHealth {
	// RuntimeConfig is optional for models - they can operate without one
	runtimeConfigHealth := result.mergedRuntimeConfig.ToComponentHealth("RuntimeConfig", aimruntimeconfig.GetRuntimeConfigHealth)
	health := []controllerutils.ComponentHealth{runtimeConfigHealth}

	// Only report image metadata health for non-custom models
	if !IsCustomModel(&result.model.Spec) {
		imageMetadataHealth := result.imageMetadata.ToComponentHealth("ImageMetadata", func(metadata *aimv1alpha1.ImageMetadata) controllerutils.ComponentHealth {
			return controllerutils.ComponentHealth{
				State:  constants.AIMStatusReady,
				Reason: "ImageMetadataFound",
			}
		})
		health = append(health, imageMetadataHealth)
	}

	serviceTemplateHealth := result.serviceTemplates.ToComponentHealth("ServiceTemplates", func(list *aimv1alpha1.AIMServiceTemplateList) controllerutils.ComponentHealth {
		return inspectServiceTemplateStatuses(
			result.model.Spec.ExpectsTemplates(&result.model.Status),
			list.Items,
		)
	})

	health = append(health, serviceTemplateHealth)
	return health
}

func (r *ModelReconciler) FetchRemoteState(
	ctx context.Context,
	c client.Client,
	reconcileCtx controllerutils.ReconcileContext[*aimv1alpha1.AIMModel],
) ModelFetchResult {
	model := reconcileCtx.Object
	ctx = log.IntoContext(ctx, log.FromContext(ctx).WithValues(
		"phase", "fetch",
		"model", model.Name,
		"namespace", model.Namespace,
		"image", model.Spec.Image,
	))

	result := ModelFetchResult{
		model: model,
	}

	result.mergedRuntimeConfig = reconcileCtx.MergedRuntimeConfig

	// Image metadata
	result.imageMetadata = fetchImageMetadata(ctx, r.Clientset, model.Spec, &model.Status, model.Namespace)

	// Service templates
	templates := &aimv1alpha1.AIMServiceTemplateList{}
	result.serviceTemplates = controllerutils.FetchList(ctx, c, templates,
		client.InNamespace(model.Namespace),
		client.MatchingFields{aimv1alpha1.ServiceTemplateModelNameIndexKey: model.Name})

	return result
}

// inspectServiceTemplateStatuses aggregates namespace template statuses into a single ComponentState.
func inspectServiceTemplateStatuses(expectsTemplates *bool, templates []aimv1alpha1.AIMServiceTemplate) controllerutils.ComponentHealth {
	statuses := make([]constants.AIMStatus, len(templates))
	for i := range templates {
		statuses[i] = templates[i].Status.Status
	}
	return aggregateTemplateStatuses(expectsTemplates, statuses)
}

// =========
// SHARED
// =========

// aggregateTemplateStatuses aggregates template statuses into a single ComponentState.
// expectsTemplates indicates whether templates are expected (nil = unknown/still fetching).
func aggregateTemplateStatuses(expectsTemplates *bool, statuses []constants.AIMStatus) controllerutils.ComponentHealth {
	var ready, progressing, degradedOrFailed, notAvailable int
	for _, s := range statuses {
		switch s {
		case constants.AIMStatusReady:
			ready++
		case constants.AIMStatusProgressing, constants.AIMStatusPending:
			progressing++
		case constants.AIMStatusDegraded, constants.AIMStatusFailed:
			degradedOrFailed++
		case constants.AIMStatusNotAvailable:
			notAvailable++
		}
	}

	total := len(statuses)

	var status constants.AIMStatus
	var reason, message string

	switch {
	// Handle templates disabled first (createServiceTemplates: false)
	// When disabled, the model doesn't care about template statuses - it's Ready immediately.
	// Any templates that exist were created externally and are not the model's responsibility.
	case expectsTemplates != nil && !*expectsTemplates:
		status = constants.AIMStatusReady
		reason = aimv1alpha1.AIMModelReasonNoTemplatesExpected
		message = "Template creation disabled for this model"

	// Handle no templates case
	case total == 0:
		switch {
		case expectsTemplates == nil:
			// Metadata not yet fetched - still working
			status = constants.AIMStatusProgressing
			reason = aimv1alpha1.AIMModelReasonAwaitingMetadata
			message = "Waiting for the model to fetch metadata"
		case *expectsTemplates:
			// We expect templates but none exist yet
			status = constants.AIMStatusProgressing
			reason = aimv1alpha1.AIMModelReasonCreatingTemplates
			message = "Creating templates for the model"
		default:
			// No templates expected - we're done
			status = constants.AIMStatusReady
			reason = aimv1alpha1.AIMModelReasonNoTemplatesExpected
			message = "The model does not have any recommended templates"
		}

	// All templates in same state
	case ready == total:
		status = constants.AIMStatusReady
		reason = aimv1alpha1.AIMModelReasonAllTemplatesReady
		message = "All templates are ready"

	case degradedOrFailed == total:
		status = constants.AIMStatusFailed
		reason = aimv1alpha1.AIMModelReasonAllTemplatesFailed
		message = "All templates are degraded or failed"

	case notAvailable == total:
		status = constants.AIMStatusNotAvailable
		reason = aimv1alpha1.AIMModelReasonNoTemplatesAvailable
		message = "None of the model's templates are available (no matching GPUs in the cluster)"

	// Mixed states - priority order: degraded > progressing > ready
	case degradedOrFailed > 0:
		// Some templates failed or degraded - this is worse than progressing
		status = constants.AIMStatusDegraded
		reason = aimv1alpha1.AIMModelReasonSomeTemplatesDegraded
		message = "Some templates are degraded or failed"

	case progressing > 0:
		// Some templates still processing, nothing failed yet
		status = constants.AIMStatusProgressing
		reason = aimv1alpha1.AIMModelReasonTemplatesProgressing
		message = "Templates are progressing"

	case ready > 0 && notAvailable > 0:
		// Mix of ready and notAvailable (no progressing, no failed)
		status = constants.AIMStatusReady
		reason = aimv1alpha1.AIMModelReasonSomeTemplatesReady
		message = "All templates have finished processing (some are not available)"

	default:
		// Templates exist but have unknown/empty status (e.g., not yet reconciled)
		status = constants.AIMStatusProgressing
		reason = aimv1alpha1.AIMModelReasonTemplatesProgressing
		message = "Unknown status"
	}

	return controllerutils.ComponentHealth{
		State:   status,
		Reason:  reason,
		Message: message,
	}
}

// fetchImageMetadata determines how to obtain image metadata for a model.
// It handles these cases:
//  0. Custom model (has modelSources) - skip fetch entirely, templates are built from customTemplates
//  1. Extraction explicitly disabled - skip fetch entirely
//  2. Spec-provided metadata (air-gapped environments) - returns the spec value directly
//  3. Already cached in status - returns empty result (no fetch needed)
//  4. Needs remote fetch - calls inspectImage to fetch from registry
func fetchImageMetadata(
	ctx context.Context,
	clientset kubernetes.Interface,
	spec aimv1alpha1.AIMModelSpec,
	status *aimv1alpha1.AIMModelStatus,
	secretNamespace string,
) controllerutils.FetchResult[*aimv1alpha1.ImageMetadata] {
	// Case 0: Custom model - skip image metadata extraction entirely
	// Custom models use spec.customTemplates instead of discovered templates
	if IsCustomModel(&spec) {
		log.FromContext(ctx).V(1).Info("custom model detected, skipping image metadata extraction")
		return controllerutils.FetchResult[*aimv1alpha1.ImageMetadata]{}
	}

	// Case 1: Extraction explicitly disabled - skip fetch entirely
	if spec.Discovery != nil && !spec.Discovery.ExtractMetadata {
		log.FromContext(ctx).V(1).Info("metadata extraction disabled in spec")
		return controllerutils.FetchResult[*aimv1alpha1.ImageMetadata]{}
	}

	// Case 2: Use spec-provided metadata (air-gapped environments)
	if spec.ImageMetadata != nil {
		log.FromContext(ctx).V(1).Info("using spec-provided imageMetadata")
		return controllerutils.FetchResult[*aimv1alpha1.ImageMetadata]{
			Value: spec.ImageMetadata,
		}
	}

	// Case 3: Already cached in status - no fetch needed
	if !shouldExtractMetadata(status) {
		return controllerutils.FetchResult[*aimv1alpha1.ImageMetadata]{}
	}

	// Case 4: Fetch from registry
	metadata, err := inspectImage(
		ctx,
		spec.Image,
		spec.ImagePullSecrets,
		clientset,
		secretNamespace,
	)
	return controllerutils.FetchResult[*aimv1alpha1.ImageMetadata]{
		Value: metadata,
		Error: err,
	}
}

// ============================================================================
// OBSERVATION
// ============================================================================

// ClusterModelObservation embeds the fetch result. The observation phase is minimal
// since FetchResult.GetComponentHealth() handles health derivation and PlanResources
// uses spec helper methods directly for planning decisions.
type ClusterModelObservation struct {
	ClusterModelFetchResult
}

func (r *ClusterModelReconciler) ComposeState(
	_ context.Context,
	_ controllerutils.ReconcileContext[*aimv1alpha1.AIMClusterModel],
	fetch ClusterModelFetchResult,
) ClusterModelObservation {
	return ClusterModelObservation{ClusterModelFetchResult: fetch}
}

// ModelObservation embeds the fetch result. The observation phase is minimal
// since FetchResult.GetComponentHealth() handles health derivation and PlanResources
// uses spec helper methods directly for planning decisions.
type ModelObservation struct {
	ModelFetchResult
}

func (r *ModelReconciler) ComposeState(
	_ context.Context,
	_ controllerutils.ReconcileContext[*aimv1alpha1.AIMModel],
	fetch ModelFetchResult,
) ModelObservation {
	return ModelObservation{ModelFetchResult: fetch}
}

// ============================================================================
// PLAN
// ============================================================================

func (r *ClusterModelReconciler) PlanResources(
	ctx context.Context,
	_ controllerutils.ReconcileContext[*aimv1alpha1.AIMClusterModel],
	obs ClusterModelObservation,
) controllerutils.PlanResult {
	logger := log.FromContext(ctx).WithName("plan")
	model := obs.model

	planResult := controllerutils.PlanResult{}

	// Check if we should create templates using spec helper
	expects := model.Spec.ExpectsTemplates(&model.Status)
	if expects == nil || !*expects {
		logger.V(1).Info("no templates expected", "expects", expects)
		return controllerutils.PlanResult{}
	}

	// For custom models, build templates from customTemplates
	if IsCustomModel(&model.Spec) {
		logger.V(1).Info("building custom templates for cluster model")
		templates := buildCustomClusterServiceTemplates(model)
		for _, template := range templates {
			planResult.Apply(template)
		}
		return planResult
	}

	// For image-based models, get metadata to create templates from
	metadata := model.Spec.GetEffectiveImageMetadata(&model.Status)
	if metadata == nil || metadata.Model == nil {
		logger.V(1).Info("no metadata available yet")
		return controllerutils.PlanResult{}
	}

	// Build templates from recommended deployments
	for _, deployment := range metadata.Model.RecommendedDeployments {
		template := buildClusterServiceTemplate(model, deployment)
		planResult.Apply(template)
	}
	return planResult
}

func (r *ModelReconciler) PlanResources(
	ctx context.Context,
	_ controllerutils.ReconcileContext[*aimv1alpha1.AIMModel],
	obs ModelObservation,
) controllerutils.PlanResult {
	logger := log.FromContext(ctx).WithName("plan")
	model := obs.model

	planResult := controllerutils.PlanResult{}

	// Check if we should create templates using spec helper
	expects := model.Spec.ExpectsTemplates(&model.Status)
	if expects == nil || !*expects {
		logger.V(1).Info("no templates expected", "expects", expects)
		return controllerutils.PlanResult{}
	}

	// For custom models, build templates from customTemplates
	if IsCustomModel(&model.Spec) {
		logger.V(1).Info("building custom templates for model")
		templates := buildCustomServiceTemplates(model)
		for _, template := range templates {
			planResult.Apply(template)
		}
		return planResult
	}

	// For image-based models, get metadata to create templates from
	metadata := model.Spec.GetEffectiveImageMetadata(&model.Status)
	if metadata == nil || metadata.Model == nil {
		logger.V(1).Info("no metadata available yet")
		return controllerutils.PlanResult{}
	}

	// Build templates from recommended deployments
	for _, deployment := range metadata.Model.RecommendedDeployments {
		template := buildServiceTemplate(model, deployment)
		planResult.Apply(template)
	}

	return planResult
}

// ============================================================================
// STATUS
// ============================================================================

func (r *ClusterModelReconciler) DecorateStatus(
	status *aimv1alpha1.AIMModelStatus,
	cm *controllerutils.ConditionManager,
	obs ClusterModelObservation,
) {
	decorateModelStatus(status, cm, &obs.model.Spec, obs.imageMetadata)
}

func (r *ModelReconciler) DecorateStatus(
	status *aimv1alpha1.AIMModelStatus,
	cm *controllerutils.ConditionManager,
	obs ModelObservation,
) {
	decorateModelStatus(status, cm, &obs.model.Spec, obs.imageMetadata)
}

// decorateModelStatus handles common status decoration for both cluster and namespace-scoped models.
func decorateModelStatus(
	status *aimv1alpha1.AIMModelStatus,
	_ *controllerutils.ConditionManager,
	spec *aimv1alpha1.AIMModelSpec,
	imageMetadataResult controllerutils.FetchResult[*aimv1alpha1.ImageMetadata],
) {
	// Set source type based on whether this is a custom model
	if IsCustomModel(spec) {
		status.SourceType = aimv1alpha1.AIMModelSourceTypeCustom
	} else {
		status.SourceType = aimv1alpha1.AIMModelSourceTypeImage
	}

	// Copy extracted imageMetadata to status (only for image-based models)
	if imageMetadataResult.OK() && imageMetadataResult.Value != nil {
		status.ImageMetadata = imageMetadataResult.Value
	}
}
