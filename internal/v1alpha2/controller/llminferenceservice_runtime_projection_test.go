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

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/serving"
)

const (
	llmTestNamespace    = "team-b"
	llmTestProfileName  = "qwen3-32b-cluster"
	llmTestConsumerName = "chat"
)

var llmProjectionSupport = serving.LLMInferenceServiceSupport{Enabled: true, KServeNamespace: "kserve-system"}

func llmProjectionScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtimeProjectionScheme(t)
	if err := kservev1alpha2.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}
	return scheme
}

func newLLMProjectionClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().
		WithScheme(llmProjectionScheme(t)).
		WithObjects(objs...).
		WithIndex(&servingv1beta1.InferenceService{}, inferenceServiceRuntimeIndexKey, indexInferenceServiceRuntime).
		WithIndex(&kservev1alpha2.LLMInferenceService{}, llmInferenceServiceBaseRefIndexKey, indexLLMInferenceServiceBaseRefs).
		WithReturnManagedFields().
		Build()
}

// deployableClusterProfile is a Ready cluster profile backing a bare
// ClusterServingRuntime / system-namespace LLMInferenceServiceConfig.
func deployableClusterProfile() *aimv1alpha2.AIMClusterProfile {
	return &aimv1alpha2.AIMClusterProfile{
		ObjectMeta: metav1.ObjectMeta{Name: llmTestProfileName, UID: "cluster-profile-uid"},
		Spec: aimv1alpha2.AIMClusterProfileSpec{
			AIMProfileSpecCommon: aimv1alpha2.AIMProfileSpecCommon{
				AimId:           "qwen/qwen3-32b",
				ModelId:         "qwen/qwen3-32b-fp8",
				Engine:          "vllm",
				Metric:          aimv1alpha1.AIMMetric("latency"),
				Precision:       aimv1alpha1.AIMPrecision("fp8"),
				AcceleratorType: aimv1alpha1.AcceleratorType("cpu"),
				Image:           "docker.io/amdenterpriseai/aim-base:0.13.1",
				ModelSources:    []aimv1alpha1.AIMModelSource{{ModelID: "qwen/qwen3-32b-fp8", SourceURI: "hf://qwen/qwen3-32b-fp8"}},
			},
		},
		Status: aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady, Deployable: true},
	}
}

func managedClusterServingRuntime(name, profileName string) *kservev1alpha1.ClusterServingRuntime {
	return &kservev1alpha1.ClusterServingRuntime{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				constants.LabelK8sManagedBy: constants.LabelValueManagedBy,
				constants.LabelProfile:      profileName,
			},
		},
	}
}

func makeConsumerLLMInferenceService(baseRefs ...string) *kservev1alpha2.LLMInferenceService {
	service := &kservev1alpha2.LLMInferenceService{ObjectMeta: metav1.ObjectMeta{Name: llmTestConsumerName, Namespace: llmTestNamespace}}
	for _, ref := range baseRefs {
		service.Spec.BaseRefs = append(service.Spec.BaseRefs, corev1.LocalObjectReference{Name: ref})
	}
	return service
}

func reconcileProjectionKey(t *testing.T, r *RuntimeProjectionReconciler, name string) {
	t.Helper()
	if _, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: client.ObjectKey{Namespace: llmTestNamespace, Name: name},
	}); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
}

func TestReconcile_llmInferenceServiceConsumerGetsCompleteShadow(t *testing.T) {
	profile := deployableClusterProfile()
	name := serving.RuntimeName(profile.Name)
	c := newLLMProjectionClient(t,
		profile,
		managedClusterServingRuntime(name, profile.Name),
		makeConsumerLLMInferenceService(name),
	)
	r := &RuntimeProjectionReconciler{Client: c, Scheme: llmProjectionScheme(t), LLMInferenceService: llmProjectionSupport}

	reconcileProjectionKey(t, r, name)

	key := client.ObjectKey{Namespace: llmTestNamespace, Name: name}
	var config kservev1alpha2.LLMInferenceServiceConfig
	if err := c.Get(context.Background(), key, &config); err != nil {
		t.Fatalf("namespaced LLMInferenceServiceConfig was not materialized: %v", err)
	}
	if config.Labels[constants.LabelRuntimeProjection] != constants.LabelValueRuntimeProjectionLazy {
		t.Errorf("config projection marker = %q, want lazy", config.Labels[constants.LabelRuntimeProjection])
	}
	if len(config.OwnerReferences) != 1 || config.OwnerReferences[0].Kind != clusterProfileKind ||
		config.OwnerReferences[0].Name != profile.Name {
		t.Errorf("config owner references = %#v, want the backing AIMClusterProfile", config.OwnerReferences)
	}
	// The config mounts the profile ConfigMap, so the namespaced pair the
	// ServingRuntime projection owns must be materialized with it.
	if err := c.Get(context.Background(), key, &corev1.ConfigMap{}); err != nil {
		t.Errorf("profile ConfigMap was not materialized: %v", err)
	}
	if err := c.Get(context.Background(), key, &kservev1alpha1.ServingRuntime{}); err != nil {
		t.Errorf("ServingRuntime was not materialized: %v", err)
	}
}

func TestReconcile_llmInferenceServiceConsumersIgnoredWithoutSupport(t *testing.T) {
	profile := deployableClusterProfile()
	name := serving.RuntimeName(profile.Name)
	c := newLLMProjectionClient(t,
		profile,
		managedClusterServingRuntime(name, profile.Name),
		makeConsumerLLMInferenceService(name),
	)
	r := &RuntimeProjectionReconciler{Client: c, Scheme: llmProjectionScheme(t)}

	reconcileProjectionKey(t, r, name)

	err := c.Get(context.Background(), client.ObjectKey{Namespace: llmTestNamespace, Name: name}, &kservev1alpha1.ServingRuntime{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected no shadow without LLMInferenceService support, got err=%v", err)
	}
}

func TestReconcile_unrenderableLLMConfigKeepsServingRuntimeShadow(t *testing.T) {
	profile := deployableClusterProfile()
	profile.Spec.ModelSources = nil
	name := serving.RuntimeName(profile.Name)
	c := newLLMProjectionClient(t,
		profile,
		managedClusterServingRuntime(name, profile.Name),
		makeConsumerISVC("chat", llmTestNamespace, name),
	)
	r := &RuntimeProjectionReconciler{Client: c, Scheme: llmProjectionScheme(t), LLMInferenceService: llmProjectionSupport}

	reconcileProjectionKey(t, r, name)

	key := client.ObjectKey{Namespace: llmTestNamespace, Name: name}
	if err := c.Get(context.Background(), key, &kservev1alpha1.ServingRuntime{}); err != nil {
		t.Fatalf("ServingRuntime shadow must not depend on the LLMInferenceServiceConfig: %v", err)
	}
	if err := c.Get(context.Background(), key, &kservev1alpha2.LLMInferenceServiceConfig{}); !apierrors.IsNotFound(err) {
		t.Fatalf("an unrenderable LLMInferenceServiceConfig must be skipped, got err=%v", err)
	}
}

func TestRuntimeKeysForLLMInferenceService_onlyReservedBaseRefs(t *testing.T) {
	service := makeConsumerLLMInferenceService("team-preset", "aim-qwen-qwen3-32b")
	assertRequestKeys(t, runtimeKeysForLLMInferenceService(context.Background(), service),
		[]string{llmTestNamespace + "/aim-qwen-qwen3-32b"})
}

func TestFindInferenceServicesForClusterProfile_includesLLMInferenceServiceConsumers(t *testing.T) {
	profile := deployableClusterProfile()
	slug := serving.ModelSlugRuntimeName(profile.Spec.AimId)
	c := newLLMProjectionClient(t,
		profile,
		makeConsumerLLMInferenceService(slug),
		makeConsumerISVC("legacy", "team-c", serving.RuntimeName(profile.Name)),
	)
	r := &RuntimeProjectionReconciler{Client: c, LLMInferenceService: llmProjectionSupport}

	assertRequestKeys(t, r.findInferenceServicesForClusterProfile(context.Background(), profile), []string{
		llmTestNamespace + "/" + slug,
		"team-c/" + serving.RuntimeName(profile.Name),
	})
}
