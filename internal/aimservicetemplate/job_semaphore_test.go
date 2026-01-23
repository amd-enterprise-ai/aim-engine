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
	"sync"
	"testing"
)

func TestDiscoveryJobSemaphore_TryAcquire(t *testing.T) {
	sem := NewDiscoveryJobSemaphore(3)

	// Should be able to acquire 3 slots
	if !sem.TryAcquire("job1") {
		t.Error("should acquire slot for job1")
	}
	if !sem.TryAcquire("job2") {
		t.Error("should acquire slot for job2")
	}
	if !sem.TryAcquire("job3") {
		t.Error("should acquire slot for job3")
	}

	// 4th should fail
	if sem.TryAcquire("job4") {
		t.Error("should not acquire slot for job4 - at capacity")
	}

	// Verify counts
	if sem.ActiveCount() != 3 {
		t.Errorf("expected 3 active, got %d", sem.ActiveCount())
	}
	if sem.AvailableSlots() != 0 {
		t.Errorf("expected 0 available, got %d", sem.AvailableSlots())
	}
}

func TestDiscoveryJobSemaphore_Release(t *testing.T) {
	sem := NewDiscoveryJobSemaphore(2)

	// Acquire 2 slots
	sem.TryAcquire("job1")
	sem.TryAcquire("job2")

	// Release one
	sem.Release("job1")

	// Should be able to acquire again
	if !sem.TryAcquire("job3") {
		t.Error("should acquire slot for job3 after release")
	}

	if sem.ActiveCount() != 2 {
		t.Errorf("expected 2 active, got %d", sem.ActiveCount())
	}
}

func TestDiscoveryJobSemaphore_DoubleAcquire(t *testing.T) {
	sem := NewDiscoveryJobSemaphore(2)

	// Acquire same key twice should succeed (idempotent)
	if !sem.TryAcquire("job1") {
		t.Error("first acquire should succeed")
	}
	if !sem.TryAcquire("job1") {
		t.Error("second acquire of same key should succeed (already held)")
	}

	// Should only count as 1
	if sem.ActiveCount() != 1 {
		t.Errorf("expected 1 active, got %d", sem.ActiveCount())
	}
}

func TestDiscoveryJobSemaphore_DoubleRelease(t *testing.T) {
	sem := NewDiscoveryJobSemaphore(2)

	sem.TryAcquire("job1")
	sem.Release("job1")
	sem.Release("job1") // Should be safe

	if sem.ActiveCount() != 0 {
		t.Errorf("expected 0 active, got %d", sem.ActiveCount())
	}
	if sem.AvailableSlots() != 2 {
		t.Errorf("expected 2 available, got %d", sem.AvailableSlots())
	}
}

func TestDiscoveryJobSemaphore_Concurrent(t *testing.T) {
	sem := NewDiscoveryJobSemaphore(10)

	var wg sync.WaitGroup
	acquired := make(chan string, 100)

	// Try to acquire 20 slots concurrently
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := JobKey("ns", "job"+string(rune('A'+n)))
			if sem.TryAcquire(key) {
				acquired <- key
			}
		}(i)
	}

	wg.Wait()
	close(acquired)

	count := 0
	for range acquired {
		count++
	}

	// Should only have acquired 10
	if count != 10 {
		t.Errorf("expected 10 acquired, got %d", count)
	}
}

func TestJobKey(t *testing.T) {
	tests := []struct {
		namespace string
		name      string
		expected  string
	}{
		{"default", "my-template", "default/my-template"},
		{"kube-system", "test", "kube-system/test"},
		{"", "cluster-template", "cluster:cluster-template"},
	}

	for _, tt := range tests {
		result := JobKey(tt.namespace, tt.name)
		if result != tt.expected {
			t.Errorf("JobKey(%q, %q) = %q, want %q", tt.namespace, tt.name, result, tt.expected)
		}
	}
}
