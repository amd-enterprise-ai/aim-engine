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

// AIMClusterServiceTemplate is the Schema for cluster-scoped AIM service templates.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=aimcltpl,categories=aim;all
// +kubebuilder:printcolumn:name="Model",type=string,JSONPath=`.spec.modelName`
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Metric",type=string,JSONPath=`.status.profile.metadata.metric`
// +kubebuilder:printcolumn:name="Precision",type=string,JSONPath=`.status.profile.metadata.precision`
// +kubebuilder:printcolumn:name="GPUs/replica",type=integer,JSONPath=`.status.profile.metadata.gpu_count`
// +kubebuilder:printcolumn:name="GPU",type=string,JSONPath=`.status.profile.metadata.gpu`
type AIMClusterServiceTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AIMClusterServiceTemplateSpec `json:"spec,omitempty"`
	Status AIMServiceTemplateStatus      `json:"status,omitempty"`
}

// AIMClusterServiceTemplateList contains a list of AIMClusterServiceTemplate.
// +kubebuilder:object:root=true
type AIMClusterServiceTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AIMClusterServiceTemplate `json:"items"`
}

func (t *AIMClusterServiceTemplate) GetModelName() string {
	return t.Spec.ModelName
}

func (t *AIMClusterServiceTemplate) GetStatus() *AIMServiceTemplateStatus {
	return &t.Status
}

func (t *AIMClusterServiceTemplate) GetSpecModelSources() []AIMModelSource {
	return t.Spec.ModelSources
}

func init() {
	SchemeBuilder.Register(&AIMClusterServiceTemplate{}, &AIMClusterServiceTemplateList{})
}
