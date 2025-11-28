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

	"github.com/amd-enterprise-ai/aim-engine/internal/aimservicetemplate"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	servingv1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

const (
	namespaceTemplateFieldOwner            = "aim-namespace-template-controller"
	namespaceTemplateRuntimeConfigIndexKey = ".spec.runtimeConfigName"
	templateCacheTemplateNameIndexKey      = ".spec.templateName"
)

// AIMServiceTemplateReconciler reconciles a AIMServiceTemplate object
type AIMServiceTemplateReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Recorder  record.EventRecorder
	Clientset kubernetes.Interface

	reconciler controllerutils.DomainReconciler[
		*aimv1alpha1.AIMServiceTemplate,
		*aimv1alpha1.AIMServiceTemplateStatus,
		aimservicetemplate.ServiceTemplateFetchResult,
		aimservicetemplate.ServiceTemplateObservation,
	]
	pipeline controllerutils.Pipeline[
		*aimv1alpha1.AIMServiceTemplate,
		*aimv1alpha1.AIMServiceTemplateStatus,
		aimservicetemplate.ServiceTemplateFetchResult,
		aimservicetemplate.ServiceTemplateObservation,
	]
}

// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimservicetemplates,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimservicetemplates/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimservicetemplates/finalizers,verbs=update
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclusterruntimeconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimruntimeconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclustermodels,verbs=get;list;watch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimmodels,verbs=get;list;watch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimtemplatecaches,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=serving.kserve.io,resources=servingruntimes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch

func (r *AIMServiceTemplateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the template
	var template aimv1alpha1.AIMServiceTemplate
	if err := r.Get(ctx, req.NamespacedName, &template); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to fetch AIMServiceTemplate")
		return ctrl.Result{}, err
	}

	if err := r.pipeline.Run(ctx, &template); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

//
//type serviceTemplateReconciler struct {
//	Clientset kubernetes.Interface
//	Scheme    *runtime.Scheme
//}
//
//func (s *serviceTemplateReconciler) Observe(ctx context.Context, c client.Client, obj *aimv1alpha1.AIMServiceTemplate) (ServiceTemplateObservation, error) {
//	obs := ServiceTemplateObservation{}
//
//	// Fetch runtime config
//	obs.RuntimeConfig = aimconfig.GetAimRuntimeConfigObservation(ctx, c, types.NamespacedName{Namespace: obj.Namespace, Name: obj.Spec.RuntimeConfigName})
//
//	// Parent model
//	modelObs, err := s.observeModel(ctx, c, obj)
//	if err != nil {
//		return obs, err
//	}
//	obs.Model = modelObs
//
//	// Discovery job
//	discoveryObs, err := observeDiscoveryJob(ctx, c, s.Clientset, obj.Namespace, obj.Name, &obj.Status)
//	if err != nil {
//		return obs, err
//	}
//	obs.Discovery = discoveryObs
//
//	// Caching
//	cacheObs, err := s.observeTemplateCache(ctx, c, obj)
//	if err != nil {
//		return obs, err
//	}
//	obs.Cache = cacheObs
//
//	clusterObs, err := s.observeClusterGpuAvailability(ctx, c, obj)
//	if err != nil {
//		return obs, err
//	}
//	obs.Cluster = clusterObs
//
//	return obs, nil
//}
//
//func (s *serviceTemplateReconciler) observeModel(ctx context.Context, c client.Client, template *aimv1alpha1.AIMServiceTemplate) (ServiceTemplateModelObservation, error) {
//	key := types.NamespacedName{Namespace: template.Namespace, Name: template.Spec.ModelName}
//	model := &aimv1alpha1.AIMModel{}
//	err := c.Get(ctx, key, model)
//
//	// Only treat "hard client errors" as Reconcile errors:
//	if err != nil && !errors.IsNotFound(err) {
//		return ServiceTemplateModelObservation{}, err
//	}
//
//	// Extract image if model was found
//	var image string
//	if err == nil && model != nil {
//		image = model.Spec.Image
//	}
//
//	return buildModelObservation(serviceTemplateModelObservationInputs{
//		model: model,
//		image: image,
//		scope: aimv1alpha1.AIMResolutionScopeNamespace,
//		error: err,
//	}), nil
//}
//
//// observeTemplateCache observes the AIM Template Cache resource
//func (s *serviceTemplateReconciler) observeTemplateCache(ctx context.Context, c client.Client, template *aimv1alpha1.AIMServiceTemplate) (ServiceTemplateCacheObservation, error) {
//	var caches aimv1alpha1.AIMTemplateCacheList
//
//	// If the cache is already set, check its status
//	if template.Status.ResolvedCache != nil {
//		templateCache := &aimv1alpha1.AIMTemplateCache{}
//		if err := c.Get(ctx, template.Status.ResolvedCache.NamespacedName(), templateCache); err != nil {
//			if errors.IsNotFound(err) {
//				// TODO Cache was deleted, should recreate
//			}
//		}
//	}
//
//	if err := c.List(ctx, &caches,
//		client.InNamespace(template.Namespace),
//		client.MatchingFields{
//			templateCacheTemplateNameIndexKey: template.Name,
//		},
//	); err != nil {
//		return ServiceTemplateCacheObservation{}, err
//	}
//
//	// Filter for namespace-scoped template caches only
//	namespaceScopedCaches := make([]aimv1alpha1.AIMTemplateCache, 0, len(caches.Items))
//	for i := range caches.Items {
//		if caches.Items[i].Spec.TemplateScope == aimv1alpha1.AIMServiceTemplateScopeNamespace {
//			namespaceScopedCaches = append(namespaceScopedCaches, caches.Items[i])
//		}
//	}
//
//	return buildTemplateCacheObservation(serviceTemplateCacheObservationInputs{
//		existingTemplateCaches: namespaceScopedCaches,
//		cachingEnabled:         template.Spec.Caching.Enabled,
//		listError:              nil,
//	}), nil
//}
//
//func (s *serviceTemplateReconciler) observeClusterGpuAvailability(ctx context.Context, c client.Client, template *aimv1alpha1.AIMServiceTemplate) (ServiceTemplateClusterObservation, error) {
//	var gpuModel string
//	if gpuSelector := template.Spec.GpuSelector; gpuSelector != nil {
//		gpuModel = gpuSelector.Model
//	}
//
//	return observeClusterGpuAvailability(ctx, c, gpuModel), nil
//}
//
//func (s *serviceTemplateReconciler) Plan(ctx context.Context, obj *aimv1alpha1.AIMServiceTemplate, obs ServiceTemplateObservation) ([]client.Object, error) {
//	var objects []client.Object
//
//	if obs.Discovery.ShouldRunDiscovery {
//		job := BuildDiscoveryJob(DiscoveryJobBuilderInputs{
//			TemplateName: obj.Name,
//			TemplateSpec: obj.Spec.AIMServiceTemplateSpecCommon,
//			Env:          obj.Spec.Env,
//			Namespace:    obj.Namespace,
//			Image:        obs.Model.Image,
//			OwnerRef:     utils.BuildOwnerReference(obj, s.Scheme),
//		})
//		objects = append(objects, job)
//	}
//
//	// TODO
//	if obs.Cache.ShouldCreateCache {
//		// TODO skip caching if template is not otherwise ready
//		cache := buildTemplateCache(obj, obs.RuntimeConfig)
//		objects = append(objects, cache)
//	}
//
//	return objects, nil
//}
//
//func (s *serviceTemplateReconciler) Project(status *aimv1alpha1.AIMServiceTemplateStatus, cm *controllerutils.ConditionManager, obs ServiceTemplateObservation) {
//	if status == nil {
//		return
//	}
//
//	h := controllerutils.NewStatusHelper(status, cm)
//
//	if s.projectModel(status, cm, h, obs) {
//		return
//	}
//	if s.projectCluster(status, cm, h, obs) {
//		return
//	}
//	if s.projectDiscovery(status, cm, h, obs) {
//		return
//	}
//	if s.projectCache(status, cm, h, obs) {
//		return
//	}
//}
//
//func (s *serviceTemplateReconciler) projectModel(status *aimv1alpha1.AIMServiceTemplateStatus, cm *controllerutils.ConditionManager, h *controllerutils.StatusHelper, obs ServiceTemplateObservation) bool {
//	if obs.Model.Error != nil {
//		if errors.IsNotFound(obs.Model.Error) {
//			h.Degraded("ModelNotFound", obs.Model.Error.Error())
//		} else {
//			h.Degraded("UnknownModelError", obs.Model.Error.Error())
//		}
//		return true
//	} else {
//		status.ResolvedModel = &aimv1alpha1.AIMResolvedReference{
//			Name:      obs.Model.Model.GetName(),
//			Namespace: obs.Model.Model.GetNamespace(),
//			Scope:     obs.Model.Scope,
//			Kind:      "AIMModel",
//			UID:       obs.Model.Model.GetUID(),
//		}
//	}
//	return false
//}
//
//func (s *serviceTemplateReconciler) projectCluster(status *aimv1alpha1.AIMServiceTemplateStatus, cm *controllerutils.ConditionManager, h *controllerutils.StatusHelper, obs ServiceTemplateObservation) bool {
//	// Cluster GPUs
//	if obs.Cluster.Error != nil {
//		// TODO
//	}
//
//	if !obs.Cluster.GpuModelAvailable {
//		status.Status = aimv1alpha1.AIMTemplateStatusNotAvailable
//		message := fmt.Sprintf("This cluster does not have '%s' GPUs", obs.Cluster.GpuModelRequested)
//		h.Degraded("GpuNotAvailable", message)
//		return true
//	}
//
//	return false
//}
//
//func (s *serviceTemplateReconciler) projectDiscovery(status *aimv1alpha1.AIMServiceTemplateStatus, cm *controllerutils.ConditionManager, h *controllerutils.StatusHelper, obs ServiceTemplateObservation) bool {
//	if obs.Discovery.Error != nil {
//		h.Failed(aimv1alpha1.AIMTemplateDiscoveryConditionType, obs.Discovery.Error.Error())
//		return true
//	} else if obs.Discovery.DiscoveryJob == nil {
//		h.Progressing("AwaitingDiscoveryJobStart", "Waiting for DiscoveryJob to start")
//	} else if obs.Discovery.DiscoveryResult != nil {
//
//	}
//	return false
//}
//
//func (s *serviceTemplateReconciler) projectCache(status *aimv1alpha1.AIMServiceTemplateStatus, cm *controllerutils.ConditionManager, h *controllerutils.StatusHelper, obs ServiceTemplateObservation) bool {
//
//	// Cache
//	// TODO should cache, but not exists
//	if obs.Cache.ShouldCreateCache {
//
//	}
//
//	// If resolved cache is set, just monitor it
//
//	// If a cache exists
//	if cache := obs.Cache.BestTemplateCache; cache != nil {
//
//	}
//
//	return false
//}
//
func requestsFromNamespaceTemplates(templates []aimv1alpha1.AIMServiceTemplate) []reconcile.Request {
	if len(templates) == 0 {
		return nil
	}
	requests := make([]reconcile.Request, 0, len(templates))
	for _, tpl := range templates {
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: tpl.Namespace,
				Name:      tpl.Name,
			},
		})
	}
	return requests
}

//
//func buildTemplateCache(template *aimv1alpha1.AIMServiceTemplate, runtimeConfigResolution *aimv1alpha1.AIMRuntimeConfigCommon) *aimv1alpha1.AIMTemplateCache {
//	return &aimv1alpha1.AIMTemplateCache{
//		TypeMeta: metav1.TypeMeta{
//			APIVersion: "aimv1alpha1",
//			Kind:       "AIMModelCache",
//		},
//		ObjectMeta: metav1.ObjectMeta{
//			Name:      template.Name,
//			Namespace: template.Namespace,
//			OwnerReferences: []metav1.OwnerReference{
//				{
//					APIVersion: template.APIVersion,
//					Kind:       template.Kind,
//					Name:       template.Name,
//					UID:        template.UID,
//					Controller: ptr.To(true),
//				},
//			},
//		},
//		Spec: aimv1alpha1.AIMTemplateCacheSpec{
//			TemplateName:     template.Name,
//			StorageClassName: runtimeConfigResolution.DefaultStorageClassName,
//			Env:              template.Spec.Env,
//			ModelSources:     template.Spec.ModelSources,
//		},
//	}
//}

func (r *AIMServiceTemplateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.reconciler = &aimservicetemplate.ServiceTemplateReconciler{
		Clientset: r.Clientset,
		Scheme:    r.Scheme,
	}
	r.pipeline = controllerutils.Pipeline[
		*aimv1alpha1.AIMServiceTemplate,
		*aimv1alpha1.AIMServiceTemplateStatus,
		aimservicetemplate.ServiceTemplateFetchResult,
		aimservicetemplate.ServiceTemplateObservation,
	]{
		Client:       mgr.GetClient(),
		StatusClient: mgr.GetClient().Status(),
		Recorder:     r.Recorder,
		FieldOwner:   "aim-service-template-controller",
		Domain:       r.reconciler,
	}
	//
	//if err := mgr.GetFieldIndexer().IndexField(ctx, &aimv1alpha1.AIMServiceTemplate{}, namespaceTemplateRuntimeConfigIndexKey, func(obj client.Object) []string {
	//	template, ok := obj.(*aimv1alpha1.AIMServiceTemplate)
	//	if !ok {
	//		return nil
	//	}
	//	return []string{template2.NormalizeRuntimeConfigName(template.Spec.RuntimeConfigName)}
	//}); err != nil {
	//	return err
	//}
	//
	//if err := mgr.GetFieldIndexer().IndexField(ctx, &aimv1alpha1.AIMTemplateCache{}, templateCacheTemplateNameIndexKey, func(obj client.Object) []string {
	//	cache, ok := obj.(*aimv1alpha1.AIMTemplateCache)
	//	if !ok {
	//		return nil
	//	}
	//	return []string{cache.Spec.TemplateName}
	//}); err != nil {
	//	return err
	//}
	//
	//runtimeConfigHandler := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
	//	runtimeConfig, ok := obj.(*aimv1alpha1.AIMRuntimeConfig)
	//	if !ok {
	//		return nil
	//	}
	//
	//	var templates aimv1alpha1.AIMServiceTemplateList
	//	if err := r.List(ctx, &templates,
	//		client.InNamespace(runtimeConfig.Namespace),
	//		client.MatchingFields{
	//			namespaceTemplateRuntimeConfigIndexKey: template2.NormalizeRuntimeConfigName(runtimeConfig.Name),
	//		},
	//	); err != nil {
	//		ctrl.LoggerFrom(ctx).Error(err, "failed to list AIMServiceTemplate for AIMRuntimeConfig",
	//			"runtimeConfig", runtimeConfig.Name, "namespace", runtimeConfig.Namespace)
	//		return nil
	//	}
	//
	//	return requestsFromNamespaceTemplates(templates.Items)
	//})
	//
	//clusterRuntimeConfigHandler := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
	//	clusterConfig, ok := obj.(*aimv1alpha1.AIMClusterRuntimeConfig)
	//	if !ok {
	//		return nil
	//	}
	//
	//	var templates aimv1alpha1.AIMServiceTemplateList
	//	if err := r.List(ctx, &templates,
	//		client.MatchingFields{
	//			namespaceTemplateRuntimeConfigIndexKey: template2.NormalizeRuntimeConfigName(clusterConfig.Name),
	//		},
	//	); err != nil {
	//		ctrl.LoggerFrom(ctx).Error(err, "failed to list AIMServiceTemplate for AIMClusterRuntimeConfig",
	//			"runtimeConfig", clusterConfig.Name)
	//		return nil
	//	}
	//
	//	return requestsFromNamespaceTemplates(templates.Items)
	//})

	nodeHandler := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		_, ok := obj.(*corev1.Node)
		if !ok {
			return nil
		}

		var templates aimv1alpha1.AIMServiceTemplateList
		if err := r.List(ctx, &templates); err != nil {
			ctrl.LoggerFrom(ctx).Error(err, "failed to list AIMServiceTemplates for Node event")
			return nil
		}

		filtered := make([]aimv1alpha1.AIMServiceTemplate, 0, len(templates.Items))
		for i := range templates.Items {
			if aimservicetemplate.TemplateRequiresGPU(templates.Items[i].Spec.AIMServiceTemplateSpecCommon) {
				filtered = append(filtered, templates.Items[i])
			}
		}

		return requestsFromNamespaceTemplates(filtered)
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(&aimv1alpha1.AIMServiceTemplate{}).
		Owns(&batchv1.Job{}).
		Owns(&servingv1alpha1.ServingRuntime{}).
		Owns(&aimv1alpha1.AIMTemplateCache{}).
		//Watches(&aimv1alpha1.AIMRuntimeConfig{}, runtimeConfigHandler).
		//Watches(&aimv1alpha1.AIMClusterRuntimeConfig{}, clusterRuntimeConfigHandler).
		Watches(&corev1.Node{}, nodeHandler, builder.WithPredicates(utils.NodeGPUChangePredicate())).
		Named("aim-namespace-template").
		Complete(r)
}
