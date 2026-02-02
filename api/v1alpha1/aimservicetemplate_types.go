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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// ServiceTemplateModelNameIndexKey is the field index key for AIMServiceTemplate.Spec.ModelName
	// This is also used for AIMClusterServiceTemplate.Spec.ModelName
	ServiceTemplateModelNameIndexKey = ".spec.modelName"
)

// AIMServiceTemplate is the Schema for namespace-scoped AIM service templates.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=aimtpl,categories=aim;all
// +kubebuilder:printcolumn:name="Model",type=string,JSONPath=`.spec.modelName`
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.status`
// +kubebuilder:printcolumn:name="Metric",type=string,JSONPath=`.status.profile.metadata.metric`
// +kubebuilder:printcolumn:name="Precision",type=string,JSONPath=`.status.profile.metadata.precision`
// +kubebuilder:printcolumn:name="GPUs",type=integer,JSONPath=`.status.resolvedHardware.gpu.requests`
type AIMServiceTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AIMServiceTemplateSpec   `json:"spec,omitempty"`
	Status AIMServiceTemplateStatus `json:"status,omitempty"`
}

// AIMServiceTemplateList contains a list of AIMServiceTemplate.
// +kubebuilder:object:root=true
type AIMServiceTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AIMServiceTemplate `json:"items"`
}

func (t *AIMServiceTemplate) GetModelName() string {
	return t.Spec.ModelName
}

func (t *AIMServiceTemplate) GetStatus() *AIMServiceTemplateStatus {
	return &t.Status
}

func (t *AIMServiceTemplate) GetSpecModelSources() []AIMModelSource {
	return t.Spec.ModelSources
}

func (t *AIMServiceTemplate) GetRuntimeConfigRef() RuntimeConfigRef {
	return t.Spec.RuntimeConfigRef
}

func init() {
	SchemeBuilder.Register(&AIMServiceTemplate{}, &AIMServiceTemplateList{})
}
