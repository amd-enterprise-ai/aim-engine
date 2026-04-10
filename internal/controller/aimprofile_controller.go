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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/aimprofile"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
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
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch

func (r *AIMProfileReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var profile aimv1alpha2.AIMProfile
	if err := r.Get(ctx, req.NamespacedName, &profile); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to fetch AIMProfile")
		return ctrl.Result{}, err
	}

	return r.pipeline.Run(ctx, &profile)
}

func (r *AIMProfileReconciler) SetupWithManager(mgr ctrl.Manager) error {
	ctx := context.Background()

	r.reconciler = &aimprofile.ProfileReconciler{
		Client: mgr.GetClient(),
		Scheme: r.Scheme,
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

	// Index AIMProfile by aimId for efficient profile selection lookups.
	if err := mgr.GetFieldIndexer().IndexField(ctx, &aimv1alpha2.AIMProfile{}, aimv1alpha2.ProfileAimIdIndexKey, func(obj client.Object) []string {
		profile, ok := obj.(*aimv1alpha2.AIMProfile)
		if !ok {
			return nil
		}
		return []string{profile.Spec.AimId}
	}); err != nil {
		return err
	}

	// Reconcile profiles when node labels/resources change (GPU availability).
	// TODO: This enqueues all profiles with accelerator requirements on any GPU node change.
	// For clusters with many profiles, consider indexing profiles by accelerator label values
	// and only enqueueing profiles whose labels match the changed node.
	nodeHandler := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, _ client.Object) []reconcile.Request {
		var profiles aimv1alpha2.AIMProfileList
		if err := r.List(ctx, &profiles); err != nil {
			log.FromContext(ctx).Error(err, "failed to list AIMProfiles for Node event")
			return nil
		}

		requests := make([]reconcile.Request, 0, len(profiles.Items))
		for _, p := range profiles.Items {
			if aimprofile.HasAcceleratorRequirement(p.Spec.AcceleratorModel, p.Spec.AcceleratorCount, p.Spec.Resources) {
				requests = append(requests, reconcile.Request{
					NamespacedName: client.ObjectKeyFromObject(&p),
				})
			}
		}
		return requests
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(&aimv1alpha2.AIMProfile{}).
		Watches(&corev1.Node{}, nodeHandler, builder.WithPredicates(utils.NodeGPUChangePredicate())).
		Named(profileControllerName).
		Complete(r)
}
