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

// AIMService manages a KServe-based AIM inference service for the selected model and template.
// Note: KServe uses {name}-{namespace} format which must not exceed 63 characters.
// This constraint is validated at runtime since CEL cannot access metadata.namespace.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:resource:shortName=aimsvc,categories=aim;all
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.status`
// +kubebuilder:printcolumn:name="Model",type=string,JSONPath=`.status.resolvedModel.name`
// +kubebuilder:printcolumn:name="Profile",type=string,JSONPath=`.status.resolvedProfile.name`
// +kubebuilder:printcolumn:name="Replicas",type=string,JSONPath=`.status.runtime.replicas`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:validation:XValidation:rule="!has(self.spec.profileOverrides) || has(self.spec.profile)",message="spec.profileOverrides requires spec.profile to be set"
// +kubebuilder:validation:XValidation:rule="!has(self.spec.template) || (oldSelf.hasValue() && has(oldSelf.value().spec.template))",message="spec.template is not supported on v1alpha2; use spec.profile or spec.model",optionalOldSelf=true
// +kubebuilder:validation:XValidation:rule="!has(self.spec.overrides) || (oldSelf.hasValue() && has(oldSelf.value().spec.overrides))",message="spec.overrides is not supported on v1alpha2; use spec.profileOverrides",optionalOldSelf=true
// +kubebuilder:validation:XValidation:rule="has(self.spec.model) || has(self.spec.profile)",message="one of spec.model or spec.profile must be specified"
// +kubebuilder:validation:XValidation:rule="!has(self.spec.profile) || !has(self.spec.profile.selector) || !has(self.spec.profile.selector.role) || self.spec.profile.selector.role == 'deployable'",message="spec.profile.selector.role on v1alpha2 may only be omitted or set to deployable; base is reserved for AIMProfileSet selectors"
// +kubebuilder:validation:XValidation:rule="!has(self.spec.profile) || !has(self.spec.profile.selector) || has(self.spec.profile.selector.aimId) || has(self.spec.profile.selector.modelRef) || has(self.spec.model)",message="spec.profile.selector must narrow on aimId, modelRef.name, or be paired with spec.model.name"
type AIMService struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   aimv1alpha1.AIMServiceSpec   `json:"spec,omitempty"`
	Status aimv1alpha1.AIMServiceStatus `json:"status,omitempty"`
}

func (svc *AIMService) GetStatus() *aimv1alpha1.AIMServiceStatus {
	return &svc.Status
}

// +kubebuilder:object:root=true
// AIMServiceList contains a list of AIMService.
type AIMServiceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AIMService `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AIMService{}, &AIMServiceList{})
}
