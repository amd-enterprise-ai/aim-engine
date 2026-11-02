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

package profilecache

import (
	"context"
	"errors"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

const testNamespace = "team-a"

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		aimv1alpha1.AddToScheme,
		aimv1alpha2.AddToScheme,
	} {
		if err := add(scheme); err != nil {
			t.Fatalf("AddToScheme() error = %v", err)
		}
	}
	return scheme
}

// cache builds an AIMProfileCache with the given identity and status. The object
// name is intentionally decoupled from profileName so tests can pin that a
// service-driven Shared cache (hashed name) still resolves by profileName.
func cache(name, profileName string, scope aimv1alpha1.AIMResolutionScope, mode aimv1alpha2.AIMProfileCacheMode, status constants.AIMStatus) *aimv1alpha2.AIMProfileCache {
	return &aimv1alpha2.AIMProfileCache{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace, Generation: 1},
		Spec: aimv1alpha2.AIMProfileCacheSpec{
			ProfileName:  profileName,
			ProfileScope: scope,
			Mode:         mode,
		},
		Status: aimv1alpha2.AIMProfileCacheStatus{
			ObservedGeneration: 1,
			Status:             status,
		},
	}
}

func newClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(objs...).Build()
}

func TestFindReadyShared(t *testing.T) {
	t.Parallel()

	const profile = "qwen3-32b-mi300x"
	ns := aimv1alpha1.AIMResolutionScopeNamespace
	cl := aimv1alpha1.AIMResolutionScopeCluster
	shared := aimv1alpha2.ProfileCacheModeShared
	dedicated := aimv1alpha2.ProfileCacheModeDedicated

	cases := []struct {
		name       string
		objs       []client.Object
		queryScope aimv1alpha1.AIMResolutionScope
		wantName   string // "" means expect nil
	}{
		{
			// The service-driven Shared cache carries a hashed object name, not
			// the profile name — it must still resolve by spec.profileName. This
			// is the core of the mode-independent-mount fix.
			name:       "service-driven shared cache (hashed name) resolves",
			objs:       []client.Object{cache(profile+"-cache-fe2d5ebc", profile, ns, shared, constants.AIMStatusReady)},
			queryScope: ns,
			wantName:   profile + "-cache-fe2d5ebc",
		},
		{
			name:       "profile-owned shared cache (named after profile) resolves",
			objs:       []client.Object{cache(profile, profile, ns, shared, constants.AIMStatusReady)},
			queryScope: ns,
			wantName:   profile,
		},
		{
			name:       "dedicated cache is never returned",
			objs:       []client.Object{cache(profile+"-svc-cache", profile, ns, dedicated, constants.AIMStatusReady)},
			queryScope: ns,
			wantName:   "",
		},
		{
			name:       "non-ready cache is skipped",
			objs:       []client.Object{cache(profile+"-cache", profile, ns, shared, constants.AIMStatusPending)},
			queryScope: ns,
			wantName:   "",
		},
		{
			name:       "different profile is skipped",
			objs:       []client.Object{cache("other-cache", "other-profile", ns, shared, constants.AIMStatusReady)},
			queryScope: ns,
			wantName:   "",
		},
		{
			name:       "empty cache scope is treated as Namespace",
			objs:       []client.Object{cache(profile+"-cache", profile, "", shared, constants.AIMStatusReady)},
			queryScope: ns,
			wantName:   profile + "-cache",
		},
		{
			name:       "cluster query does not match a namespace-scope cache",
			objs:       []client.Object{cache(profile+"-cache", profile, ns, shared, constants.AIMStatusReady)},
			queryScope: cl,
			wantName:   "",
		},
		{
			name:       "cluster query matches a cluster-scope cache",
			objs:       []client.Object{cache(profile+"-cache", profile, cl, shared, constants.AIMStatusReady)},
			queryScope: cl,
			wantName:   profile + "-cache",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := newClient(t, tc.objs...)
			got, err := FindReadyShared(context.Background(), c, testNamespace, profile, tc.queryScope)
			if err != nil {
				t.Fatalf("FindReadyShared() error = %v", err)
			}
			if tc.wantName == "" {
				if got != nil {
					t.Fatalf("FindReadyShared() = %q, want nil", got.Name)
				}
				return
			}
			if got == nil {
				t.Fatalf("FindReadyShared() = nil, want %q", tc.wantName)
			}
			if got.Name != tc.wantName {
				t.Fatalf("FindReadyShared() = %q, want %q", got.Name, tc.wantName)
			}
		})
	}
}

func TestFindReadyShared_ReturnsErrorWhenCacheReadinessIsUnknown(t *testing.T) {
	t.Parallel()

	const profile = "qwen3-32b-mi300x"
	for _, conditionType := range []string{
		controllerutils.ConditionTypeDependenciesReachable,
		controllerutils.ConditionTypeAuthValid,
		controllerutils.ConditionTypeConfigValid,
	} {
		t.Run(conditionType, func(t *testing.T) {
			unknown := cache(
				profile+"-cache",
				profile,
				aimv1alpha1.AIMResolutionScopeNamespace,
				aimv1alpha2.ProfileCacheModeShared,
				constants.AIMStatusDegraded,
			)
			unknown.Status.Conditions = []metav1.Condition{{
				Type:   conditionType,
				Status: metav1.ConditionFalse,
			}}

			got, err := FindReadyShared(
				context.Background(),
				newClient(t, unknown),
				testNamespace,
				profile,
				aimv1alpha1.AIMResolutionScopeNamespace,
			)
			if err == nil {
				t.Fatal("FindReadyShared() error = nil, want unknown-readiness error")
			}
			if got != nil {
				t.Fatalf("FindReadyShared() = %q, want nil on unknown readiness", got.Name)
			}
		})
	}
}

func TestFindReadyShared_ReturnsErrorWhenReadyStatusIsStale(t *testing.T) {
	t.Parallel()

	const profile = "qwen3-32b-mi300x"
	stale := cache(
		profile+"-cache",
		profile,
		aimv1alpha1.AIMResolutionScopeNamespace,
		aimv1alpha2.ProfileCacheModeShared,
		constants.AIMStatusReady,
	)
	stale.Generation = 2
	stale.Status.ObservedGeneration = 1

	got, err := FindReadyShared(
		context.Background(),
		newClient(t, stale),
		testNamespace,
		profile,
		aimv1alpha1.AIMResolutionScopeNamespace,
	)
	if err == nil {
		t.Fatal("FindReadyShared() error = nil, want stale-status error")
	}
	var staleErr *StatusStaleError
	if !errors.As(err, &staleErr) {
		t.Fatalf("FindReadyShared() error = %T %v, want *StatusStaleError", err, err)
	}
	if got != nil {
		t.Fatalf("FindReadyShared() = %q, want nil for stale Ready status", got.Name)
	}
}

func TestFindReadyShared_ReturnsPendingWhenObservedGenerationIsMissing(t *testing.T) {
	t.Parallel()

	const profile = "qwen3-32b-mi300x"
	unobserved := cache(
		profile+"-cache",
		profile,
		aimv1alpha1.AIMResolutionScopeNamespace,
		aimv1alpha2.ProfileCacheModeShared,
		constants.AIMStatusReady,
	)
	unobserved.Generation = 1
	unobserved.Status.ObservedGeneration = 0

	got, err := FindReadyShared(
		context.Background(),
		newClient(t, unobserved),
		testNamespace,
		profile,
		aimv1alpha1.AIMResolutionScopeNamespace,
	)
	var staleErr *StatusStaleError
	if !errors.As(err, &staleErr) {
		t.Fatalf("FindReadyShared() error = %T %v, want *StatusStaleError", err, err)
	}
	if got != nil {
		t.Fatalf("FindReadyShared() = %q, want nil while cache status catches up", got.Name)
	}
	if staleErr.Generation != 1 || staleErr.ObservedGeneration != 0 {
		t.Fatalf(
			"StatusStaleError generations = %d/%d, want generation=1 observedGeneration=0",
			staleErr.Generation,
			staleErr.ObservedGeneration,
		)
	}
}

func TestFindReadyShared_PrefersKnownReadyCacheOverUnknownCandidate(t *testing.T) {
	t.Parallel()

	const profile = "qwen3-32b-mi300x"
	scope := aimv1alpha1.AIMResolutionScopeNamespace
	unknown := cache("a-unknown", profile, scope, aimv1alpha2.ProfileCacheModeShared, constants.AIMStatusDegraded)
	unknown.Status.Conditions = []metav1.Condition{{
		Type:   controllerutils.ConditionTypeDependenciesReachable,
		Status: metav1.ConditionFalse,
	}}
	ready := cache("z-ready", profile, scope, aimv1alpha2.ProfileCacheModeShared, constants.AIMStatusReady)

	got, err := FindReadyShared(context.Background(), newClient(t, unknown, ready), testNamespace, profile, scope)
	if err != nil {
		t.Fatalf("FindReadyShared() error = %v", err)
	}
	if got == nil || got.Name != ready.Name {
		t.Fatalf("FindReadyShared() = %v, want %q", got, ready.Name)
	}
}

func TestSelectBestShared_DeterministicAcrossReadyCandidates(t *testing.T) {
	t.Parallel()

	const profile = "qwen3-32b-mi300x"
	scope := aimv1alpha1.AIMResolutionScopeNamespace
	shared := aimv1alpha2.ProfileCacheModeShared
	caches := []aimv1alpha2.AIMProfileCache{
		*cache("z-ready", profile, scope, shared, constants.AIMStatusReady),
		*cache("progressing", profile, scope, shared, constants.AIMStatusProgressing),
		*cache("a-ready", profile, scope, shared, constants.AIMStatusReady),
	}

	got := SelectBestShared(caches, profile, scope)
	if got == nil || got.Name != "a-ready" {
		t.Fatalf("SelectBestShared() = %v, want lexicographically first Ready cache a-ready", got)
	}
}

func TestSelectBestShared_PrefersCurrentStatus(t *testing.T) {
	t.Parallel()

	const profile = "qwen3-32b-mi300x"
	scope := aimv1alpha1.AIMResolutionScopeNamespace
	shared := aimv1alpha2.ProfileCacheModeShared

	stale := cache("a-stale", profile, scope, shared, constants.AIMStatusReady)
	stale.Generation = 2
	stale.Status.ObservedGeneration = 1
	current := cache("z-current", profile, scope, shared, constants.AIMStatusReady)
	current.Generation = 2
	current.Status.ObservedGeneration = 2

	got := SelectBestShared([]aimv1alpha2.AIMProfileCache{*stale, *current}, profile, scope)
	if got == nil || got.Name != current.Name {
		t.Fatalf("SelectBestShared() = %v, want current cache %q", got, current.Name)
	}
}

// TestSelectBestShared_FallsBackToStaleCache pins that a matching cache whose
// status has not caught up (spec just changed, or never observed) is still
// returned when no current one exists, so callers wait for it instead of
// planning a replacement on top of it.
func TestSelectBestShared_FallsBackToStaleCache(t *testing.T) {
	t.Parallel()

	const profile = "qwen3-32b-mi300x"
	scope := aimv1alpha1.AIMResolutionScopeNamespace
	shared := aimv1alpha2.ProfileCacheModeShared

	specChanged := cache("b-spec-changed", profile, scope, shared, constants.AIMStatusReady)
	specChanged.Generation = 2
	unobserved := cache("a-unobserved", profile, scope, shared, "")
	unobserved.Status.ObservedGeneration = 0

	got := SelectBestShared([]aimv1alpha2.AIMProfileCache{*specChanged, *unobserved}, profile, scope)
	if got == nil || got.Name != unobserved.Name {
		t.Fatalf("SelectBestShared() = %v, want stale cache %q chosen by name", got, unobserved.Name)
	}
	if StatusCurrent(got) {
		t.Fatal("StatusCurrent() = true for a cache whose status has not observed its generation")
	}

	other := cache("other-profile", "other", scope, shared, constants.AIMStatusReady)
	other.Generation = 2
	if got := SelectBestShared([]aimv1alpha2.AIMProfileCache{*other}, profile, scope); got != nil {
		t.Fatalf("SelectBestShared() = %q, want nil when no cache matches the profile", got.Name)
	}
}

// TestFindReadyShared_EmptyProfileName pins the guard: an empty profileName never
// lists or matches anything (it would otherwise shadow-match a nameless cache).
func TestFindReadyShared_EmptyProfileName(t *testing.T) {
	t.Parallel()
	c := newClient(t, cache("some-cache", "", aimv1alpha1.AIMResolutionScopeNamespace, aimv1alpha2.ProfileCacheModeShared, constants.AIMStatusReady))
	got, err := FindReadyShared(context.Background(), c, testNamespace, "", aimv1alpha1.AIMResolutionScopeNamespace)
	if err != nil {
		t.Fatalf("FindReadyShared() error = %v", err)
	}
	if got != nil {
		t.Fatalf("FindReadyShared() = %q, want nil for empty profileName", got.Name)
	}
}
