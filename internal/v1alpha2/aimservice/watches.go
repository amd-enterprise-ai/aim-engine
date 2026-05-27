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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

// isNotFoundLike treats both API-server 404s and cache-Lister
// "no kind X is registered" style misses as a normal "no such object"
// outcome. The watch fan-out is best-effort and a missing profile is the
// dominant case (cache not yet primed, profile already GC'd) — collapsing
// these into a single non-noisy branch keeps the diagnostic log focused
// on real failures (e.g. transport errors, perms).
func isNotFoundLike(err error) bool {
	return apierrors.IsNotFound(err)
}

// Field index keys live on the AIMService so watch handlers can do O(1)
// lookups into the controller-runtime cache instead of paging all services
// in the namespace on every profile/model event.
const (
	// ServiceModelNameIndex maps `.spec.model.name` (when set) onto the
	// AIMService. Used by AIM(Cluster)Model and AIM(Cluster)Profile
	// watchers so a model rename or status change fans out to all
	// services authored as `spec.model.name=foo`.
	ServiceModelNameIndex = ".spec.model.name"

	// ServiceSelectorModelRefIndex maps
	// `.spec.profile.selector.modelRef.name` onto the AIMService. Used by
	// the same watchers so the desugared (`spec.profile.selector.modelRef`
	// authored directly) form reaches the same set of services as the
	// shortcut (`spec.model.name`).
	ServiceSelectorModelRefIndex = ".spec.profile.selector.modelRef.name"

	// ServiceSelectorAimIdIndex maps `.spec.profile.selector.aimId` onto
	// the AIMService so AIMProfile events fan out to selector-driven
	// services that narrow on aimId (the primary narrowing axis the
	// v1alpha2 CEL guarantees is set when modelRef is absent).
	ServiceSelectorAimIdIndex = ".spec.profile.selector.aimId"

	// ServiceModelImageIndex maps `.spec.model.image` onto the AIMService
	// so AIM(Cluster)Model events fan out to the v1alpha2 image-shape
	// quick-start services that point at the same image. Required by
	// resolveByImage: when the operator auto-creates a dedicated
	// AIMModel for an image-shape service, the model's eventual Ready
	// transition has to re-queue the service so the resolver can
	// promote `needsAutoModel=true` into a resolved profile.
	ServiceModelImageIndex = ".spec.model.image"
)

// RegisterWatches installs the field indexers and watches that feed the
// profile-based AIMService pipeline. The top-level controller composes this
// with the v1alpha1 equivalent (template-based pipeline) to assemble the
// full watch graph.
//
// Indexes:
//   - ServiceProfileIndex (existing): name-driven services.
//   - ServiceModelNameIndex: spec.model.name shortcut.
//   - ServiceSelectorModelRefIndex: explicit selector.modelRef.name.
//   - ServiceSelectorAimIdIndex: selector.aimId.
//
// Watch sources:
//   - AIMProfile  -> namespace profile events (name + source-model label + spec.aimId)
//   - AIMClusterProfile -> cluster profile events fan out across all namespaces
//   - AIMProfileCache -> downstream cache state
//   - AIMModel / AIMClusterModel -> upstream model state for model-driven
//     services (status.managedProfiles drives downstream profile readiness).
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

	if err := mgr.GetFieldIndexer().IndexField(ctx, &aimv1alpha1.AIMService{}, ServiceModelNameIndex, func(obj client.Object) []string {
		svc, ok := obj.(*aimv1alpha1.AIMService)
		if !ok {
			return nil
		}
		if svc.Spec.Model == nil || svc.Spec.Model.Name == nil || *svc.Spec.Model.Name == "" {
			return nil
		}
		return []string{*svc.Spec.Model.Name}
	}); err != nil {
		return nil, err
	}

	if err := mgr.GetFieldIndexer().IndexField(ctx, &aimv1alpha1.AIMService{}, ServiceSelectorModelRefIndex, func(obj client.Object) []string {
		svc, ok := obj.(*aimv1alpha1.AIMService)
		if !ok {
			return nil
		}
		if svc.Spec.Profile == nil || svc.Spec.Profile.Selector == nil ||
			svc.Spec.Profile.Selector.ModelRef == nil || svc.Spec.Profile.Selector.ModelRef.Name == "" {
			return nil
		}
		return []string{svc.Spec.Profile.Selector.ModelRef.Name}
	}); err != nil {
		return nil, err
	}

	if err := mgr.GetFieldIndexer().IndexField(ctx, &aimv1alpha1.AIMService{}, ServiceSelectorAimIdIndex, func(obj client.Object) []string {
		svc, ok := obj.(*aimv1alpha1.AIMService)
		if !ok {
			return nil
		}
		if svc.Spec.Profile == nil || svc.Spec.Profile.Selector == nil || svc.Spec.Profile.Selector.AimId == "" {
			return nil
		}
		return []string{svc.Spec.Profile.Selector.AimId}
	}); err != nil {
		return nil, err
	}

	if err := mgr.GetFieldIndexer().IndexField(ctx, &aimv1alpha1.AIMService{}, ServiceModelImageIndex, func(obj client.Object) []string {
		svc, ok := obj.(*aimv1alpha1.AIMService)
		if !ok {
			return nil
		}
		if svc.Spec.Model == nil || svc.Spec.Model.Image == nil || *svc.Spec.Model.Image == "" {
			return nil
		}
		return []string{*svc.Spec.Model.Image}
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
		).
		Watches(
			&aimv1alpha2.AIMModel{},
			handler.EnqueueRequestsFromMapFunc(findServicesForModel(c)),
			builder.WithPredicates(modelRelevantChangePredicate()),
		).
		Watches(
			&aimv1alpha2.AIMClusterModel{},
			handler.EnqueueRequestsFromMapFunc(findServicesForClusterModel(c)),
			builder.WithPredicates(modelRelevantChangePredicate()),
		), nil
}

// profileRelevantChangePredicate fires on events that can change the ISVC the
// AIMService builds from a profile:
//   - readiness (Status.Status),
//   - ObservedGeneration (the profile controller reprocessed a spec change),
//   - derived resources (Status.Resources feeds ISVC container resources),
//   - derived node affinity (Status.ResolvedNodeAffinity feeds ISVC affinity),
//   - provenance labels (role / source-model / origin) — selector-driven
//     services pick up new candidates whenever a profile's role flips
//     from `base` to `deployable` or its source-model changes.
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
			if !equality.Semantic.DeepEqual(oldAff, newAff) {
				return true
			}
			return provenanceLabelsChanged(e.ObjectOld.GetLabels(), e.ObjectNew.GetLabels())
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

// modelRelevantChangePredicate fires when an AIMModel / AIMClusterModel's
// managedProfiles set or readiness changes — those are the signals that
// move the set of profiles a model-driven AIMService can resolve to.
// Cosmetic status writes (e.g. observedGeneration nudges without a real
// change) are filtered.
func modelRelevantChangePredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc:  func(_ event.CreateEvent) bool { return true },
		DeleteFunc:  func(_ event.DeleteEvent) bool { return true },
		GenericFunc: func(_ event.GenericEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldStatus, oldGen, oldManaged := modelRelevantFields(e.ObjectOld)
			newStatus, newGen, newManaged := modelRelevantFields(e.ObjectNew)
			if oldStatus != newStatus || oldGen != newGen {
				return true
			}
			return !equality.Semantic.DeepEqual(oldManaged, newManaged)
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

// modelRelevantFields extracts the model status fields the predicate cares
// about (status, observedGeneration, and the managedProfiles summary).
func modelRelevantFields(obj client.Object) (constants.AIMStatus, int64, any) {
	switch m := obj.(type) {
	case *aimv1alpha2.AIMModel:
		return m.Status.Status, m.Status.ObservedGeneration, m.Status.ManagedProfiles
	case *aimv1alpha2.AIMClusterModel:
		return m.Status.Status, m.Status.ObservedGeneration, m.Status.ManagedProfiles
	default:
		return "", 0, nil
	}
}

// provenanceLabelsChanged returns true when any of the iteration-1
// provenance labels (role / source-model[, -scope] / origin) flipped
// between the old and new object. The resolver filters by these labels, so
// any change can move a service from "matches" to "no match" or vice
// versa.
func provenanceLabelsChanged(oldLabels, newLabels map[string]string) bool {
	keys := []string{
		constants.LabelKeyProfileRole,
		constants.LabelKeyProfileOrigin,
		constants.LabelKeySourceModel,
		constants.LabelKeySourceModelScope,
	}
	for _, k := range keys {
		if oldLabels[k] != newLabels[k] {
			return true
		}
	}
	return false
}

func findServicesForProfile(c client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		profile, ok := obj.(*aimv1alpha2.AIMProfile)
		if !ok {
			return nil
		}
		// Overlay profiles are private to their owning service. Routing
		// the event to that single service short-circuits the fan-out
		// since no other service may select an overlay anyway.
		if serviceName := profile.Annotations[AnnotationOverlayService]; serviceName != "" {
			return []reconcile.Request{{
				NamespacedName: types.NamespacedName{Name: serviceName, Namespace: profile.Namespace},
			}}
		}
		requests := map[types.NamespacedName]struct{}{}
		collectServicesByField(ctx, c, requests, aimv1alpha1.AIMServiceProfileIndexKey, profile.Name, profile.Namespace)
		if sourceModel := profile.Labels[constants.LabelKeySourceModel]; sourceModel != "" {
			collectServicesByField(ctx, c, requests, ServiceModelNameIndex, sourceModel, profile.Namespace)
			collectServicesByField(ctx, c, requests, ServiceSelectorModelRefIndex, sourceModel, profile.Namespace)
		}
		if profile.Spec.AimId != "" {
			collectServicesByField(ctx, c, requests, ServiceSelectorAimIdIndex, profile.Spec.AimId, profile.Namespace)
		}
		return requestsFromSet(requests)
	}
}

func findServicesForClusterProfile(c client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		profile, ok := obj.(*aimv1alpha2.AIMClusterProfile)
		if !ok {
			return nil
		}
		// Cluster profiles are visible from every namespace; fan-out
		// queries omit the InNamespace filter.
		requests := map[types.NamespacedName]struct{}{}
		collectServicesByField(ctx, c, requests, aimv1alpha1.AIMServiceProfileIndexKey, profile.Name, "")
		if sourceModel := profile.Labels[constants.LabelKeySourceModel]; sourceModel != "" {
			collectServicesByField(ctx, c, requests, ServiceModelNameIndex, sourceModel, "")
			collectServicesByField(ctx, c, requests, ServiceSelectorModelRefIndex, sourceModel, "")
		}
		if profile.Spec.AimId != "" {
			collectServicesByField(ctx, c, requests, ServiceSelectorAimIdIndex, profile.Spec.AimId, "")
		}
		return requestsFromSet(requests)
	}
}

func findServicesForModel(c client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		model, ok := obj.(*aimv1alpha2.AIMModel)
		if !ok {
			return nil
		}
		requests := map[types.NamespacedName]struct{}{}
		collectServicesByField(ctx, c, requests, ServiceModelNameIndex, model.Name, model.Namespace)
		collectServicesByField(ctx, c, requests, ServiceSelectorModelRefIndex, model.Name, model.Namespace)
		// Image-shape quick-start services point at `spec.model.image`
		// directly. When the auto-created dedicated model becomes
		// Ready (or any user-authored model with the same image is
		// applied), re-queue every service that targets that image so
		// the resolver can clear `needsAutoModel` on the next pass.
		// Restricted to the model's own namespace because v1alpha2
		// auto-created models are namespace-scoped and only the
		// services in that namespace can resolve through them.
		if model.Spec.Image != "" {
			collectServicesByField(ctx, c, requests, ServiceModelImageIndex, model.Spec.Image, model.Namespace)
		}
		return requestsFromSet(requests)
	}
}

func findServicesForClusterModel(c client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		model, ok := obj.(*aimv1alpha2.AIMClusterModel)
		if !ok {
			return nil
		}
		// Cluster models can back services in any namespace.
		requests := map[types.NamespacedName]struct{}{}
		collectServicesByField(ctx, c, requests, ServiceModelNameIndex, model.Name, "")
		collectServicesByField(ctx, c, requests, ServiceSelectorModelRefIndex, model.Name, "")
		// Image-shape quick-start: cluster-scoped fan-out across all
		// namespaces so a globally-published AIMClusterModel reaches
		// every image-shape service that happens to match its image.
		if model.Spec.Image != "" {
			collectServicesByField(ctx, c, requests, ServiceModelImageIndex, model.Spec.Image, "")
		}
		return requestsFromSet(requests)
	}
}

func findServicesForProfileCache(c client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		cache, ok := obj.(*aimv1alpha2.AIMProfileCache)
		if !ok {
			return nil
		}
		requests := map[types.NamespacedName]struct{}{}

		// Name-driven services: `spec.profile.name == cache.Spec.ProfileName`.
		collectServicesByField(ctx, c, requests, aimv1alpha1.AIMServiceProfileIndexKey, cache.Spec.ProfileName, cache.Namespace)

		// Model-driven services: the cache feeds a resolved AIMProfile;
		// look up that profile's source-model label / aimId / overlay
		// annotation so model-shape services (`spec.model.name` or
		// `spec.profile.selector.modelRef.name` / `selector.aimId`) are
		// re-queued too. Without this fan-out the service controller would
		// only see the cache's initial empty Status (no `Status.Status`
		// field set yet) and never re-reconcile when the cache becomes
		// Ready, leaving the AIMService stuck Progressing.
		logger := log.FromContext(ctx).WithName("aimservice-watches")
		if cache.Spec.ProfileScope == aimv1alpha1.AIMResolutionScopeCluster {
			var clusterProfile aimv1alpha2.AIMClusterProfile
			err := c.Get(ctx, types.NamespacedName{Name: cache.Spec.ProfileName}, &clusterProfile)
			switch {
			case err == nil:
				addModelDrivenRequests(ctx, c, requests, clusterProfile.Labels, clusterProfile.Spec.AimId, clusterProfile.Annotations, cache.Namespace)
			case isNotFoundLike(err):
				// Profile may not yet be cached or has been GC'd — skip silently.
			default:
				// Promote unexpected errors to Info so operators have a
				// breadcrumb when the model-driven fan-out goes quiet.
				// We still continue with the partial request set so
				// name-driven services aren't blocked.
				logger.Info("failed to fetch cluster profile for cache fan-out",
					"profile", cache.Spec.ProfileName, "error", err.Error())
			}
		} else {
			var profile aimv1alpha2.AIMProfile
			err := c.Get(ctx, types.NamespacedName{Name: cache.Spec.ProfileName, Namespace: cache.Namespace}, &profile)
			switch {
			case err == nil:
				addModelDrivenRequests(ctx, c, requests, profile.Labels, profile.Spec.AimId, profile.Annotations, cache.Namespace)
			case isNotFoundLike(err):
				// Profile not in cache yet — skip silently.
			default:
				logger.Info("failed to fetch namespace profile for cache fan-out",
					"profile", cache.Spec.ProfileName, "namespace", cache.Namespace, "error", err.Error())
			}
		}

		return requestsFromSet(requests)
	}
}

// addModelDrivenRequests fans an AIMProfile / AIMClusterProfile event back
// into model-shape AIMServices via their source-model, selector.modelRef,
// and selector.aimId indices, plus the dedicated-overlay annotation.
// Restricting fan-out to `namespace` is correct for both namespace profiles
// (their cache is in the same namespace) and namespace services consuming
// cluster profiles (which the AIMProfileCache lives next to).
func addModelDrivenRequests(
	ctx context.Context,
	c client.Client,
	requests map[types.NamespacedName]struct{},
	profileLabels map[string]string,
	aimID string,
	annotations map[string]string,
	namespace string,
) {
	if sourceModel := profileLabels[constants.LabelKeySourceModel]; sourceModel != "" {
		collectServicesByField(ctx, c, requests, ServiceModelNameIndex, sourceModel, namespace)
		collectServicesByField(ctx, c, requests, ServiceSelectorModelRefIndex, sourceModel, namespace)
	}
	if aimID != "" {
		collectServicesByField(ctx, c, requests, ServiceSelectorAimIdIndex, aimID, namespace)
	}
	if serviceName := annotations[AnnotationOverlayService]; serviceName != "" {
		requests[types.NamespacedName{Name: serviceName, Namespace: namespace}] = struct{}{}
	}
}

// collectServicesByField runs a field-indexed list against the controller-
// runtime cache and merges the resulting reconcile requests into the given
// set. Lookup errors are logged but do not abort the fan-out; the watch
// loop will retry on the next event.
//
// namespace == "" widens the lookup to all namespaces, which is the right
// semantic for cluster-scoped sources (AIMClusterProfile, AIMClusterModel)
// whose changes can affect services in any namespace.
func collectServicesByField(
	ctx context.Context,
	c client.Client,
	requests map[types.NamespacedName]struct{},
	indexKey, value, namespace string,
) {
	if value == "" {
		return
	}
	opts := []client.ListOption{client.MatchingFields{indexKey: value}}
	if namespace != "" {
		opts = append(opts, client.InNamespace(namespace))
	}
	var services aimv1alpha1.AIMServiceList
	if err := c.List(ctx, &services, opts...); err != nil {
		log.FromContext(ctx).Error(err, "failed to list AIMServices",
			"indexKey", indexKey, "value", value, "namespace", namespace)
		return
	}
	for _, svc := range services.Items {
		requests[types.NamespacedName{Name: svc.Name, Namespace: svc.Namespace}] = struct{}{}
	}
}

func requestsFromSet(set map[types.NamespacedName]struct{}) []reconcile.Request {
	out := make([]reconcile.Request, 0, len(set))
	for nn := range set {
		out = append(out, reconcile.Request{NamespacedName: nn})
	}
	return out
}
