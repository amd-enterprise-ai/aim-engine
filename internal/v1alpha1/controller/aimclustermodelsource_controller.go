/*
MIT License

Copyright (c) 2025 Advanced Micro Devices, Inc.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package controller

import (
	"context"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/aimclustermodelsource"
)

const clusterModelSourceName = "cluster-model-source"

// minRegistrySyncInterval is the minimum interval between registry listings per
// source, regardless of what triggers a reconcile.
const minRegistrySyncInterval = 30 * time.Second

// AIMClusterModelSourceReconciler reconciles an AIMClusterModelSource object.
type AIMClusterModelSourceReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Recorder  record.EventRecorder
	Clientset kubernetes.Interface

	// Pipeline and domain reconciler (initialized in SetupWithManager)
	reconciler controllerutils.DomainReconciler[
		*aimv1alpha1.AIMClusterModelSource,
		*aimv1alpha1.AIMClusterModelSourceStatus,
		aimclustermodelsource.ClusterModelSourceFetch,
		aimclustermodelsource.ClusterModelSourceObservation,
	]
	pipeline controllerutils.Pipeline[
		*aimv1alpha1.AIMClusterModelSource,
		*aimv1alpha1.AIMClusterModelSourceStatus,
		aimclustermodelsource.ClusterModelSourceFetch,
		aimclustermodelsource.ClusterModelSourceObservation,
	]

	// syncMu guards lastSyncAttempt, the in-memory time each source last attempted a
	// registry listing. In-memory so the floor also covers the error path.
	syncMu          sync.Mutex
	lastSyncAttempt map[string]time.Time
}

// reserveSyncSlot atomically checks the per-source registry floor. It returns 0
// and records the attempt when a sync is allowed now, or the remaining wait
// (without recording) when the source synced too recently.
func (r *AIMClusterModelSourceReconciler) reserveSyncSlot(name string) time.Duration {
	r.syncMu.Lock()
	defer r.syncMu.Unlock()
	if r.lastSyncAttempt == nil {
		r.lastSyncAttempt = make(map[string]time.Time)
	}
	if last, ok := r.lastSyncAttempt[name]; ok {
		if wait := minRegistrySyncInterval - time.Since(last); wait > 0 {
			return wait
		}
	}
	r.lastSyncAttempt[name] = time.Now()
	return 0
}

// forgetSyncAttempt drops a deleted source's entry so the map does not grow unbounded.
func (r *AIMClusterModelSourceReconciler) forgetSyncAttempt(name string) {
	r.syncMu.Lock()
	defer r.syncMu.Unlock()
	delete(r.lastSyncAttempt, name)
}

// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclustermodelsources,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclustermodelsources/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclustermodelsources/finalizers,verbs=update
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclustermodels,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *AIMClusterModelSourceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var source aimv1alpha1.AIMClusterModelSource
	if err := r.Get(ctx, req.NamespacedName, &source); err != nil {
		if apierrors.IsNotFound(err) {
			r.forgetSyncAttempt(req.Name)
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to fetch AIMClusterModelSource")
		return ctrl.Result{}, err
	}

	syncInterval := source.Spec.SyncInterval.Duration
	if syncInterval == 0 {
		syncInterval = aimv1alpha1.DefaultSyncInterval
	}

	// Cap registry listings at one per minRegistrySyncInterval per source. The
	// remaining wait is requeued so a throttled event still lands.
	if wait := r.reserveSyncSlot(source.Name); wait > 0 {
		logger.V(1).Info("registry sync throttled", "remaining", wait.String())
		return ctrl.Result{RequeueAfter: wait}, nil
	}

	result, err := r.pipeline.Run(ctx, &source)
	if err != nil {
		return ctrl.Result{}, err
	}

	logger.V(1).Info("synced cluster model source", "discoveredModels", source.Status.DiscoveredModels)

	if result.RequeueAfter > 0 {
		return result, nil
	}
	return ctrl.Result{RequeueAfter: syncInterval}, nil
}

func (r *AIMClusterModelSourceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.reconciler = &aimclustermodelsource.ClusterModelSourceReconciler{
		Clientset:         r.Clientset,
		Scheme:            r.Scheme,
		OperatorNamespace: constants.GetOperatorNamespace(),
	}
	r.pipeline = controllerutils.Pipeline[
		*aimv1alpha1.AIMClusterModelSource,
		*aimv1alpha1.AIMClusterModelSourceStatus,
		aimclustermodelsource.ClusterModelSourceFetch,
		aimclustermodelsource.ClusterModelSourceObservation,
	]{
		Client:         mgr.GetClient(),
		StatusClient:   mgr.GetClient().Status(),
		Reconciler:     r.reconciler,
		Scheme:         r.Scheme,
		ControllerName: clusterModelSourceName,
	}
	r.Recorder = mgr.GetEventRecorderFor(r.pipeline.GetFullName())
	r.pipeline.Recorder = r.Recorder

	return ctrl.NewControllerManagedBy(mgr).
		// Drop the source's own status-only writes; spec changes, creates and deletes still pass.
		For(&aimv1alpha1.AIMClusterModelSource{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		// Re-create auto-generated models that are deleted or whose image changed.
		Watches(
			&aimv1alpha1.AIMClusterModel{},
			handler.EnqueueRequestsFromMapFunc(r.enqueueAllSources),
			builder.WithPredicates(autoGeneratedClusterModelPredicate()),
		).
		// When another source changes or is deleted, re-add models other sources also matched.
		Watches(
			&aimv1alpha1.AIMClusterModelSource{},
			handler.EnqueueRequestsFromMapFunc(r.enqueueOtherSources),
			builder.WithPredicates(clusterModelSourceChangePredicate()),
		).
		Named(clusterModelSourceName).
		Complete(r)
}

// enqueueAllSources enqueues every source; each pipeline decides whether it owns
// the affected image on its next run.
func (r *AIMClusterModelSourceReconciler) enqueueAllSources(ctx context.Context, _ client.Object) []reconcile.Request {
	return r.listSourceRequests(ctx, "")
}

// enqueueOtherSources enqueues every source except the one that triggered the
// event (its own changes are handled by the For watch).
func (r *AIMClusterModelSourceReconciler) enqueueOtherSources(ctx context.Context, obj client.Object) []reconcile.Request {
	return r.listSourceRequests(ctx, obj.GetName())
}

// listSourceRequests lists all sources and returns reconcile requests, skipping
// the source named by exclude (empty string excludes nothing).
func (r *AIMClusterModelSourceReconciler) listSourceRequests(ctx context.Context, exclude string) []reconcile.Request {
	var sources aimv1alpha1.AIMClusterModelSourceList
	if err := r.List(ctx, &sources); err != nil {
		log.FromContext(ctx).Error(err, "failed to list AIMClusterModelSources for watch event")
		return nil
	}
	requests := make([]reconcile.Request, 0, len(sources.Items))
	for i := range sources.Items {
		if sources.Items[i].Name == exclude {
			continue
		}
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: sources.Items[i].Name},
		})
	}
	return requests
}

// autoGeneratedClusterModelPredicate fires only on deletion or spec change of
// auto-generated models; creates and status-only updates are ignored.
func autoGeneratedClusterModelPredicate() predicate.Predicate {
	isAutoGenerated := func(obj client.Object) bool {
		return obj.GetLabels()[constants.LabelKeyOrigin] == constants.LabelValueOriginAutoGenerated
	}
	return predicate.Funcs{
		CreateFunc: func(event.CreateEvent) bool { return false },
		DeleteFunc: func(e event.DeleteEvent) bool { return isAutoGenerated(e.Object) },
		UpdateFunc: func(e event.UpdateEvent) bool {
			return (isAutoGenerated(e.ObjectOld) || isAutoGenerated(e.ObjectNew)) &&
				e.ObjectOld.GetGeneration() != e.ObjectNew.GetGeneration()
		},
		GenericFunc: func(event.GenericEvent) bool { return false },
	}
}

// clusterModelSourceChangePredicate fires only on deletions and spec changes;
// creates and status-only updates are ignored.
func clusterModelSourceChangePredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(event.CreateEvent) bool { return false },
		DeleteFunc: func(event.DeleteEvent) bool { return true },
		UpdateFunc: func(e event.UpdateEvent) bool {
			return e.ObjectOld.GetGeneration() != e.ObjectNew.GetGeneration()
		},
		GenericFunc: func(event.GenericEvent) bool { return false },
	}
}
