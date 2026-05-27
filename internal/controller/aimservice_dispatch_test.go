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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

func TestSelectPipeline(t *testing.T) {
	const (
		profile  = constants.ReconcilerPipelineProfile
		template = constants.ReconcilerPipelineTemplate
	)
	modelName := "some-model"
	image := "registry/image:tag"

	tests := []struct {
		name        string
		annotations map[string]string
		spec        aimv1alpha1.AIMServiceSpec
		want        string
	}{
		{
			name: "spec.profile routes to profile pipeline",
			spec: aimv1alpha1.AIMServiceSpec{
				Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: "p"},
			},
			want: profile,
		},
		{
			name: "spec.template routes to template pipeline",
			spec: aimv1alpha1.AIMServiceSpec{
				Template: &aimv1alpha1.AIMServiceTemplateConfig{Name: "t"},
			},
			want: template,
		},
		{
			name: "spec.model.name routes to template pipeline by default",
			spec: aimv1alpha1.AIMServiceSpec{
				Model: &aimv1alpha1.AIMServiceModel{Name: &modelName},
			},
			want: template,
		},
		{
			name: "spec.model.image routes to template pipeline by default",
			spec: aimv1alpha1.AIMServiceSpec{
				Model: &aimv1alpha1.AIMServiceModel{Image: &image},
			},
			want: template,
		},
		{
			name:        "annotation forces profile pipeline on a model-shaped spec",
			annotations: map[string]string{constants.AnnotationReconcilerPipeline: profile},
			spec: aimv1alpha1.AIMServiceSpec{
				Model: &aimv1alpha1.AIMServiceModel{Name: &modelName},
			},
			want: profile,
		},
		{
			name:        "annotation forces template pipeline on a profile-shaped spec",
			annotations: map[string]string{constants.AnnotationReconcilerPipeline: template},
			spec: aimv1alpha1.AIMServiceSpec{
				Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: "p"},
			},
			want: template,
		},
		{
			name:        "unknown annotation value falls through to spec shape",
			annotations: map[string]string{constants.AnnotationReconcilerPipeline: "bogus"},
			spec: aimv1alpha1.AIMServiceSpec{
				Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: "p"},
			},
			want: profile,
		},
		{
			name:        "empty annotation value falls through to spec shape",
			annotations: map[string]string{constants.AnnotationReconcilerPipeline: ""},
			spec: aimv1alpha1.AIMServiceSpec{
				Model: &aimv1alpha1.AIMServiceModel{Name: &modelName},
			},
			want: template,
		},
		// v1alpha2 quick-start: image-shape services must be able to
		// opt into the profile pipeline via the annotation so the
		// resolver's auto-create AIMModel path applies. Without the
		// annotation they keep using the v1alpha1 template pipeline
		// (which has its own image-based auto-create flow); flipping
		// is the migration-window opt-in until v1alpha1 is removed.
		{
			name:        "annotation forces profile pipeline on a spec.model.image service",
			annotations: map[string]string{constants.AnnotationReconcilerPipeline: profile},
			spec: aimv1alpha1.AIMServiceSpec{
				Model: &aimv1alpha1.AIMServiceModel{Image: &image},
			},
			want: profile,
		},
		{
			name:        "annotation forces template pipeline on a spec.model.image service",
			annotations: map[string]string{constants.AnnotationReconcilerPipeline: template},
			spec: aimv1alpha1.AIMServiceSpec{
				Model: &aimv1alpha1.AIMServiceModel{Image: &image},
			},
			want: template,
		},
		{
			name:        "annotation has higher priority than profile spec on image-shape service",
			annotations: map[string]string{constants.AnnotationReconcilerPipeline: template},
			spec: aimv1alpha1.AIMServiceSpec{
				Model:   &aimv1alpha1.AIMServiceModel{Image: &image},
				Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: "p"},
			},
			want: template,
		},
		{
			name:        "unknown annotation on image-shape falls through to template default",
			annotations: map[string]string{constants.AnnotationReconcilerPipeline: "bogus"},
			spec: aimv1alpha1.AIMServiceSpec{
				Model: &aimv1alpha1.AIMServiceModel{Image: &image},
			},
			want: template,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &aimv1alpha1.AIMService{
				ObjectMeta: metav1.ObjectMeta{Annotations: tc.annotations},
				Spec:       tc.spec,
			}
			if got := selectPipeline(svc); got != tc.want {
				t.Fatalf("selectPipeline = %q, want %q", got, tc.want)
			}
		})
	}
}
