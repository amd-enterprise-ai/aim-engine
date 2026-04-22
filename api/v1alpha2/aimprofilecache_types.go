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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

const (
	// ProfileCacheProfileNameIndexKey is the field index key for AIMProfileCache.Spec.ProfileName.
	ProfileCacheProfileNameIndexKey = ".spec.profileName"
	// ProfileCacheProfileScopeIndexKey is the field index key for AIMProfileCache.Spec.ProfileScope.
	ProfileCacheProfileScopeIndexKey = ".spec.profileScope"
)

// AIMProfileCacheMode controls the ownership behavior of artifacts created by a profile cache.
// +kubebuilder:validation:Enum=Dedicated;Shared
type AIMProfileCacheMode string

const (
	// ProfileCacheModeDedicated means artifacts are owned by this profile cache and
	// garbage collected when it is deleted.
	ProfileCacheModeDedicated AIMProfileCacheMode = "Dedicated"

	// ProfileCacheModeShared means artifacts have no owner references and persist
	// independently of the profile cache lifecycle. This is the default mode.
	ProfileCacheModeShared AIMProfileCacheMode = "Shared"
)

// AIMProfileCacheSpec defines the desired state of AIMProfileCache.
type AIMProfileCacheSpec struct {
	// ProfileName is the name of the AIMProfile or AIMClusterProfile to cache.
	// The controller resolves model sources from the referenced profile's spec.modelSources.
	// +kubebuilder:validation:MinLength=1
	ProfileName string `json:"profileName"`

	// ProfileScope indicates whether the profile is namespace-scoped or cluster-scoped.
	// +required
	// +kubebuilder:validation:Enum=Namespace;Cluster
	ProfileScope aimv1alpha1.AIMResolutionScope `json:"profileScope"`

	// StorageClassName specifies the storage class for cache volumes.
	// When not specified, uses the cluster default storage class.
	// +optional
	StorageClassName string `json:"storageClassName,omitempty"`

	// Env specifies environment variables for authentication when downloading models.
	// These variables are used for authentication with model registries (e.g., HuggingFace tokens).
	// +optional
	// +listType=map
	// +listMapKey=name
	Env []corev1.EnvVar `json:"env,omitempty"`

	// Mode controls the ownership behavior of artifacts created by this profile cache.
	// - Dedicated: artifacts are owned by this profile cache and garbage collected when it's deleted.
	// - Shared (default): artifacts have no owner references and persist independently.
	// +kubebuilder:default=Shared
	// +optional
	Mode AIMProfileCacheMode `json:"mode,omitempty"`
}

// AIMProfileCacheStatus defines the observed state of AIMProfileCache.
type AIMProfileCacheStatus struct {
	// ObservedGeneration is the most recent generation observed by the controller.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions represent the latest observations of the profile cache state.
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Status represents the current high-level status of the profile cache.
	// +kubebuilder:default=Pending
	// +kubebuilder:validation:Enum=Pending;Progressing;Ready;Failed;Degraded;NotAvailable
	Status constants.AIMStatus `json:"status,omitempty"`

	// Artifacts maps artifact names to their resolved AIMArtifact resources.
	// +optional
	Artifacts map[string]aimv1alpha1.AIMResolvedArtifact `json:"artifacts,omitempty"`
}

func (s *AIMProfileCacheStatus) GetConditions() []metav1.Condition {
	return s.Conditions
}

func (s *AIMProfileCacheStatus) SetConditions(conditions []metav1.Condition) {
	s.Conditions = conditions
}

func (s *AIMProfileCacheStatus) SetStatus(status string) {
	s.Status = constants.AIMStatus(status)
}

func (s *AIMProfileCacheStatus) GetAIMStatus() constants.AIMStatus {
	return s.Status
}

// Condition types for AIMProfileCache.
const (
	AIMProfileCacheConditionProfileFound   = "ProfileFound"
	AIMProfileCacheConditionArtifactsReady = "ArtifactsReady"
)

// Condition reasons for AIMProfileCache.
const (
	AIMProfileCacheReasonProfileNotFound = "ProfileNotFound"
	AIMProfileCacheReasonProfileResolved = "ProfileResolved"
	AIMProfileCacheReasonCreatingCaches  = "CreatingCaches"
	AIMProfileCacheReasonAllCachesReady  = "AllCachesReady"
	AIMProfileCacheReasonCachesNotReady  = "CachesNotReady"
	AIMProfileCacheReasonNoCaches        = "NoCaches"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=aimpc,categories=aim;all
// +kubebuilder:printcolumn:name="Profile",type=string,JSONPath=`.spec.profileName`
// +kubebuilder:printcolumn:name="Scope",type=string,JSONPath=`.spec.profileScope`
// +kubebuilder:printcolumn:name="Mode",type=string,JSONPath=`.spec.mode`
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// AIMProfileCache pre-warms model artifacts for a specified profile's model sources.
type AIMProfileCache struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AIMProfileCacheSpec   `json:"spec,omitempty"`
	Status AIMProfileCacheStatus `json:"status,omitempty"`
}

func (pc *AIMProfileCache) GetStatus() *AIMProfileCacheStatus {
	return &pc.Status
}

// +kubebuilder:object:root=true
// AIMProfileCacheList contains a list of AIMProfileCache.
type AIMProfileCacheList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AIMProfileCache `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AIMProfileCache{}, &AIMProfileCacheList{})
}
