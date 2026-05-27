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

package controller

import (
	"context"
	"sort"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimmodel"
)

func TestDirectModelRequest_UsesOwnerAnnotations(t *testing.T) {
	t.Parallel()

	child := &aimv1alpha2.AIMProfileSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "child",
			Namespace: "team-a",
			Annotations: map[string]string{
				aimmodel.AnnotationModelName():      "parent-model",
				aimmodel.AnnotationModelNamespace(): "team-a",
			},
		},
	}

	requests := directModelRequest(child)
	if len(requests) != 1 {
		t.Fatalf("len(requests) = %d, want 1", len(requests))
	}
	if requests[0].Name != "parent-model" || requests[0].Namespace != "team-a" {
		t.Fatalf("request = %#v, want team-a/parent-model", requests[0])
	}
}

func TestCleanupManagedProfiles_DeletesIndexedChildren(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := aimv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}

	managed := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "managed",
			Namespace:   "team-a",
			Annotations: map[string]string{aimmodel.AnnotationModelUID(): "model-uid"},
		},
	}
	external := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "external",
			Namespace:   "team-a",
			Annotations: map[string]string{aimmodel.AnnotationModelUID(): "other-model"},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(managed, external).
		WithIndex(&aimv1alpha2.AIMProfile{}, aimmodel.ManagedModelUIDIndexKey, func(obj client.Object) []string {
			return []string{obj.GetAnnotations()[aimmodel.AnnotationModelUID()]}
		}).
		Build()

	reconciler := &AIMModelReconciler{Client: fakeClient}
	if err := reconciler.cleanupManagedProfiles(context.Background(), "team-a", "model-uid"); err != nil {
		t.Fatalf("cleanupManagedProfiles() error = %v", err)
	}

	if err := fakeClient.Get(context.Background(), client.ObjectKey{Name: "managed", Namespace: "team-a"}, &aimv1alpha2.AIMProfile{}); err == nil {
		t.Fatal("managed profile still exists after cleanup")
	}
	if err := fakeClient.Get(context.Background(), client.ObjectKey{Name: "external", Namespace: "team-a"}, &aimv1alpha2.AIMProfile{}); err != nil {
		t.Fatalf("external profile lookup error = %v, want profile to remain", err)
	}
}

func TestCleanupDiscoveryCache_DeletesNamespaceModelCache(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("corev1.AddToScheme() error = %v", err)
	}

	cacheName, err := aimmodel.DiscoveryCacheName("namespace-model")
	if err != nil {
		t.Fatalf("DiscoveryCacheName() error = %v", err)
	}
	cache := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: cacheName, Namespace: "team-a"},
	}
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cache).
		Build()

	reconciler := &AIMModelReconciler{Client: fakeClient}
	if err := reconciler.cleanupDiscoveryCache(context.Background(), "team-a", "namespace-model"); err != nil {
		t.Fatalf("cleanupDiscoveryCache() error = %v", err)
	}
	if err := fakeClient.Get(context.Background(), client.ObjectKeyFromObject(cache), &corev1.ConfigMap{}); err == nil {
		t.Fatal("discovery cache still exists after cleanup")
	}
}

// TestFindModelsForNodeChange_QueuesEveryImageBackedModel verifies that node
// label / capacity changes fan out to every image-backed AIMModel regardless
// of profile shape. The native discovery pipeline always runs for image-backed
// specs, so any change in cluster accelerator labels can flip the supported
// profile set on any of them.
func TestFindModelsForNodeChange_QueuesEveryImageBackedModel(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := aimv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}

	imageOnly := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "image-only", Namespace: "team-a"},
		Spec:       aimv1alpha1.AIMModelSpec{Image: "quay.io/amd/aim-image-only:0.10.0"},
	}
	imageWithProfileCopy := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "image-with-profile-copy", Namespace: "team-a"},
		Spec: aimv1alpha1.AIMModelSpec{
			Image: "quay.io/amd/aim-image-pc:0.10.0",
			ProfileCopy: &aimv1alpha1.AIMProfileSetSpec{
				Selector: aimv1alpha1.ProfileSelector{AimId: "amd/example"},
			},
		},
	}
	// No image → not affected by node changes (legacy aimId-matching path
	// does not look at node labels).
	fineTuneOnly := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "fine-tune-only", Namespace: "team-a"},
		Spec: aimv1alpha1.AIMModelSpec{
			ProfileCopy: &aimv1alpha1.AIMProfileSetSpec{
				Selector: aimv1alpha1.ProfileSelector{AimId: "amd/example"},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(imageOnly, imageWithProfileCopy, fineTuneOnly).
		Build()

	reconciler := &AIMModelReconciler{Client: fakeClient}
	requests := reconciler.findModelsForNodeChange(context.Background(), &corev1.Node{})

	got := make([]string, 0, len(requests))
	for _, req := range requests {
		got = append(got, req.Namespace+"/"+req.Name)
	}
	sort.Strings(got)

	want := []string{"team-a/image-only", "team-a/image-with-profile-copy"}
	if len(got) != len(want) {
		t.Fatalf("len(requests) = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("requests[%d] = %q, want %q (all=%v)", i, got[i], want[i], got)
		}
	}
}

func TestFindModelsForServiceTemplate_FanOutsByAimId(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := aimv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("v1alpha1.AddToScheme() error = %v", err)
	}
	if err := aimv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatalf("v1alpha2.AddToScheme() error = %v", err)
	}

	// Three models: two share the same aimId as the template, one doesn't.
	matchA := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "ft-a", Namespace: "team-a"},
		Spec:       aimv1alpha1.AIMModelSpec{AimId: "amd/qwen3-32b"},
	}
	matchB := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "ft-b", Namespace: "team-b"},
		Spec:       aimv1alpha1.AIMModelSpec{AimId: "amd/qwen3-32b"},
	}
	noMatch := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "team-a"},
		Spec:       aimv1alpha1.AIMModelSpec{AimId: "amd/llama-70b"},
	}
	// Owning model with no aimId: should still be enqueued via spec.modelName
	// path so externally-created templates reach the owning model.
	owner := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen-owner", Namespace: "team-a"},
		Spec:       aimv1alpha1.AIMModelSpec{Image: "quay.io/amd/qwen3:0.10.0"},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(matchA, matchB, noMatch, owner).
		WithIndex(&aimv1alpha2.AIMModel{}, aimv1alpha1.ModelAimIdIndexKey, func(obj client.Object) []string {
			m, ok := obj.(*aimv1alpha2.AIMModel)
			if !ok || m.Spec.AimId == "" {
				return nil
			}
			return []string{m.Spec.AimId}
		}).
		Build()

	reconciler := &AIMModelReconciler{Client: fakeClient}

	template := &aimv1alpha1.AIMServiceTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen-template", Namespace: "team-a"},
		Spec: aimv1alpha1.AIMServiceTemplateSpec{
			AIMServiceTemplateSpecCommon: aimv1alpha1.AIMServiceTemplateSpecCommon{
				ModelName: "qwen-owner",
				AimId:     "amd/qwen3-32b",
			},
		},
	}
	requests := reconciler.findModelsForServiceTemplate(context.Background(), template)

	got := make([]string, 0, len(requests))
	for _, req := range requests {
		got = append(got, req.Namespace+"/"+req.Name)
	}
	sort.Strings(got)

	want := []string{"team-a/ft-a", "team-a/qwen-owner", "team-b/ft-b"}
	if len(got) != len(want) {
		t.Fatalf("len(requests) = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("requests[%d] = %q, want %q (all=%v)", i, got[i], want[i], got)
		}
	}
}

func TestFindModelsForClusterServiceTemplate_FanOutsByAimId(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := aimv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("v1alpha1.AddToScheme() error = %v", err)
	}
	if err := aimv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatalf("v1alpha2.AddToScheme() error = %v", err)
	}

	matchA := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "ft-a", Namespace: "team-a"},
		Spec:       aimv1alpha1.AIMModelSpec{AimId: "amd/qwen3-32b"},
	}
	noMatch := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "team-b"},
		Spec:       aimv1alpha1.AIMModelSpec{AimId: "amd/llama-70b"},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(matchA, noMatch).
		WithIndex(&aimv1alpha2.AIMModel{}, aimv1alpha1.ModelAimIdIndexKey, func(obj client.Object) []string {
			m, ok := obj.(*aimv1alpha2.AIMModel)
			if !ok || m.Spec.AimId == "" {
				return nil
			}
			return []string{m.Spec.AimId}
		}).
		Build()

	reconciler := &AIMModelReconciler{Client: fakeClient}

	template := &aimv1alpha1.AIMClusterServiceTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen-cluster-template"},
		Spec: aimv1alpha1.AIMClusterServiceTemplateSpec{
			AIMServiceTemplateSpecCommon: aimv1alpha1.AIMServiceTemplateSpecCommon{
				AimId: "amd/qwen3-32b",
			},
		},
	}
	requests := reconciler.findModelsForClusterServiceTemplate(context.Background(), template)

	if len(requests) != 1 {
		t.Fatalf("len(requests) = %d, want 1 (%v)", len(requests), requests)
	}
	if requests[0].Namespace != "team-a" || requests[0].Name != "ft-a" {
		t.Fatalf("request = %#v, want team-a/ft-a", requests[0])
	}
}
