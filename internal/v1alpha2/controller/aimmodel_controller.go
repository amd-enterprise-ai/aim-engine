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

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/imagemetadata"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/aimruntimeconfig"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimmodel"
)

const (
	modelControllerName               = "model"
	modelProfilesCleanupFinalizerName = "aim.eai.amd.com/model-profiles-cleanup"

	// legacyModelProfilesCleanupFinalizerName is an older finalizer name that
	// shipped while a separate "legacy mode" code path existed. We still
	// recognise it on existing objects so we can strip it during normal
	// reconcile or deletion, but we never add it.
	legacyModelProfilesCleanupFinalizerName = "aim.eai.amd.com/model-cluster-profiles-cleanup"
)

// AIMModelReconciler reconciles a v1alpha2 AIMModel object.
type AIMModelReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Recorder  record.EventRecorder
	Clientset kubernetes.Interface

	reconciler controllerutils.DomainReconciler[
		*aimv1alpha2.AIMModel,
		*aimv1alpha1.AIMModelStatus,
		aimmodel.ModelFetchResult,
		aimmodel.ModelObservation,
	]
	pipeline controllerutils.Pipeline[
		*aimv1alpha2.AIMModel,
		*aimv1alpha1.AIMModelStatus,
		aimmodel.ModelFetchResult,
		aimmodel.ModelObservation,
	]
}

// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimmodels,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimmodels/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimmodels/finalizers,verbs=update
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimprofilesets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimprofiles,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimruntimeconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclusterruntimeconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods/log,verbs=get
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete

func (r *AIMModelReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var model aimv1alpha2.AIMModel
	if err := r.Get(ctx, req.NamespacedName, &model); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to fetch AIMModel")
		return ctrl.Result{}, err
	}

	if model.DeletionTimestamp != nil {
		if hasModelProfilesCleanupFinalizer(&model) {
			if err := r.cleanupManagedProfiles(ctx, model.Namespace, string(model.UID)); err != nil {
				logger.Error(err, "Failed to cleanup AIMModel-managed profiles")
				return ctrl.Result{}, err
			}
			if err := r.cleanupDiscoveryCache(ctx, model.Namespace, model.Name); err != nil {
				logger.Error(err, "Failed to cleanup AIMModel discovery cache")
				return ctrl.Result{}, err
			}
			removeModelProfilesCleanupFinalizers(&model)
			if err := r.Update(ctx, &model); err != nil {
				if apierrors.IsNotFound(err) {
					return ctrl.Result{}, nil
				}
				if apierrors.IsConflict(err) {
					return ctrl.Result{Requeue: true}, nil
				}
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Strip the older legacy-mode finalizer (if present) and ensure the
	// canonical one is set. The native pipeline always runs, so we always
	// need the cleanup finalizer to GC AIMProfiles on deletion.
	finalizerChanged := false
	if controllerutil.ContainsFinalizer(&model, legacyModelProfilesCleanupFinalizerName) {
		controllerutil.RemoveFinalizer(&model, legacyModelProfilesCleanupFinalizerName)
		finalizerChanged = true
	}
	if !controllerutil.ContainsFinalizer(&model, modelProfilesCleanupFinalizerName) {
		controllerutil.AddFinalizer(&model, modelProfilesCleanupFinalizerName)
		finalizerChanged = true
	}
	if finalizerChanged {
		if err := r.Update(ctx, &model); err != nil {
			if apierrors.IsConflict(err) {
				return ctrl.Result{Requeue: true}, nil
			}
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// When the spec sets an image we may create a discovery Job; serialize
	// the "count + create" with other discovery producers via the shared
	// Lease-backed lock so the global cap holds. The registry is read before
	// the lock is taken.
	if model.Spec.Image != "" {
		ctx := imagemetadata.WithFetchResult(ctx,
			imagemetadata.Fetch(ctx, r.Clientset, model.Spec, &model.Status, model.Namespace))
		return runWithDiscoveryLock(ctx, r.Client, func() (ctrl.Result, error) {
			return r.pipeline.Run(ctx, &model)
		})
	}

	return r.pipeline.Run(ctx, &model)
}

func hasModelProfilesCleanupFinalizer(model *aimv1alpha2.AIMModel) bool {
	return controllerutil.ContainsFinalizer(model, modelProfilesCleanupFinalizerName) ||
		controllerutil.ContainsFinalizer(model, legacyModelProfilesCleanupFinalizerName)
}

func removeModelProfilesCleanupFinalizers(model *aimv1alpha2.AIMModel) {
	controllerutil.RemoveFinalizer(model, modelProfilesCleanupFinalizerName)
	controllerutil.RemoveFinalizer(model, legacyModelProfilesCleanupFinalizerName)
}

func (r *AIMModelReconciler) SetupWithManager(mgr ctrl.Manager) error {
	ctx := context.Background()

	r.Recorder = mgr.GetEventRecorderFor("aim-" + modelControllerName + "-controller")
	r.reconciler = &aimmodel.ModelReconciler{Clientset: r.Clientset}
	r.pipeline = controllerutils.Pipeline[
		*aimv1alpha2.AIMModel,
		*aimv1alpha1.AIMModelStatus,
		aimmodel.ModelFetchResult,
		aimmodel.ModelObservation,
	]{
		Client:         mgr.GetClient(),
		StatusClient:   mgr.GetClient().Status(),
		Recorder:       r.Recorder,
		ControllerName: modelControllerName,
		Reconciler:     r.reconciler,
		Scheme:         r.Scheme,
		Clientset:      r.Clientset,
		// The field manager was renamed from "model-v1alpha2" to "model"; force
		// so the renamed manager reclaims fields still owned by the old name.
		ForceApply: true,
	}

	if err := mgr.GetFieldIndexer().IndexField(ctx, &aimv1alpha2.AIMModel{}, aimv1alpha1.ModelImageIndexKey, func(obj client.Object) []string {
		model, ok := obj.(*aimv1alpha2.AIMModel)
		if !ok || model.Spec.Image == "" {
			return nil
		}
		return []string{model.Spec.Image}
	}); err != nil {
		return err
	}
	// Models that name no runtime config are indexed under "default", not
	// skipped: modelId-backed models read their profile-generation fallbacks
	// from the default config, so an edit there must reach them.
	//
	// Fan-out this widens: an edit to the *cluster* default AIMClusterRuntimeConfig
	// now enqueues every AIMModel in the cluster (findModelsForClusterRuntimeConfig
	// has no namespace filter), where before it enqueued only models naming a
	// config explicitly. Namespace-scoped configs are unaffected —
	// findModelsForRuntimeConfig filters by config.Namespace. RuntimeConfig
	// edits are rare and controller-runtime dedups the queue, so this is
	// acceptable; it is worth knowing before adding a hot write path to
	// RuntimeConfig status.
	if err := mgr.GetFieldIndexer().IndexField(ctx, &aimv1alpha2.AIMModel{}, aimv1alpha1.ModelRuntimeConfigIndexKey, func(obj client.Object) []string {
		model, ok := obj.(*aimv1alpha2.AIMModel)
		if !ok {
			return nil
		}
		name := model.Spec.Name
		if name == "" {
			name = aimruntimeconfig.DefaultRuntimeConfigName
		}
		return []string{name}
	}); err != nil {
		return err
	}

	childHandler := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		return directModelRequest(obj)
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(&aimv1alpha2.AIMModel{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&batchv1.Job{}).
		Owns(&aimv1alpha2.AIMProfileSet{}).
		Watches(
			&aimv1alpha1.AIMRuntimeConfig{},
			handler.EnqueueRequestsFromMapFunc(r.findModelsForRuntimeConfig),
		).
		Watches(
			&aimv1alpha1.AIMClusterRuntimeConfig{},
			handler.EnqueueRequestsFromMapFunc(r.findModelsForClusterRuntimeConfig),
		).
		Watches(
			&corev1.Node{},
			handler.EnqueueRequestsFromMapFunc(r.findModelsForNodeChange),
			builder.WithPredicates(utils.NodeGPUChangePredicate()),
		).
		Watches(
			&aimv1alpha2.AIMProfile{},
			childHandler,
			builder.WithPredicates(predicate.NewPredicateFuncs(hasDirectModelOwnerAnnotations)),
		).
		Named(modelControllerName).
		Complete(r)
}

func (r *AIMModelReconciler) cleanupManagedProfiles(ctx context.Context, namespace, modelUID string) error {
	var profiles aimv1alpha2.AIMProfileList
	if err := r.List(ctx, &profiles, client.InNamespace(namespace), client.MatchingFields{aimmodel.ManagedModelUIDIndexKey: modelUID}); err != nil {
		return err
	}
	for i := range profiles.Items {
		if err := r.Delete(ctx, &profiles.Items[i]); client.IgnoreNotFound(err) != nil {
			return err
		}
	}
	return nil
}

func (r *AIMModelReconciler) cleanupDiscoveryCache(ctx context.Context, namespace, modelName string) error {
	cacheName, err := aimmodel.DiscoveryCacheName(modelName)
	if err != nil {
		return err
	}
	cache := &corev1.ConfigMap{}
	if err := r.Get(ctx, types.NamespacedName{Name: cacheName, Namespace: namespace}, cache); err != nil {
		return client.IgnoreNotFound(err)
	}
	return client.IgnoreNotFound(r.Delete(ctx, cache))
}

func directModelRequest(obj client.Object) []reconcile.Request {
	annotations := obj.GetAnnotations()
	if len(annotations) == 0 {
		return nil
	}
	name := annotations[aimmodel.AnnotationModelName()]
	namespace := annotations[aimmodel.AnnotationModelNamespace()]
	if name == "" || namespace == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: client.ObjectKey{Name: name, Namespace: namespace}}}
}

func hasDirectModelOwnerAnnotations(obj client.Object) bool {
	annotations := obj.GetAnnotations()
	return annotations[aimmodel.AnnotationModelName()] != "" && annotations[aimmodel.AnnotationModelNamespace()] != ""
}

func (r *AIMModelReconciler) findModelsForRuntimeConfig(ctx context.Context, obj client.Object) []reconcile.Request {
	config, ok := obj.(*aimv1alpha1.AIMRuntimeConfig)
	if !ok {
		return nil
	}
	var models aimv1alpha2.AIMModelList
	if err := r.List(ctx, &models,
		client.InNamespace(config.Namespace),
		client.MatchingFields{aimv1alpha1.ModelRuntimeConfigIndexKey: config.Name},
	); err != nil {
		log.FromContext(ctx).Error(err, "failed to list AIMModels for RuntimeConfig", "config", config.Name)
		return nil
	}
	requests := make([]reconcile.Request, len(models.Items))
	for i, model := range models.Items {
		requests[i] = reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      model.Name,
				Namespace: model.Namespace,
			},
		}
	}
	return requests
}

func (r *AIMModelReconciler) findModelsForClusterRuntimeConfig(ctx context.Context, obj client.Object) []reconcile.Request {
	config, ok := obj.(*aimv1alpha1.AIMClusterRuntimeConfig)
	if !ok {
		return nil
	}
	var models aimv1alpha2.AIMModelList
	if err := r.List(ctx, &models, client.MatchingFields{aimv1alpha1.ModelRuntimeConfigIndexKey: config.Name}); err != nil {
		log.FromContext(ctx).Error(err, "failed to list AIMModels for ClusterRuntimeConfig", "config", config.Name)
		return nil
	}
	requests := make([]reconcile.Request, len(models.Items))
	for i, model := range models.Items {
		requests[i] = reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      model.Name,
				Namespace: model.Namespace,
			},
		}
	}
	return requests
}

func (r *AIMModelReconciler) findModelsForNodeChange(ctx context.Context, obj client.Object) []reconcile.Request {
	if _, ok := obj.(*corev1.Node); !ok {
		return nil
	}

	var models aimv1alpha2.AIMModelList
	if err := r.List(ctx, &models); err != nil {
		log.FromContext(ctx).Error(err, "failed to list AIMModels for Node event")
		return nil
	}

	requests := make([]reconcile.Request, 0, len(models.Items))
	for i := range models.Items {
		if !modelRequiresNodeReconcile(&models.Items[i]) {
			continue
		}
		requests = append(requests, reconcile.Request{
			NamespacedName: client.ObjectKeyFromObject(&models.Items[i]),
		})
	}
	return requests
}

// modelRequiresNodeReconcile returns true for AIMModels whose plan can be
// affected by node-label changes. Image-backed discovery and modelId-backed
// profile generation both filter desired profiles against node hardware.
func modelRequiresNodeReconcile(model *aimv1alpha2.AIMModel) bool {
	return model != nil && (model.Spec.Image != "" || model.Spec.ModelID != "")
}

func dedupeRequests(requests []reconcile.Request) []reconcile.Request {
	if len(requests) < 2 {
		return requests
	}
	seen := make(map[client.ObjectKey]struct{}, len(requests))
	result := make([]reconcile.Request, 0, len(requests))
	for _, req := range requests {
		if _, exists := seen[req.NamespacedName]; exists {
			continue
		}
		seen[req.NamespacedName] = struct{}{}
		result = append(result, req)
	}
	return result
}
