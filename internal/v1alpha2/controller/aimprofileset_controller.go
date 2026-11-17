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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/aimruntimeconfig"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimmodel"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimprofile"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimprofileset"
)

const (
	profileSetControllerName = "profile-set-v1alpha2"
	profileSetAimIDWildcard  = "*"
	profileSetModelRefIndex  = ".spec.selector.modelRef.name"
)

type AIMProfileSetReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Recorder  record.EventRecorder
	Clientset kubernetes.Interface

	reconciler controllerutils.DomainReconciler[
		*aimv1alpha2.AIMProfileSet,
		*aimv1alpha1.AIMProfileSetStatus,
		aimprofileset.ProfileSetFetchResult,
		aimprofileset.ProfileSetObservation,
	]
	pipeline controllerutils.Pipeline[
		*aimv1alpha2.AIMProfileSet,
		*aimv1alpha1.AIMProfileSetStatus,
		aimprofileset.ProfileSetFetchResult,
		aimprofileset.ProfileSetObservation,
	]
}

// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimprofilesets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimprofilesets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimprofilesets/finalizers,verbs=update
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimprofiles,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclusterprofiles,verbs=get;list;watch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimmodels;aimclustermodels,verbs=get;list;watch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimruntimeconfigs;aimclusterruntimeconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *AIMProfileSetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return runTypedReconcile(ctx, r.Client, req, &aimv1alpha2.AIMProfileSet{}, "Failed to fetch AIMProfileSet", func(ctx context.Context, profileSet *aimv1alpha2.AIMProfileSet) (ctrl.Result, error) {
		return r.pipeline.Run(ctx, profileSet)
	})
}

func (r *AIMProfileSetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	ctx := context.Background()
	r.Recorder = mgr.GetEventRecorderFor("aim-" + profileSetControllerName + "-controller")
	r.reconciler = &aimprofileset.ProfileSetReconciler{Scheme: r.Scheme, APIReader: mgr.GetAPIReader()}
	r.pipeline = controllerutils.Pipeline[
		*aimv1alpha2.AIMProfileSet,
		*aimv1alpha1.AIMProfileSetStatus,
		aimprofileset.ProfileSetFetchResult,
		aimprofileset.ProfileSetObservation,
	]{
		Client:         mgr.GetClient(),
		StatusClient:   mgr.GetClient().Status(),
		Recorder:       r.Recorder,
		ControllerName: profileSetControllerName,
		Reconciler:     r.reconciler,
		Scheme:         r.Scheme,
		Clientset:      r.Clientset,
	}

	if err := registerObjectIndex(ctx, mgr, &aimv1alpha2.AIMProfileSet{}, aimv1alpha1.ProfileSetAimIdIndexKey, func(set *aimv1alpha2.AIMProfileSet) []string {
		if set.Spec.Selector.AimId == "" {
			return []string{profileSetAimIDWildcard}
		}
		return []string{set.Spec.Selector.AimId}
	}); err != nil {
		return err
	}
	if err := registerObjectIndex(ctx, mgr, &aimv1alpha2.AIMProfileSet{}, aimv1alpha1.ProfileSetSourceRefIndexKey, func(set *aimv1alpha2.AIMProfileSet) []string {
		if set.Spec.SourceRef == nil {
			return nil
		}
		return singleValueIndex(profileSourceRefIndexValue(set.Namespace, set.Spec.SourceRef.Name))
	}); err != nil {
		return err
	}
	if err := registerObjectIndex(ctx, mgr, &aimv1alpha2.AIMProfileSet{}, profileSetModelRefIndex, func(set *aimv1alpha2.AIMProfileSet) []string {
		return profileSetModelIndexValues(set.Spec)
	}); err != nil {
		return err
	}

	profileHandler := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		var requests []reconcile.Request
		requests = append(requests, directProfileSetRequest(obj)...)
		switch profile := obj.(type) {
		case *aimv1alpha2.AIMProfile:
			requests = append(requests, profileSetsForAimID(ctx, r.Client, profile.Namespace, profile.Spec.AimId)...)
			requests = append(requests, profileSetsForAimID(ctx, r.Client, profile.Namespace, profileSetAimIDWildcard)...)
		case *aimv1alpha2.AIMClusterProfile:
			requests = append(requests, profileSetsForAimID(ctx, r.Client, "", profile.Spec.AimId)...)
			requests = append(requests, profileSetsForAimID(ctx, r.Client, "", profileSetAimIDWildcard)...)
		}
		return dedupeRequests(requests)
	})
	configMapHandler := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		configMap, ok := obj.(*corev1.ConfigMap)
		if !ok {
			return nil
		}
		requests := profileSetsForSourceRef(ctx, r.Client, profileSourceRefIndexValue(configMap.Namespace, configMap.Name))
		if model := catalogModelEvent(configMap); model != nil {
			requests = append(requests, profileSetsForModel(ctx, r.Client, model)...)
		}
		return dedupeRequests(requests)
	})
	modelHandler := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		return profileSetsForModel(ctx, r.Client, obj)
	})
	runtimeConfigHandler := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		switch config := obj.(type) {
		case *aimv1alpha1.AIMRuntimeConfig:
			if config.Name != aimruntimeconfig.DefaultRuntimeConfigName {
				return nil
			}
			return allProfileSets(ctx, r.Client, config.Namespace)
		case *aimv1alpha1.AIMClusterRuntimeConfig:
			if config.Name != aimruntimeconfig.DefaultRuntimeConfigName {
				return nil
			}
			return allProfileSets(ctx, r.Client, "")
		default:
			return nil
		}
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(&aimv1alpha2.AIMProfileSet{}).
		Watches(&aimv1alpha2.AIMProfile{}, profileHandler).
		Watches(&aimv1alpha2.AIMClusterProfile{}, profileHandler).
		Watches(&aimv1alpha2.AIMModel{}, modelHandler, builder.WithPredicates(modelDefinitionPredicate())).
		Watches(&aimv1alpha2.AIMClusterModel{}, modelHandler, builder.WithPredicates(modelDefinitionPredicate())).
		Watches(
			&aimv1alpha1.AIMRuntimeConfig{},
			runtimeConfigHandler,
			builder.WithPredicates(predicate.GenerationChangedPredicate{}),
		).
		Watches(
			&aimv1alpha1.AIMClusterRuntimeConfig{},
			runtimeConfigHandler,
			builder.WithPredicates(predicate.GenerationChangedPredicate{}),
		).
		Watches(
			&corev1.ConfigMap{},
			configMapHandler,
			builder.WithPredicates(discoveryCatalogConfigMapPredicate()),
		).
		Named(profileSetControllerName).
		Complete(r)
}

func allProfileSets(ctx context.Context, c client.Client, namespace string) []reconcile.Request {
	return listRequests(ctx, func(ctx context.Context) ([]*aimv1alpha2.AIMProfileSet, error) {
		var sets aimv1alpha2.AIMProfileSetList
		var options []client.ListOption
		if namespace != "" {
			options = append(options, client.InNamespace(namespace))
		}
		if err := c.List(ctx, &sets, options...); err != nil {
			return nil, err
		}
		items := make([]*aimv1alpha2.AIMProfileSet, 0, len(sets.Items))
		for i := range sets.Items {
			items = append(items, &sets.Items[i])
		}
		return items, nil
	}, "failed to list AIMProfileSets for default RuntimeConfig event")
}

func directProfileSetRequest(obj client.Object) []reconcile.Request {
	return directProfileSetRequestForScope(obj, true)
}

func profileSetsForAimID(ctx context.Context, c client.Client, namespace, aimID string) []reconcile.Request {
	if aimID == "" {
		return nil
	}
	return listRequests(ctx, func(ctx context.Context) ([]*aimv1alpha2.AIMProfileSet, error) {
		var sets aimv1alpha2.AIMProfileSetList
		opts := []client.ListOption{client.MatchingFields{aimv1alpha1.ProfileSetAimIdIndexKey: aimID}}
		if namespace != "" {
			opts = append(opts, client.InNamespace(namespace))
		}
		if err := c.List(ctx, &sets, opts...); err != nil {
			return nil, err
		}
		items := make([]*aimv1alpha2.AIMProfileSet, 0, len(sets.Items))
		for i := range sets.Items {
			items = append(items, &sets.Items[i])
		}
		return items, nil
	}, "failed to list AIMProfileSets for source profile event", "aimId", aimID)
}

func profileSetsForSourceRef(ctx context.Context, c client.Client, indexValue string) []reconcile.Request {
	if indexValue == "" {
		return nil
	}
	return listRequests(ctx, func(ctx context.Context) ([]*aimv1alpha2.AIMProfileSet, error) {
		var sets aimv1alpha2.AIMProfileSetList
		if err := c.List(ctx, &sets, client.MatchingFields{aimv1alpha1.ProfileSetSourceRefIndexKey: indexValue}); err != nil {
			return nil, err
		}
		items := make([]*aimv1alpha2.AIMProfileSet, 0, len(sets.Items))
		for i := range sets.Items {
			items = append(items, &sets.Items[i])
		}
		return items, nil
	}, "failed to list AIMProfileSets for sourceRef", "sourceRef", indexValue)
}

func profileSourceRefIndexValue(namespace, name string) string {
	if namespace == "" || name == "" {
		return ""
	}
	return namespace + "/" + name
}

func isDiscoveryCatalogConfigMap(obj client.Object) bool {
	configMap, ok := obj.(*corev1.ConfigMap)
	if !ok {
		return false
	}
	return aimprofile.HasRawProfileKeys(configMap) ||
		configMap.Data[aimprofile.DiscoveryCacheMetadataKey] != "" ||
		configMap.Annotations[aimmodel.AnnotationModelUID()] != ""
}

// discoveryCatalogConfigMapPredicate fires reconciles for any ConfigMap
// change where either the old or new revision looks like a discovery
// catalog. Filtering only on the post-change snapshot would silently drop
// "all profile keys removed" updates and leave owning AIMProfileSets
// holding stale derived profiles. The bidirectional check guarantees the
// owner sees the transition and can prune.
func discoveryCatalogConfigMapPredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool { return isDiscoveryCatalogConfigMap(e.Object) },
		DeleteFunc: func(e event.DeleteEvent) bool { return isDiscoveryCatalogConfigMap(e.Object) },
		UpdateFunc: func(e event.UpdateEvent) bool {
			return isDiscoveryCatalogConfigMap(e.ObjectOld) || isDiscoveryCatalogConfigMap(e.ObjectNew)
		},
		GenericFunc: func(e event.GenericEvent) bool { return isDiscoveryCatalogConfigMap(e.Object) },
	}
}
