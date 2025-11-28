/*
MIT License

Copyright (c) 2025 Advanced Micro Devices, Inc.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package aimmodelcache

import (
	"context"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/aimruntimeconfig"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ============================================================================
// DOMAIN RECONCILER
// ============================================================================

// Reconciler implements the domain reconciliation logic for AIMModelCache.
type Reconciler struct {
	Scheme    *runtime.Scheme
	Clientset kubernetes.Interface
}

// ============================================================================
// FETCH PHASE
// ============================================================================

// FetchResult aggregates all fetched resources for an AIMModelCache.
type FetchResult struct {
	RuntimeConfig aimruntimeconfig.RuntimeConfigFetchResult
	PVC           PVCFetchResult
	StorageClass  StorageClassFetchResult
	Job           JobFetchResult
}

// Fetch retrieves all external dependencies for an AIMModelCache.
func (r *Reconciler) Fetch(
	ctx context.Context,
	c client.Client,
	cache *aimv1alpha1.AIMModelCache,
) (FetchResult, error) {
	result := FetchResult{}

	// Fetch PVC
	pvcResult, err := fetchPVC(ctx, c, cache)
	if err != nil {
		return result, err
	}
	result.PVC = pvcResult

	// Fetch StorageClass (only if PVC exists and has a storage class)
	if pvcResult.Error == nil && pvcResult.PVC.Spec.StorageClassName != nil && *pvcResult.PVC.Spec.StorageClassName != "" {
		scResult, err := fetchStorageClass(ctx, c, pvcResult.PVC)
		if err != nil {
			return result, err
		}
		result.StorageClass = scResult
	}

	// Fetch Job
	jobResult, err := fetchJob(ctx, c, cache)
	if err != nil {
		return result, err
	}
	result.Job = jobResult

	// Fetch RuntimeConfig
	rcResult, err := aimruntimeconfig.FetchRuntimeConfig(ctx, c, cache.Spec.RuntimeConfigName, cache.Namespace)
	if err != nil {
		return result, err
	}
	result.RuntimeConfig = rcResult

	return result, nil
}

// ============================================================================
// OBSERVE PHASE
// ============================================================================

// Observation holds all observed state for an AIMModelCache.
type Observation struct {
	RuntimeConfig aimruntimeconfig.RuntimeConfigObservation
	PVC           PVCObservation
	StorageClass  StorageClassObservation
	Job           JobObservation
}

// Observe builds observation from fetched data.
func (r *Reconciler) Observe(
	ctx context.Context,
	cache *aimv1alpha1.AIMModelCache,
	fetchResult FetchResult,
) (Observation, error) {
	obs := Observation{}

	// Observe PVC subdomain
	obs.PVC = observePVC(fetchResult.PVC)

	// Observe StorageClass subdomain
	obs.StorageClass = observeStorageClass(fetchResult.StorageClass)

	// Observe Job subdomain
	obs.Job = observeJob(fetchResult.Job)

	// Observe RuntimeConfig subdomain
	obs.RuntimeConfig = aimruntimeconfig.ObserveRuntimeConfig(fetchResult.RuntimeConfig, cache.Spec.RuntimeConfigName)

	return obs, nil
}

// ============================================================================
// PLAN PHASE
// ============================================================================

// Plan determines what Kubernetes objects should be created or updated
// based on the current observation.
func (r *Reconciler) Plan(ctx context.Context, cache *aimv1alpha1.AIMModelCache, obs Observation) ([]client.Object, error) {
	var objects []client.Object

	// Plan PVC
	if pvcObj := planPVC(cache, obs, r.Scheme); pvcObj != nil {
		objects = append(objects, pvcObj)
	}

	// Plan Job
	if jobObj := planJob(cache, obs, r.Scheme); jobObj != nil {
		objects = append(objects, jobObj)
	}

	return objects, nil
}

// ============================================================================
// PROJECT PHASE
// ============================================================================

// Project updates the cache status based on observations.
func (r *Reconciler) Project(status *aimv1alpha1.AIMModelCacheStatus, cm *controllerutils.ConditionManager, obs Observation) {
	if status == nil {
		return
	}

	// Project PVC reference
	projectPVC(status, obs)

	// Project conditions
	canCreate := canCreateJob(obs)
	projectStorageReadyCondition(cm, obs)
	projectReadyCondition(cm, obs, canCreate)
	projectProgressingCondition(cm, obs, canCreate)
	projectFailureCondition(cm, obs)

	// Project overall status
	projectOverallStatus(status, obs, canCreate)
}

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================

// pvcNameForCache generates the PVC name for a model cache.
func pvcNameForCache(cache *aimv1alpha1.AIMModelCache) string {
	name, _ := utils.GenerateDerivedName([]string{cache.Name, "cache"})
	return name
}

// jobNameForCache generates the job name for a model cache.
func jobNameForCache(cache *aimv1alpha1.AIMModelCache) string {
	name, _ := utils.GenerateDerivedName([]string{cache.Name, "cache-download"})
	return name
}

// extractModelFromSourceURI extracts the model name from a sourceURI.
// Examples:
//   - "hf://amd/Llama-3.1-8B-Instruct" → "amd/Llama-3.1-8B-Instruct"
//   - "s3://bucket/model-v1" → "bucket/model-v1"
func extractModelFromSourceURI(sourceURI string) string {
	// Remove the scheme prefix (hf://, s3://, etc.)
	for i := 0; i < len(sourceURI)-2; i++ {
		if sourceURI[i] == ':' && sourceURI[i+1] == '/' && sourceURI[i+2] == '/' {
			return sourceURI[i+3:]
		}
	}
	return sourceURI
}
