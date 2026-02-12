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
	DefaultDownloadImage = "ghcr.io/silogen/aim-artifact-downloader:0.2.0"

	// ArtifactSourceURIIndexKey is the field index key for AIMArtifact.Spec.SourceURI
	ArtifactSourceURIIndexKey = ".spec.sourceUri"
)

const (
	// ArtifactConditionDownloadComplete is True when the download phase has finished
	// and the job has moved to verification. Progress will show 100% at this point.
	ArtifactConditionDownloadComplete = "DownloadComplete"

	ArtifactReasonDownloading      = "Downloading"
	ArtifactReasonDownloadComplete = "DownloadComplete"
	ArtifactReasonVerifying        = "Verifying"
	ArtifactReasonVerified         = "Verified"
)

// AIMArtifactMode indicates the ownership mode of a artifact, derived from owner references.
// +kubebuilder:validation:Enum=Dedicated;Shared
type AIMArtifactMode string

const (
	// ArtifactModeDedicated indicates the cache has owner references and will be
	// garbage collected when its owners are deleted.
	ArtifactModeDedicated AIMArtifactMode = "Dedicated"

	// ArtifactModeShared indicates the cache has no owner references and persists
	// independently, available for sharing across services.
	ArtifactModeShared AIMArtifactMode = "Shared"
)

// AIMArtifactSpec defines the desired state of AIMArtifact
type AIMArtifactSpec struct {
	// SourceURI specifies the source location of the model to download.
	// Supported protocols: hf:// (HuggingFace) and s3:// (S3-compatible storage).
	// This field uniquely identifies the artifact and is immutable after creation.
	// Example: hf://meta-llama/Llama-3-8B
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="sourceUri is immutable"
	// +kubebuilder:validation:Pattern=`^(hf|s3)://[^ \t\r\n]+$`
	SourceURI string `json:"sourceUri"`

	// ModelID is the canonical identifier in {org}/{name} format.
	// Determines the cache download path: /workspace/cache/{modelId}
	// For HuggingFace sources, this is typically derived from the URI (e.g., "meta-llama/Llama-3-8B").
	// For S3 sources, this must be explicitly provided (e.g., "my-team/fine-tuned-llama").
	// When not specified, derived from SourceURI for HuggingFace sources.
	// +optional
	// +kubebuilder:validation:Pattern=`^[a-zA-Z0-9_-]+/[a-zA-Z0-9._-]+$`
	ModelID string `json:"modelId,omitempty"`

	// StorageClassName specifies the storage class for the cache volume.
	// When not specified, uses the cluster default storage class.
	// +optional
	StorageClassName string `json:"storageClassName,omitempty"`

	// Size specifies the size of the cache volume
	// +optional
	Size resource.Quantity `json:"size"`

	// Env lists the environment variables to use for authentication when downloading models.
	// These variables are used for authentication with model registries (e.g., HuggingFace tokens).
	// +optional
	// +listType=map
	// +listMapKey=name
	Env []corev1.EnvVar `json:"env,omitempty"`

	// ModelDownloadImage specifies the container image used to download and initialize the artifact.
	// This image runs as a job to download model artifacts from the source URI to the cache volume.
	// When not specified, defaults to "ghcr.io/silogen/aim-artifact-downloader:0.2.0".
	// +optional
	// +kubebuilder:default="ghcr.io/silogen/aim-artifact-downloader:0.2.0"
	ModelDownloadImage string `json:"modelDownloadImage,omitempty"`

	// ImagePullSecrets references secrets for pulling AIM container images.
	// +optional
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// RuntimeConfigRef contains the runtime config reference for this artifact.
	RuntimeConfigRef `json:",inline"`
}

// DownloadProgress represents the download progress for a artifact
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

// DownloadState represents the current download attempt state, updated by the downloader pod
type DownloadState struct {
	// Protocol is the download protocol currently in use (e.g., "XET", "HF_TRANSFER", "HTTP")
	// +optional
	Protocol string `json:"protocol,omitempty"`

	// Attempt is the current attempt number (1-based)
	// +optional
	Attempt int32 `json:"attempt,omitempty"`

	// TotalAttempts is the total number of attempts configured via AIM_DOWNLOADER_PROTOCOL
	// +optional
	TotalAttempts int32 `json:"totalAttempts,omitempty"`

	// ProtocolSequence is the configured protocol sequence (e.g., "HF_TRANSFER,XET")
	// +optional
	ProtocolSequence string `json:"protocolSequence,omitempty"`

	// Message is a human-readable status message from the downloader
	// +optional
	Message string `json:"message,omitempty"`
}

// AIMArtifactStatus defines the observed state of AIMArtifact
type AIMArtifactStatus struct {
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions represent the latest available observations of the artifact's state
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Status represents the current status of the artifact
	// +kubebuilder:default=Pending
	// +kubebuilder:validation:Enum=Pending;Progressing;Ready;Degraded;Failed;NotAvailable
	Status constants.AIMStatus `json:"status,omitempty"`

	// Progress represents the download progress when Status is Progressing
	// +optional
	Progress *DownloadProgress `json:"progress,omitempty"`

	// Download represents the current download attempt state, patched by the downloader pod.
	// Shows which protocol is active, what attempt we're on, etc.
	// +optional
	Download *DownloadState `json:"download,omitempty"`

	// DisplaySize is the human-readable effective size (spec or discovered)
	// +optional
	DisplaySize string `json:"displaySize,omitempty"`

	// LastUsed represents the last time a model was deployed that used this cache
	LastUsed *metav1.Time `json:"lastUsed,omitempty"`

	// PersistentVolumeClaim represents the name of the created PVC
	PersistentVolumeClaim string `json:"persistentVolumeClaim,omitempty"`

	// Mode indicates the ownership mode of this artifact, derived from owner references.
	// - Dedicated: Has owner references, will be garbage collected when owners are deleted.
	// - Shared: No owner references, persists independently and can be shared.
	// +optional
	Mode AIMArtifactMode `json:"mode,omitempty"`
	// DiscoveredSizeBytes is the model size discovered via check-size job.
	// Populated when spec.size is not provided.
	// +optional
	DiscoveredSizeBytes *int64 `json:"discoveredSizeBytes,omitempty"`

	// AllocatedSize is the actual PVC size requested (including headroom).
	// +optional
	AllocatedSize resource.Quantity `json:"allocatedSize,omitempty"`

	// HeadroomPercent is the headroom percentage that was applied to the PVC size.
	// +optional
	HeadroomPercent *int32 `json:"headroomPercent,omitempty"`
}

func (m *AIMArtifact) GetStatus() *AIMArtifactStatus {
	return &m.Status
}

func (m *AIMArtifact) GetRuntimeConfigRef() RuntimeConfigRef {
	return m.Spec.RuntimeConfigRef
}

func (s *AIMArtifactStatus) GetConditions() []metav1.Condition {
	return s.Conditions
}

func (s *AIMArtifactStatus) SetConditions(conditions []metav1.Condition) {
	s.Conditions = conditions
}

func (s *AIMArtifactStatus) SetStatus(status string) {
	s.Status = constants.AIMStatus(status)
}

func (s *AIMArtifactStatus) GetAIMStatus() constants.AIMStatus {
	return s.Status
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=aimart,categories=aim;all
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.status`
// +kubebuilder:printcolumn:name="Mode",type=string,JSONPath=`.status.mode`
// +kubebuilder:printcolumn:name="Model Size",type=string,JSONPath=`.status.displaySize`
// +kubebuilder:printcolumn:name="Progress",type=string,JSONPath=`.status.progress.displayPercentage`
// +kubebuilder:printcolumn:name="Protocol",type=string,JSONPath=`.status.download.protocol`,priority=1
// +kubebuilder:printcolumn:name="Attempt",type=string,JSONPath=`.status.download.attempt`,priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AIMArtifact is the Schema for the artifacts API
type AIMArtifact struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AIMArtifactSpec   `json:"spec,omitempty"`
	Status AIMArtifactStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AIMArtifactList contains a list of AIMArtifact
type AIMArtifactList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AIMArtifact `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AIMArtifact{}, &AIMArtifactList{})
}
