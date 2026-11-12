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

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimmodel"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimprofile"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimprofileset"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/serving"
)

const (
	profileControllerName = "profile"
)

// AIMProfileReconciler reconciles a namespace-scoped AIMProfile object.
type AIMProfileReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Recorder  record.EventRecorder
	Clientset kubernetes.Interface

	// ProjectionMode governs eager ServingRuntime projection.
	ProjectionMode aimv1alpha2.RuntimeProjectionMode

	// LLMInferenceService enables LLMInferenceServiceConfig projection.
	LLMInferenceService serving.LLMInferenceServiceSupport

	reconciler controllerutils.DomainReconciler[
		*aimv1alpha2.AIMProfile,
		*aimv1alpha2.AIMProfileStatus,
		aimprofile.ProfileFetchResult,
		aimprofile.ProfileObservation,
	]
	pipeline controllerutils.Pipeline[
		*aimv1alpha2.AIMProfile,
		*aimv1alpha2.AIMProfileStatus,
		aimprofile.ProfileFetchResult,
		aimprofile.ProfileObservation,
	]
}

// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimprofiles,verbs=get;list;watch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimprofiles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimprofiles/finalizers,verbs=update
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimprofilecaches,verbs=get;list;watch
// +kubebuilder:rbac:groups=serving.kserve.io,resources=servingruntimes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=serving.kserve.io,resources=llminferenceserviceconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *AIMProfileReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return runTypedReconcile(ctx, r.Client, req, &aimv1alpha2.AIMProfile{}, "Failed to fetch AIMProfile", func(ctx context.Context, profile *aimv1alpha2.AIMProfile) (ctrl.Result, error) {
		// Ensure the role / origin / source-model labels are stamped (or
		// backfilled for user-authored profiles) before the pipeline runs,
		// so AIMProfileSet selectors that filter on these labels see a
		// consistent view at the next event.
		if _, err := aimprofile.EnsureProfileProvenanceLabels(
			ctx, r.Client, profile,
			aimprofile.ProfileRoleLabelValue(profile.Spec.AIMProfileSpecCommon),
			aimprofile.DeriveProfileOrigin(profile),
			aimprofile.SourceModelFromOwnerRefs(profile, profile.Namespace),
		); err != nil {
			return ctrl.Result{}, err
		}
		return r.pipeline.Run(ctx, profile)
	})
}

func (r *AIMProfileReconciler) SetupWithManager(mgr ctrl.Manager) error {
	ctx := context.Background()

	r.reconciler = &aimprofile.ProfileReconciler{
		Client:              mgr.GetClient(),
		Scheme:              r.Scheme,
		ProjectionMode:      r.ProjectionMode,
		LLMInferenceService: r.LLMInferenceService,
	}

	r.pipeline = controllerutils.Pipeline[
		*aimv1alpha2.AIMProfile,
		*aimv1alpha2.AIMProfileStatus,
		aimprofile.ProfileFetchResult,
		aimprofile.ProfileObservation,
	]{
		Client:         mgr.GetClient(),
		StatusClient:   mgr.GetClient().Status(),
		Recorder:       r.Recorder,
		ControllerName: profileControllerName,
		Reconciler:     r.reconciler,
		Scheme:         r.Scheme,
		Clientset:      r.Clientset,
	}
	r.Recorder = mgr.GetEventRecorderFor(r.pipeline.GetFullName())
	r.pipeline.Recorder = r.Recorder

	// Index AIMProfile by aimId and ownership annotations for efficient lookups.
	if err := registerProfileCommonIndexes(
		ctx,
		mgr,
		&aimv1alpha2.AIMProfile{},
		func(profile *aimv1alpha2.AIMProfile) string { return profile.Spec.AimId },
		func(profile *aimv1alpha2.AIMProfile) string {
			return profile.GetAnnotations()[aimprofileset.AnnotationProfileSetUID()]
		},
		func(profile *aimv1alpha2.AIMProfile) string {
			return profile.GetAnnotations()[aimmodel.AnnotationModelUID()]
		},
	); err != nil {
		return err
	}

	// Reconcile profiles when node labels/resources change (GPU availability).
	// TODO: This enqueues all profiles with accelerator requirements on any GPU node change.
	// For clusters with many profiles, consider indexing profiles by accelerator label values
	// and only enqueueing profiles whose labels match the changed node.
	nodeHandler := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, _ client.Object) []reconcile.Request {
		return enqueueRequestsForFilteredObjects(
			ctx,
			func(ctx context.Context) ([]*aimv1alpha2.AIMProfile, error) {
				var profiles aimv1alpha2.AIMProfileList
				if err := r.List(ctx, &profiles); err != nil {
					return nil, err
				}
				items := make([]*aimv1alpha2.AIMProfile, 0, len(profiles.Items))
				for i := range profiles.Items {
					items = append(items, &profiles.Items[i])
				}
				return items, nil
			},
			"failed to list AIMProfiles for Node event",
			func(profile *aimv1alpha2.AIMProfile) bool {
				return aimprofile.HasProfileAcceleratorRequirement(profile.Spec.AIMProfileSpecCommon)
			},
		)
	})

	// A model-slug runtime is elected across every primary profile sharing an
	// aimId. Reconcile the peers when one candidate changes or is deleted so a
	// rank/status transition immediately converges on the new winner. The
	// primary For watch already enqueues the changed profile itself; this map
	// intentionally returns only its peers.
	modelSlugElectionHandler := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		if !r.ProjectionMode.ProjectsModelSlug() {
			return nil
		}
		profile, ok := obj.(*aimv1alpha2.AIMProfile)
		if !ok || profile.Spec.AimId == "" {
			return nil
		}

		var peers aimv1alpha2.AIMProfileList
		if err := r.List(
			ctx,
			&peers,
			client.InNamespace(profile.Namespace),
			client.MatchingFields{aimv1alpha2.ProfileAimIdIndexKey: profile.Spec.AimId},
		); err != nil {
			ctrl.LoggerFrom(ctx).Error(err, "failed to list AIMProfile peers for runtime projection election",
				"namespace", profile.Namespace, "aimId", profile.Spec.AimId)
			return nil
		}

		requests := make([]reconcile.Request, 0, len(peers.Items))
		for i := range peers.Items {
			peer := &peers.Items[i]
			if peer.Name == profile.Name {
				continue
			}
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(peer)})
		}
		return requests
	})

	b := ctrl.NewControllerManagedBy(mgr).
		For(&aimv1alpha2.AIMProfile{}).
		// Re-elect the model-slug owner when a same-model candidate changes or
		// disappears. This same-kind secondary watch fans the event out to peers.
		Watches(&aimv1alpha2.AIMProfile{}, modelSlugElectionHandler).
		// Own both siblings of the eager projection so deleting or editing either
		// the ServingRuntime or its colocated ConfigMap re-enqueues the profile
		// and authoritatively restores the complete pair.
		Owns(&kservev1alpha1.ServingRuntime{}).
		Owns(&corev1.ConfigMap{}).
		Watches(&corev1.Node{}, nodeHandler, builder.WithPredicates(utils.NodeGPUChangePredicate())).
		// Re-project when the profile's cache reaches Ready (or its artifacts
		// change), so the eager per-profile / model-slug runtime gains the cache
		// mount without waiting for an unrelated reconcile. This covers a
		// service-driven Shared cache (which the profile does not own, so Owns
		// would never fire) and mirrors the lazy shadow's own cache watch, so both
		// projection paths pick up a late-Ready cache promptly.
		Watches(
			&aimv1alpha2.AIMProfileCache{},
			handler.EnqueueRequestsFromMapFunc(findProfilesForProfileCache),
			builder.WithPredicates(profileCacheProjectionPredicate()),
		)
	// The kind can only be watched when its CRD is installed.
	if r.LLMInferenceService.Enabled {
		b = b.Owns(&kservev1alpha2.LLMInferenceServiceConfig{})
	}
	return b.Named(profileControllerName).Complete(r)
}

// findProfilesForProfileCache maps a changed AIMProfileCache to the namespace
// AIMProfile it caches — the profile named by spec.profileName in the cache's
// own namespace — so a late-Ready (or artifact-changed) cache re-triggers the
// eager runtime projection that mounts it. Only namespace-scope caches are
// mapped: a cluster-scope cache backs an AIMClusterProfile, whose eager runtime
// is a bare CSR that mounts no cache (the lazy shadow mounts it per-namespace
// instead).
func findProfilesForProfileCache(_ context.Context, obj client.Object) []reconcile.Request {
	cache, ok := obj.(*aimv1alpha2.AIMProfileCache)
	if !ok || cache.Spec.ProfileName == "" {
		return nil
	}
	if scope := cache.Spec.ProfileScope; scope != "" && scope != aimv1alpha1.AIMResolutionScopeNamespace {
		return nil
	}
	return []reconcile.Request{{
		NamespacedName: types.NamespacedName{Namespace: cache.Namespace, Name: cache.Spec.ProfileName},
	}}
}
