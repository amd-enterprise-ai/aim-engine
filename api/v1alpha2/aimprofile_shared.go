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

package v1alpha2

import (
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

const (
	// ProfileAimIdIndexKey is the field index key for AIMProfile/AIMClusterProfile spec.aimId.
	ProfileAimIdIndexKey = ".spec.aimId"
)

// AIMProfileSpecCommon contains spec fields shared between AIMProfile and AIMClusterProfile.
// A profile answers five questions without consulting any other resource: model architecture
// (aimId), accelerator (acceleratorModel/Type/Count), K8s resources (status.resources),
// runtime config (engineArgs, engineEnv), and container image (image).
type AIMProfileSpecCommon struct {
	// AimId is the model architecture identifier (e.g., "qwen/qwen3-32b").
	// Primary matching axis for profile selection and custom weight onboarding. Immutable.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="aimId is immutable"
	AimId string `json:"aimId"`

	// ModelId is the specific model / HuggingFace URI (e.g., "qwen/qwen3-32b-fp8").
	// Determines the cache path (/workspace/cache/{modelId}) and serves as a secondary
	// discriminator for custom weight matching.
	// +optional
	ModelId string `json:"modelId,omitempty"`

	// ProfileId is the on-disk profile identifier from the AIM image
	// (e.g., "vllm-mi300x-fp8-tp1-latency"). Populated during discovery to link this
	// CRD back to the profile YAML inside the container. Not required for manually
	// created profiles.
	// +optional
	ProfileId string `json:"profileId,omitempty"`

	// Engine identifies the inference engine (e.g., "vllm", "tgi").
	// +optional
	Engine string `json:"engine,omitempty"`

	// Metric is the optimization target for this profile.
	// +optional
	// +kubebuilder:validation:Enum=latency;throughput
	Metric AIMMetric `json:"metric,omitempty"`

	// Precision is the numeric precision used by this profile.
	// +optional
	// +kubebuilder:validation:Enum=fp4;fp8;fp16;fp32;bf16;int4;int8
	Precision AIMPrecision `json:"precision,omitempty"`

	// Type indicates the optimization level. Hierarchy: optimized > general > preview > unoptimized.
	// +optional
	// +kubebuilder:validation:Enum=optimized;general;preview;unoptimized
	Type AIMProfileType `json:"type,omitempty"`

	// Primary marks this as a default/recommended profile. When true, the profile is
	// advertised for standard deployment and copied automatically for custom weight models.
	// Defaults to false when not specified.
	// +kubebuilder:default=false
	Primary bool `json:"primary"`

	// EngineArgs contains inference engine CLI arguments as a free-form JSON object.
	// Passed to the inference engine (e.g., vLLM) at startup.
	// +kubebuilder:pruning:PreserveUnknownFields
	// +kubebuilder:validation:Schemaless
	// +optional
	EngineArgs *apiextensionsv1.JSON `json:"engineArgs,omitempty"`

	// EngineEnv contains environment variables for the inference engine subprocess.
	// Applied via os.execv, distinct from container-level ContainerEnv.
	// +optional
	EngineEnv map[string]string `json:"engineEnv,omitempty"`

	// AcceleratorModel is the accelerator identifier for node selection.
	// Maps to a node label key using the Exists operator:
	//   feature.node.kubernetes.io/aim-accelerator.{value}: Exists
	// Supports both specific models (e.g., "MI300X") and architecture-level
	// fallbacks (e.g., "EPYC_ZEN5") — the AcceleratorDetector labels nodes
	// with all applicable identifiers.
	// +optional
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?$`
	// +kubebuilder:validation:MaxLength=63
	AcceleratorModel string `json:"acceleratorModel,omitempty"`

	// AcceleratorType determines the resource derivation strategy: gpu or cpu.
	// AIM Engine computes default resource requests from this field combined
	// with AcceleratorCount and cluster-level configuration.
	// +optional
	// +kubebuilder:validation:Enum=gpu;cpu
	AcceleratorType AcceleratorType `json:"acceleratorType,omitempty"`

	// AcceleratorCount is the number of accelerator units required (e.g., GPU count).
	// Combined with AcceleratorType and cluster-level configuration to compute
	// default resource requests in status.resources.
	// +optional
	// +kubebuilder:validation:Minimum=0
	AcceleratorCount int32 `json:"acceleratorCount,omitempty"`

	// Resources is an optional override for K8s resource requests/limits.
	// When set, merged on top of the defaults that AIM Engine computes from
	// AcceleratorType, AcceleratorCount, and cluster-level configuration.
	// The resolved result is written to status.resources.
	// +optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`

	// Image is the deployment container image. Required.
	// For purpose-built profiles: the full AIM image.
	// For custom weight profiles: the base image (e.g., aim-base:0.8.5).
	// +kubebuilder:validation:MinLength=1
	Image string `json:"image"`

	// ModelSources specifies model artifact sources for this profile.
	// Populated during discovery or set by user.
	// +optional
	ModelSources []AIMModelSource `json:"modelSources,omitempty"`

	// ContainerEnv specifies container-level env vars for the AIM runtime process (K8s pod spec).
	// +optional
	// +listType=map
	// +listMapKey=name
	ContainerEnv []corev1.EnvVar `json:"containerEnv,omitempty"`

	// ImagePullSecrets lists secrets for pulling container images.
	// +optional
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// ServiceAccountName specifies the service account for workloads.
	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`
}

// AIMProfileCachingConfig configures model caching behavior for namespace-scoped profiles.
type AIMProfileCachingConfig struct {
	// Enabled controls whether caching is enabled for this profile.
	// +kubebuilder:default=false
	Enabled bool `json:"enabled,omitempty"`

	// Env specifies environment variables for model download during caching.
	// If not set, falls back to the profile's ContainerEnv.
	// +optional
	// +listType=map
	// +listMapKey=name
	Env []corev1.EnvVar `json:"env,omitempty"`
}

// AIMProfileStatus defines the observed state of AIMProfile / AIMClusterProfile.
type AIMProfileStatus struct {
	// ObservedGeneration is the most recent generation observed by the controller.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Status represents the current high-level status of this profile.
	// Ready: at least one cluster node matches the profile's accelerator labels and resource requests.
	// NotAvailable: no matching nodes found.
	// +kubebuilder:default=Pending
	// +kubebuilder:validation:Enum=Pending;Progressing;Ready;Degraded;Failed;NotAvailable
	Status constants.AIMStatus `json:"status,omitempty"`

	// Version is extracted from the spec.image tag during reconciliation (e.g., "0.8.5").
	// +optional
	Version string `json:"version,omitempty"`

	// MatchingNodes is the count of cluster nodes matching both the accelerator
	// model label and status.resources requests. Zero means NotAvailable.
	// +optional
	MatchingNodes int32 `json:"matchingNodes"`

	// HardwareSummary is a human-readable string describing the hardware requirements.
	// Format: "{count} x {model}" for GPU (e.g., "1 x MI300X") or "CPU" for CPU-only.
	// +optional
	HardwareSummary string `json:"hardwareSummary,omitempty"`

	// Resources contains the definitive K8s resource requests/limits used for deployment.
	// Computed by AIM Engine from AcceleratorType, AcceleratorCount, and cluster-level
	// configuration, then merged with any spec.resources override.
	// +optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`

	// ResolvedNodeAffinity contains the computed node affinity rules derived from
	// spec.acceleratorModel. Used by AIMService when building InferenceService pods.
	// +optional
	ResolvedNodeAffinity *corev1.NodeAffinity `json:"resolvedNodeAffinity,omitempty"`

	// Conditions represent the latest observations of profile state.
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

func (s *AIMProfileStatus) GetConditions() []metav1.Condition {
	return s.Conditions
}

func (s *AIMProfileStatus) SetConditions(conditions []metav1.Condition) {
	s.Conditions = conditions
}

func (s *AIMProfileStatus) SetStatus(status string) {
	s.Status = constants.AIMStatus(status)
}

func (s *AIMProfileStatus) GetAIMStatus() constants.AIMStatus {
	return s.Status
}

// Profile condition types.
const (
	// AIMProfileConditionHardwareAvailable is True when at least one node matches the profile's
	// accelerator labels and has capacity for the requested resources.
	AIMProfileConditionHardwareAvailable = "HardwareAvailable"
)

// Profile condition reasons.
const (
	AIMProfileReasonHardwareAvailable    = "HardwareAvailable"
	AIMProfileReasonHardwareNotAvailable = "HardwareNotAvailable"
	AIMProfileReasonNoAccelerator        = "NoAcceleratorSpecified"
)
