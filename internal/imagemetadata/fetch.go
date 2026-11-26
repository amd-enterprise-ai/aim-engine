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

// Package imagemetadata reads AIM model metadata from a container image's OCI
// labels and environment.
package imagemetadata

import (
	"context"

	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

type fetchResultKey struct{}

// WithFetchResult returns a context in which Fetch returns result instead of
// reading the registry. The image-model controllers fetch before they take the
// discovery lock, so a slow registry never holds it.
func WithFetchResult(ctx context.Context, result controllerutils.FetchResult[*aimv1alpha1.ImageMetadata]) context.Context {
	return context.WithValue(ctx, fetchResultKey{}, result)
}

// Fetch determines how to obtain image metadata for a model. A result set by
// WithFetchResult takes precedence. Otherwise it handles these cases:
//  1. Extraction explicitly disabled - skip fetch entirely
//  2. Spec-provided metadata (air-gapped environments) - returns the spec value directly
//  3. Fine-tuned model - deployments come from the base model matched by aimId, skip fetch
//  4. Already inspected (status holds model metadata) - returns empty result (no fetch needed)
//  5. Needs remote fetch - calls inspectImage to fetch from registry
func Fetch(
	ctx context.Context,
	clientset kubernetes.Interface,
	spec aimv1alpha1.AIMModelSpec,
	status *aimv1alpha1.AIMModelStatus,
	secretNamespace string,
) controllerutils.FetchResult[*aimv1alpha1.ImageMetadata] {
	if result, ok := ctx.Value(fetchResultKey{}).(controllerutils.FetchResult[*aimv1alpha1.ImageMetadata]); ok {
		return result
	}

	// Case 1: Extraction explicitly disabled - skip fetch entirely
	if !spec.Discovery.IsExtractMetadataEnabled() {
		log.FromContext(ctx).V(1).Info("metadata extraction disabled in spec")
		return controllerutils.FetchResult[*aimv1alpha1.ImageMetadata]{}
	}

	// Case 2: Use spec-provided metadata (air-gapped environments)
	if spec.ImageMetadata != nil {
		log.FromContext(ctx).V(1).Info("using spec-provided imageMetadata")
		return controllerutils.FetchResult[*aimv1alpha1.ImageMetadata]{
			Value: spec.ImageMetadata,
		}
	}

	// Case 3: Fine-tuned models skip: their deployments come from the base
	// model matched by aimId.
	if spec.IsFineTunedModel() {
		return controllerutils.FetchResult[*aimv1alpha1.ImageMetadata]{}
	}

	// Case 4: Already inspected - no fetch needed
	if !shouldExtractMetadata(status) {
		return controllerutils.FetchResult[*aimv1alpha1.ImageMetadata]{}
	}

	// Case 5: Fetch from registry
	metadata, err := inspectImage(
		ctx,
		spec.Image,
		spec.ImagePullSecrets,
		clientset,
		secretNamespace,
	)
	return controllerutils.FetchResult[*aimv1alpha1.ImageMetadata]{
		Value: metadata,
		Error: err,
	}
}

// CurrentVersion marks status metadata produced by the current extraction
// contract, which records AIM_BASE_IMAGE_REF.
const CurrentVersion int32 = 1

// shouldExtractMetadata checks if metadata extraction should be attempted.
// Inspection always sets Model, so metadata without it (e.g. only
// baseImageRef) was never inspected. Metadata cached before
// AIM_BASE_IMAGE_REF extraction is inspected once more; a cache that already
// has the reference needs no registry request, and DecorateStatus marks it
// current.
func shouldExtractMetadata(status *aimv1alpha1.AIMModelStatus) bool {
	if status == nil || status.ImageMetadata == nil || status.ImageMetadata.Model == nil {
		return true
	}
	if status.ImageMetadataVersion >= CurrentVersion {
		return false
	}
	return status.ImageMetadata.BaseImageRef == ""
}

// ResultForHealth keeps a failed re-inspection of metadata cached by an older
// release from gating a model that already has usable metadata. Errors from a
// first inspection pass through unchanged.
func ResultForHealth(
	result controllerutils.FetchResult[*aimv1alpha1.ImageMetadata],
	status *aimv1alpha1.AIMModelStatus,
) controllerutils.FetchResult[*aimv1alpha1.ImageMetadata] {
	if result.HasError() && status != nil && status.ImageMetadata != nil &&
		status.ImageMetadataVersion < CurrentVersion {
		return controllerutils.FetchResult[*aimv1alpha1.ImageMetadata]{Value: status.ImageMetadata}
	}
	return result
}

// GetHealth returns the component health for a fetched image metadata result.
// If the image contains model metadata (canonicalName), reports "ImageMetadataFound".
// Otherwise the image was accessible but had no model metadata — reports "ImageFound".
func GetHealth(metadata *aimv1alpha1.ImageMetadata) controllerutils.ComponentHealth {
	if metadata != nil && metadata.Model != nil && metadata.Model.CanonicalName != "" {
		return controllerutils.ComponentHealth{
			State:  constants.AIMStatusReady,
			Reason: "ImageMetadataFound",
		}
	}
	return controllerutils.ComponentHealth{
		State:  constants.AIMStatusReady,
		Reason: "ImageFound",
	}
}
