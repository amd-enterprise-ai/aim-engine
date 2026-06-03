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
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
)

func TestTimeUntilSync(t *testing.T) {
	const interval = time.Hour

	newSource := func(generation, observed int64, lastSync *time.Time) *aimv1alpha1.AIMClusterModelSource {
		src := &aimv1alpha1.AIMClusterModelSource{}
		src.Generation = generation
		src.Status.ObservedGeneration = observed
		if lastSync != nil {
			t := metav1.NewTime(*lastSync)
			src.Status.LastSyncTime = &t
		}
		return src
	}
	timePtr := func(t time.Time) *time.Time { return &t }

	tests := []struct {
		name    string
		source  *aimv1alpha1.AIMClusterModelSource
		wantDue bool // expect wait <= 0
	}{
		{
			name:    "never synced is due",
			source:  newSource(1, 1, nil),
			wantDue: true,
		},
		{
			name:    "spec changed (generation ahead of observed) is due",
			source:  newSource(2, 1, timePtr(time.Now())),
			wantDue: true,
		},
		{
			name:    "interval elapsed is due",
			source:  newSource(1, 1, timePtr(time.Now().Add(-2*interval))),
			wantDue: true,
		},
		{
			name:    "within interval is not due",
			source:  newSource(1, 1, timePtr(time.Now().Add(-1*time.Minute))),
			wantDue: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wait := timeUntilSync(tt.source, interval)
			if tt.wantDue {
				if wait > 0 {
					t.Fatalf("timeUntilSync() = %v, want <= 0 (due)", wait)
				}
				return
			}
			if wait <= 0 || wait > interval {
				t.Fatalf("timeUntilSync() = %v, want within (0, interval]", wait)
			}
		})
	}
}

// TestReconcile_SyncNotDue_ShortCircuits verifies the gate requeues without
// running the pipeline. The zero-value pipeline would panic if reached, so a
// clean requeue with an untouched LastSyncTime proves no sync ran.
func TestReconcile_SyncNotDue_ShortCircuits(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := aimv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}

	// Truncate to seconds; metav1.Time round-trips at RFC3339 (second) precision.
	lastSync := metav1.NewTime(time.Now().Add(-1 * time.Minute).Truncate(time.Second))
	source := &aimv1alpha1.AIMClusterModelSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-source",
			Generation: 1,
		},
		Spec: aimv1alpha1.AIMClusterModelSourceSpec{
			SyncInterval: metav1.Duration{Duration: time.Hour},
		},
		Status: aimv1alpha1.AIMClusterModelSourceStatus{
			ObservedGeneration: 1,
			LastSyncTime:       &lastSync,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(source).
		WithStatusSubresource(source).
		Build()

	r := &AIMClusterModelSourceReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: source.Name},
	})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if res.RequeueAfter <= 0 || res.RequeueAfter > time.Hour {
		t.Fatalf("Reconcile() RequeueAfter = %v, want within (0, 1h]", res.RequeueAfter)
	}

	var got aimv1alpha1.AIMClusterModelSource
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: source.Name}, &got); err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !got.Status.LastSyncTime.Equal(&lastSync) {
		t.Fatalf("LastSyncTime = %v, want unchanged %v", got.Status.LastSyncTime, lastSync)
	}
}
