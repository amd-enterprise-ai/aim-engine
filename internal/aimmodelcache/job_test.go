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

package aimmodelcache

import (
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/testutil"
)

// ============================================================================
// OBSERVE TESTS
// ============================================================================

func TestObserveJob_NotFound(t *testing.T) {
	result := jobFetchResult{
		job:   &batchv1.Job{},
		error: &notFoundError{},
	}

	obs := observeJob(result)

	if obs.found {
		t.Error("expected found=false when job not found")
	}
	if obs.succeeded {
		t.Error("expected succeeded=false when job not found")
	}
	if obs.failed {
		t.Error("expected failed=false when job not found")
	}
}

func TestObserveJob_Succeeded(t *testing.T) {
	job := &batchv1.Job{
		Status: batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{
				{
					Type:   batchv1.JobComplete,
					Status: corev1.ConditionTrue,
				},
			},
			Succeeded: 1,
		},
	}

	result := jobFetchResult{
		job:   job,
		error: nil,
	}

	obs := observeJob(result)

	if !obs.found {
		t.Error("expected found=true when job exists")
	}
	if !obs.succeeded {
		t.Error("expected succeeded=true when job has Complete condition")
	}
	if obs.failed {
		t.Error("expected failed=false when job succeeded")
	}
}

func TestObserveJob_Failed(t *testing.T) {
	job := &batchv1.Job{
		Status: batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{
				{
					Type:   batchv1.JobFailed,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}

	result := jobFetchResult{
		job:   job,
		error: nil,
	}

	obs := observeJob(result)

	if !obs.found {
		t.Error("expected found=true when job exists")
	}
	if obs.succeeded {
		t.Error("expected succeeded=false when job failed")
	}
	if !obs.failed {
		t.Error("expected failed=true when job has Failed condition")
	}
}

func TestObserveJob_Running(t *testing.T) {
	job := &batchv1.Job{
		Status: batchv1.JobStatus{
			Active: 1,
		},
	}

	result := jobFetchResult{
		job:   job,
		error: nil,
	}

	obs := observeJob(result)

	if !obs.found {
		t.Error("expected found=true when job exists")
	}
	if !obs.PendingOrRunning {
		t.Error("expected PendingOrRunning=true when job has active pods")
	}
}

// ============================================================================
// PLAN TESTS
// ============================================================================

func TestCanCreateJob(t *testing.T) {
	tests := []struct {
		name     string
		obs      Observation
		expected bool
	}{
		{
			name: "storage ready",
			obs: Observation{
				pvc: pvcObservation{
					ready: true,
				},
			},
			expected: true,
		},
		{
			name: "PVC pending with waitForFirstConsumer",
			obs: Observation{
				pvc: pvcObservation{
					found: true,
					ready: false,
					pvc: &corev1.PersistentVolumeClaim{
						Status: corev1.PersistentVolumeClaimStatus{
							Phase: corev1.ClaimPending,
						},
					},
				},
				storageClass: storageClassObservation{
					found:                true,
					waitForFirstConsumer: true,
				},
			},
			expected: true,
		},
		{
			name: "PVC not found",
			obs: Observation{
				pvc: pvcObservation{
					found: false,
				},
			},
			expected: false,
		},
		{
			name: "PVC pending without waitForFirstConsumer",
			obs: Observation{
				pvc: pvcObservation{
					found: true,
					ready: false,
					pvc: &corev1.PersistentVolumeClaim{
						Status: corev1.PersistentVolumeClaimStatus{
							Phase: corev1.ClaimPending,
						},
					},
				},
				storageClass: storageClassObservation{
					found:                true,
					waitForFirstConsumer: false,
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := canCreateJob(tt.obs)
			if result != tt.expected {
				t.Errorf("canCreateJob() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestPlanJob_CanCreate(t *testing.T) {
	cache := testutil.NewModelCache()
	obs := Observation{
		pvc: pvcObservation{
			ready: true,
			pvc:   testutil.NewPVC(),
		},
		job: jobObservation{
			found: false,
		},
	}

	scheme := runtime.NewScheme()
	_ = aimv1alpha1.AddToScheme(scheme)
	_ = batchv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	jobObj := planJob(cache, obs, scheme)

	if jobObj == nil {
		t.Fatal("expected job to be planned when can create and not found")
	}

	job, ok := jobObj.(*batchv1.Job)
	if !ok {
		t.Fatal("expected Job object")
	}

	if job.Name == "" {
		t.Error("expected job to have a name")
	}
}

func TestPlanJob_AlreadyExists(t *testing.T) {
	cache := testutil.NewModelCache()
	obs := Observation{
		pvc: pvcObservation{
			ready: true,
		},
		job: jobObservation{
			found: true,
		},
	}

	scheme := runtime.NewScheme()
	jobObj := planJob(cache, obs, scheme)

	if jobObj != nil {
		t.Error("expected nil when job already exists")
	}
}

func TestPlanJob_CannotCreate(t *testing.T) {
	cache := testutil.NewModelCache()
	obs := Observation{
		pvc: pvcObservation{
			found: false, // Cannot create job without PVC
		},
		job: jobObservation{
			found: false,
		},
	}

	scheme := runtime.NewScheme()
	jobObj := planJob(cache, obs, scheme)

	if jobObj != nil {
		t.Error("expected nil when cannot create job")
	}
}
