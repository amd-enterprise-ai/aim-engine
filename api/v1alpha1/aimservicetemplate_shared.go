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
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

type AIMServiceTemplateSpecCommon struct {
	// ModelName is the model name. Matches `metadata.name` of an AIMModel or AIMClusterModel. Immutable.
	//
	// Example: `meta/llama-3-8b:1.1+20240915`
	//
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="model name is immutable"
	ModelName string `json:"modelName"`

	AIMRuntimeParameters `json:",inline"`

	// RuntimeConfigRef contains the runtime config reference for this service template
	RuntimeConfigRef `json:",inline"`

	// ImagePullSecrets lists secrets containing credentials for pulling container images.
	// These secrets are used for:
	// - Discovery dry-run jobs that inspect the model container
	// - Pulling the image for inference services
	// The secrets are merged with any model or runtime config defaults.
	// For namespace-scoped templates, secrets must exist in the same namespace.
	// For cluster-scoped templates, secrets must exist in the operator namespace.
	// +optional
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// ServiceAccountName specifies the Kubernetes service account to use for workloads related to this template.
	// This includes discovery dry-run jobs and inference services created from this template.
	// If empty, the default service account for the namespace is used.
	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// Resources defines the default container resource requirements applied to services derived from this template.
	// Service-specific values override the template defaults.
	// +optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`

	// ModelSources specifies the model sources required to run this template.
	// When provided, the discovery dry-run will be skipped and these sources will be used directly.
	// This allows users to explicitly declare model dependencies without requiring a discovery job.
	// If omitted, a discovery job will be run to automatically determine the required model sources.
	// +optional
	ModelSources []AIMModelSource `json:"modelSources,omitempty"`

	// ProfileId is the specific AIM profile ID that this template should use.
	// When set, the discovery job will be instructed to use this specific profile.
	// +optional
	ProfileId string `json:"profileId,omitempty"`
}

// AIMTemplateCachingConfig configures model caching behavior for namespace-scoped templates.
type AIMTemplateCachingConfig struct {
	// Enabled controls whether caching is enabled for this template.
	// Defaults to `false`.
	// +kubebuilder:default=false
	Enabled bool `json:"enabled,omitempty"`

	// Env specifies environment variables to use when downloading the model for caching.
	// These variables are available to the model download process and can be used
	// to configure download behavior, authentication, proxies, etc.
	// If not set, falls back to the template's top-level Env field.
	// +optional
	// +listType=map
	// +listMapKey=name
	Env []corev1.EnvVar `json:"env,omitempty"`
}

// AIMServiceTemplateSpec defines the desired state of AIMServiceTemplate (namespace-scoped).
//
// A namespaced and versioned template that selects a runtime profile
// for a given AIM model (by canonical name). Templates are intentionally
// narrow: they describe runtime selection knobs for the AIM container and do
// not redefine the full Kubernetes deployment shape.
type AIMServiceTemplateSpec struct {
	AIMServiceTemplateSpecCommon `json:",inline"`

	// Caching configures model caching behavior for this namespace-scoped template.
	// When enabled, models will be cached using the specified environment variables
	// during download.
	// +optional
	Caching *AIMTemplateCachingConfig `json:"caching,omitempty"`

	// Env specifies environment variables to use for authentication when downloading models.
	// These variables are used for authentication with model registries (e.g., HuggingFace tokens).
	// +optional
	// +listType=map
	// +listMapKey=name
	Env []corev1.EnvVar `json:"env,omitempty"`
}

// AIMClusterServiceTemplateSpec defines the desired state of AIMClusterServiceTemplate (cluster-scoped).
//
// A cluster-scoped template that selects a runtime profile for a given AIM model.
type AIMClusterServiceTemplateSpec struct {
	AIMServiceTemplateSpecCommon `json:",inline"`
}

// AIMServiceTemplateStatus defines the observed state of AIMServiceTemplate.
type AIMServiceTemplateStatus struct {
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

	// ResolvedCache captures metadata about which cache is used for this template
	// +optional
	ResolvedCache *AIMResolvedReference `json:"resolvedCache,omitempty"`

	// Status represents the current high‑level status of the template lifecycle.
	// Values: `Pending`, `Progressing`, `Ready`, `Degraded`, `Failed`.
	// +kubebuilder:default=Pending
	// +kubebuilder:validation:Enum=Pending;Progressing;Ready;Degraded;Failed;NotAvailable
	Status constants.AIMStatus `json:"status,omitempty"`

	// ModelSources list the models that this template requires to run. These are the models that will be
	// cached, if this template is cached.
	ModelSources []AIMModelSource `json:"modelSources,omitempty"`

	// Profile contains the full discovery result profile as a free-form JSON object.
	// This includes metadata, engine args, environment variables, and model details.
	Profile *AIMProfile `json:"profile,omitempty"`

	// DiscoveryJob is a reference to the job that was run for discovery
	DiscoveryJob *AIMResolvedReference `json:"discoveryJob,omitempty"`

	// Discovery contains state tracking for the discovery process, including
	// retry attempts and backoff timing for the circuit breaker pattern.
	// +optional
	Discovery *DiscoveryState `json:"discovery,omitempty"`
}

// DiscoveryState tracks the discovery process state for circuit breaker logic.
// This enables exponential backoff and prevents infinite retry loops when
// discovery jobs fail persistently.
type DiscoveryState struct {
	// Attempts is the number of discovery job attempts that have been made.
	// This counter increments each time a new discovery job is created after a failure.
	// +optional
	Attempts int32 `json:"attempts,omitempty"`

	// LastAttemptTime is the timestamp of the most recent discovery job creation.
	// Used to calculate exponential backoff before the next retry.
	// +optional
	LastAttemptTime *metav1.Time `json:"lastAttemptTime,omitempty"`

	// LastFailureReason captures the reason for the most recent discovery failure.
	// Used to classify failures as terminal vs transient.
	// +optional
	LastFailureReason string `json:"lastFailureReason,omitempty"`

	// SpecHash is a hash of the template spec fields that affect discovery.
	// When the spec changes, the circuit breaker resets to allow fresh attempts.
	// +optional
	SpecHash string `json:"specHash,omitempty"`
}

func (s *AIMServiceTemplateStatus) GetConditions() []metav1.Condition {
	return s.Conditions
}

func (s *AIMServiceTemplateStatus) SetConditions(conditions []metav1.Condition) {
	s.Conditions = conditions
}

func (s *AIMServiceTemplateStatus) SetStatus(status string) {
	s.Status = constants.AIMStatus(status)
}

func (s *AIMServiceTemplateStatus) GetAIMStatus() constants.AIMStatus {
	return s.Status
}

// AIMProfile contains the cached discovery results for a template.
// This is the processed and validated version of AIMDiscoveryProfile that is stored
// in the template's status after successful discovery.
//
// The profile serves as a cache of runtime configuration, eliminating the need to
// re-run discovery for each service that uses this template. Services and caching
// mechanisms reference this cached profile for deployment parameters and model sources.
//
// See discovery.go for AIMDiscoveryProfile (the raw discovery output) and the
// relationship between these types.
type AIMProfile struct {
	// EngineArgs contains runtime-specific engine configuration as a free-form JSON object.
	// The structure depends on the inference engine being used (e.g., vLLM, TGI).
	// These arguments are passed to the runtime container to configure model loading and inference.
	// +kubebuilder:pruning:PreserveUnknownFields
	// +kubebuilder:validation:Schemaless
	EngineArgs *apiextensionsv1.JSON `json:"engine_args,omitempty"`

	// EnvVars contains environment variables required by the runtime for this profile.
	// These may include engine-specific settings, optimization flags, or hardware configuration.
	// +optional
	EnvVars map[string]string `json:"env_vars,omitempty"`

	// Metadata provides structured information about this deployment profile's characteristics.
	Metadata AIMProfileMetadata `json:"metadata,omitempty"`

	// OriginalDiscoveryOutput contains the raw discovery job JSON output.
	// This preserves the complete discovery result from the dry-run container,
	// including all fields that may not be mapped to structured fields above.
	// +kubebuilder:pruning:PreserveUnknownFields
	// +kubebuilder:validation:Schemaless
	// +optional
	OriginalDiscoveryOutput *apiextensionsv1.JSON `json:"originalDiscoveryOutput,omitempty"`
}

// AIMProfileType indicates the optimization level of a deployment profile.
// +kubebuilder:validation:Enum=optimized;preview;unoptimized
type AIMProfileType string

const (
	// AIMProfileTypeOptimized indicates the profile has been fully optimized.
	AIMProfileTypeOptimized AIMProfileType = "optimized"
	// AIMProfileTypePreview indicates the profile is in preview/beta state.
	AIMProfileTypePreview AIMProfileType = "preview"
	// AIMProfileTypeUnoptimized indicates the profile has not been optimized.
	AIMProfileTypeUnoptimized AIMProfileType = "unoptimized"
)

// AIMProfileMetadata describes the characteristics of a cached deployment profile.
// This is identical to AIMDiscoveryProfileMetadata but exists in the template status namespace.
type AIMProfileMetadata struct {
	// Engine identifies the inference engine used for this profile (e.g., "vllm", "tgi").
	// +optional
	Engine string `json:"engine,omitempty"`

	// GPU specifies the GPU model this profile is optimized for (e.g., "MI300X", "MI325X").
	// +optional
	GPU string `json:"gpu,omitempty"`

	// GPUCount indicates how many GPUs are required per replica for this profile.
	// +optional
	GPUCount int32 `json:"gpuCount,omitempty"`

	// Metric indicates the optimization goal for this profile ("latency" or "throughput").
	// +optional
	Metric AIMMetric `json:"metric,omitempty"`

	// Precision specifies the numeric precision used in this profile (e.g., "fp16", "fp8").
	// +optional
	Precision AIMPrecision `json:"precision,omitempty"`

	// Type indicates the optimization level of this profile (optimized, preview, unoptimized).
	// +optional
	Type AIMProfileType `json:"type,omitempty"`
}

// AIMTemplateCandidateResult represents the evaluation result for a template candidate
// during template selection.
type AIMTemplateCandidateResult struct {
	// Name is the name of the template candidate.
	Name string `json:"name"`
	// Status indicates whether the candidate was "chosen" or "rejected".
	Status string `json:"status"`
	// Reason provides a CamelCase reason for the evaluation result.
	Reason string `json:"reason,omitempty"`
}

// Discovery conditions
const (
	// AIMTemplateDiscoveryConditionType is True when runtime profiles have been discovered and sources resolved for the referenced model.
	AIMTemplateDiscoveryConditionType = "Discovered"
)

// Caching conditions
const (
	// AIMTemplateConditionCacheReady is True when all requested caches have been warmed.
	AIMTemplateCacheReadyConditionType = "CacheReady"

	AIMTemplateReasonCacheReady      = "Ready"
	AIMTemplateReasonWaitingForCache = "WaitingForCache"
	AIMTemplateReasonCacheDegraded   = "CacheDegraded"
	AIMTemplateReasonCacheFailed     = "CacheFailed"
)

// Condition reasons for AIMServiceTemplate
const (
	// Discovery related
	AIMTemplateReasonAwaitingDiscovery  = "AwaitingDiscovery"
	AIMTemplateReasonProfilesDiscovered = "ProfilesDiscovered"
	AIMTemplateReasonDiscoveryFailed    = "DiscoveryFailed"

	AIMTemplateReasonGpuNotAvailable = "GpuNotAvailable"

	// Model resolution reasons
	AIMTemplateModelNotFound    = "ModelNotResolved"
	AIMTemplateReasonModelFound = "ModelResolved"

	// Template resolution reasons
	AIMTemplateReasonAwaitingTemplate = "AwaitingTemplate"
	AIMTemplateReasonTemplateFound    = "TemplateFound"

	// Model resolution condition type
	AIMServiceTemplateConditionModelFound = "ModelFound"
)
