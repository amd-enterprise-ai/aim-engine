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

package imagemetadata

import (
	"context"
	"errors"
	"testing"

	"k8s.io/utils/ptr"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

func TestFetch_SpecProvided(t *testing.T) {
	// When spec.ImageMetadata is set, it should be returned directly (air-gapped mode)
	specMetadata := &aimv1alpha1.ImageMetadata{
		Model: &aimv1alpha1.ModelMetadata{
			CanonicalName: "spec-provided-model",
		},
	}
	spec := aimv1alpha1.AIMModelSpec{
		Image:         "test:latest",
		ImageMetadata: specMetadata,
	}
	status := &aimv1alpha1.AIMModelStatus{}

	result := Fetch(context.Background(), nil, spec, status, "default")

	if result.HasError() {
		t.Errorf("expected no error, got %v", result.Error)
	}
	if result.Value != specMetadata {
		t.Error("expected spec-provided metadata to be returned")
	}
}

func TestFetch_AlreadyCached(t *testing.T) {
	// When status already has metadata, no fetch should occur
	spec := aimv1alpha1.AIMModelSpec{
		Image: "test:latest",
	}
	status := &aimv1alpha1.AIMModelStatus{
		ImageMetadata: &aimv1alpha1.ImageMetadata{
			Model: &aimv1alpha1.ModelMetadata{
				CanonicalName: "cached-model",
			},
		},
		ImageMetadataVersion: CurrentVersion,
	}

	result := Fetch(context.Background(), nil, spec, status, "default")

	// Should return empty result (no fetch needed)
	if result.HasError() {
		t.Errorf("expected no error, got %v", result.Error)
	}
	if result.Value != nil {
		t.Error("expected nil value when already cached (no fetch needed)")
	}
}

func TestFetch_ExtractionDisabledSkipsInspectionAndSpecMetadata(t *testing.T) {
	// The image reference is unparsable, so any inspection attempt would
	// surface an error.
	spec := aimv1alpha1.AIMModelSpec{
		Image:         "INVALID IMAGE REF",
		Discovery:     &aimv1alpha1.AIMModelDiscoveryConfig{ExtractMetadata: ptr.To(false)},
		ImageMetadata: &aimv1alpha1.ImageMetadata{Model: &aimv1alpha1.ModelMetadata{CanonicalName: "spec"}},
	}

	result := Fetch(context.Background(), nil, spec, &aimv1alpha1.AIMModelStatus{}, "default")

	if result.HasError() || result.Value != nil {
		t.Fatalf("expected an empty result when extraction is disabled, got value=%+v err=%v", result.Value, result.Error)
	}
}

// Older operators left status.imageMetadata holding only baseImageRef when
// inspection had not succeeded yet.
func TestFetch_StatusWithoutModelMetadata(t *testing.T) {
	specMetadata := &aimv1alpha1.ImageMetadata{Model: &aimv1alpha1.ModelMetadata{CanonicalName: "spec"}}
	tests := []struct {
		name      string
		spec      aimv1alpha1.AIMModelSpec
		wantValue *aimv1alpha1.ImageMetadata
		wantError bool
	}{
		{
			name:      "image is inspected",
			spec:      aimv1alpha1.AIMModelSpec{Image: "INVALID IMAGE REF"},
			wantError: true,
		},
		{
			name: "extraction disabled skips inspection",
			spec: aimv1alpha1.AIMModelSpec{
				Image:     "INVALID IMAGE REF",
				Discovery: &aimv1alpha1.AIMModelDiscoveryConfig{ExtractMetadata: ptr.To(false)},
			},
		},
		{
			name:      "spec-provided metadata wins",
			spec:      aimv1alpha1.AIMModelSpec{Image: "INVALID IMAGE REF", ImageMetadata: specMetadata},
			wantValue: specMetadata,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := &aimv1alpha1.AIMModelStatus{
				ImageMetadata: &aimv1alpha1.ImageMetadata{BaseImageRef: "registry.example/aim-base:1.0"},
			}

			result := Fetch(context.Background(), nil, tt.spec, status, "default")

			if result.HasError() != tt.wantError {
				t.Fatalf("error = %v, want error %v", result.Error, tt.wantError)
			}
			if result.Value != tt.wantValue {
				t.Fatalf("value = %+v, want %+v", result.Value, tt.wantValue)
			}
		})
	}
}

func TestShouldExtractMetadata(t *testing.T) {
	tests := []struct {
		name     string
		status   *aimv1alpha1.AIMModelStatus
		expected bool
	}{
		{
			name:     "nil status",
			status:   nil,
			expected: true,
		},
		{
			name:     "status with nil metadata",
			status:   &aimv1alpha1.AIMModelStatus{},
			expected: true,
		},
		{
			name: "status with current metadata",
			status: &aimv1alpha1.AIMModelStatus{
				ImageMetadata: &aimv1alpha1.ImageMetadata{
					Model: &aimv1alpha1.ModelMetadata{
						CanonicalName: "test-model",
					},
				},
				ImageMetadataVersion: CurrentVersion,
			},
			expected: false,
		},
		{
			name: "current metadata from an image without AIM labels or base image reference",
			status: &aimv1alpha1.AIMModelStatus{
				ImageMetadata:        &aimv1alpha1.ImageMetadata{Model: &aimv1alpha1.ModelMetadata{}},
				ImageMetadataVersion: CurrentVersion,
			},
			expected: false,
		},
		{
			name: "metadata cached before base image extraction is inspected again",
			status: &aimv1alpha1.AIMModelStatus{
				ImageMetadata: &aimv1alpha1.ImageMetadata{
					Model: &aimv1alpha1.ModelMetadata{CanonicalName: "test-model"},
				},
			},
			expected: true,
		},
		{
			name: "metadata cached with a base image reference needs no new inspection",
			status: &aimv1alpha1.AIMModelStatus{
				ImageMetadata: &aimv1alpha1.ImageMetadata{
					Model:        &aimv1alpha1.ModelMetadata{CanonicalName: "test-model"},
					BaseImageRef: "docker.io/amdenterpriseai/aim-base:0.11.2",
				},
			},
			expected: false,
		},
		{
			name: "status with only baseImageRef",
			status: &aimv1alpha1.AIMModelStatus{
				ImageMetadata: &aimv1alpha1.ImageMetadata{BaseImageRef: "registry.example/aim-base:1.0"},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldExtractMetadata(tt.status)
			if result != tt.expected {
				t.Errorf("shouldExtractMetadata() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestResultForHealth(t *testing.T) {
	cached := &aimv1alpha1.ImageMetadata{Model: &aimv1alpha1.ModelMetadata{CanonicalName: "cached"}}
	failed := controllerutils.FetchResult[*aimv1alpha1.ImageMetadata]{Error: errors.New("401 unauthorized")}

	t.Run("failed re-inspection of old cached metadata reports the cache", func(t *testing.T) {
		got := ResultForHealth(failed, &aimv1alpha1.AIMModelStatus{ImageMetadata: cached})
		if got.HasError() || got.Value != cached {
			t.Fatalf("result = %+v, want cached metadata without error", got)
		}
	})
	t.Run("failed first inspection keeps the error", func(t *testing.T) {
		got := ResultForHealth(failed, &aimv1alpha1.AIMModelStatus{})
		if !got.HasError() {
			t.Fatal("expected the inspection error to be kept")
		}
	})
	t.Run("failed inspection of current metadata keeps the error", func(t *testing.T) {
		got := ResultForHealth(failed, &aimv1alpha1.AIMModelStatus{
			ImageMetadata:        cached,
			ImageMetadataVersion: CurrentVersion,
		})
		if !got.HasError() {
			t.Fatal("expected the inspection error to be kept")
		}
	})
}
