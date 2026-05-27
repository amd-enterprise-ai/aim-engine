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
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
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
		Client: mgr.GetClient(),
		Scheme: r.Scheme,
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
				return aimprofile.HasAcceleratorRequirement(profile.Spec.AcceleratorModel, profile.Spec.AcceleratorCount, profile.Spec.Resources)
			},
		)
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(&aimv1alpha2.AIMClusterProfile{}).
		Watches(&corev1.Node{}, nodeHandler, builder.WithPredicates(utils.NodeGPUChangePredicate())).
		Named(clusterProfileControllerName).
		Complete(r)
}
