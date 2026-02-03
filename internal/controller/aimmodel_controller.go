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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/aimmodel"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

const (
	modelName = "model"
)

// AIMModelReconciler reconciles an AIMModel object
type AIMModelReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Recorder  record.EventRecorder
	Clientset kubernetes.Interface

	reconciler controllerutils.DomainReconciler[*aimv1alpha1.AIMModel, *aimv1alpha1.AIMModelStatus, aimmodel.ModelFetchResult, aimmodel.ModelObservation]
	pipeline   controllerutils.Pipeline[*aimv1alpha1.AIMModel, *aimv1alpha1.AIMModelStatus, aimmodel.ModelFetchResult, aimmodel.ModelObservation]
}

// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimmodels,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimmodels/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimmodels/finalizers,verbs=update
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimruntimeconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclusterruntimeconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimservicetemplates,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *AIMModelReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the model
	var model aimv1alpha1.AIMModel
	if err := r.Get(ctx, req.NamespacedName, &model); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to fetch AIMModel")
		return ctrl.Result{}, err
	}

	return r.pipeline.Run(ctx, &model)
}

func (r *AIMModelReconciler) SetupWithManager(mgr ctrl.Manager) error {
	ctx := context.Background()

	r.reconciler = &aimmodel.ModelReconciler{
		Clientset: r.Clientset,
		Scheme:    r.Scheme,
	}
	r.pipeline = controllerutils.Pipeline[
		*aimv1alpha1.AIMModel,
		*aimv1alpha1.AIMModelStatus,
		aimmodel.ModelFetchResult,
		aimmodel.ModelObservation,
	]{
		Client:         mgr.GetClient(),
		StatusClient:   mgr.GetClient().Status(),
		Recorder:       r.Recorder,
		ControllerName: modelName,
		Reconciler:     r.reconciler,
		Scheme:         r.Scheme,
		Clientset:      r.Clientset,
	}
	r.Recorder = mgr.GetEventRecorderFor(r.pipeline.GetFullName())
	r.pipeline.Recorder = r.Recorder

	// Index AIMServiceTemplate by modelName for efficient lookup
	if err := mgr.GetFieldIndexer().IndexField(ctx, &aimv1alpha1.AIMServiceTemplate{}, aimv1alpha1.ServiceTemplateModelNameIndexKey, func(obj client.Object) []string {
		template, ok := obj.(*aimv1alpha1.AIMServiceTemplate)
		if !ok {
			return nil
		}
		return []string{template.Spec.ModelName}
	}); err != nil {
		return err
	}

	// Index AIMModel by image for efficient lookup
	if err := mgr.GetFieldIndexer().IndexField(ctx, &aimv1alpha1.AIMModel{}, aimv1alpha1.ModelImageIndexKey, func(obj client.Object) []string {
		model, ok := obj.(*aimv1alpha1.AIMModel)
		if !ok {
			return nil
		}
		return []string{model.Spec.Image}
	}); err != nil {
		return err
	}

	// Index AIMModel by runtimeConfigName for efficient lookup when config changes
	if err := mgr.GetFieldIndexer().IndexField(ctx, &aimv1alpha1.AIMModel{}, aimv1alpha1.ModelRuntimeConfigIndexKey, func(obj client.Object) []string {
		model, ok := obj.(*aimv1alpha1.AIMModel)
		if !ok {
			return nil
		}
		if model.Spec.Name == "" {
			return nil
		}
		return []string{model.Spec.Name}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&aimv1alpha1.AIMModel{}).
		Owns(&aimv1alpha1.AIMServiceTemplate{}).
		// Watch all ServiceTemplates (including externally-created) that reference this model
		Watches(
			&aimv1alpha1.AIMServiceTemplate{},
			handler.EnqueueRequestsFromMapFunc(r.findModelForServiceTemplate),
		).
		// Watch namespace-scoped RuntimeConfigs and enqueue models that reference them
		Watches(
			&aimv1alpha1.AIMRuntimeConfig{},
			handler.EnqueueRequestsFromMapFunc(r.findModelsForRuntimeConfig),
		).
		// Watch cluster-scoped RuntimeConfigs and enqueue models that reference them
		Watches(
			&aimv1alpha1.AIMClusterRuntimeConfig{},
			handler.EnqueueRequestsFromMapFunc(r.findModelsForClusterRuntimeConfig),
		).
		Named(modelName).
		Complete(r)
}

// findModelForServiceTemplate returns a reconcile request for the AIMModel
// referenced by the template's spec.modelName field.
func (r *AIMModelReconciler) findModelForServiceTemplate(ctx context.Context, obj client.Object) []reconcile.Request {
	template, ok := obj.(*aimv1alpha1.AIMServiceTemplate)
	if !ok {
		return nil
	}

	if template.Spec.ModelName == "" {
		return nil
	}

	return []reconcile.Request{{
		NamespacedName: types.NamespacedName{
			Name:      template.Spec.ModelName,
			Namespace: template.Namespace,
		},
	}}
}

// findModelsForRuntimeConfig returns reconcile requests for all AIMModels
// in the same namespace that reference the given RuntimeConfig by name.
func (r *AIMModelReconciler) findModelsForRuntimeConfig(ctx context.Context, obj client.Object) []reconcile.Request {
	config, ok := obj.(*aimv1alpha1.AIMRuntimeConfig)
	if !ok {
		return nil
	}

	// Find all models in the same namespace referencing this config
	var models aimv1alpha1.AIMModelList
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

// findModelsForClusterRuntimeConfig returns reconcile requests for all AIMModels
// across all namespaces that reference the given ClusterRuntimeConfig by name.
func (r *AIMModelReconciler) findModelsForClusterRuntimeConfig(ctx context.Context, obj client.Object) []reconcile.Request {
	config, ok := obj.(*aimv1alpha1.AIMClusterRuntimeConfig)
	if !ok {
		return nil
	}

	// Find all models across all namespaces referencing this config
	var models aimv1alpha1.AIMModelList
	if err := r.List(ctx, &models,
		client.MatchingFields{aimv1alpha1.ModelRuntimeConfigIndexKey: config.Name},
	); err != nil {
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
