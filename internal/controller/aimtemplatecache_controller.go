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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/aimtemplatecache"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

const (
	templateCacheName = "template-cache"
)

// AIMTemplateCacheReconciler reconciles a AIMTemplateCache object
type AIMTemplateCacheReconciler struct {
	client.Client
	Clientset *kubernetes.Clientset
	Scheme    *runtime.Scheme
	Recorder  record.EventRecorder

	reconciler controllerutils.DomainReconciler[
		*aimv1alpha1.AIMTemplateCache,
		*aimv1alpha1.AIMTemplateCacheStatus,
		aimtemplatecache.TemplateCacheFetchResult,
		aimtemplatecache.TemplateCacheObservation,
	]
	pipeline controllerutils.Pipeline[
		*aimv1alpha1.AIMTemplateCache,
		*aimv1alpha1.AIMTemplateCacheStatus,
		aimtemplatecache.TemplateCacheFetchResult,
		aimtemplatecache.TemplateCacheObservation,
	]
}

// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimtemplatecaches,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimtemplatecaches/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimtemplatecaches/finalizers,verbs=update
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimservicetemplates,verbs=get;list;watch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclusterservicetemplates,verbs=get;list;watch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimmodelcaches,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the AIMTemplateCache object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.22.4/pkg/reconcile
func (r *AIMTemplateCacheReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the model
	var model aimv1alpha1.AIMTemplateCache
	if err := r.Get(ctx, req.NamespacedName, &model); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to fetch AIMTemplateCache")
		return ctrl.Result{}, err
	}

	if err := r.pipeline.Run(ctx, &model); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *AIMTemplateCacheReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Set up field index for AIMTemplateCache.Spec.TemplateName
	// This allows efficient lookup of template caches by template name
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&aimv1alpha1.AIMTemplateCache{},
		aimv1alpha1.TemplateCacheTemplateNameIndexKey,
		func(obj client.Object) []string {
			tc := obj.(*aimv1alpha1.AIMTemplateCache)
			return []string{tc.Spec.TemplateName}
		},
	); err != nil {
		return err
	}

	// Set up field index for AIMTemplateCache.Spec.TemplateScope
	// This allows efficient filtering of template caches by scope
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&aimv1alpha1.AIMTemplateCache{},
		aimv1alpha1.TemplateCacheTemplateScopeIndexKey,
		func(obj client.Object) []string {
			tc := obj.(*aimv1alpha1.AIMTemplateCache)
			return []string{string(tc.Spec.TemplateScope)}
		},
	); err != nil {
		return err
	}

	r.reconciler = &aimtemplatecache.TemplateCacheReconciler{
		Clientset: r.Clientset,
		Scheme:    r.Scheme,
	}
	r.pipeline = controllerutils.Pipeline[
		*aimv1alpha1.AIMTemplateCache,
		*aimv1alpha1.AIMTemplateCacheStatus,
		aimtemplatecache.TemplateCacheFetchResult,
		aimtemplatecache.TemplateCacheObservation,
	]{
		Client:         mgr.GetClient(),
		StatusClient:   mgr.GetClient().Status(),
		Recorder:       r.Recorder,
		ControllerName: templateCacheName,
		Reconciler:     r.reconciler,
		Scheme:         r.Scheme,
		Clientset:      r.Clientset,
	}
	r.Recorder = mgr.GetEventRecorderFor(r.pipeline.GetFullName())
	r.pipeline.Recorder = r.Recorder

	// Create predicate that only triggers on status changes (or create/delete)
	logger := mgr.GetLogger().WithName("template-watch")
	templateStatusPredicate := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			logger.Info("Template created", "name", e.Object.GetName(), "namespace", e.Object.GetNamespace())
			return true // Always reconcile on create
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			logger.Info("Template deleted", "name", e.Object.GetName(), "namespace", e.Object.GetNamespace())
			return true // Always reconcile on delete
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			// Only reconcile if status changed
			oldStatus := getTemplateStatus(e.ObjectOld)
			newStatus := getTemplateStatus(e.ObjectNew)
			changed := oldStatus != newStatus
			logger.Info("Template updated",
				"name", e.ObjectNew.GetName(),
				"namespace", e.ObjectNew.GetNamespace(),
				"oldStatus", oldStatus,
				"newStatus", newStatus,
				"willTrigger", changed)
			return changed
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false // Don't reconcile on generic events
		},
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&aimv1alpha1.AIMTemplateCache{}).
		Owns(&aimv1alpha1.AIMModelCache{}).
		Watches(
			&aimv1alpha1.AIMServiceTemplate{},
			handler.EnqueueRequestsFromMapFunc(r.findTemplateCachesForServiceTemplate),
			builder.WithPredicates(templateStatusPredicate),
		).
		Watches(
			&aimv1alpha1.AIMClusterServiceTemplate{},
			handler.EnqueueRequestsFromMapFunc(r.findTemplateCachesForClusterServiceTemplate),
			builder.WithPredicates(templateStatusPredicate),
		).
		Named(templateCacheName).
		Complete(r)
}

// findTemplateCachesForServiceTemplate finds all template caches that reference a namespace-scoped service template
func (r *AIMTemplateCacheReconciler) findTemplateCachesForServiceTemplate(ctx context.Context, obj client.Object) []ctrl.Request {
	template := obj.(*aimv1alpha1.AIMServiceTemplate)
	logger := log.FromContext(ctx)

	// Query for template caches using field indexes for both name and scope
	var templateCaches aimv1alpha1.AIMTemplateCacheList
	if err := r.List(ctx, &templateCaches,
		client.InNamespace(template.Namespace),
		client.MatchingFields{
			aimv1alpha1.TemplateCacheTemplateNameIndexKey:  template.Name,
			aimv1alpha1.TemplateCacheTemplateScopeIndexKey: string(aimv1alpha1.AIMResolutionScopeNamespace),
		},
	); err != nil {
		logger.Error(err, "Failed to list template caches for service template",
			"templateName", template.Name, "namespace", template.Namespace)
		return nil
	}

	// Create reconcile requests for each template cache
	requests := make([]ctrl.Request, len(templateCaches.Items))
	for i, tc := range templateCaches.Items {
		requests[i] = ctrl.Request{
			NamespacedName: client.ObjectKey{
				Name:      tc.Name,
				Namespace: tc.Namespace,
			},
		}
	}

	return requests
}

// findTemplateCachesForClusterServiceTemplate finds all template caches that reference a cluster-scoped service template
func (r *AIMTemplateCacheReconciler) findTemplateCachesForClusterServiceTemplate(ctx context.Context, obj client.Object) []ctrl.Request {
	template := obj.(*aimv1alpha1.AIMClusterServiceTemplate)

	// Query for template caches using field indexes for both name and scope
	var templateCaches aimv1alpha1.AIMTemplateCacheList
	if err := r.List(ctx, &templateCaches,
		client.MatchingFields{
			aimv1alpha1.TemplateCacheTemplateNameIndexKey:  template.Name,
			aimv1alpha1.TemplateCacheTemplateScopeIndexKey: string(aimv1alpha1.AIMResolutionScopeCluster),
		},
	); err != nil {
		log.FromContext(ctx).Error(err, "Failed to list template caches for cluster service template",
			"templateName", template.Name)
		return nil
	}

	// Create reconcile requests for each template cache
	requests := make([]ctrl.Request, len(templateCaches.Items))
	for i, tc := range templateCaches.Items {
		requests[i] = ctrl.Request{
			NamespacedName: client.ObjectKey{
				Name:      tc.Name,
				Namespace: tc.Namespace,
			},
		}
	}

	return requests
}

// getTemplateStatus extracts the status from a template object (works for both namespace and cluster scoped)
func getTemplateStatus(obj client.Object) constants.AIMStatus {
	switch t := obj.(type) {
	case *aimv1alpha1.AIMServiceTemplate:
		return t.Status.Status
	case *aimv1alpha1.AIMClusterServiceTemplate:
		return t.Status.Status
	default:
		return ""
	}
}
