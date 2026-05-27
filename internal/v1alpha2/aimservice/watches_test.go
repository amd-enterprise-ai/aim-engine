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
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
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

// TestProfileRelevantChangePredicate_ProvenanceLabelsFire covers the
// PR 3 addition: a profile whose role label flips from base to deployable
// (a base profile becoming materialised in iteration 2) must trigger a
// re-reconcile so selector-driven services pick it up.
func TestProfileRelevantChangePredicate_ProvenanceLabelsFire(t *testing.T) {
	pred := profileRelevantChangePredicate()

	oldP := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns", Labels: map[string]string{
			constants.LabelKeyProfileRole: constants.LabelValueProfileRoleBase,
		}},
		Status: aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady},
	}
	newP := oldP.DeepCopy()
	newP.Labels[constants.LabelKeyProfileRole] = constants.LabelValueProfileRoleDeployable

	if !pred.Update(event.UpdateEvent{ObjectOld: oldP, ObjectNew: newP}) {
		t.Error("expected role label flip to fire the predicate")
	}
}

// newWatchTestScheme builds a scheme with both AIM versions registered for
// the watch fan-out tests.
func newWatchTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := aimv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("register v1alpha1: %v", err)
	}
	if err := aimv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatalf("register v1alpha2: %v", err)
	}
	return scheme
}

// newWatchTestClient is a fake client with all four AIMService field
// indexes registered so the watch mappers can exercise their lookup paths.
func newWatchTestClient(t *testing.T, scheme *runtime.Scheme, objs ...client.Object) client.Client {
	t.Helper()
	return fakeclient.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithIndex(&aimv1alpha1.AIMService{}, aimv1alpha1.AIMServiceProfileIndexKey, func(obj client.Object) []string {
			svc, ok := obj.(*aimv1alpha1.AIMService)
			if !ok || svc.Spec.Profile == nil || svc.Spec.Profile.Name == "" {
				return nil
			}
			return []string{svc.Spec.Profile.Name}
		}).
		WithIndex(&aimv1alpha1.AIMService{}, ServiceModelNameIndex, func(obj client.Object) []string {
			svc, ok := obj.(*aimv1alpha1.AIMService)
			if !ok || svc.Spec.Model == nil || svc.Spec.Model.Name == nil || *svc.Spec.Model.Name == "" {
				return nil
			}
			return []string{*svc.Spec.Model.Name}
		}).
		WithIndex(&aimv1alpha1.AIMService{}, ServiceSelectorModelRefIndex, func(obj client.Object) []string {
			svc, ok := obj.(*aimv1alpha1.AIMService)
			if !ok || svc.Spec.Profile == nil || svc.Spec.Profile.Selector == nil ||
				svc.Spec.Profile.Selector.ModelRef == nil || svc.Spec.Profile.Selector.ModelRef.Name == "" {
				return nil
			}
			return []string{svc.Spec.Profile.Selector.ModelRef.Name}
		}).
		WithIndex(&aimv1alpha1.AIMService{}, ServiceSelectorAimIdIndex, func(obj client.Object) []string {
			svc, ok := obj.(*aimv1alpha1.AIMService)
			if !ok || svc.Spec.Profile == nil || svc.Spec.Profile.Selector == nil ||
				svc.Spec.Profile.Selector.AimId == "" {
				return nil
			}
			return []string{svc.Spec.Profile.Selector.AimId}
		}).
		Build()
}

// TestFindServicesForProfile_FansOutByNameModelAndAimId exercises the three
// lookup paths a single AIMProfile event takes:
//  1. profile name (services authored as spec.profile.name=foo)
//  2. source-model label (services authored as spec.model.name=foo OR
//     spec.profile.selector.modelRef.name=foo)
//  3. spec.aimId (services authored as spec.profile.selector.aimId=foo)
//
// All three must enqueue together so a single event can't leave any
// AIMService stale.
func TestFindServicesForProfile_FansOutByNameModelAndAimId(t *testing.T) {
	scheme := newWatchTestScheme(t)

	byName := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-by-name", Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: "p"},
		},
	}
	byModelShortcut := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-by-model", Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Model: &aimv1alpha1.AIMServiceModel{Name: ptr.To("source-model")},
		},
	}
	bySelectorModelRef := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-by-modelref", Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{
				Selector: &aimv1alpha1.ProfileSelector{
					ModelRef: &aimv1alpha1.ProfileSelectorModelRef{Name: "source-model"},
				},
			},
		},
	}
	bySelectorAimId := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-by-aimid", Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{
				Selector: &aimv1alpha1.ProfileSelector{AimId: "aim/one"},
			},
		},
	}
	// Service in a different namespace must NOT be enqueued — namespace
	// AIMProfile events fan out within the namespace only.
	otherNamespace := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-other-ns", Namespace: "other"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: "p"},
		},
	}
	c := newWatchTestClient(t, scheme, byName, byModelShortcut, bySelectorModelRef, bySelectorAimId, otherNamespace)

	profile := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "p",
			Namespace: "ns",
			Labels: map[string]string{
				constants.LabelKeySourceModel: "source-model",
			},
		},
		Spec: aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: aimv1alpha2.AIMProfileSpecCommon{
			AimId: "aim/one",
			Image: "img",
		}},
	}

	requests := findServicesForProfile(c)(context.Background(), profile)
	got := map[string]struct{}{}
	for _, r := range requests {
		got[r.String()] = struct{}{}
	}

	expected := []types.NamespacedName{
		{Name: "svc-by-name", Namespace: "ns"},
		{Name: "svc-by-model", Namespace: "ns"},
		{Name: "svc-by-modelref", Namespace: "ns"},
		{Name: "svc-by-aimid", Namespace: "ns"},
	}
	for _, want := range expected {
		if _, ok := got[want.String()]; !ok {
			t.Errorf("missing enqueue for %s", want)
		}
	}
	if _, ok := got["other/svc-other-ns"]; ok {
		t.Errorf("namespace fan-out leaked into other namespace")
	}
}

// TestFindServicesForClusterProfile_SpansAllNamespaces asserts that a
// cluster profile event reaches services in every namespace via the field
// indexes (no InNamespace clause).
func TestFindServicesForClusterProfile_SpansAllNamespaces(t *testing.T) {
	scheme := newWatchTestScheme(t)

	nsAlpha := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-alpha", Namespace: "alpha"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: "cluster-p"},
		},
	}
	nsBeta := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-beta", Namespace: "beta"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: "cluster-p"},
		},
	}
	c := newWatchTestClient(t, scheme, nsAlpha, nsBeta)

	clusterProfile := &aimv1alpha2.AIMClusterProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-p"},
		Spec: aimv1alpha2.AIMClusterProfileSpec{AIMProfileSpecCommon: aimv1alpha2.AIMProfileSpecCommon{
			AimId: "aim/global",
			Image: "img",
		}},
	}

	requests := findServicesForClusterProfile(c)(context.Background(), clusterProfile)
	if len(requests) != 2 {
		t.Fatalf("expected 2 enqueues (alpha + beta), got %d", len(requests))
	}
}

// TestFindServicesForModel_FansOutByShortcutAndSelector covers the new
// AIMModel watch: a model change enqueues services authored via
// spec.model.name=foo AND services authored via
// spec.profile.selector.modelRef.name=foo, since both styles need the
// downstream profile set to be re-evaluated.
func TestFindServicesForModel_FansOutByShortcutAndSelector(t *testing.T) {
	scheme := newWatchTestScheme(t)

	byShortcut := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-shortcut", Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Model: &aimv1alpha1.AIMServiceModel{Name: ptr.To("my-model")},
		},
	}
	byExplicit := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-explicit", Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{
				Selector: &aimv1alpha1.ProfileSelector{
					ModelRef: &aimv1alpha1.ProfileSelectorModelRef{Name: "my-model"},
				},
			},
		},
	}
	unrelated := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-unrelated", Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Model: &aimv1alpha1.AIMServiceModel{Name: ptr.To("other-model")},
		},
	}
	c := newWatchTestClient(t, scheme, byShortcut, byExplicit, unrelated)

	model := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "my-model", Namespace: "ns"},
	}
	requests := findServicesForModel(c)(context.Background(), model)
	got := map[string]struct{}{}
	for _, r := range requests {
		got[r.Name] = struct{}{}
	}

	if _, ok := got["svc-shortcut"]; !ok {
		t.Errorf("missing enqueue for spec.model.name shortcut service")
	}
	if _, ok := got["svc-explicit"]; !ok {
		t.Errorf("missing enqueue for selector.modelRef.name service")
	}
	if _, ok := got["svc-unrelated"]; ok {
		t.Errorf("unrelated service must not be enqueued")
	}
}
