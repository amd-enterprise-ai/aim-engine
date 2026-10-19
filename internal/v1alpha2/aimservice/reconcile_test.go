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
	"time"

	"github.com/go-logr/logr"
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	v1alpha1service "github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/aimservice"
	v1alpha1utils "github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimprofile"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/serving"
)

const (
	testProfileA           = "profile-a"
	testServiceName        = "svc"
	testModelIDFP8         = "qwen/qwen3-32b-fp8"
	testClusterProfileName = "cluster-profile"
	testAcceleratorNext    = "MI325X"
	testAcceleratorPrev    = "MI300X"

	componentNameInferenceService = "InferenceService"
	componentNameHTTPRoute        = "HTTPRoute"
	componentNameProfileCache     = "ProfileCache"
)

func TestDecorateStatus_RetiresTemplateMigrationSignal(t *testing.T) {
	cm := controllerutils.NewConditionManager([]metav1.Condition{{
		Type:   controllerutils.ConditionTypeMigrationRequired,
		Status: metav1.ConditionTrue,
		Reason: controllerutils.ReasonTemplateAPIDeprecated,
	}})
	(&ProfileServiceReconciler{}).DecorateStatus(
		&aimv1alpha1.AIMServiceStatus{},
		cm,
		ServiceObservation{ServiceFetchResult: ServiceFetchResult{
			service: &aimv1alpha1.AIMService{Spec: aimv1alpha1.AIMServiceSpec{
				Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA},
			}},
		}},
	)

	if condition := cm.Get(controllerutils.ConditionTypeMigrationRequired); condition != nil {
		t.Fatalf("MigrationRequired was not retired: %#v", condition)
	}
}

func TestGenerateProfileCacheName_SharedDeterministic(t *testing.T) {
	a, err := GenerateProfileCacheName("my-profile", "ns-alpha", "svc-a", "uid-a", aimv1alpha1.CachingModeShared, aimv1alpha1.AIMResolutionScopeNamespace)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	b, err := GenerateProfileCacheName("my-profile", "ns-alpha", "svc-b", "uid-b", aimv1alpha1.CachingModeShared, aimv1alpha1.AIMResolutionScopeNamespace)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if a != b {
		t.Fatalf("Shared cache name must depend only on (profile, namespace, scope) so services in the same namespace converge: %q vs %q", a, b)
	}
}

func TestGenerateProfileCacheName_SharedNamespaceScoped(t *testing.T) {
	same, _ := GenerateProfileCacheName("profile-x", "ns-a", "", "", aimv1alpha1.CachingModeShared, aimv1alpha1.AIMResolutionScopeNamespace)
	other, _ := GenerateProfileCacheName("profile-x", "ns-b", "", "", aimv1alpha1.CachingModeShared, aimv1alpha1.AIMResolutionScopeNamespace)
	if same == other {
		t.Fatalf("Shared cache names must differ across namespaces")
	}
}

// TestGenerateProfileCacheName_SharedScopeIsolation guards against the
// pre-fix collision where a namespace AIMProfile and a cluster
// AIMClusterProfile with the same name in the same service namespace shared
// a single AIMProfileCache and could service the wrong workload.
func TestGenerateProfileCacheName_SharedScopeIsolation(t *testing.T) {
	ns, _ := GenerateProfileCacheName("collides", "ns-alpha", "", "", aimv1alpha1.CachingModeShared, aimv1alpha1.AIMResolutionScopeNamespace)
	cluster, _ := GenerateProfileCacheName("collides", "ns-alpha", "", "", aimv1alpha1.CachingModeShared, aimv1alpha1.AIMResolutionScopeCluster)
	if ns == cluster {
		t.Fatalf("Shared cache names must differ across profile scopes for the same profile name + namespace: %q == %q", ns, cluster)
	}
}

func TestGenerateProfileCacheName_DedicatedPerService(t *testing.T) {
	a, err := GenerateProfileCacheName("my-profile", "ns-alpha", "svc-a", "uid-a", aimv1alpha1.CachingModeDedicated, aimv1alpha1.AIMResolutionScopeNamespace)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	b, err := GenerateProfileCacheName("my-profile", "ns-alpha", "svc-b", "uid-b", aimv1alpha1.CachingModeDedicated, aimv1alpha1.AIMResolutionScopeNamespace)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if a == b {
		t.Fatalf("Dedicated cache names must differ across services on the same profile: %q vs %q", a, b)
	}
}

func TestGenerateProfileCacheName_DedicatedSurvivesRecreate(t *testing.T) {
	// Same service name but different UIDs (delete-and-recreate) must
	// produce different cache names so the new instance does not pick up
	// the old cache before the previous one is GC'd.
	a, err := GenerateProfileCacheName("my-profile", "ns-alpha", "svc", "uid-old", aimv1alpha1.CachingModeDedicated, aimv1alpha1.AIMResolutionScopeNamespace)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	b, err := GenerateProfileCacheName("my-profile", "ns-alpha", "svc", "uid-new", aimv1alpha1.CachingModeDedicated, aimv1alpha1.AIMResolutionScopeNamespace)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if a == b {
		t.Fatalf("Dedicated cache names must include service UID to survive delete-and-recreate: %q vs %q", a, b)
	}
}

func TestGenerateProfileCacheName_SharedAndDedicatedDiffer(t *testing.T) {
	shared, _ := GenerateProfileCacheName("my-profile", "ns-alpha", "svc", "uid", aimv1alpha1.CachingModeShared, aimv1alpha1.AIMResolutionScopeNamespace)
	dedicated, _ := GenerateProfileCacheName("my-profile", "ns-alpha", "svc", "uid", aimv1alpha1.CachingModeDedicated, aimv1alpha1.AIMResolutionScopeNamespace)
	if shared == dedicated {
		t.Fatalf("Shared and Dedicated caches must never collide: %q == %q", shared, dedicated)
	}
}

// Regression test: distinct long profile names that share a prefix must not
// collide on the same cache resource after truncation.
func TestGenerateProfileCacheName_NoTruncationCollision(t *testing.T) {
	namespace := "qa-test-may12"
	profileA := "amdenterpriseai-aim-meta-llama-llama-3-1x-mi300x-thr-fp16-e87a"
	profileB := "amdenterpriseai-aim-meta-llama-llama-3-1x-mi300x-thr-fp16-db70"

	nameA, err := GenerateProfileCacheName(profileA, namespace, "", "", aimv1alpha1.CachingModeShared, aimv1alpha1.AIMResolutionScopeNamespace)
	if err != nil {
		t.Fatalf("generate A: %v", err)
	}
	nameB, err := GenerateProfileCacheName(profileB, namespace, "", "", aimv1alpha1.CachingModeShared, aimv1alpha1.AIMResolutionScopeNamespace)
	if err != nil {
		t.Fatalf("generate B: %v", err)
	}

	if nameA == nameB {
		t.Fatalf("profile cache names collided for distinct profiles: %q", nameA)
	}
}

func TestComposeState_NoProfile(t *testing.T) {
	r := &ProfileServiceReconciler{}
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
	}
	fetch := ServiceFetchResult{service: service}
	obs := r.ComposeState(context.Background(),
		controllerutils.ReconcileContext[*aimv1alpha1.AIMService]{Object: service},
		fetch,
	)

	if obs.resolvedProfileSpec != nil {
		t.Errorf("expected no resolved profile spec when no profile fetched")
	}
	if obs.hasModelSources {
		t.Errorf("hasModelSources should be false when no profile fetched")
	}
	if obs.profileCacheReady {
		t.Errorf("profileCacheReady should be false when no profile fetched")
	}
}

func TestComposeState_NamespaceProfileResolved(t *testing.T) {
	r := &ProfileServiceReconciler{}
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
	}
	profile := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{Name: testProfileA, Namespace: "ns"},
		Spec: aimv1alpha2.AIMProfileSpec{
			AIMProfileSpecCommon: *sampleProfileSpec(),
		},
		Status: aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady},
	}
	fetch := ServiceFetchResult{
		service: service,
		profile: controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Value: profile},
	}
	obs := r.ComposeState(context.Background(),
		controllerutils.ReconcileContext[*aimv1alpha1.AIMService]{Object: service},
		fetch,
	)

	if obs.profileName != testProfileA {
		t.Errorf("profile name not resolved: %q", obs.profileName)
	}
	if obs.profileScope != aimv1alpha1.AIMResolutionScopeNamespace {
		t.Errorf("profile scope should be namespace: %v", obs.profileScope)
	}
	if obs.resolvedProfileSpec == nil {
		t.Errorf("resolved profile spec should be populated")
	}
}

// A stale observed overlay must never be the thing new spec.profileOverrides
// are validated against. The overlay always lags by one reconcile after an
// override edit, and an invalid spec blocks apply — so validating against the
// observed copy would prevent the corrected overlay from ever being written,
// wedging the service permanently rather than transiently.
func TestComposeState_OverlayValidatesAgainstDesiredNotObserved(t *testing.T) {
	r := &ProfileServiceReconciler{}
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			ProfileOverrides: &aimv1alpha1.AIMServiceProfileOverrides{
				AcceleratorModel: testAcceleratorNext,
			},
		},
	}
	profile := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{Name: testProfileA, Namespace: "ns"},
		Spec: aimv1alpha2.AIMProfileSpec{
			AIMProfileSpecCommon: *sampleProfileSpec(),
		},
		Status: aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady},
	}
	profile.Spec.ModelSources = []aimv1alpha1.AIMModelSource{{
		ModelID: testModelIDFP8, SourceURI: "hf://qwen/qwen3-32b-fp8",
	}}

	// The overlay on the cluster still carries the pre-edit accelerator.
	staleOverlay := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "stale-overlay", Namespace: "ns", Generation: 3},
		Spec: aimv1alpha2.AIMProfileSpec{
			AIMProfileSpecCommon: *sampleProfileSpec(),
		},
		Status: aimv1alpha2.AIMProfileStatus{
			ObservedGeneration: 3,
			Status:             constants.AIMStatusReady,
		},
	}
	staleOverlay.Spec.ModelSources = profile.Spec.ModelSources
	staleOverlay.Spec.AcceleratorModel = testAcceleratorPrev

	fetch := ServiceFetchResult{
		service:        service,
		profile:        controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Value: profile},
		overlayProfile: controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Value: staleOverlay},
	}
	obs := r.ComposeState(context.Background(),
		controllerutils.ReconcileContext[*aimv1alpha1.AIMService]{Object: service},
		fetch,
	)

	if obs.configErr != nil {
		t.Fatalf("unexpected config error: %v", obs.configErr)
	}
	if obs.resolvedProfileSpec == nil {
		t.Fatal("overlay spec should be resolved")
	}
	if got := obs.resolvedProfileSpec.AcceleratorModel; got != testAcceleratorNext {
		t.Errorf("resolvedProfileSpec.AcceleratorModel = %q, want the desired %q (not the stale observed overlay)", got, testAcceleratorNext)
	}
	if obs.resolvedProfileStatus != nil {
		t.Error("status from an overlay whose spec does not match the desired overlay must be ignored")
	}
	if obs.profileReadyForService() {
		t.Error("stale Ready status must not authorize downstream service resources")
	}
}

func TestComposeState_OverlayStatusRequiresCurrentGeneration(t *testing.T) {
	r := &ProfileServiceReconciler{}
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			ProfileOverrides: &aimv1alpha1.AIMServiceProfileOverrides{
				AcceleratorModel: testAcceleratorNext,
			},
		},
	}
	profile := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{Name: testProfileA, Namespace: "ns"},
		Spec: aimv1alpha2.AIMProfileSpec{
			AIMProfileSpecCommon: *sampleProfileSpec(),
		},
	}
	observedOverlay := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "overlay", Namespace: "ns", Generation: 4},
		Spec: aimv1alpha2.AIMProfileSpec{
			AIMProfileSpecCommon: *sampleProfileSpec(),
		},
		Status: aimv1alpha2.AIMProfileStatus{
			ObservedGeneration: 3,
			Status:             constants.AIMStatusReady,
		},
	}
	observedOverlay.Spec.AcceleratorModel = testAcceleratorNext

	compose := func() ServiceObservation {
		return r.ComposeState(
			context.Background(),
			controllerutils.ReconcileContext[*aimv1alpha1.AIMService]{Object: service},
			ServiceFetchResult{
				service:        service,
				profile:        controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Value: profile},
				overlayProfile: controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Value: observedOverlay},
			},
		)
	}

	obs := compose()
	if obs.resolvedProfileStatus != nil {
		t.Fatal("Ready status from the previous overlay generation must be ignored")
	}

	observedOverlay.Status.ObservedGeneration = observedOverlay.Generation
	obs = compose()
	if obs.resolvedProfileStatus == nil {
		t.Fatal("status must become authoritative after the profile controller observes the current generation")
	}
}

func TestComposeState_AdapterCapabilityUsesEffectiveProfile(t *testing.T) {
	t.Run("feature-silent profile is rejected", func(t *testing.T) {
		r := &ProfileServiceReconciler{}
		service := &aimv1alpha1.AIMService{
			ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
			Spec: aimv1alpha1.AIMServiceSpec{
				Profile:     &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA},
				AdapterMode: aimv1alpha1.AdapterModeDynamic,
			},
		}
		profile := &aimv1alpha2.AIMProfile{
			ObjectMeta: metav1.ObjectMeta{Name: testProfileA, Namespace: "ns"},
			Spec: aimv1alpha2.AIMProfileSpec{
				AIMProfileSpecCommon: *sampleProfileSpec(),
			},
			Status: aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady},
		}

		obs := r.ComposeState(
			context.Background(),
			controllerutils.ReconcileContext[*aimv1alpha1.AIMService]{Object: service},
			ServiceFetchResult{
				service: service,
				profile: controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Value: profile},
			},
		)

		if obs.adapterState.ConfigErr == nil {
			t.Fatal("expected adapter capability error for a feature-silent profile")
		}
		if obs.desiredOverlayProfile != nil {
			t.Fatal("adapter mode must not manufacture a service-owned overlay")
		}
	})

	t.Run("explicit feature override is accepted", func(t *testing.T) {
		r := &ProfileServiceReconciler{}
		service := &aimv1alpha1.AIMService{
			ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
			Spec: aimv1alpha1.AIMServiceSpec{
				Profile:     &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA},
				AdapterMode: aimv1alpha1.AdapterModeDynamic,
				ProfileOverrides: &aimv1alpha1.AIMServiceProfileOverrides{
					Features: []string{aimv1alpha2.ProfileFeatureAdapters},
				},
			},
		}
		profile := &aimv1alpha2.AIMProfile{
			ObjectMeta: metav1.ObjectMeta{Name: testProfileA, Namespace: "ns"},
			Spec: aimv1alpha2.AIMProfileSpec{
				AIMProfileSpecCommon: *sampleProfileSpec(),
			},
			Status: aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady},
		}

		obs := r.ComposeState(
			context.Background(),
			controllerutils.ReconcileContext[*aimv1alpha1.AIMService]{Object: service},
			ServiceFetchResult{
				service: service,
				profile: controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Value: profile},
			},
		)

		if obs.adapterState.ConfigErr != nil {
			t.Fatalf("explicit feature override should satisfy the adapter gate: %v", obs.adapterState.ConfigErr)
		}
		if obs.desiredOverlayProfile == nil {
			t.Fatal("explicit feature override must materialise a service-owned overlay")
		}
		if obs.resolvedProfileSpec == nil || !obs.resolvedProfileSpec.SupportsAdapters() {
			t.Fatalf("effective profile must advertise adapters, got %#v", obs.resolvedProfileSpec)
		}
	})
}

func TestComposeState_ClusterProfileFallback(t *testing.T) {
	r := &ProfileServiceReconciler{}
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
	}
	clusterProfile := &aimv1alpha2.AIMClusterProfile{
		ObjectMeta: metav1.ObjectMeta{Name: testClusterProfileName},
		Spec: aimv1alpha2.AIMClusterProfileSpec{
			AIMProfileSpecCommon: *sampleProfileSpec(),
		},
		Status: aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady},
	}
	fetch := ServiceFetchResult{
		service:        service,
		clusterProfile: controllerutils.FetchResult[*aimv1alpha2.AIMClusterProfile]{Value: clusterProfile},
	}
	obs := r.ComposeState(context.Background(),
		controllerutils.ReconcileContext[*aimv1alpha1.AIMService]{Object: service},
		fetch,
	)
	if obs.profileScope != aimv1alpha1.AIMResolutionScopeCluster {
		t.Errorf("profile scope should be cluster: %v", obs.profileScope)
	}
	if obs.profileName != testClusterProfileName {
		t.Errorf("cluster profile name not resolved: %q", obs.profileName)
	}
}

func TestPlanResources_MaterializesProfileRuntimeBeforeInferenceService(t *testing.T) {
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testServiceName,
			Namespace: "ns",
			UID:       "service-uid",
			Labels: map[string]string{
				"team": "service-team",
				constants.AimLabelDomain + "/environment": "service-environment",
			},
		},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: testClusterProfileName},
		},
	}
	clusterProfile := &aimv1alpha2.AIMClusterProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name: testClusterProfileName,
			UID:  "profile-uid",
		},
		Spec: aimv1alpha2.AIMClusterProfileSpec{
			AIMProfileSpecCommon: *sampleProfileSpec(),
		},
		Status: aimv1alpha2.AIMProfileStatus{
			Status:     constants.AIMStatusReady,
			Deployable: true,
		},
	}
	notFound := func(resource, name string) error {
		return apierrors.NewNotFound(schema.GroupResource{
			Group:    "test",
			Resource: resource,
		}, name)
	}
	reconciler := &ProfileServiceReconciler{}
	reconcileCtx := controllerutils.ReconcileContext[*aimv1alpha1.AIMService]{Object: service}

	first := reconciler.ComposeState(
		context.Background(),
		reconcileCtx,
		ServiceFetchResult{
			service:        service,
			clusterProfile: controllerutils.FetchResult[*aimv1alpha2.AIMClusterProfile]{Value: clusterProfile},
			inferenceService: controllerutils.FetchResult[*servingv1beta1.InferenceService]{
				Error: notFound("inferenceservices", testServiceName),
			},
			profileRuntime: controllerutils.FetchResult[*kservev1alpha1.ServingRuntime]{
				Error: notFound("servingruntimes", serving.RuntimeName(testClusterProfileName)),
			},
			profileRuntimeConfigMap: controllerutils.FetchResult[*corev1.ConfigMap]{
				Error: notFound("configmaps", serving.RuntimeName(testClusterProfileName)),
			},
		},
	)
	if first.configErr != nil {
		t.Fatalf("ComposeState returned config error: %v", first.configErr)
	}
	if health := first.getProfileRuntimeHealth(); health.State != constants.AIMStatusProgressing {
		t.Fatalf("missing ServingRuntime/ConfigMap health = %q, want Progressing", health.State)
	}

	firstPlan := reconciler.PlanResources(context.Background(), reconcileCtx, first)
	runtime, configMap := assertInitialProfileRuntimePlan(t, service, clusterProfile, &firstPlan)

	changedProfile := clusterProfile.DeepCopy()
	changedProfile.Spec.Image += "-updated"
	stale := reconciler.ComposeState(
		context.Background(),
		reconcileCtx,
		ServiceFetchResult{
			service:        service,
			clusterProfile: controllerutils.FetchResult[*aimv1alpha2.AIMClusterProfile]{Value: changedProfile},
			inferenceService: controllerutils.FetchResult[*servingv1beta1.InferenceService]{
				Error: notFound("inferenceservices", testServiceName),
			},
			profileRuntime:          controllerutils.FetchResult[*kservev1alpha1.ServingRuntime]{Value: runtime},
			profileRuntimeConfigMap: controllerutils.FetchResult[*corev1.ConfigMap]{Value: configMap},
		},
	)
	if stale.configErr != nil {
		t.Fatalf("ComposeState with stale projection returned config error: %v", stale.configErr)
	}
	oldHash := runtime.Annotations[constants.AnnotationRuntimeProjectionContentHash]
	wantHash := stale.desiredProfileRuntime.Annotations[constants.AnnotationRuntimeProjectionContentHash]
	if oldHash == "" || wantHash == "" || oldHash == wantHash {
		t.Fatalf("profile content change hashes: observed=%q desired=%q, want different non-empty hashes", oldHash, wantHash)
	}
	if health := stale.getProfileRuntimeHealth(); health.State != constants.AIMStatusProgressing ||
		health.Reason != reasonProfileRuntimeConverging {
		t.Fatalf("stale projection health = %q/%q, want Progressing/ProfileRuntimeConverging", health.State, health.Reason)
	}
	assertStaleProfileRuntimePlan(t, reconciler, reconcileCtx, service, stale)
	assertStaleProfileRuntimeOwnerUID(t, reconciler, reconcileCtx, first, runtime, configMap)
	assertStaleProfileRuntimeObservedContent(t, reconciler, reconcileCtx, service, first, runtime, configMap)

	second := first
	second.profileRuntime = controllerutils.FetchResult[*kservev1alpha1.ServingRuntime]{Value: runtime}
	second.profileRuntimeConfigMap = controllerutils.FetchResult[*corev1.ConfigMap]{Value: configMap}
	if health := second.getProfileRuntimeHealth(); health.State != constants.AIMStatusReady {
		t.Fatalf("materialized ServingRuntime/ConfigMap health = %q, want Ready", health.State)
	}
	secondPlan := reconciler.PlanResources(context.Background(), reconcileCtx, second)

	if !planAppliesType[*servingv1beta1.InferenceService](secondPlan.GetToApply()) {
		t.Fatal("InferenceService must be planned after the ServingRuntime and ConfigMap are observed")
	}
}

func TestPlanResources_MigrationHandoffPreservesLegacyInferenceServiceAtPinnedGeneration(t *testing.T) {
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{
			Name:       testServiceName,
			Namespace:  "ns",
			UID:        "service-uid",
			Generation: 7,
			Annotations: map[string]string{
				constants.AnnotationReconcilerPipeline:                          constants.ReconcilerPipelineProfile,
				constants.AnnotationMigrationPreserveInferenceServiceGeneration: "7",
				constants.AnnotationMigrationPreserveInferenceServiceUID:        "legacy-isvc-uid",
			},
		},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: testClusterProfileName},
		},
	}
	clusterProfile := &aimv1alpha2.AIMClusterProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name: testClusterProfileName,
			UID:  "profile-uid",
		},
		Spec: aimv1alpha2.AIMClusterProfileSpec{
			AIMProfileSpecCommon: *sampleProfileSpec(),
		},
		Status: aimv1alpha2.AIMProfileStatus{
			Status:     constants.AIMStatusReady,
			Deployable: true,
		},
	}
	isvcName, err := v1alpha1service.GenerateInferenceServiceName(service.Name, service.Namespace)
	if err != nil {
		t.Fatalf("generate InferenceService name: %v", err)
	}
	legacyConfigMapName := v1alpha1utils.ServiceCustomProfileConfigMapName(service.Name)
	legacyISVC := currentReadyInferenceService(service.Namespace, legacyConfigMapName)
	legacyISVC.Name = isvcName
	legacyISVC.UID = "legacy-isvc-uid"
	legacyISVC.Labels = map[string]string{
		constants.LabelTemplate: "legacy-template",
	}
	legacyISVC.Spec.Predictor.Containers = []corev1.Container{{
		Name:  constants.ContainerKServe,
		Image: "example.invalid/legacy-inline:1",
	}}
	legacyConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      legacyConfigMapName,
			Namespace: service.Namespace,
			Labels: map[string]string{
				constants.LabelK8sManagedBy: constants.LabelValueManagedBy,
			},
			OwnerReferences: []metav1.OwnerReference{{
				Kind: "AIMService",
				UID:  service.UID,
			}},
		},
	}
	notFound := func(resource, name string) error {
		return apierrors.NewNotFound(schema.GroupResource{
			Group:    "test",
			Resource: resource,
		}, name)
	}
	reconciler := &ProfileServiceReconciler{}
	reconcileCtx := controllerutils.ReconcileContext[*aimv1alpha1.AIMService]{Object: service}
	first := reconciler.ComposeState(
		context.Background(),
		reconcileCtx,
		ServiceFetchResult{
			service:                service,
			clusterProfile:         controllerutils.FetchResult[*aimv1alpha2.AIMClusterProfile]{Value: clusterProfile},
			inferenceService:       controllerutils.FetchResult[*servingv1beta1.InferenceService]{Value: legacyISVC},
			legacyProfileConfigMap: controllerutils.FetchResult[*corev1.ConfigMap]{Value: legacyConfigMap},
			profileRuntime: controllerutils.FetchResult[*kservev1alpha1.ServingRuntime]{
				Error: notFound("servingruntimes", serving.RuntimeName(testClusterProfileName)),
			},
			profileRuntimeConfigMap: controllerutils.FetchResult[*corev1.ConfigMap]{
				Error: notFound("configmaps", serving.RuntimeName(testClusterProfileName)),
			},
		},
	)
	if first.configErr != nil {
		t.Fatalf("ComposeState returned config error: %v", first.configErr)
	}
	firstPlan := reconciler.PlanResources(context.Background(), reconcileCtx, first)
	runtime, configMap := assertInitialProfileRuntimePlan(t, service, clusterProfile, &firstPlan)

	second := first
	second.profileRuntime = controllerutils.FetchResult[*kservev1alpha1.ServingRuntime]{Value: runtime}
	second.profileRuntimeConfigMap = controllerutils.FetchResult[*corev1.ConfigMap]{Value: configMap}
	secondPlan := reconciler.PlanResources(context.Background(), reconcileCtx, second)

	if planAppliesType[*servingv1beta1.InferenceService](secondPlan.GetToApply()) {
		t.Fatal("generation-pinned migration handoff must observe, not rewrite, the legacy InferenceService")
	}
	if count := legacyConfigMapDeleteCount(t, secondPlan, service.Namespace, legacyConfigMapName); count != 0 {
		t.Fatalf("generation-pinned migration handoff deleted referenced legacy ConfigMap")
	}

	service.Generation++
	afterServiceChange := reconciler.PlanResources(context.Background(), reconcileCtx, second)
	if !planAppliesType[*servingv1beta1.InferenceService](afterServiceChange.GetToApply()) {
		t.Fatal("a later AIMService spec generation must resume normal profile InferenceService rendering")
	}
	if count := legacyConfigMapDeleteCount(t, afterServiceChange, service.Namespace, legacyConfigMapName); count != 0 {
		t.Fatal("a planned replacement must not make the observed legacy ConfigMap safe to delete")
	}
}

func TestPlanResources_ActiveMigrationPinFailsClosed(t *testing.T) {
	tests := []struct {
		name string
		edit func(*aimv1alpha1.AIMService, *controllerutils.FetchResult[*servingv1beta1.InferenceService])
	}{
		{
			name: "missing UID pin",
			edit: func(service *aimv1alpha1.AIMService, _ *controllerutils.FetchResult[*servingv1beta1.InferenceService]) {
				delete(service.Annotations, constants.AnnotationMigrationPreserveInferenceServiceUID)
			},
		},
		{
			name: "wrong UID pin",
			edit: func(service *aimv1alpha1.AIMService, _ *controllerutils.FetchResult[*servingv1beta1.InferenceService]) {
				service.Annotations[constants.AnnotationMigrationPreserveInferenceServiceUID] = "other-isvc-uid"
			},
		},
		{
			name: "missing InferenceService",
			edit: func(_ *aimv1alpha1.AIMService, inferenceService *controllerutils.FetchResult[*servingv1beta1.InferenceService]) {
				inferenceService.Value = nil
				inferenceService.Error = apierrors.NewNotFound(
					schema.GroupResource{Group: "serving.kserve.io", Resource: "inferenceservices"},
					testServiceName,
				)
			},
		},
		{
			name: "terminating InferenceService",
			edit: func(_ *aimv1alpha1.AIMService, inferenceService *controllerutils.FetchResult[*servingv1beta1.InferenceService]) {
				deletionTimestamp := metav1.Now()
				inferenceService.Value.DeletionTimestamp = &deletionTimestamp
			},
		},
		{
			name: "missing template label",
			edit: func(_ *aimv1alpha1.AIMService, inferenceService *controllerutils.FetchResult[*servingv1beta1.InferenceService]) {
				delete(inferenceService.Value.Labels, constants.LabelTemplate)
			},
		},
		{
			name: "profile label present",
			edit: func(_ *aimv1alpha1.AIMService, inferenceService *controllerutils.FetchResult[*servingv1beta1.InferenceService]) {
				inferenceService.Value.Labels[constants.LabelProfile] = testClusterProfileName
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &aimv1alpha1.AIMService{
				ObjectMeta: metav1.ObjectMeta{
					Name:       testServiceName,
					Namespace:  "ns",
					Generation: 7,
					Annotations: map[string]string{
						constants.AnnotationMigrationPreserveInferenceServiceGeneration: "7",
						constants.AnnotationMigrationPreserveInferenceServiceUID:        "legacy-isvc-uid",
					},
				},
				Spec: aimv1alpha1.AIMServiceSpec{
					Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: testClusterProfileName},
				},
			}
			clusterProfile := &aimv1alpha2.AIMClusterProfile{
				ObjectMeta: metav1.ObjectMeta{Name: testClusterProfileName, UID: "profile-uid"},
				Spec: aimv1alpha2.AIMClusterProfileSpec{
					AIMProfileSpecCommon: *sampleProfileSpec(),
				},
				Status: aimv1alpha2.AIMProfileStatus{
					Status:     constants.AIMStatusReady,
					Deployable: true,
				},
			}
			inferenceService := controllerutils.FetchResult[*servingv1beta1.InferenceService]{
				Value: &servingv1beta1.InferenceService{
					ObjectMeta: metav1.ObjectMeta{
						Name:      testServiceName,
						Namespace: service.Namespace,
						UID:       "legacy-isvc-uid",
						Labels: map[string]string{
							constants.LabelTemplate: "legacy-template",
						},
					},
				},
			}
			test.edit(service, &inferenceService)

			reconciler := &ProfileServiceReconciler{}
			reconcileCtx := controllerutils.ReconcileContext[*aimv1alpha1.AIMService]{Object: service}
			obs := reconciler.ComposeState(context.Background(), reconcileCtx, ServiceFetchResult{
				service:          service,
				clusterProfile:   controllerutils.FetchResult[*aimv1alpha2.AIMClusterProfile]{Value: clusterProfile},
				inferenceService: inferenceService,
			})
			if obs.configErr == nil {
				t.Fatal("invalid active migration preservation pin did not surface a configuration error")
			}
			if health := obs.getConfigHealth(); len(health.Errors) != 1 {
				t.Fatalf("migration preservation health = %#v", health)
			}

			plan := reconciler.PlanResources(context.Background(), reconcileCtx, obs)
			if planAppliesType[*servingv1beta1.InferenceService](plan.GetToApply()) {
				t.Fatal("invalid active migration preservation pin planned an InferenceService mutation")
			}
		})
	}
}

func assertInitialProfileRuntimePlan(
	t *testing.T,
	service *aimv1alpha1.AIMService,
	clusterProfile *aimv1alpha2.AIMClusterProfile,
	plan *controllerutils.PlanResult,
) (*kservev1alpha1.ServingRuntime, *corev1.ConfigMap) {
	t.Helper()
	if plan.RequeueAfter == 0 {
		t.Fatal("missing ServingRuntime and ConfigMap must request a follow-up reconcile")
	}
	if planAppliesType[*servingv1beta1.InferenceService](plan.GetToApply()) {
		t.Fatal("InferenceService must not be planned before the ServingRuntime and ConfigMap exist")
	}

	var runtime *kservev1alpha1.ServingRuntime
	var configMap *corev1.ConfigMap
	for _, obj := range plan.GetToApplyWithoutOwnerRef() {
		switch typed := obj.(type) {
		case *kservev1alpha1.ServingRuntime:
			runtime = typed
		case *corev1.ConfigMap:
			configMap = typed
		}
	}
	if runtime == nil || configMap == nil {
		t.Fatalf("first pass must plan both ServingRuntime and ConfigMap, got servingRuntime=%v configMap=%v", runtime != nil, configMap != nil)
	}

	controllerutils.PropagateLabelsForResult(
		service,
		plan,
		&controllerutils.LabelPropagationSettings{Enabled: true, Match: []string{"team"}},
	)
	controllerutils.ApplyControllerLabelsToResult(plan, map[string]string{
		constants.LabelK8sManagedBy: constants.LabelValueManagedBy,
		serviceControllerNameLabel:  service.Name,
	})
	controllerutils.RemoveExcludedLabelsFromResult(plan)
	assertSharedProjectionMetadata(t, clusterProfile, runtime, configMap)
	return runtime, configMap
}

func assertSharedProjectionMetadata(
	t *testing.T,
	clusterProfile *aimv1alpha2.AIMClusterProfile,
	objects ...metav1.Object,
) {
	t.Helper()
	for _, obj := range objects {
		for _, label := range []string{"team", constants.AimLabelDomain + "/environment", serviceControllerNameLabel} {
			if _, exists := obj.GetLabels()[label]; exists {
				t.Errorf("shared projected object inherited service label %q", label)
			}
		}
		if got := obj.GetLabels()[constants.LabelProfile]; got != clusterProfile.Name {
			t.Errorf("profile label = %q, want %q", got, clusterProfile.Name)
		}
		refs := obj.GetOwnerReferences()
		if len(refs) != 1 || refs[0].Kind != "AIMClusterProfile" || refs[0].UID != clusterProfile.UID {
			t.Fatalf("projected object must remain owned by the cluster profile, got %#v", refs)
		}
	}
}

func assertStaleProfileRuntimePlan(
	t *testing.T,
	reconciler *ProfileServiceReconciler,
	reconcileCtx controllerutils.ReconcileContext[*aimv1alpha1.AIMService],
	service *aimv1alpha1.AIMService,
	stale ServiceObservation,
) {
	t.Helper()
	previousRouting := service.Spec.Routing
	defer func() { service.Spec.Routing = previousRouting }()
	service.Spec.Routing = &aimv1alpha1.AIMRuntimeRoutingConfig{
		Enabled:    ptr.To(true),
		GatewayRef: &gatewayapiv1.ParentReference{Name: "gateway"},
	}

	freshPlan := reconciler.PlanResources(context.Background(), reconcileCtx, stale)
	if planAppliesType[*gatewayapiv1.HTTPRoute](freshPlan.GetToApply()) {
		t.Fatal("brand-new service must not plan HTTPRoute before its InferenceService exists")
	}

	stale.inferenceService = controllerutils.FetchResult[*servingv1beta1.InferenceService]{
		Value: &servingv1beta1.InferenceService{
			ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: service.Namespace},
		},
	}
	stalePlan := reconciler.PlanResources(context.Background(), reconcileCtx, stale)
	if stalePlan.RequeueAfter == 0 {
		t.Fatal("stale ServingRuntime and ConfigMap must request a follow-up reconcile")
	}
	if planAppliesType[*servingv1beta1.InferenceService](stalePlan.GetToApply()) {
		t.Fatal("InferenceService must not be planned against stale ServingRuntime/ConfigMap content")
	}
	if !planAppliesType[*gatewayapiv1.HTTPRoute](stalePlan.GetToApply()) {
		t.Fatal("stale runtime projection must defer only the InferenceService, not independent HTTPRoute planning")
	}
	if got := len(stalePlan.GetToApplyWithoutOwnerRef()); got != 0 {
		t.Fatalf("AIMService must leave stale existing projection repair to its projection controller, got %d applies", got)
	}
}

func assertStaleProfileRuntimeOwnerUID(
	t *testing.T,
	reconciler *ProfileServiceReconciler,
	reconcileCtx controllerutils.ReconcileContext[*aimv1alpha1.AIMService],
	first ServiceObservation,
	runtime *kservev1alpha1.ServingRuntime,
	configMap *corev1.ConfigMap,
) {
	t.Helper()
	for _, sibling := range []string{"ServingRuntime", "ConfigMap"} {
		t.Run("stale "+sibling+" owner UID", func(t *testing.T) {
			actualRuntime := runtime.DeepCopy()
			actualConfigMap := configMap.DeepCopy()
			if sibling == "ServingRuntime" {
				actualRuntime.OwnerReferences[0].UID = "previous-profile-uid"
			} else {
				actualConfigMap.OwnerReferences[0].UID = "previous-profile-uid"
			}

			staleOwner := first
			staleOwner.profileRuntime = controllerutils.FetchResult[*kservev1alpha1.ServingRuntime]{Value: actualRuntime}
			staleOwner.profileRuntimeConfigMap = controllerutils.FetchResult[*corev1.ConfigMap]{Value: actualConfigMap}
			if staleOwner.profileRuntimeCurrent() {
				t.Fatalf("matching content hash must not hide stale %s owner UID", sibling)
			}
			ownerPlan := reconciler.PlanResources(context.Background(), reconcileCtx, staleOwner)
			if ownerPlan.RequeueAfter == 0 {
				t.Fatalf("stale %s owner UID must request a follow-up reconcile", sibling)
			}
			if planAppliesType[*servingv1beta1.InferenceService](ownerPlan.GetToApply()) {
				t.Fatalf("InferenceService must not be planned with stale %s ownership", sibling)
			}
		})
	}
}

func assertStaleProfileRuntimeObservedContent(
	t *testing.T,
	reconciler *ProfileServiceReconciler,
	reconcileCtx controllerutils.ReconcileContext[*aimv1alpha1.AIMService],
	service *aimv1alpha1.AIMService,
	first ServiceObservation,
	runtime *kservev1alpha1.ServingRuntime,
	configMap *corev1.ConfigMap,
) {
	t.Helper()
	tests := []struct {
		name   string
		mutate func(*kservev1alpha1.ServingRuntime, *corev1.ConfigMap)
	}{
		{
			name: "ServingRuntime spec drift with unchanged annotation",
			mutate: func(actualRuntime *kservev1alpha1.ServingRuntime, _ *corev1.ConfigMap) {
				actualRuntime.Spec.Containers[0].Image += "-drifted"
			},
		},
		{
			name: "ConfigMap data drift with unchanged annotation",
			mutate: func(_ *kservev1alpha1.ServingRuntime, actualConfigMap *corev1.ConfigMap) {
				actualConfigMap.Data["drifted-profile.yaml"] = "unexpected"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actualRuntime := runtime.DeepCopy()
			actualConfigMap := configMap.DeepCopy()
			tt.mutate(actualRuntime, actualConfigMap)

			stale := first
			stale.profileRuntime = controllerutils.FetchResult[*kservev1alpha1.ServingRuntime]{Value: actualRuntime}
			stale.profileRuntimeConfigMap = controllerutils.FetchResult[*corev1.ConfigMap]{Value: actualConfigMap}
			if stale.profileRuntimeCurrent() {
				t.Fatal("stored content hash must not hide drift in observed projection content")
			}
			if health := stale.getProfileRuntimeHealth(); health.State != constants.AIMStatusProgressing ||
				health.Reason != reasonProfileRuntimeConverging {
				t.Fatalf("drifted projection health = %q/%q, want Progressing/ProfileRuntimeConverging",
					health.State, health.Reason)
			}
			assertStaleProfileRuntimePlan(t, reconciler, reconcileCtx, service, stale)
		})
	}
}

func planAppliesType[T client.Object](objects []client.Object) bool {
	for _, obj := range objects {
		if _, ok := obj.(T); ok {
			return true
		}
	}
	return false
}

func TestProfileRuntimeHealth_UnexpectedErrorWinsOverMissingSibling(t *testing.T) {
	t.Parallel()

	runtimeName := serving.RuntimeName(testClusterProfileName)
	notFound := apierrors.NewNotFound(
		schema.GroupResource{Group: "serving.kserve.io", Resource: "servingruntimes"},
		runtimeName,
	)
	forbidden := apierrors.NewForbidden(
		schema.GroupResource{Resource: "configmaps"},
		runtimeName,
		errors.New("access denied"),
	)
	spec := sampleProfileSpec()
	status := &aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady}
	obs := ServiceObservation{
		resolvedProfileSpec:   spec,
		resolvedProfileStatus: status,
		ServiceFetchResult: ServiceFetchResult{
			profileRuntime: controllerutils.FetchResult[*kservev1alpha1.ServingRuntime]{
				Error: notFound,
			},
			profileRuntimeConfigMap: controllerutils.FetchResult[*corev1.ConfigMap]{
				Error: forbidden,
			},
		},
	}

	health := obs.getProfileRuntimeHealth()
	if health.State != constants.AIMStatusFailed {
		t.Fatalf("health state = %q, want Failed", health.State)
	}
	if health.Reason != reasonFetchError {
		t.Fatalf("health reason = %q, want %q", health.Reason, reasonFetchError)
	}
	if len(health.Errors) != 1 || !apierrors.IsForbidden(health.Errors[0]) {
		t.Fatalf("health errors = %v, want the ConfigMap Forbidden error", health.Errors)
	}

	plan := controllerutils.PlanResult{}
	if !planProfileRuntimeMaterialization(&plan, obs, logr.Discard()) {
		t.Fatal("runtime fetch error must defer InferenceService reconciliation")
	}
	if plan.RequeueAfter != 0 {
		t.Fatalf("runtime fetch error requested fixed requeue %s; want pipeline-managed retry", plan.RequeueAfter)
	}
}

func TestPipelineRun_MaterializesProfileRuntimeBeforeInferenceService(t *testing.T) {
	ctx := context.Background()
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testServiceName,
			Namespace: "ns",
			UID:       "service-uid",
		},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: testClusterProfileName},
		},
	}
	clusterProfile := &aimv1alpha2.AIMClusterProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name: testClusterProfileName,
			UID:  "profile-uid",
		},
		Spec: aimv1alpha2.AIMClusterProfileSpec{
			AIMProfileSpecCommon: *sampleProfileSpec(),
		},
		Status: aimv1alpha2.AIMProfileStatus{
			Status:     constants.AIMStatusReady,
			Deployable: true,
		},
	}

	c, pipeline := newProfileRuntimePipelineFixture(t, service, clusterProfile)

	serviceKey := client.ObjectKey{Name: testServiceName, Namespace: "ns"}
	isvcName, err := v1alpha1service.GenerateInferenceServiceName(testServiceName, "ns")
	if err != nil {
		t.Fatalf("generate InferenceService name: %v", err)
	}
	isvcKey := client.ObjectKey{Name: isvcName, Namespace: "ns"}
	currentService := getProfileServiceForTest(t, ctx, c, serviceKey)
	if requeueAfter := runProfileServicePipelineCycle(t, ctx, pipeline, currentService); requeueAfter == 0 {
		t.Fatal("first pipeline cycle must request a follow-up after materializing the ServingRuntime")
	}

	runtimeName := serving.RuntimeName(testClusterProfileName)
	runtimeKey := client.ObjectKey{Name: runtimeName, Namespace: "ns"}
	projectedRuntime := getObjectForTest(t, ctx, c, runtimeKey, &kservev1alpha1.ServingRuntime{})
	projectedConfigMap := getObjectForTest(t, ctx, c, runtimeKey, &corev1.ConfigMap{})
	for _, obj := range []client.Object{projectedRuntime, projectedConfigMap} {
		if _, exists := obj.GetLabels()[serviceControllerNameLabel]; exists {
			t.Errorf("%T has misleading shared-resource label %q", obj, serviceControllerNameLabel)
		}
	}
	if err := c.Get(ctx, isvcKey, &servingv1beta1.InferenceService{}); !apierrors.IsNotFound(err) {
		t.Fatalf("InferenceService after first pipeline cycle: got error %v, want NotFound", err)
	}

	currentService = getProfileServiceForTest(t, ctx, c, serviceKey)
	runProfileServicePipelineCycle(t, ctx, pipeline, currentService)
	isvc := getObjectForTest(t, ctx, c, isvcKey, &servingv1beta1.InferenceService{})
	assertInferenceServiceRuntime(t, isvc, runtimeName)

	// Changing the resolved profile is a two-pass rollout. The first pass
	// materializes the new projection while the existing ISVC keeps serving
	// through the old one; the second updates the ISVC reference.
	updatedProfile := clusterProfile.DeepCopy()
	updatedProfile.Name = testClusterProfileName + "-updated"
	updatedProfile.UID = "updated-profile-uid"
	updatedProfile.ResourceVersion = ""
	if err := c.Create(ctx, updatedProfile); err != nil {
		t.Fatalf("create updated profile: %v", err)
	}
	currentService = getProfileServiceForTest(t, ctx, c, serviceKey)
	currentService.Spec.Profile.Name = updatedProfile.Name
	if err := c.Update(ctx, currentService); err != nil {
		t.Fatalf("update service profile: %v", err)
	}

	currentService = getProfileServiceForTest(t, ctx, c, serviceKey)
	if requeueAfter := runProfileServicePipelineCycle(t, ctx, pipeline, currentService); requeueAfter == 0 {
		t.Fatal("profile change must request a follow-up after materializing the new ServingRuntime")
	}

	updatedRuntimeName := serving.RuntimeName(updatedProfile.Name)
	updatedRuntimeKey := client.ObjectKey{Name: updatedRuntimeName, Namespace: "ns"}
	getObjectForTest(t, ctx, c, updatedRuntimeKey, &kservev1alpha1.ServingRuntime{})
	getObjectForTest(t, ctx, c, updatedRuntimeKey, &corev1.ConfigMap{})
	isvc = getObjectForTest(t, ctx, c, isvcKey, &servingv1beta1.InferenceService{})
	assertInferenceServiceRuntime(t, isvc, runtimeName)

	currentService = getProfileServiceForTest(t, ctx, c, serviceKey)
	runProfileServicePipelineCycle(t, ctx, pipeline, currentService)
	isvc = getObjectForTest(t, ctx, c, isvcKey, &servingv1beta1.InferenceService{})
	assertInferenceServiceRuntime(t, isvc, updatedRuntimeName)
}

type profileRuntimeTestPipeline = controllerutils.Pipeline[
	*aimv1alpha1.AIMService,
	*aimv1alpha1.AIMServiceStatus,
	ServiceFetchResult,
	ServiceObservation,
]

func newProfileRuntimePipelineFixture(
	t *testing.T,
	service *aimv1alpha1.AIMService,
	clusterProfile *aimv1alpha2.AIMClusterProfile,
) (client.Client, *profileRuntimeTestPipeline) {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, addToScheme := range []func(*runtime.Scheme) error{
		corev1.AddToScheme,
		autoscalingv2.AddToScheme,
		aimv1alpha1.AddToScheme,
		aimv1alpha2.AddToScheme,
		kservev1alpha1.AddToScheme,
		servingv1beta1.AddToScheme,
		gatewayapiv1.Install,
	} {
		if err := addToScheme(scheme); err != nil {
			t.Fatalf("add test types to scheme: %v", err)
		}
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&aimv1alpha1.AIMService{}).
		WithObjects(service, clusterProfile).
		Build()
	recorder := record.NewFakeRecorder(20)
	reconciler := &ProfileServiceReconciler{Scheme: scheme, Recorder: recorder}
	pipeline := &profileRuntimeTestPipeline{
		Client:         c,
		StatusClient:   c.Status(),
		Recorder:       recorder,
		ControllerName: "service",
		Reconciler:     reconciler,
		Scheme:         scheme,
	}
	return c, pipeline
}

func runProfileServicePipelineCycle(
	t *testing.T,
	ctx context.Context,
	pipeline *profileRuntimeTestPipeline,
	service *aimv1alpha1.AIMService,
) time.Duration {
	t.Helper()
	result, err := pipeline.Run(ctx, service)
	if err != nil {
		t.Fatalf("profile service pipeline cycle: %v", err)
	}
	return result.RequeueAfter
}

func getProfileServiceForTest(
	t *testing.T,
	ctx context.Context,
	c client.Client,
	key client.ObjectKey,
) *aimv1alpha1.AIMService {
	t.Helper()
	return getObjectForTest(t, ctx, c, key, &aimv1alpha1.AIMService{})
}

func getObjectForTest[T client.Object](
	t *testing.T,
	ctx context.Context,
	c client.Client,
	key client.ObjectKey,
	obj T,
) T {
	t.Helper()
	if err := c.Get(ctx, key, obj); err != nil {
		t.Fatalf("get %T %s: %v", obj, key, err)
	}
	return obj
}

func assertInferenceServiceRuntime(
	t *testing.T,
	isvc *servingv1beta1.InferenceService,
	want string,
) {
	t.Helper()
	if isvc.Spec.Predictor.Model == nil ||
		isvc.Spec.Predictor.Model.Runtime == nil ||
		*isvc.Spec.Predictor.Model.Runtime != want {
		t.Fatalf("InferenceService ServingRuntime reference = %#v, want %q", isvc.Spec.Predictor.Model, want)
	}
}

// TestBuildInferenceServiceFromProfile_ReferencesRuntime pins the Phase C
// contract: the InferenceService references the projected KServe ServingRuntime
// (aim-<profile.Name>) via predictor.model.runtime + modelFormat and emits NO
// inline predictor (no container image, profile ConfigMap volume/mount, or
// framework env). The ServingRuntime carries all of that; the service only
// references it.
func TestBuildInferenceServiceFromProfile_ReferencesRuntime(t *testing.T) {
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA},
		},
	}
	profileSpec := sampleProfileSpec()
	profileStatus := &aimv1alpha2.AIMProfileStatus{
		Status: constants.AIMStatusReady,
		Resources: &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("8")},
		},
		// Affinity belongs on the ServingRuntime, never the overlay; pin that the
		// overlay drops it even when the profile resolved one.
		ResolvedNodeAffinity: &corev1.NodeAffinity{},
	}

	r := &ProfileServiceReconciler{}
	obs := ServiceObservation{
		ServiceFetchResult:    ServiceFetchResult{service: service},
		resolvedProfileSpec:   profileSpec,
		resolvedProfileStatus: profileStatus,
		profileName:           testProfileA,
		profileScope:          aimv1alpha1.AIMResolutionScopeNamespace,
	}
	r.composeDerivedNames(context.Background(), &obs)
	if obs.configErr != nil {
		t.Fatalf("composeDerivedNames returned error: %v", obs.configErr)
	}

	isvc := buildInferenceServiceFromProfile(service, obs)
	if isvc == nil {
		t.Fatalf("buildInferenceServiceFromProfile returned nil")
	}

	if isvc.Labels[constants.LabelService] != testServiceName {
		t.Errorf("missing service label: %v", isvc.Labels)
	}
	if isvc.Labels[constants.LabelProfile] != testProfileA {
		t.Errorf("missing profile label: %v", isvc.Labels)
	}

	model := isvc.Spec.Predictor.Model
	if model == nil {
		t.Fatalf("expected predictor.model to be set")
	}
	if model.Runtime == nil || *model.Runtime != serving.RuntimeName(testProfileA) {
		t.Errorf("predictor.model.runtime = %v, want %q", model.Runtime, serving.RuntimeName(testProfileA))
	}
	if model.ModelFormat.Name != serving.RuntimeModelFormat {
		t.Errorf("modelFormat.name = %q, want %q", model.ModelFormat.Name, serving.RuntimeModelFormat)
	}

	// No inline predictor: no explicit container, no inline image, no profile
	// ConfigMap volume, no framework env on the overlay.
	if len(isvc.Spec.Predictor.Containers) != 0 {
		t.Errorf("overlay must not inline predictor containers, got %d", len(isvc.Spec.Predictor.Containers))
	}
	if model.Image != "" {
		t.Errorf("overlay model must not carry an inline image, got %q", model.Image)
	}
	for _, v := range isvc.Spec.Predictor.Volumes {
		if v.Name == profileVolumePrefix {
			t.Errorf("overlay must not mount the profile ConfigMap volume %q (ServingRuntime owns it)", profileVolumePrefix)
		}
	}
	for _, e := range model.Env {
		if e.Name == constants.EnvAIMProfileID {
			t.Errorf("overlay must not carry framework env %q (ServingRuntime owns it)", constants.EnvAIMProfileID)
		}
	}
	if isvc.Spec.Predictor.Affinity != nil {
		t.Errorf("overlay must not carry node affinity (ServingRuntime owns it)")
	}
	// No service-level resource override -> the overlay leaves resources to the
	// ServingRuntime (empty requests/limits on the InferenceService model).
	if len(model.Resources.Requests) != 0 || len(model.Resources.Limits) != 0 {
		t.Errorf("overlay must not carry profile resources without a service override, got %+v", model.Resources)
	}
}

func TestBuildInferenceServiceFromProfile_ServiceAccountPrecedence(t *testing.T) {
	profileSpec := sampleProfileSpec()
	profileSpec.ServiceAccountName = "profile-sa"
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
	}
	obs := ServiceObservation{
		resolvedProfileSpec:   profileSpec,
		resolvedProfileStatus: &aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady},
		profileName:           testProfileA,
		isvcName:              "service-isvc",
	}

	isvc := buildInferenceServiceFromProfile(service, obs)
	if got := isvc.Spec.Predictor.ServiceAccountName; got != "profile-sa" {
		t.Fatalf("profile service account fallback = %q, want profile-sa", got)
	}

	service.Spec.ServiceAccountName = "service-sa"
	isvc = buildInferenceServiceFromProfile(service, obs)
	if got := isvc.Spec.Predictor.ServiceAccountName; got != "service-sa" {
		t.Fatalf("service account override = %q, want service-sa", got)
	}
}

// TestBuildInferenceServiceFromProfile_ConvergesRuntimeReference pins that an
// existing InferenceService follows the currently resolved profile rather than
// indefinitely serving through a stale projection.
func TestBuildInferenceServiceFromProfile_ConvergesRuntimeReference(t *testing.T) {
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA},
		},
	}
	existing := &servingv1beta1.InferenceService{
		Spec: servingv1beta1.InferenceServiceSpec{
			Predictor: servingv1beta1.PredictorSpec{
				Model: &servingv1beta1.ModelSpec{Runtime: ptr.To("aim-some-previous-runtime")},
			},
		},
	}

	obs := ServiceObservation{
		ServiceFetchResult: ServiceFetchResult{
			service:          service,
			inferenceService: controllerutils.FetchResult[*servingv1beta1.InferenceService]{Value: existing},
		},
		resolvedProfileSpec:   sampleProfileSpec(),
		resolvedProfileStatus: &aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady},
		profileName:           testProfileA,
		profileScope:          aimv1alpha1.AIMResolutionScopeNamespace,
	}
	(&ProfileServiceReconciler{}).composeDerivedNames(context.Background(), &obs)

	isvc := buildInferenceServiceFromProfile(service, obs)
	if isvc == nil {
		t.Fatalf("expected non-nil ISVC")
	}
	want := serving.RuntimeName(testProfileA)
	if got := isvc.Spec.Predictor.Model.Runtime; got == nil || *got != want {
		t.Errorf("ServingRuntime reference = %v, want resolved profile runtime %q", got, want)
	}
}

// TestBuildInferenceServiceFromProfile_StampsRuntimeProfile pins the lazy
// ServingRuntime-projection fast-path: a service stamps the backing profile name
// so the lazy ServingRuntime-keyed controller can materialize the ServingRuntime
// in the service's namespace before it exists. The annotation is stamped for
// BOTH scopes: a cluster profile is the cross-scope case, and a namespace
// profile needs it under Reduced mode (no eager per-profile ServingRuntime).
func TestBuildInferenceServiceFromProfile_StampsRuntimeProfile(t *testing.T) {
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: testClusterProfileName},
		},
	}
	obs := ServiceObservation{
		ServiceFetchResult:    ServiceFetchResult{service: service},
		resolvedProfileSpec:   sampleProfileSpec(),
		resolvedProfileStatus: &aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady},
		profileName:           testClusterProfileName,
		profileScope:          aimv1alpha1.AIMResolutionScopeCluster,
	}
	(&ProfileServiceReconciler{}).composeDerivedNames(context.Background(), &obs)

	isvc := buildInferenceServiceFromProfile(service, obs)
	if isvc == nil {
		t.Fatalf("expected non-nil ISVC")
	}
	if got := isvc.Annotations[constants.AnnotationRuntimeProfile]; got != testClusterProfileName {
		t.Errorf("cluster-scoped service must stamp runtime-profile annotation, got %q", got)
	}

	// Namespace scope stamps it too so a Reduced-mode namespace-profile-backed
	// service can be lazily completed.
	obs.profileScope = aimv1alpha1.AIMResolutionScopeNamespace
	nsISVC := buildInferenceServiceFromProfile(service, obs)
	if got := nsISVC.Annotations[constants.AnnotationRuntimeProfile]; got != testClusterProfileName {
		t.Errorf("namespace-scoped service must also stamp the runtime-profile annotation, got %q", got)
	}
}

// TestBuildInferenceServiceFromProfile_ServiceResourceOverrideOnModel pins that
// a service-level resource override carries the fully merged, valid resource
// block. KServe preserves resource-map keys inherited from a ServingRuntime when
// the InferenceService omits them, so sending only requests.memory could
// otherwise retain a smaller ServingRuntime limits.memory.
func TestBuildInferenceServiceFromProfile_ServiceResourceOverrideOnModel(t *testing.T) {
	override := &corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("64Gi")},
	}
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile:   &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA},
			Resources: override,
		},
	}
	profileStatus := &aimv1alpha2.AIMProfileStatus{
		Status: constants.AIMStatusReady,
		Resources: &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("32Gi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("48Gi"),
			},
		},
	}
	obs := ServiceObservation{
		ServiceFetchResult:    ServiceFetchResult{service: service},
		resolvedProfileSpec:   sampleProfileSpec(),
		resolvedProfileStatus: profileStatus,
		profileName:           testProfileA,
		profileScope:          aimv1alpha1.AIMResolutionScopeNamespace,
	}
	obs.effectiveResources = effectiveResourcesForService(service, obs.resolvedProfileSpec, profileStatus)
	(&ProfileServiceReconciler{}).composeDerivedNames(context.Background(), &obs)

	isvc := buildInferenceServiceFromProfile(service, obs)
	if isvc == nil {
		t.Fatalf("expected non-nil ISVC")
	}
	if got := isvc.Spec.Predictor.Model.Resources.Requests.Memory(); got.Cmp(resource.MustParse("64Gi")) != 0 {
		t.Errorf("service resource override not carried on model: got %v, want 64Gi", got)
	}
	if got := isvc.Spec.Predictor.Model.Resources.Limits.Memory(); got.Cmp(resource.MustParse("64Gi")) != 0 {
		t.Errorf("omitted memory limit must be raised to the request: got %v, want 64Gi", got)
	}
	if got := isvc.Spec.Predictor.Model.Resources.Requests.Cpu(); got.Cmp(resource.MustParse("4")) != 0 {
		t.Errorf("profile CPU request was not preserved: got %v, want 4", got)
	}
}

func TestPlanResources_serviceOverrideRescuesProfileReadiness(t *testing.T) {
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA},
			Resources: &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("16Gi")},
			},
		},
	}
	profileSpec := sampleProfileSpec()
	profileStatus := &aimv1alpha2.AIMProfileStatus{
		Status:     constants.AIMStatusNotAvailable,
		Deployable: true,
		Resources: &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceName("amd.com/gpu"): resource.MustParse("1"),
				corev1.ResourceCPU:                 resource.MustParse("4"),
				corev1.ResourceMemory:              resource.MustParse("32Gi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceName("amd.com/gpu"): resource.MustParse("1"),
				corev1.ResourceMemory:              resource.MustParse("48Gi"),
			},
		},
	}
	profile := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{Name: testProfileA, Namespace: "ns"},
		Spec: aimv1alpha2.AIMProfileSpec{
			AIMProfileSpecCommon: *profileSpec,
		},
		Status: *profileStatus,
	}
	node := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "smaller-node",
			Labels: map[string]string{
				aimprofile.AcceleratorLabelPrefix + "MI300X": "1",
				aimprofile.PartitioningSchemeLabelPrefix +
					aimprofile.PartitioningSchemeDefault: "1",
			},
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceName("amd.com/gpu"): resource.MustParse("1"),
				corev1.ResourceCPU:                 resource.MustParse("8"),
				corev1.ResourceMemory:              resource.MustParse("24Gi"),
			},
		},
	}
	reconciler := &ProfileServiceReconciler{}
	obs := reconciler.ComposeState(
		context.Background(),
		controllerutils.ReconcileContext[*aimv1alpha1.AIMService]{Object: service},
		ServiceFetchResult{
			service:            service,
			profile:            controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Value: profile},
			resourceMatchNodes: []corev1.Node{node},
		},
	)
	if obs.serviceResourceMatch.MatchingNodes != 1 {
		t.Fatalf("service resource match = %d, want 1", obs.serviceResourceMatch.MatchingNodes)
	}

	plan := reconciler.PlanResources(
		context.Background(),
		controllerutils.ReconcileContext[*aimv1alpha1.AIMService]{Object: service},
		obs,
	)

	var sawInferenceService bool
	for _, obj := range plan.GetToApply() {
		if _, ok := obj.(*servingv1beta1.InferenceService); ok {
			sawInferenceService = true
		}
	}
	if !sawInferenceService {
		t.Fatal("service override matching a node must allow InferenceService planning")
	}
	if health := obs.getProfileHealth(); health.State != constants.AIMStatusReady {
		t.Errorf("profile health = %s, want Ready; message=%q", health.State, health.Message)
	}

	obs.resolvedProfileStatus.Status = constants.AIMStatusProgressing
	if obs.profileReadyForService() {
		t.Fatal("service override must not bypass a profile that has not completed hardware observation")
	}
}

func TestBuildInferenceServiceFromProfile_NilSpecReturnsNil(t *testing.T) {
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
	}
	obs := ServiceObservation{
		ServiceFetchResult: ServiceFetchResult{service: service},
	}
	if isvc := buildInferenceServiceFromProfile(service, obs); isvc != nil {
		t.Fatalf("expected nil ISVC when profile spec is nil")
	}
}

// TestResolvedModelIdFromProfile verifies the model-id identity follows the
// AIM model-serving process's resolution chain
// `profile.model_id or config.model_id or config.aim_id`: ModelId wins, then
// modelSources[0].modelId, then aimId.
func TestResolvedModelIdFromProfile(t *testing.T) {
	// ModelId wins even when modelSources carry a different id (matches the
	// projected profile YAML consumed by the AIM model-serving process, which
	// writes profile.model_id from spec.ModelId).
	modelIDWins := sampleProfileSpec() // ModelId = qwen/qwen3-32b-fp8
	modelIDWins.ModelSources = []aimv1alpha1.AIMModelSource{
		{ModelID: "acme/my-finetune-v1", SourceURI: "hf://acme/my-finetune-v1"},
	}
	if got := resolvedModelId(modelIDWins); got != testModelIDFP8 {
		t.Errorf("ModelId set: resolvedModelId() = %q, want ModelId %q", got, testModelIDFP8)
	}

	// ModelId empty falls back to modelSources[0].modelId.
	viaSources := sampleProfileSpec()
	viaSources.ModelId = ""
	viaSources.ModelSources = []aimv1alpha1.AIMModelSource{
		{ModelID: "acme/my-finetune-v1", SourceURI: "hf://acme/my-finetune-v1"},
	}
	if got := resolvedModelId(viaSources); got != "acme/my-finetune-v1" {
		t.Errorf("ModelId empty + modelSources: resolvedModelId() = %q, want %q", got, "acme/my-finetune-v1")
	}

	// ModelId and modelSources empty falls back to aimId.
	viaAimID := sampleProfileSpec() // AimId = qwen/qwen3-32b
	viaAimID.ModelId = ""
	if got := resolvedModelId(viaAimID); got != "qwen/qwen3-32b" {
		t.Errorf("ModelId + modelSources empty: resolvedModelId() = %q, want aimId %q", got, "qwen/qwen3-32b")
	}
}

// TestBuildInferenceServiceFromProfile_AnnotationPropagation verifies cluster-auth
// annotations propagate from the AIMService to the InferenceService, unrelated
// annotations (including our own control annotations) are dropped, and the
// controller-owned model-id annotation is stamped from the resolved profile
// rather than any user-supplied value.
func TestBuildInferenceServiceFromProfile_AnnotationPropagation(t *testing.T) {
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testServiceName,
			Namespace: "ns",
			Annotations: map[string]string{
				"cluster-auth/allowed-group":            "ce0c754f-bb1b-63bb-5134-5501142effe7",
				"aim.eai.amd.com/model-id":              "user-tried-to-override",
				"aim.eai.amd.com/reconciliation-paused": "true",
				"example.com/foreign":                   "drop-me",
			},
		},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA},
		},
	}

	profileSpec := sampleProfileSpec()
	profileSpec.ModelSources = []aimv1alpha1.AIMModelSource{
		{ModelID: testModelIDFP8, SourceURI: "hf://qwen/qwen3-32b-fp8"},
	}
	profileStatus := &aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady}

	r := &ProfileServiceReconciler{}
	obs := ServiceObservation{
		ServiceFetchResult:    ServiceFetchResult{service: service},
		resolvedProfileSpec:   profileSpec,
		resolvedProfileStatus: profileStatus,
		profileName:           testProfileA,
		profileScope:          aimv1alpha1.AIMResolutionScopeNamespace,
	}
	r.composeDerivedNames(context.Background(), &obs)
	if obs.configErr != nil {
		t.Fatalf("composeDerivedNames returned error: %v", obs.configErr)
	}

	isvc := buildInferenceServiceFromProfile(service, obs)
	if isvc == nil {
		t.Fatalf("buildInferenceServiceFromProfile returned nil")
	}

	ann := isvc.Annotations
	if got := ann["cluster-auth/allowed-group"]; got != "ce0c754f-bb1b-63bb-5134-5501142effe7" {
		t.Errorf("expected cluster-auth annotation propagated, got %q", got)
	}
	if _, ok := ann["example.com/foreign"]; ok {
		t.Error("foreign annotation example.com/foreign must not be propagated")
	}
	if _, ok := ann["aim.eai.amd.com/reconciliation-paused"]; ok {
		t.Error("control annotation aim.eai.amd.com/reconciliation-paused must not be propagated")
	}
	if got := ann[constants.AnnotationModelId]; got != testModelIDFP8 {
		t.Errorf("model-id annotation = %q, want controller-owned %q (must not be overridable from service spec)", got, testModelIDFP8)
	}
}

func TestGetConfigHealth_NoErrorReturnsEmpty(t *testing.T) {
	obs := ServiceObservation{}
	if got := obs.getConfigHealth(); got.Component != "" {
		t.Errorf("expected empty ComponentHealth when configErr is nil, got %+v", got)
	}
}

func TestGetConfigHealth_SurfacesInvalidSpec(t *testing.T) {
	obs := ServiceObservation{
		configErr: errors.New("assemble profile YAML: invalid engineArgs"),
	}
	health := obs.getConfigHealth()

	if health.Component != "ProfileConfig" {
		t.Errorf("unexpected component: %q", health.Component)
	}
	if health.State != constants.AIMStatusFailed {
		t.Errorf("expected Failed state, got %q", health.State)
	}
	if health.DependencyType != controllerutils.DependencyTypeUpstream {
		t.Errorf("expected upstream dependency type, got %q", health.DependencyType)
	}
	if len(health.Errors) != 1 {
		t.Fatalf("expected a single wrapped error, got %d", len(health.Errors))
	}
	if categorized := controllerutils.CategorizeError(health.Errors[0]); categorized.Category() != controllerutils.ErrorCategoryInvalidSpec {
		t.Errorf("expected InvalidSpec category, got %v", categorized.Category())
	}
}

func TestGetComponentHealth_IncludesConfigAndRouteEntries(t *testing.T) {
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
	}
	obs := ServiceObservation{
		ServiceFetchResult: ServiceFetchResult{service: service},
		configErr:          errors.New("bad profile"),
	}
	entries := obs.GetComponentHealth(context.Background(), nil)
	var haveConfig bool
	for _, e := range entries {
		if e.Component == "ProfileConfig" {
			haveConfig = true
		}
		if e.Component == componentNameHTTPRoute {
			t.Errorf("HTTPRoute entry should be suppressed when routing is disabled")
		}
	}
	if !haveConfig {
		t.Errorf("expected ProfileConfig entry to be emitted when configErr is set")
	}
}

// TestGetComponentHealth_ScaleToZeroRequiresRouting verifies the profile
// pipeline surfaces the invalid scale-from-zero-without-routing combination as
// a ConfigValid-driving InvalidSpec error, even before a profile resolves.
func TestGetComponentHealth_ScaleToZeroRequiresRouting(t *testing.T) {
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
		Spec:       aimv1alpha1.AIMServiceSpec{MinReplicas: ptr.To(int32(0))},
	}
	obs := ServiceObservation{
		ServiceFetchResult: ServiceFetchResult{service: service},
	}

	entries := obs.GetComponentHealth(context.Background(), nil)

	var cfg *controllerutils.ComponentHealth
	for i := range entries {
		if entries[i].Component == v1alpha1service.ComponentScaleToZeroConfig {
			cfg = &entries[i]
		}
	}
	if cfg == nil {
		t.Fatalf("expected a ScaleToZeroConfig component health entry")
	}
	if cfg.State != constants.AIMStatusFailed {
		t.Errorf("ScaleToZeroConfig state = %q, want Failed", cfg.State)
	}
	if cfg.Reason != aimv1alpha1.AIMServiceReasonRoutingRequired {
		t.Errorf("ScaleToZeroConfig reason = %q, want %q", cfg.Reason, aimv1alpha1.AIMServiceReasonRoutingRequired)
	}
	if len(cfg.Errors) != 1 ||
		controllerutils.CategorizeError(cfg.Errors[0]).Category() != controllerutils.ErrorCategoryInvalidSpec {
		t.Errorf("expected a single InvalidSpec error, got %+v", cfg.Errors)
	}
}

// TestGetComponentHealth_MultiListenerRequiresHostname verifies the profile
// pipeline surfaces the host-pinning guard (multi-listener parent gateway with
// no routing hostnames) as a ConfigValid-driving InvalidSpec error on the
// HTTPRoute component, sharing the v1alpha1 helper.
func TestGetComponentHealth_MultiListenerRequiresHostname(t *testing.T) {
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
	}
	service.Spec.Routing = &aimv1alpha1.AIMRuntimeRoutingConfig{
		Enabled:    ptr.To(true),
		GatewayRef: &gatewayapiv1.ParentReference{Name: "gw"},
	}
	gateway := &gatewayapiv1.Gateway{
		Spec: gatewayapiv1.GatewaySpec{
			Listeners: []gatewayapiv1.Listener{
				{Name: "a", Protocol: gatewayapiv1.HTTPProtocolType, Port: 80},
				{Name: "b", Protocol: gatewayapiv1.HTTPProtocolType, Port: 80},
			},
		},
	}
	obs := ServiceObservation{
		ServiceFetchResult: ServiceFetchResult{
			service: service,
			gateway: controllerutils.FetchResult[*gatewayapiv1.Gateway]{Value: gateway},
		},
	}

	entries := obs.GetComponentHealth(context.Background(), nil)

	var cfg *controllerutils.ComponentHealth
	for i := range entries {
		if entries[i].Component == v1alpha1service.ComponentRouteConfig {
			cfg = &entries[i]
		}
	}
	if cfg == nil {
		t.Fatalf("expected a RouteConfig component health entry")
	}
	if cfg.State != constants.AIMStatusFailed {
		t.Errorf("RouteConfig state = %q, want Failed", cfg.State)
	}
	if cfg.Reason != v1alpha1service.ReasonRouteHostnameRequired {
		t.Errorf("RouteConfig reason = %q, want %q", cfg.Reason, v1alpha1service.ReasonRouteHostnameRequired)
	}
	if len(cfg.Errors) != 1 ||
		controllerutils.CategorizeError(cfg.Errors[0]).Category() != controllerutils.ErrorCategoryInvalidSpec {
		t.Errorf("expected a single InvalidSpec error, got %+v", cfg.Errors)
	}
}

// TestGetComponentHealth_ProfileNotFound_SuppressesDownstream pins F13 part 1:
// when no profile resolves, the planner intentionally skips creating ISVC
// and HTTPRoute, so reporting them as "Creating"/"not found" would mislead
// users. Only the gating Profile (and RuntimeConfig if observed) component
// should be reported.
func TestGetComponentHealth_ProfileNotFound_SuppressesDownstream(t *testing.T) {
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
	}
	notFound := apierrors.NewNotFound(schema.GroupResource{}, "missing-isvc")
	obs := ServiceObservation{
		ServiceFetchResult: ServiceFetchResult{
			service:          service,
			inferenceService: controllerutils.FetchResult[*servingv1beta1.InferenceService]{Error: notFound},
		},
	}

	entries := obs.GetComponentHealth(context.Background(), nil)

	var profileHealth *controllerutils.ComponentHealth
	for i, e := range entries {
		switch e.Component {
		case "Profile":
			profileHealth = &entries[i]
		case componentNameInferenceService, componentNameHTTPRoute, componentNameProfileCache:
			t.Errorf("downstream component %q should be suppressed when no profile resolved; got %+v", e.Component, e)
		}
	}
	if profileHealth == nil {
		t.Fatalf("expected a Profile component health entry")
	}
	if profileHealth.State != constants.AIMStatusFailed {
		t.Errorf("ProfileNotFound state = %q, want Failed (so the framework rolls it up to Ready=False/ProfileNotFound)", profileHealth.State)
	}
	if profileHealth.Reason != aimv1alpha1.AIMServiceReasonProfileNotFound {
		t.Errorf("ProfileNotFound reason = %q, want %q", profileHealth.Reason, aimv1alpha1.AIMServiceReasonProfileNotFound)
	}
}

// TestGetComponentHealth_BaseProfile_SuppressesDownstream pins F13 part 1
// for the base-profile path: the resolved profile exists but is missing
// aimId or modelSources. The planner skips downstream creation and the user
// must fix the profile before anything can progress.
func TestGetComponentHealth_BaseProfile_SuppressesDownstream(t *testing.T) {
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
	}
	baseProfileSpec := &aimv1alpha2.AIMProfileSpecCommon{
		Image:            "registry.example.com/base:0.1",
		AcceleratorModel: "EPYC_ZEN5",
		AcceleratorType:  "cpu",
		AcceleratorCount: 128,
	}
	baseProfileStatus := &aimv1alpha2.AIMProfileStatus{
		Status:     constants.AIMStatusReady,
		Deployable: false,
	}
	notFound := apierrors.NewNotFound(schema.GroupResource{}, "missing-isvc")
	obs := ServiceObservation{
		ServiceFetchResult: ServiceFetchResult{
			service:          service,
			inferenceService: controllerutils.FetchResult[*servingv1beta1.InferenceService]{Error: notFound},
		},
		profileName:           "base-profile",
		profileScope:          aimv1alpha1.AIMResolutionScopeNamespace,
		resolvedProfileSpec:   baseProfileSpec,
		resolvedProfileStatus: baseProfileStatus,
	}

	entries := obs.GetComponentHealth(context.Background(), nil)

	var profileHealth *controllerutils.ComponentHealth
	for i, e := range entries {
		switch e.Component {
		case "Profile":
			profileHealth = &entries[i]
		case componentNameInferenceService, componentNameHTTPRoute, componentNameProfileCache:
			t.Errorf("downstream component %q should be suppressed when profile is a base profile; got %+v", e.Component, e)
		}
	}
	if profileHealth == nil {
		t.Fatalf("expected a Profile component health entry")
	}
	if profileHealth.State != constants.AIMStatusFailed {
		t.Errorf("base profile state = %q, want Failed", profileHealth.State)
	}
	if profileHealth.Reason != aimv1alpha1.AIMServiceReasonBaseProfile {
		t.Errorf("base profile reason = %q, want %q", profileHealth.Reason, aimv1alpha1.AIMServiceReasonBaseProfile)
	}
}

// TestGetComponentHealth_ResolvedProfile_KeepsDownstream confirms the
// suppression in F13 only fires for terminal-by-default Profile failures
// (base profile / not-found). When the profile resolves to a real spec —
// even if it's still progressing — downstream conditions remain visible
// so users can watch cache / ISVC progress.
func TestGetComponentHealth_ResolvedProfile_KeepsDownstream(t *testing.T) {
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
	}
	notFound := apierrors.NewNotFound(schema.GroupResource{}, "missing-isvc")
	obs := ServiceObservation{
		ServiceFetchResult: ServiceFetchResult{
			service:          service,
			inferenceService: controllerutils.FetchResult[*servingv1beta1.InferenceService]{Error: notFound},
		},
		profileName:           testProfileA,
		profileScope:          aimv1alpha1.AIMResolutionScopeNamespace,
		resolvedProfileSpec:   sampleProfileSpec(),
		resolvedProfileStatus: &aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady, Deployable: true},
	}

	entries := obs.GetComponentHealth(context.Background(), nil)

	var sawISVC bool
	for _, e := range entries {
		if e.Component == componentNameInferenceService {
			sawISVC = true
		}
	}
	if !sawISVC {
		t.Errorf("expected InferenceService entry for a deployable resolved profile; got %+v", entries)
	}
}

// TestGetComponentHealth_ScaleToZeroIdleReportsReady pins the v1alpha2 fix for
// the idle-readiness gap: a healthily-idled scale-to-zero service (KEDA scaled
// the predictor to zero, no pods, HPA reporting ScalingDisabled) must report
// the InferenceServicePods and HPA components as Ready/ScaledToZero rather than
// stalling at "no pods"/"waiting" forever.
func TestGetComponentHealth_ScaleToZeroIdleReportsReady(t *testing.T) {
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			MinReplicas: ptr.To(int32(0)),
			MaxReplicas: ptr.To(int32(3)),
		},
	}
	// Routing enabled (promoted field from the inlined AIMRuntimeConfig) so the
	// scale-to-zero routing prerequisite passes and does not add an unrelated
	// ConfigValid=False entry.
	service.Spec.Routing = &aimv1alpha1.AIMRuntimeRoutingConfig{Enabled: ptr.To(true)}

	// HPA at the idle floor: ScalingActive=False with KEDA's authoritative
	// ScalingDisabled reason.
	idleHPA := &autoscalingv2.HorizontalPodAutoscaler{
		Status: autoscalingv2.HorizontalPodAutoscalerStatus{
			Conditions: []autoscalingv2.HorizontalPodAutoscalerCondition{
				{Type: autoscalingv2.ScalingActive, Status: corev1.ConditionFalse, Reason: "ScalingDisabled"},
			},
		},
	}
	emptyPods := &controllerutils.FetchResult[*corev1.PodList]{Value: &corev1.PodList{}}

	obs := ServiceObservation{
		ServiceFetchResult: ServiceFetchResult{
			service:              service,
			inferenceService:     controllerutils.FetchResult[*servingv1beta1.InferenceService]{Value: &servingv1beta1.InferenceService{}},
			inferenceServicePods: emptyPods,
			hpa:                  controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler]{Value: idleHPA},
		},
		resolvedProfileSpec:   sampleProfileSpec(),
		resolvedProfileStatus: &aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady, Deployable: true},
		profileName:           testProfileA,
		profileScope:          aimv1alpha1.AIMResolutionScopeNamespace,
	}

	entries := obs.GetComponentHealth(context.Background(), nil)

	var pods, hpa *controllerutils.ComponentHealth
	for i := range entries {
		switch entries[i].Component {
		case "InferenceServicePods":
			pods = &entries[i]
		case "HPA":
			hpa = &entries[i]
		}
	}

	if pods == nil {
		t.Fatalf("expected an InferenceServicePods entry; got %+v", entries)
	}
	if pods.State != constants.AIMStatusReady || pods.Reason != aimv1alpha1.AIMServiceReasonScaledToZero {
		t.Errorf("InferenceServicePods = %q/%q, want Ready/ScaledToZero", pods.State, pods.Reason)
	}

	if hpa == nil {
		t.Fatalf("expected an HPA entry; got %+v", entries)
	}
	if hpa.State != constants.AIMStatusReady || hpa.Reason != aimv1alpha1.AIMServiceReasonScaledToZero {
		t.Errorf("HPA = %q/%q, want Ready/ScaledToZero", hpa.State, hpa.Reason)
	}
}

func TestGetInferenceServiceHealth_ReadyPodsOverrideStaleGeneration(t *testing.T) {
	inferenceService := &servingv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Name:       testServiceName,
			Namespace:  "ns",
			Generation: 2,
		},
		Status: servingv1beta1.InferenceServiceStatus{
			Status: duckv1.Status{
				ObservedGeneration: 1,
				Conditions: duckv1.Conditions{{
					Type:   "Ready",
					Status: corev1.ConditionTrue,
				}},
			},
		},
	}
	readyPods := &corev1.PodList{Items: []corev1.Pod{{
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{{
				Type:   corev1.PodReady,
				Status: corev1.ConditionTrue,
			}},
		},
	}}}

	obs := ServiceObservation{ServiceFetchResult: ServiceFetchResult{
		inferenceService: controllerutils.FetchResult[*servingv1beta1.InferenceService]{Value: inferenceService},
		inferenceServicePods: &controllerutils.FetchResult[*corev1.PodList]{
			Value: readyPods,
		},
	}}
	health := obs.getInferenceServiceHealth()
	if health.State != constants.AIMStatusReady ||
		health.Reason != aimv1alpha1.AIMServiceReasonRuntimeScaling {
		t.Fatalf("health = %#v, want Ready/RuntimeScaling", health)
	}

	obs.inferenceServicePods.Value.Items[0].Status.Conditions[0].Status = corev1.ConditionFalse
	health = obs.getInferenceServiceHealth()
	if health.State != constants.AIMStatusProgressing ||
		health.Reason != aimv1alpha1.AIMServiceReasonCreatingRuntime {
		t.Fatalf("health without a ready pod = %#v, want Progressing/CreatingRuntime", health)
	}
}

func TestPlanProfileCache_SkipsWhenExisting(t *testing.T) {
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA},
		},
	}
	obs := ServiceObservation{
		ServiceFetchResult: ServiceFetchResult{
			service:      service,
			profileCache: controllerutils.FetchResult[*aimv1alpha2.AIMProfileCache]{Value: &aimv1alpha2.AIMProfileCache{}},
		},
		profileName:         testProfileA,
		resolvedProfileSpec: sampleProfileSpec(),
	}
	if got := planProfileCache(service, obs); got != nil {
		t.Fatalf("planProfileCache should return nil when cache already exists, got %+v", got)
	}
}

// buildTestISVC is a small helper that returns a freshly-built ISVC for a
// service spec with a ready profile. It keeps the autoscaling tests short
// and focused on the one dimension each case is probing.
func buildTestISVC(t *testing.T, spec aimv1alpha1.AIMServiceSpec) *servingv1beta1.InferenceService {
	t.Helper()
	spec.Profile = &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA}

	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
		Spec:       spec,
	}
	obs := ServiceObservation{
		ServiceFetchResult:    ServiceFetchResult{service: service},
		resolvedProfileSpec:   sampleProfileSpec(),
		resolvedProfileStatus: &aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady},
		profileName:           testProfileA,
		profileScope:          aimv1alpha1.AIMResolutionScopeNamespace,
	}
	(&ProfileServiceReconciler{}).composeDerivedNames(context.Background(), &obs)
	if obs.configErr != nil {
		t.Fatalf("composeDerivedNames error: %v", obs.configErr)
	}
	isvc := buildInferenceServiceFromProfile(service, obs)
	if isvc == nil {
		t.Fatalf("expected non-nil ISVC")
	}
	return isvc
}

// TestBuildInferenceServiceFromProfile_Replicas_DefaultsToOne guards against
// the v1alpha2 builder silently dropping MaxReplicas (previously it only set
// MinReplicas, leaving MaxReplicas=0 which KServe interprets as "no cap").
func TestBuildInferenceServiceFromProfile_Replicas_DefaultsToOne(t *testing.T) {
	isvc := buildTestISVC(t, aimv1alpha1.AIMServiceSpec{})

	if got := isvc.Spec.Predictor.MinReplicas; got == nil || *got != 1 {
		t.Errorf("MinReplicas: want *1, got %v", got)
	}
	if got := isvc.Spec.Predictor.MaxReplicas; got != 1 {
		t.Errorf("MaxReplicas: want 1, got %d", got)
	}
	if got := isvc.Annotations[constants.AnnotationKServeAutoscalerClass]; got != constants.AutoscalerClassNone {
		t.Errorf("autoscaler class: want %q, got %q", constants.AutoscalerClassNone, got)
	}
}

func TestBuildInferenceServiceFromProfile_Replicas_FixedDisablesHPA(t *testing.T) {
	isvc := buildTestISVC(t, aimv1alpha1.AIMServiceSpec{
		Replicas: ptr.To(int32(3)),
	})

	if got := isvc.Spec.Predictor.MinReplicas; got == nil || *got != 3 {
		t.Errorf("MinReplicas: want *3, got %v", got)
	}
	if got := isvc.Spec.Predictor.MaxReplicas; got != 3 {
		t.Errorf("MaxReplicas: want 3 (HPA disabled, min==max), got %d", got)
	}
	if got := isvc.Annotations[constants.AnnotationKServeAutoscalerClass]; got != constants.AutoscalerClassNone {
		t.Errorf("autoscaler class: want %q (fixed replicas disables HPA), got %q",
			constants.AutoscalerClassNone, got)
	}
}

func TestBuildInferenceServiceFromProfile_Replicas_AutoScalingHandsOffToExternal(t *testing.T) {
	isvc := buildTestISVC(t, aimv1alpha1.AIMServiceSpec{
		MinReplicas: ptr.To(int32(2)),
		MaxReplicas: ptr.To(int32(5)),
	})

	if got := isvc.Spec.Predictor.MinReplicas; got == nil || *got != 2 {
		t.Errorf("MinReplicas: want *2, got %v", got)
	}
	if got := isvc.Spec.Predictor.MaxReplicas; got != 5 {
		t.Errorf("MaxReplicas: want 5, got %d", got)
	}
	// autoscalerClass=external is what tells KServe's KEDA reconciler to
	// short-circuit; the AIMService controller writes the actual
	// ScaledObject (see scaledobject.go). Asserting "keda" here would
	// re-introduce two ScaledObjects fighting over the same Deployment.
	if got := isvc.Annotations[constants.AnnotationKServeAutoscalerClass]; got != constants.AutoscalerClassExternal {
		t.Errorf("autoscaler class: want %q (AIM Engine owns the ScaledObject under min/max), got %q",
			constants.AutoscalerClassExternal, got)
	}
}

// TestBuildInferenceServiceFromProfile_SharedCacheNotOverlaid pins that a
// profile-owned (Shared) cache is NOT overlaid onto the InferenceService — the
// ServingRuntime mounts it instead, so overlaying it would duplicate the
// ServingRuntime's volume.
func TestBuildInferenceServiceFromProfile_SharedCacheNotOverlaid(t *testing.T) {
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA},
			// Shared is the default; state it explicitly for the reader.
			Caching: &aimv1alpha1.AIMServiceCachingConfig{Mode: aimv1alpha1.CachingModeShared},
		},
	}
	obs := ServiceObservation{
		ServiceFetchResult: ServiceFetchResult{
			service:      service,
			profileCache: controllerutils.FetchResult[*aimv1alpha2.AIMProfileCache]{Value: readyCacheFixture()},
		},
		resolvedProfileSpec:   profileSpecWithModelSources(),
		resolvedProfileStatus: &aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady},
		profileName:           testProfileA,
		profileScope:          aimv1alpha1.AIMResolutionScopeNamespace,
		profileCacheReady:     true,
	}
	(&ProfileServiceReconciler{}).composeDerivedNames(context.Background(), &obs)

	isvc := buildInferenceServiceFromProfile(service, obs)
	if isvc == nil {
		t.Fatalf("expected non-nil ISVC")
	}
	if len(isvc.Spec.Predictor.Volumes) != 0 {
		t.Errorf("Shared (profile-owned) cache must not be overlaid; got volumes %+v", isvc.Spec.Predictor.Volumes)
	}
	if len(isvc.Spec.Predictor.Model.VolumeMounts) != 0 {
		t.Errorf("Shared (profile-owned) cache must not add model mounts; got %+v", isvc.Spec.Predictor.Model.VolumeMounts)
	}
}

// TestBuildInferenceServiceFromProfile_DedicatedCacheOverlaid pins that a
// service-owned (Dedicated) cache lands on the overlay: the PVC volume, the
// model volume mount, and the framework redirect environment (overriding
// same-named entries from the ServingRuntime container).
func TestBuildInferenceServiceFromProfile_DedicatedCacheOverlaid(t *testing.T) {
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA},
			Caching: &aimv1alpha1.AIMServiceCachingConfig{Mode: aimv1alpha1.CachingModeDedicated},
		},
	}
	obs := ServiceObservation{
		ServiceFetchResult: ServiceFetchResult{
			service:      service,
			profileCache: controllerutils.FetchResult[*aimv1alpha2.AIMProfileCache]{Value: readyCacheFixture()},
		},
		resolvedProfileSpec:   profileSpecWithModelSources(),
		resolvedProfileStatus: &aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady},
		profileName:           testProfileA,
		profileScope:          aimv1alpha1.AIMResolutionScopeNamespace,
		profileCacheReady:     true,
	}
	(&ProfileServiceReconciler{}).composeDerivedNames(context.Background(), &obs)

	isvc := buildInferenceServiceFromProfile(service, obs)
	if isvc == nil {
		t.Fatalf("expected non-nil ISVC")
	}

	if !volumeRefsPVC(isvc.Spec.Predictor.Volumes, "weights-pvc") {
		t.Errorf("Dedicated (service-owned) cache PVC must be overlaid; got volumes %+v", isvc.Spec.Predictor.Volumes)
	}
	model := isvc.Spec.Predictor.Model
	if len(model.VolumeMounts) == 0 {
		t.Errorf("Dedicated cache must add a model volume mount")
	}
	// Redirect environment must be present so the AIM model-serving process
	// loads from the local cache, overriding same-named entries from the
	// ServingRuntime container.
	env := envMap(model.Env)
	if env[constants.EnvAIMModelID] == "" {
		t.Errorf("Dedicated cache overlay must carry the %s redirect env", constants.EnvAIMModelID)
	}
}

func TestComposeProfileRuntimeProjection_DedicatedServiceUsesSharedProjectionCache(t *testing.T) {
	spec := profileSpecWithModelSources()
	profile := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testProfileA,
			Namespace: "ns",
			UID:       types.UID("profile-uid"),
		},
		Spec: aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: *spec},
		Status: aimv1alpha2.AIMProfileStatus{
			Status:     constants.AIMStatusReady,
			Deployable: true,
		},
	}
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA},
			Caching: &aimv1alpha1.AIMServiceCachingConfig{Mode: aimv1alpha1.CachingModeDedicated},
		},
	}
	dedicatedCache := readyCacheFixture()
	dedicatedCache.Spec.Mode = aimv1alpha2.ProfileCacheModeDedicated
	dedicatedCache.Status.Artifacts["weights"] = aimv1alpha1.AIMResolvedArtifact{
		Name:                  "weights",
		Model:                 "org/model",
		Status:                constants.AIMStatusReady,
		PersistentVolumeClaim: "dedicated-pvc",
		MountPoint:            "/workspace/cache/org/model",
	}
	sharedCache := readyCacheFixture()
	sharedCache.Spec.Mode = aimv1alpha2.ProfileCacheModeShared
	sharedCache.Status.Artifacts["weights"] = aimv1alpha1.AIMResolvedArtifact{
		Name:                  "weights",
		Model:                 "org/model",
		Status:                constants.AIMStatusReady,
		PersistentVolumeClaim: "shared-pvc",
		MountPoint:            "/workspace/cache/org/model",
	}
	notFound := apierrors.NewNotFound(schema.GroupResource{}, serving.RuntimeName(testProfileA))
	obs := ServiceObservation{
		ServiceFetchResult: ServiceFetchResult{
			service:                 service,
			profile:                 controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Value: profile},
			profileCache:            controllerutils.FetchResult[*aimv1alpha2.AIMProfileCache]{Value: dedicatedCache},
			runtimeProfileCache:     controllerutils.FetchResult[*aimv1alpha2.AIMProfileCache]{Value: sharedCache},
			profileRuntime:          controllerutils.FetchResult[*kservev1alpha1.ServingRuntime]{Error: notFound},
			profileRuntimeConfigMap: controllerutils.FetchResult[*corev1.ConfigMap]{Error: notFound},
		},
		resolvedProfileSpec:   spec,
		resolvedProfileStatus: &profile.Status,
		profileName:           testProfileA,
		profileScope:          aimv1alpha1.AIMResolutionScopeNamespace,
		hasModelSources:       true,
		profileCacheReady:     true,
	}

	(&ProfileServiceReconciler{}).composeProfileRuntimeProjection(&obs)

	if obs.configErr != nil {
		t.Fatalf("composeProfileRuntimeProjection() error = %v", obs.configErr)
	}
	if obs.desiredProfileRuntime == nil {
		t.Fatal("expected desired profile ServingRuntime")
	}
	if !volumeRefsPVC(obs.desiredProfileRuntime.Spec.Volumes, "shared-pvc") {
		t.Fatalf("runtime projection must use producer-selected Shared cache; volumes = %+v",
			obs.desiredProfileRuntime.Spec.Volumes)
	}
	if volumeRefsPVC(obs.desiredProfileRuntime.Spec.Volumes, "dedicated-pvc") {
		t.Fatalf("runtime projection must not use the service-owned Dedicated cache; volumes = %+v",
			obs.desiredProfileRuntime.Spec.Volumes)
	}
}

func TestBuildInferenceServiceFromProfile_DedicatedCacheOverridesDirectVLLMModelReference(t *testing.T) {
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Caching: &aimv1alpha1.AIMServiceCachingConfig{Mode: aimv1alpha1.CachingModeDedicated},
		},
	}
	profileSpec := profileSpecWithModelSources()
	profileSpec.Engine = "vllm"
	profileSpec.AcceleratorVendor = aimv1alpha1.AcceleratorVendorNVIDIA
	cache := readyCacheFixture()
	obs := ServiceObservation{
		ServiceFetchResult: ServiceFetchResult{
			service:      service,
			profileCache: controllerutils.FetchResult[*aimv1alpha2.AIMProfileCache]{Value: cache},
		},
		resolvedProfileSpec:   profileSpec,
		resolvedProfileStatus: &aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady},
		profileName:           testProfileA,
		isvcName:              "service-isvc",
		profileCacheReady:     true,
	}

	isvc := buildInferenceServiceFromProfile(service, obs)
	env := envMap(isvc.Spec.Predictor.Model.Env)
	if got := env[constants.EnvAIMVLLMModel]; got != "/workspace/cache/org/model" {
		t.Fatalf("%s = %q, want dedicated cache mount", constants.EnvAIMVLLMModel, got)
	}
	if env["HF_HUB_OFFLINE"] != "1" || env["TRANSFORMERS_OFFLINE"] != "1" {
		t.Fatalf("dedicated direct-vLLM cache must force offline mode: %#v", env)
	}
}

func profileSpecWithModelSources() *aimv1alpha2.AIMProfileSpecCommon {
	spec := sampleProfileSpec()
	spec.ModelSources = []aimv1alpha1.AIMModelSource{{ModelID: "org/model", SourceURI: "hf://org/model"}}
	return spec
}

func readyCacheFixture() *aimv1alpha2.AIMProfileCache {
	return &aimv1alpha2.AIMProfileCache{
		Status: aimv1alpha2.AIMProfileCacheStatus{
			Status: constants.AIMStatusReady,
			Artifacts: map[string]aimv1alpha1.AIMResolvedArtifact{
				"weights": {
					Name:                  "weights",
					Model:                 "org/model",
					Status:                constants.AIMStatusReady,
					PersistentVolumeClaim: "weights-pvc",
					MountPoint:            "/workspace/cache/org/model",
				},
			},
		},
	}
}

func volumeRefsPVC(volumes []corev1.Volume, claimName string) bool {
	for _, v := range volumes {
		if v.PersistentVolumeClaim != nil && v.PersistentVolumeClaim.ClaimName == claimName {
			return true
		}
	}
	return false
}

func envMap(vars []corev1.EnvVar) map[string]string {
	m := make(map[string]string, len(vars))
	for _, e := range vars {
		m[e.Name] = e.Value
	}
	return m
}

func TestPlanProfileCache_CreatesSharedCache(t *testing.T) {
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA},
		},
	}
	profileSpec := sampleProfileSpec()
	profileSpec.ModelSources = []aimv1alpha1.AIMModelSource{{
		ModelID:   "org/model",
		SourceURI: "hf://org/model",
	}}

	obs := ServiceObservation{
		ServiceFetchResult:  ServiceFetchResult{service: service},
		profileName:         testProfileA,
		profileScope:        aimv1alpha1.AIMResolutionScopeNamespace,
		resolvedProfileSpec: profileSpec,
	}

	cache := planProfileCache(service, obs)
	if cache == nil {
		t.Fatalf("expected profile cache to be planned")
	}
	if cache.Namespace != "ns" {
		t.Errorf("unexpected namespace: %q", cache.Namespace)
	}
	if cache.Spec.ProfileName != testProfileA {
		t.Errorf("unexpected profile name on cache spec: %q", cache.Spec.ProfileName)
	}
	if cache.Spec.Mode != aimv1alpha2.ProfileCacheModeShared {
		t.Errorf("expected shared cache mode, got %q", cache.Spec.Mode)
	}
	if cache.Labels[constants.LabelService] != testServiceName {
		t.Errorf("expected service label on cache, got %v", cache.Labels)
	}
}

// TestPlanProfileCache_ServiceCachingEnvReachesCache locks the regression fix:
// for a cluster-scoped (or overlay) profile there is no profile caching.env, so
// the service's spec.caching.env is the only credential source that reaches the
// download Job. obs.profile.Value is nil here to model the cluster-scoped path.
func TestPlanProfileCache_ServiceCachingEnvReachesCache(t *testing.T) {
	token := corev1.EnvVar{
		Name: "HF_TOKEN",
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "hf-token"},
				Key:                  "token",
			},
		},
	}
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA},
			Caching: &aimv1alpha1.AIMServiceCachingConfig{Env: []corev1.EnvVar{token}},
		},
	}
	profileSpec := sampleProfileSpec()
	profileSpec.ModelSources = []aimv1alpha1.AIMModelSource{{
		ModelID:   "org/model",
		SourceURI: "hf://org/model",
	}}

	obs := ServiceObservation{
		ServiceFetchResult:  ServiceFetchResult{service: service},
		profileName:         testProfileA,
		profileScope:        aimv1alpha1.AIMResolutionScopeCluster,
		resolvedProfileSpec: profileSpec,
	}

	cache := planProfileCache(service, obs)
	if cache == nil {
		t.Fatalf("expected profile cache to be planned")
	}
	if len(cache.Spec.Env) != 1 || cache.Spec.Env[0].Name != "HF_TOKEN" {
		t.Fatalf("service caching.env must reach cache.spec.env, got %+v", cache.Spec.Env)
	}
	if cache.Spec.Env[0].ValueFrom == nil || cache.Spec.Env[0].ValueFrom.SecretKeyRef == nil {
		t.Errorf("secretKeyRef must be preserved on the cache env, got %+v", cache.Spec.Env[0])
	}
}

// TestPlanProfileCache_ServiceCachingEnvOverridesProfile verifies the merge
// precedence: when both the namespace profile and the service set caching.env,
// the service value wins on conflicting names.
func TestPlanProfileCache_ServiceCachingEnvOverridesProfile(t *testing.T) {
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA},
			Caching: &aimv1alpha1.AIMServiceCachingConfig{
				Env: []corev1.EnvVar{{Name: "HF_TOKEN", Value: "from-service"}},
			},
		},
	}
	profileSpec := sampleProfileSpec()
	profileSpec.ModelSources = []aimv1alpha1.AIMModelSource{{ModelID: "org/model", SourceURI: "hf://org/model"}}

	profile := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{Name: testProfileA, Namespace: "ns"},
		Spec: aimv1alpha2.AIMProfileSpec{
			Caching: &aimv1alpha2.AIMProfileCachingConfig{
				Env: []corev1.EnvVar{
					{Name: "HF_TOKEN", Value: "from-profile"},
					{Name: "PROFILE_ONLY", Value: "profile-only-val"},
				},
			},
		},
	}

	obs := ServiceObservation{
		ServiceFetchResult:  ServiceFetchResult{service: service},
		profileName:         testProfileA,
		profileScope:        aimv1alpha1.AIMResolutionScopeNamespace,
		resolvedProfileSpec: profileSpec,
	}
	obs.profile.Value = profile

	cache := planProfileCache(service, obs)
	if cache == nil {
		t.Fatalf("expected profile cache to be planned")
	}
	got := envMap(cache.Spec.Env)
	if got["HF_TOKEN"] != "from-service" {
		t.Errorf("service caching.env must override profile on conflict, got %q", got["HF_TOKEN"])
	}
	if got["PROFILE_ONLY"] != "profile-only-val" {
		t.Errorf("profile-only caching.env key must be preserved, got %q", got["PROFILE_ONLY"])
	}
}

// TestPlanProfileCache_PropagatesRuntimeConfigRef verifies the service's
// AIMRuntimeConfig reference flows onto the AIMProfileCache so a named
// AIMRuntimeConfig's env can feed the download Job (instead of always the
// default-named config).
func TestPlanProfileCache_PropagatesRuntimeConfigRef(t *testing.T) {
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile:          &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA},
			RuntimeConfigRef: aimv1alpha1.RuntimeConfigRef{Name: "fast"},
		},
	}
	profileSpec := sampleProfileSpec()
	profileSpec.ModelSources = []aimv1alpha1.AIMModelSource{{ModelID: "org/model", SourceURI: "hf://org/model"}}

	obs := ServiceObservation{
		ServiceFetchResult:  ServiceFetchResult{service: service},
		profileName:         testProfileA,
		profileScope:        aimv1alpha1.AIMResolutionScopeNamespace,
		resolvedProfileSpec: profileSpec,
	}

	cache := planProfileCache(service, obs)
	if cache == nil {
		t.Fatalf("expected profile cache to be planned")
	}
	if cache.Spec.Name != "fast" {
		t.Errorf("expected runtimeConfigRef to propagate, got %q", cache.Spec.Name)
	}
}

// TestPlanResources_ScaleToZeroEmitsScaledObject verifies the v1alpha2
// pipeline wires the ScaledObject planner. Asserts at the GVK level so the
// v1alpha1 planner tests remain the source of truth for resource shape.
func TestPlanResources_ScaleToZeroEmitsScaledObject(t *testing.T) {
	t.Setenv(constants.EnvAIMGatewayActivationScope, constants.GatewayActivationScopeHTTPRoute)

	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile:     &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA},
			MinReplicas: ptr.To(int32(0)),
			MaxReplicas: ptr.To(int32(3)),
			AutoScaling: &aimv1alpha1.AIMServiceAutoScaling{
				Metrics: []aimv1alpha1.AIMServiceMetricsSpec{{
					Type: "PodMetric",
					PodMetric: &aimv1alpha1.AIMServicePodMetricSource{
						Metric: &aimv1alpha1.AIMServicePodMetric{
							Backend:     "opentelemetry",
							MetricNames: []string{"vllm:num_requests_running"},
							Query:       "vllm:num_requests_running",
						},
						Target: &aimv1alpha1.AIMServiceMetricTarget{Type: "Value", Value: "1"},
					},
				}},
			},
		},
	}
	service.Spec.Routing = &aimv1alpha1.AIMRuntimeRoutingConfig{Enabled: ptr.To(true)}

	r := &ProfileServiceReconciler{}
	obs := ServiceObservation{
		ServiceFetchResult:  ServiceFetchResult{service: service},
		resolvedProfileSpec: sampleProfileSpec(),
		// Deployable=true mirrors what the AIMProfile reconciler stamps
		// once the profile validates and is what isDeployable() short-
		// circuits on. Setting it here keeps the fixture spec minimal
		// (no ModelSources) so PlanResources does not divert into the
		// AIMProfileCache branch, which would early-return on the
		// unready cache and skip the scale-to-zero resources we are
		// asserting on.
		resolvedProfileStatus: &aimv1alpha2.AIMProfileStatus{
			Status:     constants.AIMStatusReady,
			Deployable: true,
		},
		profileName:      testProfileA,
		profileScope:     aimv1alpha1.AIMResolutionScopeNamespace,
		profileAssembled: true,
	}
	r.composeDerivedNames(context.Background(), &obs)
	if obs.configErr != nil {
		t.Fatalf("composeDerivedNames: %v", obs.configErr)
	}

	pr := r.PlanResources(context.Background(),
		controllerutils.ReconcileContext[*aimv1alpha1.AIMService]{Object: service},
		obs,
	)

	var sawScaledObject bool
	for _, obj := range pr.GetToApply() {
		gvk := obj.GetObjectKind().GroupVersionKind()
		if gvk.Group == "keda.sh" && gvk.Kind == "ScaledObject" {
			sawScaledObject = true
		}
	}
	if !sawScaledObject {
		t.Errorf("scale-to-zero v1alpha2 service must produce a KEDA ScaledObject")
	}
}

func TestPlanResourcesSchedulesActivationMetricGraceDeadline(t *testing.T) {
	t.Setenv(constants.EnvAIMGatewayActivationScope, constants.GatewayActivationScopeHTTPRoute)
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			MinReplicas: ptr.To(int32(0)),
		},
	}
	service.Spec.Routing = &aimv1alpha1.AIMRuntimeRoutingConfig{Enabled: ptr.To(true)}
	hpa := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Generation: 1},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			Metrics: []autoscalingv2.MetricSpec{{
				Type: autoscalingv2.ExternalMetricSourceType,
				External: &autoscalingv2.ExternalMetricSource{
					Metric: autoscalingv2.MetricIdentifier{Name: "s0-test-service"},
					Target: autoscalingv2.MetricTarget{
						Type:  autoscalingv2.ValueMetricType,
						Value: ptr.To(resource.MustParse("1")),
					},
				},
			}},
		},
		Status: autoscalingv2.HorizontalPodAutoscalerStatus{
			ObservedGeneration: ptr.To(int64(1)),
			Conditions: []autoscalingv2.HorizontalPodAutoscalerCondition{{
				Type:   autoscalingv2.ScalingActive,
				Status: corev1.ConditionFalse,
				Reason: "FailedGetExternalMetric",
			}},
		},
	}
	obs := ServiceObservation{ServiceFetchResult: ServiceFetchResult{
		service: service,
		hpa:     controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler]{Value: hpa},
		inferenceServicePods: &controllerutils.FetchResult[*corev1.PodList]{
			Value: &corev1.PodList{Items: []corev1.Pod{{}}},
		},
	}}

	plan := (&ProfileServiceReconciler{}).PlanResources(
		context.Background(),
		controllerutils.ReconcileContext[*aimv1alpha1.AIMService]{Object: service},
		obs,
	)
	const gracePeriod = 3 * time.Minute
	if plan.RequeueAfter < gracePeriod-10*time.Second || plan.RequeueAfter > gracePeriod {
		t.Errorf("RequeueAfter=%s, want approximately %s", plan.RequeueAfter, gracePeriod)
	}
}

func TestResolveEffectiveResourcesFromProfile(t *testing.T) {
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
	}
	profileSpec := sampleProfileSpec()
	memBlock := &corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("32Gi")},
		Limits:   corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("48Gi")},
	}

	tests := []struct {
		name   string
		status *aimv1alpha2.AIMProfileStatus
		want   *corev1.ResourceRequirements
	}{
		{
			name:   "nil status returns nil",
			status: nil,
			want:   nil,
		},
		{
			name:   "profile not ready returns nil",
			status: &aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusProgressing, Resources: memBlock},
			want:   nil,
		},
		{
			name:   "profile ready returns merged resources",
			status: &aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady, Resources: memBlock},
			want:   memBlock,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveEffectiveResourcesFromProfile(service, profileSpec, tc.status)
			switch {
			case tc.want == nil:
				if got != nil {
					t.Errorf("want nil, got %+v", got)
				}
			case got == nil:
				t.Errorf("want non-nil, got nil")
			default:
				if got.Limits.Memory().Cmp(*tc.want.Limits.Memory()) != 0 {
					t.Errorf("limits.memory: want %v, got %v", tc.want.Limits.Memory(), got.Limits.Memory())
				}
				if got.Requests.Memory().Cmp(*tc.want.Requests.Memory()) != 0 {
					t.Errorf("requests.memory: want %v, got %v", tc.want.Requests.Memory(), got.Requests.Memory())
				}
			}
		})
	}
}

// TestResolveEffectiveResourcesFromProfile_ServiceOverrideWins verifies the
// service-level Resources override takes precedence over the profile's
// computed resources (matching resolveResourcesFromProfile's precedence).
func TestResolveEffectiveResourcesFromProfile_ServiceOverrideWins(t *testing.T) {
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Resources: &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("16Gi")},
			},
		},
	}
	profileSpec := sampleProfileSpec()
	profileStatus := &aimv1alpha2.AIMProfileStatus{
		Status: constants.AIMStatusReady,
		Resources: &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("128Gi")},
		},
	}

	got := resolveEffectiveResourcesFromProfile(service, profileSpec, profileStatus)
	if got == nil {
		t.Fatal("expected non-nil resources")
	}
	if got.Requests.Memory().Cmp(resource.MustParse("16Gi")) != 0 {
		t.Errorf("service override should win; got %v, want 16Gi", got.Requests.Memory())
	}
}

func TestMergeResourceRequirements_oneSidedOverrideKeepsValidPair(t *testing.T) {
	base := &corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("32Gi")},
		Limits:   corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("48Gi")},
	}

	t.Run("larger request raises inherited limit", func(t *testing.T) {
		got := mergeResourceRequirements(base, &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("64Gi")},
		})
		if got.Requests.Memory().Cmp(resource.MustParse("64Gi")) != 0 {
			t.Errorf("request = %s, want 64Gi", got.Requests.Memory().String())
		}
		if got.Limits.Memory().Cmp(resource.MustParse("64Gi")) != 0 {
			t.Errorf("limit = %s, want 64Gi", got.Limits.Memory().String())
		}
	})

	t.Run("smaller limit lowers inherited request", func(t *testing.T) {
		got := mergeResourceRequirements(base, &corev1.ResourceRequirements{
			Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("16Gi")},
		})
		if got.Requests.Memory().Cmp(resource.MustParse("16Gi")) != 0 {
			t.Errorf("request = %s, want 16Gi", got.Requests.Memory().String())
		}
		if got.Limits.Memory().Cmp(resource.MustParse("16Gi")) != 0 {
			t.Errorf("limit = %s, want 16Gi", got.Limits.Memory().String())
		}
	})

	t.Run("incompatible explicit pair is rejected", func(t *testing.T) {
		got := mergeResourceRequirements(base, &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("64Gi")},
			Limits:   corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("48Gi")},
		})
		if err := validateResourceRequirements(&got); err == nil {
			t.Fatal("expected incompatible explicit request and limit to be rejected")
		}
	})

	t.Run("one-sided extended resource request is mirrored", func(t *testing.T) {
		device := corev1.ResourceName("amd.com/gpu")
		got := mergeResourceRequirements(
			&corev1.ResourceRequirements{
				Requests: corev1.ResourceList{device: resource.MustParse("4")},
				Limits:   corev1.ResourceList{device: resource.MustParse("4")},
			},
			&corev1.ResourceRequirements{
				Requests: corev1.ResourceList{device: resource.MustParse("2")},
			},
		)
		request := got.Requests[device]
		if request.Cmp(resource.MustParse("2")) != 0 {
			t.Errorf("GPU request = %s, want 2", request.String())
		}
		limit := got.Limits[device]
		if limit.Cmp(resource.MustParse("2")) != 0 {
			t.Errorf("GPU limit = %s, want 2", limit.String())
		}
	})

	t.Run("unequal explicit extended resource pair is rejected", func(t *testing.T) {
		device := corev1.ResourceName("amd.com/gpu")
		got := mergeResourceRequirements(nil, &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{device: resource.MustParse("1")},
			Limits:   corev1.ResourceList{device: resource.MustParse("2")},
		})
		if err := validateResourceRequirements(&got); err == nil {
			t.Fatal("expected unequal explicit GPU request and limit to be rejected")
		}
	})
}

func TestPlanProfileCache_DedicatedHonorsServiceCachingMode(t *testing.T) {
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testServiceName,
			Namespace: "ns",
			UID:       "service-uid-123",
		},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA},
			Caching: &aimv1alpha1.AIMServiceCachingConfig{
				Mode: aimv1alpha1.CachingModeDedicated,
			},
		},
	}
	profileSpec := sampleProfileSpec()
	profileSpec.ModelSources = []aimv1alpha1.AIMModelSource{{
		ModelID:   "org/model",
		SourceURI: "hf://org/model",
	}}

	obs := ServiceObservation{
		ServiceFetchResult:  ServiceFetchResult{service: service},
		profileName:         testProfileA,
		profileScope:        aimv1alpha1.AIMResolutionScopeNamespace,
		resolvedProfileSpec: profileSpec,
	}

	cache := planProfileCache(service, obs)
	if cache == nil {
		t.Fatalf("expected profile cache to be planned for Dedicated service")
	}
	if cache.Spec.Mode != aimv1alpha2.ProfileCacheModeDedicated {
		t.Fatalf("Dedicated AIMService caching must produce a Dedicated AIMProfileCache, got %q", cache.Spec.Mode)
	}

	// And the planned cache name must match what the dedicated lookup
	// pathway expects, so a follow-up reconcile finds the exact same
	// cache without listing.
	expected, err := GenerateProfileCacheName(testProfileA, "ns", testServiceName, "service-uid-123", aimv1alpha1.CachingModeDedicated, aimv1alpha1.AIMResolutionScopeNamespace)
	if err != nil {
		t.Fatalf("generate expected name: %v", err)
	}
	if cache.Name != expected {
		t.Fatalf("Dedicated cache name does not match deterministic-lookup formula: got %q, want %q", cache.Name, expected)
	}
}

func TestPlanResources_LegacyProfileConfigMapCleanupRequiresSafeTransition(t *testing.T) {
	service, configMap := legacyProfileConfigMapCleanupFixture(t)

	stale := currentReadyInferenceService(service.Namespace)
	stale.Generation++
	notReady := currentReadyInferenceService(service.Namespace)
	notReady.Status.Conditions[0].Status = corev1.ConditionFalse

	tests := []struct {
		name             string
		inferenceService controllerutils.FetchResult[*servingv1beta1.InferenceService]
		wantDelete       bool
	}{
		{
			name: "current Ready InferenceService without reference",
			inferenceService: controllerutils.FetchResult[*servingv1beta1.InferenceService]{
				Value: currentReadyInferenceService(service.Namespace, "unrelated-config-map"),
			},
			wantDelete: true,
		},
		{
			name: "current Ready InferenceService with legacy reference",
			inferenceService: controllerutils.FetchResult[*servingv1beta1.InferenceService]{
				Value: currentReadyInferenceService(service.Namespace, "unrelated-config-map", configMap.Name),
			},
		},
		{
			name: "missing InferenceService",
			inferenceService: controllerutils.FetchResult[*servingv1beta1.InferenceService]{
				Error: apierrors.NewNotFound(
					schema.GroupResource{Group: "serving.kserve.io", Resource: "inferenceservices"},
					service.Name,
				),
			},
		},
		{
			name: "InferenceService fetch failed",
			inferenceService: controllerutils.FetchResult[*servingv1beta1.InferenceService]{
				Error: errors.New("fetch InferenceService"),
			},
		},
		{
			name:             "stale InferenceService generation",
			inferenceService: controllerutils.FetchResult[*servingv1beta1.InferenceService]{Value: stale},
		},
		{
			name:             "InferenceService Ready False",
			inferenceService: controllerutils.FetchResult[*servingv1beta1.InferenceService]{Value: notReady},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := planLegacyProfileConfigMapCleanupForTest(service, configMap, test.inferenceService)
			got := legacyConfigMapDeleteCount(t, plan, service.Namespace, configMap.Name)
			if test.wantDelete && got != 1 {
				t.Fatalf("legacy ConfigMap deletes = %d, want 1", got)
			}
			if !test.wantDelete && got != 0 {
				t.Fatalf("legacy ConfigMap deletes = %d, want 0", got)
			}
		})
	}
}

func TestPlanResources_LegacyProfileConfigMapCleanupSafetyGuards(t *testing.T) {
	service, configMap := legacyProfileConfigMapCleanupFixture(t)
	withoutOwner := configMap.DeepCopy()
	withoutOwner.OwnerReferences = nil
	wrongOwner := configMap.DeepCopy()
	wrongOwner.OwnerReferences[0].UID = "different-service-uid"
	withoutManagedBy := configMap.DeepCopy()
	withoutManagedBy.Labels = nil

	tests := []struct {
		name      string
		configMap *corev1.ConfigMap
	}{
		{name: "no owner reference", configMap: withoutOwner},
		{name: "wrong owner UID", configMap: wrongOwner},
		{name: "missing managed-by label", configMap: withoutManagedBy},
		{name: "no legacy ConfigMap observed", configMap: nil},
	}

	inferenceService := controllerutils.FetchResult[*servingv1beta1.InferenceService]{
		Value: currentReadyInferenceService(service.Namespace),
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := planLegacyProfileConfigMapCleanupForTest(service, test.configMap, inferenceService)
			if got := legacyConfigMapDeleteCount(t, plan, service.Namespace, configMap.Name); got != 0 {
				t.Fatalf("legacy ConfigMap deletes = %d, want 0", got)
			}
		})
	}
}

func legacyProfileConfigMapCleanupFixture(t *testing.T) (*aimv1alpha1.AIMService, *corev1.ConfigMap) {
	t.Helper()

	const serviceUID = "svc-uid-legacy-1"
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns", UID: serviceUID},
	}
	legacyName, err := legacyProfileConfigMapName(service.Name)
	if err != nil {
		t.Fatalf("legacyProfileConfigMapName: %v", err)
	}
	return service, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      legacyName,
			Namespace: service.Namespace,
			Labels: map[string]string{
				constants.LabelK8sManagedBy: constants.LabelValueManagedBy,
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "aim.eai.amd.com/v1alpha1",
				Kind:       "AIMService",
				Name:       service.Name,
				UID:        serviceUID,
				Controller: ptr.To(true),
			}},
		},
	}
}

func currentReadyInferenceService(namespace string, configMapNames ...string) *servingv1beta1.InferenceService {
	volumes := make([]corev1.Volume, 0, len(configMapNames))
	for _, name := range configMapNames {
		volumes = append(volumes, corev1.Volume{
			Name: "config-map-" + name,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: name},
				},
			},
		})
	}
	return &servingv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Name:       testServiceName,
			Namespace:  namespace,
			Generation: 1,
		},
		Spec: servingv1beta1.InferenceServiceSpec{
			Predictor: servingv1beta1.PredictorSpec{
				PodSpec: servingv1beta1.PodSpec{Volumes: volumes},
			},
		},
		Status: servingv1beta1.InferenceServiceStatus{
			Status: duckv1.Status{
				ObservedGeneration: 1,
				Conditions: duckv1.Conditions{{
					Type:   "Ready",
					Status: corev1.ConditionTrue,
				}},
			},
		},
	}
}

func planLegacyProfileConfigMapCleanupForTest(
	service *aimv1alpha1.AIMService,
	configMap *corev1.ConfigMap,
	inferenceService controllerutils.FetchResult[*servingv1beta1.InferenceService],
) controllerutils.PlanResult {
	reconciler := &ProfileServiceReconciler{}
	obs := ServiceObservation{
		ServiceFetchResult: ServiceFetchResult{
			service:                service,
			inferenceService:       inferenceService,
			legacyProfileConfigMap: controllerutils.FetchResult[*corev1.ConfigMap]{Value: configMap},
		},
	}
	return reconciler.PlanResources(
		context.Background(),
		controllerutils.ReconcileContext[*aimv1alpha1.AIMService]{Object: service},
		obs,
	)
}

func legacyConfigMapDeleteCount(
	t *testing.T,
	plan controllerutils.PlanResult,
	namespace string,
	name string,
) int {
	t.Helper()

	count := 0
	for _, obj := range plan.GetToDelete() {
		configMap, ok := obj.(*corev1.ConfigMap)
		if !ok {
			continue
		}
		if configMap.Name != name || configMap.Namespace != namespace {
			t.Fatalf("plan queued deletion of unexpected ConfigMap %s/%s", configMap.Namespace, configMap.Name)
		}
		count++
	}
	return count
}
