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

package aimartifact

import (
	"context"
	"testing"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

// ============================================================
// NeedsQuotaLock tests
// ============================================================

func TestNeedsQuotaLock(t *testing.T) {
	tests := []struct {
		name string
		opts []func(*aimv1alpha1.AIMArtifact)
		want bool
	}{
		{
			name: "PVC already exists",
			opts: []func(*aimv1alpha1.AIMArtifact){
				withSpecSize(1 * gi),
				func(a *aimv1alpha1.AIMArtifact) { a.Status.PersistentVolumeClaim = "pvc-123" },
			},
			want: false,
		},
		{
			name: "already Ready",
			opts: []func(*aimv1alpha1.AIMArtifact){
				withSpecSize(1 * gi),
				withReady(),
			},
			want: false,
		},
		{
			name: "spec.size set, no PVC — needs lock",
			opts: []func(*aimv1alpha1.AIMArtifact){
				withSpecSize(1 * gi),
			},
			want: true,
		},
		{
			name: "discoveredSizeBytes set, no PVC — needs lock",
			opts: []func(*aimv1alpha1.AIMArtifact){
				withDiscoveredSize(500 * 1024 * 1024),
			},
			want: true,
		},
		{
			name: "no size known (discovery in progress) — no lock needed",
			opts: nil,
			want: false,
		},
		{
			name: "no size, progressing status — no lock (size discovery phase)",
			opts: []func(*aimv1alpha1.AIMArtifact){
				func(a *aimv1alpha1.AIMArtifact) { a.Status.Status = constants.AIMStatusProgressing },
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := makeArtifact("test", "uid-1", tt.opts...)
			got := NeedsQuotaLock(&a)
			if got != tt.want {
				t.Errorf("NeedsQuotaLock() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ============================================================
// quotaLock acquire / release / expiry tests
// ============================================================

func newTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = coordinationv1.AddToScheme(scheme)
	return scheme
}

func newTestLock(t *testing.T) *quotaLock {
	t.Helper()
	scheme := newTestScheme()
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	return &quotaLock{
		client:    c,
		namespace: "test-ns",
		identity:  "controller-a",
	}
}

func TestTryAcquire_CreatesLeaseWhenNotFound(t *testing.T) {
	lock := newTestLock(t)
	ctx := context.Background()

	acquired, err := lock.tryAcquire(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !acquired {
		t.Fatal("expected to acquire lock on first attempt")
	}

	var lease coordinationv1.Lease
	if err := lock.client.Get(ctx, leaseKey(lock), &lease); err != nil {
		t.Fatalf("lease should exist: %v", err)
	}
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != "controller-a" {
		t.Errorf("holder = %v, want controller-a", lease.Spec.HolderIdentity)
	}
}

func TestTryAcquire_RenewsOwnLease(t *testing.T) {
	lock := newTestLock(t)
	ctx := context.Background()

	if _, err := lock.tryAcquire(ctx); err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	acquired, err := lock.tryAcquire(ctx)
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if !acquired {
		t.Fatal("should re-acquire own lock")
	}
}

func TestTryAcquire_RejectsWhenHeldByOther(t *testing.T) {
	lock := newTestLock(t)
	ctx := context.Background()

	if _, err := lock.tryAcquire(ctx); err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	otherLock := &quotaLock{
		client:    lock.client,
		namespace: lock.namespace,
		identity:  "controller-b",
	}

	acquired, err := otherLock.tryAcquire(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if acquired {
		t.Fatal("should not acquire lock held by another")
	}
}

func TestTryAcquire_TakesOverExpiredLease(t *testing.T) {
	now := metav1.NowMicro()
	expired := metav1.NewMicroTime(now.Add(-2 * quotaLockDuration))

	existingLease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      quotaLockName,
			Namespace: "test-ns",
		},
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity:       ptr.To("dead-controller"),
			AcquireTime:          &expired,
			RenewTime:            &expired,
			LeaseDurationSeconds: ptr.To(int32(quotaLockDuration.Seconds())),
		},
	}

	scheme := newTestScheme()
	c := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(existingLease).Build()

	lock := &quotaLock{
		client:    c,
		namespace: "test-ns",
		identity:  "new-controller",
	}
	ctx := context.Background()

	acquired, err := lock.tryAcquire(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !acquired {
		t.Fatal("should acquire expired lock")
	}

	var lease coordinationv1.Lease
	if err := c.Get(ctx, leaseKey(lock), &lease); err != nil {
		t.Fatalf("get lease: %v", err)
	}
	if *lease.Spec.HolderIdentity != "new-controller" {
		t.Errorf("holder = %v, want new-controller", *lease.Spec.HolderIdentity)
	}
}

func TestRelease_ClearsHolder(t *testing.T) {
	lock := newTestLock(t)
	ctx := context.Background()

	if _, err := lock.tryAcquire(ctx); err != nil {
		t.Fatalf("acquire: %v", err)
	}

	if err := lock.release(ctx); err != nil {
		t.Fatalf("release: %v", err)
	}

	var lease coordinationv1.Lease
	if err := lock.client.Get(ctx, leaseKey(lock), &lease); err != nil {
		t.Fatalf("get lease: %v", err)
	}
	if lease.Spec.HolderIdentity != nil {
		t.Errorf("holder should be nil after release, got %v", *lease.Spec.HolderIdentity)
	}
}

func TestRelease_NoopWhenNotHolder(t *testing.T) {
	lock := newTestLock(t)
	ctx := context.Background()

	if _, err := lock.tryAcquire(ctx); err != nil {
		t.Fatalf("acquire: %v", err)
	}

	otherLock := &quotaLock{
		client:    lock.client,
		namespace: lock.namespace,
		identity:  "controller-b",
	}
	if err := otherLock.release(ctx); err != nil {
		t.Fatalf("release by non-holder should succeed: %v", err)
	}

	var lease coordinationv1.Lease
	if err := lock.client.Get(ctx, leaseKey(lock), &lease); err != nil {
		t.Fatalf("get lease: %v", err)
	}
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != "controller-a" {
		t.Errorf("holder should still be controller-a, got %v", lease.Spec.HolderIdentity)
	}
}

func TestRelease_NoopWhenLeaseNotFound(t *testing.T) {
	lock := newTestLock(t)
	if err := lock.release(context.Background()); err != nil {
		t.Fatalf("release without lease should succeed: %v", err)
	}
}

func TestIsExpired(t *testing.T) {
	lock := newTestLock(t)
	now := metav1.NowMicro()
	old := metav1.NewMicroTime(now.Add(-2 * quotaLockDuration))

	tests := []struct {
		name string
		spec coordinationv1.LeaseSpec
		want bool
	}{
		{
			name: "nil holder",
			spec: coordinationv1.LeaseSpec{},
			want: true,
		},
		{
			name: "empty holder",
			spec: coordinationv1.LeaseSpec{HolderIdentity: ptr.To("")},
			want: true,
		},
		{
			name: "nil renewTime",
			spec: coordinationv1.LeaseSpec{HolderIdentity: ptr.To("x")},
			want: true,
		},
		{
			name: "recently renewed",
			spec: coordinationv1.LeaseSpec{
				HolderIdentity:       ptr.To("x"),
				RenewTime:            &now,
				LeaseDurationSeconds: ptr.To(int32(30)),
			},
			want: false,
		},
		{
			name: "long expired",
			spec: coordinationv1.LeaseSpec{
				HolderIdentity:       ptr.To("x"),
				RenewTime:            &old,
				LeaseDurationSeconds: ptr.To(int32(30)),
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lease := &coordinationv1.Lease{Spec: tt.spec}
			got := lock.isExpired(lease)
			if got != tt.want {
				t.Errorf("isExpired() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ============================================================
// Acquire with timeout
// ============================================================

func TestAcquire_SucceedsImmediately(t *testing.T) {
	lock := newTestLock(t)
	if err := lock.acquire(context.Background(), 5*time.Second); err != nil {
		t.Fatalf("acquire should succeed: %v", err)
	}
}

func TestAcquire_TimesOutWhenHeldByOther(t *testing.T) {
	lock := newTestLock(t)
	ctx := context.Background()

	if _, err := lock.tryAcquire(ctx); err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	otherLock := &quotaLock{
		client:    lock.client,
		namespace: lock.namespace,
		identity:  "controller-b",
	}

	err := otherLock.acquire(ctx, 300*time.Millisecond)
	if err == nil {
		t.Fatal("acquire should timeout")
	}
}

func TestAcquire_RespectsContextCancellation(t *testing.T) {
	lock := newTestLock(t)
	ctx := context.Background()

	if _, err := lock.tryAcquire(ctx); err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	otherLock := &quotaLock{
		client:    lock.client,
		namespace: lock.namespace,
		identity:  "controller-b",
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := otherLock.acquire(ctx, 30*time.Second)
	if err == nil {
		t.Fatal("acquire should fail with cancelled context")
	}
}

// ============================================================
// Release after acquire enables re-acquisition
// ============================================================

func TestAcquireRelease_AllowsReacquisition(t *testing.T) {
	lock := newTestLock(t)
	ctx := context.Background()

	if err := lock.acquire(ctx, 5*time.Second); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := lock.release(ctx); err != nil {
		t.Fatalf("release: %v", err)
	}

	otherLock := &quotaLock{
		client:    lock.client,
		namespace: lock.namespace,
		identity:  "controller-b",
	}
	acquired, err := otherLock.tryAcquire(ctx)
	if err != nil {
		t.Fatalf("re-acquire: %v", err)
	}
	if !acquired {
		t.Fatal("should acquire after release")
	}
}

// ============================================================
// sizeKnownFromStatus tests
// ============================================================

func TestSizeKnownFromStatus(t *testing.T) {
	tests := []struct {
		name string
		opts []func(*aimv1alpha1.AIMArtifact)
		want bool
	}{
		{
			name: "no size info",
			want: false,
		},
		{
			name: "spec.size set",
			opts: []func(*aimv1alpha1.AIMArtifact){
				func(a *aimv1alpha1.AIMArtifact) {
					a.Spec.Size = resource.MustParse("1Gi")
				},
			},
			want: true,
		},
		{
			name: "discoveredSizeBytes set",
			opts: []func(*aimv1alpha1.AIMArtifact){withDiscoveredSize(500)},
			want: true,
		},
		{
			name: "both set",
			opts: []func(*aimv1alpha1.AIMArtifact){
				func(a *aimv1alpha1.AIMArtifact) {
					a.Spec.Size = resource.MustParse("1Gi")
				},
				withDiscoveredSize(500),
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := makeArtifact("test", "uid-1", tt.opts...)
			if got := sizeKnownFromStatus(&a); got != tt.want {
				t.Errorf("sizeKnownFromStatus() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ============================================================
// helpers
// ============================================================

func leaseKey(l *quotaLock) client.ObjectKey {
	return client.ObjectKey{Namespace: l.namespace, Name: quotaLockName}
}
