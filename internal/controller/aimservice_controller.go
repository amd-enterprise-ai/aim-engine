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
	"strings"

	"github.com/go-logr/logr"
	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/aimservice"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

const (
	aimServiceFieldOwner       = "aim-service-controller"
	aimServiceTemplateIndexKey = ".spec.templateRef"
	// AIMCacheBasePath is the base directory where AIM expects to find cached models
	AIMCacheBasePath = "/workspace/model-cache"
)

// AIMServiceReconciler reconciles AIMService resources into KServe InferenceServices.
type AIMServiceReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder

	reconciler controllerutils.DomainReconciler[
		*aimv1alpha1.AIMService,
		*aimv1alpha1.AIMServiceStatus,
		aimservice.ServiceTemplateFetchResult,
		aimservice.ServiceTemplateObservation,
	]
	pipeline controllerutils.Pipeline[
		*aimv1alpha1.AIMService,
		*aimv1alpha1.AIMServiceStatus,
		aimservice.ServiceTemplateFetchResult,
		aimservice.ServiceTemplateObservation,
	]
}

// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimservices,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimservices/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimservicetemplates,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aim.eai.amd.com,resources=aimclusterservicetemplates,verbs=get;list;watch
// +kubebuilder:rbac:groups=serving.kserve.io,resources=inferenceservices,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=serving.kserve.io,resources=inferenceservices/status,verbs=get
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes/status,verbs=get
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch

func (r *AIMServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var service aimv1alpha1.AIMService
	if err := r.Get(ctx, req.NamespacedName, &service); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	logger.V(1).Info("Reconciling AIMService", "name", service.Name, "namespace", service.Namespace)

	if err := r.pipeline.Run(ctx, &service); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// findServicesByTemplate finds services that reference a template by name or share the same model
func (r *AIMServiceReconciler) findServicesByTemplate(
	ctx context.Context,
	templateName string,
	templateNamespace string,
	modelName string,
	isClusterScoped bool,
) []aimv1alpha1.AIMService {
	// Find services that reference this template by name (explicit templateRef or already resolved)
	var servicesWithRef aimv1alpha1.AIMServiceList
	listOpts := []client.ListOption{client.MatchingFields{aimServiceTemplateIndexKey: templateName}}
	if !isClusterScoped {
		listOpts = append(listOpts, client.InNamespace(templateNamespace))
	}

	if err := r.List(ctx, &servicesWithRef, listOpts...); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "failed to list AIMServices for template", "template", templateName)
		return nil
	}

	// Also find services doing auto-selection with the same image name
	var servicesWithImage aimv1alpha1.AIMServiceList
	imageListOpts := []client.ListOption{}
	if !isClusterScoped {
		imageListOpts = append(imageListOpts, client.InNamespace(templateNamespace))
	}

	if err := r.List(ctx, &servicesWithImage, imageListOpts...); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "failed to list AIMServices for image matching")
		return nil
	}

	// Combine results, filtering for services with matching image that don't have explicit templateRef
	serviceMap := make(map[string]aimv1alpha1.AIMService)
	for _, svc := range servicesWithRef.Items {
		serviceMap[svc.Namespace+"/"+svc.Name] = svc
	}

	for _, svc := range servicesWithImage.Items {
		// Skip if already included via template name index
		key := svc.Namespace + "/" + svc.Name
		if _, exists := serviceMap[key]; exists {
			continue
		}

		// Include if doing auto-selection (no templateRef) and matches resolved image
		if strings.TrimSpace(svc.Spec.TemplateRef) == "" {
			svcModelName := r.getServiceModelName(&svc)
			if svcModelName != "" && svcModelName == strings.TrimSpace(modelName) {
				serviceMap[key] = svc
			}
		}
	}

	// Convert map to slice
	services := make([]aimv1alpha1.AIMService, 0, len(serviceMap))
	for _, svc := range serviceMap {
		services = append(services, svc)
	}

	return services
}

// getServiceModelName extracts the model name from a service
func (r *AIMServiceReconciler) getServiceModelName(svc *aimv1alpha1.AIMService) string {
	if svc.Status.ResolvedModel != nil {
		return svc.Status.ResolvedModel.Name
	}
	if svc.Spec.Model.Ref != nil {
		return strings.TrimSpace(*svc.Spec.Model.Ref)
	}
	return ""
}

// templateHandlerFunc returns a handler function for AIMServiceTemplate watches
func (r *AIMServiceReconciler) templateHandlerFunc() handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		template, ok := obj.(*aimv1alpha1.AIMServiceTemplate)
		if !ok {
			return nil
		}

		services := r.findServicesByTemplate(ctx, template.Name, template.Namespace, template.Spec.ModelName, false)
		return RequestsForServices(services)
	}
}

// clusterTemplateHandlerFunc returns a handler function for AIMClusterServiceTemplate watches
func (r *AIMServiceReconciler) clusterTemplateHandlerFunc() handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		clusterTemplate, ok := obj.(*aimv1alpha1.AIMClusterServiceTemplate)
		if !ok {
			return nil
		}

		services := r.findServicesByTemplate(ctx, clusterTemplate.Name, "", clusterTemplate.Spec.ModelName, true)
		return RequestsForServices(services)
	}
}

func (r *AIMServiceReconciler) templateCacheHandlerFunc() handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		templateCache, ok := obj.(*aimv1alpha1.AIMTemplateCache)
		if !ok {
			return nil
		}

		var services aimv1alpha1.AIMServiceList
		if err := r.List(ctx, &services,
			client.InNamespace(templateCache.Namespace),
			client.MatchingFields{aimServiceTemplateIndexKey: templateCache.Spec.TemplateName},
		); err != nil {
			ctrl.LoggerFrom(ctx).Error(err, "failed to list AIMServices for AIMServiceTemplate", "template", templateCache.Name)
			return nil
		}

		return RequestsForServices(services.Items)
	}
}

// findServicesByModel finds services that reference a model
func (r *AIMServiceReconciler) findServicesByModel(ctx context.Context, model *aimv1alpha1.AIMModel) []aimv1alpha1.AIMService {
	logger := ctrl.LoggerFrom(ctx)

	// Only trigger reconciliation for auto-created models
	if model.Labels[constants.LabelAutoCreated] != "true" {
		logger.V(1).Info("Skipping model - not auto-created", "model", model.Name)
		return nil
	}

	// Find services using this model
	var services aimv1alpha1.AIMServiceList
	if err := r.List(ctx, &services, client.InNamespace(model.Namespace)); err != nil {
		logger.Error(err, "failed to list AIMServices for AIMModel", "model", model.Name)
		return nil
	}

	var matchingServices []aimv1alpha1.AIMService
	for i := range services.Items {
		svc := &services.Items[i]
		if r.serviceUsesModel(svc, model, logger) {
			matchingServices = append(matchingServices, *svc)
		}
	}

	return matchingServices
}

// serviceUsesModel checks if a service uses the specified model
func (r *AIMServiceReconciler) serviceUsesModel(svc *aimv1alpha1.AIMService, model *aimv1alpha1.AIMModel, logger logr.Logger) bool {
	// Check if service uses this model by:
	// 1. Explicit ref (spec.model.ref)
	if svc.Spec.Model.Ref != nil && *svc.Spec.Model.Ref == model.Name {
		return true
	}
	// 2. Image URL that resolves to this model (check status)
	if svc.Status.ResolvedModel != nil && svc.Status.ResolvedModel.Name == model.Name {
		return true
	}
	// 3. Image URL in spec (need to check if it would resolve to this model)
	// This is the case when service was just created and status not yet set
	if svc.Spec.Model.Image != nil {
		logger.V(1).Info("Service has image URL but no resolved image yet",
			"service", svc.Name,
			"image", *svc.Spec.Model.Image,
			"model", model.Name)
		// For now, add all services with image URLs in the same namespace
		// The service reconciliation will properly resolve and filter
		return true
	}
	return false
}

// modelHandlerFunc returns a handler function for AIMModel watches
func (r *AIMServiceReconciler) modelHandlerFunc() handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		model, ok := obj.(*aimv1alpha1.AIMModel)
		if !ok {
			return nil
		}

		matchingServices := r.findServicesByModel(ctx, model)
		return RequestsForServices(matchingServices)
	}
}

// modelPredicate returns a predicate for AIMModel watches
func (r *AIMServiceReconciler) modelPredicate() predicate.Funcs {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			// Don't trigger on creation - model status is empty initially
			ctrl.Log.V(1).Info("AIMModel create event (skipped)", "model", e.Object.GetName())
			return false
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldModel, ok := e.ObjectOld.(*aimv1alpha1.AIMModel)
			if !ok {
				return false
			}
			newModel, ok := e.ObjectNew.(*aimv1alpha1.AIMModel)
			if !ok {
				return false
			}
			// Trigger if status changed
			statusChanged := oldModel.Status.Status != newModel.Status.Status
			if statusChanged {
				ctrl.Log.Info("AIMModel status changed - triggering reconciliation",
					"model", newModel.Name,
					"namespace", newModel.Namespace,
					"oldStatus", oldModel.Status.Status,
					"newStatus", newModel.Status.Status)
			} else {
				ctrl.Log.V(1).Info("AIMModel update (no status change)",
					"model", newModel.Name,
					"status", newModel.Status.Status)
			}
			return statusChanged
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			ctrl.Log.V(1).Info("AIMModel delete event (skipped)", "model", e.Object.GetName())
			return false
		},
	}
}

// findServicesByClusterModel finds services that reference a cluster model
func (r *AIMServiceReconciler) findServicesByClusterModel(ctx context.Context, clusterModel *aimv1alpha1.AIMClusterModel) []aimv1alpha1.AIMService {
	logger := ctrl.LoggerFrom(ctx)

	// Note: Unlike namespace-scoped models, cluster models are never auto-created by the system.
	// They are manually created infrastructure resources, so we reconcile services for all cluster model changes.

	// Find services across all namespaces using this cluster model
	var services aimv1alpha1.AIMServiceList
	if err := r.List(ctx, &services); err != nil {
		logger.Error(err, "failed to list AIMServices for AIMClusterModel", "model", clusterModel.Name)
		return nil
	}

	var matchingServices []aimv1alpha1.AIMService
	for i := range services.Items {
		svc := &services.Items[i]
		if r.serviceUsesClusterModel(svc, clusterModel, logger) {
			matchingServices = append(matchingServices, *svc)
		}
	}

	return matchingServices
}

// serviceUsesClusterModel checks if a service uses the specified cluster model
func (r *AIMServiceReconciler) serviceUsesClusterModel(svc *aimv1alpha1.AIMService, clusterModel *aimv1alpha1.AIMClusterModel, logger logr.Logger) bool {
	// Check if service uses this cluster model by:
	// 1. Explicit ref (spec.model.ref)
	if svc.Spec.Model.Ref != nil && *svc.Spec.Model.Ref == clusterModel.Name {
		return true
	}
	// 2. Image URL that resolves to this cluster model (check status)
	if svc.Status.ResolvedModel != nil && svc.Status.ResolvedModel.Name == clusterModel.Name {
		return true
	}
	// 3. Image URL in spec (need to check if it would resolve to this cluster model)
	// This is the case when service was just created and status not yet set
	if svc.Spec.Model.Image != nil {
		logger.V(1).Info("Service has image URL but no resolved image yet",
			"service", svc.Name,
			"namespace", svc.Namespace,
			"image", *svc.Spec.Model.Image,
			"clusterModel", clusterModel.Name)
		// For cluster models, add all services with image URLs across all namespaces
		// The service reconciliation will properly resolve and filter
		return true
	}
	return false
}

func (r *AIMServiceReconciler) clusterModelHandlerFunc() handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		clusterModel, ok := obj.(*aimv1alpha1.AIMClusterModel)
		if !ok {
			return nil
		}

		matchingServices := r.findServicesByClusterModel(ctx, clusterModel)
		return RequestsForServices(matchingServices)
	}
}

// clusterModelPredicate returns a predicate for AIMClusterModel watches
func (r *AIMServiceReconciler) clusterModelPredicate() predicate.Funcs {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			// Don't trigger on creation - model status is empty initially
			return false
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldModel, ok := e.ObjectOld.(*aimv1alpha1.AIMClusterModel)
			if !ok {
				return false
			}
			newModel, ok := e.ObjectNew.(*aimv1alpha1.AIMClusterModel)
			if !ok {
				return false
			}
			// Trigger if status changed
			statusChanged := oldModel.Status.Status != newModel.Status.Status
			if statusChanged {
				ctrl.Log.Info("AIMClusterModel status changed - triggering reconciliation",
					"model", newModel.Name,
					"oldStatus", oldModel.Status.Status,
					"newStatus", newModel.Status.Status)
			}
			return statusChanged
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			ctrl.Log.V(1).Info("AIMClusterModel delete event (skipped)", "model", e.Object.GetName())
			return false
		},
	}
}

func (r *AIMServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	ctx := context.Background()

	if err := mgr.GetFieldIndexer().IndexField(ctx, &aimv1alpha1.AIMService{}, aimServiceTemplateIndexKey, r.templateIndexFunc); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&aimv1alpha1.AIMService{}).
		Owns(&servingv1beta1.InferenceService{}).
		Owns(&aimv1alpha1.AIMServiceTemplate{}).
		Owns(&gatewayapiv1.HTTPRoute{}).
		Watches(&aimv1alpha1.AIMServiceTemplate{}, handler.EnqueueRequestsFromMapFunc(r.templateHandlerFunc())).
		Watches(&aimv1alpha1.AIMClusterServiceTemplate{}, handler.EnqueueRequestsFromMapFunc(r.clusterTemplateHandlerFunc())).
		Watches(&aimv1alpha1.AIMModel{}, handler.EnqueueRequestsFromMapFunc(r.modelHandlerFunc()), builder.WithPredicates(r.modelPredicate())).
		Watches(&aimv1alpha1.AIMClusterModel{}, handler.EnqueueRequestsFromMapFunc(r.clusterModelHandlerFunc()), builder.WithPredicates(r.clusterModelPredicate())).
		Watches(&aimv1alpha1.AIMTemplateCache{}, handler.EnqueueRequestsFromMapFunc(r.templateCacheHandlerFunc())).
		Named("aim-service").
		Complete(r)
}

// templateIndexFunc provides the index function for template references
func (r *AIMServiceReconciler) templateIndexFunc(obj client.Object) []string {
	service, ok := obj.(*aimv1alpha1.AIMService)
	if !ok {
		return nil
	}
	resolved := strings.TrimSpace(service.Spec.TemplateRef)
	if resolved == "" {
		if service.Status.ResolvedTemplate != nil {
			resolved = strings.TrimSpace(service.Status.ResolvedTemplate.Name)
		}
	}
	if resolved == "" {
		return nil
	}
	return []string{resolved}
}

// RequestsForServices converts a list of AIMServices to reconcile requests.
func RequestsForServices(services []aimv1alpha1.AIMService) []reconcile.Request {
	if len(services) == 0 {
		return nil
	}

	requests := make([]reconcile.Request, 0, len(services))
	for _, svc := range services {
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: svc.Namespace,
				Name:      svc.Name,
			},
		})
	}
	return requests
}
