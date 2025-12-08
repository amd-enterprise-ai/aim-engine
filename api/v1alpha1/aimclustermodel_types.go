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
	// ClusterModelImageIndexKey is the field index key for AIMClusterModel.Spec.Image
	ClusterModelImageIndexKey = ".spec.image"
)

// AIMClusterModel is a cluster-scoped model catalog entry for AIM container images.
//
// Cluster-scoped models can be referenced by AIMServices in any namespace, making them ideal for
// shared model deployments across teams and projects. Like namespace-scoped AIMModels, cluster models
// trigger discovery jobs to extract metadata and generate service templates.
//
// When both cluster and namespace models exist for the same container image, services will preferentially
// use the namespace-scoped AIMModel when referenced by image URI.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=aimclmdl,categories=aim;all
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.status`
// +kubebuilder:printcolumn:name="Image",type=string,JSONPath=`.spec.image`
// +kubebuilder:printcolumn:name="Model",type=string,JSONPath=`.status.imageMetadata.model.canonicalName`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type AIMClusterModel struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AIMModelSpec   `json:"spec,omitempty"`
	Status AIMModelStatus `json:"status,omitempty"`
}

// AIMClusterModelList contains a list of AIMClusterModel.
// +kubebuilder:object:root=true
type AIMClusterModelList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AIMClusterModel `json:"items"`
}

func (img *AIMClusterModel) GetStatus() *AIMModelStatus {
	return &img.Status
}

func init() {
	SchemeBuilder.Register(&AIMClusterModel{}, &AIMClusterModelList{})
}
