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
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
)

func TestEnsureAdapterParentOwnerRef(t *testing.T) {
	const (
		namespace       = "models"
		parentName      = "parent"
		adapterName     = "adapter"
		resourceVersion = "7"
	)
	parentUID := types.UID("parent-uid")
	adapterUID := types.UID("adapter-uid")
	otherUID := types.UID("other-uid")
	trueValue := true
	falseValue := false

	otherRef := metav1.OwnerReference{
		APIVersion:         "example.com/v1",
		Kind:               "Widget",
		Name:               "other",
		UID:                otherUID,
		Controller:         &falseValue,
		BlockOwnerDeletion: &trueValue,
	}
	parentRef := func(apiVersion string, controller, block *bool) metav1.OwnerReference {
		return metav1.OwnerReference{
			APIVersion:         apiVersion,
			Kind:               "AIMArtifact",
			Name:               parentName,
			UID:                parentUID,
			Controller:         controller,
			BlockOwnerDeletion: block,
		}
	}

	spec := aimv1alpha1.AIMArtifactSpec{
		Type:           aimv1alpha1.ArtifactTypeAdapter,
		ParentArtifact: parentName,
		ModelID:        "example/adapter",
		SourceURI:      "hf://example/adapter",
	}
	labels := map[string]string{"keep": "label"}
	annotations := map[string]string{"keep": "annotation"}

	tests := []struct {
		name      string
		refs      []metav1.OwnerReference
		want      []metav1.OwnerReference
		wantAdded bool
	}{
		{
			name: "v1alpha1 link is rewritten and flags stay",
			refs: []metav1.OwnerReference{
				parentRef("aim.eai.amd.com/v1alpha1", &trueValue, &falseValue),
				otherRef,
			},
			want: []metav1.OwnerReference{
				parentRef(aimv1alpha2.GroupVersion.String(), &trueValue, &falseValue),
				otherRef,
			},
		},
		{
			name: "new link is written at v1alpha2",
			refs: []metav1.OwnerReference{otherRef},
			want: []metav1.OwnerReference{
				otherRef,
				parentRef(aimv1alpha2.GroupVersion.String(), nil, nil),
			},
			wantAdded: true,
		},
	}

	scheme := runtime.NewScheme()
	if err := aimv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme: %v", err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parent := &aimv1alpha1.AIMArtifact{
				ObjectMeta: metav1.ObjectMeta{
					Name:      parentName,
					Namespace: namespace,
					UID:       parentUID,
				},
				Spec: aimv1alpha1.AIMArtifactSpec{
					Type:      aimv1alpha1.ArtifactTypeModel,
					SourceURI: "hf://example/parent",
				},
			}
			adapter := &aimv1alpha1.AIMArtifact{
				ObjectMeta: metav1.ObjectMeta{
					Name:            adapterName,
					Namespace:       namespace,
					UID:             adapterUID,
					ResourceVersion: resourceVersion,
					Labels:          labels,
					Annotations:     annotations,
					OwnerReferences: tt.refs,
				},
				Spec: spec,
			}
			reconciler := &AIMArtifactReconciler{
				Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(parent, adapter).Build(),
				Scheme: scheme,
			}
			key := client.ObjectKey{Namespace: namespace, Name: adapterName}

			loaded := &aimv1alpha1.AIMArtifact{}
			if err := reconciler.Get(context.Background(), key, loaded); err != nil {
				t.Fatalf("get adapter: %v", err)
			}
			specBefore := loaded.Spec.DeepCopy()
			added, err := reconciler.ensureAdapterParentOwnerRef(context.Background(), loaded)
			if err != nil {
				t.Fatalf("ensure owner reference: %v", err)
			}
			if added != tt.wantAdded {
				t.Fatalf("added = %v, want %v", added, tt.wantAdded)
			}

			got := &aimv1alpha1.AIMArtifact{}
			if err := reconciler.Get(context.Background(), key, got); err != nil {
				t.Fatalf("get stored adapter: %v", err)
			}
			if got.UID != adapterUID {
				t.Errorf("uid = %s, want %s", got.UID, adapterUID)
			}
			if !reflect.DeepEqual(got.OwnerReferences, tt.want) {
				t.Errorf("owner references = %#v, want %#v", got.OwnerReferences, tt.want)
			}
			if !reflect.DeepEqual(&got.Spec, specBefore) {
				t.Errorf("spec = %#v, want %#v", got.Spec, *specBefore)
			}
			if !reflect.DeepEqual(got.Labels, labels) || !reflect.DeepEqual(got.Annotations, annotations) {
				t.Errorf("labels = %v annotations = %v", got.Labels, got.Annotations)
			}
			if got.ResourceVersion == resourceVersion {
				t.Fatal("resourceVersion unchanged after a write")
			}

			writtenVersion := got.ResourceVersion
			added, err = reconciler.ensureAdapterParentOwnerRef(context.Background(), got)
			if err != nil {
				t.Fatalf("second ensure owner reference: %v", err)
			}
			if added {
				t.Fatal("second ensure owner reference added it again")
			}
			if err := reconciler.Get(context.Background(), key, got); err != nil {
				t.Fatalf("get adapter after second run: %v", err)
			}
			if got.ResourceVersion != writtenVersion {
				t.Errorf("second run changed resourceVersion from %s to %s", writtenVersion, got.ResourceVersion)
			}
			if !reflect.DeepEqual(got.OwnerReferences, tt.want) {
				t.Errorf("owner references after second run = %#v, want %#v", got.OwnerReferences, tt.want)
			}
		})
	}
}
