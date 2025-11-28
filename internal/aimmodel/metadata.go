package aimmodel

import (
	"context"
	"errors"
	"fmt"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type ModelMetadataFetchResult struct {
	ImageMetadata *aimv1alpha1.ImageMetadata
	Error         error
}

func FetchModelMetadataResult(ctx context.Context, clientset kubernetes.Interface, modelSpec aimv1alpha1.AIMModelSpec, secretNamespace string) ModelMetadataFetchResult {
	result := ModelMetadataFetchResult{}

	metadata, metadataErr := InspectImage(
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

type ModelMetadataObservation struct {
	ExtractedMetadata *aimv1alpha1.ImageMetadata
	Extracted         bool
	Error             error

	FormatError   *MetadataFormatError
	RegistryError *utils.ImageRegistryError
}

// ObserveModelMetadata builds a metadata observation from fetched data.
// No client access - all fetching should happen in the Fetch phase.
func ObserveModelMetadata(
	status *aimv1alpha1.AIMModelStatus,
	result *ModelMetadataFetchResult,
) ModelMetadataObservation {
	obs := ModelMetadataObservation{}
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

		var fmtErr *MetadataFormatError
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
func projectModelMetadata(
	cm *controllerutils.ConditionManager,
	h *controllerutils.StatusHelper,
	observation ModelMetadataObservation,
) bool {
	if observation.Error != nil {

		if err := observation.FormatError; err != nil {
			if err.Reason == "MetadataMissingRecommendedDeployments" {
				// Non-fatal, might be a base image with missing labels
				cm.Set(aimv1alpha1.AIMModelConditionMetadataExtracted, metav1.ConditionFalse, err.Reason, err.Error(), controllerutils.LevelNone)
			} else {
				// Fatal, labels found but wrong format
				h.Failed(err.Reason, err.Error())
				cm.Set(aimv1alpha1.AIMModelConditionMetadataExtracted, metav1.ConditionFalse, err.Reason, err.Error(), controllerutils.LevelWarning)
				return true
			}
		}

		if err := observation.RegistryError; err != nil {
			setMetadataExtractionConditionFromRegistry(cm, err)
			if err.Type == utils.ImagePullErrorNotFound {
				// Fatal, image not found
				h.Failed(reasonForRegistry(err), err.Error())
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
				controllerutils.LevelWarning,
			)
			// Keep going
		}
	}

	return false
}

func reasonForRegistry(err *utils.ImageRegistryError) string {
	switch err.Type {
	case utils.ImagePullErrorAuth:
		return aimv1alpha1.AIMModelReasonImagePullAuthFailure
	case utils.ImagePullErrorNotFound:
		return aimv1alpha1.AIMModelReasonImageNotFound
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
		controllerutils.LevelWarning,
	)
}

// ==============
// UTILS
// ==============

// ShouldExtractMetadata checks if metadata extraction should be attempted.
// Returns false if metadata already exists in status (cached).
func ShouldExtractMetadata(status *aimv1alpha1.AIMModelStatus) bool {
	return status == nil || status.ImageMetadata == nil
}
