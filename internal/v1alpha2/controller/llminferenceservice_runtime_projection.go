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

package controller

import (
	"context"
	"sort"
	"strings"

	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/runtimeprojection"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/serving"
)

// llmInferenceServiceBaseRefIndexKey indexes KServe LLMInferenceServices by the
// reserved aim- LLMInferenceServiceConfig names in spec.baseRefs. Those names
// are the projection keys shared with the ServingRuntime of the same name.
const llmInferenceServiceBaseRefIndexKey = ".spec.baseRefs.aim"

// managedBaseRefs returns the reserved aim- LLMInferenceServiceConfig names an
// LLMInferenceService references.
func managedBaseRefs(service *kservev1alpha2.LLMInferenceService) []string {
	var names []string
	for _, ref := range service.Spec.BaseRefs {
		if strings.HasPrefix(ref.Name, serving.RuntimeNamePrefix) {
			names = append(names, ref.Name)
		}
	}
	return names
}

func indexLLMInferenceServiceBaseRefs(obj client.Object) []string {
	service, ok := obj.(*kservev1alpha2.LLMInferenceService)
	if !ok {
		return nil
	}
	return managedBaseRefs(service)
}

func runtimeKeysForLLMInferenceService(_ context.Context, obj client.Object) []reconcile.Request {
	service, ok := obj.(*kservev1alpha2.LLMInferenceService)
	if !ok {
		return nil
	}
	names := managedBaseRefs(service)
	requests := make([]reconcile.Request, 0, len(names))
	for _, name := range names {
		requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{
			Namespace: service.Namespace,
			Name:      name,
		}})
	}
	return requests
}

// llmInferenceServiceProjectionPredicate mirrors
// inferenceServiceProjectionPredicate: only spec (baseRefs) and the
// runtime-profile annotation drive the projection, so status churn is dropped.
func llmInferenceServiceProjectionPredicate() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			if e.ObjectOld == nil || e.ObjectNew == nil {
				return true
			}
			if e.ObjectOld.GetGeneration() != e.ObjectNew.GetGeneration() {
				return true
			}
			return e.ObjectOld.GetAnnotations()[constants.AnnotationRuntimeProfile] !=
				e.ObjectNew.GetAnnotations()[constants.AnnotationRuntimeProfile]
		},
	}
}

// activeLLMInferenceServicesForRuntime lists the non-deleting
// LLMInferenceServices in the key's namespace that reference the key's
// LLMInferenceServiceConfig name. Empty when LLMInferenceService support is
// off.
func (r *RuntimeProjectionReconciler) activeLLMInferenceServicesForRuntime(
	ctx context.Context,
	key types.NamespacedName,
) ([]kservev1alpha2.LLMInferenceService, error) {
	if !r.LLMInferenceService.Enabled {
		return nil, nil
	}
	var services kservev1alpha2.LLMInferenceServiceList
	if err := r.List(
		ctx,
		&services,
		client.InNamespace(key.Namespace),
		client.MatchingFields{llmInferenceServiceBaseRefIndexKey: key.Name},
	); err != nil {
		return nil, err
	}
	active := make([]kservev1alpha2.LLMInferenceService, 0, len(services.Items))
	for i := range services.Items {
		if services.Items[i].DeletionTimestamp == nil {
			active = append(active, services.Items[i])
		}
	}
	sort.Slice(active, func(i, j int) bool {
		return active[i].Name < active[j].Name
	})
	return active, nil
}

// llmConfigSibling returns the namespaced LLMInferenceServiceConfig that shares
// the key's name, or nil when absent or LLMInferenceService support is off.
func (r *RuntimeProjectionReconciler) llmConfigSibling(
	ctx context.Context,
	key types.NamespacedName,
) (*kservev1alpha2.LLMInferenceServiceConfig, error) {
	if !r.LLMInferenceService.Enabled {
		return nil, nil
	}
	var config kservev1alpha2.LLMInferenceServiceConfig
	if err := r.Get(ctx, key, &config); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &config, nil
}

// llmInferenceServicesReferencingRuntimes is the LLMInferenceService
// counterpart of inferenceServicesReferencingRuntimes.
func (r *RuntimeProjectionReconciler) llmInferenceServicesReferencingRuntimes(
	ctx context.Context,
	namespace string,
	runtimeNames []string,
) []reconcile.Request {
	if !r.LLMInferenceService.Enabled {
		return nil
	}
	requests := map[types.NamespacedName]struct{}{}
	for _, name := range runtimeNames {
		if name == "" {
			continue
		}
		opts := []client.ListOption{client.MatchingFields{llmInferenceServiceBaseRefIndexKey: name}}
		if namespace != "" {
			opts = append(opts, client.InNamespace(namespace))
		}
		var services kservev1alpha2.LLMInferenceServiceList
		if err := r.List(ctx, &services, opts...); err != nil {
			log.FromContext(ctx).Error(err, "failed to list LLMInferenceServices referencing an LLMInferenceServiceConfig",
				"configName", name, "namespace", namespace)
			continue
		}
		for i := range services.Items {
			if services.Items[i].DeletionTimestamp != nil {
				continue
			}
			requests[types.NamespacedName{Namespace: services.Items[i].Namespace, Name: name}] = struct{}{}
		}
	}
	return requestsFromSet(requests)
}

// addLazyLLMConfigShadowRequests adds the profile's existing lazy
// LLMInferenceServiceConfig shadows to requests.
func (r *RuntimeProjectionReconciler) addLazyLLMConfigShadowRequests(
	ctx context.Context,
	requests map[types.NamespacedName]struct{},
	profile runtimeprojection.BackingProfile,
	opts []client.ListOption,
) {
	if !r.LLMInferenceService.Enabled {
		return
	}
	var configs kservev1alpha2.LLMInferenceServiceConfigList
	if err := r.List(ctx, &configs, opts...); err != nil {
		log.FromContext(ctx).Error(err, "failed to list lazy LLMInferenceServiceConfig shadows",
			"profile", profile.GetName())
		return
	}
	for i := range configs.Items {
		r.addOwnedLazyShadowRequest(requests, &configs.Items[i], profile)
	}
}
