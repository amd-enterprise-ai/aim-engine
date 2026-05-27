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

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
)

// AIMClusterModel is the Schema for cluster-scoped v1alpha2 AIM model resources.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:resource:scope=Cluster,shortName=aimclmdl,categories=aim;all
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.status`
// +kubebuilder:printcolumn:name="Kind",type=string,JSONPath=`.status.kind`
// +kubebuilder:printcolumn:name="AimID",type=string,JSONPath=`.status.aimId`
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.status.version`
// +kubebuilder:printcolumn:name="Managed",type=integer,JSONPath=`.status.managedProfiles.total`
// +kubebuilder:printcolumn:name="Ready",type=integer,JSONPath=`.status.managedProfiles.ready`
// +kubebuilder:printcolumn:name="Base",type=integer,JSONPath=`.status.managedProfiles.base`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// See AIMModel (api/v1alpha2/aimmodel_types.go) for the rationale of the
// CEL rules below — AIMClusterModel mirrors the namespace-scoped contract.
// +kubebuilder:validation:XValidation:rule="(has(self.spec.image) && size(self.spec.image) > 0) != (has(self.spec.profiles) || has(self.spec.derivedFrom)) || (oldSelf.hasValue() && !((has(oldSelf.value().spec.image) && size(oldSelf.value().spec.image) > 0) != (has(oldSelf.value().spec.profiles) || has(oldSelf.value().spec.derivedFrom))))",message="exactly one of spec.image or spec.profiles must be set on v1alpha2",optionalOldSelf=true
// +kubebuilder:validation:XValidation:rule="!has(self.spec.profileCopy) || (oldSelf.hasValue() && has(oldSelf.value().spec.profileCopy))",message="spec.profileCopy is forbidden on v1alpha2; use spec.profiles",optionalOldSelf=true
// +kubebuilder:validation:XValidation:rule="!has(self.spec.derivedFrom) || (oldSelf.hasValue() && has(oldSelf.value().spec.derivedFrom))",message="spec.derivedFrom is deprecated on v1alpha2; use spec.profiles",optionalOldSelf=true
// +kubebuilder:validation:XValidation:rule="!has(self.spec.custom) || (oldSelf.hasValue() && has(oldSelf.value().spec.custom))",message="spec.custom is forbidden on v1alpha2",optionalOldSelf=true
// +kubebuilder:validation:XValidation:rule="!has(self.spec.customTemplates) || size(self.spec.customTemplates) == 0 || (oldSelf.hasValue() && has(oldSelf.value().spec.customTemplates) && size(oldSelf.value().spec.customTemplates) > 0)",message="spec.customTemplates is forbidden on v1alpha2",optionalOldSelf=true
// +kubebuilder:validation:XValidation:rule="!has(self.spec.modelSources) || size(self.spec.modelSources) == 0 || (oldSelf.hasValue() && has(oldSelf.value().spec.modelSources) && size(oldSelf.value().spec.modelSources) > 0)",message="spec.modelSources is forbidden on v1alpha2; use spec.profiles.overrides.modelSources",optionalOldSelf=true
// +kubebuilder:validation:XValidation:rule="(!has(self.spec.profiles) && !has(self.spec.derivedFrom)) || (!has(self.spec.aimId) && !has(self.spec.discovery) && !has(self.spec.defaultServiceTemplate) && !has(self.spec.runtimeConfigName) && !has(self.spec.env) && !has(self.spec.imageMetadata))",message="spec.profiles cannot be combined with spec.aimId, spec.discovery, spec.defaultServiceTemplate, spec.runtimeConfigName, spec.env, or spec.imageMetadata"
//
//nolint:lll // kubebuilder marker; CEL rules cannot be wrapped across lines
type AIMClusterModel struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   aimv1alpha1.AIMModelSpec   `json:"spec,omitempty"`
	Status aimv1alpha1.AIMModelStatus `json:"status,omitempty"`
}

func (m *AIMClusterModel) GetStatus() *aimv1alpha1.AIMModelStatus {
	return &m.Status
}

// AIMClusterModelList contains a list of AIMClusterModel.
// +kubebuilder:object:root=true
type AIMClusterModelList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AIMClusterModel `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AIMClusterModel{}, &AIMClusterModelList{})
}
