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

package aimprofile

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

// TestPlanResources_NoCacheWhenNotEnabled pins the default: no Profile-owned
// cache is materialised when spec.caching is nil or disabled, even if the
// profile carries model sources. The service-driven path is then the only
// way an AIMProfileCache shows up — matching v1alpha1 behavior when an
// AIMServiceTemplate has no Caching block.
func TestPlanResources_NoCacheWhenNotEnabled(t *testing.T) {
	cases := map[string]*aimv1alpha2.AIMProfile{
		"caching nil": {
			ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"},
			Spec: aimv1alpha2.AIMProfileSpec{
				AIMProfileSpecCommon: aimv1alpha2.AIMProfileSpecCommon{
					ModelSources: []aimv1alpha1.AIMModelSource{
						{ModelID: "m", SourceURI: "hf://m"},
					},
				},
			},
		},
		"caching disabled": {
			ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"},
			Spec: aimv1alpha2.AIMProfileSpec{
				AIMProfileSpecCommon: aimv1alpha2.AIMProfileSpecCommon{
					ModelSources: []aimv1alpha1.AIMModelSource{
						{ModelID: "m", SourceURI: "hf://m"},
					},
				},
				Caching: &aimv1alpha2.AIMProfileCachingConfig{Enabled: false},
			},
		},
		"no model sources": {
			ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"},
			Spec: aimv1alpha2.AIMProfileSpec{
				Caching: &aimv1alpha2.AIMProfileCachingConfig{Enabled: true},
			},
		},
	}
	for name, profile := range cases {
		t.Run(name, func(t *testing.T) {
			r := &ProfileReconciler{}
			plan := r.PlanResources(
				context.Background(),
				controllerutils.ReconcileContext[*aimv1alpha2.AIMProfile]{Object: profile},
				ProfileObservation{},
			)
			if got := len(plan.GetToApply()); got != 0 {
				t.Fatalf("expected no resources planned, got %d", got)
			}
		})
	}
}

// TestPlanResources_EmitsProfileOwnedCacheWhenEnabled pins Path A: when a
// namespace-scoped AIMProfile sets caching.enabled and has model sources, the
// reconciler emits a single AIMProfileCache that is named after the profile,
// in the profile's namespace, in Shared mode. The pipeline owner-refs it back
// to the profile during apply, so deleting the profile garbage-collects the
// cache (and its dedicated artifacts, when applicable).
func TestPlanResources_EmitsProfileOwnedCacheWhenEnabled(t *testing.T) {
	profile := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "qwen3-32b-mi300x",
			Namespace: "team-a",
		},
		Spec: aimv1alpha2.AIMProfileSpec{
			AIMProfileSpecCommon: aimv1alpha2.AIMProfileSpecCommon{
				ModelSources: []aimv1alpha1.AIMModelSource{
					{ModelID: "qwen/qwen3-32b-fp8", SourceURI: "hf://qwen/qwen3-32b-fp8"},
				},
			},
			Caching: &aimv1alpha2.AIMProfileCachingConfig{
				Enabled: true,
				Env:     []corev1.EnvVar{{Name: "HF_TOKEN", Value: "xyz"}},
			},
		},
	}

	r := &ProfileReconciler{}
	plan := r.PlanResources(
		context.Background(),
		controllerutils.ReconcileContext[*aimv1alpha2.AIMProfile]{Object: profile},
		ProfileObservation{},
	)

	objects := plan.GetToApply()
	if len(objects) != 1 {
		t.Fatalf("expected exactly one Profile-owned cache, got %d", len(objects))
	}
	cache, ok := objects[0].(*aimv1alpha2.AIMProfileCache)
	if !ok {
		t.Fatalf("expected AIMProfileCache, got %T", objects[0])
	}
	if cache.Name != profile.Name {
		t.Errorf("Profile-owned cache must be named after the profile (1:1): got %q want %q", cache.Name, profile.Name)
	}
	if cache.Namespace != profile.Namespace {
		t.Errorf("cache namespace mismatch: got %q want %q", cache.Namespace, profile.Namespace)
	}
	if cache.Spec.ProfileName != profile.Name {
		t.Errorf("cache.spec.profileName must reference the source profile, got %q", cache.Spec.ProfileName)
	}
	if cache.Spec.ProfileScope != aimv1alpha1.AIMResolutionScopeNamespace {
		t.Errorf("cache.spec.profileScope must be Namespace for namespace-scoped profile, got %q", cache.Spec.ProfileScope)
	}
	if cache.Spec.Mode != aimv1alpha2.ProfileCacheModeShared {
		t.Errorf("Profile-owned cache must use Shared mode so artifacts dedupe by weights, got %q", cache.Spec.Mode)
	}
	if len(cache.Spec.Env) != 1 || cache.Spec.Env[0].Name != "HF_TOKEN" {
		t.Errorf("cache.spec.env must inherit profile.spec.caching.env, got %+v", cache.Spec.Env)
	}
}

// TestClusterProfilePlanResources_NeverEmitsCache pins the deliberate gap:
// a cluster-scoped profile cannot directly create a namespaced cache because
// it has no target namespace. The schema enforces this at the type level —
// AIMClusterProfileSpec deliberately omits the Caching field that
// AIMProfileSpec carries, so this test also pins that the reconciler does
// nothing with model sources alone. AIMServices that target a cluster
// profile drive cache creation namespace-side via the service-driven path.
func TestClusterProfilePlanResources_NeverEmitsCache(t *testing.T) {
	profile := &aimv1alpha2.AIMClusterProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "p"},
		Spec: aimv1alpha2.AIMClusterProfileSpec{
			AIMProfileSpecCommon: aimv1alpha2.AIMProfileSpecCommon{
				ModelSources: []aimv1alpha1.AIMModelSource{
					{ModelID: "m", SourceURI: "hf://m"},
				},
			},
		},
	}

	r := &ClusterProfileReconciler{}
	plan := r.PlanResources(
		context.Background(),
		controllerutils.ReconcileContext[*aimv1alpha2.AIMClusterProfile]{Object: profile},
		ClusterProfileObservation{},
	)
	if got := len(plan.GetToApply()); got != 0 {
		t.Fatalf("AIMClusterProfile must not emit a namespaced cache: got %d resources planned", got)
	}
}
