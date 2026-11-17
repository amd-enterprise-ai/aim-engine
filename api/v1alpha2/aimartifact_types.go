// MIT License
//
// Copyright (c) 2026 Advanced Micro Devices, Inc.
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

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
)

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
// +kubebuilder:validation:XValidation:rule="has(self.spec.downloadFilter) == has(oldSelf.spec.downloadFilter) && (!has(self.spec.downloadFilter) || self.spec.downloadFilter == oldSelf.spec.downloadFilter)",message="spec.downloadFilter is immutable"
// +kubebuilder:validation:XValidation:rule="!has(self.spec.type) || self.spec.type != 'adapter' || ((has(self.spec.parentArtifact) && !has(self.spec.compatibleWith)) || (!has(self.spec.parentArtifact) && has(self.spec.compatibleWith)))",message="exactly one of spec.parentArtifact or spec.compatibleWith is required when spec.type is adapter"
// +kubebuilder:validation:XValidation:rule="!has(self.spec.type) || self.spec.type != 'adapter' || (has(self.spec.modelId) && size(self.spec.modelId) > 0)",message="spec.modelId is required when spec.type is adapter"
// +kubebuilder:validation:XValidation:rule="!has(self.spec.parentArtifact) || (has(self.spec.type) && self.spec.type == 'adapter')",message="spec.parentArtifact is only allowed when spec.type is adapter"
// +kubebuilder:validation:XValidation:rule="!has(self.spec.compatibleWith) || (has(self.spec.type) && self.spec.type == 'adapter')",message="spec.compatibleWith is only allowed when spec.type is adapter"
// +kubebuilder:validation:XValidation:rule="!has(self.spec.adapterDisk) || !has(self.spec.type) || self.spec.type == 'model'",message="spec.adapterDisk is only allowed when spec.type is model"
// +kubebuilder:validation:XValidation:rule="!has(self.spec.rank) || (has(self.spec.type) && self.spec.type == 'adapter')",message="spec.rank is only allowed when spec.type is adapter"

// AIMArtifact is the Schema for the artifacts API
type AIMArtifact struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   aimv1alpha1.AIMArtifactSpec   `json:"spec,omitempty"`
	Status aimv1alpha1.AIMArtifactStatus `json:"status,omitempty"`
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
