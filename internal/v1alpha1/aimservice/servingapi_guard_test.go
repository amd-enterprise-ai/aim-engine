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

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
)

func TestTemplatePipelineServingAPIHealth(t *testing.T) {
	for _, tc := range []struct {
		servingAPI aimv1alpha1.AIMServingAPI
		wantReport bool
	}{
		{"", false},
		{aimv1alpha1.ServingAPIAuto, false},
		{aimv1alpha1.ServingAPIInferenceService, false},
		{aimv1alpha1.ServingAPILLMInferenceService, true},
	} {
		t.Run(string(tc.servingAPI), func(t *testing.T) {
			service := &aimv1alpha1.AIMService{Spec: aimv1alpha1.AIMServiceSpec{ServingAPI: tc.servingAPI}}
			health := templatePipelineServingAPIHealth(service)
			if got := health.Component != ""; got != tc.wantReport {
				t.Fatalf("reported = %v, want %v (%+v)", got, tc.wantReport, health)
			}
			if tc.wantReport && len(health.Errors) == 0 {
				t.Fatal("the rejection must carry an invalid-spec error so it blocks apply")
			}
		})
	}
}
