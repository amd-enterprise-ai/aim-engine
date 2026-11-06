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
	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimmodel"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimprofile"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimprofileset"
)

const (
	clusterProfileControllerName = "cluster-profile"
)

// AIMClusterProfileReconciler reconciles a cluster-scoped AIMClusterProfile object.
type AIMClusterProfileReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Recorder  record.EventRecorder
	Clientset kubernetes.Interface

	// ProjectionMode governs eager ClusterServingRuntime projection.
	ProjectionMode aimv1alpha2.RuntimeProjectionMode

	reconciler controllerutils.DomainReconciler[
		*aimv1alpha2.AIMClusterProfile,
		*aimv1alpha2.AIMProfileStatus,
		aimprofile.ClusterProfileFetchResult,
		aimprofile.ClusterProfileObservation,
	]
	pipeline controllerutils.Pipeline[
		*aimv1alpha2.AIMClusterProfile,
		*aimv1alpha2.AIMProfileStatus,
		aimprofile.ClusterProfileFetchResult,
		aimprofile.ClusterProfileObservation,
	]
}

// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclusterprofiles,verbs=get;list;watch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclusterprofiles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclusterprofiles/finalizers,verbs=update
// +kubebuilder:rbac:groups=serving.kserve.io,resources=clusterservingruntimes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups=resource.k8s.io,resources=resourceslices,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *AIMClusterProfileReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return runTypedReconcile(ctx, r.Client, req, &aimv1alpha2.AIMClusterProfile{}, "Failed to fetch AIMClusterProfile", func(ctx context.Context, profile *aimv1alpha2.AIMClusterProfile) (ctrl.Result, error) {
		// Ensure the role / origin / source-model labels are stamped (or
		// backfilled for user-authored profiles) before the pipeline runs,
		// so AIMProfileSet selectors that filter on these labels see a
		// consistent view at the next event.
		if _, err := aimprofile.EnsureProfileProvenanceLabels(
			ctx, r.Client, profile,
			aimprofile.ProfileRoleLabelValue(profile.Spec.AIMProfileSpecCommon),
			aimprofile.DeriveProfileOrigin(profile),
			aimprofile.SourceModelFromOwnerRefs(profile, ""),
		); err != nil {
			return ctrl.Result{}, err
		}
		return r.pipeline.Run(ctx, profile)
	})
}

func (r *AIMClusterProfileReconciler) SetupWithManager(mgr ctrl.Manager) error {
	ctx := context.Background()

	r.reconciler = &aimprofile.ClusterProfileReconciler{
		Client:         mgr.GetClient(),
		Scheme:         r.Scheme,
		ProjectionMode: r.ProjectionMode,
	}

	r.pipeline = controllerutils.Pipeline[
		*aimv1alpha2.AIMClusterProfile,
		*aimv1alpha2.AIMProfileStatus,
		aimprofile.ClusterProfileFetchResult,
		aimprofile.ClusterProfileObservation,
	]{
		Client:         mgr.GetClient(),
		StatusClient:   mgr.GetClient().Status(),
		Recorder:       r.Recorder,
		ControllerName: clusterProfileControllerName,
		Reconciler:     r.reconciler,
		Scheme:         r.Scheme,
		Clientset:      r.Clientset,
	}
	r.Recorder = mgr.GetEventRecorderFor(r.pipeline.GetFullName())
	r.pipeline.Recorder = r.Recorder

	// Index AIMClusterProfile by aimId and ownership annotations for efficient lookups.
	if err := registerProfileCommonIndexes(
		ctx,
		mgr,
		&aimv1alpha2.AIMClusterProfile{},
		func(profile *aimv1alpha2.AIMClusterProfile) string { return profile.Spec.AimId },
		func(profile *aimv1alpha2.AIMClusterProfile) string {
			return profile.GetAnnotations()[aimprofileset.AnnotationProfileSetUID()]
		},
		func(profile *aimv1alpha2.AIMClusterProfile) string {
			return profile.GetAnnotations()[aimmodel.AnnotationModelUID()]
		},
	); err != nil {
		return err
	}

	// Reconcile cluster profiles when node labels/resources change.
	// TODO: This enqueues all cluster profiles with accelerator requirements on any GPU node
	// change. For clusters with many profiles, consider indexing by accelerator label values.
	nodeHandler := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, _ client.Object) []reconcile.Request {
		return enqueueRequestsForFilteredObjects(
			ctx,
			func(ctx context.Context) ([]*aimv1alpha2.AIMClusterProfile, error) {
				var profiles aimv1alpha2.AIMClusterProfileList
				if err := r.List(ctx, &profiles); err != nil {
					return nil, err
				}
				items := make([]*aimv1alpha2.AIMClusterProfile, 0, len(profiles.Items))
				for i := range profiles.Items {
					items = append(items, &profiles.Items[i])
				}
				return items, nil
			},
			"failed to list AIMClusterProfiles for Node event",
			func(profile *aimv1alpha2.AIMClusterProfile) bool {
				return aimprofile.HasProfileAcceleratorRequirement(profile.Spec.AIMProfileSpecCommon)
			},
		)
	})

	// Keep the cluster model-slug election converged when a same-aimId
	// candidate changes rank/status or is deleted. The primary For watch handles
	// the changed object; this secondary same-kind watch enqueues only its peers.
	modelSlugElectionHandler := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		if !r.ProjectionMode.ProjectsModelSlug() {
			return nil
		}
		profile, ok := obj.(*aimv1alpha2.AIMClusterProfile)
		if !ok || profile.Spec.AimId == "" {
			return nil
		}

		var peers aimv1alpha2.AIMClusterProfileList
		if err := r.List(
			ctx,
			&peers,
			client.MatchingFields{aimv1alpha2.ProfileAimIdIndexKey: profile.Spec.AimId},
		); err != nil {
			ctrl.LoggerFrom(ctx).Error(err, "failed to list AIMClusterProfile peers for runtime projection election",
				"aimId", profile.Spec.AimId)
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
		For(&aimv1alpha2.AIMClusterProfile{}).
		// Re-elect the model-slug owner when a same-model candidate changes or
		// disappears. This same-kind secondary watch fans the event out to peers.
		Watches(&aimv1alpha2.AIMClusterProfile{}, modelSlugElectionHandler).
		// Own the projected ClusterServingRuntime so drift (hand-edits) and
		// node-inventory changes (via the Node watch) self-heal through reconcile.
		Owns(&kservev1alpha1.ClusterServingRuntime{}).
		Watches(&corev1.Node{}, nodeHandler, builder.WithPredicates(utils.NodeGPUChangePredicate()))
	// AMD GPU DRA ResourceSlices change which nodes match, like node changes.
	if utils.ResourceSliceAPIAvailable(mgr.GetRESTMapper()) {
		b = b.Watches(&resourcev1.ResourceSlice{}, nodeHandler, builder.WithPredicates(utils.AMDGPUResourceSlicePredicate()))
	}
	return b.Named(clusterProfileControllerName).Complete(r)
}
