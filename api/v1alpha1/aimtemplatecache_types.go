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
	// TemplateCacheTemplateNameIndexKey is the field index key for AIMTemplateCache.Spec.TemplateName
	TemplateCacheTemplateNameIndexKey = ".spec.templateName"
)

// AIMTemplateCacheSpec defines the desired state of AIMTemplateCache
type AIMTemplateCacheSpec struct {
	// TemplateName is the name of the AIMServiceTemplate or AIMClusterServiceTemplate to cache.
	// The controller will first look for a namespace-scoped AIMServiceTemplate in the same namespace.
	// If not found, it will look for a cluster-scoped AIMClusterServiceTemplate with the same name.
	// Namespace-scoped templates take priority over cluster-scoped templates.
	// +kubebuilder:validation:MinLength=1
	TemplateName string `json:"templateName"`

	// TemplateScope indicates whether the template is namespace-scoped or cluster-scoped.
	// This field is set by the controller during template resolution.
	// +required
	TemplateScope AIMServiceTemplateScope `json:"templateScope"`

	// Env specifies environment variables to use for authentication when downloading models.
	// These variables are used for authentication with model registries (e.g., HuggingFace tokens).
	// +optional
	// +listType=map
	// +listMapKey=name
	Env []corev1.EnvVar `json:"env,omitempty"`

	// ImagePullSecrets references secrets for pulling AIM container images.
	// +optional
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// StorageClassName specifies the storage class for cache volumes.
	// When not specified, uses the cluster default storage class.
	// +optional
	StorageClassName string `json:"storageClassName,omitempty"`

	// DownloadImage specifies the container image used to download and initialize model caches.
	// When not specified, the controller uses the default model download image.
	// +optional
	DownloadImage string `json:"downloadImage,omitempty"`

	// ModelSources specifies the model sources to cache for this template.
	// These sources are typically copied from the resolved template's model sources.
	// +optional
	ModelSources []AIMModelSource `json:"modelSources,omitempty"`

	// RuntimeConfigRef contains the runtime config reference for this template cache.
	RuntimeConfigRef `json:",inline"`
}

// AIMTemplateCacheStatus defines the observed state of AIMTemplateCache
type AIMTemplateCacheStatus struct {
	// ObservedGeneration is the most recent generation observed by the controller.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions represent the latest observations of the template cache state.
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ResolvedRuntimeConfig captures metadata about the runtime config that was resolved.
	// +optional
	ResolvedRuntimeConfig *AIMResolvedReference `json:"resolvedRuntimeConfig,omitempty"`

	// Status represents the current high-level status of the template cache.
	// +kubebuilder:default=Pending
	// +kubebuilder:validation:Enum=Pending;Ready
	Status constants.AIMStatus `json:"status,omitempty"`

	// ResolvedTemplateKind indicates whether the template resolved to a namespace-scoped
	// AIMServiceTemplate or cluster-scoped AIMClusterServiceTemplate.
	// Values: "AIMServiceTemplate", "AIMClusterServiceTemplate"
	ResolvedTemplateKind string `json:"resolvedTemplateKind,omitempty"`
}

func (s *AIMTemplateCacheStatus) GetConditions() []metav1.Condition {
	return s.Conditions
}

func (s *AIMTemplateCacheStatus) SetConditions(conditions []metav1.Condition) {
	s.Conditions = conditions
}

func (s *AIMTemplateCacheStatus) SetStatus(status string) {
	s.Status = constants.AIMStatus(status)
}

// Condition types for AIMTemplateCache
const (
	// AIMTemplateCacheConditionResolved is True when the template reference has been resolved.
	AIMTemplateCacheConditionResolved = "Resolved"
	// AIMTemplateCacheConditionCacheReady is True when the template's models are cached.
	AIMTemplateCacheConditionCacheReady = "CacheReady"
	// AIMTemplateCacheConditionReady is True when the template cache is ready.
	AIMTemplateCacheConditionReady = "Ready"
	// AIMTemplateCacheConditionProgressing is True when cache warming is in progress.
	AIMTemplateCacheConditionProgressing = "Progressing"
	// AIMTemplateCacheConditionFailure is True when a failure has occurred.
	AIMTemplateCacheConditionFailure = "Failure"
)

// Condition reasons for AIMTemplateCache
const (
	// Resolution related
	AIMTemplateCacheReasonTemplateNotFound = "TemplateNotFound"
	AIMTemplateCacheReasonResolved         = "Resolved"

	// Cache related
	AIMTemplateCacheReasonWarming = "Warming"
	AIMTemplateCacheReasonWarm    = "Warm"
	AIMTemplateCacheReasonFailed  = "Failed"

	// Template resolution
	AIMTemplateCacheConditionTemplateFound = "TemplateFound"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=aimtc,categories=aim;all
// +kubebuilder:printcolumn:name="Template",type=string,JSONPath=`.spec.templateName`
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.status`
// +kubebuilder:printcolumn:name="Kind",type=string,JSONPath=`.status.resolvedTemplateKind`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// AIMTemplateCache pre-warms model caches for a specified template.
type AIMTemplateCache struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AIMTemplateCacheSpec   `json:"spec,omitempty"`
	Status AIMTemplateCacheStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// AIMTemplateCacheList contains a list of AIMTemplateCache.
type AIMTemplateCacheList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AIMTemplateCache `json:"items"`
}

func (t *AIMTemplateCache) GetStatus() *AIMTemplateCacheStatus {
	return &t.Status
}

func init() {
	SchemeBuilder.Register(&AIMTemplateCache{}, &AIMTemplateCacheList{})
}
