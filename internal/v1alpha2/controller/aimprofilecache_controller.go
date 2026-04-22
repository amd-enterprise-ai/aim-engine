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
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	sharedcontroller "github.com/amd-enterprise-ai/aim-engine/internal/controller"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimprofilecache"
)

const (
	profileCacheName = "profile-cache"

	finalizerProfileCacheArtifactCleanup = "aim.eai.amd.com/profile-cache-artifact-cleanup"
)

// AIMProfileCacheReconciler reconciles AIMProfileCache objects.
type AIMProfileCacheReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Recorder  record.EventRecorder
	Clientset kubernetes.Interface

	reconciler controllerutils.DomainReconciler[
		*aimv1alpha2.AIMProfileCache,
		*aimv1alpha2.AIMProfileCacheStatus,
		aimprofilecache.ProfileCacheFetchResult,
		aimprofilecache.ProfileCacheObservation,
	]
	pipeline controllerutils.Pipeline[
		*aimv1alpha2.AIMProfileCache,
		*aimv1alpha2.AIMProfileCacheStatus,
		aimprofilecache.ProfileCacheFetchResult,
		aimprofilecache.ProfileCacheObservation,
	]
}

// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimprofilecaches,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimprofilecaches/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimprofilecaches/finalizers,verbs=update
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimprofiles,verbs=get;list;watch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclusterprofiles,verbs=get;list;watch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimartifacts,verbs=get;list;watch;create;update;patch;delete

func (r *AIMProfileCacheReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var pc aimv1alpha2.AIMProfileCache
	if err := r.Get(ctx, req.NamespacedName, &pc); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to fetch AIMProfileCache")
		return ctrl.Result{}, err
	}

	// Handle deletion with finalizer
	if pc.DeletionTimestamp != nil {
		if controllerutil.ContainsFinalizer(&pc, finalizerProfileCacheArtifactCleanup) {
			namespaceTerminating, err := sharedcontroller.IsNamespaceTerminating(ctx, r.Client, pc.Namespace)
			if err != nil {
				if apierrors.IsForbidden(err) {
					logger.Info("Failed to read namespace during deletion, assuming terminating",
						"namespace", pc.Namespace, "profileCache", pc.Name)
					namespaceTerminating = true
				} else {
					logger.Error(err, "Failed to check namespace termination", "namespace", pc.Namespace)
					return ctrl.Result{}, err
				}
			}

			if namespaceTerminating {
				logger.Info("Namespace is terminating, skipping artifact cleanup",
					"namespace", pc.Namespace, "profileCache", pc.Name)
			} else {
				if err := r.cleanupArtifacts(ctx, &pc); err != nil {
					logger.Error(err, "Failed to cleanup artifacts")
					return ctrl.Result{}, err
				}
			}

			// Metadata-scoped patch: send only the finalizer diff, never spec.
			patch := client.MergeFrom(pc.DeepCopy())
			controllerutil.RemoveFinalizer(&pc, finalizerProfileCacheArtifactCleanup)
			if err := r.Patch(ctx, &pc, patch); err != nil {
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

	// Ensure finalizer is present (metadata-scoped patch).
	if !controllerutil.ContainsFinalizer(&pc, finalizerProfileCacheArtifactCleanup) {
		patch := client.MergeFrom(pc.DeepCopy())
		controllerutil.AddFinalizer(&pc, finalizerProfileCacheArtifactCleanup)
		if err := r.Patch(ctx, &pc, patch); err != nil {
			if apierrors.IsConflict(err) {
				return ctrl.Result{Requeue: true}, nil
			}
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	return r.pipeline.Run(ctx, &pc)
}

func (r *AIMProfileCacheReconciler) SetupWithManager(mgr ctrl.Manager) error {
	ctx := context.Background()

	// Field indexes for efficient lookups
	if err := mgr.GetFieldIndexer().IndexField(ctx, &aimv1alpha2.AIMProfileCache{}, aimv1alpha2.ProfileCacheProfileNameIndexKey, func(obj client.Object) []string {
		pc := obj.(*aimv1alpha2.AIMProfileCache)
		return []string{pc.Spec.ProfileName}
	}); err != nil {
		return err
	}
	if err := mgr.GetFieldIndexer().IndexField(ctx, &aimv1alpha2.AIMProfileCache{}, aimv1alpha2.ProfileCacheProfileScopeIndexKey, func(obj client.Object) []string {
		pc := obj.(*aimv1alpha2.AIMProfileCache)
		return []string{string(pc.Spec.ProfileScope)}
	}); err != nil {
		return err
	}

	r.reconciler = &aimprofilecache.ProfileCacheReconciler{
		Scheme: r.Scheme,
	}

	r.pipeline = controllerutils.Pipeline[
		*aimv1alpha2.AIMProfileCache,
		*aimv1alpha2.AIMProfileCacheStatus,
		aimprofilecache.ProfileCacheFetchResult,
		aimprofilecache.ProfileCacheObservation,
	]{
		Client:         mgr.GetClient(),
		StatusClient:   mgr.GetClient().Status(),
		Recorder:       r.Recorder,
		ControllerName: profileCacheName,
		Reconciler:     r.reconciler,
		Scheme:         r.Scheme,
		Clientset:      r.Clientset,
	}
	r.Recorder = mgr.GetEventRecorderFor(r.pipeline.GetFullName())
	r.pipeline.Recorder = r.Recorder

	// Profile status change predicate — only reconcile when profile readiness changes
	profileStatusPredicate := predicate.Funcs{
		CreateFunc:  func(_ event.CreateEvent) bool { return true },
		DeleteFunc:  func(_ event.DeleteEvent) bool { return true },
		GenericFunc: func(_ event.GenericEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldStatus := getProfileStatus(e.ObjectOld)
			newStatus := getProfileStatus(e.ObjectNew)
			return oldStatus != newStatus
		},
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&aimv1alpha2.AIMProfileCache{}).
		Watches(
			&aimv1alpha1.AIMArtifact{},
			handler.EnqueueRequestsFromMapFunc(r.findProfileCachesForArtifact),
		).
		Watches(
			&aimv1alpha2.AIMProfile{},
			handler.EnqueueRequestsFromMapFunc(r.findProfileCachesForProfile),
			builder.WithPredicates(profileStatusPredicate),
		).
		Watches(
			&aimv1alpha2.AIMClusterProfile{},
			handler.EnqueueRequestsFromMapFunc(r.findProfileCachesForClusterProfile),
			builder.WithPredicates(profileStatusPredicate),
		).
		Named(profileCacheName).
		Complete(r)
}

func (r *AIMProfileCacheReconciler) findProfileCachesForProfile(ctx context.Context, obj client.Object) []ctrl.Request {
	profile := obj.(*aimv1alpha2.AIMProfile)

	var caches aimv1alpha2.AIMProfileCacheList
	if err := r.List(ctx, &caches,
		client.InNamespace(profile.Namespace),
		client.MatchingFields{
			aimv1alpha2.ProfileCacheProfileNameIndexKey:  profile.Name,
			aimv1alpha2.ProfileCacheProfileScopeIndexKey: string(aimv1alpha1.AIMResolutionScopeNamespace),
		},
	); err != nil {
		log.FromContext(ctx).Error(err, "Failed to list profile caches for profile",
			"profileName", profile.Name, "namespace", profile.Namespace)
		return nil
	}

	requests := make([]ctrl.Request, len(caches.Items))
	for i, pc := range caches.Items {
		requests[i] = ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&pc)}
	}
	return requests
}

func (r *AIMProfileCacheReconciler) findProfileCachesForClusterProfile(ctx context.Context, obj client.Object) []ctrl.Request {
	profile := obj.(*aimv1alpha2.AIMClusterProfile)

	var caches aimv1alpha2.AIMProfileCacheList
	if err := r.List(ctx, &caches,
		client.MatchingFields{
			aimv1alpha2.ProfileCacheProfileNameIndexKey:  profile.Name,
			aimv1alpha2.ProfileCacheProfileScopeIndexKey: string(aimv1alpha1.AIMResolutionScopeCluster),
		},
	); err != nil {
		log.FromContext(ctx).Error(err, "Failed to list profile caches for cluster profile",
			"profileName", profile.Name)
		return nil
	}

	requests := make([]ctrl.Request, len(caches.Items))
	for i, pc := range caches.Items {
		requests[i] = ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&pc)}
	}
	return requests
}

func (r *AIMProfileCacheReconciler) findProfileCachesForArtifact(ctx context.Context, obj client.Object) []ctrl.Request {
	artifact := obj.(*aimv1alpha1.AIMArtifact)

	profileCacheName := artifact.Labels[constants.LabelProfileCacheName]
	if profileCacheName == "" {
		return nil
	}

	return []ctrl.Request{
		{NamespacedName: client.ObjectKey{Name: profileCacheName, Namespace: artifact.Namespace}},
	}
}

func getProfileStatus(obj client.Object) constants.AIMStatus {
	switch p := obj.(type) {
	case *aimv1alpha2.AIMProfile:
		return p.Status.Status
	case *aimv1alpha2.AIMClusterProfile:
		return p.Status.Status
	default:
		return ""
	}
}

// cleanupArtifacts deletes AIMArtifacts created by this profile cache that are not Ready.
func (r *AIMProfileCacheReconciler) cleanupArtifacts(ctx context.Context, pc *aimv1alpha2.AIMProfileCache) error {
	logger := log.FromContext(ctx)

	profileCacheLabelValue, err := utils.SanitizeLabelValue(pc.Name)
	if err != nil {
		return fmt.Errorf("failed to sanitize profile cache name for label: %w", err)
	}

	var artifacts aimv1alpha1.AIMArtifactList
	if err := r.List(ctx, &artifacts,
		client.InNamespace(pc.Namespace),
		client.MatchingLabels{constants.LabelProfileCacheName: profileCacheLabelValue},
	); err != nil {
		if apierrors.IsForbidden(err) || apierrors.IsNotFound(err) {
			logger.Info("Skipping cleanup, namespace may be terminating", "profileCache", pc.Name)
			return nil
		}
		return fmt.Errorf("failed to list artifacts for cleanup: %w", err)
	}

	var errs []error
	for i := range artifacts.Items {
		a := &artifacts.Items[i]
		if a.Status.Status != constants.AIMStatusReady {
			if deleteErr := r.Delete(ctx, a); deleteErr != nil && !apierrors.IsNotFound(deleteErr) {
				if apierrors.IsForbidden(deleteErr) {
					continue
				}
				errs = append(errs, fmt.Errorf("failed to delete artifact %s: %w", a.Name, deleteErr))
			} else {
				logger.Info("Deleted non-ready artifact during profile cache cleanup",
					"artifact", a.Name, "profileCache", pc.Name, "artifactStatus", a.Status.Status)
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("cleanup errors: %v", errs)
	}
	return nil
}
