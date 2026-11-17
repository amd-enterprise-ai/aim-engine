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

package controller

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

// Both cache controllers map an artifact event to every cache in the namespace, so the
// predicate is what keeps a download's progress patches - one every PROGRESS_INTERVAL
// seconds for its whole duration - from reconciling all of them. It has to pass anything
// the reconciles read, since a dropped wakeup is the defect those watches exist to avoid.
func TestArtifactWakeupPredicate(t *testing.T) {
	readyCondition := func(status metav1.ConditionStatus, reason, message string) metav1.Condition {
		return metav1.Condition{
			Type:    controllerutils.ConditionTypeReady,
			Status:  status,
			Reason:  reason,
			Message: message,
		}
	}

	downloading := func() *aimv1alpha1.AIMArtifact {
		return &aimv1alpha1.AIMArtifact{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "artifact",
				Namespace:  "aim-artifact-wakeup-test",
				Generation: 1,
			},
			Spec: aimv1alpha1.AIMArtifactSpec{
				SourceURI: "hf://meta-llama/Llama-3.1-8B-Instruct",
			},
			Status: aimv1alpha1.AIMArtifactStatus{
				Status:                constants.AIMStatusProgressing,
				PersistentVolumeClaim: "artifact-cache-9579bc74",
				Progress: &aimv1alpha1.DownloadProgress{
					Percentage:        40,
					DownloadedBytes:   214748364,
					TotalBytes:        536870912,
					DisplayPercentage: "40 %",
				},
				Conditions: []metav1.Condition{
					readyCondition(metav1.ConditionFalse, "Progressing", "Waiting for components to become ready"),
				},
			},
		}
	}

	tests := []struct {
		name     string
		mutate   func(*aimv1alpha1.AIMArtifact)
		wantWake bool
	}{
		{
			name: "progress advanced",
			mutate: func(a *aimv1alpha1.AIMArtifact) {
				a.Status.Progress.Percentage = 80
				a.Status.Progress.DownloadedBytes = 429496729
				a.Status.Progress.DisplayPercentage = "80 %"
			},
			wantWake: false,
		},
		{
			name: "size accounting written",
			mutate: func(a *aimv1alpha1.AIMArtifact) {
				a.Status.DisplaySize = "512 MiB"
				a.Status.ResolvedSourceURI = "s3://aim-cache/artifacts/model/d699cbf72e50fd7d"
			},
			wantWake: false,
		},
		{
			name: "unrelated condition flipped",
			mutate: func(a *aimv1alpha1.AIMArtifact) {
				a.Status.Conditions = append(a.Status.Conditions, metav1.Condition{
					Type:    "DownloadJobPodsReady",
					Status:  metav1.ConditionTrue,
					Reason:  "PodsReady",
					Message: "Pods are ready",
				})
			},
			wantWake: false,
		},
		{
			name:     "became Ready",
			mutate:   func(a *aimv1alpha1.AIMArtifact) { a.Status.Status = constants.AIMStatusReady },
			wantWake: true,
		},
		{
			name:     "failed",
			mutate:   func(a *aimv1alpha1.AIMArtifact) { a.Status.Status = constants.AIMStatusFailed },
			wantWake: true,
		},
		{
			name:     "pvc rebound",
			mutate:   func(a *aimv1alpha1.AIMArtifact) { a.Status.PersistentVolumeClaim = "artifact-cache-other" },
			wantWake: true,
		},
		{
			name: "ready condition reason changed",
			mutate: func(a *aimv1alpha1.AIMArtifact) {
				a.Status.Conditions[0] = readyCondition(metav1.ConditionFalse, "QuotaBlocked", "Storage quota exceeded")
			},
			wantWake: true,
		},
		{
			// The message is deliberately outside the comparison: it is free text, and a
			// dedup key that contains free text stops deduplicating the moment someone
			// puts a byte count in it. The cache's own condition message can lag a
			// reworded artifact message until the next real change.
			name: "ready condition message reworded without a new reason",
			mutate: func(a *aimv1alpha1.AIMArtifact) {
				a.Status.Conditions[0] = readyCondition(metav1.ConditionFalse, "Progressing", "Waiting for components (downloaded 214748364 of 536870912 bytes)")
			},
			wantWake: false,
		},
		{
			name: "adopted by a cache",
			mutate: func(a *aimv1alpha1.AIMArtifact) {
				a.OwnerReferences = []metav1.OwnerReference{{
					APIVersion: aimv1alpha2.GroupVersion.String(),
					Kind:       "AIMProfileCache",
					Name:       "cache-dedicated",
					UID:        "cache-dedicated-uid",
				}}
			},
			wantWake: true,
		},
		{
			name:     "spec changed",
			mutate:   func(a *aimv1alpha1.AIMArtifact) { a.Generation = 2 },
			wantWake: true,
		},
		{
			name:     "deletion started",
			mutate:   func(a *aimv1alpha1.AIMArtifact) { a.DeletionTimestamp = &metav1.Time{Time: time.Now()} },
			wantWake: true,
		},
		{
			// An informer resync delivers an UpdateEvent whose old and new objects are
			// identical, which compares equal and is dropped.
			name:     "informer resync delivers an unchanged object",
			mutate:   func(*aimv1alpha1.AIMArtifact) {},
			wantWake: false,
		},
	}

	predicates := ArtifactWakeupPredicate()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldArtifact := downloading()
			newArtifact := downloading()
			tt.mutate(newArtifact)

			got := predicates.Update(event.UpdateEvent{ObjectOld: oldArtifact, ObjectNew: newArtifact})
			if got != tt.wantWake {
				t.Fatalf("Update() = %v, want %v", got, tt.wantWake)
			}
		})
	}

	t.Run("owner identity changed without changing owner count", func(t *testing.T) {
		oldArtifact := downloading()
		oldArtifact.OwnerReferences = []metav1.OwnerReference{{UID: "cache-a-uid"}}
		newArtifact := oldArtifact.DeepCopy()
		newArtifact.OwnerReferences = []metav1.OwnerReference{{UID: "cache-b-uid"}}

		if !predicates.Update(event.UpdateEvent{ObjectOld: oldArtifact, ObjectNew: newArtifact}) {
			t.Error("Update() = false, want true")
		}
	})

	t.Run("owner order changed", func(t *testing.T) {
		oldArtifact := downloading()
		oldArtifact.OwnerReferences = []metav1.OwnerReference{{UID: "cache-a-uid"}, {UID: "cache-b-uid"}}
		newArtifact := oldArtifact.DeepCopy()
		newArtifact.OwnerReferences = []metav1.OwnerReference{{UID: "cache-b-uid"}, {UID: "cache-a-uid"}}

		if predicates.Update(event.UpdateEvent{ObjectOld: oldArtifact, ObjectNew: newArtifact}) {
			t.Error("Update() = true, want false")
		}
	})

	t.Run("creates and deletes always wake", func(t *testing.T) {
		if !predicates.Create(event.CreateEvent{Object: downloading()}) {
			t.Error("Create() = false, want true")
		}
		if !predicates.Delete(event.DeleteEvent{Object: downloading()}) {
			t.Error("Delete() = false, want true")
		}
		if predicates.Generic(event.GenericEvent{Object: downloading()}) {
			t.Error("Generic() = true, want false")
		}
	})

	t.Run("non-artifact objects fail open", func(t *testing.T) {
		other := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "not-an-artifact"}}
		if !predicates.Update(event.UpdateEvent{ObjectOld: other, ObjectNew: other}) {
			t.Error("Update() with a non-AIMArtifact = false, want true (fail open)")
		}
	})
}
