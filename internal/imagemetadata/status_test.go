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
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

func TestDecorateStatusReportsMissingBaseImageWithoutGatingReadiness(t *testing.T) {
	status := &aimv1alpha1.AIMModelStatus{Status: constants.AIMStatusReady}
	cm := controllerutils.NewConditionManager(nil)

	DecorateStatus(
		status,
		cm,
		&aimv1alpha1.AIMModelSpec{Image: "registry.example/model:1.0"},
		controllerutils.FetchResult[*aimv1alpha1.ImageMetadata]{
			Value: &aimv1alpha1.ImageMetadata{},
		},
	)

	condition := cm.Get(ConditionTypeRuntimeImageLineage)
	if condition == nil {
		t.Fatal("missing runtime image lineage condition")
	}
	if condition.Status != metav1.ConditionFalse || condition.Reason != ReasonBaseImageRefMissing {
		t.Fatalf("runtime image lineage condition = %#v", condition)
	}
	if strings.HasSuffix(condition.Type, controllerutils.ComponentConditionSuffix) {
		t.Fatalf("condition %q participates in readiness rollup", condition.Type)
	}
	if status.Status != constants.AIMStatusReady {
		t.Fatalf("status = %q, want normal model usage to remain Ready", status.Status)
	}
	if status.ImageMetadataVersion != CurrentVersion {
		t.Fatalf("image metadata version = %d, want %d", status.ImageMetadataVersion, CurrentVersion)
	}
}

func TestDecorateStatusMissingBaseImageMessageNamesWhyImageIsUninspected(t *testing.T) {
	const image = "registry.example/model:1.0"
	tests := []struct {
		name        string
		discovery   *aimv1alpha1.AIMModelDiscoveryConfig
		result      controllerutils.FetchResult[*aimv1alpha1.ImageMetadata]
		wantMessage string
	}{
		{
			name:        "inspected image without the reference",
			result:      controllerutils.FetchResult[*aimv1alpha1.ImageMetadata]{Value: &aimv1alpha1.ImageMetadata{}},
			wantMessage: `Image "registry.example/model:1.0" does not declare AIM_BASE_IMAGE_REF; the model serves normally, but weight-replacing profile copies need spec.imageMetadata with baseImageRef`,
		},
		{
			name:        "extractMetadata false",
			discovery:   &aimv1alpha1.AIMModelDiscoveryConfig{ExtractMetadata: ptr.To(false)},
			wantMessage: `Image "registry.example/model:1.0" is not inspected for AIM_BASE_IMAGE_REF because spec.discovery.extractMetadata is false; weight-replacing profile copies need spec.imageMetadata with baseImageRef`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := controllerutils.NewConditionManager(nil)
			DecorateStatus(&aimv1alpha1.AIMModelStatus{}, cm, &aimv1alpha1.AIMModelSpec{Image: image, Discovery: tt.discovery}, tt.result)

			condition := cm.Get(ConditionTypeRuntimeImageLineage)
			if condition == nil || condition.Reason != ReasonBaseImageRefMissing || condition.Message != tt.wantMessage {
				t.Fatalf("runtime image lineage condition = %#v, want %s with message %q",
					condition, ReasonBaseImageRefMissing, tt.wantMessage)
			}
		})
	}
}

func TestDecorateStatusClearsMissingBaseImageConditionAfterRecovery(t *testing.T) {
	status := &aimv1alpha1.AIMModelStatus{
		Conditions: []metav1.Condition{{
			Type:   ConditionTypeRuntimeImageLineage,
			Status: metav1.ConditionFalse,
			Reason: ReasonBaseImageRefMissing,
		}},
	}
	cm := controllerutils.NewConditionManager(status.Conditions)

	DecorateStatus(
		status,
		cm,
		&aimv1alpha1.AIMModelSpec{Image: "registry.example/model:1.0"},
		controllerutils.FetchResult[*aimv1alpha1.ImageMetadata]{
			Value: &aimv1alpha1.ImageMetadata{
				BaseImageRef: "docker.io/amdenterpriseai/aim-base:0.11.2",
			},
		},
	)

	if condition := cm.Get(ConditionTypeRuntimeImageLineage); condition != nil {
		t.Fatalf("stale runtime image lineage condition was not cleared: %#v", condition)
	}
}

func TestDecorateStatusRecordsFailedLegacyMigrationWithoutReplacingCachedMetadata(t *testing.T) {
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

	DecorateStatus(
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
	condition := cm.Get(ConditionTypeRuntimeImageLineage)
	if condition == nil {
		t.Fatal("missing runtime image lineage diagnostic")
	}
	if condition.Status != metav1.ConditionFalse || condition.Reason != ReasonBaseImageRefInspectionFailed {
		t.Fatalf("runtime image lineage condition = %#v", condition)
	}
	if !strings.Contains(condition.Message, "401 unauthorized") {
		t.Fatalf("condition message %q does not explain the migration failure", condition.Message)
	}
	if !shouldExtractMetadata(status) {
		t.Fatal("failed migration should remain eligible for recovery after registry access is fixed")
	}
}
