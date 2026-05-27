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
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	discoverylock "github.com/amd-enterprise-ai/aim-engine/internal/discovery/lock"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimmodel"
)

const (
	clusterModelControllerName                = "cluster-model-v1alpha2"
	clusterModelDiscoveryCleanupFinalizerName = "aim.eai.amd.com/cluster-model-discovery-cache-cleanup"
)

type AIMClusterModelReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Recorder  record.EventRecorder
	Clientset kubernetes.Interface

	reconciler controllerutils.DomainReconciler[
		*aimv1alpha2.AIMClusterModel,
		*aimv1alpha1.AIMModelStatus,
		aimmodel.ClusterModelFetchResult,
		aimmodel.ClusterModelObservation,
	]
	pipeline controllerutils.Pipeline[
		*aimv1alpha2.AIMClusterModel,
		*aimv1alpha1.AIMModelStatus,
		aimmodel.ClusterModelFetchResult,
		aimmodel.ClusterModelObservation,
	]
}

// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclustermodels,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclustermodels/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclustermodels/finalizers,verbs=update
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclusterprofilesets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclusterprofiles,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods/log,verbs=get
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete

func (r *AIMClusterModelReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	var model aimv1alpha2.AIMClusterModel
	if err := r.Get(ctx, req.NamespacedName, &model); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to fetch AIMClusterModel")
		return ctrl.Result{}, err
	}

	if model.DeletionTimestamp != nil {
		if controllerutil.ContainsFinalizer(&model, clusterModelDiscoveryCleanupFinalizerName) {
			if err := r.cleanupDiscoveryCache(ctx, model.Name); err != nil {
				logger.Error(err, "Failed to cleanup AIMClusterModel discovery cache")
				return ctrl.Result{}, err
			}
			controllerutil.RemoveFinalizer(&model, clusterModelDiscoveryCleanupFinalizerName)
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

	if !controllerutil.ContainsFinalizer(&model, clusterModelDiscoveryCleanupFinalizerName) {
		controllerutil.AddFinalizer(&model, clusterModelDiscoveryCleanupFinalizerName)
		if err := r.Update(ctx, &model); err != nil {
			if apierrors.IsConflict(err) {
				return ctrl.Result{Requeue: true}, nil
			}
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

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

func (r *AIMClusterModelReconciler) SetupWithManager(mgr ctrl.Manager) error {
	ctx := context.Background()

	r.Recorder = mgr.GetEventRecorderFor("aim-" + clusterModelControllerName + "-controller")
	r.reconciler = &aimmodel.ClusterModelReconciler{
		Client:    mgr.GetClient(),
		Clientset: r.Clientset,
		Scheme:    r.Scheme,
	}
	r.pipeline = controllerutils.Pipeline[
		*aimv1alpha2.AIMClusterModel,
		*aimv1alpha1.AIMModelStatus,
		aimmodel.ClusterModelFetchResult,
		aimmodel.ClusterModelObservation,
	]{
		Client:         mgr.GetClient(),
		StatusClient:   mgr.GetClient().Status(),
		Recorder:       r.Recorder,
		ControllerName: clusterModelControllerName,
		Reconciler:     r.reconciler,
		Scheme:         r.Scheme,
		Clientset:      r.Clientset,
	}

	if err := mgr.GetFieldIndexer().IndexField(ctx, &aimv1alpha1.AIMClusterServiceTemplate{}, aimv1alpha1.ServiceTemplateModelNameIndexKey, func(obj client.Object) []string {
		template, ok := obj.(*aimv1alpha1.AIMClusterServiceTemplate)
		if !ok {
			return nil
		}
		return []string{template.Spec.ModelName}
	}); err != nil {
		return err
	}
	if err := mgr.GetFieldIndexer().IndexField(ctx, &aimv1alpha2.AIMClusterModel{}, aimv1alpha1.ClusterModelImageIndexKey, func(obj client.Object) []string {
		model, ok := obj.(*aimv1alpha2.AIMClusterModel)
		if !ok || model.Spec.Image == "" {
			return nil
		}
		return []string{model.Spec.Image}
	}); err != nil {
		return err
	}
	if err := mgr.GetFieldIndexer().IndexField(ctx, &aimv1alpha2.AIMClusterModel{}, aimv1alpha1.ClusterModelRuntimeConfigIndexKey, func(obj client.Object) []string {
		model, ok := obj.(*aimv1alpha2.AIMClusterModel)
		if !ok || model.Spec.Name == "" {
			return nil
		}
		return []string{model.Spec.Name}
	}); err != nil {
		return err
	}

	// Index AIMClusterModel by aimId for reverse lookup when official cluster
	// templates change.
	if err := mgr.GetFieldIndexer().IndexField(ctx, &aimv1alpha2.AIMClusterModel{}, aimv1alpha1.ClusterModelAimIdIndexKey, func(obj client.Object) []string {
		model, ok := obj.(*aimv1alpha2.AIMClusterModel)
		if !ok || model.Spec.AimId == "" {
			return nil
		}
		return []string{model.Spec.AimId}
	}); err != nil {
		return err
	}

	childHandler := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		return directClusterModelRequest(obj)
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(&aimv1alpha2.AIMClusterModel{}).
		Owns(&batchv1.Job{}).
		Owns(&aimv1alpha1.AIMClusterServiceTemplate{}).
		Owns(&aimv1alpha2.AIMClusterProfileSet{}).
		// Watch cluster-scoped templates for aimId-based matching: when a new
		// official template appears, fine-tuned cluster models need to reconcile.
		Watches(
			&aimv1alpha1.AIMClusterServiceTemplate{},
			handler.EnqueueRequestsFromMapFunc(r.findClusterModelsForClusterServiceTemplate),
		).
		Watches(
			&aimv1alpha1.AIMClusterRuntimeConfig{},
			handler.EnqueueRequestsFromMapFunc(r.findClusterModelsForClusterRuntimeConfig),
		).
		Watches(
			&corev1.Node{},
			handler.EnqueueRequestsFromMapFunc(r.findClusterModelsForNodeChange),
			builder.WithPredicates(utils.NodeGPUChangePredicate()),
		).
		Watches(
			&corev1.ConfigMap{},
			childHandler,
			builder.WithPredicates(predicate.NewPredicateFuncs(hasDirectClusterModelOwnerAnnotations)),
		).
		Watches(
			&aimv1alpha2.AIMClusterProfile{},
			childHandler,
			builder.WithPredicates(predicate.NewPredicateFuncs(hasDirectClusterModelOwnerAnnotations)),
		).
		Named(clusterModelControllerName).
		Complete(r)
}

func (r *AIMClusterModelReconciler) cleanupDiscoveryCache(ctx context.Context, modelName string) error {
	cacheName, err := aimmodel.DiscoveryCacheName(modelName)
	if err != nil {
		return err
	}
	cache := &corev1.ConfigMap{}
	cache.Name = cacheName
	cache.Namespace = constants.GetOperatorNamespace()
	return client.IgnoreNotFound(r.Delete(ctx, cache))
}

func directClusterModelRequest(obj client.Object) []reconcile.Request {
	annotations := obj.GetAnnotations()
	if len(annotations) == 0 {
		return nil
	}
	name := annotations[aimmodel.AnnotationModelName()]
	if name == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: client.ObjectKey{Name: name}}}
}

func hasDirectClusterModelOwnerAnnotations(obj client.Object) bool {
	annotations := obj.GetAnnotations()
	return annotations[aimmodel.AnnotationModelName()] != ""
}

// findClusterModelsForClusterServiceTemplate returns reconcile requests for
// all AIMClusterModels whose aimId matches the cluster template's aimId.
// Triggers re-matching when official templates are created/updated/deleted.
func (r *AIMClusterModelReconciler) findClusterModelsForClusterServiceTemplate(ctx context.Context, obj client.Object) []reconcile.Request {
	template, ok := obj.(*aimv1alpha1.AIMClusterServiceTemplate)
	if !ok || template.Spec.AimId == "" {
		return nil
	}

	var models aimv1alpha2.AIMClusterModelList
	if err := r.List(ctx, &models,
		client.MatchingFields{aimv1alpha1.ClusterModelAimIdIndexKey: template.Spec.AimId},
	); err != nil {
		log.FromContext(ctx).Error(err, "failed to list AIMClusterModels for template aimId",
			"aimId", template.Spec.AimId)
		return nil
	}

	requests := make([]reconcile.Request, len(models.Items))
	for i, model := range models.Items {
		requests[i] = reconcile.Request{NamespacedName: types.NamespacedName{Name: model.Name}}
	}
	return requests
}

func (r *AIMClusterModelReconciler) findClusterModelsForClusterRuntimeConfig(ctx context.Context, obj client.Object) []reconcile.Request {
	config, ok := obj.(*aimv1alpha1.AIMClusterRuntimeConfig)
	if !ok {
		return nil
	}
	var models aimv1alpha2.AIMClusterModelList
	if err := r.List(ctx, &models, client.MatchingFields{aimv1alpha1.ClusterModelRuntimeConfigIndexKey: config.Name}); err != nil {
		log.FromContext(ctx).Error(err, "failed to list AIMClusterModels for ClusterRuntimeConfig", "config", config.Name)
		return nil
	}
	requests := make([]reconcile.Request, len(models.Items))
	for i, model := range models.Items {
		requests[i] = reconcile.Request{NamespacedName: types.NamespacedName{Name: model.Name}}
	}
	return requests
}

func (r *AIMClusterModelReconciler) findClusterModelsForNodeChange(ctx context.Context, obj client.Object) []reconcile.Request {
	if _, ok := obj.(*corev1.Node); !ok {
		return nil
	}

	var models aimv1alpha2.AIMClusterModelList
	if err := r.List(ctx, &models); err != nil {
		log.FromContext(ctx).Error(err, "failed to list AIMClusterModels for Node event")
		return nil
	}

	requests := make([]reconcile.Request, 0, len(models.Items))
	for i := range models.Items {
		if !clusterModelRequiresNodeReconcile(&models.Items[i]) {
			continue
		}
		requests = append(requests, reconcile.Request{
			NamespacedName: client.ObjectKeyFromObject(&models.Items[i]),
		})
	}
	return requests
}

func clusterModelRequiresNodeReconcile(model *aimv1alpha2.AIMClusterModel) bool {
	return model != nil && model.Spec.Image != ""
}
