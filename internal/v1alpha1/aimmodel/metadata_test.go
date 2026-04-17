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
	"errors"
	"fmt"
	"testing"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

func TestIsTransientMetadataError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name: "registry auth error - transient",
			err: &utils.ImageRegistryError{
				Type:    utils.ImagePullErrorAuth,
				Message: "authentication failed: 401 unauthorized",
			},
			expected: true,
		},
		{
			name: "registry not found error - permanent",
			err: &utils.ImageRegistryError{
				Type:    utils.ImagePullErrorNotFound,
				Message: "image not found: 404",
			},
			expected: false,
		},
		{
			name: "registry generic error - transient (network issues)",
			err: &utils.ImageRegistryError{
				Type:    utils.ImagePullErrorGeneric,
				Message: "connection timeout",
			},
			expected: true,
		},
		{
			name: "format error - missing recommended deployments - permanent",
			err: &metadataFormatError{
				Reason:  "MetadataMissingRecommendedDeployments",
				Message: "auto template creation requires image label with at least one entry",
			},
			expected: false,
		},
		{
			name: "format error - invalid JSON - permanent",
			err: &metadataFormatError{
				Reason:  "InvalidMetadataFormat",
				Message: "failed to parse recommended deployments JSON",
			},
			expected: false,
		},
		{
			name:     "unknown error - assumed transient",
			err:      errors.New("some unexpected error"),
			expected: true,
		},
		{
			name:     "wrapped registry auth error - transient",
			err:      fmt.Errorf("metadata required for template creation: %w", &utils.ImageRegistryError{Type: utils.ImagePullErrorAuth, Message: "403 forbidden"}),
			expected: true,
		},
		{
			name:     "wrapped format error - permanent",
			err:      fmt.Errorf("failed to extract metadata: %w", &metadataFormatError{Reason: "InvalidFormat", Message: "malformed"}),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isTransientMetadataError(tt.err)
			if result != tt.expected {
				t.Errorf("isTransientMetadataError() = %v, want %v for error: %v", result, tt.expected, tt.err)
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
			name: "status with existing metadata",
			status: &aimv1alpha1.AIMModelStatus{
				ImageMetadata: &aimv1alpha1.ImageMetadata{
					Model: &aimv1alpha1.ModelMetadata{
						CanonicalName: "test-model",
					},
				},
			},
			expected: false,
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
