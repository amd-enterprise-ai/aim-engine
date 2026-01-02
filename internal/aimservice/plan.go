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

package aimservice

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

const (
	// DefaultPVCHeadroomPercent is the default headroom percentage for PVC sizing
	DefaultPVCHeadroomPercent = 10
)

// planDerivedTemplate creates a derived template if the service has overrides.
func planDerivedTemplate(
	service *aimv1alpha1.AIMService,
	templateName string,
	templateSpec *aimv1alpha1.AIMServiceTemplateSpec,
	obs ServiceObservation,
) client.Object {
	// Only create derived template if service has overrides
	if service.Spec.Overrides == nil {
		return nil
	}

	// Check if we already have the derived template
	if obs.template.Value != nil {
		// Template already exists, check if it's our derived template
		if hasLabel(obs.template.Value.Labels, constants.LabelDerivedTemplate, "true") {
			return nil // Already exists
		}
	}

	// Get model name for the derived template
	modelName := ""
	if obs.model.Value != nil {
		modelName = obs.model.Value.Name
	} else if obs.clusterModel.Value != nil {
		modelName = obs.clusterModel.Value.Name
	}

	// Calculate the derived template name
	suffix := overridesSuffix(service.Spec.Overrides)
	derivedName := derivedTemplateName(templateName, suffix)

	return buildDerivedTemplate(service, derivedName, modelName, templateSpec)
}

// planTemplateCache creates a template cache if caching is enabled.
func planTemplateCache(
	service *aimv1alpha1.AIMService,
	templateName string,
	templateStatus *aimv1alpha1.AIMServiceTemplateStatus,
	obs ServiceObservation,
) client.Object {
	cachingMode := service.Spec.GetCachingMode()

	// Only create cache for Always or Auto mode
	if cachingMode == aimv1alpha1.CachingModeNever {
		return nil
	}

	// Don't create if template cache already exists
	if obs.templateCache.Value != nil {
		return nil
	}

	// Need model sources in template status to determine what to cache
	if templateStatus == nil || len(templateStatus.ModelSources) == 0 {
		return nil
	}

	// Resolve storage class
	storageClassName := resolveStorageClassName(service, obs)

	cacheName, err := GenerateTemplateCacheName(templateName, service.Namespace)
	if err != nil {
		return nil
	}

	serviceLabelValue, _ := utils.SanitizeLabelValue(service.Name)

	cache := &aimv1alpha1.AIMTemplateCache{
		TypeMeta: metav1.TypeMeta{
			APIVersion: aimv1alpha1.GroupVersion.String(),
			Kind:       "AIMTemplateCache",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      cacheName,
			Namespace: service.Namespace,
			Labels: map[string]string{
				constants.LabelService: serviceLabelValue,
			},
		},
		Spec: aimv1alpha1.AIMTemplateCacheSpec{
			TemplateName:     templateName,
			TemplateScope:    aimv1alpha1.AIMServiceTemplateScopeNamespace, // Default to namespace scope
			StorageClassName: storageClassName,
			RuntimeConfigRef: service.Spec.RuntimeConfigRef,
		},
	}

	return cache
}

// planServicePVC creates a PVC for the service if no template cache is available.
func planServicePVC(
	service *aimv1alpha1.AIMService,
	templateName string,
	templateStatus *aimv1alpha1.AIMServiceTemplateStatus,
	obs ServiceObservation,
) client.Object {
	cachingMode := service.Spec.GetCachingMode()

	// If caching is required (Always mode), don't create a temp PVC
	if cachingMode == aimv1alpha1.CachingModeAlways {
		return nil
	}

	// If template cache exists and is ready, don't need PVC
	if obs.templateCache.Value != nil &&
		obs.templateCache.Value.Status.Status == constants.AIMStatusReady {
		return nil
	}

	// If PVC already exists, don't create again
	if obs.pvc.Value != nil {
		return nil
	}

	// Need model sources to calculate size
	if templateStatus == nil || len(templateStatus.ModelSources) == 0 {
		return nil
	}

	// Calculate required size
	headroomPercent := resolvePVCHeadroomPercent(service, obs)
	size, err := calculateRequiredStorageSize(templateStatus.ModelSources, headroomPercent)
	if err != nil {
		return nil
	}

	pvcName, err := GenerateServicePVCName(service.Name, service.Namespace)
	if err != nil {
		return nil
	}

	storageClassName := resolveStorageClassName(service, obs)
	var sc *string
	if storageClassName != "" {
		sc = &storageClassName
	}

	serviceLabelValue, _ := utils.SanitizeLabelValue(service.Name)
	templateLabelValue, _ := utils.SanitizeLabelValue(templateName)

	pvc := &corev1.PersistentVolumeClaim{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "PersistentVolumeClaim",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Namespace: service.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": constants.LabelValueManagedBy,
				"app.kubernetes.io/component":  "model-storage",
				constants.LabelService:         serviceLabelValue,
				constants.LabelCacheType:       constants.LabelValueCacheTypeTemp,
				constants.LabelTemplate:        templateLabelValue,
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         service.APIVersion,
					Kind:               service.Kind,
					Name:               service.Name,
					UID:                service.UID,
					Controller:         ptr.To(true),
					BlockOwnerDeletion: ptr.To(true),
				},
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: size,
				},
			},
			StorageClassName: sc,
		},
	}

	return pvc
}

// planInferenceService creates the KServe InferenceService.
func planInferenceService(
	ctx context.Context,
	service *aimv1alpha1.AIMService,
	templateName string,
	templateSpec *aimv1alpha1.AIMServiceTemplateSpec,
	templateStatus *aimv1alpha1.AIMServiceTemplateStatus,
	obs ServiceObservation,
) client.Object {
	logger := log.FromContext(ctx)

	// Check if we're ready to create the InferenceService
	if !isReadyForInferenceService(service, obs) {
		logger.V(1).Info("not ready to create InferenceService")
		return nil
	}

	// Build the InferenceService
	return buildInferenceService(service, templateName, templateSpec, templateStatus, obs)
}

// planHTTPRoute creates the HTTPRoute if routing is enabled.
func planHTTPRoute(
	service *aimv1alpha1.AIMService,
	obs ServiceObservation,
) client.Object {
	runtimeConfig := obs.mergedRuntimeConfig.Value
	if !isRoutingEnabled(service, runtimeConfig) {
		return nil
	}

	// Need gateway ref to create route
	gatewayRef := resolveGatewayRef(service, runtimeConfig)
	if gatewayRef == nil {
		return nil
	}

	return buildHTTPRoute(service, gatewayRef, runtimeConfig)
}

// isReadyForInferenceService checks if all prerequisites are met to create the InferenceService.
func isReadyForInferenceService(service *aimv1alpha1.AIMService, obs ServiceObservation) bool {
	cachingMode := service.Spec.GetCachingMode()

	// Check model is ready
	modelReady := false
	if obs.model.Value != nil {
		modelReady = obs.model.Value.Status.Status == constants.AIMStatusReady
	} else if obs.clusterModel.Value != nil {
		modelReady = obs.clusterModel.Value.Status.Status == constants.AIMStatusReady
	}
	if !modelReady {
		return false
	}

	// Check if we have storage
	switch cachingMode {
	case aimv1alpha1.CachingModeAlways:
		// Require template cache to be ready
		if obs.templateCache.Value == nil ||
			obs.templateCache.Value.Status.Status != constants.AIMStatusReady {
			return false
		}
	case aimv1alpha1.CachingModeAuto:
		// Either cache or PVC is fine
		hasCache := obs.templateCache.Value != nil &&
			obs.templateCache.Value.Status.Status == constants.AIMStatusReady
		hasPVC := obs.pvc.Value != nil
		if !hasCache && !hasPVC {
			return false
		}
	case aimv1alpha1.CachingModeNever:
		// No cache required, but need PVC for download
		if obs.pvc.Value == nil {
			return false
		}
	}

	return true
}

// resolveStorageClassName determines the storage class to use.
func resolveStorageClassName(service *aimv1alpha1.AIMService, obs ServiceObservation) string {
	// Service-level storage config takes precedence
	if service.Spec.Storage != nil && service.Spec.Storage.DefaultStorageClassName != nil {
		return *service.Spec.Storage.DefaultStorageClassName
	}

	// Fall back to runtime config
	if obs.mergedRuntimeConfig.Value != nil && obs.mergedRuntimeConfig.Value.Storage != nil {
		if obs.mergedRuntimeConfig.Value.Storage.DefaultStorageClassName != nil {
			return *obs.mergedRuntimeConfig.Value.Storage.DefaultStorageClassName
		}
	}

	return ""
}

// resolvePVCHeadroomPercent determines the PVC headroom percentage.
func resolvePVCHeadroomPercent(service *aimv1alpha1.AIMService, obs ServiceObservation) int32 {
	// Service-level storage config takes precedence
	if service.Spec.Storage != nil && service.Spec.Storage.PVCHeadroomPercent != nil {
		return *service.Spec.Storage.PVCHeadroomPercent
	}

	// Fall back to runtime config
	if obs.mergedRuntimeConfig.Value != nil && obs.mergedRuntimeConfig.Value.Storage != nil {
		if obs.mergedRuntimeConfig.Value.Storage.PVCHeadroomPercent != nil {
			return *obs.mergedRuntimeConfig.Value.Storage.PVCHeadroomPercent
		}
	}

	return DefaultPVCHeadroomPercent
}

// calculateRequiredStorageSize computes total storage needed for model sources.
func calculateRequiredStorageSize(modelSources []aimv1alpha1.AIMModelSource, headroomPercent int32) (resource.Quantity, error) {
	if len(modelSources) == 0 {
		return resource.Quantity{}, fmt.Errorf("no model sources available")
	}

	var totalBytes int64
	for _, source := range modelSources {
		if source.Size.IsZero() {
			return resource.Quantity{}, fmt.Errorf("model source %q has no size specified", source.Name)
		}
		totalBytes += source.Size.Value()
	}

	if totalBytes == 0 {
		return resource.Quantity{}, fmt.Errorf("total model size is zero")
	}

	// Apply headroom and round to nearest Gi
	return quantityWithHeadroom(totalBytes, headroomPercent), nil
}

// quantityWithHeadroom adds headroom percentage and rounds to nearest Gi.
func quantityWithHeadroom(bytes int64, headroomPercent int32) resource.Quantity {
	// Add headroom
	withHeadroom := float64(bytes) * (1.0 + float64(headroomPercent)/100.0)

	// Convert to Gi and round up
	gi := withHeadroom / (1024 * 1024 * 1024)
	roundedGi := int64(gi + 0.999) // Round up

	if roundedGi < 1 {
		roundedGi = 1
	}

	return resource.MustParse(fmt.Sprintf("%dGi", roundedGi))
}

// resolveGatewayRef gets the gateway reference from service or runtime config.
func resolveGatewayRef(service *aimv1alpha1.AIMService, runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon) *gatewayapiv1.ParentReference {
	// Service-level override
	if service.Spec.Routing != nil && service.Spec.Routing.GatewayRef != nil {
		return service.Spec.Routing.GatewayRef
	}

	// Fall back to runtime config
	if runtimeConfig != nil && runtimeConfig.Routing != nil && runtimeConfig.Routing.GatewayRef != nil {
		return runtimeConfig.Routing.GatewayRef
	}

	return nil
}

// hasLabel checks if the labels map has the specified key=value.
func hasLabel(labels map[string]string, key, value string) bool {
	if labels == nil {
		return false
	}
	return labels[key] == value
}
