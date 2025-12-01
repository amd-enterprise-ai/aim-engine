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
	"context"
	"errors"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/amd-enterprise-ai/aim-engine/internal/constants"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

type modelMetadataFetchResult struct {
	ImageMetadata *aimv1alpha1.ImageMetadata
	Error         error
}

func fetchModelMetadataResult(ctx context.Context, clientset kubernetes.Interface, modelSpec aimv1alpha1.AIMModelSpec, secretNamespace string) modelMetadataFetchResult {
	result := modelMetadataFetchResult{}

	metadata, metadataErr := inspectImage(
		ctx,
		modelSpec.Image,
		modelSpec.ImagePullSecrets,
		clientset,
		secretNamespace,
	)
	if metadataErr != nil {
		result.Error = metadataErr
	} else {
		result.ImageMetadata = metadata
	}

	return result
}

type modelMetadataObservation struct {
	ExtractedMetadata *aimv1alpha1.ImageMetadata
	Extracted         bool
	Error             error

	FormatError   *metadataFormatError
	RegistryError *utils.ImageRegistryError
}

// observeModelMetadata builds a metadata observation from fetched data.
// No client access - all fetching should happen in the Fetch phase.
func observeModelMetadata(
	status *aimv1alpha1.AIMModelStatus,
	result *modelMetadataFetchResult,
) modelMetadataObservation {
	obs := modelMetadataObservation{}
	if result == nil {
		// Model metadata was not extracted or attempted
		return obs
	}

	// If metadata already exists in status (not fetched), use cached version
	if status != nil && status.ImageMetadata != nil {
		obs.ExtractedMetadata = status.ImageMetadata
		obs.Extracted = false
		return obs
	}

	// If extraction succeeded
	if result.Error == nil && result.ImageMetadata != nil {
		obs.ExtractedMetadata = result.ImageMetadata
		obs.Extracted = true
		return obs
	}

	// Handle extraction errors
	if result.Error != nil {
		obs.Error = result.Error

		var fmtErr *metadataFormatError
		var regErr *utils.ImageRegistryError

		switch {
		case errors.As(result.Error, &fmtErr):
			obs.FormatError = fmtErr
		case errors.As(result.Error, &regErr):
			obs.RegistryError = regErr
		}
	}

	return obs
}

// projectModelMetadata projects metadata observation into status conditions and overall status.
// Returns true if a fatal error occurred and reconciliation should stop, false otherwise.
//
//nolint:unparam // bool return kept for API consistency with other project functions
func projectModelMetadata(
	cm *controllerutils.ConditionManager,
	h *controllerutils.StatusHelper,
	observation modelMetadataObservation,
) bool {
	if observation.Error != nil {

		if err := observation.FormatError; err != nil {
			if err.Reason == "MetadataMissingRecommendedDeployments" {
				// Non-fatal, might be a base image with missing labels
				cm.Set(aimv1alpha1.AIMModelConditionMetadataExtracted, metav1.ConditionFalse, err.Reason, err.Error())
			} else {
				// Fatal, labels found but wrong format
				h.Failed(err.Reason, err.Error())
				cm.Set(aimv1alpha1.AIMModelConditionMetadataExtracted, metav1.ConditionFalse, err.Reason, err.Error(), controllerutils.AsWarning())
				return true
			}
		}

		if err := observation.RegistryError; err != nil {
			setMetadataExtractionConditionFromRegistry(cm, err)
			if err.Type == utils.ImagePullErrorNotFound {
				// Fatal, image not found
				h.Failed(reasonForRegistry(err), err.Error())
				return true
			} else {
				// Recoverable, image found but auth / other error
				h.Degraded(reasonForRegistry(err), err.Error())
			}
		}

		// Other non-categorized error
		if err := observation.Error; err != nil && observation.FormatError == nil && observation.RegistryError == nil {
			cm.Set(
				aimv1alpha1.AIMModelConditionMetadataExtracted,
				metav1.ConditionFalse,
				aimv1alpha1.AIMModelReasonMetadataExtractionFailed,
				fmt.Sprintf("Failed to extract metadata: %v", observation.Error),
				controllerutils.AsWarning(),
			)
			// Keep going
		}
	} else if observation.ExtractedMetadata != nil {
		// Success - metadata was extracted
		cm.Set(
			aimv1alpha1.AIMModelConditionMetadataExtracted,
			metav1.ConditionTrue,
			"MetadataExtracted",
			"Successfully extracted image metadata",
		)
	}
	// Note: If Error is nil and ExtractedMetadata is nil, metadata extraction was not attempted
	// (e.g., already cached in status), so we don't update the condition

	return false
}

func reasonForRegistry(err *utils.ImageRegistryError) string {
	switch err.Type {
	case utils.ImagePullErrorAuth:
		return constants.ReasonImagePullAuthFailure
	case utils.ImagePullErrorNotFound:
		return constants.ReasonImageNotFound
	default:
		return aimv1alpha1.AIMModelReasonMetadataExtractionFailed
	}
}

func setMetadataExtractionConditionFromRegistry(
	cm *controllerutils.ConditionManager,
	regErr *utils.ImageRegistryError,
) {
	reason := reasonForRegistry(regErr)
	cm.Set(
		aimv1alpha1.AIMModelConditionMetadataExtracted,
		metav1.ConditionFalse,
		reason,
		regErr.Error(),
		controllerutils.AsWarning(),
	)
}

// ShouldRequeueForMetadataRetry checks if metadata extraction failed with a recoverable error
// and should be retried after a delay.
func ShouldRequeueForMetadataRetry(status *aimv1alpha1.AIMModelStatus) bool {
	// If metadata already extracted, no need to retry
	if status.ImageMetadata != nil {
		return false
	}

	// Check the MetadataExtracted condition
	for _, cond := range status.Conditions {
		if cond.Type == aimv1alpha1.AIMModelConditionMetadataExtracted && cond.Status == metav1.ConditionFalse {
			// Retry for recoverable errors (auth failures, transient network issues)
			// Don't retry for fatal errors (image not found, invalid format)
			switch cond.Reason {
			case constants.ReasonImageNotFound:
				// Fatal - image doesn't exist
				return false
			case constants.ReasonImagePullAuthFailure,
				aimv1alpha1.AIMModelReasonMetadataExtractionFailed:
				// Recoverable - could be transient network/auth issues
				return true
			default:
				// For format errors or unknown reasons, don't retry
				return false
			}
		}
	}

	return false
}

// ==============
// UTILS
// ==============

// shouldExtractMetadata checks if metadata extraction should be attempted.
// Returns false if metadata already exists in status (cached).
func shouldExtractMetadata(status *aimv1alpha1.AIMModelStatus) bool {
	return status == nil || status.ImageMetadata == nil
}
