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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/aimruntimeconfig"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

const (
	templateCacheFinalizer        = constants.AimLabelDomain + "/template-cache.cleanup"
	modelCachesComponentName      = "ModelCaches"
	modelCachesReadyConditionType = modelCachesComponentName + "Ready"
)

type TemplateCacheReconciler struct {
	Clientset kubernetes.Interface
	Scheme    *runtime.Scheme
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
	modelCaches            controllerutils.FetchResult[*aimv1alpha1.AIMModelCacheList]
}

func (r *TemplateCacheReconciler) FetchRemoteState(
	ctx context.Context,
	c client.Client,
	reconcileCtx controllerutils.ReconcileContext[*aimv1alpha1.AIMTemplateCache],
) TemplateCacheFetchResult {
	templateCache := reconcileCtx.Object

	result := TemplateCacheFetchResult{
		templateCache:       templateCache,
		mergedRuntimeConfig: reconcileCtx.MergedRuntimeConfig,
	}

	result.serviceTemplate = controllerutils.Fetch(ctx, c, client.ObjectKey{Name: templateCache.Spec.TemplateName, Namespace: templateCache.Namespace}, &aimv1alpha1.AIMServiceTemplate{})

	if result.serviceTemplate.IsNotFound() {
		clusterServiceTemplate := controllerutils.Fetch(ctx, c, client.ObjectKey{Name: templateCache.Spec.TemplateName}, &aimv1alpha1.AIMClusterServiceTemplate{})
		result.clusterServiceTemplate = &clusterServiceTemplate
	}

	// Fetch all model caches in the namespace
	result.modelCaches = controllerutils.FetchList(ctx, c, &aimv1alpha1.AIMModelCacheList{}, client.InNamespace(templateCache.Namespace))

	return result
}

// GetComponentHealth returns the health of all dependencies for status computation.
func (result TemplateCacheFetchResult) GetComponentHealth() []controllerutils.ComponentHealth {
	// Runtime config is an upstream dependency
	health := []controllerutils.ComponentHealth{
		result.mergedRuntimeConfig.ToUpstreamComponentHealth("RuntimeConfig", aimruntimeconfig.GetRuntimeConfigHealth),
	}

	// Add service template health
	// Templates are upstream dependencies - this controller depends on them
	// If namespace-scoped template was resolved or had a non-NotFound error, use it
	// Otherwise check cluster-scoped template
	if result.serviceTemplate.OK() || (result.serviceTemplate.Error != nil && !result.serviceTemplate.IsNotFound()) {
		health = append(health, result.serviceTemplate.ToUpstreamComponentHealth("ServiceTemplate", func(template *aimv1alpha1.AIMServiceTemplate) controllerutils.ComponentHealth {
			return getComponentHealthFromStatus(&template.Status, template.Status.Status)
		}))
	} else if result.clusterServiceTemplate != nil {
		health = append(health, result.clusterServiceTemplate.ToUpstreamComponentHealth("ServiceTemplate", func(template *aimv1alpha1.AIMClusterServiceTemplate) controllerutils.ComponentHealth {
			return getComponentHealthFromStatus(&template.Status, template.Status.Status)
		}))
	}

	// NOTE: We only report fetch errors here. The actual ModelCachesReady condition
	// is set in DecorateStatus because determining cache health requires:
	// 1. Matching caches to template ModelSources by SourceURI and StorageClass
	// 2. Finding the "best" cache for each model source
	// 3. Aggregating status across matched caches
	//
	// This matching logic is computed in ComposeState, which runs AFTER GetComponentHealth.
	// Therefore, DecorateStatus (which runs after ComposeState) handles the success case.
	if !result.modelCaches.OK() {
		health = append(health, result.modelCaches.ToDownstreamComponentHealth(modelCachesComponentName, func(list *aimv1alpha1.AIMModelCacheList) controllerutils.ComponentHealth {
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
	BestModelCaches    map[string]aimv1alpha1.AIMModelCache
}

// GetComponentHealth overrides the embedded FetchResult's method to include model cache health.
// This is necessary because cache matching happens in ComposeState, which runs after FetchRemoteState.
func (obs TemplateCacheObservation) GetComponentHealth() []controllerutils.ComponentHealth {
	// Start with the base health from the embedded FetchResult
	health := obs.TemplateCacheFetchResult.GetComponentHealth()

	// Report model cache health if we have matched caches (computed in ComposeState)
	if len(obs.BestModelCaches) > 0 {
		// Find the worst status among all caches
		worstStatus := constants.AIMStatusReady
		for _, mc := range obs.BestModelCaches {
			if constants.CompareAIMStatus(mc.Status.Status, worstStatus) < 0 {
				worstStatus = mc.Status.Status
			}
		}

		health = append(health, controllerutils.ComponentHealth{
			Component:      modelCachesComponentName,
			State:          worstStatus,
			DependencyType: controllerutils.DependencyTypeDownstream,
		})
	} else if len(obs.MissingCaches) > 0 {
		// Caches are being created
		health = append(health, controllerutils.ComponentHealth{
			Component:      modelCachesComponentName,
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
	if fetch.serviceTemplate.OK() {
		templateModelSources = fetch.serviceTemplate.Value.Status.ModelSources
	} else if fetch.clusterServiceTemplate.OK() {
		templateModelSources = fetch.clusterServiceTemplate.Value.Status.ModelSources
	} else {
		return obs
	}

	obs.BestModelCaches = map[string]aimv1alpha1.AIMModelCache{}

	logger.Info("ComposeState: checking model caches",
		"templateCache", tc.Name,
		"templateModelSources", len(templateModelSources),
		"fetchedModelCaches", len(fetch.modelCaches.Value.Items))

	// Loop through model sources from the template and check with what's available in our namespace
	for _, model := range templateModelSources {
		found := false
		bestStatusModelCache := aimv1alpha1.AIMModelCache{}
		for _, cached := range fetch.modelCaches.Value.Items {
			logger.Info("ComposeState: evaluating model cache",
				"cacheName", cached.Name,
				"cacheStatus", cached.Status.Status,
				"cacheSourceURI", cached.Spec.SourceURI,
				"modelSourceURI", model.SourceURI)

			if cached.Status.Status == "" {
				logger.Info("ComposeState: skipping cache with empty status", "cacheName", cached.Name)
				continue
			}
			// ModelCache is a match if it has the same SourceURI and a StorageClass matching our config
			if cached.Spec.SourceURI == model.SourceURI &&
				(tc.Spec.StorageClassName == "" || tc.Spec.StorageClassName == cached.Spec.StorageClassName) {
				// Select the first matching cache, or replace with a better one
				// Note: !found is needed because CompareAIMStatus("", "Failed") returns 0 (equal),
				// since empty string gets priority 0 from the map (same as Failed)
				if !found || constants.CompareAIMStatus(bestStatusModelCache.Status.Status, cached.Status.Status) < 0 {
					logger.Info("ComposeState: selected cache as best match",
						"cacheName", cached.Name,
						"cacheStatus", cached.Status.Status,
						"previousBestStatus", bestStatusModelCache.Status.Status)
					found = true
					bestStatusModelCache = cached
				}
			}
		}
		if found {
			logger.Info("ComposeState: model source matched",
				"modelID", model.ModelID,
				"bestCacheName", bestStatusModelCache.Name,
				"bestCacheStatus", bestStatusModelCache.Status.Status)
			obs.BestModelCaches[model.ModelID] = bestStatusModelCache
		} else {
			logger.Info("ComposeState: model source missing cache", "modelID", model.ModelID)
			obs.MissingCaches = append(obs.MissingCaches, model)
		}
	}

	return obs
}

func (r *TemplateCacheReconciler) PlanResources(
	ctx context.Context,
	reconcileCtx controllerutils.ReconcileContext[*aimv1alpha1.AIMTemplateCache],
	obs TemplateCacheObservation,
) controllerutils.PlanResult {
	tc := reconcileCtx.Object
	result := controllerutils.PlanResult{}

	for idx, cache := range obs.MissingCaches {
		// Sanitize the model name for use as a Kubernetes resource name
		// The original model name (with capitalization) is preserved in SourceURI for matching
		// Replace dots with dashes first to ensure DNS-compliant names (dots cause warnings in Pod names)
		nameWithoutDots := strings.ReplaceAll(cache.SourceURI, ".", "-")

		modelCacheName, _ := utils.GenerateDerivedName([]string{nameWithoutDots},
			// Include all the fields that can impact the model cache uniqueness
			// TODO verify for any side effects
			utils.WithHashSource(cache.SourceURI, tc.Spec.Env, tc.Spec.StorageClassName),
		)
		// sanitizedName := utils.MakeRFC1123Compliant(nameWithoutDots)

		// Sanitize template cache name for label value
		templateCacheLabelValue, _ := utils.SanitizeLabelValue(tc.Name)

		mc := &aimv1alpha1.AIMModelCache{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "aimv1alpha1",
				Kind:       "AIMModelCache",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      modelCacheName,
				Namespace: tc.Namespace,
				Labels: map[string]string{
					"template-created":                           "true", // Backward compatibility  TODO is this still needed?
					constants.AimLabelDomain + "/template.name":  tc.Spec.TemplateName,
					constants.AimLabelDomain + "/template.scope": string(tc.Spec.TemplateScope),
					constants.AimLabelDomain + "/template.index": strconv.Itoa(idx),
					constants.LabelTemplateCacheName:             templateCacheLabelValue,
				},
			},
			Spec: aimv1alpha1.AIMModelCacheSpec{
				StorageClassName: tc.Spec.StorageClassName,
				SourceURI:        cache.SourceURI,
				ModelID:          cache.ModelID,
				Size:             *cache.Size,
				// Merge base-level env with per-source env (source takes precedence)
				Env:              utils.MergeEnvVars(tc.Spec.Env, cache.Env),
				RuntimeConfigRef: tc.Spec.RuntimeConfigRef,
			},
		}
		// Use ApplyWithoutOwnerRef so model caches can be shared across template caches
		// and outlive the creating template cache (if Ready)
		result.ApplyWithoutOwnerRef(mc)
	}
	return result
}

// DecorateStatus implements StatusDecorator to populate status fields and set domain-specific conditions.
// The framework will set the overall Ready condition after this runs, based on all conditions.
//
// NOTE: This method sets the ModelCachesReady condition (rather than GetComponentHealth) because
// cache health depends on matching logic computed in ComposeState - specifically which caches
// match the template's ModelSources. See GetComponentHealth for more context.
func (r *TemplateCacheReconciler) DecorateStatus(
	status *aimv1alpha1.AIMTemplateCacheStatus,
	cm *controllerutils.ConditionManager,
	obs TemplateCacheObservation,
) {
	// If we have any missing caches, mark the condition and return
	if len(obs.MissingCaches) > 0 {
		cm.MarkFalse(modelCachesReadyConditionType, "CreatingCaches", "Waiting for the AIM model caches to be created")
		return
	}
	if len(obs.BestModelCaches) > 0 {
		// Find the worst status among all caches
		var statusValues []constants.AIMStatus
		for _, mc := range obs.BestModelCaches {
			statusValues = append(statusValues, mc.Status.Status)
		}
		worstCacheStatus := slices.MinFunc(statusValues, constants.CompareAIMStatus)

		if worstCacheStatus == constants.AIMStatusReady {
			cm.MarkTrue(modelCachesReadyConditionType, "AllCachesReady", "All caches are ready")
		} else {
			// Find the cache with the worst status and propagate its Ready condition
			var worstCache *aimv1alpha1.AIMModelCache
			for _, mc := range obs.BestModelCaches {
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
						cm.MarkFalse(modelCachesReadyConditionType, cond.Reason, "One or more caches are not ready: "+cond.Message)
						break
					}
				}
			}

			// Fallback if no Ready condition found
			if cm.Get(modelCachesReadyConditionType) == nil {
				cm.MarkFalse(modelCachesReadyConditionType, "CachesNotReady", "One or more caches are not ready")
			}
		}
	} else {
		// Shouldn't reach this, but just in case
		cm.MarkFalse(modelCachesReadyConditionType, "NoCaches", "No model caches to track", controllerutils.AsError())
	}

	// Populate the ModelCaches status field with details about resolved caches
	if len(obs.BestModelCaches) > 0 {
		status.ModelCaches = make(map[string]aimv1alpha1.AIMResolvedModelCache, len(obs.BestModelCaches))
		for modelName, mc := range obs.BestModelCaches {
			status.ModelCaches[mc.Name] = aimv1alpha1.AIMResolvedModelCache{
				UID:                   string(mc.UID),
				Name:                  mc.Name,
				Model:                 modelName,
				Status:                mc.Status.Status,
				PersistentVolumeClaim: mc.Status.PersistentVolumeClaim,
			}
		}
	} else {
		status.ModelCaches = nil
	}
}
