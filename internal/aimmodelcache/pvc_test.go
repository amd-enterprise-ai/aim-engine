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
	"testing"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/aimruntimeconfig"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/testutil"
)

// ============================================================================
// OBSERVE TESTS
// ============================================================================

func TestObservePVC_NotFound(t *testing.T) {
	result := pvcFetchResult{
		PVC:   &corev1.PersistentVolumeClaim{},
		error: &notFoundError{},
	}

	obs := observePVC(result)

	if obs.found {
		t.Error("expected found=false when PVC not found")
	}
	if obs.bound {
		t.Error("expected bound=false when PVC not found")
	}
	if obs.ready {
		t.Error("expected ready=false when PVC not found")
	}
}

func TestObservePVC_Bound(t *testing.T) {
	pvc := testutil.NewPVC(
		testutil.WithPVCName("test-cache-pvc"),
		testutil.WithPVCPhase(corev1.ClaimBound),
	)

	result := pvcFetchResult{
		PVC:   pvc,
		error: nil,
	}

	obs := observePVC(result)

	if !obs.found {
		t.Error("expected found=true when PVC exists")
	}
	if !obs.bound {
		t.Error("expected bound=true when PVC is bound")
	}
	if !obs.ready {
		t.Error("expected ready=true when PVC is bound")
	}
	if obs.lost {
		t.Error("expected lost=false when PVC is bound")
	}
}

func TestObservePVC_Pending(t *testing.T) {
	pvc := testutil.NewPVC(
		testutil.WithPVCPhase(corev1.ClaimPending),
	)

	result := pvcFetchResult{
		PVC:   pvc,
		error: nil,
	}

	obs := observePVC(result)

	if !obs.found {
		t.Error("expected found=true when PVC exists")
	}
	if obs.bound {
		t.Error("expected bound=false when PVC is pending")
	}
	if obs.ready {
		t.Error("expected ready=false when PVC is pending")
	}
}

func TestObservePVC_Lost(t *testing.T) {
	pvc := &corev1.PersistentVolumeClaim{
		Status: corev1.PersistentVolumeClaimStatus{
			Phase: corev1.ClaimLost,
		},
	}

	result := pvcFetchResult{
		PVC:   pvc,
		error: nil,
	}

	obs := observePVC(result)

	if !obs.found {
		t.Error("expected found=true when PVC exists")
	}
	if obs.ready {
		t.Error("expected ready=false when PVC is lost")
	}
	if !obs.lost {
		t.Error("expected lost=true when PVC phase is Lost")
	}
}

func TestObserveStorageClass_NotFound(t *testing.T) {
	result := storageClassFetchResult{
		storageClass: &storagev1.StorageClass{},
		error:        &notFoundError{},
	}

	obs := observeStorageClass(result)

	if obs.found {
		t.Error("expected found=false when storage class not found")
	}
	if obs.waitForFirstConsumer {
		t.Error("expected waitForFirstConsumer=false when not found")
	}
}

func TestObserveStorageClass_Immediate(t *testing.T) {
	sc := &storagev1.StorageClass{
		VolumeBindingMode: ptr.To(storagev1.VolumeBindingImmediate),
	}

	result := storageClassFetchResult{
		storageClass: sc,
		error:        nil,
	}

	obs := observeStorageClass(result)

	if !obs.found {
		t.Error("expected found=true when storage class exists")
	}
	if obs.waitForFirstConsumer {
		t.Error("expected waitForFirstConsumer=false for Immediate binding")
	}
}

func TestObserveStorageClass_WaitForFirstConsumer(t *testing.T) {
	sc := &storagev1.StorageClass{
		VolumeBindingMode: ptr.To(storagev1.VolumeBindingWaitForFirstConsumer),
	}

	result := storageClassFetchResult{
		storageClass: sc,
		error:        nil,
	}

	obs := observeStorageClass(result)

	if !obs.found {
		t.Error("expected found=true when storage class exists")
	}
	if !obs.waitForFirstConsumer {
		t.Error("expected waitForFirstConsumer=true for WaitForFirstConsumer binding")
	}
}

// ============================================================================
// PLAN TESTS
// ============================================================================

func TestPlanPVC_NotFound(t *testing.T) {
	cache := testutil.NewModelCache()
	obs := Observation{
		pvc: pvcObservation{
			found: false,
		},
		runtimeConfig: aimruntimeconfig.RuntimeConfigObservation{
			MergedConfig: &aimv1alpha1.AIMRuntimeConfigCommon{
				AIMServiceRuntimeConfig: aimv1alpha1.AIMServiceRuntimeConfig{
					Storage: &aimv1alpha1.AIMStorageConfig{
						PVCHeadroomPercent: ptr.To(int32(10)),
					},
				},
			},
		},
	}

	scheme := runtime.NewScheme()
	_ = aimv1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	pvcObj := planPVC(cache, obs, scheme)

	if pvcObj == nil {
		t.Fatal("expected PVC to be planned when not found")
	}

	pvc, ok := pvcObj.(*corev1.PersistentVolumeClaim)
	if !ok {
		t.Fatal("expected PVC object")
	}

	if pvc.Name == "" {
		t.Error("expected PVC to have a name")
	}
	if len(pvc.Spec.AccessModes) == 0 {
		t.Error("expected PVC to have access modes")
	}
	storageRequest := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
	if storageRequest.IsZero() {
		t.Error("expected PVC to have storage request")
	}
}

func TestPlanPVC_AlreadyExists(t *testing.T) {
	cache := testutil.NewModelCache()
	obs := Observation{
		pvc: pvcObservation{
			found: true,
			pvc:   testutil.NewPVC(),
		},
	}

	scheme := runtime.NewScheme()
	pvcObj := planPVC(cache, obs, scheme)

	if pvcObj != nil {
		t.Error("expected nil when PVC already exists (immutable)")
	}
}

// ============================================================================
// PROJECT TESTS
// ============================================================================

func TestProjectPVC(t *testing.T) {
	tests := []struct {
		name           string
		obs            Observation
		expectPVCName  string
		expectNoPVCRef bool
	}{
		{
			name: "PVC found",
			obs: Observation{
				pvc: pvcObservation{
					found: true,
					pvc:   testutil.NewPVC(testutil.WithPVCName("test-pvc")),
				},
			},
			expectPVCName: "test-pvc",
		},
		{
			name: "PVC not found",
			obs: Observation{
				pvc: pvcObservation{
					found: false,
				},
			},
			expectNoPVCRef: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := &aimv1alpha1.AIMModelCacheStatus{}
			projectPVC(status, tt.obs)

			if tt.expectNoPVCRef && status.PersistentVolumeClaim != "" {
				t.Errorf("expected no PVC ref, got %s", status.PersistentVolumeClaim)
			}
			if !tt.expectNoPVCRef && status.PersistentVolumeClaim != tt.expectPVCName {
				t.Errorf("expected PVC ref %s, got %s", tt.expectPVCName, status.PersistentVolumeClaim)
			}
		})
	}
}

func TestProjectStorageReadyCondition(t *testing.T) {
	tests := []struct {
		name           string
		obs            Observation
		expectStatus   metav1.ConditionStatus
		expectReason   string
		expectNotFound bool
	}{
		{
			name: "PVC not found",
			obs: Observation{
				pvc: pvcObservation{found: false},
			},
			expectStatus: metav1.ConditionFalse,
			expectReason: aimv1alpha1.AIMModelCacheReasonPVCPending,
		},
		{
			name: "PVC bound",
			obs: Observation{
				pvc: pvcObservation{
					found: true,
					pvc: &corev1.PersistentVolumeClaim{
						Status: corev1.PersistentVolumeClaimStatus{
							Phase: corev1.ClaimBound,
						},
					},
				},
			},
			expectStatus: metav1.ConditionTrue,
			expectReason: aimv1alpha1.AIMModelCacheReasonPVCBound,
		},
		{
			name: "PVC pending",
			obs: Observation{
				pvc: pvcObservation{
					found: true,
					pvc: &corev1.PersistentVolumeClaim{
						Status: corev1.PersistentVolumeClaimStatus{
							Phase: corev1.ClaimPending,
						},
					},
				},
			},
			expectStatus: metav1.ConditionFalse,
			expectReason: aimv1alpha1.AIMModelCacheReasonPVCProvisioning,
		},
		{
			name: "PVC lost",
			obs: Observation{
				pvc: pvcObservation{
					found: true,
					pvc: &corev1.PersistentVolumeClaim{
						Status: corev1.PersistentVolumeClaimStatus{
							Phase: corev1.ClaimLost,
						},
					},
				},
			},
			expectStatus: metav1.ConditionFalse,
			expectReason: aimv1alpha1.AIMModelCacheReasonPVCLost,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := controllerutils.NewConditionManager(nil)
			projectStorageReadyCondition(cm, tt.obs)

			conditions := cm.Conditions()
			found := false
			for _, cond := range conditions {
				if cond.Type == aimv1alpha1.AIMModelCacheConditionStorageReady {
					found = true
					if cond.Status != tt.expectStatus {
						t.Errorf("expected status %v, got %v", tt.expectStatus, cond.Status)
					}
					if cond.Reason != tt.expectReason {
						t.Errorf("expected reason %s, got %s", tt.expectReason, cond.Reason)
					}
				}
			}

			if !found && !tt.expectNotFound {
				t.Error("expected StorageReady condition to be set")
			}
		})
	}
}
