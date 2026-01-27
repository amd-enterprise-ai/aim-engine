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

package aimkvcache

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

const (
	kvCacheTypeRedis         = "redis"
	kvCacheDefaultRedisImage = "redis:7.2.4"
)

type AIMKVCacheReconciler struct {
	Clientset kubernetes.Interface
	Scheme    *runtime.Scheme
}

type FetchResult struct {
	kvCache *aimv1alpha1.AIMKVCache

	statefulSet controllerutils.FetchResult[*appsv1.StatefulSet]
	service     controllerutils.FetchResult[*corev1.Service]
}

func (r *AIMKVCacheReconciler) FetchRemoteState(
	ctx context.Context,
	c client.Client,
	reconcileCtx controllerutils.ReconcileContext[*aimv1alpha1.AIMKVCache],
) FetchResult {
	kvc := reconcileCtx.Object
	ctx = log.IntoContext(ctx, log.FromContext(ctx).WithValues(
		"phase", "fetch",
		"kvCache", kvc.Name,
		"type", kvc.Spec.KVCacheType,
	))

	result := FetchResult{
		kvCache: kvc,
	}

	ssName, _ := r.GetStatefulSetName(kvc)
	svcName, _ := r.GetServiceName(kvc)

	ss := &appsv1.StatefulSet{}
	result.statefulSet = controllerutils.Fetch(ctx, c, client.ObjectKey{Namespace: kvc.Namespace, Name: ssName}, ss)

	svc := &corev1.Service{}
	result.service = controllerutils.Fetch(ctx, c, client.ObjectKey{Namespace: kvc.Namespace, Name: svcName}, svc)

	return result
}

func (result FetchResult) GetComponentHealth() []controllerutils.ComponentHealth {
	return []controllerutils.ComponentHealth{
		result.statefulSet.ToDownstreamComponentHealth("StatefulSet", controllerutils.GetStatefulSetHealth),
		result.service.ToDownstreamComponentHealth("Service", controllerutils.GetServiceHealth),
	}
}

func (r *AIMKVCacheReconciler) DecorateStatus(
	status *aimv1alpha1.AIMKVCacheStatus,
	_ *controllerutils.ConditionManager,
	obs Observation,
) {
	kvc := obs.kvCache

	// Record that we've observed this generation
	status.ObservedGeneration = obs.kvCache.Generation

	// Set resource names
	status.StatefulSetName, _ = r.GetStatefulSetName(kvc)
	status.ServiceName, _ = r.GetServiceName(kvc)

	// Set endpoint
	if svcName, _ := r.GetServiceName(kvc); svcName != "" {
		status.Endpoint = fmt.Sprintf("redis://%s.%s:6379", svcName, kvc.Namespace)
	}

	// Set replica info from StatefulSet
	if obs.statefulSet.OK() && obs.statefulSet.Value != nil {
		ss := obs.statefulSet.Value
		if ss.Spec.Replicas != nil {
			status.Replicas = *ss.Spec.Replicas
		}
		status.ReadyReplicas = ss.Status.ReadyReplicas
	}

	// Set storage size from spec
	storageSize := r.getStorageSize(kvc)
	status.StorageSize = storageSize.String()
}

type Observation struct {
	FetchResult

	// needsStatefulSetUpdate is true if the existing StatefulSet differs from desired state
	needsStatefulSetUpdate bool
	// needsServiceUpdate is true if the existing Service differs from desired state
	needsServiceUpdate bool
}

// envVarsEqual compares two slices of EnvVar, ignoring order
func envVarsEqual(desired, observed []corev1.EnvVar) bool {
	if len(desired) != len(observed) {
		return false
	}
	observedMap := make(map[string]corev1.EnvVar, len(observed))
	for _, env := range observed {
		observedMap[env.Name] = env
	}
	for _, env := range desired {
		if obs, ok := observedMap[env.Name]; !ok || !equality.Semantic.DeepEqual(env, obs) {
			return false
		}
	}
	return true
}

// statefulSetNeedsUpdate returns true if the observed StatefulSet differs from desired mutable fields
func (r *AIMKVCacheReconciler) statefulSetNeedsUpdate(kvc *aimv1alpha1.AIMKVCache, observed *appsv1.StatefulSet) bool {
	// Find the redis container
	var container *corev1.Container
	for i := range observed.Spec.Template.Spec.Containers {
		if observed.Spec.Template.Spec.Containers[i].Name == "redis" {
			container = &observed.Spec.Template.Spec.Containers[i]
			break
		}
	}
	if container == nil {
		return true
	}

	// Check image
	if container.Image != r.getImage(kvc) {
		return true
	}

	// Check env vars
	if !envVarsEqual(r.getEnv(kvc), container.Env) {
		return true
	}

	// Check resources
	if !equality.Semantic.DeepEqual(r.getResources(kvc), container.Resources) {
		return true
	}

	return false
}

// serviceNeedsUpdate returns true if the observed Service differs from desired mutable fields
func (r *AIMKVCacheReconciler) serviceNeedsUpdate(_ *aimv1alpha1.AIMKVCache, observed *corev1.Service) bool {
	// Service ports are hardcoded, check if expected port exists
	for _, port := range observed.Spec.Ports {
		if port.Port == 6379 && port.Name == "redis" {
			return false
		}
	}
	return true
}

func (r *AIMKVCacheReconciler) ComposeState(
	_ context.Context,
	_ controllerutils.ReconcileContext[*aimv1alpha1.AIMKVCache],
	fetch FetchResult,
) Observation {
	obs := Observation{FetchResult: fetch}
	kvc := fetch.kvCache

	// Check if StatefulSet needs update
	if fetch.statefulSet.OK() && fetch.statefulSet.Value != nil {
		obs.needsStatefulSetUpdate = r.statefulSetNeedsUpdate(kvc, fetch.statefulSet.Value)
	}

	// Check if Service needs update
	if fetch.service.OK() && fetch.service.Value != nil {
		obs.needsServiceUpdate = r.serviceNeedsUpdate(kvc, fetch.service.Value)
	}

	return obs
}

func (r *AIMKVCacheReconciler) PlanResources(
	ctx context.Context,
	_ controllerutils.ReconcileContext[*aimv1alpha1.AIMKVCache],
	obs Observation,
) controllerutils.PlanResult {
	logger := log.FromContext(ctx).WithName("plan")
	kvc := obs.kvCache
	planResult := controllerutils.PlanResult{}

	if kvc.Spec.KVCacheType != kvCacheTypeRedis && kvc.Spec.KVCacheType != "" {
		logger.Error(fmt.Errorf("unsupported KVCacheType"), "Only redis is supported", "type", kvc.Spec.KVCacheType)
		return controllerutils.PlanResult{}
	}

	// Plan Service
	if obs.service.IsNotFound() || obs.needsServiceUpdate {
		if svc := r.buildRedisService(kvc); svc != nil {
			planResult.Apply(svc)
		}
	}

	// Plan StatefulSet
	if obs.statefulSet.IsNotFound() || obs.needsStatefulSetUpdate {
		if ss := r.buildRedisStatefulSet(kvc); ss != nil {
			planResult.Apply(ss)
		}
	}

	return planResult
}
