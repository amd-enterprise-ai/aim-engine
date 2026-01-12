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
