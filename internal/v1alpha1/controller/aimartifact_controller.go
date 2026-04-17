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
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
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
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/aimartifact"
)

const (
	artifactName = "artifact"

	// Container status reasons
	reasonCrashLoopBackOff           = "CrashLoopBackOff"
	reasonCreateContainerConfigError = "CreateContainerConfigError"
	reasonCreateContainerError       = "CreateContainerError"
)

// AIMArtifactReconciler reconciles a AIMArtifact object
type AIMArtifactReconciler struct {
	client.Client
	Clientset *kubernetes.Clientset
	Scheme    *runtime.Scheme
	Recorder  record.EventRecorder

	reconciler controllerutils.DomainReconciler[*aimv1alpha1.AIMArtifact, *aimv1alpha1.AIMArtifactStatus, aimartifact.ArtifactFetchResult, aimartifact.ArtifactObservation]
	pipeline   controllerutils.Pipeline[*aimv1alpha1.AIMArtifact, *aimv1alpha1.AIMArtifactStatus, aimartifact.ArtifactFetchResult, aimartifact.ArtifactObservation]
}

// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimartifacts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimartifacts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimartifacts/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods/log,verbs=get
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=storage.k8s.io,resources=storageclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=create;get;list;watch;patch;update
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles,resourceNames=artifact-status-updater,verbs=bind

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the AIMArtifact object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.22.4/pkg/reconcile
func (r *AIMArtifactReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the model
	var model aimv1alpha1.AIMArtifact
	if err := r.Get(ctx, req.NamespacedName, &model); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to fetch AIMArtifact")
		return ctrl.Result{}, err
	}

	var result ctrl.Result
	var pipelineErr error

	// Acquire the quota lock when PVC creation is possible.
	// This serializes the "evaluate quota + create PVC" operation across all
	// artifact reconciliations, preventing two artifacts from both passing
	// the quota check and both creating PVCs that together exceed the limit.
	if aimartifact.NeedsQuotaLock(&model) {
		lockErr := aimartifact.WithQuotaLock(ctx, r.Client, 30*time.Second, func() error {
			result, pipelineErr = r.pipeline.Run(ctx, &model)
			return nil
		})
		if lockErr != nil {
			logger.V(1).Info("Could not acquire quota lock, requeuing",
				"artifact", model.Name, "namespace", model.Namespace)
			return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
		}
		if pipelineErr != nil {
			return ctrl.Result{}, pipelineErr
		}
	} else {
		result, pipelineErr = r.pipeline.Run(ctx, &model)
		if pipelineErr != nil {
			return ctrl.Result{}, pipelineErr
		}
	}

	// If pipeline requests a requeue, honor it
	if result.RequeueAfter > 0 {
		return result, nil
	}

	// Requeue periodically while download is in progress to update progress status
	if model.Status.Status == constants.AIMStatusProgressing {
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	return ctrl.Result{}, nil
}

// downloadJobPodPredicate filters pod events to only react to significant state changes
// that should be reflected in the AIMArtifact status.
func downloadJobPodPredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			pod := e.Object.(*corev1.Pod)
			// React to pods that are created and have issues right away
			return hasSignificantPodIssue(pod)
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldPod := e.ObjectOld.(*corev1.Pod)
			newPod := e.ObjectNew.(*corev1.Pod)

			// Only react if the pod status changed in a significant way
			oldHasIssue := hasSignificantPodIssue(oldPod)
			newHasIssue := hasSignificantPodIssue(newPod)

			// React if issue status changed
			if oldHasIssue != newHasIssue {
				return true
			}

			// React if pod phase changed
			if oldPod.Status.Phase != newPod.Status.Phase {
				return true
			}

			// Also react to container status changes (for cases where phase doesn't change)
			// This catches transitions like Pending -> Pending with ErrImagePull added
			if len(oldPod.Status.ContainerStatuses) != len(newPod.Status.ContainerStatuses) {
				return true
			}
			if len(oldPod.Status.InitContainerStatuses) != len(newPod.Status.InitContainerStatuses) {
				return true
			}

			// Check if any container waiting reasons changed
			for i := range newPod.Status.ContainerStatuses {
				if i < len(oldPod.Status.ContainerStatuses) {
					oldWaiting := oldPod.Status.ContainerStatuses[i].State.Waiting
					newWaiting := newPod.Status.ContainerStatuses[i].State.Waiting
					if (oldWaiting == nil) != (newWaiting == nil) {
						return true
					}
					if oldWaiting != nil && newWaiting != nil && oldWaiting.Reason != newWaiting.Reason {
						return true
					}
				}
			}

			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			// Always react to pod deletions
			return true
		},
	}
}

// findArtifactsForRoleBinding maps a RoleBinding to all AIMArtifacts in the same namespace.
// This is used to reconcile artifacts when the status-updater RoleBinding is created/deleted.
func (r *AIMArtifactReconciler) findArtifactsForRoleBinding(ctx context.Context, obj client.Object) []ctrl.Request {
	rb, ok := obj.(*rbacv1.RoleBinding)
	if !ok {
		return nil
	}

	// Only care about our specific RoleBinding
	if rb.Name != "aim-engine-artifact-status-updater" {
		return nil
	}

	// Find all AIMArtifacts in the same namespace
	var caches aimv1alpha1.AIMArtifactList
	if err := r.List(ctx, &caches, client.InNamespace(rb.Namespace)); err != nil {
		log.FromContext(ctx).Error(err, "failed to list AIMArtifacts for RoleBinding",
			"rolebinding", rb.Name, "namespace", rb.Namespace)
		return nil
	}

	requests := make([]ctrl.Request, len(caches.Items))
	for i := range caches.Items {
		requests[i] = ctrl.Request{
			NamespacedName: client.ObjectKey{
				Namespace: caches.Items[i].Namespace,
				Name:      caches.Items[i].Name,
			},
		}
	}

	return requests
}

// roleBindingPredicate filters RoleBinding events to only the status-updater RoleBinding.
func roleBindingPredicate() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return obj.GetName() == "aim-engine-artifact-status-updater"
	})
}

// hasSignificantPodIssue checks if a pod has any issues that should trigger reconciliation
func hasSignificantPodIssue(pod *corev1.Pod) bool {
	// Check for image pull errors using existing utility
	if utils.CheckPodImagePullStatus(pod) != nil {
		return true
	}

	// Check for crash loop backoff
	for _, status := range pod.Status.ContainerStatuses {
		if status.State.Waiting != nil && status.State.Waiting.Reason == reasonCrashLoopBackOff {
			return true
		}
	}

	// Check init containers for crash loop backoff too
	for _, status := range pod.Status.InitContainerStatuses {
		if status.State.Waiting != nil && status.State.Waiting.Reason == reasonCrashLoopBackOff {
			return true
		}
	}

	// Check for config errors (CreateContainerConfigError, CreateContainerError)
	for _, status := range pod.Status.ContainerStatuses {
		if status.State.Waiting != nil {
			reason := status.State.Waiting.Reason
			if reason == reasonCreateContainerConfigError || reason == reasonCreateContainerError {
				return true
			}
		}
	}

	// Check init containers for config errors too
	for _, status := range pod.Status.InitContainerStatuses {
		if status.State.Waiting != nil {
			reason := status.State.Waiting.Reason
			if reason == reasonCreateContainerConfigError || reason == reasonCreateContainerError {
				return true
			}
		}
	}

	// Check if pod is pending for too long or has scheduling issues
	if pod.Status.Phase == corev1.PodPending {
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodScheduled && condition.Status == corev1.ConditionFalse {
				// Scheduling issues like insufficient resources, node selector mismatch
				if condition.Reason == "Unschedulable" {
					return true
				}
			}
		}
	}

	return false
}

// findArtifactForPod maps a Pod to its owning AIMArtifact using the cache.name label.
func (r *AIMArtifactReconciler) findArtifactForPod(ctx context.Context, pod client.Object) []ctrl.Request {
	cacheName, ok := pod.GetLabels()[constants.LabelKeyCacheName]
	if !ok || cacheName == "" {
		return nil
	}

	return []ctrl.Request{
		{
			NamespacedName: client.ObjectKey{
				Namespace: pod.GetNamespace(),
				Name:      cacheName,
			},
		},
	}
}

// findArtifactsForNamespace enqueues all AIMArtifacts in a namespace when
// the namespace's storage quota annotation changes.
func (r *AIMArtifactReconciler) findArtifactsForNamespace(ctx context.Context, obj client.Object) []ctrl.Request {
	ns, ok := obj.(*corev1.Namespace)
	if !ok {
		return nil
	}

	var artifacts aimv1alpha1.AIMArtifactList
	if err := r.List(ctx, &artifacts, client.InNamespace(ns.Name)); err != nil {
		log.FromContext(ctx).Error(err, "failed to list AIMArtifacts for Namespace",
			"namespace", ns.Name)
		return nil
	}

	requests := make([]ctrl.Request, len(artifacts.Items))
	for i := range artifacts.Items {
		requests[i] = ctrl.Request{
			NamespacedName: client.ObjectKeyFromObject(&artifacts.Items[i]),
		}
	}
	return requests
}

// namespaceQuotaPredicate triggers only when the artifact storage quota annotation changes.
func namespaceQuotaPredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			_, has := e.Object.GetAnnotations()[aimv1alpha1.ArtifactStorageQuotaAnnotation]
			return has
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldVal := e.ObjectOld.GetAnnotations()[aimv1alpha1.ArtifactStorageQuotaAnnotation]
			newVal := e.ObjectNew.GetAnnotations()[aimv1alpha1.ArtifactStorageQuotaAnnotation]
			return oldVal != newVal
		},
		DeleteFunc: func(_ event.DeleteEvent) bool { return false },
	}
}

// findAllArtifactsForClusterConfig enqueues every AIMArtifact in the cluster
// when the cluster runtime config's quota settings change.
func (r *AIMArtifactReconciler) findAllArtifactsForClusterConfig(ctx context.Context, _ client.Object) []ctrl.Request {
	var artifacts aimv1alpha1.AIMArtifactList
	if err := r.List(ctx, &artifacts); err != nil {
		log.FromContext(ctx).Error(err, "failed to list AIMArtifacts for ClusterRuntimeConfig change")
		return nil
	}

	requests := make([]ctrl.Request, len(artifacts.Items))
	for i := range artifacts.Items {
		requests[i] = ctrl.Request{
			NamespacedName: client.ObjectKeyFromObject(&artifacts.Items[i]),
		}
	}
	return requests
}

// clusterQuotaPredicate triggers only when the ArtifactStorageQuota field changes.
func clusterQuotaPredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			cfg, ok := e.Object.(*aimv1alpha1.AIMClusterRuntimeConfig)
			return ok && cfg.Spec.ArtifactStorageQuota != nil
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldCfg, ok1 := e.ObjectOld.(*aimv1alpha1.AIMClusterRuntimeConfig)
			newCfg, ok2 := e.ObjectNew.(*aimv1alpha1.AIMClusterRuntimeConfig)
			if !ok1 || !ok2 {
				return false
			}
			oldHas := oldCfg.Spec.ArtifactStorageQuota != nil
			newHas := newCfg.Spec.ArtifactStorageQuota != nil
			if oldHas != newHas {
				return true
			}
			if !oldHas {
				return false
			}
			// Both have quota; trigger if either limit changed.
			return !quotaEqual(oldCfg.Spec.ArtifactStorageQuota, newCfg.Spec.ArtifactStorageQuota)
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			cfg, ok := e.Object.(*aimv1alpha1.AIMClusterRuntimeConfig)
			return ok && cfg.Spec.ArtifactStorageQuota != nil
		},
	}
}

func quotaEqual(a, b *aimv1alpha1.AIMArtifactStorageQuota) bool {
	clEq := quantityPtrEqual(a.ClusterLimit, b.ClusterLimit)
	nsEq := quantityPtrEqual(a.DefaultNamespaceLimit, b.DefaultNamespaceLimit)
	return clEq && nsEq
}

func quantityPtrEqual(a, b *resource.Quantity) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Cmp(*b) == 0
}

// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclusterruntimeconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimtemplatecaches,verbs=get;list;watch
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch

// SetupWithManager sets up the controller with the Manager.
func (r *AIMArtifactReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.reconciler = &aimartifact.ArtifactReconciler{
		Clientset: r.Clientset,
		Scheme:    r.Scheme,
		APIReader: mgr.GetAPIReader(),
		Recorder:  r.Recorder,
	}
	r.pipeline = controllerutils.Pipeline[
		*aimv1alpha1.AIMArtifact,
		*aimv1alpha1.AIMArtifactStatus,
		aimartifact.ArtifactFetchResult,
		aimartifact.ArtifactObservation,
	]{
		Client:         mgr.GetClient(),
		StatusClient:   mgr.GetClient().Status(),
		Recorder:       r.Recorder,
		ControllerName: artifactName,
		Reconciler:     r.reconciler,
		Scheme:         r.Scheme,
		Clientset:      r.Clientset,
	}
	r.Recorder = mgr.GetEventRecorderFor(r.pipeline.GetFullName())
	r.pipeline.Recorder = r.Recorder

	return ctrl.NewControllerManagedBy(mgr).
		For(&aimv1alpha1.AIMArtifact{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&batchv1.Job{}).
		Watches(
			&corev1.Pod{},
			handler.EnqueueRequestsFromMapFunc(r.findArtifactForPod),
			builder.WithPredicates(downloadJobPodPredicate()),
		).
		Watches(
			&rbacv1.RoleBinding{},
			handler.EnqueueRequestsFromMapFunc(r.findArtifactsForRoleBinding),
			builder.WithPredicates(roleBindingPredicate()),
		).
		Watches(
			&corev1.Namespace{},
			handler.EnqueueRequestsFromMapFunc(r.findArtifactsForNamespace),
			builder.WithPredicates(namespaceQuotaPredicate()),
		).
		Watches(
			&aimv1alpha1.AIMClusterRuntimeConfig{},
			handler.EnqueueRequestsFromMapFunc(r.findAllArtifactsForClusterConfig),
			builder.WithPredicates(clusterQuotaPredicate()),
		).
		Named(artifactName).
		Complete(r)
}
