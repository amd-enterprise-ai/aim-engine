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

	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/aimservice"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

const (
	serviceName = "service"

	// finalizerTemplateCacheCleanup is the finalizer for cleaning up non-Available template caches
	// when an AIMService is deleted. Template caches that are stuck in Failed/Pending states
	// cannot be re-created while they exist, so we must delete them on service deletion.
	finalizerTemplateCacheCleanup = "aim.eai.amd.com/template-cache-cleanup"
)

// AIMServiceReconciler reconciles a AIMService object
type AIMServiceReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Recorder  record.EventRecorder
	Clientset kubernetes.Interface

	reconciler controllerutils.DomainReconciler[*aimv1alpha1.AIMService, *aimv1alpha1.AIMServiceStatus, aimservice.ServiceFetchResult, aimservice.ServiceObservation]
	pipeline   controllerutils.Pipeline[*aimv1alpha1.AIMService, *aimv1alpha1.AIMServiceStatus, aimservice.ServiceFetchResult, aimservice.ServiceObservation]
}

// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimservices,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimservices/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimservices/finalizers,verbs=update
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimmodels,verbs=get;list;watch;create
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclustermodels,verbs=get;list;watch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimservicetemplates,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclusterservicetemplates,verbs=get;list;watch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimtemplatecaches,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimmodelcaches,verbs=get;list;watch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimruntimeconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclusterruntimeconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups=serving.kserve.io,resources=inferenceservices,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *AIMServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the service
	var service aimv1alpha1.AIMService
	if err := r.Get(ctx, req.NamespacedName, &service); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to fetch AIMService")
		return ctrl.Result{}, err
	}

	// Handle finalizer for template cache cleanup
	if service.DeletionTimestamp != nil {
		// Service is being deleted
		if controllerutil.ContainsFinalizer(&service, finalizerTemplateCacheCleanup) {
			// Run cleanup logic
			if err := r.cleanupTemplateCaches(ctx, &service); err != nil {
				logger.Error(err, "Failed to cleanup template caches")
				return ctrl.Result{}, err
			}

			// Remove the finalizer
			controllerutil.RemoveFinalizer(&service, finalizerTemplateCacheCleanup)
			if err := r.Update(ctx, &service); err != nil {
				if apierrors.IsConflict(err) {
					// Conflict, retry on next reconcile
					return ctrl.Result{Requeue: true}, nil
				}
				return ctrl.Result{}, err
			}
		}
		// Stop reconciliation as the resource is being deleted
		return ctrl.Result{}, nil
	}

	// Ensure finalizer is present
	if !controllerutil.ContainsFinalizer(&service, finalizerTemplateCacheCleanup) {
		controllerutil.AddFinalizer(&service, finalizerTemplateCacheCleanup)
		if err := r.Update(ctx, &service); err != nil {
			if apierrors.IsConflict(err) {
				// Conflict, retry on next reconcile
				return ctrl.Result{Requeue: true}, nil
			}
			return ctrl.Result{}, err
		}
		// Requeue to continue with main reconciliation after finalizer is added
		return ctrl.Result{Requeue: true}, nil
	}

	return r.pipeline.Run(ctx, &service)
}

// SetupWithManager sets up the controller with the Manager.
func (r *AIMServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	ctx := context.Background()

	r.reconciler = &aimservice.ServiceReconciler{
		Clientset: r.Clientset,
		Scheme:    r.Scheme,
	}
	r.pipeline = controllerutils.Pipeline[
		*aimv1alpha1.AIMService,
		*aimv1alpha1.AIMServiceStatus,
		aimservice.ServiceFetchResult,
		aimservice.ServiceObservation,
	]{
		Client:         mgr.GetClient(),
		StatusClient:   mgr.GetClient().Status(),
		Recorder:       r.Recorder,
		ControllerName: serviceName,
		Reconciler:     r.reconciler,
		Scheme:         r.Scheme,
		Clientset:      r.Clientset,
	}
	r.Recorder = mgr.GetEventRecorderFor(r.pipeline.GetFullName())
	r.pipeline.Recorder = r.Recorder

	// Index AIMService by template name for efficient lookup when templates change
	if err := mgr.GetFieldIndexer().IndexField(ctx, &aimv1alpha1.AIMService{}, aimv1alpha1.AIMServiceTemplateIndexKey, func(obj client.Object) []string {
		svc, ok := obj.(*aimv1alpha1.AIMService)
		if !ok {
			return nil
		}
		if svc.Spec.Template.Name == "" {
			return nil
		}
		return []string{svc.Spec.Template.Name}
	}); err != nil {
		return err
	}

	// Index AIMService by resolved template name for efficient lookup when template caches change
	if err := mgr.GetFieldIndexer().IndexField(ctx, &aimv1alpha1.AIMService{}, aimv1alpha1.AIMServiceResolvedTemplateIndexKey, func(obj client.Object) []string {
		svc, ok := obj.(*aimv1alpha1.AIMService)
		if !ok {
			return nil
		}
		if svc.Status.ResolvedTemplate == nil || svc.Status.ResolvedTemplate.Name == "" {
			return nil
		}
		return []string{svc.Status.ResolvedTemplate.Name}
	}); err != nil {
		return err
	}

	// Index Events by involvedObject.name for efficient lookup when fetching InferenceService events
	if err := mgr.GetFieldIndexer().IndexField(ctx, &corev1.Event{}, "involvedObject.name", func(obj client.Object) []string {
		event, ok := obj.(*corev1.Event)
		if !ok {
			return nil
		}
		return []string{event.InvolvedObject.Name}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&aimv1alpha1.AIMService{}).
		Owns(&servingv1beta1.InferenceService{}).
		Owns(&gatewayapiv1.HTTPRoute{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		// Watch namespace-scoped templates and enqueue services that reference them
		Watches(
			&aimv1alpha1.AIMServiceTemplate{},
			handler.EnqueueRequestsFromMapFunc(r.findServicesForTemplate),
		).
		// Watch cluster-scoped templates and enqueue services that reference them
		Watches(
			&aimv1alpha1.AIMClusterServiceTemplate{},
			handler.EnqueueRequestsFromMapFunc(r.findServicesForClusterTemplate),
		).
		// Watch namespace-scoped models and enqueue services using them
		Watches(
			&aimv1alpha1.AIMModel{},
			handler.EnqueueRequestsFromMapFunc(r.findServicesForModel),
		).
		// Watch cluster-scoped models and enqueue services using them
		Watches(
			&aimv1alpha1.AIMClusterModel{},
			handler.EnqueueRequestsFromMapFunc(r.findServicesForClusterModel),
		).
		// Watch namespace-scoped RuntimeConfigs and enqueue services that reference them
		Watches(
			&aimv1alpha1.AIMRuntimeConfig{},
			handler.EnqueueRequestsFromMapFunc(r.findServicesForRuntimeConfig),
		).
		// Watch cluster-scoped RuntimeConfigs and enqueue services that reference them
		Watches(
			&aimv1alpha1.AIMClusterRuntimeConfig{},
			handler.EnqueueRequestsFromMapFunc(r.findServicesForClusterRuntimeConfig),
		).
		// Watch template caches and enqueue services that use them
		Watches(
			&aimv1alpha1.AIMTemplateCache{},
			handler.EnqueueRequestsFromMapFunc(r.findServicesForTemplateCache),
		).
		// Watch events for InferenceServices to detect configuration errors like ServerlessModeRejected
		Watches(
			&corev1.Event{},
			handler.EnqueueRequestsFromMapFunc(r.findServicesForInferenceServiceEvent),
		).
		// Watch pods for InferenceServices to detect ImagePull errors, pending states, etc.
		Watches(
			&corev1.Pod{},
			handler.EnqueueRequestsFromMapFunc(r.findServicesForInferenceServicePod),
		).
		Named(serviceName).
		Complete(r)
}

// findServicesForTemplate returns reconcile requests for all AIMServices
// that reference the given template by name.
func (r *AIMServiceReconciler) findServicesForTemplate(ctx context.Context, obj client.Object) []reconcile.Request {
	template, ok := obj.(*aimv1alpha1.AIMServiceTemplate)
	if !ok {
		return nil
	}

	// Find all services in the same namespace referencing this template
	var services aimv1alpha1.AIMServiceList
	if err := r.List(ctx, &services,
		client.InNamespace(template.Namespace),
		client.MatchingFields{aimv1alpha1.AIMServiceTemplateIndexKey: template.Name},
	); err != nil {
		log.FromContext(ctx).Error(err, "failed to list AIMServices for template", "template", template.Name)
		return nil
	}

	requests := make([]reconcile.Request, len(services.Items))
	for i, svc := range services.Items {
		requests[i] = reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      svc.Name,
				Namespace: svc.Namespace,
			},
		}
	}
	return requests
}

// findServicesForClusterTemplate returns reconcile requests for all AIMServices
// that reference the given cluster template by name.
func (r *AIMServiceReconciler) findServicesForClusterTemplate(ctx context.Context, obj client.Object) []reconcile.Request {
	template, ok := obj.(*aimv1alpha1.AIMClusterServiceTemplate)
	if !ok {
		return nil
	}

	// Find all services across all namespaces referencing this template
	var services aimv1alpha1.AIMServiceList
	if err := r.List(ctx, &services,
		client.MatchingFields{aimv1alpha1.AIMServiceTemplateIndexKey: template.Name},
	); err != nil {
		log.FromContext(ctx).Error(err, "failed to list AIMServices for cluster template", "template", template.Name)
		return nil
	}

	requests := make([]reconcile.Request, len(services.Items))
	for i, svc := range services.Items {
		requests[i] = reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      svc.Name,
				Namespace: svc.Namespace,
			},
		}
	}
	return requests
}

// findServicesForModel returns reconcile requests for all AIMServices
// that reference the given model by name or by image.
func (r *AIMServiceReconciler) findServicesForModel(ctx context.Context, obj client.Object) []reconcile.Request {
	model, ok := obj.(*aimv1alpha1.AIMModel)
	if !ok {
		return nil
	}

	// Find services in the same namespace that might use this model
	var services aimv1alpha1.AIMServiceList
	if err := r.List(ctx, &services, client.InNamespace(model.Namespace)); err != nil {
		log.FromContext(ctx).Error(err, "failed to list AIMServices for model", "model", model.Name)
		return nil
	}

	var requests []reconcile.Request
	for _, svc := range services.Items {
		// Check if service references this model by name
		if svc.Spec.Model.Name != nil && *svc.Spec.Model.Name == model.Name {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      svc.Name,
					Namespace: svc.Namespace,
				},
			})
			continue
		}
		// Check if service references this model by image
		if svc.Spec.Model.Image != nil && *svc.Spec.Model.Image == model.Spec.Image {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      svc.Name,
					Namespace: svc.Namespace,
				},
			})
		}
	}
	return requests
}

// findServicesForClusterModel returns reconcile requests for all AIMServices
// that reference the given cluster model by name or by image.
func (r *AIMServiceReconciler) findServicesForClusterModel(ctx context.Context, obj client.Object) []reconcile.Request {
	model, ok := obj.(*aimv1alpha1.AIMClusterModel)
	if !ok {
		return nil
	}

	// Find all services that might use this cluster model
	var services aimv1alpha1.AIMServiceList
	if err := r.List(ctx, &services); err != nil {
		log.FromContext(ctx).Error(err, "failed to list AIMServices for cluster model", "model", model.Name)
		return nil
	}

	var requests []reconcile.Request
	for _, svc := range services.Items {
		// Check if service references this cluster model by name
		if svc.Spec.Model.Name != nil && *svc.Spec.Model.Name == model.Name {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      svc.Name,
					Namespace: svc.Namespace,
				},
			})
			continue
		}
		// Check if service references this cluster model by image
		if svc.Spec.Model.Image != nil && *svc.Spec.Model.Image == model.Spec.Image {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      svc.Name,
					Namespace: svc.Namespace,
				},
			})
		}
	}
	return requests
}

// findServicesForRuntimeConfig returns reconcile requests for all AIMServices
// in the same namespace that reference the given RuntimeConfig.
func (r *AIMServiceReconciler) findServicesForRuntimeConfig(ctx context.Context, obj client.Object) []reconcile.Request {
	config, ok := obj.(*aimv1alpha1.AIMRuntimeConfig)
	if !ok {
		return nil
	}

	// Find all services in the same namespace
	var services aimv1alpha1.AIMServiceList
	if err := r.List(ctx, &services, client.InNamespace(config.Namespace)); err != nil {
		log.FromContext(ctx).Error(err, "failed to list AIMServices for RuntimeConfig", "config", config.Name)
		return nil
	}

	var requests []reconcile.Request
	for _, svc := range services.Items {
		// Check if service references this config or uses default
		if svc.Spec.Name == config.Name || svc.Spec.Name == "" {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      svc.Name,
					Namespace: svc.Namespace,
				},
			})
		}
	}
	return requests
}

// findServicesForClusterRuntimeConfig returns reconcile requests for all AIMServices
// that reference the given ClusterRuntimeConfig.
func (r *AIMServiceReconciler) findServicesForClusterRuntimeConfig(ctx context.Context, obj client.Object) []reconcile.Request {
	config, ok := obj.(*aimv1alpha1.AIMClusterRuntimeConfig)
	if !ok {
		return nil
	}

	// Find all services across all namespaces
	var services aimv1alpha1.AIMServiceList
	if err := r.List(ctx, &services); err != nil {
		log.FromContext(ctx).Error(err, "failed to list AIMServices for ClusterRuntimeConfig", "config", config.Name)
		return nil
	}

	var requests []reconcile.Request
	for _, svc := range services.Items {
		// Check if service references this config or uses default
		if svc.Spec.Name == config.Name || svc.Spec.Name == "" {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      svc.Name,
					Namespace: svc.Namespace,
				},
			})
		}
	}
	return requests
}

// findServicesForTemplateCache returns reconcile requests for all AIMServices
// that use the same template as the given template cache.
// Template caches are not owned by services (to allow sharing), so we find services
// by matching the cache's templateName against services' resolved template name.
func (r *AIMServiceReconciler) findServicesForTemplateCache(ctx context.Context, obj client.Object) []reconcile.Request {
	cache, ok := obj.(*aimv1alpha1.AIMTemplateCache)
	if !ok {
		return nil
	}

	// Find all services in the same namespace that have resolved to this template
	var services aimv1alpha1.AIMServiceList
	if err := r.List(ctx, &services,
		client.InNamespace(cache.Namespace),
		client.MatchingFields{aimv1alpha1.AIMServiceResolvedTemplateIndexKey: cache.Spec.TemplateName},
	); err != nil {
		log.FromContext(ctx).Error(err, "failed to list AIMServices for template cache",
			"cache", cache.Name, "templateName", cache.Spec.TemplateName)
		return nil
	}

	requests := make([]reconcile.Request, len(services.Items))
	for i, svc := range services.Items {
		requests[i] = reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      svc.Name,
				Namespace: svc.Namespace,
			},
		}
	}
	return requests
}

// findServicesForInferenceServicePod returns reconcile requests for AIMServices
// when a pod belonging to one of their InferenceServices changes.
// This enables detection of ImagePull errors, pending states, etc.
func (r *AIMServiceReconciler) findServicesForInferenceServicePod(ctx context.Context, obj client.Object) []reconcile.Request {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return nil
	}

	// Check if this pod belongs to a KServe InferenceService
	isvcName, hasLabel := pod.Labels[constants.LabelKServeInferenceService]
	if !hasLabel {
		return nil
	}

	// Find the InferenceService
	isvc := &servingv1beta1.InferenceService{}
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: pod.Namespace,
		Name:      isvcName,
	}, isvc); err != nil {
		return nil
	}

	// Find owner AIMService from the InferenceService's owner references
	for _, ownerRef := range isvc.OwnerReferences {
		if ownerRef.Kind == "AIMService" {
			return []reconcile.Request{
				{
					NamespacedName: types.NamespacedName{
						Name:      ownerRef.Name,
						Namespace: pod.Namespace,
					},
				},
			}
		}
	}

	return nil
}

// findServicesForInferenceServiceEvent returns reconcile requests for AIMServices
// when an event is created for an InferenceService they own.
// This enables detection of configuration errors like ServerlessModeRejected.
func (r *AIMServiceReconciler) findServicesForInferenceServiceEvent(ctx context.Context, obj client.Object) []reconcile.Request {
	event, ok := obj.(*corev1.Event)
	if !ok {
		return nil
	}

	// Only process events for InferenceServices
	if event.InvolvedObject.Kind != "InferenceService" {
		return nil
	}

	// Only process warning events that indicate configuration problems
	if event.Type != corev1.EventTypeWarning {
		return nil
	}

	// Find the InferenceService
	isvc := &servingv1beta1.InferenceService{}
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: event.InvolvedObject.Namespace,
		Name:      event.InvolvedObject.Name,
	}, isvc); err != nil {
		return nil
	}

	// Find owner AIMService from the InferenceService's owner references
	for _, ownerRef := range isvc.OwnerReferences {
		if ownerRef.Kind == "AIMService" {
			return []reconcile.Request{
				{
					NamespacedName: types.NamespacedName{
						Name:      ownerRef.Name,
						Namespace: event.InvolvedObject.Namespace,
					},
				},
			}
		}
	}

	return nil
}

// cleanupTemplateCaches deletes AIMTemplateCaches created by this service that are not Available.
// Template caches that are stuck in Failed/Pending states cannot be re-created while they exist,
// blocking any future service that would use the same template. Deleting non-Available caches
// on service deletion ensures a clean slate for recreation.
func (r *AIMServiceReconciler) cleanupTemplateCaches(ctx context.Context, service *aimv1alpha1.AIMService) error {
	logger := log.FromContext(ctx)

	// Sanitize service name for label matching
	serviceLabelValue, err := utils.SanitizeLabelValue(service.Name)
	if err != nil {
		return fmt.Errorf("failed to sanitize service name for label: %w", err)
	}

	// List all AIMTemplateCaches created by this AIMService
	var templateCaches aimv1alpha1.AIMTemplateCacheList
	if err := r.List(ctx, &templateCaches,
		client.InNamespace(service.Namespace),
		client.MatchingLabels{
			constants.LabelService: serviceLabelValue,
		},
	); err != nil {
		// If the namespace is being deleted, skip cleanup
		if apierrors.IsForbidden(err) || apierrors.IsNotFound(err) {
			logger.Info("Skipping cleanup, namespace may be terminating", "service", service.Name)
			return nil
		}
		return fmt.Errorf("failed to list template caches for cleanup: %w", err)
	}

	// Delete only the ones that are not Available
	var errs []error
	for i := range templateCaches.Items {
		tc := &templateCaches.Items[i]
		if tc.Status.Status != constants.AIMStatusReady {
			if deleteErr := r.Delete(ctx, tc); deleteErr != nil && !apierrors.IsNotFound(deleteErr) {
				// If namespace is terminating, continue
				if apierrors.IsForbidden(deleteErr) {
					continue
				}
				errs = append(errs, fmt.Errorf("failed to delete template cache %s: %w", tc.Name, deleteErr))
			} else {
				logger.Info("Deleted non-available template cache during service cleanup",
					"templateCache", tc.Name,
					"service", service.Name,
					"cacheStatus", tc.Status.Status)
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("cleanup errors: %v", errs)
	}
	return nil
}
