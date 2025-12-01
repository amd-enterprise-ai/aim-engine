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

	"github.com/amd-enterprise-ai/aim-engine/internal/aimservicetemplate"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
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
		Reconciler:   r.reconciler,
		Scheme:       r.Scheme,
	}

	// Index AIMTemplateCache by templateName for efficient lookup
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &aimv1alpha1.AIMTemplateCache{}, aimv1alpha1.TemplateCacheTemplateNameIndexKey, func(obj client.Object) []string {
		cache, ok := obj.(*aimv1alpha1.AIMTemplateCache)
		if !ok {
			return nil
		}
		return []string{cache.Spec.TemplateName}
	}); err != nil {
		return err
	}

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
		// Watches(&aimv1alpha1.AIMRuntimeConfig{}, runtimeConfigHandler).
		// Watches(&aimv1alpha1.AIMClusterRuntimeConfig{}, clusterRuntimeConfigHandler).
		Watches(&corev1.Node{}, nodeHandler, builder.WithPredicates(utils.NodeGPUChangePredicate())).
		Named("aim-namespace-template").
		Complete(r)
}
