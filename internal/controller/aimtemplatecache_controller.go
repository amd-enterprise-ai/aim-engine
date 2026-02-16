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
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/aimtemplatecache"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

const (
	templateCacheName = "template-cache"

	// finalizerArtifactCleanup is the finalizer for cleaning up non-Available artifacts
	// when an AIMTemplateCache is deleted. artifacts that are stuck in Failed/Pending states
	// cannot be re-created while they exist, so we must delete them on template cache deletion.
	finalizerArtifactCleanup = "aim.eai.amd.com/artifactcleanup"
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
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimartifacts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *AIMTemplateCacheReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the template cache
	var templateCache aimv1alpha1.AIMTemplateCache
	if err := r.Get(ctx, req.NamespacedName, &templateCache); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to fetch AIMTemplateCache")
		return ctrl.Result{}, err
	}

	// Handle finalizer for artifact cleanup
	if templateCache.DeletionTimestamp != nil {
		// Template cache is being deleted
		if controllerutil.ContainsFinalizer(&templateCache, finalizerArtifactCleanup) {
			namespaceTerminating, err := isNamespaceTerminating(ctx, r.Client, templateCache.Namespace)
			if err != nil {
				if apierrors.IsForbidden(err) {
					logger.Info("Failed to read namespace during deletion, assuming namespace is terminating",
						"namespace", templateCache.Namespace,
						"templateCache", templateCache.Name,
						"finalizer", finalizerArtifactCleanup)
					namespaceTerminating = true
				} else {
					logger.Error(err, "Failed to check namespace termination", "namespace", templateCache.Namespace)
					return ctrl.Result{}, err
				}
			}

			if namespaceTerminating {
				logger.Info("Namespace is terminating, skipping artifact cleanup before finalizer removal",
					"namespace", templateCache.Namespace,
					"templateCache", templateCache.Name)
			} else {
				// Run cleanup logic
				if err := r.cleanupArtifacts(ctx, &templateCache); err != nil {
					logger.Error(err, "Failed to cleanup artifacts")
					return ctrl.Result{}, err
				}
			}

			// Remove the finalizer
			logger.Info("Removing template cache cleanup finalizer",
				"templateCache", templateCache.Name,
				"namespace", templateCache.Namespace,
				"finalizer", finalizerArtifactCleanup)
			controllerutil.RemoveFinalizer(&templateCache, finalizerArtifactCleanup)
			if err := r.Update(ctx, &templateCache); err != nil {
				if apierrors.IsNotFound(err) {
					// Resource already deleted while removing finalizer
					return ctrl.Result{}, nil
				}
				if apierrors.IsConflict(err) {
					// Conflict, retry on next reconcile
					return ctrl.Result{Requeue: true}, nil
				}
				return ctrl.Result{}, err
			}
			logger.Info("Removed template cache cleanup finalizer",
				"templateCache", templateCache.Name,
				"namespace", templateCache.Namespace,
				"finalizer", finalizerArtifactCleanup)
		}
		// Stop reconciliation as the resource is being deleted
		return ctrl.Result{}, nil
	}

	// Ensure finalizer is present
	if !controllerutil.ContainsFinalizer(&templateCache, finalizerArtifactCleanup) {
		controllerutil.AddFinalizer(&templateCache, finalizerArtifactCleanup)
		if err := r.Update(ctx, &templateCache); err != nil {
			if apierrors.IsConflict(err) {
				// Conflict, retry on next reconcile
				return ctrl.Result{Requeue: true}, nil
			}
			return ctrl.Result{}, err
		}
		// Requeue to continue with main reconciliation after finalizer is added
		return ctrl.Result{Requeue: true}, nil
	}

	return r.pipeline.Run(ctx, &templateCache)
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
		// Watch artifacts and enqueue template caches that created them (via label)
		// artifacts are shared resources without owner references
		Watches(
			&aimv1alpha1.AIMArtifact{},
			handler.EnqueueRequestsFromMapFunc(r.findTemplateCachesForArtifact),
		).
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

// findTemplateCachesForArtifact finds all template caches that created a artifact (via label).
// artifacts are shared resources without owner references, so we use a label-based lookup.
func (r *AIMTemplateCacheReconciler) findTemplateCachesForArtifact(ctx context.Context, obj client.Object) []ctrl.Request {
	artifact := obj.(*aimv1alpha1.AIMArtifact)

	// Get the template cache name from the label
	templateCacheName := artifact.Labels[constants.LabelTemplateCacheName]
	if templateCacheName == "" {
		// artifact was not created by a template cache (or is a legacy cache)
		return nil
	}

	// Return a reconcile request for the template cache
	return []ctrl.Request{
		{
			NamespacedName: client.ObjectKey{
				Name:      templateCacheName,
				Namespace: artifact.Namespace,
			},
		},
	}
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

// cleanupArtifacts deletes AIMArtifacts created by this template cache that are not Available.
// artifacts that are stuck in Failed/Pending states cannot be re-created while they exist,
// blocking any future template cache that would use the same template. Deleting non-Available caches
// on template cache deletion ensures a clean slate for recreation.
func (r *AIMTemplateCacheReconciler) cleanupArtifacts(ctx context.Context, templateCache *aimv1alpha1.AIMTemplateCache) error {
	logger := log.FromContext(ctx)

	// Sanitize template cache name for label matching
	templateCacheLabelValue, err := utils.SanitizeLabelValue(templateCache.Name)
	if err != nil {
		return fmt.Errorf("failed to sanitize template cache name for label: %w", err)
	}

	// List all AIMArtifacts created by this template cache (via label)
	var artifacts aimv1alpha1.AIMArtifactList
	if err := r.List(ctx, &artifacts,
		client.InNamespace(templateCache.Namespace),
		client.MatchingLabels{
			constants.LabelTemplateCacheName: templateCacheLabelValue,
		},
	); err != nil {
		// If the namespace is being deleted, skip cleanup
		if apierrors.IsForbidden(err) || apierrors.IsNotFound(err) {
			logger.Info("Skipping cleanup, namespace may be terminating", "templateCache", templateCache.Name)
			return nil
		}
		return fmt.Errorf("failed to list artifacts for cleanup: %w", err)
	}

	// Delete only the ones that are not in Ready state
	var errs []error
	for i := range artifacts.Items {
		mc := &artifacts.Items[i]
		if mc.Status.Status != constants.AIMStatusReady {
			if deleteErr := r.Delete(ctx, mc); deleteErr != nil && !apierrors.IsNotFound(deleteErr) {
				// If namespace is terminating, continue
				if apierrors.IsForbidden(deleteErr) {
					continue
				}
				errs = append(errs, fmt.Errorf("failed to delete artifact %s: %w", mc.Name, deleteErr))
			} else {
				logger.Info("Deleted non-available artifact during template cache cleanup",
					"artifact", mc.Name,
					"templateCache", templateCache.Name,
					"cacheStatus", mc.Status.Status)
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("cleanup errors: %v", errs)
	}
	return nil
}
