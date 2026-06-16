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

// DefaultDownloadImage is the container image used for artifact downloads when
// not overridden per-resource. MUST be set at build time via ldflags to match
// the release tag (see the Makefile's LDFLAGS or the Dockerfile). An empty
// default is intentional: a binary built without that ldflag would otherwise
// silently fall back to a rolling `:latest` tag, which on nodes with
// `imagePullPolicy: IfNotPresent` can resolve to an arbitrarily stale cached
// layer. The operator startup validation in cmd/main.go refuses to run when
// this value is empty so the misconfiguration is caught immediately.
var DefaultDownloadImage = ""

const (
	// ArtifactSourceURIIndexKey is the field index key for AIMArtifact.Spec.SourceURI
	ArtifactSourceURIIndexKey = ".spec.sourceUri"

	// ArtifactParentIndexKey is the field index key for AIMArtifact.Spec.ParentArtifact.
	// Used to enqueue adapter artifacts when their parent model artifact changes.
	ArtifactParentIndexKey = ".spec.parentArtifact"
)

// AIMArtifactType discriminates a model artifact from a LoRA adapter artifact.
// +kubebuilder:validation:Enum=model;adapter
type AIMArtifactType string

const (
	// ArtifactTypeModel is a base model artifact backed by a cache PVC. This is the
	// default and matches the behavior of artifacts created before adapters existed.
	ArtifactTypeModel AIMArtifactType = "model"

	// ArtifactTypeAdapter is a LoRA adapter definition. Adapter artifacts do not get
	// their own cache PVC; their bytes are staged per-consuming-service into the
	// parent model artifact's adapter disk.
	ArtifactTypeAdapter AIMArtifactType = "adapter"
)

// Adapter-related condition reasons surfaced on AIMArtifact:type=adapter.
const (
	// ArtifactReasonParentNotFound indicates the referenced parentArtifact does not exist.
	ArtifactReasonParentNotFound = "ParentArtifactNotFound"
	// ArtifactReasonParentNotModel indicates the referenced parentArtifact is not type=model.
	ArtifactReasonParentNotModel = "ParentArtifactNotModel"
	// ArtifactReasonParentLacksAdapterDisk indicates the parent model artifact has no adapterDisk.
	ArtifactReasonParentLacksAdapterDisk = "ParentLacksAdapterDisk"
	// ArtifactReasonAdapterValidated indicates an adapter's source and lineage are validated.
	ArtifactReasonAdapterValidated = "AdapterValidated"
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

const (
	// ArtifactConditionStorageQuotaExceeded is True when the artifact cannot create
	// its PVC because doing so would exceed the namespace or cluster storage quota.
	ArtifactConditionStorageQuotaExceeded = "StorageQuotaExceeded"

	ArtifactReasonNamespaceQuotaExceeded = "NamespaceQuotaExceeded"
	ArtifactReasonClusterQuotaExceeded   = "ClusterQuotaExceeded"
	ArtifactReasonEvicting               = "Evicting"
	ArtifactReasonWithinQuota            = "WithinQuota"

	// ArtifactStorageQuotaAnnotation is the namespace annotation key for per-namespace
	// artifact storage quota. Overrides DefaultNamespaceLimit from AIMClusterRuntimeConfig.
	ArtifactStorageQuotaAnnotation = "aim.eai.amd.com/artifact-storage-quota"

	// ArtifactEvictionProtectedAnnotation, when set to ArtifactEvictionProtectedValue
	// on an AIMArtifact, protects it from automatic eviction regardless of
	// defaultRetentionPriority or spec.retentionPriority. Use this to exempt specific
	// artifacts from eviction when a cluster/namespace-wide defaultRetentionPriority
	// is configured.
	ArtifactEvictionProtectedAnnotation = "aim.eai.amd.com/eviction-protected"
	ArtifactEvictionProtectedValue      = "true"
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

// AIMAdapterDisk configures the shared, RWX adapter disk provisioned alongside a
// model artifact. When present on a type=model artifact, the controller provisions
// a second PersistentVolumeClaim (ReadWriteMany) owned by the model artifact and
// shared by every AIMService that serves adapters on this base model.
type AIMAdapterDisk struct {
	// Size is the requested size of the adapter disk PVC.
	// Defaults to 50Gi when unset (a cascade default may override it).
	// +optional
	Size resource.Quantity `json:"size,omitempty"`

	// StorageClassName specifies the storage class for the adapter disk.
	// When empty, the cluster default storage class is used.
	// The access mode is fixed at ReadWriteMany by the controller.
	// +optional
	StorageClassName string `json:"storageClassName,omitempty"`
}

// AIMArtifactSpec defines the desired state of AIMArtifact
type AIMArtifactSpec struct {
	// Type discriminates a base model artifact (`model`) from a LoRA adapter
	// definition (`adapter`). Defaults to `model`; immutable after creation.
	// Adapter artifacts require parentArtifact and modelId, and do not get their
	// own cache PVC.
	// +optional
	// +kubebuilder:default=model
	Type AIMArtifactType `json:"type,omitempty"`

	// SourceURI specifies the source location of the model to download.
	// Supported protocols: hf:// (HuggingFace) and s3:// (S3-compatible storage).
	// This field uniquely identifies the artifact and is immutable after creation.
	// Example: hf://meta-llama/Llama-3-8B
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="sourceUri is immutable"
	// +kubebuilder:validation:Pattern=`^(hf|s3)://[^ \t\r\n]+$`
	SourceURI string `json:"sourceUri"`

	// ParentArtifact names the base model AIMArtifact (type=model) this adapter is
	// compatible with. Required and only allowed when type=adapter; immutable.
	// The adapter is owned by (cascade-deleted with) the parent. Compatibility is
	// keyed on the parent's modelId.
	// +optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="parentArtifact is immutable"
	ParentArtifact string `json:"parentArtifact,omitempty"`

	// Rank is the LoRA rank of the adapter. Optional; only meaningful when type=adapter.
	// +optional
	// +kubebuilder:validation:Minimum=1
	Rank *int32 `json:"rank,omitempty"`

	// AdapterDisk, when set on a type=model artifact, provisions a shared ReadWriteMany
	// adapter disk owned by this model artifact and partitioned per consuming service.
	// Only allowed when type=model.
	// +optional
	AdapterDisk *AIMAdapterDisk `json:"adapterDisk,omitempty"`

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
	// When not specified, the controller uses its built-in default (matching the release version).
	// +optional
	ModelDownloadImage string `json:"modelDownloadImage,omitempty"`

	// DownloadFilter controls which files are included or excluded when downloading from HuggingFace.
	// Overrides any filter set in the runtime config's storage.downloadFilter.
	// When neither is set, subdirectory files are excluded by default (equivalent to exclude: ["*/*"]).
	// To download all files including subdirectories, set this to an empty object: downloadFilter: {}.
	// This field is immutable — to change the filter, recreate the artifact.
	// +optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="downloadFilter is immutable"
	DownloadFilter *AIMDownloadFilter `json:"downloadFilter,omitempty"`

	// ImagePullSecrets references secrets for pulling AIM container images.
	// +optional
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// RetentionPriority marks this artifact as eligible for automatic eviction
	// when storage quota is exceeded. Lower values are evicted first.
	// Artifacts without this field are only evictable if a defaultRetentionPriority
	// is configured in the runtime config. Use the aim.eai.amd.com/eviction-protected
	// annotation to exempt an artifact from eviction entirely.
	// +optional
	// +kubebuilder:validation:Minimum=0
	RetentionPriority *int32 `json:"retentionPriority,omitempty"`

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

	// ResolvedSourceURI is the effective download source after cache resolution.
	// When the S3 artifact cache has a hit, this contains the rewritten s3:// URI.
	// When empty, spec.sourceUri is used directly.
	// +optional
	ResolvedSourceURI string `json:"resolvedSourceUri,omitempty"`

	// AdapterPersistentVolumeClaim is the name of the shared adapter disk PVC
	// provisioned for a type=model artifact that declares an adapterDisk. Empty
	// otherwise.
	// +optional
	AdapterPersistentVolumeClaim string `json:"adapterPersistentVolumeClaim,omitempty"`

	// AdapterPath is the resolved on-disk directory name for a type=adapter artifact,
	// frozen at first resolution (defaults to metadata.name). This is the canonical
	// copy, mirrored into consuming services' status.
	// +optional
	AdapterPath string `json:"adapterPath,omitempty"`

	// ResolvedParent captures the resolved parent model artifact for a type=adapter
	// artifact, including its UID.
	// +optional
	ResolvedParent *AIMResolvedReference `json:"resolvedParent,omitempty"`

	// ParentModelID is the parent model artifact's modelId, denormalized onto the
	// adapter for convenience (refreshed each reconcile).
	// +optional
	ParentModelID string `json:"parentModelId,omitempty"`
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
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`,priority=1
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.status`
// +kubebuilder:printcolumn:name="Mode",type=string,JSONPath=`.status.mode`
// +kubebuilder:printcolumn:name="Model Size",type=string,JSONPath=`.status.displaySize`
// +kubebuilder:printcolumn:name="Progress",type=string,JSONPath=`.status.progress.displayPercentage`
// +kubebuilder:printcolumn:name="Protocol",type=string,JSONPath=`.status.download.protocol`,priority=1
// +kubebuilder:printcolumn:name="Attempt",type=string,JSONPath=`.status.download.attempt`,priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:validation:XValidation:rule="!has(self.spec.type) || !oldSelf.hasValue() || !has(oldSelf.value().spec.type) || self.spec.type == oldSelf.value().spec.type",message="spec.type is immutable",optionalOldSelf=true
// +kubebuilder:validation:XValidation:rule="!has(self.spec.type) || self.spec.type != 'adapter' || has(self.spec.parentArtifact)",message="spec.parentArtifact is required when spec.type is adapter"
// +kubebuilder:validation:XValidation:rule="!has(self.spec.type) || self.spec.type != 'adapter' || (has(self.spec.modelId) && size(self.spec.modelId) > 0)",message="spec.modelId is required when spec.type is adapter"
// +kubebuilder:validation:XValidation:rule="!has(self.spec.parentArtifact) || (has(self.spec.type) && self.spec.type == 'adapter')",message="spec.parentArtifact is only allowed when spec.type is adapter"
// +kubebuilder:validation:XValidation:rule="!has(self.spec.adapterDisk) || !has(self.spec.type) || self.spec.type == 'model'",message="spec.adapterDisk is only allowed when spec.type is model"
// +kubebuilder:validation:XValidation:rule="!has(self.spec.rank) || (has(self.spec.type) && self.spec.type == 'adapter')",message="spec.rank is only allowed when spec.type is adapter"

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
