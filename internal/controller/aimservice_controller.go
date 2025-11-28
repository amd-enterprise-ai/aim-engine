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

	"github.com/amd-enterprise-ai/aim-engine/internal/aimservice"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
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

//
//// ============================================================================
//// DOMAIN RECONCILER IMPLEMENTATION
//// ============================================================================
//
//type serviceReconciler struct {
//	Scheme *runtime.Scheme
//}
//
//// ----- Observe Phase -----
//
//func (s *serviceReconciler) Observe(ctx context.Context, c client.Client, service *aimv1alpha1.AIMService) (*aimservicetemplate2.ServiceObservation, error) {
//	logger := log.FromContext(ctx)
//	resolution, selectionStatus, err := aimservicetemplate2.ResolveTemplateNameForService(ctx, c, service)
//	if err != nil {
//		return nil, err
//	}
//
//	logger.V(1).Info("Template resolution complete",
//		"finalName", resolution.FinalName,
//		"baseName", resolution.BaseName,
//		"derived", resolution.Derived,
//		"autoSelected", selectionStatus.AutoSelected,
//		"candidateCount", selectionStatus.CandidateCount)
//
//	obs := &aimservicetemplate2.ServiceObservation{
//		TemplateName:              resolution.FinalName,
//		BaseTemplateName:          resolution.BaseName,
//		Scope:                     aimtypes.TemplateScopeNone,
//		AutoSelectedTemplate:      selectionStatus.AutoSelected,
//		TemplateSelectionReason:   selectionStatus.SelectionReason,
//		TemplateSelectionMessage:  selectionStatus.SelectionMessage,
//		TemplateSelectionCount:    selectionStatus.CandidateCount,
//		TemplatesExistButNotReady: selectionStatus.TemplatesExistButNotReady,
//		ImageReady:                selectionStatus.ImageReady,
//		ImageReadyReason:          selectionStatus.ImageReadyReason,
//		ImageReadyMessage:         selectionStatus.ImageReadyMessage,
//		ModelResolutionErr:        selectionStatus.ModelResolutionErr,
//	}
//
//	// Observe template based on whether it's derived or not
//	if resolution.Derived {
//		obs.TemplateNamespace = service.Namespace
//		logger.V(1).Info("Observing derived template", "templateName", resolution.FinalName)
//		if err := aimservicetemplate2.ObserveDerivedTemplate(ctx, c, service, resolution, obs); err != nil {
//			return nil, err
//		}
//	} else if resolution.FinalName != "" {
//		logger.V(1).Info("Observing non-derived template", "templateName", resolution.FinalName, "scope", resolution.Scope)
//		if err := aimservicetemplate2.ObserveNonDerivedTemplate(ctx, c, service, resolution.FinalName, resolution.Scope, obs); err != nil {
//			return nil, err
//		}
//	}
//
//	// Set template namespace if creating a new template
//	if obs.ShouldCreateTemplate && obs.TemplateNamespace == "" {
//		obs.TemplateNamespace = service.Namespace
//	}
//
//	// Only auto-create templates when overrides are specified (derived templates).
//	// If no template can be resolved and no overrides are specified, the service should degrade.
//	// This prevents magic template creation and enforces explicit configuration.
//	if !obs.TemplateFound() && resolution.Derived {
//		obs.ShouldCreateTemplate = true
//		logger.V(1).Info("Will create derived template", "templateName", obs.TemplateName)
//	}
//
//	// Resolve route path if routing is enabled via service or runtime defaults
//	routingConfig := routingconfig.Resolve(service, obs.RuntimeConfigSpec.Routing)
//
//	if routingConfig.Enabled && obs.TemplateFound() {
//		logger.V(1).Info("Routing is enabled, resolving route path")
//		if routePath, err := routing.ResolveServiceRoutePath(service, obs.RuntimeConfigSpec); err != nil {
//			obs.PathTemplateErr = err
//			logger.V(1).Info("Route path resolution failed", "error", err)
//		} else {
//			obs.RoutePath = routePath
//			logger.V(1).Info("Route path resolved", "path", routePath)
//		}
//
//		// Resolve request timeout for the route
//		obs.RouteTimeout = routing.ResolveServiceRouteTimeout(service, obs.RuntimeConfigSpec)
//		if obs.RouteTimeout != nil {
//			logger.V(1).Info("Route timeout resolved", "timeout", *obs.RouteTimeout)
//		} else {
//			logger.V(1).Info("No route timeout configured")
//		}
//	}
//
//	// Check InferenceService pods for image pull errors
//	// Only check if we have a valid runtime name (template resolution succeeded)
//	if obs.RuntimeName() != "" {
//		logger.V(1).Info("Checking InferenceService pods for image pull errors",
//			"serviceName", service.Name)
//		// Use the same naming function that we use when creating the InferenceService
//		isvcName := aimservice.GenerateInferenceServiceName(service.Name, service.Namespace)
//		obs.InferenceServicePodImageError = aimservicetemplate.CheckInferenceServicePodImagePullStatus(
//			ctx, c, isvcName, service.Namespace)
//		if obs.InferenceServicePodImageError != nil {
//			logger.V(1).Info("Found InferenceService pod image pull error",
//				"errorType", obs.InferenceServicePodImageError.Type,
//				"container", obs.InferenceServicePodImageError.Container)
//		}
//	}
//
//	//
//
//	// Always check for template caches that can be used for this service
//	// This allows services to use pre-created caches even if cacheModel=false
//	if obs.TemplateName != "" {
//		templateCaches := aimv1alpha1.AIMTemplateCacheList{}
//		err := c.List(ctx, &templateCaches, client.InNamespace(service.Namespace))
//		if err != nil {
//			return nil, err
//		}
//
//		// Look for a template cache that matches our template
//		for _, tc := range templateCaches.Items {
//			if tc.Spec.TemplateName == obs.TemplateName {
//				obs.TemplateCache = &tc
//				logger.V(1).Info("Found template cache for service",
//					"cache", tc.Name,
//					"template", obs.TemplateName,
//					"status", tc.Status.Status)
//
//				// Fetch the model caches referenced by this template cache
//				if tc.Status.Status == aimv1alpha1.AIMTemplateCacheStatusAvailable ||
//					tc.Status.Status == aimv1alpha1.AIMTemplateCacheStatusProgressing {
//					modelCaches := aimv1alpha1.AIMModelCacheList{}
//					err = c.List(ctx, &modelCaches, client.InNamespace(service.Namespace))
//					if err != nil {
//						return nil, err
//					}
//					obs.ModelCaches = &modelCaches
//					logger.V(1).Info("Found model caches for template cache",
//						"count", len(modelCaches.Items))
//				}
//				break
//			}
//		}
//	}
//
//	return obs, nil
//}
//
//// ----- Plan Phase -----
//
//func (s *serviceReconciler) Plan(ctx context.Context, service *aimv1alpha1.AIMService, obs *aimservicetemplate2.ServiceObservation) ([]client.Object, error) {
//	logger := log.FromContext(ctx)
//	var desired []client.Object
//
//	if obs == nil {
//		return desired, nil
//	}
//
//	ownerRef := metav1.OwnerReference{
//		APIVersion:         service.APIVersion,
//		Kind:               service.Kind,
//		Name:               service.Name,
//		UID:                service.UID,
//		Controller:         Pointer(true),
//		BlockOwnerDeletion: Pointer(true),
//	}
//
//	// Plan derived template if needed
//	if template := s.planDerivedTemplate(logger, service, obs); template != nil {
//		desired = append(desired, template)
//	}
//
//	// Plan template cache if needed
//	if templateCache := s.planTemplateCache(ctx, logger, service, obs); templateCache != nil {
//		desired = append(desired, templateCache)
//	}
//
//	// Plan InferenceService and HTTPRoute if template is ready
//	if obs.TemplateAvailable && obs.RuntimeConfigErr == nil {
//		isvcObjects := s.planInferenceServiceAndRoute(logger, service, obs, ownerRef)
//		desired = append(desired, isvcObjects...)
//	} else {
//		logger.V(1).Info("Template not available or runtime config error, skipping InferenceService",
//			"templateAvailable", obs.TemplateAvailable,
//			"hasRuntimeConfigErr", obs.RuntimeConfigErr != nil)
//	}
//
//	return desired, nil
//}
//
//// ============================================================================
//// PLAN HELPERS
//// ============================================================================
//
//func (s *serviceReconciler) planDerivedTemplate(logger logr.Logger, service *aimv1alpha1.AIMService, obs *aimservicetemplate2.ServiceObservation) client.Object {
//	// Manage namespace-scoped template if we created it or need to create it.
//	if obs.ShouldCreateTemplate || (obs.Scope == aimtypes.TemplateScopeNamespace && obs.TemplateOwnedByService) {
//		logger.V(1).Info("Planning to manage derived template",
//			"shouldCreate", obs.ShouldCreateTemplate,
//			"ownedByService", obs.TemplateOwnedByService)
//		var baseSpec *aimv1alpha1.AIMServiceTemplateSpec
//		if obs.TemplateSpec != nil {
//			baseSpec = obs.TemplateSpec.DeepCopy()
//		}
//		// Get resolved model name from observation
//		resolvedModelName := ""
//		if obs.ResolvedImage != nil {
//			resolvedModelName = obs.ResolvedImage.Name
//		}
//		return aimservicetemplate.BuildDerivedTemplate(service, obs.TemplateName, resolvedModelName, baseSpec)
//	}
//	return nil
//}
//
//func (s *serviceReconciler) planTemplateCache(ctx context.Context, logger logr.Logger, service *aimv1alpha1.AIMService, obs *aimservicetemplate2.ServiceObservation) client.Object {
//	// Create template cache if service requests caching but none exists
//	// Works for both namespace-scoped and cluster-scoped templates
//	if service.Spec.CacheModel && obs.TemplateCache == nil && obs.TemplateAvailable &&
//		obs.TemplateStatus != nil && len(obs.TemplateStatus.ModelSources) > 0 {
//		logger.V(1).Info("Service requests caching but no template cache exists, creating one",
//			"templateName", obs.TemplateName, "scope", obs.Scope)
//
//		cache := &aimv1alpha1.AIMTemplateCache{
//			TypeMeta: metav1.TypeMeta{
//				APIVersion: "aim.eai.amd.com/v1alpha1",
//				Kind:       "AIMTemplateCache",
//			},
//			ObjectMeta: metav1.ObjectMeta{
//				Name:      obs.TemplateName,
//				Namespace: service.Namespace,
//			},
//			Spec: aimv1alpha1.AIMTemplateCacheSpec{
//				TemplateName:     obs.TemplateName,
//				StorageClassName: obs.RuntimeConfigSpec.DefaultStorageClassName,
//				Env:              service.Spec.Env,
//			},
//		}
//
//		// Only set owner reference for namespace-scoped templates
//		// Kubernetes doesn't allow namespace-scoped resources to own cluster-scoped resources
//		if obs.Scope == aimtypes.TemplateScopeNamespace {
//			var template aimv1alpha1.AIMServiceTemplate
//			err := r.Get(ctx, client.ObjectKey{
//				Namespace: service.Namespace,
//				Name:      obs.TemplateName,
//			}, &template)
//			if err != nil {
//				logger.V(1).Info("Failed to get template for cache creation", "error", err)
//				return nil
//			}
//
//			cache.OwnerReferences = []metav1.OwnerReference{
//				{
//					APIVersion:         template.APIVersion,
//					Kind:               template.Kind,
//					Name:               template.Name,
//					UID:                template.UID,
//					Controller:         Pointer(true),
//					BlockOwnerDeletion: Pointer(true),
//				},
//			}
//		}
//		// For cluster-scoped templates, no owner reference is set
//		// The cache lifecycle is managed by the template cache controller
//
//		return cache
//	}
//	return nil
//}
//
//type cacheMount struct {
//	cache     aimv1alpha1.AIMModelCache
//	modelName string
//}
//
//func (s *serviceReconciler) computeModelCacheMounts(service *aimv1alpha1.AIMService, obs *aimservicetemplate2.ServiceObservation, templateState TemplateState) ([]cacheMount, bool) {
//	modelsReady := templateState.ModelSource != nil
//	templateCacheReady := obs.TemplateCache != nil && obs.TemplateCache.Status.Status == aimv1alpha1.AIMTemplateCacheStatusAvailable
//
//	var modelCachesToMount []cacheMount
//	// If template cache is ready, use it regardless of whether cacheModel is set
//	if modelsReady && templateCacheReady {
//		// We know our models, verify that they are cached
//	SEARCH:
//		for _, model := range templateState.Status.ModelSources {
//			for _, modelCache := range obs.ModelCaches.Items {
//				// Select first modelCache that matches sourceURI and is Available
//				if model.SourceURI == modelCache.Spec.SourceURI && modelCache.Status.Status == aimv1alpha1.AIMModelCacheStatusAvailable {
//					modelCachesToMount = append(modelCachesToMount, cacheMount{
//						cache:     modelCache,
//						modelName: model.Name,
//					})
//					continue SEARCH
//				}
//			}
//			// We searched for an Available cache, but didn't find one
//			// If cacheModel is true, this is a failure (we need the cache)
//			// If cacheModel is false, we can fall back to downloading (models are still ready)
//			if service.Spec.CacheModel {
//				modelsReady = false
//			}
//		}
//	}
//
//	return modelCachesToMount, modelsReady
//}
//
//func (s *serviceReconciler) planInferenceServiceAndRoute(logger logr.Logger, service *aimv1alpha1.AIMService, obs *aimservicetemplate2.ServiceObservation, ownerRef metav1.OwnerReference) []client.Object {
//	var desired []client.Object
//
//	logger.V(1).Info("Template is available, planning InferenceService")
//	routePath := routing.DefaultRoutePath(service)
//	if obs.PathTemplateErr == nil && obs.RoutePath != "" {
//		routePath = obs.RoutePath
//	}
//
//	templateState := NewTemplateState(TemplateState{
//		Name:              obs.TemplateName,
//		Namespace:         obs.TemplateNamespace,
//		SpecCommon:        obs.TemplateSpecCommon,
//		ImageResources:    obs.ImageResources,
//		RuntimeConfigSpec: obs.RuntimeConfigSpec,
//		Status:            obs.TemplateStatus,
//	})
//
//	serviceState := aimservice.NewServiceState(service, templateState, aimservice.ServiceStateOptions{
//		RuntimeName:    obs.RuntimeName(),
//		RoutePath:      routePath,
//		RequestTimeout: obs.RouteTimeout,
//	})
//
//	// Compute model cache mounts
//	modelCachesToMount, modelsReady := s.computeModelCacheMounts(service, obs, templateState)
//	templateCacheReady := obs.TemplateCache != nil && obs.TemplateCache.Status.Status == aimv1alpha1.AIMTemplateCacheStatusAvailable
//
//	// Determine if we need a service PVC
//	// Only create service PVC if:
//	// 1. cacheModel is false (service doesn't want to wait for template cache) AND
//	// 2. No template cache exists (no pre-created cache available to use)
//	// This prevents creating temp PVCs when cacheModel=true or when a cache already exists
//	var servicePVC *v1.PersistentVolumeClaim
//	var servicePVCErr error
//	if !service.Spec.CacheModel && obs.TemplateCache == nil {
//		headroomPercent := aimservicetemplate.GetPVCHeadroomPercent(obs.RuntimeConfigSpec)
//		servicePVC, servicePVCErr = buildServicePVC(service, templateState, obs.RuntimeConfigSpec.DefaultStorageClassName, headroomPercent)
//		if servicePVCErr != nil {
//			logger.V(1).Info("Failed to build service PVC", "error", servicePVCErr)
//			// This error will be handled in status projection
//		} else {
//			desired = append(desired, servicePVC)
//		}
//	}
//
//	// Service is ready if:
//	// - Models are ready AND
//	// - Either we don't need caching OR cache is ready
//	// - AND either we have a template cache OR we successfully created a service PVC
//	serviceReady := modelsReady && (!service.Spec.CacheModel || templateCacheReady) && (templateCacheReady || servicePVC != nil)
//
//	// Only create InferenceService if we have a model source and storage
//	if serviceReady {
//		inferenceService := aimservice.BuildInferenceService(serviceState, ownerRef)
//
//		// Set AIM_CACHE_PATH env var for all services
//		inferenceService.Spec.Predictor.Model.Env = append(
//			inferenceService.Spec.Predictor.Model.Env,
//			v1.EnvVar{
//				Name:  "AIM_CACHE_PATH",
//				Value: AIMCacheBasePath,
//			})
//
//		// Mount either template cache PVCs or service PVC
//		if len(modelCachesToMount) > 0 {
//			// Mount template cache PVCs
//			for _, cm := range modelCachesToMount {
//				addModelCacheMount(inferenceService, cm.cache, cm.modelName)
//			}
//		} else if servicePVC != nil {
//			// Mount service PVC for model downloads
//			addServicePVCMount(inferenceService, servicePVC.Name)
//		}
//
//		desired = append(desired, inferenceService)
//	} else {
//		if servicePVCErr != nil {
//			logger.V(1).Info("Service not ready due to PVC creation error", "error", servicePVCErr)
//		} else {
//			logger.V(1).Info("Model source not available, skipping InferenceService creation")
//		}
//	}
//
//	// Create HTTPRoute if routing is enabled, regardless of model source availability
//	if serviceState.Routing.Enabled && serviceState.Routing.GatewayRef != nil && obs.PathTemplateErr == nil {
//		logger.V(1).Info("Routing enabled, building HTTPRoute",
//			"gateway", serviceState.Routing.GatewayRef.Name,
//			"path", routePath)
//		route := routing.BuildInferenceServiceHTTPRoute(serviceState, ownerRef)
//		desired = append(desired, route)
//	}
//
//	return desired
//}
//
//func (r *AIMServiceReconciler) plan(ctx context.Context, service *aimv1alpha1.AIMService, obs *aimservicetemplate2.ServiceObservation) []client.Object {
//	logger := log.FromContext(ctx)
//	var desired []client.Object
//
//	if obs == nil {
//		return desired
//	}
//
//	ownerRef := metav1.OwnerReference{
//		APIVersion:         service.APIVersion,
//		Kind:               service.Kind,
//		Name:               service.Name,
//		UID:                service.UID,
//		Controller:         Pointer(true),
//		BlockOwnerDeletion: Pointer(true),
//	}
//
//	// Plan derived template if needed
//	if template := r.planDerivedTemplate(logger, service, obs); template != nil {
//		desired = append(desired, template)
//	}
//
//	// Plan template cache if needed
//	if templateCache := r.planTemplateCache(ctx, logger, service, obs); templateCache != nil {
//		desired = append(desired, templateCache)
//	}
//
//	// Plan InferenceService and HTTPRoute if template is ready
//	if obs.TemplateAvailable && obs.RuntimeConfigErr == nil {
//		isvcObjects := r.planInferenceServiceAndRoute(logger, service, obs, ownerRef)
//		desired = append(desired, isvcObjects...)
//	} else {
//		logger.V(1).Info("Template not available or runtime config error, skipping InferenceService",
//			"templateAvailable", obs.TemplateAvailable,
//			"hasRuntimeConfigErr", obs.RuntimeConfigErr != nil)
//	}
//
//	return desired
//}
//
//// buildServicePVC creates a PVC for a service to store downloaded models.
//// This is used when there's no template cache available.
//// Returns nil and an error if model sizes aren't specified.
//func buildServicePVC(service *aimv1alpha1.AIMService, templateState TemplateState, storageClassName string, headroomPercent int32) (*v1.PersistentVolumeClaim, error) {
//	// Generate PVC name using same pattern as InferenceService
//	pvcName := aimservice.GenerateInferenceServiceName(service.Name, service.Namespace) + "-temp-cache"
//
//	// Calculate required size from model sources
//	size, err := calculateRequiredStorageSize(templateState, headroomPercent)
//	if err != nil {
//		return nil, fmt.Errorf("cannot determine storage size: %w", err)
//	}
//
//	var sc *string
//	if storageClassName != "" {
//		sc = &storageClassName
//	}
//
//	return &v1.PersistentVolumeClaim{
//		TypeMeta: metav1.TypeMeta{
//			APIVersion: "v1",
//			Kind:       "PersistentVolumeClaim",
//		},
//		ObjectMeta: metav1.ObjectMeta{
//			Name:      pvcName,
//			Namespace: service.Namespace,
//			Labels: map[string]string{
//				"app.kubernetes.io/managed-by": "aim-service-controller",
//				"app.kubernetes.io/component":  "model-storage",
//				constants.LabelKeyServiceName:  aimservice.SanitizeLabelValue(service.Name),
//				constants.LabelKeyCacheType:    constants.LabelValueCacheTypeTempService,
//				constants.LabelKeyTemplate:     templateState.Name,
//				constants.LabelKeyModelID:      aimservice.SanitizeLabelValue(templateState.SpecCommon.ModelName),
//			},
//			OwnerReferences: []metav1.OwnerReference{
//				{
//					APIVersion:         service.APIVersion,
//					Kind:               service.Kind,
//					Name:               service.Name,
//					UID:                service.UID,
//					Controller:         Pointer(true),
//					BlockOwnerDeletion: Pointer(true),
//				},
//			},
//		},
//		Spec: v1.PersistentVolumeClaimSpec{
//			AccessModes: []v1.PersistentVolumeAccessMode{v1.ReadWriteMany},
//			Resources: v1.VolumeResourceRequirements{
//				Requests: v1.ResourceList{
//					v1.ResourceStorage: size,
//				},
//			},
//			StorageClassName: sc,
//		},
//	}, nil
//}
//
//// calculateRequiredStorageSize computes the total storage needed for model sources.
//// Returns sum of all model sizes plus the specified headroom percentage, or an error if sizes aren't specified.
//// headroomPercent represents the percentage (0-100) of extra space to add. For example, 10 means 10% extra.
//func calculateRequiredStorageSize(templateState TemplateState, headroomPercent int32) (resource.Quantity, error) {
//	if templateState.Status == nil || len(templateState.Status.ModelSources) == 0 {
//		return resource.Quantity{}, fmt.Errorf("no model sources available in template")
//	}
//
//	var totalBytes int64
//	for _, modelSource := range templateState.Status.ModelSources {
//		if modelSource.Size.IsZero() {
//			return resource.Quantity{}, fmt.Errorf("model source %q has no size specified", modelSource.Name)
//		}
//		totalBytes += modelSource.Size.Value()
//	}
//
//	if totalBytes == 0 {
//		return resource.Quantity{}, fmt.Errorf("total model size is zero")
//	}
//
//	// Apply headroom and round to nearest Gi using shared utility
//	return utils.QuantityWithHeadroom(totalBytes, headroomPercent), nil
//}
//
//func addServicePVCMount(inferenceService *servingv1beta1.InferenceService, pvcName string) {
//	volumeName := "model-storage"
//
//	// Add the PVC volume
//	inferenceService.Spec.Predictor.Volumes = append(inferenceService.Spec.Predictor.Volumes, v1.Volume{
//		Name: volumeName,
//		VolumeSource: v1.VolumeSource{
//			PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{
//				ClaimName: pvcName,
//			},
//		},
//	})
//
//	// Mount the volume in the kserve-container
//	inferenceService.Spec.Predictor.Model.VolumeMounts = append(inferenceService.Spec.Predictor.Model.VolumeMounts, v1.VolumeMount{
//		Name:      volumeName,
//		MountPath: AIMCacheBasePath,
//	})
//}
//
//func addModelCacheMount(inferenceService *servingv1beta1.InferenceService, modelCache aimv1alpha1.AIMModelCache, modelName string) {
//	// Sanitize volume name for Kubernetes (no dots allowed in volume names, only lowercase alphanumeric and '-')
//	volumeName := utils.MakeRFC1123Compliant(modelCache.Name)
//	volumeName = strings.ReplaceAll(volumeName, ".", "-")
//
//	// Add the PVC volume for the model cache
//	inferenceService.Spec.Predictor.Volumes = append(inferenceService.Spec.Predictor.Volumes, v1.Volume{
//		Name: volumeName,
//		VolumeSource: v1.VolumeSource{
//			PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{
//				ClaimName: modelCache.Status.PersistentVolumeClaim,
//			},
//		},
//	})
//
//	// Mount at the AIM cache base path + model name (using filepath.Join for safe path construction)
//	// e.g., /workspace/model-cache/meta-llama/Llama-3.1-8B
//	mountPath := filepath.Join(AIMCacheBasePath, modelName)
//
//	inferenceService.Spec.Predictor.Model.VolumeMounts = append(
//		inferenceService.Spec.Predictor.Model.VolumeMounts,
//		v1.VolumeMount{
//			Name:      volumeName,
//			MountPath: mountPath,
//		})
//}
//
//func (r *AIMServiceReconciler) projectStatus(
//	ctx context.Context,
//	service *aimv1alpha1.AIMService,
//	obs *aimservicetemplate2.ServiceObservation,
//	errs controllerutils.ReconcileErrors,
//) error {
//	// Fetch InferenceService and HTTPRoute for status evaluation
//	var inferenceService *servingv1beta1.InferenceService
//	{
//		var is servingv1beta1.InferenceService
//		// Use the same naming function that we use when creating the InferenceService
//		isvcName := aimservice.GenerateInferenceServiceName(service.Name, service.Namespace)
//		if err := r.Get(ctx, types.NamespacedName{
//			Namespace: service.Namespace,
//			Name:      isvcName,
//		}, &is); err == nil {
//			inferenceService = &is
//		}
//	}
//
//	var httpRoute *gatewayapiv1.HTTPRoute
//	// Check if routing is enabled via service spec or runtime config
//	var runtimeRouting *aimv1alpha1.AIMRuntimeRoutingConfig
//	if obs != nil {
//		runtimeRouting = obs.RuntimeConfigSpec.Routing
//	}
//	routingConfig := routingconfig.Resolve(service, runtimeRouting)
//	if routingConfig.Enabled {
//		routeName := routing.InferenceServiceRouteName(service.Name)
//		var route gatewayapiv1.HTTPRoute
//		if err := r.Get(ctx, types.NamespacedName{Name: routeName, Namespace: service.Namespace}, &route); err == nil {
//			httpRoute = &route
//		}
//	}
//
//	// Delegate status projection to shared function
//	ProjectServiceStatus(service, obs, inferenceService, httpRoute, errs)
//	return nil
//}
//
//// findServicesByTemplate finds services that reference a template by name or share the same model
//func (r *AIMServiceReconciler) findServicesByTemplate(
//	ctx context.Context,
//	templateName string,
//	templateNamespace string,
//	modelName string,
//	isClusterScoped bool,
//) []aimv1alpha1.AIMService {
//	// Find services that reference this template by name (explicit templateRef or already resolved)
//	var servicesWithRef aimv1alpha1.AIMServiceList
//	listOpts := []client.ListOption{client.MatchingFields{aimServiceTemplateIndexKey: templateName}}
//	if !isClusterScoped {
//		listOpts = append(listOpts, client.InNamespace(templateNamespace))
//	}
//
//	if err := r.List(ctx, &servicesWithRef, listOpts...); err != nil {
//		ctrl.LoggerFrom(ctx).Error(err, "failed to list AIMServices for template", "template", templateName)
//		return nil
//	}
//
//	// Also find services doing auto-selection with the same image name
//	var servicesWithImage aimv1alpha1.AIMServiceList
//	imageListOpts := []client.ListOption{}
//	if !isClusterScoped {
//		imageListOpts = append(imageListOpts, client.InNamespace(templateNamespace))
//	}
//
//	if err := r.List(ctx, &servicesWithImage, imageListOpts...); err != nil {
//		ctrl.LoggerFrom(ctx).Error(err, "failed to list AIMServices for image matching")
//		return nil
//	}
//
//	// Combine results, filtering for services with matching image that don't have explicit templateRef
//	serviceMap := make(map[string]aimv1alpha1.AIMService)
//	for _, svc := range servicesWithRef.Items {
//		serviceMap[svc.Namespace+"/"+svc.Name] = svc
//	}
//
//	for _, svc := range servicesWithImage.Items {
//		// Skip if already included via template name index
//		key := svc.Namespace + "/" + svc.Name
//		if _, exists := serviceMap[key]; exists {
//			continue
//		}
//
//		// Include if doing auto-selection (no templateRef) and matches resolved image
//		if strings.TrimSpace(svc.Spec.TemplateRef) == "" {
//			svcModelName := r.getServiceModelName(&svc)
//			if svcModelName != "" && svcModelName == strings.TrimSpace(modelName) {
//				serviceMap[key] = svc
//			}
//		}
//	}
//
//	// Convert map to slice
//	services := make([]aimv1alpha1.AIMService, 0, len(serviceMap))
//	for _, svc := range serviceMap {
//		services = append(services, svc)
//	}
//
//	return services
//}
//
//// getServiceModelName extracts the model name from a service
//func (r *AIMServiceReconciler) getServiceModelName(svc *aimv1alpha1.AIMService) string {
//	if svc.Status.ResolvedModel != nil {
//		return svc.Status.ResolvedModel.Name
//	}
//	if svc.Spec.Model.Ref != nil {
//		return strings.TrimSpace(*svc.Spec.Model.Ref)
//	}
//	return ""
//}
//
//// templateHandlerFunc returns a handler function for AIMServiceTemplate watches
//func (r *AIMServiceReconciler) templateHandlerFunc() handler.MapFunc {
//	return func(ctx context.Context, obj client.Object) []reconcile.Request {
//		template, ok := obj.(*aimv1alpha1.AIMServiceTemplate)
//		if !ok {
//			return nil
//		}
//
//		services := r.findServicesByTemplate(ctx, template.Name, template.Namespace, template.Spec.ModelName, false)
//		return RequestsForServices(services)
//	}
//}
//
//// clusterTemplateHandlerFunc returns a handler function for AIMClusterServiceTemplate watches
//func (r *AIMServiceReconciler) clusterTemplateHandlerFunc() handler.MapFunc {
//	return func(ctx context.Context, obj client.Object) []reconcile.Request {
//		clusterTemplate, ok := obj.(*aimv1alpha1.AIMClusterServiceTemplate)
//		if !ok {
//			return nil
//		}
//
//		services := r.findServicesByTemplate(ctx, clusterTemplate.Name, "", clusterTemplate.Spec.ModelName, true)
//		return RequestsForServices(services)
//	}
//}
//
//func (r *AIMServiceReconciler) templateCacheHandlerFunc() handler.MapFunc {
//	return func(ctx context.Context, obj client.Object) []reconcile.Request {
//		templateCache, ok := obj.(*aimv1alpha1.AIMTemplateCache)
//		if !ok {
//			return nil
//		}
//
//		var services aimv1alpha1.AIMServiceList
//		if err := r.List(ctx, &services,
//			client.InNamespace(templateCache.Namespace),
//			client.MatchingFields{aimServiceTemplateIndexKey: templateCache.Spec.TemplateName},
//		); err != nil {
//			ctrl.LoggerFrom(ctx).Error(err, "failed to list AIMServices for AIMServiceTemplate", "template", templateCache.Name)
//			return nil
//		}
//
//		return RequestsForServices(services.Items)
//	}
//}
//
//// findServicesByModel finds services that reference a model
//func (r *AIMServiceReconciler) findServicesByModel(ctx context.Context, model *aimv1alpha1.AIMModel) []aimv1alpha1.AIMService {
//	logger := ctrl.LoggerFrom(ctx)
//
//	// Only trigger reconciliation for auto-created models
//	if model.Labels[aimmodel.LabelAutoCreated] != "true" {
//		logger.V(1).Info("Skipping model - not auto-created", "model", model.Name)
//		return nil
//	}
//
//	// Find services using this model
//	var services aimv1alpha1.AIMServiceList
//	if err := r.List(ctx, &services, client.InNamespace(model.Namespace)); err != nil {
//		logger.Error(err, "failed to list AIMServices for AIMModel", "model", model.Name)
//		return nil
//	}
//
//	var matchingServices []aimv1alpha1.AIMService
//	for i := range services.Items {
//		svc := &services.Items[i]
//		if r.serviceUsesModel(svc, model, logger) {
//			matchingServices = append(matchingServices, *svc)
//		}
//	}
//
//	return matchingServices
//}
//
//// serviceUsesModel checks if a service uses the specified model
//func (r *AIMServiceReconciler) serviceUsesModel(svc *aimv1alpha1.AIMService, model *aimv1alpha1.AIMModel, logger logr.Logger) bool {
//	// Check if service uses this model by:
//	// 1. Explicit ref (spec.model.ref)
//	if svc.Spec.Model.Ref != nil && *svc.Spec.Model.Ref == model.Name {
//		return true
//	}
//	// 2. Image URL that resolves to this model (check status)
//	if svc.Status.ResolvedModel != nil && svc.Status.ResolvedModel.Name == model.Name {
//		return true
//	}
//	// 3. Image URL in spec (need to check if it would resolve to this model)
//	// This is the case when service was just created and status not yet set
//	if svc.Spec.Model.Image != nil {
//		logger.V(1).Info("Service has image URL but no resolved image yet",
//			"service", svc.Name,
//			"image", *svc.Spec.Model.Image,
//			"model", model.Name)
//		// For now, add all services with image URLs in the same namespace
//		// The service reconciliation will properly resolve and filter
//		return true
//	}
//	return false
//}
//
//// modelHandlerFunc returns a handler function for AIMModel watches
//func (r *AIMServiceReconciler) modelHandlerFunc() handler.MapFunc {
//	return func(ctx context.Context, obj client.Object) []reconcile.Request {
//		model, ok := obj.(*aimv1alpha1.AIMModel)
//		if !ok {
//			return nil
//		}
//
//		matchingServices := r.findServicesByModel(ctx, model)
//		return RequestsForServices(matchingServices)
//	}
//}
//
//// modelPredicate returns a predicate for AIMModel watches
//func (r *AIMServiceReconciler) modelPredicate() predicate.Funcs {
//	return predicate.Funcs{
//		CreateFunc: func(e event.CreateEvent) bool {
//			// Don't trigger on creation - model status is empty initially
//			ctrl.Log.V(1).Info("AIMModel create event (skipped)", "model", e.Object.GetName())
//			return false
//		},
//		UpdateFunc: func(e event.UpdateEvent) bool {
//			oldModel, ok := e.ObjectOld.(*aimv1alpha1.AIMModel)
//			if !ok {
//				return false
//			}
//			newModel, ok := e.ObjectNew.(*aimv1alpha1.AIMModel)
//			if !ok {
//				return false
//			}
//			// Trigger if status changed
//			statusChanged := oldModel.Status.Status != newModel.Status.Status
//			if statusChanged {
//				ctrl.Log.Info("AIMModel status changed - triggering reconciliation",
//					"model", newModel.Name,
//					"namespace", newModel.Namespace,
//					"oldStatus", oldModel.Status.Status,
//					"newStatus", newModel.Status.Status)
//			} else {
//				ctrl.Log.V(1).Info("AIMModel update (no status change)",
//					"model", newModel.Name,
//					"status", newModel.Status.Status)
//			}
//			return statusChanged
//		},
//		DeleteFunc: func(e event.DeleteEvent) bool {
//			ctrl.Log.V(1).Info("AIMModel delete event (skipped)", "model", e.Object.GetName())
//			return false
//		},
//	}
//}
//
//// findServicesByClusterModel finds services that reference a cluster model
//func (r *AIMServiceReconciler) findServicesByClusterModel(ctx context.Context, clusterModel *aimv1alpha1.AIMClusterModel) []aimv1alpha1.AIMService {
//	logger := ctrl.LoggerFrom(ctx)
//
//	// Note: Unlike namespace-scoped models, cluster models are never auto-created by the system.
//	// They are manually created infrastructure resources, so we reconcile services for all cluster model changes.
//
//	// Find services across all namespaces using this cluster model
//	var services aimv1alpha1.AIMServiceList
//	if err := r.List(ctx, &services); err != nil {
//		logger.Error(err, "failed to list AIMServices for AIMClusterModel", "model", clusterModel.Name)
//		return nil
//	}
//
//	var matchingServices []aimv1alpha1.AIMService
//	for i := range services.Items {
//		svc := &services.Items[i]
//		if r.serviceUsesClusterModel(svc, clusterModel, logger) {
//			matchingServices = append(matchingServices, *svc)
//		}
//	}
//
//	return matchingServices
//}
//
//// serviceUsesClusterModel checks if a service uses the specified cluster model
//func (r *AIMServiceReconciler) serviceUsesClusterModel(svc *aimv1alpha1.AIMService, clusterModel *aimv1alpha1.AIMClusterModel, logger logr.Logger) bool {
//	// Check if service uses this cluster model by:
//	// 1. Explicit ref (spec.model.ref)
//	if svc.Spec.Model.Ref != nil && *svc.Spec.Model.Ref == clusterModel.Name {
//		return true
//	}
//	// 2. Image URL that resolves to this cluster model (check status)
//	if svc.Status.ResolvedModel != nil && svc.Status.ResolvedModel.Name == clusterModel.Name {
//		return true
//	}
//	// 3. Image URL in spec (need to check if it would resolve to this cluster model)
//	// This is the case when service was just created and status not yet set
//	if svc.Spec.Model.Image != nil {
//		logger.V(1).Info("Service has image URL but no resolved image yet",
//			"service", svc.Name,
//			"namespace", svc.Namespace,
//			"image", *svc.Spec.Model.Image,
//			"clusterModel", clusterModel.Name)
//		// For cluster models, add all services with image URLs across all namespaces
//		// The service reconciliation will properly resolve and filter
//		return true
//	}
//	return false
//}
//
//func (r *AIMServiceReconciler) clusterModelHandlerFunc() handler.MapFunc {
//	return func(ctx context.Context, obj client.Object) []reconcile.Request {
//		clusterModel, ok := obj.(*aimv1alpha1.AIMClusterModel)
//		if !ok {
//			return nil
//		}
//
//		matchingServices := r.findServicesByClusterModel(ctx, clusterModel)
//		return RequestsForServices(matchingServices)
//	}
//}
//
//// clusterModelPredicate returns a predicate for AIMClusterModel watches
//func (r *AIMServiceReconciler) clusterModelPredicate() predicate.Funcs {
//	return predicate.Funcs{
//		CreateFunc: func(e event.CreateEvent) bool {
//			// Don't trigger on creation - model status is empty initially
//			ctrl.Log.V(1).Info("AIMClusterModel create event (skipped)", "model", e.Object.GetName())
//			return false
//		},
//		UpdateFunc: func(e event.UpdateEvent) bool {
//			oldModel, ok := e.ObjectOld.(*aimv1alpha1.AIMClusterModel)
//			if !ok {
//				return false
//			}
//			newModel, ok := e.ObjectNew.(*aimv1alpha1.AIMClusterModel)
//			if !ok {
//				return false
//			}
//			// Trigger if status changed
//			statusChanged := oldModel.Status.Status != newModel.Status.Status
//			if statusChanged {
//				ctrl.Log.Info("AIMClusterModel status changed - triggering reconciliation",
//					"model", newModel.Name,
//					"oldStatus", oldModel.Status.Status,
//					"newStatus", newModel.Status.Status)
//			} else {
//				ctrl.Log.V(1).Info("AIMClusterModel update (no status change)",
//					"model", newModel.Name,
//					"status", newModel.Status.Status)
//			}
//			return statusChanged
//		},
//		DeleteFunc: func(e event.DeleteEvent) bool {
//			ctrl.Log.V(1).Info("AIMClusterModel delete event (skipped)", "model", e.Object.GetName())
//			return false
//		},
//	}
//}
//
//func (r *AIMServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
//	ctx := context.Background()
//
//	if err := mgr.GetFieldIndexer().IndexField(ctx, &aimv1alpha1.AIMService{}, aimServiceTemplateIndexKey, r.templateIndexFunc); err != nil {
//		return err
//	}
//
//	return ctrl.NewControllerManagedBy(mgr).
//		For(&aimv1alpha1.AIMService{}).
//		Owns(&servingv1beta1.InferenceService{}).
//		Owns(&aimv1alpha1.AIMServiceTemplate{}).
//		Owns(&gatewayapiv1.HTTPRoute{}).
//		Watches(&aimv1alpha1.AIMServiceTemplate{}, handler.EnqueueRequestsFromMapFunc(r.templateHandlerFunc())).
//		Watches(&aimv1alpha1.AIMClusterServiceTemplate{}, handler.EnqueueRequestsFromMapFunc(r.clusterTemplateHandlerFunc())).
//		Watches(&aimv1alpha1.AIMModel{}, handler.EnqueueRequestsFromMapFunc(r.modelHandlerFunc()), builder.WithPredicates(r.modelPredicate())).
//		Watches(&aimv1alpha1.AIMClusterModel{}, handler.EnqueueRequestsFromMapFunc(r.clusterModelHandlerFunc()), builder.WithPredicates(r.clusterModelPredicate())).
//		Watches(&aimv1alpha1.AIMTemplateCache{}, handler.EnqueueRequestsFromMapFunc(r.templateCacheHandlerFunc())).
//		Named("aim-service").
//		Complete(r)
//}
//
//// templateIndexFunc provides the index function for template references
//func (r *AIMServiceReconciler) templateIndexFunc(obj client.Object) []string {
//	service, ok := obj.(*aimv1alpha1.AIMService)
//	if !ok {
//		return nil
//	}
//	resolved := strings.TrimSpace(service.Spec.TemplateRef)
//	if resolved == "" {
//		if service.Status.ResolvedTemplate != nil {
//			resolved = strings.TrimSpace(service.Status.ResolvedTemplate.Name)
//		}
//	}
//	if resolved == "" {
//		return nil
//	}
//	return []string{resolved}
//}
//
//// Pointer returns a pointer to the given value
//func Pointer[T any](v T) *T {
//	return &v
//}
//
//// RequestsForServices converts a list of AIMServices to reconcile requests.
//func RequestsForServices(services []aimv1alpha1.AIMService) []reconcile.Request {
//	if len(services) == 0 {
//		return nil
//	}
//
//	requests := make([]reconcile.Request, 0, len(services))
//	for _, svc := range services {
//		requests = append(requests, reconcile.Request{
//			NamespacedName: types.NamespacedName{
//				Namespace: svc.Namespace,
//				Name:      svc.Name,
//			},
//		})
//	}
//	return requests
//}
//
//// EvaluateHTTPRouteStatus checks the HTTPRoute status and returns readiness state.
//func EvaluateHTTPRouteStatus(route *gatewayapiv1.HTTPRoute) (bool, string, string) {
//	if route == nil {
//		return false, aimv1alpha1.AIMServiceReasonConfiguringRoute, "HTTPRoute not found"
//	}
//	status := route.Status
//	if len(status.Parents) == 0 {
//		return false, aimv1alpha1.AIMServiceReasonConfiguringRoute, "HTTPRoute has no parent status"
//	}
//	for _, parent := range status.Parents {
//		// Check if the HTTPRoute is accepted by this parent gateway
//		acceptedCond := meta.FindStatusCondition(parent.Conditions, string(gatewayapiv1.RouteConditionAccepted))
//		if acceptedCond == nil {
//			return false, aimv1alpha1.AIMServiceReasonConfiguringRoute, "HTTPRoute Accepted condition not found"
//		}
//		if acceptedCond.Status != metav1.ConditionTrue {
//			reason := acceptedCond.Reason
//			if reason == "" {
//				reason = aimv1alpha1.AIMServiceReasonRouteFailed
//			}
//			message := acceptedCond.Message
//			if message == "" {
//				message = "HTTPRoute not accepted by gateway"
//			}
//			return false, reason, message
//		}
//	}
//	return true, aimv1alpha1.AIMServiceReasonRouteReady, "HTTPRoute is ready"
//}
//
//// EvaluateRoutingStatus checks routing configuration and updates status accordingly.
//// Returns (enabled, ready, hasFatalError) to indicate if routing is enabled, if it's ready, and if there's a terminal error.
//func EvaluateRoutingStatus(
//	service *aimv1alpha1.AIMService,
//	obs *aimservicetemplate2.ServiceObservation,
//	status *aimv1alpha1.AIMServiceStatus,
//	setCondition func(conditionType string, conditionStatus metav1.ConditionStatus, reason, message string),
//) (enabled bool, ready bool, hasFatalError bool) {
//	var runtimeRouting *aimv1alpha1.AIMRuntimeRoutingConfig
//	if obs != nil {
//		runtimeRouting = obs.RuntimeConfigSpec.Routing
//	}
//
//	resolved := routingconfig.Resolve(service, runtimeRouting)
//	if !resolved.Enabled {
//		setCondition(aimv1alpha1.AIMServiceConditionRoutingReady, metav1.ConditionTrue, aimv1alpha1.AIMServiceReasonRouteReady, "Routing disabled")
//		return false, true, false
//	}
//
//	routePath := routing.DefaultRoutePath(service)
//	if obs != nil && obs.RoutePath != "" {
//		routePath = obs.RoutePath
//	}
//
//	status.Routing = &aimv1alpha1.AIMServiceRoutingStatus{
//		Path: routePath,
//	}
//
//	if resolved.GatewayRef == nil {
//		message := "routing.gatewayRef must be specified via AIMService or runtime config when routing is enabled"
//		setCondition(aimv1alpha1.AIMServiceConditionRoutingReady, metav1.ConditionFalse, aimv1alpha1.AIMServiceReasonRouteFailed, message)
//		status.Status = aimv1alpha1.AIMServiceStatusFailed
//		setCondition(aimv1alpha1.AIMServiceConditionFailure, metav1.ConditionTrue, aimv1alpha1.AIMServiceReasonRouteFailed,
//			"Routing gateway reference is missing")
//		return true, false, true
//	}
//
//	return true, false, false
//}
//
//// HandleReconcileErrors processes reconciliation errors and updates service status.
//// Returns true if errors were found and handled.
//func HandleReconcileErrors(
//	status *aimv1alpha1.AIMServiceStatus,
//	setCondition func(conditionType string, conditionStatus metav1.ConditionStatus, reason, message string),
//	errs controllerutils.ReconcileErrors,
//) bool {
//	if !errs.HasError() {
//		return false
//	}
//
//	status.Status = aimv1alpha1.AIMServiceStatusFailed
//
//	reason := aimv1alpha1.AIMServiceReasonValidationFailed
//	message := "Reconciliation failed"
//	switch {
//	case errs.ObserveErr != nil:
//		message = fmt.Sprintf("Observation failed: %v", errs.ObserveErr)
//	case errs.PlanErr != nil:
//		message = fmt.Sprintf("Planning failed: %v", errs.PlanErr)
//	case errs.ApplyErr != nil:
//		reason = aimv1alpha1.AIMServiceReasonRuntimeFailed
//		message = fmt.Sprintf("Apply failed: %v", errs.ApplyErr)
//	}
//
//	setCondition(aimv1alpha1.AIMServiceConditionFailure, metav1.ConditionTrue, reason, message)
//	setCondition(aimv1alpha1.AIMServiceConditionResolved, metav1.ConditionFalse, reason, "Template resolution pending due to reconciliation failure")
//	setCondition(aimv1alpha1.AIMServiceConditionRuntimeReady, metav1.ConditionFalse, reason, message)
//	setCondition(aimv1alpha1.AIMServiceConditionProgressing, metav1.ConditionFalse, reason, "Reconciliation halted due to failure")
//	setCondition(aimv1alpha1.AIMServiceConditionReady, metav1.ConditionFalse, reason, message)
//	return true
//}
//
//// HandleRuntimeConfigMissing checks for missing runtime config and updates status.
//// Returns true if the runtime config is missing.
//func HandleRuntimeConfigMissing(
//	status *aimv1alpha1.AIMServiceStatus,
//	obs *aimservicetemplate2.ServiceObservation,
//	setCondition func(conditionType string, conditionStatus metav1.ConditionStatus, reason, message string),
//) bool {
//	if obs.RuntimeConfigErr == nil {
//		return false
//	}
//
//	status.Status = aimv1alpha1.AIMServiceStatusDegraded
//	message := obs.RuntimeConfigErr.Error()
//	reason := aimv1alpha1.AIMServiceReasonRuntimeConfigMissing
//	setCondition(aimv1alpha1.AIMServiceConditionFailure, metav1.ConditionTrue, reason, message)
//	setCondition(aimv1alpha1.AIMServiceConditionRuntimeReady, metav1.ConditionFalse, reason, "Cannot configure runtime without AIMRuntimeConfig")
//	setCondition(aimv1alpha1.AIMServiceConditionProgressing, metav1.ConditionFalse, reason, "Runtime configuration is missing")
//	setCondition(aimv1alpha1.AIMServiceConditionReady, metav1.ConditionFalse, reason, message)
//	return true
//}
//
//// HandleModelResolutionFailure checks for model resolution failures and updates status.
//// Returns true if model resolution failed.
//func HandleModelResolutionFailure(
//	status *aimv1alpha1.AIMServiceStatus,
//	obs *aimservicetemplate2.ServiceObservation,
//	setCondition func(conditionType string, conditionStatus metav1.ConditionStatus, reason, message string),
//) bool {
//	if obs.ModelResolutionErr == nil {
//		return false
//	}
//
//	status.Status = aimv1alpha1.AIMServiceStatusFailed
//	message := obs.ModelResolutionErr.Error()
//	reason := "ModelResolutionFailed"
//
//	// Check if the error is due to multiple models being found
//	if errors.Is(obs.ModelResolutionErr, model.ErrMultipleModelsFound) {
//		reason = "MultipleModelsFound"
//	}
//
//	setCondition(aimv1alpha1.AIMServiceConditionFailure, metav1.ConditionTrue, reason, message)
//	setCondition(aimv1alpha1.AIMServiceConditionResolved, metav1.ConditionFalse, reason, "Cannot resolve model for service")
//	setCondition(aimv1alpha1.AIMServiceConditionRuntimeReady, metav1.ConditionFalse, reason, "Cannot proceed without resolved model")
//	setCondition(aimv1alpha1.AIMServiceConditionProgressing, metav1.ConditionFalse, reason, "Model resolution failed")
//	setCondition(aimv1alpha1.AIMServiceConditionReady, metav1.ConditionFalse, reason, message)
//	return true
//}
//
//// HandleImageMissing checks for missing image and updates status.
//// Returns true if the image is missing.
//func HandleImageMissing(
//	status *aimv1alpha1.AIMServiceStatus,
//	obs *aimservicetemplate2.ServiceObservation,
//	setCondition func(conditionType string, conditionStatus metav1.ConditionStatus, reason, message string),
//) bool {
//	if obs.ImageErr == nil {
//		return false
//	}
//
//	status.Status = aimv1alpha1.AIMServiceStatusDegraded
//	message := obs.ImageErr.Error()
//	reason := "ImageNotFound"
//	setCondition(aimv1alpha1.AIMServiceConditionFailure, metav1.ConditionTrue, reason, message)
//	setCondition(aimv1alpha1.AIMServiceConditionRuntimeReady, metav1.ConditionFalse, reason, "Cannot create InferenceService without image")
//	setCondition(aimv1alpha1.AIMServiceConditionProgressing, metav1.ConditionFalse, reason, "Image is missing")
//	setCondition(aimv1alpha1.AIMServiceConditionReady, metav1.ConditionFalse, reason, message)
//	return true
//}
//
//// HandleImageNotReady checks if the resolved image is not yet ready and updates status.
//// Returns true if the service should wait for the image to become ready.
//func HandleImageNotReady(
//	status *aimv1alpha1.AIMServiceStatus,
//	obs *aimservicetemplate2.ServiceObservation,
//	setCondition func(conditionType string, conditionStatus metav1.ConditionStatus, reason, message string),
//) bool {
//	if obs == nil || obs.ImageReady || obs.ImageReadyReason == "" {
//		return false
//	}
//
//	message := obs.ImageReadyMessage
//	if message == "" {
//		message = "Image is not ready"
//	}
//
//	// Set status based on the reason
//	// ModelFailed is a terminal error (e.g., image not found 404) - cascade to Failed
//	// ModelDegraded is a recoverable error (e.g., auth issues) - cascade to Degraded
//	// Other reasons (e.g., model pending) - set to Pending
//	switch obs.ImageReadyReason {
//	case "ModelFailed":
//		status.Status = aimv1alpha1.AIMServiceStatusFailed
//		setCondition(aimv1alpha1.AIMServiceConditionFailure, metav1.ConditionTrue, obs.ImageReadyReason, message)
//	case "ModelDegraded":
//		status.Status = aimv1alpha1.AIMServiceStatusDegraded
//		setCondition(aimv1alpha1.AIMServiceConditionFailure, metav1.ConditionTrue, obs.ImageReadyReason, message)
//	default:
//		status.Status = aimv1alpha1.AIMServiceStatusPending
//	}
//
//	setCondition(aimv1alpha1.AIMServiceConditionResolved, metav1.ConditionFalse, obs.ImageReadyReason, message)
//	setCondition(aimv1alpha1.AIMServiceConditionRuntimeReady, metav1.ConditionFalse, obs.ImageReadyReason, "Waiting for image readiness")
//	setCondition(aimv1alpha1.AIMServiceConditionProgressing, metav1.ConditionTrue, obs.ImageReadyReason, "Awaiting image readiness")
//	setCondition(aimv1alpha1.AIMServiceConditionReady, metav1.ConditionFalse, obs.ImageReadyReason, message)
//	return true
//}
//
//// HandlePathTemplateError checks for path template errors and updates status.
//// Returns true if there is a path template error.
//// This can occur when routing is enabled (via service spec or runtime config) but the path template is invalid.
//func HandlePathTemplateError(
//	status *aimv1alpha1.AIMServiceStatus,
//	service *aimv1alpha1.AIMService,
//	obs *aimservicetemplate2.ServiceObservation,
//	setCondition func(conditionType string, conditionStatus metav1.ConditionStatus, reason, message string),
//) bool {
//	if obs == nil || obs.PathTemplateErr == nil {
//		return false
//	}
//
//	// Check if routing is enabled (via service spec or runtime config)
//	var runtimeRouting *aimv1alpha1.AIMRuntimeRoutingConfig
//	if obs != nil {
//		runtimeRouting = obs.RuntimeConfigSpec.Routing
//	}
//	resolved := routingconfig.Resolve(service, runtimeRouting)
//	if !resolved.Enabled {
//		// Path template error doesn't matter if routing is disabled
//		return false
//	}
//
//	status.Status = aimv1alpha1.AIMServiceStatusDegraded
//	message := obs.PathTemplateErr.Error()
//	reason := aimv1alpha1.AIMServiceReasonPathTemplateInvalid
//	setCondition(aimv1alpha1.AIMServiceConditionFailure, metav1.ConditionTrue, reason, message)
//	setCondition(aimv1alpha1.AIMServiceConditionRuntimeReady, metav1.ConditionFalse, reason, "Cannot configure HTTP routing")
//	setCondition(aimv1alpha1.AIMServiceConditionProgressing, metav1.ConditionFalse, reason, "Path template is invalid")
//	setCondition(aimv1alpha1.AIMServiceConditionReady, metav1.ConditionFalse, reason, message)
//	return true
//}
//
//// HandleTemplateDegraded checks if the template is degraded, not available, or failed and updates status.
//// Returns true if the template is degraded, not available, or failed.
//func HandleTemplateDegraded(
//	status *aimv1alpha1.AIMServiceStatus,
//	obs *aimservicetemplate2.ServiceObservation,
//	setCondition func(conditionType string, conditionStatus metav1.ConditionStatus, reason, message string),
//) bool {
//	if obs.TemplateStatus == nil {
//		return false
//	}
//
//	// Handle Degraded, NotAvailable, and Failed template statuses
//	if obs.TemplateStatus.Status != aimv1alpha1.AIMTemplateStatusDegraded &&
//		obs.TemplateStatus.Status != aimv1alpha1.AIMTemplateStatusNotAvailable &&
//		obs.TemplateStatus.Status != aimv1alpha1.AIMTemplateStatusFailed {
//		return false
//	}
//
//	// Use Failed for terminal failures, Degraded for recoverable issues (including NotAvailable)
//	if obs.TemplateStatus.Status == aimv1alpha1.AIMTemplateStatusFailed {
//		status.Status = aimv1alpha1.AIMServiceStatusFailed
//	} else {
//		status.Status = aimv1alpha1.AIMServiceStatusDegraded
//	}
//
//	templateReason := "TemplateDegraded"
//	templateMessage := "Template is not available"
//
//	// Extract failure details from template conditions
//	for _, cond := range obs.TemplateStatus.Conditions {
//		if cond.Type == "Failure" && cond.Status == metav1.ConditionTrue {
//			if cond.Message != "" {
//				templateMessage = cond.Message
//			}
//			if cond.Reason != "" {
//				templateReason = cond.Reason
//			}
//			break
//		}
//	}
//
//	setCondition(aimv1alpha1.AIMServiceConditionFailure, metav1.ConditionTrue, templateReason, templateMessage)
//	setCondition(aimv1alpha1.AIMServiceConditionRuntimeReady, metav1.ConditionFalse, templateReason,
//		"Cannot create InferenceService due to template issues")
//	setCondition(aimv1alpha1.AIMServiceConditionProgressing, metav1.ConditionFalse, templateReason,
//		"Service cannot proceed due to template issues")
//	setCondition(aimv1alpha1.AIMServiceConditionReady, metav1.ConditionFalse, templateReason,
//		"Service cannot be ready due to template issues")
//	return true
//}
//
//// HandleTemplateNotAvailable checks if the template is not available and updates status.
//// Returns true if the template is not yet available (Pending or Progressing).
//// Sets the service to Pending state because it's waiting for a dependency (the template).
//func HandleTemplateNotAvailable(
//	status *aimv1alpha1.AIMServiceStatus,
//	obs *aimservicetemplate2.ServiceObservation,
//	setCondition func(conditionType string, conditionStatus metav1.ConditionStatus, reason, message string),
//) bool {
//	if obs.TemplateAvailable {
//		return false
//	}
//
//	// Service is Pending because it's waiting for the template to become available.
//	// The template itself may be Progressing (running discovery) or Pending.
//	status.Status = aimv1alpha1.AIMServiceStatusPending
//
//	reason := "TemplateNotAvailable"
//	setCondition(aimv1alpha1.AIMServiceConditionRuntimeReady, metav1.ConditionFalse, reason,
//		fmt.Sprintf("Template %q is not yet Available", obs.TemplateName))
//	setCondition(aimv1alpha1.AIMServiceConditionProgressing, metav1.ConditionTrue, reason,
//		"Waiting for template discovery to complete")
//	setCondition(aimv1alpha1.AIMServiceConditionReady, metav1.ConditionFalse, reason,
//		"Template is not available")
//	return true
//}
//
//// HandleTemplateSelectionFailure reports failures during automatic template selection.
//func HandleTemplateSelectionFailure(
//	status *aimv1alpha1.AIMServiceStatus,
//	obs *aimservicetemplate2.ServiceObservation,
//	setCondition func(conditionType string, conditionStatus metav1.ConditionStatus, reason, message string),
//) bool {
//	if obs == nil || obs.TemplateSelectionReason == "" {
//		return false
//	}
//
//	message := obs.TemplateSelectionMessage
//	if message == "" {
//		message = "Template selection failed"
//	}
//
//	status.Status = aimv1alpha1.AIMServiceStatusFailed
//	reason := obs.TemplateSelectionReason
//	setCondition(aimv1alpha1.AIMServiceConditionFailure, metav1.ConditionTrue, reason, message)
//	setCondition(aimv1alpha1.AIMServiceConditionResolved, metav1.ConditionFalse, reason, message)
//	setCondition(aimv1alpha1.AIMServiceConditionRuntimeReady, metav1.ConditionFalse, reason, "Cannot proceed without a unique template")
//	setCondition(aimv1alpha1.AIMServiceConditionProgressing, metav1.ConditionFalse, reason, "Template selection failed")
//	setCondition(aimv1alpha1.AIMServiceConditionReady, metav1.ConditionFalse, reason, message)
//	return true
//}
//
//// HandleMissingModelSource checks if the template is available but has no model sources.
//// Returns true if model sources are missing (discovery succeeded but produced no usable sources).
//func HandleMissingModelSource(
//	status *aimv1alpha1.AIMServiceStatus,
//	obs *aimservicetemplate2.ServiceObservation,
//	setCondition func(conditionType string, conditionStatus metav1.ConditionStatus, reason, message string),
//) bool {
//	if !obs.TemplateAvailable || obs.TemplateStatus == nil {
//		return false
//	}
//
//	// Check if template is Available but has no model sources
//	hasModelSources := len(obs.TemplateStatus.ModelSources) > 0
//	if hasModelSources {
//		return false
//	}
//
//	status.Status = aimv1alpha1.AIMServiceStatusDegraded
//	reason := "NoModelSources"
//	message := fmt.Sprintf("Template %q is Available but discovery produced no usable model sources", obs.TemplateName)
//
//	setCondition(aimv1alpha1.AIMServiceConditionFailure, metav1.ConditionTrue, reason, message)
//	setCondition(aimv1alpha1.AIMServiceConditionRuntimeReady, metav1.ConditionFalse, reason,
//		"Cannot create InferenceService without model sources")
//	setCondition(aimv1alpha1.AIMServiceConditionProgressing, metav1.ConditionFalse, reason,
//		"Service is degraded due to missing model sources")
//	setCondition(aimv1alpha1.AIMServiceConditionReady, metav1.ConditionFalse, reason,
//		"Service cannot be ready without model sources")
//	return true
//}
//
//// HandleInferenceServicePodImageError checks for image pull errors in InferenceService pods.
//// Returns true if an image pull error was detected.
//func HandleInferenceServicePodImageError(
//	status *aimv1alpha1.AIMServiceStatus,
//	obs *aimservicetemplate2.ServiceObservation,
//	setCondition func(conditionType string, conditionStatus metav1.ConditionStatus, reason, message string),
//) bool {
//	if obs == nil || obs.InferenceServicePodImageError == nil {
//		return false
//	}
//
//	pullErr := obs.InferenceServicePodImageError
//
//	// Determine the condition reason based on error type
//	var conditionReason string
//	switch pullErr.Type {
//	case aimmodel2.ImagePullErrorAuth:
//		conditionReason = aimv1alpha1.AIMServiceReasonImagePullAuthFailure
//	case aimmodel2.ImagePullErrorNotFound:
//		conditionReason = aimv1alpha1.AIMServiceReasonImageNotFound
//	default:
//		conditionReason = aimv1alpha1.AIMServiceReasonImagePullBackOff
//	}
//
//	// Format detailed message
//	containerType := "Container"
//	if pullErr.IsInitContainer {
//		containerType = "Init container"
//	}
//	detailedMessage := fmt.Sprintf("InferenceService pod %s %q is stuck in %s: %s",
//		containerType, pullErr.Container, pullErr.Reason, pullErr.Message)
//
//	status.Status = aimv1alpha1.AIMServiceStatusDegraded
//	setCondition(aimv1alpha1.AIMServiceConditionFailure, metav1.ConditionTrue, conditionReason, detailedMessage)
//	setCondition(aimv1alpha1.AIMServiceConditionRuntimeReady, metav1.ConditionFalse, conditionReason,
//		"InferenceService cannot run due to image pull failure")
//	setCondition(aimv1alpha1.AIMServiceConditionProgressing, metav1.ConditionFalse, conditionReason,
//		"Service is degraded due to image pull failure")
//	setCondition(aimv1alpha1.AIMServiceConditionReady, metav1.ConditionFalse, conditionReason,
//		"Service cannot be ready due to image pull failure")
//	return true
//}
//
//// EvaluateInferenceServiceStatus checks InferenceService and routing readiness.
//// Updates status conditions based on the InferenceService and routing state.
//func EvaluateInferenceServiceStatus(
//	status *aimv1alpha1.AIMServiceStatus,
//	obs *aimservicetemplate2.ServiceObservation,
//	inferenceService *servingv1beta1.InferenceService,
//	httpRoute *gatewayapiv1.HTTPRoute,
//	routingEnabled bool,
//	routingReady bool,
//	setCondition func(conditionType string, conditionStatus metav1.ConditionStatus, reason, message string),
//) {
//	if inferenceService == nil {
//		if status.Status != aimv1alpha1.AIMServiceStatusFailed {
//			status.Status = aimv1alpha1.AIMServiceStatusStarting
//		}
//		setCondition(aimv1alpha1.AIMServiceConditionRuntimeReady, metav1.ConditionFalse, aimv1alpha1.AIMServiceReasonCreatingRuntime,
//			"Waiting for InferenceService creation")
//		setCondition(aimv1alpha1.AIMServiceConditionProgressing, metav1.ConditionTrue, aimv1alpha1.AIMServiceReasonCreatingRuntime,
//			"Reconciling InferenceService resources")
//		setCondition(aimv1alpha1.AIMServiceConditionReady, metav1.ConditionFalse, aimv1alpha1.AIMServiceReasonCreatingRuntime,
//			"InferenceService not yet created")
//		return
//	}
//
//	if inferenceService.Status.IsReady() && routingReady {
//		status.Status = aimv1alpha1.AIMServiceStatusRunning
//		setCondition(aimv1alpha1.AIMServiceConditionRuntimeReady, metav1.ConditionTrue, aimv1alpha1.AIMServiceReasonRuntimeReady,
//			"InferenceService is ready")
//		setCondition(aimv1alpha1.AIMServiceConditionProgressing, metav1.ConditionFalse, aimv1alpha1.AIMServiceReasonRuntimeReady,
//			"Service is running")
//		setCondition(aimv1alpha1.AIMServiceConditionReady, metav1.ConditionTrue, aimv1alpha1.AIMServiceReasonRuntimeReady,
//			"AIMService is ready to serve traffic")
//		return
//	}
//
//	if status.Status != aimv1alpha1.AIMServiceStatusFailed && status.Status != aimv1alpha1.AIMServiceStatusDegraded {
//		status.Status = aimv1alpha1.AIMServiceStatusStarting
//	}
//	reason := aimv1alpha1.AIMServiceReasonCreatingRuntime
//	message := "Waiting for InferenceService to become ready"
//	if inferenceService.Status.ModelStatus.LastFailureInfo != nil {
//		reason = aimv1alpha1.AIMServiceReasonRuntimeFailed
//		message = inferenceService.Status.ModelStatus.LastFailureInfo.Message
//		status.Status = aimv1alpha1.AIMServiceStatusDegraded
//		setCondition(aimv1alpha1.AIMServiceConditionFailure, metav1.ConditionTrue, reason, message)
//	}
//	if routingEnabled && !routingReady && reason == aimv1alpha1.AIMServiceReasonCreatingRuntime {
//		reason = aimv1alpha1.AIMServiceReasonConfiguringRoute
//		message = "Waiting for HTTPRoute to become ready"
//	}
//
//	setCondition(aimv1alpha1.AIMServiceConditionRuntimeReady, metav1.ConditionFalse, reason, message)
//	setCondition(aimv1alpha1.AIMServiceConditionProgressing, metav1.ConditionTrue, reason, "InferenceService reconciliation in progress")
//	setCondition(aimv1alpha1.AIMServiceConditionReady, metav1.ConditionFalse, reason, message)
//}
//
//func convertTemplateScope(scope types.TemplateScope) aimv1alpha1.AIMResolutionScope {
//	switch scope {
//	case types.TemplateScopeNamespace:
//		return aimv1alpha1.AIMResolutionScopeNamespace
//	case types.TemplateScopeCluster:
//		return aimv1alpha1.AIMResolutionScopeCluster
//	default:
//		return aimv1alpha1.AIMResolutionScopeUnknown
//	}
//}
//
//// initializeStatusReferences resets and populates resolved references in status.
//func initializeStatusReferences(status *aimv1alpha1.AIMServiceStatus, obs *aimservicetemplate2.ServiceObservation) {
//	status.ResolvedRuntimeConfig = nil
//	status.ResolvedModel = nil
//	status.Routing = nil
//	status.ResolvedTemplateCache = nil
//
//	if obs != nil && obs.ResolvedRuntimeConfig != nil {
//		status.ResolvedRuntimeConfig = obs.ResolvedRuntimeConfig
//	}
//	if obs != nil && obs.ResolvedImage != nil {
//		status.ResolvedModel = obs.ResolvedImage
//	}
//	if obs != nil && obs.TemplateCache != nil {
//		status.ResolvedTemplateCache = &aimv1alpha1.AIMResolvedReference{
//			Name:      obs.TemplateCache.Name,
//			Namespace: obs.TemplateCache.Namespace,
//			Kind:      "AIMTemplateCache",
//			Scope: func() aimv1alpha1.AIMResolutionScope {
//				if obs.TemplateCache.Namespace != "" {
//					return aimv1alpha1.AIMResolutionScopeNamespace
//				}
//				return aimv1alpha1.AIMResolutionScopeCluster
//			}(),
//			UID: obs.TemplateCache.UID,
//		}
//	}
//}
//
//// setupCacheCondition sets the cache condition based on whether caching is requested.
//func setupCacheCondition(
//	service *aimv1alpha1.AIMService,
//	setCondition func(conditionType string, conditionStatus metav1.ConditionStatus, reason, message string),
//) {
//	if !service.Spec.CacheModel {
//		setCondition(aimv1alpha1.AIMServiceConditionCacheReady, metav1.ConditionTrue, aimv1alpha1.AIMServiceReasonCacheWarm, "Caching not requested")
//	} else {
//		setCondition(aimv1alpha1.AIMServiceConditionCacheReady, metav1.ConditionFalse, aimv1alpha1.AIMServiceReasonWaitingForCache, "Waiting for cache warm-up")
//	}
//}
//
//// setupResolvedTemplate populates the resolved template reference in status.
//func setupResolvedTemplate(obs *aimservicetemplate2.ServiceObservation, status *aimv1alpha1.AIMServiceStatus) {
//	status.ResolvedTemplate = nil
//	if obs != nil && obs.TemplateName != "" {
//		status.ResolvedTemplate = &aimv1alpha1.AIMResolvedReference{
//			Name:      obs.TemplateName,
//			Namespace: obs.TemplateNamespace,
//			Scope:     convertTemplateScope(obs.Scope),
//			Kind:      "AIMServiceTemplate",
//		}
//	}
//	// Don't set resolvedTemplate if no template was actually resolved
//}
//
//// evaluateHTTPRouteReadiness checks HTTP route status and updates routing conditions.
//// Returns the updated routingReady flag.
//func evaluateHTTPRouteReadiness(
//	httpRoute *gatewayapiv1.HTTPRoute,
//	status *aimv1alpha1.AIMServiceStatus,
//	setCondition func(conditionType string, conditionStatus metav1.ConditionStatus, reason, message string),
//) bool {
//	if httpRoute == nil {
//		setCondition(aimv1alpha1.AIMServiceConditionRoutingReady, metav1.ConditionFalse, aimv1alpha1.AIMServiceReasonConfiguringRoute,
//			"Waiting for HTTPRoute to be created")
//		return false
//	}
//
//	ready, reason, message := EvaluateHTTPRouteStatus(httpRoute)
//	conditionStatus := metav1.ConditionFalse
//	if ready {
//		conditionStatus = metav1.ConditionTrue
//	}
//	setCondition(aimv1alpha1.AIMServiceConditionRoutingReady, conditionStatus, reason, message)
//	if !ready && reason == aimv1alpha1.AIMServiceReasonRouteFailed {
//		status.Status = aimv1alpha1.AIMServiceStatusDegraded
//		setCondition(aimv1alpha1.AIMServiceConditionFailure, metav1.ConditionTrue, reason, message)
//	}
//	return ready
//}
//
//// handleTemplateNotFound handles the case when no template is found.
//// Returns true if this handler applies.
//func handleTemplateNotFound(
//	obs *aimservicetemplate2.ServiceObservation,
//	status *aimv1alpha1.AIMServiceStatus,
//	setCondition func(conditionType string, conditionStatus metav1.ConditionStatus, reason, message string),
//) bool {
//	if obs != nil && obs.TemplateFound() {
//		return false
//	}
//
//	var message string
//	if obs != nil && obs.ShouldCreateTemplate {
//		status.Status = aimv1alpha1.AIMServiceStatusPending
//		message = "Template not found; creating derived template"
//		setCondition(aimv1alpha1.AIMServiceConditionResolved, metav1.ConditionFalse, aimv1alpha1.AIMServiceReasonTemplateNotFound, message)
//		setCondition(aimv1alpha1.AIMServiceConditionRuntimeReady, metav1.ConditionFalse, aimv1alpha1.AIMServiceReasonCreatingRuntime, "Waiting for template creation")
//		setCondition(aimv1alpha1.AIMServiceConditionProgressing, metav1.ConditionTrue, aimv1alpha1.AIMServiceReasonTemplateNotFound, "Waiting for template to be created")
//	} else if obs != nil && obs.TemplatesExistButNotReady {
//		// Templates exist but aren't Available yet - service should wait
//		status.Status = aimv1alpha1.AIMServiceStatusPending
//		message = "Waiting for templates to become Available"
//		setCondition(aimv1alpha1.AIMServiceConditionResolved, metav1.ConditionFalse, "TemplateNotAvailable", message)
//		setCondition(aimv1alpha1.AIMServiceConditionRuntimeReady, metav1.ConditionFalse, "TemplateNotAvailable", "Waiting for template discovery to complete")
//		setCondition(aimv1alpha1.AIMServiceConditionProgressing, metav1.ConditionTrue, "TemplateNotAvailable", "Templates exist but are not yet Available")
//	} else {
//		// No template could be resolved and no derived template will be created.
//		// This is a degraded state - the service cannot proceed.
//		status.Status = aimv1alpha1.AIMServiceStatusDegraded
//		if obs != nil {
//			switch {
//			case obs.TemplateSelectionMessage != "":
//				message = obs.TemplateSelectionMessage
//			case obs.BaseTemplateName == "":
//				message = "No template reference specified and no templates are available for the selected image. Provide spec.templateRef or create templates for the image."
//			default:
//				message = fmt.Sprintf("Template %q not found. Create the template or verify the template name.", obs.BaseTemplateName)
//			}
//		} else {
//			message = "Template not found"
//		}
//		setCondition(aimv1alpha1.AIMServiceConditionFailure, metav1.ConditionTrue, aimv1alpha1.AIMServiceReasonTemplateNotFound, message)
//		setCondition(aimv1alpha1.AIMServiceConditionResolved, metav1.ConditionFalse, aimv1alpha1.AIMServiceReasonTemplateNotFound, message)
//		setCondition(aimv1alpha1.AIMServiceConditionRuntimeReady, metav1.ConditionFalse, aimv1alpha1.AIMServiceReasonTemplateNotFound, "Referenced template does not exist")
//		setCondition(aimv1alpha1.AIMServiceConditionProgressing, metav1.ConditionFalse, aimv1alpha1.AIMServiceReasonTemplateNotFound, "Cannot proceed without template")
//	}
//	setCondition(aimv1alpha1.AIMServiceConditionReady, metav1.ConditionFalse, aimv1alpha1.AIMServiceReasonTemplateNotFound, "Template missing")
//	return true
//}
//
//// ProjectServiceStatus computes and updates the service status based on observations and errors.
//// This is a high-level orchestrator that calls the individual status handler functions.
//func ProjectServiceStatus(
//	service *aimv1alpha1.AIMService,
//	obs *aimservicetemplate2.ServiceObservation,
//	inferenceService *servingv1beta1.InferenceService,
//	httpRoute *gatewayapiv1.HTTPRoute,
//	errs controllerutils.ReconcileErrors,
//) {
//	status := &service.Status
//	initializeStatusReferences(status, obs)
//
//	// Helper to update status conditions.
//	setCondition := func(conditionType string, conditionStatus metav1.ConditionStatus, reason, message string) {
//		cond := metav1.Condition{
//			Type:               conditionType,
//			Status:             conditionStatus,
//			Reason:             reason,
//			Message:            message,
//			ObservedGeneration: service.Generation,
//			LastTransitionTime: metav1.Now(),
//		}
//		meta.SetStatusCondition(&status.Conditions, cond)
//	}
//
//	setupCacheCondition(service, setCondition)
//	setupResolvedTemplate(obs, status)
//
//	routingEnabled, routingReady, routingHasFatalError := EvaluateRoutingStatus(service, obs, status, setCondition)
//
//	// Check routing readiness if enabled (but skip if we already have a fatal routing error)
//	if routingEnabled && !routingHasFatalError {
//		routingReady = evaluateHTTPRouteReadiness(httpRoute, status, setCondition)
//	}
//
//	if HandleReconcileErrors(status, setCondition, errs) {
//		return
//	}
//
//	// Clear failure condition when reconciliation succeeds and there are no routing errors.
//	if !routingEnabled || (routingReady && !routingHasFatalError) {
//		setCondition(aimv1alpha1.AIMServiceConditionFailure, metav1.ConditionFalse, aimv1alpha1.AIMServiceReasonResolved, "No active failures")
//	}
//
//	if HandleModelResolutionFailure(status, obs, setCondition) {
//		return
//	}
//
//	if HandleImageMissing(status, obs, setCondition) {
//		return
//	}
//
//	if HandleImageNotReady(status, obs, setCondition) {
//		return
//	}
//
//	if HandleTemplateSelectionFailure(status, obs, setCondition) {
//		return
//	}
//
//	if handleTemplateNotFound(obs, status, setCondition) {
//		return
//	}
//
//	setCondition(aimv1alpha1.AIMServiceConditionResolved, metav1.ConditionTrue, aimv1alpha1.AIMServiceReasonResolved,
//		fmt.Sprintf("Resolved template %q", obs.TemplateName))
//
//	if HandleRuntimeConfigMissing(status, obs, setCondition) {
//		return
//	}
//
//	if HandlePathTemplateError(status, service, obs, setCondition) {
//		return
//	}
//
//	if HandleTemplateDegraded(status, obs, setCondition) {
//		return
//	}
//
//	if HandleTemplateNotAvailable(status, obs, setCondition) {
//		return
//	}
//
//	if HandleMissingModelSource(status, obs, setCondition) {
//		return
//	}
//
//	// Check for image pull errors in InferenceService pods
//	if HandleInferenceServicePodImageError(status, obs, setCondition) {
//		return
//	}
//
//	if service.Spec.CacheModel {
//		if obs.TemplateCache != nil && obs.TemplateCache.Status.Status == aimv1alpha1.AIMTemplateCacheStatusAvailable {
//			setCondition(aimv1alpha1.AIMServiceConditionCacheReady, metav1.ConditionTrue, aimv1alpha1.AIMServiceReasonCacheWarm, "Template cache is warm")
//		} else {
//			setCondition(aimv1alpha1.AIMServiceConditionCacheReady, metav1.ConditionFalse, aimv1alpha1.AIMServiceReasonCacheWarm, "Template caching is enabled")
//		}
//	}
//
//	EvaluateInferenceServiceStatus(status, obs, inferenceService, httpRoute, routingEnabled, routingReady, setCondition)
//}
