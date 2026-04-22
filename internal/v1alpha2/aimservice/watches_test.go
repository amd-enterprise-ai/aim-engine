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

package aimservice

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"

	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

// TestProfileRelevantChangePredicate_StatusTransition is the baseline case:
// readiness flip must enqueue the service even if Generation is unchanged.
func TestProfileRelevantChangePredicate_StatusTransition(t *testing.T) {
	pred := profileRelevantChangePredicate()

	oldP := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"},
		Status:     aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusPending},
	}
	newP := oldP.DeepCopy()
	newP.Status.Status = constants.AIMStatusReady

	if !pred.Update(event.UpdateEvent{ObjectOld: oldP, ObjectNew: newP}) {
		t.Error("expected status transition Pending->Ready to fire the predicate")
	}
}

// TestProfileRelevantChangePredicate_ResourcesChange covers the regression we
// just fixed: when the profile controller recomputes Status.Resources without
// a Generation bump (e.g. node-label-driven accelerator swap), the AIMService
// must still pick up the new container resources.
func TestProfileRelevantChangePredicate_ResourcesChange(t *testing.T) {
	pred := profileRelevantChangePredicate()

	oldP := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"},
		Status: aimv1alpha2.AIMProfileStatus{
			Status: constants.AIMStatusReady,
			Resources: &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"),
				},
			},
		},
	}
	newP := oldP.DeepCopy()
	newP.Status.Resources.Requests[corev1.ResourceCPU] = resource.MustParse("8")

	if !pred.Update(event.UpdateEvent{ObjectOld: oldP, ObjectNew: newP}) {
		t.Error("expected Status.Resources change to fire the predicate")
	}
}

// TestProfileRelevantChangePredicate_AffinityChange is the companion case:
// the resolved node affinity can change without a Generation bump when node
// labels shift. The AIMService must re-reconcile so the ISVC's affinity
// stays aligned.
func TestProfileRelevantChangePredicate_AffinityChange(t *testing.T) {
	pred := profileRelevantChangePredicate()

	aff := func(label string) *corev1.NodeAffinity {
		return &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{{
					MatchExpressions: []corev1.NodeSelectorRequirement{{
						Key: label, Operator: corev1.NodeSelectorOpExists,
					}},
				}},
			},
		}
	}

	oldP := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"},
		Status: aimv1alpha2.AIMProfileStatus{
			Status:               constants.AIMStatusReady,
			ResolvedNodeAffinity: aff("feature.node.kubernetes.io/aim-accelerator.MI300X"),
		},
	}
	newP := oldP.DeepCopy()
	newP.Status.ResolvedNodeAffinity = aff("feature.node.kubernetes.io/aim-accelerator.MI325X")

	if !pred.Update(event.UpdateEvent{ObjectOld: oldP, ObjectNew: newP}) {
		t.Error("expected Status.ResolvedNodeAffinity change to fire the predicate")
	}
}

// TestProfileRelevantChangePredicate_NoiseFilteredOut ensures cosmetic status
// writes don't flood the AIMService with reconciles. This is what stops the
// predicate from being the equivalent of "always fire".
func TestProfileRelevantChangePredicate_NoiseFilteredOut(t *testing.T) {
	pred := profileRelevantChangePredicate()

	oldP := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"},
		Status: aimv1alpha2.AIMProfileStatus{
			Status:             constants.AIMStatusReady,
			ObservedGeneration: 5,
		},
	}
	newP := oldP.DeepCopy()

	if pred.Update(event.UpdateEvent{ObjectOld: oldP, ObjectNew: newP}) {
		t.Error("expected no-op status update to be filtered out")
	}
}
