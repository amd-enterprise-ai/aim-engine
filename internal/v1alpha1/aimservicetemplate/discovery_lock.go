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

package aimservicetemplate

import (
	"context"
	"fmt"
	"os"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

const (
	// DiscoveryLockName is the name of the Lease used for discovery job creation locking.
	DiscoveryLockName = "aim-discovery-lock"

	// DiscoveryLockDuration is how long a lock is held before it expires.
	// This should be long enough to cover a full reconcile cycle (typically < 2 seconds)
	// but short enough that crashed controllers don't block for too long.
	DiscoveryLockDuration = 30 * time.Second

	// DiscoveryLockRetryInterval is how long to wait between lock acquisition attempts.
	DiscoveryLockRetryInterval = 100 * time.Millisecond
)

// DiscoveryLock provides distributed locking for discovery job creation using Kubernetes Leases.
// This ensures that only one controller can check job counts and create jobs atomically,
// preventing race conditions where multiple controllers see available slots and all create jobs.
type DiscoveryLock struct {
	client    client.Client
	namespace string
	identity  string
}

// NewDiscoveryLock creates a new DiscoveryLock instance.
func NewDiscoveryLock(c client.Client) *DiscoveryLock {
	// Use pod name + hostname as identity for uniqueness
	identity := os.Getenv("POD_NAME")
	if identity == "" {
		hostname, _ := os.Hostname()
		identity = hostname
	}
	if identity == "" {
		identity = fmt.Sprintf("unknown-%d", time.Now().UnixNano())
	}

	return &DiscoveryLock{
		client:    c,
		namespace: constants.GetOperatorNamespace(),
		identity:  identity,
	}
}

// Acquire attempts to acquire the discovery lock within the given timeout.
// Returns nil if the lock was acquired, or an error if it couldn't be acquired in time.
func (l *DiscoveryLock) Acquire(ctx context.Context, timeout time.Duration) error {
	logger := log.FromContext(ctx).WithName("discovery-lock")
	deadline := time.Now().Add(timeout)

	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout acquiring discovery lock after %v", timeout)
		}

		acquired, err := l.tryAcquire(ctx)
		if err != nil {
			logger.Error(err, "error trying to acquire discovery lock")
			// Continue retrying on transient errors
		}

		if acquired {
			logger.V(1).Info("acquired discovery lock", "identity", l.identity)
			return nil
		}

		// Wait before retrying
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(DiscoveryLockRetryInterval):
			// Continue
		}
	}
}

// tryAcquire makes a single attempt to acquire the lock.
// Returns (true, nil) if acquired, (false, nil) if held by another, or (false, err) on error.
func (l *DiscoveryLock) tryAcquire(ctx context.Context) (bool, error) {
	now := metav1.NowMicro()
	leaseKey := client.ObjectKey{Namespace: l.namespace, Name: DiscoveryLockName}

	// Try to get existing lease
	var lease coordinationv1.Lease
	err := l.client.Get(ctx, leaseKey, &lease)

	if apierrors.IsNotFound(err) {
		// Lease doesn't exist - create it with us as holder
		newLease := &coordinationv1.Lease{
			ObjectMeta: metav1.ObjectMeta{
				Name:      DiscoveryLockName,
				Namespace: l.namespace,
			},
			Spec: coordinationv1.LeaseSpec{
				HolderIdentity:       ptr.To(l.identity),
				AcquireTime:          &now,
				RenewTime:            &now,
				LeaseDurationSeconds: ptr.To(int32(DiscoveryLockDuration.Seconds())),
			},
		}

		if err := l.client.Create(ctx, newLease); err != nil {
			if apierrors.IsAlreadyExists(err) {
				// Race condition - someone else created it first
				return false, nil
			}
			return false, err
		}
		return true, nil
	}

	if err != nil {
		return false, err
	}

	// Lease exists - check if we already hold it or if it's expired
	if lease.Spec.HolderIdentity != nil && *lease.Spec.HolderIdentity == l.identity {
		// We already hold the lock - renew it
		lease.Spec.RenewTime = &now
		if err := l.client.Update(ctx, &lease); err != nil {
			return false, err
		}
		return true, nil
	}

	// Check if the lock has expired
	if l.isExpired(&lease) {
		// Lock expired - try to acquire it
		lease.Spec.HolderIdentity = ptr.To(l.identity)
		lease.Spec.AcquireTime = &now
		lease.Spec.RenewTime = &now

		if err := l.client.Update(ctx, &lease); err != nil {
			if apierrors.IsConflict(err) {
				// Someone else acquired it - that's fine
				return false, nil
			}
			return false, err
		}
		return true, nil
	}

	// Lock is held by someone else and not expired
	return false, nil
}

// Release releases the discovery lock.
// Safe to call even if we don't hold the lock.
func (l *DiscoveryLock) Release(ctx context.Context) error {
	logger := log.FromContext(ctx).WithName("discovery-lock")
	leaseKey := client.ObjectKey{Namespace: l.namespace, Name: DiscoveryLockName}

	var lease coordinationv1.Lease
	if err := l.client.Get(ctx, leaseKey, &lease); err != nil {
		if apierrors.IsNotFound(err) {
			return nil // Nothing to release
		}
		return err
	}

	// Only release if we're the holder
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != l.identity {
		return nil // We don't hold it
	}

	// Clear the holder identity to release the lock
	lease.Spec.HolderIdentity = nil
	lease.Spec.RenewTime = nil

	if err := l.client.Update(ctx, &lease); err != nil {
		if apierrors.IsConflict(err) {
			// Conflict is fine - someone else modified it
			return nil
		}
		logger.Error(err, "failed to release discovery lock")
		return err
	}

	logger.V(1).Info("released discovery lock", "identity", l.identity)
	return nil
}

// isExpired checks if a lease has expired based on its renew time and duration.
func (l *DiscoveryLock) isExpired(lease *coordinationv1.Lease) bool {
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity == "" {
		return true // No holder means available
	}

	if lease.Spec.RenewTime == nil {
		return true // No renew time means expired
	}

	duration := DiscoveryLockDuration
	if lease.Spec.LeaseDurationSeconds != nil {
		duration = time.Duration(*lease.Spec.LeaseDurationSeconds) * time.Second
	}

	expiryTime := lease.Spec.RenewTime.Add(duration)
	return time.Now().After(expiryTime)
}

// CountActiveDiscoveryJobs counts the number of active (non-complete) discovery jobs in the cluster.
func CountActiveDiscoveryJobs(ctx context.Context, c client.Client) (int, error) {
	var jobList batchv1.JobList
	if err := c.List(ctx, &jobList, client.MatchingLabels{
		"app.kubernetes.io/name":       "aim-discovery",
		"app.kubernetes.io/component":  constants.LabelValueComponentDiscovery,
		"app.kubernetes.io/managed-by": constants.LabelValueManagedByController,
	}); err != nil {
		return 0, err
	}

	active := 0
	for i := range jobList.Items {
		if !IsJobComplete(&jobList.Items[i]) {
			active++
		}
	}

	return active, nil
}

// NeedsDiscoveryLock returns true if the template might need to create a discovery job,
// meaning we should acquire the lock before running the pipeline.
func NeedsDiscoveryLock(status constants.AIMStatus, hasInlineModelSources bool) bool {
	// No lock needed if template is Ready (discovery already done)
	if status == constants.AIMStatusReady {
		return false
	}

	// No lock needed if template has inline model sources (no discovery needed)
	if hasInlineModelSources {
		return false
	}

	// Template might need discovery - acquire lock
	return true
}

// WithDiscoveryLock acquires the discovery lock, runs the given function, and releases the lock.
// If the lock cannot be acquired within the timeout, the function is not run and we return
// a result indicating requeue.
func WithDiscoveryLock(ctx context.Context, c client.Client, timeout time.Duration, fn func() error) error {
	lock := NewDiscoveryLock(c)

	if err := lock.Acquire(ctx, timeout); err != nil {
		// Log but don't return error - caller should handle requeue
		logger := log.FromContext(ctx).WithName("discovery-lock")
		logger.V(1).Info("could not acquire discovery lock", "error", err)
		return err
	}

	defer func() {
		if err := lock.Release(ctx); err != nil {
			logger := log.FromContext(ctx).WithName("discovery-lock")
			logger.Error(err, "failed to release discovery lock")
		}
	}()

	return fn()
}
