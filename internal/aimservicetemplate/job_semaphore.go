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

	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

// jobCreationMu serializes the "check count and plan job" section to prevent
// race conditions where multiple reconciliations all see slots available and
// plan jobs simultaneously.
var jobCreationMu sync.Mutex

// WithJobCreationLock executes the given function while holding the job creation lock.
// This serializes job creation decisions to prevent race conditions.
func WithJobCreationLock(fn func()) {
	jobCreationMu.Lock()
	defer jobCreationMu.Unlock()
	fn()
}

// DiscoveryJobSemaphore manages concurrent discovery job creation using a
// non-blocking semaphore pattern. It tracks which jobs have acquired slots
// to ensure proper slot release when jobs complete.
type DiscoveryJobSemaphore struct {
	// slots is a buffered channel acting as a counting semaphore.
	// Each token represents permission to have an active discovery job.
	slots chan struct{}

	// activeJobs tracks which job keys currently hold slots.
	// Key format: "namespace/jobName" for namespaced, "jobName" for cluster-scoped.
	activeJobs map[string]struct{}

	// mu protects activeJobs map
	mu sync.Mutex
}

// globalDiscoverySemaphore is the singleton instance used by all reconcilers.
var globalDiscoverySemaphore = NewDiscoveryJobSemaphore(constants.MaxConcurrentDiscoveryJobs)

// NewDiscoveryJobSemaphore creates a new semaphore with the specified number of slots.
func NewDiscoveryJobSemaphore(maxSlots int) *DiscoveryJobSemaphore {
	sem := &DiscoveryJobSemaphore{
		slots:      make(chan struct{}, maxSlots),
		activeJobs: make(map[string]struct{}),
	}
	// Fill the channel with tokens (available slots)
	for i := 0; i < maxSlots; i++ {
		sem.slots <- struct{}{}
	}
	return sem
}

// TryAcquire attempts to acquire a slot for the given job key.
// Returns true if a slot was acquired, false if no slots are available.
// This is non-blocking - it returns immediately.
func (s *DiscoveryJobSemaphore) TryAcquire(jobKey string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if this job already has a slot
	if _, exists := s.activeJobs[jobKey]; exists {
		return true // Already acquired
	}

	// Try to take a token (non-blocking)
	select {
	case <-s.slots:
		// Got a slot
		s.activeJobs[jobKey] = struct{}{}
		return true
	default:
		// No slots available
		return false
	}
}

// Release returns a slot for the given job key.
// Safe to call multiple times - only releases if the job actually holds a slot.
// Returns true if a slot was actually released.
func (s *DiscoveryJobSemaphore) Release(jobKey string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.activeJobs[jobKey]; exists {
		delete(s.activeJobs, jobKey)
		// Return the token
		select {
		case s.slots <- struct{}{}:
			// Slot returned
		default:
			// Channel full - shouldn't happen if properly balanced
		}
		return true
	}
	return false
}

// IsHeld returns true if the given job key currently holds a slot.
func (s *DiscoveryJobSemaphore) IsHeld(jobKey string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, exists := s.activeJobs[jobKey]
	return exists
}

// ActiveCount returns the number of currently held slots.
func (s *DiscoveryJobSemaphore) ActiveCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.activeJobs)
}

// AvailableSlots returns the number of available slots.
func (s *DiscoveryJobSemaphore) AvailableSlots() int {
	return len(s.slots)
}

// GetGlobalSemaphore returns the global discovery job semaphore instance.
func GetGlobalSemaphore() *DiscoveryJobSemaphore {
	return globalDiscoverySemaphore
}

// JobKey creates a consistent key for a template given namespace and name.
// For namespace-scoped templates, use the namespace.
// For cluster-scoped templates, pass empty namespace (will use "cluster:" prefix).
func JobKey(namespace, name string) string {
	if namespace == "" {
		return "cluster:" + name
	}
	return namespace + "/" + name
}
