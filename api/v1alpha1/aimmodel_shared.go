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
	// AIMModelConditionMetadataExtracted captures whether image metadata extraction succeeded.
	AIMModelConditionMetadataExtracted = "MetadataExtracted"

	// AIMModelReasonMetadataExtracted indicates metadata extraction succeeded.
	AIMModelReasonMetadataExtracted = "MetadataExtracted"

	// AIMModelReasonMetadataExtractionFailed indicates metadata extraction failed (non-blocking, prevents retries).
	AIMModelReasonMetadataExtractionFailed = "MetadataExtractionFailed"

	// Runtime config resolution reasons
	AIMModelReasonConfigNotFound     = "ConfigNotFound"
	AIMModelReasonRuntimeConfigError = "RuntimeConfigError"
	AIMModelReasonUsingDefaults      = "UsingDefaults"
	AIMModelReasonResolved           = "Resolved"

	// Template status reasons
	AIMModelReasonAllTemplatesFailed                    = "AllTemplatesFailed"
	AIMModelReasonNoTemplatesAvailable                  = "NoTemplatesAvailable"
	AIMModelReasonSomeTemplatesDegraded                 = "SomeTemplatesDegraded"
	AIMModelReasonTemplatesProgressing                  = "TemplatesProgressing"
	AIMModelReasonAllTemplatesReady                     = "AllTemplatesReady"
	AIMModelReasonSomeTemplatesReady                    = "SomeTemplatesReady"
	AIMModelReasonNoTemplatesExpected                   = "NoTemplatesExpected"
	AIMModelReasonAwaitingMetadata                      = "AwaitingMetadata"
	AIMModelReasonCreatingTemplates                     = "CreatingTemplates"
	AIMModelReasonMetadataMissingRecommendedDeployments = "MetadataMissingRecommendedDeployments"
)

// AIMModelSourceType indicates how a model's artifacts are sourced.
// +kubebuilder:validation:Enum=Image;Custom
type AIMModelSourceType string

const (
	// AIMModelSourceTypeImage indicates the model is discovered from container image labels.
	AIMModelSourceTypeImage AIMModelSourceType = "Image"
	// AIMModelSourceTypeCustom indicates the model uses explicit spec.modelSources.
	AIMModelSourceTypeCustom AIMModelSourceType = "Custom"
)

// AIMCustomTemplate defines a custom template configuration for a model.
// When modelSources are specified directly on AIMModel, customTemplates allow
// defining explicit hardware requirements and profiles, skipping the discovery job.
type AIMCustomTemplate struct {
	// Name is the template name. If not provided, auto-generated from model name + profile.
	// +optional
	Name string `json:"name,omitempty"`

	// Type indicates the optimization status of this template.
	// - optimized: Template has been tuned for performance
	// - preview: Template is experimental/pre-release
	// - unoptimized: Default, no specific optimizations applied
	// +optional
	// +kubebuilder:validation:Enum=optimized;preview;unoptimized
	// +kubebuilder:default=unoptimized
	Type AIMProfileType `json:"type,omitempty"`

	// Env specifies environment variable overrides when this template is selected.
	// +optional
	// +listType=map
	// +listMapKey=name
	Env []corev1.EnvVar `json:"env,omitempty"`

	// Hardware specifies GPU and CPU requirements for this template.
	// Optional when spec.hardware is set (inherits from spec).
	// When both are set, values are merged field-by-field with template taking precedence.
	// +optional
	Hardware *AIMHardwareRequirements `json:"hardware,omitempty"`

	// Profile declares runtime profile variables for template selection.
	// Used when multiple templates exist to select based on metric/precision.
	// +optional
	Profile *AIMTemplateProfile `json:"profile,omitempty"`
}

// AIMTemplateProfile declares profile variables for template selection.
// Used in AIMCustomTemplate to specify optimization targets.
type AIMTemplateProfile struct {
	// Metric specifies the optimization target (e.g., latency, throughput).
	// +optional
	// +kubebuilder:validation:Enum=latency;throughput
	Metric AIMMetric `json:"metric,omitempty"`

	// Precision specifies the numerical precision (e.g., fp8, fp16, bf16).
	// +optional
	// +kubebuilder:validation:Enum=auto;fp4;fp8;fp16;fp32;bf16;int4;int8
	Precision AIMPrecision `json:"precision,omitempty"`
}

// AIMModelSpec defines the desired state of AIMModel.
type AIMModelSpec struct {
	// Image is the container image URI for this AIM model.
	// This image is inspected by the operator to select runtime profiles used by templates.
	// Discovery behavior is controlled by the discovery field and runtime config's AutoDiscovery setting.
	// +kubebuilder:validation:MinLength=1
	Image string `json:"image"`

	// Discovery controls discovery behavior for this model.
	// When unset, uses runtime config defaults.
	// +optional
	Discovery *AIMModelDiscoveryConfig `json:"discovery,omitempty"`

	// DefaultServiceTemplate specifies the default AIMServiceTemplate to use when creating services for this model.
	// When set, services that reference this model will use this template if no template is explicitly specified.
	// If this is not set, a template will be automatically selected.
	// +optional
	DefaultServiceTemplate string `json:"defaultServiceTemplate,omitempty"`

	// ModelSources specifies the model sources to use for this model.
	// When specified, these sources are used instead of auto-discovery from the container image.
	// This enables pre-creating custom models with explicit model sources.
	// For custom models, modelSources[].size is required (discovery does not run).
	// AIM runtime currently supports only one model source.
	// +optional
	// +kubebuilder:validation:MaxItems=1
	ModelSources []AIMModelSource `json:"modelSources,omitempty"`

	// Hardware specifies default hardware requirements for all custom templates.
	// Individual templates can override these defaults.
	// Required when modelSources is set and customTemplates is empty.
	// +optional
	Hardware *AIMHardwareRequirements `json:"hardware,omitempty"`

	// Type specifies default type for all custom templates.
	// Individual templates can override this default.
	// When nil, templates default to "unoptimized".
	// +optional
	// +kubebuilder:validation:Enum=optimized;preview;unoptimized
	Type *AIMProfileType `json:"type,omitempty"`

	// CustomTemplates defines explicit template configurations for this model.
	// When modelSources are specified, these templates are created directly
	// without running a discovery job.
	// If omitted when modelSources is set, a single template is auto-generated
	// using the spec-level hardware requirements.
	// +optional
	CustomTemplates []AIMCustomTemplate `json:"customTemplates,omitempty"`

	// RuntimeConfigRef contains the runtime config reference for this model, and is used to control discovery behavior.
	RuntimeConfigRef `json:",inline"`

	// ImagePullSecrets lists secrets containing credentials for pulling the model container image.
	// These secrets are used for:
	// - OCI registry metadata extraction during discovery
	// - Pulling the image for inference services
	// The secrets are merged with any runtime config defaults.
	// For namespace-scoped models, secrets must exist in the same namespace.
	// For cluster-scoped models, secrets must exist in the operator namespace.
	// +optional
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// Env specifies environment variables for authentication during model discovery and metadata extraction.
	// These variables are used for authentication with model registries (e.g., HuggingFace tokens).
	// +optional
	// +listType=map
	// +listMapKey=name
	Env []corev1.EnvVar `json:"env,omitempty"`

	// ServiceAccountName specifies the Kubernetes service account to use for workloads related to this model.
	// This includes metadata extraction jobs and any other model-related operations.
	// If empty, the default service account for the namespace is used.
	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// Resources defines the default resource requirements for services using this model.
	// Template- or service-level values override these defaults.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// ImageMetadata is the metadata that is used to determine which recommended service templates to create,
	// and to drive clients with richer metadata regarding this particular model. For most cases the user does
	// not need to set this field manually, for images that have the supported labels embedded in them
	// the `AIM(Cluster)Model.status.imageMetadata` field is automatically filled from the container image labels.
	// This field is intended to be used when there are network restrictions, or in other similar situations.
	// If this field is set, the remote extraction will not be performed at all.
	ImageMetadata *ImageMetadata `json:"imageMetadata,omitempty"`
}

// AIMModelDiscoveryConfig controls discovery behavior for a model.
type AIMModelDiscoveryConfig struct {
	// ExtractMetadata controls whether metadata extraction runs for this model.
	// During metadata extraction, the controller connects to the image registry and
	// extracts the image's labels.
	// +optional
	// +kubebuilder:default=true
	ExtractMetadata bool `json:"extractMetadata,omitempty"`

	// CreateServiceTemplates controls whether (cluster) service templates are auto-created from the image metadata.
	// +optional
	// +kubebuilder:default=true
	CreateServiceTemplates bool `json:"createServiceTemplates,omitempty"`
}

// AIMModelStatus defines the observed state of AIMModel.
type AIMModelStatus struct {
	// ObservedGeneration is the most recent generation observed by the controller
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Status represents the overall status of the image based on its templates
	// +kubebuilder:default=Pending
	// +kubebuilder:validation:Enum=Pending;Progressing;Ready;Degraded;Failed;NotAvailable
	Status constants.AIMStatus `json:"status,omitempty"`

	// Conditions represent the latest available observations of the model's state
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ResolvedRuntimeConfig captures metadata about the runtime config that was resolved.
	// +optional
	ResolvedRuntimeConfig *AIMResolvedReference `json:"resolvedRuntimeConfig,omitempty"`

	// ImageMetadata is the metadata extracted from an AIM image
	// +optional
	ImageMetadata *ImageMetadata `json:"imageMetadata,omitempty"`

	// SourceType indicates how this model's artifacts are sourced.
	// - "Image": Model discovered from container image labels
	// - "Custom": Model uses explicit spec.modelSources
	// Set by the controller based on whether spec.modelSources is populated.
	// +optional
	SourceType AIMModelSourceType `json:"sourceType,omitempty"`
}

func (s *AIMModelStatus) GetConditions() []metav1.Condition {
	return s.Conditions
}

func (s *AIMModelStatus) SetConditions(conditions []metav1.Condition) {
	s.Conditions = conditions
}

func (s *AIMModelStatus) SetStatus(status string) {
	s.Status = constants.AIMStatus(status)
}

func (s *AIMModelStatus) GetAIMStatus() constants.AIMStatus {
	return s.Status
}

// GetEffectiveImageMetadata returns metadata from spec (if provided) or status (if extracted).
// Spec takes precedence over status since it represents user intent.
func (s *AIMModelSpec) GetEffectiveImageMetadata(status *AIMModelStatus) *ImageMetadata {
	if s.ImageMetadata != nil {
		return s.ImageMetadata
	}
	if status != nil {
		return status.ImageMetadata
	}
	return nil
}

// ShouldCreateTemplates returns whether template creation is enabled for this model.
// Returns true if discovery.createServiceTemplates is unset or true.
func (s *AIMModelSpec) ShouldCreateTemplates() bool {
	if s.Discovery == nil {
		return true // Default: create templates
	}
	return s.Discovery.CreateServiceTemplates
}

// ExpectsTemplates returns whether this model should have auto-created templates.
// Returns:
//   - ptr to true: templates expected (has recommendedDeployments and creation enabled)
//   - ptr to false: no templates expected (no recommendedDeployments or creation disabled)
//   - nil: unknown (metadata not yet available)
func (s *AIMModelSpec) ExpectsTemplates(status *AIMModelStatus) *bool {
	// Check if template creation is disabled
	if !s.ShouldCreateTemplates() {
		result := false
		return &result
	}

	metadata := s.GetEffectiveImageMetadata(status)
	if metadata == nil {
		return nil // Unknown - still fetching
	}

	hasDeployments := metadata.Model != nil && len(metadata.Model.RecommendedDeployments) > 0
	return &hasDeployments
}
