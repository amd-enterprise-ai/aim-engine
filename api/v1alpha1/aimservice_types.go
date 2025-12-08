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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

const (
	// AIMServiceTemplateIndexKey is the field index key for AIMService template reference
	// Indexes by .spec.TemplateName or status.ResolvedTemplate.Name
	AIMServiceTemplateIndexKey = ".spec.templateRef"
)

// AIMCachingMode controls caching behavior for a service.
// +kubebuilder:validation:Enum=Auto;Always;Never
type AIMCachingMode string

const (
	// CachingModeAuto uses cache if it exists, but doesn't create one.
	// This is the default mode.
	CachingModeAuto AIMCachingMode = "Auto"

	// CachingModeAlways always uses cache, creating one if it doesn't exist.
	CachingModeAlways AIMCachingMode = "Always"

	// CachingModeNever never uses cache, even if one exists.
	CachingModeNever AIMCachingMode = "Never"
)

// AIMServiceCachingConfig controls caching behavior for a service.
type AIMServiceCachingConfig struct {
	// Mode controls when to use caching.
	// - Auto (default): Use cache if it exists, but don't create one
	// - Always: Always use cache, create if it doesn't exist
	// - Never: Don't use cache even if it exists
	// +kubebuilder:default=Auto
	// +optional
	Mode AIMCachingMode `json:"mode,omitempty"`
}

// AIMServiceModel specifies which model to deploy. Exactly one field must be set.
// +kubebuilder:validation:XValidation:rule="(has(self.ref) && !has(self.image) && !has(self.custom)) || (!has(self.ref) && has(self.image) && !has(self.custom)) || (!has(self.ref) && !has(self.image) && has(self.custom))",message="exactly one of ref, image, or custom must be specified"
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="model selection is immutable after creation"
type AIMServiceModel struct {
	// Ref references an existing AIMModel or AIMClusterModel by metadata.name.
	// The controller looks for a namespace-scoped AIMModel first, then falls back to cluster-scoped AIMClusterModel.
	// Example: `meta-llama-3-8b`.
	// +optional
	Ref *string `json:"ref,omitempty"`

	// Image specifies a container image URI directly.
	// The controller searches for an existing model with this image, or creates one if none exists.
	// The scope of the created model is controlled by the runtime config's ModelCreationScope field.
	// Example: `ghcr.io/silogen/llama-3-8b:v1.2.0`.
	// +optional
	Image *string `json:"image,omitempty"`

	// Custom specifies a custom model configuration with explicit base image,
	// model sources, and GPU requirements.
	// +optional
	Custom *AIMServiceModelCustom `json:"custom,omitempty"`
}

// AIMServiceModelCustom specifies a custom model configuration with explicit base image,
// model sources, and GPU requirements.
// +kubebuilder:validation:XValidation:rule="size(self.modelSources) >= 1",message="at least one model source must be specified"
// +kubebuilder:validation:XValidation:rule="has(self.gpuSelector) && self.gpuSelector.model != \"\" && self.gpuSelector.count > 0",message="gpuSelector must be fully specified with model and count"
type AIMServiceModelCustom struct {
	// BaseImage is the container image URI for the AIM base image.
	// This will be used as the image for the auto-created AIMModel.
	// Example: `ghcr.io/silogen/aim-base:0.7.0`.
	// +required
	BaseImage string `json:"baseImage"`

	// ModelSources specifies the model artifacts to use.
	// The controller will create a template with these sources inline,
	// and discovery will validate/enrich them with size information.
	// AIM runtime currently supports only one model source.
	// +required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=1
	ModelSources []AIMModelSource `json:"modelSources"`

	// GpuSelector specifies the GPU requirements for this custom model.
	// This is mandatory and cannot be overridden by service-level overrides.
	// +required
	GpuSelector AIMGpuSelector `json:"gpuSelector"`
}

// AIMServiceOverrides allows overriding template parameters at the service level.
// All fields are optional. When specified, they override the corresponding values
// from the referenced AIMServiceTemplate.
type AIMServiceOverrides struct {
	AIMRuntimeParameters `json:",inline"`
}

// AIMServiceSpec defines the desired state of AIMService.
//
// Binds a canonical model to an AIMServiceTemplate and configures replicas,
// caching behavior, and optional overrides. The template governs the base
// runtime selection knobs, while the overrides field allows service-specific
// customization.
type AIMServiceSpec struct {
	// Model specifies which model to deploy using one of the available reference methods.
	// Use `ref` to reference an existing AIMModel/AIMClusterModel by name, or use `image`
	// to specify a container image URI directly (which will auto-create a model if needed).
	Model AIMServiceModel `json:"model"`

	// TemplateName is the name of the AIMServiceTemplate or AIMClusterServiceTemplate to use.
	// The template selects the runtime profile and GPU parameters.
	TemplateName string `json:"templateName,omitempty"`

	// Caching controls caching behavior for this service.
	// When nil, defaults to Auto mode (use cache if available, don't create).
	// +optional
	Caching *AIMServiceCachingConfig `json:"caching,omitempty"`

	// DEPRECATED: Use Caching.Mode instead. This field will be removed in a future version.
	// For backward compatibility, if Caching is not set, this field is used.
	// Tri-state logic: nil=Auto, true=Always, false=Never
	// +optional
	// +kubebuilder:validation:Deprecated
	// +kubebuilder:validation:DeprecatedMessage="Use Caching.Mode instead. This field will be removed in a future version."
	CacheModel *bool `json:"cacheModel,omitempty"`

	// Replicas overrides the number of replicas for this service.
	// Other runtime settings remain governed by the template unless overridden.
	// +kubebuilder:default=1
	Replicas *int32 `json:"replicas,omitempty"`

	// RuntimeConfigName references the AIM runtime configuration (by name) to use for this service.
	// The controller looks for a namespace-scoped AIMRuntimeConfig first, then falls back to
	// cluster-scoped AIMClusterRuntimeConfig. This serves as the base configuration that can be
	// overridden by service-level storage and routing fields.
	// +kubebuilder:default=default
	RuntimeConfigName string `json:"runtimeConfigName,omitempty"`

	// Inline AIMServiceRuntimeConfig fields for cleaner access
	AIMServiceRuntimeConfig `json:",inline"`

	// Resources overrides the container resource requirements for this service.
	// When specified, these values take precedence over the template and image defaults.
	// +optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`

	// Overrides allows overriding specific template parameters for this service.
	// When specified, these values take precedence over the template values.
	// +optional
	Overrides *AIMServiceOverrides `json:"overrides,omitempty"`

	// Env specifies environment variables to use for authentication when downloading models.
	// These variables are used for authentication with model registries (e.g., HuggingFace tokens).
	// +optional
	// +listType=map
	// +listMapKey=name
	Env []corev1.EnvVar `json:"env,omitempty"`

	// ImagePullSecrets references secrets for pulling AIM container images.
	// +optional
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// ServiceAccountName specifies the Kubernetes service account to use for the inference workload.
	// This service account is used by the deployed inference pods.
	// If empty, the default service account for the namespace is used.
	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`
}

// AIMServiceStatus defines the observed state of AIMService.
type AIMServiceStatus struct {
	// ObservedGeneration is the most recent generation observed by the controller.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions represent the latest observations of template state.
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ResolvedRuntimeConfig captures metadata about the runtime config that was resolved.
	// +optional
	ResolvedRuntimeConfig *AIMResolvedReference `json:"resolvedRuntimeConfig,omitempty"`

	// ResolvedModel captures metadata about the image that was resolved.
	// +optional
	ResolvedModel *AIMResolvedReference `json:"resolvedModel,omitempty"`

	// Status represents the current high‑level status of the service lifecycle.
	// Values: `Pending`, `Starting`, `Running`, `Degraded`, `Failed`.
	// +kubebuilder:default=Pending
	// +kubebuilder:validation:Enum=Pending;Starting;Running;Degraded;Failed
	Status constants.AIMStatus `json:"status,omitempty"`

	// Routing surfaces information about the configured HTTP routing, when enabled.
	// +optional
	Routing *AIMServiceRoutingStatus `json:"routing,omitempty"`

	// ResolvedTemplate captures metadata about the template that satisfied the reference.
	ResolvedTemplate *AIMResolvedReference `json:"resolvedTemplate,omitempty"`

	// Cache captures cache-related status for this service.
	// +optional
	Cache *AIMServiceCacheStatus `json:"cache,omitempty"`
}

// AIMServiceCacheStatus captures cache-related status for an AIMService.
type AIMServiceCacheStatus struct {
	// TemplateCacheRef references the TemplateCache being used, if any.
	// +optional
	TemplateCacheRef *AIMResolvedReference `json:"templateCacheRef,omitempty"`

	// RetryAttempts tracks how many times this service has attempted to retry a failed cache.
	// Each service gets exactly one retry attempt. When a TemplateCache enters Failed state,
	// this counter is incremented from 0 to 1 after deleting failed ModelCaches.
	// If the retry fails (cache enters Failed again with attempts == 1), the service degrades.
	// +optional
	RetryAttempts int `json:"retryAttempts,omitempty"`
}

func (s *AIMServiceStatus) GetConditions() []metav1.Condition {
	return s.Conditions
}

func (s *AIMServiceStatus) SetConditions(conditions []metav1.Condition) {
	s.Conditions = conditions
}

func (s *AIMServiceStatus) SetStatus(status string) {
	s.Status = constants.AIMStatus(status)
}

// AIMServiceStatusEnum defines coarse-grained states for a service.
// +kubebuilder:validation:Enum=Pending;Starting;Running;Failed;Degraded
type AIMServiceStatusEnum string

// Condition types for AIMService
const (
	// ConditionResolved is True when the model and template have been validated and a runtime profile has been selected.
	AIMServiceConditionTemplateResolved = "TemplateResolved"

	// ConditionCacheReady is True when required caches are present or warmed as requested.
	AIMServiceConditionCacheReady = "CacheReady"

	// ConditionRuntimeReady is True when the underlying KServe runtime and inferenceService are ready.
	AIMServiceConditionRuntimeReady = "RuntimeReady"

	// ConditionRoutingReady is True when exposure and routing through the configured gateway are ready.
	AIMServiceConditionRoutingReady = "RoutingReady"

	// ConditionModelResolved is True when the model has been resolved.
	AIMServiceConditionModelResolved = "ModelResolved"

	// ConditionStorageReady is True when storage (PVC or cache) is ready.
	AIMServiceConditionStorageReady = "StorageReady"
)

// Condition reasons for AIMService
const (
	// Model Resolution
	AIMServiceReasonInvalidImageReference = "InvalidImageReference"
	AIMServiceReasonModelNotFound         = "ModelNotFound"
	AIMServiceReasonCreatingModel         = "CreatingModel"
	AIMServiceReasonModelNotReady         = "ModelNotReady"
	AIMServiceReasonModelResolved         = "ModelResolved"
	AIMServiceReasonMultipleModelsFound   = "MultipleModelsFound"

	// Template Resolution
	AIMServiceReasonTemplateNotFound           = "TemplateNotFound"
	AIMServiceReasonTemplateSelectionFailed    = "TemplateSelectionFailed"
	AIMServiceReasonTemplateNotReady           = "TemplateNotReady"
	AIMServiceReasonResolved                   = "Resolved"
	AIMServiceReasonValidationFailed           = "ValidationFailed"
	AIMServiceReasonTemplateSelectionAmbiguous = "TemplateSelectionAmbiguous"

	// Storage
	AIMServiceReasonCreatingPVC  = "CreatingPVC"
	AIMServiceReasonPVCNotBound  = "PVCNotBound"
	AIMServiceReasonStorageReady = "StorageReady"

	// Cache
	AIMServiceReasonCacheCreating = "CacheCreating"
	AIMServiceReasonCacheNotReady = "CacheNotReady"
	AIMServiceReasonCacheReady    = "CacheReady"
	AIMServiceReasonCacheRetrying = "CacheRetrying"
	AIMServiceReasonCacheFailed   = "CacheFailed"

	// Runtime
	AIMServiceReasonCreatingRuntime = "CreatingRuntime"
	AIMServiceReasonRuntimeReady    = "RuntimeReady"
	AIMServiceReasonRuntimeFailed   = "RuntimeFailed"

	// Routing
	AIMServiceReasonConfiguringRoute    = "ConfiguringRoute"
	AIMServiceReasonRouteReady          = "RouteReady"
	AIMServiceReasonRouteFailed         = "RouteFailed"
	AIMServiceReasonPathTemplateInvalid = "PathTemplateInvalid"
)

// AIMService manages a KServe-based AIM inference service for the selected model and template.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=aimsvc,categories=aim;all
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.status`
// +kubebuilder:printcolumn:name="Model",type=string,JSONPath=`.status.resolvedImage.name`
// +kubebuilder:printcolumn:name="Template",type=string,JSONPath=`.status.resolvedTemplate.name`
// +kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=`.spec.replicas`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:validation:XValidation:rule="self.metadata.name.size() + self.metadata.namespace.size() <= 62",message="combined length of name and namespace cannot exceed 62 characters (KServe uses {name}-{namespace} format which must not exceed 63 characters)"
type AIMService struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AIMServiceSpec   `json:"spec,omitempty"`
	Status AIMServiceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// AIMServiceList contains a list of AIMService.
type AIMServiceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AIMService `json:"items"`
}

// AIMServiceRoutingStatus captures observed routing details.
type AIMServiceRoutingStatus struct {
	// Path is the HTTP path prefix used when routing is enabled.
	// Example: `/tenant/svc-uuid`.
	// +optional
	Path string `json:"path,omitempty"`
}

// GetStatus returns a pointer to the AIMService status.
func (svc *AIMService) GetStatus() *AIMServiceStatus {
	return &svc.Status
}

// GetCachingMode returns the effective caching mode for this service.
// It checks the new Caching.Mode field first, then falls back to the deprecated
// CacheModel field for backward compatibility.
func (spec *AIMServiceSpec) GetCachingMode() AIMCachingMode {
	// Prefer new Caching.Mode field
	if spec.Caching != nil && spec.Caching.Mode != "" {
		return spec.Caching.Mode
	}

	// Fall back to deprecated CacheModel field
	if spec.CacheModel != nil {
		if *spec.CacheModel {
			return CachingModeAlways
		}
		return CachingModeNever
	}

	// Default to Auto
	return CachingModeAuto
}

func init() {
	SchemeBuilder.Register(&AIMService{}, &AIMServiceList{})
}
