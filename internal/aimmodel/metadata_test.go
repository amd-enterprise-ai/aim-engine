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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

// ============================================================================
// HELPER FUNCTIONS TESTS
// ============================================================================

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

func TestShouldRequeueForMetadataRetry(t *testing.T) {
	tests := []struct {
		name     string
		status   *aimv1alpha1.AIMModelStatus
		expected bool
	}{
		{
			name: "metadata already extracted",
			status: &aimv1alpha1.AIMModelStatus{
				ImageMetadata: &aimv1alpha1.ImageMetadata{
					Model: &aimv1alpha1.ModelMetadata{
						CanonicalName: "test-model",
					},
				},
			},
			expected: false,
		},
		{
			name: "auth failure - should retry",
			status: &aimv1alpha1.AIMModelStatus{
				Conditions: []metav1.Condition{
					{
						Type:   aimv1alpha1.AIMModelConditionMetadataExtracted,
						Status: metav1.ConditionFalse,
						Reason: constants.ReasonImagePullAuthFailure,
					},
				},
			},
			expected: true,
		},
		{
			name: "image not found - should not retry",
			status: &aimv1alpha1.AIMModelStatus{
				Conditions: []metav1.Condition{
					{
						Type:   aimv1alpha1.AIMModelConditionMetadataExtracted,
						Status: metav1.ConditionFalse,
						Reason: constants.ReasonImageNotFound,
					},
				},
			},
			expected: false,
		},
		{
			name: "generic extraction failure - should retry",
			status: &aimv1alpha1.AIMModelStatus{
				Conditions: []metav1.Condition{
					{
						Type:   aimv1alpha1.AIMModelConditionMetadataExtracted,
						Status: metav1.ConditionFalse,
						Reason: aimv1alpha1.AIMModelReasonMetadataExtractionFailed,
					},
				},
			},
			expected: true,
		},
		{
			name: "format error - should not retry",
			status: &aimv1alpha1.AIMModelStatus{
				Conditions: []metav1.Condition{
					{
						Type:   aimv1alpha1.AIMModelConditionMetadataExtracted,
						Status: metav1.ConditionFalse,
						Reason: "MetadataFormatInvalid",
					},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ShouldRequeueForMetadataRetry(tt.status)
			if result != tt.expected {
				t.Errorf("ShouldRequeueForMetadataRetry() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestReasonForRegistry(t *testing.T) {
	tests := []struct {
		name     string
		errType  utils.ImagePullErrorType
		expected string
	}{
		{
			name:     "auth error",
			errType:  utils.ImagePullErrorAuth,
			expected: constants.ReasonImagePullAuthFailure,
		},
		{
			name:     "not found error",
			errType:  utils.ImagePullErrorNotFound,
			expected: constants.ReasonImageNotFound,
		},
		{
			name:     "generic error",
			errType:  utils.ImagePullErrorGeneric,
			expected: aimv1alpha1.AIMModelReasonMetadataExtractionFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &utils.ImageRegistryError{
				Type: tt.errType,
			}
			result := reasonForRegistry(err)
			if result != tt.expected {
				t.Errorf("reasonForRegistry() = %v, want %v", result, tt.expected)
			}
		})
	}
}

// ============================================================================
// OBSERVE TESTS
// ============================================================================

func TestObserveModelMetadata_NilResult(t *testing.T) {
	status := &aimv1alpha1.AIMModelStatus{}
	obs := observeModelMetadata(status, nil)

	if obs.ExtractedMetadata != nil {
		t.Error("expected ExtractedMetadata=nil when result is nil")
	}
	if obs.Extracted {
		t.Error("expected Extracted=false when result is nil")
	}
	if obs.Error != nil {
		t.Error("expected Error=nil when result is nil")
	}
}

func TestObserveModelMetadata_StatusHasCachedMetadata(t *testing.T) {
	cachedMetadata := &aimv1alpha1.ImageMetadata{
		Model: &aimv1alpha1.ModelMetadata{
			CanonicalName: "cached-model",
		},
	}
	status := &aimv1alpha1.AIMModelStatus{
		ImageMetadata: cachedMetadata,
	}
	result := &modelMetadataFetchResult{
		ImageMetadata: &aimv1alpha1.ImageMetadata{
			Model: &aimv1alpha1.ModelMetadata{
				CanonicalName: "new-model",
			},
		},
	}

	obs := observeModelMetadata(status, result)

	if obs.ExtractedMetadata != cachedMetadata {
		t.Error("expected cached metadata to be used")
	}
	if obs.Extracted {
		t.Error("expected Extracted=false when using cached metadata")
	}
}

func TestObserveModelMetadata_ExtractionSuccess(t *testing.T) {
	extractedMetadata := &aimv1alpha1.ImageMetadata{
		Model: &aimv1alpha1.ModelMetadata{
			CanonicalName: "new-model",
		},
	}
	status := &aimv1alpha1.AIMModelStatus{}
	result := &modelMetadataFetchResult{
		ImageMetadata: extractedMetadata,
	}

	obs := observeModelMetadata(status, result)

	if obs.ExtractedMetadata != extractedMetadata {
		t.Error("expected extracted metadata to be set")
	}
	if !obs.Extracted {
		t.Error("expected Extracted=true when extraction succeeds")
	}
	if obs.Error != nil {
		t.Error("expected Error=nil when extraction succeeds")
	}
}

func TestObserveModelMetadata_FormatError(t *testing.T) {
	formatErr := &metadataFormatError{
		Reason:  "MetadataFormatInvalid",
		Message: "invalid format",
	}
	status := &aimv1alpha1.AIMModelStatus{}
	result := &modelMetadataFetchResult{
		Error: formatErr,
	}

	obs := observeModelMetadata(status, result)

	if obs.Error == nil {
		t.Error("expected Error to be set")
	}
	if obs.FormatError == nil {
		t.Error("expected FormatError to be set")
	}
	if obs.FormatError.Reason != "MetadataFormatInvalid" {
		t.Errorf("expected FormatError.Reason=MetadataFormatInvalid, got %s", obs.FormatError.Reason)
	}
}

func TestObserveModelMetadata_RegistryError(t *testing.T) {
	regErr := &utils.ImageRegistryError{
		Type:    utils.ImagePullErrorAuth,
		Message: "auth failed",
	}
	status := &aimv1alpha1.AIMModelStatus{}
	result := &modelMetadataFetchResult{
		Error: regErr,
	}

	obs := observeModelMetadata(status, result)

	if obs.Error == nil {
		t.Error("expected Error to be set")
	}
	if obs.RegistryError == nil {
		t.Error("expected RegistryError to be set")
	}
	if obs.RegistryError.Type != utils.ImagePullErrorAuth {
		t.Errorf("expected RegistryError.Type=ImagePullErrorAuth, got %v", obs.RegistryError.Type)
	}
}

func TestObserveModelMetadata_GenericError(t *testing.T) {
	genericErr := errors.New("some error")
	status := &aimv1alpha1.AIMModelStatus{}
	result := &modelMetadataFetchResult{
		Error: genericErr,
	}

	obs := observeModelMetadata(status, result)

	if obs.Error == nil {
		t.Error("expected Error to be set")
	}
	if obs.FormatError != nil {
		t.Error("expected FormatError=nil for generic error")
	}
	if obs.RegistryError != nil {
		t.Error("expected RegistryError=nil for generic error")
	}
}

// ============================================================================
// PROJECT TESTS
// ============================================================================

func TestProjectModelMetadata_NoError(t *testing.T) {
	obs := modelMetadataObservation{
		ExtractedMetadata: &aimv1alpha1.ImageMetadata{
			Model: &aimv1alpha1.ModelMetadata{
				CanonicalName: "test-model",
			},
		},
	}

	cm := controllerutils.NewConditionManager(nil)
	status := &aimv1alpha1.AIMModelStatus{}
	sh := controllerutils.NewStatusHelper(status, cm)

	fatal := projectModelMetadata(status, cm, sh, obs)

	if fatal {
		t.Error("expected fatal=false when no error")
	}

	// Check condition
	conditions := cm.Conditions()
	found := false
	for _, cond := range conditions {
		if cond.Type == aimv1alpha1.AIMModelConditionMetadataExtracted {
			found = true
			if cond.Status != metav1.ConditionTrue {
				t.Errorf("expected condition status=True, got %v", cond.Status)
			}
			if cond.Reason != "MetadataExtracted" {
				t.Errorf("expected reason=MetadataExtracted, got %s", cond.Reason)
			}
		}
	}
	if !found {
		t.Error("expected MetadataExtracted condition to be set")
	}
}

func TestProjectModelMetadata_FormatError_NonFatal(t *testing.T) {
	obs := modelMetadataObservation{
		Error: &metadataFormatError{
			Reason:  "MetadataMissingRecommendedDeployments",
			Message: "missing deployments",
		},
		FormatError: &metadataFormatError{
			Reason:  "MetadataMissingRecommendedDeployments",
			Message: "missing deployments",
		},
	}

	cm := controllerutils.NewConditionManager(nil)
	status := &aimv1alpha1.AIMModelStatus{}
	sh := controllerutils.NewStatusHelper(status, cm)

	fatal := projectModelMetadata(status, cm, sh, obs)

	if fatal {
		t.Error("expected fatal=false for non-fatal format error")
	}

	// Check condition
	conditions := cm.Conditions()
	found := false
	for _, cond := range conditions {
		if cond.Type == aimv1alpha1.AIMModelConditionMetadataExtracted {
			found = true
			if cond.Status != metav1.ConditionFalse {
				t.Errorf("expected condition status=False, got %v", cond.Status)
			}
		}
	}
	if !found {
		t.Error("expected MetadataExtracted condition to be set")
	}
}

func TestProjectModelMetadata_FormatError_Fatal(t *testing.T) {
	obs := modelMetadataObservation{
		Error: &metadataFormatError{
			Reason:  "MetadataFormatInvalid",
			Message: "invalid format",
		},
		FormatError: &metadataFormatError{
			Reason:  "MetadataFormatInvalid",
			Message: "invalid format",
		},
	}

	cm := controllerutils.NewConditionManager(nil)
	status := &aimv1alpha1.AIMModelStatus{}
	sh := controllerutils.NewStatusHelper(status, cm)

	fatal := projectModelMetadata(status, cm, sh, obs)

	if !fatal {
		t.Error("expected fatal=true for fatal format error")
	}

	if status.Status != constants.AIMStatusFailed {
		t.Errorf("expected status=Failed, got %s", status.Status)
	}
}

func TestProjectModelMetadata_RegistryError_NotFound_Fatal(t *testing.T) {
	obs := modelMetadataObservation{
		Error: &utils.ImageRegistryError{
			Type:    utils.ImagePullErrorNotFound,
			Message: "image not found",
		},
		RegistryError: &utils.ImageRegistryError{
			Type:    utils.ImagePullErrorNotFound,
			Message: "image not found",
		},
	}

	cm := controllerutils.NewConditionManager(nil)
	status := &aimv1alpha1.AIMModelStatus{}
	sh := controllerutils.NewStatusHelper(status, cm)

	fatal := projectModelMetadata(status, cm, sh, obs)

	if !fatal {
		t.Error("expected fatal=true for ImageNotFound error")
	}

	if status.Status != constants.AIMStatusFailed {
		t.Errorf("expected status=Failed, got %s", status.Status)
	}
}

func TestProjectModelMetadata_RegistryError_Auth_Recoverable(t *testing.T) {
	obs := modelMetadataObservation{
		Error: &utils.ImageRegistryError{
			Type:    utils.ImagePullErrorAuth,
			Message: "auth failed",
		},
		RegistryError: &utils.ImageRegistryError{
			Type:    utils.ImagePullErrorAuth,
			Message: "auth failed",
		},
	}

	cm := controllerutils.NewConditionManager(nil)
	status := &aimv1alpha1.AIMModelStatus{}
	sh := controllerutils.NewStatusHelper(status, cm)

	fatal := projectModelMetadata(status, cm, sh, obs)

	if fatal {
		t.Error("expected fatal=false for recoverable auth error")
	}

	if status.Status != constants.AIMStatusDegraded {
		t.Errorf("expected status=Degraded, got %s", status.Status)
	}
}

func TestProjectModelMetadata_GenericError(t *testing.T) {
	obs := modelMetadataObservation{
		Error: errors.New("some error"),
	}

	cm := controllerutils.NewConditionManager(nil)
	status := &aimv1alpha1.AIMModelStatus{}
	sh := controllerutils.NewStatusHelper(status, cm)

	fatal := projectModelMetadata(status, cm, sh, obs)

	if fatal {
		t.Error("expected fatal=false for generic error")
	}

	// Check condition with warning
	conditions := cm.Conditions()
	found := false
	for _, cond := range conditions {
		if cond.Type == aimv1alpha1.AIMModelConditionMetadataExtracted {
			found = true
			if cond.Status != metav1.ConditionFalse {
				t.Errorf("expected condition status=False, got %v", cond.Status)
			}
		}
	}
	if !found {
		t.Error("expected MetadataExtracted condition to be set")
	}
}

func TestSetMetadataExtractionConditionFromRegistry(t *testing.T) {
	tests := []struct {
		name         string
		errType      utils.ImagePullErrorType
		expectReason string
	}{
		{
			name:         "auth error",
			errType:      utils.ImagePullErrorAuth,
			expectReason: constants.ReasonImagePullAuthFailure,
		},
		{
			name:         "not found error",
			errType:      utils.ImagePullErrorNotFound,
			expectReason: constants.ReasonImageNotFound,
		},
		{
			name:         "generic error",
			errType:      utils.ImagePullErrorGeneric,
			expectReason: aimv1alpha1.AIMModelReasonMetadataExtractionFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := controllerutils.NewConditionManager(nil)
			regErr := &utils.ImageRegistryError{
				Type:    tt.errType,
				Message: "test error",
			}

			setMetadataExtractionConditionFromRegistry(cm, regErr)

			conditions := cm.Conditions()
			found := false
			for _, cond := range conditions {
				if cond.Type == aimv1alpha1.AIMModelConditionMetadataExtracted {
					found = true
					if cond.Status != metav1.ConditionFalse {
						t.Errorf("expected status=False, got %v", cond.Status)
					}
					if cond.Reason != tt.expectReason {
						t.Errorf("expected reason=%s, got %s", tt.expectReason, cond.Reason)
					}
				}
			}
			if !found {
				t.Error("expected MetadataExtracted condition to be set")
			}
		})
	}
}
