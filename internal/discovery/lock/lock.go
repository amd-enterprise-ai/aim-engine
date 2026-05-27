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

// Package lock provides distributed locking and a global concurrency cap for
// discovery Jobs created by the AIM operator.
//
// A single Lease (aim-discovery-lock in the operator namespace) serializes the
// "count active discovery jobs + create new one" operation across all controller
// replicas and across all discovery producers (v1alpha1 AIMServiceTemplate,
// v1alpha2 AIMModel/AIMClusterModel). CountActiveDiscoveryJobs filters by the
// shared discovery labels so the cap applies globally — the underlying
// constraint is registry bandwidth, which is cluster-wide.
package lock

import (
	"context"
	"fmt"
	"os"
	"sync"
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

// localLock serializes discovery-lock acquisition among goroutines in the
// same operator pod. The Lease only serializes across pods (its
// HolderIdentity defaults to POD_NAME, so every goroutine in the same pod
// appears as already holding the lock); the local mutex closes that gap so
// the critical section is exclusive intra-pod as well.
var localLock sync.Mutex

const (
	// DiscoveryLockName is the name of the Lease used for discovery job creation locking.
	DiscoveryLockName = "aim-discovery-lock"

	// DiscoveryLockDuration is how long a lock is held before it expires.
	// Long enough to cover a full reconcile cycle (typically < 2 seconds) but
	// short enough that crashed controllers don't block for too long.
	DiscoveryLockDuration = 30 * time.Second

	// DiscoveryLockRetryInterval is how long to wait between lock acquisition attempts.
	DiscoveryLockRetryInterval = 100 * time.Millisecond

	// DiscoveryLabelName is the shared app.kubernetes.io/name value used to
	// identify all discovery Jobs across API versions. Counting against this
	// label keeps v1alpha1 and v1alpha2 discovery sharing the same global budget.
	DiscoveryLabelName = "aim-discovery"
)

// DiscoveryLock provides distributed locking for discovery job creation using Kubernetes Leases.
// This ensures that only one controller can check job counts and create jobs atomically,
// preventing race conditions where multiple controllers see available slots and all create jobs.
type DiscoveryLock struct {
	client    client.Client
	namespace string
	identity  string
}

// New creates a new DiscoveryLock instance.
func New(c client.Client) *DiscoveryLock {
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

	// Per-attempt errors during normal contention (optimistic-concurrency conflicts
	// from many reconcilers in the same pod racing the same Lease) are expected and
	// noisy; we only surface them at debug level here. If the caller's overall
	// timeout elapses we still return a real error from this function, so genuine
	// API outages are not hidden — they show up as the timeout error below.
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout acquiring discovery lock after %v", timeout)
		}

		acquired, err := l.tryAcquire(ctx)
		if err != nil {
			logger.V(1).Info("transient error trying to acquire discovery lock", "error", err.Error())
		}

		if acquired {
			logger.V(1).Info("acquired discovery lock", "identity", l.identity)
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(DiscoveryLockRetryInterval):
		}
	}
}

// tryAcquire makes a single attempt to acquire the lock.
func (l *DiscoveryLock) tryAcquire(ctx context.Context) (bool, error) {
	now := metav1.NowMicro()
	leaseKey := client.ObjectKey{Namespace: l.namespace, Name: DiscoveryLockName}

	var lease coordinationv1.Lease
	err := l.client.Get(ctx, leaseKey, &lease)

	if apierrors.IsNotFound(err) {
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
		// HolderIdentity is the pod name, which all reconciler goroutines in this
		// pod share. Many of them call Acquire concurrently, all see "we already
		// hold it" and race on this Update — losers get apierrors.IsConflict, which
		// is fully benign: another goroutine already bumped RenewTime for us. Treat
		// it like the takeover branch below and let the loser retry on the next
		// loop iteration; the next Get will reflect the renewed lease and we move
		// on without surfacing a noisy error.
		lease.Spec.RenewTime = &now
		if err := l.client.Update(ctx, &lease); err != nil {
			if apierrors.IsConflict(err) {
				return false, nil
			}
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

// Release releases the discovery lock.
// Safe to call even if we don't hold the lock.
func (l *DiscoveryLock) Release(ctx context.Context) error {
	logger := log.FromContext(ctx).WithName("discovery-lock")
	leaseKey := client.ObjectKey{Namespace: l.namespace, Name: DiscoveryLockName}

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
		logger.Error(err, "failed to release discovery lock")
		return err
	}

	logger.V(1).Info("released discovery lock", "identity", l.identity)
	return nil
}

func (l *DiscoveryLock) isExpired(lease *coordinationv1.Lease) bool {
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity == "" {
		return true
	}
	if lease.Spec.RenewTime == nil {
		return true
	}

	duration := DiscoveryLockDuration
	if lease.Spec.LeaseDurationSeconds != nil {
		duration = time.Duration(*lease.Spec.LeaseDurationSeconds) * time.Second
	}

	expiryTime := lease.Spec.RenewTime.Add(duration)
	return time.Now().After(expiryTime)
}

// CountActiveDiscoveryJobs counts the number of active (non-complete) discovery Jobs
// across all namespaces. The filter matches every discovery Job the operator creates
// (both v1alpha1 templates and v1alpha2 models), so the returned count is the
// global cluster-wide budget consumption.
func CountActiveDiscoveryJobs(ctx context.Context, c client.Client) (int, error) {
	var jobList batchv1.JobList
	if err := c.List(ctx, &jobList, client.MatchingLabels{
		"app.kubernetes.io/name":       DiscoveryLabelName,
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

// IsJobComplete returns true if the Job has completed (succeeded or failed).
func IsJobComplete(job *batchv1.Job) bool {
	if job == nil {
		return false
	}
	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobComplete && condition.Status == "True" {
			return true
		}
		if condition.Type == batchv1.JobFailed && condition.Status == "True" {
			return true
		}
	}
	return false
}

// WithDiscoveryLock acquires the discovery lock, runs the given function, and
// releases the lock. If the lock cannot be acquired within the timeout, the
// function is not run and the caller should handle requeue.
//
// The local sync.Mutex around Acquire/Release is what makes the critical
// section truly exclusive: many reconciler goroutines in this pod can call
// this concurrently and the Lease alone treats them all as the same holder
// (HolderIdentity == POD_NAME), which would otherwise let several of them
// run the body in parallel. Cross-pod exclusion is still provided by the
// Lease itself. The local mutex is also taken before the timeout-bounded
// acquire so a single context's cancellation correctly aborts both the
// in-process wait and the API-level acquire loop.
func WithDiscoveryLock(ctx context.Context, c client.Client, timeout time.Duration, fn func() error) error {
	if err := acquireLocalLock(ctx); err != nil {
		return err
	}
	defer localLock.Unlock()

	l := New(c)

	if err := l.Acquire(ctx, timeout); err != nil {
		logger := log.FromContext(ctx).WithName("discovery-lock")
		logger.V(1).Info("could not acquire discovery lock", "error", err)
		return err
	}

	defer func() {
		if err := l.Release(ctx); err != nil {
			logger := log.FromContext(ctx).WithName("discovery-lock")
			logger.Error(err, "failed to release discovery lock")
		}
	}()

	return fn()
}

// acquireLocalLock blocks until either the in-process mutex is taken or the
// context is cancelled. sync.Mutex.Lock has no context-aware variant so we
// poll a Locker through a goroutine and select on ctx.Done().
func acquireLocalLock(ctx context.Context) error {
	acquired := make(chan struct{})
	go func() {
		localLock.Lock()
		close(acquired)
	}()
	select {
	case <-acquired:
		return nil
	case <-ctx.Done():
		// We have a goroutine still racing to acquire the mutex. Wait
		// for it (in the background) and immediately release so the
		// next caller is not blocked forever by a phantom holder.
		go func() {
			<-acquired
			localLock.Unlock()
		}()
		return ctx.Err()
	}
}
