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
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"

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
)

const (
	clusterTemplateFieldOwner            = "aim-cluster-template-controller"
	clusterTemplateRuntimeConfigIndexKey = ".spec.runtimeConfigName"
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
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimruntimeconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclustermodels,verbs=get;list;watch
// +kubebuilder:rbac:groups=serving.kserve.io,resources=clusterservingruntimes;servingruntimes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=serving.kserve.io,resources=clusterservingruntimes/status;servingruntimes/status;inferenceservices/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch

func (r *AIMClusterServiceTemplateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the template
	var template aimv1alpha1.AIMClusterServiceTemplate
	if err := r.Get(ctx, req.NamespacedName, &template); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to fetch AIMClusterServiceTemplate")
		return ctrl.Result{}, err
	}

	if err := r.pipeline.Run(ctx, &template); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

//
//type clusterServiceTemplateReconciler struct {
//	Clientset kubernetes.Interface
//	Scheme    *runtime.Scheme
//}
//
//func (s *clusterServiceTemplateReconciler) Observe(ctx context.Context, c client.Client, obj *aimv1alpha1.AIMClusterServiceTemplate) (ServiceTemplateObservation, error) {
//	obs := ServiceTemplateObservation{}
//
//	operatorNamespace := constants.GetOperatorNamespace()
//
//	// Fetch runtime config (cluster-scoped)
//	obs.RuntimeConfig = aimconfig.GetAimRuntimeConfigObservation(ctx, c, types.NamespacedName{Name: obj.Spec.RuntimeConfigName})
//
//	// Parent model (cluster-scoped)
//	modelObs, err := s.observeModel(ctx, c, obj)
//	if err != nil {
//		return obs, err
//	}
//	obs.Model = modelObs
//
//	// Discovery job (runs in operator namespace for cluster-scoped templates)
//	discoveryObs, err := observeDiscoveryJob(ctx, c, s.Clientset, operatorNamespace, obj.Name, &obj.Status)
//	if err != nil {
//		return obs, err
//	}
//	obs.Discovery = discoveryObs
//
//	// GPU availability
//	var gpuModel string
//	if gpuSelector := obj.Spec.GpuSelector; gpuSelector != nil {
//		gpuModel = gpuSelector.Model
//	}
//	obs.Cluster = observeClusterGpuAvailability(ctx, c, gpuModel)
//
//	return obs, nil
//}
//
//func (s *clusterServiceTemplateReconciler) observeModel(ctx context.Context, c client.Client, template *aimv1alpha1.AIMClusterServiceTemplate) (ServiceTemplateModelObservation, error) {
//	key := types.NamespacedName{Name: template.Spec.ModelName}
//	model := &aimv1alpha1.AIMClusterModel{}
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
//		scope: aimv1alpha1.AIMResolutionScopeCluster,
//		error: err,
//	}), nil
//}
//
//func (s *clusterServiceTemplateReconciler) Plan(ctx context.Context, obj *aimv1alpha1.AIMClusterServiceTemplate, obs ServiceTemplateObservation) ([]client.Object, error) {
//	var objects []client.Object
//
//	operatorNamespace := constants.GetOperatorNamespace()
//
//	if obs.Discovery.ShouldRunDiscovery {
//		job := BuildDiscoveryJob(DiscoveryJobBuilderInputs{
//			TemplateName: obj.Name,
//			TemplateSpec: obj.Spec.AIMServiceTemplateSpecCommon,
//			Env:          obj.Spec.Env,
//			Namespace:    operatorNamespace,
//			Image:        obs.Model.Image,
//			OwnerRef:     utils.BuildOwnerReference(obj, s.Scheme),
//		})
//		objects = append(objects, job)
//	}
//
//	// Note: Cluster-scoped templates do not create caches
//
//	return objects, nil
//}
//
//func (s *clusterServiceTemplateReconciler) Project(status *aimv1alpha1.AIMServiceTemplateStatus, cm *controllerutils.ConditionManager, obs ServiceTemplateObservation) {
//	// TODO: Implement projection
//}

func requestsFromClusterTemplates(templates []aimv1alpha1.AIMClusterServiceTemplate) []reconcile.Request {
	if len(templates) == 0 {
		return nil
	}
	requests := make([]reconcile.Request, 0, len(templates))
	for _, tpl := range templates {
		requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Name: tpl.Name}})
	}
	return requests
}

func (r *AIMClusterServiceTemplateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	//ctx := context.Background()

	r.reconciler = &aimservicetemplate.ClusterServiceTemplateReconciler{
		Clientset: r.Clientset,
		Scheme:    r.Scheme,
	}
	r.pipeline = controllerutils.Pipeline[
		*aimv1alpha1.AIMClusterServiceTemplate,
		*aimv1alpha1.AIMServiceTemplateStatus,
		aimservicetemplate.ClusterServiceTemplateFetchResult,
		aimservicetemplate.ClusterServiceTemplateObservation,
	]{
		Client:       mgr.GetClient(),
		StatusClient: mgr.GetClient().Status(),
		Recorder:     r.Recorder,
		FieldOwner:   clusterTemplateFieldOwner,
		Domain:       r.reconciler,
	}

	//if err := mgr.GetFieldIndexer().IndexField(ctx, &aimv1alpha1.AIMClusterServiceTemplate{}, clusterTemplateRuntimeConfigIndexKey, func(obj client.Object) []string {
	//	template, ok := obj.(*aimv1alpha1.AIMClusterServiceTemplate)
	//	if !ok {
	//		return nil
	//	}
	//	return []string{aimtemplate.NormalizeRuntimeConfigName(template.Spec.RuntimeConfigName)}
	//}); err != nil {
	//	return err
	//}
	//
	//clusterRuntimeConfigHandler := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
	//	clusterConfig, ok := obj.(*aimv1alpha1.AIMClusterRuntimeConfig)
	//	if !ok {
	//		return nil
	//	}
	//
	//	var templates aimv1alpha1.AIMClusterServiceTemplateList
	//	if err := r.List(ctx, &templates,
	//		client.MatchingFields{
	//			clusterTemplateRuntimeConfigIndexKey: aimtemplate.NormalizeRuntimeConfigName(clusterConfig.Name),
	//		},
	//	); err != nil {
	//		ctrl.LoggerFrom(ctx).Error(err, "failed to list AIMClusterServiceTemplate for AIMClusterRuntimeConfig",
	//			"runtimeConfig", clusterConfig.Name)
	//		return nil
	//	}
	//
	//	return requestsFromClusterTemplates(templates.Items)
	//})
	//
	//runtimeConfigHandler := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
	//	runtimeConfig, ok := obj.(*aimv1alpha1.AIMRuntimeConfig)
	//	if !ok {
	//		return nil
	//	}
	//
	//	operatorNamespace := constants.GetOperatorNamespace()
	//	if runtimeConfig.Namespace != operatorNamespace {
	//		return nil
	//	}
	//
	//	var templates aimv1alpha1.AIMClusterServiceTemplateList
	//	if err := r.List(ctx, &templates,
	//		client.MatchingFields{
	//			clusterTemplateRuntimeConfigIndexKey: aimtemplate.NormalizeRuntimeConfigName(runtimeConfig.Name),
	//		},
	//	); err != nil {
	//		ctrl.LoggerFrom(ctx).Error(err, "failed to list AIMClusterServiceTemplate for AIMRuntimeConfig",
	//			"runtimeConfig", runtimeConfig.Name, "namespace", runtimeConfig.Namespace)
	//		return nil
	//	}
	//
	//	return requestsFromClusterTemplates(templates.Items)
	//})

	nodeHandler := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		_, ok := obj.(*corev1.Node)
		if !ok {
			return nil
		}

		var templates aimv1alpha1.AIMClusterServiceTemplateList
		if err := r.List(ctx, &templates); err != nil {
			ctrl.LoggerFrom(ctx).Error(err, "failed to list AIMClusterServiceTemplates for Node event")
			return nil
		}

		filtered := make([]aimv1alpha1.AIMClusterServiceTemplate, 0, len(templates.Items))
		for i := range templates.Items {
			if aimservicetemplate.TemplateRequiresGPU(templates.Items[i].Spec.AIMServiceTemplateSpecCommon) {
				filtered = append(filtered, templates.Items[i])
			}
		}

		return requestsFromClusterTemplates(filtered)
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(&aimv1alpha1.AIMClusterServiceTemplate{}).
		Owns(&batchv1.Job{}).
		//Watches(&aimv1alpha1.AIMClusterRuntimeConfig{}, clusterRuntimeConfigHandler).
		//Watches(&aimv1alpha1.AIMRuntimeConfig{}, runtimeConfigHandler).
		Watches(&corev1.Node{}, nodeHandler, builder.WithPredicates(utils.NodeGPUChangePredicate())).
		Named("aim-cluster-template").
		Complete(r)
}
