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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AIMClusterProfileSpec defines the desired state of a cluster-scoped AIMClusterProfile.
type AIMClusterProfileSpec struct {
	AIMProfileSpecCommon `json:",inline"`
}

// AIMClusterProfile is the Schema for cluster-scoped AIM profiles.
// Cluster profiles are visible across all namespaces. They can be created manually
// or, in the future, automatically during model discovery by a v1alpha2 model controller.
// Unlike namespace-scoped AIMProfiles, cluster profiles do not support caching
// configuration since caches are namespace-scoped.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=aimclprf,categories=aim;all
// +kubebuilder:printcolumn:name="AimId",type=string,JSONPath=`.spec.aimId`
// +kubebuilder:printcolumn:name="Engine",type=string,JSONPath=`.spec.engine`
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.status`
// +kubebuilder:printcolumn:name="Origin",type=string,JSONPath=`.status.origin`
// +kubebuilder:printcolumn:name="Hardware",type=string,JSONPath=`.status.hardwareSummary`
// +kubebuilder:printcolumn:name="Metric",type=string,JSONPath=`.spec.metric`
// +kubebuilder:printcolumn:name="Precision",type=string,JSONPath=`.spec.precision`
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Partitioning",type=string,priority=1,JSONPath=`.spec.acceleratorPartitioningMode`
// +kubebuilder:printcolumn:name="Primary",type=boolean,JSONPath=`.spec.primary`
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.status.version`
// +kubebuilder:printcolumn:name="Manual",type=boolean,priority=1,JSONPath=`.spec.manualSelectionOnly`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:selectablefield:JSONPath=`.spec.aimId`
// Deployable profiles have both aimId and modelSources populated; base
// profiles (custom-model derivation source material) have neither. Mixed
// (one of the two set) is rejected to keep status.deployable derivable from
// spec.
// +kubebuilder:validation:XValidation:rule="(has(self.spec.aimId) && size(self.spec.aimId) > 0) == (has(self.spec.modelSources) && size(self.spec.modelSources) > 0)",message="spec.aimId and spec.modelSources must both be set (deployable profile) or both be empty (base profile)"
// +kubebuilder:validation:XValidation:rule="!(has(self.spec.acceleratorType) && self.spec.acceleratorType == 'cpu') || !has(self.spec.acceleratorPartitioningMode) || size(self.spec.acceleratorPartitioningMode) == 0 || self.spec.acceleratorPartitioningMode == 'unpartitioned'",message="acceleratorPartitioningMode must be 'unpartitioned' (or unset) when acceleratorType is cpu"
//
//nolint:lll // kubebuilder marker; CEL rule cannot be wrapped across lines
type AIMClusterProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AIMClusterProfileSpec `json:"spec,omitempty"`
	Status AIMProfileStatus      `json:"status,omitempty"`
}

func (p *AIMClusterProfile) GetStatus() *AIMProfileStatus {
	return &p.Status
}

// AIMClusterProfileList contains a list of AIMClusterProfile.
// +kubebuilder:object:root=true
type AIMClusterProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AIMClusterProfile `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AIMClusterProfile{}, &AIMClusterProfileList{})
}
