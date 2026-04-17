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

package aimmodel

import (
	"errors"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

// isTransientMetadataError checks if a metadata error is transient (recoverable with retry)
// vs permanent (image not found, invalid format).
// Transient errors should trigger K8s exponential backoff retry when metadata is required.
// Permanent errors should be surfaced as status conditions without retry.
func isTransientMetadataError(err error) bool {
	if err == nil {
		return false
	}

	var regErr *utils.ImageRegistryError
	if errors.As(err, &regErr) {
		switch regErr.Type {
		case utils.ImagePullErrorNotFound:
			// Image/manifest not found (404) is permanent - the resource doesn't exist
			return false
		case utils.ImagePullErrorAuth:
			// Auth failures (401/403) are transient - credentials might be updated
			return true
		case utils.ImagePullErrorGeneric:
			// Generic errors (5xx server errors, network timeouts, etc.) are transient
			return true
		default:
			// Unknown error types assumed transient for safety
			return true
		}
	}

	var fmtErr *metadataFormatError
	if errors.As(err, &fmtErr) {
		// Format errors are permanent - the metadata is malformed and won't fix itself
		return false
	}

	// Unknown errors are assumed transient (network issues, timeouts, etc.)
	return true
}

// ==============
// UTILS
// ==============

// shouldExtractMetadata checks if metadata extraction should be attempted.
// Returns false if metadata already exists in status (cached).
func shouldExtractMetadata(status *aimv1alpha1.AIMModelStatus) bool {
	return status == nil || status.ImageMetadata == nil
}
