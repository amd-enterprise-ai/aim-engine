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
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
)

func TestModelStatusOnlyReadinessDoesNotFanOut(t *testing.T) {
	old := &aimv1alpha2.AIMModel{ObjectMeta: metav1.ObjectMeta{Name: "model", Generation: 1}}
	next := old.DeepCopy()
	next.Status.ManagedProfiles.Total = 10
	p := modelDefinitionPredicate()
	if p.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: next}) {
		t.Fatal("readiness inventory triggered definition consumers")
	}
	next.Spec.Image = "registry/model:2.0"
	if !p.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: next}) {
		t.Fatal("source image update ignored")
	}
}

func TestModelAndCatalogEventsReachSetsWithoutProfiles(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := aimv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	sets := []client.Object{
		&aimv1alpha2.AIMProfileSet{ObjectMeta: metav1.ObjectMeta{Name: "broad", Namespace: "team"}},
		&aimv1alpha2.AIMProfileSet{ObjectMeta: metav1.ObjectMeta{Name: "other-namespace", Namespace: "other"}},
		&aimv1alpha2.AIMProfileSet{ObjectMeta: metav1.ObjectMeta{Name: "named", Namespace: "team"}, Spec: aimv1alpha1.AIMProfileSetSpec{Selector: aimv1alpha1.ProfileSelector{ModelRef: &aimv1alpha1.ProfileSelectorModelRef{Name: "model"}}}},
		&aimv1alpha2.AIMProfileSet{ObjectMeta: metav1.ObjectMeta{Name: "unrelated", Namespace: "team"}, Spec: aimv1alpha1.AIMProfileSetSpec{Selector: aimv1alpha1.ProfileSelector{ModelRef: &aimv1alpha1.ProfileSelectorModelRef{Name: "other-model"}}}},
		&aimv1alpha2.AIMProfileSet{ObjectMeta: metav1.ObjectMeta{Name: "explicit-catalog", Namespace: "team"}, Spec: aimv1alpha1.AIMProfileSetSpec{SourceRef: &aimv1alpha1.ProfileSourceRef{Name: "cache"}}},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sets...).
		WithIndex(&aimv1alpha2.AIMProfileSet{}, profileSetModelRefIndex, func(obj client.Object) []string {
			return profileSetModelIndexValues(obj.(*aimv1alpha2.AIMProfileSet).Spec)
		}).Build()
	catalog := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "team", OwnerReferences: []metav1.OwnerReference{{Kind: "AIMModel", Name: "model"}}}}
	model := catalogModelEvent(catalog)
	requests := profileSetsForModel(context.Background(), c, model)
	if len(requests) != 2 {
		t.Fatalf("namespace catalog event requested %v", requests)
	}
	for _, req := range requests {
		if req.Namespace != "team" || (req.Name != "broad" && req.Name != "named") {
			t.Fatalf("unrelated set enqueued: %v", req)
		}
	}
	catalog.OwnerReferences[0].Kind = "AIMClusterModel"
	requests = profileSetsForModel(context.Background(), c, catalogModelEvent(catalog))
	if len(requests) != 3 {
		t.Fatalf("cluster catalog should reach namespace fallback consumers: %v", requests)
	}
}
