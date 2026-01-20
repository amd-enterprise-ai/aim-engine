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
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

// ApplyDesiredState applies the desired set of objects via Server-Side Apply (SSA).
// Objects are applied in deterministic order: by GVK, then namespace, then name.
// If owner is provided, owner references will be set on all objects before applying.
func ApplyDesiredState(
	ctx context.Context,
	k8sClient client.Client,
	fieldOwner string,
	scheme *runtime.Scheme,
	desired []client.Object,
	owner client.Object,
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

	// Set owner references if owner is provided
	if owner != nil {
		// Ensure owner has GVK set
		if err := stampGVK(owner, scheme); err != nil {
			return fmt.Errorf("failed to stamp GVK on owner: %w", err)
		}

		for _, obj := range desired {
			if err := setOwnerReference(owner, obj, scheme); err != nil {
				return fmt.Errorf("failed to set owner reference: %w", err)
			}
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

// setOwnerReference sets an owner reference on the object.
// The owner and object must both have GVK set before calling this function.
func setOwnerReference(owner, obj client.Object, _ *runtime.Scheme) error {
	ownerGVK := owner.GetObjectKind().GroupVersionKind()
	if ownerGVK.Empty() {
		return fmt.Errorf("owner GVK is not set")
	}

	objGVK := obj.GetObjectKind().GroupVersionKind()
	if objGVK.Empty() {
		return fmt.Errorf("object GVK is not set")
	}

	// Create owner reference
	ownerRef := metav1.OwnerReference{
		APIVersion: ownerGVK.GroupVersion().String(),
		Kind:       ownerGVK.Kind,
		Name:       owner.GetName(),
		UID:        owner.GetUID(),
		Controller: ptr.To(true),
		// BlockOwnerDeletion: ptr.To(true),  // TODO make configurable
	}

	// Get current owner references and append
	ownerRefs := obj.GetOwnerReferences()

	// Check if this owner reference already exists (by UID)
	found := false
	for i := range ownerRefs {
		if ownerRefs[i].UID == ownerRef.UID {
			// Update existing reference
			ownerRefs[i] = ownerRef
			found = true
			break
		}
	}

	if !found {
		ownerRefs = append(ownerRefs, ownerRef)
	}

	obj.SetOwnerReferences(ownerRefs)
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

func PropagateLabelsForResult(parent client.Object, planResult *PlanResult, config *aimv1alpha1.AIMRuntimeConfigCommon) {
	for _, obj := range planResult.toApply {
		PropagateLabels(parent, obj, config)
	}
	for _, obj := range planResult.toApplyWithoutOwnerRef {
		PropagateLabels(parent, obj, config)
	}
}

// ApplyControllerLabelsToResult adds controller-specific labels to all resources in the PlanResult.
// These labels are added to both the resource metadata and to Job pod templates (if applicable).
// Labels are merged with existing labels - existing labels are not overwritten.
func ApplyControllerLabelsToResult(planResult *PlanResult, labels map[string]string) {
	for _, obj := range planResult.toApply {
		applyControllerLabels(obj, labels)
	}
	for _, obj := range planResult.toApplyWithoutOwnerRef {
		applyControllerLabels(obj, labels)
	}
}

// applyControllerLabels adds the provided labels to the object's metadata.
// For Jobs, custom domain labels (not app.kubernetes.io/managed-by) are also added to the pod template spec.
// This is because pods are managed by the Job controller, not by our custom controller.
// Existing labels are preserved (not overwritten).
func applyControllerLabels(obj client.Object, labels map[string]string) {
	if len(labels) == 0 {
		return
	}

	// Get or initialize object labels
	objLabels := obj.GetLabels()
	if objLabels == nil {
		objLabels = make(map[string]string)
	}

	// Add new labels (don't overwrite existing ones)
	for key, value := range labels {
		if _, exists := objLabels[key]; !exists {
			objLabels[key] = value
		}
	}

	obj.SetLabels(objLabels)

	// Special handling for Jobs: propagate custom domain labels to PodTemplateSpec,
	// but NOT app.kubernetes.io/managed-by since pods are managed by the Job controller.
	if job, ok := obj.(*batchv1.Job); ok {
		if job.Spec.Template.Labels == nil {
			job.Spec.Template.Labels = make(map[string]string)
		}
		for key, value := range labels {
			// Skip app.kubernetes.io/managed-by for pods - they're managed by Job controller
			if key == "app.kubernetes.io/managed-by" {
				continue
			}
			// Only add if not already present in pod template
			if _, exists := job.Spec.Template.Labels[key]; !exists {
				job.Spec.Template.Labels[key] = value
			}
		}
	}
}

// PropagateLabels propagates labels from a parent resource to a child resource.
// AIM system labels (aim.eai.amd.com/*) are always propagated to maintain traceability
// across the resource hierarchy. User-defined labels are propagated based on the
// runtime config's label propagation settings.
//
// Parameters:
//   - parent: The source resource whose labels should be propagated
//   - child: The target resource that will receive the propagated labels
//   - config: The runtime config common spec containing label propagation settings
//
// The function does nothing if the parent has no labels.
// The child's existing labels are preserved and only new labels are added.
//
// Special handling for Jobs: Labels are also propagated to the PodTemplateSpec.
func PropagateLabels(parent, child client.Object, config *aimv1alpha1.AIMRuntimeConfigCommon) {
	parentLabels := parent.GetLabels()
	if len(parentLabels) == 0 {
		return
	}

	// Initialize child labels if nil
	childLabels := child.GetLabels()
	if childLabels == nil {
		childLabels = make(map[string]string)
	}

	// Collect labels to propagate
	labelsToPropagate := make(map[string]string)

	// Check if user-defined label propagation is enabled
	userPropagationEnabled := config != nil &&
		config.LabelPropagation != nil &&
		config.LabelPropagation.Enabled &&
		len(config.LabelPropagation.Match) > 0

	// Iterate through parent labels and collect ones to propagate
	for key, value := range parentLabels {
		// Skip if child already has this label
		if _, exists := childLabels[key]; exists {
			continue
		}

		// Always propagate AIM system labels for traceability across the resource hierarchy
		if strings.HasPrefix(key, constants.AimLabelDomain+"/") {
			childLabels[key] = value
			labelsToPropagate[key] = value
			continue
		}

		// Propagate labels matching user-defined patterns if enabled
		if userPropagationEnabled && matchesAnyPattern(key, config.LabelPropagation.Match) {
			childLabels[key] = value
			labelsToPropagate[key] = value
		}
	}

	child.SetLabels(childLabels)

	// Special handling for Jobs: also propagate to PodTemplateSpec
	if job, ok := child.(*batchv1.Job); ok && len(labelsToPropagate) > 0 {
		if job.Spec.Template.Labels == nil {
			job.Spec.Template.Labels = make(map[string]string)
		}
		for key, value := range labelsToPropagate {
			// Only add if not already present in pod template
			if _, exists := job.Spec.Template.Labels[key]; !exists {
				job.Spec.Template.Labels[key] = value
			}
		}
	}
}

// matchesAnyPattern checks if a label key matches any of the provided patterns.
// Patterns support wildcards using filepath.Match semantics (e.g., "org.my/*", "team-*").
func matchesAnyPattern(key string, patterns []string) bool {
	for _, pattern := range patterns {
		// filepath.Match supports * and ? wildcards
		matched, err := filepath.Match(pattern, key)
		if err != nil {
			// Invalid pattern, skip it
			continue
		}
		if matched {
			return true
		}
	}
	return false
}
