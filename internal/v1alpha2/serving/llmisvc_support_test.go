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

package serving

import (
	"errors"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fakediscovery "k8s.io/client-go/discovery/fake"
	clienttesting "k8s.io/client-go/testing"
)

func newFakeDiscovery(resources ...*metav1.APIResourceList) *fakediscovery.FakeDiscovery {
	return &fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{Resources: resources}}
}

func TestDetectLLMInferenceServiceAPI(t *testing.T) {
	tests := []struct {
		name      string
		resources []*metav1.APIResourceList
		want      bool
	}{
		{
			name: "both kinds served",
			resources: []*metav1.APIResourceList{{
				GroupVersion: "serving.kserve.io/v1alpha2",
				APIResources: []metav1.APIResource{
					{Name: "llminferenceservices", Kind: "LLMInferenceService"},
					{Name: "llminferenceserviceconfigs", Kind: "LLMInferenceServiceConfig"},
				},
			}},
			want: true,
		},
		{
			name: "only the service kind served",
			resources: []*metav1.APIResourceList{{
				GroupVersion: "serving.kserve.io/v1alpha2",
				APIResources: []metav1.APIResource{
					{Name: "llminferenceservices", Kind: "LLMInferenceService"},
				},
			}},
			want: false,
		},
		{
			name: "only the v1alpha1 group version installed",
			resources: []*metav1.APIResourceList{{
				GroupVersion: "serving.kserve.io/v1alpha1",
				APIResources: []metav1.APIResource{
					{Name: "llminferenceservices", Kind: "LLMInferenceService"},
					{Name: "llminferenceserviceconfigs", Kind: "LLMInferenceServiceConfig"},
				},
			}},
			want: false,
		},
		{
			name: "no KServe APIs",
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DetectLLMInferenceServiceAPI(newFakeDiscovery(tt.resources...))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("DetectLLMInferenceServiceAPI() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDetectLLMInferenceServiceAPI_discoveryErrorIsNotAbsence(t *testing.T) {
	client := newFakeDiscovery()
	client.PrependReactor("get", "resource", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("connection refused")
	})

	if _, err := DetectLLMInferenceServiceAPI(client); err == nil {
		t.Fatal("expected a discovery failure to be returned, got nil")
	}
}
