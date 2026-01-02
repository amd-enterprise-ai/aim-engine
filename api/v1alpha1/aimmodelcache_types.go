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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

const (
	DefaultDownloadImage = "kserve/storage-initializer:v0.16.0-rc0"

	// ModelCacheSourceURIIndexKey is the field index key for AIMModelCache.Spec.SourceURI
	ModelCacheSourceURIIndexKey = ".spec.sourceUri"
)

// AIMModelCacheSpec defines the desired state of AIMModelCache
type AIMModelCacheSpec struct {
	// SourceURI specifies the source location of the model to download.
	// Supported protocols: hf:// (HuggingFace) and s3:// (S3-compatible storage).
	// This field uniquely identifies the model cache and is immutable after creation.
	// Example: hf://meta-llama/Llama-3-8B
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="sourceUri is immutable"
	// +kubebuilder:validation:Pattern=`^(hf|s3)://[^ \t\r\n]+$`
	SourceURI string `json:"sourceUri"`

	// StorageClassName specifies the storage class for the cache volume.
	// When not specified, uses the cluster default storage class.
	// +optional
	StorageClassName string `json:"storageClassName,omitempty"`

	// Size specifies the size of the cache volume
	Size resource.Quantity `json:"size"`

	// Env lists the environment variables to use for authentication when downloading models.
	// These variables are used for authentication with model registries (e.g., HuggingFace tokens).
	// +optional
	// +listType=map
	// +listMapKey=name
	Env []corev1.EnvVar `json:"env,omitempty"`

	// ModelDownloadImage specifies the container image used to download and initialize the model cache.
	// This image runs as a job to download model artifacts from the source URI to the cache volume.
	// When not specified, defaults to kserve/storage-initializer:v0.16.0.
	// +optional
	// +kubebuilder:default="kserve/storage-initializer:v0.16.0"
	ModelDownloadImage string `json:"modelDownloadImage,omitempty"`

	// ImagePullSecrets references secrets for pulling AIM container images.
	// +optional
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// RuntimeConfigRef contains the runtime config reference for this model cache.
	RuntimeConfigRef `json:",inline"`
}

// DownloadProgress represents the download progress for a model cache
type DownloadProgress struct {
	// TotalBytes is the expected total size of the download in bytes
	// +optional
	TotalBytes int64 `json:"totalBytes,omitempty"`

	// DownloadedBytes is the number of bytes downloaded so far
	// +optional
	DownloadedBytes int64 `json:"downloadedBytes,omitempty"`

	// Percentage is the download progress as a percentage (0-100)
	// +optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	Percentage int32 `json:"percentage,omitempty"`

	// DisplayPercentage is a human-readable progress string (e.g., "45 %")
	// This field is automatically populated from Progress.Percentage
	// +optional
	DisplayPercentage string `json:"displayPercentage,omitempty"`
}

// AIMModelCacheStatus defines the observed state of AIMModelCache
type AIMModelCacheStatus struct {
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions represent the latest available observations of the model cache's state
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Status represents the current status of the model cache
	// +kubebuilder:default=Pending
	// +kubebuilder:validation:Enum=Pending;Progressing;Ready;Degraded;Failed
	Status constants.AIMStatus `json:"status,omitempty"`

	// Progress represents the download progress when Status is Progressing
	// +optional
	Progress *DownloadProgress `json:"progress,omitempty"`

	// LastUsed represents the last time a model was deployed that used this cache
	LastUsed *metav1.Time `json:"lastUsed,omitempty"`

	// PersistentVolumeClaim represents the name of the created PVC
	PersistentVolumeClaim string `json:"persistentVolumeClaim,omitempty"`
}

func (m *AIMModelCache) GetStatus() *AIMModelCacheStatus {
	return &m.Status
}

func (s *AIMModelCacheStatus) GetConditions() []metav1.Condition {
	return s.Conditions
}

func (s *AIMModelCacheStatus) SetConditions(conditions []metav1.Condition) {
	s.Conditions = conditions
}

func (s *AIMModelCacheStatus) SetStatus(status string) {
	s.Status = constants.AIMStatus(status)
}

// Condition types for AIMModelCache
const (
	// AIMModelCacheConditionStorageReady is True when storage backing the cache is provisioned and mounted (PVC Bound)
	AIMModelCacheConditionStorageReady = "StorageReady"
	AIMModelCacheDownloadReady         = "DownloadReady"
)

// Condition reasons for AIMModelCache
const (
	// StorageReady-related reasons
	AIMModelCacheReasonPVCProvisioning = "PVCProvisioning"
	AIMModelCacheReasonPVCBound        = "PVCBound"
	AIMModelCacheReasonPVCPending      = "PVCPending"
	AIMModelCacheReasonPVCLost         = "PVCLost"

	// Progressing-related reasons
	AIMModelCacheReasonWaitingForPVC = "WaitingForPVC"
	AIMModelCacheReasonDownloading   = "Downloading"
	AIMModelCacheReasonRetryBackoff  = "RetryBackoff"

	// Ready-related reasons
	AIMModelCacheReasonWarm = "Warm"

	// Failure-related reasons
	AIMModelCacheReasonNoFailure      = "NoFailure"
	AIMModelCacheReasonDownloadFailed = "DownloadFailed"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=aimmc,categories=aim;all
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.status`
// +kubebuilder:printcolumn:name="Model Size",type=string,JSONPath=`.spec.size`
// +kubebuilder:printcolumn:name="Progress",type=string,JSONPath=`.status.progress.displayPercentage`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AIMModelCache is the Schema for the modelcaches API
type AIMModelCache struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AIMModelCacheSpec   `json:"spec,omitempty"`
	Status AIMModelCacheStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AIMModelCacheList contains a list of AIMModelCache
type AIMModelCacheList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AIMModelCache `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AIMModelCache{}, &AIMModelCacheList{})
}
