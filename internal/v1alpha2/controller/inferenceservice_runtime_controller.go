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

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/profilecache"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/profileyaml"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/runtimeprojection"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/serving"
)

const (
	runtimeProjectionControllerName = "runtime-projection"
	// runtimeProjectionFieldOwner is the SSA field manager for the lazily
	// materialized namespace ServingRuntime + ConfigMap. Distinct from the
	// profile/service field owners so authority over these objects is traceable.
	runtimeProjectionFieldOwner = "aim-runtime-projection-controller"

	// inferenceServiceRuntimeIndexKey indexes KServe InferenceServices by the
	// ServingRuntime or ClusterServingRuntime name they reference
	// (spec.predictor.model.runtime). The mapping handlers use it to fan a
	// changed profile, cache, or shadow object back to the InferenceServices
	// that consume the affected ServingRuntime or ClusterServingRuntime, without
	// paging every InferenceService on each event.
	inferenceServiceRuntimeIndexKey = ".spec.predictor.model.runtime"

	namespaceProfileKind = "AIMProfile"
	clusterProfileKind   = "AIMClusterProfile"
)

// RuntimeProjectionReconciler reconciles
// namespace/KServe-ServingRuntime-name keys and lazily materializes a complete
// namespaced KServe ServingRuntime plus its colocated ConfigMap.
// The objects are owned by the backing profile and applied authoritatively
// (SSA + ForceOwnership) under the reserved aim- prefix.
//
// This reconciler is mode-independent and always on: it guarantees an
// AIMService's referenced ServingRuntime (and any native KServe InferenceService
// referencing a managed ClusterServingRuntime) gets its colocated profile
// ConfigMap regardless of the eager projection mode.
type RuntimeProjectionReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
	// APIReader is the uncached, direct-to-API reader used for the authoritative
	// completeness check of a hand-authored KServe ServingRuntime's colocated
	// ConfigMap. Nil in unit tests, which fall back to Client.
	APIReader client.Reader
	Clientset kubernetes.Interface
}

// configMapReader returns the reader used for ConfigMap point reads that must
// see objects outside the label-scoped cache. Falls back to the cached client
// when APIReader is unset (unit tests wiring only Client).
func (r *RuntimeProjectionReconciler) configMapReader() client.Reader {
	if r.APIReader != nil {
		return r.APIReader
	}
	return r.Client
}

// +kubebuilder:rbac:groups=serving.kserve.io,resources=inferenceservices,verbs=get;list;watch
// +kubebuilder:rbac:groups=serving.kserve.io,resources=servingruntimes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=serving.kserve.io,resources=clusterservingruntimes,verbs=get;list;watch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclusterprofiles,verbs=get;list;watch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimprofiles,verbs=get;list;watch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimprofilecaches,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete

func (r *RuntimeProjectionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	state, err := r.resolveState(ctx, req.NamespacedName)
	if err != nil {
		return ctrl.Result{}, err
	}

	desired, err := runtimeprojection.DesiredForRuntime(req.Namespace, req.Name, state)
	if err != nil {
		return ctrl.Result{}, err
	}
	if desired.Runtime == nil {
		return ctrl.Result{}, nil
	}

	objects := []client.Object{desired.Runtime, desired.ConfigMap}
	if err := controllerutils.ApplyDesiredStateWithForceFailFast(
		ctx, r.Client, runtimeProjectionFieldOwner, r.Scheme, objects, desired.Owner,
	); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("materialized namespaced KServe ServingRuntime",
		"servingRuntime", desired.Runtime.Name,
		"namespace", desired.Runtime.Namespace,
		"profile", desired.Owner.GetName())
	return ctrl.Result{}, nil
}

// resolveState reads the state for one namespace/KServe-ServingRuntime-name key.
// Existing lazy sibling ownership is authoritative, followed by a managed
// ClusterServingRuntime and then deterministic annotations on active indexed
// InferenceService consumers.
func (r *RuntimeProjectionReconciler) resolveState(
	ctx context.Context,
	key types.NamespacedName,
) (runtimeprojection.ProjectionState, error) {
	var state runtimeprojection.ProjectionState

	if key.Name == "" || !strings.HasPrefix(key.Name, serving.RuntimeNamePrefix) {
		return state, nil
	}

	sr, cm, err := r.runtimeSiblings(ctx, key)
	if err != nil {
		return state, err
	}
	if runtimeSiblingsComplete(sr, cm) {
		state.NamespaceRuntimeComplete = true
		return state, nil
	}

	consumers, err := r.activeInferenceServicesForRuntime(ctx, key)
	if err != nil {
		return state, err
	}

	// A stale queue entry must not resurrect a fully removed inactive shadow.
	if sr == nil && cm == nil && len(consumers) == 0 {
		return state, nil
	}

	state.ExistingShadowProfile, err = r.profileFromLazySiblings(ctx, sr, cm)
	if err != nil {
		return state, err
	}
	if state.ExistingShadowProfile == nil {
		state.ManagedRuntimeProfile, err = r.profileFromManagedClusterRuntime(ctx, key.Name)
		if err != nil {
			return state, err
		}
	}
	if state.ExistingShadowProfile == nil && state.ManagedRuntimeProfile == nil {
		state.AnnotatedProfile, err = r.profileFromInferenceServices(ctx, key.Name, consumers)
		if err != nil {
			return state, err
		}
	}

	if profile := state.BackingProfile(); profile != nil {
		state.Cache, err = r.readyProfileCache(ctx, key.Namespace, profile.GetName(), backingProfileScope(profile))
		if err != nil {
			return state, err
		}
	}

	return state, nil
}

func (r *RuntimeProjectionReconciler) runtimeSiblings(
	ctx context.Context,
	key types.NamespacedName,
) (*kservev1alpha1.ServingRuntime, *corev1.ConfigMap, error) {
	var sr kservev1alpha1.ServingRuntime
	if err := r.Get(ctx, key, &sr); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, nil, err
		}
	} else {
		var cm corev1.ConfigMap
		if err := r.configMapReader().Get(ctx, key, &cm); err != nil {
			if !apierrors.IsNotFound(err) {
				return nil, nil, err
			}
			return &sr, nil, nil
		}
		return &sr, &cm, nil
	}

	var cm corev1.ConfigMap
	if err := r.configMapReader().Get(ctx, key, &cm); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	return nil, &cm, nil
}

func runtimeSiblingsComplete(sr *kservev1alpha1.ServingRuntime, cm *corev1.ConfigMap) bool {
	if sr == nil || cm == nil {
		return false
	}
	if sr.GetLabels()[constants.LabelRuntimeProjection] == constants.LabelValueRuntimeProjectionEager {
		return true
	}
	return !ownedByLazyProjection(sr) && !ownedByLazyProjection(cm)
}

// backingProfileScope classifies a resolved backing profile so the colocated
// cache lookup matches the profile-owned cache's ProfileScope: a namespace
// AIMProfile pairs with a namespace-scope cache, a cluster AIMClusterProfile
// with a cluster-scope cache.
func backingProfileScope(profile runtimeprojection.BackingProfile) aimv1alpha1.AIMResolutionScope {
	if _, ok := profile.(*aimv1alpha2.AIMProfile); ok {
		return aimv1alpha1.AIMResolutionScopeNamespace
	}
	return aimv1alpha1.AIMResolutionScopeCluster
}

// namespaceRuntimeComplete reports whether a complete namespaced KServe
// ServingRuntime plus its colocated ConfigMap of the same name already exists.
// The lazy projection defers to that pair and materializes no shadow.
//
// A complete ServingRuntime this controller materialized itself (its lazy
// shadow) does NOT count: it must be re-applied on every reconcile so a drift
// edit, a late-Ready cache, or backing-profile changes are reasserted by the
// authoritative force-apply. The lazy marker identifies the shadow normally;
// its SSA field manager preserves that identity if the marker is removed.
// Eager projections and hand-authored pairs are deferred to.
func (r *RuntimeProjectionReconciler) namespaceRuntimeComplete(
	ctx context.Context,
	namespace, name string,
) (bool, error) {
	sr, cm, err := r.runtimeSiblings(ctx, types.NamespacedName{Namespace: namespace, Name: name})
	if err != nil {
		return false, err
	}
	return runtimeSiblingsComplete(sr, cm), nil
}

// ownedByLazyProjection reports whether the object is a shadow this controller
// materialized. The marker is the primary identity; the SSA field manager is
// durable fallback evidence when an out-of-band edit removes that marker.
//
// The marker (not the ownerReference kind alone) is load-bearing: a
// namespace-profile-backed shadow and that profile's eager per-profile
// projection share the same AIMProfile ownerRef, so the kind can't distinguish
// them. An explicit eager marker therefore always wins, even if stale managed
// fields still mention this controller. Unmarked hand-authored objects have
// neither the lazy marker nor this controller's field manager and remain
// deferred to.
func ownedByLazyProjection(obj client.Object) bool {
	switch obj.GetLabels()[constants.LabelRuntimeProjection] {
	case constants.LabelValueRuntimeProjectionEager:
		return false
	case constants.LabelValueRuntimeProjectionLazy:
		return true
	}
	// Keep managedFields enabled in the manager cache: without it, a removed
	// marker would make this controller's existing shadow look hand-authored.
	for _, entry := range obj.GetManagedFields() {
		if entry.Manager == runtimeProjectionFieldOwner {
			return true
		}
	}
	return false
}

func (r *RuntimeProjectionReconciler) profileFromLazySiblings(
	ctx context.Context,
	sr *kservev1alpha1.ServingRuntime,
	cm *corev1.ConfigMap,
) (runtimeprojection.BackingProfile, error) {
	objects := make([]client.Object, 0, 2)
	if sr != nil {
		objects = append(objects, sr)
	}
	if cm != nil {
		objects = append(objects, cm)
	}
	for _, obj := range objects {
		if !isManagedLazyRuntimeObject(obj) {
			continue
		}
		profile, err := r.profileFromOwnerReferences(ctx, obj.GetNamespace(), obj.GetOwnerReferences())
		if err != nil {
			return nil, err
		}
		if profile != nil {
			return profile, nil
		}
	}
	return nil, nil
}

func (r *RuntimeProjectionReconciler) profileFromOwnerReferences(
	ctx context.Context,
	namespace string,
	refs []metav1.OwnerReference,
) (runtimeprojection.BackingProfile, error) {
	for _, ref := range refs {
		if ref.APIVersion != "" && ref.APIVersion != aimv1alpha2.GroupVersion.String() {
			continue
		}
		switch ref.Kind {
		case namespaceProfileKind:
			profile, err := r.namespaceBackingProfile(ctx, namespace, ref.Name)
			if err != nil {
				return nil, err
			}
			if profile != nil && profile.GetUID() == ref.UID {
				return profile, nil
			}
		case clusterProfileKind:
			profile, err := r.clusterBackingProfile(ctx, ref.Name)
			if err != nil {
				return nil, err
			}
			if profile != nil && profile.GetUID() == ref.UID {
				return profile, nil
			}
		}
	}
	return nil, nil
}

func (r *RuntimeProjectionReconciler) activeInferenceServicesForRuntime(
	ctx context.Context,
	key types.NamespacedName,
) ([]servingv1beta1.InferenceService, error) {
	var isvcs servingv1beta1.InferenceServiceList
	if err := r.List(
		ctx,
		&isvcs,
		client.InNamespace(key.Namespace),
		client.MatchingFields{inferenceServiceRuntimeIndexKey: key.Name},
	); err != nil {
		return nil, err
	}
	active := make([]servingv1beta1.InferenceService, 0, len(isvcs.Items))
	for i := range isvcs.Items {
		if isvcs.Items[i].DeletionTimestamp == nil {
			active = append(active, isvcs.Items[i])
		}
	}
	sort.Slice(active, func(i, j int) bool {
		return active[i].Name < active[j].Name
	})
	return active, nil
}

func (r *RuntimeProjectionReconciler) profileFromInferenceServices(
	ctx context.Context,
	runtimeName string,
	isvcs []servingv1beta1.InferenceService,
) (runtimeprojection.BackingProfile, error) {
	for i := range isvcs {
		profileName := isvcs[i].GetAnnotations()[constants.AnnotationRuntimeProfile]
		profile, err := r.lookupBackingProfile(ctx, isvcs[i].Namespace, profileName)
		if err != nil {
			return nil, err
		}
		if profile != nil && runtimeprojection.RuntimeNameMatchesProfile(profile, runtimeName) {
			return profile, nil
		}
	}
	return nil, nil
}

// profileFromManagedClusterRuntime resolves the backing AIMClusterProfile of a
// referenced KServe ClusterServingRuntime by following its ownerReference
// (Kind=AIMClusterProfile) or profile correlator label. This is the native,
// annotation-free flow; it only ever yields a cluster profile (a namespace
// profile projects a namespaced ServingRuntime, never a ClusterServingRuntime).
func (r *RuntimeProjectionReconciler) profileFromManagedClusterRuntime(
	ctx context.Context,
	runtimeName string,
) (runtimeprojection.BackingProfile, error) {
	var csr kservev1alpha1.ClusterServingRuntime
	if err := r.Get(ctx, client.ObjectKey{Name: runtimeName}, &csr); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}

	if !isManagedRuntimeObject(&csr) {
		return nil, nil
	}
	for _, ref := range csr.GetOwnerReferences() {
		if ref.Kind != clusterProfileKind {
			continue
		}
		return r.profileFromOwnerReferences(ctx, "", []metav1.OwnerReference{ref})
	}
	profileName := csr.GetLabels()[constants.LabelProfile]
	if profileName == "" {
		return nil, nil
	}
	return r.clusterBackingProfile(ctx, profileName)
}

// lookupBackingProfile resolves a backing profile by name, preferring a
// namespace AIMProfile in the given namespace over a cluster AIMClusterProfile
// of the same name. This mirrors the AIMService resolver's namespace-over-cluster
// precedence (internal/v1alpha2/aimservice/resolver.go resolveByName), so the
// lazy resolver settles on the same profile scope the service did. Returns a
// genuine nil interface (never a typed-nil pointer) when neither exists, so the
// first-non-nil resolution order in ProjectionState.BackingProfile stays sound.
func (r *RuntimeProjectionReconciler) lookupBackingProfile(
	ctx context.Context,
	namespace, name string,
) (runtimeprojection.BackingProfile, error) {
	if name == "" {
		return nil, nil
	}

	var nsProfile aimv1alpha2.AIMProfile
	err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &nsProfile)
	if err == nil {
		return &nsProfile, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, err
	}

	return r.clusterBackingProfile(ctx, name)
}

// clusterBackingProfile resolves a cluster AIMClusterProfile by name, returning a
// genuine nil interface (not a typed-nil pointer) when absent.
func (r *RuntimeProjectionReconciler) clusterBackingProfile(
	ctx context.Context,
	name string,
) (runtimeprojection.BackingProfile, error) {
	var profile aimv1alpha2.AIMClusterProfile
	if err := r.Get(ctx, client.ObjectKey{Name: name}, &profile); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &profile, nil
}

// readyProfileCache returns a Ready, profile-owned (Shared) AIMProfileCache in
// the namespace that caches the given profile at the given scope, or nil when
// none exists. The cache contributes the profile-owned cache mount on the
// materialized ServingRuntime. Scope must match the backing profile's scope: a
// cluster-scope cache for an AIMClusterProfile-backed shadow, a namespace-scope
// cache for a namespace-AIMProfile-backed shadow.
//
// It delegates to the shared profilecache resolver so this lazy shadow and the
// eager profile-reconciler projection mount the identical cache — the fix for
// the mode-dependent Shared-cache mount (a service-driven Shared cache resolves
// here whether or not the profile set caching.enabled).
func (r *RuntimeProjectionReconciler) readyProfileCache(
	ctx context.Context,
	namespace, profileName string,
	scope aimv1alpha1.AIMResolutionScope,
) (*aimv1alpha2.AIMProfileCache, error) {
	return profilecache.FindReadyShared(ctx, r.Client, namespace, profileName, scope)
}

func (r *RuntimeProjectionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Recorder == nil {
		r.Recorder = mgr.GetEventRecorderFor("aim-" + runtimeProjectionControllerName + "-controller")
	}
	if r.APIReader == nil {
		r.APIReader = mgr.GetAPIReader()
	}

	// Index InferenceServices by their referenced ServingRuntime or
	// ClusterServingRuntime name so the mapping handlers can look up the
	// affected consumers efficiently.
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&servingv1beta1.InferenceService{},
		inferenceServiceRuntimeIndexKey,
		indexInferenceServiceRuntime,
	); err != nil {
		return err
	}

	// KServe ServingRuntime is the primary source, so every queue key is
	// consistently namespace/ServingRuntime-name. Other sources map to that same
	// key shape.
	return ctrl.NewControllerManagedBy(mgr).
		For(
			&kservev1alpha1.ServingRuntime{},
			builder.WithPredicates(managedRuntimeObjectPredicate()),
		).
		Watches(
			&servingv1beta1.InferenceService{},
			handler.EnqueueRequestsFromMapFunc(runtimeKeyForInferenceService),
			builder.WithPredicates(inferenceServiceProjectionPredicate()),
		).
		Watches(
			&corev1.ConfigMap{},
			handler.EnqueueRequestsFromMapFunc(r.findInferenceServicesForManagedRuntimeObject),
			builder.WithPredicates(managedRuntimeObjectPredicate()),
		).
		Watches(
			&aimv1alpha2.AIMClusterProfile{},
			handler.EnqueueRequestsFromMapFunc(r.findInferenceServicesForClusterProfile),
			builder.WithPredicates(clusterProfileProjectionPredicate()),
		).
		Watches(
			&aimv1alpha2.AIMProfile{},
			handler.EnqueueRequestsFromMapFunc(r.findInferenceServicesForProfile),
			builder.WithPredicates(profileProjectionPredicate()),
		).
		Watches(
			&aimv1alpha2.AIMProfileCache{},
			handler.EnqueueRequestsFromMapFunc(r.findInferenceServicesForProfileCache),
			builder.WithPredicates(profileCacheProjectionPredicate()),
		).
		Named(runtimeProjectionControllerName).
		Complete(r)
}

// indexInferenceServiceRuntime extracts the KServe ServingRuntime or
// ClusterServingRuntime name an InferenceService references for the field
// index; InferenceServices with no runtime reference are omitted.
func indexInferenceServiceRuntime(obj client.Object) []string {
	isvc, ok := obj.(*servingv1beta1.InferenceService)
	if !ok {
		return nil
	}
	name := runtimeprojection.ReferencedRuntimeName(isvc)
	if name == "" {
		return nil
	}
	return []string{name}
}

func runtimeKeyForInferenceService(_ context.Context, obj client.Object) []reconcile.Request {
	isvc, ok := obj.(*servingv1beta1.InferenceService)
	if !ok {
		return nil
	}
	name := runtimeprojection.ReferencedRuntimeName(isvc)
	if name == "" || !strings.HasPrefix(name, serving.RuntimeNamePrefix) {
		return nil
	}
	return []reconcile.Request{{NamespacedName: types.NamespacedName{
		Namespace: isvc.Namespace,
		Name:      name,
	}}}
}

// findInferenceServicesForManagedRuntimeObject preserves the historical helper
// name while mapping a lazy ServingRuntime or ConfigMap sibling directly to its
// ServingRuntime key.
func (r *RuntimeProjectionReconciler) findInferenceServicesForManagedRuntimeObject(
	_ context.Context,
	obj client.Object,
) []reconcile.Request {
	if !isManagedRuntimeObject(obj) {
		return nil
	}
	return []reconcile.Request{{NamespacedName: client.ObjectKeyFromObject(obj)}}
}

// findInferenceServicesForClusterProfile returns the union of active consumer
// ServingRuntime keys and every existing lazy shadow owned by this exact
// profile.
func (r *RuntimeProjectionReconciler) findInferenceServicesForClusterProfile(
	ctx context.Context,
	obj client.Object,
) []reconcile.Request {
	profile, ok := obj.(*aimv1alpha2.AIMClusterProfile)
	if !ok {
		return nil
	}
	return mergeRequests(
		r.inferenceServicesReferencingRuntimes(ctx, "", runtimeNamesForProfile(profile.Name, profile.Spec.AimId)),
		r.lazyShadowRequestsForProfile(ctx, profile, ""),
	)
}

// findInferenceServicesForProfile is the namespace-scoped equivalent.
func (r *RuntimeProjectionReconciler) findInferenceServicesForProfile(
	ctx context.Context,
	obj client.Object,
) []reconcile.Request {
	profile, ok := obj.(*aimv1alpha2.AIMProfile)
	if !ok {
		return nil
	}
	return mergeRequests(
		r.inferenceServicesReferencingRuntimes(ctx, profile.Namespace, runtimeNamesForProfile(profile.Name, profile.Spec.AimId)),
		r.lazyShadowRequestsForProfile(ctx, profile, profile.Namespace),
	)
}

// findInferenceServicesForProfileCache returns active ServingRuntime keys plus
// inactive lazy shadows in the cache's namespace and exact profile scope.
func (r *RuntimeProjectionReconciler) findInferenceServicesForProfileCache(
	ctx context.Context,
	obj client.Object,
) []reconcile.Request {
	cache, ok := obj.(*aimv1alpha2.AIMProfileCache)
	if !ok {
		return nil
	}
	profile, err := r.backingProfileForCache(ctx, cache)
	if err != nil {
		log.FromContext(ctx).Error(err, "failed to resolve backing profile for cache event",
			"cache", cache.Name, "namespace", cache.Namespace, "profile", cache.Spec.ProfileName)
	}
	aimID := ""
	if profile != nil {
		aimID = profile.GetProfileSpecCommon().AimId
	}
	requests := r.inferenceServicesReferencingRuntimes(
		ctx,
		cache.Namespace,
		runtimeNamesForProfile(cache.Spec.ProfileName, aimID),
	)
	if profile != nil {
		requests = mergeRequests(requests, r.lazyShadowRequestsForProfile(ctx, profile, cache.Namespace))
	}
	return requests
}

// backingProfileForCache resolves the profile a cache caches, keyed on the
// cache's declared ProfileScope (the inverse of backingProfileScope): a
// cluster-scope cache resolves a cluster AIMClusterProfile; a namespace-scope
// cache (the default when the scope is empty, matching the profilecache
// resolver's normalization) resolves a namespace AIMProfile in the cache's own
// namespace. Resolving strictly by the declared scope — rather than the
// namespace-first lookupBackingProfile — avoids a cluster-scope cache picking up
// a same-named namespace profile (or vice versa). Returns a genuine nil
// interface (never a typed-nil pointer) when the profile is absent.
func (r *RuntimeProjectionReconciler) backingProfileForCache(
	ctx context.Context,
	cache *aimv1alpha2.AIMProfileCache,
) (runtimeprojection.BackingProfile, error) {
	if cache.Spec.ProfileScope == aimv1alpha1.AIMResolutionScopeCluster {
		return r.clusterBackingProfile(ctx, cache.Spec.ProfileName)
	}
	return r.namespaceBackingProfile(ctx, cache.Namespace, cache.Spec.ProfileName)
}

// namespaceBackingProfile resolves a namespace AIMProfile by name in the given
// namespace, returning a genuine nil interface (not a typed-nil pointer) when
// absent. Unlike lookupBackingProfile it does not fall back to a cluster
// profile: callers that already know the scope (a namespace-scope cache) must
// not silently resolve a same-named cluster profile.
func (r *RuntimeProjectionReconciler) namespaceBackingProfile(
	ctx context.Context,
	namespace, name string,
) (runtimeprojection.BackingProfile, error) {
	if name == "" {
		return nil, nil
	}
	var profile aimv1alpha2.AIMProfile
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &profile); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &profile, nil
}

// inferenceServicesReferencingRuntimes returns de-duplicated namespaced
// ServingRuntime keys for active indexed InferenceService consumers. An empty
// namespace widens the lookup cluster-wide.
func (r *RuntimeProjectionReconciler) inferenceServicesReferencingRuntimes(
	ctx context.Context,
	namespace string,
	runtimeNames []string,
) []reconcile.Request {
	requests := map[types.NamespacedName]struct{}{}
	for _, name := range runtimeNames {
		if name == "" {
			continue
		}
		opts := []client.ListOption{client.MatchingFields{inferenceServiceRuntimeIndexKey: name}}
		if namespace != "" {
			opts = append(opts, client.InNamespace(namespace))
		}
		var isvcs servingv1beta1.InferenceServiceList
		if err := r.List(ctx, &isvcs, opts...); err != nil {
			log.FromContext(ctx).Error(err, "failed to list InferenceServices referencing a KServe ServingRuntime or ClusterServingRuntime",
				"runtimeName", name, "namespace", namespace)
			continue
		}
		for i := range isvcs.Items {
			if isvcs.Items[i].DeletionTimestamp != nil {
				continue
			}
			runtimeName := runtimeprojection.ReferencedRuntimeName(&isvcs.Items[i])
			requests[types.NamespacedName{
				Namespace: isvcs.Items[i].Namespace,
				Name:      runtimeName,
			}] = struct{}{}
		}
	}

	return requestsFromSet(requests)
}

func (r *RuntimeProjectionReconciler) lazyShadowRequestsForProfile(
	ctx context.Context,
	profile runtimeprojection.BackingProfile,
	namespace string,
) []reconcile.Request {
	requests := map[types.NamespacedName]struct{}{}
	opts := []client.ListOption{}
	if namespace != "" {
		opts = append(opts, client.InNamespace(namespace))
	}

	var runtimes kservev1alpha1.ServingRuntimeList
	if err := r.List(ctx, &runtimes, opts...); err != nil {
		log.FromContext(ctx).Error(err, "failed to list lazy ServingRuntime shadows",
			"profile", profile.GetName(), "namespace", namespace)
	} else {
		for i := range runtimes.Items {
			r.addOwnedLazyShadowRequest(requests, &runtimes.Items[i], profile)
		}
	}

	var configMaps corev1.ConfigMapList
	if err := r.List(ctx, &configMaps, opts...); err != nil {
		log.FromContext(ctx).Error(err, "failed to list lazy ConfigMap shadows",
			"profile", profile.GetName(), "namespace", namespace)
	} else {
		for i := range configMaps.Items {
			r.addOwnedLazyShadowRequest(requests, &configMaps.Items[i], profile)
		}
	}
	return requestsFromSet(requests)
}

func (r *RuntimeProjectionReconciler) addOwnedLazyShadowRequest(
	requests map[types.NamespacedName]struct{},
	obj client.Object,
	profile runtimeprojection.BackingProfile,
) {
	if !isManagedLazyRuntimeObject(obj) || !ownedByExactProfile(obj, profile) {
		return
	}
	requests[client.ObjectKeyFromObject(obj)] = struct{}{}
}

func ownedByExactProfile(obj client.Object, profile runtimeprojection.BackingProfile) bool {
	kind := ""
	switch profile.(type) {
	case *aimv1alpha2.AIMProfile:
		kind = namespaceProfileKind
	case *aimv1alpha2.AIMClusterProfile:
		kind = clusterProfileKind
	default:
		return false
	}
	for _, ref := range obj.GetOwnerReferences() {
		if ref.APIVersion == aimv1alpha2.GroupVersion.String() &&
			ref.Kind == kind &&
			ref.Name == profile.GetName() &&
			ref.UID == profile.GetUID() {
			return true
		}
	}
	return false
}

func mergeRequests(groups ...[]reconcile.Request) []reconcile.Request {
	requests := map[types.NamespacedName]struct{}{}
	for _, group := range groups {
		for _, request := range group {
			requests[request.NamespacedName] = struct{}{}
		}
	}
	return requestsFromSet(requests)
}

func requestsFromSet(requests map[types.NamespacedName]struct{}) []reconcile.Request {
	keys := make([]types.NamespacedName, 0, len(requests))
	for key := range requests {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Namespace != keys[j].Namespace {
			return keys[i].Namespace < keys[j].Namespace
		}
		return keys[i].Name < keys[j].Name
	})
	out := make([]reconcile.Request, 0, len(keys))
	for _, key := range keys {
		out = append(out, reconcile.Request{NamespacedName: key})
	}
	return out
}

// runtimeNamesForProfile returns the reserved KServe ServingRuntime or
// ClusterServingRuntime names a profile can back: the per-profile name and,
// when the aimId is known, the model-slug primary name. Listing the slug name
// only widens the re-enqueue fan-out, which is harmless when DesiredFor declines
// it.
func runtimeNamesForProfile(profileName, aimID string) []string {
	names := []string{serving.RuntimeName(profileName)}
	if aimID != "" {
		names = append(names, serving.ModelSlugRuntimeName(aimID))
	}
	return names
}

// isManagedRuntimeObject is the stable portion of lazy-shadow identification.
// It intentionally excludes the lazy marker so an update that removes only that
// marker can still map the object after the predicate matched the old value.
func isManagedRuntimeObject(obj client.Object) bool {
	if obj == nil {
		return false
	}
	return strings.HasPrefix(obj.GetName(), serving.RuntimeNamePrefix) &&
		obj.GetLabels()[constants.LabelK8sManagedBy] == constants.LabelValueManagedBy
}

func isManagedLazyRuntimeObject(obj client.Object) bool {
	return isManagedRuntimeObject(obj) && ownedByLazyProjection(obj)
}

// inferenceServiceProjectionPredicate drops InferenceService updates that can't
// change the lazy ServingRuntime shadow. The reconcile reads only the referenced
// ServingRuntime or ClusterServingRuntime name (spec) and the runtime-profile
// annotation, never status, so KServe's frequent status writes are pure churn —
// worst for the never-"complete" cross-scope/Reduced shadows that force-apply on
// every pass. Create/Delete/Generic keep the default (fire).
func inferenceServiceProjectionPredicate() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			if e.ObjectOld == nil || e.ObjectNew == nil {
				return true
			}
			// Spec changes bump generation; the runtime-profile annotation is
			// metadata (no bump) yet drives resolution, so check it too.
			if e.ObjectOld.GetGeneration() != e.ObjectNew.GetGeneration() {
				return true
			}
			return e.ObjectOld.GetAnnotations()[constants.AnnotationRuntimeProfile] !=
				e.ObjectNew.GetAnnotations()[constants.AnnotationRuntimeProfile]
		},
	}
}

// managedRuntimeObjectPredicate restricts SR/ConfigMap events to lazy shadows.
// Testing both update sides preserves a reconcile when drift removes the lazy
// marker itself; the field-manager fallback keeps subsequent events visible
// until the marker is restored. Creates are ignored because the producing
// reconcile already force-applied both siblings.
func managedRuntimeObjectPredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(_ event.CreateEvent) bool { return false },
		DeleteFunc: func(e event.DeleteEvent) bool { return isManagedLazyRuntimeObject(e.Object) },
		UpdateFunc: func(e event.UpdateEvent) bool {
			return isManagedLazyRuntimeObject(e.ObjectOld) || isManagedLazyRuntimeObject(e.ObjectNew)
		},
		GenericFunc: func(_ event.GenericEvent) bool { return false },
	}
}

// clusterProfileProjectionPredicate fires on the AIMClusterProfile changes that
// move what the shadow should contain: a spec change (image / resources /
// affinity source), a readiness transition (covers an ISVC that raced ahead of
// its profile), or a recomputed status.resources / status.resolvedNodeAffinity.
// Deletes fire so existing ServingRuntime keys are enqueued before
// owner-reference garbage collection settles. Cosmetic status writes are
// filtered.
func clusterProfileProjectionPredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc:  func(_ event.CreateEvent) bool { return true },
		DeleteFunc:  func(_ event.DeleteEvent) bool { return true },
		GenericFunc: func(_ event.GenericEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldProfile, ok1 := e.ObjectOld.(*aimv1alpha2.AIMClusterProfile)
			newProfile, ok2 := e.ObjectNew.(*aimv1alpha2.AIMClusterProfile)
			if !ok1 || !ok2 {
				return true
			}
			if oldProfile.Annotations[profileyaml.AnnotationContract] !=
				newProfile.Annotations[profileyaml.AnnotationContract] {
				return true
			}
			return profileProjectionChanged(
				oldProfile.Generation, newProfile.Generation,
				&oldProfile.Status, &newProfile.Status,
			)
		},
	}
}

// profileProjectionPredicate is the namespace-AIMProfile analogue of
// clusterProfileProjectionPredicate: it fires on the spec/status changes that
// move what a namespace-profile-backed lazy shadow should contain, and mirrors
// the cluster predicate's create/delete/generic policy.
func profileProjectionPredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc:  func(_ event.CreateEvent) bool { return true },
		DeleteFunc:  func(_ event.DeleteEvent) bool { return true },
		GenericFunc: func(_ event.GenericEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldProfile, ok1 := e.ObjectOld.(*aimv1alpha2.AIMProfile)
			newProfile, ok2 := e.ObjectNew.(*aimv1alpha2.AIMProfile)
			if !ok1 || !ok2 {
				return true
			}
			if oldProfile.Annotations[profileyaml.AnnotationContract] !=
				newProfile.Annotations[profileyaml.AnnotationContract] {
				return true
			}
			return profileProjectionChanged(
				oldProfile.Generation, newProfile.Generation,
				&oldProfile.Status, &newProfile.Status,
			)
		},
	}
}

// profileProjectionChanged reports whether a profile update moved something the
// lazy shadow reflects: a spec change (generation), a readiness/observed-
// generation transition (covers an ISVC that raced ahead of its profile), or a
// recomputed status.resources / status.resolvedNodeAffinity. Cosmetic status
// writes return false so the watch does not hot-loop. Shared by the cluster and
// namespace profile predicates since both scopes carry the same AIMProfileStatus.
func profileProjectionChanged(oldGen, newGen int64, oldStatus, newStatus *aimv1alpha2.AIMProfileStatus) bool {
	if oldGen != newGen {
		return true
	}
	if oldStatus.Status != newStatus.Status {
		return true
	}
	if oldStatus.ObservedGeneration != newStatus.ObservedGeneration {
		return true
	}
	if !equality.Semantic.DeepEqual(oldStatus.Resources, newStatus.Resources) {
		return true
	}
	return !equality.Semantic.DeepEqual(oldStatus.ResolvedNodeAffinity, newStatus.ResolvedNodeAffinity)
}

// profileCacheProjectionPredicate fires when a cache's spec generation,
// observed generation, readiness, or resolved artifacts change — the signals
// that add, remove, or retarget the profile-owned cache mount on the shadow.
// Generation is load-bearing: profileName, profileScope, and mode all live in
// spec, and an update handler maps both the old and new cache objects to their
// affected runtime keys. ObservedGeneration re-enqueues projections when status
// catches up to a spec change. Cosmetic status writes are filtered to avoid
// hot-loops.
func profileCacheProjectionPredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc:  func(_ event.CreateEvent) bool { return true },
		DeleteFunc:  func(_ event.DeleteEvent) bool { return true },
		GenericFunc: func(_ event.GenericEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldCache, ok1 := e.ObjectOld.(*aimv1alpha2.AIMProfileCache)
			newCache, ok2 := e.ObjectNew.(*aimv1alpha2.AIMProfileCache)
			if !ok1 || !ok2 {
				return true
			}
			if oldCache.Generation != newCache.Generation {
				return true
			}
			if oldCache.Status.ObservedGeneration != newCache.Status.ObservedGeneration {
				return true
			}
			if oldCache.Status.Status != newCache.Status.Status {
				return true
			}
			return !equality.Semantic.DeepEqual(oldCache.Status.Artifacts, newCache.Status.Artifacts)
		},
	}
}
