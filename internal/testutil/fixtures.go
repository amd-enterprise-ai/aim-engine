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

package testutil

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

// RuntimeConfig fixtures

type RuntimeConfigOption func(*aimv1alpha1.AIMRuntimeConfig)

func NewRuntimeConfig(opts ...RuntimeConfigOption) *aimv1alpha1.AIMRuntimeConfig {
	rc := &aimv1alpha1.AIMRuntimeConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default",
			Namespace: "default",
		},
		Spec: aimv1alpha1.AIMRuntimeConfigSpec{},
	}
	for _, opt := range opts {
		opt(rc)
	}
	return rc
}

func WithRuntimeConfigName(name string) RuntimeConfigOption {
	return func(rc *aimv1alpha1.AIMRuntimeConfig) {
		rc.Name = name
	}
}

func WithRuntimeConfigNamespace(namespace string) RuntimeConfigOption {
	return func(rc *aimv1alpha1.AIMRuntimeConfig) {
		rc.Namespace = namespace
	}
}

// Model fixtures

type ModelOption func(*aimv1alpha1.AIMModel)

func NewModel(opts ...ModelOption) *aimv1alpha1.AIMModel {
	model := &aimv1alpha1.AIMModel{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-model",
			Namespace: "default",
		},
		Spec: aimv1alpha1.AIMModelSpec{
			Image: "ghcr.io/test/model:latest",
		},
		Status: aimv1alpha1.AIMModelStatus{
			Status: constants.AIMStatusReady,
		},
	}
	for _, opt := range opts {
		opt(model)
	}
	return model
}

func WithModelName(name string) ModelOption {
	return func(m *aimv1alpha1.AIMModel) {
		m.Name = name
	}
}

func WithModelNamespace(namespace string) ModelOption {
	return func(m *aimv1alpha1.AIMModel) {
		m.Namespace = namespace
	}
}

func WithModelImage(image string) ModelOption {
	return func(m *aimv1alpha1.AIMModel) {
		m.Spec.Image = image
	}
}

func WithModelStatus(status constants.AIMStatus) ModelOption {
	return func(m *aimv1alpha1.AIMModel) {
		m.Status.Status = status
	}
}

func WithModelMetadata(metadata *aimv1alpha1.ImageMetadata) ModelOption {
	return func(m *aimv1alpha1.AIMModel) {
		m.Status.ImageMetadata = metadata
	}
}

// Template fixtures

type TemplateOption func(*aimv1alpha1.AIMServiceTemplate)

func NewTemplate(opts ...TemplateOption) *aimv1alpha1.AIMServiceTemplate {
	template := &aimv1alpha1.AIMServiceTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-template",
			Namespace: "default",
		},
		Spec: aimv1alpha1.AIMServiceTemplateSpec{
			AIMServiceTemplateSpecCommon: aimv1alpha1.AIMServiceTemplateSpecCommon{
				ModelName: "test-model",
			},
		},
		Status: aimv1alpha1.AIMServiceTemplateStatus{
			Status: constants.AIMStatusReady,
		},
	}
	for _, opt := range opts {
		opt(template)
	}
	return template
}

func WithTemplateName(name string) TemplateOption {
	return func(t *aimv1alpha1.AIMServiceTemplate) {
		t.Name = name
	}
}

func WithTemplateNamespace(namespace string) TemplateOption {
	return func(t *aimv1alpha1.AIMServiceTemplate) {
		t.Namespace = namespace
	}
}

func WithTemplateModelName(modelName string) TemplateOption {
	return func(t *aimv1alpha1.AIMServiceTemplate) {
		t.Spec.ModelName = modelName
	}
}

func WithTemplateStatus(status constants.AIMStatus) TemplateOption {
	return func(t *aimv1alpha1.AIMServiceTemplate) {
		t.Status.Status = status
	}
}

func WithTemplateModelSources(sources []aimv1alpha1.AIMModelSource) TemplateOption {
	return func(t *aimv1alpha1.AIMServiceTemplate) {
		t.Status.ModelSources = sources
	}
}

// Service fixtures

type ServiceOption func(*aimv1alpha1.AIMService)

func NewService(opts ...ServiceOption) *aimv1alpha1.AIMService {
	svc := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-service",
			Namespace: "default",
		},
		Spec: aimv1alpha1.AIMServiceSpec{
			Model: aimv1alpha1.AIMServiceModel{
				Name: ptr.To("test-model"),
			},
			Template: aimv1alpha1.AIMServiceTemplateConfig{
				Name: "test-template",
			},
			Replicas: ptr.To(int32(1)),
		},
		Status: aimv1alpha1.AIMServiceStatus{
			Status: constants.AIMStatusPending,
		},
	}
	for _, opt := range opts {
		opt(svc)
	}
	return svc
}

func WithServiceName(name string) ServiceOption {
	return func(s *aimv1alpha1.AIMService) {
		s.Name = name
	}
}

func WithServiceNamespace(namespace string) ServiceOption {
	return func(s *aimv1alpha1.AIMService) {
		s.Namespace = namespace
	}
}

func WithServiceModelRef(modelRef string) ServiceOption {
	return func(s *aimv1alpha1.AIMService) {
		s.Spec.Model.Name = &modelRef
	}
}

func WithServiceTemplate(templateName string) ServiceOption {
	return func(s *aimv1alpha1.AIMService) {
		s.Spec.Template.Name = templateName
	}
}

func WithServiceStatus(status constants.AIMStatus) ServiceOption {
	return func(s *aimv1alpha1.AIMService) {
		s.Status.Status = status
	}
}

// ModelCache fixtures

type ModelCacheOption func(*aimv1alpha1.AIMModelCache)

func NewModelCache(opts ...ModelCacheOption) *aimv1alpha1.AIMModelCache {
	mc := &aimv1alpha1.AIMModelCache{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cache",
			Namespace: "default",
		},
		Spec: aimv1alpha1.AIMModelCacheSpec{
			SourceURI:          "hf://test/model",
			Size:               resource.MustParse("10Gi"),
			ModelDownloadImage: "kserve/storage-initializer:v0.16.0-rc0",
		},
		Status: aimv1alpha1.AIMModelCacheStatus{
			Status: constants.AIMStatusPending,
		},
	}
	for _, opt := range opts {
		opt(mc)
	}
	return mc
}

func WithModelCacheName(name string) ModelCacheOption {
	return func(mc *aimv1alpha1.AIMModelCache) {
		mc.Name = name
	}
}

func WithModelCacheNamespace(namespace string) ModelCacheOption {
	return func(mc *aimv1alpha1.AIMModelCache) {
		mc.Namespace = namespace
	}
}

func WithModelCacheSourceURI(uri string) ModelCacheOption {
	return func(mc *aimv1alpha1.AIMModelCache) {
		mc.Spec.SourceURI = uri
	}
}

func WithModelCacheStatus(status constants.AIMStatus) ModelCacheOption {
	return func(mc *aimv1alpha1.AIMModelCache) {
		mc.Status.Status = status
	}
}

// TemplateCache fixtures

type TemplateCacheOption func(*aimv1alpha1.AIMTemplateCache)

func NewTemplateCache(opts ...TemplateCacheOption) *aimv1alpha1.AIMTemplateCache {
	tc := &aimv1alpha1.AIMTemplateCache{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-template-cache",
			Namespace: "default",
		},
		Spec: aimv1alpha1.AIMTemplateCacheSpec{
			TemplateName:  "test-template",
			TemplateScope: aimv1alpha1.AIMServiceTemplateScopeNamespace,
		},
		Status: aimv1alpha1.AIMTemplateCacheStatus{
			Status: constants.AIMStatusPending,
		},
	}
	for _, opt := range opts {
		opt(tc)
	}
	return tc
}

func WithTemplateCacheName(name string) TemplateCacheOption {
	return func(tc *aimv1alpha1.AIMTemplateCache) {
		tc.Name = name
	}
}

func WithTemplateCacheNamespace(namespace string) TemplateCacheOption {
	return func(tc *aimv1alpha1.AIMTemplateCache) {
		tc.Namespace = namespace
	}
}

func WithTemplateCacheTemplate(templateName string) TemplateCacheOption {
	return func(tc *aimv1alpha1.AIMTemplateCache) {
		tc.Spec.TemplateName = templateName
	}
}

func WithTemplateCacheStatus(status constants.AIMStatus) TemplateCacheOption {
	return func(tc *aimv1alpha1.AIMTemplateCache) {
		tc.Status.Status = status
	}
}

// PVC fixtures

type PVCOption func(*corev1.PersistentVolumeClaim)

func NewPVC(opts ...PVCOption) *corev1.PersistentVolumeClaim {
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pvc",
			Namespace: "default",
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse("10Gi"),
				},
			},
		},
		Status: corev1.PersistentVolumeClaimStatus{
			Phase: corev1.ClaimPending,
		},
	}
	for _, opt := range opts {
		opt(pvc)
	}
	return pvc
}

func WithPVCName(name string) PVCOption {
	return func(pvc *corev1.PersistentVolumeClaim) {
		pvc.Name = name
	}
}

func WithPVCNamespace(namespace string) PVCOption {
	return func(pvc *corev1.PersistentVolumeClaim) {
		pvc.Namespace = namespace
	}
}

func WithPVCPhase(phase corev1.PersistentVolumeClaimPhase) PVCOption {
	return func(pvc *corev1.PersistentVolumeClaim) {
		pvc.Status.Phase = phase
	}
}
