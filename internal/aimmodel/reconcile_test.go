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
	"context"
	"errors"
	"testing"

	"k8s.io/utils/ptr"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

// ============================================================================
// AGGREGATE TEMPLATE STATUSES TESTS
// ============================================================================

func TestAggregateTemplateStatuses_NoTemplates(t *testing.T) {
	tests := []struct {
		name           string
		expectsPtr     *bool
		expectedStatus constants.AIMStatus
		expectedReason string
	}{
		{
			name:           "nil expectsTemplates - awaiting metadata",
			expectsPtr:     nil,
			expectedStatus: constants.AIMStatusProgressing,
			expectedReason: aimv1alpha1.AIMModelReasonAwaitingMetadata,
		},
		{
			name:           "expects templates but none exist",
			expectsPtr:     ptr.To(true),
			expectedStatus: constants.AIMStatusProgressing,
			expectedReason: aimv1alpha1.AIMModelReasonCreatingTemplates,
		},
		{
			name:           "no templates expected",
			expectsPtr:     ptr.To(false),
			expectedStatus: constants.AIMStatusReady,
			expectedReason: aimv1alpha1.AIMModelReasonNoTemplatesExpected,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := aggregateTemplateStatuses(tt.expectsPtr, []constants.AIMStatus{})

			if result.State != tt.expectedStatus {
				t.Errorf("expected status=%s, got %s", tt.expectedStatus, result.State)
			}
			if result.Reason != tt.expectedReason {
				t.Errorf("expected reason=%s, got %s", tt.expectedReason, result.Reason)
			}
		})
	}
}

func TestAggregateTemplateStatuses_AllSameState(t *testing.T) {
	tests := []struct {
		name           string
		statuses       []constants.AIMStatus
		expectedStatus constants.AIMStatus
		expectedReason string
	}{
		{
			name:           "all ready",
			statuses:       []constants.AIMStatus{constants.AIMStatusReady, constants.AIMStatusReady},
			expectedStatus: constants.AIMStatusReady,
			expectedReason: aimv1alpha1.AIMModelReasonAllTemplatesReady,
		},
		{
			name:           "all failed",
			statuses:       []constants.AIMStatus{constants.AIMStatusFailed, constants.AIMStatusFailed},
			expectedStatus: constants.AIMStatusFailed,
			expectedReason: aimv1alpha1.AIMModelReasonAllTemplatesFailed,
		},
		{
			name:           "all degraded counts as failed",
			statuses:       []constants.AIMStatus{constants.AIMStatusDegraded, constants.AIMStatusDegraded},
			expectedStatus: constants.AIMStatusFailed,
			expectedReason: aimv1alpha1.AIMModelReasonAllTemplatesFailed,
		},
		{
			name:           "all not available",
			statuses:       []constants.AIMStatus{constants.AIMStatusNotAvailable, constants.AIMStatusNotAvailable},
			expectedStatus: constants.AIMStatusNotAvailable,
			expectedReason: aimv1alpha1.AIMModelReasonNoTemplatesAvailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectsTemplates := true
			result := aggregateTemplateStatuses(&expectsTemplates, tt.statuses)

			if result.State != tt.expectedStatus {
				t.Errorf("expected status=%s, got %s", tt.expectedStatus, result.State)
			}
			if result.Reason != tt.expectedReason {
				t.Errorf("expected reason=%s, got %s", tt.expectedReason, result.Reason)
			}
		})
	}
}

func TestAggregateTemplateStatuses_MixedStates(t *testing.T) {
	tests := []struct {
		name           string
		statuses       []constants.AIMStatus
		expectedStatus constants.AIMStatus
		expectedReason string
	}{
		{
			name:           "degraded takes priority over progressing",
			statuses:       []constants.AIMStatus{constants.AIMStatusProgressing, constants.AIMStatusDegraded},
			expectedStatus: constants.AIMStatusDegraded,
			expectedReason: aimv1alpha1.AIMModelReasonSomeTemplatesDegraded,
		},
		{
			name:           "failed counts as degraded in mixed state",
			statuses:       []constants.AIMStatus{constants.AIMStatusReady, constants.AIMStatusFailed},
			expectedStatus: constants.AIMStatusDegraded,
			expectedReason: aimv1alpha1.AIMModelReasonSomeTemplatesDegraded,
		},
		{
			name:           "progressing takes priority over ready",
			statuses:       []constants.AIMStatus{constants.AIMStatusReady, constants.AIMStatusProgressing},
			expectedStatus: constants.AIMStatusProgressing,
			expectedReason: aimv1alpha1.AIMModelReasonTemplatesProgressing,
		},
		{
			name:           "pending counts as progressing",
			statuses:       []constants.AIMStatus{constants.AIMStatusReady, constants.AIMStatusPending},
			expectedStatus: constants.AIMStatusProgressing,
			expectedReason: aimv1alpha1.AIMModelReasonTemplatesProgressing,
		},
		{
			name:           "ready and notAvailable is ready",
			statuses:       []constants.AIMStatus{constants.AIMStatusReady, constants.AIMStatusNotAvailable},
			expectedStatus: constants.AIMStatusReady,
			expectedReason: aimv1alpha1.AIMModelReasonSomeTemplatesReady,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectsTemplates := true
			result := aggregateTemplateStatuses(&expectsTemplates, tt.statuses)

			if result.State != tt.expectedStatus {
				t.Errorf("expected status=%s, got %s", tt.expectedStatus, result.State)
			}
			if result.Reason != tt.expectedReason {
				t.Errorf("expected reason=%s, got %s", tt.expectedReason, result.Reason)
			}
		})
	}
}

// ============================================================================
// INSPECT TEMPLATE STATUSES TESTS
// ============================================================================

func TestInspectClusterTemplateStatuses(t *testing.T) {
	templates := []aimv1alpha1.AIMClusterServiceTemplate{
		{Status: aimv1alpha1.AIMServiceTemplateStatus{Status: constants.AIMStatusReady}},
		{Status: aimv1alpha1.AIMServiceTemplateStatus{Status: constants.AIMStatusReady}},
	}
	expectsTemplates := true

	result := inspectClusterTemplateStatuses(&expectsTemplates, templates)

	if result.State != constants.AIMStatusReady {
		t.Errorf("expected status=Ready, got %s", result.State)
	}
}

func TestInspectServiceTemplateStatuses(t *testing.T) {
	templates := []aimv1alpha1.AIMServiceTemplate{
		{Status: aimv1alpha1.AIMServiceTemplateStatus{Status: constants.AIMStatusReady}},
		{Status: aimv1alpha1.AIMServiceTemplateStatus{Status: constants.AIMStatusDegraded}},
	}
	expectsTemplates := true

	result := inspectServiceTemplateStatuses(&expectsTemplates, templates)

	if result.State != constants.AIMStatusDegraded {
		t.Errorf("expected status=Degraded for mixed ready/degraded, got %s", result.State)
	}
}

// ============================================================================
// GET COMPONENT HEALTH TESTS
// ============================================================================

func TestClusterModelFetchResult_GetComponentHealth(t *testing.T) {
	clusterModel := &aimv1alpha1.AIMClusterModel{
		Spec: aimv1alpha1.AIMModelSpec{
			Image: "test:latest",
		},
	}

	result := ClusterModelFetchResult{
		model: clusterModel,
		mergedRuntimeConfig: controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]{
			Value: &aimv1alpha1.AIMRuntimeConfigCommon{},
		},
		imageMetadata: controllerutils.FetchResult[*aimv1alpha1.ImageMetadata]{
			Value: &aimv1alpha1.ImageMetadata{},
		},
		clusterServiceTemplates: controllerutils.FetchResult[*aimv1alpha1.AIMClusterServiceTemplateList]{
			Value: &aimv1alpha1.AIMClusterServiceTemplateList{
				Items: []aimv1alpha1.AIMClusterServiceTemplate{
					{Status: aimv1alpha1.AIMServiceTemplateStatus{Status: constants.AIMStatusReady}},
				},
			},
		},
	}

	health := result.GetComponentHealth()

	if len(health) != 3 {
		t.Fatalf("expected 3 component health entries, got %d", len(health))
	}
}

func TestModelFetchResult_GetComponentHealth(t *testing.T) {
	model := &aimv1alpha1.AIMModel{
		Spec: aimv1alpha1.AIMModelSpec{
			Image: "test:latest",
		},
	}

	result := ModelFetchResult{
		model: model,
		mergedRuntimeConfig: controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]{
			Value: nil, // No config found
		},
		imageMetadata: controllerutils.FetchResult[*aimv1alpha1.ImageMetadata]{
			Value: &aimv1alpha1.ImageMetadata{},
		},
		serviceTemplates: controllerutils.FetchResult[*aimv1alpha1.AIMServiceTemplateList]{
			Value: &aimv1alpha1.AIMServiceTemplateList{},
		},
	}

	health := result.GetComponentHealth()

	if len(health) != 3 {
		t.Fatalf("expected 3 component health entries, got %d", len(health))
	}
}

// ============================================================================
// FETCH IMAGE METADATA TESTS
// ============================================================================

func TestFetchImageMetadata_SpecProvided(t *testing.T) {
	// When spec.ImageMetadata is set, it should be returned directly (air-gapped mode)
	specMetadata := &aimv1alpha1.ImageMetadata{
		Model: &aimv1alpha1.ModelMetadata{
			CanonicalName: "spec-provided-model",
		},
	}
	spec := aimv1alpha1.AIMModelSpec{
		Image:         "test:latest",
		ImageMetadata: specMetadata,
	}
	status := &aimv1alpha1.AIMModelStatus{}

	result := fetchImageMetadata(context.Background(), nil, spec, status, "default")

	if result.HasError() {
		t.Errorf("expected no error, got %v", result.Error)
	}
	if result.Value != specMetadata {
		t.Error("expected spec-provided metadata to be returned")
	}
}

func TestFetchImageMetadata_AlreadyCached(t *testing.T) {
	// When status already has metadata, no fetch should occur
	spec := aimv1alpha1.AIMModelSpec{
		Image: "test:latest",
	}
	status := &aimv1alpha1.AIMModelStatus{
		ImageMetadata: &aimv1alpha1.ImageMetadata{
			Model: &aimv1alpha1.ModelMetadata{
				CanonicalName: "cached-model",
			},
		},
	}

	result := fetchImageMetadata(context.Background(), nil, spec, status, "default")

	// Should return empty result (no fetch needed)
	if result.HasError() {
		t.Errorf("expected no error, got %v", result.Error)
	}
	if result.Value != nil {
		t.Error("expected nil value when already cached (no fetch needed)")
	}
}

// ============================================================================
// IMAGE METADATA COMPONENT HEALTH TESTS
// ============================================================================

func TestImageMetadataComponentHealth_Success(t *testing.T) {
	model := &aimv1alpha1.AIMModel{}

	result := ModelFetchResult{
		model: model,
		mergedRuntimeConfig: controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]{
			Value: &aimv1alpha1.AIMRuntimeConfigCommon{},
		},
		imageMetadata: controllerutils.FetchResult[*aimv1alpha1.ImageMetadata]{
			Value: &aimv1alpha1.ImageMetadata{
				Model: &aimv1alpha1.ModelMetadata{
					CanonicalName: "test-model",
				},
			},
		},
		serviceTemplates: controllerutils.FetchResult[*aimv1alpha1.AIMServiceTemplateList]{
			Value: &aimv1alpha1.AIMServiceTemplateList{},
		},
	}

	health := result.GetComponentHealth()

	// Find ImageMetadata health
	var imageMetadataHealth *controllerutils.ComponentHealth
	for i := range health {
		if health[i].Component == testComponentImageMetadata {
			imageMetadataHealth = &health[i]
			break
		}
	}

	if imageMetadataHealth == nil {
		t.Fatal("expected ImageMetadata component health")
	}
	if imageMetadataHealth.GetState() != constants.AIMStatusReady {
		t.Errorf("expected Ready state, got %s", imageMetadataHealth.GetState())
	}
	if len(imageMetadataHealth.Errors) != 0 {
		t.Errorf("expected no errors, got %v", imageMetadataHealth.Errors)
	}
}

func TestImageMetadataComponentHealth_AuthError(t *testing.T) {
	model := &aimv1alpha1.AIMModel{}

	authErr := &utils.ImageRegistryError{
		Type:    utils.ImagePullErrorAuth,
		Message: "401 unauthorized",
	}

	result := ModelFetchResult{
		model: model,
		mergedRuntimeConfig: controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]{
			Value: &aimv1alpha1.AIMRuntimeConfigCommon{},
		},
		imageMetadata: controllerutils.FetchResult[*aimv1alpha1.ImageMetadata]{
			Error: authErr,
		},
		serviceTemplates: controllerutils.FetchResult[*aimv1alpha1.AIMServiceTemplateList]{
			Value: &aimv1alpha1.AIMServiceTemplateList{},
		},
	}

	health := result.GetComponentHealth()

	var imageMetadataHealth *controllerutils.ComponentHealth
	for i := range health {
		if health[i].Component == testComponentImageMetadata {
			imageMetadataHealth = &health[i]
			break
		}
	}

	if imageMetadataHealth == nil {
		t.Fatal("expected ImageMetadata component health")
	}
	if len(imageMetadataHealth.Errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(imageMetadataHealth.Errors))
	}
	// The error should be passed through for state engine categorization
	if !errors.Is(imageMetadataHealth.Errors[0], authErr) {
		t.Errorf("expected auth error to be passed through, got %v", imageMetadataHealth.Errors[0])
	}
}

func TestImageMetadataComponentHealth_NotFoundError(t *testing.T) {
	model := &aimv1alpha1.AIMModel{}

	notFoundErr := &utils.ImageRegistryError{
		Type:    utils.ImagePullErrorNotFound,
		Message: "404 not found",
	}

	result := ModelFetchResult{
		model: model,
		mergedRuntimeConfig: controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]{
			Value: &aimv1alpha1.AIMRuntimeConfigCommon{},
		},
		imageMetadata: controllerutils.FetchResult[*aimv1alpha1.ImageMetadata]{
			Error: notFoundErr,
		},
		serviceTemplates: controllerutils.FetchResult[*aimv1alpha1.AIMServiceTemplateList]{
			Value: &aimv1alpha1.AIMServiceTemplateList{},
		},
	}

	health := result.GetComponentHealth()

	var imageMetadataHealth *controllerutils.ComponentHealth
	for i := range health {
		if health[i].Component == testComponentImageMetadata {
			imageMetadataHealth = &health[i]
			break
		}
	}

	if imageMetadataHealth == nil {
		t.Fatal("expected ImageMetadata component health")
	}
	if len(imageMetadataHealth.Errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(imageMetadataHealth.Errors))
	}
}

func TestImageMetadataComponentHealth_GenericError(t *testing.T) {
	model := &aimv1alpha1.AIMModel{}

	genericErr := &utils.ImageRegistryError{
		Type:    utils.ImagePullErrorGeneric,
		Message: "connection timeout",
	}

	result := ModelFetchResult{
		model: model,
		mergedRuntimeConfig: controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]{
			Value: &aimv1alpha1.AIMRuntimeConfigCommon{},
		},
		imageMetadata: controllerutils.FetchResult[*aimv1alpha1.ImageMetadata]{
			Error: genericErr,
		},
		serviceTemplates: controllerutils.FetchResult[*aimv1alpha1.AIMServiceTemplateList]{
			Value: &aimv1alpha1.AIMServiceTemplateList{},
		},
	}

	health := result.GetComponentHealth()

	var imageMetadataHealth *controllerutils.ComponentHealth
	for i := range health {
		if health[i].Component == testComponentImageMetadata {
			imageMetadataHealth = &health[i]
			break
		}
	}

	if imageMetadataHealth == nil {
		t.Fatal("expected ImageMetadata component health")
	}
	// Generic errors should be passed through for infrastructure error handling
	if len(imageMetadataHealth.Errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(imageMetadataHealth.Errors))
	}
}

// ============================================================================
// DISCOVERY CONFIG TESTS
// ============================================================================

func TestAIMModelSpec_ShouldCreateTemplates(t *testing.T) {
	tests := []struct {
		name     string
		spec     aimv1alpha1.AIMModelSpec
		expected bool
	}{
		{
			name: "nil discovery - defaults to true",
			spec: aimv1alpha1.AIMModelSpec{
				Image:     "test:latest",
				Discovery: nil,
			},
			expected: true,
		},
		{
			name: "discovery with createServiceTemplates=true",
			spec: aimv1alpha1.AIMModelSpec{
				Image: "test:latest",
				Discovery: &aimv1alpha1.AIMModelDiscoveryConfig{
					ExtractMetadata:        true,
					CreateServiceTemplates: true,
				},
			},
			expected: true,
		},
		{
			name: "discovery with createServiceTemplates=false",
			spec: aimv1alpha1.AIMModelSpec{
				Image: "test:latest",
				Discovery: &aimv1alpha1.AIMModelDiscoveryConfig{
					ExtractMetadata:        true,
					CreateServiceTemplates: false,
				},
			},
			expected: false,
		},
		{
			name: "extractMetadata=false but createServiceTemplates=true",
			spec: aimv1alpha1.AIMModelSpec{
				Image: "test:latest",
				Discovery: &aimv1alpha1.AIMModelDiscoveryConfig{
					ExtractMetadata:        false,
					CreateServiceTemplates: true,
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.spec.ShouldCreateTemplates()
			if result != tt.expected {
				t.Errorf("expected ShouldCreateTemplates()=%v, got %v", tt.expected, result)
			}
		})
	}
}

func TestAIMModelSpec_ExpectsTemplates(t *testing.T) {
	tests := []struct {
		name     string
		spec     aimv1alpha1.AIMModelSpec
		status   *aimv1alpha1.AIMModelStatus
		expected *bool // nil means unknown
	}{
		{
			name: "createServiceTemplates=false - no templates expected",
			spec: aimv1alpha1.AIMModelSpec{
				Image: "test:latest",
				Discovery: &aimv1alpha1.AIMModelDiscoveryConfig{
					CreateServiceTemplates: false,
				},
			},
			status:   nil,
			expected: ptr.To(false),
		},
		{
			name: "no metadata available - unknown",
			spec: aimv1alpha1.AIMModelSpec{
				Image: "test:latest",
			},
			status:   &aimv1alpha1.AIMModelStatus{},
			expected: nil,
		},
		{
			name: "spec metadata with recommended deployments - expects templates",
			spec: aimv1alpha1.AIMModelSpec{
				Image: "test:latest",
				ImageMetadata: &aimv1alpha1.ImageMetadata{
					Model: &aimv1alpha1.ModelMetadata{
						CanonicalName: "test-model",
						RecommendedDeployments: []aimv1alpha1.RecommendedDeployment{
							{GPUModel: "MI300X", GPUCount: 1},
						},
					},
				},
			},
			status:   nil,
			expected: ptr.To(true),
		},
		{
			name: "spec metadata without recommended deployments - no templates",
			spec: aimv1alpha1.AIMModelSpec{
				Image: "test:latest",
				ImageMetadata: &aimv1alpha1.ImageMetadata{
					Model: &aimv1alpha1.ModelMetadata{
						CanonicalName:          "test-model",
						RecommendedDeployments: nil,
					},
				},
			},
			status:   nil,
			expected: ptr.To(false),
		},
		{
			name: "status metadata with recommended deployments - expects templates",
			spec: aimv1alpha1.AIMModelSpec{
				Image: "test:latest",
			},
			status: &aimv1alpha1.AIMModelStatus{
				ImageMetadata: &aimv1alpha1.ImageMetadata{
					Model: &aimv1alpha1.ModelMetadata{
						CanonicalName: "test-model",
						RecommendedDeployments: []aimv1alpha1.RecommendedDeployment{
							{GPUModel: "MI300X", GPUCount: 1},
						},
					},
				},
			},
			expected: ptr.To(true),
		},
		{
			name: "spec metadata takes precedence over status",
			spec: aimv1alpha1.AIMModelSpec{
				Image: "test:latest",
				ImageMetadata: &aimv1alpha1.ImageMetadata{
					Model: &aimv1alpha1.ModelMetadata{
						CanonicalName:          "spec-model",
						RecommendedDeployments: nil, // No deployments in spec
					},
				},
			},
			status: &aimv1alpha1.AIMModelStatus{
				ImageMetadata: &aimv1alpha1.ImageMetadata{
					Model: &aimv1alpha1.ModelMetadata{
						CanonicalName: "status-model",
						RecommendedDeployments: []aimv1alpha1.RecommendedDeployment{
							{GPUModel: "MI300X", GPUCount: 1}, // Status has deployments
						},
					},
				},
			},
			expected: ptr.To(false), // Spec takes precedence, no deployments
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.spec.ExpectsTemplates(tt.status)

			if tt.expected == nil {
				if result != nil {
					t.Errorf("expected nil (unknown), got %v", *result)
				}
			} else {
				if result == nil {
					t.Errorf("expected %v, got nil", *tt.expected)
				} else if *result != *tt.expected {
					t.Errorf("expected %v, got %v", *tt.expected, *result)
				}
			}
		})
	}
}

func TestAIMModelSpec_GetEffectiveImageMetadata(t *testing.T) {
	specMetadata := &aimv1alpha1.ImageMetadata{
		Model: &aimv1alpha1.ModelMetadata{CanonicalName: "spec-model"},
	}
	statusMetadata := &aimv1alpha1.ImageMetadata{
		Model: &aimv1alpha1.ModelMetadata{CanonicalName: "status-model"},
	}

	tests := []struct {
		name              string
		spec              aimv1alpha1.AIMModelSpec
		status            *aimv1alpha1.AIMModelStatus
		expectedCanonical string
	}{
		{
			name: "spec metadata takes precedence",
			spec: aimv1alpha1.AIMModelSpec{
				Image:         "test:latest",
				ImageMetadata: specMetadata,
			},
			status: &aimv1alpha1.AIMModelStatus{
				ImageMetadata: statusMetadata,
			},
			expectedCanonical: "spec-model",
		},
		{
			name: "status metadata when spec is nil",
			spec: aimv1alpha1.AIMModelSpec{
				Image: "test:latest",
			},
			status: &aimv1alpha1.AIMModelStatus{
				ImageMetadata: statusMetadata,
			},
			expectedCanonical: "status-model",
		},
		{
			name: "nil when both are nil",
			spec: aimv1alpha1.AIMModelSpec{
				Image: "test:latest",
			},
			status:            &aimv1alpha1.AIMModelStatus{},
			expectedCanonical: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.spec.GetEffectiveImageMetadata(tt.status)

			if tt.expectedCanonical == "" {
				if result != nil {
					t.Errorf("expected nil, got %v", result)
				}
			} else {
				if result == nil || result.Model == nil {
					t.Fatalf("expected non-nil result with model")
				}
				if result.Model.CanonicalName != tt.expectedCanonical {
					t.Errorf("expected canonicalName '%s', got '%s'", tt.expectedCanonical, result.Model.CanonicalName)
				}
			}
		})
	}
}
