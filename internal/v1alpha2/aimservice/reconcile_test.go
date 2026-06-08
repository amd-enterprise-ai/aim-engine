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

	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

const (
	testProfileA    = "profile-a"
	testServiceName = "svc"
	testModelIDFP8  = "qwen/qwen3-32b-fp8"

	componentNameInferenceService = "InferenceService"
	componentNameHTTPRoute        = "HTTPRoute"
	componentNameProfileCache     = "ProfileCache"
)

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

func TestComposeState_ClusterProfileFallback(t *testing.T) {
	r := &ProfileServiceReconciler{}
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
	}
	clusterProfile := &aimv1alpha2.AIMClusterProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-profile"},
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
	if obs.profileName != "cluster-profile" {
		t.Errorf("cluster profile name not resolved: %q", obs.profileName)
	}
}

func TestBuildInferenceServiceFromProfile_BasicShape(t *testing.T) {
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
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("8"),
				corev1.ResourceMemory: resource.MustParse("32Gi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("16"),
				corev1.ResourceMemory: resource.MustParse("64Gi"),
			},
		},
	}

	r := &ProfileServiceReconciler{}
	obs := ServiceObservation{
		ServiceFetchResult: ServiceFetchResult{
			service: service,
		},
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

	if isvc.Namespace != "ns" {
		t.Errorf("unexpected namespace: %q", isvc.Namespace)
	}
	if isvc.Labels[constants.LabelService] != testServiceName {
		t.Errorf("missing service label: %v", isvc.Labels)
	}
	if isvc.Labels[constants.LabelProfile] != testProfileA {
		t.Errorf("missing profile label: %v", isvc.Labels)
	}

	if len(isvc.Spec.Predictor.Containers) == 0 {
		t.Fatalf("expected at least one container")
	}
	container := isvc.Spec.Predictor.Containers[0]
	if container.Image != profileSpec.Image {
		t.Errorf("container image mismatch: got %q want %q", container.Image, profileSpec.Image)
	}

	// Profile volume + mount should be present.
	foundProfileVol := false
	for _, v := range isvc.Spec.Predictor.Volumes {
		if v.Name == profileVolumePrefix {
			foundProfileVol = true
		}
	}
	if !foundProfileVol {
		t.Errorf("profile volume %q not found on ISVC", profileVolumePrefix)
	}

	foundProfileMount := false
	for _, m := range container.VolumeMounts {
		if m.Name == profileVolumePrefix {
			foundProfileMount = true
		}
	}
	if !foundProfileMount {
		t.Errorf("profile volume mount %q not found on container", profileVolumePrefix)
	}

	// AIM_PROFILE_ID env var should be present and reference custom/<aimId>/...
	foundEnv := false
	for _, e := range container.Env {
		if e.Name == constants.EnvAIMProfileID {
			foundEnv = true
			if e.Value == "" {
				t.Errorf("AIM_PROFILE_ID env value is empty")
			}
		}
	}
	if !foundEnv {
		t.Errorf("expected AIM_PROFILE_ID env var on container")
	}

	// Resource requirements from profile status should be propagated.
	if container.Resources.Requests.Cpu().Cmp(resource.MustParse("8")) != 0 {
		t.Errorf("CPU requests not propagated: %v", container.Resources.Requests.Cpu())
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
// runtime resolution chain `profile.model_id or config.model_id or config.aim_id`:
// ModelId wins, then modelSources[0].modelId, then aimId.
func TestResolvedModelIdFromProfile(t *testing.T) {
	// ModelId wins even when modelSources carry a different id (matches the
	// runtime, which writes profile.model_id from spec.ModelId).
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

func TestBuildInferenceServiceFromProfile_Replicas_AutoScalingEnablesKEDA(t *testing.T) {
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
	if got := isvc.Annotations[constants.AnnotationKServeAutoscalerClass]; got != constants.AutoscalerClassKeda {
		t.Errorf("autoscaler class: want %q (min/max triggers KEDA), got %q",
			constants.AutoscalerClassKeda, got)
	}
}

// TestBuildInferenceServiceFromProfile_FrameworkEnvVarsWinOverProfile verifies
// that a profile author cannot override AIM_PROFILE_ID via ContainerEnv. This
// is essential because the framework's value is the only one that actually
// resolves to the projected ConfigMap path.
func TestBuildInferenceServiceFromProfile_FrameworkEnvVarsWinOverProfile(t *testing.T) {
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA},
		},
	}
	profileSpec := sampleProfileSpec()
	profileSpec.ContainerEnv = []corev1.EnvVar{
		{Name: constants.EnvAIMProfileID, Value: "hijacked"},
		{Name: "PROFILE_ONLY", Value: "kept"},
	}

	obs := ServiceObservation{
		ServiceFetchResult:    ServiceFetchResult{service: service},
		resolvedProfileSpec:   profileSpec,
		resolvedProfileStatus: &aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady},
		profileName:           testProfileA,
		profileScope:          aimv1alpha1.AIMResolutionScopeNamespace,
	}
	(&ProfileServiceReconciler{}).composeDerivedNames(context.Background(), &obs)

	isvc := buildInferenceServiceFromProfile(service, obs)
	if isvc == nil {
		t.Fatalf("expected non-nil ISVC")
	}
	env := envMap(isvc.Spec.Predictor.Containers[0].Env)

	if env[constants.EnvAIMProfileID] == "hijacked" {
		t.Errorf("profile.ContainerEnv must not be able to override %s", constants.EnvAIMProfileID)
	}
	if env[constants.EnvAIMProfileID] == "" {
		t.Errorf("framework env var %s is missing", constants.EnvAIMProfileID)
	}
	if env["PROFILE_ONLY"] != "kept" {
		t.Errorf("non-overlapping profile env var should survive: got %q", env["PROFILE_ONLY"])
	}
}

// TestBuildInferenceServiceFromProfile_FrameworkBeatsOverlayContainerEnv pins
// the v1alpha2 precedence rule: user-supplied ContainerEnv flows through the
// overlay's ContainerEnv (already merged by ApplyProfileCopyOverrides in
// ComposeState) and is then overlaid by framework AIM_* vars. AIM_* identity
// variables belong to the controller; the user cannot reshape them via
// spec.profileOverrides.containerEnv.
//
// This is a deliberate behaviour change from the inline-override era where
// service-level overrides were re-applied after framework vars. It is the
// natural consequence of moving overrides into a real overlay AIMProfile —
// the AIMService never re-applies overrides at deploy time.
func TestBuildInferenceServiceFromProfile_FrameworkBeatsOverlayContainerEnv(t *testing.T) {
	// Simulate the post-overlay state: the overlay's spec.containerEnv
	// already carries the user's containerEnv override. The reconciler
	// must NOT let that hijack a framework AIM_* var.
	overlaySpec := sampleProfileSpec()
	overlaySpec.ContainerEnv = []corev1.EnvVar{
		{Name: constants.EnvAIMProfileID, Value: "user-attempt-via-overlay"},
		{Name: "USER_FLAG", Value: "kept"},
	}

	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA},
		},
	}
	obs := ServiceObservation{
		ServiceFetchResult:    ServiceFetchResult{service: service},
		resolvedProfileSpec:   overlaySpec,
		resolvedProfileStatus: &aimv1alpha2.AIMProfileStatus{Status: constants.AIMStatusReady},
		profileName:           testProfileA,
		profileScope:          aimv1alpha1.AIMResolutionScopeNamespace,
	}
	(&ProfileServiceReconciler{}).composeDerivedNames(context.Background(), &obs)

	isvc := buildInferenceServiceFromProfile(service, obs)
	if isvc == nil {
		t.Fatalf("expected non-nil ISVC")
	}
	env := envMap(isvc.Spec.Predictor.Containers[0].Env)

	if env[constants.EnvAIMProfileID] == "user-attempt-via-overlay" {
		t.Errorf("framework %s must win over overlay containerEnv, got user value", constants.EnvAIMProfileID)
	}
	if env[constants.EnvAIMProfileID] == "" {
		t.Errorf("framework %s must be present", constants.EnvAIMProfileID)
	}
	if env["USER_FLAG"] != "kept" {
		t.Errorf("non-AIM_* user containerEnv via the overlay should survive, got %q", env["USER_FLAG"])
	}
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
