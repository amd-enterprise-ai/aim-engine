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
	// AIMServiceTemplateIndexKey is the field index key used by controller-runtime for
	// indexing AIMService resources by their template reference (.spec.template.name).
	// This enables efficient lookups of services that reference a specific template.
	AIMServiceTemplateIndexKey = ".spec.templateRef"

	// AIMServiceResolvedTemplateIndexKey is the field index key for resolved template name
	// Indexes by .status.resolvedTemplate.name for finding services using a specific template
	AIMServiceResolvedTemplateIndexKey = ".status.resolvedTemplate.name"
)

// AIMCachingMode controls caching behavior for a service.
// Canonical values are Dedicated and Shared.
// Legacy values are accepted for backward compatibility:
// - Always maps to Shared
// - Auto maps to Shared
// - Never maps to Dedicated
// +kubebuilder:validation:Enum=Dedicated;Shared;Auto;Always;Never
type AIMCachingMode string

const (
	// CachingModeDedicated always creates service-owned dedicated caches/artifacts.
	CachingModeDedicated AIMCachingMode = "Dedicated"

	// CachingModeShared reuses and creates shared caches/artifacts.
	CachingModeShared AIMCachingMode = "Shared"

	// CachingModeAuto is deprecated legacy value that maps to Shared.
	CachingModeAuto AIMCachingMode = "Auto"

	// CachingModeAlways is deprecated legacy value that maps to Shared.
	CachingModeAlways AIMCachingMode = "Always"

	// CachingModeNever is deprecated legacy value that maps to Dedicated.
	CachingModeNever AIMCachingMode = "Never"
)

// AIMServiceCachingConfig controls caching behavior for a service.
type AIMServiceCachingConfig struct {
	// Mode controls when to use caching.
	// Canonical values:
	// - Shared (default): reuse/create shared cache assets
	// - Dedicated: create service-owned dedicated cache assets
	//
	// Legacy values are accepted and normalized:
	// - Always -> Shared
	// - Auto -> Shared
	// - Never -> Dedicated
	// +kubebuilder:default=Shared
	// +optional
	Mode AIMCachingMode `json:"mode,omitempty"`
}

// AIMServiceTemplateConfig contains template selection configuration for AIMService.
type AIMServiceTemplateConfig struct {
	// Name is the name of the AIMServiceTemplate or AIMClusterServiceTemplate to use.
	// The template selects the runtime profile and GPU parameters.
	// When not specified, a template will be automatically selected based on the model.
	// +optional
	Name string `json:"name,omitempty"`

	// AllowUnoptimized, if true, will allow automatic selection of templates
	// that resolve to an unoptimized profile.
	// +optional
	AllowUnoptimized bool `json:"allowUnoptimized,omitempty"`
}

// AIMServiceModel specifies which model to deploy. Exactly one field must be set.
// +kubebuilder:validation:XValidation:rule="(has(self.name) ? 1 : 0) + (has(self.image) ? 1 : 0) + (has(self.custom) ? 1 : 0) == 1",message="exactly one of name, image, or custom must be specified"
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="model selection is immutable after creation"
type AIMServiceModel struct {
	// Name references an existing AIMModel or AIMClusterModel by metadata.name.
	// The controller looks for a namespace-scoped AIMModel first, then falls back to cluster-scoped AIMClusterModel.
	// Example: `meta-llama-3-8b`
	// +optional
	Name *string `json:"name,omitempty"`

	// Image specifies a container image URI directly.
	// The controller searches for an existing model with this image, or creates one if none exists.
	// The scope of the created model is controlled by the runtime config's ModelCreationScope field.
	// Example: `ghcr.io/silogen/llama-3-8b:v1.2.0`
	// +optional
	Image *string `json:"image,omitempty"`

	// Custom specifies a custom model configuration with explicit base image,
	// model sources, and hardware requirements. The controller will search for
	// an existing matching AIMModel or auto-create one if not found.
	// +optional
	Custom *AIMServiceModelCustom `json:"custom,omitempty"`
}

// AIMServiceModelCustom specifies a custom model configuration with explicit base image,
// model sources, and hardware requirements. Used for ad-hoc custom model deployments.
// +kubebuilder:validation:XValidation:rule="size(self.modelSources) >= 1",message="at least one model source must be specified"
type AIMServiceModelCustom struct {
	// BaseImage is the container image URI for the AIM base image.
	// This will be used as the image for the auto-created AIMModel.
	// Example: `ghcr.io/silogen/aim-base:0.7.0`
	// +required
	BaseImage string `json:"baseImage"`

	// ModelSources specifies the model sources to use.
	// The controller will search for or create an AIMModel with these sources.
	// The size field is optional - if not specified, it will be discovered by the download job.
	// AIM runtime currently supports only one model source.
	// +required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=1
	ModelSources []AIMModelSource `json:"modelSources"`

	// Hardware specifies the GPU and CPU requirements for this custom model.
	// GPU is optional - if not set, no GPUs are requested (CPU-only model).
	// +required
	Hardware AIMHardwareRequirements `json:"hardware"`
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

	// Template contains template selection and configuration.
	// Use Template.Name to specify an explicit template, or omit to auto-select.
	// +optional
	Template AIMServiceTemplateConfig `json:"template,omitempty"`

	// Caching controls caching behavior for this service.
	// When nil, defaults to Shared mode.
	// +optional
	Caching *AIMServiceCachingConfig `json:"caching,omitempty"`

	// DEPRECATED: Use Caching.Mode instead. This field will be removed in a future version.
	// This field is no longer honored by the controller.
	// +optional
	// +kubebuilder:validation:Deprecated
	// +kubebuilder:validation:DeprecatedMessage="Use Caching.Mode instead. This field will be removed in a future version."
	CacheModel *bool `json:"cacheModel,omitempty"`

	// Replicas specifies the number of replicas for this service.
	// When not specified, defaults to 1 replica.
	// This value overrides any replica settings from the template.
	// For autoscaling, use MinReplicas and MaxReplicas instead.
	// +optional
	// +kubebuilder:default=1
	Replicas *int32 `json:"replicas,omitempty"`

	// MinReplicas specifies the minimum number of replicas for autoscaling.
	// Defaults to 1. Scale to zero is not supported.
	// When specified with MaxReplicas, enables autoscaling for the service.
	// +optional
	// +kubebuilder:validation:Minimum=1
	MinReplicas *int32 `json:"minReplicas,omitempty"`

	// MaxReplicas specifies the maximum number of replicas for autoscaling.
	// Required when MinReplicas is set or when AutoScaling configuration is provided.
	// +optional
	// +kubebuilder:validation:Minimum=1
	MaxReplicas *int32 `json:"maxReplicas,omitempty"`

	// AutoScaling configures advanced autoscaling behavior using KEDA.
	// Supports custom metrics from OpenTelemetry backend.
	// When specified, MinReplicas and MaxReplicas should also be set.
	// +optional
	AutoScaling *AIMServiceAutoScaling `json:"autoScaling,omitempty"`

	// RuntimeConfigRef contains the runtime config reference for this service.
	// The result of the merged runtime configs is merged with the inline AIMServiceRuntimeConfig configuration.
	RuntimeConfigRef `json:",inline"`

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

	// Runtime captures runtime status including replica counts.
	// +optional
	Runtime *AIMServiceRuntimeStatus `json:"runtime,omitempty"`
}

// AIMServiceCacheStatus captures cache-related status for an AIMService.
type AIMServiceCacheStatus struct {
	// TemplateCacheRef references the TemplateCache being used, if any.
	// +optional
	TemplateCacheRef *AIMResolvedReference `json:"templateCacheRef,omitempty"`

	// RetryAttempts tracks how many times this service has attempted to retry a failed cache.
	// Each service gets exactly one retry attempt. When a TemplateCache enters Failed state,
	// this counter is incremented from 0 to 1 after deleting failed Artifacts.
	// If the retry fails (cache enters Failed again with attempts == 1), the service degrades.
	// +optional
	RetryAttempts int `json:"retryAttempts,omitempty"`
}

// AIMServiceRuntimeStatus captures runtime status including replica counts from HPA.
type AIMServiceRuntimeStatus struct {
	// CurrentReplicas is the current number of replicas as reported by the HPA.
	// +optional
	CurrentReplicas int32 `json:"currentReplicas,omitempty"`

	// DesiredReplicas is the desired number of replicas as determined by the HPA.
	// +optional
	DesiredReplicas int32 `json:"desiredReplicas,omitempty"`

	// MinReplicas is the minimum number of replicas configured for autoscaling.
	// +optional
	MinReplicas int32 `json:"minReplicas,omitempty"`

	// MaxReplicas is the maximum number of replicas configured for autoscaling.
	// +optional
	MaxReplicas int32 `json:"maxReplicas,omitempty"`

	// Replicas is a formatted display string for kubectl output.
	// Shows "current" for fixed replicas or "current/desired (min-max)" for autoscaling.
	// +optional
	Replicas string `json:"replicas,omitempty"`
}

func (s *AIMService) GetRuntimeConfigRef() RuntimeConfigRef {
	return s.Spec.RuntimeConfigRef
}

func (s *AIMServiceStatus) GetConditions() []metav1.Condition {
	return s.Conditions
}

func (s *AIMServiceStatus) SetConditions(conditions []metav1.Condition) {
	s.Conditions = conditions
}

func (s *AIMServiceStatus) SetStatus(status string) {
	// Map framework statuses to AIMService-specific statuses.
	// AIMService uses: Pending, Starting, Running, Failed, Degraded
	// Framework uses: Pending, Progressing, Ready, Failed, Degraded
	switch constants.AIMStatus(status) {
	case constants.AIMStatusProgressing:
		s.Status = constants.AIMStatusStarting
	case constants.AIMStatusReady:
		s.Status = constants.AIMStatusRunning
	default:
		s.Status = constants.AIMStatus(status)
	}
}

func (s *AIMServiceStatus) GetAIMStatus() constants.AIMStatus {
	return s.Status
}

// AIMServiceStatusEnum defines coarse-grained states for a service.
// +kubebuilder:validation:Enum=Pending;Starting;Running;Failed;Degraded
type AIMServiceStatusEnum string

// Condition reasons for AIMService
const (
	// Model Resolution
	AIMServiceReasonInvalidImageReference = "InvalidImageReference"
	AIMServiceReasonModelNotFound         = "ModelNotFound"
	AIMServiceReasonCreatingModel         = "CreatingModel"
	AIMServiceReasonModelNotReady         = "ModelNotReady"
	AIMServiceReasonModelResolved         = "ModelResolved"

	// Template Resolution
	AIMServiceReasonTemplateNotFound           = "TemplateNotFound"
	AIMServiceReasonTemplateNotReady           = "TemplateNotReady"
	AIMServiceReasonResolved                   = "Resolved"
	AIMServiceReasonTemplateSelectionAmbiguous = "TemplateSelectionAmbiguous"

	// Storage
	AIMServiceReasonPVCNotBound      = "PVCNotBound"
	AIMServiceReasonStorageReady     = "StorageReady"
	AIMServiceReasonStorageSizeError = "StorageSizeError"

	// Cache
	AIMServiceReasonCacheCreating = "CacheCreating"
	AIMServiceReasonCacheNotReady = "CacheNotReady"
	AIMServiceReasonCacheReady    = "CacheReady"
	AIMServiceReasonCacheFailed   = "CacheFailed"

	// Runtime
	AIMServiceReasonCreatingRuntime = "CreatingRuntime"
	AIMServiceReasonRuntimeReady    = "RuntimeReady"

	// Routing
	AIMServiceReasonPathTemplateInvalid = "PathTemplateInvalid"
)

// AIMService manages a KServe-based AIM inference service for the selected model and template.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=aimsvc,categories=aim;all
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.status`
// +kubebuilder:printcolumn:name="Model",type=string,JSONPath=`.status.resolvedModel.name`
// +kubebuilder:printcolumn:name="Template",type=string,JSONPath=`.status.resolvedTemplate.name`
// +kubebuilder:printcolumn:name="Replicas",type=string,JSONPath=`.status.runtime.replicas`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// Note: KServe uses {name}-{namespace} format which must not exceed 63 characters.
// This constraint is validated at runtime since CEL cannot access metadata.namespace.
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
	// Example: `/tenant/svc-uuid`
	// +optional
	Path string `json:"path,omitempty"`
}

// GetStatus returns a pointer to the AIMService status.
func (svc *AIMService) GetStatus() *AIMServiceStatus {
	return &svc.Status
}

// GetCachingMode returns the effective canonical caching mode for this service.
// Legacy values are normalized for backward compatibility.
func (spec *AIMServiceSpec) GetCachingMode() AIMCachingMode {
	if spec.Caching == nil || spec.Caching.Mode == "" {
		return CachingModeShared
	}

	switch spec.Caching.Mode {
	case CachingModeDedicated, CachingModeNever:
		return CachingModeDedicated
	case CachingModeShared, CachingModeAlways, CachingModeAuto:
		return CachingModeShared
	default:
		// Defensive default for unknown values.
		return CachingModeShared
	}
}

func init() {
	SchemeBuilder.Register(&AIMService{}, &AIMServiceList{})
}
