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
	"time"

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
	discoverylock "github.com/amd-enterprise-ai/aim-engine/internal/discovery/lock"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
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
	// Lease-backed lock so the global cap holds.
	if model.Spec.Image != "" {
		var result ctrl.Result
		lockErr := discoverylock.WithDiscoveryLock(ctx, r.Client, 30*time.Second, func() error {
			var err error
			result, err = r.pipeline.Run(ctx, &model)
			return err
		})
		if lockErr != nil {
			logger.V(1).Info("could not acquire discovery lock, requeuing", "model", model.Name)
			return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
		}
		return result, nil
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
	r.reconciler = &aimmodel.ModelReconciler{
		Client:    mgr.GetClient(),
		Clientset: r.Clientset,
		Scheme:    r.Scheme,
	}
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
	}

	if err := mgr.GetFieldIndexer().IndexField(ctx, &aimv1alpha1.AIMServiceTemplate{}, aimv1alpha1.ServiceTemplateModelNameIndexKey, func(obj client.Object) []string {
		template, ok := obj.(*aimv1alpha1.AIMServiceTemplate)
		if !ok {
			return nil
		}
		return []string{template.Spec.ModelName}
	}); err != nil {
		return err
	}
	// Index AIMServiceTemplate / AIMClusterServiceTemplate by aimId so the
	// legacy fine-tuned matching path (driven from this v1alpha2 controller's
	// reconciler via the legacy package) can resolve template candidates by
	// aimId across both scopes. Without these indexes the
	// `client.MatchingFields{ServiceTemplateAimIdIndexKey: ...}` lookups in
	// FetchRemoteState silently return empty, fine-tuned models match nothing,
	// and no namespace-scoped fine-tuned template copies get applied.
	if err := mgr.GetFieldIndexer().IndexField(ctx, &aimv1alpha1.AIMServiceTemplate{}, aimv1alpha1.ServiceTemplateAimIdIndexKey, func(obj client.Object) []string {
		template, ok := obj.(*aimv1alpha1.AIMServiceTemplate)
		if !ok || template.Spec.AimId == "" {
			return nil
		}
		return []string{template.Spec.AimId}
	}); err != nil {
		return err
	}
	if err := mgr.GetFieldIndexer().IndexField(ctx, &aimv1alpha1.AIMClusterServiceTemplate{}, aimv1alpha1.ServiceTemplateAimIdIndexKey, func(obj client.Object) []string {
		template, ok := obj.(*aimv1alpha1.AIMClusterServiceTemplate)
		if !ok || template.Spec.AimId == "" {
			return nil
		}
		return []string{template.Spec.AimId}
	}); err != nil {
		return err
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
	if err := mgr.GetFieldIndexer().IndexField(ctx, &aimv1alpha2.AIMModel{}, aimv1alpha1.ModelRuntimeConfigIndexKey, func(obj client.Object) []string {
		model, ok := obj.(*aimv1alpha2.AIMModel)
		if !ok || model.Spec.Name == "" {
			return nil
		}
		return []string{model.Spec.Name}
	}); err != nil {
		return err
	}

	// Index AIMModel by aimId for reverse lookup so fine-tuned consumers
	// re-reconcile when a source AIMServiceTemplate's aimId is late-bound.
	if err := mgr.GetFieldIndexer().IndexField(ctx, &aimv1alpha2.AIMModel{}, aimv1alpha1.ModelAimIdIndexKey, func(obj client.Object) []string {
		model, ok := obj.(*aimv1alpha2.AIMModel)
		if !ok || model.Spec.AimId == "" {
			return nil
		}
		return []string{model.Spec.AimId}
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
		Owns(&aimv1alpha1.AIMServiceTemplate{}).
		Owns(&aimv1alpha2.AIMProfileSet{}).
		// Watch namespace-scoped ServiceTemplates and enqueue:
		//   - the owning AIMModel (so externally-created templates are observed)
		//   - any fine-tuned AIMModels whose spec.aimId matches the template's
		//     spec.aimId (so fine-tuned consumers re-reconcile when a source
		//     template's aimId is late-bound by its discovery job).
		Watches(
			&aimv1alpha1.AIMServiceTemplate{},
			handler.EnqueueRequestsFromMapFunc(r.findModelsForServiceTemplate),
		).
		// Watch cluster-scoped templates for aimId-based matching: when a new
		// official template appears, fine-tuned models need to reconcile.
		Watches(
			&aimv1alpha1.AIMClusterServiceTemplate{},
			handler.EnqueueRequestsFromMapFunc(r.findModelsForClusterServiceTemplate),
		).
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

// findModelsForServiceTemplate returns reconcile requests for AIMModels
// affected by the given AIMServiceTemplate event. It enqueues:
//
//  1. The owning AIMModel referenced by template.spec.modelName (in the same
//     namespace). This covers externally-created templates that aren't owned
//     via OwnerReferences.
//  2. All fine-tuned AIMModels (across namespaces) whose spec.aimId matches
//     template.spec.aimId. Fine-tuned models resolve their deployment image
//     by reading the matched template's owner — once a namespace-scoped
//     source template late-binds its aimId via discovery, the consumers must
//     re-reconcile to pick up the new match.
//
// Duplicates are deduped by controller-runtime's workqueue.
func (r *AIMModelReconciler) findModelsForServiceTemplate(ctx context.Context, obj client.Object) []reconcile.Request {
	template, ok := obj.(*aimv1alpha1.AIMServiceTemplate)
	if !ok {
		return nil
	}

	var requests []reconcile.Request

	if template.Spec.ModelName != "" {
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      template.Spec.ModelName,
				Namespace: template.Namespace,
			},
		})
	}

	if template.Spec.AimId != "" {
		var models aimv1alpha2.AIMModelList
		if err := r.List(ctx, &models,
			client.MatchingFields{aimv1alpha1.ModelAimIdIndexKey: template.Spec.AimId},
		); err != nil {
			log.FromContext(ctx).Error(err, "failed to list AIMModels for ServiceTemplate aimId",
				"aimId", template.Spec.AimId)
		} else {
			for _, model := range models.Items {
				requests = append(requests, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name:      model.Name,
						Namespace: model.Namespace,
					},
				})
			}
		}
	}

	return requests
}

// findModelsForClusterServiceTemplate returns reconcile requests for all
// AIMModels whose aimId matches the cluster template's aimId. Triggers
// re-matching when official templates are created/updated/deleted.
func (r *AIMModelReconciler) findModelsForClusterServiceTemplate(ctx context.Context, obj client.Object) []reconcile.Request {
	template, ok := obj.(*aimv1alpha1.AIMClusterServiceTemplate)
	if !ok || template.Spec.AimId == "" {
		return nil
	}

	var models aimv1alpha2.AIMModelList
	if err := r.List(ctx, &models,
		client.MatchingFields{aimv1alpha1.ModelAimIdIndexKey: template.Spec.AimId},
	); err != nil {
		log.FromContext(ctx).Error(err, "failed to list AIMModels for ClusterServiceTemplate aimId",
			"aimId", template.Spec.AimId)
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
// affected by node-label changes. Today that's just image-backed models — the
// native discovery pipeline filters discovered profiles against node
// accelerator labels, so any node fleet change can flip the supported set.
func modelRequiresNodeReconcile(model *aimv1alpha2.AIMModel) bool {
	return model != nil && model.Spec.Image != ""
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
