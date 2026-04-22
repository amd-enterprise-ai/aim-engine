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
	autoscalingv2 "k8s.io/api/autoscaling/v2"
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
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/aimservice"
	profileservice "github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimservice"
)

const (
	serviceName = "service"

	// finalizerCacheCleanup guards the AIMService against deletion until any
	// non-Ready caches it created (AIMTemplateCache, AIMProfileCache) and
	// their non-Ready shared AIMArtifacts are torn down. Shared caches and
	// shared artifacts that are Ready are preserved for reuse by other
	// services referencing the same template or profile.
	finalizerCacheCleanup = "aim.eai.amd.com/cache-cleanup"

	// finalizerTemplateCacheCleanupLegacy is the legacy finalizer name. Any
	// existing service still carrying this string is migrated to
	// finalizerCacheCleanup on its next reconcile.
	finalizerTemplateCacheCleanupLegacy = "aim.eai.amd.com/template-cache-cleanup"
)

// AIMServiceReconciler reconciles AIMService objects.
// It dispatches to the template pipeline (v1alpha1) or profile pipeline (v1alpha2)
// based on whether spec.profile is set.
type AIMServiceReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Recorder  record.EventRecorder
	Clientset kubernetes.Interface

	// templateReconciler handles the v1alpha1 template-based path.
	templateReconciler controllerutils.DomainReconciler[*aimv1alpha1.AIMService, *aimv1alpha1.AIMServiceStatus, aimservice.ServiceFetchResult, aimservice.ServiceObservation]
	templatePipeline   controllerutils.Pipeline[*aimv1alpha1.AIMService, *aimv1alpha1.AIMServiceStatus, aimservice.ServiceFetchResult, aimservice.ServiceObservation]

	// profileReconciler handles the v1alpha2 profile-based path.
	profileReconciler controllerutils.DomainReconciler[*aimv1alpha1.AIMService, *aimv1alpha1.AIMServiceStatus, profileservice.ServiceFetchResult, profileservice.ServiceObservation]
	profilePipeline   controllerutils.Pipeline[*aimv1alpha1.AIMService, *aimv1alpha1.AIMServiceStatus, profileservice.ServiceFetchResult, profileservice.ServiceObservation]
}

// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimservices,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimservices/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimservices/finalizers,verbs=update
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimmodels,verbs=get;list;watch;create
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclustermodels,verbs=get;list;watch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimservicetemplates,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclusterservicetemplates,verbs=get;list;watch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimprofiles,verbs=get;list;watch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclusterprofiles,verbs=get;list;watch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimtemplatecaches,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimprofilecaches,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimartifacts,verbs=get;list;watch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimruntimeconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclusterruntimeconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups=serving.kserve.io,resources=inferenceservices,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch
// +kubebuilder:rbac:groups=autoscaling,resources=horizontalpodautoscalers,verbs=get;list;watch

func (r *AIMServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var service aimv1alpha1.AIMService
	if err := r.Get(ctx, req.NamespacedName, &service); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to fetch AIMService")
		return ctrl.Result{}, err
	}

	// Finalizer for cache cleanup. The finalizer covers AIMTemplateCache
	// (v1alpha1) and AIMProfileCache (v1alpha2) plus non-Ready shared
	// AIMArtifacts created for this service's profile cache.
	hasCurrent := controllerutil.ContainsFinalizer(&service, finalizerCacheCleanup)
	hasLegacy := controllerutil.ContainsFinalizer(&service, finalizerTemplateCacheCleanupLegacy)

	if service.DeletionTimestamp != nil {
		if !hasCurrent && !hasLegacy {
			return ctrl.Result{}, nil
		}

		namespaceTerminating, err := IsNamespaceTerminating(ctx, r.Client, service.Namespace)
		if err != nil {
			if apierrors.IsForbidden(err) {
				logger.Info("Failed to read namespace during deletion, assuming namespace is terminating",
					"namespace", service.Namespace,
					"service", service.Name)
				namespaceTerminating = true
			} else {
				logger.Error(err, "Failed to check namespace termination", "namespace", service.Namespace)
				return ctrl.Result{}, err
			}
		}

		if namespaceTerminating {
			logger.Info("Namespace is terminating, skipping cache cleanup before finalizer removal",
				"namespace", service.Namespace,
				"service", service.Name)
		} else {
			if err := r.cleanupCaches(ctx, &service); err != nil {
				logger.Error(err, "Failed to cleanup caches")
				return ctrl.Result{}, err
			}
		}

		// Use a metadata-scoped Patch rather than a full Update: spec is
		// not changed here, so we send only the finalizer diff. This keeps
		// the over-the-wire request minimal and avoids any risk of the
		// typed spec being mutated by client-side round-trip during
		// finalizer removal.
		patch := client.MergeFrom(service.DeepCopy())
		controllerutil.RemoveFinalizer(&service, finalizerCacheCleanup)
		controllerutil.RemoveFinalizer(&service, finalizerTemplateCacheCleanupLegacy)
		if err := r.Patch(ctx, &service, patch); err != nil {
			if apierrors.IsNotFound(err) {
				return ctrl.Result{}, nil
			}
			if apierrors.IsConflict(err) {
				return ctrl.Result{Requeue: true}, nil
			}
			return ctrl.Result{}, err
		}
		logger.Info("Removed service cleanup finalizer",
			"service", service.Name,
			"namespace", service.Namespace)
		return ctrl.Result{}, nil
	}

	// Ensure the current finalizer is present and strip the legacy one if
	// present. Metadata-scoped patch; see rationale above.
	if !hasCurrent || hasLegacy {
		patch := client.MergeFrom(service.DeepCopy())
		controllerutil.AddFinalizer(&service, finalizerCacheCleanup)
		controllerutil.RemoveFinalizer(&service, finalizerTemplateCacheCleanupLegacy)
		if err := r.Patch(ctx, &service, patch); err != nil {
			if apierrors.IsConflict(err) {
				return ctrl.Result{Requeue: true}, nil
			}
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Dispatch based on which reference the user supplied. Mutual
	// exclusivity between spec.profile and spec.template is enforced by
	// CEL validation on the AIMService type.
	if service.Spec.Profile != nil {
		return r.profilePipeline.Run(ctx, &service)
	}
	return r.templatePipeline.Run(ctx, &service)
}

// SetupWithManager sets up the controller with the Manager.
//
// Wiring is split across packages so each domain owns its own watches:
//   - internal/v1alpha1/aimservice.RegisterWatches registers template/model/
//     runtimeconfig/templatecache indices, watches, and map funcs.
//   - internal/v1alpha2/aimservice.RegisterWatches registers profile/
//     clusterprofile/profilecache indices, watches, and map funcs.
//
// Shared watches that belong to neither domain (pods, events, HPAs, and the
// Owns() relationships on the generated KServe/HTTPRoute/PVC/ConfigMap
// resources) stay in this file.
func (r *AIMServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	ctx := context.Background()
	c := mgr.GetClient()
	recorder := mgr.GetEventRecorderFor("aim-" + serviceName + "-controller")

	r.templateReconciler = &aimservice.ServiceReconciler{
		Clientset: r.Clientset,
		Scheme:    r.Scheme,
	}
	r.templatePipeline = controllerutils.Pipeline[
		*aimv1alpha1.AIMService,
		*aimv1alpha1.AIMServiceStatus,
		aimservice.ServiceFetchResult,
		aimservice.ServiceObservation,
	]{
		Client:         c,
		StatusClient:   c.Status(),
		Recorder:       recorder,
		ControllerName: serviceName,
		Reconciler:     r.templateReconciler,
		Scheme:         r.Scheme,
		Clientset:      r.Clientset,
	}

	r.profileReconciler = &profileservice.ProfileServiceReconciler{
		Scheme: r.Scheme,
	}
	r.profilePipeline = controllerutils.Pipeline[
		*aimv1alpha1.AIMService,
		*aimv1alpha1.AIMServiceStatus,
		profileservice.ServiceFetchResult,
		profileservice.ServiceObservation,
	]{
		Client:         c,
		StatusClient:   c.Status(),
		Recorder:       recorder,
		ControllerName: serviceName,
		Reconciler:     r.profileReconciler,
		Scheme:         r.Scheme,
		Clientset:      r.Clientset,
	}

	r.Recorder = recorder

	if err := mgr.GetFieldIndexer().IndexField(ctx, &corev1.Event{}, "involvedObject.name", func(obj client.Object) []string {
		event, ok := obj.(*corev1.Event)
		if !ok {
			return nil
		}
		return []string{event.InvolvedObject.Name}
	}); err != nil {
		return err
	}

	b := ctrl.NewControllerManagedBy(mgr).
		For(&aimv1alpha1.AIMService{}).
		Owns(&servingv1beta1.InferenceService{}).
		Owns(&gatewayapiv1.HTTPRoute{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&corev1.ConfigMap{})

	b, err := aimservice.RegisterWatches(ctx, mgr, b, c)
	if err != nil {
		return err
	}
	b, err = profileservice.RegisterWatches(ctx, mgr, b, c)
	if err != nil {
		return err
	}

	return b.
		Watches(
			&corev1.Event{},
			handler.EnqueueRequestsFromMapFunc(r.findServicesForInferenceServiceEvent),
		).
		Watches(
			&corev1.Pod{},
			handler.EnqueueRequestsFromMapFunc(r.findServicesForInferenceServicePod),
		).
		Watches(
			&autoscalingv2.HorizontalPodAutoscaler{},
			handler.EnqueueRequestsFromMapFunc(r.findServicesForHPA),
			builder.WithPredicates(hpaReplicaChangePredicate()),
		).
		Named(serviceName).
		Complete(r)
}

func (r *AIMServiceReconciler) findServicesForInferenceServicePod(ctx context.Context, obj client.Object) []reconcile.Request {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return nil
	}

	isvcName, hasLabel := pod.Labels[constants.LabelKServeInferenceService]
	if !hasLabel {
		return nil
	}

	isvc := &servingv1beta1.InferenceService{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: pod.Namespace, Name: isvcName}, isvc); err != nil {
		return nil
	}

	for _, ownerRef := range isvc.OwnerReferences {
		if ownerRef.Kind == "AIMService" {
			return []reconcile.Request{{
				NamespacedName: types.NamespacedName{Name: ownerRef.Name, Namespace: pod.Namespace},
			}}
		}
	}
	return nil
}

func (r *AIMServiceReconciler) findServicesForInferenceServiceEvent(ctx context.Context, obj client.Object) []reconcile.Request {
	evt, ok := obj.(*corev1.Event)
	if !ok {
		return nil
	}

	if evt.InvolvedObject.Kind != "InferenceService" || evt.Type != corev1.EventTypeWarning {
		return nil
	}

	isvc := &servingv1beta1.InferenceService{}
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: evt.InvolvedObject.Namespace,
		Name:      evt.InvolvedObject.Name,
	}, isvc); err != nil {
		return nil
	}

	for _, ownerRef := range isvc.OwnerReferences {
		if ownerRef.Kind == "AIMService" {
			return []reconcile.Request{{
				NamespacedName: types.NamespacedName{Name: ownerRef.Name, Namespace: evt.InvolvedObject.Namespace},
			}}
		}
	}
	return nil
}

func hpaReplicaChangePredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool { return true },
		DeleteFunc: func(e event.DeleteEvent) bool { return true },
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldHPA, ok := e.ObjectOld.(*autoscalingv2.HorizontalPodAutoscaler)
			if !ok {
				return false
			}
			newHPA, ok := e.ObjectNew.(*autoscalingv2.HorizontalPodAutoscaler)
			if !ok {
				return false
			}

			if oldHPA.Status.CurrentReplicas != newHPA.Status.CurrentReplicas ||
				oldHPA.Status.DesiredReplicas != newHPA.Status.DesiredReplicas {
				return true
			}

			oldMin, newMin := int32(1), int32(1)
			if oldHPA.Spec.MinReplicas != nil {
				oldMin = *oldHPA.Spec.MinReplicas
			}
			if newHPA.Spec.MinReplicas != nil {
				newMin = *newHPA.Spec.MinReplicas
			}
			return oldMin != newMin || oldHPA.Spec.MaxReplicas != newHPA.Spec.MaxReplicas
		},
		GenericFunc: func(e event.GenericEvent) bool { return false },
	}
}

func (r *AIMServiceReconciler) findServicesForHPA(ctx context.Context, obj client.Object) []reconcile.Request {
	hpa, ok := obj.(*autoscalingv2.HorizontalPodAutoscaler)
	if !ok {
		return nil
	}

	svcName, hasLabel := hpa.Labels[constants.LabelService]
	if !hasLabel {
		return nil
	}

	return []reconcile.Request{{
		NamespacedName: types.NamespacedName{Name: svcName, Namespace: hpa.Namespace},
	}}
}

// cleanupCaches deletes non-Ready caches and shared artifacts that were
// created on behalf of the service being deleted. Ready resources are
// preserved so other services referencing the same template or profile can
// continue to reuse them.
//
// Scope:
//   - AIMTemplateCache labeled with this service (v1alpha1 template path)
//   - AIMProfileCache labeled with this service (v1alpha2 profile path, shared mode)
//   - AIMArtifact labeled with the profile cache name (shared artifacts created
//     by AIMProfileCache without owner references; dedicated-mode artifacts
//     cascade via owner refs and are not touched here)
//
// Forbidden / NotFound errors on List are treated as "namespace is being
// torn down" and skipped. Delete errors are aggregated so a single failure
// does not abandon the rest of the cleanup.
func (r *AIMServiceReconciler) cleanupCaches(ctx context.Context, service *aimv1alpha1.AIMService) error {
	serviceLabelValue, err := utils.SanitizeLabelValue(service.Name)
	if err != nil {
		return fmt.Errorf("failed to sanitize service name for label: %w", err)
	}

	logger := log.FromContext(ctx)
	var errs []error

	tcErrs, terminating, err := r.cleanupTemplateCaches(ctx, service, serviceLabelValue)
	if err != nil {
		return err
	}
	errs = append(errs, tcErrs...)
	if terminating {
		logger.Info("Skipping cleanup, namespace may be terminating", "service", service.Name)
		return joinCleanupErrs(errs)
	}

	deletedPCs, pcErrs, terminating, err := r.cleanupProfileCaches(ctx, service, serviceLabelValue)
	if err != nil {
		return err
	}
	errs = append(errs, pcErrs...)
	if terminating {
		logger.Info("Skipping artifact cleanup, namespace may be terminating", "service", service.Name)
		return joinCleanupErrs(errs)
	}

	errs = append(errs, r.cleanupSharedArtifactsForCaches(ctx, service, deletedPCs)...)
	return joinCleanupErrs(errs)
}

func joinCleanupErrs(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("cleanup errors: %v", errs)
}

// cleanupTemplateCaches deletes non-Ready AIMTemplateCaches labeled for this
// service. Returns (deleteErrs, namespaceTerminating, fatalErr). A fatal
// error (e.g. transient List failure) aborts the entire cleanup; the
// terminating flag signals the caller to stop without treating it as an
// error.
func (r *AIMServiceReconciler) cleanupTemplateCaches(
	ctx context.Context,
	service *aimv1alpha1.AIMService,
	serviceLabel string,
) ([]error, bool, error) {
	logger := log.FromContext(ctx)
	var list aimv1alpha1.AIMTemplateCacheList
	if err := r.List(ctx, &list,
		client.InNamespace(service.Namespace),
		client.MatchingLabels{constants.LabelService: serviceLabel},
	); err != nil {
		if apierrors.IsForbidden(err) || apierrors.IsNotFound(err) {
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("failed to list template caches for cleanup: %w", err)
	}
	var errs []error
	for i := range list.Items {
		tc := &list.Items[i]
		if tc.Status.Status == constants.AIMStatusReady {
			continue
		}
		if deleteErr := r.Delete(ctx, tc); deleteErr != nil && !apierrors.IsNotFound(deleteErr) {
			if apierrors.IsForbidden(deleteErr) {
				continue
			}
			errs = append(errs, fmt.Errorf("failed to delete template cache %s: %w", tc.Name, deleteErr))
			continue
		}
		logger.Info("Deleted non-ready template cache during service cleanup",
			"templateCache", tc.Name,
			"service", service.Name,
			"cacheStatus", tc.Status.Status)
	}
	return errs, false, nil
}

// cleanupProfileCaches deletes non-Ready AIMProfileCaches labeled for this
// service and returns the names of the caches actually deleted so the
// caller can target matching shared artifacts. Ready caches are preserved
// for other services' use.
func (r *AIMServiceReconciler) cleanupProfileCaches(
	ctx context.Context,
	service *aimv1alpha1.AIMService,
	serviceLabel string,
) ([]string, []error, bool, error) {
	logger := log.FromContext(ctx)
	var list aimv1alpha2.AIMProfileCacheList
	if err := r.List(ctx, &list,
		client.InNamespace(service.Namespace),
		client.MatchingLabels{constants.LabelService: serviceLabel},
	); err != nil {
		if apierrors.IsForbidden(err) || apierrors.IsNotFound(err) {
			return nil, nil, true, nil
		}
		return nil, nil, false, fmt.Errorf("failed to list profile caches for cleanup: %w", err)
	}
	var (
		deleted []string
		errs    []error
	)
	for i := range list.Items {
		pc := &list.Items[i]
		if pc.Status.Status == constants.AIMStatusReady {
			continue
		}
		if deleteErr := r.Delete(ctx, pc); deleteErr != nil && !apierrors.IsNotFound(deleteErr) {
			if apierrors.IsForbidden(deleteErr) {
				continue
			}
			errs = append(errs, fmt.Errorf("failed to delete profile cache %s: %w", pc.Name, deleteErr))
			continue
		}
		deleted = append(deleted, pc.Name)
		logger.Info("Deleted non-ready profile cache during service cleanup",
			"profileCache", pc.Name,
			"service", service.Name,
			"cacheStatus", pc.Status.Status)
	}
	return deleted, errs, false, nil
}

// cleanupSharedArtifactsForCaches deletes non-Ready shared AIMArtifacts
// (those without owner references) labeled with any of the given profile
// cache names. Dedicated-mode artifacts have owner refs and are
// garbage-collected when the profile cache is deleted.
func (r *AIMServiceReconciler) cleanupSharedArtifactsForCaches(
	ctx context.Context,
	service *aimv1alpha1.AIMService,
	cacheNames []string,
) []error {
	logger := log.FromContext(ctx)
	var errs []error
	for _, pcName := range cacheNames {
		pcLabelValue, sanitizeErr := utils.SanitizeLabelValue(pcName)
		if sanitizeErr != nil {
			errs = append(errs, fmt.Errorf("failed to sanitize profile cache name for label: %w", sanitizeErr))
			continue
		}
		var artifacts aimv1alpha1.AIMArtifactList
		if err := r.List(ctx, &artifacts,
			client.InNamespace(service.Namespace),
			client.MatchingLabels{constants.LabelProfileCacheName: pcLabelValue},
		); err != nil {
			if apierrors.IsForbidden(err) || apierrors.IsNotFound(err) {
				continue
			}
			errs = append(errs, fmt.Errorf("failed to list artifacts for profile cache %s: %w", pcName, err))
			continue
		}
		for i := range artifacts.Items {
			art := &artifacts.Items[i]
			if len(art.GetOwnerReferences()) > 0 {
				continue
			}
			if art.Status.Status == constants.AIMStatusReady {
				continue
			}
			if deleteErr := r.Delete(ctx, art); deleteErr != nil && !apierrors.IsNotFound(deleteErr) {
				if apierrors.IsForbidden(deleteErr) {
					continue
				}
				errs = append(errs, fmt.Errorf("failed to delete artifact %s: %w", art.Name, deleteErr))
				continue
			}
			logger.Info("Deleted non-ready shared artifact during service cleanup",
				"artifact", art.Name,
				"profileCache", pcName,
				"service", service.Name,
				"artifactStatus", art.Status.Status)
		}
	}
	return errs
}
