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
}

func (r *AIMKVCacheReconciler) ComposeState(
	_ context.Context,
	_ controllerutils.ReconcileContext[*aimv1alpha1.AIMKVCache],
	fetch FetchResult,
) Observation {
	return Observation{FetchResult: fetch}
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

	desiredService := r.buildRedisService(kvc)
	if desiredService != nil {
		planResult.Apply(desiredService)
	}

	desiredStatefulSet := r.buildRedisStatefulSet(kvc)
	if desiredStatefulSet != nil {
		planResult.Apply(desiredStatefulSet)
	}

	return planResult
}
