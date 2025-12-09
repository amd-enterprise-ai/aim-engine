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

package controllerutils

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PlanResult contains the desired state changes from the Plan phase.
type PlanResult struct {
	// Apply are objects to create or update via Server-Side Apply
	Apply []client.Object

	// Delete are objects to delete
	Delete []client.Object
}

// DomainReconciler is implemented by domain-specific logic for a CRD.
type DomainReconciler[T ObjectWithStatus[S], S StatusWithConditions, F any, Obs any] interface {
	// Fetch can hit the API via client and returns the fetched objects.
	Fetch(ctx context.Context, c client.Client, obj T) (F, error)

	// Observe interprets the fetched objects into a meaningful observation.
	Observe(ctx context.Context, obj T, fetched F) (Obs, error)

	// Plan must be pure: no client calls, just derive desired state changes based on the object + observed state.
	Plan(ctx context.Context, obj T, obs Obs) (PlanResult, error)

	// Project mutates obj.Status via the ConditionManager based on obs/plan/errors.
	Project(status S, cm *ConditionManager, obs Obs)
}

// Pipeline wires a domain reconciler with controller-runtime utilities.
type Pipeline[T ObjectWithStatus[S], S StatusWithConditions, F any, Obs any] struct {
	Client       client.Client
	StatusClient client.StatusWriter // usually mgr.GetClient().Status()
	Recorder     record.EventRecorder
	FieldOwner   string
	Reconciler   DomainReconciler[T, S, F, Obs]
	Scheme       *runtime.Scheme
}

// Run executes the standard Observe → Plan → Apply → Project → Events → Status flow.
// It does NOT handle:
// - fetching the object from the API
// - deletion / finalizers
// Those remain in the controller's Reconcile.
func (p *Pipeline[T, S, F, Obs]) Run(ctx context.Context, obj T) error {
	// 1) Get current status pointer (will be mutated)
	status := obj.GetStatus() // S, e.g. *AIMServiceStatus

	// 2) Deep copy the entire object to capture old status for comparison
	oldObj, ok := obj.DeepCopyObject().(T)
	if !ok {
		return fmt.Errorf("DeepCopyObject returned unexpected type, expected %T", obj)
	}
	oldStatus := oldObj.GetStatus()
	oldConditions := append([]metav1.Condition(nil), status.GetConditions()...)

	// Condition manager from existing conditions
	cm := NewConditionManager(oldConditions)

	// Fetch phase - get all resources needed for observation
	// Returns errors ONLY for transient infrastructure issues (network, API server failures).
	// Semantic errors (NotFound, Invalid) MUST be included in fetch result and
	// handled in Observe phase to update status appropriately.
	// Infrastructure errors cause immediate retry without status update.
	fetched, fetchError := p.Reconciler.Fetch(ctx, p.Client, obj)
	if fetchError != nil {
		// Infrastructure error - return for exponential backoff.
		// Status is NOT updated to avoid noise from transient network/API issues.
		// Project phase does NOT run - this is intentional for infrastructure failures.
		return fmt.Errorf("fetch failed: %w", fetchError)
	}

	// Observe phase - interpret fetched resources into domain observations
	// Returns errors ONLY for unexpected failures that should trigger retry.
	// Semantic issues (NotFound, validation errors) MUST be reflected in observations,
	// not returned as errors, so they can be surfaced in status via Project phase.
	obs, obsErr := p.Reconciler.Observe(ctx, obj, fetched)
	if obsErr != nil {
		// Unexpected observation error - return for retry without status update.
		// Project phase does NOT run - this is intentional for infrastructure failures.
		return fmt.Errorf("observe failed: %w", obsErr)
	}

	// Plan phase - derive desired state changes based on observations
	// Should be pure - no client calls, just logic based on current state.
	// Returns errors ONLY for unexpected failures (e.g., internal logic errors).
	planResult, planErr := p.Reconciler.Plan(ctx, obj, obs)
	if planErr != nil {
		// Planning error - return for retry without status update.
		// Project phase does NOT run - this is intentional for infrastructure failures.
		return fmt.Errorf("plan failed: %w", planErr)
	}

	// Delete phase - delete objects before applying new state
	// Continue processing all deletes and aggregate errors to avoid silent failures
	var deleteErrs []error
	for _, objToDelete := range planResult.Delete {
		if err := p.Client.Delete(ctx, objToDelete); client.IgnoreNotFound(err) != nil {
			// Deletion failed - collect error and continue to process remaining deletes
			gvk := objToDelete.GetObjectKind().GroupVersionKind()
			key := client.ObjectKeyFromObject(objToDelete)
			deleteErrs = append(deleteErrs, fmt.Errorf("delete failed for %s %s/%s: %w", gvk.Kind, key.Namespace, key.Name, err))
		}
	}
	// If any deletes failed, return aggregated error for retry
	if len(deleteErrs) > 0 {
		return fmt.Errorf("delete phase failed: %w", errors.Join(deleteErrs...))
	}

	// Apply phase - use Server-Side Apply to create/update desired objects
	if len(planResult.Apply) > 0 {
		if err := ApplyDesiredState(
			ctx,
			p.Client,
			p.FieldOwner,
			p.Scheme,
			planResult.Apply,
		); err != nil {
			// Apply failed - return for retry
			// ApplyDesiredState already wraps with context
			return fmt.Errorf("apply failed: %w", err)
		}
	}

	// Project phase - updates status based on observations and planned changes
	// This phase runs ONLY after successful Fetch/Observe/Plan phases.
	// Infrastructure failures in earlier phases cause immediate retry without status update.
	// Semantic errors MUST be reflected in observations (not returned as errors) so they
	// can be surfaced here via conditions. Domain reconciler updates conditions to reflect
	// observed state and any semantic issues encountered.
	p.Reconciler.Project(status, cm, obs)

	// Update conditions from manager
	status.SetConditions(cm.Conditions())

	// Emit events and logs based on condition transitions and recurring configs
	transitions := DiffConditionTransitions(
		oldConditions,
		status.GetConditions(),
	)

	// Emit events for transitions (EventOnTransition mode)
	EmitConditionTransitions(p.Recorder, obj, transitions, cm)

	// Emit recurring events (EventAlways mode)
	EmitRecurringEvents(p.Recorder, obj, cm)

	// Emit logs for transitions (LogOnTransition mode)
	EmitConditionLogs(ctx, transitions, cm)

	// Emit recurring logs (LogAlways mode)
	EmitRecurringLogs(ctx, cm)

	// Update status only if changed (compare with deep copied old status)
	if !equality.Semantic.DeepEqual(oldStatus, status) {
		if err := p.StatusClient.Update(ctx, obj); err != nil {
			return fmt.Errorf("status update failed: %w", err)
		}
	}

	return nil
}

// StatusWithConditions is a constraint for status types that have Conditions.
type StatusWithConditions interface {
	GetConditions() []metav1.Condition
	SetConditions([]metav1.Condition)
	SetStatus(string)
}

// ObjectWithStatus is a constraint for objects that have a Status field with conditions.
type ObjectWithStatus[S StatusWithConditions] interface {
	runtime.Object
	client.Object
	GetStatus() S
}

// ApplyDesiredState applies the desired set of objects via Server-Side Apply (SSA).
// Objects are applied in deterministic order: by GVK, then namespace, then name.
func ApplyDesiredState(
	ctx context.Context,
	k8sClient client.Client,
	fieldOwner string,
	scheme *runtime.Scheme,
	desired []client.Object,
) error {
	if len(desired) == 0 {
		return nil
	}

	// Ensure all objects have GVK set
	for _, obj := range desired {
		if err := stampGVK(obj, scheme); err != nil {
			return fmt.Errorf("failed to stamp GVK: %w", err)
		}
	}

	// Sort deterministically
	sorted := sortObjects(desired)

	// Apply each object via SSA
	for _, obj := range sorted {
		gvk := obj.GetObjectKind().GroupVersionKind()
		key := client.ObjectKeyFromObject(obj)

		// Use Server-Side Apply (SSA) to create/update desired objects.
		// The FieldOwner parameter ensures this controller owns only the fields it manages.
		// SSA will automatically handle conflicts - if another manager has changed fields,
		// this apply will only update fields owned by this controller's field manager.
		// This allows proper cooperation with kubectl and other controllers.
		if err := k8sClient.Patch(
			ctx,
			obj,
			client.Apply,
			client.FieldOwner(fieldOwner),
		); err != nil {
			return fmt.Errorf("failed to apply %s %s: %w", gvk.Kind, key.Name, err)
		}
	}

	return nil
}

// stampGVK ensures the object has its GVK set from the scheme
func stampGVK(obj client.Object, scheme *runtime.Scheme) error {
	gvks, _, err := scheme.ObjectKinds(obj)
	if err != nil {
		return fmt.Errorf("cannot find GVK for %T: %w", obj, err)
	}
	if len(gvks) == 0 {
		return fmt.Errorf("no GVK registered for %T", obj)
	}
	obj.GetObjectKind().SetGroupVersionKind(gvks[0])
	return nil
}

// sortObjects returns objects sorted by GVK, namespace, name for determinism
func sortObjects(objects []client.Object) []client.Object {
	sorted := make([]client.Object, len(objects))
	copy(sorted, objects)

	sort.Slice(sorted, func(i, j int) bool {
		objI := sorted[i]
		objJ := sorted[j]

		gvkI := objI.GetObjectKind().GroupVersionKind().String()
		gvkJ := objJ.GetObjectKind().GroupVersionKind().String()

		if gvkI != gvkJ {
			return gvkI < gvkJ
		}

		nsI := objI.GetNamespace()
		nsJ := objJ.GetNamespace()

		if nsI != nsJ {
			return nsI < nsJ
		}

		return objI.GetName() < objJ.GetName()
	})

	return sorted
}
