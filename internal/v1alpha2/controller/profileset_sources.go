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

	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimmodel"
)

// Model readiness and hardware inventory do not affect published definitions.
// Avoid fan-out on those status-only updates; catalog events cover discovery.
func modelDefinitionPredicate() predicate.Predicate {
	return predicate.Funcs{UpdateFunc: func(e event.UpdateEvent) bool {
		if e.ObjectOld.GetGeneration() != e.ObjectNew.GetGeneration() ||
			!apiequality.Semantic.DeepEqual(e.ObjectOld.GetDeletionTimestamp(), e.ObjectNew.GetDeletionTimestamp()) {
			return true
		}
		switch old := e.ObjectOld.(type) {
		case *aimv1alpha2.AIMModel:
			next, ok := e.ObjectNew.(*aimv1alpha2.AIMModel)
			return !ok || !apiequality.Semantic.DeepEqual(old.Spec, next.Spec) ||
				!apiequality.Semantic.DeepEqual(old.Status.ImageMetadata, next.Status.ImageMetadata)
		case *aimv1alpha2.AIMClusterModel:
			next, ok := e.ObjectNew.(*aimv1alpha2.AIMClusterModel)
			return !ok || !apiequality.Semantic.DeepEqual(old.Spec, next.Spec) ||
				!apiequality.Semantic.DeepEqual(old.Status.ImageMetadata, next.Status.ImageMetadata)
		default:
			return true
		}
	}}
}

func profileSetModelIndexValues(spec aimv1alpha1.AIMProfileSetSpec) []string {
	if spec.SourceRef != nil {
		return nil
	}
	if spec.Selector.ModelRef != nil && spec.Selector.ModelRef.Name != "" {
		return []string{spec.Selector.ModelRef.Name}
	}
	return []string{profileSetAimIDWildcard}
}

func profileSetsForModel(ctx context.Context, c client.Client, model client.Object) []reconcile.Request {
	return listRequests(ctx, func(ctx context.Context) ([]*aimv1alpha2.AIMProfileSet, error) {
		var out []*aimv1alpha2.AIMProfileSet
		for _, key := range []string{model.GetName(), profileSetAimIDWildcard} {
			var sets aimv1alpha2.AIMProfileSetList
			opts := []client.ListOption{client.MatchingFields{profileSetModelRefIndex: key}}
			if model.GetNamespace() != "" {
				opts = append(opts, client.InNamespace(model.GetNamespace()))
			}
			if err := c.List(ctx, &sets, opts...); err != nil {
				return nil, err
			}
			for i := range sets.Items {
				set := &sets.Items[i]
				if ref := set.Spec.Selector.ModelRef; ref != nil {
					if model.GetNamespace() == "" && ref.Scope == aimv1alpha1.ProfileSelectorScopeNamespace {
						continue
					}
					if model.GetNamespace() != "" && ref.Scope == aimv1alpha1.ProfileSelectorScopeCluster {
						continue
					}
				}
				out = append(out, set)
			}
		}
		return out, nil
	}, "failed to list AIMProfileSets for model event", "model", model.GetName())
}

func clusterProfileSetsForModel(ctx context.Context, c client.Client, model client.Object) []reconcile.Request {
	return listRequests(ctx, func(ctx context.Context) ([]*aimv1alpha2.AIMClusterProfileSet, error) {
		var out []*aimv1alpha2.AIMClusterProfileSet
		for _, key := range []string{model.GetName(), profileSetAimIDWildcard} {
			var sets aimv1alpha2.AIMClusterProfileSetList
			if err := c.List(ctx, &sets, client.MatchingFields{profileSetModelRefIndex: key}); err != nil {
				return nil, err
			}
			for i := range sets.Items {
				set := &sets.Items[i]
				if ref := set.Spec.Selector.ModelRef; ref != nil && ref.Scope == aimv1alpha1.ProfileSelectorScopeNamespace {
					continue
				}
				out = append(out, set)
			}
		}
		return out, nil
	}, "failed to list AIMClusterProfileSets for model event", "model", model.GetName())
}

// ConfigMap events carry the producing model even before a model status update
// or any profile materialization. Old revisions also route deletion events.
func catalogModelEvent(cm *corev1.ConfigMap) client.Object {
	for _, owner := range cm.OwnerReferences {
		switch owner.Kind {
		case "AIMClusterModel":
			return &aimv1alpha2.AIMClusterModel{ObjectMeta: metav1.ObjectMeta{Name: owner.Name}}
		case "AIMModel":
			return &aimv1alpha2.AIMModel{ObjectMeta: metav1.ObjectMeta{Name: owner.Name, Namespace: cm.Namespace}}
		}
	}
	if name := cm.Annotations[aimmodel.AnnotationModelName()]; name != "" {
		return &aimv1alpha2.AIMModel{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: cm.Namespace}}
	}
	return nil
}
