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

package aimservice

import (
	"context"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

// RegisterWatches installs the field indexers and watches that feed the
// template-based AIMService pipeline. All template/model/runtimeconfig/
// templatecache wiring lives here so the top-level controller only needs
// to compose this with the v1alpha2 equivalent.
//
// The caller passes the Manager (for field indexers), the current Builder
// (so watches can chain), and the read client used by the map funcs.
func RegisterWatches(ctx context.Context, mgr manager.Manager, b *builder.Builder, c client.Client) (*builder.Builder, error) {
	if err := mgr.GetFieldIndexer().IndexField(ctx, &aimv1alpha1.AIMService{}, aimv1alpha1.AIMServiceTemplateIndexKey, func(obj client.Object) []string {
		svc, ok := obj.(*aimv1alpha1.AIMService)
		if !ok {
			return nil
		}
		if svc.Spec.Template == nil || svc.Spec.Template.Name == "" {
			return nil
		}
		return []string{svc.Spec.Template.Name}
	}); err != nil {
		return nil, err
	}

	if err := mgr.GetFieldIndexer().IndexField(ctx, &aimv1alpha1.AIMService{}, aimv1alpha1.AIMServiceResolvedTemplateIndexKey, func(obj client.Object) []string {
		svc, ok := obj.(*aimv1alpha1.AIMService)
		if !ok {
			return nil
		}
		if svc.Status.ResolvedTemplate == nil || svc.Status.ResolvedTemplate.Name == "" {
			return nil
		}
		return []string{svc.Status.ResolvedTemplate.Name}
	}); err != nil {
		return nil, err
	}

	return b.
		Watches(&aimv1alpha1.AIMServiceTemplate{}, handler.EnqueueRequestsFromMapFunc(findServicesForTemplate(c))).
		Watches(&aimv1alpha1.AIMClusterServiceTemplate{}, handler.EnqueueRequestsFromMapFunc(findServicesForClusterTemplate(c))).
		Watches(&aimv1alpha1.AIMModel{}, handler.EnqueueRequestsFromMapFunc(findServicesForModel(c))).
		Watches(&aimv1alpha1.AIMClusterModel{}, handler.EnqueueRequestsFromMapFunc(findServicesForClusterModel(c))).
		Watches(&aimv1alpha1.AIMRuntimeConfig{}, handler.EnqueueRequestsFromMapFunc(findServicesForRuntimeConfig(c))).
		Watches(&aimv1alpha1.AIMClusterRuntimeConfig{}, handler.EnqueueRequestsFromMapFunc(findServicesForClusterRuntimeConfig(c))).
		Watches(&aimv1alpha1.AIMTemplateCache{}, handler.EnqueueRequestsFromMapFunc(findServicesForTemplateCache(c))), nil
}

func findServicesForTemplate(c client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		template, ok := obj.(*aimv1alpha1.AIMServiceTemplate)
		if !ok {
			return nil
		}
		var services aimv1alpha1.AIMServiceList
		if err := c.List(ctx, &services,
			client.InNamespace(template.Namespace),
			client.MatchingFields{aimv1alpha1.AIMServiceTemplateIndexKey: template.Name},
		); err != nil {
			log.FromContext(ctx).Error(err, "failed to list AIMServices for template", "template", template.Name)
			return nil
		}
		return requestsForServices(services.Items)
	}
}

func findServicesForClusterTemplate(c client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		template, ok := obj.(*aimv1alpha1.AIMClusterServiceTemplate)
		if !ok {
			return nil
		}
		var services aimv1alpha1.AIMServiceList
		if err := c.List(ctx, &services,
			client.MatchingFields{aimv1alpha1.AIMServiceTemplateIndexKey: template.Name},
		); err != nil {
			log.FromContext(ctx).Error(err, "failed to list AIMServices for cluster template", "template", template.Name)
			return nil
		}
		return requestsForServices(services.Items)
	}
}

func findServicesForModel(c client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		model, ok := obj.(*aimv1alpha1.AIMModel)
		if !ok {
			return nil
		}

		if svcName, ok := model.Labels[constants.LabelKeyService]; ok && svcName != "" {
			return []reconcile.Request{{
				NamespacedName: types.NamespacedName{Name: svcName, Namespace: model.Namespace},
			}}
		}

		var services aimv1alpha1.AIMServiceList
		if err := c.List(ctx, &services, client.InNamespace(model.Namespace)); err != nil {
			log.FromContext(ctx).Error(err, "failed to list AIMServices for model", "model", model.Name)
			return nil
		}

		var requests []reconcile.Request
		for _, svc := range services.Items {
			if svc.Spec.Model == nil {
				continue
			}
			if svc.Spec.Model.Name != nil && *svc.Spec.Model.Name == model.Name {
				requests = append(requests, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: svc.Name, Namespace: svc.Namespace},
				})
				continue
			}
			if svc.Spec.Model.Image != nil && *svc.Spec.Model.Image == model.Spec.Image {
				requests = append(requests, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: svc.Name, Namespace: svc.Namespace},
				})
			}
		}
		return requests
	}
}

func findServicesForClusterModel(c client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		model, ok := obj.(*aimv1alpha1.AIMClusterModel)
		if !ok {
			return nil
		}
		var services aimv1alpha1.AIMServiceList
		if err := c.List(ctx, &services); err != nil {
			log.FromContext(ctx).Error(err, "failed to list AIMServices for cluster model", "model", model.Name)
			return nil
		}
		var requests []reconcile.Request
		for _, svc := range services.Items {
			if svc.Spec.Model == nil {
				continue
			}
			if svc.Spec.Model.Name != nil && *svc.Spec.Model.Name == model.Name {
				requests = append(requests, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: svc.Name, Namespace: svc.Namespace},
				})
				continue
			}
			if svc.Spec.Model.Image != nil && *svc.Spec.Model.Image == model.Spec.Image {
				requests = append(requests, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: svc.Name, Namespace: svc.Namespace},
				})
			}
		}
		return requests
	}
}

func findServicesForRuntimeConfig(c client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		config, ok := obj.(*aimv1alpha1.AIMRuntimeConfig)
		if !ok {
			return nil
		}
		var services aimv1alpha1.AIMServiceList
		if err := c.List(ctx, &services, client.InNamespace(config.Namespace)); err != nil {
			log.FromContext(ctx).Error(err, "failed to list AIMServices for RuntimeConfig", "config", config.Name)
			return nil
		}
		var requests []reconcile.Request
		for _, svc := range services.Items {
			if svc.Spec.Name == config.Name || svc.Spec.Name == "" {
				requests = append(requests, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: svc.Name, Namespace: svc.Namespace},
				})
			}
		}
		return requests
	}
}

func findServicesForClusterRuntimeConfig(c client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		config, ok := obj.(*aimv1alpha1.AIMClusterRuntimeConfig)
		if !ok {
			return nil
		}
		var services aimv1alpha1.AIMServiceList
		if err := c.List(ctx, &services); err != nil {
			log.FromContext(ctx).Error(err, "failed to list AIMServices for ClusterRuntimeConfig", "config", config.Name)
			return nil
		}
		var requests []reconcile.Request
		for _, svc := range services.Items {
			if svc.Spec.Name == config.Name || svc.Spec.Name == "" {
				requests = append(requests, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: svc.Name, Namespace: svc.Namespace},
				})
			}
		}
		return requests
	}
}

func findServicesForTemplateCache(c client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		cache, ok := obj.(*aimv1alpha1.AIMTemplateCache)
		if !ok {
			return nil
		}
		var services aimv1alpha1.AIMServiceList
		if err := c.List(ctx, &services,
			client.InNamespace(cache.Namespace),
			client.MatchingFields{aimv1alpha1.AIMServiceResolvedTemplateIndexKey: cache.Spec.TemplateName},
		); err != nil {
			log.FromContext(ctx).Error(err, "failed to list AIMServices for template cache",
				"cache", cache.Name, "templateName", cache.Spec.TemplateName)
			return nil
		}
		return requestsForServices(services.Items)
	}
}

// requestsForServices is a small helper to keep the map funcs uniform.
func requestsForServices(items []aimv1alpha1.AIMService) []reconcile.Request {
	requests := make([]reconcile.Request, len(items))
	for i, svc := range items {
		requests[i] = reconcile.Request{
			NamespacedName: types.NamespacedName{Name: svc.Name, Namespace: svc.Namespace},
		}
	}
	return requests
}
