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
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/aimservicetemplate"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

const (
	clusterServiceTemplateName = "cluster-service-template"

	// clusterServiceTemplateRuntimeConfigIndexKey is used to index cluster service templates by runtime config name
	clusterServiceTemplateRuntimeConfigIndexKey = ".spec.runtimeConfigName"
)

// AIMClusterServiceTemplateReconciler reconciles a AIMClusterServiceTemplate object
type AIMClusterServiceTemplateReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Recorder  record.EventRecorder
	Clientset kubernetes.Interface

	reconciler controllerutils.DomainReconciler[
		*aimv1alpha1.AIMClusterServiceTemplate,
		*aimv1alpha1.AIMServiceTemplateStatus,
		aimservicetemplate.ClusterServiceTemplateFetchResult,
		aimservicetemplate.ClusterServiceTemplateObservation,
	]
	pipeline controllerutils.Pipeline[
		*aimv1alpha1.AIMClusterServiceTemplate,
		*aimv1alpha1.AIMServiceTemplateStatus,
		aimservicetemplate.ClusterServiceTemplateFetchResult,
		aimservicetemplate.ClusterServiceTemplateObservation,
	]
}

// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclusterservicetemplates,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclusterservicetemplates/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclusterservicetemplates/finalizers,verbs=update
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclusterruntimeconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimruntimeconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclustermodels,verbs=get;list;watch
// +kubebuilder:rbac:groups=serving.kserve.io,resources=clusterservingruntimes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods/log,verbs=get
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *AIMClusterServiceTemplateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the template
	var template aimv1alpha1.AIMClusterServiceTemplate
	if err := r.Get(ctx, req.NamespacedName, &template); err != nil {
		if apierrors.IsNotFound(err) {
			// Template was deleted - release any semaphore slot it might hold
			semaphoreKey := aimservicetemplate.JobKey("", req.Name) // Cluster-scoped, no namespace
			aimservicetemplate.GetGlobalSemaphore().Release(semaphoreKey)
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to fetch AIMClusterServiceTemplate")
		return ctrl.Result{}, err
	}

	if err := r.pipeline.Run(ctx, &template); err != nil {
		return ctrl.Result{}, err
	}

	// Check if the template is waiting for a semaphore slot and requeue if so.
	// This is needed because templates blocked by the concurrent limit don't get
	// automatically requeued when a slot becomes available.
	semaphoreKey := aimservicetemplate.JobKey("", req.Name) // Cluster-scoped, no namespace
	if !aimservicetemplate.GetGlobalSemaphore().IsHeld(semaphoreKey) &&
		template.Status.Status != constants.AIMStatusReady &&
		len(template.Spec.ModelSources) == 0 {
		// Template needs discovery but doesn't hold a semaphore slot.
		// Check if semaphore is at capacity - if so, requeue with delay.
		if aimservicetemplate.GetGlobalSemaphore().AvailableSlots() == 0 {
			logger.V(1).Info("cluster template waiting for semaphore slot, requeuing",
				"semaphoreKey", semaphoreKey)
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *AIMClusterServiceTemplateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	ctx := context.Background()

	// Initialize the domain reconciler
	r.reconciler = &aimservicetemplate.ClusterServiceTemplateReconciler{
		Clientset: r.Clientset,
		Scheme:    r.Scheme,
	}

	// Initialize the pipeline
	r.pipeline = controllerutils.Pipeline[
		*aimv1alpha1.AIMClusterServiceTemplate,
		*aimv1alpha1.AIMServiceTemplateStatus,
		aimservicetemplate.ClusterServiceTemplateFetchResult,
		aimservicetemplate.ClusterServiceTemplateObservation,
	]{
		Client:         mgr.GetClient(),
		StatusClient:   mgr.GetClient().Status(),
		Recorder:       r.Recorder,
		ControllerName: clusterServiceTemplateName,
		Reconciler:     r.reconciler,
		Scheme:         r.Scheme,
		Clientset:      r.Clientset,
	}
	r.Recorder = mgr.GetEventRecorderFor(r.pipeline.GetFullName())
	r.pipeline.Recorder = r.Recorder

	// Index AIMClusterServiceTemplate by runtimeConfigName for efficient lookup when config changes
	if err := mgr.GetFieldIndexer().IndexField(ctx, &aimv1alpha1.AIMClusterServiceTemplate{}, clusterServiceTemplateRuntimeConfigIndexKey, func(obj client.Object) []string {
		template, ok := obj.(*aimv1alpha1.AIMClusterServiceTemplate)
		if !ok {
			return nil
		}
		// Return default if not specified
		name := template.Spec.Name
		if name == "" {
			name = "default"
		}
		return []string{name}
	}); err != nil {
		return err
	}

	// Handler for cluster-scoped RuntimeConfig changes
	clusterRuntimeConfigHandler := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		config, ok := obj.(*aimv1alpha1.AIMClusterRuntimeConfig)
		if !ok {
			return nil
		}

		var templates aimv1alpha1.AIMClusterServiceTemplateList
		if err := r.List(ctx, &templates,
			client.MatchingFields{clusterServiceTemplateRuntimeConfigIndexKey: config.Name},
		); err != nil {
			log.FromContext(ctx).Error(err, "failed to list AIMClusterServiceTemplates for AIMClusterRuntimeConfig",
				"config", config.Name)
			return nil
		}

		return requestsFromClusterServiceTemplates(templates.Items)
	})

	// Handler for namespace-scoped RuntimeConfig changes (in operator namespace)
	runtimeConfigHandler := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		config, ok := obj.(*aimv1alpha1.AIMRuntimeConfig)
		if !ok {
			return nil
		}

		// Only process runtime configs in the operator namespace
		operatorNamespace := constants.GetOperatorNamespace()
		if config.Namespace != operatorNamespace {
			return nil
		}

		var templates aimv1alpha1.AIMClusterServiceTemplateList
		if err := r.List(ctx, &templates,
			client.MatchingFields{clusterServiceTemplateRuntimeConfigIndexKey: config.Name},
		); err != nil {
			log.FromContext(ctx).Error(err, "failed to list AIMClusterServiceTemplates for AIMRuntimeConfig",
				"config", config.Name, "namespace", config.Namespace)
			return nil
		}

		return requestsFromClusterServiceTemplates(templates.Items)
	})

	// Handler for node changes - reconcile templates that require GPUs
	nodeHandler := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		_, ok := obj.(*corev1.Node)
		if !ok {
			return nil
		}

		var templates aimv1alpha1.AIMClusterServiceTemplateList
		if err := r.List(ctx, &templates); err != nil {
			log.FromContext(ctx).Error(err, "failed to list AIMClusterServiceTemplates for Node event")
			return nil
		}

		// Filter to templates that require GPUs
		filtered := make([]aimv1alpha1.AIMClusterServiceTemplate, 0, len(templates.Items))
		for i := range templates.Items {
			if aimservicetemplate.TemplateRequiresGPU(templates.Items[i].Spec.AIMServiceTemplateSpecCommon) {
				filtered = append(filtered, templates.Items[i])
			}
		}

		return requestsFromClusterServiceTemplates(filtered)
	})

	// Handler for AIMClusterModel changes - reconcile templates that reference the model
	clusterModelHandler := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		model, ok := obj.(*aimv1alpha1.AIMClusterModel)
		if !ok {
			return nil
		}

		var templates aimv1alpha1.AIMClusterServiceTemplateList
		if err := r.List(ctx, &templates,
			client.MatchingFields{aimv1alpha1.ServiceTemplateModelNameIndexKey: model.Name},
		); err != nil {
			log.FromContext(ctx).Error(err, "failed to list AIMClusterServiceTemplates for AIMClusterModel",
				"model", model.Name)
			return nil
		}

		return requestsFromClusterServiceTemplates(templates.Items)
	})

	// Handler for discovery Pod changes - reconcile cluster template when pod status changes
	// Cluster-scoped templates run discovery jobs in the operator namespace
	discoveryPodHandler := handler.EnqueueRequestsFromMapFunc(r.findClusterTemplateForDiscoveryPod)

	return ctrl.NewControllerManagedBy(mgr).
		For(&aimv1alpha1.AIMClusterServiceTemplate{}).
		Owns(&batchv1.Job{}).
		Watches(&aimv1alpha1.AIMClusterRuntimeConfig{}, clusterRuntimeConfigHandler).
		Watches(&aimv1alpha1.AIMRuntimeConfig{}, runtimeConfigHandler).
		Watches(&corev1.Node{}, nodeHandler, builder.WithPredicates(utils.NodeGPUChangePredicate())).
		Watches(&aimv1alpha1.AIMClusterModel{}, clusterModelHandler).
		Watches(&corev1.Pod{}, discoveryPodHandler, builder.WithPredicates(clusterDiscoveryPodPredicate())).
		Named(clusterServiceTemplateName).
		Complete(r)
}

// findClusterTemplateForDiscoveryPod maps a discovery Pod to its owning AIMClusterServiceTemplate using the template label.
func (r *AIMClusterServiceTemplateReconciler) findClusterTemplateForDiscoveryPod(ctx context.Context, pod client.Object) []reconcile.Request {
	templateName, ok := pod.GetLabels()[constants.LabelKeyTemplate]
	if !ok || templateName == "" {
		return nil
	}

	// For cluster-scoped templates, the template is cluster-scoped (no namespace)
	return []reconcile.Request{
		{
			NamespacedName: client.ObjectKey{
				Name: templateName,
			},
		},
	}
}

// clusterDiscoveryPodPredicate filters pod events to only react to significant state changes
// for cluster-scoped service template discovery pods (which run in the operator namespace).
func clusterDiscoveryPodPredicate() predicate.Predicate {
	operatorNamespace := constants.GetOperatorNamespace()

	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			pod := e.Object.(*corev1.Pod)
			// Only care about pods in the operator namespace
			if pod.GetNamespace() != operatorNamespace {
				return false
			}
			// Only care about discovery pods (those with template label)
			if _, ok := pod.GetLabels()[constants.LabelKeyTemplate]; !ok {
				return false
			}
			// React to pods that have issues right away
			return hasClusterDiscoveryPodIssue(pod)
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			newPod := e.ObjectNew.(*corev1.Pod)
			// Only care about pods in the operator namespace
			if newPod.GetNamespace() != operatorNamespace {
				return false
			}
			// Only care about discovery pods (those with template label)
			if _, ok := newPod.GetLabels()[constants.LabelKeyTemplate]; !ok {
				return false
			}

			oldPod := e.ObjectOld.(*corev1.Pod)

			// React if issue status changed
			oldHasIssue := hasClusterDiscoveryPodIssue(oldPod)
			newHasIssue := hasClusterDiscoveryPodIssue(newPod)
			if oldHasIssue != newHasIssue {
				return true
			}

			// React if pod phase changed
			if oldPod.Status.Phase != newPod.Status.Phase {
				return true
			}

			// React to container status changes (for cases where phase doesn't change)
			if len(oldPod.Status.ContainerStatuses) != len(newPod.Status.ContainerStatuses) {
				return true
			}

			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			pod := e.Object
			// Only care about pods in the operator namespace with template label
			if pod.GetNamespace() != operatorNamespace {
				return false
			}
			_, ok := pod.GetLabels()[constants.LabelKeyTemplate]
			return ok
		},
	}
}

// hasClusterDiscoveryPodIssue checks if a pod has issues that should be reported.
func hasClusterDiscoveryPodIssue(pod *corev1.Pod) bool {
	// Check for image pull errors
	if utils.CheckPodImagePullStatus(pod) != nil {
		return true
	}

	// Check for crash loop backoff
	for _, status := range pod.Status.ContainerStatuses {
		if status.State.Waiting != nil && status.State.Waiting.Reason == "CrashLoopBackOff" {
			return true
		}
	}

	// Check for config errors
	for _, status := range pod.Status.ContainerStatuses {
		if status.State.Waiting != nil {
			reason := status.State.Waiting.Reason
			if reason == "CreateContainerConfigError" || reason == "CreateContainerError" {
				return true
			}
		}
	}

	return false
}

// requestsFromClusterServiceTemplates converts a list of cluster templates to reconcile requests.
func requestsFromClusterServiceTemplates(templates []aimv1alpha1.AIMClusterServiceTemplate) []reconcile.Request {
	if len(templates) == 0 {
		return nil
	}
	requests := make([]reconcile.Request, 0, len(templates))
	for _, tpl := range templates {
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name: tpl.Name,
			},
		})
	}
	return requests
}
