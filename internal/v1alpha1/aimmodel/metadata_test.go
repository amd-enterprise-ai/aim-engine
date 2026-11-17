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
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
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
			name: "legacy status with existing metadata is migrated",
			status: &aimv1alpha1.AIMModelStatus{
				ImageMetadata: &aimv1alpha1.ImageMetadata{
					Model: &aimv1alpha1.ModelMetadata{
						CanonicalName: "test-model",
					},
				},
			},
			expected: true,
		},
		{
			name: "legacy status with base image reference needs no registry migration",
			status: &aimv1alpha1.AIMModelStatus{
				ImageMetadata: &aimv1alpha1.ImageMetadata{
					BaseImageRef: "docker.io/amdenterpriseai/aim-base:0.11.2",
				},
			},
			expected: false,
		},
		{
			name: "current metadata without base image reference is not repeatedly inspected",
			status: &aimv1alpha1.AIMModelStatus{
				ImageMetadata:        &aimv1alpha1.ImageMetadata{},
				ImageMetadataVersion: currentImageMetadataVersion,
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

func TestDecorateModelStatusReportsMissingBaseImageWithoutGatingReadiness(t *testing.T) {
	status := &aimv1alpha1.AIMModelStatus{Status: constants.AIMStatusReady}
	cm := controllerutils.NewConditionManager(nil)

	decorateModelStatus(
		status,
		cm,
		&aimv1alpha1.AIMModelSpec{Image: "registry.example/model:1.0"},
		controllerutils.FetchResult[*aimv1alpha1.ImageMetadata]{
			Value: &aimv1alpha1.ImageMetadata{},
		},
	)

	condition := cm.Get(conditionTypeRuntimeImageLineage)
	if condition == nil {
		t.Fatal("missing runtime image lineage condition")
	}
	if condition.Status != metav1.ConditionFalse || condition.Reason != reasonBaseImageRefMissing {
		t.Fatalf("runtime image lineage condition = %#v", condition)
	}
	if strings.HasSuffix(condition.Type, controllerutils.ComponentConditionSuffix) {
		t.Fatalf("condition %q participates in readiness rollup", condition.Type)
	}
	if status.Status != constants.AIMStatusReady {
		t.Fatalf("status = %q, want normal model usage to remain Ready", status.Status)
	}
	if status.ImageMetadataVersion != currentImageMetadataVersion {
		t.Fatalf("image metadata version = %d, want %d", status.ImageMetadataVersion, currentImageMetadataVersion)
	}
}

func TestDecorateModelStatusClearsMissingBaseImageConditionAfterRecovery(t *testing.T) {
	status := &aimv1alpha1.AIMModelStatus{
		Conditions: []metav1.Condition{{
			Type:   conditionTypeRuntimeImageLineage,
			Status: metav1.ConditionFalse,
			Reason: reasonBaseImageRefMissing,
		}},
	}
	cm := controllerutils.NewConditionManager(status.Conditions)

	decorateModelStatus(
		status,
		cm,
		&aimv1alpha1.AIMModelSpec{Image: "registry.example/model:1.0"},
		controllerutils.FetchResult[*aimv1alpha1.ImageMetadata]{
			Value: &aimv1alpha1.ImageMetadata{
				BaseImageRef: "docker.io/amdenterpriseai/aim-base:0.11.2",
			},
		},
	)

	if condition := cm.Get(conditionTypeRuntimeImageLineage); condition != nil {
		t.Fatalf("stale runtime image lineage condition was not cleared: %#v", condition)
	}
}

func TestDecorateModelStatusRecordsFailedLegacyMigrationWithoutReplacingCachedMetadata(t *testing.T) {
	cached := &aimv1alpha1.ImageMetadata{
		Model: &aimv1alpha1.ModelMetadata{CanonicalName: "cached-model"},
	}
	status := &aimv1alpha1.AIMModelStatus{
		Status:        constants.AIMStatusReady,
		ImageMetadata: cached,
	}
	cm := controllerutils.NewConditionManager(nil)
	migrationErr := &utils.ImageRegistryError{
		Type:    utils.ImagePullErrorAuth,
		Message: "401 unauthorized",
	}

	decorateModelStatus(
		status,
		cm,
		&aimv1alpha1.AIMModelSpec{Image: "registry.example/model:1.0"},
		controllerutils.FetchResult[*aimv1alpha1.ImageMetadata]{Error: migrationErr},
	)

	if status.ImageMetadata != cached {
		t.Fatal("failed migration replaced usable cached metadata")
	}
	if status.ImageMetadataVersion != 0 {
		t.Fatalf("failed migration advanced image metadata version to %d", status.ImageMetadataVersion)
	}
	if status.Status != constants.AIMStatusReady {
		t.Fatalf("status = %q, want normal model usage to remain Ready", status.Status)
	}
	condition := cm.Get(conditionTypeRuntimeImageLineage)
	if condition == nil {
		t.Fatal("missing runtime image lineage diagnostic")
	}
	if condition.Status != metav1.ConditionFalse || condition.Reason != reasonBaseImageRefInspectionFailed {
		t.Fatalf("runtime image lineage condition = %#v", condition)
	}
	if !strings.Contains(condition.Message, "401 unauthorized") {
		t.Fatalf("condition message %q does not explain the migration failure", condition.Message)
	}
	if !shouldExtractMetadata(status) {
		t.Fatal("failed migration should remain eligible for recovery after registry access is fixed")
	}
}
