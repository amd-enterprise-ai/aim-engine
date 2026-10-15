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

// AIMClusterComposition publishes a reusable cluster-scoped serving recipe.
// Exact profile references in a cluster recipe must target AIMClusterProfile;
// selectors may still resolve namespace profiles when a service instantiates
// the recipe.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=aimclcomp,categories=aim;all
// +kubebuilder:printcolumn:name="Mode",type=string,JSONPath=`.status.resolvedMode`
// +kubebuilder:printcolumn:name="Model Binding",type=string,JSONPath=`.status.modelBindingType`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:validation:XValidation:rule="!has(self.spec.main) || !has(self.spec.main.profile) || !has(self.spec.main.profile.ref) || self.spec.main.profile.ref.kind == 'AIMClusterProfile'",message="cluster composition main profile refs must target AIMClusterProfile"
// +kubebuilder:validation:XValidation:rule="!has(self.spec.main) || !has(self.spec.main.worker) || !has(self.spec.main.worker.profile) || !has(self.spec.main.worker.profile.ref) || self.spec.main.worker.profile.ref.kind == 'AIMClusterProfile'",message="cluster composition main worker profile refs must target AIMClusterProfile"
// +kubebuilder:validation:XValidation:rule="!has(self.spec.prefill) || !has(self.spec.prefill.profile) || !has(self.spec.prefill.profile.ref) || self.spec.prefill.profile.ref.kind == 'AIMClusterProfile'",message="cluster composition prefill profile refs must target AIMClusterProfile"
// +kubebuilder:validation:XValidation:rule="!has(self.spec.prefill) || !has(self.spec.prefill.worker) || !has(self.spec.prefill.worker.profile) || !has(self.spec.prefill.worker.profile.ref) || self.spec.prefill.worker.profile.ref.kind == 'AIMClusterProfile'",message="cluster composition prefill worker profile refs must target AIMClusterProfile"
// +kubebuilder:validation:XValidation:rule="!has(self.spec.decode) || !has(self.spec.decode.profile) || !has(self.spec.decode.profile.ref) || self.spec.decode.profile.ref.kind == 'AIMClusterProfile'",message="cluster composition decode profile refs must target AIMClusterProfile"
// +kubebuilder:validation:XValidation:rule="!has(self.spec.decode) || !has(self.spec.decode.worker) || !has(self.spec.decode.worker.profile) || !has(self.spec.decode.worker.profile.ref) || self.spec.decode.worker.profile.ref.kind == 'AIMClusterProfile'",message="cluster composition decode worker profile refs must target AIMClusterProfile"
//
//nolint:lll // kubebuilder markers; CEL rules cannot be wrapped across lines
type AIMClusterComposition struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   aimv1alpha1.AIMCompositionSpec   `json:"spec,omitempty"`
	Status aimv1alpha1.AIMCompositionStatus `json:"status,omitempty"`
}

// AIMClusterCompositionList contains a list of AIMClusterComposition resources.
// +kubebuilder:object:root=true
type AIMClusterCompositionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AIMClusterComposition `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AIMClusterComposition{}, &AIMClusterCompositionList{})
}
