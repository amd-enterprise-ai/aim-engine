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

package aimtemplatecache

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/aimruntimeconfig"
)

const (
	templateCacheFinalizer      = constants.AimLabelDomain + "/template-cache.cleanup"
	artifactsComponentName      = "Artifacts"
	artifactsReadyConditionType = artifactsComponentName + "Ready"

	// artifactTerminatingReason distinguishes "waiting for a delete to finish" from
	// CreatingCaches' "not created yet".
	artifactTerminatingReason = "ArtifactTerminating"
)

type TemplateCacheReconciler struct {
	Clientset kubernetes.Interface
	Scheme    *runtime.Scheme
}

func (r *TemplateCacheReconciler) GetApplyOptions(obs TemplateCacheObservation) controllerutils.ApplyOptions {
	return aimruntimeconfig.GetApplyOptions(obs.mergedRuntimeConfig.Value)
}

// getComponentHealthFromStatus derives component health from any status with conditions and an overall status.
// It extracts the Ready condition and uses the status's overall state for more specific mapping.
func getComponentHealthFromStatus[S interface {
	GetConditions() []metav1.Condition
	*T
}, T any](statusPtr S, overallStatus constants.AIMStatus) controllerutils.ComponentHealth {
	conditions := statusPtr.GetConditions()

	// Find the Ready condition
	for _, cond := range conditions {
		if cond.Type == controllerutils.ConditionTypeReady {
			// Map condition status to AIMStatus
			var state constants.AIMStatus
			switch cond.Status {
			case metav1.ConditionTrue:
				state = constants.AIMStatusReady
			case metav1.ConditionFalse:
				// Use the overall status for more specific state
				if overallStatus != "" {
					state = overallStatus
				} else {
					state = constants.AIMStatusFailed
				}
			case metav1.ConditionUnknown:
				state = constants.AIMStatusProgressing
			default:
				state = constants.AIMStatusProgressing
			}

			return controllerutils.ComponentHealth{
				State:   state,
				Reason:  cond.Reason,
				Message: cond.Message,
			}
		}
	}

	// Fallback if no Ready condition found - use overall status
	return controllerutils.ComponentHealth{
		State:   overallStatus,
		Reason:  "ResourceFound",
		Message: "Resource found but no Ready condition present",
	}
}

type TemplateCacheFetchResult struct {
	templateCache *aimv1alpha1.AIMTemplateCache

	mergedRuntimeConfig    controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]
	serviceTemplate        controllerutils.FetchResult[*aimv1alpha1.AIMServiceTemplate]
	clusterServiceTemplate *controllerutils.FetchResult[*aimv1alpha1.AIMClusterServiceTemplate]
	artifacts              controllerutils.FetchResult[*aimv1alpha1.AIMArtifactList]
}

func (r *TemplateCacheReconciler) FetchRemoteState(
	ctx context.Context,
	c client.Client,
	reconcileCtx controllerutils.ReconcileContext[*aimv1alpha1.AIMTemplateCache],
) TemplateCacheFetchResult {
	templateCache := reconcileCtx.Object

	runtimeConfigRef := templateCache.GetRuntimeConfigRef()
	result := TemplateCacheFetchResult{
		templateCache:       templateCache,
		mergedRuntimeConfig: aimruntimeconfig.FetchMergedRuntimeConfig(ctx, c, runtimeConfigRef.Name, templateCache.Namespace),
	}

	// Fetch template based on the specified scope
	switch templateCache.Spec.TemplateScope {
	case aimv1alpha1.AIMServiceTemplateScopeCluster:
		// Only look for cluster-scoped template
		clusterServiceTemplate := controllerutils.Fetch(ctx, c, client.ObjectKey{Name: templateCache.Spec.TemplateName}, &aimv1alpha1.AIMClusterServiceTemplate{})
		result.clusterServiceTemplate = &clusterServiceTemplate
	case aimv1alpha1.AIMServiceTemplateScopeNamespace:
		// Only look for namespace-scoped template
		result.serviceTemplate = controllerutils.Fetch(ctx, c, client.ObjectKey{Name: templateCache.Spec.TemplateName, Namespace: templateCache.Namespace}, &aimv1alpha1.AIMServiceTemplate{})
	default:
		// For unknown or unset scope, try namespace first then cluster (backwards compatible behavior)
		result.serviceTemplate = controllerutils.Fetch(ctx, c, client.ObjectKey{Name: templateCache.Spec.TemplateName, Namespace: templateCache.Namespace}, &aimv1alpha1.AIMServiceTemplate{})
		if result.serviceTemplate.IsNotFound() {
			clusterServiceTemplate := controllerutils.Fetch(ctx, c, client.ObjectKey{Name: templateCache.Spec.TemplateName}, &aimv1alpha1.AIMClusterServiceTemplate{})
			result.clusterServiceTemplate = &clusterServiceTemplate
		}
	}

	// Fetch all artifacts in the namespace
	result.artifacts = controllerutils.FetchList(ctx, c, &aimv1alpha1.AIMArtifactList{}, client.InNamespace(templateCache.Namespace))

	return result
}

// GetComponentHealth returns the health of all dependencies for status computation.
func (result TemplateCacheFetchResult) GetComponentHealth() []controllerutils.ComponentHealth {
	// Runtime config is an upstream dependency
	health := []controllerutils.ComponentHealth{
		result.mergedRuntimeConfig.ToUpstreamComponentHealth("RuntimeConfig", aimruntimeconfig.GetRuntimeConfigHealth),
	}

	// Add service template health based on which template was fetched
	// Templates are upstream dependencies - this controller depends on them
	// Only one of serviceTemplate or clusterServiceTemplate will be set based on the scope
	if result.clusterServiceTemplate != nil {
		// Cluster scope was requested - check cluster template
		health = append(health, result.clusterServiceTemplate.ToUpstreamComponentHealth("ServiceTemplate", func(template *aimv1alpha1.AIMClusterServiceTemplate) controllerutils.ComponentHealth {
			return getComponentHealthFromStatus(&template.Status, template.Status.Status)
		}))
	} else if result.serviceTemplate.Value != nil || result.serviceTemplate.Error != nil {
		// Namespace scope was requested (or default fallback) - check namespace template
		health = append(health, result.serviceTemplate.ToUpstreamComponentHealth("ServiceTemplate", func(template *aimv1alpha1.AIMServiceTemplate) controllerutils.ComponentHealth {
			return getComponentHealthFromStatus(&template.Status, template.Status.Status)
		}))
	}

	// NOTE: We only report fetch errors here. The actual ArtifactsReady condition
	// is set in DecorateStatus because determining cache health requires:
	// 1. Matching caches to template ModelSources by SourceURI and StorageClass
	// 2. Finding the "best" cache for each model source
	// 3. Aggregating status across matched caches
	//
	// This matching logic is computed in ComposeState, which runs AFTER GetComponentHealth.
	// Therefore, DecorateStatus (which runs after ComposeState) handles the success case.
	if !result.artifacts.OK() {
		health = append(health, result.artifacts.ToDownstreamComponentHealth(artifactsComponentName, func(list *aimv1alpha1.AIMArtifactList) controllerutils.ComponentHealth {
			// This should not get called, as there was an error
			return controllerutils.ComponentHealth{}
		}))
	}

	return health
}

// Observe (thin wrapper for now, may be removed later)

type TemplateCacheObservation struct {
	TemplateCacheFetchResult

	AllCachesAvailable bool
	MissingCaches      []aimv1alpha1.AIMModelSource
	BestArtifacts      map[string]aimv1alpha1.AIMArtifact

	// TerminatingArtifactNames lists artifacts this cache would have adopted but that
	// are being deleted. Held apart from both BestArtifacts and MissingCaches: their PVC
	// is about to be collected so they cannot back a Ready cache, and in Shared mode a
	// replacement hashes to the same name, so recreating one before the delete completes
	// would patch the dying object instead.
	TerminatingArtifactNames []string
}

// GetComponentHealth overrides the embedded FetchResult's method to include artifact health.
// This is necessary because cache matching happens in ComposeState, which runs after FetchRemoteState.
func (obs TemplateCacheObservation) GetComponentHealth() []controllerutils.ComponentHealth {
	// Start with the base health from the embedded FetchResult
	health := obs.TemplateCacheFetchResult.GetComponentHealth()

	// Report artifact health if we have matched caches (computed in ComposeState)
	if len(obs.BestArtifacts) > 0 {
		// Find the worst status among all caches
		worstStatus := constants.AIMStatusReady
		for _, mc := range obs.BestArtifacts {
			if constants.CompareAIMStatus(mc.Status.Status, worstStatus) < 0 {
				worstStatus = mc.Status.Status
			}
		}

		health = append(health, controllerutils.ComponentHealth{
			Component:      artifactsComponentName,
			State:          worstStatus,
			DependencyType: controllerutils.DependencyTypeDownstream,
		})
	} else if len(obs.MissingCaches) > 0 || len(obs.TerminatingArtifactNames) > 0 {
		// Caches are being created, or are waiting for a delete to finish first
		health = append(health, controllerutils.ComponentHealth{
			Component:      artifactsComponentName,
			State:          constants.AIMStatusProgressing,
			DependencyType: controllerutils.DependencyTypeDownstream,
		})
	}

	return health
}

func (r *TemplateCacheReconciler) ComposeState(
	ctx context.Context,
	reconcileCtx controllerutils.ReconcileContext[*aimv1alpha1.AIMTemplateCache],
	fetch TemplateCacheFetchResult,
) TemplateCacheObservation {
	logger := log.FromContext(ctx)
	obs := TemplateCacheObservation{
		TemplateCacheFetchResult: fetch,
	}

	var templateModelSources []aimv1alpha1.AIMModelSource
	tc := reconcileCtx.Object

	// Read model sources from Status (populated by discovery), not Spec
	// Check Value != nil because when templateScope is Cluster, serviceTemplate is not fetched
	// and has zero value (Error=nil, Value=nil), so OK() returns true but Value is nil
	if fetch.serviceTemplate.OK() && fetch.serviceTemplate.Value != nil {
		templateModelSources = fetch.serviceTemplate.Value.Status.ModelSources
	} else if fetch.clusterServiceTemplate != nil && fetch.clusterServiceTemplate.OK() && fetch.clusterServiceTemplate.Value != nil {
		templateModelSources = fetch.clusterServiceTemplate.Value.Status.ModelSources
	} else {
		return obs
	}

	obs.BestArtifacts = map[string]aimv1alpha1.AIMArtifact{}

	logger.V(1).Info("ComposeState: checking artifacts",
		"templateCache", tc.Name,
		"templateModelSources", len(templateModelSources),
		"fetchedArtifacts", len(fetch.artifacts.Value.Items))

	// Loop through model sources from the template and check with what's available in our namespace
	for _, model := range templateModelSources {
		match := matchArtifactForSource(ctx, tc, fetch.artifacts.Value.Items, model)
		switch {
		case match.found:
			logger.V(1).Info("ComposeState: model source matched",
				"modelID", model.ModelID,
				"bestCacheName", match.best.Name,
				"bestCacheStatus", match.best.Status.Status)
			obs.BestArtifacts[model.ModelID] = match.best
		case len(match.terminatingNames) > 0:
			logger.V(1).Info("ComposeState: model source served only by terminating artifacts",
				"modelID", model.ModelID,
				"terminatingArtifacts", match.terminatingNames)
			obs.TerminatingArtifactNames = append(obs.TerminatingArtifactNames, match.terminatingNames...)
		default:
			logger.V(1).Info("ComposeState: model source missing cache", "modelID", model.ModelID)
			obs.MissingCaches = append(obs.MissingCaches, model)
		}
	}

	return obs
}

// artifactMatch is the outcome of resolving one model source against the artifacts in
// the namespace.
type artifactMatch struct {
	best  aimv1alpha1.AIMArtifact
	found bool

	// terminatingNames holds artifacts that serve the model source and would be
	// adoptable were they not being deleted.
	terminatingNames []string
}

// matchArtifactForSource picks the artifact this cache should resolve modelSource to:
// the adoptable candidate with the best status. A terminating candidate is reported
// separately rather than adopted or treated as missing - see TerminatingArtifactNames.
func matchArtifactForSource(
	ctx context.Context,
	templateCache *aimv1alpha1.AIMTemplateCache,
	artifacts []aimv1alpha1.AIMArtifact,
	modelSource aimv1alpha1.AIMModelSource,
) artifactMatch {
	logger := log.FromContext(ctx)
	var match artifactMatch

	for _, cached := range artifacts {
		logger.V(1).Info("ComposeState: evaluating artifact",
			"cacheName", cached.Name,
			"cacheStatus", cached.Status.Status,
			"cacheSourceURI", cached.Spec.SourceURI,
			"modelSourceURI", modelSource.SourceURI)

		if !ArtifactAdoptableBy(templateCache, &cached) {
			continue
		}

		// Artifact is a match if it serves the model source we are resolving
		if cached.Spec.SourceURI != modelSource.SourceURI {
			continue
		}

		if !s3RuntimeConfigMatches(
			modelSource.SourceURI,
			cached.Spec.RuntimeConfigRef,
			templateCache.Spec.RuntimeConfigRef,
		) {
			continue
		}

		// Must precede the empty-status guard: an artifact deleted before its first
		// status still has to be waited out, and an apply over a deletionTimestamp is
		// accepted silently, so classifying it as missing writes to the dying object.
		if cached.DeletionTimestamp != nil {
			logger.V(1).Info("ComposeState: skipping terminating artifact", "cacheName", cached.Name)
			match.terminatingNames = append(match.terminatingNames, cached.Name)
			continue
		}

		if cached.Status.Status == "" {
			logger.V(1).Info("ComposeState: skipping cache with empty status", "cacheName", cached.Name)
			continue
		}

		// Select the first matching cache, or replace with a better one
		// Note: !found is needed because CompareAIMStatus("", "Failed") returns 0 (equal),
		// since empty string gets priority 0 from the map (same as Failed)
		if !match.found || constants.CompareAIMStatus(match.best.Status.Status, cached.Status.Status) < 0 {
			logger.V(1).Info("ComposeState: selected cache as best match",
				"cacheName", cached.Name,
				"cacheStatus", cached.Status.Status,
				"previousBestStatus", match.best.Status.Status)
			match.found = true
			match.best = cached
		}
	}

	return match
}

func (r *TemplateCacheReconciler) PlanResources(
	ctx context.Context,
	reconcileCtx controllerutils.ReconcileContext[*aimv1alpha1.AIMTemplateCache],
	obs TemplateCacheObservation,
) controllerutils.PlanResult {
	tc := reconcileCtx.Object
	result := controllerutils.PlanResult{}

	for idx, cache := range obs.MissingCaches {
		artifactName, _ := generateArtifactName(tc, cache)

		// Sanitize template cache name for label value
		templateCacheLabelValue, _ := utils.SanitizeLabelValue(tc.Name)

		mc := &aimv1alpha1.AIMArtifact{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "aimv1alpha1",
				Kind:       "AIMArtifact",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      artifactName,
				Namespace: tc.Namespace,
				Labels: map[string]string{
					"template-created":                           "true", // Backward compatibility  TODO is this still needed?
					constants.AimLabelDomain + "/template.name":  tc.Spec.TemplateName,
					constants.AimLabelDomain + "/template.scope": string(tc.Spec.TemplateScope),
					constants.AimLabelDomain + "/template.index": strconv.Itoa(idx),
					constants.LabelTemplateCacheName:             templateCacheLabelValue,
				},
			},
			Spec: aimv1alpha1.AIMArtifactSpec{
				StorageClassName: tc.Spec.StorageClassName,
				SourceURI:        cache.SourceURI,
				ModelID:          cache.ModelID,
				Size:             getSizeOrZero(cache.Size),
				// Merge base-level env with per-source env (source takes precedence)
				Env:              utils.MergeEnvVars(tc.Spec.Env, cache.Env),
				RuntimeConfigRef: tc.Spec.RuntimeConfigRef,
			},
		}

		// Apply based on template cache mode:
		// - Dedicated: artifacts are owned by this template cache (garbage collected with it)
		// - Shared: artifacts have no owner references (persist independently)
		if tc.Spec.Mode == aimv1alpha1.TemplateCacheModeDedicated {
			result.Apply(mc)
		} else {
			result.ApplyWithoutOwnerRef(mc)
		}
	}

	return result
}

// generateArtifactName returns a deterministic artifact name.
// Shared caches keep cross-cache reuse behavior by not scoping names to template cache.
// Dedicated caches scope names to template cache so dedicated/shared artifacts can coexist.
func generateArtifactName(tc *aimv1alpha1.AIMTemplateCache, modelSource aimv1alpha1.AIMModelSource) (string, error) {
	// Replace dots with dashes first to ensure DNS-compliant names (dots cause warnings in Pod names)
	nameWithoutDots := strings.ReplaceAll(modelSource.SourceURI, ".", "-")
	hashInputs := []any{
		modelSource.SourceURI,
		utils.MergeEnvVars(tc.Spec.Env, modelSource.Env),
		tc.Spec.StorageClassName,
	}
	if strings.HasPrefix(modelSource.SourceURI, "s3://") {
		hashInputs = append(hashInputs, normalizedRuntimeConfigName(tc.Spec.RuntimeConfigRef))
	}

	if tc.Spec.Mode == aimv1alpha1.TemplateCacheModeDedicated {
		hashInputs = append(hashInputs, "dedicated", tc.Name)
	}

	return utils.GenerateDerivedName(
		[]string{nameWithoutDots},
		utils.WithHashSource(hashInputs...),
		utils.WithHashLength(10),
	)
}

// ArtifactAdoptableBy reports whether templateCache is allowed to use artifact,
// judged only on the cache's mode and storage class. Callers that resolve a
// specific model source must also compare SourceURI; the artifact watch cannot,
// because the model sources live on the (separately fetched) template.
//
// Deliberately ignores deletionTimestamp. The artifact watch filters events through
// this predicate, so rejecting a terminating artifact here would drop the very wakeups
// that tell a cache to stop advertising it. That rejection is matchArtifactForSource's
// job instead.
func ArtifactAdoptableBy(templateCache *aimv1alpha1.AIMTemplateCache, artifact *aimv1alpha1.AIMArtifact) bool {
	// Enforce mode isolation:
	// - Shared template caches can use only shared artifacts (no owner refs)
	// - Dedicated template caches can use only artifacts owned by this template cache
	switch templateCache.Spec.Mode {
	case aimv1alpha1.TemplateCacheModeShared:
		if len(artifact.GetOwnerReferences()) > 0 {
			return false
		}
	case aimv1alpha1.TemplateCacheModeDedicated:
		if !hasOwnerReferenceUID(artifact.GetOwnerReferences(), templateCache.UID) {
			return false
		}
	}

	return templateCache.Spec.StorageClassName == "" ||
		templateCache.Spec.StorageClassName == artifact.Spec.StorageClassName
}

func s3RuntimeConfigMatches(
	sourceURI string,
	left aimv1alpha1.RuntimeConfigRef,
	right aimv1alpha1.RuntimeConfigRef,
) bool {
	return !strings.HasPrefix(sourceURI, "s3://") ||
		normalizedRuntimeConfigName(left) == normalizedRuntimeConfigName(right)
}

func normalizedRuntimeConfigName(ref aimv1alpha1.RuntimeConfigRef) string {
	if ref.Name == "" {
		return aimruntimeconfig.DefaultRuntimeConfigName
	}
	return ref.Name
}

func hasOwnerReferenceUID(ownerRefs []metav1.OwnerReference, ownerUID types.UID) bool {
	for _, ownerRef := range ownerRefs {
		if ownerRef.UID == ownerUID {
			return true
		}
	}
	return false
}

// DecorateStatus implements StatusDecorator to populate status fields and set domain-specific conditions.
// The framework will set the overall Ready condition after this runs, based on all conditions.
//
// NOTE: This method sets the ArtifactsReady condition (rather than GetComponentHealth) because
// cache health depends on matching logic computed in ComposeState - specifically which caches
// match the template's ModelSources. See GetComponentHealth for more context.
func (r *TemplateCacheReconciler) DecorateStatus(
	status *aimv1alpha1.AIMTemplateCacheStatus,
	cm *controllerutils.ConditionManager,
	obs TemplateCacheObservation,
) {
	controllerutils.SetTemplateMigrationRequired(cm, true)

	// Must run ahead of the early returns below: an artifact this cache advertised
	// until now has to stop being advertised, PVC included, the moment it starts
	// being deleted.
	status.Artifacts = buildResolvedArtifacts(obs.BestArtifacts)

	// Reported ahead of the missing artifacts it will turn into: "waiting for a delete"
	// is the part an operator can act on.
	if len(obs.TerminatingArtifactNames) > 0 {
		cm.MarkFalse(artifactsReadyConditionType, artifactTerminatingReason,
			"Waiting for AIM artifacts to finish deleting before recreating them: "+strings.Join(obs.TerminatingArtifactNames, ", "))
		return
	}
	// If we have any missing caches, mark the condition and return
	if len(obs.MissingCaches) > 0 {
		cm.MarkFalse(artifactsReadyConditionType, "CreatingCaches", "Waiting for the AIM artifacts to be created")
		return
	}
	if len(obs.BestArtifacts) > 0 {
		// Find the worst status among all caches
		var statusValues []constants.AIMStatus
		for _, mc := range obs.BestArtifacts {
			statusValues = append(statusValues, mc.Status.Status)
		}
		worstCacheStatus := slices.MinFunc(statusValues, constants.CompareAIMStatus)

		if worstCacheStatus == constants.AIMStatusReady {
			cm.MarkTrue(artifactsReadyConditionType, "AllCachesReady", "All caches are ready")
		} else {
			// Find the cache with the worst status and propagate its Ready condition
			var worstCache *aimv1alpha1.AIMArtifact
			for _, mc := range obs.BestArtifacts {
				if mc.Status.Status == worstCacheStatus {
					mcCopy := mc
					worstCache = &mcCopy
					break
				}
			}

			if worstCache != nil {
				// Extract the Ready condition from the worst cache
				for _, cond := range worstCache.Status.Conditions {
					if cond.Type == controllerutils.ConditionTypeReady {
						cm.MarkFalse(artifactsReadyConditionType, cond.Reason, "One or more caches are not ready: "+cond.Message)
						break
					}
				}
			}

			// Fallback if no Ready condition found
			if cm.Get(artifactsReadyConditionType) == nil {
				cm.MarkFalse(artifactsReadyConditionType, "CachesNotReady", "One or more caches are not ready")
			}
		}
	} else {
		// Shouldn't reach this, but just in case
		cm.MarkFalse(artifactsReadyConditionType, "NoCaches", "No artifacts to track", controllerutils.AsError())
	}
}

// buildResolvedArtifacts renders the resolved artifacts for status.artifacts, keyed by
// artifact name. Returns nil when nothing is resolved, so the field is cleared rather
// than left holding a previous reconcile's artifacts.
func buildResolvedArtifacts(bestArtifacts map[string]aimv1alpha1.AIMArtifact) map[string]aimv1alpha1.AIMResolvedArtifact {
	if len(bestArtifacts) == 0 {
		return nil
	}

	resolved := make(map[string]aimv1alpha1.AIMResolvedArtifact, len(bestArtifacts))
	for modelName, artifact := range bestArtifacts {
		resolved[artifact.Name] = aimv1alpha1.AIMResolvedArtifact{
			UID:                   string(artifact.UID),
			Name:                  artifact.Name,
			Model:                 modelName,
			Status:                artifact.Status.Status,
			PersistentVolumeClaim: artifact.Status.PersistentVolumeClaim,
		}
	}
	return resolved
}

// getSizeOrZero returns the size value or zero quantity if nil.
// This allows creating artifacts without a known size - the artifact
// controller will run a check-size job to discover the size.
func getSizeOrZero(size *resource.Quantity) resource.Quantity {
	if size == nil {
		return resource.Quantity{}
	}
	return *size
}
