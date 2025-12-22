/*
MIT License

Copyright (c) 2025 Advanced Micro Devices, Inc.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package aimclustermodelsource

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
)

// Test constants
const testImageURI = "ghcr.io/org/model:1.0.0"

func TestGenerateModelName(t *testing.T) {
	tests := []struct {
		name       string
		imageURI   string
		wantPrefix string
		wantMaxLen int
	}{
		{
			name:       "basic image with tag",
			imageURI:   "ghcr.io/silogen/aim-llama:1.0.0",
			wantPrefix: "aim-llama-",
			wantMaxLen: 63,
		},
		{
			name:       "docker hub image",
			imageURI:   "docker.io/library/ubuntu:22.04",
			wantPrefix: "ubuntu-",
			wantMaxLen: 63,
		},
		{
			name:       "long image name is truncated",
			imageURI:   "ghcr.io/silogen/aim-mistralai-mistral-small-3.2-24b-instruct-2506:0.8.5",
			wantPrefix: "aim-mistralai-mistral-small-3-2-24b-instruct-",
			wantMaxLen: 63,
		},
		{
			name:       "special characters sanitized",
			imageURI:   "ghcr.io/org/My_Model.Name:v1.0.0",
			wantPrefix: "my-model-name-",
			wantMaxLen: 63,
		},
		{
			name:       "latest tag included",
			imageURI:   "docker.io/org/model:latest",
			wantPrefix: "model-latest-",
			wantMaxLen: 63,
		},
		{
			name:       "no tag defaults to latest",
			imageURI:   "docker.io/org/model",
			wantPrefix: "model-latest-",
			wantMaxLen: 63,
		},
		{
			name:       "digest reference uses short digest",
			imageURI:   "ghcr.io/org/model@sha256:abc123def456789",
			wantPrefix: "model-abc123-",
			wantMaxLen: 63,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := generateModelName(tt.imageURI)

			// Check max length
			if len(got) > tt.wantMaxLen {
				t.Errorf("generateModelName() returned name with length %d, want <= %d: %q", len(got), tt.wantMaxLen, got)
			}

			// Check prefix
			if !strings.HasPrefix(got, tt.wantPrefix) {
				t.Errorf("generateModelName() = %q, want prefix %q", got, tt.wantPrefix)
			}

			// Check for valid Kubernetes name characters
			for _, c := range got {
				isLower := c >= 'a' && c <= 'z'
				isDigit := c >= '0' && c <= '9'
				isDash := c == '-'
				if !isLower && !isDigit && !isDash {
					t.Errorf("generateModelName() = %q contains invalid character %q", got, string(c))
				}
			}

			// Check doesn't start or end with dash
			if strings.HasPrefix(got, "-") {
				t.Errorf("generateModelName() = %q starts with dash", got)
			}
			if strings.HasSuffix(got, "-") {
				t.Errorf("generateModelName() = %q ends with dash", got)
			}
		})
	}
}

func TestGenerateModelName_Deterministic(t *testing.T) {
	// Generate name multiple times
	name1 := generateModelName(testImageURI)
	name2 := generateModelName(testImageURI)
	name3 := generateModelName(testImageURI)

	// All should be identical
	if name1 != name2 || name2 != name3 {
		t.Errorf("generateModelName() is not deterministic: %q, %q, %q", name1, name2, name3)
	}
}

func TestGenerateModelName_UniqueForDifferentImages(t *testing.T) {
	uri2 := "ghcr.io/org/model:2.0.0"
	uri3 := "docker.io/org/model:1.0.0"

	name1 := generateModelName(testImageURI)
	name2 := generateModelName(uri2)
	name3 := generateModelName(uri3)

	// All should be different (hash ensures uniqueness)
	if name1 == name2 {
		t.Errorf("names should differ for different tags: %q == %q", name1, name2)
	}
	if name1 == name3 {
		t.Errorf("names should differ for different registries: %q == %q", name1, name3)
	}
	if name2 == name3 {
		t.Errorf("names should differ: %q == %q", name2, name3)
	}
}

func TestGenerateModelName_HashSuffix(t *testing.T) {
	name := generateModelName(testImageURI)

	// Should end with 8-character hex hash
	parts := strings.Split(name, "-")
	if len(parts) < 2 {
		t.Fatalf("expected name to have at least 2 parts separated by dash: %q", name)
	}

	hash := parts[len(parts)-1]
	if len(hash) != 8 {
		t.Errorf("hash suffix should be 8 characters, got %d: %q", len(hash), hash)
	}

	// Should be valid hex
	for _, c := range hash {
		isHexDigit := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
		if !isHexDigit {
			t.Errorf("hash suffix contains non-hex character: %q in %q", string(c), hash)
		}
	}
}

func TestSanitizeNameComponent(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "lowercase conversion",
			input: "MySource",
			want:  "mysource",
		},
		{
			name:  "underscore to dash",
			input: "my_source",
			want:  "my-source",
		},
		{
			name:  "dot to dash",
			input: "my.source",
			want:  "my-source",
		},
		{
			name:  "multiple special chars",
			input: "my_source.name__test",
			want:  "my-source-name-test",
		},
		{
			name:  "trim leading dashes",
			input: "--leading",
			want:  "leading",
		},
		{
			name:  "trim trailing dashes",
			input: "trailing--",
			want:  "trailing",
		},
		{
			name:  "collapse multiple dashes",
			input: "multi---dash",
			want:  "multi-dash",
		},
		{
			name:  "already valid",
			input: "valid-name-123",
			want:  "valid-name-123",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "only special chars",
			input: "___",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeNameComponent(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeNameComponent(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestBuildClusterModel(t *testing.T) {
	source := &aimv1alpha1.AIMClusterModelSource{
		ObjectMeta: metav1.ObjectMeta{
			Name: testSourceName,
		},
	}

	img := RegistryImage{
		Registry:   "ghcr.io",
		Repository: "silogen/aim-llama",
		Tag:        "1.0.0",
	}

	model := buildClusterModel(source, img)

	// Check name is set
	if model.Name == "" {
		t.Error("model name should not be empty")
	}

	// Check name length
	if len(model.Name) > 63 {
		t.Errorf("model name length %d exceeds 63", len(model.Name))
	}

	// Check label is set
	if model.Labels[LabelKeyModelSource] != testSourceName {
		t.Errorf("model source label = %q, want %q", model.Labels[LabelKeyModelSource], testSourceName)
	}

	// Check image is set correctly
	expectedImage := "ghcr.io/silogen/aim-llama:1.0.0"
	if model.Spec.Image != expectedImage {
		t.Errorf("model image = %q, want %q", model.Spec.Image, expectedImage)
	}
}

func TestBuildClusterModel_DockerHub(t *testing.T) {
	source := &aimv1alpha1.AIMClusterModelSource{
		ObjectMeta: metav1.ObjectMeta{
			Name: "docker-source",
		},
	}

	img := RegistryImage{
		Registry:   "docker.io",
		Repository: "library/ubuntu",
		Tag:        "22.04",
	}

	model := buildClusterModel(source, img)

	// Docker Hub images don't include registry prefix
	expectedImage := "library/ubuntu:22.04"
	if model.Spec.Image != expectedImage {
		t.Errorf("model image = %q, want %q", model.Spec.Image, expectedImage)
	}
}

func TestRegistryImage_ToImageURI(t *testing.T) {
	tests := []struct {
		name string
		img  RegistryImage
		want string
	}{
		{
			name: "ghcr.io image",
			img:  RegistryImage{Registry: "ghcr.io", Repository: "org/model", Tag: "1.0.0"},
			want: "ghcr.io/org/model:1.0.0",
		},
		{
			name: "docker.io image",
			img:  RegistryImage{Registry: "docker.io", Repository: "library/ubuntu", Tag: "22.04"},
			want: "library/ubuntu:22.04",
		},
		{
			name: "empty registry",
			img:  RegistryImage{Registry: "", Repository: "org/model", Tag: "1.0.0"},
			want: "org/model:1.0.0",
		},
		{
			name: "custom registry",
			img:  RegistryImage{Registry: "my-registry.example.com", Repository: "team/app", Tag: "latest"},
			want: "my-registry.example.com/team/app:latest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.img.ToImageURI()
			if got != tt.want {
				t.Errorf("ToImageURI() = %q, want %q", got, tt.want)
			}
		})
	}
}
