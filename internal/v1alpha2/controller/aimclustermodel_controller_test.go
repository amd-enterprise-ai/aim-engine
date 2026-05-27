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
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimmodel"
)

func TestCleanupDiscoveryCache_DeletesExpectedConfigMap(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}

	cache := &corev1.ConfigMap{}
	cacheName, err := aimmodel.DiscoveryCacheName("cluster-model")
	if err != nil {
		t.Fatalf("DiscoveryCacheName() error = %v", err)
	}
	cache.Name = cacheName
	cache.Namespace = constants.GetOperatorNamespace()
	other := &corev1.ConfigMap{}
	other.Name = "other"
	other.Namespace = cache.Namespace

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cache, other).Build()
	reconciler := &AIMClusterModelReconciler{Client: fakeClient}

	if err := reconciler.cleanupDiscoveryCache(context.Background(), "cluster-model"); err != nil {
		t.Fatalf("cleanupDiscoveryCache() error = %v", err)
	}
	if err := fakeClient.Get(context.Background(), client.ObjectKeyFromObject(cache), &corev1.ConfigMap{}); err == nil {
		t.Fatal("discovery cache still exists after cleanup")
	}
	if err := fakeClient.Get(context.Background(), client.ObjectKeyFromObject(other), &corev1.ConfigMap{}); err != nil {
		t.Fatalf("other configmap lookup error = %v, want object to remain", err)
	}
}

// TestFindClusterModelsForNodeChange_QueuesEveryImageBackedModel verifies
// that node fleet changes fan out to every image-backed AIMClusterModel
// regardless of profile shape — image-backed specs always run native
// discovery, and node accelerator labels are part of its supported-profile
// filter.
func TestFindClusterModelsForNodeChange_QueuesEveryImageBackedModel(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := aimv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}

	imageOnly := &aimv1alpha2.AIMClusterModel{
		ObjectMeta: metav1.ObjectMeta{Name: "image-only"},
		Spec:       aimv1alpha1.AIMModelSpec{Image: "quay.io/amd/aim-image-only:0.10.0"},
	}
	imageWithProfileCopy := &aimv1alpha2.AIMClusterModel{
		ObjectMeta: metav1.ObjectMeta{Name: "image-with-profile-copy"},
		Spec: aimv1alpha1.AIMModelSpec{
			Image: "quay.io/amd/aim-image-pc:0.10.0",
			ProfileCopy: &aimv1alpha1.AIMProfileSetSpec{
				Selector: aimv1alpha1.ProfileSelector{AimId: "amd/example"},
			},
		},
	}
	fineTuneOnly := &aimv1alpha2.AIMClusterModel{
		ObjectMeta: metav1.ObjectMeta{Name: "fine-tune-only"},
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

	reconciler := &AIMClusterModelReconciler{Client: fakeClient}
	requests := reconciler.findClusterModelsForNodeChange(context.Background(), &corev1.Node{})

	got := make([]string, 0, len(requests))
	for _, req := range requests {
		got = append(got, req.Name)
	}
	sort.Strings(got)

	want := []string{"image-only", "image-with-profile-copy"}
	if len(got) != len(want) {
		t.Fatalf("len(requests) = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("requests[%d] = %q, want %q (all=%v)", i, got[i], want[i], got)
		}
	}
}
