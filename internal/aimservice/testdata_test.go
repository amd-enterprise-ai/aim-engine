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
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

// ============================================================================
// TEST CONTEXT
// ============================================================================

const (
	testNamespace = "test-ns"
	testModelName = "test-model"
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
				UID:       "test-service-uid",
			},
			Spec: aimv1alpha1.AIMServiceSpec{},
		},
	}
}

func (b *ServiceBuilder) WithNamespace(ns string) *ServiceBuilder {
	b.service.Namespace = ns
	return b
}

func (b *ServiceBuilder) WithModelName(name string) *ServiceBuilder {
	b.service.Spec.Model.Name = ptr.To(name)
	return b
}

func (b *ServiceBuilder) WithModelImage(image string) *ServiceBuilder {
	b.service.Spec.Model.Image = ptr.To(image)
	return b
}

func (b *ServiceBuilder) WithTemplateName(name string) *ServiceBuilder {
	b.service.Spec.Template.Name = name
	return b
}

func (b *ServiceBuilder) WithAllowUnoptimized(allow bool) *ServiceBuilder {
	b.service.Spec.Template.AllowUnoptimized = allow
	return b
}

func (b *ServiceBuilder) WithCachingMode(mode aimv1alpha1.AIMCachingMode) *ServiceBuilder {
	if b.service.Spec.Caching == nil {
		b.service.Spec.Caching = &aimv1alpha1.AIMServiceCachingConfig{}
	}
	b.service.Spec.Caching.Mode = mode
	return b
}

func (b *ServiceBuilder) WithOverrideMetric(metric aimv1alpha1.AIMMetric) *ServiceBuilder {
	if b.service.Spec.Overrides == nil {
		b.service.Spec.Overrides = &aimv1alpha1.AIMServiceOverrides{}
	}
	b.service.Spec.Overrides.Metric = &metric
	return b
}

func (b *ServiceBuilder) WithOverridePrecision(precision aimv1alpha1.AIMPrecision) *ServiceBuilder {
	if b.service.Spec.Overrides == nil {
		b.service.Spec.Overrides = &aimv1alpha1.AIMServiceOverrides{}
	}
	b.service.Spec.Overrides.Precision = &precision
	return b
}

func (b *ServiceBuilder) WithOverrideGPU(model string, count int32) *ServiceBuilder {
	if b.service.Spec.Overrides == nil {
		b.service.Spec.Overrides = &aimv1alpha1.AIMServiceOverrides{}
	}
	b.service.Spec.Overrides.Gpu = &aimv1alpha1.AIMGpuRequirements{
		Models:   []string{model},
		Requests: count,
	}
	return b
}

func (b *ServiceBuilder) Build() *aimv1alpha1.AIMService {
	return b.service.DeepCopy()
}

// ============================================================================
// BUILDERS - AIMModel
// ============================================================================

// ModelBuilder provides a fluent API for constructing AIMModel test fixtures.
type ModelBuilder struct {
	model *aimv1alpha1.AIMModel
}

// NewModel creates a new ModelBuilder with sensible defaults.
func NewModel(name string) *ModelBuilder {
	return &ModelBuilder{
		model: &aimv1alpha1.AIMModel{
			TypeMeta: metav1.TypeMeta{
				APIVersion: aimv1alpha1.GroupVersion.String(),
				Kind:       "AIMModel",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: testNamespace,
				UID:       "test-model-uid",
			},
			Spec: aimv1alpha1.AIMModelSpec{},
			Status: aimv1alpha1.AIMModelStatus{
				Status: constants.AIMStatusReady,
			},
		},
	}
}

func (b *ModelBuilder) WithNamespace(ns string) *ModelBuilder {
	b.model.Namespace = ns
	return b
}

func (b *ModelBuilder) WithImage(image string) *ModelBuilder {
	b.model.Spec.Image = image
	return b
}

func (b *ModelBuilder) WithStatus(status constants.AIMStatus) *ModelBuilder {
	b.model.Status.Status = status
	return b
}

func (b *ModelBuilder) Build() *aimv1alpha1.AIMModel {
	return b.model.DeepCopy()
}

// ============================================================================
// BUILDERS - AIMClusterModel
// ============================================================================

// ClusterModelBuilder provides a fluent API for constructing AIMClusterModel test fixtures.
type ClusterModelBuilder struct {
	model *aimv1alpha1.AIMClusterModel
}

// NewClusterModel creates a new ClusterModelBuilder with sensible defaults.
func NewClusterModel(name string) *ClusterModelBuilder {
	return &ClusterModelBuilder{
		model: &aimv1alpha1.AIMClusterModel{
			TypeMeta: metav1.TypeMeta{
				APIVersion: aimv1alpha1.GroupVersion.String(),
				Kind:       "AIMClusterModel",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
				UID:  "test-cluster-model-uid",
			},
			Spec: aimv1alpha1.AIMModelSpec{},
			Status: aimv1alpha1.AIMModelStatus{
				Status: constants.AIMStatusReady,
			},
		},
	}
}

func (b *ClusterModelBuilder) WithImage(image string) *ClusterModelBuilder {
	b.model.Spec.Image = image
	return b
}

func (b *ClusterModelBuilder) WithStatus(status constants.AIMStatus) *ClusterModelBuilder {
	b.model.Status.Status = status
	return b
}

func (b *ClusterModelBuilder) Build() *aimv1alpha1.AIMClusterModel {
	return b.model.DeepCopy()
}

// ============================================================================
// BUILDERS - AIMServiceTemplate
// ============================================================================

// TemplateBuilder provides a fluent API for constructing AIMServiceTemplate test fixtures.
type TemplateBuilder struct {
	template *aimv1alpha1.AIMServiceTemplate
}

// NewTemplate creates a new TemplateBuilder with sensible defaults.
func NewTemplate(name string) *TemplateBuilder {
	return &TemplateBuilder{
		template: &aimv1alpha1.AIMServiceTemplate{
			TypeMeta: metav1.TypeMeta{
				APIVersion: aimv1alpha1.GroupVersion.String(),
				Kind:       "AIMServiceTemplate",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: testNamespace,
				UID:       "test-template-uid",
			},
			Spec: aimv1alpha1.AIMServiceTemplateSpec{},
			Status: aimv1alpha1.AIMServiceTemplateStatus{
				Status: constants.AIMStatusReady,
				Profile: &aimv1alpha1.AIMProfile{
					Metadata: aimv1alpha1.AIMProfileMetadata{
						Type: aimv1alpha1.AIMProfileTypeOptimized,
					},
				},
			},
		},
	}
}

func (b *TemplateBuilder) WithNamespace(ns string) *TemplateBuilder {
	b.template.Namespace = ns
	return b
}

func (b *TemplateBuilder) WithModelName(name string) *TemplateBuilder {
	b.template.Spec.ModelName = name
	return b
}

func (b *TemplateBuilder) WithStatus(status constants.AIMStatus) *TemplateBuilder {
	b.template.Status.Status = status
	return b
}

func (b *TemplateBuilder) WithProfileType(profileType aimv1alpha1.AIMProfileType) *TemplateBuilder {
	b.template.Status.Profile.Metadata.Type = profileType
	return b
}

func (b *TemplateBuilder) WithGPU(model string, count int) *TemplateBuilder {
	b.template.Status.Profile.Metadata.GPU = model
	b.template.Status.Profile.Metadata.GPUCount = int32(count)
	return b
}

func (b *TemplateBuilder) WithMetric(metric aimv1alpha1.AIMMetric) *TemplateBuilder {
	b.template.Status.Profile.Metadata.Metric = metric
	return b
}

func (b *TemplateBuilder) WithPrecision(precision aimv1alpha1.AIMPrecision) *TemplateBuilder {
	b.template.Status.Profile.Metadata.Precision = precision
	return b
}

func (b *TemplateBuilder) WithModelSources(sources ...aimv1alpha1.AIMModelSource) *TemplateBuilder {
	b.template.Status.ModelSources = sources
	return b
}

func (b *TemplateBuilder) Build() *aimv1alpha1.AIMServiceTemplate {
	return b.template.DeepCopy()
}

// ============================================================================
// BUILDERS - AIMClusterServiceTemplate
// ============================================================================

// ClusterTemplateBuilder provides a fluent API for constructing AIMClusterServiceTemplate test fixtures.
type ClusterTemplateBuilder struct {
	template *aimv1alpha1.AIMClusterServiceTemplate
}

// NewClusterTemplate creates a new ClusterTemplateBuilder with sensible defaults.
func NewClusterTemplate(name string) *ClusterTemplateBuilder {
	return &ClusterTemplateBuilder{
		template: &aimv1alpha1.AIMClusterServiceTemplate{
			TypeMeta: metav1.TypeMeta{
				APIVersion: aimv1alpha1.GroupVersion.String(),
				Kind:       "AIMClusterServiceTemplate",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
				UID:  "test-cluster-template-uid",
			},
			Spec: aimv1alpha1.AIMClusterServiceTemplateSpec{},
			Status: aimv1alpha1.AIMServiceTemplateStatus{
				Status: constants.AIMStatusReady,
				Profile: &aimv1alpha1.AIMProfile{
					Metadata: aimv1alpha1.AIMProfileMetadata{
						Type: aimv1alpha1.AIMProfileTypeOptimized,
					},
				},
			},
		},
	}
}

func (b *ClusterTemplateBuilder) WithModelName(name string) *ClusterTemplateBuilder {
	b.template.Spec.ModelName = name
	return b
}

func (b *ClusterTemplateBuilder) WithStatus(status constants.AIMStatus) *ClusterTemplateBuilder {
	b.template.Status.Status = status
	return b
}

func (b *ClusterTemplateBuilder) WithProfileType(profileType aimv1alpha1.AIMProfileType) *ClusterTemplateBuilder {
	b.template.Status.Profile.Metadata.Type = profileType
	return b
}

func (b *ClusterTemplateBuilder) WithGPU(model string, count int) *ClusterTemplateBuilder {
	b.template.Status.Profile.Metadata.GPU = model
	b.template.Status.Profile.Metadata.GPUCount = int32(count)
	return b
}

func (b *ClusterTemplateBuilder) WithMetric(metric aimv1alpha1.AIMMetric) *ClusterTemplateBuilder {
	b.template.Status.Profile.Metadata.Metric = metric
	return b
}

func (b *ClusterTemplateBuilder) WithPrecision(precision aimv1alpha1.AIMPrecision) *ClusterTemplateBuilder {
	b.template.Status.Profile.Metadata.Precision = precision
	return b
}

func (b *ClusterTemplateBuilder) Build() *aimv1alpha1.AIMClusterServiceTemplate {
	return b.template.DeepCopy()
}

// ============================================================================
// BUILDERS - TemplateCandidate (for selection tests)
// ============================================================================

// CandidateBuilder provides a fluent API for constructing TemplateCandidate test fixtures.
type CandidateBuilder struct {
	candidate TemplateCandidate
}

// NewCandidate creates a new CandidateBuilder with sensible defaults.
func NewCandidate(name string) *CandidateBuilder {
	return &CandidateBuilder{
		candidate: TemplateCandidate{
			Name:      name,
			Namespace: testNamespace,
			Scope:     aimv1alpha1.AIMResolutionScopeNamespace,
			Status: aimv1alpha1.AIMServiceTemplateStatus{
				Status: constants.AIMStatusReady,
				Profile: &aimv1alpha1.AIMProfile{
					Metadata: aimv1alpha1.AIMProfileMetadata{
						Type: aimv1alpha1.AIMProfileTypeOptimized,
					},
				},
			},
		},
	}
}

func (b *CandidateBuilder) WithNamespace(ns string) *CandidateBuilder {
	b.candidate.Namespace = ns
	return b
}

func (b *CandidateBuilder) WithScope(scope aimv1alpha1.AIMResolutionScope) *CandidateBuilder {
	b.candidate.Scope = scope
	return b
}

func (b *CandidateBuilder) WithStatus(status constants.AIMStatus) *CandidateBuilder {
	b.candidate.Status.Status = status
	return b
}

func (b *CandidateBuilder) WithProfileType(profileType aimv1alpha1.AIMProfileType) *CandidateBuilder {
	b.candidate.Status.Profile.Metadata.Type = profileType
	return b
}

func (b *CandidateBuilder) WithGPU(model string, count int) *CandidateBuilder {
	b.candidate.Status.Profile.Metadata.GPU = model
	b.candidate.Status.Profile.Metadata.GPUCount = int32(count)
	return b
}

func (b *CandidateBuilder) WithMetric(metric aimv1alpha1.AIMMetric) *CandidateBuilder {
	b.candidate.Status.Profile.Metadata.Metric = metric
	return b
}

func (b *CandidateBuilder) WithPrecision(precision aimv1alpha1.AIMPrecision) *CandidateBuilder {
	b.candidate.Status.Profile.Metadata.Precision = precision
	return b
}

func (b *CandidateBuilder) Build() TemplateCandidate {
	return b.candidate
}

// ============================================================================
// BUILDERS - Nodes (for GPU availability tests)
// ============================================================================

// NodeBuilder provides a fluent API for constructing Node test fixtures.
type NodeBuilder struct {
	node *corev1.Node
}

// NewNode creates a new NodeBuilder with sensible defaults.
func NewNode(name string) *NodeBuilder {
	return &NodeBuilder{
		node: &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:   name,
				Labels: make(map[string]string),
			},
		},
	}
}

func (b *NodeBuilder) WithGPUProductID(productID string) *NodeBuilder {
	b.node.Labels[utils.LabelAMDGPUDeviceID] = productID
	return b
}

func (b *NodeBuilder) Build() *corev1.Node {
	return b.node.DeepCopy()
}

// ============================================================================
// HELPERS - Model Sources
// ============================================================================

// NewModelSource creates a model source with the given URI and size.
func NewModelSource(uri string, sizeBytes int64) aimv1alpha1.AIMModelSource {
	return aimv1alpha1.AIMModelSource{
		SourceURI: uri,
		Size:      resource.NewQuantity(sizeBytes, resource.BinarySI),
	}
}

// NewModelSourceWithoutSize creates a model source without size (for error testing).
func NewModelSourceWithoutSize(uri string) aimv1alpha1.AIMModelSource {
	return aimv1alpha1.AIMModelSource{
		SourceURI: uri,
	}
}

// ============================================================================
// HELPERS - Fake Client
// ============================================================================

// newFakeClient creates a fake controller-runtime client with the given objects.
func newFakeClient(objs ...client.Object) client.Client {
	scheme := runtime.NewScheme()
	_ = aimv1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		Build()
}

// testContext returns a context suitable for testing.
func testContext() context.Context {
	return context.Background()
}
