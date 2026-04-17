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
	"errors"
	"strings"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

// ============================================================================
// GENERATE MODEL NAME TESTS
// ============================================================================

func TestGenerateModelName(t *testing.T) {
	tests := []struct {
		name         string
		imageURI     string
		wantContains []string
		wantErr      bool
	}{
		{
			name:         "simple image",
			imageURI:     "ghcr.io/amd/llama-3-8b:v1.0.0",
			wantContains: []string{"llama-3-8b", "v1-0-0"}, // dots converted to dashes for k8s compatibility
			wantErr:      false,
		},
		{
			name:         "image with port",
			imageURI:     "registry.example.com:5000/models/mistral:latest",
			wantContains: []string{"mistral", "latest"},
			wantErr:      false,
		},
		{
			name:         "image with digest",
			imageURI:     "ghcr.io/amd/model@sha256:abc123",
			wantContains: []string{"model"},
			wantErr:      false,
		},
		{
			name:         "image without tag uses latest",
			imageURI:     "ghcr.io/amd/model",
			wantContains: []string{"model", "latest"},
			wantErr:      false,
		},
		{
			name:     "empty image fails",
			imageURI: "",
			wantErr:  true,
		},
		{
			name:     "invalid image fails",
			imageURI: ":::invalid",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := GenerateModelName(tt.imageURI)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			// Check that result contains expected substrings
			for _, want := range tt.wantContains {
				if !strings.Contains(result, want) {
					t.Errorf("expected result to contain %q, got %q", want, result)
				}
			}

			// Verify name is Kubernetes-valid (lowercase, no special chars except -)
			if result != strings.ToLower(result) {
				t.Errorf("expected lowercase name, got %q", result)
			}
		})
	}
}

func TestGenerateModelName_Deterministic(t *testing.T) {
	imageURI := "ghcr.io/amd/llama-3-8b:v1.0.0"

	result1, err1 := GenerateModelName(imageURI)
	result2, err2 := GenerateModelName(imageURI)
	result3, err3 := GenerateModelName(imageURI)

	if err1 != nil || err2 != nil || err3 != nil {
		t.Fatalf("unexpected errors: %v, %v, %v", err1, err2, err3)
	}

	if result1 != result2 || result2 != result3 {
		t.Errorf("expected deterministic output, got %q, %q, %q", result1, result2, result3)
	}
}

func TestGenerateModelName_DifferentImagesDifferentNames(t *testing.T) {
	result1, _ := GenerateModelName("ghcr.io/amd/llama-3-8b:v1.0.0")
	result2, _ := GenerateModelName("ghcr.io/amd/llama-3-8b:v2.0.0")

	if result1 == result2 {
		t.Errorf("expected different names for different images, got same: %q", result1)
	}
}

// ============================================================================
// FETCH MODEL BY NAME TESTS
// ============================================================================

func TestFetchModel_ByName_NamespaceScoped(t *testing.T) {
	ctx := testContext()

	model := NewModel("test-model").Build()
	service := NewService("svc").WithModelName("test-model").Build()

	c := newFakeClient(model)
	result := fetchModel(ctx, c, service)

	if result.Model.Error != nil {
		t.Errorf("unexpected error: %v", result.Model.Error)
	}
	if result.Model.Value == nil {
		t.Error("expected model to be found, got nil")
	}
	if result.Model.Value != nil && result.Model.Value.Name != "test-model" {
		t.Errorf("expected model name test-model, got %s", result.Model.Value.Name)
	}
}

func TestFetchModel_ByName_ClusterScoped(t *testing.T) {
	ctx := testContext()

	clusterModel := NewClusterModel("cluster-model").Build()
	service := NewService("svc").WithModelName("cluster-model").Build()

	c := newFakeClient(clusterModel)
	result := fetchModel(ctx, c, service)

	if result.ClusterModel.Error != nil {
		t.Errorf("unexpected error: %v", result.ClusterModel.Error)
	}
	if result.ClusterModel.Value == nil {
		t.Error("expected cluster model to be found, got nil")
	}
	if result.ClusterModel.Value != nil && result.ClusterModel.Value.Name != "cluster-model" {
		t.Errorf("expected cluster model name cluster-model, got %s", result.ClusterModel.Value.Name)
	}
}

func TestFetchModel_ByName_NamespacePrecedence(t *testing.T) {
	ctx := testContext()

	// Both namespace and cluster model exist with same name
	nsModel := NewModel("shared-name").Build()
	clusterModel := NewClusterModel("shared-name").Build()
	service := NewService("svc").WithModelName("shared-name").Build()

	c := newFakeClient(nsModel, clusterModel)
	result := fetchModel(ctx, c, service)

	// Namespace model should take precedence
	if result.Model.Value == nil {
		t.Error("expected namespace model to be found, got nil")
	}
	if result.ClusterModel.Value != nil {
		t.Error("expected cluster model to NOT be fetched when namespace model exists")
	}
}

func TestFetchModel_ByName_NotFound(t *testing.T) {
	ctx := testContext()

	service := NewService("svc").WithModelName("nonexistent").Build()

	c := newFakeClient()
	result := fetchModel(ctx, c, service)

	if result.Model.Error == nil {
		t.Error("expected error for missing model")
	}
	if !strings.Contains(result.Model.Error.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %v", result.Model.Error)
	}
}

// ============================================================================
// FETCH MODEL BY IMAGE TESTS
// ============================================================================

func TestFetchModel_ByImage_ExistingModel(t *testing.T) {
	ctx := testContext()

	model := NewModel("existing-model").WithImage("ghcr.io/amd/llama:v1").Build()
	service := NewService("svc").WithModelImage("ghcr.io/amd/llama:v1").Build()

	c := newFakeClient(model)
	result := fetchModel(ctx, c, service)

	if result.Model.Error != nil {
		t.Errorf("unexpected error: %v", result.Model.Error)
	}
	if result.Model.Value == nil {
		t.Error("expected model to be found, got nil")
	}
	if result.ImageURI != "ghcr.io/amd/llama:v1" {
		t.Errorf("expected imageURI to be set, got %q", result.ImageURI)
	}
}

func TestFetchModel_ByImage_NoExistingModel(t *testing.T) {
	ctx := testContext()

	service := NewService("svc").WithModelImage("ghcr.io/amd/new-model:v1").Build()

	c := newFakeClient()
	result := fetchModel(ctx, c, service)

	// No error - ComposeState determines if creation is needed
	if result.Model.Error != nil {
		t.Errorf("unexpected error: %v", result.Model.Error)
	}
	if result.Model.Value != nil {
		t.Error("expected no model found, got one")
	}
	if result.ImageURI != "ghcr.io/amd/new-model:v1" {
		t.Errorf("expected imageURI to be set, got %q", result.ImageURI)
	}
}

func TestFetchModel_ByImage_MultipleModels(t *testing.T) {
	ctx := testContext()

	// Two models with same image - ambiguous
	model1 := NewModel("model-1").WithImage("ghcr.io/amd/llama:v1").Build()
	model2 := NewClusterModel("model-2").WithImage("ghcr.io/amd/llama:v1").Build()
	service := NewService("svc").WithModelImage("ghcr.io/amd/llama:v1").Build()

	c := newFakeClient(model1, model2)
	result := fetchModel(ctx, c, service)

	if result.Model.Error == nil {
		t.Error("expected error for multiple models with same image")
	}
	if !errors.Is(result.Model.Error, ErrMultipleModelsFound) {
		t.Errorf("expected ErrMultipleModelsFound, got: %v", result.Model.Error)
	}
}

// ============================================================================
// FETCH MODEL - RESOLVED ARTIFACT TESTS
// ============================================================================

func TestFetchModel_UsesResolvedModel_WhenReady(t *testing.T) {
	ctx := testContext()

	model := NewModel("cached-model").WithStatus(constants.AIMStatusReady).Build()
	service := NewService("svc").WithModelName("different-name").Build()
	// Simulate previously resolved model in status
	service.Status.ResolvedModel = &aimv1alpha1.AIMResolvedReference{
		Name:      "cached-model",
		Namespace: testNamespace,
		Scope:     aimv1alpha1.AIMResolutionScopeNamespace,
	}

	c := newFakeClient(model)
	result := fetchModel(ctx, c, service)

	// Should use the resolved model, not re-resolve by name
	if result.Model.Value == nil {
		t.Error("expected resolved model to be used")
	}
	if result.Model.Value != nil && result.Model.Value.Name != "cached-model" {
		t.Errorf("expected cached-model, got %s", result.Model.Value.Name)
	}
}

func TestFetchModel_ReResolvesWhenResolvedModel_NotReady(t *testing.T) {
	ctx := testContext()

	// Cached model is not ready
	cachedModel := NewModel("cached-model").WithStatus(constants.AIMStatusPending).Build()
	// But there's another model we could use
	readyModel := NewModel("ready-model").WithStatus(constants.AIMStatusReady).Build()

	service := NewService("svc").WithModelName("ready-model").Build()
	service.Status.ResolvedModel = &aimv1alpha1.AIMResolvedReference{
		Name:      "cached-model",
		Namespace: testNamespace,
		Scope:     aimv1alpha1.AIMResolutionScopeNamespace,
	}

	c := newFakeClient(cachedModel, readyModel)
	result := fetchModel(ctx, c, service)

	// Should re-resolve and find the ready model
	if result.Model.Value == nil {
		t.Error("expected model to be found")
	}
	if result.Model.Value != nil && result.Model.Value.Name != "ready-model" {
		t.Errorf("expected ready-model, got %s", result.Model.Value.Name)
	}
}

func TestFetchModel_ReResolvesWhenResolvedModel_Deleted(t *testing.T) {
	ctx := testContext()

	// Cached model was deleted, but we have another one
	newModel := NewModel("new-model").WithStatus(constants.AIMStatusReady).Build()

	service := NewService("svc").WithModelName("new-model").Build()
	service.Status.ResolvedModel = &aimv1alpha1.AIMResolvedReference{
		Name:      "deleted-model",
		Namespace: testNamespace,
		Scope:     aimv1alpha1.AIMResolutionScopeNamespace,
	}

	c := newFakeClient(newModel)
	result := fetchModel(ctx, c, service)

	// Should re-resolve and find the new model
	if result.Model.Value == nil {
		t.Error("expected model to be found")
	}
	if result.Model.Value != nil && result.Model.Value.Name != "new-model" {
		t.Errorf("expected new-model, got %s", result.Model.Value.Name)
	}
}

// ============================================================================
// FETCH MODEL - NO MODEL SPECIFIED
// ============================================================================

func TestFetchModel_NoModelSpecified(t *testing.T) {
	ctx := testContext()

	service := NewService("svc").Build() // No model name or image

	c := newFakeClient()
	result := fetchModel(ctx, c, service)

	if result.Model.Error == nil {
		t.Error("expected error when no model specified")
	}
	if !strings.Contains(result.Model.Error.Error(), "no model specified") {
		t.Errorf("expected 'no model specified' error, got: %v", result.Model.Error)
	}
}

// ============================================================================
// FIND MODELS WITH IMAGE TESTS
// ============================================================================

func TestFindModelsWithImage(t *testing.T) {
	ctx := testContext()

	tests := []struct {
		name          string
		objects       []client.Object
		imageURI      string
		expectedCount int
		expectedScope []aimv1alpha1.AIMResolutionScope
	}{
		{
			name:          "no models",
			objects:       []client.Object{},
			imageURI:      "ghcr.io/amd/llama:v1",
			expectedCount: 0,
		},
		{
			name: "single namespace model",
			objects: []client.Object{
				NewModel("m1").WithImage("ghcr.io/amd/llama:v1").Build(),
			},
			imageURI:      "ghcr.io/amd/llama:v1",
			expectedCount: 1,
			expectedScope: []aimv1alpha1.AIMResolutionScope{aimv1alpha1.AIMResolutionScopeNamespace},
		},
		{
			name: "single cluster model",
			objects: []client.Object{
				NewClusterModel("m1").WithImage("ghcr.io/amd/llama:v1").Build(),
			},
			imageURI:      "ghcr.io/amd/llama:v1",
			expectedCount: 1,
			expectedScope: []aimv1alpha1.AIMResolutionScope{aimv1alpha1.AIMResolutionScopeCluster},
		},
		{
			name: "both namespace and cluster",
			objects: []client.Object{
				NewModel("m1").WithImage("ghcr.io/amd/llama:v1").Build(),
				NewClusterModel("m2").WithImage("ghcr.io/amd/llama:v1").Build(),
			},
			imageURI:      "ghcr.io/amd/llama:v1",
			expectedCount: 2,
		},
		{
			name: "no matching image",
			objects: []client.Object{
				NewModel("m1").WithImage("ghcr.io/amd/other:v1").Build(),
			},
			imageURI:      "ghcr.io/amd/llama:v1",
			expectedCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newFakeClient(tt.objects...)
			results, err := findModelsWithImage(ctx, c, testNamespace, tt.imageURI)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if len(results) != tt.expectedCount {
				t.Errorf("expected %d results, got %d", tt.expectedCount, len(results))
			}

			for i, expected := range tt.expectedScope {
				if i < len(results) && results[i].Scope != expected {
					t.Errorf("expected scope %v at index %d, got %v", expected, i, results[i].Scope)
				}
			}
		})
	}
}

// ============================================================================
// BUILD MODEL FOR IMAGE TESTS
// ============================================================================

func TestBuildModelForImage(t *testing.T) {
	service := NewService("svc").Build()
	imageURI := "ghcr.io/amd/llama:v1"
	modelName := "llama-v1-abc123"

	result := buildModelForImage(service, imageURI, modelName)

	// Check metadata
	if result.Name != modelName {
		t.Errorf("expected name %s, got %s", modelName, result.Name)
	}
	if result.Namespace != service.Namespace {
		t.Errorf("expected namespace %s, got %s", service.Namespace, result.Namespace)
	}

	// Check labels
	if result.Labels[constants.LabelKeyOrigin] != constants.LabelValueOriginAutoGenerated {
		t.Errorf("expected origin label %s, got %s",
			constants.LabelValueOriginAutoGenerated, result.Labels[constants.LabelKeyOrigin])
	}

	// Check spec
	if result.Spec.Image != imageURI {
		t.Errorf("expected image %s, got %s", imageURI, result.Spec.Image)
	}

	// Check no owner references (model is shared/orphaned)
	if len(result.OwnerReferences) != 0 {
		t.Errorf("expected no owner references, got %d", len(result.OwnerReferences))
	}
}

func TestBuildModelForImage_InheritsServiceConfig(t *testing.T) {
	service := NewService("svc").Build()
	service.Spec.ServiceAccountName = "my-sa"
	// Note: RuntimeConfigRef and ImagePullSecrets are also inherited

	result := buildModelForImage(service, "ghcr.io/amd/llama:v1", "llama")

	if result.Spec.ServiceAccountName != "my-sa" {
		t.Errorf("expected service account my-sa, got %s", result.Spec.ServiceAccountName)
	}
}
