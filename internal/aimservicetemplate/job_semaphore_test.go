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

func TestReleaseOrphanedSlot(t *testing.T) {
	tests := []struct {
		name           string
		slotHeld       bool
		jobExists      bool
		templateReady  bool
		expectReleased bool
	}{
		{
			name:           "orphaned slot: held, no job, not ready",
			slotHeld:       true,
			jobExists:      false,
			templateReady:  false,
			expectReleased: true,
		},
		{
			name:           "not orphaned: slot not held",
			slotHeld:       false,
			jobExists:      false,
			templateReady:  false,
			expectReleased: false,
		},
		{
			name:           "not orphaned: job exists",
			slotHeld:       true,
			jobExists:      true,
			templateReady:  false,
			expectReleased: false,
		},
		{
			name:           "not orphaned: template ready",
			slotHeld:       true,
			jobExists:      false,
			templateReady:  true,
			expectReleased: false,
		},
		{
			name:           "not orphaned: job exists and template ready",
			slotHeld:       true,
			jobExists:      true,
			templateReady:  true,
			expectReleased: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset semaphore for each test
			globalDiscoverySemaphore = NewDiscoveryJobSemaphore(10)
			semaphoreKey := "test-namespace/test-template"

			// Setup initial state
			if tt.slotHeld {
				GetGlobalSemaphore().TryAcquire(semaphoreKey)
			}

			initialActive := GetGlobalSemaphore().ActiveCount()

			// Call the function under test
			released := ReleaseOrphanedSlot(semaphoreKey, tt.jobExists, tt.templateReady)

			if released != tt.expectReleased {
				t.Errorf("ReleaseOrphanedSlot() = %v, want %v", released, tt.expectReleased)
			}

			// Verify semaphore state
			finalActive := GetGlobalSemaphore().ActiveCount()
			if tt.expectReleased {
				if finalActive != initialActive-1 {
					t.Errorf("expected active count to decrease by 1, was %d, now %d", initialActive, finalActive)
				}
			} else {
				if finalActive != initialActive {
					t.Errorf("expected active count to stay same, was %d, now %d", initialActive, finalActive)
				}
			}
		})
	}
}

// TestOrphanedSlotRecovery simulates the race condition where a slot is acquired
// but job creation fails, and verifies that the slot can be recovered.
func TestOrphanedSlotRecovery(t *testing.T) {
	globalDiscoverySemaphore = NewDiscoveryJobSemaphore(10)
	semaphoreKey := "default/my-template"

	// Step 1: Acquire a slot (simulating what happens in PlanResources)
	if !GetGlobalSemaphore().TryAcquire(semaphoreKey) {
		t.Fatal("failed to acquire initial slot")
	}

	if GetGlobalSemaphore().ActiveCount() != 1 {
		t.Errorf("expected 1 active slot, got %d", GetGlobalSemaphore().ActiveCount())
	}

	// Step 2: Simulate Apply failure - job was never created
	// At this point, slot is held but no job exists

	// Step 3: On next reconcile, tryAcquireDiscoverySlot would return false
	// because IsHeld returns true. This is the bug we're fixing.

	// Step 4: Controller detects orphaned slot and releases it
	released := ReleaseOrphanedSlot(semaphoreKey, false, false)
	if !released {
		t.Error("expected orphaned slot to be released")
	}

	if GetGlobalSemaphore().ActiveCount() != 0 {
		t.Errorf("expected 0 active slots after recovery, got %d", GetGlobalSemaphore().ActiveCount())
	}

	// Step 5: Now the template can acquire a slot again
	if !GetGlobalSemaphore().TryAcquire(semaphoreKey) {
		t.Error("failed to acquire slot after recovery")
	}

	if GetGlobalSemaphore().ActiveCount() != 1 {
		t.Errorf("expected 1 active slot after re-acquire, got %d", GetGlobalSemaphore().ActiveCount())
	}
}
