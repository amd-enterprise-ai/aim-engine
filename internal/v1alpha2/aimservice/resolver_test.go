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
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

// testImageRef is a stable image reference used throughout the
// image-shape resolver tests so the lint goconst rule stays satisfied
// (the value itself is arbitrary; only its uniqueness matters).
const testImageRef = "ghcr.io/silogen/aim-dummy:0.2.0"

// Shared identifiers for the TestStickyBinding_* family. Centralising
// these keeps the helpers (resolverProfileBuilderWithUID,
// serviceWithBinding) free of always-the-same parameters that unparam
// would otherwise flag, and keeps goconst quiet about the repeated
// "contender-fp8" literal that appears across most sticky tests.
const (
	stickyTestNamespace    = "ns"
	stickyTestServiceName  = "svc"
	stickyTestBoundUID     = "bound-uid"
	stickyTestAimID        = "aim/one"
	stickyContenderProfile = "contender-fp8"
)

// newResolverTestScheme returns a scheme registered with the AIM v1alpha1
// and v1alpha2 types used by the resolver.
func newResolverTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := aimv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("register v1alpha1: %v", err)
	}
	if err := aimv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatalf("register v1alpha2: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("register corev1: %v", err)
	}
	return scheme
}

// resolverProfileBuilder is a tiny helper to keep the deployable AIMProfile
// fixtures readable. The minimum spec used by these tests is whatever passes
// the CRD-level invariants and exercises the resolver's filter pipeline.
// All test profiles are Primary=true; mixing primary/non-primary on the
// same selector targets is exercised separately by the AIMProfileSet tests.
func resolverProfileBuilder(name, namespace, aimID, precision string, labels map[string]string) *aimv1alpha2.AIMProfile {
	p := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: aimv1alpha2.AIMProfileSpec{
			AIMProfileSpecCommon: aimv1alpha2.AIMProfileSpecCommon{
				AimId:            aimID,
				ModelId:          aimID,
				Engine:           "vllm",
				Precision:        aimv1alpha1.AIMPrecision(precision),
				Type:             aimv1alpha1.AIMProfileTypeOptimized,
				Primary:          true,
				AcceleratorModel: "MI300X",
				AcceleratorType:  aimv1alpha1.AcceleratorTypeGPU,
				AcceleratorCount: 1,
				Image:            "ghcr.io/aim/test:1.0.0",
				ModelSources: []aimv1alpha1.AIMModelSource{
					{ModelID: aimID, SourceURI: "hf://" + aimID},
				},
			},
		},
		Status: aimv1alpha2.AIMProfileStatus{
			Status:     constants.AIMStatusReady,
			Deployable: true,
		},
	}
	return p
}

// deployableProvenanceLabels returns the iteration-1 provenance labels a
// model-derived AIMProfile carries (role=deployable + source-model). Used
// for selector-driven resolver tests.
func deployableProvenanceLabels(sourceModel string) map[string]string {
	return map[string]string{
		constants.LabelKeyProfileRole:      constants.LabelValueProfileRoleDeployable,
		constants.LabelKeyProfileOrigin:    string(aimv1alpha1.ProfileOriginDerived),
		constants.LabelKeySourceModel:      sourceModel,
		constants.LabelKeySourceModelScope: constants.LabelValueSourceModelScopeNamespace,
	}
}

// newResolverClient builds a fake controller-runtime client wired with the
// ProfileAimIdIndex so the resolver's MatchingFields lookup behaves the
// same way as in production. The Model.spec.image indexes feed the
// image-shape resolver path (resolveByImage) so the v1alpha2 quick-start
// tests reproduce the production index behaviour. Other indexes are not
// used by the resolver itself (they feed the watch fan-out, exercised by
// watches_test.go).
func newResolverClient(t *testing.T, scheme *runtime.Scheme, objs ...client.Object) client.Client {
	t.Helper()
	return fakeclient.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithIndex(&aimv1alpha2.AIMProfile{}, aimv1alpha2.ProfileAimIdIndexKey, func(obj client.Object) []string {
			p, ok := obj.(*aimv1alpha2.AIMProfile)
			if !ok || p.Spec.AimId == "" {
				return nil
			}
			return []string{p.Spec.AimId}
		}).
		WithIndex(&aimv1alpha2.AIMClusterProfile{}, aimv1alpha2.ProfileAimIdIndexKey, func(obj client.Object) []string {
			p, ok := obj.(*aimv1alpha2.AIMClusterProfile)
			if !ok || p.Spec.AimId == "" {
				return nil
			}
			return []string{p.Spec.AimId}
		}).
		WithIndex(&aimv1alpha2.AIMModel{}, aimv1alpha1.ModelImageIndexKey, func(obj client.Object) []string {
			m, ok := obj.(*aimv1alpha2.AIMModel)
			if !ok || m.Spec.Image == "" {
				return nil
			}
			return []string{m.Spec.Image}
		}).
		WithIndex(&aimv1alpha2.AIMClusterModel{}, aimv1alpha1.ClusterModelImageIndexKey, func(obj client.Object) []string {
			m, ok := obj.(*aimv1alpha2.AIMClusterModel)
			if !ok || m.Spec.Image == "" {
				return nil
			}
			return []string{m.Spec.Image}
		}).
		Build()
}

// TestResolveByName_NamespaceWins is the simplest happy path: an AIMService
// references an AIMProfile by name and the resolver returns the namespace
// profile.
func TestResolveByName_NamespaceWins(t *testing.T) {
	scheme := newResolverTestScheme(t)
	profile := resolverProfileBuilder("my-profile", "ns", "aim/one", "fp8", nil)
	c := newResolverClient(t, scheme, profile)

	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: "my-profile"},
		},
	}

	ns, cluster, res := resolveProfileCandidates(context.Background(), c, nil, service)
	if !ns.OK() || ns.Value == nil || ns.Value.Name != "my-profile" {
		t.Fatalf("resolver did not return the namespace profile: %+v", ns)
	}
	if cluster.Value != nil {
		t.Fatalf("cluster profile must be empty when namespace match exists")
	}
	if res.shape != resolutionShapeName {
		t.Errorf("shape = %q, want Name", res.shape)
	}
	if res.notFoundReason != "" {
		t.Errorf("unexpected notFoundReason: %q", res.notFoundReason)
	}
}

// TestResolveByName_ClusterFallback covers the v1alpha1-era behaviour where
// the namespace profile is missing and the resolver falls back to the
// cluster profile.
func TestResolveByName_ClusterFallback(t *testing.T) {
	scheme := newResolverTestScheme(t)
	cluster := &aimv1alpha2.AIMClusterProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "global-profile"},
		Spec: aimv1alpha2.AIMClusterProfileSpec{
			AIMProfileSpecCommon: resolverProfileBuilder("global-profile", "", "aim/one", "fp8", nil).Spec.AIMProfileSpecCommon,
		},
		Status: aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady, Deployable: true},
	}
	c := newResolverClient(t, scheme, cluster)

	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: "global-profile"},
		},
	}

	ns, clusterRes, _ := resolveProfileCandidates(context.Background(), c, nil, service)
	if ns.Value != nil {
		t.Fatalf("namespace profile must be empty when only cluster profile exists")
	}
	if !clusterRes.OK() || clusterRes.Value == nil || clusterRes.Value.Name != "global-profile" {
		t.Fatalf("resolver did not fall back to cluster profile: %+v", clusterRes)
	}
}

// TestResolveByName_NotFound asserts the resolver returns a ProfileNotFound
// reason with a tailored message when neither scope has the named profile.
func TestResolveByName_NotFound(t *testing.T) {
	scheme := newResolverTestScheme(t)
	c := newResolverClient(t, scheme)

	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: "missing"},
		},
	}

	_, _, res := resolveProfileCandidates(context.Background(), c, nil, service)
	if res.notFoundReason != aimv1alpha1.AIMServiceReasonProfileNotFound {
		t.Errorf("notFoundReason = %q, want %q", res.notFoundReason, aimv1alpha1.AIMServiceReasonProfileNotFound)
	}
	if res.notFoundMessage == "" {
		t.Errorf("notFoundMessage should be populated")
	}
}

// TestResolveBySelector_NamespaceListErrorIsInfra ensures a transient
// List failure on the namespace AIMProfile list surfaces as a FetchResult
// error (infrastructure) rather than ProfileNotFound. Without this,
// API/RBAC outages were misreported as a terminal user-config failure.
func TestResolveBySelector_NamespaceListErrorIsInfra(t *testing.T) {
	scheme := newResolverTestScheme(t)
	listErr := errors.New("boom: forbidden listing AIMProfiles")
	c := fakeclient.NewClientBuilder().
		WithScheme(scheme).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				if _, ok := list.(*aimv1alpha2.AIMProfileList); ok {
					return listErr
				}
				return c.List(ctx, list, opts...)
			},
		}).
		Build()

	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{
				Selector: &aimv1alpha1.ProfileSelector{AimId: "aim/one"},
			},
		},
	}

	ns, _, res := resolveProfileCandidates(context.Background(), c, nil, service)
	if ns.Error == nil {
		t.Fatalf("namespace FetchResult.Error must be set on List failure, got nil")
	}
	if !errors.Is(ns.Error, listErr) {
		t.Fatalf("namespace FetchResult.Error must wrap original error, got: %v", ns.Error)
	}
	if res.notFoundReason != "" {
		t.Fatalf("List failures must not be reported as ProfileNotFound; got reason=%q", res.notFoundReason)
	}
	if res.listErr == nil {
		t.Fatalf("resolution.listErr should be populated")
	}
}

// TestResolveBySelector_ClusterListErrorIsInfra covers the cluster-scope
// branch: a namespace-only selector that explicitly requests cluster
// scope must surface a cluster List failure as infra, not ProfileNotFound.
func TestResolveBySelector_ClusterListErrorIsInfra(t *testing.T) {
	scheme := newResolverTestScheme(t)
	listErr := errors.New("boom: API server unavailable")
	c := fakeclient.NewClientBuilder().
		WithScheme(scheme).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				if _, ok := list.(*aimv1alpha2.AIMClusterProfileList); ok {
					return listErr
				}
				return c.List(ctx, list, opts...)
			},
		}).
		Build()

	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{
				Selector: &aimv1alpha1.ProfileSelector{
					AimId: "aim/one",
					ModelRef: &aimv1alpha1.ProfileSelectorModelRef{
						Name:  "some-model",
						Scope: aimv1alpha1.ProfileSelectorScopeCluster,
					},
				},
			},
		},
	}

	_, cluster, res := resolveProfileCandidates(context.Background(), c, nil, service)
	if cluster.Error == nil {
		t.Fatalf("cluster FetchResult.Error must be set on List failure, got nil")
	}
	if !errors.Is(cluster.Error, listErr) {
		t.Fatalf("cluster FetchResult.Error must wrap original, got: %v", cluster.Error)
	}
	if res.notFoundReason != "" {
		t.Fatalf("List failures must not be reported as ProfileNotFound; got reason=%q", res.notFoundReason)
	}
}

// TestResolveByModel_DesugarsToModelRefLabel covers shape 2: spec.model.name
// without an explicit selector. The resolver must desugar the model name to
// selector.modelRef.name and match the source-model label stamped on
// model-derived profiles.
func TestResolveByModel_DesugarsToModelRefLabel(t *testing.T) {
	scheme := newResolverTestScheme(t)

	matching := resolverProfileBuilder("derived-profile", "ns", "aim/one", "fp8",
		deployableProvenanceLabels("my-model"))
	otherModel := resolverProfileBuilder("other-model-profile", "ns", "aim/one", "fp8",
		deployableProvenanceLabels("some-other-model"))
	c := newResolverClient(t, scheme, matching, otherModel)

	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Model: &aimv1alpha1.AIMServiceModel{Name: ptr.To("my-model")},
		},
	}

	ns, _, res := resolveProfileCandidates(context.Background(), c, nil, service)
	if !ns.OK() || ns.Value == nil {
		t.Fatalf("resolver did not pick the model-derived profile: %+v / res=%+v", ns, res)
	}
	if ns.Value.Name != "derived-profile" {
		t.Errorf("resolver picked %q, want %q", ns.Value.Name, "derived-profile")
	}
	if res.shape != resolutionShapeModelOnly {
		t.Errorf("shape = %q, want ModelOnly", res.shape)
	}
}

// TestResolveByModelAndSelector_PrecisionNarrowing covers shape 3:
// spec.model.name + spec.profile.selector{precision} must filter the model's
// profile pool by precision before ranking.
func TestResolveByModelAndSelector_PrecisionNarrowing(t *testing.T) {
	scheme := newResolverTestScheme(t)

	fp8 := resolverProfileBuilder("derived-fp8", "ns", "aim/one", "fp8",
		deployableProvenanceLabels("my-model"))
	bf16 := resolverProfileBuilder("derived-bf16", "ns", "aim/one", "bf16",
		deployableProvenanceLabels("my-model"))
	c := newResolverClient(t, scheme, fp8, bf16)

	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Model: &aimv1alpha1.AIMServiceModel{Name: ptr.To("my-model")},
			Profile: &aimv1alpha1.AIMServiceProfileConfig{
				Selector: &aimv1alpha1.ProfileSelector{
					Precision: aimv1alpha1.AIMPrecision("fp8"),
				},
			},
		},
	}

	ns, _, res := resolveProfileCandidates(context.Background(), c, nil, service)
	if !ns.OK() || ns.Value == nil || ns.Value.Name != "derived-fp8" {
		t.Fatalf("expected derived-fp8 winner, got %+v / res=%+v", ns, res)
	}
	if res.shape != resolutionShapeModelSelector {
		t.Errorf("shape = %q, want ModelAndSelector", res.shape)
	}
}

// TestResolveByGlobalSelector_AimIdNarrowing covers shape 4: a selector with
// only aimId set (no model anchor) — the resolver must list profiles by the
// aimId field index and apply the role label filter.
func TestResolveByGlobalSelector_AimIdNarrowing(t *testing.T) {
	scheme := newResolverTestScheme(t)

	target := resolverProfileBuilder("aim-one-profile", "ns", "aim/one", "fp8",
		map[string]string{constants.LabelKeyProfileRole: constants.LabelValueProfileRoleDeployable})
	other := resolverProfileBuilder("aim-two-profile", "ns", "aim/two", "fp8",
		map[string]string{constants.LabelKeyProfileRole: constants.LabelValueProfileRoleDeployable})
	c := newResolverClient(t, scheme, target, other)

	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{
				Selector: &aimv1alpha1.ProfileSelector{AimId: "aim/one"},
			},
		},
	}

	ns, _, res := resolveProfileCandidates(context.Background(), c, nil, service)
	if !ns.OK() || ns.Value == nil || ns.Value.Name != "aim-one-profile" {
		t.Fatalf("expected aim-one-profile winner, got %+v / res=%+v", ns, res)
	}
	if res.shape != resolutionShapeGlobalSelector {
		t.Errorf("shape = %q, want Selector", res.shape)
	}
}

// TestResolveBySelector_BaseProfilesExcluded asserts that profiles labelled
// role=base are filtered out by the resolver via the role-label selector
// (default Deployable means exclude base).
func TestResolveBySelector_BaseProfilesExcluded(t *testing.T) {
	scheme := newResolverTestScheme(t)

	baseProfile := resolverProfileBuilder("base-only", "ns", "aim/one", "fp8",
		map[string]string{constants.LabelKeyProfileRole: constants.LabelValueProfileRoleBase})
	deployable := resolverProfileBuilder("deployable", "ns", "aim/one", "fp8",
		map[string]string{constants.LabelKeyProfileRole: constants.LabelValueProfileRoleDeployable})
	c := newResolverClient(t, scheme, baseProfile, deployable)

	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{
				Selector: &aimv1alpha1.ProfileSelector{AimId: "aim/one"},
			},
		},
	}

	ns, _, _ := resolveProfileCandidates(context.Background(), c, nil, service)
	if ns.Value == nil || ns.Value.Name != "deployable" {
		t.Fatalf("expected deployable winner, got %+v", ns)
	}
}

// TestResolveBySelector_OverlaysFromOtherServicesSkipped guards against a
// global selector adopting another AIMService's overlay AIMProfile, which
// would create a cross-service ownership tangle.
func TestResolveBySelector_OverlaysFromOtherServicesSkipped(t *testing.T) {
	scheme := newResolverTestScheme(t)

	overlay := resolverProfileBuilder("overlay-from-other-service", "ns", "aim/one", "fp8",
		map[string]string{constants.LabelKeyProfileRole: constants.LabelValueProfileRoleDeployable})
	overlay.Annotations = map[string]string{AnnotationOverlayService: "other-service"}
	regular := resolverProfileBuilder("regular-profile", "ns", "aim/one", "fp8",
		map[string]string{constants.LabelKeyProfileRole: constants.LabelValueProfileRoleDeployable})
	c := newResolverClient(t, scheme, overlay, regular)

	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "my-service", Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{
				Selector: &aimv1alpha1.ProfileSelector{AimId: "aim/one"},
			},
		},
	}

	ns, _, _ := resolveProfileCandidates(context.Background(), c, nil, service)
	if ns.Value == nil || ns.Value.Name != "regular-profile" {
		t.Fatalf("expected regular-profile, got %+v", ns)
	}
}

// TestResolveBySelector_AmbiguousEmitsEvent ensures that when multiple
// candidates survive ranking, the resolver picks the deterministic
// alphabetical winner and emits a ProfileSelectorAmbiguous event.
func TestResolveBySelector_AmbiguousEmitsEvent(t *testing.T) {
	scheme := newResolverTestScheme(t)
	labels := map[string]string{constants.LabelKeyProfileRole: constants.LabelValueProfileRoleDeployable}

	// All three profiles have the same primary/type/version so the
	// alphabetical name tie-break decides the winner.
	a := resolverProfileBuilder("aaa", "ns", "aim/one", "fp8", labels)
	b := resolverProfileBuilder("bbb", "ns", "aim/one", "fp8", labels)
	c := resolverProfileBuilder("ccc", "ns", "aim/one", "fp8", labels)
	cli := newResolverClient(t, scheme, a, b, c)
	recorder := record.NewFakeRecorder(10)

	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{
				Selector: &aimv1alpha1.ProfileSelector{AimId: "aim/one"},
			},
		},
	}

	ns, _, res := resolveProfileCandidates(context.Background(), cli, recorder, service)
	if ns.Value == nil || ns.Value.Name != "aaa" {
		t.Fatalf("expected alphabetical winner aaa, got %+v", ns)
	}
	if !res.ambiguous {
		t.Errorf("res.ambiguous should be true with three candidates")
	}
	select {
	case ev := <-recorder.Events:
		if !contains(ev, aimv1alpha1.AIMServiceReasonProfileSelectorAmbiguous) {
			t.Errorf("event missing ProfileSelectorAmbiguous reason: %q", ev)
		}
	default:
		t.Error("expected a ProfileSelectorAmbiguous event")
	}
}

// TestResolveBySelector_AmbiguousEventNamesActualWinner guards the fix
// where the ambiguous-event used to report `candidates[0]` (the
// alphabetically-first candidate) even when status-based ranking
// promoted a different candidate as the actual winner. The event must
// always name the SelectBestPtr winner so users see the profile we are
// about to use.
func TestResolveBySelector_AmbiguousEventNamesActualWinner(t *testing.T) {
	scheme := newResolverTestScheme(t)
	labels := map[string]string{constants.LabelKeyProfileRole: constants.LabelValueProfileRoleDeployable}

	notReady := resolverProfileBuilder("aaa", "ns", "aim/one", "fp8", labels)
	notReady.Status = aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusNotAvailable, Deployable: true}
	ready := resolverProfileBuilder("bbb", "ns", "aim/one", "fp8", labels)
	ready.Status = aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady, Deployable: true}

	cli := newResolverClient(t, scheme, notReady, ready)
	recorder := record.NewFakeRecorder(10)

	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{
				Selector: &aimv1alpha1.ProfileSelector{AimId: "aim/one"},
			},
		},
	}

	ns, _, res := resolveProfileCandidates(context.Background(), cli, recorder, service)
	if ns.Value == nil || ns.Value.Name != "bbb" {
		t.Fatalf("expected status-promoted winner bbb, got %+v", ns.Value)
	}
	if !res.ambiguous {
		t.Fatalf("res.ambiguous should be true with two candidates")
	}
	select {
	case ev := <-recorder.Events:
		if !contains(ev, `picking "bbb"`) {
			t.Fatalf("event must name the actual winner bbb (not alphabetically-first aaa); got: %q", ev)
		}
	default:
		t.Fatal("expected a ProfileSelectorAmbiguous event")
	}
}

// TestResolveBySelector_CleanTiebreakDoesNotEmitAmbiguous guards the
// "tie-at-top" semantic for the ProfileSelectorAmbiguous event: when
// multiple profiles match the selector but the ranker can break the
// tie cleanly at any tier above the alphabetical-name last resort
// (here: metric=latency beats metric=throughput), the event must NOT
// fire — there's no real ambiguity to warn the user about. The old
// behavior was to emit the event whenever len(candidates) > 1, which
// turned every multi-match into noise.
func TestResolveBySelector_CleanTiebreakDoesNotEmitAmbiguous(t *testing.T) {
	scheme := newResolverTestScheme(t)
	labels := map[string]string{constants.LabelKeyProfileRole: constants.LabelValueProfileRoleDeployable}

	// Both profiles are equal at every tier above metric. Metric breaks
	// the tie (latency > throughput), so the resolver should silently
	// pick the latency one without emitting an ambiguous event.
	latency := resolverProfileBuilder("aaa-throughput", "ns", "aim/one", "fp8", labels)
	latency.Name = "aaa-latency" // alphabetically first; would have won the old name tie-break too
	latency.Spec.Metric = aimv1alpha1.AIMMetricLatency
	throughput := resolverProfileBuilder("zzz-throughput", "ns", "aim/one", "fp8", labels)
	throughput.Spec.Metric = aimv1alpha1.AIMMetricThroughput

	cli := newResolverClient(t, scheme, latency, throughput)
	recorder := record.NewFakeRecorder(10)

	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{
				Selector: &aimv1alpha1.ProfileSelector{AimId: "aim/one"},
			},
		},
	}

	ns, _, res := resolveProfileCandidates(context.Background(), cli, recorder, service)
	if ns.Value == nil || ns.Value.Name != "aaa-latency" {
		t.Fatalf("expected ranker to pick latency profile, got %+v", ns.Value)
	}
	if res.ambiguous {
		t.Errorf("res.ambiguous must be false when metric breaks the tie")
	}
	select {
	case ev := <-recorder.Events:
		t.Errorf("no ProfileSelectorAmbiguous event expected on clean tiebreak; got: %q", ev)
	default:
	}
}

// TestResolveBySelector_NamespaceWinsOverCluster keeps the namespace-over-
// cluster precedence the rest of the engine uses: when a namespace AIMProfile
// and a cluster AIMClusterProfile both match, the namespace one wins.
func TestResolveBySelector_NamespaceWinsOverCluster(t *testing.T) {
	scheme := newResolverTestScheme(t)
	labels := map[string]string{constants.LabelKeyProfileRole: constants.LabelValueProfileRoleDeployable}

	nsProfile := resolverProfileBuilder("ns-profile", "ns", "aim/one", "fp8", labels)
	clusterProfile := &aimv1alpha2.AIMClusterProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-profile", Labels: labels},
		Spec: aimv1alpha2.AIMClusterProfileSpec{
			AIMProfileSpecCommon: nsProfile.Spec.AIMProfileSpecCommon,
		},
		Status: aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady, Deployable: true},
	}
	c := newResolverClient(t, scheme, nsProfile, clusterProfile)

	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{
				Selector: &aimv1alpha1.ProfileSelector{AimId: "aim/one"},
			},
		},
	}

	ns, cluster, _ := resolveProfileCandidates(context.Background(), c, nil, service)
	if ns.Value == nil || ns.Value.Name != "ns-profile" {
		t.Fatalf("expected ns-profile winner, got %+v", ns)
	}
	if cluster.Value != nil {
		t.Fatalf("cluster profile must be unset when namespace match exists")
	}
}

// TestBaseProfileRejection_StatusDeployableFalse exercises the unit-level
// base-profile gate in getProfileHealth: a profile with
// status.deployable=false and a structurally non-deployable spec must
// surface BaseProfile.
func TestBaseProfileRejection_StatusDeployableFalse(t *testing.T) {
	baseSpec := &aimv1alpha2.AIMProfileSpecCommon{Image: "img"} // no aimId, no modelSources
	obs := ServiceObservation{
		resolvedProfileSpec:   baseSpec,
		resolvedProfileStatus: &aimv1alpha2.AIMProfileStatus{Deployable: false},
		profileName:           "base",
	}
	health := obs.getProfileHealth()
	if health.Reason != aimv1alpha1.AIMServiceReasonBaseProfile {
		t.Errorf("expected BaseProfile reason, got %q", health.Reason)
	}
	if health.State != constants.AIMStatusFailed {
		t.Errorf("expected Failed state, got %v", health.State)
	}
}

// TestBaseProfileRejection_SpecBasedFallback covers the transient state
// where status.deployable hasn't been stamped yet but the spec is
// structurally deployable. The gate must NOT reject in this case.
func TestBaseProfileRejection_SpecBasedFallback(t *testing.T) {
	deployableSpec := &aimv1alpha2.AIMProfileSpecCommon{
		AimId: "aim/one",
		Image: "img",
		ModelSources: []aimv1alpha1.AIMModelSource{
			{ModelID: "aim/one", SourceURI: "hf://aim/one"},
		},
	}
	obs := ServiceObservation{
		resolvedProfileSpec:   deployableSpec,
		resolvedProfileStatus: &aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady, Deployable: false},
		profileName:           "transient",
	}
	health := obs.getProfileHealth()
	if health.Reason == aimv1alpha1.AIMServiceReasonBaseProfile {
		t.Errorf("spec-based fallback should accept structurally-deployable profile; got BaseProfile")
	}
}

// TestResolveByImage_NoMatchSignalsAutoCreate covers the v1alpha2
// quick-start: a service authored with spec.model.image but no existing
// AIMModel must surface ProfileNotFound and stamp needsAutoModel=true so
// the plan step creates a dedicated AIMModel. The autoModelImage is
// captured for the planner.
func TestResolveByImage_NoMatchSignalsAutoCreate(t *testing.T) {
	scheme := newResolverTestScheme(t)
	c := newResolverClient(t, scheme)

	image := testImageRef
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Model: &aimv1alpha1.AIMServiceModel{Image: ptr.To(image)},
		},
	}

	ns, cluster, res := resolveProfileCandidates(context.Background(), c, nil, service)
	if ns.Value != nil || cluster.Value != nil {
		t.Fatalf("no model should resolve; got ns=%+v cluster=%+v", ns, cluster)
	}
	if res.shape != resolutionShapeModelImage {
		t.Errorf("shape = %q, want ModelImage", res.shape)
	}
	if !res.needsAutoModel {
		t.Errorf("needsAutoModel = false, want true (no existing model matches)")
	}
	if res.autoModelImage != image {
		t.Errorf("autoModelImage = %q, want %q", res.autoModelImage, image)
	}
	if res.notFoundReason != aimv1alpha1.AIMServiceReasonProfileNotFound {
		t.Errorf("notFoundReason = %q, want ProfileNotFound", res.notFoundReason)
	}
	if !contains(res.notFoundMessage, image) {
		t.Errorf("notFoundMessage should mention the image; got %q", res.notFoundMessage)
	}
}

// TestResolveByImage_SingleMatchDesugarsToModelRef asserts the happy
// path: a single AIMModel with the requested image is found, the
// resolver desugars to the by-selector path, and a deployable AIMProfile
// labelled with that model as source wins. needsAutoModel must be false
// so the planner does not create a duplicate model.
func TestResolveByImage_SingleMatchDesugarsToModelRef(t *testing.T) {
	scheme := newResolverTestScheme(t)
	image := testImageRef
	model := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "my-model", Namespace: "ns"},
		Spec:       aimv1alpha1.AIMModelSpec{Image: image},
	}
	matching := resolverProfileBuilder("derived-profile", "ns", "aim/one", "fp8",
		deployableProvenanceLabels("my-model"))
	c := newResolverClient(t, scheme, model, matching)

	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Model: &aimv1alpha1.AIMServiceModel{Image: ptr.To(image)},
		},
	}

	ns, _, res := resolveProfileCandidates(context.Background(), c, nil, service)
	if !ns.OK() || ns.Value == nil || ns.Value.Name != "derived-profile" {
		t.Fatalf("expected derived-profile winner, got %+v / res=%+v", ns, res)
	}
	if res.shape != resolutionShapeModelImage {
		t.Errorf("shape = %q, want ModelImage (preserved through desugar)", res.shape)
	}
	if res.needsAutoModel {
		t.Errorf("needsAutoModel must be false when an AIMModel already matches the image")
	}
}

// TestResolveByImage_ClusterMatchDesugars confirms a cluster-scoped
// AIMClusterModel match also desugars and resolves through the cluster
// profile pool. Namespace match is absent so the cluster fallback fires.
func TestResolveByImage_ClusterMatchDesugars(t *testing.T) {
	scheme := newResolverTestScheme(t)
	image := testImageRef
	clusterModel := &aimv1alpha2.AIMClusterModel{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-model"},
		Spec:       aimv1alpha1.AIMModelSpec{Image: image},
	}
	clusterLabels := deployableProvenanceLabels("cluster-model")
	clusterLabels[constants.LabelKeySourceModelScope] = constants.LabelValueSourceModelScopeCluster
	clusterProfile := &aimv1alpha2.AIMClusterProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-derived", Labels: clusterLabels},
		Spec: aimv1alpha2.AIMClusterProfileSpec{
			AIMProfileSpecCommon: resolverProfileBuilder("cluster-derived", "", "aim/one", "fp8", nil).Spec.AIMProfileSpecCommon,
		},
		Status: aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady, Deployable: true},
	}
	c := newResolverClient(t, scheme, clusterModel, clusterProfile)

	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Model: &aimv1alpha1.AIMServiceModel{Image: ptr.To(image)},
		},
	}

	ns, cluster, res := resolveProfileCandidates(context.Background(), c, nil, service)
	if ns.Value != nil {
		t.Fatalf("namespace result should be empty for cluster-only match; got %+v", ns)
	}
	if !cluster.OK() || cluster.Value == nil || cluster.Value.Name != "cluster-derived" {
		t.Fatalf("expected cluster-derived winner, got %+v / res=%+v", cluster, res)
	}
	if res.needsAutoModel {
		t.Errorf("needsAutoModel must be false when a cluster model matches the image")
	}
}

// TestResolveByImage_NamespaceWinsOverCluster asserts namespace-over-
// cluster precedence in the image path: a namespace AIMModel and a
// cluster AIMClusterModel with the same image must NOT be reported as
// ambiguous; the resolver picks the namespace model silently.
func TestResolveByImage_NamespaceWinsOverCluster(t *testing.T) {
	scheme := newResolverTestScheme(t)
	image := testImageRef
	nsModel := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "ns-model", Namespace: "ns"},
		Spec:       aimv1alpha1.AIMModelSpec{Image: image},
	}
	clusterModel := &aimv1alpha2.AIMClusterModel{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-model"},
		Spec:       aimv1alpha1.AIMModelSpec{Image: image},
	}
	nsProfile := resolverProfileBuilder("ns-profile", "ns", "aim/one", "fp8",
		deployableProvenanceLabels("ns-model"))
	c := newResolverClient(t, scheme, nsModel, clusterModel, nsProfile)

	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Model: &aimv1alpha1.AIMServiceModel{Image: ptr.To(image)},
		},
	}

	ns, cluster, res := resolveProfileCandidates(context.Background(), c, nil, service)
	if cluster.Value != nil {
		t.Fatalf("cluster profile must not win when namespace model exists; got %+v", cluster)
	}
	if !ns.OK() || ns.Value == nil || ns.Value.Name != "ns-profile" {
		t.Fatalf("expected ns-profile winner, got %+v / res=%+v", ns, res)
	}
	if res.needsAutoModel {
		t.Errorf("needsAutoModel must be false; namespace model already exists")
	}
}

// TestResolveByImage_MultipleMatchesAmbiguous asserts the resolver
// refuses to guess when two namespace AIMModels share an image. The
// user-facing message must mention `spec.model.name` so the user knows
// how to pin.
func TestResolveByImage_MultipleMatchesAmbiguous(t *testing.T) {
	scheme := newResolverTestScheme(t)
	image := testImageRef
	a := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "model-a", Namespace: "ns"},
		Spec:       aimv1alpha1.AIMModelSpec{Image: image},
	}
	b := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "model-b", Namespace: "ns"},
		Spec:       aimv1alpha1.AIMModelSpec{Image: image},
	}
	c := newResolverClient(t, scheme, a, b)

	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Model: &aimv1alpha1.AIMServiceModel{Image: ptr.To(image)},
		},
	}

	ns, cluster, res := resolveProfileCandidates(context.Background(), c, nil, service)
	if ns.Value != nil || cluster.Value != nil {
		t.Fatalf("ambiguous image must not resolve; got ns=%+v cluster=%+v", ns, cluster)
	}
	if res.needsAutoModel {
		t.Errorf("needsAutoModel must be false on ambiguity (creating another model would worsen the problem)")
	}
	if res.notFoundReason != aimv1alpha1.AIMServiceReasonProfileNotFound {
		t.Errorf("notFoundReason = %q, want ProfileNotFound", res.notFoundReason)
	}
	if !contains(res.notFoundMessage, "spec.model.name") {
		t.Errorf("notFoundMessage must guide users to pin via spec.model.name; got %q", res.notFoundMessage)
	}
	if !contains(res.notFoundMessage, "model-a") || !contains(res.notFoundMessage, "model-b") {
		t.Errorf("notFoundMessage should list the matching model names; got %q", res.notFoundMessage)
	}
}

// TestResolveByImage_WithSelectorOverlayNarrows confirms the image path
// honours a precision selector overlay: spec.model.image picks the
// AIMModel anchor, then spec.profile.selector.precision narrows the
// resolved AIMProfile pool. Mirrors the by-name + selector behaviour
// users get via spec.model.name.
func TestResolveByImage_WithSelectorOverlayNarrows(t *testing.T) {
	scheme := newResolverTestScheme(t)
	image := testImageRef
	model := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "my-model", Namespace: "ns"},
		Spec:       aimv1alpha1.AIMModelSpec{Image: image},
	}
	fp8 := resolverProfileBuilder("derived-fp8", "ns", "aim/one", "fp8",
		deployableProvenanceLabels("my-model"))
	bf16 := resolverProfileBuilder("derived-bf16", "ns", "aim/one", "bf16",
		deployableProvenanceLabels("my-model"))
	c := newResolverClient(t, scheme, model, fp8, bf16)

	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Model: &aimv1alpha1.AIMServiceModel{Image: ptr.To(image)},
			Profile: &aimv1alpha1.AIMServiceProfileConfig{
				Selector: &aimv1alpha1.ProfileSelector{
					Precision: aimv1alpha1.AIMPrecision("fp8"),
				},
			},
		},
	}

	ns, _, res := resolveProfileCandidates(context.Background(), c, nil, service)
	if !ns.OK() || ns.Value == nil || ns.Value.Name != "derived-fp8" {
		t.Fatalf("expected derived-fp8 winner via image+selector, got %+v / res=%+v", ns, res)
	}
	if res.shape != resolutionShapeModelImage {
		t.Errorf("shape = %q, want ModelImage (preserved through desugar)", res.shape)
	}
}

// TestResolveByImage_ImageOnlyServiceWithoutMatch_OriginalServiceUnchanged
// guards the in-memory desugar in resolveByImage: the deep-copy must
// prevent the resolver from mutating the caller's AIMService spec, even
// if downstream filtering modifies the synthetic copy in some future
// refactor.
func TestResolveByImage_ImageOnlyServiceWithoutMatch_OriginalServiceUnchanged(t *testing.T) {
	scheme := newResolverTestScheme(t)
	image := testImageRef
	model := &aimv1alpha2.AIMModel{
		ObjectMeta: metav1.ObjectMeta{Name: "my-model", Namespace: "ns"},
		Spec:       aimv1alpha1.AIMModelSpec{Image: image},
	}
	matching := resolverProfileBuilder("derived-profile", "ns", "aim/one", "fp8",
		deployableProvenanceLabels("my-model"))
	c := newResolverClient(t, scheme, model, matching)

	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Model: &aimv1alpha1.AIMServiceModel{Image: ptr.To(image)},
		},
	}

	_, _, _ = resolveProfileCandidates(context.Background(), c, nil, service)
	if service.Spec.Model == nil || service.Spec.Model.Image == nil {
		t.Fatalf("original service.Spec.Model.Image must be preserved; got %+v", service.Spec.Model)
	}
	if service.Spec.Model.Name != nil {
		t.Errorf("original service.Spec.Model.Name must remain unset; got %v", *service.Spec.Model.Name)
	}
	if *service.Spec.Model.Image != image {
		t.Errorf("image mutated: got %q, want %q", *service.Spec.Model.Image, image)
	}
}

// TestGenerateAutoModelName_Deterministic asserts the dedicated-model
// name is deterministic per (service, image), so repeated reconciles
// converge on the same object via SSA (no churn).
func TestGenerateAutoModelName_Deterministic(t *testing.T) {
	name1, err := GenerateAutoModelName("svc", testImageRef)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	name2, err := GenerateAutoModelName("svc", testImageRef)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name1 != name2 {
		t.Errorf("non-deterministic name: %q vs %q", name1, name2)
	}
	if !contains(name1, "svc-auto-") {
		t.Errorf("name should carry the svc-auto- prefix; got %q", name1)
	}
}

// TestGenerateAutoModelName_DifferentInputsDifferentNames covers the
// uniqueness story: the dedicated model name must differ per
// (service, image) tuple so two services with different images do not
// collide on a single auto-model resource.
func TestGenerateAutoModelName_DifferentInputsDifferentNames(t *testing.T) {
	a, _ := GenerateAutoModelName("svc-a", testImageRef)
	b, _ := GenerateAutoModelName("svc-b", testImageRef)
	c, _ := GenerateAutoModelName("svc-a", "ghcr.io/silogen/aim-dummy:0.3.0")

	if a == b {
		t.Errorf("different services should produce different names; both got %q", a)
	}
	if a == c {
		t.Errorf("different images should produce different names; both got %q", a)
	}
}

// TestBuildAutoCreatedAIMModel_ShapeAndProvenance covers the structural
// invariants the v1alpha2 model controller relies on:
//   - apiVersion=v1alpha2 (otherwise the model controller would emit
//     AIMServiceTemplates instead of AIMProfiles and the resolver would
//     never find a match).
//   - origin=auto-generated label so `kubectl get aimmodel -l 'origin!=
//     auto-generated'` hides it.
//   - the AnnotationAutoModelSource annotation surfaces the provenance.
//   - imagePullSecrets / serviceAccountName are inherited from the
//     authoring service so private-registry pulls work.
//   - no forbidden v1alpha2 fields (runtimeConfigName, env, custom, ...)
//     are populated.
func TestBuildAutoCreatedAIMModel_ShapeAndProvenance(t *testing.T) {
	image := testImageRef
	svc := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "chat", Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			ImagePullSecrets: []corev1.LocalObjectReference{
				{Name: "ghcr-pull"},
			},
			ServiceAccountName: "aim-puller",
			Model:              &aimv1alpha1.AIMServiceModel{Image: ptr.To(image)},
		},
	}

	model, err := buildAutoCreatedAIMModel(svc, image)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if model.APIVersion != aimv1alpha2.GroupVersion.String() {
		t.Errorf("apiVersion = %q, want %q", model.APIVersion, aimv1alpha2.GroupVersion.String())
	}
	if model.Kind != "AIMModel" {
		t.Errorf("kind = %q, want AIMModel", model.Kind)
	}
	if model.Namespace != "ns" {
		t.Errorf("namespace = %q, want ns", model.Namespace)
	}
	if !contains(model.Name, "chat-auto-") {
		t.Errorf("name should begin with chat-auto-; got %q", model.Name)
	}
	if model.Labels[constants.LabelKeyOrigin] != constants.LabelValueOriginAutoGenerated {
		t.Errorf("origin label = %q, want %q",
			model.Labels[constants.LabelKeyOrigin], constants.LabelValueOriginAutoGenerated)
	}
	if model.Annotations[AnnotationAutoModelSource] != AutoModelSourceSpecModelImage {
		t.Errorf("auto-source annotation = %q, want %q",
			model.Annotations[AnnotationAutoModelSource], AutoModelSourceSpecModelImage)
	}
	if model.Spec.Image != image {
		t.Errorf("spec.image = %q, want %q", model.Spec.Image, image)
	}
	if len(model.Spec.ImagePullSecrets) != 1 || model.Spec.ImagePullSecrets[0].Name != "ghcr-pull" {
		t.Errorf("imagePullSecrets not inherited from service: %+v", model.Spec.ImagePullSecrets)
	}
	if model.Spec.ServiceAccountName != "aim-puller" {
		t.Errorf("serviceAccountName not inherited: got %q", model.Spec.ServiceAccountName)
	}
	// v1alpha2 forbids these on a freshly-created object; the resolver
	// path must not populate any of them.
	if model.Spec.AimId != "" {
		t.Errorf("spec.aimId must be empty; got %q", model.Spec.AimId)
	}
	if model.Spec.Name != "" {
		t.Errorf("spec.runtimeConfigName must be empty on v1alpha2 auto-models; got %q",
			model.Spec.Name)
	}
	if len(model.Spec.Env) != 0 {
		t.Errorf("spec.env must be empty on v1alpha2 auto-models; got %+v", model.Spec.Env)
	}
	if model.Spec.Custom != nil {
		t.Errorf("spec.custom must be empty on v1alpha2 auto-models; got %+v", model.Spec.Custom)
	}
	if len(model.Spec.ModelSources) != 0 {
		t.Errorf("spec.modelSources must be empty on v1alpha2 auto-models; got %+v", model.Spec.ModelSources)
	}
	if model.Spec.Profiles != nil {
		t.Errorf("spec.profiles must be empty on v1alpha2 image-based auto-models; got %+v", model.Spec.Profiles)
	}
}

// TestShouldPlanAutoCreatedModel_OnlyOnImageShapeAndSignal ensures the
// gate fires only when the resolver signalled needsAutoModel via the
// image shape with no list error. False positives would create models
// for services that already resolved, or for shapes that don't carry an
// image at all.
func TestShouldPlanAutoCreatedModel_OnlyOnImageShapeAndSignal(t *testing.T) {
	tests := []struct {
		name string
		res  profileResolution
		want bool
	}{
		{
			name: "image shape + signal + no list error => plan",
			res: profileResolution{
				shape:          resolutionShapeModelImage,
				needsAutoModel: true,
				autoModelImage: testImageRef,
			},
			want: true,
		},
		{
			name: "image shape but no signal (existing model resolved) => skip",
			res: profileResolution{
				shape:          resolutionShapeModelImage,
				needsAutoModel: false,
				autoModelImage: testImageRef,
			},
			want: false,
		},
		{
			name: "image shape with list error => skip (retry, don't race)",
			res: profileResolution{
				shape:          resolutionShapeModelImage,
				needsAutoModel: true,
				autoModelImage: testImageRef,
				listErr:        errors.New("boom"),
			},
			want: false,
		},
		{
			name: "by-name shape with signal somehow set => skip (programming error guard)",
			res: profileResolution{
				shape:          resolutionShapeModelOnly,
				needsAutoModel: true,
				autoModelImage: testImageRef,
			},
			want: false,
		},
		{
			name: "image shape with empty image string => skip",
			res: profileResolution{
				shape:          resolutionShapeModelImage,
				needsAutoModel: true,
			},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			obs := ServiceObservation{}
			obs.resolution = tc.res
			if got := shouldPlanAutoCreatedModel(obs); got != tc.want {
				t.Errorf("shouldPlanAutoCreatedModel = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestProfileLess exercises the full tier order of the resolver ranker.
// Each case isolates one tier by making everything except that tier
// identical, then a final case stacks every tier in opposing directions
// to confirm the earlier tier wins.
func TestProfileLess(t *testing.T) {
	type spec = aimv1alpha2.AIMProfileSpecCommon

	tests := []struct {
		name string
		a, b spec
		aVer string
		bVer string
		// aWins reports the expected return of profileLess(a, b).
		aWins bool
	}{
		{
			name:  "tier 1: primary beats non-primary",
			a:     spec{Primary: true, Type: aimv1alpha1.AIMProfileTypeUnoptimized},
			b:     spec{Primary: false, Type: aimv1alpha1.AIMProfileTypeOptimized},
			aWins: true,
		},
		{
			name:  "tier 2: optimized beats general",
			a:     spec{Type: aimv1alpha1.AIMProfileTypeOptimized, AcceleratorModel: "W7900"},
			b:     spec{Type: aimv1alpha1.AIMProfileTypeGeneral, AcceleratorModel: "MI325X"},
			aWins: true,
		},
		{
			name:  "tier 3: MI325X beats MI300X",
			a:     spec{Type: aimv1alpha1.AIMProfileTypeOptimized, AcceleratorModel: "MI325X", Metric: "throughput"},
			b:     spec{Type: aimv1alpha1.AIMProfileTypeOptimized, AcceleratorModel: "MI300X", Metric: "latency"},
			aWins: true,
		},
		{
			name:  "tier 3: known model beats unknown model",
			a:     spec{Type: aimv1alpha1.AIMProfileTypeOptimized, AcceleratorModel: "MI210"},
			b:     spec{Type: aimv1alpha1.AIMProfileTypeOptimized, AcceleratorModel: "MI999X"},
			aWins: true,
		},
		{
			name:  "tier 4: latency beats throughput",
			a:     spec{Type: aimv1alpha1.AIMProfileTypeOptimized, AcceleratorModel: "MI300X", Metric: "latency", Precision: "fp16"},
			b:     spec{Type: aimv1alpha1.AIMProfileTypeOptimized, AcceleratorModel: "MI300X", Metric: "throughput", Precision: "fp4"},
			aWins: true,
		},
		{
			name:  "tier 5: fp8 beats fp16",
			a:     spec{Type: aimv1alpha1.AIMProfileTypeOptimized, AcceleratorModel: "MI300X", Metric: "latency", Precision: "fp8", AcceleratorCount: 4},
			b:     spec{Type: aimv1alpha1.AIMProfileTypeOptimized, AcceleratorModel: "MI300X", Metric: "latency", Precision: "fp16", AcceleratorCount: 1},
			aWins: true,
		},
		{
			name: "tier 6: smaller count wins on tie",
			a: spec{
				Type: aimv1alpha1.AIMProfileTypeOptimized, AcceleratorModel: "MI300X", Metric: "latency",
				Precision: "fp8", AcceleratorCount: 1, AcceleratorType: aimv1alpha1.AcceleratorTypeGPU,
			},
			b: spec{
				Type: aimv1alpha1.AIMProfileTypeOptimized, AcceleratorModel: "MI300X", Metric: "latency",
				Precision: "fp8", AcceleratorCount: 4, AcceleratorType: aimv1alpha1.AcceleratorTypeGPU,
			},
			aWins: true,
		},
		{
			name: "tier 6: zero count loses to explicit count",
			a: spec{
				Type: aimv1alpha1.AIMProfileTypeOptimized, AcceleratorModel: "MI300X", Metric: "latency",
				Precision: "fp8", AcceleratorCount: 8,
			},
			b: spec{
				Type: aimv1alpha1.AIMProfileTypeOptimized, AcceleratorModel: "MI300X", Metric: "latency",
				Precision: "fp8", AcceleratorCount: 0,
			},
			aWins: true,
		},
		{
			name: "tier 7: gpu beats cpu on tie",
			a: spec{
				Type: aimv1alpha1.AIMProfileTypeOptimized, AcceleratorModel: "MI300X", Metric: "latency",
				Precision: "fp8", AcceleratorCount: 1, AcceleratorType: aimv1alpha1.AcceleratorTypeGPU,
			},
			b: spec{
				Type: aimv1alpha1.AIMProfileTypeOptimized, AcceleratorModel: "MI300X", Metric: "latency",
				Precision: "fp8", AcceleratorCount: 1, AcceleratorType: aimv1alpha1.AcceleratorTypeCPU,
			},
			aWins: true,
		},
		{
			name: "tier 8: higher version wins on tie",
			a: spec{
				Type: aimv1alpha1.AIMProfileTypeOptimized, AcceleratorModel: "MI300X", Metric: "latency",
				Precision: "fp8", AcceleratorCount: 1, AcceleratorType: aimv1alpha1.AcceleratorTypeGPU,
			},
			b: spec{
				Type: aimv1alpha1.AIMProfileTypeOptimized, AcceleratorModel: "MI300X", Metric: "latency",
				Precision: "fp8", AcceleratorCount: 1, AcceleratorType: aimv1alpha1.AcceleratorTypeGPU,
			},
			aVer:  "2.0.0",
			bVer:  "1.9.0",
			aWins: true,
		},
		{
			name: "tier 9: alphabetical name as last resort",
			a: spec{
				Type: aimv1alpha1.AIMProfileTypeOptimized, AcceleratorModel: "MI300X", Metric: "latency",
				Precision: "fp8", AcceleratorCount: 1, AcceleratorType: aimv1alpha1.AcceleratorTypeGPU,
			},
			b: spec{
				Type: aimv1alpha1.AIMProfileTypeOptimized, AcceleratorModel: "MI300X", Metric: "latency",
				Precision: "fp8", AcceleratorCount: 1, AcceleratorType: aimv1alpha1.AcceleratorTypeGPU,
			},
			aVer:  "1.0.0",
			bVer:  "1.0.0",
			aWins: true, // "alpha" < "beta" decided by names below
		},
		{
			name:  "stacked: earlier tier wins despite later tiers favoring b",
			a:     spec{Primary: true, Type: aimv1alpha1.AIMProfileTypeUnoptimized, AcceleratorModel: "W7900", Metric: "throughput", Precision: "fp32", AcceleratorCount: 8, AcceleratorType: aimv1alpha1.AcceleratorTypeCPU},
			b:     spec{Primary: false, Type: aimv1alpha1.AIMProfileTypeOptimized, AcceleratorModel: "MI325X", Metric: "latency", Precision: "fp4", AcceleratorCount: 1, AcceleratorType: aimv1alpha1.AcceleratorTypeGPU},
			aWins: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			aName, bName := "alpha", "beta"
			if got := profileLess(&tc.a, &tc.b, tc.aVer, tc.bVer, aName, bName); got != tc.aWins {
				t.Errorf("profileLess = %v, want %v", got, tc.aWins)
			}
			// Symmetry: swapping inputs flips the result (unless equal).
			if got := profileLess(&tc.b, &tc.a, tc.bVer, tc.aVer, bName, aName); got == tc.aWins {
				t.Errorf("profileLess not antisymmetric for case %q", tc.name)
			}
		})
	}
}

// contains is a tiny helper for asserting on the FakeRecorder's untyped
// event strings (format: "<TYPE> <REASON> <MESSAGE>").
func contains(s, sub string) bool {
	return len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// drainRecorder returns every event the fake recorder has buffered so
// tests can assert on the full set instead of just the first one. The
// resolver may emit multiple events per call (e.g. ProfileSelectorAmbiguous
// + ProfileRebound on a contested rebind), and select-default semantics
// would silently drop everything after the first.
func drainRecorder(r *record.FakeRecorder) []string {
	var out []string
	for {
		select {
		case ev := <-r.Events:
			out = append(out, ev)
		default:
			return out
		}
	}
}

// resolverProfileBuilderWithUID returns a profile with an explicit UID so
// sticky-binding tests can store the same UID in status.resolvedProfile
// and confirm the resolver actually re-fetched the bound profile.
//
// Namespace, aimID, and the underlying fixture shape are all fixed for
// this helper because every sticky test in the file shares one ns and
// one aimId; threading either through every call would trip the unparam
// linter without buying any flexibility. The variable axes (name,
// precision, labels, uid) are the ones that actually distinguish the
// fixtures across the TestStickyBinding_* family.
func resolverProfileBuilderWithUID(name, precision string,
	labels map[string]string, uid string,
) *aimv1alpha2.AIMProfile {
	p := resolverProfileBuilder(name, stickyTestNamespace, stickyTestAimID, precision, labels)
	p.UID = types.UID(uid)
	return p
}

// serviceWithBinding constructs an AIMService whose status.resolvedProfile
// points at the named namespace-scoped profile. Mirrors what the reconciler
// would have written after a previous successful resolution and lets the
// sticky-binding tests start from a "service already bound" state.
//
// Service name, namespace, and bound UID are all fixed (stickyTestServiceName
// / stickyTestNamespace / stickyTestBoundUID) because no test in this
// family varies them — only the bound profile name and selector differ
// across call sites.
func serviceWithBinding(selector *aimv1alpha1.ProfileSelector,
	boundName string,
) *aimv1alpha1.AIMService {
	return &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: stickyTestServiceName, Namespace: stickyTestNamespace},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Selector: selector},
		},
		Status: aimv1alpha1.AIMServiceStatus{
			ResolvedProfile: &aimv1alpha1.AIMResolvedReference{
				Name:      boundName,
				Namespace: stickyTestNamespace,
				Scope:     aimv1alpha1.AIMResolutionScopeNamespace,
				UID:       types.UID(stickyTestBoundUID),
			},
		},
	}
}

// TestStickyBinding_KeepsBoundProfileWhenHigherRankedAppears is the central
// behaviour guarantee: once status.resolvedProfile is set, the resolver
// keeps using that profile even when the candidate set grows to include a
// strictly higher-ranked alternative. This mirrors the real-world scenario
// of a second AIMModel landing in the same namespace with overlapping
// aimId and re-priming the ranker — without stickiness, the running
// service would silently rebind to a different precision / weights /
// image, surprising the operator and orphaning the existing cache.
func TestStickyBinding_KeepsBoundProfileWhenHigherRankedAppears(t *testing.T) {
	scheme := newResolverTestScheme(t)
	labels := map[string]string{constants.LabelKeyProfileRole: constants.LabelValueProfileRoleDeployable}

	// Originally-bound profile (fp16).
	bound := resolverProfileBuilderWithUID("bound-fp16", "fp16", labels, stickyTestBoundUID)
	// Strictly higher-ranked alternative (fp8 — beats fp16 at the
	// precision tier when everything else ties). Without the sticky
	// short-circuit, the resolver would adopt this profile.
	contender := resolverProfileBuilderWithUID(stickyContenderProfile, "fp8", labels, "contender-uid")
	c := newResolverClient(t, scheme, bound, contender)

	recorder := record.NewFakeRecorder(10)
	service := serviceWithBinding(
		&aimv1alpha1.ProfileSelector{AimId: "aim/one"},
		"bound-fp16")

	ns, _, _ := resolveProfileCandidates(context.Background(), c, recorder, service)
	if ns.Value == nil || ns.Value.Name != "bound-fp16" {
		t.Fatalf("sticky binding should keep bound-fp16 even when contender-fp8 outranks it; got %+v", ns.Value)
	}
	for _, ev := range drainRecorder(recorder) {
		if contains(ev, aimv1alpha1.AIMServiceReasonProfileRebound) {
			t.Errorf("unexpected ProfileRebound event on sticky-keep path: %q", ev)
		}
	}
}

// TestStickyBinding_RebindsWhenBoundProfileDeleted exercises the
// natural-rebind path: the previously-bound profile is gone (deleted out
// from under the service), the resolver must fall back to a fresh rank
// over the remaining candidates and surface the transition via a
// ProfileRebound event so operators can audit the change.
func TestStickyBinding_RebindsWhenBoundProfileDeleted(t *testing.T) {
	scheme := newResolverTestScheme(t)
	labels := map[string]string{constants.LabelKeyProfileRole: constants.LabelValueProfileRoleDeployable}

	// Only the contender exists; the previously-bound profile is missing
	// from the cluster (i.e. someone deleted it).
	contender := resolverProfileBuilderWithUID(stickyContenderProfile, "fp8", labels, "contender-uid")
	c := newResolverClient(t, scheme, contender)

	recorder := record.NewFakeRecorder(10)
	service := serviceWithBinding(
		&aimv1alpha1.ProfileSelector{AimId: "aim/one"},
		"bound-fp16")

	ns, _, _ := resolveProfileCandidates(context.Background(), c, recorder, service)
	if ns.Value == nil || ns.Value.Name != stickyContenderProfile {
		t.Fatalf("expected rebind to %s after bound profile deleted; got %+v", stickyContenderProfile, ns.Value)
	}
	if !reboundEventEmitted(t, drainRecorder(recorder), "bound-fp16", stickyContenderProfile) {
		t.Errorf("expected a ProfileRebound event naming bound-fp16 -> %s", stickyContenderProfile)
	}
}

// TestStickyBinding_RebindsWhenSelectorNoLongerMatches covers user-intent
// changes: the bound profile still exists, but the service's selector has
// been narrowed (here: a precision filter excludes the previously-bound
// fp16 profile). The sticky short-circuit must defer to the new intent
// and re-rank.
func TestStickyBinding_RebindsWhenSelectorNoLongerMatches(t *testing.T) {
	scheme := newResolverTestScheme(t)
	labels := map[string]string{constants.LabelKeyProfileRole: constants.LabelValueProfileRoleDeployable}

	bound := resolverProfileBuilderWithUID("bound-fp16", "fp16", labels, stickyTestBoundUID)
	contender := resolverProfileBuilderWithUID(stickyContenderProfile, "fp8", labels, "contender-uid")
	c := newResolverClient(t, scheme, bound, contender)

	recorder := record.NewFakeRecorder(10)
	// Selector now narrows to fp8; the previously-bound fp16 no longer
	// matches even though it still exists.
	service := serviceWithBinding(
		&aimv1alpha1.ProfileSelector{AimId: "aim/one", Precision: "fp8"},
		"bound-fp16")

	ns, _, _ := resolveProfileCandidates(context.Background(), c, recorder, service)
	if ns.Value == nil || ns.Value.Name != stickyContenderProfile {
		t.Fatalf("expected rebind to %s after selector mismatch; got %+v", stickyContenderProfile, ns.Value)
	}
	if !reboundEventEmitted(t, drainRecorder(recorder), "bound-fp16", stickyContenderProfile) {
		t.Errorf("expected a ProfileRebound event naming bound-fp16 -> %s (selector mismatch)", stickyContenderProfile)
	}
}

// TestStickyBinding_ForceRebindAnnotationOverrides confirms the explicit
// opt-out path: with the force-rebind annotation set, the resolver
// ignores the still-valid sticky binding and re-ranks. The event surfaces
// the rebind including the annotation as the reason.
func TestStickyBinding_ForceRebindAnnotationOverrides(t *testing.T) {
	scheme := newResolverTestScheme(t)
	labels := map[string]string{constants.LabelKeyProfileRole: constants.LabelValueProfileRoleDeployable}

	bound := resolverProfileBuilderWithUID("bound-fp16", "fp16", labels, stickyTestBoundUID)
	contender := resolverProfileBuilderWithUID(stickyContenderProfile, "fp8", labels, "contender-uid")
	c := newResolverClient(t, scheme, bound, contender)

	recorder := record.NewFakeRecorder(10)
	service := serviceWithBinding(
		&aimv1alpha1.ProfileSelector{AimId: "aim/one"},
		"bound-fp16")
	service.Annotations = map[string]string{constants.AnnotationForceRebind: "now"}

	ns, _, _ := resolveProfileCandidates(context.Background(), c, recorder, service)
	if ns.Value == nil || ns.Value.Name != stickyContenderProfile {
		t.Fatalf("expected rebind to %s with force-rebind annotation; got %+v", stickyContenderProfile, ns.Value)
	}
	events := drainRecorder(recorder)
	if !reboundEventEmitted(t, events, "bound-fp16", stickyContenderProfile) {
		t.Errorf("expected a ProfileRebound event naming bound-fp16 -> %s (force-rebind)", stickyContenderProfile)
	}
	// Reason text should reference the annotation so operators can
	// correlate the rebind with their explicit action.
	if !anyEventContains(events, "force-rebind") {
		t.Error("expected the ProfileRebound event message to reference 'force-rebind'")
	}
}

// TestStickyBinding_NoEventOnFirstBinding asserts the resolver stays
// quiet when there's no prior binding to compare against — ProfileRebound
// is for transitions, not for the initial pick (which is already covered
// by the framework's ProfileResolved event).
func TestStickyBinding_NoEventOnFirstBinding(t *testing.T) {
	scheme := newResolverTestScheme(t)
	labels := map[string]string{constants.LabelKeyProfileRole: constants.LabelValueProfileRoleDeployable}

	only := resolverProfileBuilder("only", "ns", "aim/one", "fp8", labels)
	c := newResolverClient(t, scheme, only)

	recorder := record.NewFakeRecorder(10)
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{
				Selector: &aimv1alpha1.ProfileSelector{AimId: "aim/one"},
			},
		},
		// No Status.ResolvedProfile → first binding.
	}

	ns, _, _ := resolveProfileCandidates(context.Background(), c, recorder, service)
	if ns.Value == nil || ns.Value.Name != "only" {
		t.Fatalf("expected first-bind to pick 'only'; got %+v", ns.Value)
	}
	for _, ev := range drainRecorder(recorder) {
		if contains(ev, aimv1alpha1.AIMServiceReasonProfileRebound) {
			t.Errorf("ProfileRebound should not fire on first binding: %q", ev)
		}
	}
}

// TestStickyBinding_NoEventWhenForceRebindLandsOnSameWinner guards
// against false-positive rebind events: when the user toggles
// force-rebind but the ranker re-picks the same profile (e.g. because
// it's still the best candidate), no transition occurred and no event
// should fire.
func TestStickyBinding_NoEventWhenForceRebindLandsOnSameWinner(t *testing.T) {
	scheme := newResolverTestScheme(t)
	labels := map[string]string{constants.LabelKeyProfileRole: constants.LabelValueProfileRoleDeployable}

	bound := resolverProfileBuilderWithUID("only", "fp8", labels, stickyTestBoundUID)
	c := newResolverClient(t, scheme, bound)

	recorder := record.NewFakeRecorder(10)
	service := serviceWithBinding(
		&aimv1alpha1.ProfileSelector{AimId: "aim/one"},
		"only")
	service.Annotations = map[string]string{constants.AnnotationForceRebind: "now"}

	ns, _, _ := resolveProfileCandidates(context.Background(), c, recorder, service)
	if ns.Value == nil || ns.Value.Name != "only" {
		t.Fatalf("expected ranker to re-pick 'only'; got %+v", ns.Value)
	}
	for _, ev := range drainRecorder(recorder) {
		if contains(ev, aimv1alpha1.AIMServiceReasonProfileRebound) {
			t.Errorf("ProfileRebound should not fire when winner equals previous binding: %q", ev)
		}
	}
}

// TestStickyBinding_BoundProfileBelowMinimumTypeTriggersRebind covers a
// subtle edge case: the bound profile sits below the selector's minimumType
// floor (e.g. it was retyped to unoptimized, or the default optimized floor
// would never have auto-selected it). The selector match predicate now
// excludes it via the floor and the sticky path must fall through to a rebind.
func TestStickyBinding_BoundProfileBelowMinimumTypeTriggersRebind(t *testing.T) {
	scheme := newResolverTestScheme(t)
	labels := map[string]string{constants.LabelKeyProfileRole: constants.LabelValueProfileRoleDeployable}

	// Bound profile is unoptimized — below the default optimized floor that
	// composeServiceSelector applies, so sticky must respect that exclusion.
	bound := resolverProfileBuilderWithUID("bound", "fp16", labels, stickyTestBoundUID)
	bound.Spec.Type = aimv1alpha1.AIMProfileTypeUnoptimized
	other := resolverProfileBuilderWithUID("other", "fp8", labels, "other-uid")
	c := newResolverClient(t, scheme, bound, other)

	recorder := record.NewFakeRecorder(10)
	service := serviceWithBinding(
		&aimv1alpha1.ProfileSelector{AimId: "aim/one"},
		"bound")

	ns, _, _ := resolveProfileCandidates(context.Background(), c, recorder, service)
	if ns.Value == nil || ns.Value.Name != "other" {
		t.Fatalf("expected rebind to 'other' when bound is below the minimumType floor; got %+v", ns.Value)
	}
	if !reboundEventEmitted(t, drainRecorder(recorder), "bound", "other") {
		t.Error("expected a ProfileRebound event when bound profile drops below the minimumType floor")
	}
}

// TestStickyBinding_ClusterScopedBoundProfile mirrors the namespace test
// for the cluster-scope path: status.resolvedProfile points to an
// AIMClusterProfile, the resolver must fetch from the cluster scope and
// short-circuit on a still-valid binding.
func TestStickyBinding_ClusterScopedBoundProfile(t *testing.T) {
	scheme := newResolverTestScheme(t)
	labels := map[string]string{constants.LabelKeyProfileRole: constants.LabelValueProfileRoleDeployable}

	// Cluster-scoped bound profile + a stronger contender. Sticky should
	// honour the existing binding even though the contender outranks it.
	boundNS := resolverProfileBuilder("placeholder-namespace", stickyTestNamespace, "ignored", "fp16", labels)
	_ = boundNS // not used; included for parity with newResolverClient signature
	boundCluster := &aimv1alpha2.AIMClusterProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-bound", UID: types.UID("cluster-bound-uid"), Labels: labels},
		Spec: aimv1alpha2.AIMClusterProfileSpec{
			AIMProfileSpecCommon: aimv1alpha2.AIMProfileSpecCommon{
				AimId: "aim/one", ModelId: "aim/one", Engine: "vllm",
				Precision: "fp16", Type: aimv1alpha1.AIMProfileTypeOptimized, Primary: true,
				AcceleratorModel: "MI300X", AcceleratorType: aimv1alpha1.AcceleratorTypeGPU,
				AcceleratorCount: 1, Image: "ghcr.io/aim/test:1.0.0",
				ModelSources: []aimv1alpha1.AIMModelSource{{ModelID: "aim/one", SourceURI: "hf://aim/one"}},
			},
		},
		Status: aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady, Deployable: true},
	}
	contenderCluster := &aimv1alpha2.AIMClusterProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-contender", UID: types.UID("cluster-contender-uid"), Labels: labels},
		Spec: aimv1alpha2.AIMClusterProfileSpec{
			AIMProfileSpecCommon: aimv1alpha2.AIMProfileSpecCommon{
				AimId: "aim/one", ModelId: "aim/one", Engine: "vllm",
				Precision: "fp8", Type: aimv1alpha1.AIMProfileTypeOptimized, Primary: true,
				AcceleratorModel: "MI300X", AcceleratorType: aimv1alpha1.AcceleratorTypeGPU,
				AcceleratorCount: 1, Image: "ghcr.io/aim/test:1.0.0",
				ModelSources: []aimv1alpha1.AIMModelSource{{ModelID: "aim/one", SourceURI: "hf://aim/one"}},
			},
		},
		Status: aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady, Deployable: true},
	}
	c := newResolverClient(t, scheme, boundCluster, contenderCluster)

	recorder := record.NewFakeRecorder(10)
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: stickyTestServiceName, Namespace: stickyTestNamespace},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{
				Selector: &aimv1alpha1.ProfileSelector{AimId: "aim/one"},
			},
		},
		Status: aimv1alpha1.AIMServiceStatus{
			ResolvedProfile: &aimv1alpha1.AIMResolvedReference{
				Name:  "cluster-bound",
				Scope: aimv1alpha1.AIMResolutionScopeCluster,
				UID:   types.UID("cluster-bound-uid"),
			},
		},
	}

	_, cluster, _ := resolveProfileCandidates(context.Background(), c, recorder, service)
	if cluster.Value == nil || cluster.Value.Name != "cluster-bound" {
		t.Fatalf("sticky cluster binding should keep cluster-bound; got %+v", cluster.Value)
	}
	for _, ev := range drainRecorder(recorder) {
		if contains(ev, aimv1alpha1.AIMServiceReasonProfileRebound) {
			t.Errorf("unexpected ProfileRebound event on sticky cluster-keep path: %q", ev)
		}
	}
}

// reboundEventEmitted scans the buffered events for a ProfileRebound
// event mentioning both the previous and new profile names. The helper
// exists because event messages embed format args we'd otherwise be
// asserting on with brittle substring math at every call site.
func reboundEventEmitted(t *testing.T, events []string, previous, current string) bool {
	t.Helper()
	for _, ev := range events {
		if !contains(ev, aimv1alpha1.AIMServiceReasonProfileRebound) {
			continue
		}
		if contains(ev, previous) && contains(ev, current) {
			return true
		}
	}
	return false
}

// anyEventContains is a small convenience for asserting on a substring
// across the full set of recorded events without writing the same
// for-range loop in every test.
func anyEventContains(events []string, sub string) bool {
	for _, ev := range events {
		if contains(ev, sub) {
			return true
		}
	}
	return false
}
