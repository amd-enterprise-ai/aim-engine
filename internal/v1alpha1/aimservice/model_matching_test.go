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
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/types"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	legacyaimmodel "github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/aimmodel"
)

func TestGenerateCustomModelName(t *testing.T) {
	service := NewService("source").Build()
	tests := []struct {
		name     string
		custom   *aimv1alpha1.AIMServiceModelCustom
		expected string
	}{
		{
			name: "simple model ID",
			custom: &aimv1alpha1.AIMServiceModelCustom{
				BaseImage: "ghcr.io/amdenterpriseai/aim-base:0.7.0",
				ModelSources: []aimv1alpha1.AIMModelSource{
					{ModelID: "my-org/my-model", SourceURI: "s3://bucket/model"},
				},
			},
			expected: "my-org-my-model-", // Hash will be appended
		},
		{
			name: "model ID with special chars",
			custom: &aimv1alpha1.AIMServiceModelCustom{
				BaseImage: "ghcr.io/amdenterpriseai/aim-base:0.7.0",
				ModelSources: []aimv1alpha1.AIMModelSource{
					{ModelID: "Meta-Llama/Llama-3.1-70B", SourceURI: "hf://Meta-Llama/Llama-3.1-70B"},
				},
			},
			expected: "meta-llama-llama-3.1-70b-", // Sanitized and lowercased
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GenerateCustomModelName(service, tt.custom)

			// Check that it starts with expected prefix (hash is appended)
			if len(result) < len(tt.expected) {
				t.Errorf("GenerateCustomModelName() = %q, expected to start with %q", result, tt.expected)
			}

			// Check it's a valid Kubernetes name (max 63 chars, lowercase, alphanumeric and hyphens)
			if len(result) > 63 {
				t.Errorf("GenerateCustomModelName() = %q, length %d exceeds 63", result, len(result))
			}
		})
	}
}

func TestGenerateCustomModelName_Deterministic(t *testing.T) {
	service := NewService("source").Build()
	custom := &aimv1alpha1.AIMServiceModelCustom{
		BaseImage: "ghcr.io/amdenterpriseai/aim-base:0.7.0",
		ModelSources: []aimv1alpha1.AIMModelSource{
			{ModelID: "my-org/my-model", SourceURI: "s3://bucket/model"},
		},
		Hardware: aimv1alpha1.AIMHardwareRequirements{
			GPU: &aimv1alpha1.AIMGpuRequirements{},
		},
	}

	name1 := GenerateCustomModelName(service, custom)
	name2 := GenerateCustomModelName(service, custom)

	if name1 != name2 {
		t.Errorf("GenerateCustomModelName() not deterministic: %q != %q", name1, name2)
	}
}

func TestGenerateCustomModelName_DifferentEndpoints(t *testing.T) {
	service := NewService("source").Build()
	// Two custom models with same base but different S3 endpoints should get different names
	custom1 := &aimv1alpha1.AIMServiceModelCustom{
		BaseImage: "ghcr.io/amdenterpriseai/aim-base:0.7.0",
		ModelSources: []aimv1alpha1.AIMModelSource{
			{
				ModelID:   "my-org/my-model",
				SourceURI: "s3://bucket/model",
				Env: []corev1.EnvVar{
					{Name: "AWS_ENDPOINT_URL", Value: "https://minio-1.local"},
				},
			},
		},
	}

	custom2 := &aimv1alpha1.AIMServiceModelCustom{
		BaseImage: "ghcr.io/amdenterpriseai/aim-base:0.7.0",
		ModelSources: []aimv1alpha1.AIMModelSource{
			{
				ModelID:   "my-org/my-model",
				SourceURI: "s3://bucket/model",
				Env: []corev1.EnvVar{
					{Name: "AWS_ENDPOINT_URL", Value: "https://minio-2.local"},
				},
			},
		},
	}

	name1 := GenerateCustomModelName(service, custom1)
	name2 := GenerateCustomModelName(service, custom2)

	if name1 == name2 {
		t.Errorf("Different S3 endpoints should generate different names: %q == %q", name1, name2)
	}
}

func TestGenerateCustomModelName_DifferentServiceUIDs(t *testing.T) {
	custom := &aimv1alpha1.AIMServiceModelCustom{
		BaseImage: "ghcr.io/amdenterpriseai/aim-base:0.7.0",
		ModelSources: []aimv1alpha1.AIMModelSource{
			{ModelID: "my-org/my-model", SourceURI: "s3://bucket/model"},
		},
	}
	first := NewService("first").Build()
	first.UID = types.UID("first-service-uid")
	second := NewService("second").Build()
	second.UID = types.UID("second-service-uid")

	firstName := GenerateCustomModelName(first, custom)
	secondName := GenerateCustomModelName(second, custom)

	if firstName == secondName {
		t.Fatalf("different service UIDs generated the same custom model name %q", firstName)
	}
	if len(firstName) > 63 || len(secondName) > 63 {
		t.Fatalf("custom model names exceed 63 characters: %q, %q", firstName, secondName)
	}
}

func TestModelSourcesMatch(t *testing.T) {
	tests := []struct {
		name     string
		a        []aimv1alpha1.AIMModelSource
		b        []aimv1alpha1.AIMModelSource
		expected bool
	}{
		{
			name:     "both empty",
			a:        nil,
			b:        nil,
			expected: true,
		},
		{
			name: "same sources",
			a: []aimv1alpha1.AIMModelSource{
				{ModelID: "org/model", SourceURI: "s3://bucket/model"},
			},
			b: []aimv1alpha1.AIMModelSource{
				{ModelID: "org/model", SourceURI: "s3://bucket/model"},
			},
			expected: true,
		},
		{
			name: "different model IDs",
			a: []aimv1alpha1.AIMModelSource{
				{ModelID: "org/model-a", SourceURI: "s3://bucket/model"},
			},
			b: []aimv1alpha1.AIMModelSource{
				{ModelID: "org/model-b", SourceURI: "s3://bucket/model"},
			},
			expected: false,
		},
		{
			name: "different source URIs",
			a: []aimv1alpha1.AIMModelSource{
				{ModelID: "org/model", SourceURI: "s3://bucket-a/model"},
			},
			b: []aimv1alpha1.AIMModelSource{
				{ModelID: "org/model", SourceURI: "s3://bucket-b/model"},
			},
			expected: false,
		},
		{
			name: "different S3 endpoints",
			a: []aimv1alpha1.AIMModelSource{
				{
					ModelID:   "org/model",
					SourceURI: "s3://bucket/model",
					Env:       []corev1.EnvVar{{Name: "AWS_ENDPOINT_URL", Value: "https://minio-1.local"}},
				},
			},
			b: []aimv1alpha1.AIMModelSource{
				{
					ModelID:   "org/model",
					SourceURI: "s3://bucket/model",
					Env:       []corev1.EnvVar{{Name: "AWS_ENDPOINT_URL", Value: "https://minio-2.local"}},
				},
			},
			expected: false,
		},
		{
			name: "same S3 endpoints",
			a: []aimv1alpha1.AIMModelSource{
				{
					ModelID:   "org/model",
					SourceURI: "s3://bucket/model",
					Env:       []corev1.EnvVar{{Name: "AWS_ENDPOINT_URL", Value: "https://minio.local"}},
				},
			},
			b: []aimv1alpha1.AIMModelSource{
				{
					ModelID:   "org/model",
					SourceURI: "s3://bucket/model",
					Env:       []corev1.EnvVar{{Name: "AWS_ENDPOINT_URL", Value: "https://minio.local"}},
				},
			},
			expected: true,
		},
		{
			name: "different lengths",
			a: []aimv1alpha1.AIMModelSource{
				{ModelID: "org/model-1", SourceURI: "s3://bucket/model-1"},
				{ModelID: "org/model-2", SourceURI: "s3://bucket/model-2"},
			},
			b: []aimv1alpha1.AIMModelSource{
				{ModelID: "org/model-1", SourceURI: "s3://bucket/model-1"},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := modelSourcesMatch(tt.a, tt.b)
			if result != tt.expected {
				t.Errorf("modelSourcesMatch() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func newCustomModelMatchingFixture(
	t *testing.T,
) (*aimv1alpha1.AIMService, *aimv1alpha1.AIMServiceModelCustom, string) {
	t.Helper()
	custom := &aimv1alpha1.AIMServiceModelCustom{
		BaseImage: "ghcr.io/amdenterpriseai/aim-base:0.7.0",
		ModelSources: []aimv1alpha1.AIMModelSource{
			{ModelID: "my-org/my-model", SourceURI: "s3://bucket/model"},
		},
		Hardware: aimv1alpha1.AIMHardwareRequirements{
			GPU: &aimv1alpha1.AIMGpuRequirements{},
		},
	}
	service := NewService("source").Build()
	service.Spec.Model = &aimv1alpha1.AIMServiceModel{Custom: custom}
	payload, err := legacyaimmodel.EncodeLegacyAutoCustomSpec(custom)
	if err != nil {
		t.Fatalf("encode compatibility payload: %v", err)
	}
	return service, custom, payload
}

func TestFindMatchingCustomModel(t *testing.T) {
	service, custom, payload := newCustomModelMatchingFixture(t)

	t.Run("missing deterministic candidate creates fresh", func(t *testing.T) {
		result, err := FindMatchingCustomModel(context.Background(), newFakeClient(), service, custom)
		if err != nil {
			t.Fatalf("FindMatchingCustomModel() error = %v", err)
		}
		if result != nil {
			t.Fatalf("FindMatchingCustomModel() = %s, want nil", result.Name)
		}
	})

	t.Run("exact service-scoped compatibility candidate is hydrated and reused", func(t *testing.T) {
		model := buildModelForCustom(service, custom, payload, GenerateCustomModelName(service, custom))
		model.Status.Status = constants.AIMStatusReady
		result, err := FindMatchingCustomModel(context.Background(), newFakeClient(service, model), service, custom)
		if err != nil {
			t.Fatalf("FindMatchingCustomModel() error = %v", err)
		}
		if result == nil || result.Name != model.Name {
			t.Fatalf("FindMatchingCustomModel() = %#v, want %s", result, model.Name)
		}
		if !modelSourcesMatch(result.Spec.ModelSources, custom.ModelSources) || result.Spec.Custom == nil {
			t.Fatalf("reused model was not hydrated with exact predecessor input: %#v", result.Spec)
		}
		if len(model.Spec.ModelSources) != 0 || model.Spec.Custom != nil {
			t.Fatalf("stored compatibility model was mutated: %#v", model.Spec)
		}
	})

	t.Run("exact predecessor-name compatibility candidate is reused", func(t *testing.T) {
		model := buildModelForCustom(service, custom, payload, generateLegacyCustomModelName(custom))
		result, err := FindMatchingCustomModel(context.Background(), newFakeClient(service, model), service, custom)
		if err != nil {
			t.Fatalf("FindMatchingCustomModel() error = %v", err)
		}
		if result == nil || result.Name != model.Name {
			t.Fatalf("FindMatchingCustomModel() = %#v, want %s", result, model.Name)
		}
	})

	t.Run("exact grandfathered predecessor candidate is reused without rewrite", func(t *testing.T) {
		model := buildModelForCustom(service, custom, payload, generateLegacyCustomModelName(custom))
		model.Annotations = nil
		model.OwnerReferences = nil
		model.Spec.ModelSources = custom.ModelSources
		model.Spec.Custom = &aimv1alpha1.AIMCustomModelSpec{
			Hardware:      custom.Hardware.DeepCopy(),
			VersionPolicy: aimv1alpha1.AIMVersionPolicyPinned,
		}
		model.Status.Status = constants.AIMStatusReady
		k8sClient := newFakeClient(service, model)
		original := model.DeepCopy()

		result, err := FindMatchingCustomModel(context.Background(), k8sClient, service, custom)
		if err != nil {
			t.Fatalf("FindMatchingCustomModel() error = %v", err)
		}
		if result == nil || result.Name != model.Name {
			t.Fatalf("FindMatchingCustomModel() = %#v, want %s", result, model.Name)
		}
		if !modelSourcesMatch(result.Spec.ModelSources, custom.ModelSources) || result.Spec.Custom == nil {
			t.Fatalf("reused grandfathered model lost predecessor input: %#v", result.Spec)
		}
		if !equality.Semantic.DeepEqual(model, original) {
			t.Fatalf("stored grandfathered model was rewritten:\ngot:  %#v\nwant: %#v", model, original)
		}
		if _, annotated := model.Annotations[legacyaimmodel.AnnotationLegacyAutoCustomSpec]; annotated {
			t.Fatal("stored grandfathered model gained a compatibility annotation")
		}
		if len(model.OwnerReferences) != 0 {
			t.Fatalf("stored grandfathered model gained owner references: %#v", model.OwnerReferences)
		}
	})
}

func TestFindMatchingCustomModel_Collisions(t *testing.T) {
	service, custom, payload := newCustomModelMatchingFixture(t)

	t.Run("malformed payload at service-scoped name fails closed as invalid input", func(t *testing.T) {
		model := buildModelForCustom(service, custom, payload, GenerateCustomModelName(service, custom))
		model.Annotations[legacyaimmodel.AnnotationLegacyAutoCustomSpec] = `{"baseImage":`
		result, err := FindMatchingCustomModel(context.Background(), newFakeClient(service, model), service, custom)
		if result != nil || err == nil {
			t.Fatalf("FindMatchingCustomModel() = (%#v, %v), want nil error result and error", result, err)
		}
		if got := controllerutils.CategorizeError(err).Category(); got != controllerutils.ErrorCategoryInvalidSpec {
			t.Fatalf("error category = %s, want InvalidSpec", got)
		}
	})

	t.Run("malformed payload at pertinent predecessor name fails closed", func(t *testing.T) {
		model := buildModelForCustom(service, custom, payload, generateLegacyCustomModelName(custom))
		model.Annotations[legacyaimmodel.AnnotationLegacyAutoCustomSpec] = `{"baseImage":`
		result, err := FindMatchingCustomModel(context.Background(), newFakeClient(service, model), service, custom)
		if result != nil || err == nil {
			t.Fatalf("FindMatchingCustomModel() = (%#v, %v), want nil error result and error", result, err)
		}
		if got := controllerutils.CategorizeError(err).Category(); got != controllerutils.ErrorCategoryInvalidSpec {
			t.Fatalf("error category = %s, want InvalidSpec", got)
		}
	})

	t.Run("predecessor model owned by another service does not block creation", func(t *testing.T) {
		model := buildModelForCustom(service, custom, payload, generateLegacyCustomModelName(custom))
		second := NewService("second-source").Build()
		second.UID = types.UID("second-service-uid")
		second.Spec.Model = &aimv1alpha1.AIMServiceModel{Custom: custom.DeepCopy()}
		result, err := FindMatchingCustomModel(
			context.Background(),
			newFakeClient(service, second, model),
			second,
			second.Spec.Model.Custom,
		)
		if err != nil {
			t.Fatalf("FindMatchingCustomModel() error = %v", err)
		}
		if result != nil {
			t.Fatalf("FindMatchingCustomModel() = %#v, want nil so a scoped model is created", result)
		}
	})

	t.Run("same input model for another service does not block creation", func(t *testing.T) {
		first := service.DeepCopy()
		first.UID = types.UID("first-service-uid")
		second := NewService("second-source").Build()
		second.UID = types.UID("second-service-uid")
		second.Spec.Model = &aimv1alpha1.AIMServiceModel{Custom: custom.DeepCopy()}
		firstModel := buildModelForCustom(
			first,
			custom,
			payload,
			GenerateCustomModelName(first, custom),
		)

		result, err := FindMatchingCustomModel(
			context.Background(),
			newFakeClient(first, second, firstModel),
			second,
			second.Spec.Model.Custom,
		)
		if err != nil {
			t.Fatalf("FindMatchingCustomModel() error = %v", err)
		}
		if result != nil {
			t.Fatalf("FindMatchingCustomModel() = %#v, want nil so a scoped model is created", result)
		}
	})

	t.Run("other service at service-scoped name is a collision", func(t *testing.T) {
		other := NewService("other-source").Build()
		other.UID = types.UID("other-service-uid")
		other.Spec.Model = &aimv1alpha1.AIMServiceModel{Custom: custom.DeepCopy()}
		model := buildModelForCustom(
			other,
			custom,
			payload,
			GenerateCustomModelName(service, custom),
		)

		result, err := FindMatchingCustomModel(
			context.Background(),
			newFakeClient(service, other, model),
			service,
			custom,
		)
		if result != nil || err == nil {
			t.Fatalf("FindMatchingCustomModel() = (%#v, %v), want collision", result, err)
		}
		if got := controllerutils.CategorizeError(err).Category(); got != controllerutils.ErrorCategoryInvalidSpec {
			t.Fatalf("error category = %s, want InvalidSpec", got)
		}
	})

	t.Run("grandfathered object at service-scoped name is a collision", func(t *testing.T) {
		model := buildModelForCustom(service, custom, payload, GenerateCustomModelName(service, custom))
		model.Annotations = nil
		model.OwnerReferences = nil
		model.Spec.ModelSources = custom.ModelSources
		model.Spec.Custom = &aimv1alpha1.AIMCustomModelSpec{
			Hardware:      custom.Hardware.DeepCopy(),
			VersionPolicy: aimv1alpha1.AIMVersionPolicyPinned,
		}

		result, err := FindMatchingCustomModel(
			context.Background(),
			newFakeClient(service, model),
			service,
			custom,
		)
		if result != nil || err == nil {
			t.Fatalf("FindMatchingCustomModel() = (%#v, %v), want collision", result, err)
		}
		if got := controllerutils.CategorizeError(err).Category(); got != controllerutils.ErrorCategoryInvalidSpec {
			t.Fatalf("error category = %s, want InvalidSpec", got)
		}
	})

	t.Run("ordinary object at service-scoped name is a collision", func(t *testing.T) {
		model := &aimv1alpha2.AIMModel{}
		model.Name = GenerateCustomModelName(service, custom)
		model.Namespace = service.Namespace
		model.Spec.Image = custom.BaseImage
		result, err := FindMatchingCustomModel(context.Background(), newFakeClient(model), service, custom)
		if result != nil || err == nil {
			t.Fatalf("FindMatchingCustomModel() = (%#v, %v), want collision", result, err)
		}
	})
}
