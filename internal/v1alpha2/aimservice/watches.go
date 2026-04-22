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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

// RegisterWatches installs the field indexer and watches that feed the
// profile-based AIMService pipeline (namespace/cluster profile + profile
// cache). The top-level controller composes this with the v1alpha1
// equivalent to assemble the full watch graph.
func RegisterWatches(ctx context.Context, mgr manager.Manager, b *builder.Builder, c client.Client) (*builder.Builder, error) {
	if err := mgr.GetFieldIndexer().IndexField(ctx, &aimv1alpha1.AIMService{}, aimv1alpha1.AIMServiceProfileIndexKey, func(obj client.Object) []string {
		svc, ok := obj.(*aimv1alpha1.AIMService)
		if !ok {
			return nil
		}
		if svc.Spec.Profile == nil || svc.Spec.Profile.Name == "" {
			return nil
		}
		return []string{svc.Spec.Profile.Name}
	}); err != nil {
		return nil, err
	}

	return b.
		Watches(
			&aimv1alpha2.AIMProfile{},
			handler.EnqueueRequestsFromMapFunc(findServicesForProfile(c)),
			builder.WithPredicates(profileRelevantChangePredicate()),
		).
		Watches(
			&aimv1alpha2.AIMClusterProfile{},
			handler.EnqueueRequestsFromMapFunc(findServicesForClusterProfile(c)),
			builder.WithPredicates(profileRelevantChangePredicate()),
		).
		Watches(
			&aimv1alpha2.AIMProfileCache{},
			handler.EnqueueRequestsFromMapFunc(findServicesForProfileCache(c)),
			builder.WithPredicates(profileCacheRelevantChangePredicate()),
		), nil
}

// profileRelevantChangePredicate fires on events that can change the ISVC the
// AIMService builds from a profile:
//   - readiness (Status.Status),
//   - ObservedGeneration (the profile controller reprocessed a spec change),
//   - derived resources (Status.Resources feeds ISVC container resources),
//   - derived node affinity (Status.ResolvedNodeAffinity feeds ISVC affinity).
//
// ObservedGeneration alone is not a sufficient proxy: if the profile
// controller recomputes Resources or ResolvedNodeAffinity on node-label
// changes without a spec bump, Generation stays put. Comparing the derived
// fields semantically keeps the AIMService in sync without a full deep-equal
// on Status. Routine status sub-resource writes that don't move any of these
// fields are filtered out to prevent reconcile hot-loops.
func profileRelevantChangePredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc:  func(_ event.CreateEvent) bool { return true },
		DeleteFunc:  func(_ event.DeleteEvent) bool { return true },
		GenericFunc: func(_ event.GenericEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldStatus, oldGen, oldRes, oldAff := profileRelevantFields(e.ObjectOld)
			newStatus, newGen, newRes, newAff := profileRelevantFields(e.ObjectNew)
			if oldStatus != newStatus || oldGen != newGen {
				return true
			}
			if !equality.Semantic.DeepEqual(oldRes, newRes) {
				return true
			}
			return !equality.Semantic.DeepEqual(oldAff, newAff)
		},
	}
}

// profileCacheRelevantChangePredicate fires when any field the AIMService
// consumes from the cache changes: high-level Status (Ready / Progressing /
// Failed), ObservedGeneration (the cache controller has reprocessed a spec
// change), or the resolved Artifacts list (PVC names or mount points that
// feed the InferenceService volume mounts). Cosmetic status writes that
// don't move any of these fields are filtered out to avoid reconcile
// hot-loops.
func profileCacheRelevantChangePredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc:  func(_ event.CreateEvent) bool { return true },
		DeleteFunc:  func(_ event.DeleteEvent) bool { return true },
		GenericFunc: func(_ event.GenericEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldCache, oldOK := e.ObjectOld.(*aimv1alpha2.AIMProfileCache)
			newCache, newOK := e.ObjectNew.(*aimv1alpha2.AIMProfileCache)
			if !oldOK || !newOK {
				return true
			}
			if oldCache.Status.Status != newCache.Status.Status {
				return true
			}
			if oldCache.Status.ObservedGeneration != newCache.Status.ObservedGeneration {
				return true
			}
			return !equality.Semantic.DeepEqual(oldCache.Status.Artifacts, newCache.Status.Artifacts)
		},
	}
}

// profileRelevantFields extracts the four status fields the predicate cares
// about from either profile scope. Keeping it a single helper makes the
// namespaced and cluster-scoped watches behave identically.
func profileRelevantFields(obj client.Object) (
	status constants.AIMStatus,
	generation int64,
	resources *corev1.ResourceRequirements,
	affinity *corev1.NodeAffinity,
) {
	switch p := obj.(type) {
	case *aimv1alpha2.AIMProfile:
		return p.Status.Status, p.Status.ObservedGeneration, p.Status.Resources, p.Status.ResolvedNodeAffinity
	case *aimv1alpha2.AIMClusterProfile:
		return p.Status.Status, p.Status.ObservedGeneration, p.Status.Resources, p.Status.ResolvedNodeAffinity
	default:
		return "", 0, nil, nil
	}
}

func findServicesForProfile(c client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		profile, ok := obj.(*aimv1alpha2.AIMProfile)
		if !ok {
			return nil
		}
		var services aimv1alpha1.AIMServiceList
		if err := c.List(ctx, &services,
			client.InNamespace(profile.Namespace),
			client.MatchingFields{aimv1alpha1.AIMServiceProfileIndexKey: profile.Name},
		); err != nil {
			log.FromContext(ctx).Error(err, "failed to list AIMServices for profile", "profile", profile.Name)
			return nil
		}
		return requestsForServices(services.Items)
	}
}

func findServicesForClusterProfile(c client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		profile, ok := obj.(*aimv1alpha2.AIMClusterProfile)
		if !ok {
			return nil
		}
		var services aimv1alpha1.AIMServiceList
		if err := c.List(ctx, &services,
			client.MatchingFields{aimv1alpha1.AIMServiceProfileIndexKey: profile.Name},
		); err != nil {
			log.FromContext(ctx).Error(err, "failed to list AIMServices for cluster profile", "profile", profile.Name)
			return nil
		}
		return requestsForServices(services.Items)
	}
}

func findServicesForProfileCache(c client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		cache, ok := obj.(*aimv1alpha2.AIMProfileCache)
		if !ok {
			return nil
		}
		var services aimv1alpha1.AIMServiceList
		if err := c.List(ctx, &services,
			client.InNamespace(cache.Namespace),
			client.MatchingFields{aimv1alpha1.AIMServiceProfileIndexKey: cache.Spec.ProfileName},
		); err != nil {
			log.FromContext(ctx).Error(err, "failed to list AIMServices for profile cache",
				"cache", cache.Name, "profileName", cache.Spec.ProfileName)
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
