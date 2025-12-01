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
	AIMModelReasonMetadataMissingRecommendedDeployments = "MetadataMissingRecommendedDeployments"
)

// AIMModelDiscoveryConfig controls discovery behavior for a model.
type AIMModelDiscoveryConfig struct {
	// Enabled controls whether discovery runs for this model.
	// When unset (nil), uses the runtime config's model.autoDiscovery setting.
	// When true, discovery always runs regardless of runtime config.
	// When false, discovery never runs regardless of runtime config.
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// AutoCreateTemplates controls whether templates are auto-created from discovery results.
	// When unset, templates are created if discovery succeeds and returns recommended deployments.
	// When false, discovery runs but templates are not created (metadata extraction only).
	// When true, templates are always created from discovery results.
	// +optional
	AutoCreateTemplates *bool `json:"autoCreateTemplates,omitempty"`
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

	// DefaultServiceTemplate is the default template to use for this image, if the user does not provide any
	DefaultServiceTemplate string `json:"defaultServiceTemplate,omitempty"`

	// ModelSources specifies the model artifacts to use for this model.
	// When specified, these sources are used instead of auto-discovery from the container image.
	// This enables pre-creating custom models with explicit model sources.
	// The discovery job will validate and enrich these sources with size information.
	// AIM runtime currently supports only one model source.
	// +optional
	// +kubebuilder:validation:MaxItems=1
	ModelSources []AIMModelSource `json:"modelSources,omitempty"`

	// RuntimeConfigName references the AIM runtime configuration (by name) to use for this image.
	// The runtime config controls discovery behavior and model creation scope.
	// +kubebuilder:default=default
	RuntimeConfigName string `json:"runtimeConfigName,omitempty"`

	// ImagePullSecrets lists secrets containing credentials for pulling the model container image.
	// These secrets are used for:
	// - OCI registry metadata extraction during discovery
	// - Pulling the image for inference services
	// The secrets are merged with any runtime config defaults.
	// For namespace-scoped models, secrets must exist in the same namespace.
	// For cluster-scoped models, secrets must exist in the operator namespace.
	// +optional
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// Env contains the environment variables used, if authentication is needed during the discovery job
	Env []corev1.EnvVar `json:"env,omitempty"`

	// ServiceAccountName specifies the Kubernetes service account to use for workloads related to this model.
	// This includes metadata extraction jobs and any other model-related operations.
	// If empty, the default service account for the namespace is used.
	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// Resources defines the default resource requirements for services using this image.
	// Template- or service-level values override these defaults.
	// +Optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
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
