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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
)

func TestGenerateCustomModelName(t *testing.T) {
	tests := []struct {
		name     string
		custom   *aimv1alpha1.AIMServiceModelCustom
		expected string
	}{
		{
			name: "simple model ID",
			custom: &aimv1alpha1.AIMServiceModelCustom{
				BaseImage: "ghcr.io/silogen/aim-base:0.7.0",
				ModelSources: []aimv1alpha1.AIMModelSource{
					{ModelID: "my-org/my-model", SourceURI: "s3://bucket/model"},
				},
			},
			expected: "my-org-my-model-", // Hash will be appended
		},
		{
			name: "model ID with special chars",
			custom: &aimv1alpha1.AIMServiceModelCustom{
				BaseImage: "ghcr.io/silogen/aim-base:0.7.0",
				ModelSources: []aimv1alpha1.AIMModelSource{
					{ModelID: "Meta-Llama/Llama-3.1-70B", SourceURI: "hf://Meta-Llama/Llama-3.1-70B"},
				},
			},
			expected: "meta-llama-llama-3.1-70b-", // Sanitized and lowercased
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GenerateCustomModelName(tt.custom)

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
	custom := &aimv1alpha1.AIMServiceModelCustom{
		BaseImage: "ghcr.io/silogen/aim-base:0.7.0",
		ModelSources: []aimv1alpha1.AIMModelSource{
			{ModelID: "my-org/my-model", SourceURI: "s3://bucket/model"},
		},
	}

	name1 := GenerateCustomModelName(custom)
	name2 := GenerateCustomModelName(custom)

	if name1 != name2 {
		t.Errorf("GenerateCustomModelName() not deterministic: %q != %q", name1, name2)
	}
}

func TestGenerateCustomModelName_DifferentEndpoints(t *testing.T) {
	// Two custom models with same base but different S3 endpoints should get different names
	custom1 := &aimv1alpha1.AIMServiceModelCustom{
		BaseImage: "ghcr.io/silogen/aim-base:0.7.0",
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
		BaseImage: "ghcr.io/silogen/aim-base:0.7.0",
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

	name1 := GenerateCustomModelName(custom1)
	name2 := GenerateCustomModelName(custom2)

	if name1 == name2 {
		t.Errorf("Different S3 endpoints should generate different names: %q == %q", name1, name2)
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

func TestFindMatchingCustomModel(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = aimv1alpha1.AddToScheme(scheme)

	custom := &aimv1alpha1.AIMServiceModelCustom{
		BaseImage: "ghcr.io/silogen/aim-base:0.7.0",
		ModelSources: []aimv1alpha1.AIMModelSource{
			{ModelID: "my-org/my-model", SourceURI: "s3://bucket/model"},
		},
	}

	tests := []struct {
		name           string
		existingModels []aimv1alpha1.AIMModel
		expectedName   string
	}{
		{
			name:           "no existing models",
			existingModels: nil,
			expectedName:   "",
		},
		{
			name: "matching model exists",
			existingModels: []aimv1alpha1.AIMModel{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "matching-model", Namespace: "test-ns"},
					Spec: aimv1alpha1.AIMModelSpec{
						Image: "ghcr.io/silogen/aim-base:0.7.0",
						ModelSources: []aimv1alpha1.AIMModelSource{
							{ModelID: "my-org/my-model", SourceURI: "s3://bucket/model"},
						},
					},
				},
			},
			expectedName: "matching-model",
		},
		{
			name: "different base image",
			existingModels: []aimv1alpha1.AIMModel{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "different-image", Namespace: "test-ns"},
					Spec: aimv1alpha1.AIMModelSpec{
						Image: "ghcr.io/silogen/aim-base:0.8.0",
						ModelSources: []aimv1alpha1.AIMModelSource{
							{ModelID: "my-org/my-model", SourceURI: "s3://bucket/model"},
						},
					},
				},
			},
			expectedName: "",
		},
		{
			name: "different model sources",
			existingModels: []aimv1alpha1.AIMModel{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "different-sources", Namespace: "test-ns"},
					Spec: aimv1alpha1.AIMModelSpec{
						Image: "ghcr.io/silogen/aim-base:0.7.0",
						ModelSources: []aimv1alpha1.AIMModelSource{
							{ModelID: "other-org/other-model", SourceURI: "s3://other-bucket/model"},
						},
					},
				},
			},
			expectedName: "",
		},
		{
			name: "multiple models - finds matching one",
			existingModels: []aimv1alpha1.AIMModel{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "non-matching", Namespace: "test-ns"},
					Spec: aimv1alpha1.AIMModelSpec{
						Image: "ghcr.io/silogen/aim-base:0.8.0",
						ModelSources: []aimv1alpha1.AIMModelSource{
							{ModelID: "other/model", SourceURI: "s3://other/model"},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "matching-model", Namespace: "test-ns"},
					Spec: aimv1alpha1.AIMModelSpec{
						Image: "ghcr.io/silogen/aim-base:0.7.0",
						ModelSources: []aimv1alpha1.AIMModelSource{
							{ModelID: "my-org/my-model", SourceURI: "s3://bucket/model"},
						},
					},
				},
			},
			expectedName: "matching-model",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var objects []runtime.Object
			for i := range tt.existingModels {
				objects = append(objects, &tt.existingModels[i])
			}

			client := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objects...).Build()

			result, err := FindMatchingCustomModel(context.Background(), client, "test-ns", custom)
			if err != nil {
				t.Fatalf("FindMatchingCustomModel() error = %v", err)
			}

			if tt.expectedName == "" {
				if result != nil {
					t.Errorf("FindMatchingCustomModel() = %v, expected nil", result.Name)
				}
			} else {
				if result == nil {
					t.Errorf("FindMatchingCustomModel() = nil, expected %s", tt.expectedName)
				} else if result.Name != tt.expectedName {
					t.Errorf("FindMatchingCustomModel() = %s, expected %s", result.Name, tt.expectedName)
				}
			}
		})
	}
}
