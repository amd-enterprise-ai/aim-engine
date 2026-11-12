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
	"strings"
	"testing"

	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	kserveconstants "github.com/kserve/kserve/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/serving"
)

func TestFindServicesForInferenceServicePod_llmWorkloadPod(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := kservev1alpha2.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	llmService := &kservev1alpha2.LLMInferenceService{ObjectMeta: metav1.ObjectMeta{
		Name:            "chat-x1y2",
		Namespace:       "team-a",
		OwnerReferences: []metav1.OwnerReference{{Kind: "AIMService", Name: "chat", APIVersion: "aim.eai.amd.com/v1alpha2"}},
	}}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name:      "chat-x1y2-kserve-abc",
		Namespace: "team-a",
		Labels: map[string]string{
			kserveconstants.KubernetesAppNameLabelKey:   llmService.Name,
			kserveconstants.KubernetesPartOfLabelKey:    kserveconstants.LLMInferenceServicePartOfValue,
			kserveconstants.KubernetesComponentLabelKey: kserveconstants.LLMComponentWorkload,
		},
	}}

	r := &AIMServiceReconciler{
		Client:              fake.NewClientBuilder().WithScheme(scheme).WithObjects(llmService).Build(),
		LLMInferenceService: serving.LLMInferenceServiceSupport{Enabled: true},
	}
	requests := r.findServicesForInferenceServicePod(context.Background(), pod)
	if len(requests) != 1 || requests[0].Name != "chat" || requests[0].Namespace != "team-a" {
		t.Fatalf("requests = %v, want the owning AIMService team-a/chat", requests)
	}

	r.LLMInferenceService.Enabled = false
	if requests := r.findServicesForInferenceServicePod(context.Background(), pod); len(requests) != 0 {
		t.Fatalf("requests = %v, want none without LLMInferenceService support", requests)
	}
}

func TestFindServicesForLLMBaseRef(t *testing.T) {
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{corev1.AddToScheme, aimv1alpha2.AddToScheme, kservev1alpha2.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	service := func(name, namespace string, refs ...string) *aimv1alpha2.AIMService {
		svc := &aimv1alpha2.AIMService{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
		svc.Spec.LLMInferenceService = &aimv1alpha1.AIMServiceLLMInferenceServiceConfig{}
		for _, ref := range refs {
			svc.Spec.LLMInferenceService.BaseRefs = append(svc.Spec.LLMInferenceService.BaseRefs, aimv1alpha1.AIMLLMInferenceServiceConfigReference{Name: ref})
		}
		return svc
	}
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(
			service("a", "team-a", "team-preset"),
			service("b", "team-b", "team-preset"),
			service("c", "team-a", "other"),
		).
		WithIndex(&aimv1alpha2.AIMService{}, llmBaseRefIndexKey, indexServiceLLMBaseRefs).
		Build()
	r := &AIMServiceReconciler{
		Client:              c,
		LLMInferenceService: serving.LLMInferenceServiceSupport{Enabled: true, KServeNamespace: "kserve-system"},
	}

	keys := func(obj *kservev1alpha2.LLMInferenceServiceConfig) []string {
		var out []string
		for _, req := range r.findServicesForLLMBaseRef(context.Background(), obj) {
			out = append(out, req.Namespace+"/"+req.Name)
		}
		sort.Strings(out)
		return out
	}
	namespaced := &kservev1alpha2.LLMInferenceServiceConfig{ObjectMeta: metav1.ObjectMeta{Name: "team-preset", Namespace: "team-a"}}
	if got := keys(namespaced); strings.Join(got, ",") != "team-a/a" {
		t.Errorf("namespaced preset mapped to %v, want only team-a/a", got)
	}
	shared := &kservev1alpha2.LLMInferenceServiceConfig{ObjectMeta: metav1.ObjectMeta{Name: "team-preset", Namespace: "kserve-system"}}
	if got := keys(shared); strings.Join(got, ",") != "team-a/a,team-b/b" {
		t.Errorf("KServe-namespace preset mapped to %v, want services in every namespace", got)
	}
}
