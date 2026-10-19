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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

func TestFindServicesForTemplateUsesCanonicalIndex(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := aimv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := aimv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	service := &aimv1alpha2.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "service", Namespace: "default"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Template: &aimv1alpha1.AIMServiceTemplateConfig{Name: "template"},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(service).
		WithIndex(&aimv1alpha2.AIMService{}, aimv1alpha1.AIMServiceTemplateIndexKey, func(obj client.Object) []string {
			svc := obj.(*aimv1alpha2.AIMService)
			return []string{svc.Spec.Template.Name}
		}).
		Build()

	requests := findServicesForTemplate(c)(context.Background(), &aimv1alpha1.AIMServiceTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "template", Namespace: "default"},
	})
	if len(requests) != 1 || requests[0].Name != service.Name {
		t.Fatalf("requests = %#v, want canonical indexed service", requests)
	}
}

func TestRequestsForTemplateServicesFiltersByActivePipeline(t *testing.T) {
	modelName := "model"
	service := func(
		name string,
		annotations map[string]string,
		spec aimv1alpha1.AIMServiceSpec,
	) aimv1alpha2.AIMService {
		return aimv1alpha2.AIMService{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Namespace:   "default",
				Annotations: annotations,
			},
			Spec: spec,
		}
	}

	services := []aimv1alpha2.AIMService{
		service("inferred-template", nil, aimv1alpha1.AIMServiceSpec{
			Model: &aimv1alpha1.AIMServiceModel{Name: &modelName},
		}),
		service("inferred-profile", nil, aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: "profile"},
		}),
		service(
			"forced-profile",
			map[string]string{constants.AnnotationReconcilerPipeline: constants.ReconcilerPipelineProfile},
			aimv1alpha1.AIMServiceSpec{Model: &aimv1alpha1.AIMServiceModel{Name: &modelName}},
		),
		service(
			"forced-template",
			map[string]string{constants.AnnotationReconcilerPipeline: constants.ReconcilerPipelineTemplate},
			aimv1alpha1.AIMServiceSpec{
				Model:   &aimv1alpha1.AIMServiceModel{Name: &modelName},
				Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: "profile"},
			},
		),
		service(
			"grandfathered-profile",
			map[string]string{constants.AnnotationReconcilerPipeline: "historical"},
			aimv1alpha1.AIMServiceSpec{
				Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: "profile"},
			},
		),
	}

	requests := requestsForTemplateServices(services)
	got := map[string]struct{}{}
	for _, request := range requests {
		got[request.Name] = struct{}{}
	}
	for _, name := range []string{"inferred-template", "forced-template"} {
		if _, found := got[name]; !found {
			t.Errorf("missing template-pipeline enqueue for %q", name)
		}
	}
	if len(got) != 2 {
		t.Fatalf("template watch enqueued inactive services: %#v", got)
	}
}
