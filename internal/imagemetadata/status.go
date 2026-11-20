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
	"fmt"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

const (
	ConditionTypeRuntimeImageLineage   = "RuntimeImageLineage"
	ReasonBaseImageRefMissing          = "BaseImageRefMissing"
	ReasonBaseImageRefInspectionFailed = "BaseImageRefInspectionFailed"
	ReasonInvalidImageReference        = "InvalidImageReference"
	ReasonImageInspectionFailed        = "ImageInspectionFailed"
)

// DecorateStatus records fetched image metadata on an image model's status
// and reports whether the image declares AIM_BASE_IMAGE_REF.
func DecorateStatus(
	status *aimv1alpha1.AIMModelStatus,
	cm *controllerutils.ConditionManager,
	spec *aimv1alpha1.AIMModelSpec,
	result controllerutils.FetchResult[*aimv1alpha1.ImageMetadata],
) {
	if result.OK() && result.Value != nil {
		status.ImageMetadata = result.Value
		status.ImageMetadataVersion = CurrentVersion
	} else if status.ImageMetadata != nil && status.ImageMetadata.BaseImageRef != "" {
		// Metadata cached by an older release already has the reference the
		// current contract records, so it is current without a registry request.
		status.ImageMetadataVersion = CurrentVersion
	}
	decorateRuntimeImageLineage(status, cm, spec, result)
}

// decorateRuntimeImageLineage exposes a missing AIM_BASE_IMAGE_REF without
// making the model unready. The condition name deliberately does not end in
// "Ready", so the status rollup treats it as informational: only
// weight-replacing copies of the model's profiles need the reference.
func decorateRuntimeImageLineage(
	status *aimv1alpha1.AIMModelStatus,
	cm *controllerutils.ConditionManager,
	spec *aimv1alpha1.AIMModelSpec,
	result controllerutils.FetchResult[*aimv1alpha1.ImageMetadata],
) {
	if cm == nil || spec == nil || spec.IsFineTunedModel() || spec.Image == "" {
		return
	}
	metadata := spec.GetEffectiveImageMetadata(status)
	if metadata != nil && metadata.BaseImageRef != "" {
		cm.Delete(ConditionTypeRuntimeImageLineage)
		return
	}
	if result.HasError() && status.ImageMetadata != nil {
		cm.MarkFalse(
			ConditionTypeRuntimeImageLineage,
			ReasonBaseImageRefInspectionFailed,
			fmt.Sprintf(
				"Could not re-inspect image %q for AIM_BASE_IMAGE_REF: %v; the model keeps serving from cached metadata, but weight-replacing profile copies need registry access or spec.imageMetadata with baseImageRef",
				spec.Image,
				result.Error,
			),
			controllerutils.AsInfo(),
		)
		return
	}
	extractionComplete := spec.ImageMetadata != nil ||
		status.ImageMetadataVersion >= CurrentVersion ||
		!spec.Discovery.IsExtractMetadataEnabled()
	if !extractionComplete {
		// Old cached metadata is still being re-inspected, or the first
		// inspection failed and the ImageMetadata condition already says why.
		cm.Delete(ConditionTypeRuntimeImageLineage)
		return
	}
	message := fmt.Sprintf(
		"Image %q does not declare AIM_BASE_IMAGE_REF; the model serves normally, but weight-replacing profile copies need spec.imageMetadata with baseImageRef",
		spec.Image,
	)
	if !spec.Discovery.IsExtractMetadataEnabled() {
		message = fmt.Sprintf(
			"Image %q is not inspected for AIM_BASE_IMAGE_REF because spec.discovery.extractMetadata is false; weight-replacing profile copies need spec.imageMetadata with baseImageRef",
			spec.Image,
		)
	}
	cm.MarkFalse(ConditionTypeRuntimeImageLineage, ReasonBaseImageRefMissing, message, controllerutils.AsInfo())
}
