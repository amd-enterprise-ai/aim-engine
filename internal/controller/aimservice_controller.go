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
	"slices"
	"sort"

	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
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
	// non-Ready AIMProfileCaches it created and their non-Ready shared
	// AIMArtifacts are torn down. Shared caches and shared artifacts that are
	// Ready are preserved for reuse by other services referencing the same
	// profile.
	finalizerCacheCleanup = "aim.eai.amd.com/cache-cleanup"

	// finalizerTemplateCacheCleanupLegacy is the legacy finalizer name. Any
	// existing service still carrying this string is migrated to
	// finalizerCacheCleanup on its next reconcile.
	finalizerTemplateCacheCleanupLegacy = "aim.eai.amd.com/template-cache-cleanup"
)

// AIMServiceReconciler reconciles AIMService objects through the profile
// pipeline.
type AIMServiceReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Clientset kubernetes.Interface

	// OpenTelemetryCollectorServed reports whether the cluster serves the
	// OpenTelemetryCollector API. When true the controller creates and watches
	// the metrics collector sidecar config of autoscaled predictors; the watch
	// cannot start against a missing API.
	OpenTelemetryCollectorServed bool

	// ScaledObjectServed reports whether the cluster serves the KEDA
	// ScaledObject API. When true the controller reads a predictor's
	// ScaledObject so it can remove it once the service stops autoscaling.
	ScaledObjectServed bool

	profileReconciler controllerutils.DomainReconciler[*aimv1alpha2.AIMService, *aimv1alpha1.AIMServiceStatus, profileservice.ServiceFetchResult, profileservice.ServiceObservation]
	profilePipeline   *controllerutils.Pipeline[*aimv1alpha2.AIMService, *aimv1alpha1.AIMServiceStatus, profileservice.ServiceFetchResult, profileservice.ServiceObservation]
}

// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimservices,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimservices/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimservices/finalizers,verbs=update
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimmodels,verbs=get;list;watch;create
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclustermodels,verbs=get;list;watch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimprofiles,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclusterprofiles,verbs=get;list;watch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimprofilecaches,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimartifacts,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimruntimeconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclusterruntimeconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups=serving.kserve.io,resources=inferenceservices,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=serving.kserve.io,resources=servingruntimes,verbs=get;create;patch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways,verbs=get;list;watch
// +kubebuilder:rbac:groups=keda.sh,resources=scaledobjects,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=opentelemetry.io,resources=opentelemetrycollectors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch
// +kubebuilder:rbac:groups=autoscaling,resources=horizontalpodautoscalers,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete

func (r *AIMServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var service aimv1alpha2.AIMService
	if err := r.Get(ctx, req.NamespacedName, &service); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to fetch AIMService")
		return ctrl.Result{}, err
	}

	// Finalizer for cache cleanup. The finalizer covers AIMProfileCache plus
	// non-Ready shared AIMArtifacts created for this service's profile cache.
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

	return r.profilePipeline.Run(ctx, &service)
}

// SetupWithManager sets up the controller with the Manager.
//
// internal/v1alpha2/aimservice.RegisterWatches registers the profile/model/
// runtimeconfig/profilecache indices, watches, and map funcs. Watches that
// belong to no domain (pods, events, HPAs, gateways, and the Owns()
// relationships on the generated KServe/HTTPRoute/PVC/ConfigMap/Job
// resources) stay in this file.
func (r *AIMServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	ctx := context.Background()
	c := mgr.GetClient()
	recorder := mgr.GetEventRecorderFor("aim-" + serviceName + "-controller")

	r.profileReconciler = &profileservice.ProfileServiceReconciler{
		Scheme:                       r.Scheme,
		Recorder:                     recorder,
		APIReader:                    mgr.GetAPIReader(),
		OpenTelemetryCollectorServed: r.OpenTelemetryCollectorServed,
		ScaledObjectServed:           r.ScaledObjectServed,
	}
	r.profilePipeline = &controllerutils.Pipeline[
		*aimv1alpha2.AIMService,
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
		For(&aimv1alpha2.AIMService{}).
		Owns(&servingv1beta1.InferenceService{}).
		Owns(&gatewayapiv1.HTTPRoute{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&batchv1.Job{})
	if r.OpenTelemetryCollectorServed {
		// A KServe-created collector is controlled by the InferenceService, so
		// Owns does not see it; the handover waits on its deletion.
		b = b.Owns(aimservice.NewOTelCollectorMetadata(), builder.OnlyMetadata).
			Watches(
				aimservice.NewOTelCollectorMetadata(),
				handler.EnqueueRequestsFromMapFunc(r.findServicesForInferenceServiceChild),
				builder.OnlyMetadata,
			)
	}

	b, err := profileservice.RegisterWatches(ctx, mgr, b, c)
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
			handler.EnqueueRequestsFromMapFunc(r.findServicesForPredictorObject),
		).
		// The predictor Deployment's status carries the rollout progress that
		// stale-child cleanup and the rollout verdict wait on.
		Watches(
			&appsv1.Deployment{},
			handler.EnqueueRequestsFromMapFunc(r.findServicesForPredictorObject),
		).
		Watches(
			&autoscalingv2.HorizontalPodAutoscaler{},
			handler.EnqueueRequestsFromMapFunc(r.findServicesForHPA),
			builder.WithPredicates(hpaRelevantChangePredicate()),
		).
		Watches(
			&gatewayapiv1.Gateway{},
			handler.EnqueueRequestsFromMapFunc(r.findServicesForGateway),
		).
		Named(serviceName).
		Complete(r)
}

// findServicesForGateway enqueues every AIMService when a Gateway changes.
// The host-pinning guard depends on the parent gateway's listener count, so a
// listener added or removed must re-evaluate services that route through it.
// Gateways change rarely and the gatewayRef can be supplied indirectly via
// runtime config (which is not indexed per service), so a cluster-wide enqueue
// is the simplest correct trigger.
func (r *AIMServiceReconciler) findServicesForGateway(ctx context.Context, _ client.Object) []reconcile.Request {
	var services aimv1alpha2.AIMServiceList
	if err := r.List(ctx, &services); err != nil {
		return nil
	}

	requests := make([]reconcile.Request, 0, len(services.Items))
	for i := range services.Items {
		svc := &services.Items[i]
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: svc.Name, Namespace: svc.Namespace},
		})
	}
	return requests
}

// findServicesForPredictorObject maps a predictor pod or Deployment, through
// the KServe InferenceService label, to the AIMService that owns it.
func (r *AIMServiceReconciler) findServicesForPredictorObject(ctx context.Context, obj client.Object) []reconcile.Request {
	isvcName, hasLabel := obj.GetLabels()[constants.LabelKServeInferenceService]
	if !hasLabel {
		return nil
	}
	return r.findServicesForInferenceService(ctx, obj.GetNamespace(), isvcName)
}

// findServicesForInferenceServiceChild maps an object that a KServe
// InferenceService controls, such as the collector KServe creates, to the
// AIMService that owns the InferenceService.
func (r *AIMServiceReconciler) findServicesForInferenceServiceChild(ctx context.Context, obj client.Object) []reconcile.Request {
	ref := metav1.GetControllerOfNoCopy(obj)
	if ref == nil || ref.Kind != "InferenceService" ||
		schema.FromAPIVersionAndKind(ref.APIVersion, ref.Kind).Group != servingv1beta1.SchemeGroupVersion.Group {
		return nil
	}
	return r.findServicesForInferenceService(ctx, obj.GetNamespace(), ref.Name)
}

// findServicesForInferenceService maps an InferenceService to the AIMService
// that owns it.
func (r *AIMServiceReconciler) findServicesForInferenceService(ctx context.Context, namespace, isvcName string) []reconcile.Request {
	isvc := &servingv1beta1.InferenceService{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: isvcName}, isvc); err != nil {
		return nil
	}

	for _, ownerRef := range isvc.OwnerReferences {
		if ownerRef.Kind == "AIMService" {
			return []reconcile.Request{{
				NamespacedName: types.NamespacedName{Name: ownerRef.Name, Namespace: namespace},
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

type hpaExternalMetricAvailability struct {
	name     string
	hasValue bool
}

type hpaConditionState struct {
	conditionType autoscalingv2.HorizontalPodAutoscalerConditionType
	status        corev1.ConditionStatus
	reason        string
}

// hpaRelevantChangePredicate accepts only HPA updates that can change
// AIMService status. In particular, it observes the shape and availability of
// external metrics without reconciling on every numeric metric sample.
func hpaRelevantChangePredicate() predicate.Predicate {
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
			if oldHPA.Generation != newHPA.Generation ||
				!int64PointersEqual(oldHPA.Status.ObservedGeneration, newHPA.Status.ObservedGeneration) {
				return true
			}
			if !slices.Equal(
				hpaExternalMetricAvailabilityFingerprint(oldHPA),
				hpaExternalMetricAvailabilityFingerprint(newHPA),
			) || !slices.Equal(
				hpaRelevantConditionFingerprint(oldHPA),
				hpaRelevantConditionFingerprint(newHPA),
			) {
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

func int64PointersEqual(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func hpaExternalMetricAvailabilityFingerprint(
	hpa *autoscalingv2.HorizontalPodAutoscaler,
) []hpaExternalMetricAvailability {
	fingerprint := make([]hpaExternalMetricAvailability, 0, len(hpa.Status.CurrentMetrics))
	for _, metric := range hpa.Status.CurrentMetrics {
		if metric.Type != autoscalingv2.ExternalMetricSourceType || metric.External == nil {
			continue
		}
		fingerprint = append(fingerprint, hpaExternalMetricAvailability{
			name: metric.External.Metric.Name,
			hasValue: metric.External.Current.Value != nil ||
				metric.External.Current.AverageValue != nil,
		})
	}
	sort.Slice(fingerprint, func(i, j int) bool {
		if fingerprint[i].name == fingerprint[j].name {
			return !fingerprint[i].hasValue && fingerprint[j].hasValue
		}
		return fingerprint[i].name < fingerprint[j].name
	})
	return fingerprint
}

func hpaRelevantConditionFingerprint(
	hpa *autoscalingv2.HorizontalPodAutoscaler,
) []hpaConditionState {
	fingerprint := make([]hpaConditionState, 0, 2)
	for _, condition := range hpa.Status.Conditions {
		if condition.Type != autoscalingv2.ScalingActive &&
			condition.Type != autoscalingv2.AbleToScale {
			continue
		}
		fingerprint = append(fingerprint, hpaConditionState{
			conditionType: condition.Type,
			status:        condition.Status,
			reason:        condition.Reason,
		})
	}
	sort.Slice(fingerprint, func(i, j int) bool {
		if fingerprint[i].conditionType != fingerprint[j].conditionType {
			return fingerprint[i].conditionType < fingerprint[j].conditionType
		}
		if fingerprint[i].status != fingerprint[j].status {
			return fingerprint[i].status < fingerprint[j].status
		}
		return fingerprint[i].reason < fingerprint[j].reason
	})
	return fingerprint
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
// preserved so other services referencing the same profile can continue to
// reuse them.
//
// Scope:
//   - AIMProfileCache labeled with this service (shared mode)
//   - AIMArtifact labeled with the profile cache name (shared artifacts created
//     by AIMProfileCache without owner references; dedicated-mode artifacts
//     cascade via owner refs and are not touched here)
//
// Forbidden / NotFound errors on List are treated as "namespace is being
// torn down" and skipped. Delete errors are aggregated so a single failure
// does not abandon the rest of the cleanup.
func (r *AIMServiceReconciler) cleanupCaches(ctx context.Context, service *aimv1alpha2.AIMService) error {
	serviceLabelValue, err := utils.SanitizeLabelValue(service.Name)
	if err != nil {
		return fmt.Errorf("failed to sanitize service name for label: %w", err)
	}

	logger := log.FromContext(ctx)

	deletedPCs, errs, terminating, err := r.cleanupProfileCaches(ctx, service, serviceLabelValue)
	if err != nil {
		return err
	}
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

// cleanupProfileCaches deletes non-Ready AIMProfileCaches labeled for this
// service and returns the names of the caches actually deleted so the
// caller can target matching shared artifacts. Ready caches are preserved
// for other services' use.
func (r *AIMServiceReconciler) cleanupProfileCaches(
	ctx context.Context,
	service *aimv1alpha2.AIMService,
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
	service *aimv1alpha2.AIMService,
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
