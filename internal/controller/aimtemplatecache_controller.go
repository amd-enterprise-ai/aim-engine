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

package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/aimtemplatecache"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

// AIMTemplateCacheReconciler reconciles a AIMTemplateCache object
type AIMTemplateCacheReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Recorder  record.EventRecorder
	Clientset kubernetes.Interface

	reconciler *aimtemplatecache.Reconciler
	pipeline   controllerutils.Pipeline[
		*aimv1alpha1.AIMTemplateCache,
		*aimv1alpha1.AIMTemplateCacheStatus,
		aimtemplatecache.FetchResult,
		aimtemplatecache.Observation,
	]
}

// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimtemplatecaches,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimtemplatecaches/status,verbs=get;update;patch

const (
	templateCacheFieldOwner = "aimtemplatecache-controller"
)

func (r *AIMTemplateCacheReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch CR
	var cache aimv1alpha1.AIMTemplateCache
	if err := r.Get(ctx, req.NamespacedName, &cache); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	logger.V(1).Info("Reconciling AIMTemplateCache", "name", cache.Name, "namespace", cache.Namespace)

	if err := r.pipeline.Run(ctx, &cache); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *AIMTemplateCacheReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("aimtemplatecache-controller")

	r.reconciler = &aimtemplatecache.Reconciler{
		Scheme: r.Scheme,
	}

	r.pipeline = controllerutils.Pipeline[
		*aimv1alpha1.AIMTemplateCache,
		*aimv1alpha1.AIMTemplateCacheStatus,
		aimtemplatecache.FetchResult,
		aimtemplatecache.Observation,
	]{
		Client:       r.Client,
		StatusClient: r.Status(),
		Recorder:     r.Recorder,
		FieldOwner:   templateCacheFieldOwner,
		Reconciler:   r.reconciler,
		Scheme:       r.Scheme,
	}

	modelCacheHandler := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		modelCache, ok := obj.(*aimv1alpha1.AIMModelCache)
		if !ok {
			return nil
		}

		var templateCaches aimv1alpha1.AIMTemplateCacheList
		if err := r.List(ctx, &templateCaches,
			client.InNamespace(modelCache.Namespace),
		); err != nil {
			ctrl.LoggerFrom(ctx).Error(err, "failed to list AIMTemplateCaches for AIMModelCaches",
				"runtimeConfig", modelCache.Name, "namespace", modelCache.Namespace)
			return nil
		}

		return requestsFromTemplateCaches(templateCaches.Items)
	})

	// Watch for ServiceTemplate changes and reconcile any TemplateCaches that reference them
	serviceTemplateHandler := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		template, ok := obj.(*aimv1alpha1.AIMServiceTemplate)
		if !ok {
			return nil
		}

		// Find all TemplateCaches in the same namespace that reference this template
		var templateCaches aimv1alpha1.AIMTemplateCacheList
		if err := r.List(ctx, &templateCaches,
			client.InNamespace(template.Namespace),
		); err != nil {
			ctrl.LoggerFrom(ctx).Error(err, "failed to list AIMTemplateCaches for ServiceTemplate",
				"template", template.Name, "namespace", template.Namespace)
			return nil
		}

		var requests []reconcile.Request
		for _, tc := range templateCaches.Items {
			if tc.Spec.TemplateName == template.Name {
				requests = append(requests, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Namespace: tc.Namespace,
						Name:      tc.Name,
					},
				})
			}
		}
		return requests
	})

	// Watch for ClusterServiceTemplate changes and reconcile any TemplateCaches that reference them
	clusterServiceTemplateHandler := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		template, ok := obj.(*aimv1alpha1.AIMClusterServiceTemplate)
		if !ok {
			return nil
		}

		// Find all TemplateCaches across all namespaces that reference this cluster template
		var templateCaches aimv1alpha1.AIMTemplateCacheList
		if err := r.List(ctx, &templateCaches); err != nil {
			ctrl.LoggerFrom(ctx).Error(err, "failed to list AIMTemplateCaches for ClusterServiceTemplate",
				"template", template.Name)
			return nil
		}

		var requests []reconcile.Request
		for _, tc := range templateCaches.Items {
			if tc.Spec.TemplateName == template.Name {
				requests = append(requests, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Namespace: tc.Namespace,
						Name:      tc.Name,
					},
				})
			}
		}
		return requests
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(&aimv1alpha1.AIMTemplateCache{}).
		Watches(&aimv1alpha1.AIMModelCache{}, modelCacheHandler).
		Watches(&aimv1alpha1.AIMServiceTemplate{}, serviceTemplateHandler).
		Watches(&aimv1alpha1.AIMClusterServiceTemplate{}, clusterServiceTemplateHandler).
		WithOptions(controller.Options{MaxConcurrentReconciles: 2}).
		Named("aimtemplatecache-controller").
		Complete(r)
}

func requestsFromTemplateCaches(templateCaches []aimv1alpha1.AIMTemplateCache) []reconcile.Request {
	if len(templateCaches) == 0 {
		return nil
	}
	requests := make([]reconcile.Request, 0, len(templateCaches))
	for _, tc := range templateCaches {
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: tc.Namespace,
				Name:      tc.Name,
			},
		})
	}
	return requests
}
