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
	"sync"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

// resetGlobalSemaphore resets the global semaphore to a fresh state for testing.
// This is needed because the semaphore is a package-level singleton.
func resetGlobalSemaphore() {
	globalDiscoverySemaphore = NewDiscoveryJobSemaphore(constants.MaxConcurrentDiscoveryJobs)
}

func makeTestTemplate(name, namespace string) *aimv1alpha1.AIMServiceTemplate {
	return &aimv1alpha1.AIMServiceTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       types.UID(fmt.Sprintf("uid-%s", name)),
		},
		Spec: aimv1alpha1.AIMServiceTemplateSpec{
			AIMServiceTemplateSpecCommon: aimv1alpha1.AIMServiceTemplateSpecCommon{
				ModelName: "test-model",
			},
		},
	}
}

func makeTestModel() *aimv1alpha1.AIMModel {
	return &aimv1alpha1.AIMModel{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-model",
			Namespace: "default",
		},
		Spec: aimv1alpha1.AIMModelSpec{
			Image: "test-image:latest",
		},
		Status: aimv1alpha1.AIMModelStatus{
			Status: constants.AIMStatusReady,
		},
	}
}

func TestPlanResources_AcquiresSemaphoreSlot(t *testing.T) {
	resetGlobalSemaphore()

	r := &ServiceTemplateReconciler{}
	ctx := context.Background()

	template := makeTestTemplate("test-template", "default")
	model := makeTestModel()

	obs := ServiceTemplateObservation{
		ServiceTemplateFetchResult: ServiceTemplateFetchResult{
			template:     template,
			model:        controllerutils.FetchResult[*aimv1alpha1.AIMModel]{Value: model},
			discoveryJob: controllerutils.FetchResult[*batchv1.Job]{}, // No existing job
		},
	}

	reconcileCtx := controllerutils.ReconcileContext[*aimv1alpha1.AIMServiceTemplate]{
		Object: template,
	}

	// Before: semaphore should have all slots available
	if GetGlobalSemaphore().ActiveCount() != 0 {
		t.Errorf("expected 0 active slots before, got %d", GetGlobalSemaphore().ActiveCount())
	}

	// Call PlanResources
	result := r.PlanResources(ctx, reconcileCtx, obs)

	// After: should have acquired a slot
	if GetGlobalSemaphore().ActiveCount() != 1 {
		t.Errorf("expected 1 active slot after, got %d", GetGlobalSemaphore().ActiveCount())
	}

	// Should have a job to apply
	if len(result.GetToApply()) != 1 {
		t.Errorf("expected 1 object to apply, got %d", len(result.GetToApply()))
	}

	// Verify the slot is held for the correct key
	key := JobKey(template.Namespace, template.Name)
	if !GetGlobalSemaphore().IsHeld(key) {
		t.Errorf("expected semaphore slot to be held for key %s", key)
	}
}

func TestPlanResources_RespectsMaxConcurrentLimit(t *testing.T) {
	resetGlobalSemaphore()

	r := &ServiceTemplateReconciler{}
	ctx := context.Background()

	// Create more templates than the max limit
	numTemplates := constants.MaxConcurrentDiscoveryJobs + 5
	jobsPlanned := 0

	for i := 0; i < numTemplates; i++ {
		template := makeTestTemplate(fmt.Sprintf("test-template-%d", i), "default")
		model := makeTestModel()

		obs := ServiceTemplateObservation{
			ServiceTemplateFetchResult: ServiceTemplateFetchResult{
				template:     template,
				model:        controllerutils.FetchResult[*aimv1alpha1.AIMModel]{Value: model},
				discoveryJob: controllerutils.FetchResult[*batchv1.Job]{}, // No existing job
			},
		}

		reconcileCtx := controllerutils.ReconcileContext[*aimv1alpha1.AIMServiceTemplate]{
			Object: template,
		}

		result := r.PlanResources(ctx, reconcileCtx, obs)
		if len(result.GetToApply()) > 0 {
			jobsPlanned++
		}
	}

	// Should only have planned MaxConcurrentDiscoveryJobs
	if jobsPlanned != constants.MaxConcurrentDiscoveryJobs {
		t.Errorf("expected %d jobs planned, got %d", constants.MaxConcurrentDiscoveryJobs, jobsPlanned)
	}

	// Semaphore should be at capacity
	if GetGlobalSemaphore().ActiveCount() != constants.MaxConcurrentDiscoveryJobs {
		t.Errorf("expected %d active slots, got %d", constants.MaxConcurrentDiscoveryJobs, GetGlobalSemaphore().ActiveCount())
	}

	if GetGlobalSemaphore().AvailableSlots() != 0 {
		t.Errorf("expected 0 available slots, got %d", GetGlobalSemaphore().AvailableSlots())
	}
}

func TestPlanResources_ConcurrentAccess(t *testing.T) {
	resetGlobalSemaphore()

	r := &ServiceTemplateReconciler{}
	ctx := context.Background()

	numGoroutines := 30
	var wg sync.WaitGroup
	jobsPlanned := make(chan int, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			template := makeTestTemplate(fmt.Sprintf("test-template-%d", idx), "default")
			model := makeTestModel()

			obs := ServiceTemplateObservation{
				ServiceTemplateFetchResult: ServiceTemplateFetchResult{
					template:     template,
					model:        controllerutils.FetchResult[*aimv1alpha1.AIMModel]{Value: model},
					discoveryJob: controllerutils.FetchResult[*batchv1.Job]{}, // No existing job
				},
			}

			reconcileCtx := controllerutils.ReconcileContext[*aimv1alpha1.AIMServiceTemplate]{
				Object: template,
			}

			result := r.PlanResources(ctx, reconcileCtx, obs)
			if len(result.GetToApply()) > 0 {
				jobsPlanned <- 1
			} else {
				jobsPlanned <- 0
			}
		}(i)
	}

	wg.Wait()
	close(jobsPlanned)

	total := 0
	for planned := range jobsPlanned {
		total += planned
	}

	// Should only have planned MaxConcurrentDiscoveryJobs even with concurrent access
	if total != constants.MaxConcurrentDiscoveryJobs {
		t.Errorf("expected %d jobs planned concurrently, got %d", constants.MaxConcurrentDiscoveryJobs, total)
	}
}

func TestPlanResources_ReleasesSlotOnJobComplete(t *testing.T) {
	resetGlobalSemaphore()

	r := &ServiceTemplateReconciler{}
	ctx := context.Background()

	template := makeTestTemplate("test-template", "default")
	model := makeTestModel()

	// First reconciliation: no job, should acquire slot and plan job
	obs1 := ServiceTemplateObservation{
		ServiceTemplateFetchResult: ServiceTemplateFetchResult{
			template:     template,
			model:        controllerutils.FetchResult[*aimv1alpha1.AIMModel]{Value: model},
			discoveryJob: controllerutils.FetchResult[*batchv1.Job]{}, // No existing job
		},
	}

	reconcileCtx := controllerutils.ReconcileContext[*aimv1alpha1.AIMServiceTemplate]{
		Object: template,
	}

	r.PlanResources(ctx, reconcileCtx, obs1)

	if GetGlobalSemaphore().ActiveCount() != 1 {
		t.Errorf("expected 1 active slot after first reconcile, got %d", GetGlobalSemaphore().ActiveCount())
	}

	// Second reconciliation: job completed, should release slot
	completedJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "discover-test-template-abc123",
			Namespace: "default",
		},
		Status: batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{
				{
					Type:   batchv1.JobComplete,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}

	obs2 := ServiceTemplateObservation{
		ServiceTemplateFetchResult: ServiceTemplateFetchResult{
			template:     template,
			model:        controllerutils.FetchResult[*aimv1alpha1.AIMModel]{Value: model},
			discoveryJob: controllerutils.FetchResult[*batchv1.Job]{Value: completedJob},
		},
	}

	r.PlanResources(ctx, reconcileCtx, obs2)

	// Slot should be released
	if GetGlobalSemaphore().ActiveCount() != 0 {
		t.Errorf("expected 0 active slots after job completion, got %d", GetGlobalSemaphore().ActiveCount())
	}
}

func TestPlanResources_SkipsWhenJobAlreadyExists(t *testing.T) {
	resetGlobalSemaphore()

	r := &ServiceTemplateReconciler{}
	ctx := context.Background()

	template := makeTestTemplate("test-template", "default")
	model := makeTestModel()

	// Existing active job
	activeJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "discover-test-template-abc123",
			Namespace: "default",
		},
		Status: batchv1.JobStatus{
			Active: 1, // Job is running
		},
	}

	obs := ServiceTemplateObservation{
		ServiceTemplateFetchResult: ServiceTemplateFetchResult{
			template:     template,
			model:        controllerutils.FetchResult[*aimv1alpha1.AIMModel]{Value: model},
			discoveryJob: controllerutils.FetchResult[*batchv1.Job]{Value: activeJob},
		},
	}

	reconcileCtx := controllerutils.ReconcileContext[*aimv1alpha1.AIMServiceTemplate]{
		Object: template,
	}

	result := r.PlanResources(ctx, reconcileCtx, obs)

	// Should NOT have acquired a slot (job already exists)
	if GetGlobalSemaphore().ActiveCount() != 0 {
		t.Errorf("expected 0 active slots when job exists, got %d", GetGlobalSemaphore().ActiveCount())
	}

	// Should NOT plan a new job
	if len(result.GetToApply()) != 0 {
		t.Errorf("expected 0 objects to apply when job exists, got %d", len(result.GetToApply()))
	}
}

func TestPlanResources_SameTemplateMultipleReconciles(t *testing.T) {
	resetGlobalSemaphore()

	r := &ServiceTemplateReconciler{}
	ctx := context.Background()

	// Same template reconciled multiple times concurrently
	// This simulates the race condition where multiple reconciliations
	// are triggered for the same template (e.g., from different events)
	numReconciles := 10
	var wg sync.WaitGroup
	jobsPlanned := make(chan int, numReconciles)

	template := makeTestTemplate("same-template", "default")
	model := makeTestModel()

	for i := 0; i < numReconciles; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			obs := ServiceTemplateObservation{
				ServiceTemplateFetchResult: ServiceTemplateFetchResult{
					template:     template,
					model:        controllerutils.FetchResult[*aimv1alpha1.AIMModel]{Value: model},
					discoveryJob: controllerutils.FetchResult[*batchv1.Job]{}, // No existing job
				},
			}

			reconcileCtx := controllerutils.ReconcileContext[*aimv1alpha1.AIMServiceTemplate]{
				Object: template,
			}

			result := r.PlanResources(ctx, reconcileCtx, obs)
			if len(result.GetToApply()) > 0 {
				jobsPlanned <- 1
			} else {
				jobsPlanned <- 0
			}
		}()
	}

	wg.Wait()
	close(jobsPlanned)

	total := 0
	for planned := range jobsPlanned {
		total += planned
	}

	// Only ONE reconcile should plan a job (the one that first acquires the semaphore).
	// Others should see the slot is already held and skip job creation.
	if total != 1 {
		t.Errorf("expected exactly 1 reconcile to plan a job (same template), got %d", total)
	}

	// Semaphore should have 1 slot held
	if GetGlobalSemaphore().ActiveCount() != 1 {
		t.Errorf("expected 1 active slot for same template, got %d", GetGlobalSemaphore().ActiveCount())
	}
}

func TestPlanResources_DifferentNamespacesSameTemplateName(t *testing.T) {
	resetGlobalSemaphore()

	r := &ServiceTemplateReconciler{}
	ctx := context.Background()

	// Create templates with the same name but different namespaces
	// These should be tracked separately in the semaphore
	template1 := makeTestTemplate("my-template", "namespace-1")
	template2 := makeTestTemplate("my-template", "namespace-2")
	model := makeTestModel()

	obs1 := ServiceTemplateObservation{
		ServiceTemplateFetchResult: ServiceTemplateFetchResult{
			template:     template1,
			model:        controllerutils.FetchResult[*aimv1alpha1.AIMModel]{Value: model},
			discoveryJob: controllerutils.FetchResult[*batchv1.Job]{},
		},
	}

	obs2 := ServiceTemplateObservation{
		ServiceTemplateFetchResult: ServiceTemplateFetchResult{
			template:     template2,
			model:        controllerutils.FetchResult[*aimv1alpha1.AIMModel]{Value: model},
			discoveryJob: controllerutils.FetchResult[*batchv1.Job]{},
		},
	}

	reconcileCtx1 := controllerutils.ReconcileContext[*aimv1alpha1.AIMServiceTemplate]{
		Object: template1,
	}
	reconcileCtx2 := controllerutils.ReconcileContext[*aimv1alpha1.AIMServiceTemplate]{
		Object: template2,
	}

	result1 := r.PlanResources(ctx, reconcileCtx1, obs1)
	result2 := r.PlanResources(ctx, reconcileCtx2, obs2)

	// Both should have planned a job (different namespaces = different keys)
	if len(result1.GetToApply()) != 1 {
		t.Errorf("expected 1 object to apply for template1, got %d", len(result1.GetToApply()))
	}
	if len(result2.GetToApply()) != 1 {
		t.Errorf("expected 1 object to apply for template2, got %d", len(result2.GetToApply()))
	}

	// Semaphore should have 2 slots held
	if GetGlobalSemaphore().ActiveCount() != 2 {
		t.Errorf("expected 2 active slots for different namespaces, got %d", GetGlobalSemaphore().ActiveCount())
	}

	// Verify both keys are held
	key1 := JobKey(template1.Namespace, template1.Name)
	key2 := JobKey(template2.Namespace, template2.Name)
	if !GetGlobalSemaphore().IsHeld(key1) {
		t.Errorf("expected semaphore slot to be held for key %s", key1)
	}
	if !GetGlobalSemaphore().IsHeld(key2) {
		t.Errorf("expected semaphore slot to be held for key %s", key2)
	}
}
