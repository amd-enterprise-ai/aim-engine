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
	"encoding/json"
	"strings"
	"testing"

	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/trust"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimprofile"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/profileyaml"
)

// TestHasProfileOverrides_DetectsRealOverrides asserts hasProfileOverrides
// detects every wide-override field. A regression here would silently turn off
// overlay materialisation for that field and the override would vanish.
func TestHasProfileOverrides_DetectsRealOverrides(t *testing.T) {
	cases := map[string]*aimv1alpha1.AIMServiceProfileOverrides{
		"modelSources":     {ModelSources: []aimv1alpha1.AIMModelSource{{ModelID: "x", SourceURI: "hf://x"}}},
		"features":         {Features: []string{"adapters"}},
		"acceleratorModel": {AcceleratorModel: "MI325X"},
		"acceleratorCount": {AcceleratorCount: ptr.To[int32](2)},
		"containerEnv":     {ContainerEnv: []corev1.EnvVar{{Name: "X", Value: "y"}}},
		"engineEnv":        {EngineEnv: map[string]string{"a": "b"}},
		"engineArgs":       {EngineArgs: mustJSON(t, map[string]any{"dtype": "fp16"})},
	}
	for name, o := range cases {
		t.Run(name, func(t *testing.T) {
			if !hasProfileOverrides(o) {
				t.Fatalf("hasProfileOverrides should be true when %s is set", name)
			}
		})
	}
}

// TestHasProfileOverrides_TreatsEmptyAsNoOp guards against accidentally
// materialising a per-service overlay for users who emit `profileOverrides: {}`
// from a templating layer. Two services that both end up with nil/empty
// overrides on the same seed must continue to point at the seed (and hence
// share the namespace cache) — that's the v1alpha1 sharing parity the chain
// promises.
func TestHasProfileOverrides_TreatsEmptyAsNoOp(t *testing.T) {
	cases := map[string]*aimv1alpha1.AIMServiceProfileOverrides{
		"nil":   nil,
		"empty": {},
		"all-zero": {
			ModelSources: []aimv1alpha1.AIMModelSource{},
			Features:     []string{},
			ContainerEnv: []corev1.EnvVar{},
			EngineEnv:    map[string]string{},
		},
	}
	for name, o := range cases {
		t.Run(name, func(t *testing.T) {
			if hasProfileOverrides(o) {
				t.Fatalf("hasProfileOverrides should be false for %s", name)
			}
		})
	}
}

// TestBuildServiceOverlayProfile_AppliesAllOverrides exercises the full set of
// supported fields end-to-end and asserts the overlay AIMProfile is shaped
// correctly: name is namespace-scoped, ContainerEnv merges with profile
// baseline, engine fields are merged not replaced (ApplyProfileCopyOverrides
// semantics), identity fields (acceleratorModel, modelSources) replace
// outright, and inherited fields (pull secrets, SA) flow from the seed.
func TestBuildServiceOverlayProfile_AppliesAllOverrides(t *testing.T) {
	seed := sampleProfileSpec()
	seed.ContainerEnv = []corev1.EnvVar{
		{Name: "BASE_KEEPER", Value: "preserved"},
		{Name: "OVERRIDABLE", Value: "from-profile"},
	}
	seed.EngineEnv = map[string]string{"K1": "v-from-profile", "K2": "stays"}
	seed.EngineArgs = mustJSON(t, map[string]any{"max-model-len": 8192.0, "dtype": "auto"})
	seed.Features = []string{"native-feature"}
	seed.ImagePullSecrets = []corev1.LocalObjectReference{{Name: "ghcr-secret"}}
	seed.ServiceAccountName = "seed-sa"
	seedContract, err := profileyaml.Inspect([]byte(`metadata:
  engine: vllm
  gpu: MI300X
  gpu_count: 1
  manual_selection_only: false
  metric: latency
  precision: fp16
  type: optimized
  capabilities:
    reasoning: true
engine_args: {}
env_vars: {}
`))
	if err != nil {
		t.Fatalf("Inspect seed profile YAML: %v", err)
	}
	seedProfile := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:        testProfileA,
			Annotations: profileyaml.Mark(nil, seedContract),
		},
		Spec: aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: *seed},
	}

	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testServiceName,
			Namespace: "ns",
			UID:       types.UID("service-uid"),
		},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA},
			ProfileOverrides: &aimv1alpha1.AIMServiceProfileOverrides{
				ModelSources: []aimv1alpha1.AIMModelSource{
					{ModelID: "user/finetune-v2", SourceURI: "s3://bucket/weights/"},
				},
				Features:         []string{"adapters", "native-feature"},
				AcceleratorModel: "MI325X",
				AcceleratorCount: ptr.To[int32](2),
				ContainerEnv: []corev1.EnvVar{
					{Name: "OVERRIDABLE", Value: "from-service"},
					{Name: "USER_NEW", Value: "appended"},
				},
				EngineEnv:  map[string]string{"K1": "v-from-service", "K3": "added"},
				EngineArgs: mustJSON(t, map[string]any{"dtype": "fp16", "tensor-parallel-size": 2.0}),
			},
		},
	}

	obs := ServiceObservation{
		ServiceFetchResult: ServiceFetchResult{
			service: service,
			profile: controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Value: seedProfile},
		},
		resolvedProfileSpec:   seed,
		resolvedProfileStatus: &aimv1alpha2.AIMProfileStatus{},
		profileName:           testProfileA,
		profileScope:          aimv1alpha1.AIMResolutionScopeNamespace,
	}

	overlay, overlaySpec, err := buildServiceOverlayProfile(service, obs)
	if err != nil {
		t.Fatalf("buildServiceOverlayProfile returned error: %v", err)
	}

	if overlay.Namespace != "ns" {
		t.Errorf("overlay namespace: got %q, want %q", overlay.Namespace, "ns")
	}
	if overlay.Annotations[AnnotationOverlayService] != testServiceName {
		t.Errorf("overlay missing service back-reference annotation, got %v", overlay.Annotations)
	}
	assertInheritedOverlayContract(t, overlay.Annotations)

	if len(overlaySpec.ModelSources) != 1 || overlaySpec.ModelSources[0].ModelID != "user/finetune-v2" {
		t.Errorf("ModelSources not replaced: %+v", overlaySpec.ModelSources)
	}
	if overlaySpec.ModelId != "user/finetune-v2" {
		t.Errorf("ModelId did not pick up the override modelId: %q", overlaySpec.ModelId)
	}
	if overlaySpec.AcceleratorModel != "MI325X" {
		t.Errorf("AcceleratorModel: got %q want MI325X", overlaySpec.AcceleratorModel)
	}
	if overlaySpec.AcceleratorCount != 2 {
		t.Errorf("AcceleratorCount: got %d want 2", overlaySpec.AcceleratorCount)
	}
	if len(overlaySpec.Features) != 2 ||
		overlaySpec.Features[0] != "native-feature" ||
		overlaySpec.Features[1] != "adapters" {
		t.Errorf("Features must be a stable set union, got %v", overlaySpec.Features)
	}

	envByName := make(map[string]string, len(overlaySpec.ContainerEnv))
	for _, e := range overlaySpec.ContainerEnv {
		envByName[e.Name] = e.Value
	}
	if envByName["BASE_KEEPER"] != "preserved" {
		t.Errorf("BASE_KEEPER from profile must be preserved when override does not touch it: %q", envByName["BASE_KEEPER"])
	}
	if envByName["OVERRIDABLE"] != "from-service" {
		t.Errorf("OVERRIDABLE: override must replace profile value, got %q", envByName["OVERRIDABLE"])
	}
	if envByName["USER_NEW"] != "appended" {
		t.Errorf("USER_NEW: override-only env var must be appended, got %q", envByName["USER_NEW"])
	}

	if overlaySpec.EngineEnv["K1"] != "v-from-service" {
		t.Errorf("EngineEnv K1: override must replace profile value, got %q", overlaySpec.EngineEnv["K1"])
	}
	if overlaySpec.EngineEnv["K2"] != "stays" {
		t.Errorf("EngineEnv K2: profile-only key must be preserved, got %q", overlaySpec.EngineEnv["K2"])
	}
	if overlaySpec.EngineEnv["K3"] != "added" {
		t.Errorf("EngineEnv K3: override-only key must be appended, got %q", overlaySpec.EngineEnv["K3"])
	}

	args := decodeJSONObject(t, overlaySpec.EngineArgs)
	if args["dtype"] != "fp16" {
		t.Errorf("EngineArgs dtype: override must replace, got %v", args["dtype"])
	}
	if args["max-model-len"] != 8192.0 {
		t.Errorf("EngineArgs max-model-len: profile baseline must be preserved, got %v", args["max-model-len"])
	}
	if args["tensor-parallel-size"] != 2.0 {
		t.Errorf("EngineArgs tensor-parallel-size: override-only key must be added, got %v", args["tensor-parallel-size"])
	}

	if len(overlaySpec.ImagePullSecrets) != 1 || overlaySpec.ImagePullSecrets[0].Name != "ghcr-secret" {
		t.Errorf("ImagePullSecrets must inherit from seed: %+v", overlaySpec.ImagePullSecrets)
	}
	if overlaySpec.ServiceAccountName != "seed-sa" {
		t.Errorf("ServiceAccountName must inherit from seed: %q", overlaySpec.ServiceAccountName)
	}
}

func assertInheritedOverlayContract(t *testing.T, annotations map[string]string) {
	t.Helper()

	overlayContract, found, err := profileyaml.FromAnnotations(annotations)
	if err != nil {
		t.Fatalf("parse overlay YAML contract: %v", err)
	}
	if !found {
		t.Fatal("overlay YAML contract annotation is missing")
	}
	for _, field := range []string{"gpu", "gpu_count", "manual_selection_only"} {
		if !overlayContract.HasMetadataField(field) {
			t.Errorf("overlay YAML contract missing inherited field %q: %s", field, overlayContract.Encode())
		}
	}
	extensions := overlayContract.Extensions()
	if _, ok := extensions.Metadata["capabilities"]; !ok {
		t.Errorf("overlay dropped inherited capabilities: %#v", extensions.Metadata)
	}
}

// TestBuildServiceOverlayProfile_NameSurvivesRecreate guarantees an AIMService
// that's deleted and recreated with the same name (different UID) does not
// inherit the previous incarnation's overlay. Without service UID in the name
// hash, a stale overlay could shadow the fresh service's intended overrides.
func TestBuildServiceOverlayProfile_NameSurvivesRecreate(t *testing.T) {
	seed := sampleProfileSpec()
	overrides := &aimv1alpha1.AIMServiceProfileOverrides{AcceleratorModel: "MI325X"}

	build := func(uid string) string {
		service := &aimv1alpha1.AIMService{
			ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns", UID: types.UID(uid)},
			Spec: aimv1alpha1.AIMServiceSpec{
				Profile:          &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA},
				ProfileOverrides: overrides,
			},
		}
		obs := ServiceObservation{
			ServiceFetchResult:    ServiceFetchResult{service: service},
			resolvedProfileSpec:   seed,
			resolvedProfileStatus: &aimv1alpha2.AIMProfileStatus{},
			profileName:           testProfileA,
			profileScope:          aimv1alpha1.AIMResolutionScopeNamespace,
		}
		overlay, _, err := buildServiceOverlayProfile(service, obs)
		if err != nil {
			t.Fatalf("buildServiceOverlayProfile (uid=%s) error: %v", uid, err)
		}
		return overlay.Name
	}

	if a, b := build("uid-old"), build("uid-new"); a == b {
		t.Fatalf("overlay name must include service UID to survive delete-and-recreate, got %q == %q", a, b)
	}
}

// TestComposeState_NoOverridesDoesNotMaterialiseOverlay pins that the overlay
// path is taken only when overrides are real. A service that omits
// profileOverrides (or sets `{}`) must continue to point obs at the seed
// directly, preserving cross-service cache sharing in Shared mode.
func TestComposeState_NoOverridesDoesNotMaterialiseOverlay(t *testing.T) {
	seedProfile := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{Name: testProfileA, Namespace: "ns"},
		Spec:       aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: *sampleProfileSpec()},
	}
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns", UID: "u"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA},
		},
	}
	fetch := ServiceFetchResult{
		service: service,
		profile: controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Value: seedProfile},
	}
	obs := (&ProfileServiceReconciler{}).ComposeState(context.Background(), controllerutils.ReconcileContext[*aimv1alpha1.AIMService]{}, fetch)

	if obs.desiredOverlayProfile != nil {
		t.Fatalf("no overrides set: ComposeState must not materialise an overlay")
	}
	if obs.profileName != testProfileA {
		t.Fatalf("profileName must point at the seed when no overlay, got %q", obs.profileName)
	}
}

// TestComposeState_OverridesMaterialiseOverlay covers the normal overlay path:
// when a real override is set, ComposeState repoints profileName to the
// overlay so the cache, ConfigMap, and ISVC all consume the merged spec.
func TestComposeState_OverridesMaterialiseOverlay(t *testing.T) {
	seedProfile := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{Name: testProfileA, Namespace: "ns"},
		Spec:       aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: *sampleProfileSpec()},
	}
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns", UID: "service-uid"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA},
			ProfileOverrides: &aimv1alpha1.AIMServiceProfileOverrides{
				AcceleratorModel: "MI325X",
			},
		},
	}
	fetch := ServiceFetchResult{
		service: service,
		profile: controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Value: seedProfile},
	}
	obs := (&ProfileServiceReconciler{}).ComposeState(context.Background(), controllerutils.ReconcileContext[*aimv1alpha1.AIMService]{}, fetch)

	if obs.desiredOverlayProfile == nil {
		t.Fatalf("override set: ComposeState must materialise an overlay")
	}
	if obs.profileName == testProfileA {
		t.Fatalf("profileName must move from seed (%q) to the overlay name", testProfileA)
	}
	if obs.profileName != obs.desiredOverlayProfile.Name {
		t.Fatalf("profileName (%q) must equal overlay name (%q)", obs.profileName, obs.desiredOverlayProfile.Name)
	}
	if obs.resolvedProfileSpec == nil || obs.resolvedProfileSpec.AcceleratorModel != "MI325X" {
		t.Fatalf("resolvedProfileSpec must reflect override: %+v", obs.resolvedProfileSpec)
	}
}

func TestComposeState_WeightsReplacementAppliesRuntimeConfigImageMapping(t *testing.T) {
	seedSpec := sampleProfileSpec()
	seedSpec.Image = "ghcr.io/private-team/optimized:0.11"
	seedSpec.ModelSources = []aimv1alpha1.AIMModelSource{
		{ModelID: "base/model", SourceURI: "hf://base/model"},
	}
	seedProfile := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name: testProfileA, Namespace: "ns",
			Annotations: map[string]string{aimprofile.AnnotationBaseImage: "docker.io/amdenterpriseai/aim-base:0.11.2"},
		},
		Spec: aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: *seedSpec},
		Status: aimv1alpha2.AIMProfileStatus{
			// The producer has corrected lineage, but profile status has
			// not caught up. The overlay must use the new annotation now.
			Status:    "Ready",
			BaseImage: "docker.io/vllm/vllm-openai-rocm:v0.16.0",
		},
	}
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns", UID: "service-uid"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA},
			ProfileOverrides: &aimv1alpha1.AIMServiceProfileOverrides{
				ModelSources: []aimv1alpha1.AIMModelSource{
					{ModelID: "user/weights", SourceURI: "s3://bucket/weights"},
				},
			},
		},
	}
	fetch := ServiceFetchResult{
		service: service,
		profile: controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Value: seedProfile},
		mergedRuntimeConfig: controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]{
			Value: &aimv1alpha1.AIMRuntimeConfigCommon{
				ImageRegistry: &aimv1alpha1.AIMImageRegistryConfig{
					Mappings: []aimv1alpha1.AIMImageRegistryMapping{
						{
							From: "docker.io/amdenterpriseai",
							To:   "ghcr.io/private-team",
						},
					},
				},
			},
		},
	}

	obs := (&ProfileServiceReconciler{}).ComposeState(
		context.Background(),
		controllerutils.ReconcileContext[*aimv1alpha1.AIMService]{},
		fetch,
	)

	if obs.configErr != nil {
		t.Fatalf("ComposeState returned config error: %v", obs.configErr)
	}
	if obs.desiredOverlayProfile == nil {
		t.Fatal("weights replacement must materialise an overlay")
	}
	if got, want := obs.desiredOverlayProfile.Spec.Image, "ghcr.io/private-team/aim-base:0.11.2"; got != want {
		t.Fatalf("overlay image = %q, want mapped AIM_BASE_IMAGE_REF %q", got, want)
	}
	if got, want := obs.desiredOverlayProfile.Spec.BaseImageRef, "docker.io/amdenterpriseai/aim-base:0.11.2"; got != want {
		t.Fatalf("overlay baseImageRef = %q, want canonical producer reference %q", got, want)
	}
	if got, want := obs.desiredOverlayProfile.Annotations[aimprofile.AnnotationBaseImage], "docker.io/amdenterpriseai/aim-base:0.11.2"; got != want {
		t.Fatalf("overlay base-image annotation = %q, want canonical producer reference %q", got, want)
	}
}

func TestComposeState_HandAuthoredBaseImageRefSupportsWeightReplacement(t *testing.T) {
	seedSpec := sampleProfileSpec()
	seedSpec.Image = "ghcr.io/private-team/optimized:0.11"
	seedSpec.BaseImageRef = "ghcr.io/private-team/aim-base:0.11.2"
	seedSpec.ModelSources = []aimv1alpha1.AIMModelSource{
		{ModelID: "base/model", SourceURI: "hf://base/model"},
	}
	seedProfile := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{Name: testProfileA, Namespace: "ns"},
		Spec:       aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: *seedSpec},
	}
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns", UID: "service-uid"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA},
			ProfileOverrides: &aimv1alpha1.AIMServiceProfileOverrides{
				ModelSources: []aimv1alpha1.AIMModelSource{
					{ModelID: "user/weights", SourceURI: "s3://bucket/weights"},
				},
			},
		},
	}
	fetch := ServiceFetchResult{
		service: service,
		profile: controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Value: seedProfile},
	}

	obs := (&ProfileServiceReconciler{}).ComposeState(
		context.Background(),
		controllerutils.ReconcileContext[*aimv1alpha1.AIMService]{},
		fetch,
	)

	if obs.configErr != nil {
		t.Fatalf("ComposeState returned config error: %v", obs.configErr)
	}
	if obs.desiredOverlayProfile == nil {
		t.Fatal("hand-authored baseImageRef must allow the overlay to materialise")
	}
	if got, want := obs.desiredOverlayProfile.Spec.Image, seedSpec.BaseImageRef; got != want {
		t.Fatalf("overlay image = %q, want declared baseImageRef %q", got, want)
	}
	if got, want := obs.desiredOverlayProfile.Spec.BaseImageRef, seedSpec.BaseImageRef; got != want {
		t.Fatalf("overlay baseImageRef = %q, want %q", got, want)
	}
}

func TestComposeState_WeightsReplacementWithoutBaseImageFailsGracefully(t *testing.T) {
	seedSpec := sampleProfileSpec()
	seedSpec.ModelSources = []aimv1alpha1.AIMModelSource{
		{ModelID: "base/model", SourceURI: "hf://base/model"},
	}
	seedProfile := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{Name: testProfileA, Namespace: "ns"},
		Spec:       aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: *seedSpec},
		Status:     aimv1alpha2.AIMProfileStatus{Status: "Ready"},
	}
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns", UID: "service-uid"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA},
			ProfileOverrides: &aimv1alpha1.AIMServiceProfileOverrides{
				ModelSources: []aimv1alpha1.AIMModelSource{
					{ModelID: "user/weights", SourceURI: "s3://bucket/weights"},
				},
			},
		},
	}
	fetch := ServiceFetchResult{
		service: service,
		profile: controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Value: seedProfile},
	}

	reconciler := &ProfileServiceReconciler{}
	obs := reconciler.ComposeState(
		context.Background(),
		controllerutils.ReconcileContext[*aimv1alpha1.AIMService]{},
		fetch,
	)

	if obs.configErr == nil {
		t.Fatal("ComposeState config error = nil, want controlled missing-base-image error")
	}
	if !strings.Contains(obs.configErr.Error(), "has no AIM_BASE_IMAGE_REF") {
		t.Fatalf("ComposeState config error = %q, want missing AIM_BASE_IMAGE_REF", obs.configErr)
	}
	health := obs.getConfigHealth()
	if len(health.Errors) != 1 {
		t.Fatalf("config health errors = %v, want one actionable error", health.Errors)
	}
	categorized := controllerutils.CategorizeError(health.Errors[0])
	if categorized.Reason() != aimprofile.ReasonBaseImageUnresolved {
		t.Fatalf("config health reason = %q, want %q", categorized.Reason(), aimprofile.ReasonBaseImageUnresolved)
	}
	if categorized.Category() != controllerutils.ErrorCategoryMissingUpstreamDependency {
		t.Fatalf("config health category = %s, want MissingReference", categorized.Category())
	}
	if health.State != constants.AIMStatusDegraded {
		t.Fatalf("config health state = %q, want %q", health.State, constants.AIMStatusDegraded)
	}
	if obs.desiredOverlayProfile != nil {
		t.Fatal("missing AIM_BASE_IMAGE_REF must not materialise an overlay")
	}

	plan := reconciler.PlanResources(
		context.Background(),
		controllerutils.ReconcileContext[*aimv1alpha1.AIMService]{},
		obs,
	)
	if len(plan.GetToCreate()) != 0 ||
		len(plan.GetToApply()) != 0 ||
		len(plan.GetToApplyWithoutOwnerRef()) != 0 ||
		len(plan.GetToApplyWithForce()) != 0 ||
		len(plan.GetToDelete()) != 0 {
		t.Fatal("missing AIM_BASE_IMAGE_REF must skip all resource mutations")
	}
}

func TestComposeState_OverlayUsesFetchedStatusWhenPresent(t *testing.T) {
	seedProfile := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{Name: testProfileA, Namespace: "ns"},
		Spec:       aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: *sampleProfileSpec()},
		Status:     aimv1alpha2.AIMProfileStatus{Status: "Ready"},
	}
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns", UID: "service-uid"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA},
			ProfileOverrides: &aimv1alpha1.AIMServiceProfileOverrides{
				AcceleratorModel: "MI325X",
			},
		},
	}
	seedObs := ServiceObservation{
		ServiceFetchResult: ServiceFetchResult{
			service: service,
			profile: controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Value: seedProfile},
		},
		resolvedProfileSpec:   &seedProfile.Spec.AIMProfileSpecCommon,
		resolvedProfileStatus: &seedProfile.Status,
		profileName:           testProfileA,
		profileScope:          aimv1alpha1.AIMResolutionScopeNamespace,
	}
	overlay, _, err := buildServiceOverlayProfile(service, seedObs)
	if err != nil {
		t.Fatalf("buildServiceOverlayProfile returned error: %v", err)
	}
	overlay.UID = "overlay-uid"
	overlay.Status = aimv1alpha2.AIMProfileStatus{Status: "Ready"}

	fetch := ServiceFetchResult{
		service:        service,
		profile:        controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Value: seedProfile},
		overlayProfile: controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Value: overlay},
	}
	obs := (&ProfileServiceReconciler{}).ComposeState(context.Background(), controllerutils.ReconcileContext[*aimv1alpha1.AIMService]{}, fetch)

	if obs.profileName != overlay.Name {
		t.Fatalf("profileName = %q, want fetched overlay %q", obs.profileName, overlay.Name)
	}
	if obs.resolvedProfileStatus == nil || obs.resolvedProfileStatus.Status != "Ready" {
		t.Fatalf("resolvedProfileStatus = %#v, want fetched overlay status", obs.resolvedProfileStatus)
	}

	status := &aimv1alpha1.AIMServiceStatus{}
	(&ProfileServiceReconciler{}).DecorateStatus(status, nil, obs)
	if status.ResolvedProfile == nil || status.ResolvedProfile.UID != overlay.UID {
		t.Fatalf("ResolvedProfile = %#v, want overlay UID %q", status.ResolvedProfile, overlay.UID)
	}
}

func TestPlanResources_OverlayMustBeReadyBeforeCache(t *testing.T) {
	seedSpec := sampleProfileSpec()
	seedSpec.ModelSources = []aimv1alpha1.AIMModelSource{{ModelID: "base/model", SourceURI: "hf://base/model"}}
	seedProfile := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{Name: testProfileA, Namespace: "ns"},
		Spec:       aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: *seedSpec},
		Status: aimv1alpha2.AIMProfileStatus{
			Status:    "Ready",
			BaseImage: "docker.io/amdenterpriseai/aim-base:0.11",
		},
	}
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns", UID: "service-uid"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA},
			ProfileOverrides: &aimv1alpha1.AIMServiceProfileOverrides{
				ModelSources: []aimv1alpha1.AIMModelSource{{ModelID: "user/weights", SourceURI: "s3://bucket/weights"}},
			},
		},
	}
	fetch := ServiceFetchResult{
		service: service,
		profile: controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Value: seedProfile},
	}

	r := &ProfileServiceReconciler{}
	obs := r.ComposeState(context.Background(), controllerutils.ReconcileContext[*aimv1alpha1.AIMService]{}, fetch)
	plan := r.PlanResources(context.Background(), controllerutils.ReconcileContext[*aimv1alpha1.AIMService]{}, obs)

	var overlays, caches int
	for _, obj := range plan.GetToApply() {
		switch obj.(type) {
		case *aimv1alpha2.AIMProfile:
			overlays++
		case *aimv1alpha2.AIMProfileCache:
			caches++
		}
	}
	for _, obj := range plan.GetToApplyWithoutOwnerRef() {
		if _, ok := obj.(*aimv1alpha2.AIMProfileCache); ok {
			caches++
		}
	}
	if overlays != 1 {
		t.Fatalf("overlay applies = %d, want 1", overlays)
	}
	if caches != 0 {
		t.Fatalf("profile caches applies = %d, want 0 until overlay profile is Ready", caches)
	}
}

func TestPlanResources_OverlayReadyPlansCacheForOverlayName(t *testing.T) {
	seedSpec := sampleProfileSpec()
	seedSpec.ModelSources = []aimv1alpha1.AIMModelSource{{ModelID: "base/model", SourceURI: "hf://base/model"}}
	seedProfile := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{Name: testProfileA, Namespace: "ns"},
		Spec:       aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: *seedSpec},
		Status: aimv1alpha2.AIMProfileStatus{
			Status:    "Ready",
			BaseImage: "docker.io/amdenterpriseai/aim-base:0.11",
		},
	}
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns", UID: "service-uid"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA},
			ProfileOverrides: &aimv1alpha1.AIMServiceProfileOverrides{
				ModelSources: []aimv1alpha1.AIMModelSource{{ModelID: "user/weights", SourceURI: "s3://bucket/weights"}},
			},
		},
	}
	seedObs := ServiceObservation{
		ServiceFetchResult: ServiceFetchResult{
			service: service,
			profile: controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Value: seedProfile},
		},
		resolvedProfileSpec:   &seedProfile.Spec.AIMProfileSpecCommon,
		resolvedProfileStatus: &seedProfile.Status,
		profileName:           testProfileA,
		profileScope:          aimv1alpha1.AIMResolutionScopeNamespace,
	}
	overlay, _, err := buildServiceOverlayProfile(service, seedObs)
	if err != nil {
		t.Fatalf("buildServiceOverlayProfile returned error: %v", err)
	}
	overlay.Status = aimv1alpha2.AIMProfileStatus{Status: "Ready"}

	fetch := ServiceFetchResult{
		service:        service,
		profile:        controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Value: seedProfile},
		overlayProfile: controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Value: overlay},
	}

	r := &ProfileServiceReconciler{}
	obs := r.ComposeState(context.Background(), controllerutils.ReconcileContext[*aimv1alpha1.AIMService]{}, fetch)
	plan := r.PlanResources(context.Background(), controllerutils.ReconcileContext[*aimv1alpha1.AIMService]{}, obs)

	var cache *aimv1alpha2.AIMProfileCache
	for _, obj := range plan.GetToApply() {
		if pc, ok := obj.(*aimv1alpha2.AIMProfileCache); ok {
			cache = pc
		}
	}
	for _, obj := range plan.GetToApplyWithoutOwnerRef() {
		if pc, ok := obj.(*aimv1alpha2.AIMProfileCache); ok {
			cache = pc
		}
	}
	if cache == nil {
		t.Fatal("expected profile cache to be planned once overlay profile is Ready")
	}
	if cache.Spec.ProfileName != overlay.Name {
		t.Fatalf("cache profileName = %q, want overlay %q", cache.Spec.ProfileName, overlay.Name)
	}
}

// TestPlanResources_OverlayQueuedForApply checks PlanResources emits the
// overlay AIMProfile when overrides are present. Without this, the cache
// reference to the overlay name would never resolve in subsequent reconciles.
func TestPlanResources_OverlayQueuedForApply(t *testing.T) {
	seedProfile := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{Name: testProfileA, Namespace: "ns"},
		Spec:       aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: *sampleProfileSpec()},
		Status:     aimv1alpha2.AIMProfileStatus{Status: "Ready"},
	}
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns", UID: "service-uid"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA},
			ProfileOverrides: &aimv1alpha1.AIMServiceProfileOverrides{
				ModelSources: []aimv1alpha1.AIMModelSource{
					{ModelID: "user/weights", SourceURI: "s3://b/w/"},
				},
			},
		},
	}
	fetch := ServiceFetchResult{
		service: service,
		profile: controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Value: seedProfile},
	}

	r := &ProfileServiceReconciler{}
	obs := r.ComposeState(context.Background(), controllerutils.ReconcileContext[*aimv1alpha1.AIMService]{}, fetch)
	plan := r.PlanResources(context.Background(), controllerutils.ReconcileContext[*aimv1alpha1.AIMService]{}, obs)

	overlayFound := false
	for _, obj := range plan.GetToApply() {
		p, ok := obj.(*aimv1alpha2.AIMProfile)
		if !ok {
			continue
		}
		if p.Name == obs.desiredOverlayProfile.Name {
			overlayFound = true
		}
	}
	if !overlayFound {
		t.Fatalf("overlay AIMProfile not present in plan.toApply (must be applied as an owner-ref'd resource)")
	}
}

// decodeJSONObject is a small test helper that materialises a JSON-typed CRD
// field back into a Go map for assertion. Local to the overlay tests so the
// rest of the package's helpers stay focused.
func decodeJSONObject(t *testing.T, value *apiextensionsv1.JSON) map[string]any {
	t.Helper()
	if value == nil || len(value.Raw) == 0 {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(value.Raw, &out); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	return out
}

func TestBuildServiceOverlayProfile_SucceedsAfterContractBackfill(t *testing.T) {
	seedSpec := sampleProfileSpec()
	seedProfile := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{Name: testProfileA, Namespace: "ns"},
		Spec:       aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: *seedSpec},
		Status:     aimv1alpha2.AIMProfileStatus{Origin: aimv1alpha1.ProfileOriginDerived},
	}
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns", UID: "uid"},
		Spec: aimv1alpha1.AIMServiceSpec{
			Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA},
			ProfileOverrides: &aimv1alpha1.AIMServiceProfileOverrides{
				AcceleratorModel: "MI325X",
			},
		},
	}
	obs := ServiceObservation{
		ServiceFetchResult: ServiceFetchResult{
			service: service,
			profile: controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Value: seedProfile},
		},
		resolvedProfileSpec: seedSpec,
		profileName:         testProfileA,
	}

	if _, _, err := buildServiceOverlayProfile(service, obs); err == nil {
		t.Fatal("expected a derived seed without a profile YAML contract to fail")
	}

	seedProfile.Annotations = profileyaml.Mark(seedProfile.Annotations, profileyaml.DefaultContract())
	overlay, _, err := buildServiceOverlayProfile(service, obs)
	if err != nil {
		t.Fatalf("buildServiceOverlayProfile after contract backfill: %v", err)
	}
	if overlay.Annotations[profileyaml.AnnotationContract] == "" {
		t.Fatal("overlay did not inherit the backfilled profile YAML contract")
	}
}

// TestBuildServiceOverlayProfile_StampsProvenanceLabels asserts the overlay
// AIMProfile is marked private: role=deployable + origin=Derived are stamped
// for downstream gating, but source-model labels are NEVER stamped (even when
// the seed carries them). This is what makes overlays invisible to
// selector queries and prevents selector-driven services from recursively
// resolving their own overlay as a seed.
func TestBuildServiceOverlayProfile_StampsProvenanceLabels(t *testing.T) {
	t.Run("seed carries source-model, overlay must not inherit it", func(t *testing.T) {
		seedSpec := sampleProfileSpec()
		seedProfile := &aimv1alpha2.AIMProfile{
			ObjectMeta: metav1.ObjectMeta{
				Name:      testProfileA,
				Namespace: "ns",
				Labels: map[string]string{
					constants.LabelKeySourceModel:      "qwen-model",
					constants.LabelKeySourceModelScope: constants.LabelValueSourceModelScopeNamespace,
				},
			},
			Spec: aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: *seedSpec},
		}

		service := &aimv1alpha1.AIMService{
			ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns", UID: "uid"},
			Spec: aimv1alpha1.AIMServiceSpec{
				Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA},
				ProfileOverrides: &aimv1alpha1.AIMServiceProfileOverrides{
					AcceleratorModel: "MI325X",
				},
			},
		}
		obs := ServiceObservation{
			ServiceFetchResult:  ServiceFetchResult{service: service, profile: controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Value: seedProfile}},
			resolvedProfileSpec: seedSpec,
			profileName:         testProfileA,
		}
		overlay, _, err := buildServiceOverlayProfile(service, obs)
		if err != nil {
			t.Fatalf("buildServiceOverlayProfile: %v", err)
		}

		labels := overlay.GetLabels()
		if labels[constants.LabelKeyProfileRole] != constants.LabelValueProfileRoleDeployable {
			t.Errorf("profile-role = %q, want deployable", labels[constants.LabelKeyProfileRole])
		}
		if labels[constants.LabelKeyProfileOrigin] != string(aimv1alpha1.ProfileOriginDerived) {
			t.Errorf("profile-origin = %q, want Derived", labels[constants.LabelKeyProfileOrigin])
		}
		if v, ok := labels[constants.LabelKeySourceModel]; ok {
			t.Errorf("source-model must NOT be stamped on overlays (recursive-selection guard); got %q", v)
		}
		if v, ok := labels[constants.LabelKeySourceModelScope]; ok {
			t.Errorf("source-model-scope must NOT be stamped on overlays; got %q", v)
		}
	})

	t.Run("user-authored seed leaves source-model unset", func(t *testing.T) {
		seedSpec := sampleProfileSpec()
		seedProfile := &aimv1alpha2.AIMProfile{
			ObjectMeta: metav1.ObjectMeta{Name: testProfileA, Namespace: "ns"},
			Spec:       aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: *seedSpec},
		}
		service := &aimv1alpha1.AIMService{
			ObjectMeta: metav1.ObjectMeta{Name: testServiceName, Namespace: "ns", UID: "uid"},
			Spec: aimv1alpha1.AIMServiceSpec{
				Profile: &aimv1alpha1.AIMServiceProfileConfig{Name: testProfileA},
				ProfileOverrides: &aimv1alpha1.AIMServiceProfileOverrides{
					AcceleratorModel: "MI325X",
				},
			},
		}
		obs := ServiceObservation{
			ServiceFetchResult:  ServiceFetchResult{service: service, profile: controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Value: seedProfile}},
			resolvedProfileSpec: seedSpec,
			profileName:         testProfileA,
		}
		overlay, _, err := buildServiceOverlayProfile(service, obs)
		if err != nil {
			t.Fatalf("buildServiceOverlayProfile: %v", err)
		}

		labels := overlay.GetLabels()
		if labels[constants.LabelKeyProfileRole] != constants.LabelValueProfileRoleDeployable {
			t.Errorf("profile-role = %q, want deployable", labels[constants.LabelKeyProfileRole])
		}
		if labels[constants.LabelKeyProfileOrigin] != string(aimv1alpha1.ProfileOriginDerived) {
			t.Errorf("profile-origin = %q, want Derived", labels[constants.LabelKeyProfileOrigin])
		}
		if _, ok := labels[constants.LabelKeySourceModel]; ok {
			t.Errorf("source-model should be unset for overlays, got %q", labels[constants.LabelKeySourceModel])
		}
	})
}

// TestFilterNamespaceProfilesBySpec_ExcludesOverlays guards against the
// recursive self-selection bug: a selector-driven AIMService with
// profileOverrides was previously able to resolve its own overlay as a
// seed in the next reconcile, building an overlay-of-overlay chain.
func TestFilterNamespaceProfilesBySpec_ExcludesOverlays(t *testing.T) {
	plain := aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "seed", Namespace: "ns"},
		Spec:       aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: *sampleProfileSpec()},
	}
	ownOverlay := aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "my-overlay",
			Namespace:   "ns",
			Annotations: map[string]string{AnnotationOverlayService: testServiceName},
		},
		Spec: aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: *sampleProfileSpec()},
	}
	otherOverlay := aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "other-overlay",
			Namespace:   "ns",
			Annotations: map[string]string{AnnotationOverlayService: "other-svc"},
		},
		Spec: aimv1alpha2.AIMProfileSpec{AIMProfileSpecCommon: *sampleProfileSpec()},
	}

	out := filterNamespaceProfilesBySpec(
		[]aimv1alpha2.AIMProfile{plain, ownOverlay, otherOverlay},
		aimv1alpha1.ProfileSelector{Role: aimv1alpha1.ProfileSelectorRoleDeployable},
	)
	if len(out) != 1 {
		t.Fatalf("filter returned %d candidates, want 1", len(out))
	}
	if out[0].Name != "seed" {
		t.Fatalf("filter returned overlay %q; expected only the non-overlay seed", out[0].Name)
	}
}

// Adapter capability is an explicit assertion about the selected image, not
// something inferred from service intent. A feature override creates a private
// overlay carrying that assertion.
func TestBuildServiceOverlayProfile_AppliesExplicitAdapterFeatureOverride(t *testing.T) {
	t.Parallel()

	seed := sampleProfileSpec()
	seed.Features = nil

	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "ns", UID: types.UID("svc-uid")},
		Spec: aimv1alpha1.AIMServiceSpec{
			ProfileOverrides: &aimv1alpha1.AIMServiceProfileOverrides{
				Features: []string{aimv1alpha2.ProfileFeatureAdapters},
			},
			Adapters: []aimv1alpha1.AIMServiceAdapterReference{
				{Name: "lora-a", Kind: aimv1alpha1.AdapterKindAIMArtifact},
			},
		},
	}
	obs := ServiceObservation{resolvedProfileSpec: seed, profileName: "seed-profile"}

	_, overlaySpec, err := buildServiceOverlayProfile(service, obs)
	if err != nil {
		t.Fatalf("buildServiceOverlayProfile() error = %v", err)
	}
	if !overlaySpec.SupportsAdapters() {
		t.Errorf("overlay must advertise adapters, got features %v", overlaySpec.Features)
	}
}

// Declaring adapters alone must not manufacture capability metadata or fork an
// overlay. The resolved profile must advertise adapters, either at the source
// or through an explicit profileOverrides.features assertion.
func TestNeedsServiceOverlay_DoesNotInferAdapterFeatureFromServiceIntent(t *testing.T) {
	t.Parallel()

	service := &aimv1alpha1.AIMService{
		Spec: aimv1alpha1.AIMServiceSpec{
			Adapters: []aimv1alpha1.AIMServiceAdapterReference{
				{Name: "lora-a", Kind: aimv1alpha1.AdapterKindAIMArtifact},
			},
		},
	}

	if needsServiceOverlay(service) {
		t.Fatal("spec.adapters alone must not create a service-owned overlay")
	}
}

func TestNeedsServiceOverlay_IncludesExplicitAdapterFeatureOverride(t *testing.T) {
	t.Parallel()

	service := &aimv1alpha1.AIMService{
		Spec: aimv1alpha1.AIMServiceSpec{
			ProfileOverrides: &aimv1alpha1.AIMServiceProfileOverrides{
				Features: []string{aimv1alpha2.ProfileFeatureAdapters},
			},
			Adapters: []aimv1alpha1.AIMServiceAdapterReference{
				{Name: "medical-lora", Kind: aimv1alpha1.AdapterKindAIMArtifact},
			},
		},
	}

	if !needsServiceOverlay(service) {
		t.Fatal("explicit features override must create a service-owned overlay")
	}
}

func TestAddRuntimeTrustOverlayProjectsCombinedBundle(t *testing.T) {
	isvc := &servingv1beta1.InferenceService{}
	isvc.Spec.Predictor.Model = &servingv1beta1.ModelSpec{}
	config := &aimv1alpha1.AIMRuntimeConfigCommon{
		AdditionalCABundleRef: &aimv1alpha1.AdditionalCABundleReference{
			Name: "company-ca",
			Key:  "ca.crt",
		},
		Artifact: &aimv1alpha1.AIMArtifactConfig{
			ModelDownloadImage: "registry.example.test/downloader:v1",
		},
	}
	configMap := &corev1.ConfigMap{Data: map[string]string{"ca.crt": "rotation-one"}}

	addRuntimeTrustOverlay(isvc, config, configMap)

	if len(isvc.Spec.Predictor.InitContainers) != 1 {
		t.Fatalf("init containers = %d, want 1", len(isvc.Spec.Predictor.InitContainers))
	}
	if got := isvc.Spec.Predictor.InitContainers[0].Image; got != "registry.example.test/downloader:v1" {
		t.Errorf("init image = %q", got)
	}
	if got := isvc.Spec.Predictor.InitContainers[0].Env; len(got) != 0 {
		t.Errorf("trust init env = %#v, want no rollout metadata", got)
	}
	if got := overlayEnvValue(isvc.Spec.Predictor.Model.Env, "REQUESTS_CA_BUNDLE"); got != trust.OutputFilePath {
		t.Errorf("REQUESTS_CA_BUNDLE = %q", got)
	}
	if isvc.Spec.Predictor.SecurityContext != nil {
		t.Fatalf("trust projection changed predictor security context: %#v", isvc.Spec.Predictor.SecurityContext)
	}
}

func TestAddRuntimeTrustOverlaySkipsUnavailableBundle(t *testing.T) {
	isvc := &servingv1beta1.InferenceService{}
	isvc.Spec.Predictor.Model = &servingv1beta1.ModelSpec{}
	isvc.Spec.Predictor.Model.Env = []corev1.EnvVar{{
		Name: "REQUESTS_CA_BUNDLE", Value: "/vendor/roots.pem",
	}}
	config := &aimv1alpha1.AIMRuntimeConfigCommon{
		AdditionalCABundleRef: &aimv1alpha1.AdditionalCABundleReference{
			Name: "missing-ca",
			Key:  "ca.crt",
		},
	}

	addRuntimeTrustOverlay(isvc, config, nil)

	if len(isvc.Spec.Predictor.InitContainers) != 0 || len(isvc.Spec.Predictor.Volumes) != 0 {
		t.Fatalf("unavailable trust bundle changed pod spec: %#v", isvc.Spec.Predictor)
	}
	if got := overlayEnvValue(isvc.Spec.Predictor.Model.Env, "REQUESTS_CA_BUNDLE"); got != "/vendor/roots.pem" {
		t.Errorf("REQUESTS_CA_BUNDLE = %q, want existing public roots unchanged", got)
	}
}

func overlayEnvValue(env []corev1.EnvVar, name string) string {
	for _, item := range env {
		if item.Name == name {
			return item.Value
		}
	}
	return ""
}
