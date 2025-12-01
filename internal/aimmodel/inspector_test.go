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

package aimmodel

import (
	"reflect"
	"testing"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
)

// ============================================================================
// LABEL PARSING TESTS
// ============================================================================

func TestGetAMDLabel(t *testing.T) {
	tests := []struct {
		name     string
		labels   map[string]string
		suffix   string
		expected string
	}{
		{
			name: "new prefix only",
			labels: map[string]string{
				"com.amd.aim.model.canonicalName": "test-model",
			},
			suffix:   "model.canonicalName",
			expected: "test-model",
		},
		{
			name: "legacy prefix only",
			labels: map[string]string{
				"org.amd.silogen.model.canonicalName": "legacy-model",
			},
			suffix:   "model.canonicalName",
			expected: "legacy-model",
		},
		{
			name: "both prefixes - new takes priority",
			labels: map[string]string{
				"com.amd.aim.model.canonicalName":     "new-model",
				"org.amd.silogen.model.canonicalName": "legacy-model",
			},
			suffix:   "model.canonicalName",
			expected: "new-model",
		},
		{
			name: "new prefix empty - falls back to legacy",
			labels: map[string]string{
				"com.amd.aim.model.canonicalName":     "",
				"org.amd.silogen.model.canonicalName": "legacy-model",
			},
			suffix:   "model.canonicalName",
			expected: "legacy-model",
		},
		{
			name:     "neither prefix exists",
			labels:   map[string]string{},
			suffix:   "model.canonicalName",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getAMDLabel(tt.labels, tt.suffix)
			if result != tt.expected {
				t.Errorf("getAMDLabel() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestParseCommaSeparated(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "single item",
			input:    "tag1",
			expected: []string{"tag1"},
		},
		{
			name:     "multiple items",
			input:    "tag1,tag2,tag3",
			expected: []string{"tag1", "tag2", "tag3"},
		},
		{
			name:     "items with spaces",
			input:    "tag1 , tag2 , tag3",
			expected: []string{"tag1", "tag2", "tag3"},
		},
		{
			name:     "empty string",
			input:    "",
			expected: []string{},
		},
		{
			name:     "trailing comma",
			input:    "tag1,tag2,",
			expected: []string{"tag1", "tag2"},
		},
		{
			name:     "leading comma",
			input:    ",tag1,tag2",
			expected: []string{"tag1", "tag2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseCommaSeparated(tt.input)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("parseCommaSeparated() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestParseRecommendedDeployments(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expected    []aimv1alpha1.RecommendedDeployment
		expectError bool
	}{
		{
			name:  "JSON array",
			input: `[{"gpuModel": "MI300X", "gpuCount": 1, "metric": "throughput", "precision": "fp16"}]`,
			expected: []aimv1alpha1.RecommendedDeployment{
				{
					GPUModel:  "MI300X",
					GPUCount:  1,
					Metric:    "throughput",
					Precision: "fp16",
				},
			},
			expectError: false,
		},
		{
			name:  "Python-style dict with single quotes",
			input: `[{'gpuModel': 'MI300X', 'gpuCount': 2, 'metric': 'latency'}]`,
			expected: []aimv1alpha1.RecommendedDeployment{
				{
					GPUModel: "MI300X",
					GPUCount: 2,
					Metric:   "latency",
				},
			},
			expectError: false,
		},
		{
			name:  "comma-separated objects without brackets",
			input: `{"gpuModel": "MI300X", "gpuCount": 1}, {"gpuModel": "MI300X", "gpuCount": 2}`,
			expected: []aimv1alpha1.RecommendedDeployment{
				{
					GPUModel: "MI300X",
					GPUCount: 1,
				},
				{
					GPUModel: "MI300X",
					GPUCount: 2,
				},
			},
			expectError: false,
		},
		{
			name:  "Python-style without brackets",
			input: `{'gpuModel': 'MI300X', 'gpuCount': 1}`,
			expected: []aimv1alpha1.RecommendedDeployment{
				{
					GPUModel: "MI300X",
					GPUCount: 1,
				},
			},
			expectError: false,
		},
		{
			name:        "invalid JSON",
			input:       `{invalid json`,
			expected:    nil,
			expectError: true,
		},
		{
			name:  "multiple deployments with all fields",
			input: `[{"gpuModel": "MI300X", "gpuCount": 1, "metric": "throughput", "precision": "fp16"}, {"gpuModel": "MI300X", "gpuCount": 4, "metric": "latency", "precision": "fp32"}]`,
			expected: []aimv1alpha1.RecommendedDeployment{
				{
					GPUModel:  "MI300X",
					GPUCount:  1,
					Metric:    "throughput",
					Precision: "fp16",
				},
				{
					GPUModel:  "MI300X",
					GPUCount:  4,
					Metric:    "latency",
					Precision: "fp32",
				},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseRecommendedDeployments(tt.input)
			if tt.expectError {
				if err == nil {
					t.Error("expected error but got none")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("parseRecommendedDeployments() = %+v, want %+v", result, tt.expected)
			}
		})
	}
}

func TestParseImageLabels_Empty(t *testing.T) {
	labels := map[string]string{}

	metadata, err := parseImageLabels(labels)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if metadata == nil {
		t.Fatal("expected metadata to be non-nil")
	}
	if metadata.OCI == nil {
		t.Error("expected OCI metadata to be initialized")
	}
	if metadata.Model == nil {
		t.Error("expected Model metadata to be initialized")
	}
	if metadata.Model.CanonicalName != "" {
		t.Error("expected CanonicalName to be empty")
	}
}

func TestParseImageLabels_OCIStandard(t *testing.T) {
	labels := map[string]string{
		"org.opencontainers.image.title":         "Test Image",
		"org.opencontainers.image.description":   "A test image",
		"org.opencontainers.image.licenses":      "MIT",
		"org.opencontainers.image.vendor":        "AMD",
		"org.opencontainers.image.authors":       "AMD Team",
		"org.opencontainers.image.source":        "https://github.com/test/repo",
		"org.opencontainers.image.documentation": "https://docs.test.com",
		"org.opencontainers.image.created":       "2025-01-01T00:00:00Z",
		"org.opencontainers.image.revision":      "abc123",
		"org.opencontainers.image.version":       "1.0.0",
	}

	metadata, err := parseImageLabels(labels)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if metadata.OCI.Title != "Test Image" {
		t.Error("expected OCI title to be set")
	}
	if metadata.OCI.Description != "A test image" {
		t.Error("expected OCI description to be set")
	}
	if metadata.OCI.Vendor != "AMD" {
		t.Error("expected OCI vendor to be set")
	}
}

func TestParseImageLabels_AMDModelLabels(t *testing.T) {
	labels := map[string]string{
		"com.amd.aim.model.canonicalName": "llama-3.1-8b",
		"com.amd.aim.model.source":        "hf://meta-llama/Llama-3.1-8B",
		"com.amd.aim.title":               "Llama 3.1 8B",
		"com.amd.aim.description.full":    "Large language model",
		"com.amd.aim.release.notes":       "Release notes here",
		"com.amd.aim.model.tags":          "llm,chat,instruction",
		"com.amd.aim.model.versions":      "3.1,3.1.0",
		"com.amd.aim.model.variants":      "base,instruct",
		"com.amd.aim.hfToken.required":    "true",
	}

	metadata, err := parseImageLabels(labels)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if metadata.Model.CanonicalName != "llama-3.1-8b" {
		t.Error("expected CanonicalName to be set")
	}
	if metadata.Model.Source != "hf://meta-llama/Llama-3.1-8B" {
		t.Error("expected Source to be set")
	}
	if !metadata.Model.HFTokenRequired {
		t.Error("expected HFTokenRequired to be true")
	}

	expectedTags := []string{"llm", "chat", "instruction"}
	if !reflect.DeepEqual(metadata.Model.Tags, expectedTags) {
		t.Errorf("expected Tags=%v, got %v", expectedTags, metadata.Model.Tags)
	}

	expectedVersions := []string{"3.1", "3.1.0"}
	if !reflect.DeepEqual(metadata.Model.Versions, expectedVersions) {
		t.Errorf("expected Versions=%v, got %v", expectedVersions, metadata.Model.Versions)
	}
}

func TestParseImageLabels_LegacyPrefix(t *testing.T) {
	labels := map[string]string{
		"org.amd.silogen.model.canonicalName": "legacy-model",
		"org.amd.silogen.model.source":        "hf://legacy/model",
	}

	metadata, err := parseImageLabels(labels)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if metadata.Model.CanonicalName != "legacy-model" {
		t.Error("expected CanonicalName from legacy prefix")
	}
	if metadata.Model.Source != "hf://legacy/model" {
		t.Error("expected Source from legacy prefix")
	}
}

func TestParseImageLabels_WithRecommendedDeployments(t *testing.T) {
	labels := map[string]string{
		"com.amd.aim.model.canonicalName":          "test-model",
		"com.amd.aim.model.recommendedDeployments": `[{"gpuModel": "MI300X", "gpuCount": 1, "metric": "throughput", "precision": "fp16"}]`,
	}

	metadata, err := parseImageLabels(labels)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if len(metadata.Model.RecommendedDeployments) != 1 {
		t.Fatalf("expected 1 recommended deployment, got %d", len(metadata.Model.RecommendedDeployments))
	}

	deployment := metadata.Model.RecommendedDeployments[0]
	if deployment.GPUModel != "MI300X" {
		t.Errorf("expected GPUModel=MI300X, got %s", deployment.GPUModel)
	}
	if deployment.GPUCount != 1 {
		t.Errorf("expected GPUCount=1, got %d", deployment.GPUCount)
	}
}

func TestParseImageLabels_InvalidRecommendedDeployments(t *testing.T) {
	labels := map[string]string{
		"com.amd.aim.model.recommendedDeployments": `{invalid json`,
	}

	_, err := parseImageLabels(labels)
	if err == nil {
		t.Error("expected error for invalid recommended deployments JSON")
	}
}

func TestParseImageLabels_InvalidHFToken(t *testing.T) {
	labels := map[string]string{
		"com.amd.aim.hfToken.required": "not-a-bool",
	}

	metadata, err := parseImageLabels(labels)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Invalid boolean should be silently ignored, not cause error
	if metadata.Model.HFTokenRequired {
		t.Error("expected HFTokenRequired to be false when invalid value provided")
	}
}

func TestMetadataFormatError_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      *metadataFormatError
		expected string
	}{
		{
			name: "with message",
			err: &metadataFormatError{
				Reason:  "TestReason",
				Message: "test error message",
			},
			expected: "test error message",
		},
		{
			name: "without message",
			err: &metadataFormatError{
				Reason: "TestReason",
			},
			expected: "image metadata malformed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.err.Error()
			if result != tt.expected {
				t.Errorf("Error() = %q, want %q", result, tt.expected)
			}
		})
	}
}

// ============================================================================
// EDGE CASE TESTS
// ============================================================================

func TestParseImageLabels_MixedPrefixes(t *testing.T) {
	labels := map[string]string{
		"com.amd.aim.model.canonicalName": "new-model",
		"org.amd.silogen.model.source":    "legacy-source",
		"com.amd.aim.model.tags":          "tag1,tag2",
		"org.amd.silogen.model.versions":  "v1,v2",
	}

	metadata, err := parseImageLabels(labels)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// New prefix should take priority
	if metadata.Model.CanonicalName != "new-model" {
		t.Error("expected new prefix to take priority for CanonicalName")
	}

	// Legacy should be used where new is missing
	if metadata.Model.Source != "legacy-source" {
		t.Error("expected legacy prefix to be used for Source")
	}

	// Should parse both new and legacy comma-separated fields
	if len(metadata.Model.Tags) != 2 {
		t.Error("expected Tags to be parsed from new prefix")
	}
	if len(metadata.Model.Versions) != 2 {
		t.Error("expected Versions to be parsed from legacy prefix")
	}
}

func TestParseRecommendedDeployments_PartialFields(t *testing.T) {
	input := `[{"gpuModel": "MI300X"}]`

	result, err := parseRecommendedDeployments(input)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if len(result) != 1 {
		t.Fatalf("expected 1 deployment, got %d", len(result))
	}

	// GPUCount should be 0 (default) when not specified
	if result[0].GPUModel != "MI300X" {
		t.Error("expected GPUModel to be set")
	}
	if result[0].GPUCount != 0 {
		t.Error("expected GPUCount to be 0 when not specified")
	}
	if result[0].Metric != "" {
		t.Error("expected Metric to be empty when not specified")
	}
}
