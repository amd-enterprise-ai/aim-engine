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

package aimservice

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
)

// ============================================================================
// TEST CONTEXT
// ============================================================================

const (
	testNamespace  = "test-ns"
	testServiceUID = "test-service-uid"
)

// ============================================================================
// BUILDERS - AIMService
// ============================================================================

// ServiceBuilder provides a fluent API for constructing AIMService test fixtures.
type ServiceBuilder struct {
	service *aimv1alpha1.AIMService
}

// NewService creates a new ServiceBuilder with sensible defaults.
func NewService(name string) *ServiceBuilder {
	return &ServiceBuilder{
		service: &aimv1alpha1.AIMService{
			TypeMeta: metav1.TypeMeta{
				APIVersion: aimv1alpha1.GroupVersion.String(),
				Kind:       "AIMService",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: testNamespace,
				UID:       testServiceUID,
			},
			Spec: aimv1alpha1.AIMServiceSpec{},
		},
	}
}

func (b *ServiceBuilder) WithNamespace(ns string) *ServiceBuilder {
	b.service.Namespace = ns
	return b
}

func (b *ServiceBuilder) WithModelImage(image string) *ServiceBuilder {
	if b.service.Spec.Model == nil {
		b.service.Spec.Model = &aimv1alpha1.AIMServiceModel{}
	}
	b.service.Spec.Model.Image = ptr.To(image)
	return b
}

func (b *ServiceBuilder) Build() *aimv1alpha1.AIMService {
	return b.service.DeepCopy()
}

// v1alpha2View presents a v1alpha1 fixture to helpers that take a v1alpha2
// service. The copy is shallow, so field assignments made through the returned
// service are not seen by the caller.
func v1alpha2View(service *aimv1alpha1.AIMService) *aimv1alpha2.AIMService {
	if service == nil {
		return nil
	}
	return &aimv1alpha2.AIMService{
		TypeMeta:   service.TypeMeta,
		ObjectMeta: service.ObjectMeta,
		Spec:       service.Spec,
		Status:     service.Status,
	}
}
