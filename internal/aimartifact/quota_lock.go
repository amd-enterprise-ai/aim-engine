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
	"fmt"
	"os"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

const (
	quotaLockName          = "aim-artifact-quota-lock"
	quotaLockDuration      = 30 * time.Second
	quotaLockRetryInterval = 100 * time.Millisecond
)

// quotaLock provides distributed locking for artifact PVC creation using Kubernetes Leases.
// This serializes the "evaluate quota + create PVC" operation across all artifact
// reconciliations, preventing two artifacts from both passing the quota check and
// both creating PVCs that together exceed the configured limit.
type quotaLock struct {
	client    client.Client
	namespace string
	identity  string
}

func newQuotaLock(c client.Client) *quotaLock {
	identity := os.Getenv("POD_NAME")
	if identity == "" {
		hostname, _ := os.Hostname()
		identity = hostname
	}
	if identity == "" {
		identity = fmt.Sprintf("unknown-%d", time.Now().UnixNano())
	}

	return &quotaLock{
		client:    c,
		namespace: constants.GetOperatorNamespace(),
		identity:  identity,
	}
}

func (l *quotaLock) acquire(ctx context.Context, timeout time.Duration) error {
	logger := log.FromContext(ctx).WithName("quota-lock")
	deadline := time.Now().Add(timeout)

	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout acquiring quota lock after %v", timeout)
		}

		acquired, err := l.tryAcquire(ctx)
		if err != nil {
			logger.Error(err, "error trying to acquire quota lock")
		}

		if acquired {
			logger.V(1).Info("acquired quota lock", "identity", l.identity)
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(quotaLockRetryInterval):
		}
	}
}

func (l *quotaLock) tryAcquire(ctx context.Context) (bool, error) {
	now := metav1.NowMicro()
	leaseKey := client.ObjectKey{Namespace: l.namespace, Name: quotaLockName}

	var lease coordinationv1.Lease
	err := l.client.Get(ctx, leaseKey, &lease)

	if apierrors.IsNotFound(err) {
		newLease := &coordinationv1.Lease{
			ObjectMeta: metav1.ObjectMeta{
				Name:      quotaLockName,
				Namespace: l.namespace,
			},
			Spec: coordinationv1.LeaseSpec{
				HolderIdentity:       ptr.To(l.identity),
				AcquireTime:          &now,
				RenewTime:            &now,
				LeaseDurationSeconds: ptr.To(int32(quotaLockDuration.Seconds())),
			},
		}
		if err := l.client.Create(ctx, newLease); err != nil {
			if apierrors.IsAlreadyExists(err) {
				return false, nil
			}
			return false, err
		}
		return true, nil
	}

	if err != nil {
		return false, err
	}

	if lease.Spec.HolderIdentity != nil && *lease.Spec.HolderIdentity == l.identity {
		lease.Spec.RenewTime = &now
		if err := l.client.Update(ctx, &lease); err != nil {
			return false, err
		}
		return true, nil
	}

	if l.isExpired(&lease) {
		lease.Spec.HolderIdentity = ptr.To(l.identity)
		lease.Spec.AcquireTime = &now
		lease.Spec.RenewTime = &now
		if err := l.client.Update(ctx, &lease); err != nil {
			if apierrors.IsConflict(err) {
				return false, nil
			}
			return false, err
		}
		return true, nil
	}

	return false, nil
}

func (l *quotaLock) release(ctx context.Context) error {
	logger := log.FromContext(ctx).WithName("quota-lock")
	leaseKey := client.ObjectKey{Namespace: l.namespace, Name: quotaLockName}

	var lease coordinationv1.Lease
	if err := l.client.Get(ctx, leaseKey, &lease); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != l.identity {
		return nil
	}

	lease.Spec.HolderIdentity = nil
	lease.Spec.RenewTime = nil
	if err := l.client.Update(ctx, &lease); err != nil {
		if apierrors.IsConflict(err) {
			return nil
		}
		logger.Error(err, "failed to release quota lock")
		return err
	}

	logger.V(1).Info("released quota lock", "identity", l.identity)
	return nil
}

func (l *quotaLock) isExpired(lease *coordinationv1.Lease) bool {
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity == "" {
		return true
	}
	if lease.Spec.RenewTime == nil {
		return true
	}
	duration := quotaLockDuration
	if lease.Spec.LeaseDurationSeconds != nil {
		duration = time.Duration(*lease.Spec.LeaseDurationSeconds) * time.Second
	}
	return time.Now().After(lease.Spec.RenewTime.Add(duration))
}

// NeedsQuotaLock returns true if the artifact is likely to create a PVC in this
// reconciliation cycle, meaning the pipeline should run under the quota lock
// to serialize quota evaluation and PVC creation.
//
// The lock is only taken when size is already known from persisted state
// (spec.size or status.discoveredSizeBytes), since PVC creation requires a
// concrete size. Artifacts still in size discovery don't create PVCs and
// skipping the lock avoids unnecessary serialization.
//
// When a check-size job completes, PlanResources defers PVC creation to the
// next reconcile cycle so that discoveredSizeBytes is persisted to status
// first. This guarantees NeedsQuotaLock returns true on the cycle that
// actually creates the PVC.
func NeedsQuotaLock(artifact *aimv1alpha1.AIMArtifact) bool {
	if artifact.Status.PersistentVolumeClaim != "" {
		return false
	}
	if artifact.Status.Status == constants.AIMStatusReady {
		return false
	}
	return sizeKnownFromStatus(artifact)
}

// sizeKnownFromStatus returns true if the artifact's size can be determined
// from persisted spec/status fields (without running the pipeline).
func sizeKnownFromStatus(artifact *aimv1alpha1.AIMArtifact) bool {
	return !artifact.Spec.Size.IsZero() || artifact.Status.DiscoveredSizeBytes != nil
}

// WithQuotaLock acquires the quota lock, runs fn, and releases the lock.
// If the lock cannot be acquired within the timeout, fn is not called and
// an error is returned — the caller should requeue.
func WithQuotaLock(ctx context.Context, c client.Client, timeout time.Duration, fn func() error) error {
	lock := newQuotaLock(c)

	if err := lock.acquire(ctx, timeout); err != nil {
		logger := log.FromContext(ctx).WithName("quota-lock")
		logger.V(1).Info("could not acquire quota lock", "error", err)
		return err
	}

	defer func() {
		if err := lock.release(ctx); err != nil {
			logger := log.FromContext(ctx).WithName("quota-lock")
			logger.Error(err, "failed to release quota lock")
		}
	}()

	return fn()
}
