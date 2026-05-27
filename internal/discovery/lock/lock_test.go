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

package lock

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestWithDiscoveryLock_IntraPodExclusion verifies that even though the
// distributed Lease alone cannot distinguish between goroutines running in
// the same pod (they all share HolderIdentity == POD_NAME), the in-process
// mutex guarantees only one body runs at a time.
func TestWithDiscoveryLock_IntraPodExclusion(t *testing.T) {
	t.Parallel()

	c := fake.NewClientBuilder().Build()

	var (
		inflight int32
		maxSeen  int32
		wg       sync.WaitGroup
	)

	const workers = 10
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			err := WithDiscoveryLock(context.Background(), c, 5*time.Second, func() error {
				cur := atomic.AddInt32(&inflight, 1)
				for {
					prev := atomic.LoadInt32(&maxSeen)
					if cur <= prev || atomic.CompareAndSwapInt32(&maxSeen, prev, cur) {
						break
					}
				}
				// Hold the lock briefly so any concurrent
				// holder would be observable.
				time.Sleep(5 * time.Millisecond)
				atomic.AddInt32(&inflight, -1)
				return nil
			})
			if err != nil {
				t.Errorf("WithDiscoveryLock: %v", err)
			}
		}()
	}

	wg.Wait()

	if got := atomic.LoadInt32(&maxSeen); got != 1 {
		t.Fatalf("max concurrent holders = %d, want 1 (intra-pod exclusion broken)", got)
	}
}

// TestWithDiscoveryLock_ContextCancel ensures that a cancelled context while
// waiting for the in-process mutex returns the context error rather than
// blocking forever, and that the lock is eventually released so future
// callers are not starved.
func TestWithDiscoveryLock_ContextCancel(t *testing.T) {
	t.Parallel()

	c := fake.NewClientBuilder().Build()

	holderRunning := make(chan struct{})
	holderRelease := make(chan struct{})
	holderDone := make(chan struct{})

	go func() {
		defer close(holderDone)
		_ = WithDiscoveryLock(context.Background(), c, 5*time.Second, func() error {
			close(holderRunning)
			<-holderRelease
			return nil
		})
	}()

	<-holderRunning

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := WithDiscoveryLock(ctx, c, 5*time.Second, func() error {
		t.Fatal("body should not have run for a cancelled context")
		return nil
	})
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}

	close(holderRelease)
	<-holderDone

	// A subsequent caller with a live context should still be able to
	// take the lock — the cancelled caller must not have leaked the mutex.
	done := make(chan struct{})
	go func() {
		_ = WithDiscoveryLock(context.Background(), c, 5*time.Second, func() error {
			close(done)
			return nil
		})
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("lock leaked after cancelled caller; follow-up caller never ran")
	}
}
