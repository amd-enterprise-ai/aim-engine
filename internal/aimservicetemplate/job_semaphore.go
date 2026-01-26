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
	"sync"

	batchv1 "k8s.io/api/batch/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

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

// semaphoreInitOnce ensures InitializeSemaphoreFromCluster is only called once.
var semaphoreInitOnce sync.Once

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

// ReleaseOrphanedSlot releases a semaphore slot if it's held but no job exists.
// This handles the race condition where a slot is acquired in PlanResources
// but job creation fails during Apply. Returns true if a slot was released.
//
// Parameters:
//   - semaphoreKey: the key for the template (use JobKey to generate)
//   - jobExists: whether a discovery job exists in the cluster for this template
//   - templateReady: whether the template has reached Ready status
//
// A slot is considered orphaned if:
//   - The slot is held (IsHeld returns true)
//   - No job exists in the cluster (jobExists is false)
//   - The template is not Ready (templateReady is false)
func ReleaseOrphanedSlot(semaphoreKey string, jobExists, templateReady bool) bool {
	if !GetGlobalSemaphore().IsHeld(semaphoreKey) {
		return false
	}
	if templateReady {
		// Template is Ready, slot should be released by the normal flow
		return false
	}
	if jobExists {
		// Job exists, not orphaned
		return false
	}
	// Slot is held, job doesn't exist, template not Ready = orphaned
	return GetGlobalSemaphore().Release(semaphoreKey)
}

// InitializeSemaphoreFromCluster synchronizes the semaphore state with active discovery jobs
// in the cluster. This should be called on operator startup to ensure the semaphore reflects
// actual cluster state, preventing job over-creation after operator restarts.
// This function is safe to call multiple times - it only runs once.
func InitializeSemaphoreFromCluster(ctx context.Context, c client.Client) error {
	var initErr error

	semaphoreInitOnce.Do(func() {
		logger := log.FromContext(ctx).WithName("semaphore-init")

		var jobList batchv1.JobList
		if err := c.List(ctx, &jobList, client.MatchingLabels{
			"app.kubernetes.io/name":       "aim-discovery",
			"app.kubernetes.io/component":  constants.LabelValueComponentDiscovery,
			"app.kubernetes.io/managed-by": constants.LabelValueManagedByController,
		}); err != nil {
			logger.Error(err, "Failed to list discovery jobs for semaphore initialization")
			initErr = err
			return
		}

		sem := GetGlobalSemaphore()
		initialized := 0

		for i := range jobList.Items {
			job := &jobList.Items[i]
			// Only track active (non-complete) jobs
			if !IsJobComplete(job) {
				templateName := job.Labels[constants.LabelKeyTemplate]
				if templateName == "" {
					continue
				}

				// Determine if this is a cluster-scoped template job
				// Cluster template jobs run in the operator namespace and have no namespace in the template reference
				var jobKey string
				if job.Namespace == constants.GetOperatorNamespace() {
					// Could be cluster-scoped - check if there's a namespace-scoped template with this name
					// For simplicity, we use the job namespace to determine scope
					jobKey = JobKey("", templateName)
				} else {
					jobKey = JobKey(job.Namespace, templateName)
				}

				if sem.TryAcquire(jobKey) {
					initialized++
					logger.V(1).Info("Acquired semaphore slot for existing active job",
						"jobKey", jobKey,
						"jobName", job.Name)
				}
			}
		}

		logger.Info("Semaphore initialized from cluster state",
			"activeJobs", initialized,
			"totalSlots", constants.MaxConcurrentDiscoveryJobs,
			"availableSlots", sem.AvailableSlots())
	})

	return initErr
}
