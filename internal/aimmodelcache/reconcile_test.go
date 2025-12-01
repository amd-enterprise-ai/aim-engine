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

THE SOFTWARE IS PROVIDED "AS IS" WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package aimmodelcache

import (
	"context"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/aimruntimeconfig"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/testutil"
)

// ============================================================================
// TEST HELPERS
// ============================================================================

// notFoundError simulates a Kubernetes NotFound error
type notFoundError struct{}

func (e *notFoundError) Error() string { return "not found" }

// ============================================================================
// HELPER FUNCTIONS TESTS
// ============================================================================

func TestExtractModelFromSourceURI(t *testing.T) {
	tests := []struct {
		name      string
		sourceURI string
		expected  string
	}{
		{
			name:      "HuggingFace URI",
			sourceURI: "hf://amd/Llama-3.1-8B-Instruct",
			expected:  "amd/Llama-3.1-8B-Instruct",
		},
		{
			name:      "S3 URI",
			sourceURI: "s3://bucket/model-v1",
			expected:  "bucket/model-v1",
		},
		{
			name:      "No scheme",
			sourceURI: "just-a-path",
			expected:  "just-a-path",
		},
		{
			name:      "Empty string",
			sourceURI: "",
			expected:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractModelFromSourceURI(tt.sourceURI)
			if result != tt.expected {
				t.Errorf("extractModelFromSourceURI(%q) = %q, want %q", tt.sourceURI, result, tt.expected)
			}
		})
	}
}

// ============================================================================
// OBSERVE INTEGRATION TESTS
// ============================================================================

func TestObserve(t *testing.T) {
	cache := testutil.NewModelCache()
	fetchResult := FetchResult{
		runtimeConfig: aimruntimeconfig.RuntimeConfigFetchResult{
			ConfigName:      "default",
			ClusterConfig:   nil,
			NamespaceConfig: nil,
		},
		pvc: pvcFetchResult{
			PVC:   testutil.NewPVC(),
			error: nil,
		},
		storageClass: storageClassFetchResult{
			storageClass: &storagev1.StorageClass{
				VolumeBindingMode: ptr.To(storagev1.VolumeBindingImmediate),
			},
			error: nil,
		},
		job: jobFetchResult{
			job:   &batchv1.Job{},
			error: &notFoundError{},
		},
	}

	reconciler := &Reconciler{}
	obs, err := reconciler.Observe(context.Background(), cache, fetchResult)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !obs.pvc.found {
		t.Error("expected PVC to be found")
	}
	if !obs.storageClass.found {
		t.Error("expected StorageClass to be found")
	}
	if obs.job.found {
		t.Error("expected job not to be found")
	}
}

// ============================================================================
// PROJECT INTEGRATION TESTS
// ============================================================================

func TestProject_IntegrationWithRuntimeConfig(t *testing.T) {
	obs := Observation{
		runtimeConfig: aimruntimeconfig.RuntimeConfigObservation{
			ConfigNotFound: true,
			Error:          &notFoundError{},
		},
		pvc: pvcObservation{
			found: true,
			ready: true,
		},
	}

	status := &aimv1alpha1.AIMModelCacheStatus{}
	cm := controllerutils.NewConditionManager(nil)

	reconciler := &Reconciler{
		Scheme: runtime.NewScheme(),
	}
	reconciler.Project(status, cm, obs)

	// Should stop at runtime config error
	if status.Status != constants.AIMStatusFailed {
		t.Errorf("expected status Failed when runtime config not found, got %s", status.Status)
	}
}
